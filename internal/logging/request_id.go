// Package logging provides correlation-ID helpers used to propagate a single
// request_id through every subsystem touched by an inbound API request or
// background tick.
//
// The pattern is intentionally tiny and dependency-free: a context key, a
// constructor for fresh IDs, and a getter that returns "" when no ID is
// present. Callers can either pass the ID inline as a slog field
// (`slog.Info("event", "request_id", logging.RequestID(ctx))`) or — when a
// function has 2+ log lines — build a contextual logger once at the top of
// the function (`log := logging.LoggerFromContext(ctx); log.Info("event")`).
//
// Synthetic IDs for background work (reconciler ticks, PR poller, catalog
// scans) follow a "<source>-<unix_ts>" shape (e.g. `recon-1716543200`,
// `prtrack-1716543201`). They make non-request-driven log lines visually
// distinct from real `req-<hex>` IDs while remaining grep-friendly.
package logging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"
)

// requestIDKey is the unexported context key used to attach a request_id to
// a context. Using an empty struct type avoids accidental collisions with
// other packages' context keys (per Go's context.WithValue conventions).
type requestIDKey struct{}

// requestIDField is the canonical slog attribute key for the correlation ID.
// Exported so other packages can reference it instead of hardcoding the
// string literal — this keeps log-query tooling in sync if the field name
// ever changes.
const requestIDField = "request_id"

// RequestIDField returns the canonical slog attribute key for the correlation
// ID. Use it when constructing slog attributes by hand so the field name
// stays consistent across the codebase.
func RequestIDField() string { return requestIDField }

// WithRequestID returns a new context that carries the given request_id.
// If id is empty the input context is returned unchanged — callers should
// always pair this with NewRequestID() or an explicit synthetic ID.
func WithRequestID(ctx context.Context, id string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestID returns the request_id attached to ctx, or "" if none is set.
// Safe to call with a nil context.
func RequestID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// NewRequestID returns a fresh request_id in the canonical `req-<hex>` shape.
// The hex suffix is 16 hex characters (8 random bytes) — enough entropy to
// be unique across the lifetime of any practical Sharko deployment without
// the full ceremony of a UUID.
//
// If the OS random source fails (effectively never), the function falls back
// to a timestamp-based ID so log correlation continues to work; a complete
// random failure in production would be visible through other channels long
// before correlation IDs became the operator's primary concern.
func NewRequestID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// Fallback: nanosecond timestamp. Loses cross-request uniqueness in
		// theory but is good enough to keep correlation working during the
		// (vanishingly rare) entropy outage that would trigger this branch.
		return fmt.Sprintf("req-fb%d", time.Now().UnixNano())
	}
	return "req-" + hex.EncodeToString(buf[:])
}

// LoggerFromContext returns slog.Default() decorated with the request_id
// from ctx, if one is present. When no ID is set, the default logger is
// returned unchanged.
//
// This is the convenience entry point for functions that emit 2+ log lines:
// build one contextual logger at the top and use it for every subsequent
// emission instead of repeating `slog.Info(..., "request_id", logging.RequestID(ctx), ...)`
// per call site.
func LoggerFromContext(ctx context.Context) *slog.Logger {
	id := RequestID(ctx)
	if id == "" {
		return slog.Default()
	}
	return slog.Default().With(requestIDField, id)
}

// Attr returns a slog.Attr carrying the request_id from ctx (or empty when
// none is set). Useful at call sites that already use the slog.Attr-based
// API (`slog.LogAttrs`).
func Attr(ctx context.Context) slog.Attr {
	return slog.String(requestIDField, RequestID(ctx))
}
