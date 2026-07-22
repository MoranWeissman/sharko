package operator

import (
	"testing"

	"k8s.io/client-go/rest"

	v1alpha1 "github.com/MoranWeissman/sharko/api/v1alpha1"
)

// TestNewManager verifies that the manager constructor:
// 1. Successfully creates a manager with a fake config
// 2. Registers the required schemes (client-go + v1alpha1)
// 3. Does NOT require a live apiserver (this is a pure construction test)
//
// This is Story 1.2 — the manager is created but has NO reconcilers yet.
func TestNewManager(t *testing.T) {
	// Minimal fake REST config — no real cluster needed for this test
	cfg := &rest.Config{
		Host: "https://fake-apiserver:6443",
	}

	namespace := "test-namespace"

	mgr, err := NewManager(cfg, namespace)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	if mgr == nil {
		t.Fatal("NewManager returned nil manager with no error")
	}

	// Verify that the v1alpha1 ClusterAddons type is registered in the scheme
	scheme := mgr.GetScheme()
	if scheme == nil {
		t.Fatal("manager scheme is nil")
	}

	// Check that the ClusterAddons type is known to the scheme
	gvk := v1alpha1.GroupVersion.WithKind("ClusterAddons")
	if !scheme.Recognizes(gvk) {
		t.Errorf("scheme does not recognize ClusterAddons type (gvk: %v)", gvk)
	}

	// Check that the ClusterAddonsList type is also registered
	listGVK := v1alpha1.GroupVersion.WithKind("ClusterAddonsList")
	if !scheme.Recognizes(listGVK) {
		t.Errorf("scheme does not recognize ClusterAddonsList type (gvk: %v)", listGVK)
	}
}
