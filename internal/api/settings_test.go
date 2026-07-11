package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/settings"
)

// V2-cleanup-90.3 / review finding M7b — HTTP-layer tests for the
// allow-inline-credentials setting endpoints. Before this file, the
// GET/PUT handlers in settings.go (added in V2-cleanup-89.6) had zero HTTP
// test coverage — only the underlying settings.Store methods were tested.

// settingsAdminReq builds an authenticated admin request — the PUT route
// requires authz.Require("settings.allow-inline-credentials") which
// resolves to RoleAdmin.
func settingsAdminReq(method, body string) *http.Request {
	var r io.Reader
	if body != "" {
		r = bytes.NewBufferString(body)
	}
	req := httptest.NewRequest(method, "/api/v1/settings/allow-inline-credentials", r)
	req.Header.Set("X-Sharko-User", "admin")
	req.Header.Set("X-Sharko-Role", "admin")
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestHandleGetAllowInlineCredentials_HappyPath(t *testing.T) {
	srv := newIsolatedTestServer(t)
	client := fake.NewSimpleClientset()
	store := settings.NewStore(client, "sharko")
	if err := store.SetAllowInlineCredentials(t.Context(), false); err != nil {
		t.Fatalf("SetAllowInlineCredentials: %v", err)
	}
	srv.SetSettingsStore(store)
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/allow-inline-credentials", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var body allowInlineCredentialsResponse
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.AllowInlineCredentials {
		t.Errorf("allow_inline_credentials = true, want false (the value persisted before the GET)")
	}
}

func TestHandleGetAllowInlineCredentials_NilStore_ReturnsDefaultTrue(t *testing.T) {
	// A Server with no SetSettingsStore call at all — matches an
	// out-of-cluster / local dev deployment. Pinned status: 200 with the
	// static default (true, allowed), NOT an error.
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/allow-inline-credentials", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with no settings store wired, got %d (body=%s)", w.Code, w.Body.String())
	}
	var body allowInlineCredentialsResponse
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.AllowInlineCredentials {
		t.Errorf("allow_inline_credentials = false, want default true when no settings store is wired")
	}
}

func TestHandleSetAllowInlineCredentials_Admin_HappyPath(t *testing.T) {
	srv := newIsolatedTestServer(t)
	client := fake.NewSimpleClientset()
	store := settings.NewStore(client, "sharko")
	srv.SetSettingsStore(store)
	router := NewRouter(srv, nil)

	req := settingsAdminReq(http.MethodPut, `{"allow_inline_credentials": false}`)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var body allowInlineCredentialsResponse
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.AllowInlineCredentials {
		t.Errorf("response allow_inline_credentials = true, want false")
	}

	// Persisted — a follow-up GET must see the same value.
	allow, err := store.GetAllowInlineCredentials(t.Context())
	if err != nil {
		t.Fatalf("GetAllowInlineCredentials: %v", err)
	}
	if allow {
		t.Error("expected the setting to be persisted as false")
	}
}

func TestHandleSetAllowInlineCredentials_NonAdmin_403(t *testing.T) {
	srv := newIsolatedTestServer(t)
	client := fake.NewSimpleClientset()
	store := settings.NewStore(client, "sharko")
	srv.SetSettingsStore(store)
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings/allow-inline-credentials", bytes.NewBufferString(`{"allow_inline_credentials": false}`))
	req.Header.Set("X-Sharko-User", "bob")
	req.Header.Set("X-Sharko-Role", "operator")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a non-admin role, got %d (body=%s)", w.Code, w.Body.String())
	}

	// The setting must be untouched — still the default.
	allow, err := store.GetAllowInlineCredentials(t.Context())
	if err != nil {
		t.Fatalf("GetAllowInlineCredentials: %v", err)
	}
	if !allow {
		t.Error("a rejected PUT must not have changed the setting")
	}
}

func TestHandleSetAllowInlineCredentials_NilStore_Returns503(t *testing.T) {
	// Pinned status for "no settings store wired" on the PUT path: 503,
	// NOT the GET path's silent-default-true behavior — a write with
	// nowhere to persist to must be visible as an error, not silently
	// dropped.
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	req := settingsAdminReq(http.MethodPut, `{"allow_inline_credentials": false}`)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 with no settings store wired, got %d (body=%s)", w.Code, w.Body.String())
	}
}

func TestHandleSetAllowInlineCredentials_BadBody_400(t *testing.T) {
	srv := newIsolatedTestServer(t)
	client := fake.NewSimpleClientset()
	store := settings.NewStore(client, "sharko")
	srv.SetSettingsStore(store)
	router := NewRouter(srv, nil)

	req := settingsAdminReq(http.MethodPut, `{not valid json`)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a malformed body, got %d (body=%s)", w.Code, w.Body.String())
	}
}
