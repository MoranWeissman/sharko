package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// putConnection sends PUT /api/v1/connections/patch-handler-test with the
// git and argocd blocks the existing tests use, plus whatever extra keys the
// test wants to add.
func putConnectionWith(t *testing.T, router http.Handler, argoURL string, extra map[string]interface{}) {
	t.Helper()
	payload := map[string]interface{}{
		"git": map[string]interface{}{"provider": "github", "owner": "sharko-test", "repo": "sharko-addons"},
		"argocd": map[string]interface{}{
			"server_url": argoURL,
			"token":      "stub-token-not-validated-by-httptest",
			"insecure":   true,
		},
	}
	for k, v := range extra {
		payload[k] = v
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/connections/patch-handler-test", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /connections status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
}

// Choosing "None" for a secrets backend must really remove the saved one.
// A missing provider block still means "leave it alone".
func TestUpdateConnection_EmptyProviderTypeClearsSavedBackend(t *testing.T) {
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_CONFIG_FILE", "/dev/null")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/dev/null")
	t.Setenv("AWS_REGION", "eu-west-1")
	t.Setenv("AWS_PROFILE", "")

	srv := newIsolatedTestServer(t)
	argoStub := startArgocdStub(t, nil)
	seedActiveConnectionWithArgo(t, srv, argoStub.URL)
	router := NewRouter(srv, nil)

	putConnectionWith(t, router, argoStub.URL, map[string]interface{}{
		"provider":              map[string]interface{}{"type": "aws-sm", "region": "eu-west-1"},
		"addon_secret_provider": map[string]interface{}{"type": "aws-sm", "region": "eu-west-1"},
	})
	conn, err := srv.connSvc.GetConnection("patch-handler-test")
	if err != nil || conn == nil || conn.Provider == nil || conn.AddonSecretProvider == nil {
		t.Fatalf("setup: both backends should be saved, got %+v (err %v)", conn, err)
	}

	// No provider keys at all: nothing changes.
	putConnectionWith(t, router, argoStub.URL, nil)
	conn, _ = srv.connSvc.GetConnection("patch-handler-test")
	if conn.Provider == nil || conn.AddonSecretProvider == nil {
		t.Fatalf("a request with no provider blocks must keep the saved backends, got %+v", conn)
	}

	// Empty type: cleared.
	putConnectionWith(t, router, argoStub.URL, map[string]interface{}{
		"provider":              map[string]interface{}{"type": ""},
		"addon_secret_provider": map[string]interface{}{"type": ""},
	})
	conn, _ = srv.connSvc.GetConnection("patch-handler-test")
	if conn.Provider != nil {
		t.Errorf("provider should be cleared by an empty type, still %+v", conn.Provider)
	}
	if conn.AddonSecretProvider != nil {
		t.Errorf("addon secret provider should be cleared by an empty type, still %+v", conn.AddonSecretProvider)
	}
}
