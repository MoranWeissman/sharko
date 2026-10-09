package orchestrator

import (
	"context"
	"strings"
	"testing"
)

// V2-cleanup-22, Part 2 — referential integrity: EnableAddon /
// RegisterCluster / SetClusterAddonValues reject an addon that is not in the
// catalog (clear *AddonNotInCatalogError → 4xx at the API edge), and stop
// swallowing the catalog-read error.

// catalogOnlyYAML returns a catalog seed containing exactly the given addons.
func catalogOnlyYAML(addons ...string) []byte {
	var b strings.Builder
	b.WriteString("applicationsets:\n")
	for _, a := range addons {
		b.WriteString("  - name: " + a + "\n")
		b.WriteString("    chart: " + a + "\n")
		b.WriteString("    repoURL: https://example.com\n")
		b.WriteString("    version: 1.0.0\n")
	}
	return []byte(b.String())
}

// TestEnableAddon_NotInCatalog_Rejected: enabling an addon absent from the
// catalog returns *AddonNotInCatalogError, writes no label, and opens no PR.
func TestEnableAddon_NotInCatalog_Rejected(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	// Catalog has cert-manager only; we try to enable "ghost".
	git.files["configuration/addons-catalog.yaml"] = catalogOnlyYAML("cert-manager")
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters:\n  - name: prod-eu\n    labels: {}\n")
	git.files["configuration/addons-clusters-values/prod-eu.yaml"] = []byte("clusterGlobalValues:\n")

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	_, err := orch.EnableAddon(context.Background(), EnableAddonRequest{
		Cluster: "prod-eu",
		Addon:   "ghost",
		Yes:     true,
	})
	if err == nil {
		t.Fatal("expected rejection enabling an addon not in the catalog, got nil")
	}
	if !IsAddonNotInCatalog(err) {
		t.Fatalf("expected *AddonNotInCatalogError, got: %v", err)
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should name the bad addon, got: %v", err)
	}
	if len(git.prs) != 0 {
		t.Errorf("expected no PR on referential-integrity rejection, got %d", len(git.prs))
	}
}

// TestEnableAddon_CatalogReadError_Surfaced: a genuine catalog read failure
// is no longer swallowed — it surfaces as a plain (non-AddonNotInCatalog)
// error so the API maps it to 502, not a 4xx.
func TestEnableAddon_CatalogReadError_Surfaced(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	// Remove the seeded catalog so the read fails ("file not found").
	delete(git.files, "configuration/addons-catalog.yaml")
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters:\n  - name: prod-eu\n    labels: {}\n")
	git.files["configuration/addons-clusters-values/prod-eu.yaml"] = []byte("clusterGlobalValues:\n")

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	_, err := orch.EnableAddon(context.Background(), EnableAddonRequest{
		Cluster: "prod-eu",
		Addon:   "cert-manager",
		Yes:     true,
	})
	if err == nil {
		t.Fatal("expected the catalog read error to surface, got nil (still swallowed?)")
	}
	if IsAddonNotInCatalog(err) {
		t.Fatalf("read failure must NOT be classified as AddonNotInCatalog: %v", err)
	}
	if !strings.Contains(err.Error(), "catalog") {
		t.Errorf("expected a catalog-read error, got: %v", err)
	}
}

// TestRegisterCluster_BadAddon_RejectsWholeRequest: registering with one bad
// addon name rejects the entire request and names the bad addon.
func TestRegisterCluster_BadAddon_RejectsWholeRequest(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	git.files["configuration/addons-catalog.yaml"] = catalogOnlyYAML("cert-manager", "metrics-server")

	orch := New(nil, defaultCreds(), argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	_, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:   "prod-eu",
		Region: "eu-west-1",
		Addons: map[string]bool{"cert-manager": true, "ghost": true},
	})
	if err == nil {
		t.Fatal("expected rejection registering with a bad addon, got nil")
	}
	if !IsAddonNotInCatalog(err) {
		t.Fatalf("expected *AddonNotInCatalogError, got: %v", err)
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should name the bad addon, got: %v", err)
	}
	if len(git.prs) != 0 {
		t.Errorf("expected no PR — whole request rejected, got %d PRs", len(git.prs))
	}
}

// TestRegisterCluster_NoAddons_SkipsCatalog: a bare registration with no
// addons does not depend on the catalog being readable.
func TestRegisterCluster_NoAddons_SkipsCatalog(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	delete(git.files, "configuration/addons-catalog.yaml") // catalog unreadable

	orch := New(nil, defaultCreds(), argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:   "prod-eu",
		Region: "eu-west-1",
		// no Addons
	})
	if err != nil {
		t.Fatalf("bare registration should not require a readable catalog: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected success, got %q (%s)", result.Status, result.Error)
	}
}

// TestSetClusterAddonValues_NotInCatalog_Rejected: writing per-cluster values
// for an addon not in the catalog is rejected.
func TestSetClusterAddonValues_NotInCatalog_Rejected(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	git.files["configuration/addons-catalog.yaml"] = catalogOnlyYAML("cert-manager")

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	_, err := orch.SetClusterAddonValues(context.Background(), "prod-eu", "ghost", "replicaCount: 2\n", nil, false)
	if err == nil {
		t.Fatal("expected rejection setting values for an addon not in the catalog, got nil")
	}
	if !IsAddonNotInCatalog(err) {
		t.Fatalf("expected *AddonNotInCatalogError, got: %v", err)
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should name the bad addon, got: %v", err)
	}
	if len(git.prs) != 0 {
		t.Errorf("expected no PR on rejection, got %d", len(git.prs))
	}
}

// TestSetClusterAddonValues_RemoveAllowedForNonCatalogAddon: clearing values
// (empty payload) for an addon that already left the catalog must stay
// allowed as cleanup.
func TestSetClusterAddonValues_RemoveAllowedForNonCatalogAddon(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	git.files["configuration/addons-catalog.yaml"] = catalogOnlyYAML("cert-manager")
	git.files["configuration/addons-clusters-values/prod-eu.yaml"] = []byte("ghost:\n  replicaCount: 2\n")

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	_, err := orch.SetClusterAddonValues(context.Background(), "prod-eu", "ghost", "", nil, false)
	if err != nil {
		t.Fatalf("removing overrides for a non-catalog addon should be allowed (cleanup): %v", err)
	}
}

// TestGuardrails_FullFlow_Regression is the top-risk guard (risk #1): a normal
// end-to-end flow — register a cluster with a REAL addon, enable it, then edit
// its per-cluster values — STILL succeeds after all the V2-cleanup-22 changes.
func TestGuardrails_FullFlow_Regression(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider() // seeds the default catalog (incl. cert-manager)

	orch := New(nil, defaultCreds(), argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	// 1) Register with a real addon.
	regResult, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:   "prod-eu",
		Region: "eu-west-1",
		Addons: map[string]bool{"cert-manager": true},
	})
	if err != nil {
		t.Fatalf("register failed: %v", err)
	}
	if regResult.Status != "success" {
		t.Fatalf("register status = %q (%s)", regResult.Status, regResult.Error)
	}

	// 2) Enable a real addon on the (now-managed) cluster. RegisterCluster
	//    writes managed-clusters.yaml via the auto-merged PR into git.files,
	//    so the cluster is present for the enable label write.
	enResult, err := orch.EnableAddon(context.Background(), EnableAddonRequest{
		Cluster: "prod-eu",
		Addon:   "cert-manager",
		Yes:     true,
	})
	if err != nil {
		t.Fatalf("enable failed: %v", err)
	}
	if enResult.Status != "success" {
		t.Fatalf("enable status = %q (%s)", enResult.Status, enResult.Error)
	}

	// 3) Edit per-cluster values for the real addon.
	if _, err := orch.SetClusterAddonValues(context.Background(), "prod-eu", "cert-manager", "replicaCount: 2\n", nil, false); err != nil {
		t.Fatalf("values edit failed: %v", err)
	}

	// The managed-clusters file carries the canonical enabled label.
	mc := string(git.files["configuration/managed-clusters.yaml"])
	if !strings.Contains(mc, "cert-manager: enabled") {
		t.Errorf("expected canonical 'cert-manager: enabled' label after full flow, got:\n%s", mc)
	}
}

// ── V2-cleanup-32: UpdateClusterAddons referential-integrity guard ────────────

// TestUpdateClusterAddons_UnknownAddon_Rejected ensures that a PATCH request
// containing an addon name absent from the catalog is rejected before any Git
// write or ArgoCD label update occurs.
func TestUpdateClusterAddons_UnknownAddon_Rejected(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	// Catalog has cert-manager only; we try to update with "ghost".
	git.files["configuration/addons-catalog.yaml"] = catalogOnlyYAML("cert-manager")
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters:\n  - name: prod-eu\n    labels: {}\n")
	git.files["configuration/addons-clusters-values/prod-eu.yaml"] = []byte("clusterGlobalValues:\n")

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	_, err := orch.UpdateClusterAddons(context.Background(), "prod-eu", "https://k8s.example.com:6443", "", map[string]bool{
		"ghost": true,
	}, nil, false)
	if err == nil {
		t.Fatal("expected rejection when addon is not in catalog, got nil")
	}
	if !IsAddonNotInCatalog(err) {
		t.Fatalf("expected *AddonNotInCatalogError, got: %v", err)
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should name the bad addon, got: %v", err)
	}
	// No PR should have been opened and no label should have been updated.
	if len(git.prs) != 0 {
		t.Errorf("expected no PR on referential-integrity rejection, got %d", len(git.prs))
	}
	if len(argocd.updatedLabels) != 0 {
		t.Errorf("expected no ArgoCD label update on rejection, got %d entries", len(argocd.updatedLabels))
	}
}

// TestUpdateClusterAddons_MixedAddonNames_Rejected: one valid + one unknown
// name → the whole request is rejected (all-or-nothing).
func TestUpdateClusterAddons_MixedAddonNames_Rejected(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	git.files["configuration/addons-catalog.yaml"] = catalogOnlyYAML("cert-manager", "metrics-server")

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	_, err := orch.UpdateClusterAddons(context.Background(), "prod-eu", "https://k8s.example.com:6443", "", map[string]bool{
		"cert-manager": true,
		"ghost":        false,
	}, nil, false)
	if err == nil {
		t.Fatal("expected rejection, got nil")
	}
	if !IsAddonNotInCatalog(err) {
		t.Fatalf("expected *AddonNotInCatalogError, got: %v", err)
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should name the bad addon, got: %v", err)
	}
	if len(git.prs) != 0 {
		t.Errorf("expected no PR on rejection, got %d", len(git.prs))
	}
}

// TestUpdateClusterAddons_ValidCatalogAddons_Succeeds: a request containing
// only catalog-registered addons passes the guard and completes normally.
func TestUpdateClusterAddons_ValidCatalogAddons_Succeeds(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	git.files["configuration/addons-catalog.yaml"] = catalogOnlyYAML("cert-manager", "metrics-server")
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters:\n  - name: prod-eu\n    labels: {}\n")
	git.files["configuration/addons-clusters-values/prod-eu.yaml"] = []byte("clusterGlobalValues:\n")

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	result, err := orch.UpdateClusterAddons(context.Background(), "prod-eu", "https://k8s.example.com:6443", "", map[string]bool{
		"cert-manager":   true,
		"metrics-server": false,
	}, nil, false)
	if err != nil {
		t.Fatalf("expected success for valid catalog addons, got: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected status 'success', got %q (%s)", result.Status, result.Error)
	}
}

// TestUpdateClusterAddons_EmptyMap_SkipsGuard: an empty addons map bypasses the
// catalog check (nothing to validate) and completes normally.
func TestUpdateClusterAddons_EmptyMap_SkipsGuard(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	// No catalog at all — but empty addons map should still succeed.
	delete(git.files, "configuration/addons-catalog.yaml")
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters:\n  - name: prod-eu\n    labels: {}\n")
	git.files["configuration/addons-clusters-values/prod-eu.yaml"] = []byte("clusterGlobalValues:\n")

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	result, err := orch.UpdateClusterAddons(context.Background(), "prod-eu", "https://k8s.example.com:6443", "", map[string]bool{}, nil, false)
	if err != nil {
		t.Fatalf("empty addons map should skip the catalog guard: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected status 'success', got %q", result.Status)
	}
}
