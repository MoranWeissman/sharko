//go:build e2e

package lifecycle

import (
	"net/http"
	"os/exec"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/tests/e2e/harness"
)

// TestClusterTest_ProviderCrossContamination_NamespaceSwitch is the V125-1-13.6
// regression test for the namespace cross-contamination fix landed in
// V125-1-10.8 (commit 28e5bcda). It boots a real Sharko via Helm with a
// connection whose Provider config mirrors the maintainer's pre-10.8 dev
// install — Type="k8s-secrets", Namespace="sharko" — then PUTs the connection
// to switch Type to "argocd" while leaving Namespace="sharko" untouched (the
// exact UI dropdown change that triggered the bug), registers a target kind
// cluster via the kubeconfig flow, and asserts POST /clusters/{name}/test
// returns HTTP 200 with all PASS steps.
//
// What this test would catch pre-V125-1-10.8:
//
//   Before the fix, ArgoCDProvider inherited cfg.Namespace from the
//   ProviderConfig the user had previously set for the k8s-secrets backend.
//   When the operator switched the Type dropdown to "argocd" — but did NOT
//   manually clear the Namespace field — ArgoCDProvider would receive
//   cfg.Namespace="sharko" from ReinitializeFromConnection (router.go:332-343)
//   and look for cluster Secrets in the "sharko" namespace instead of
//   "argocd". The cluster Secret lives in "argocd" (that's where ArgoCD
//   itself writes it on the kubeconfig register flow), so GetCredentials
//   would return "secret not found" and POST /clusters/{name}/test would
//   surface a 503 with error_code=ERR_AUTH or a "Fetch credentials" step
//   in status=fail.
//
//   The V125-1-10.8 fix (resolveArgoCDNamespace in
//   internal/providers/argocd_provider.go:112-126) hardcodes the resolved
//   namespace to SHARKO_ARGOCD_NAMESPACE env or "argocd" regardless of what
//   cfg.Namespace says, and emits a one-shot WARN log when the cross-
//   contamination is detected. With the fix in place, this test passes
//   green; without it, the cluster test 503s.
//
// Why SharkoModeHelm is required:
//
//   This test exercises the full ReinitializeFromConnection → providers.New
//   → NewArgoCDProvider → resolveArgoCDNamespace path against a real
//   in-cluster K8s API. SharkoModeInProcess can't satisfy this surface
//   because (a) the in-process boot path doesn't wire a credentials provider
//   at all (sharko.go:236-275 — no providers.New call) and (b) the
//   ArgoCDProvider's K8s client construction goes through
//   rest.InClusterConfig which only succeeds inside a pod. Helm mode boots
//   Sharko as a real pod with a real ServiceAccount so the provider can
//   list/get Secrets in the argocd namespace via its bound ClusterRole +
//   argocd-secrets Role (charts/sharko/templates/rbac.yaml).
//
// Skip-graceful behaviour mirrors cluster_test_argocd_provider_test.go:
//   - Missing kind/kubectl/docker/helm → t.Skip with the standard diagnostic
//   - SHARKO_E2E_SKIP_HELM=1 (or absence of E2E_SHARKO_MODE=helm in CI) →
//     this test is gated to Helm mode and skips when the harness defaults
//     to in-process. The whole point of the test is the real-pod path; an
//     in-process run would prove nothing.
func TestClusterTest_ProviderCrossContamination_NamespaceSwitch(t *testing.T) {
	// ---- prereq guards: skip cleanly when the host can't run kind+helm ----
	if _, err := exec.LookPath("kind"); err != nil {
		t.Skip("ProviderCrossContamination: kind not installed; install via `brew install kind`")
	}
	if _, err := exec.LookPath("kubectl"); err != nil {
		t.Skip("ProviderCrossContamination: kubectl not installed")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("ProviderCrossContamination: docker not installed (required by kind)")
	}
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("ProviderCrossContamination: helm not installed; install via `brew install helm`")
	}
	if out, err := exec.Command("docker", "info").CombinedOutput(); err != nil {
		t.Skipf("ProviderCrossContamination: docker daemon not reachable: %v\noutput: %s", err, out)
	}

	// ---- safety: clean up stale e2e clusters from prior failed runs ----
	harness.DestroyAllStaleE2EClusters(t)

	// ---- provision topology: 1 mgmt (hosts ArgoCD + Sharko) + 1 target ----
	t.Logf("provisioning kind topology (1 mgmt + 1 target) — typically 60-90s")
	clusters := harness.ProvisionTopology(t, harness.ProvisionRequest{NumTargets: 1})
	t.Cleanup(func() { harness.DestroyTopology(t, clusters) })
	mgmt, target := clusters[0], clusters[1]

	harness.WaitClusterReady(t, mgmt, 90*time.Second)
	harness.WaitClusterReady(t, target, 90*time.Second)

	t.Logf("installing argocd into management cluster (%s)", mgmt.Name)
	harness.InstallArgoCD(t, mgmt)

	// Host-side ArgoCD access (port-forward + JWT) — used to populate the
	// connection's Argocd.Token. The token is JWT and works equally against
	// the in-cluster service URL (which is what the Sharko pod uses), so we
	// don't need separate auth for the pod-side path.
	argoAccess := startArgoCDAccess(t, mgmt)

	// GitFake + ghmock are NOT consumed by the Sharko pod in this test (the
	// pod can't reach host loopback), but we still create a connection with a
	// canonical github.com URL so connection validation passes. The cluster
	// register flow (kubeconfig provider → ArgoCD REST) and the cluster test
	// flow (ArgoCDProvider → K8s Secrets) do NOT touch the git side.
	gitfake := harness.StartGitFake(t)
	_ = gitfake // kept alive for symmetry with sibling tests; not consumed

	// ---- boot Sharko via Helm into the mgmt cluster ----
	t.Logf("installing sharko via Helm into mgmt cluster (%s) — this can take 1-3 minutes", mgmt.Name)
	sharko := harness.StartSharko(t, harness.SharkoConfig{
		Mode:        harness.SharkoModeHelm,
		MgmtCluster: &mgmt,
		GitFake:     gitfake,
	})
	sharko.WaitHealthy(t, 60*time.Second)

	admin := harness.NewClient(t, sharko)

	// ---- Step 1: Create the active connection with the pre-10.8 shape ----
	//
	// Provider.Type = "k8s-secrets"
	// Provider.Namespace = "sharko"
	//
	// This is the maintainer's pre-fix dev install setup: the operator
	// configured k8s-secrets as the backend with namespace="sharko" (the
	// namespace where the operator's Secret-based kubeconfig store lives).
	// On a pre-V125-1-10.8 build the same operator who later switches the
	// dropdown to "argocd" will hit the cross-contamination bug.
	const connName = "cross-contamination-regression"
	const argoNamespace = "argocd"
	const stalePreSwitchNamespace = "sharko" // the namespace value that pre-10.8 leaked through

	createReq := models.CreateConnectionRequest{
		Name: connName,
		Git: models.GitRepoConfig{
			Provider: models.GitProviderGitHub,
			Owner:    "sharko-e2e",
			Repo:     "sharko-addons",
			RepoURL:  "https://github.com/sharko-e2e/sharko-addons",
			Token:    "ghmock-test-token", // not used by the test — connection validation only
		},
		Argocd: models.ArgocdConfig{
			// Helm-mode Sharko runs INSIDE the kind cluster, so the
			// connection's argocd.server_url must be the in-cluster
			// service DNS — NOT argoAccess.URL which is the host-side
			// port-forwarded URL only useful for the test process.
			// Mirrors helmModeArgocdServerURL in
			// cluster_test_argocd_provider_test.go (sibling file in the
			// same package — the const is reused as-is below to keep
			// the two tests in lockstep).
			ServerURL: helmModeArgocdServerURL,
			Token:     argoAccess.Token,
			Namespace: argoNamespace,
			Insecure:  true,
		},
		Provider: &models.ProviderConfig{
			Type:      "k8s-secrets",
			Namespace: stalePreSwitchNamespace,
		},
		SetAsDefault: true,
	}
	admin.CreateConnection(t, createReq)
	admin.SetActiveConnection(t, connName)

	// Sanity check: the active connection carries the pre-switch shape.
	{
		list := admin.ListConnections(t)
		if list.ActiveConnection != connName {
			t.Fatalf("active connection = %q, want %q", list.ActiveConnection, connName)
		}
		var got *models.ConnectionResponse
		for i := range list.Connections {
			if list.Connections[i].Name == connName {
				got = &list.Connections[i]
				break
			}
		}
		if got == nil {
			t.Fatalf("connection %q not found in list", connName)
		}
		if got.Provider == nil {
			t.Fatalf("active connection has nil Provider — expected k8s-secrets/sharko")
		}
		if got.Provider.Type != "k8s-secrets" || got.Provider.Namespace != stalePreSwitchNamespace {
			t.Fatalf("pre-switch provider shape = %+v, want type=k8s-secrets ns=%q", got.Provider, stalePreSwitchNamespace)
		}
		t.Logf("pre-switch state confirmed: Provider.Type=%q Provider.Namespace=%q", got.Provider.Type, got.Provider.Namespace)
	}

	// ---- Step 2: Switch Provider.Type to "argocd" via PUT (the dropdown) ----
	//
	// Critical: Namespace stays "sharko" — that's the cross-contamination
	// scenario. The UI dropdown sends a PUT /connections/{name} with the
	// updated Type but does NOT clear Namespace; that's the exact wire-shape
	// that triggered the bug pre-fix. handleUpdateConnection dispatches to
	// ConnectionService.Create which reuses CreateConnectionRequest, so the
	// shape is identical to step 1 with one field flipped.
	updateReq := createReq
	updateReq.Provider = &models.ProviderConfig{
		Type:      "argocd",
		Namespace: stalePreSwitchNamespace, // intentionally NOT cleared — mimics dropdown change
	}
	admin.UpdateConnection(t, connName, updateReq)

	// Verify the post-switch shape: Type now argocd, Namespace still
	// "sharko" (carried over). This is what Sharko's
	// ReinitializeFromConnection now passes to providers.New —
	// resolveArgoCDNamespace must IGNORE the "sharko" value and resolve to
	// "argocd" instead. Pre-fix it would have passed "sharko" through and
	// broken downstream lookups.
	{
		list := admin.ListConnections(t)
		var got *models.ConnectionResponse
		for i := range list.Connections {
			if list.Connections[i].Name == connName {
				got = &list.Connections[i]
				break
			}
		}
		if got == nil || got.Provider == nil {
			t.Fatalf("connection %q lost its Provider after PUT", connName)
		}
		if got.Provider.Type != "argocd" {
			t.Fatalf("post-switch Provider.Type = %q, want %q", got.Provider.Type, "argocd")
		}
		if got.Provider.Namespace != stalePreSwitchNamespace {
			// If this fires the API silently rewrote the namespace, which
			// would also defeat the regression scenario. Hard fail.
			t.Fatalf("post-switch Provider.Namespace = %q, want %q (the cross-contamination scenario "+
				"requires the leftover ns to survive the PUT — if it doesn't, the test no longer "+
				"covers the V125-1-10.8 fix)", got.Provider.Namespace, stalePreSwitchNamespace)
		}
		t.Logf("post-switch state: Provider.Type=argocd Provider.Namespace=%q (cross-contamination scenario set up)",
			got.Provider.Namespace)
	}

	// ---- Step 3: Register the target via the kubeconfig flow ----
	//
	// Same shape as the sibling cluster_test_argocd_provider_test.go: the
	// kubeconfig register flow inside Sharko reaches ArgoCD, ArgoCD writes
	// a bearerToken-shape cluster Secret into the argocd namespace.
	const clusterName = "ns-cross-target"
	t.Logf("registering target cluster %q via kubeconfig flow", clusterName)
	registerBody := makeKubeconfigRegisterBody(t, target, clusterName)
	resp := admin.Do(t, http.MethodPost, "/api/v1/clusters", registerBody)
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("RegisterCluster: status=%d (kubeconfig register flow against helm-installed sharko failed)",
			resp.StatusCode)
	}

	// Wait for ListClusters to surface the new cluster (proves Sharko's
	// cluster-service view is consistent with what ArgoCD wrote).
	harness.Eventually(t, 30*time.Second, func() bool {
		lr := admin.ListClusters(t)
		for _, c := range lr.Clusters {
			if c.Name == clusterName && c.Managed {
				return true
			}
		}
		return false
	}, "registered cluster %q never appeared in list", clusterName)

	// ---- Step 4: The assertion — POST /clusters/{name}/test must 200 ----
	//
	// On the post-fix code: ArgoCDProvider was constructed with
	// cfg.Namespace="sharko" (from the connection's leftover field) but
	// resolveArgoCDNamespace ignored it and bound the provider to "argocd".
	// GetCredentials looks in the right namespace, finds the bearerToken
	// Secret ArgoCD wrote during register, and the test passes.
	//
	// On a pre-fix code: ArgoCDProvider was bound to "sharko", GetCredentials
	// returns "secret not found" → handler emits a 200 with the first step
	// "Fetch credentials" in status="fail" (or a 503 ERR_AUTH on some code
	// paths). The "all PASS" assertion would fail loudly.
	status, body := admin.TestClusterConnectivity(t, clusterName, false /* deep */)
	if status != http.StatusOK {
		t.Fatalf("Test endpoint: status=%d (want 200) — pre-fix code would surface this as a 503 from "+
			"the cross-contamination bug; body=%v", status, body)
	}

	steps, ok := body["steps"].([]any)
	if !ok || len(steps) == 0 {
		t.Fatalf("Test endpoint returned 200 but no steps in body: %v", body)
	}

	// All steps must be PASS. The first step must be "Fetch credentials" with
	// status=pass — that is the load-bearing assertion: it proves
	// ArgoCDProvider.GetCredentials succeeded against the argocd namespace
	// despite the connection carrying Provider.Namespace="sharko".
	first, _ := steps[0].(map[string]any)
	if first["name"] != "Fetch credentials" {
		t.Errorf("first step name = %v, want %q", first["name"], "Fetch credentials")
	}
	if first["status"] != "pass" {
		t.Fatalf("first step status = %v (want pass) — this is the V125-1-10.8 regression: "+
			"ArgoCDProvider failed to fetch credentials, which on a pre-fix build means it looked in "+
			"the leftover Provider.Namespace=%q instead of resolving to %q. Detail: %v",
			first["status"], stalePreSwitchNamespace, argoNamespace, first["detail"])
	}
	for i, s := range steps {
		stepMap, _ := s.(map[string]any)
		if stepMap["status"] != "pass" {
			t.Errorf("step[%d] %v: status=%v (want pass) — body=%v", i, stepMap["name"], stepMap["status"], body)
		}
	}
	t.Logf("ProviderCrossContamination: %d/%d steps PASS — V125-1-10.8 fix confirmed",
		len(steps), len(steps))

	// Final invariant — sharko is still healthy after the suite.
	h := admin.Health(t)
	if h.Status == "" {
		t.Errorf("final health: empty status: %+v", h)
	}
}
