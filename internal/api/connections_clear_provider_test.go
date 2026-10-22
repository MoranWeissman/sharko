package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
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

// Saving the secrets backend from Settings leaves the namespace and the role
// out of the request, because that screen has no fields for them. They must
// still be there after the save. A different backend starts clean, and a value
// that is sent wins over the saved one.
func TestUpdateConnection_SaveKeepsNamespaceAndRoleARN(t *testing.T) {
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_CONFIG_FILE", "/dev/null")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/dev/null")
	t.Setenv("AWS_REGION", "eu-west-1")
	t.Setenv("AWS_PROFILE", "")

	srv := newIsolatedTestServer(t)
	argoStub := startArgocdStub(t, nil)
	seedActiveConnectionWithArgo(t, srv, argoStub.URL)
	router := NewRouter(srv, nil)

	const role = "arn:aws:iam::111122223333:role/sharko-test"
	putConnectionWith(t, router, argoStub.URL, map[string]interface{}{
		"provider":              map[string]interface{}{"type": "aws-sm", "region": "eu-west-1", "role_arn": role},
		"addon_secret_provider": map[string]interface{}{"type": "k8s-secrets", "namespace": "my-secrets"},
	})
	get := func() (provider, addon *models.ProviderConfig) {
		conn, err := srv.connSvc.GetConnection("patch-handler-test")
		if err != nil || conn == nil {
			t.Fatalf("GetConnection: %v", err)
		}
		return conn.Provider, conn.AddonSecretProvider
	}
	p, a := get()
	if p == nil || p.RoleARN != role || a == nil || a.Namespace != "my-secrets" {
		t.Fatalf("setup: both values should be saved, got %+v / %+v", p, a)
	}

	// Settings-style save: same backends, new prefix, no namespace or role sent.
	putConnectionWith(t, router, argoStub.URL, map[string]interface{}{
		"provider":              map[string]interface{}{"type": "aws-sm", "region": "eu-west-1", "prefix": "clusters/"},
		"addon_secret_provider": map[string]interface{}{"type": "kubernetes"},
	})
	p, a = get()
	if p == nil || p.RoleARN != role || p.Prefix != "clusters/" {
		t.Errorf("role must survive a save that leaves it out, and the prefix change must land, got %+v", p)
	}
	if a == nil || a.Namespace != "my-secrets" {
		t.Errorf("namespace must survive a save that leaves it out (alias of the same backend), got %+v", a)
	}

	// A value that is sent replaces the saved one.
	putConnectionWith(t, router, argoStub.URL, map[string]interface{}{
		"provider":              map[string]interface{}{"type": "aws-sm", "region": "eu-west-1", "role_arn": "arn:aws:iam::111122223333:role/other"},
		"addon_secret_provider": map[string]interface{}{"type": "k8s-secrets", "namespace": "other-ns"},
	})
	p, a = get()
	if p.RoleARN != "arn:aws:iam::111122223333:role/other" || a.Namespace != "other-ns" {
		t.Errorf("sent values must win, got %+v / %+v", p, a)
	}

	// A different backend starts clean.
	putConnectionWith(t, router, argoStub.URL, map[string]interface{}{
		"provider":              map[string]interface{}{"type": "k8s-secrets"},
		"addon_secret_provider": map[string]interface{}{"type": "aws-sm", "region": "eu-west-1"},
	})
	p, a = get()
	if p.RoleARN != "" || p.Region != "" {
		t.Errorf("role must not follow the user onto another backend, got %+v", p)
	}
	if a.Namespace != "" {
		t.Errorf("namespace must not follow the user onto another backend, got %+v", a)
	}
}

func TestMergeProviderConfig_KeepsHiddenFieldsOnlyForSameBackend(t *testing.T) {
	saved := &models.ProviderConfig{Type: "aws-sm", Region: "r", Namespace: "ns", RoleARN: "arn"}
	got := mergeProviderConfig(&models.ProviderConfig{Type: "aws-secrets-manager", Region: "r2"}, saved)
	if got.Namespace != "ns" || got.RoleARN != "arn" || got.Region != "r2" {
		t.Errorf("same backend by alias: want kept namespace/role and new region, got %+v", got)
	}
	got = mergeProviderConfig(&models.ProviderConfig{Type: "k8s-secrets"}, saved)
	if got.Namespace != "" || got.RoleARN != "" {
		t.Errorf("different backend: want clean, got %+v", got)
	}
	got = mergeProviderConfig(&models.ProviderConfig{Type: "aws-sm"}, nil)
	if got.Type != "aws-sm" {
		t.Errorf("nothing saved: request should pass through, got %+v", got)
	}
}
