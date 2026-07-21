package v1alpha1

import (
	"encoding/json"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestClusterAddonsMarshaling verifies that the ClusterAddons type can be
// marshaled to JSON and unmarshaled back without loss.
func TestClusterAddonsMarshaling(t *testing.T) {
	enabled := true
	disabled := false

	original := &ClusterAddons{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "sharko.dev/v1alpha1",
			Kind:       "ClusterAddons",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "prod-eu",
			Namespace: "sharko",
		},
		Spec: ClusterAddonsSpec{
			Cluster: "prod-eu",
			Addons: []AddonAssignment{
				{
					Name:    "cert-manager",
					Version: "v1.13.0",
					Enabled: &enabled,
				},
				{
					Name:    "ingress-nginx",
					Enabled: &disabled,
				},
			},
		},
		Status: ClusterAddonsStatus{
			ObservedGeneration: 1,
			SyncedAddons:       1,
		},
	}

	// Marshal to JSON
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal ClusterAddons: %v", err)
	}

	// Unmarshal back
	var restored ClusterAddons
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("failed to unmarshal ClusterAddons: %v", err)
	}

	// Verify key fields
	if restored.Spec.Cluster != "prod-eu" {
		t.Errorf("expected cluster 'prod-eu', got %q", restored.Spec.Cluster)
	}
	if len(restored.Spec.Addons) != 2 {
		t.Errorf("expected 2 addons, got %d", len(restored.Spec.Addons))
	}
	if restored.Spec.Addons[0].Name != "cert-manager" {
		t.Errorf("expected addon 'cert-manager', got %q", restored.Spec.Addons[0].Name)
	}
	if restored.Spec.Addons[0].Enabled == nil || !*restored.Spec.Addons[0].Enabled {
		t.Errorf("expected addon 'cert-manager' to be enabled")
	}
	if restored.Spec.Addons[1].Enabled == nil || *restored.Spec.Addons[1].Enabled {
		t.Errorf("expected addon 'ingress-nginx' to be disabled")
	}
}

// TestDeepCopy verifies that the generated DeepCopy methods work correctly.
func TestDeepCopy(t *testing.T) {
	enabled := true
	original := &ClusterAddons{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "sharko",
		},
		Spec: ClusterAddonsSpec{
			Cluster: "test-cluster",
			Addons: []AddonAssignment{
				{
					Name:    "addon-1",
					Enabled: &enabled,
				},
			},
		},
	}

	// Deep copy
	copied := original.DeepCopy()

	// Modify the copy
	copied.Spec.Cluster = "modified-cluster"
	*copied.Spec.Addons[0].Enabled = false

	// Verify original is unchanged
	if original.Spec.Cluster != "test-cluster" {
		t.Errorf("original cluster was modified: got %q", original.Spec.Cluster)
	}
	if !*original.Spec.Addons[0].Enabled {
		t.Errorf("original addon enabled flag was modified")
	}
}
