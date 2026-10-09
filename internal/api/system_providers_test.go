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
	installCredProvider(srv, healthTestStubCredProvider{}, &providers.AddonSecretProviderConfig{
		Type:   "aws-sm",
		Region: "eu-west-1",
		Prefix: "clusters/",
	}, nil)

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
	installCredProvider(srv, healthTestStubCredProvider{}, nil, &providers.ClusterTestProviderConfig{Type: "k8s-secrets"})

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

// V3-P1.1: GET /api/v1/providers must report addon_secret_status when the
// addon-secret backend is missing or invalid (e.g., "argocd" which is rejected
// by NewAddonSecretProvider). The UI uses this to require an explicit addon-
// secret backend choice when the user picks "argocd" for cluster-credentials.
func TestGetProviders_AddonSecretStatus_OK(t *testing.T) {
	srv := newTestServer()
	installCredProvider(srv, healthTestStubCredProvider{}, &providers.AddonSecretProviderConfig{
		Type:   "aws-sm",
		Region: "us-east-1",
	}, nil)

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

	// When addon-secret backend is valid, addon_secret_status is "ok" (or absent
	// — the response omits it when status is ok to reduce noise).
	status, present := body.ConfiguredProvider["addon_secret_status"]
	if present && status != "ok" {
		t.Errorf("expected addon_secret_status absent or ok, got %v", status)
	}
}

func TestGetProviders_AddonSecretStatus_Missing(t *testing.T) {
	srv := newTestServer()
	installCredProvider(srv, healthTestStubCredProvider{}, &providers.AddonSecretProviderConfig{
		Type: "", // no addon-secret backend configured
	}, nil)

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

	status := body.ConfiguredProvider["addon_secret_status"]
	if status != "missing" {
		t.Errorf("expected addon_secret_status=missing when Type is empty, got %v", status)
	}
	message := body.ConfiguredProvider["addon_secret_message"]
	if message == nil || message == "" {
		t.Error("expected non-empty addon_secret_message when status=missing")
	}
}

func TestGetProviders_AddonSecretStatus_InvalidArgoCD(t *testing.T) {
	srv := newTestServer()
	installCredProvider(srv, healthTestStubCredProvider{}, &providers.AddonSecretProviderConfig{
		Type: "argocd", // rejected by NewAddonSecretProvider
	}, nil)

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

	status := body.ConfiguredProvider["addon_secret_status"]
	if status != "invalid_argocd" {
		t.Errorf("expected addon_secret_status=invalid_argocd when Type=argocd, got %v", status)
	}
	message := body.ConfiguredProvider["addon_secret_message"]
	if message == nil || message == "" {
		t.Error("expected non-empty addon_secret_message when status=invalid_argocd")
	}
}
