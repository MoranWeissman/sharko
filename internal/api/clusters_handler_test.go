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

	writeServerError(w, http.StatusInternalServerError, "list_clusters", leak)

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

	// Required content — sanitized message (uses http.StatusText so it
	// stays consistent with the HTTP status line) + op identifier for
	// client correlation with server logs.
	var parsed map[string]string
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if parsed["error"] != http.StatusText(http.StatusInternalServerError) {
		t.Errorf(`error field = %q, want %q`, parsed["error"], http.StatusText(http.StatusInternalServerError))
	}
	if parsed["op"] != "list_clusters" {
		t.Errorf(`op field = %q, want "list_clusters"`, parsed["op"])
	}
}

// TestWriteServerError_DoesNotLeakErrorString_503 is the V124-2.10
// follow-up: the same wire-contract guarantee MUST hold for legitimate
// upstream-unavailability responses, not just 500. Review finding B1
// caught that the V124-2.2 sweep stopped at 500 paths and left ~17
// 503 sites in clusters/addons/dashboard/connections still calling
// writeError(w, 503, err.Error()) — leaking raw error strings to the
// API client. With writeServerError now generalised over status, this
// test pins the wire contract for 503.
func TestWriteServerError_DoesNotLeakErrorString_503(t *testing.T) {
	w := httptest.NewRecorder()
	leak := errors.New("no active connection configured: tried 3 connections under /etc/sharko/conn-cache, all failed bcrypt verify against /var/run/secrets/auth")

	writeServerError(w, http.StatusServiceUnavailable, "get_active_git_provider", leak)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
	body, _ := io.ReadAll(w.Body)
	bodyStr := string(body)

	forbidden := []string{
		"no active connection configured",
		"/etc/sharko/conn-cache",
		"/var/run/secrets/auth",
		"bcrypt",
	}
	for _, s := range forbidden {
		if strings.Contains(bodyStr, s) {
			t.Errorf("response body leaked %q: %s", s, bodyStr)
		}
	}

	var parsed map[string]string
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if parsed["error"] != http.StatusText(http.StatusServiceUnavailable) {
		t.Errorf(`error field = %q, want %q`, parsed["error"], http.StatusText(http.StatusServiceUnavailable))
	}
	if parsed["op"] != "get_active_git_provider" {
		t.Errorf(`op field = %q, want "get_active_git_provider"`, parsed["op"])
	}
}

// TestWriteServerError_PreservesStatusText confirms the body's "error"
// field tracks the HTTP status — so a 503 reads "Service Unavailable"
// and a 500 reads "Internal Server Error". This locks down the
// status-text-derivation step inside writeServerError that closes
// review finding M2 (writeServerError-always-500-loses-status).
func TestWriteServerError_PreservesStatusText(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{http.StatusInternalServerError, "Internal Server Error"},
		{http.StatusServiceUnavailable, "Service Unavailable"},
		{http.StatusBadGateway, "Bad Gateway"},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		writeServerError(w, tc.status, "op", errors.New("any underlying error"))
		if w.Code != tc.status {
			t.Errorf("status = %d, want %d", w.Code, tc.status)
		}
		var parsed map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &parsed); err != nil {
			t.Fatalf("response is not valid JSON: %v", err)
		}
		if parsed["error"] != tc.want {
			t.Errorf(`status %d: error field = %q, want %q`, tc.status, parsed["error"], tc.want)
		}
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
	writeServerError(httptest.NewRecorder(), http.StatusInternalServerError, "list_clusters", leak)

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
