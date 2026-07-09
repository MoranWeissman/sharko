package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// --- handleGetSystemCapabilities (read-tier — any authenticated user) ---
//
// GET /api/v1/system/capabilities has NO authz.RequireWithResponse call
// (unlike the admin-only PUT /settings/probe-mode), so a viewer must be
// able to reach it — the register-cluster screen needs this data before
// the user has been assigned any elevated role. This mirrors the pattern
// authz_unguarded_endpoints_test.go uses for the write-side tier checks,
// applied to the read side instead.

func TestGetSystemCapabilities_ViewerAllowed(t *testing.T) {
	s := &Server{}
	req := withRole(httptest.NewRequest(http.MethodGet, "/api/v1/system/capabilities", nil), "viewer")
	rw := httptest.NewRecorder()
	s.handleGetSystemCapabilities(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rw.Code, rw.Body.String())
	}
}

func TestGetSystemCapabilities_NoRoleHeaderAllowed(t *testing.T) {
	// "auth not configured" mode (no X-Sharko-User/-Role at all) must also
	// succeed — this endpoint performs no role check of its own.
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/capabilities", nil)
	rw := httptest.NewRecorder()
	s.handleGetSystemCapabilities(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rw.Code, rw.Body.String())
	}
}

// TestGetSystemCapabilities_ResponseShape asserts the exact JSON contract
// the frontend story builds on: a top-level "aws" object (with "detected"
// and "method" keys always present) and a top-level "hub_platform" string.
// Deliberately does NOT assert Detected/Method/IdentityARN values — those
// depend on the ambient environment go test runs in (see
// internal/capabilities/aws_test.go for the fully-mocked detection-logic
// coverage) and asserting them here would make this test either flaky or,
// worse, encode whatever AWS identity happens to be on the machine running
// the suite.
func TestGetSystemCapabilities_ResponseShape(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/capabilities", nil)
	rw := httptest.NewRecorder()
	s.handleGetSystemCapabilities(rw, req)

	var body map[string]json.RawMessage
	if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v; body = %s", err, rw.Body.String())
	}

	awsRaw, ok := body["aws"]
	if !ok {
		t.Fatal("response missing top-level \"aws\" key")
	}
	var aws map[string]json.RawMessage
	if err := json.Unmarshal(awsRaw, &aws); err != nil {
		t.Fatalf("decode aws object: %v", err)
	}
	if _, ok := aws["detected"]; !ok {
		t.Error("aws object missing \"detected\" key")
	}
	if _, ok := aws["method"]; !ok {
		t.Error("aws object missing \"method\" key")
	}

	hubRaw, ok := body["hub_platform"]
	if !ok {
		t.Fatal("response missing top-level \"hub_platform\" key")
	}
	var hub string
	if err := json.Unmarshal(hubRaw, &hub); err != nil {
		t.Fatalf("decode hub_platform: %v", err)
	}
	if hub != "eks" && hub != "unknown" {
		t.Errorf("hub_platform = %q, want \"eks\" or \"unknown\"", hub)
	}
}

// TestGetAWSDetector_CachedAcrossCalls asserts the Server-level lazy-init
// returns the SAME detector instance on repeated calls — the point being
// that the detector (and therefore its own internal sts:GetCallerIdentity
// cache) is a per-server singleton, not rebuilt per request.
func TestGetAWSDetector_CachedAcrossCalls(t *testing.T) {
	s := &Server{}
	d1 := s.getAWSDetector()
	d2 := s.getAWSDetector()
	if d1 != d2 {
		t.Error("getAWSDetector returned different instances across calls, want the same cached instance")
	}
}

func TestGetHubPlatformDetector_CachedAcrossCalls(t *testing.T) {
	s := &Server{}
	d1 := s.getHubPlatformDetector()
	d2 := s.getHubPlatformDetector()
	if d1 != d2 {
		t.Error("getHubPlatformDetector returned different instances across calls, want the same cached instance")
	}
}

// TestHubKubeVersion_NoK8sClient_DegradesGracefully covers the common
// dev/local-mode and test-server case: no in-cluster k8s client wired via
// argoReconcilerConfig, so hubKubeVersion must return an error (never
// panic, never block) and the caller (HubPlatformDetector) must fall back
// to "unknown".
func TestHubKubeVersion_NoK8sClient_DegradesGracefully(t *testing.T) {
	s := &Server{}
	version, err := s.hubKubeVersion(context.Background())
	if err == nil {
		t.Fatal("expected an error with no in-cluster k8s client wired, got nil")
	}
	if version != "" {
		t.Errorf("version = %q, want empty on error", version)
	}

	got := s.getHubPlatformDetector().Detect(context.Background())
	if got != "unknown" {
		t.Errorf("Detect() = %q, want \"unknown\"", got)
	}
}
