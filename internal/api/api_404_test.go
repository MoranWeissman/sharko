package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// V124-4.4 / BUG-020 — unknown `/api/v1/*` paths must return a structured
// 404 JSON, NOT silently fall through to the SPA's index.html.
//
// Pre-V124-4 the SPA catch-all (`mux.HandleFunc("/", ...)`) served
// index.html for any path not matched by a more specific route. The V124
// Track B re-smoke (B.4) hit `POST /api/v1/notifications/providers` and
// got 200 OK with an HTML body, which the runner read as a passing
// validation case — the literal opposite of what was meant.
//
// The fix registers `/api/v1/` as an explicit catch-all BEFORE the SPA
// catch-all so unknown API paths fail loudly with a stable error code.
// Real API routes are registered above with method+path patterns that win
// the match by Go 1.22+ ServeMux longest-match semantics.

func TestUnknownAPIPath_POST_Returns404JSON(t *testing.T) {
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/notifications/providers", bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (V124-4.4: unknown API path must NOT return SPA HTML 200)", w.Code)
	}
	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("response is not valid JSON (likely SPA HTML fell through): %v\nbody: %s", err, w.Body.String())
	}
	if resp["code"] != "endpoint_not_found" {
		t.Errorf("body code = %q, want endpoint_not_found", resp["code"])
	}
	if resp["path"] != "/api/v1/notifications/providers" {
		t.Errorf("body should echo the requested path, got: %q", resp["path"])
	}
	if resp["method"] != "POST" {
		t.Errorf("body should echo the request method, got: %q", resp["method"])
	}
}

func TestUnknownAPIPath_GET_Returns404JSON(t *testing.T) {
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/no/such/path", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("response is not valid JSON: %v\nbody: %s", err, w.Body.String())
	}
	if resp["code"] != "endpoint_not_found" {
		t.Errorf("body code = %q, want endpoint_not_found", resp["code"])
	}
}

// TestKnownAPIPath_StillWorks — regression guard: the new /api/v1/ catch-all
// must NOT shadow real registered routes. Go 1.22+ ServeMux longest-match
// rules should give the specific routes precedence, but we pin the
// behaviour here so a future router refactor can't quietly break it.
func TestKnownAPIPath_StillWorks(t *testing.T) {
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code == http.StatusNotFound {
		t.Fatalf("V124-4.4 regression: /api/v1/health was shadowed by the catch-all (status=%d)", w.Code)
	}
}
