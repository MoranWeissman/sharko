package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// V124-4.1 / BUG-018 — write/discover endpoints that require a credentials
// provider must respond with 503 + an actionable hint when no provider is
// configured, NOT 501 (which implied "endpoint not implemented" and led the
// V124-3 smoke runner to mis-classify the failure).
//
// These tests pin the new contract:
//   - HTTP status is 503 Service Unavailable (not 501)
//   - Body includes a stable error code "provider_not_configured"
//   - Body includes a hint string operators/UIs can render
//
// Coverage: every handler that early-returns on `s.credProvider == nil`.
//
// Note: handler order is "authz → provider check → other". Tests use the
// isolated test server which has no auth headers wired, so the authz layer
// allows the request through and we hit the provider check first.

func assertProviderMissingResponse(t *testing.T, w *httptest.ResponseRecorder, label string) {
	t.Helper()
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("%s: status = %d, want 503 (V124-4.1: provider-missing must NOT return 501)", label, w.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("%s: response is not valid JSON: %v", label, err)
	}
	if body["code"] != "provider_not_configured" {
		t.Errorf("%s: body code = %q, want %q", label, body["code"], "provider_not_configured")
	}
	if body["hint"] == "" {
		t.Errorf("%s: body hint should be non-empty (operators need an actionable next step)", label)
	}
	if body["error"] == "" {
		t.Errorf("%s: body error should be non-empty", label)
	}
}

func TestRegisterCluster_NoProvider_Returns503(t *testing.T) {
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	body, _ := json.Marshal(map[string]string{"name": "any-cluster"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assertProviderMissingResponse(t, w, "POST /clusters")
}

func TestBatchRegisterClusters_NoProvider_Returns503(t *testing.T) {
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	body, _ := json.Marshal(map[string]interface{}{"clusters": []map[string]string{{"name": "a"}}})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/batch", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assertProviderMissingResponse(t, w, "POST /clusters/batch")
}

func TestDiscoverClusters_NoProvider_Returns503(t *testing.T) {
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/available", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assertProviderMissingResponse(t, w, "GET /clusters/available")
}

func TestRefreshClusterCredentials_NoProvider_Returns503(t *testing.T) {
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/foo/refresh", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assertProviderMissingResponse(t, w, "POST /clusters/{name}/refresh")
}

func TestListClusterSecrets_NoProvider_Returns503(t *testing.T) {
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/foo/secrets", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assertProviderMissingResponse(t, w, "GET /clusters/{name}/secrets")
}

func TestRefreshClusterSecrets_NoProvider_Returns503(t *testing.T) {
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/foo/secrets/refresh", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assertProviderMissingResponse(t, w, "POST /clusters/{name}/secrets/refresh")
}
