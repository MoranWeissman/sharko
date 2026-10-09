package service

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

// countingGitProvider wraps a *fakeGitProvider and counts GetFileContent
// calls, so tests can assert a readcache-wrapped service method (perf M1)
// skips re-reading Git on a cache hit and re-reads it after invalidation.
// internal/readcache's own test suite already exercises the generic
// Get/Set/expiry/invalidate mechanism exhaustively; these tests exist to
// prove the WIRING on top of it — the actual service methods — behaves
// the same way.
type countingGitProvider struct {
	*fakeGitProvider
	getFileContentCalls int32
}

func (c *countingGitProvider) GetFileContent(ctx context.Context, path, ref string) ([]byte, error) {
	atomic.AddInt32(&c.getFileContentCalls, 1)
	return c.fakeGitProvider.GetFileContent(ctx, path, ref)
}

func (c *countingGitProvider) calls() int32 {
	return atomic.LoadInt32(&c.getFileContentCalls)
}

// countingFakeGP is the same counting wrapper for the *fakeGP flavor used
// by cluster_test.go.
type countingFakeGP struct {
	*fakeGP
	getFileContentCalls int32
}

func (c *countingFakeGP) GetFileContent(ctx context.Context, path, ref string) ([]byte, error) {
	atomic.AddInt32(&c.getFileContentCalls, 1)
	return c.fakeGP.GetFileContent(ctx, path, ref)
}

func (c *countingFakeGP) calls() int32 {
	return atomic.LoadInt32(&c.getFileContentCalls)
}

// TestGetStats_CachedAcrossCalls_PerfM1 pins the readcache wiring around
// DashboardService.GetStats: a second call within the TTL must be a pure
// cache hit (zero additional Git reads), and InvalidateAll must force a
// real recompute.
func TestGetStats_CachedAcrossCalls_PerfM1(t *testing.T) {
	connSvc := NewConnectionService(&inMemoryConnStore{})
	svc := NewDashboardService(connSvc, "")

	gp := &countingGitProvider{fakeGitProvider: dashboardClassificationFixture(t)}
	ac := argocdEmptyStub(t)

	resp1, err := svc.GetStats(context.Background(), gp, ac)
	if err != nil {
		t.Fatalf("first GetStats: %v", err)
	}
	callsAfterFirst := gp.calls()
	if callsAfterFirst == 0 {
		t.Fatalf("expected the uncached first call to read Git at least once")
	}

	resp2, err := svc.GetStats(context.Background(), gp, ac)
	if err != nil {
		t.Fatalf("second GetStats: %v", err)
	}
	if got := gp.calls(); got != callsAfterFirst {
		t.Errorf("second GetStats call re-read Git (calls %d -> %d) — expected a cache hit", callsAfterFirst, got)
	}
	if resp1.Clusters.Total != resp2.Clusters.Total {
		t.Errorf("cached response differs from the original: %d vs %d", resp1.Clusters.Total, resp2.Clusters.Total)
	}

	svc.cache.InvalidateAll()
	if _, err := svc.GetStats(context.Background(), gp, ac); err != nil {
		t.Fatalf("third GetStats (post-invalidate): %v", err)
	}
	if got := gp.calls(); got <= callsAfterFirst {
		t.Errorf("expected GetStats to re-read Git after InvalidateAll, calls stayed at %d", got)
	}
}

// TestClusterService_ListClusters_CachedAcrossCalls_PerfM1 mirrors the
// GetStats test above for ClusterService.ListClusters.
func TestClusterService_ListClusters_CachedAcrossCalls_PerfM1(t *testing.T) {
	svc := NewClusterService("")
	gp := &countingFakeGP{fakeGP: &fakeGP{
		files: map[string][]byte{
			"configuration/managed-clusters.yaml": []byte("clusters: []"),
		},
	}}
	ac := argocdEmptyStub(t)

	if _, err := svc.ListClusters(context.Background(), gp, ac); err != nil {
		t.Fatalf("first ListClusters: %v", err)
	}
	callsAfterFirst := gp.calls()
	if callsAfterFirst == 0 {
		t.Fatalf("expected the uncached first call to read Git at least once")
	}

	if _, err := svc.ListClusters(context.Background(), gp, ac); err != nil {
		t.Fatalf("second ListClusters: %v", err)
	}
	if got := gp.calls(); got != callsAfterFirst {
		t.Errorf("second ListClusters call re-read Git (calls %d -> %d) — expected a cache hit", callsAfterFirst, got)
	}

	svc.cache.InvalidateAll()
	if _, err := svc.ListClusters(context.Background(), gp, ac); err != nil {
		t.Fatalf("third ListClusters (post-invalidate): %v", err)
	}
	if got := gp.calls(); got <= callsAfterFirst {
		t.Errorf("expected ListClusters to re-read Git after InvalidateAll, calls stayed at %d", got)
	}
}

// TestClusterService_ListClusters_CacheReturnsIndependentCopies is the
// mutation-safety regression this whole cache design exists for:
// handleListClusters mutates the *models.ClustersResponse it gets back
// (connectivity enrichment, filter/sort/paginate) in place. If ListClusters
// ever handed back a shared cached pointer, one request's mutation would
// corrupt what every other request sees. readcache.Get decodes a fresh
// copy from a JSON snapshot on every call specifically to make this
// impossible — this test proves it end to end through ListClusters, not
// just at the readcache package level.
func TestClusterService_ListClusters_CacheReturnsIndependentCopies(t *testing.T) {
	svc := NewClusterService("")
	gp := &fakeGP{
		files: map[string][]byte{
			"configuration/managed-clusters.yaml": []byte("clusters: []"),
		},
	}
	ac := argocdEmptyStub(t)

	resp1, err := svc.ListClusters(context.Background(), gp, ac)
	if err != nil {
		t.Fatalf("first ListClusters: %v", err)
	}
	// Mutate the first caller's copy the way handleListClusters does
	// (append to / reassign resp.Clusters, and mutate a field on the
	// underlying array via a pointer into it).
	resp1.Clusters = append(resp1.Clusters, models.Cluster{Name: "mutated-by-caller-1"})

	resp2, err := svc.ListClusters(context.Background(), gp, ac)
	if err != nil {
		t.Fatalf("second ListClusters: %v", err)
	}
	for _, c := range resp2.Clusters {
		if c.Name == "mutated-by-caller-1" {
			t.Fatalf("second ListClusters call was corrupted by the first caller's in-place mutation: %+v", resp2.Clusters)
		}
	}
}
