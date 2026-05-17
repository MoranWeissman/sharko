//go:build e2e

package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/MoranWeissman/sharko/internal/providers"
	"github.com/MoranWeissman/sharko/tests/e2e/harness"
)

// Story V125-1-13.4 — convert cluster_test_argocd_provider to run + pass under
// SharkoModeHelm.
//
// Pre-Wave-D this file held a t.Skip stub: in-process Sharko cannot satisfy
// rest.InClusterConfig(), so the auto-default ArgoCDProvider was never wired
// and the Test endpoint always returned BUG-035 503/no_secrets_backend. Wave C
// (commit 7d0cd65d) wired SharkoModeHelm to call installSharkoHelm +
// bootstrapHelmSharkoAuth, which gives us a real in-cluster Sharko whose
// /api/v1/clusters/{name}/test handler can exercise the full
// ArgoCDProvider.GetCredentials → verify.Stage1 path against an actual ArgoCD
// cluster Secret in the argocd namespace.
//
// What this test pins (per V125-1-10.3 contract — see
// internal/api/clusters_discover.go::handleTestCluster + the
// ArgoCDProviderCode* constants in internal/providers/argocd_provider.go):
//
//  1. Happy path: registering a target kind cluster via the kubeconfig POST
//     flow causes ArgoCD to write a bearerToken-shape cluster Secret into the
//     argocd namespace. POST /clusters/{name}/test then routes through the
//     auto-default ArgoCDProvider, returns HTTP 200, and the response carries
//     the Stage1 verify.Steps with every step status == "pass".
//
//  2. Typed-error paths (Story 10.3): mutating the live cluster Secret's
//     data["config"] field to inject awsAuthConfig / execProviderConfig /
//     unrecognised auth shapes routes through ArgoCDProviderError and
//     surfaces as HTTP 503 with stable error_code values:
//       - awsAuthConfig    → argocd_provider_iam_required
//       - execProviderConfig → argocd_provider_exec_unsupported
//       - no recognised shape (config field stripped) → argocd_provider_unsupported_auth
//
// Test ordering: the typed-error subtests run sequentially against a single
// shared registered cluster (one Helm install + one ArgoCD register is the
// expensive setup, ~2-3 minutes; we don't pay it three more times). Each
// subtest restores the bearerToken shape via t.Cleanup so the next subtest
// starts from a known-good Secret.

const (
	// argocdHappyClusterName is the cluster name used for the happy-path
	// register + every typed-error subtest's mutate-and-test cycle. Single
	// register keeps the suite under ~3 minutes total.
	argocdHappyClusterName = "argocd-provider-e2e"

	// helmModeArgocdServerURL is the in-cluster ArgoCD server URL the
	// Helm-installed Sharko pod uses to talk to ArgoCD. Hardcoded service
	// DNS — the harness's ArgoCD install always lands in the "argocd" ns
	// with the standard "argocd-server" Service name (see argocd.go).
	// Mirrors the same constant addon_cluster_test.go::registerConnection
	// uses (line 428) for the helm-mode active connection.
	helmModeArgocdServerURL = "https://argocd-server.argocd.svc.cluster.local"
)

// TestClusterTest_ArgoCDProvider is the Wave D parent test. Subtests run
// sequentially against one Helm-installed Sharko + one registered target.
//
// The expensive setup (kind + ArgoCD + Sharko Helm install + cluster
// registration) happens once in the parent body; each t.Run subtest mutates
// the registered cluster's ArgoCD Secret then asserts the Test endpoint
// response. A t.Cleanup inside each subtest restores the bearerToken shape so
// subsequent subtests start clean.
func TestClusterTest_ArgoCDProvider(t *testing.T) {
	// ---- prereq guards: skip cleanly when the host can't run kind/helm ----
	for _, bin := range []string{"kind", "kubectl", "docker", "helm"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("TestClusterTest_ArgoCDProvider: %s not installed; install via the project README's e2e prereqs", bin)
		}
	}
	if out, err := exec.Command("docker", "info").CombinedOutput(); err != nil {
		t.Skipf("TestClusterTest_ArgoCDProvider: docker daemon not reachable: %v\noutput: %s", err, out)
	}

	// ---- safety: clean up stale e2e clusters from prior failed runs ----
	harness.DestroyAllStaleE2EClusters(t)

	// ---- provision topology: 1 mgmt + 1 target ----
	t.Logf("provisioning kind topology (1 mgmt + 1 target) — typically 60-90s")
	clusters := harness.ProvisionTopology(t, harness.ProvisionRequest{NumTargets: 1})
	t.Cleanup(func() { harness.DestroyTopology(t, clusters) })
	mgmt, target := clusters[0], clusters[1]

	harness.WaitClusterReady(t, mgmt, 90*time.Second)
	harness.WaitClusterReady(t, target, 90*time.Second)

	// ---- install ArgoCD into mgmt (~90s on cold image pull) ----
	t.Logf("installing argocd into management cluster (%s)", mgmt.Name)
	harness.InstallArgoCD(t, mgmt)

	// ---- start gitfake + ghmock for the active connection ----
	gitfake := harness.StartGitFake(t)
	ghmock := harness.StartGitMock(t)

	// ---- boot Sharko via Helm (Wave C wiring) ----
	// SharkoModeHelm requires cfg.MgmtCluster; pass a pointer into the
	// clusters slice (ProvisionTopology returns []KindCluster by value).
	t.Logf("installing sharko into mgmt cluster via helm (~30-90s depending on docker build cache)")
	sharko := harness.StartSharko(t, harness.SharkoConfig{
		Mode:        harness.SharkoModeHelm,
		MgmtCluster: &mgmt,
		GitFake:     gitfake,
		GitProvider: ghmock,
	})
	sharko.WaitHealthy(t, 30*time.Second)

	// Build a typed API client. NewClient picks up sharko.AdminUser /
	// sharko.AdminPass which Wave B's bootstrapHelmSharkoAuth populated
	// from sharko-initial-admin-secret in the kind cluster.
	admin := harness.NewClient(t, sharko)

	// ---- read ArgoCD admin password + login (needed for active connection) ----
	argoPwd := fetchArgoAdminPassword(t, mgmt)
	// We never actually port-forward ArgoCD here — the active connection's
	// argocd.server_url is the in-cluster service DNS that the Sharko pod
	// dials directly. We only need the password to get an admin JWT for
	// connSvc.GetActiveArgocdClient to authenticate against ArgoCD. Login
	// against ArgoCD's REST API is a per-process call; we run it through a
	// short-lived port-forward using the existing cluster_helpers
	// startArgoCDAccess primitive which already wraps that pattern.
	argoAccess := startArgoCDAccess(t, mgmt)
	_ = argoPwd // password was probed for diagnostics; the port-forward path
	// internally re-reads the secret. Future cleanup: collapse the two reads.

	// ---- seed active connection with in-cluster ArgoCD URL ----
	// Helm-mode Sharko runs INSIDE the kind cluster, so the active
	// connection's argocd.server_url MUST be the in-cluster service DNS
	// (helmModeArgocdServerURL above) — NOT argoAccess.URL which is the
	// host-side port-forwarded URL useful only for the test process.
	//
	// Same constraint applies to the gitfake URL: gitfake.RepoURL is a
	// 127.0.0.1:<port> address on the test host. From inside the Sharko
	// Pod that loopback resolves to the Pod itself, not the host. Rewrite
	// it via harness.RewriteHostLoopbackForPod so the Pod reaches the
	// host's gitfake via host.docker.internal (Docker Desktop) or the
	// host-gateway extraHost on Linux kind.
	podReachableGitfakeURL := harness.RewriteHostLoopbackForPod(gitfake.RepoURL)
	seedHelmActiveConnection(t, admin, podReachableGitfakeURL, helmModeArgocdServerURL, argoAccess.Token)

	// ---- register the target via the kubeconfig flow ----
	// internal/orchestrator/cluster.go's kubeconfig branch parses the
	// inline kubeconfig directly (skips credProvider) and registers the
	// cluster in ArgoCD. ArgoCD then writes the cluster Secret with the
	// bearerToken auth shape into the argocd namespace.
	t.Logf("registering target cluster %q via kubeconfig flow", argocdHappyClusterName)
	body := makeKubeconfigRegisterBody(t, target, argocdHappyClusterName)
	regResp := admin.Do(t, http.MethodPost, "/api/v1/clusters", body)
	regResp.Body.Close()
	if regResp.StatusCode < 200 || regResp.StatusCode >= 300 {
		t.Fatalf("RegisterCluster: status=%d (helm-mode active connection may need adjustment — see report)", regResp.StatusCode)
	}

	// ---- wait for ArgoCD's cluster Secret to land in argocd ns ----
	// The Secret write is the load-bearing precondition for the Test
	// endpoint — without it ArgoCDProvider.findClusterSecret returns
	// NotFound. Helm-mode Sharko's argoSecretManager + reconciler may also
	// have written a Sharko-managed Secret (Step 3b in cluster.go); either
	// way, by the time this poll succeeds, the Secret exists.
	mgmtK8s := buildK8sClient(t, mgmt.Kubeconfig)
	waitForArgoCDClusterSecret(t, mgmtK8s, argocdHappyClusterName, 60*time.Second)

	// =======================================================================
	// Subtests share the registered cluster.
	// =======================================================================

	t.Run("HappyPath_BearerToken_Returns200_AllStepsPass", func(t *testing.T) {
		status, respBody := admin.TestClusterConnectivity(t, argocdHappyClusterName, false)
		if status != http.StatusOK {
			t.Fatalf("Test endpoint: status=%d body=%v (expected 200 for bearerToken happy path)", status, respBody)
		}
		// The 6-step Stage1 chain (per internal/api/clusters_discover.go::handleTestCluster):
		//   Fetch credentials → Fetch server version → Ensure namespace
		//   → Create test secret → Read back test secret → Delete test secret
		//
		// handleTestCluster prepends "Fetch credentials" (status=pass) to
		// verify.Stage1's 5-step result. We assert ALL must be "pass".
		assertAllStepsPass(t, respBody)
	})

	t.Run("AWSAuthConfig_Returns503_IAMRequired", func(t *testing.T) {
		restoreCfg := mutateArgoCDClusterSecretConfig(t, mgmtK8s, argocdHappyClusterName,
			[]byte(`{"awsAuthConfig":{"clusterName":"e2e-fake","roleARN":"arn:aws:iam::000000000000:role/e2e-fake"},"tlsClientConfig":{"insecure":true}}`))
		t.Cleanup(restoreCfg)

		status, respBody := admin.TestClusterConnectivity(t, argocdHappyClusterName, false)
		if status != http.StatusServiceUnavailable {
			t.Fatalf("Test endpoint: status=%d body=%v (expected 503 for awsAuthConfig path)", status, respBody)
		}
		assertErrorCode(t, respBody, providers.ArgoCDProviderCodeIAMRequired)
	})

	t.Run("ExecProviderConfig_Returns503_ExecUnsupported", func(t *testing.T) {
		restoreCfg := mutateArgoCDClusterSecretConfig(t, mgmtK8s, argocdHappyClusterName,
			[]byte(`{"execProviderConfig":{"command":"aws-iam-authenticator","apiVersion":"client.authentication.k8s.io/v1beta1"},"tlsClientConfig":{"insecure":true}}`))
		t.Cleanup(restoreCfg)

		status, respBody := admin.TestClusterConnectivity(t, argocdHappyClusterName, false)
		if status != http.StatusServiceUnavailable {
			t.Fatalf("Test endpoint: status=%d body=%v (expected 503 for execProviderConfig path)", status, respBody)
		}
		assertErrorCode(t, respBody, providers.ArgoCDProviderCodeExecUnsupported)
	})

	t.Run("UnrecognisedAuthShape_Returns503_UnsupportedAuth", func(t *testing.T) {
		// Empty config (no bearerToken / awsAuthConfig / execProviderConfig)
		// → ArgoCDProvider.GetCredentials hits the default switch arm and
		// returns ArgoCDProviderCodeUnsupportedAuth.
		restoreCfg := mutateArgoCDClusterSecretConfig(t, mgmtK8s, argocdHappyClusterName,
			[]byte(`{"tlsClientConfig":{"insecure":true}}`))
		t.Cleanup(restoreCfg)

		status, respBody := admin.TestClusterConnectivity(t, argocdHappyClusterName, false)
		if status != http.StatusServiceUnavailable {
			t.Fatalf("Test endpoint: status=%d body=%v (expected 503 for unsupported auth path)", status, respBody)
		}
		assertErrorCode(t, respBody, providers.ArgoCDProviderCodeUnsupportedAuth)
	})

	// Final invariant — sharko is still healthy after all subtests.
	if h := admin.Health(t); h.Status == "" {
		t.Errorf("final health: empty status: %+v", h)
	}

	// Touch ghmock so its t.Helper / cleanup path is exercised (and so
	// the import is not stripped). The register flow's PR list is
	// informational only.
	if prs := ghmock.ListMockPRs("all"); len(prs) > 0 {
		var titles []string
		for _, pr := range prs {
			titles = append(titles, pr.Title)
		}
		t.Logf("ghmock PRs: %s", strings.Join(titles, "; "))
	}
}

// ---------------------------------------------------------------------------
// helpers — local to this test (not exported into harness per Wave D's
// "use harness primitives, don't extend them" rule).
// ---------------------------------------------------------------------------

// seedHelmActiveConnection seeds an active connection whose argocd.server_url
// is the in-cluster service DNS (reachable from the Sharko pod). This is
// distinct from cluster_helpers.go::seedActiveConnection, which uses the
// host-side argoAccess.URL — fine for in-process Sharko, broken for
// helm-mode Sharko (the pod's DNS resolver doesn't know about 127.0.0.1).
//
// The git side stays gitfake-via-ghmock — the Sharko pod will attempt to
// reach gitfake.RepoURL when the kubeconfig register flow hits its commit
// step. If gitfake is bound to a host port that's not reachable from inside
// kind, the register flow may fail at the git commit step. That failure is
// captured by the t.Fatalf at the call site, with a hint pointing back here.
func seedHelmActiveConnection(t *testing.T, admin *harness.Client, gitfakeRepoURL, argoServerURL, argoToken string) {
	t.Helper()
	body := map[string]any{
		"name": "e2e-argocd-provider",
		"git": map[string]any{
			"provider": "github",
			"repo_url": gitfakeRepoURL,
			"owner":    "sharko-e2e",
			"repo":     "sharko-addons",
			"token":    "ghmock-test-token",
		},
		"argocd": map[string]any{
			"server_url": argoServerURL,
			"token":      argoToken,
			"namespace":  "argocd",
			"insecure":   true,
		},
		"set_as_default": true,
	}
	resp := admin.Do(t, http.MethodPost, "/api/v1/connections/", body)
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("seedHelmActiveConnection: create status=%d", resp.StatusCode)
	}
	resp2 := admin.Do(t, http.MethodPost, "/api/v1/connections/active",
		map[string]string{"connection_name": "e2e-argocd-provider"})
	resp2.Body.Close()
	if resp2.StatusCode < 200 || resp2.StatusCode >= 300 {
		t.Fatalf("seedHelmActiveConnection: activate status=%d", resp2.StatusCode)
	}
	t.Logf("harness/lifecycle: seeded helm-mode active connection [argo=%s]", argoServerURL)
}

// buildK8sClient returns a kubernetes.Interface bound to the supplied
// kubeconfig path. Mirrors harness.buildHelmK8sClient (which is unexported).
// Used to read + mutate ArgoCD cluster Secrets in the argocd namespace from
// the test process.
func buildK8sClient(t *testing.T, kubeconfigPath string) kubernetes.Interface {
	t.Helper()
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		t.Fatalf("buildK8sClient: BuildConfigFromFlags(%s): %v", kubeconfigPath, err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("buildK8sClient: NewForConfig: %v", err)
	}
	return cs
}

// waitForArgoCDClusterSecret polls the argocd namespace for an ArgoCD
// cluster Secret whose data["name"] equals clusterName. ArgoCD's cluster
// Secrets carry the label argocd.argoproj.io/secret-type=cluster; we filter
// on that to keep the list small.
//
// The helm-mode Sharko's argoSecretManager + the kubeconfig register flow
// race to write this Secret — whichever wins, the poll succeeds. Polls every
// 1s; t.Fatalf on timeout with diagnostic count of cluster Secrets seen.
func waitForArgoCDClusterSecret(t *testing.T, cs kubernetes.Interface, clusterName string, timeout time.Duration) *corev1.Secret {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastSeen int
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		list, err := cs.CoreV1().Secrets("argocd").List(ctx, metav1.ListOptions{
			LabelSelector: "argocd.argoproj.io/secret-type=cluster",
		})
		cancel()
		if err == nil {
			lastSeen = len(list.Items)
			for i := range list.Items {
				s := &list.Items[i]
				if string(s.Data["name"]) == clusterName {
					t.Logf("harness/lifecycle: argocd cluster Secret for %q found in argocd ns (name=%s)", clusterName, s.Name)
					return s
				}
			}
		}
		time.Sleep(1 * time.Second)
	}
	t.Fatalf("waitForArgoCDClusterSecret: cluster Secret for %q never appeared within %s (saw %d cluster-typed Secrets in argocd ns)",
		clusterName, timeout, lastSeen)
	return nil // unreachable
}

// mutateArgoCDClusterSecretConfig overwrites the named cluster Secret's
// data["config"] field with newConfig (a raw JSON document) and returns a
// restore func that puts the original config back on cleanup. Used by the
// typed-error subtests to inject awsAuthConfig / execProviderConfig / empty
// auth shapes without re-registering the cluster between subtests.
//
// The Secret is located by data["name"] equality (same lookup ArgoCDProvider
// uses). t.Fatalf on any K8s API failure or when the Secret is missing.
func mutateArgoCDClusterSecretConfig(t *testing.T, cs kubernetes.Interface, clusterName string, newConfig []byte) func() {
	t.Helper()
	secret := findArgoCDClusterSecret(t, cs, clusterName)
	originalConfig := append([]byte(nil), secret.Data["config"]...)
	originalName := secret.Name

	secret.Data["config"] = newConfig
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	_, err := cs.CoreV1().Secrets("argocd").Update(ctx, secret, metav1.UpdateOptions{})
	cancel()
	if err != nil {
		t.Fatalf("mutateArgoCDClusterSecretConfig: Update %s: %v", originalName, err)
	}
	t.Logf("harness/lifecycle: mutated cluster Secret %s/argocd config (orig-len=%d new-len=%d)",
		originalName, len(originalConfig), len(newConfig))

	return func() {
		// Re-fetch the Secret on restore so we pick up the latest
		// resourceVersion (Sharko's reconciler may have rewritten the
		// Secret in the interim — the conflict-free way is to re-read).
		current, err := findArgoCDClusterSecretByName(cs, originalName)
		if err != nil {
			t.Logf("harness/lifecycle: restore mutateArgoCDClusterSecretConfig: re-fetch %s failed: %v", originalName, err)
			return
		}
		current.Data["config"] = originalConfig
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := cs.CoreV1().Secrets("argocd").Update(ctx, current, metav1.UpdateOptions{}); err != nil {
			t.Logf("harness/lifecycle: restore mutateArgoCDClusterSecretConfig: %v", err)
		}
	}
}

// findArgoCDClusterSecret returns the ArgoCD cluster Secret whose data["name"]
// equals clusterName. t.Fatalf when not found.
func findArgoCDClusterSecret(t *testing.T, cs kubernetes.Interface, clusterName string) *corev1.Secret {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	list, err := cs.CoreV1().Secrets("argocd").List(ctx, metav1.ListOptions{
		LabelSelector: "argocd.argoproj.io/secret-type=cluster",
	})
	if err != nil {
		t.Fatalf("findArgoCDClusterSecret: List: %v", err)
	}
	for i := range list.Items {
		s := &list.Items[i]
		if string(s.Data["name"]) == clusterName {
			return s
		}
	}
	t.Fatalf("findArgoCDClusterSecret: no cluster Secret in argocd ns matches data[name]=%q (saw %d cluster-typed Secrets)",
		clusterName, len(list.Items))
	return nil // unreachable
}

// findArgoCDClusterSecretByName fetches a Secret by its K8s metadata.name in
// the argocd namespace. Returns the K8s NotFound error verbatim so callers
// can log-and-skip on cleanup.
func findArgoCDClusterSecretByName(cs kubernetes.Interface, secretName string) (*corev1.Secret, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := cs.CoreV1().Secrets("argocd").Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("secret %s/argocd not found: %w", secretName, err)
		}
		return nil, err
	}
	return s, nil
}

// assertAllStepsPass walks the testClusterResponse.steps array and fails the
// test with a step-by-step summary if any step's status != "pass".
//
// The shape (per internal/api/clusters_discover.go::testClusterResponse):
//
//	steps: [
//	  {name: "Fetch credentials",    status: "pass"},
//	  {name: "Fetch server version", status: "pass", detail: "v1.30.0"},
//	  {name: "Ensure namespace",     status: "pass", detail: "created"},
//	  {name: "Create test secret",   status: "pass"},
//	  {name: "Read back test secret",status: "pass"},
//	  {name: "Delete test secret",   status: "pass"},
//	]
func assertAllStepsPass(t *testing.T, body map[string]any) {
	t.Helper()
	steps, ok := body["steps"].([]any)
	if !ok || len(steps) == 0 {
		t.Fatalf("assertAllStepsPass: no steps in body: %v", body)
	}
	if len(steps) < 5 {
		// 5 from verify.Stage1 + 1 prepended by handleTestCluster = 6.
		// Allow >= 5 to stay loose against future step additions, but flag a
		// step count below the Stage1 baseline.
		t.Errorf("assertAllStepsPass: only %d steps (expected at least 5 from verify.Stage1 + the Fetch-credentials prepend)", len(steps))
	}
	for i, raw := range steps {
		s, _ := raw.(map[string]any)
		name, _ := s["name"].(string)
		status, _ := s["status"].(string)
		detail, _ := s["detail"].(string)
		if status != "pass" {
			t.Errorf("step %d (%q) status=%q (want pass) detail=%q", i, name, status, detail)
		}
	}
	if !t.Failed() {
		t.Logf("assertAllStepsPass: %d steps, all pass", len(steps))
	}
}

// assertErrorCode parses body["error_code"] and fails the test if it does
// not equal want. Captures body["error"] in the failure message so the
// human-readable detail is visible without re-running.
func assertErrorCode(t *testing.T, body map[string]any, want string) {
	t.Helper()
	got, _ := body["error_code"].(string)
	if got != want {
		errMsg, _ := body["error"].(string)
		// Pretty-print the body for diagnosability.
		raw, _ := json.Marshal(body)
		t.Errorf("error_code = %q, want %q (error=%q full=%s)", got, want, errMsg, raw)
	}
}
