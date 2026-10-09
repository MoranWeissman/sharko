package api

// audit_stream_auth_test.go — V2-cleanup-85.2 regression coverage.
//
// Background: the audit Live Tail feature in the UI uses the browser's
// native EventSource, which cannot set an Authorization header — so it
// passes the session token as a `?token=` query param instead.
// basicAuthMiddleware only ever read `Authorization: Bearer`, so Live Tail
// 401-looped forever. The fix accepts `?token=` ONLY for
// `GET /api/v1/audit/stream`; every other route must keep requiring a real
// Bearer header.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newAuthedTestServer returns a Server with at least one user configured
// (so authStore.HasUsers() is true and basicAuthMiddleware actually
// enforces auth) plus a valid session token for that user.
func newAuthedTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	srv := newTestServer()
	if err := srv.authStore.AddUser("alice", "correct-horse-battery-staple", "admin"); err != nil {
		t.Fatalf("AddUser: %v", err)
	}

	token := "test-session-token-" + t.Name()
	sessionsMu.Lock()
	activeSessions[token] = &sessionInfo{Username: "alice", Expiry: time.Now().Add(1 * time.Hour)}
	sessionsMu.Unlock()
	t.Cleanup(func() {
		sessionsMu.Lock()
		delete(activeSessions, token)
		sessionsMu.Unlock()
	})

	return srv, token
}

// TestAuditStream_QueryTokenAuth_Valid — a valid ?token= with NO
// Authorization header must authenticate GET /api/v1/audit/stream (the
// EventSource case). We cancel the request context shortly after dispatch
// so the streaming handler's `<-r.Context().Done()` branch returns instead
// of hanging the test; the recorder's status defaults to 200 unless the
// handler wrote an error, so an unauthenticated request (401) is
// distinguishable from an authenticated one that just never saw an event.
func TestAuditStream_QueryTokenAuth_Valid(t *testing.T) {
	srv, token := newAuthedTestServer(t)
	router := NewRouter(srv, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/stream?token="+token, nil).WithContext(ctx)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (stream opened) for a valid ?token=, got %d (body=%s)", w.Code, w.Body.String())
	}
}

// TestAuditStream_QueryTokenAuth_InvalidOrAbsent — an invalid or missing
// token must still 401. The query-param fallback validates the SAME way
// the Bearer path does — it is not an auth bypass.
func TestAuditStream_QueryTokenAuth_InvalidOrAbsent(t *testing.T) {
	srv, _ := newAuthedTestServer(t)
	router := NewRouter(srv, nil)

	tests := []struct {
		name string
		url  string
	}{
		{"invalid token", "/api/v1/audit/stream?token=not-a-real-token"},
		{"absent token", "/api/v1/audit/stream"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.url, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d (body=%s)", w.Code, w.Body.String())
			}
		})
	}
}

// TestQueryTokenAuth_ScopedToAuditStreamOnly — the ?token= fallback must
// NOT work on any other route, even with a valid token. Widening it would
// let a token leak into browser history / server access logs / referrer
// headers for every endpoint instead of just the one SSE route that has no
// other option.
func TestQueryTokenAuth_ScopedToAuditStreamOnly(t *testing.T) {
	srv, token := newAuthedTestServer(t)
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters?token="+token, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 — query-param auth must stay scoped to GET /api/v1/audit/stream, got %d (body=%s)", w.Code, w.Body.String())
	}
}

// TestAuditStream_BearerAuth_StillWorks — the pre-existing Bearer path must
// be unaffected by the new fallback.
func TestAuditStream_BearerAuth_StillWorks(t *testing.T) {
	srv, token := newAuthedTestServer(t)
	router := NewRouter(srv, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/stream", nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (stream opened) for a valid Bearer token, got %d (body=%s)", w.Code, w.Body.String())
	}
}
