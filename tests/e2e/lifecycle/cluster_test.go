//go:build e2e

package lifecycle

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/tests/e2e/harness"
)

// TestClusterLifecycle is the V2 Epic 7-1.4 master e2e test for the
// cluster surface. It exercises 21 endpoints under one shared topology:
//
//   - 1 management kind cluster (mgmt) running upstream ArgoCD.
//   - 2 target kind clusters (target-1, target-2) used as the
//     destination for kubeconfig-provider register tests.
//   - In-process sharko bound to a localhost httptest port, configured
//     with:
//       * MockGitProvider (in-memory, intercepts every git write so the
//         orchestrator's PR + merge flow lands in deterministic state).
//       * Connection seeded via POST /api/v1/connections/ pointing at
//         the kubectl-port-forwarded ArgoCD URL with the admin JWT.
//
// Each subtest is intentionally independent — they share the topology
// + sharko + admin client, but no per-subtest state. Order does matter
// only for the register-then-everything-else flow (we register one
// cluster early so subtests downstream have a managed cluster to
// inspect); register-orphan / batch / discovery / orphan-delete each
// create their own ephemeral state to avoid coupling.
//
// Skip-graceful behaviour: any subtest that exercises an endpoint
// requiring the EKS credentials provider (cred provider is nil for
// kubeconfig-only e2e runs) accepts a 503 response and t.Skip's the
// subtest body with a clear message. The handler's reachability is
// still proven (route registered, auth enforced, error path correct),
// just the deep functional path is deferred to a future story that
// adds a credentials provider stub.
func TestClusterLifecycle(t *testing.T) {
	// ---- prereq guards: skip cleanly when the host can't run kind ----
	if _, err := exec.LookPath("kind"); err != nil {
		t.Skip("TestClusterLifecycle: kind not installed; install via `brew install kind` or https://kind.sigs.k8s.io/")
	}
	if _, err := exec.LookPath("kubectl"); err != nil {
		t.Skip("TestClusterLifecycle: kubectl not installed")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("TestClusterLifecycle: docker not installed (required by kind)")
	}
	// docker daemon reachable? `docker info` exits non-zero when daemon
	// is down even though the binary is present.
	if out, err := exec.Command("docker", "info").CombinedOutput(); err != nil {
		t.Skipf("TestClusterLifecycle: docker daemon not reachable: %v\noutput: %s", err, out)
	}

	// ---- safety: clean up stale e2e clusters from prior failed runs ----
	harness.DestroyAllStaleE2EClusters(t)

	// ---- provision topology: 1 mgmt + 2 targets ----
	t.Logf("provisioning kind topology (1 mgmt + 2 targets) — typically 60-90s")
	clusters := harness.ProvisionTopology(t, harness.ProvisionRequest{NumTargets: 2})
	t.Cleanup(func() { harness.DestroyTopology(t, clusters) })
	mgmt, target1, target2 := clusters[0], clusters[1], clusters[2]

	// Wait for nodes to report Ready=True before installing argocd.
	harness.WaitClusterReady(t, mgmt, 90*time.Second)
	harness.WaitClusterReady(t, target1, 90*time.Second)
	harness.WaitClusterReady(t, target2, 90*time.Second)

	// ---- install argocd into mgmt (~60-90s) ----
	t.Logf("installing argocd into management cluster (%s)", mgmt.Name)
	harness.InstallArgoCD(t, mgmt)

	// ---- get host-reachable ArgoCD URL + admin token ----
	argoAccess := startArgoCDAccess(t, mgmt)

	// ---- start git fixtures + sharko ----
	gitfake := harness.StartGitFake(t)
	ghmock := harness.StartGitMock(t)

	sharko := harness.StartSharko(t, harness.SharkoConfig{
		Mode:        harness.SharkoModeInProcess,
		GitFake:     gitfake,
		GitProvider: ghmock,
	})
	sharko.WaitHealthy(t, 30*time.Second)
	harness.SeedUsers(t, sharko, harness.DefaultTestUsers())

	admin := harness.NewClient(t, sharko)

	// Seed a fully-formed active connection so the cluster handlers'
	// dual GetActiveGitProvider / GetActiveArgocdClient path resolves.
	// The git provider is overridden by SharkoConfig.GitProvider, so
	// the git config is only required to satisfy create-time validation;
	// the argocd config is real and load-bearing.
	seedActiveConnection(t, admin, gitfake.RepoURL, argoAccess.URL, argoAccess.Token)

	// ---------------------------------------------------------------
	// subtests
	// ---------------------------------------------------------------

	const (
		// One managed cluster is registered early in the suite and
		// re-used by the read-side subtests (Get, History, Comparison,
		// ConfigDiff, Values, Patch, Test, Refresh, Diagnose, Secrets,
		// Deregister). Defining it once keeps the state graph clear.
		managedClusterName = "lifecycle-managed"
	)

	t.Run("ListClustersInitiallyEmpty", func(t *testing.T) {
		resp := admin.ListClusters(t)
		if resp == nil {
			t.Fatal("ListClusters returned nil")
		}
		// Brand-new sharko + brand-new repo — there are no managed
		// clusters yet. Pending / orphan slices must be non-nil
		// (see V125-1.4 contract on ClustersResponse).
		if resp.PendingRegistrations == nil {
			t.Error("PendingRegistrations is nil; contract requires non-nil empty slice")
		}
		if resp.OrphanRegistrations == nil {
			t.Error("OrphanRegistrations is nil; contract requires non-nil empty slice")
		}
		t.Logf("clusters=%d pending=%d orphan=%d", len(resp.Clusters), len(resp.PendingRegistrations), len(resp.OrphanRegistrations))
	})

	t.Run("RegisterManagedCluster", func(t *testing.T) {
		body := makeKubeconfigRegisterBody(t, target1, managedClusterName)
		// Use the lower-level Do helper so we can accept either 201
		// (success), 207 (partial — happens when the in-memory ghmock
		// + real argocd disagree about the cluster state) or 200
		// (dry-run, but we are not dry-running here).
		resp := admin.Do(t, http.MethodPost, "/api/v1/clusters", body)
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			t.Fatalf("RegisterCluster: status=%d", resp.StatusCode)
		}
		var result orchestrator.RegisterClusterResult
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatalf("decode register result: %v", err)
		}
		t.Logf("register status=%s git=%+v argo_secret=%s", result.Status, result.Git, result.ArgoSecretStatus)
		// MockGitProvider's ListMockPRs lets us assert that the
		// register flow opened (and merged, when auto-merge is on)
		// the values-file PR.
		prs := ghmock.ListMockPRs("all")
		if len(prs) == 0 {
			t.Logf("ghmock: no PRs created (auto-merge may have merged + closed before list)")
		} else {
			for _, pr := range prs {
				t.Logf("ghmock PR #%d state=%s head=%s base=%s title=%q",
					pr.Number, pr.State, pr.HeadBranch, pr.BaseBranch, pr.Title)
			}
		}
		// Confirm sharko's view: ListClusters must surface the new
		// cluster with Managed=true.
		harness.Eventually(t, 10*time.Second, func() bool {
			lr := admin.ListClusters(t)
			for _, c := range lr.Clusters {
				if c.Name == managedClusterName && c.Managed {
					return true
				}
			}
			return false
		}, "registered cluster %q never appeared in list", managedClusterName)
	})

	t.Run("GetCluster", func(t *testing.T) {
		detail := admin.GetCluster(t, managedClusterName)
		if detail.Cluster.Name != managedClusterName {
			t.Errorf("GetCluster: name=%q want=%q", detail.Cluster.Name, managedClusterName)
		}
		t.Logf("GetCluster: addons=%d managed=%v", len(detail.Addons), detail.Cluster.Managed)
	})

	t.Run("PatchClusterLabels", func(t *testing.T) {
		// Empty addons map is the "no-op patch" — we want to prove
		// the route is reachable and writes a PR (or returns a clean
		// no-change response). The full secret_path branch is exercised
		// in addon_cluster_test.go (V2 Epic 7-1.7); here we only need
		// the round-trip to succeed.
		body := admin.PatchClusterAddons(t, managedClusterName, map[string]bool{}, nil)
		t.Logf("PatchClusterAddons body=%v", body)
	})

	t.Run("TestConnectivity", func(t *testing.T) {
		status, body := admin.TestClusterConnectivity(t, managedClusterName, false)
		switch status {
		case http.StatusOK:
			t.Logf("test ok: %v", body)
		case http.StatusServiceUnavailable:
			t.Skipf("TestConnectivity: 503 (no credentials provider configured) — skip-graceful per V124-4.1")
		default:
			t.Fatalf("unexpected status=%d body=%v", status, body)
		}
	})

	t.Run("RefreshArgoCD", func(t *testing.T) {
		status, body := admin.RefreshClusterCredentials(t, managedClusterName)
		switch status {
		case http.StatusOK:
			t.Logf("refresh ok: %v", body)
		case http.StatusServiceUnavailable:
			t.Skipf("RefreshArgoCD: 503 (no credentials provider configured)")
		case http.StatusBadGateway, http.StatusNotFound:
			// Allowed: the cluster may not yet appear in argocd's
			// own cluster list (the secret has been created but
			// argocd has not yet reconciled); the route's auth +
			// reachability is still proven.
			t.Logf("RefreshArgoCD: status=%d body=%v (route reachable; argocd reconcile pending)", status, body)
		default:
			t.Fatalf("unexpected status=%d body=%v", status, body)
		}
	})

	t.Run("Diagnose", func(t *testing.T) {
		status, body := admin.DiagnoseCluster(t, managedClusterName)
		switch status {
		case http.StatusOK:
			t.Logf("diagnose ok: %v", body)
		case http.StatusServiceUnavailable:
			t.Skipf("Diagnose: 503 (no credentials provider configured)")
		default:
			t.Fatalf("unexpected status=%d body=%v", status, body)
		}
	})

	t.Run("History", func(t *testing.T) {
		body := admin.GetClusterHistory(t, managedClusterName)
		if _, ok := body["cluster_name"]; !ok {
			t.Errorf("history: missing cluster_name key: %v", body)
		}
		if _, ok := body["history"]; !ok {
			t.Errorf("history: missing history key: %v", body)
		}
	})

	t.Run("Comparison", func(t *testing.T) {
		// 200 happy path; 404 when argocd has not yet seen the cluster
		// (acceptable for the first-pass register flow).
		resp := admin.Do(t, http.MethodGet, "/api/v1/clusters/"+managedClusterName+"/comparison", nil)
		defer resp.Body.Close()
		switch resp.StatusCode {
		case http.StatusOK:
			t.Logf("comparison ok")
		case http.StatusNotFound:
			t.Logf("comparison: 404 (cluster not yet in argocd; acceptable post-register)")
		default:
			t.Fatalf("comparison: status=%d", resp.StatusCode)
		}
	})

	t.Run("ConfigDiff", func(t *testing.T) {
		body := admin.GetClusterConfigDiff(t, managedClusterName)
		t.Logf("config-diff body keys: %v", mapKeys(body))
	})

	t.Run("Values", func(t *testing.T) {
		body := admin.GetClusterValues(t, managedClusterName)
		t.Logf("values body keys: %v", mapKeys(body))
	})

	t.Run("Secrets", func(t *testing.T) {
		status, body := admin.ListClusterSecrets(t, managedClusterName)
		switch status {
		case http.StatusOK:
			t.Logf("secrets list: %v", body)
		case http.StatusServiceUnavailable:
			t.Skipf("Secrets: 503 (no credentials provider configured) — skip-graceful")
		default:
			t.Fatalf("unexpected status=%d body=%v", status, body)
		}
	})

	t.Run("SecretsRefresh", func(t *testing.T) {
		status, body := admin.RefreshClusterSecrets(t, managedClusterName)
		switch status {
		case http.StatusOK:
			t.Logf("secrets refresh: %v", body)
		case http.StatusServiceUnavailable:
			t.Skipf("SecretsRefresh: 503 (no credentials provider configured)")
		default:
			t.Fatalf("unexpected status=%d body=%v", status, body)
		}
	})

	t.Run("ListAvailable", func(t *testing.T) {
		// /clusters/available is the discovery endpoint — requires
		// credProvider (EKS path). Skip-graceful on 503.
		status, body := admin.ListAvailableClusters(t)
		switch status {
		case http.StatusOK:
			t.Logf("available: %v", body)
		case http.StatusServiceUnavailable:
			t.Skipf("ListAvailable: 503 (no credentials provider) — kubeconfig path not exposed via /available")
		default:
			t.Fatalf("unexpected status=%d body=%v", status, body)
		}
	})

	t.Run("DiscoverEKS", func(t *testing.T) {
		status, body := admin.DiscoverEKSClusters(t, "us-east-1")
		switch status {
		case http.StatusOK:
			t.Logf("discover ok: %v", body)
		case http.StatusServiceUnavailable:
			t.Skipf("DiscoverEKS: 503 (no credentials provider) — EKS-only handler")
		case http.StatusBadGateway:
			t.Logf("DiscoverEKS: 502 (provider configured but discovery failed) — route reachable")
		default:
			t.Fatalf("unexpected status=%d body=%v", status, body)
		}
	})

	t.Run("AdoptClusters", func(t *testing.T) {
		req := orchestrator.AdoptClustersRequest{
			Clusters:  []string{"some-cluster"},
			AutoMerge: false,
			DryRun:    true,
		}
		status, body := admin.AdoptClusters(t, req)
		switch status {
		case http.StatusOK, http.StatusMultiStatus:
			t.Logf("adopt ok: %v", body)
		case http.StatusServiceUnavailable:
			t.Skipf("AdoptClusters: 503 (no credentials provider)")
		case http.StatusBadGateway:
			t.Logf("AdoptClusters: 502 (cluster not found in argocd; route reachable)")
		default:
			t.Fatalf("unexpected status=%d body=%v", status, body)
		}
	})

	t.Run("UnadoptCluster", func(t *testing.T) {
		// Unadopt against a non-existent cluster — we expect either
		// 404 (cluster unknown) or 502 (orchestrator error). The
		// route's auth + parameter validation is what we verify.
		status, body := admin.UnadoptCluster(t, "does-not-exist", true)
		t.Logf("unadopt non-existent: status=%d body=%v", status, body)
		if status >= 200 && status < 300 {
			t.Errorf("unadopt non-existent: expected non-2xx, got %d", status)
		}
	})

	t.Run("BatchRegister", func(t *testing.T) {
		// Batch with two kubeconfig requests. The handler requires
		// credProvider so this almost always 503s in this in-process
		// configuration; skip-graceful.
		reqs := []orchestrator.RegisterClusterRequest{
			{Name: "lifecycle-batch-1", Provider: "kubeconfig"},
			{Name: "lifecycle-batch-2", Provider: "kubeconfig"},
		}
		status, body := admin.BatchRegisterClusters(t, reqs)
		switch status {
		case http.StatusOK, http.StatusMultiStatus:
			t.Logf("batch ok: %v", body)
		case http.StatusServiceUnavailable:
			t.Skipf("BatchRegister: 503 (no credentials provider) — kubeconfig path not exposed via /batch")
		case http.StatusBadRequest:
			// Some validators run before credProvider check.
			t.Logf("batch: 400 (validation failed): %v", body)
		default:
			t.Fatalf("unexpected status=%d body=%v", status, body)
		}
	})

	t.Run("ClusterNodes", func(t *testing.T) {
		body := admin.GetClusterNodes(t)
		// in-process sharko is NOT in-cluster, so the handler returns
		// 200 with an empty nodes slice and a "message" explaining
		// the degraded state. We just assert the schema.
		if _, ok := body["nodes"]; !ok {
			t.Errorf("cluster nodes: missing nodes key: %v", body)
		}
		t.Logf("cluster nodes: %v", body)
	})

	t.Run("OrphanDelete_NonOrphanRefused", func(t *testing.T) {
		// Try to orphan-delete the managed cluster — handler MUST
		// refuse with 400 (the cluster is in git, so it is not an
		// orphan; user must use DELETE /clusters/{name}). This is
		// the safety contract from V125-1-7 / BUG-058.
		status := admin.DeleteOrphanCluster(t, managedClusterName)
		if status != http.StatusBadRequest && status != http.StatusNotFound {
			t.Errorf("orphan-delete on managed cluster: status=%d, want 400 (refused) or 404 (not in argocd)", status)
		} else {
			t.Logf("orphan-delete on managed cluster correctly refused: status=%d", status)
		}
	})

	t.Run("DeregisterCluster", func(t *testing.T) {
		// Tear down the managed cluster we registered early. cleanup=git
		// is the cheapest deregister path that exercises the orchestrator's
		// remove flow without requiring credProvider (cleanup=all needs it).
		status, body := admin.RemoveCluster(t, managedClusterName, "git", false)
		switch status {
		case http.StatusOK, http.StatusMultiStatus:
			t.Logf("deregister ok: status=%d body=%v", status, body)
		case http.StatusBadGateway:
			t.Fatalf("deregister failed: status=%d body=%v", status, body)
		default:
			t.Logf("deregister: status=%d body=%v", status, body)
		}
	})

	t.Run("RegisterOrphanByCancellingPR", func(t *testing.T) {
		// V125-1-7 / BUG-058 orphan flow: register a cluster on a
		// connection where PR auto-merge is off. The orchestrator's
		// pre-merge ArgoCD register call (internal/orchestrator/cluster.go:408
		// fallback path) creates a cluster Secret in argocd before the
		// PR opens. If the PR is then closed without merging, we
		// have an orphan. The exact behaviour depends on whether the
		// argo-secret-manager is wired (it is not in the in-process
		// configuration), so this subtest is best-effort: register a
		// throwaway cluster, then close the PR and assert the
		// orphan-delete endpoint can clean up.
		const orphanName = "lifecycle-orphan-candidate"
		body := makeKubeconfigRegisterBody(t, target2, orphanName)
		resp := admin.Do(t, http.MethodPost, "/api/v1/clusters", body)
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			// Acceptable: with auto-merge ON (default), the register
			// completes synchronously and there is no orphan PR to
			// close. Skip-graceful.
			t.Logf("register orphan-candidate: status=%d (acceptable; auto-merge may be on)", resp.StatusCode)
		}

		// Try to find the open PR for this cluster name (by title —
		// the orchestrator emits "<commit_prefix> register cluster
		// <name>" with a randomised branch suffix, so we cannot
		// assume the branch name).
		var openPRNum int
		for _, pr := range ghmock.ListMockPRs("open") {
			if strings.Contains(pr.Title, orphanName) {
				openPRNum = pr.Number
				break
			}
		}
		if openPRNum == 0 {
			t.Logf("no open PR found for orphan candidate %q (auto-merge likely merged it); skipping close-PR step", orphanName)
		} else {
			ghmock.ClosePR(t, openPRNum)
			t.Logf("closed PR #%d for %q to manufacture orphan state", openPRNum, orphanName)
		}

		// Best-effort cleanup: orphan-delete the candidate.
		status := admin.DeleteOrphanCluster(t, orphanName)
		t.Logf("orphan-delete on cancelled cluster %q: status=%d", orphanName, status)
	})

	// ---------------------------------------------------------------
	// Final invariant — sharko is still healthy after the suite.
	// Catches regressions where a handler corrupts shared state.
	// ---------------------------------------------------------------
	t.Run("FinalHealthCheck", func(t *testing.T) {
		h := admin.Health(t)
		if h.Status == "" {
			t.Errorf("final health: empty status: %+v", h)
		}
		t.Logf("final health: %+v", h)
	})
}

// mapKeys returns the keys of m in any order; used for diagnostic
// logging when we don't want to dump full payloads.
func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

