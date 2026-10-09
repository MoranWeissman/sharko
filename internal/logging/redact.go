// Sensitive-field redaction for slog.
//
// This file adds RedactHandler, a slog.Handler wrapper that walks every
// attribute on every emitted log record and replaces credential-shaped
// VALUES with the literal string "[REDACTED]" before passing the record on
// to the next handler in the chain.
//
// Three independent detectors fire in order:
//
//  1. Sensitive-key heuristic — the attribute KEY matches a known-credential
//     name (exact, case-insensitive) or has a credential-shaped suffix
//     (_token, _password, _secret, _key). Catches `slog.String("token", ...)`,
//     `slog.String("db_password", ...)`, `slog.String("API_KEY", ...)`, etc.
//
//  2. JWT-shape detector — the attribute VALUE matches the canonical
//     `eyJ<header>.<payload>.<signature>` JWT regex. Catches a leaked JWT
//     even when the key name is innocuous (e.g. `slog.String("body", jwt)`).
//
//  3. Error-value classifier (B9) — the attribute VALUE *is* a Go error. Its
//     words are replaced with credsafe.LogClass(err): a description built only
//     from sentinel matches, stdlib interface probes and Go TYPE NAMES, never
//     from err.Error(). This is the detector that closes the log leak, and it
//     is the only one that fires on TYPE rather than on shape.
//
//  4. Base64-blob detector — the attribute VALUE is >100 chars and consists
//     entirely of the base64 alphabet `[A-Za-z0-9+/=]`. Catches kubeconfig
//     fragments, PEM-encoded certificates pasted into log lines, and other
//     large opaque secret-shaped payloads. The 100-char threshold avoids
//     false-positives on short tokens (which are caught by the key-name
//     heuristic) and on short alphanumeric IDs.
//
// The three shape detectors collapse to the same replacement string
// "[REDACTED]" — deliberately type-blind, so a downstream reader of the logs
// cannot tell whether the redacted field was a JWT, a kubeconfig, or a
// password. This prevents partial-information leaks ("ah, it was a JWT — so
// the key was `auth_token`").
//
// The error classifier is the exception, and on purpose. A flat "[REDACTED]"
// where the error used to be would close the leak and take every log line's
// usefulness with it, and a fix that makes debugging impossible gets reverted
// by the first operator who has an outage. So an error is replaced by
// something an operator can still work from — what kind of failure it was and
// the Go types it passed through — and none of that comes from the error's
// text. See internal/credsafe/errorclass.go.
//
// # Why the fix is here and not at the call sites
//
// Eighty-eight lines in this tree hand an error to slog. Fixing eighty-eight
// call sites protects the eighty-eight that exist and nothing written next
// year, and "remember to sanitise before logging" is exactly the rule that has
// been forgotten in every one of these stories. One wrapper on the sink every
// record already passes through is one decision instead of eighty-eight, and
// it is fail-safe by default rather than by discipline.
//
// What the sink CANNOT see is a raw payload that arrives already flattened to
// a string — `slog.Error(..., "body", string(respBody))`. A string is a
// string; no type survives for the handler to classify. Those are removed at
// the call site, and log_error_guard_test.go holds the line by naming every
// slog call in the tree that touches an error or a provider-sourced value.
//
// There is no opt-out. There used to be one: an attribute key beginning
// `_unsafe_` skipped every detector, for dev-debug instrumentation where the
// author wanted the raw value. Nothing in the tree ever used it outside its
// own tests, and it was a switch that turned the protection off from the call
// site — which is the one thing the sink exists to make impossible. A caller
// deciding a value is safe is exactly the judgement that has been wrong in
// every story in this class. It is gone, and the sink now has no branch that
// can be talked out of running.
//
// Group traversal: slog supports nested attribute groups (`slog.Group("creds",
// slog.String("token", ...))`). The handler recursively walks every group
// so a sensitive field nested inside a group is still redacted.
//
// Performance: redaction runs in the handler chain on every log record, so
// the hot path matters. The regexes are compiled at package init (one-shot
// cost). String detection uses simple `strings.HasSuffix` / `strings.EqualFold`
// checks against a fixed-size set, NOT a map lookup per call (the set is
// small enough that linear scan is faster than map overhead).
//
// The wrapper installs FIRST in the handler chain at slog init, so every
// downstream handler (JSON, text, file, network sink) sees only redacted
// values. Adding the wrapper later in the chain would let an upstream
// handler serialize the raw value before redaction — defeating the point.

package logging

import (
	"context"
	"io"
	"log/slog"
	"regexp"
	"strings"

	"github.com/MoranWeissman/sharko/internal/credsafe"
)

// redactedPlaceholder is the uniform replacement string. Deliberately
// type-blind: a reader of the logs cannot tell whether the original value
// was a JWT, a kubeconfig, or a password.
const redactedPlaceholder = "[REDACTED]"

// sensitiveKeysExact is the case-insensitive set of attribute key names
// whose VALUES are always redacted. Add only canonical secret-bearing
// names here — the suffix list below catches dynamic / domain-specific
// variants (`db_password`, `argocd_token`).
var sensitiveKeysExact = []string{
	"token",
	"password",
	"kubeconfig",
	"secret",
	"pat",
	"bearer_token",
	"authorization",
	"api_key",
	"apikey",
	"auth_token",
	"access_token",
	"refresh_token",
	"private_key",
	"cert_data",
}

// sensitiveKeySuffixes catches dynamic-but-credential-shaped key names:
// `db_password`, `argocd_token`, `webhook_secret`, `signing_key`. The
// suffix match is case-insensitive (see isSensitiveKey).
var sensitiveKeySuffixes = []string{
	"_token",
	"_password",
	"_secret",
	"_key",
}

// jwtRegex matches a canonical three-segment JWT: base64url header,
// base64url payload, base64url signature. The `eyJ` prefix is the base64
// encoding of `{"` — every JWT header starts with `{"alg":...`, so every
// JWT base64-encodes to `eyJ...`. Anchored to avoid substring matches.
var jwtRegex = regexp.MustCompile(`^eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$`)

// base64BlobRegex matches a value consisting entirely of the standard
// base64 alphabet (with padding) — a strong signal of opaque binary
// payload encoded for transport (kubeconfig, certificate, key material).
// The 100-char minimum is applied separately so a short alphanumeric
// identifier is not over-redacted.
var base64BlobRegex = regexp.MustCompile(`^[A-Za-z0-9+/=]+$`)

const base64BlobMinLen = 100

// RedactHandler wraps another slog.Handler and redacts sensitive values
// before passing records through.
//
// Construct via NewRedactHandler; the zero value is not usable.
type RedactHandler struct {
	inner slog.Handler
}

// NewRedactHandler wraps inner with credential-shape redaction. Returns
// inner unwrapped if inner is nil (defensive — there is nothing useful
// to redact records for if no downstream handler exists).
func NewRedactHandler(inner slog.Handler) slog.Handler {
	if inner == nil {
		return nil
	}
	return &RedactHandler{inner: inner}
}

// NewHandler builds the log handler chain Sharko actually runs: a JSON
// handler at the given level, wrapped in redaction.
//
// It exists so there is exactly ONE definition of that chain. `sharko serve`
// calls it, and so does the test harness that captures log output to prove
// nothing leaks. Before B9 the two were written out separately, and the test
// harness's copy had no redaction in it at all — so every "the log must not
// carry this" assertion in the tree was being made against a pipeline that was
// not the one shipping. Two spellings of the same chain is how a proof ends up
// proving something about a pipeline nobody runs.
func NewHandler(w io.Writer, level slog.Level) slog.Handler {
	return NewRedactHandler(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}))
}

// Enabled delegates to the wrapped handler — the redaction wrapper never
// suppresses records, only their values.
func (h *RedactHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle redacts every attribute on the record and forwards it to the
// wrapped handler. Uses Record.Clone() to avoid mutating a Record that
// upstream code may have a reference to.
func (h *RedactHandler) Handle(ctx context.Context, r slog.Record) error {
	clone := r.Clone()
	// We can't replace attrs in-place on a Record, so collect the
	// redacted ones and rebuild via a fresh Record.
	redacted := make([]slog.Attr, 0, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		redacted = append(redacted, redactAttr(a))
		return true
	})

	// Build a new record with the same metadata and the redacted attrs.
	out := slog.NewRecord(clone.Time, clone.Level, clone.Message, clone.PC)
	out.AddAttrs(redacted...)
	return h.inner.Handle(ctx, out)
}

// WithAttrs returns a new RedactHandler whose wrapped handler has the
// pre-redacted attrs applied. Attrs set via .With() are redacted ONCE
// at attachment time so every record using the With-derived logger
// sees the same redacted view without re-running the regex per call.
func (h *RedactHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	redacted := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		redacted[i] = redactAttr(a)
	}
	return &RedactHandler{inner: h.inner.WithAttrs(redacted)}
}

// WithGroup delegates to the wrapped handler. Group nesting is handled
// at record-traversal time (see redactAttr — group values recurse).
func (h *RedactHandler) WithGroup(name string) slog.Handler {
	return &RedactHandler{inner: h.inner.WithGroup(name)}
}

// redactAttr returns a possibly-redacted copy of attr.
//
//   - Group-valued attrs recurse so nested sensitive attrs are caught.
//   - Otherwise: redact if the KEY is sensitive (heuristic) OR the
//     VALUE matches the JWT regex OR the VALUE is a base64 blob >100 chars.
func redactAttr(attr slog.Attr) slog.Attr {
	// Recurse into groups so `slog.Group("creds", slog.String("token", ...))`
	// is traversed and the inner "token" attr is redacted.
	if attr.Value.Kind() == slog.KindGroup {
		inner := attr.Value.Group()
		redacted := make([]slog.Attr, len(inner))
		for i, sub := range inner {
			redacted[i] = redactAttr(sub)
		}
		return slog.Attr{Key: attr.Key, Value: slog.GroupValue(redacted...)}
	}

	if isSensitiveKey(attr.Key) {
		return slog.String(attr.Key, redactedPlaceholder)
	}

	// Error-value classification (B9). An error's words are whatever a
	// provider, a Git library, the Kubernetes API server or net/url decided to
	// put in them, and on the Git side that routinely includes a repository
	// address with the access token inside it. The words never reach the
	// downstream handler; a type-derived classification does.
	//
	// Kind() is checked before Resolve() so an error that also implements
	// slog.LogValuer cannot resolve itself into a plain string and slip past
	// as free text.
	if attr.Value.Kind() == slog.KindAny {
		if err, isErr := attr.Value.Any().(error); isErr {
			return slog.String(attr.Key, credsafe.LogClass(err))
		}
	}

	// Value-shape detection — only for string-valued attrs. Resolve the
	// value first so LogValuer-wrapped values (lazy strings) are seen.
	resolved := attr.Value.Resolve()
	if resolved.Kind() == slog.KindString {
		s := resolved.String()
		if shouldRedactValue(s) {
			return slog.String(attr.Key, redactedPlaceholder)
		}
		// Credential-carrying URL (B14). The three detectors above cannot
		// see a plain STRING value holding a credential under a key nobody
		// thought to call sensitive — and "repo" is exactly such a key. A
		// chart repository address is routinely written
		// https://<token>@host/org/repo, so `slog.String("repo", repoURL)`
		// is the token in the log, written by somebody who thought they
		// were being helpful about which repo failed.
		//
		// Patching that one call site would have left the SHAPE open for
		// the next person, and there was already a second one
		// (internal/advisories logs "url", repoURL). So the fix is here,
		// at the sink, the same way the error classifier is.
		//
		// This is NOT a scan for text that looks like a secret. It fires
		// on one STRUCTURAL fact, and credsafe owns which fact that is:
		// credsafe read the value and found a place a credential can
		// live — a userinfo section, a query or a fragment — or could not
		// read it at all. A value credsafe read all the way through and
		// found none of those in is left exactly as written.
		if safe, carried := safeCredentialURL(s); carried {
			return slog.String(attr.Key, safe)
		}
	}

	return attr
}

// safeCredentialURL reports whether a string value must be rewritten before it
// goes into a log line, and if so what to write instead.
//
// Every rule here is credsafe's. credsafe.ContainsCredentialCarrierRune says
// whether the text even has a place for user information, a query or a
// fragment; credsafe.ClassifyAddress decides what the value is; and
// credsafe.SafeRepoURL strips it. This file holds no rule of its own about
// which characters can carry a credential, and that is the point. It used to
// hold half of one — a test for "@" — while credsafe held the whole one, and
// half a rule is how an address carrying its token as a query parameter walked
// past the stripper that exists to remove query parameters.
//
// All three verdicts are answered here, and only the explicitly credential-free
// one is written out untouched (BF12). A value credsafe could not read is
// blanked rather than printed: an unreadable string is precisely the one where
// a token could be anywhere in it. That is why a scp-style Git remote, which
// Sharko does not support and net/url cannot parse, no longer appears in a log
// line as written.
//
// # The one place a value is left alone without a verdict, and why (BF13)
//
// Everywhere else in Sharko, an operator-supplied address is handed back only
// after credsafe has explicitly said it is credential-free. The gate on the
// first line below is the single exception, and it is a proof rather than a
// guess: user information needs an "@", a query needs a "?", a fragment needs
// a "#", and a string holding none of the three has no credential position in
// it at all. Without that gate the sink would have to run the address grammar
// over every log string in the tree — "cluster registered", "prod-eu",
// "/etc/sharko" — and blank every one of them, which would end logging.
//
// # The cost of the gate being on the other side, decided on purpose (BF13)
//
// Once a string DOES hold one of the three characters, it is judged as an
// address, and ordinary English that happens to hold one is not an address:
//
//	"did you mean --server?"
//	"pod is not ready: is the image pullable?"
//	"see issue #42 for details"
//
// All three come back as [REDACTED], and an operator reading a log loses the
// sentence. That was looked at again while the address grammar was rewritten,
// and it is being LEFT AS IT IS. Narrowing it means writing a rule for "this
// is prose, not an address", and that rule would have to be right about a
// string like
//
//	"could not reach https://user:pw@git.example — is the image pullable?"
//
// which reads as prose and carries a password. Loosening a credential rule to
// win back readability is the exact pressure that opened both previous holes
// in this area, and the thing being traded away here is small: only string
// ATTRIBUTES are rewritten, never the log MESSAGE, so the human sentence an
// operator actually reads is untouched. If the diagnostic value of those
// attributes is ever worth recovering, it is worth its own change with its own
// tests, not a quiet widening inside a fix for a leak.
func safeCredentialURL(s string) (string, bool) {
	if !credsafe.ContainsCredentialCarrierRune(s) {
		// No "@", no "?" and no "#" anywhere in the text, so it has no
		// userinfo section, no query and no fragment to carry anything.
		// This is not a safety verdict; it is the only case where there is
		// provably nothing for one to be about.
		return "", false
	}
	switch credsafe.ClassifyAddress(s) {
	case credsafe.AddressCredentialFree:
		return "", false
	case credsafe.AddressCarriesCredential:
		if safe := credsafe.SafeRepoURL(s); safe != "" {
			return safe, true
		}
		return redactedPlaceholder, true
	case credsafe.AddressUnclassifiable:
		return redactedPlaceholder, true
	default:
		// A verdict this build has never heard of is not a safe one.
		return redactedPlaceholder, true
	}
}

// isSensitiveKey returns true if key matches a canonical sensitive name
// (case-insensitive exact match) or ends with a sensitive suffix
// (`_token`, `_password`, `_secret`, `_key`).
//
// The empty-key case is allowed through — slog uses empty group keys for
// inlining, and an attribute with an empty key shouldn't trigger
// suffix-only redaction (`_key` shouldn't match `""`).
func isSensitiveKey(key string) bool {
	if key == "" {
		return false
	}
	// Exact, case-insensitive.
	for _, candidate := range sensitiveKeysExact {
		if strings.EqualFold(key, candidate) {
			return true
		}
	}
	// Suffix, case-insensitive. Lowercase once and compare.
	lower := strings.ToLower(key)
	for _, suffix := range sensitiveKeySuffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

// shouldRedactValue returns true if the string value matches the JWT
// regex OR is a base64 blob >100 chars.
//
// Both detectors are anchored to avoid substring matches — a log line
// that happens to mention "eyJ" inside a longer English sentence is
// not a JWT.
func shouldRedactValue(s string) bool {
	if jwtRegex.MatchString(s) {
		return true
	}
	if len(s) >= base64BlobMinLen && base64BlobRegex.MatchString(s) {
		return true
	}
	return false
}
