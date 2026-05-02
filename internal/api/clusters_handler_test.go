package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// V124-2.2 — handler-level guarantees for the BUG-005 family.
//
// We exercise the writeServerError contract directly rather than spinning
// up a full Server, because the cluster handlers depend on a live ArgoCD
// client which is hard to stub at the HTTP layer. The two invariants we
// lock down here:
//
//  1. The JSON response body MUST NOT include the underlying error string
//     (no filesystem paths, no upstream messages).
//  2. The full error MUST be logged server-side under a grep-friendly op
//     identifier so debugging is unaffected.
//
// Plus a service-level test (in cluster_test.go) that proves the empty-
// list semantic for missing managed-clusters.yaml.

func TestWriteServerError_DoesNotLeakErrorString(t *testing.T) {
	w := httptest.NewRecorder()
	leak := errors.New("reading managed-clusters.yaml: file not found: /home/user/.sharko/secret-path/managed-clusters.yaml")

	writeServerError(w, "list_clusters", leak)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
	body, _ := io.ReadAll(w.Body)
	bodyStr := string(body)

	// Forbidden substrings — anything that would have leaked under the
	// pre-fix behaviour.
	forbidden := []string{
		"/home/user/",
		"managed-clusters.yaml",
		"file not found",
		".sharko",
		"secret-path",
	}
	for _, s := range forbidden {
		if strings.Contains(bodyStr, s) {
			t.Errorf("response body leaked %q: %s", s, bodyStr)
		}
	}

	// Required content — sanitized message + op identifier for client
	// correlation with server logs.
	var parsed map[string]string
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if parsed["error"] != "internal server error" {
		t.Errorf(`error field = %q, want "internal server error"`, parsed["error"])
	}
	if parsed["op"] != "list_clusters" {
		t.Errorf(`op field = %q, want "list_clusters"`, parsed["op"])
	}
}

// TestWriteServerError_LogsFullError captures the slog output to confirm
// the underlying error is preserved server-side. This is the other half
// of the contract: scrub the wire, keep the logs.
func TestWriteServerError_LogsFullError(t *testing.T) {
	cap := &captureHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(cap))
	t.Cleanup(func() { slog.SetDefault(prev) })

	leak := errors.New("reading managed-clusters.yaml: file not found: configuration/managed-clusters.yaml")
	writeServerError(httptest.NewRecorder(), "list_clusters", leak)

	if len(cap.records) == 0 {
		t.Fatal("expected at least one log record, got none")
	}
	joined := strings.Join(cap.records, "\n")
	if !strings.Contains(joined, "list_clusters") {
		t.Errorf("op identifier missing from logs: %s", joined)
	}
	if !strings.Contains(joined, "managed-clusters.yaml") {
		t.Errorf("underlying error not preserved in logs (debug regression): %s", joined)
	}
}

// captureHandler is a minimal slog.Handler that stores formatted records
// in memory so tests can assert on log output without a real backend.
type captureHandler struct {
	records []string
}

func (h *captureHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	var sb strings.Builder
	sb.WriteString(r.Message)
	r.Attrs(func(a slog.Attr) bool {
		sb.WriteString(" ")
		sb.WriteString(a.Key)
		sb.WriteString("=")
		sb.WriteString(a.Value.String())
		return true
	})
	h.records = append(h.records, sb.String())
	return nil
}
func (h *captureHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(_ string) slog.Handler      { return h }
