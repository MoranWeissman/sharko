package orchestrator

// V2-cleanup-44: UpdateClusterAddons must write managed-clusters.yaml in the
// same PR as the per-cluster values file so that disabling/toggling an addon
// actually sticks. The cluster reconciler reads managed-clusters.yaml as the
// source of truth; a values-only write caused the reconciler to re-enable any
// disabled addon on the next cycle.
//
// Tests here pin:
//   - disable: managed-clusters.yaml is in the PR files, label is "disabled"
//   - enable:  managed-clusters.yaml is in the PR files, label is "enabled"
//   - multi-cluster safety: only the target cluster's entry is mutated
//   - no-half-write: missing managed-clusters.yaml → op fails before any PR

import (
	"context"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

// managedClustersWithKedaEnabled is a bare-YAML managed-clusters.yaml with
// keda enabled on "test" and an unrelated "other-cluster" entry.
const managedClustersWithKedaEnabled = `clusters:
  - name: test
    labels:
      keda: enabled
  - name: other-cluster
    labels:
      keda: enabled
`

// managedClustersWithFooDisabled is a bare-YAML managed-clusters.yaml with
// the "foo" addon absent (unlabeled) on "test" and an unrelated "other" entry.
const managedClustersWithFooAbsent = `clusters:
  - name: test
    labels: {}
  - name: other-cluster
    labels:
      foo: disabled
`

// managedClustersKedaAndFoo has both keda and foo enabled on "test".
const managedClustersKedaAndFoo = `clusters:
  - name: test
    labels:
      keda: enabled
      foo: enabled
`

// setupUpdateClusterAddonsOrch creates a minimal orchestrator wired to the given
// git mock (no cred provider so secrets steps are skipped, keeping tests focused).
func setupUpdateClusterAddonsOrch(git *mockGitProvider) *Orchestrator {
	paths := defaultPaths()
	// managed-clusters path lives in defaultPaths' ManagedClusters field (empty
	// string → falls back to the literal in cluster.go).
	return New(nil, nil, newMockArgocd(), git, autoMergeGitOps(), paths, nil)
}

// TestUpdateClusterAddons_Disable_WritesManagedClusters pins the primary bug:
// disabling keda via UpdateClusterAddons must include managed-clusters.yaml in
// the committed files with keda set to the canonical "disabled" label value.
func TestUpdateClusterAddons_Disable_WritesManagedClusters(t *testing.T) {
	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte(managedClustersWithKedaEnabled)
	git.files["configuration/addons-clusters-values/test.yaml"] = []byte("clusterGlobalValues:\n")

	orch := setupUpdateClusterAddonsOrch(git)

	result, err := orch.UpdateClusterAddons(context.Background(), "test", "https://k8s.example.com", "", map[string]bool{"keda": false}, nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected success status, got %q (err: %s)", result.Status, result.Error)
	}

	// Values file must be present.
	valuesPath := "configuration/addons-clusters-values/test.yaml"
	if _, ok := git.files[valuesPath]; !ok {
		t.Error("values file not written to git")
	}

	// managed-clusters.yaml MUST be present — this is the bug we are fixing.
	mcPath := "configuration/managed-clusters.yaml"
	mcBytes, ok := git.files[mcPath]
	if !ok {
		t.Fatal("managed-clusters.yaml not written to git (V2-cleanup-44: disable does not stick without this)")
	}

	// Parse the written file and assert keda is disabled on "test".
	spec, parseErr := models.LoadManagedClusters(mcBytes)
	if parseErr != nil {
		t.Fatalf("could not parse written managed-clusters.yaml: %v\n%s", parseErr, mcBytes)
	}
	var testEntry *models.ManagedClusterEntry
	for i := range spec.Clusters {
		if spec.Clusters[i].Name == "test" {
			testEntry = &spec.Clusters[i]
			break
		}
	}
	if testEntry == nil {
		t.Fatal("cluster 'test' not found in written managed-clusters.yaml")
	}
	if testEntry.Labels["keda"] != models.LabelDisabled {
		t.Errorf("expected keda label = %q, got %q", models.LabelDisabled, testEntry.Labels["keda"])
	}
}

// TestUpdateClusterAddons_Enable_WritesManagedClusters pins the enable path:
// enabling an addon must also write managed-clusters.yaml with the canonical
// "enabled" label value for that addon.
func TestUpdateClusterAddons_Enable_WritesManagedClusters(t *testing.T) {
	git := newMockGitProvider()
	// "foo" is absent from test's labels — enabling it should add the label.
	git.files["configuration/managed-clusters.yaml"] = []byte(managedClustersWithFooAbsent)
	git.files["configuration/addons-clusters-values/test.yaml"] = []byte("clusterGlobalValues:\n")
	// Seed "foo" in the catalog so the referential-integrity guard passes.
	git.files["configuration/addons-catalog.yaml"] = []byte(`applicationsets:
  - name: foo
    chart: foo
    repoURL: https://example.com
    version: 1.0.0
  - name: keda
    chart: keda
    repoURL: https://example.com
    version: 1.0.0
  - name: cert-manager
    chart: cert-manager
    repoURL: https://charts.jetstack.io
    version: 1.0.0
  - name: metrics-server
    chart: metrics-server
    repoURL: https://example.com
    version: 1.0.0
  - name: datadog
    chart: datadog
    repoURL: https://example.com
    version: 1.0.0
  - name: logging
    chart: logging
    repoURL: https://example.com
    version: 1.0.0
  - name: monitoring
    chart: kube-prometheus-stack
    repoURL: https://example.com
    version: 1.0.0
`)

	orch := setupUpdateClusterAddonsOrch(git)

	result, err := orch.UpdateClusterAddons(context.Background(), "test", "https://k8s.example.com", "", map[string]bool{"foo": true}, nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected success status, got %q (err: %s)", result.Status, result.Error)
	}

	mcPath := "configuration/managed-clusters.yaml"
	mcBytes, ok := git.files[mcPath]
	if !ok {
		t.Fatal("managed-clusters.yaml not written to git (enable path must also write it)")
	}

	spec, parseErr := models.LoadManagedClusters(mcBytes)
	if parseErr != nil {
		t.Fatalf("could not parse written managed-clusters.yaml: %v\n%s", parseErr, mcBytes)
	}
	var testEntry *models.ManagedClusterEntry
	for i := range spec.Clusters {
		if spec.Clusters[i].Name == "test" {
			testEntry = &spec.Clusters[i]
			break
		}
	}
	if testEntry == nil {
		t.Fatal("cluster 'test' not found in written managed-clusters.yaml")
	}
	if testEntry.Labels["foo"] != models.LabelEnabled {
		t.Errorf("expected foo label = %q, got %q", models.LabelEnabled, testEntry.Labels["foo"])
	}
}

// TestUpdateClusterAddons_MultiCluster_OtherClusterUntouched pins isolation:
// when the request touches only cluster "test", the "other-cluster" entry in
// managed-clusters.yaml must remain exactly as it was.
func TestUpdateClusterAddons_MultiCluster_OtherClusterUntouched(t *testing.T) {
	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte(managedClustersWithKedaEnabled)
	git.files["configuration/addons-clusters-values/test.yaml"] = []byte("clusterGlobalValues:\n")

	orch := setupUpdateClusterAddonsOrch(git)

	_, err := orch.UpdateClusterAddons(context.Background(), "test", "https://k8s.example.com", "", map[string]bool{"keda": false}, nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mcBytes, ok := git.files["configuration/managed-clusters.yaml"]
	if !ok {
		t.Fatal("managed-clusters.yaml not written to git")
	}

	spec, parseErr := models.LoadManagedClusters(mcBytes)
	if parseErr != nil {
		t.Fatalf("could not parse written managed-clusters.yaml: %v", parseErr)
	}

	// "test" should have keda disabled.
	var testEntry, otherEntry *models.ManagedClusterEntry
	for i := range spec.Clusters {
		switch spec.Clusters[i].Name {
		case "test":
			testEntry = &spec.Clusters[i]
		case "other-cluster":
			otherEntry = &spec.Clusters[i]
		}
	}
	if testEntry == nil {
		t.Fatal("cluster 'test' not found after write")
	}
	if otherEntry == nil {
		t.Fatal("cluster 'other-cluster' not found after write — it should be preserved")
	}
	if testEntry.Labels["keda"] != models.LabelDisabled {
		t.Errorf("cluster test: expected keda=%q, got %q", models.LabelDisabled, testEntry.Labels["keda"])
	}
	// "other-cluster" must still have keda=enabled — only "test" was in the request.
	if otherEntry.Labels["keda"] != models.LabelEnabled {
		t.Errorf("cluster other-cluster: expected keda=%q (untouched), got %q", models.LabelEnabled, otherEntry.Labels["keda"])
	}
}

// TestUpdateClusterAddons_MissingManagedClusters_FailsBeforePR pins the
// no-half-write guard: if managed-clusters.yaml is missing (or unreadable),
// the operation must fail BEFORE opening a PR so no values-only commit lands.
func TestUpdateClusterAddons_MissingManagedClusters_FailsBeforePR(t *testing.T) {
	git := newMockGitProvider()
	// Seed the values file but NOT managed-clusters.yaml.
	git.files["configuration/addons-clusters-values/test.yaml"] = []byte("clusterGlobalValues:\n")
	// Remove the default catalog so ManagedClusters check fires (but catalog is
	// checked after referential integrity — keep keda in catalog so we get past
	// that guard and hit the managed-clusters guard).
	// defaultTestCatalogYAML already includes keda, so newMockGitProvider is fine.

	orch := setupUpdateClusterAddonsOrch(git)

	result, err := orch.UpdateClusterAddons(context.Background(), "test", "https://k8s.example.com", "", map[string]bool{"keda": false}, nil, false)
	if err != nil {
		t.Fatalf("unexpected hard error (should return a result with failed status): %v", err)
	}

	if result.Status != "failed" {
		t.Errorf("expected status 'failed' when managed-clusters.yaml is missing, got %q", result.Status)
	}
	if result.FailedStep != "update_addon_label" {
		t.Errorf("expected failed step 'update_addon_label', got %q", result.FailedStep)
	}

	// No PR must have been opened (no branches created = no PR attempt).
	if len(git.branches) != 0 {
		t.Errorf("no-half-write violated: a branch was created (%v) even though managed-clusters.yaml was missing", git.branches)
	}
	if len(git.prs) != 0 {
		t.Errorf("no-half-write violated: a PR was opened (%v) even though managed-clusters.yaml was missing", git.prs)
	}
}

// TestUpdateClusterAddons_EmptyAddonsMap_NoManagedClustersWrite pins that
// an empty addons map skips the managed-clusters.yaml write entirely
// (no mutation needed, consistent with the existing empty-map behaviour).
func TestUpdateClusterAddons_EmptyAddonsMap_NoManagedClustersWrite(t *testing.T) {
	git := newMockGitProvider()
	initialMC := "clusters:\n  - name: test\n    labels:\n      keda: enabled\n"
	git.files["configuration/managed-clusters.yaml"] = []byte(initialMC)
	git.files["configuration/addons-clusters-values/test.yaml"] = []byte("clusterGlobalValues:\n")

	orch := setupUpdateClusterAddonsOrch(git)

	result, err := orch.UpdateClusterAddons(context.Background(), "test", "https://k8s.example.com", "", map[string]bool{}, nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected success, got %q", result.Status)
	}

	// managed-clusters.yaml should be unchanged (empty addons map → no mutations).
	if string(git.files["configuration/managed-clusters.yaml"]) != initialMC {
		t.Error("managed-clusters.yaml was mutated for an empty addons map — it should be untouched")
	}
}

// TestUpdateClusterAddons_ValuesFileAlwaysWritten pins that the values file
// is always committed, even when managed-clusters.yaml is also written.
func TestUpdateClusterAddons_ValuesFileAlwaysWritten(t *testing.T) {
	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte(managedClustersKedaAndFoo)
	git.files["configuration/addons-clusters-values/test.yaml"] = []byte("clusterGlobalValues:\n")

	orch := setupUpdateClusterAddonsOrch(git)

	result, err := orch.UpdateClusterAddons(context.Background(), "test", "https://k8s.example.com", "", map[string]bool{"keda": false}, nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected success, got %q (err: %s)", result.Status, result.Error)
	}

	valuesPath := "configuration/addons-clusters-values/test.yaml"
	if _, ok := git.files[valuesPath]; !ok {
		t.Error("values file not written to git")
	}
	// managed-clusters.yaml must also be present.
	if _, ok := git.files["configuration/managed-clusters.yaml"]; !ok {
		t.Error("managed-clusters.yaml not written to git")
	}
	// Verify the managed-clusters.yaml label content reflects the disable.
	mcBytes := git.files["configuration/managed-clusters.yaml"]
	if !strings.Contains(string(mcBytes), "disabled") {
		t.Errorf("managed-clusters.yaml does not contain 'disabled' label value:\n%s", mcBytes)
	}
}
