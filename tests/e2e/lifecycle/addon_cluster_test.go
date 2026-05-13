//go:build e2e

// Package lifecycle exercises sharko's per-cluster orchestration surface
// end-to-end against a real kind topology (1 mgmt + N targets) with
// upstream ArgoCD installed in the management cluster.
//
// Story 7-1.7 covers the three argocd-touching per-cluster addon
// endpoints:
//
//   POST   /api/v1/clusters/{name}/addons/{addon}   — enable
//   DELETE /api/v1/clusters/{name}/addons/{addon}   — disable
//   POST   /api/v1/addons/{name}/upgrade            — global upgrade
//
// The per-cluster values-override endpoint
// (PUT /clusters/{cluster}/addons/{name}/values) is story 7-1.8 and is
// intentionally NOT tested here.
//
// Boot model: the test uses the in-process sharko boot path
// (SharkoModeInProcess) with the gitfake + ghmock injected. The kind
// topology and InstallArgoCD are still wired in so the harness invariant
// "lifecycle tests need a real argocd in mgmt" is honoured even though
// the in-process boot does not yet helm-install sharko itself (deferred
// to story 7-1.10). Once 7-1.10 lands, a `Mode: SharkoModeHelm` flip on
// the SharkoConfig below should be the only change needed to upgrade
// this test to full-stack fidelity.
//
// Why dry-run for enable/disable: the live (non-dry-run) enable path
// needs the cluster to be already registered in
// configuration/managed-clusters.yaml AND a valid per-cluster values
// file; the same is true for disable. The 7-1.4 cluster-lifecycle story
// pre-seeds those — until that lands we exercise the API contract via
// dry_run=true (which still proves per-cluster routing isolation, file
// preview correctness, and confirmation gating). The upgrade subtest
// runs LIVE because the upgrade endpoint has no dry_run mode and only
// touches the catalog (which we can pre-seed against the mock).
package lifecycle

import (
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/providers"
	"github.com/MoranWeissman/sharko/tests/e2e/harness"
)

// ---------------------------------------------------------------------------
// fixtures
// ---------------------------------------------------------------------------

const (
	// Catalog seeded into the mock git provider so global upgrade has
	// something to rewrite. Kept tiny — just two addons so the test asserts
	// that an upgrade of one preserves the other (the standard "blast
	// radius" assertion).
	addonsCatalogYAML = `# Addons catalog
applicationsets:
  - name: metrics-server
    repoURL: https://kubernetes-sigs.github.io/metrics-server
    chart: metrics-server
    version: 3.12.0
  - name: kube-state-metrics
    repoURL: https://prometheus-community.github.io/helm-charts
    chart: kube-state-metrics
    version: 5.20.0
`

	// Initial managed-clusters.yaml with both target clusters and both
	// addons disabled. Enable/disable subtests use dry_run so the file
	// itself is never rewritten — but the orchestrator's dry-run path
	// returns the same FilesToWrite preview regardless of file presence,
	// so seeding it keeps the upgrade subtest's commit graph realistic.
	managedClustersYAML = `# Managed clusters
clusters:
  - name: target-1
    labels:
      metrics-server: disabled
      kube-state-metrics: disabled
  - name: target-2
    labels:
      metrics-server: disabled
      kube-state-metrics: disabled
`

	// Per-cluster values files — required by the live disable path
	// (existingValues read from git). Empty stanzas are sufficient for
	// extractAddonsFromValues; the dry_run path does not actually read
	// them but seeding makes future live-mode upgrades trivial.
	target1ValuesYAML = "# target-1 values\naddons: {}\n"
	target2ValuesYAML = "# target-2 values\naddons: {}\n"

	// Repo layout matches the cmd/sharko/serve.go defaults so any future
	// helm-mode flip uses the same on-disk shape.
	pathClusterValues   = "configuration/addons-clusters-values"
	pathManagedClusters = "configuration/managed-clusters.yaml"
	pathCatalog         = "configuration/addons-catalog.yaml"

	// Connection name + dummy ArgoCD token (the in-process test never
	// dials argocd — buildArgocdClient just constructs a *http.Client; the
	// dry_run / catalog-only paths exercised here never issue an HTTP
	// request to argocd-server).
	connName    = "e2e-conn"
	argocdToken = "e2e-fake-argocd-token"
)

// ---------------------------------------------------------------------------
// test
// ---------------------------------------------------------------------------

// TestPerClusterAddonLifecycle exercises the three argocd-touching
// per-cluster addon endpoints across two target clusters with real
// ArgoCD installed in the mgmt cluster.
//
// Subtests (each runs serially, sharing the kind topology + sharko
// instance for speed):
//
//  1. EnableAddonOnTarget1     — POST .../target-1/addons/metrics-server
//                                with dry_run=true. Asserts response
//                                names target-1 (NOT target-2) and
//                                previews the right files.
//  2. DisableAddonOnTarget1    — DELETE same path with dry_run=true.
//                                Asserts dry-run preview shape.
//  3. UpgradeAddonGlobally     — POST /addons/metrics-server/upgrade
//                                (live). Asserts the catalog version
//                                advances AND the other addon is
//                                untouched.
//  4. MultiClusterIsolation    — Drives enable on (target-1, addon X)
//                                and (target-2, addon Y) and asserts
//                                each response carries the right cluster
//                                + addon, never crossed wires.
//
// Skips when kind / docker are not on PATH. Skips with a clear
// diagnostic when ProvisionTopology or InstallArgoCD fails (kind/docker
// installed but not functional).
func TestPerClusterAddonLifecycle(t *testing.T) {
	if _, err := exec.LookPath("kind"); err != nil {
		t.Skip("TestPerClusterAddonLifecycle: kind binary not on PATH")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("TestPerClusterAddonLifecycle: docker binary not on PATH")
	}

	// Mop up any stragglers from prior aborted runs. Idempotent — only
	// touches clusters carrying the e2e.sharko.io/test=true sentinel.
	harness.DestroyAllStaleE2EClusters(t)

	// 1 mgmt + 2 targets, all parallel-provisioned.
	clusters := harness.ProvisionTopology(t, harness.ProvisionRequest{NumTargets: 2})
	t.Cleanup(func() { harness.DestroyTopology(t, clusters) })
	mgmt := clusters[0]
	// target1 / target2 are referenced by name in API calls; the kind
	// KindCluster handles are only needed if a future live mode wants
	// the kubeconfig.
	target1Name := clusters[1].Name
	target2Name := clusters[2].Name
	t.Logf("topology ready: mgmt=%s target1=%s target2=%s", mgmt.Name, target1Name, target2Name)

	// ArgoCD in mgmt — the harness invariant for any addon-orchestration
	// test, even though the in-process boot does not actually call
	// argocd in this story.
	harness.InstallArgoCD(t, mgmt)

	// Sharko in-process with the GH mock as the active git provider.
	git := harness.StartGitFake(t)
	ghmock := harness.StartGitMock(t)
	sharko := harness.StartSharko(t, harness.SharkoConfig{
		Mode:        harness.SharkoModeInProcess,
		MgmtCluster: &mgmt,
		GitFake:     git,
		GitProvider: ghmock,
	})
	sharko.WaitHealthy(t, 30*time.Second)

	// Wire repo paths + gitops config so the orchestrator's path joins
	// hit the canonical sharko layout. SetWriteAPIDeps is the same hook
	// cmd/sharko/serve.go uses; we pass nil credProvider/providerCfg
	// because the dry_run + catalog-only paths do not need cluster
	// credentials.
	sharko.APIServer().SetWriteAPIDeps(
		providers.ClusterCredentialsProvider(nil),
		(*providers.Config)(nil),
		orchestrator.RepoPathsConfig{
			ClusterValues:   pathClusterValues,
			GlobalValues:    "configuration/addons-global-values",
			Catalog:         pathCatalog,
			Charts:          "charts/",
			Bootstrap:       "bootstrap/",
			ManagedClusters: pathManagedClusters,
		},
		orchestrator.GitOpsConfig{
			BaseBranch:   "main",
			BranchPrefix: "sharko/",
			CommitPrefix: "sharko:",
			RepoURL:      git.RepoURL,
		},
	)

	// Seed users so any future RBAC subtest can borrow this scaffold.
	harness.SeedUsers(t, sharko, harness.DefaultTestUsers())

	// Connection — needed because every per-cluster addon handler calls
	// connSvc.GetActiveArgocdClient + connSvc.GetActiveGitProvider before
	// branching on dry_run. SetDemoGitProvider already wired the git
	// override; we still need a connection record so the argocd client
	// constructor succeeds.
	admin := harness.NewClient(t, sharko)
	registerConnection(t, sharko, admin, git.RepoURL)

	// Pre-seed the mock so the upgrade subtest has a parseable catalog
	// and the disable dry-run preview matches what a real workflow would
	// see (file already present → action=update).
	seedMockGit(t, ghmock)

	// ----------------------------------------------------------------------
	// Subtests share the topology + sharko above. Run sequentially because
	// they share state on the in-memory mock.
	// ----------------------------------------------------------------------

	t.Run("EnableAddonOnTarget1", func(t *testing.T) {
		req := orchestrator.EnableAddonRequest{
			Cluster: target1Name,
			Addon:   "metrics-server",
			DryRun:  true,
			// Yes intentionally false — dry_run path returns BEFORE the
			// confirmation gate, so this proves the dry-run short-circuit
			// works.
		}
		got := admin.EnableAddonOnCluster(t, target1Name, "metrics-server", req)
		if got.Status != "success" {
			t.Fatalf("EnableAddon dry_run: status=%q want success; result=%+v", got.Status, got)
		}
		if got.Cluster != target1Name {
			t.Errorf("EnableAddon: result.Cluster=%q want %q (per-cluster routing)", got.Cluster, target1Name)
		}
		if got.Addon != "metrics-server" {
			t.Errorf("EnableAddon: result.Addon=%q want metrics-server", got.Addon)
		}
		if got.DryRun == nil {
			t.Fatal("EnableAddon dry_run: DryRun preview is nil")
		}
		// Two file previews — the per-cluster values + managed-clusters.yaml.
		if n := len(got.DryRun.FilesToWrite); n < 1 {
			t.Errorf("EnableAddon dry_run: FilesToWrite empty")
		}
		// The values file path MUST mention target-1 (the per-cluster
		// isolation invariant) and MUST NOT mention target-2.
		expectPathContains(t, got.DryRun.FilesToWrite, target1Name, "target-2")
		// PR title must reference both the addon and the cluster.
		if !strings.Contains(got.DryRun.PRTitle, "metrics-server") ||
			!strings.Contains(got.DryRun.PRTitle, target1Name) {
			t.Errorf("EnableAddon dry_run: PRTitle=%q must mention metrics-server + %s", got.DryRun.PRTitle, target1Name)
		}
		// Negative isolation: target-2 was never written to in the mock.
		if ghmock.FileExists("main", pathClusterValues+"/"+target2Name+".yaml") {
			t.Errorf("EnableAddon dry_run leaked to target-2 (file appeared on main)")
		}
		t.Logf("EnableAddon dry_run on %s: PR title=%q files=%d", target1Name, got.DryRun.PRTitle, len(got.DryRun.FilesToWrite))
	})

	t.Run("DisableAddonOnTarget1", func(t *testing.T) {
		req := orchestrator.DisableAddonRequest{
			Cluster: target1Name,
			Addon:   "metrics-server",
			Cleanup: "all",
			DryRun:  true,
		}
		got := admin.DisableAddonOnCluster(t, target1Name, "metrics-server", req)
		if got.Status != "success" {
			t.Fatalf("DisableAddon dry_run: status=%q want success; result=%+v", got.Status, got)
		}
		if got.Cluster != target1Name {
			t.Errorf("DisableAddon: result.Cluster=%q want %q", got.Cluster, target1Name)
		}
		if got.Addon != "metrics-server" {
			t.Errorf("DisableAddon: result.Addon=%q want metrics-server", got.Addon)
		}
		if got.Cleanup != "all" {
			t.Errorf("DisableAddon: result.Cleanup=%q want all", got.Cleanup)
		}
		if got.DryRun == nil {
			t.Fatal("DisableAddon dry_run: DryRun preview is nil")
		}
		expectPathContains(t, got.DryRun.FilesToWrite, target1Name, "target-2")
		if !strings.Contains(got.DryRun.PRTitle, "metrics-server") ||
			!strings.Contains(got.DryRun.PRTitle, target1Name) {
			t.Errorf("DisableAddon dry_run: PRTitle=%q must mention metrics-server + %s", got.DryRun.PRTitle, target1Name)
		}
		t.Logf("DisableAddon dry_run on %s: cleanup=%s files=%d", target1Name, got.Cleanup, len(got.DryRun.FilesToWrite))
	})

	t.Run("UpgradeAddonGlobally", func(t *testing.T) {
		// LIVE — the upgrade endpoint has no dry_run mode. Touches only
		// the catalog file (no cluster values, no remote argocd call).
		const newVersion = "3.12.1"
		got := admin.UpgradeAddon(t, "metrics-server", harness.UpgradeAddonRequest{
			Version: newVersion,
		})
		if got == nil {
			t.Fatal("UpgradeAddon: nil result")
		}
		// PR may be auto-merged (gitopsCfg.PRAutoMerge=false by default,
		// so likely Merged=false here — the assertion is on the catalog
		// content rather than merge state).
		if got.PRUrl == "" && got.Branch == "" {
			t.Errorf("UpgradeAddon: GitResult missing PRUrl AND Branch — at least one expected")
		}
		t.Logf("UpgradeAddon: branch=%s pr=%s merged=%v", got.Branch, got.PRUrl, got.Merged)

		// The branch sharko opened MUST contain the updated catalog with
		// the new version of metrics-server, AND the un-touched version
		// of kube-state-metrics. We assert against the branch (not main)
		// because PRAutoMerge=false leaves the change in a feature
		// branch until human approval.
		var probeBranch string
		switch {
		case got.Merged:
			probeBranch = "main"
		case got.Branch != "":
			probeBranch = got.Branch
		default:
			// Fallback: scan branches for the sharko/ prefix.
			for _, b := range ghmock.ListBranches() {
				if strings.HasPrefix(b, "sharko/") {
					probeBranch = b
					break
				}
			}
		}
		if probeBranch == "" {
			t.Fatal("UpgradeAddon: cannot determine which branch holds the catalog change")
		}
		updated := ghmock.FileAt(probeBranch, pathCatalog)
		if updated == "" {
			t.Fatalf("UpgradeAddon: catalog missing on branch %q", probeBranch)
		}
		if !strings.Contains(updated, "version: "+newVersion) {
			t.Errorf("UpgradeAddon: catalog on %q does not contain version %s\n%s", probeBranch, newVersion, updated)
		}
		// The other addon's version is preserved.
		if !strings.Contains(updated, "version: 5.20.0") {
			t.Errorf("UpgradeAddon: kube-state-metrics version was modified (blast radius leak)\n%s", updated)
		}
	})

	t.Run("MultiClusterIsolation", func(t *testing.T) {
		// Enable addon X on target-1, addon Y on target-2 — assert each
		// dry-run response carries the right cluster + addon. This
		// exercises the per-cluster routing path through the API
		// (handler reads PathValue("name")) and through the orchestrator
		// (writes the correct values file path).
		req1 := orchestrator.EnableAddonRequest{
			Cluster: target1Name,
			Addon:   "metrics-server",
			DryRun:  true,
		}
		got1 := admin.EnableAddonOnCluster(t, target1Name, "metrics-server", req1)
		if got1.Cluster != target1Name || got1.Addon != "metrics-server" {
			t.Errorf("MultiCluster: got1.Cluster=%q got1.Addon=%q", got1.Cluster, got1.Addon)
		}
		expectPathContains(t, got1.DryRun.FilesToWrite, target1Name, target2Name)

		req2 := orchestrator.EnableAddonRequest{
			Cluster: target2Name,
			Addon:   "kube-state-metrics",
			DryRun:  true,
		}
		got2 := admin.EnableAddonOnCluster(t, target2Name, "kube-state-metrics", req2)
		if got2.Cluster != target2Name || got2.Addon != "kube-state-metrics" {
			t.Errorf("MultiCluster: got2.Cluster=%q got2.Addon=%q", got2.Cluster, got2.Addon)
		}
		expectPathContains(t, got2.DryRun.FilesToWrite, target2Name, target1Name)

		t.Logf("MultiCluster: target1.metrics-server.dry-run + target2.kube-state-metrics.dry-run both routed correctly")
	})

	t.Run("EnableRequiresConfirmation", func(t *testing.T) {
		// Negative path — without yes:true and without dry_run, the
		// orchestrator returns "confirmation required" which the handler
		// surfaces as 400 (not 502). Anchors the gate as a contract.
		req := orchestrator.EnableAddonRequest{
			Cluster: target1Name,
			Addon:   "metrics-server",
			// DryRun:false, Yes:false — should 400.
		}
		resp := admin.EnableAddonOnClusterRaw(t, target1Name, "metrics-server", req,
			harness.WithExpectStatus(http.StatusBadRequest),
			harness.WithNoRetry(),
		)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("EnableRequiresConfirmation: status=%d want 400", resp.StatusCode)
		}
	})
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// registerConnection POSTs a sharko connection record naming the gitfake
// repo and a placeholder ArgoCD URL+token. Required because every
// per-cluster addon handler calls connSvc.GetActiveArgocdClient (which
// builds a client from the connection's argocd config) before branching
// on dry_run.
//
// The placeholder ArgoCD URL is never dialed — the in-process boot path
// exercised here only uses dry_run (enable/disable) or catalog-only
// (upgrade) flows, neither of which issues an HTTP request to argocd.
// A future helm-mode test would replace this with the real argocd-server
// NodePort URL + a real admin token extracted from the kind cluster.
func registerConnection(t *testing.T, sharko *harness.Sharko, admin *harness.Client, repoURL string) {
	t.Helper()

	// Use the typed model directly — same shape the wizard sends.
	body := map[string]any{
		"name": connName,
		"git": map[string]any{
			"provider": "github",
			"repo_url": "https://github.com/sharko-e2e/sharko-addons",
			"owner":    "sharko-e2e",
			"repo":     "sharko-addons",
			"token":    "e2e-fake-pat",
		},
		"argocd": map[string]any{
			"server_url": "http://argocd-server.argocd.svc.cluster.local",
			"token":      argocdToken,
			"namespace":  "argocd",
			"insecure":   true,
		},
		"set_as_default": true,
	}
	admin.PostJSON(t, "/api/v1/connections/", body, nil,
		harness.WithExpectStatus(http.StatusCreated),
	)

	// Set active so connSvc.getActiveConn returns it. SetActive returns
	// 200 OK on success; the typed helper accepts any 2xx by default.
	admin.PostJSON(t, "/api/v1/connections/active",
		map[string]string{"connection_name": connName}, nil,
	)
}

// seedMockGit pre-populates the in-memory git mock with the addons
// catalog, managed-clusters.yaml, and per-target values files. Required
// for the live UpgradeAddonGlobally subtest (which reads the catalog
// from main) and for any future live enable/disable path.
func seedMockGit(t *testing.T, mock *harness.MockGitProvider) {
	t.Helper()
	files := map[string][]byte{
		pathCatalog:         []byte(addonsCatalogYAML),
		pathManagedClusters: []byte(managedClustersYAML),
		pathClusterValues + "/target-1.yaml": []byte(target1ValuesYAML),
		pathClusterValues + "/target-2.yaml": []byte(target2ValuesYAML),
	}
	if err := mock.BatchCreateFiles(t.Context(), files, "main", "seed: e2e fixtures"); err != nil {
		t.Fatalf("seedMockGit: %v", err)
	}
}

// expectPathContains asserts that at least one FilePreview's Path
// contains expectSubstr and that no FilePreview's Path contains
// forbidSubstr. The dual assertion catches the per-cluster routing
// invariant — enable on target-1 must NOT preview a write to target-2.
func expectPathContains(t *testing.T, previews []orchestrator.FilePreview, expectSubstr, forbidSubstr string) {
	t.Helper()
	if len(previews) == 0 {
		t.Errorf("expectPathContains: no previews to inspect (want substring %q)", expectSubstr)
		return
	}
	hit := false
	for _, p := range previews {
		if strings.Contains(p.Path, expectSubstr) {
			hit = true
		}
		if forbidSubstr != "" && strings.Contains(p.Path, forbidSubstr) {
			t.Errorf("expectPathContains: preview path %q must NOT contain %q", p.Path, forbidSubstr)
		}
	}
	if !hit {
		paths := make([]string, len(previews))
		for i, p := range previews {
			paths[i] = p.Path
		}
		t.Errorf("expectPathContains: no preview path contains %q (got %s)", expectSubstr, strings.Join(paths, ", "))
	}
}

