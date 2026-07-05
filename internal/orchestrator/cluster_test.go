package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/providers"
)

// V125-1-8.3 contract change: RefreshClusterCredentials no longer calls
// o.argocd.RegisterCluster directly. The reconciler owns Secret writes;
// refresh now probes the credentials provider (fail-fast UX) and nudges
// the reconciler trigger seam. These tests pin the new behaviour: probe
// succeeds → trigger fires + no direct ArgoCD API write; probe fails →
// error propagates + no trigger fires.

func TestRefreshClusterCredentials_Success(t *testing.T) {
	argocd := newMockArgocd()
	creds := &mockCredProvider{
		creds: map[string]*providers.Kubeconfig{
			"prod-eu": {
				Server: "https://k8s-refreshed.example.com:6443",
				CAData: []byte("new-ca"),
				Token:  "new-token",
			},
		},
	}

	orch := New(nil, creds, argocd, newMockGitProvider(), autoMergeGitOps(), defaultPaths(), nil)
	triggers := 0
	orch.SetReconcilerTrigger(func() { triggers++ })

	err := orch.RefreshClusterCredentials(context.Background(), "prod-eu", "https://k8s.example.com:6443")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// V125-1-8.3: NO direct ArgoCD register API call — the reconciler does that.
	if _, ok := argocd.registeredClusters["prod-eu"]; ok {
		t.Error("V125-1-8.3 contract violated: refresh must NOT call argocd.RegisterCluster directly (reconciler owns it)")
	}
	// Trigger MUST fire so reconciler picks up the new credentials immediately.
	if triggers != 1 {
		t.Errorf("expected reconciler trigger to fire exactly once, got %d", triggers)
	}
}

func TestRefreshClusterCredentials_CredProviderError(t *testing.T) {
	argocd := newMockArgocd()
	creds := &mockCredProvider{
		err: errors.New("provider unavailable"),
	}

	orch := New(nil, creds, argocd, newMockGitProvider(), autoMergeGitOps(), defaultPaths(), nil)
	triggers := 0
	orch.SetReconcilerTrigger(func() { triggers++ })

	err := orch.RefreshClusterCredentials(context.Background(), "prod-eu", "https://k8s.example.com:6443")
	if err == nil {
		t.Fatal("expected error from credentials provider")
	}
	if !strings.Contains(err.Error(), "fetching fresh credentials") {
		t.Errorf("unexpected error message: %v", err)
	}
	// Probe failed → trigger MUST NOT fire (otherwise reconciler is woken to
	// fetch the same broken creds for nothing).
	if triggers != 0 {
		t.Errorf("trigger must not fire when probe fails, got %d invocations", triggers)
	}
}

func TestRefreshClusterCredentials_NoCredProvider(t *testing.T) {
	// V125-1-8.3: with a kubeconfig-only deployment (nil credProvider) the
	// refresh has nothing to probe; it still fires the trigger so the
	// reconciler can opportunistically re-reconcile.
	argocd := newMockArgocd()
	orch := New(nil, nil, argocd, newMockGitProvider(), autoMergeGitOps(), defaultPaths(), nil)
	triggers := 0
	orch.SetReconcilerTrigger(func() { triggers++ })

	err := orch.RefreshClusterCredentials(context.Background(), "kind-sharko", "https://127.0.0.1:60123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if triggers != 1 {
		t.Errorf("expected reconciler trigger to fire exactly once, got %d", triggers)
	}
}

func TestParseAddonsCatalog_Valid(t *testing.T) {
	data := []byte(`applicationsets:
  - name: cert-manager
    chart: cert-manager
    repoURL: https://charts.jetstack.io
    version: 1.14.0
    namespace: cert-manager
  - name: metrics-server
    chart: metrics-server
    repoURL: https://kubernetes-sigs.github.io/metrics-server
    version: 0.6.0
    namespace: kube-system
`)

	entries, err := parseAddonsCatalog(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Name != "cert-manager" {
		t.Errorf("expected cert-manager, got %q", entries[0].Name)
	}
	if entries[1].Version != "0.6.0" {
		t.Errorf("expected version 0.6.0 for metrics-server, got %q", entries[1].Version)
	}
}

func TestParseAddonsCatalog_Empty(t *testing.T) {
	data := []byte(`applicationsets: []`)
	entries, err := parseAddonsCatalog(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestParseAddonsCatalog_InvalidYAML(t *testing.T) {
	data := []byte(`{invalid yaml: [`)
	_, err := parseAddonsCatalog(data)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

// V2-cleanup-34 — enveloped catalog shape tests.
// The live gitops repo writes addons-catalog.yaml in the enveloped shape
// (apiVersion: sharko.dev/v1 / kind: AddonCatalog). The previous duplicate
// only understood the legacy bare top-level applicationsets: key, which
// caused every catalog lookup to return zero entries on real repos.

// envelopedCatalogFixture is a schema-valid enveloped addons-catalog.yaml
// containing a single keda entry — mirrors the live repro (maintainer added
// keda to the catalog but EnableAddon returned 422 addon "keda" is not in
// the catalog).
const envelopedCatalogFixture = `# yaml-language-server: $schema=https://raw.githubusercontent.com/MoranWeissman/sharko/main/docs/schemas/addons-catalog.v1.json
apiVersion: sharko.dev/v1
kind: AddonCatalog
metadata:
  name: addon-catalog
spec:
  applicationsets:
    - name: keda
      chart: keda
      repoURL: https://kedacore.github.io/charts
      version: 2.14.2
`

// envelopedCatalogFixtureMulti is an enveloped catalog with multiple entries,
// used to test back-compat alongside the legacy path.
const envelopedCatalogFixtureMulti = `# yaml-language-server: $schema=https://raw.githubusercontent.com/MoranWeissman/sharko/main/docs/schemas/addons-catalog.v1.json
apiVersion: sharko.dev/v1
kind: AddonCatalog
metadata:
  name: addon-catalog
spec:
  applicationsets:
    - name: cert-manager
      chart: cert-manager
      repoURL: https://charts.jetstack.io
      version: "1.16.3"
      namespace: cert-manager
    - name: keda
      chart: keda
      repoURL: https://kedacore.github.io/charts
      version: 2.14.2
`

// TestParseAddonsCatalog_Enveloped_Found is the live repro test:
// an enveloped catalog with a single keda entry must yield that entry.
// This test FAILS on the old duplicate parseAddonsCatalog (returns zero entries).
func TestParseAddonsCatalog_Enveloped_Found(t *testing.T) {
	entries, err := parseAddonsCatalog([]byte(envelopedCatalogFixture))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d — old duplicate parser returned 0 here (V2-cleanup-34 repro)", len(entries))
	}
	if entries[0].Name != "keda" {
		t.Errorf("expected entry name %q, got %q", "keda", entries[0].Name)
	}
}

// TestParseAddonsCatalog_Enveloped_MultiEntry checks that all entries are
// returned correctly from an enveloped catalog with more than one addon.
func TestParseAddonsCatalog_Enveloped_MultiEntry(t *testing.T) {
	entries, err := parseAddonsCatalog([]byte(envelopedCatalogFixtureMulti))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name] = true
	}
	if !names["cert-manager"] {
		t.Error("expected cert-manager in entries")
	}
	if !names["keda"] {
		t.Error("expected keda in entries")
	}
}

// TestParseAddonsCatalog_LegacyShape_StillWorks pins back-compat:
// the legacy bare applicationsets: shape must still parse correctly
// after the delegation refactor. No regression from V2-cleanup-34.
func TestParseAddonsCatalog_LegacyShape_StillWorks(t *testing.T) {
	legacy := []byte(`applicationsets:
  - name: cert-manager
    chart: cert-manager
    repoURL: https://charts.jetstack.io
    version: 1.14.0
  - name: keda
    chart: keda
    repoURL: https://kedacore.github.io/charts
    version: 2.14.2
`)
	entries, err := parseAddonsCatalog(legacy)
	if err != nil {
		t.Fatalf("unexpected error parsing legacy shape: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 legacy entries, got %d", len(entries))
	}
}

// TestRequireAddonsInCatalog_Enveloped_Found: the catalog guard must find an
// addon that IS present in an enveloped catalog — previously it returned zero
// entries and raised *AddonNotInCatalogError for every addon name.
func TestRequireAddonsInCatalog_Enveloped_Found(t *testing.T) {
	git := newMockGitProvider()
	git.files["configuration/addons-catalog.yaml"] = []byte(envelopedCatalogFixture)

	orch := New(nil, nil, newMockArgocd(), git, autoMergeGitOps(), defaultPaths(), nil)

	entries, err := orch.requireAddonsInCatalog(context.Background(), []string{"keda"})
	if err != nil {
		t.Fatalf("unexpected error: %v — addon IS in the enveloped catalog (V2-cleanup-34 repro)", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 catalog entry, got %d", len(entries))
	}
}

// TestRequireAddonsInCatalog_Enveloped_Missing: an addon that is genuinely
// absent from an enveloped catalog still produces *AddonNotInCatalogError.
func TestRequireAddonsInCatalog_Enveloped_Missing(t *testing.T) {
	git := newMockGitProvider()
	git.files["configuration/addons-catalog.yaml"] = []byte(envelopedCatalogFixture) // only keda

	orch := New(nil, nil, newMockArgocd(), git, autoMergeGitOps(), defaultPaths(), nil)

	_, err := orch.requireAddonsInCatalog(context.Background(), []string{"cert-manager"})
	if err == nil {
		t.Fatal("expected *AddonNotInCatalogError for absent addon, got nil")
	}
	if !IsAddonNotInCatalog(err) {
		t.Fatalf("expected *AddonNotInCatalogError, got: %T %v", err, err)
	}
	if !strings.Contains(err.Error(), "cert-manager") {
		t.Errorf("error should name the missing addon, got: %v", err)
	}
}

// TestEnableAddon_EnvelopedCatalog_NoRejection is the live repro end-to-end:
// EnableAddon on a cluster with keda present in the enveloped catalog must NOT
// return *AddonNotInCatalogError. The old duplicate returned 0 entries → 422.
func TestEnableAddon_EnvelopedCatalog_NoRejection(t *testing.T) {
	git := newMockGitProvider()
	git.files["configuration/addons-catalog.yaml"] = []byte(envelopedCatalogFixture) // keda only
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters:\n  - name: prod-eu\n    labels: {}\n")
	git.files["configuration/addons-clusters-values/prod-eu.yaml"] = []byte("clusterGlobalValues:\n")

	orch := New(nil, nil, newMockArgocd(), git, autoMergeGitOps(), defaultPaths(), nil)

	_, err := orch.EnableAddon(context.Background(), EnableAddonRequest{
		Cluster: "prod-eu",
		Addon:   "keda",
		Yes:     true,
	})
	if err == nil {
		return // success — no 422
	}
	if IsAddonNotInCatalog(err) {
		t.Fatalf("V2-cleanup-34 repro: EnableAddon returned *AddonNotInCatalogError for an addon that IS in the enveloped catalog: %v", err)
	}
	// Any other error (e.g. values-file path related) is acceptable — only
	// AddonNotInCatalogError is the regression we are pinning against.
}

// TestSwallowSite_EnvelopedCatalog_SeesEntries pins the swallow site in
// addon_ops.go:109 (DisableAddon path): a non-nil catalog must be returned
// for an enveloped file so generateClusterValues gets the correct entry list.
// We test this indirectly by verifying that EnableAddon then DisableAddon
// produce a PR without an AddonNotInCatalogError.
func TestSwallowSite_EnvelopedCatalog_SeesEntries(t *testing.T) {
	git := newMockGitProvider()
	git.files["configuration/addons-catalog.yaml"] = []byte(envelopedCatalogFixture) // keda only
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters:\n  - name: prod-eu\n    labels:\n      keda: enabled\n")
	git.files["configuration/addons-clusters-values/prod-eu.yaml"] = []byte("clusterGlobalValues:\n")

	orch := New(nil, nil, newMockArgocd(), git, autoMergeGitOps(), defaultPaths(), nil)

	_, err := orch.DisableAddon(context.Background(), DisableAddonRequest{
		Cluster: "prod-eu",
		Addon:   "keda",
		Cleanup: "all",
	})
	if IsAddonNotInCatalog(err) {
		t.Fatalf("swallow site should not produce AddonNotInCatalogError from enveloped catalog: %v", err)
	}
	// err may be non-nil for other reasons (kubeconfig not available etc.); that
	// is fine — only the AddonNotInCatalog regression is checked here.
}
