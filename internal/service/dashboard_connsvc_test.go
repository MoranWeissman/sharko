package service

import (
	"context"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

// TestDashboardService_SetConnectionService_RepointsConnectionTotals is the
// S3 demo-wiring regression test: DashboardService bakes connSvc in at
// construction (cmd/sharko/serve.go), and api.Server.SetDemoConnectionService
// only reassigns the SERVER's own connSvc field — before this fix, that
// reassignment never reached a DashboardService built earlier from the
// pre-swap pointer, so GetStats's connection-totals stat (dashboard.go's
// connStats) kept reporting the real store's connections even after demo
// mode swapped in the in-memory one.
//
// This test constructs a DashboardService against a connSvc with ZERO
// connections, calls SetConnectionService with a second connSvc that has
// ONE, and asserts the connection total reflects the SECOND one on the
// SAME service instance — proving the seam repoints what GetStats reads,
// not just that the field exists. Uses getStatsUncached (this test lives
// in package service) rather than the cached GetStats: GetStats caches by
// a fixed key, so calling it twice on one instance would just replay the
// first (pre-swap) result regardless of whether the fix works.
func TestDashboardService_SetConnectionService_RepointsConnectionTotals(t *testing.T) {
	emptyConnSvc := NewConnectionService(&inMemoryConnStore{})
	svc := NewDashboardService(emptyConnSvc, "")
	gp := &fakeGP{} // every lookup returns wrapped ErrFileNotFound -> zero git-derived stats

	before, err := svc.getStatsUncached(context.Background(), gp, argocdEmptyStub(t))
	if err != nil {
		t.Fatalf("getStatsUncached (before swap): %v", err)
	}
	if before.Connections.Total != 0 {
		t.Fatalf("connections.total before swap = %d, want 0", before.Connections.Total)
	}

	demoStore := &inMemoryConnStore{
		connections: []models.Connection{{
			Name: "demo/sharko-addons",
			Git: models.GitRepoConfig{
				Provider: models.GitProviderGitHub,
				Owner:    "demo",
				Repo:     "sharko-addons",
				Token:    "demo-token",
			},
			IsDefault: true,
		}},
		active: "demo/sharko-addons",
	}
	demoConnSvc := NewConnectionService(demoStore)
	svc.SetConnectionService(demoConnSvc)

	after, err := svc.getStatsUncached(context.Background(), gp, argocdEmptyStub(t))
	if err != nil {
		t.Fatalf("getStatsUncached (after swap): %v", err)
	}
	if after.Connections.Total != 1 {
		t.Errorf("connections.total after SetConnectionService = %d, want 1 (the swap did not take effect)", after.Connections.Total)
	}
}
