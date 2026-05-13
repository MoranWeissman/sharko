// V125-1-6: orchestrator-side tracking-funnel tests.
//
// Verifies that every PR-creating orchestrator entry point now produces
// exactly ONE TrackPR call with the canonical OperationCode and the
// correct cluster/addon attribution. Without this funnel, the dashboard
// PR panel only saw addon-from-UI PRs (BUG-056).

package orchestrator

import (
	"context"
	"sync"
	"testing"
)

// stubPRTracker records every TrackPR call for assertion. Thread-safe
// because the orchestrator's git mutex serializes commitChanges, but
// the recorder still uses a mutex defensively in case future callers
// invoke TrackPR concurrently.
type stubPRTracker struct {
	mu    sync.Mutex
	calls []TrackedPR
}

func (s *stubPRTracker) TrackPR(_ context.Context, p TrackedPR) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, p)
	return nil
}

func (s *stubPRTracker) snapshot() []TrackedPR {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]TrackedPR, len(s.calls))
	copy(out, s.calls)
	return out
}

// TestRegisterCluster_TracksWithRegisterClusterOp confirms the new
// orchestrator-funneled TrackPR call fires once with the canonical
// "register-cluster" Operation. Pre-V125-1-6 the dashboard never saw
// register-cluster PRs because the API handler had no TrackPR call on
// that path.
func TestRegisterCluster_TracksWithRegisterClusterOp(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	tracker := &stubPRTracker{}

	orch := New(nil, defaultCreds(), argocd, git, defaultGitOps(), defaultPaths(), nil)
	orch.SetPRTracker(tracker)

	if _, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:   "prod-eu",
		Region: "eu-west-1",
	}); err != nil {
		t.Fatalf("RegisterCluster: %v", err)
	}

	calls := tracker.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 TrackPR call, got %d", len(calls))
	}
	if calls[0].Operation != "register-cluster" {
		t.Errorf("Operation: got %q want register-cluster", calls[0].Operation)
	}
	if calls[0].Cluster != "prod-eu" {
		t.Errorf("Cluster: got %q want prod-eu", calls[0].Cluster)
	}
}

// TestAddAddon_TracksWithAddonAddOp covers the addon catalog entry
// point. Pre-V125-1-6 this WAS tracked at the handler layer (one of the
// few cases that worked); the assertion now lives at the orchestrator
// layer to prove the funnel still fires after we removed the redundant
// handler call.
func TestAddAddon_TracksWithAddonAddOp(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	tracker := &stubPRTracker{}

	// Seed addons-catalog.yaml with the structure AddAddon expects.
	// The mutator looks for a bare `applicationsets:` line, not the
	// inline-list shorthand `applicationsets: []`.
	git.files["configuration/addons-catalog.yaml"] = []byte("applicationsets:\n")

	orch := New(nil, nil, argocd, git, defaultGitOps(), defaultPaths(), nil)
	orch.SetPRTracker(tracker)

	if _, err := orch.AddAddon(context.Background(), AddAddonRequest{
		Name:      "metrics-server",
		Chart:     "metrics-server",
		RepoURL:   "https://kubernetes-sigs.github.io/metrics-server/",
		Version:   "3.12.1",
		Namespace: "kube-system",
	}); err != nil {
		t.Fatalf("AddAddon: %v", err)
	}

	calls := tracker.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 TrackPR call, got %d", len(calls))
	}
	if calls[0].Operation != "addon-add" {
		t.Errorf("Operation: got %q want addon-add", calls[0].Operation)
	}
	if calls[0].Addon != "metrics-server" {
		t.Errorf("Addon: got %q want metrics-server", calls[0].Addon)
	}
}

// TestUpgradeAddonGlobal_TracksWithAddonUpgradeOp verifies global
// upgrades land under the canonical "addon-upgrade" Operation.
func TestUpgradeAddonGlobal_TracksWithAddonUpgradeOp(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	tracker := &stubPRTracker{}

	// Seed a catalog with the addon to upgrade.
	git.files["configuration/addons-catalog.yaml"] = []byte("addons:\n- name: metrics-server\n  chart: metrics-server\n  repoUrl: https://example.com\n  version: 3.12.0\n")

	orch := New(nil, nil, argocd, git, defaultGitOps(), defaultPaths(), nil)
	orch.SetPRTracker(tracker)

	if _, err := orch.UpgradeAddonGlobal(context.Background(), "metrics-server", "3.12.1"); err != nil {
		t.Fatalf("UpgradeAddonGlobal: %v", err)
	}

	calls := tracker.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 TrackPR call, got %d", len(calls))
	}
	if calls[0].Operation != "addon-upgrade" {
		t.Errorf("Operation: got %q want addon-upgrade", calls[0].Operation)
	}
	if calls[0].Addon != "metrics-server" {
		t.Errorf("Addon: got %q want metrics-server", calls[0].Addon)
	}
}

// TestNoTracker_NoOp verifies that omitting SetPRTracker (the default
// constructor state) does NOT cause a panic or error in the PR-creating
// path. Test seam.
func TestNoTracker_NoOp(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	orch := New(nil, defaultCreds(), argocd, git, defaultGitOps(), defaultPaths(), nil)
	// No SetPRTracker call.

	if _, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:   "prod-eu",
		Region: "eu-west-1",
	}); err != nil {
		t.Fatalf("RegisterCluster (no tracker): %v", err)
	}
}
