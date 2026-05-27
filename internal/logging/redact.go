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
//  3. Base64-blob detector — the attribute VALUE is >100 chars and consists
//     entirely of the base64 alphabet `[A-Za-z0-9+/=]`. Catches kubeconfig
//     fragments, PEM-encoded certificates pasted into log lines, and other
//     large opaque secret-shaped payloads. The 100-char threshold avoids
//     false-positives on short tokens (which are caught by the key-name
//     heuristic) and on short alphanumeric IDs.
//
// All three detectors collapse to the same replacement string "[REDACTED]"
// — deliberately type-blind, so a downstream reader of the logs cannot tell
// whether the redacted field was a JWT, a kubeconfig, or a password. This
// prevents partial-information leaks ("ah, it was a JWT — so the key was
// `auth_token`").
//
// Opt-out escape hatch: an attribute key with the prefix `_unsafe_` bypasses
// ALL three detectors. This is for deliberate dev-debug instrumentation
// where the operator explicitly wants the raw value in the log. The prefix
// must be present at every call site — there is no global "disable
// redaction" flag, so a stray `_unsafe_` import cannot silently widen the
// surface. The naming is intentional: it should look ugly in code review.
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
	"log/slog"
	"regexp"
	"strings"
)

// redactedPlaceholder is the uniform replacement string. Deliberately
// type-blind: a reader of the logs cannot tell whether the original value
// was a JWT, a kubeconfig, or a password.
const redactedPlaceholder = "[REDACTED]"

// unsafePrefix is the opt-out escape hatch. An attribute key with this
// prefix bypasses ALL redaction. Intended for deliberate dev-debug
// instrumentation where the operator explicitly wants the raw value.
const unsafePrefix = "_unsafe_"

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
//   - `_unsafe_*` keys bypass redaction entirely.
//   - Group-valued attrs recurse so nested sensitive attrs are caught.
//   - Otherwise: redact if the KEY is sensitive (heuristic) OR the
//     VALUE matches the JWT regex OR the VALUE is a base64 blob >100 chars.
func redactAttr(attr slog.Attr) slog.Attr {
	if strings.HasPrefix(attr.Key, unsafePrefix) {
		return attr
	}

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

	// Value-shape detection — only for string-valued attrs. Resolve the
	// value first so LogValuer-wrapped values (lazy strings) are seen.
	resolved := attr.Value.Resolve()
	if resolved.Kind() == slog.KindString {
		s := resolved.String()
		if shouldRedactValue(s) {
			return slog.String(attr.Key, redactedPlaceholder)
		}
	}

	return attr
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
