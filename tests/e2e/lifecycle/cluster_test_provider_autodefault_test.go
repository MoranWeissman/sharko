//go:build e2e

package lifecycle

import (
	"net/http"
	"os/exec"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/tests/e2e/harness"
)

// TestClusterTest_ProviderAutoDefault_HappyPath is the V125-1-13.5 regression
// pin for V125-1-10.7's serve.go ungate (commit f0e2b314).
//
// The bug it catches: pre-V125-1-10.7, cmd/sharko/serve.go gated the call to
// providers.New() on `connProv.Type != ""`. That gate silently bypassed the
// in-cluster auto-default path V125-1-10.2 added inside providers.New() — when
// no provider was configured AND in-cluster K8s was detected, providers.New()
// is supposed to return an ArgoCDProvider so dev installs (helm into kind)
// work out of the box without an explicit Settings step. The gate prevented
// providers.New() from ever being called in that scenario, leaving
// credProvider nil. The user-visible surface was the Test endpoint returning
// HTTP 503 with error_code=no_secrets_backend on a fresh helm install.
//
// V125-1-10.7's fix removed the gate so providers.New() is always called,
// allowing the auto-default to fire. This test proves the fix is in place
// end-to-end against a real helm-installed Sharko in a kind cluster:
//
//  1. Provision a kind mgmt cluster + install ArgoCD.
//  2. Helm-install Sharko with NO Provider configuration (no HelmOverrides
//     setting provider.* values; the chart's default empty Provider config
//     is what we want — it forces the in-cluster auto-default branch).
//  3. Hit GET /api/v1/providers — assert configured_provider is non-nil.
//
// Pre-V125-1-10.7 the Settings were empty AND the gate prevented
// providers.New() from being called → credProvider stayed nil →
// srv.providerCfg stayed nil → GET /providers would return
// {"configured_provider": null}. Post-fix, the auto-default fires inside
// providers.New() (because rest.InClusterConfig() succeeds in the helm-
// installed pod), credProvider is set to an ArgoCDProvider, and
// configured_provider is non-nil with status="connected" or "configured".
//
// Assertion approach: introspection (GET /api/v1/providers). The indirect
// behavioural path (register target, POST /clusters/{name}/test, expect 200)
// would also work but adds substantial fragility — git reachability from the
// in-cluster pod to a localhost-bound GitFake, kubeconfig-from-target-into-
// mgmt routing, ArgoCD secret round-trip — none of which the regression itself
// requires us to exercise. The /providers endpoint exposes exactly the state
// the bug corrupted (credProvider nil vs non-nil) with the fewest moving
// parts.
//
// Mode requirement: SharkoModeHelm only. The in-process boot path cannot
// satisfy rest.InClusterConfig() (the test binary is not running inside a
// K8s pod), so the auto-default branch never fires there — testing this in
// SharkoModeInProcess would silently always-pass-with-zero-coverage.
func TestClusterTest_ProviderAutoDefault_HappyPath(t *testing.T) {
	// ---- prereq guards: kind/kubectl/docker/helm + docker daemon ----
	if _, err := exec.LookPath("kind"); err != nil {
		t.Skip("kind not installed; install via `brew install kind` or https://kind.sigs.k8s.io/")
	}
	if _, err := exec.LookPath("kubectl"); err != nil {
		t.Skip("kubectl not installed")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not installed (required by kind)")
	}
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not installed; install via `brew install helm` (required by SharkoModeHelm)")
	}
	if out, err := exec.Command("docker", "info").CombinedOutput(); err != nil {
		t.Skipf("docker daemon not reachable: %v\noutput: %s", err, out)
	}

	// ---- safety: clean up stale e2e clusters from prior failed runs ----
	harness.DestroyAllStaleE2EClusters(t)

	// ---- provision topology: 1 mgmt is enough — the regression is about
	//      the in-cluster auto-default firing inside Sharko's own pod ----
	t.Logf("provisioning kind topology (1 mgmt) — typically 60-90s")
	clusters := harness.ProvisionTopology(t, harness.ProvisionRequest{NumTargets: 0})
	t.Cleanup(func() { harness.DestroyTopology(t, clusters) })
	mgmt := clusters[0]

	harness.WaitClusterReady(t, mgmt, 90*time.Second)

	// ArgoCD must be installed before Sharko comes up — ArgoCDProvider's
	// HealthCheck will probe the argocd namespace and the GET /providers
	// status field reports "error" if it can't reach it.
	t.Logf("installing argocd into management cluster (%s)", mgmt.Name)
	harness.InstallArgoCD(t, mgmt)

	// ---- start Sharko via Helm with NO provider config ----
	// The chart's default values leave Provider unset; we deliberately pass
	// an empty HelmOverrides (any provider.* key would defeat the test by
	// bypassing the auto-default). GitFake is required by StartSharko but
	// the in-cluster Sharko pod does not need to reach it for this test —
	// the introspection endpoint is read-only and does not trigger any git
	// fetch.
	gitfake := harness.StartGitFake(t)

	sharko := harness.StartSharko(t, harness.SharkoConfig{
		Mode:        harness.SharkoModeHelm,
		MgmtCluster: &mgmt,
		GitFake:     gitfake,
		// HelmOverrides: nil — leave the chart's default empty Provider so
		// connSvc.GetProviderConfig() returns nil/Type="" inside the pod.
	})
	sharko.WaitHealthy(t, 60*time.Second)

	admin := harness.NewClient(t, sharko)

	// ---- the assertion: GET /api/v1/providers must report a configured
	//      provider, NOT configured_provider:null ----
	//
	// Pre-V125-1-10.7: with no provider in connection AND the connProv.Type
	// != "" gate in serve.go, providers.New() was never called → credProvider
	// stayed nil → srv.providerCfg stayed nil → handleGetProviders short-
	// circuits to {"configured_provider": null}.
	//
	// Post-fix: providers.New() is always called → in-cluster auto-default
	// fires (rest.InClusterConfig() succeeds in the pod) → ArgoCDProvider
	// installed → srv.providerCfg + srv.credProvider both non-nil →
	// handleGetProviders returns a populated configured_provider object with
	// a status field reporting HealthCheck result.
	var resp map[string]any
	admin.GetJSON(t, "/api/v1/providers", &resp)

	configured, ok := resp["configured_provider"]
	if !ok {
		t.Fatalf("GET /api/v1/providers: response missing configured_provider key: %v", resp)
	}
	if configured == nil {
		t.Fatalf("GET /api/v1/providers: configured_provider is null — the V125-1-10.7 ungate fix is NOT in effect; "+
			"providers.New() was not called for the empty-Type connection so credProvider stayed nil. "+
			"Full response: %v", resp)
	}
	provInfo, ok := configured.(map[string]any)
	if !ok {
		t.Fatalf("GET /api/v1/providers: configured_provider is not an object: %T = %v", configured, configured)
	}

	// Status must be present and not "error" — proves credProvider was
	// constructed AND its HealthCheck call succeeded against the in-cluster
	// argocd namespace. "configured" (skipped HealthCheck — credProvider was
	// nil) would also indicate a regression, so we require "connected".
	status, _ := provInfo["status"].(string)
	switch status {
	case "connected":
		t.Logf("GET /api/v1/providers: configured_provider OK [type=%v region=%v status=%s] — "+
			"V125-1-10.7 ungate verified (auto-default ArgoCDProvider fired in-cluster)",
			provInfo["type"], provInfo["region"], status)
	case "error":
		// credProvider WAS set (auto-default fired) but its HealthCheck
		// failed — that's not the V125-1-10.7 regression itself, but it's
		// a real failure on the test stack (e.g. ArgoCD didn't come up
		// cleanly in mgmt). Surface as fail, not skip — silent skips on a
		// regression test would defeat the purpose.
		t.Fatalf("GET /api/v1/providers: configured_provider has status=error (HealthCheck failed against argocd ns): %v",
			provInfo)
	case "configured":
		// "configured" is the s.credProvider==nil branch in
		// handleGetProviders — i.e. providerCfg is set but credProvider
		// isn't. That's the exact pre-V125-1-10.7 partial-state we MUST
		// fail on (the gate's wreckage).
		t.Fatalf("GET /api/v1/providers: status=configured (credProvider is nil despite providerCfg being set) — "+
			"V125-1-10.7 ungate is NOT fully in effect: %v", provInfo)
	default:
		t.Fatalf("GET /api/v1/providers: unexpected status=%q in configured_provider: %v", status, provInfo)
	}

	// Final invariant — Sharko is still healthy after the introspection probe.
	h := admin.Health(t)
	if h.Status == "" {
		t.Errorf("final health: empty status: %+v", h)
	}

	// Touch the Do helper once with a HEAD-equivalent shape so a future
	// reviewer can see the typed client surface is fully wired in helm mode
	// (every other lifecycle test asserts this implicitly via cluster
	// register flows). Keeps the test minimal but proves more than a single
	// GET would.
	resp2 := admin.Do(t, http.MethodGet, "/api/v1/health", nil)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("GET /api/v1/health round-trip: status=%d, want 200", resp2.StatusCode)
	}
}
