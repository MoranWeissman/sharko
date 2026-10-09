package orchestrator

import (
	"context"
	"sync"
	"testing"

	"github.com/MoranWeissman/sharko/internal/providers"
)

// V125-1-8.3 behavioral parity tests — every register/refresh path must
// hand off to the V125-1-8 reconciler instead of calling the direct ArgoCD
// API. The design doc §12 V125-1-8 step 5 names this as "the hardest part"
// of the architectural close because the bug class (BUG-058 orphan-on-
// PR-close) shipped from the inverse contract.
//
// These tests pin two invariants on every path:
//
//  1. `argocd.registeredClusters` stays empty — the orchestrator MUST NOT
//     dispatch ArgoCD.RegisterCluster directly (the reconciler owns it,
//     post-merge, via argosecrets.Manager.Ensure).
//  2. The triggerFn seam fires exactly once — wired in V125-1-8.4 to
//     reconciler.Trigger() so post-merge convergence is immediate
//     rather than 30s-tick-delayed.
//
// Together these guarantee the "PR is the Gate" semantics: nothing exists
// in the argocd namespace before the managed-clusters.yaml PR merges.

// reconcilerProbe is a thread-safe counter for trigger seam invocations.
type reconcilerProbe struct {
	mu    sync.Mutex
	calls int
}

func (p *reconcilerProbe) trigger() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
}

func (p *reconcilerProbe) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// TestRegisterCluster_Kubeconfig_NoPreCreateSecret pins the kubeconfig
// path — the historic BUG-058 trigger. Pre-V125-1-8.3 this fell through
// to a direct ArgoCD API RegisterCluster call when argoSecretManager was
// nil. Post-V125-1-8.3 the call is gone; the reconciler picks up the new
// managed-clusters.yaml entry via the trigger seam.
func TestRegisterCluster_Kubeconfig_NoPreCreateSecret(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	probe := &reconcilerProbe{}
	orch.SetReconcilerTrigger(probe.trigger)

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:       "kind-sharko",
		Provider:   "kubeconfig",
		Kubeconfig: v125TestBearerKubeconfig,
		Addons:     map[string]bool{"cert-manager": true},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected success, got %q (error: %s)", result.Status, result.Error)
	}
	if len(argocd.registeredClusters) != 0 {
		t.Errorf("V125-1-8.3: kubeconfig path must NOT call ArgoCD.RegisterCluster (reconciler owns it); got %d direct register calls", len(argocd.registeredClusters))
	}
	if probe.count() != 1 {
		t.Errorf("expected reconciler trigger to fire exactly once, got %d", probe.count())
	}
}

// TestRegisterCluster_EKSDirect_NoPreCreateSecret pins the EKS/AWS-SM
// credentials path (provider unset; falls through to credProvider.
// GetCredentials). Auto-merge ON exercises the old Step 3b
// argoSecretManager.Ensure pre-merge call site.
func TestRegisterCluster_EKSDirect_NoPreCreateSecret(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	creds := &mockCredProvider{
		creds: map[string]*providers.Kubeconfig{
			"prod-eu": {
				Server: "https://eks.example.com:443",
				CAData: []byte("ca"),
				Token:  "tok",
			},
		},
	}

	orch := New(nil, creds, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	probe := &reconcilerProbe{}
	orch.SetReconcilerTrigger(probe.trigger)

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:   "prod-eu",
		Region: "eu-west-1",
		Addons: map[string]bool{"datadog": true},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected success, got %q (error: %s)", result.Status, result.Error)
	}
	if len(argocd.registeredClusters) != 0 {
		t.Errorf("V125-1-8.3: EKS-direct path must NOT call ArgoCD.RegisterCluster (reconciler owns it); got %d direct register calls", len(argocd.registeredClusters))
	}
	if probe.count() != 1 {
		t.Errorf("expected reconciler trigger to fire exactly once, got %d", probe.count())
	}
}

// TestRegisterCluster_EKSDiscovery_NoPreCreateSecret mirrors the EKS
// path with PR mode (manual merge). Pre-V125-1-8.3 this skipped the
// pre-merge Ensure (path 2 in design doc §2) but the same architectural
// fix — defer ALL Secret writes to the reconciler — applies. Auto-merge
// false MUST also fire the trigger so a manually-merged PR still nudges
// the reconciler instead of waiting for the 30s tick.
func TestRegisterCluster_EKSDiscovery_NoPreCreateSecret(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	creds := &mockCredProvider{
		creds: map[string]*providers.Kubeconfig{
			"prod-us": {
				Server: "https://us.eks.example.com:443",
				CAData: []byte("ca"),
				Token:  "tok",
			},
		},
	}

	orch := New(nil, creds, argocd, git, defaultGitOps(), defaultPaths(), nil)
	probe := &reconcilerProbe{}
	orch.SetReconcilerTrigger(probe.trigger)

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:   "prod-us",
		Region: "us-east-1",
		Addons: map[string]bool{"datadog": true},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected success, got %q (error: %s)", result.Status, result.Error)
	}
	if len(argocd.registeredClusters) != 0 {
		t.Errorf("V125-1-8.3: EKS-discovery path must NOT call ArgoCD.RegisterCluster (reconciler owns it); got %d direct register calls", len(argocd.registeredClusters))
	}
	if probe.count() != 1 {
		t.Errorf("expected reconciler trigger to fire exactly once for manual-merge path, got %d", probe.count())
	}
}

// TestRegisterCluster_Batch_NoPreCreateSecret exercises the batch path.
// The batch operation loops RegisterCluster N times; the trigger MUST
// fire N times so the reconciler picks up each PR (auto-merge here, so
// trigger nudges immediately after each Secret should land).
func TestRegisterCluster_Batch_NoPreCreateSecret(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	creds := &mockCredProvider{
		creds: map[string]*providers.Kubeconfig{
			"prod-eu": {Server: "https://eu.example.com:443", CAData: []byte("ca"), Token: "tok"},
			"prod-us": {Server: "https://us.example.com:443", CAData: []byte("ca"), Token: "tok"},
			"staging": {Server: "https://stg.example.com:443", CAData: []byte("ca"), Token: "tok"},
		},
	}

	orch := New(nil, creds, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	probe := &reconcilerProbe{}
	orch.SetReconcilerTrigger(probe.trigger)

	requests := []RegisterClusterRequest{
		{Name: "prod-eu", Addons: map[string]bool{"datadog": true}, Region: "eu-west-1"},
		{Name: "prod-us", Addons: map[string]bool{"datadog": true}, Region: "us-east-1"},
		{Name: "staging", Addons: map[string]bool{"keda": true}, Region: "eu-west-1"},
	}
	result := orch.RegisterClusterBatch(context.Background(), requests)
	if result.Succeeded != 3 {
		t.Errorf("expected 3 successes, got %d (failed=%d)", result.Succeeded, result.Failed)
	}
	if len(argocd.registeredClusters) != 0 {
		t.Errorf("V125-1-8.3: batch path must NOT call ArgoCD.RegisterCluster (reconciler owns it); got %d direct register calls", len(argocd.registeredClusters))
	}
	if probe.count() != 3 {
		t.Errorf("expected reconciler trigger to fire 3 times (one per batch member), got %d", probe.count())
	}
}

// TestRefreshClusterCredentials_NoPreCreateSecret confirms the credentials-
// refresh path also hands off to the reconciler instead of calling
// ArgoCD.RegisterCluster directly (the pre-V125-1-8.3 implementation
// upserted the Secret via ArgoCD's API in-process).
func TestRefreshClusterCredentials_NoPreCreateSecret(t *testing.T) {
	argocd := newMockArgocd()
	creds := &mockCredProvider{
		creds: map[string]*providers.Kubeconfig{
			"prod-eu": {
				Server: "https://k8s.example.com:6443",
				CAData: []byte("new-ca"),
				Token:  "new-tok",
			},
		},
	}
	orch := New(nil, creds, argocd, newMockGitProvider(), autoMergeGitOps(), defaultPaths(), nil)
	probe := &reconcilerProbe{}
	orch.SetReconcilerTrigger(probe.trigger)

	if err := orch.RefreshClusterCredentials(context.Background(), "prod-eu", "https://k8s.example.com:6443"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(argocd.registeredClusters) != 0 {
		t.Errorf("V125-1-8.3: refresh must NOT call ArgoCD.RegisterCluster (reconciler owns it); got %d direct register calls", len(argocd.registeredClusters))
	}
	if probe.count() != 1 {
		t.Errorf("expected reconciler trigger to fire exactly once, got %d", probe.count())
	}
}

// TestRegisterCluster_TriggerSeam_NilByDefault confirms the seam is
// genuinely optional — an orchestrator without SetReconcilerTrigger
// configured must still register clusters (the periodic 30s tick is the
// safety net). Tests + back-compat callers that never set the trigger
// rely on this no-op behaviour.
func TestRegisterCluster_TriggerSeam_NilByDefault(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	orch := New(nil, defaultCreds(), argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	// Deliberately DO NOT call SetReconcilerTrigger.

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:   "prod-eu",
		Addons: map[string]bool{"cert-manager": true},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected success, got %q", result.Status)
	}
}
