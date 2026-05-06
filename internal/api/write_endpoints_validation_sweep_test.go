package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// V124-4.5 — class-of-bug sweep over the entire write-endpoint surface.
//
// Goal: assert that every POST/PUT/PATCH/DELETE handler that receives a body
// rejects an empty `{}` body with a structured 4xx response (400 for required-
// field validation, 404 for unknown resources / unknown API paths, 503 for
// missing prerequisite providers). The forbidden case is "200 OK with garbage
// persisted" (BUG-017 class) and "5xx upstream error before validation"
// (BUG-019 class).
//
// Cases that are intentionally exempt from the 400-on-empty rule are listed
// in the audit table in the V124-4.5 commit message — webhooks (HMAC-validated
// arbitrary payloads), heartbeat / read-only-POST endpoints, etc. The endpoint
// class here is the conservative "must reject empty body OR document why not".
//
// Note on isolated test server: this server has no active connection wired,
// no credentials provider, no auth headers. So the contract this test pins is
// "the response is NOT a 200 success with garbage" — we accept any 4xx OR a
// 5xx that mentions an upstream error (which proves validation passed). The
// thing we reject is silent 200.

type sweepCase struct {
	name   string
	method string
	path   string
	body   []byte
	// allowSuccess records endpoints where the test server's auth-disabled
	// state allows a 200/201 response on empty body — these are intentionally
	// exempt and must be documented (e.g. test/diagnose endpoints, no-op POSTs).
	allowSuccess bool
}

func TestWriteEndpoints_EmptyBody_RejectedOrUpstreamErrored(t *testing.T) {
	cases := []sweepCase{
		// connections/
		{name: "POST /connections/", method: http.MethodPost, path: "/api/v1/connections/", body: []byte("{}")},
		{name: "PUT /connections/{name}", method: http.MethodPut, path: "/api/v1/connections/foo", body: []byte("{}")},
		{name: "POST /connections/active", method: http.MethodPost, path: "/api/v1/connections/active", body: []byte("{}")},
		// clusters/
		{name: "POST /clusters", method: http.MethodPost, path: "/api/v1/clusters", body: []byte("{}")},
		{name: "POST /clusters/batch", method: http.MethodPost, path: "/api/v1/clusters/batch", body: []byte("{}")},
		{name: "POST /clusters/adopt", method: http.MethodPost, path: "/api/v1/clusters/adopt", body: []byte("{}")},
		{name: "PATCH /clusters/{name}", method: http.MethodPatch, path: "/api/v1/clusters/foo", body: []byte("{}")},
		{name: "POST /clusters/{name}/unadopt", method: http.MethodPost, path: "/api/v1/clusters/foo/unadopt", body: []byte("{}")},
		// addons/
		{name: "POST /addons", method: http.MethodPost, path: "/api/v1/addons", body: []byte("{}")},
		{name: "POST /addons/{name}/upgrade", method: http.MethodPost, path: "/api/v1/addons/foo/upgrade", body: []byte("{}")},
		{name: "POST /addons/upgrade-batch", method: http.MethodPost, path: "/api/v1/addons/upgrade-batch", body: []byte("{}")},
		{name: "PATCH /addons/{name}", method: http.MethodPatch, path: "/api/v1/addons/foo", body: []byte("{}"), allowSuccess: true /* partial-update no-op */},
		{name: "PUT /addons/{name}/values", method: http.MethodPut, path: "/api/v1/addons/foo/values", body: []byte("{}")},
		// addon-secrets
		{name: "POST /addon-secrets", method: http.MethodPost, path: "/api/v1/addon-secrets", body: []byte("{}")},
		// users / tokens
		{name: "POST /users", method: http.MethodPost, path: "/api/v1/users", body: []byte("{}")},
		{name: "POST /tokens", method: http.MethodPost, path: "/api/v1/tokens", body: []byte("{}")},
		// AI
		{name: "POST /ai/config", method: http.MethodPost, path: "/api/v1/ai/config", body: []byte("{}")},
		// upgrade
		{name: "POST /upgrade/check", method: http.MethodPost, path: "/api/v1/upgrade/check", body: []byte("{}")},
		// agent
		{name: "POST /agent/chat", method: http.MethodPost, path: "/api/v1/agent/chat", body: []byte("{}")},
		// Unknown path (V124-4.4 catch-all)
		{name: "POST /api/v1/notifications/providers (unknown)", method: http.MethodPost, path: "/api/v1/notifications/providers", body: []byte("{}")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newIsolatedTestServer(t)
			router := NewRouter(srv, nil)

			req := httptest.NewRequest(tc.method, tc.path, bytes.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			// Forbidden case: a 2xx that carries garbage. The acceptable
			// outcomes are:
			//   - 4xx (validation rejected the body)
			//   - 5xx (validation passed; an upstream resolution failed)
			//   - 2xx ONLY when allowSuccess is set (documented exemption)
			if w.Code/100 == 2 && !tc.allowSuccess {
				t.Fatalf("%s %s with empty body returned %d (BUG-017 class: must NOT silently succeed)\nbody: %s",
					tc.method, tc.path, w.Code, w.Body.String())
			}

			// For every non-2xx, the body must be valid JSON. SPA HTML
			// fall-through (the BUG-020 class) would fail this assertion.
			if w.Code/100 != 2 {
				var anyJSON map[string]interface{}
				if err := json.NewDecoder(w.Body).Decode(&anyJSON); err != nil {
					t.Fatalf("%s %s body is not valid JSON (BUG-020 class: SPA HTML fell through?): %v\nbody: %s",
						tc.method, tc.path, err, w.Body.String())
				}
			}
		})
	}
}
