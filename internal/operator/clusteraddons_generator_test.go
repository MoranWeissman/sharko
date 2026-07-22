package operator

import (
	"context"
	"errors"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "github.com/MoranWeissman/sharko/api/v1alpha1"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
)

// TestClusterAddonsGenerator_CreateCRs tests that N managed clusters → N CRs
// created with correct spec + managed-by label.
func TestClusterAddonsGenerator_CreateCRs(t *testing.T) {
	ctx := context.Background()

	// Managed-clusters.yaml with 2 clusters, each with 2 addons.
	managedClustersBody := []byte(`apiVersion: sharko.dev/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: prod-eu
      labels:
        datadog: enabled
        external-dns: disabled
    - name: staging-us
      labels:
        prometheus: enabled
`)

	gitReader := func(ctx context.Context, path, branch string) ([]byte, error) {
		return managedClustersBody, nil
	}

	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	gen := NewClusterAddonsGenerator(fakeClient, gitReader, "sharko")
	gen.generateOnce(ctx)

	// Verify CRs were created.
	var list v1alpha1.ClusterAddonsList
	if err := fakeClient.List(ctx, &list, client.InNamespace("sharko")); err != nil {
		t.Fatalf("List CRs: %v", err)
	}

	if len(list.Items) != 2 {
		t.Fatalf("expected 2 CRs, got %d", len(list.Items))
	}

	// Check prod-eu CR.
	var prodEU v1alpha1.ClusterAddons
	if err := fakeClient.Get(ctx, client.ObjectKey{Namespace: "sharko", Name: "prod-eu"}, &prodEU); err != nil {
		t.Fatalf("Get prod-eu CR: %v", err)
	}

	if prodEU.Spec.Cluster != "prod-eu" {
		t.Errorf("prod-eu: spec.cluster = %q, want prod-eu", prodEU.Spec.Cluster)
	}

	if prodEU.Labels[ManagedByLabelKey] != ManagedByLabelValue {
		t.Errorf("prod-eu: managed-by label = %q, want %q", prodEU.Labels[ManagedByLabelKey], ManagedByLabelValue)
	}

	if len(prodEU.Spec.Addons) != 2 {
		t.Fatalf("prod-eu: expected 2 addons, got %d", len(prodEU.Spec.Addons))
	}

	// Verify addon assignments.
	addonsMap := make(map[string]bool)
	for _, addon := range prodEU.Spec.Addons {
		addonsMap[addon.Name] = *addon.Enabled
	}

	if !addonsMap["datadog"] {
		t.Errorf("prod-eu: datadog should be enabled")
	}
	if addonsMap["external-dns"] {
		t.Errorf("prod-eu: external-dns should be disabled")
	}

	// Check staging-us CR.
	var stagingUS v1alpha1.ClusterAddons
	if err := fakeClient.Get(ctx, client.ObjectKey{Namespace: "sharko", Name: "staging-us"}, &stagingUS); err != nil {
		t.Fatalf("Get staging-us CR: %v", err)
	}

	if stagingUS.Spec.Cluster != "staging-us" {
		t.Errorf("staging-us: spec.cluster = %q, want staging-us", stagingUS.Spec.Cluster)
	}

	if len(stagingUS.Spec.Addons) != 1 {
		t.Fatalf("staging-us: expected 1 addon, got %d", len(stagingUS.Spec.Addons))
	}

	if stagingUS.Spec.Addons[0].Name != "prometheus" || !*stagingUS.Spec.Addons[0].Enabled {
		t.Errorf("staging-us: expected prometheus enabled, got %+v", stagingUS.Spec.Addons[0])
	}
}

// TestClusterAddonsGenerator_PruneOrphans tests that removing a cluster from
// managed-clusters.yaml prunes ONLY its sharko-owned CR (not un-owned CRs).
func TestClusterAddonsGenerator_PruneOrphans(t *testing.T) {
	ctx := context.Background()

	// Initial state: 2 clusters.
	initialBody := []byte(`apiVersion: sharko.dev/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: prod-eu
      labels:
        datadog: enabled
    - name: staging-us
      labels:
        prometheus: enabled
`)

	// After removal: only prod-eu.
	afterRemovalBody := []byte(`apiVersion: sharko.dev/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: prod-eu
      labels:
        datadog: enabled
`)

	bodySource := initialBody
	gitReader := func(ctx context.Context, path, branch string) ([]byte, error) {
		return bodySource, nil
	}

	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	gen := NewClusterAddonsGenerator(fakeClient, gitReader, "sharko")

	// Generate initial CRs.
	gen.generateOnce(ctx)

	var list v1alpha1.ClusterAddonsList
	if err := fakeClient.List(ctx, &list, client.InNamespace("sharko")); err != nil {
		t.Fatalf("List initial CRs: %v", err)
	}
	if len(list.Items) != 2 {
		t.Fatalf("initial: expected 2 CRs, got %d", len(list.Items))
	}

	// Now remove staging-us from managed-clusters.yaml.
	bodySource = afterRemovalBody
	gen.generateOnce(ctx)

	// Verify staging-us CR was pruned.
	if err := fakeClient.List(ctx, &list, client.InNamespace("sharko")); err != nil {
		t.Fatalf("List after prune: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("after prune: expected 1 CR, got %d", len(list.Items))
	}

	if list.Items[0].Name != "prod-eu" {
		t.Errorf("after prune: remaining CR name = %q, want prod-eu", list.Items[0].Name)
	}
}

// TestClusterAddonsGenerator_NeverPruneUnownedCRs tests that a fake un-owned
// CR (no managed-by label) is NEVER pruned.
func TestClusterAddonsGenerator_NeverPruneUnownedCRs(t *testing.T) {
	ctx := context.Background()

	// Managed-clusters.yaml with 1 cluster.
	managedClustersBody := []byte(`apiVersion: sharko.dev/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: prod-eu
      labels:
        datadog: enabled
`)

	gitReader := func(ctx context.Context, path, branch string) ([]byte, error) {
		return managedClustersBody, nil
	}

	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	// Pre-create a hand-written CR (no managed-by label).
	handWrittenCR := &v1alpha1.ClusterAddons{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hand-written-cluster",
			Namespace: "sharko",
			// No managed-by label — user's CR.
		},
		Spec: v1alpha1.ClusterAddonsSpec{
			Cluster: "hand-written-cluster",
		},
	}
	if err := fakeClient.Create(ctx, handWrittenCR); err != nil {
		t.Fatalf("Create hand-written CR: %v", err)
	}

	gen := NewClusterAddonsGenerator(fakeClient, gitReader, "sharko")
	gen.generateOnce(ctx)

	// Verify both CRs exist: prod-eu (sharko-owned) + hand-written-cluster (unowned).
	var list v1alpha1.ClusterAddonsList
	if err := fakeClient.List(ctx, &list, client.InNamespace("sharko")); err != nil {
		t.Fatalf("List CRs: %v", err)
	}

	if len(list.Items) != 2 {
		t.Fatalf("expected 2 CRs (1 sharko-owned + 1 hand-written), got %d", len(list.Items))
	}

	// Find the hand-written CR and verify it's untouched.
	var found bool
	for _, cr := range list.Items {
		if cr.Name == "hand-written-cluster" {
			found = true
			if cr.Labels[ManagedByLabelKey] == ManagedByLabelValue {
				t.Errorf("hand-written CR should NOT have managed-by=sharko label")
			}
		}
	}

	if !found {
		t.Errorf("hand-written CR was pruned (should never happen)")
	}
}

// TestClusterAddonsGenerator_SpecOnlyNoStatus tests that the generator writes
// spec but not status (status stays zero-value).
func TestClusterAddonsGenerator_SpecOnlyNoStatus(t *testing.T) {
	ctx := context.Background()

	managedClustersBody := []byte(`apiVersion: sharko.dev/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: prod-eu
      labels:
        datadog: enabled
`)

	gitReader := func(ctx context.Context, path, branch string) ([]byte, error) {
		return managedClustersBody, nil
	}

	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	gen := NewClusterAddonsGenerator(fakeClient, gitReader, "sharko")
	gen.generateOnce(ctx)

	var cr v1alpha1.ClusterAddons
	if err := fakeClient.Get(ctx, client.ObjectKey{Namespace: "sharko", Name: "prod-eu"}, &cr); err != nil {
		t.Fatalf("Get prod-eu CR: %v", err)
	}

	// Verify status fields are zero-value (never written by generator).
	if cr.Status.ObservedGeneration != 0 {
		t.Errorf("status.observedGeneration = %d, want 0 (generator should not write status)", cr.Status.ObservedGeneration)
	}

	if cr.Status.SyncedAddons != 0 {
		t.Errorf("status.syncedAddons = %d, want 0", cr.Status.SyncedAddons)
	}

	if cr.Status.LastReconcileTime != nil {
		t.Errorf("status.lastReconcileTime should be nil, got %v", cr.Status.LastReconcileTime)
	}

	if len(cr.Status.Conditions) != 0 {
		t.Errorf("status.conditions should be empty, got %d conditions", len(cr.Status.Conditions))
	}
}

// TestClusterAddonsGenerator_Idempotent tests that running the generator twice
// with the same input produces no changes (idempotent).
func TestClusterAddonsGenerator_Idempotent(t *testing.T) {
	ctx := context.Background()

	managedClustersBody := []byte(`apiVersion: sharko.dev/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: prod-eu
      labels:
        datadog: enabled
`)

	gitReader := func(ctx context.Context, path, branch string) ([]byte, error) {
		return managedClustersBody, nil
	}

	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	gen := NewClusterAddonsGenerator(fakeClient, gitReader, "sharko")

	// First run — create CR.
	gen.generateOnce(ctx)

	var cr1 v1alpha1.ClusterAddons
	if err := fakeClient.Get(ctx, client.ObjectKey{Namespace: "sharko", Name: "prod-eu"}, &cr1); err != nil {
		t.Fatalf("Get prod-eu after first run: %v", err)
	}

	generation1 := cr1.Generation

	// Second run — should be a no-op (no changes).
	gen.generateOnce(ctx)

	var cr2 v1alpha1.ClusterAddons
	if err := fakeClient.Get(ctx, client.ObjectKey{Namespace: "sharko", Name: "prod-eu"}, &cr2); err != nil {
		t.Fatalf("Get prod-eu after second run: %v", err)
	}

	generation2 := cr2.Generation

	if generation2 != generation1 {
		t.Errorf("generation changed from %d to %d (should be idempotent, no changes)", generation1, generation2)
	}
}

// TestClusterAddonsGenerator_FileNotFound tests that a missing
// managed-clusters.yaml is treated as "empty desired state" (prune all
// sharko-owned CRs).
func TestClusterAddonsGenerator_FileNotFound(t *testing.T) {
	ctx := context.Background()

	gitReader := func(ctx context.Context, path, branch string) ([]byte, error) {
		return nil, gitprovider.ErrFileNotFound
	}

	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	// Pre-create a sharko-owned CR.
	existingCR := &v1alpha1.ClusterAddons{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "prod-eu",
			Namespace: "sharko",
			Labels: map[string]string{
				ManagedByLabelKey: ManagedByLabelValue,
			},
		},
		Spec: v1alpha1.ClusterAddonsSpec{
			Cluster: "prod-eu",
		},
	}
	if err := fakeClient.Create(ctx, existingCR); err != nil {
		t.Fatalf("Create existing CR: %v", err)
	}

	gen := NewClusterAddonsGenerator(fakeClient, gitReader, "sharko")
	gen.generateOnce(ctx)

	// Verify the CR was pruned (empty desired state).
	var list v1alpha1.ClusterAddonsList
	if err := fakeClient.List(ctx, &list, client.InNamespace("sharko")); err != nil {
		t.Fatalf("List CRs: %v", err)
	}

	if len(list.Items) != 0 {
		t.Errorf("expected 0 CRs (file not found = empty desired state), got %d", len(list.Items))
	}
}

// TestClusterAddonsGenerator_GitReadError tests that a git read error (other
// than ErrFileNotFound) is logged and generation is skipped (no CRs created).
func TestClusterAddonsGenerator_GitReadError(t *testing.T) {
	ctx := context.Background()

	gitReader := func(ctx context.Context, path, branch string) ([]byte, error) {
		return nil, errors.New("git connection lost")
	}

	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	gen := NewClusterAddonsGenerator(fakeClient, gitReader, "sharko")
	gen.generateOnce(ctx)

	// Verify no CRs were created (error path skips generation).
	var list v1alpha1.ClusterAddonsList
	if err := fakeClient.List(ctx, &list, client.InNamespace("sharko")); err != nil {
		t.Fatalf("List CRs: %v", err)
	}

	if len(list.Items) != 0 {
		t.Errorf("expected 0 CRs (git error should skip generation), got %d", len(list.Items))
	}
}
