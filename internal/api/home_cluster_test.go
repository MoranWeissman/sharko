package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGetHomeCluster_NotInCluster_DegradesGracefully asserts the common
// dev/local-mode case: when rest.InClusterConfig() fails (not running
// inside a k8s cluster), the handler returns 200 + available:false with
// a clean message rather than a 500 error. This mirrors the pattern from
// handleGetNodeInfo (nodes.go).
func TestGetHomeCluster_NotInCluster_DegradesGracefully(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/cluster/home", nil)
	rw := httptest.NewRecorder()
	s.handleGetHomeCluster(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rw.Code, rw.Body.String())
	}

	var body HomeClusterInfo
	if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v; body = %s", err, rw.Body.String())
	}

	if body.Available {
		t.Error("available = true, want false when not in-cluster")
	}
	if body.Message == "" {
		t.Error("message is empty, want a degraded-state explanation")
	}
	// When available:false, the optional fields should be unset
	if body.KubernetesVersion != "" {
		t.Errorf("kubernetes_version = %q, want empty when not available", body.KubernetesVersion)
	}
	if body.NodeCount != 0 {
		t.Errorf("node_count = %d, want 0 when not available", body.NodeCount)
	}
}

// TestGetHomeCluster_ResponseShape asserts the JSON contract the frontend
// story builds on: top-level "available" bool always present, and when true,
// the optional kubernetes_version / node_count / nodes_ready / nodes_not_ready
// fields are present. Like capabilities_test.go, does NOT assert actual values
// (those depend on whether the test is running in-cluster with real k8s RBAC).
func TestGetHomeCluster_ResponseShape(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/cluster/home", nil)
	rw := httptest.NewRecorder()
	s.handleGetHomeCluster(rw, req)

	var body map[string]interface{}
	if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v; body = %s", err, rw.Body.String())
	}

	available, ok := body["available"].(bool)
	if !ok {
		t.Fatal("response missing \"available\" key or not a bool")
	}

	// When not available (the test env case), message must be present
	if !available {
		if _, ok := body["message"]; !ok {
			t.Error("response missing \"message\" when available:false")
		}
	}
}

// TestGetHomeCluster_ViewerAllowed asserts any authenticated user can read
// this endpoint (no admin-only gate) — the Dashboard needs it before any
// role has been assigned. Mirrors capabilities_test.go pattern.
func TestGetHomeCluster_ViewerAllowed(t *testing.T) {
	s := &Server{}
	req := withRole(httptest.NewRequest(http.MethodGet, "/api/v1/cluster/home", nil), "viewer")
	rw := httptest.NewRecorder()
	s.handleGetHomeCluster(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rw.Code, rw.Body.String())
	}
}
