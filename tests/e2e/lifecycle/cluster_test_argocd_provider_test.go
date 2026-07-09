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
//  2. Typed-error paths (Story 10.3, upgraded by V2-cleanup-88.2): mutating
//     the live cluster Secret's data["config"] field to inject awsAuthConfig /
//     execProviderConfig / unrecognised auth shapes routes through
//     ArgoCDProviderError and surfaces as HTTP 503 with stable error_code
//     values. Since V2-cleanup-88.2, Sharko PARSES the AWS shapes
//     (awsAuthConfig, and execProviderConfig for the known AWS authenticators
//     argocd-k8s-auth/aws + aws-iam-authenticator) and attempts to mint an EKS
//     token with its OWN AWS identity — it never shells out to the exec
//     plugin. The kind-based e2e Sharko pod has no AWS identity at all, so
//     that mint attempt always fails, and both AWS shapes still surface
//     argocd_provider_iam_required (the code's meaning shifted from "shape
//     rejected outright" to "shape parsed, but Sharko has no usable AWS
//     identity to mint with" — see internal/providers/argocd_provider.go):
//       - awsAuthConfig                              → argocd_provider_iam_required
//       - execProviderConfig, known AWS authenticator → argocd_provider_iam_required
//       - execProviderConfig, unrecognised command    → argocd_provider_exec_unsupported
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

	// ---- start gitfake + ghmock (kept for symmetry with sibling tests) ----
	// gitfake is not consumed by this test's Sharko pod — see the
	// "structural blocker" note below for the V125-1-13.x.6 rewrite that
	// bypasses Sharko's git-touching RegisterCluster flow entirely. Kept
	// alive so any future test that DOES want git can rely on the same
	// harness primitive being present.
	gitfake := harness.StartGitFake(t)
	ghmock := harness.StartGitMock(t)
	_ = gitfake
	_ = ghmock

	// ---- boot Sharko via Helm (Wave C wiring) ----
	// SharkoModeHelm requires cfg.MgmtCluster; pass a pointer into the
	// clusters slice (ProvisionTopology returns []KindCluster by value).
	t.Logf("installing sharko into mgmt cluster via helm (~30-90s depending on docker build cache)")
	sharko := harness.StartSharko(t, harness.SharkoConfig{
		Mode:        harness.SharkoModeHelm,
		MgmtCluster: &mgmt,
		GitFake:     gitfake,
	})
	sharko.WaitHealthy(t, 30*time.Second)

	// Build a typed API client. NewClient picks up sharko.AdminUser /
	// sharko.AdminPass which Wave B's bootstrapHelmSharkoAuth populated
	// from sharko-initial-admin-secret in the kind cluster.
	admin := harness.NewClient(t, sharko)

	// ---- host-side ArgoCD access (port-forward + JWT) ----
	// Used to register the target cluster directly in ArgoCD via REST API
	// in the V125-1-13.x.6 rewrite (see registerClusterInArgoCDDirect).
	argoAccess := startArgoCDAccess(t, mgmt)

	// ---- V125-1-13.x.6 — register cluster directly in ArgoCD (bypass Sharko) ----
	//
	// Why this bypasses Sharko's POST /api/v1/clusters:
	//
	// Sharko's RegisterCluster path goes through Git (commitChangesWithMeta →
	// CreateBranch / BatchCreateFiles / CreatePullRequest / MergePullRequest).
	// Sharko's GitHubProvider uses the go-github REST client hard-wired to
	// api.github.com — the in-cluster gitfake Service speaks the git smart-HTTP
	// wire protocol, NOT GitHub's REST API, so an in-pod Sharko can never
	// satisfy its own git operations against the gitfake. The whitelisted
	// gitfake URL in SHARKO_E2E_GIT_HOSTS_ALLOWLIST gets the connection past
	// URL validation, but the subsequent BatchCreateFiles / CreatePullRequest
	// calls still hit api.github.com (404 → register flow returns "partial"
	// without reaching Step 6's ArgoCD register) → no cluster Secret in argocd
	// ns → this test hangs at waitForArgoCDClusterSecret.
	//
	// Direct ArgoCD registration via REST API lands the same bearerToken-shape
	// cluster Secret in the argocd namespace that Sharko's Step 6 would have
	// produced. The auto-default ArgoCDProvider inside the Sharko pod then
	// satisfies the Test endpoint subtests against that real Secret, which is
	// exactly the contract V125-1-10.3 / V125-1-13.4 was built to pin.
	//
	// This DOES forgo coverage of Sharko's own git-side register flow in
	// helm-mode, but that coverage already exists in the in-process e2e suite
	// (cluster_test.go::RegisterManagedCluster) where ghmock can intercept
	// the api.github.com calls. The helm-mode coverage here is specifically
	// about the auto-default ArgoCDProvider + the typed-error paths against a
	// real K8s API, neither of which involve git.
	t.Logf("registering target cluster %q directly in ArgoCD (bypassing Sharko git flow)", argocdHappyClusterName)
	registerClusterInArgoCDDirect(t, argoAccess, target, argocdHappyClusterName)

	// ---- wait for ArgoCD's cluster Secret to land in argocd ns ----
	// The Secret write is the load-bearing precondition for the Test
	// endpoint — without it ArgoCDProvider.findClusterSecret returns
	// NotFound. ArgoCD's POST /api/v1/clusters writes the Secret synchronously,
	// so this poll is fast (~1s) — kept as a safety net for any race.
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
		// V2-cleanup-88.2: Sharko now PARSES this shape and attempts to mint
		// an EKS token with its own AWS identity (never shelling out). The
		// kind-based e2e Sharko pod has no AWS identity at all, so the mint
		// attempt fails and this still surfaces argocd_provider_iam_required
		// — same code as before, now meaning "parsed, but no usable AWS
		// identity" rather than "shape rejected outright".
		restoreCfg := mutateArgoCDClusterSecretConfig(t, mgmtK8s, argocdHappyClusterName,
			[]byte(`{"awsAuthConfig":{"clusterName":"e2e-fake","roleARN":"arn:aws:iam::000000000000:role/e2e-fake"},"tlsClientConfig":{"insecure":true}}`))
		t.Cleanup(restoreCfg)

		status, respBody := admin.TestClusterConnectivity(t, argocdHappyClusterName, false)
		if status != http.StatusServiceUnavailable {
			t.Fatalf("Test endpoint: status=%d body=%v (expected 503 for awsAuthConfig path)", status, respBody)
		}
		assertErrorCode(t, respBody, providers.ArgoCDProviderCodeIAMRequired)
	})

	t.Run("KnownAWSExecProviderConfig_Returns503_IAMRequired", func(t *testing.T) {
		// V2-cleanup-88.2: aws-iam-authenticator is now a RECOGNIZED AWS
		// authenticator — Sharko parses --cluster-name/--role-arn/AWS_REGION
		// out of the exec args/env and attempts the same own-identity mint
		// (never executing the plugin). Same reasoning as the awsAuthConfig
		// subtest above: no AWS identity on the e2e pod → mint fails →
		// argocd_provider_iam_required, NOT argocd_provider_exec_unsupported
		// (that code is now reserved for genuinely unrecognized commands —
		// see the next subtest).
		restoreCfg := mutateArgoCDClusterSecretConfig(t, mgmtK8s, argocdHappyClusterName,
			[]byte(`{"execProviderConfig":{"command":"aws-iam-authenticator","apiVersion":"client.authentication.k8s.io/v1beta1"},"tlsClientConfig":{"insecure":true}}`))
		t.Cleanup(restoreCfg)

		status, respBody := admin.TestClusterConnectivity(t, argocdHappyClusterName, false)
		if status != http.StatusServiceUnavailable {
			t.Fatalf("Test endpoint: status=%d body=%v (expected 503 for known-AWS execProviderConfig path)", status, respBody)
		}
		assertErrorCode(t, respBody, providers.ArgoCDProviderCodeIAMRequired)
	})

	t.Run("UnknownExecProviderConfig_Returns503_ExecUnsupported", func(t *testing.T) {
		// A command Sharko does NOT recognize as an AWS authenticator (e.g. a
		// GCP helper) is still rejected outright — Sharko never shells out to
		// any exec-plugin binary, known or not.
		restoreCfg := mutateArgoCDClusterSecretConfig(t, mgmtK8s, argocdHappyClusterName,
			[]byte(`{"execProviderConfig":{"command":"gke-gcloud-auth-plugin","apiVersion":"client.authentication.k8s.io/v1beta1"},"tlsClientConfig":{"insecure":true}}`))
		t.Cleanup(restoreCfg)

		status, respBody := admin.TestClusterConnectivity(t, argocdHappyClusterName, false)
		if status != http.StatusServiceUnavailable {
			t.Fatalf("Test endpoint: status=%d body=%v (expected 503 for unrecognized execProviderConfig path)", status, respBody)
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
