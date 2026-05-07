package api

// stale_login_route_test.go — V124-6.1 / BUG-021 regression coverage.
//
// Background: a maintainer UI walkthrough on 2026-05-08 reported a 401 on
// `POST /api/v1/login` (146µs, no payload echoed) — indistinguishable from a
// real auth endpoint failing on bad credentials. Investigation showed the
// path was never registered; the 401 came from basicAuthMiddleware swallowing
// the unauthenticated POST. We now register an explicit 404 handler and
// carve `/api/v1/login` out of basicAuthMiddleware so the 404 reaches the
// client. The real endpoint remains `POST /api/v1/auth/login`.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStaleLoginRoute_Returns404WithHint(t *testing.T) {
	srv := newTestServer()
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/login",
		bytes.NewReader([]byte(`{"username":"admin","password":"x"}`)))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on POST /api/v1/login, got %d (body=%s)", w.Code, w.Body.String())
	}

	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode error response: %v (body=%s)", err, w.Body.String())
	}
	msg := body["error"]
	if msg == "" {
		// Tolerate either "error" or "message" key — writeError uses "error".
		msg = body["message"]
	}
	if !strings.Contains(strings.ToLower(msg), "auth/login") {
		t.Fatalf("expected hint pointing to /auth/login in error message, got %q", msg)
	}
}

// TestRealLoginRoute_StillReachable guards against accidental regression of
// the real endpoint when the dead-route stub is wired up.
func TestRealLoginRoute_StillReachable(t *testing.T) {
	srv := newTestServer()
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		bytes.NewReader([]byte(`{"username":"admin","password":"wrong"}`)))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// In the test server there are no users configured, so handleLogin's
	// "no auth configured" branch issues an anonymous session token and
	// returns 200. Either way, status MUST NOT be 404 — the dead-route
	// stub must not have stolen the real endpoint.
	if w.Code == http.StatusNotFound {
		t.Fatalf("real /api/v1/auth/login should not be 404; got %d (body=%s)",
			w.Code, w.Body.String())
	}
}
