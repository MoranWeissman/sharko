package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/MoranWeissman/sharko/internal/providers"
)

// V2-cleanup-55.5: GET /api/v1/providers must report the configured prefix.
// providerDisplay() has always computed (type, region, prefix), but the
// response JSON only carried type/region/status — prefix appeared solely in
// a warn log. The UI no longer depends on this endpoint for prefix (it
// hydrates from GET /connections since PR #446), but the endpoint should be
// honest for other API consumers.
func TestGetProviders_ReportsPrefix(t *testing.T) {
	srv := newTestServer()
	srv.credProvider = healthTestStubCredProvider{}
	srv.addonSecretCfg = &providers.AddonSecretProviderConfig{
		Type:   "aws-sm",
		Region: "eu-west-1",
		Prefix: "clusters/",
	}

	req := httptest.NewRequest("GET", "/api/v1/providers", nil)
	w := httptest.NewRecorder()
	srv.handleGetProviders(w, req)

	if w.Code != 200 {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var body struct {
		ConfiguredProvider map[string]interface{} `json:"configured_provider"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.ConfiguredProvider == nil {
		t.Fatal("expected configured_provider in response, got nil")
	}

	if got := body.ConfiguredProvider["prefix"]; got != "clusters/" {
		t.Errorf("expected prefix=%q in response, got %v", "clusters/", got)
	}
	if got := body.ConfiguredProvider["type"]; got != "aws-sm" {
		t.Errorf("expected type=aws-sm, got %v", got)
	}
	if got := body.ConfiguredProvider["region"]; got != "eu-west-1" {
		t.Errorf("expected region=eu-west-1, got %v", got)
	}
	// healthTestStubCredProvider.HealthCheck returns nil → connected.
	if got := body.ConfiguredProvider["status"]; got != "connected" {
		t.Errorf("expected status=connected, got %v", got)
	}
}

// A cluster-test-only install has no addon-secret config, so prefix falls
// back to empty — the key must still be present (empty string), never absent,
// so consumers get a stable shape.
func TestGetProviders_PrefixKeyPresentWhenEmpty(t *testing.T) {
	srv := newTestServer()
	srv.credProvider = healthTestStubCredProvider{}
	srv.clusterTestCfg = &providers.ClusterTestProviderConfig{Type: "k8s-secrets"}

	req := httptest.NewRequest("GET", "/api/v1/providers", nil)
	w := httptest.NewRecorder()
	srv.handleGetProviders(w, req)

	if w.Code != 200 {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var body struct {
		ConfiguredProvider map[string]interface{} `json:"configured_provider"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.ConfiguredProvider == nil {
		t.Fatal("expected configured_provider in response, got nil")
	}

	prefix, present := body.ConfiguredProvider["prefix"]
	if !present {
		t.Fatal("expected prefix key to be present in configured_provider")
	}
	if prefix != "" {
		t.Errorf("expected empty prefix for cluster-test-only install, got %v", prefix)
	}
	if got := body.ConfiguredProvider["type"]; got != "k8s-secrets" {
		t.Errorf("expected type=k8s-secrets (cluster-test fallback), got %v", got)
	}
}
