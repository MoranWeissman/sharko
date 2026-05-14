//go:build e2e

package lifecycle

import (
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/tests/e2e/harness"
)

// TestArgoCDProviderClusterTest is the V125-1-10.3 happy-path e2e: it boots a
// minimal kind topology (1 mgmt + 1 target), installs ArgoCD into mgmt, runs
// in-process Sharko, registers the target via the kubeconfig flow (which
// causes ArgoCD to write a bearerToken-shape cluster Secret into the
// argocd namespace), and then calls POST /api/v1/clusters/{name}/test.
//
// The contract this test pins:
//
//   - When the active credentials provider is an ArgoCDProvider (the v1.25+
//     in-cluster default) AND the registered cluster's ArgoCD Secret uses the
//     bearerToken auth shape (the shape PR #321's kubeconfig-register flow
//     writes — see internal/argocd/client_write.go::RegisterCluster), the
//     Test endpoint returns HTTP 200 with the verify Steps showing
//     "Fetch credentials" = "pass" and the rest of Stage1 either pass or
//     fail-on-network — never a 503 with an argocd_provider_* error_code.
//
//   - The bearerToken happy-path is the PRIMARY production target for
//     self-hosted K8s clusters on EC2 / VMs (kind / minikube here are the
//     dev-time stand-in for that). Per maintainer 2026-05-14 framing, the
//     awsAuthConfig (IAM) and execProviderConfig (exec-plugin) shapes are
//     the *unsupported* paths that route to argocd_provider_iam_required /
//     argocd_provider_exec_unsupported respectively; this test does NOT
//     exercise those — they are covered by the unit tests in
//     internal/api/clusters_test_argocd_provider_errors_test.go.
//
// Skip-graceful behaviour:
//
//   - When kind / kubectl / docker are missing on the host, the test skips
//     with the same diagnostic the rest of the e2e lifecycle suite uses.
//   - When the in-process Sharko has no credentials provider wired (the
//     current default for SharkoModeInProcess — the auto-default ArgoCD
//     path triggers only when rest.InClusterConfig() succeeds, which it
//     cannot from the test binary), the Test endpoint returns the BUG-035
//     503 with error_code=no_secrets_backend. The test recognises this
//     specific 503 and t.Skips with a clear pointer to the future story
//     that lands the ArgoCDProvider-against-mgmt wiring (Helm-install path
//     in tests/e2e/harness; tracked alongside V125-1-10's e2e expansion).
//     Once that lands, this subtest becomes a hard pass-or-fail gate.
//
// This split keeps the test useful in CI today (it proves the route still
// exists, the register flow still runs, and the failure mode is the
// expected BUG-035 503 — not a regression to a different shape) while
// remaining a forward-compatible pin for the bearerToken happy path.
func TestArgoCDProviderClusterTest(t *testing.T) {
	// ---- prereq guards: skip cleanly when the host can't run kind ----
	if _, err := exec.LookPath("kind"); err != nil {
		t.Skip("TestArgoCDProviderClusterTest: kind not installed; install via `brew install kind` or https://kind.sigs.k8s.io/")
	}
	if _, err := exec.LookPath("kubectl"); err != nil {
		t.Skip("TestArgoCDProviderClusterTest: kubectl not installed")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("TestArgoCDProviderClusterTest: docker not installed (required by kind)")
	}
	if out, err := exec.Command("docker", "info").CombinedOutput(); err != nil {
		t.Skipf("TestArgoCDProviderClusterTest: docker daemon not reachable: %v\noutput: %s", err, out)
	}

	// ---- safety: clean up stale e2e clusters from prior failed runs ----
	harness.DestroyAllStaleE2EClusters(t)

	// ---- provision topology: 1 mgmt + 1 target (smaller than the
	//      master lifecycle suite — we only need one register + one test) ----
	t.Logf("provisioning kind topology (1 mgmt + 1 target) — typically 60-90s")
	clusters := harness.ProvisionTopology(t, harness.ProvisionRequest{NumTargets: 1})
	t.Cleanup(func() { harness.DestroyTopology(t, clusters) })
	mgmt, target := clusters[0], clusters[1]

	harness.WaitClusterReady(t, mgmt, 90*time.Second)
	harness.WaitClusterReady(t, target, 90*time.Second)

	t.Logf("installing argocd into management cluster (%s)", mgmt.Name)
	harness.InstallArgoCD(t, mgmt)

	argoAccess := startArgoCDAccess(t, mgmt)

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

	seedActiveConnection(t, admin, gitfake.RepoURL, argoAccess.URL, argoAccess.Token)

	const clusterName = "argocd-provider-happy"

	// ---- register the target via the kubeconfig flow ----
	// internal/orchestrator/cluster.go's kubeconfig branch eventually calls
	// internal/argocd/client_write.go::RegisterCluster which POSTs to the
	// ArgoCD REST API; ArgoCD writes the cluster Secret with the
	// bearerToken auth shape into the argocd namespace.
	t.Logf("registering target cluster %q via kubeconfig flow", clusterName)
	body := makeKubeconfigRegisterBody(t, target, clusterName)
	resp := admin.Do(t, http.MethodPost, "/api/v1/clusters", body)
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("RegisterCluster: status=%d", resp.StatusCode)
	}

	// Confirm sharko's view: ListClusters surfaces the new cluster.
	harness.Eventually(t, 15*time.Second, func() bool {
		lr := admin.ListClusters(t)
		for _, c := range lr.Clusters {
			if c.Name == clusterName && c.Managed {
				return true
			}
		}
		return false
	}, "registered cluster %q never appeared in list", clusterName)

	// ---- the assertion — Test endpoint against the bearerToken-shape
	//      Secret that was just written ----
	status, respBody := admin.TestClusterConnectivity(t, clusterName, false)

	switch status {
	case http.StatusOK:
		// Happy path. Verify the steps array carries a non-empty
		// "Fetch credentials" step with status=pass — that is the
		// signal that ArgoCDProvider.GetCredentials was the call that
		// satisfied the test (not the legacy K8s Secrets / AWS SM path).
		steps, ok := respBody["steps"].([]any)
		if !ok || len(steps) == 0 {
			t.Fatalf("Test endpoint returned 200 but no steps in body: %v", respBody)
		}
		first, _ := steps[0].(map[string]any)
		if first["name"] != "Fetch credentials" {
			t.Errorf("first step name = %v, want %q", first["name"], "Fetch credentials")
		}
		if first["status"] != "pass" {
			t.Errorf("first step status = %v, want %q (proves ArgoCDProvider.GetCredentials succeeded against the bearerToken Secret)",
				first["status"], "pass")
		}
		t.Logf("Test endpoint OK — bearerToken happy path verified for self-hosted K8s registration shape (%d steps)", len(steps))

	case http.StatusServiceUnavailable:
		// Inspect the error_code to distinguish the two skip-graceful
		// branches:
		//   - no_secrets_backend → harness has no credProvider wired
		//     (current SharkoModeInProcess default; expected today)
		//   - argocd_provider_*  → ArgoCDProvider IS wired but the
		//     register flow ended up with a non-bearerToken Secret
		//     shape (would be a real regression — fail loud).
		errCode, _ := respBody["error_code"].(string)
		switch errCode {
		case "no_secrets_backend":
			t.Skipf("TestArgoCDProviderClusterTest: in-process Sharko has no credentials provider wired " +
				"(error_code=no_secrets_backend). The bearerToken happy path is unverified until the " +
				"e2e harness wires an ArgoCDProvider against the mgmt cluster (tracked alongside " +
				"V125-1-10's harness expansion). Until then, this 503 is the expected outcome on the " +
				"current in-process boot path.")
		case "argocd_provider_iam_required",
			"argocd_provider_exec_unsupported",
			"argocd_provider_unsupported_auth":
			t.Fatalf("Test endpoint returned 503 with error_code=%q — the kubeconfig register flow "+
				"should produce a bearerToken-shape Secret, not %s. Body: %v",
				errCode, errCode, respBody)
		default:
			t.Fatalf("Test endpoint returned 503 with unknown error_code=%q (body=%v)", errCode, respBody)
		}

	default:
		t.Fatalf("Test endpoint: unexpected status=%d body=%v", status, respBody)
	}

	// Final invariant — sharko is still healthy after the suite.
	h := admin.Health(t)
	if h.Status == "" {
		t.Errorf("final health: empty status: %+v", h)
	}

	// Touch ghmock so its t.Helper / cleanup path is exercised (and so
	// the import is not stripped). The register flow's PR list is
	// informational only; absence is fine when auto-merge merged + closed.
	if prs := ghmock.ListMockPRs("all"); len(prs) > 0 {
		var titles []string
		for _, pr := range prs {
			titles = append(titles, pr.Title)
		}
		t.Logf("ghmock PRs: %s", strings.Join(titles, "; "))
	}
}
