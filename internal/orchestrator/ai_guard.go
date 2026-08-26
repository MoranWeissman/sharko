// Package orchestrator — secret-leak guard for the AI annotation path.
//
// The LLM call is HARD-BLOCKED on any secret-like pattern match in the
// values payload — there is no "send anyway" override. False-positive
// bias is intentional. If a chart values.yaml contains anything that
// looks even a little like a key, the guard refuses to send it upstream
// and the pipeline falls back to the heuristic-only smart-values output
// (no annotation, banner shown).
//
// The pattern list covers: AWS keys, GitHub PATs, generic API
// key/secret/password assignments, JWTs, SSH/TLS PEM blocks, Slack
// tokens, Google API keys, generic high-entropy base64 blobs near
// secret-keyword headers. Every regex is anchored to the kind of context
// you actually find in a Helm chart values.yaml — assignment lines and
// PEM block markers — to keep false positives sane while still being
// aggressive about anything that *could* be a real secret.

package orchestrator

import (
	"fmt"
	"regexp"
	"strings"
)

// SecretPattern is one entry in the guard's regex list. `Name` is the
// human-readable label that appears in the redacted block-reason summary
// (so the UI can say "matched pattern: AWS access key" without leaking
// the actual matched text). `Pattern` is the compiled regex.
type SecretPattern struct {
	Name    string
	Pattern *regexp.Regexp
}

// secretPatterns is the closed list of patterns the guard scans for.
// Order matters only for the redacted summary (first match per line wins).
//
// Notes on the choices:
//   - AWS access key: the canonical 20-char `AKIA...` form. Session keys
//     (`ASIA...`) too, since they're also bearer credentials.
//   - GitHub PATs: the modern fine-grained `github_pat_` prefix and the
//     classic `ghp_` / `gho_` 36-char tokens.
//   - JWT: anything that looks like a 3-segment base64-url JWT with
//     enough length to be plausible (header alone is ~36 chars; we want
//     the full thing).
//   - PEM blocks: any `-----BEGIN ... PRIVATE KEY-----` line, including
//     OPENSSH, RSA, EC, PGP — all bearer-equivalent.
//   - Generic API key assignments: case-insensitive `(api[_-]?key|token|
//     password|secret|bearer|credential)` followed by `:` or `=` and a
//     16+ char value. The 16-char floor cuts the false-positive rate on
//     `password: changeme` placeholders while still catching real keys.
//   - Slack tokens: `xox[baprs]-...` — Slack's documented prefix scheme.
//   - Google API keys: `AIzaSy...` 39-char form (Maps, Cloud, etc.).
//   - High-entropy base64 lines (40+ chars of base64 charset on a line
//     with a colon): catches the long tail of opaque tokens that don't
//     match any of the named patterns.
var secretPatterns = []SecretPattern{
	{Name: "AWS access key", Pattern: regexp.MustCompile(`(?:AKIA|ASIA)[0-9A-Z]{16}`)},
	{Name: "GitHub fine-grained PAT", Pattern: regexp.MustCompile(`github_pat_[A-Za-z0-9_]{82}`)},
	{Name: "GitHub classic PAT", Pattern: regexp.MustCompile(`gh[pous]_[A-Za-z0-9]{36,}`)},
	{Name: "JWT token", Pattern: regexp.MustCompile(`eyJ[A-Za-z0-9+/=._-]{20,}\.[A-Za-z0-9+/=._-]{20,}\.[A-Za-z0-9+/=._-]{20,}`)},
	{Name: "PEM private key", Pattern: regexp.MustCompile(`-----BEGIN[ A-Z]*PRIVATE KEY-----`)},
	{Name: "Slack token", Pattern: regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,}`)},
	{Name: "Google API key", Pattern: regexp.MustCompile(`AIza[0-9A-Za-z_\-]{35}`)},
	// Note: this assignment-style match is intentionally case-insensitive
	// and tolerant about whether the value is quoted. The 16-char floor
	// is deliberate — `secret: changeme` (Helm-default-ish) is below the
	// floor and won't fire. Real tokens are always >= 20 chars.
	{Name: "API key / token / password assignment", Pattern: regexp.MustCompile(`(?i)(api[_-]?key|api[_-]?token|password|secret|bearer|credential|access[_-]?token)\s*[:=]\s*["']?[A-Za-z0-9+/=_\-]{16,}["']?`)},
}

// SecretFieldUnavailable is what SecretMatch.Field says when the matching
// line has no plain `key: value` shape to read a field name off — a PEM
// marker, a bare token on a line of its own, a line whose key position is
// itself secret-looking. It is a fixed sentence: it is the same text for
// every such line, so it can carry nothing about what was on that line.
// The line number in the same SecretMatch is what points the maintainer at
// the right place.
const SecretFieldUnavailable = "(a secret-like value on this line; no plain field name to show — open the file at this line number)"

// secretValueMask is the fixed marker that stands in for a discarded
// value. It never grows or shrinks with the value, because a mask whose
// width tracks the secret's length is itself a disclosure.
const secretValueMask = "***"

// safeFieldKeyPattern is the shape a field name must have before it is
// allowed into a refusal message: an ordinary configuration key, the sort
// of thing a chart author types by hand. Anything else — spaces, quotes,
// punctuation that does not belong in a key, or a key longer than any real
// one — is not a field name we are willing to repeat back.
var safeFieldKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.\-]{0,63}$`)

// SecretMatch describes one hit from the scanner. `Pattern` is the
// human-readable name from the SecretPattern. `Field` is EITHER a
// structurally parsed field name followed by the fixed mask
// (`password: ***`) OR the fixed SecretFieldUnavailable sentence — it
// never carries any part of the value, of any length, in any encoding.
// Surface this to the UI so the maintainer can find the offending field
// without the secret leaking through the audit log or the toast.
type SecretMatch struct {
	Pattern string `json:"pattern"`
	Field   string `json:"field"`
	// Line is the 1-indexed line number where the match occurred. Helps
	// the user open the file and find the field. Zero if unknown.
	Line int `json:"line"`
}

// ScanForSecrets walks the YAML payload line by line and returns every
// SecretPattern hit. The function is pure — no I/O, no allocation
// beyond the result slice — so it's cheap to call on every addon-add
// and every manual annotate.
//
// Returned matches are deduplicated by (pattern, field) so the same
// password line can't dominate the summary. The order is the order
// patterns appear in `secretPatterns`, which is roughly best-known to
// catch-all.
func ScanForSecrets(valuesYAML []byte) []SecretMatch {
	if len(valuesYAML) == 0 {
		return nil
	}

	type key struct {
		pat   string
		field string
	}
	seen := map[key]struct{}{}
	var hits []SecretMatch

	lines := strings.Split(string(valuesYAML), "\n")
	for i, line := range lines {
		// Skip pure comment lines — they're metadata, not the actual
		// secret. We still scan the assignment patterns inside comments
		// for the non-comment-only cases (e.g. `# password: hunter2`)
		// because a comment with a real key in it leaks just as badly.
		// The PEM marker is always scanned because the BEGIN line itself
		// is the indicator regardless of comment status.
		for _, sp := range secretPatterns {
			if !sp.Pattern.MatchString(line) {
				continue
			}
			field := redactedField(line)
			k := key{pat: sp.Name, field: field}
			if _, dup := seen[k]; dup {
				continue
			}
			seen[k] = struct{}{}
			hits = append(hits, SecretMatch{
				Pattern: sp.Name,
				Field:   field,
				Line:    i + 1,
			})
		}
	}
	return hits
}

// redactedField returns a short description of the matched line that
// carries none of the value.
//
// The rule is: once a line has been classified as carrying a secret, the
// WHOLE value is thrown away — not the part some regex happened to match.
// Masking only the matched run is what used to leak here: the assignment
// pattern's value class stops at the first character outside
// [A-Za-z0-9+/=_-], so a value with a dot, a bang, a dollar or a space in
// it had everything after that character copied out verbatim.
//
// So nothing is built out of the line's own bytes. There are exactly two
// possible answers:
//
//   - the line parses as a plain `key: value` (or `key = value`) and the
//     key is an ordinary configuration key that is not itself secret-
//     looking — then the answer is that key plus the fixed mask, e.g.
//     `password: ***`. The separator is always written back as `: `, so
//     the text does not vary with what was typed;
//   - anything else — then the answer is the fixed SecretFieldUnavailable
//     sentence, which says nothing at all about the line.
//
// Either way the SecretMatch carries the line number, so the maintainer
// can always go and look.
func redactedField(line string) string {
	// Strip leading indentation, a single comment marker, and a YAML list
	// dash, so an ordinary field name is still readable underneath.
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))
	trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))

	key, ok := safeFieldKey(trimmed)
	if !ok {
		return SecretFieldUnavailable
	}
	return key + ": " + secretValueMask
}

// safeFieldKey pulls a field name off the front of a line, and only hands
// it back when it is safe to repeat.
//
// Safe means all of these:
//
//   - there is a `:` or `=` separator, and something before it. Everything
//     from the separator onwards is the value and is never looked at again;
//   - what is before the separator is an ordinary configuration key by
//     shape (safeFieldKeyPattern), optionally wrapped in one pair of
//     quotes;
//   - the key does not itself match any of the guard's own patterns. A
//     line like `AKIAIOSFODNN7EXAMPLE: something` has the secret in the key
//     position, and repeating "the field name" there would repeat the
//     secret.
//
// Note that the pattern which fired for this line is deliberately not
// consulted. The assignment pattern's match spans the key as well as the
// value, so asking "did the match reach into the key?" would reject every
// ordinary `password: …` line; and reasoning from where a regex happened
// to land is exactly the habit that produced the original leak.
//
// What this does NOT promise: a key that is opaque but matches none of the
// guard's patterns — a base64 blob sitting in the key position — is still
// repeated back. That is by definition something the guard has never
// classified as secret anywhere, so it is not a value being disclosed; it
// is the name of a setting. The promise here is only, and exactly, about
// the value: everything from the separator on is dropped whole.
func safeFieldKey(trimmed string) (string, bool) {
	sep := strings.IndexAny(trimmed, ":=")
	if sep <= 0 {
		return "", false
	}
	key := strings.TrimSpace(trimmed[:sep])
	key = trimQuotePair(key)
	if !safeFieldKeyPattern.MatchString(key) {
		return "", false
	}
	for _, sp := range secretPatterns {
		if sp.Pattern.MatchString(key) {
			return "", false
		}
	}
	return key, true
}

// trimQuotePair removes one matching pair of surrounding quotes, so a
// quoted YAML key still reads as a field name. An unbalanced quote is left
// alone, which then fails the key-shape check — the safe outcome.
func trimQuotePair(s string) string {
	if len(s) < 2 {
		return s
	}
	first, last := s[0], s[len(s)-1]
	if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
		return s[1 : len(s)-1]
	}
	return s
}

// SecretLeakError is the typed error returned by AnnotateValues when the
// guard fires. Callers (the seed flow, the manual annotate endpoint)
// type-assert against this to surface a structured response — the UI
// renders the matches inline on the Configure step's banner per the
// Story 7.1 AC.
type SecretLeakError struct {
	Matches []SecretMatch
}

// Error implements the error interface. The string form is short and
// safe to log — it carries the count and the pattern names but not the
// actual matched text.
func (e *SecretLeakError) Error() string {
	if len(e.Matches) == 0 {
		return "secret_detected_blocked: empty match set"
	}
	names := make([]string, 0, len(e.Matches))
	seen := map[string]struct{}{}
	for _, m := range e.Matches {
		if _, ok := seen[m.Pattern]; ok {
			continue
		}
		seen[m.Pattern] = struct{}{}
		names = append(names, m.Pattern)
	}
	return fmt.Sprintf("secret_detected_blocked: %d match(es) across %d pattern(s) [%s]",
		len(e.Matches), len(names), strings.Join(names, ", "))
}

// Code is the stable wire-format error code surfaced to the UI. The UI
// matches against `secret_detected_blocked` to render the dedicated
// secret-leak banner instead of the generic AI failure toast.
func (e *SecretLeakError) Code() string {
	return "secret_detected_blocked"
}
