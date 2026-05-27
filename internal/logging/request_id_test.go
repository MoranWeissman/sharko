package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestRequestIDRoundTrip(t *testing.T) {
	ctx := context.Background()
	if got := RequestID(ctx); got != "" {
		t.Fatalf("expected empty RequestID on bare context, got %q", got)
	}

	ctx = WithRequestID(ctx, "req-abc123")
	if got := RequestID(ctx); got != "req-abc123" {
		t.Fatalf("expected req-abc123, got %q", got)
	}
}

func TestRequestIDMissing(t *testing.T) {
	// nil context must not panic — defensive contract.
	if got := RequestID(nil); got != "" { //nolint:staticcheck // intentional nil-ctx test
		t.Fatalf("expected empty RequestID on nil context, got %q", got)
	}
}

func TestWithRequestIDEmptyIsNoop(t *testing.T) {
	parent := WithRequestID(context.Background(), "req-keep")
	child := WithRequestID(parent, "")
	if got := RequestID(child); got != "req-keep" {
		t.Fatalf("expected req-keep preserved when overriding with empty, got %q", got)
	}
}

func TestWithRequestIDNilContext(t *testing.T) {
	// Passing a nil context must not panic and must return a usable context.
	ctx := WithRequestID(nil, "req-from-nil") //nolint:staticcheck // intentional nil-ctx test
	if got := RequestID(ctx); got != "req-from-nil" {
		t.Fatalf("expected req-from-nil, got %q", got)
	}
}

func TestNewRequestIDShape(t *testing.T) {
	id := NewRequestID()
	if !strings.HasPrefix(id, "req-") {
		t.Fatalf("expected req- prefix, got %q", id)
	}
	if len(id) < len("req-")+8 {
		t.Fatalf("expected id length >= 12, got %d (%q)", len(id), id)
	}
}

func TestNewRequestIDUniqueness(t *testing.T) {
	// Two calls in a row must produce different IDs (random suffix).
	a := NewRequestID()
	b := NewRequestID()
	if a == b {
		t.Fatalf("expected distinct IDs, got %q == %q", a, b)
	}
}

func TestLoggerFromContextEmits(t *testing.T) {
	// Capture slog output via a buffer-backed JSON handler so we can
	// assert the request_id appears in the emitted record.
	var buf bytes.Buffer
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))

	ctx := WithRequestID(context.Background(), "req-deadbeef")
	logger := LoggerFromContext(ctx)
	logger.Info("hello")

	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &rec); err != nil {
		t.Fatalf("decode emitted record: %v (raw=%q)", err, buf.String())
	}
	if rec["request_id"] != "req-deadbeef" {
		t.Fatalf("expected request_id=req-deadbeef in record, got %v (raw=%q)", rec["request_id"], buf.String())
	}
	if rec["msg"] != "hello" {
		t.Fatalf("expected msg=hello, got %v", rec["msg"])
	}
}

func TestLoggerFromContextNoIDReturnsDefault(t *testing.T) {
	logger := LoggerFromContext(context.Background())
	if logger != slog.Default() {
		t.Fatalf("expected slog.Default() when ctx has no request_id, got a different logger")
	}
}

func TestAttrCarriesRequestID(t *testing.T) {
	ctx := WithRequestID(context.Background(), "req-zz")
	attr := Attr(ctx)
	if attr.Key != "request_id" {
		t.Fatalf("expected key=request_id, got %q", attr.Key)
	}
	if attr.Value.String() != "req-zz" {
		t.Fatalf("expected value=req-zz, got %q", attr.Value.String())
	}
}

func TestRequestIDField(t *testing.T) {
	if RequestIDField() != "request_id" {
		t.Fatalf("expected request_id, got %q", RequestIDField())
	}
}
