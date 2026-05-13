package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/models"
)

// V125-1-7 / BUG-058 — orphan-cluster resolver behaviour.
//
// Pinned contracts:
//
//  1. ArgoCD clusters with no managed-clusters.yaml entry AND no pending
//     register PR are surfaced as orphans.
//  2. Pending-but-not-in-git clusters are excluded from orphans (they're
//     pending, not orphans).
//  3. The in-cluster entry is always excluded.
//  4. ArgoCD list errors degrade to empty + warn (V124-22 pattern), never
//     a 500.
//  5. nil lister returns non-nil empty (V125-1.4 nil-array regression
//     guard).

// fakeArgoLister is the minimal argocd lister surface the resolver needs.
// We keep it local to the test file so a future ArgoCD interface change
// doesn't accidentally drag this fake along.
type fakeArgoLister struct {
	clusters []models.ArgocdCluster
	err      error
}

func (f *fakeArgoLister) ListClusters(_ context.Context) ([]models.ArgocdCluster, error) {
	return f.clusters, f.err
}

// Compile-time assertion the fake satisfies the resolver's interface.
var _ argocdClusterLister = (*fakeArgoLister)(nil)

func TestResolveOrphanRegistrations_NoOrphansWhenAllManaged(t *testing.T) {
	// 1 ArgoCD cluster + same name in git → no orphan.
	lister := &fakeArgoLister{
		clusters: []models.ArgocdCluster{
			{Name: "prod-eu", Server: "https://prod-eu.example.com"},
		},
	}
	gitClusters := []models.Cluster{{Name: "prod-eu"}}

	got := resolveOrphanRegistrations(context.Background(), lister, gitClusters, nil)
	if got == nil {
		t.Fatal("expected non-nil empty slice (V125-1.4 nil-array regression guard)")
	}
	if len(got) != 0 {
		t.Errorf("expected 0 orphans for fully-managed cluster, got %d: %+v", len(got), got)
	}
}

func TestResolveOrphanRegistrations_SingleOrphanDetected(t *testing.T) {
	// 1 ArgoCD cluster + 0 git + 0 pending → 1 orphan (the V125-1-7
	// reproducer: maintainer closed the register PR without merging,
	// leaving the ArgoCD cluster Secret behind).
	lister := &fakeArgoLister{
		clusters: []models.ArgocdCluster{
			{Name: "kind-local", Server: "https://kind-local.local:6443"},
		},
	}

	got := resolveOrphanRegistrations(context.Background(), lister, nil, nil)
	if len(got) != 1 {
		t.Fatalf("expected 1 orphan, got %d: %+v", len(got), got)
	}
	if got[0].ClusterName != "kind-local" {
		t.Errorf("orphan cluster_name = %q, want %q", got[0].ClusterName, "kind-local")
	}
	if got[0].ServerURL != "https://kind-local.local:6443" {
		t.Errorf("orphan server_url = %q", got[0].ServerURL)
	}
	if got[0].LastSeenAt == "" {
		t.Error("expected non-empty last_seen_at (resolver-call-time fallback)")
	}
	// Sanity: last_seen_at must parse as RFC3339.
	if _, err := time.Parse(time.RFC3339, got[0].LastSeenAt); err != nil {
		t.Errorf("last_seen_at not RFC3339-parseable: %q (%v)", got[0].LastSeenAt, err)
	}
}

func TestResolveOrphanRegistrations_PendingExcludedFromOrphans(t *testing.T) {
	// A cluster in ArgoCD that ALSO has an open register PR is pending,
	// not orphan. It must NOT show up in the orphan list — that surface
	// belongs exclusively to V125-1.5's PendingRegistrations resolver.
	lister := &fakeArgoLister{
		clusters: []models.ArgocdCluster{
			{Name: "kind-local", Server: "https://kind-local.local:6443"},
		},
	}
	pending := map[string]struct{}{"kind-local": {}}

	got := resolveOrphanRegistrations(context.Background(), lister, nil, pending)
	if len(got) != 0 {
		t.Errorf("expected pending cluster to be excluded from orphans, got %d: %+v", len(got), got)
	}
}

func TestResolveOrphanRegistrations_InClusterExcluded(t *testing.T) {
	// The in-cluster entry is Sharko's own host cluster. It is never an
	// orphan even though it has no managed-clusters.yaml entry.
	lister := &fakeArgoLister{
		clusters: []models.ArgocdCluster{
			{Name: "in-cluster", Server: "https://kubernetes.default.svc"},
			// Test the prefix path explicitly — a custom name that
			// nonetheless points at https://kubernetes.default... must
			// also be skipped (the orchestrator uses `https://kubernetes.default`
			// as its host-cluster sigil).
			{Name: "host", Server: "https://kubernetes.default.svc.cluster.local"},
			// And one real orphan, just to confirm the rest of the
			// algorithm still runs.
			{Name: "real-orphan", Server: "https://real.example.com"},
		},
	}

	got := resolveOrphanRegistrations(context.Background(), lister, nil, nil)
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 orphan (in-cluster + host filtered out), got %d: %+v", len(got), got)
	}
	if got[0].ClusterName != "real-orphan" {
		t.Errorf("expected orphan = real-orphan, got %q", got[0].ClusterName)
	}
}

func TestResolveOrphanRegistrations_ListErrorDegradesToEmpty(t *testing.T) {
	// V124-22 / V125-1.5 dignified-degrade pattern: a transient ArgoCD
	// blip MUST NOT 500 the entire /clusters endpoint. The resolver
	// swallows the error, logs a warning, and returns the same non-nil
	// empty slice it would on the no-orphans happy path.
	lister := &fakeArgoLister{err: errors.New("argocd unreachable (transient)")}

	got := resolveOrphanRegistrations(context.Background(), lister, nil, nil)
	if got == nil {
		t.Fatal("expected non-nil empty slice on lister error")
	}
	if len(got) != 0 {
		t.Errorf("expected 0 orphans on lister error, got %d", len(got))
	}
}

func TestResolveOrphanRegistrations_NilListerReturnsEmpty(t *testing.T) {
	// Defensive: handler may pass a nil lister if no active connection.
	// The resolver must not crash.
	got := resolveOrphanRegistrations(context.Background(), nil, nil, nil)
	if got == nil {
		t.Fatal("expected non-nil empty slice on nil lister")
	}
	if len(got) != 0 {
		t.Errorf("expected 0 orphans on nil lister, got %d", len(got))
	}
}

func TestResolveOrphanRegistrations_MixedScenario(t *testing.T) {
	// Realistic scenario: 4 ArgoCD clusters, 1 managed, 1 pending,
	// 1 in-cluster, 1 orphan. Only the orphan must surface.
	lister := &fakeArgoLister{
		clusters: []models.ArgocdCluster{
			{Name: "in-cluster", Server: "https://kubernetes.default.svc"},
			{Name: "prod-eu", Server: "https://prod-eu.example.com"},   // managed
			{Name: "kind-pending", Server: "https://kind.local:6443"},  // pending
			{Name: "kind-orphan", Server: "https://orphan.local:6443"}, // orphan
		},
	}
	gitClusters := []models.Cluster{{Name: "prod-eu"}}
	pending := map[string]struct{}{"kind-pending": {}}

	got := resolveOrphanRegistrations(context.Background(), lister, gitClusters, pending)
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 orphan, got %d: %+v", len(got), got)
	}
	if got[0].ClusterName != "kind-orphan" {
		t.Errorf("expected orphan kind-orphan, got %q", got[0].ClusterName)
	}
}
