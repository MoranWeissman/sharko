package api

import (
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

// V3 LW-10 — a pending (or orphan) registration that sits in the
// "not_in_git" lane must be pruned from resp.Clusters AND excluded from
// resp.HealthStats.NotInGit. The service layer computes NotInGit before the
// handler prunes, so without the correction the "Available to manage" stat
// card (and the FE's total_in_git + not_in_git total) over-counts by the
// number pruned.

func nameSet(names ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(names))
	for _, n := range names {
		m[n] = struct{}{}
	}
	return m
}

func TestPrunePendingAndOrphan_ExcludesPendingFromNotInGitCount(t *testing.T) {
	resp := &models.ClustersResponse{
		Clusters: []models.Cluster{
			{Name: "prod-us", Managed: true, ConnectionStatus: "Successful"},
			// A discovered (truly unmanaged) cluster — stays.
			{Name: "real-discovered", Managed: false, ConnectionStatus: "not_in_git"},
			// A pending registration that leaked into the not_in_git lane
			// (ArgoCD Secret pre-created, values-file PR not merged) — dropped.
			{Name: "kind-local", Managed: false, ConnectionStatus: "not_in_git"},
		},
		HealthStats: &models.ClusterHealthStats{
			TotalInGit: 1,
			Connected:  1,
			// Service layer counted BOTH not_in_git clusters (2) before the prune.
			NotInGit: 2,
		},
	}

	prunePendingAndOrphanFromNotInGit(resp, nameSet("kind-local"), nil)

	// kind-local removed from the cluster list.
	if len(resp.Clusters) != 2 {
		t.Fatalf("expected 2 clusters after prune, got %d: %+v", len(resp.Clusters), resp.Clusters)
	}
	for _, c := range resp.Clusters {
		if c.Name == "kind-local" {
			t.Errorf("pending cluster kind-local should have been pruned from the list")
		}
	}

	// NotInGit corrected: 2 - 1 pending = 1.
	if resp.HealthStats.NotInGit != 1 {
		t.Errorf("NotInGit = %d, want 1 (pending registration excluded)", resp.HealthStats.NotInGit)
	}
}

func TestPrunePendingAndOrphan_ExcludesOrphanFromNotInGitCount(t *testing.T) {
	resp := &models.ClustersResponse{
		Clusters: []models.Cluster{
			{Name: "real-discovered", Managed: false, ConnectionStatus: "not_in_git"},
			{Name: "orphan-cluster", Managed: false, ConnectionStatus: "not_in_git"},
		},
		HealthStats: &models.ClusterHealthStats{NotInGit: 2},
	}

	prunePendingAndOrphanFromNotInGit(resp, nil, nameSet("orphan-cluster"))

	if len(resp.Clusters) != 1 {
		t.Fatalf("expected 1 cluster after prune, got %d", len(resp.Clusters))
	}
	if resp.HealthStats.NotInGit != 1 {
		t.Errorf("NotInGit = %d, want 1 (orphan excluded)", resp.HealthStats.NotInGit)
	}
}

func TestPrunePendingAndOrphan_ManagedNameCollisionNotCounted(t *testing.T) {
	// A managed cluster (in git) that shares a name with an open register PR
	// (idempotent-retry case) must NOT be pruned and must NOT decrement the
	// count — the prune is gated on the not_in_git lane only.
	resp := &models.ClustersResponse{
		Clusters: []models.Cluster{
			{Name: "prod-eu", Managed: true, ConnectionStatus: "Successful"},
		},
		HealthStats: &models.ClusterHealthStats{TotalInGit: 1, Connected: 1, NotInGit: 0},
	}

	prunePendingAndOrphanFromNotInGit(resp, nameSet("prod-eu"), nil)

	if len(resp.Clusters) != 1 {
		t.Fatalf("managed cluster must not be pruned; got %d clusters", len(resp.Clusters))
	}
	if resp.HealthStats.NotInGit != 0 {
		t.Errorf("NotInGit = %d, want 0 (managed collision must not decrement)", resp.HealthStats.NotInGit)
	}
}

func TestPrunePendingAndOrphan_NeverUnderflows(t *testing.T) {
	// Defensive: if the service-layer count is somehow lower than the number
	// pruned (should not happen, but guard anyway), NotInGit floors at 0.
	resp := &models.ClustersResponse{
		Clusters: []models.Cluster{
			{Name: "a", Managed: false, ConnectionStatus: "not_in_git"},
			{Name: "b", Managed: false, ConnectionStatus: "not_in_git"},
		},
		HealthStats: &models.ClusterHealthStats{NotInGit: 1},
	}

	prunePendingAndOrphanFromNotInGit(resp, nameSet("a", "b"), nil)

	if resp.HealthStats.NotInGit != 0 {
		t.Errorf("NotInGit = %d, want 0 (must not underflow below zero)", resp.HealthStats.NotInGit)
	}
}

func TestPrunePendingAndOrphan_NilHealthStatsSafe(t *testing.T) {
	// A response with no HealthStats (e.g. ArgoCD unreachable path) must not
	// panic — the prune still runs on the cluster list.
	resp := &models.ClustersResponse{
		Clusters: []models.Cluster{
			{Name: "kind-local", Managed: false, ConnectionStatus: "not_in_git"},
		},
		HealthStats: nil,
	}

	prunePendingAndOrphanFromNotInGit(resp, nameSet("kind-local"), nil)

	if len(resp.Clusters) != 0 {
		t.Errorf("expected pending cluster pruned even with nil HealthStats, got %d", len(resp.Clusters))
	}
}

func TestPrunePendingAndOrphan_NoNamesIsNoOp(t *testing.T) {
	resp := &models.ClustersResponse{
		Clusters: []models.Cluster{
			{Name: "real-discovered", Managed: false, ConnectionStatus: "not_in_git"},
		},
		HealthStats: &models.ClusterHealthStats{NotInGit: 1},
	}

	prunePendingAndOrphanFromNotInGit(resp, nil, nil)

	if len(resp.Clusters) != 1 {
		t.Errorf("no pending/orphan names: cluster list must be unchanged, got %d", len(resp.Clusters))
	}
	if resp.HealthStats.NotInGit != 1 {
		t.Errorf("no pending/orphan names: NotInGit must be unchanged, got %d", resp.HealthStats.NotInGit)
	}
}
