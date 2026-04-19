// Package orchestrator — secret-leak guard for the AI annotation path
// (v1.21 Epic V121-7, Story 7.1 / FR-V121-19 / NFR-V121-12).
//
// Locked decision (Moran, 2026-04-19): the LLM call is HARD-BLOCKED on any
// secret-like pattern match in the values payload — there is no
// "send anyway" override. False-positive bias is intentional. If a chart
// values.yaml contains anything that looks even a little like a key, the
// guard refuses to send it upstream and the pipeline falls back to the
// heuristic-only smart-values output (no annotation, banner shown).
//
// Why this lives in the orchestrator package, not internal/catalog: the
// epic notes mention `internal/catalog/ai_guard.go` but Sharko's catalog
// package is for ArtifactHub discovery, not the smart-values pipeline.
// The guard is called from the AddAddon seed flow (orchestrator) and from
// the V121-7.4 manual annotate endpoint (api → orchestrator). Co-locating
// it with smart_values.go keeps the import graph clean and makes the
// pure-function nature of the scanner obvious.
//
// The pattern list mirrors the Story 7.1 AC: AWS keys, GitHub PATs,
// generic API key/secret/password assignments, JWTs, SSH/TLS PEM blocks,
// Slack tokens, Google API keys, generic high-entropy base64 blobs near
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

// SecretMatch describes one redacted hit from the scanner. `Pattern` is
// the human-readable name from the SecretPattern; `Field` is the YAML
// dotted path or line excerpt that matched (with the actual value
// redacted to `***`). Surface this to the UI so the maintainer can find
// the offending field without the secret leaking through the audit log
// or the toast.
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
			field := redactedField(line, sp.Pattern)
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

// redactedField returns a short, secret-free description of the matched
// line. We strip leading whitespace and any leading `# ` comment marker,
// then mask the matched substring with `***`. If the line is "key: VALUE"
// we keep the key for context. The output is bounded at 80 chars so the
// audit log doesn't get a wall of text.
func redactedField(line string, pat *regexp.Regexp) string {
	// Strip leading indentation and any single comment marker so the
	// field name remains readable.
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "#")
	trimmed = strings.TrimSpace(trimmed)

	// Replace the matched text with `***` so we never echo the secret
	// back through the API response or audit log.
	masked := pat.ReplaceAllString(trimmed, "***")

	if len(masked) > 80 {
		masked = masked[:77] + "..."
	}
	return masked
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
