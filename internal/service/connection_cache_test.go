package service

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/models"
)

// countingStore wraps a real config.Store and counts calls to the two
// methods that make up the "resolve the active connection" round-trip
// (GetActiveConnection + GetConnection) — on a K8sStore each is a Secret
// GET+decrypt, which is exactly the cost perf S1 caches away. Everything
// else passes straight through to the wrapped store.
type countingStore struct {
	config.Store

	getActiveConnectionCalls int32
	getConnectionCalls       int32
}

func (c *countingStore) GetActiveConnection() (string, error) {
	atomic.AddInt32(&c.getActiveConnectionCalls, 1)
	return c.Store.GetActiveConnection()
}

func (c *countingStore) GetConnection(name string) (*models.Connection, error) {
	atomic.AddInt32(&c.getConnectionCalls, 1)
	return c.Store.GetConnection(name)
}

func testConnection(name, argocdToken, gitToken string) models.Connection {
	return models.Connection{
		Name: name,
		Git: models.GitRepoConfig{
			Provider: models.GitProviderGitHub,
			Owner:    "owner",
			Repo:     "repo",
			Token:    gitToken,
		},
		Argocd: models.ArgocdConfig{
			ServerURL: "https://argocd.example.com",
			Token:     argocdToken,
			Namespace: "argocd",
		},
	}
}

// TestActiveConnectionCache_RepeatedCallsHitStoreOnce is the core perf S1
// regression test: repeated GetActiveArgocdClient/GetActiveGitProvider calls
// against the same active connection must resolve it from the store exactly
// once, not once per call.
func TestActiveConnectionCache_RepeatedCallsHitStoreOnce(t *testing.T) {
	base := config.NewFileStore(t.TempDir() + "/conn.yaml")
	if err := base.SaveConnection(testConnection("test", "argocd-tok", "git-tok")); err != nil {
		t.Fatalf("SaveConnection: %v", err)
	}
	if err := base.SetActiveConnection("test"); err != nil {
		t.Fatalf("SetActiveConnection: %v", err)
	}

	cs := &countingStore{Store: base}
	svc := NewConnectionService(cs)

	const iterations = 5
	for i := 0; i < iterations; i++ {
		if _, err := svc.GetActiveArgocdClient(); err != nil {
			t.Fatalf("GetActiveArgocdClient call %d: %v", i, err)
		}
		if _, err := svc.GetActiveGitProvider(); err != nil {
			t.Fatalf("GetActiveGitProvider call %d: %v", i, err)
		}
		if _, err := svc.GetActiveConnectionInfo(); err != nil {
			t.Fatalf("GetActiveConnectionInfo call %d: %v", i, err)
		}
	}

	if got := atomic.LoadInt32(&cs.getActiveConnectionCalls); got != 1 {
		t.Errorf("store.GetActiveConnection called %d times across %d iterations, want 1 (cache should absorb repeats)", got, iterations)
	}
	if got := atomic.LoadInt32(&cs.getConnectionCalls); got != 1 {
		t.Errorf("store.GetConnection called %d times across %d iterations, want 1", got, iterations)
	}

	// Repeated GetActiveArgocdClient calls must also return the SAME
	// *argocd.Client instance (not just equal config) — that's what makes
	// the underlying http.Transport (and its TLS session reuse) actually
	// shared across requests instead of rebuilt per call.
	c1, err := svc.GetActiveArgocdClient()
	if err != nil {
		t.Fatalf("GetActiveArgocdClient: %v", err)
	}
	c2, err := svc.GetActiveArgocdClient()
	if err != nil {
		t.Fatalf("GetActiveArgocdClient: %v", err)
	}
	if c1 != c2 {
		t.Errorf("GetActiveArgocdClient returned different instances across calls: %p != %p — transport/keep-alives not being reused", c1, c2)
	}
}

// TestActiveConnectionCache_InvalidatesOnMutation covers every mutation path
// on ConnectionService (Create/update, SetActive, Delete) plus the
// InvalidateActiveCache escape hatch used by store-level mutation paths
// (the git-native env reconcile) — each must force the next GetActive* call
// to re-read the store rather than serve stale cached credentials.
func TestActiveConnectionCache_InvalidatesOnMutation(t *testing.T) {
	t.Run("Create (update) invalidates", func(t *testing.T) {
		base := config.NewFileStore(t.TempDir() + "/conn.yaml")
		cs := &countingStore{Store: base}
		svc := NewConnectionService(cs)

		if err := svc.Create(models.CreateConnectionRequest{
			Name: "test",
			Git: models.GitRepoConfig{
				Provider: models.GitProviderGitHub, Owner: "owner", Repo: "repo", Token: "tok1",
			},
			Argocd: models.ArgocdConfig{ServerURL: "https://argocd.example.com", Token: "argocd-tok1", Namespace: "argocd"},
		}); err != nil {
			t.Fatalf("Create: %v", err)
		}

		client1, err := svc.GetActiveArgocdClient()
		if err != nil {
			t.Fatalf("GetActiveArgocdClient (1st): %v", err)
		}
		_ = client1
		if got := atomic.LoadInt32(&cs.getActiveConnectionCalls); got != 1 {
			t.Fatalf("store.GetActiveConnection called %d times, want 1 before update", got)
		}

		// Update — token changes. The cache must be dropped so the next
		// GetActiveArgocdClient rebuilds against the NEW token, not the
		// stale one from client1's cache generation.
		if err := svc.Create(models.CreateConnectionRequest{
			Name: "test",
			Git: models.GitRepoConfig{
				Provider: models.GitProviderGitHub, Owner: "owner", Repo: "repo", Token: "tok2",
			},
			Argocd: models.ArgocdConfig{ServerURL: "https://argocd.example.com", Token: "argocd-tok2", Namespace: "argocd"},
		}); err != nil {
			t.Fatalf("Create (update): %v", err)
		}

		if _, err := svc.GetActiveArgocdClient(); err != nil {
			t.Fatalf("GetActiveArgocdClient (2nd): %v", err)
		}
		if got := atomic.LoadInt32(&cs.getActiveConnectionCalls); got != 2 {
			t.Errorf("store.GetActiveConnection called %d times after update, want 2 (cache must be dropped on Create)", got)
		}
	})

	t.Run("SetActive invalidates", func(t *testing.T) {
		base := config.NewFileStore(t.TempDir() + "/conn.yaml")
		if err := base.SaveConnection(testConnection("a", "argocd-a", "git-a")); err != nil {
			t.Fatalf("SaveConnection a: %v", err)
		}
		if err := base.SaveConnection(testConnection("b", "argocd-b", "git-b")); err != nil {
			t.Fatalf("SaveConnection b: %v", err)
		}
		if err := base.SetActiveConnection("a"); err != nil {
			t.Fatalf("SetActiveConnection a: %v", err)
		}

		cs := &countingStore{Store: base}
		svc := NewConnectionService(cs)

		conn1, err := svc.GetActiveConnectionInfo()
		if err != nil {
			t.Fatalf("GetActiveConnectionInfo: %v", err)
		}
		if conn1.Name != "a" {
			t.Fatalf("active connection = %q, want a", conn1.Name)
		}

		if err := svc.SetActive("b"); err != nil {
			t.Fatalf("SetActive(b): %v", err)
		}

		conn2, err := svc.GetActiveConnectionInfo()
		if err != nil {
			t.Fatalf("GetActiveConnectionInfo after SetActive: %v", err)
		}
		if conn2.Name != "b" {
			t.Errorf("active connection after SetActive(b) = %q, want b — stale cache not invalidated", conn2.Name)
		}
	})

	t.Run("Delete invalidates", func(t *testing.T) {
		base := config.NewFileStore(t.TempDir() + "/conn.yaml")
		if err := base.SaveConnection(testConnection("a", "argocd-a", "git-a")); err != nil {
			t.Fatalf("SaveConnection a: %v", err)
		}
		if err := base.SaveConnection(testConnection("b", "argocd-b", "git-b")); err != nil {
			t.Fatalf("SaveConnection b: %v", err)
		}
		if err := base.SetActiveConnection("a"); err != nil {
			t.Fatalf("SetActiveConnection a: %v", err)
		}

		cs := &countingStore{Store: base}
		svc := NewConnectionService(cs)

		if _, err := svc.GetActiveConnectionInfo(); err != nil {
			t.Fatalf("GetActiveConnectionInfo: %v", err)
		}

		// Deleting the (currently inactive) "b" connection must still drop
		// the cache — the cache invalidation contract is "any mutation",
		// not "only mutations to the active connection".
		if err := svc.Delete("b"); err != nil {
			t.Fatalf("Delete(b): %v", err)
		}

		countBefore := atomic.LoadInt32(&cs.getActiveConnectionCalls)
		if _, err := svc.GetActiveConnectionInfo(); err != nil {
			t.Fatalf("GetActiveConnectionInfo after Delete: %v", err)
		}
		countAfter := atomic.LoadInt32(&cs.getActiveConnectionCalls)
		if countAfter != countBefore+1 {
			t.Errorf("store.GetActiveConnection call count = %d -> %d, want +1 after Delete (cache must be dropped)", countBefore, countAfter)
		}
	})

	t.Run("InvalidateActiveCache (store-level mutation path)", func(t *testing.T) {
		// Mirrors config.ReconcileConnectionFromEnv / MergeConnectionFromEnvAtomic,
		// which mutate the store directly (outside ConnectionService) — those
		// callers (cmd/sharko/serve.go) must call InvalidateActiveCache
		// themselves, since the service can't observe a direct store write.
		base := config.NewFileStore(t.TempDir() + "/conn.yaml")
		if err := base.SaveConnection(testConnection("test", "argocd-tok1", "git-tok1")); err != nil {
			t.Fatalf("SaveConnection: %v", err)
		}
		if err := base.SetActiveConnection("test"); err != nil {
			t.Fatalf("SetActiveConnection: %v", err)
		}

		svc := NewConnectionService(base)
		if _, err := svc.GetActiveConnectionInfo(); err != nil {
			t.Fatalf("GetActiveConnectionInfo: %v", err)
		}

		// Mutate the store directly, bypassing svc entirely.
		if err := base.SaveConnection(testConnection("test", "argocd-tok2", "git-tok2")); err != nil {
			t.Fatalf("SaveConnection (direct): %v", err)
		}

		// Without invalidation the cache would still serve the stale
		// connection — prove the escape hatch fixes that.
		svc.InvalidateActiveCache()

		conn, err := svc.GetActiveConnectionInfo()
		if err != nil {
			t.Fatalf("GetActiveConnectionInfo after direct store write: %v", err)
		}
		if conn.Argocd.Token != "argocd-tok2" {
			t.Errorf("ArgoCD token = %q, want argocd-tok2 (InvalidateActiveCache did not force a fresh read)", conn.Argocd.Token)
		}
	})
}

// TestActiveConnectionCache_Concurrent exercises GetActiveArgocdClient and
// GetActiveGitProvider from many goroutines at once against a cold cache
// (run under go test -race). It asserts: no data race (the race detector
// catches that on its own), every call succeeds, and every goroutine
// converges on the SAME cached client/provider instance rather than each
// building its own (a "thundering rebuild").
func TestActiveConnectionCache_Concurrent(t *testing.T) {
	base := config.NewFileStore(t.TempDir() + "/conn.yaml")
	if err := base.SaveConnection(testConnection("test", "argocd-tok", "git-tok")); err != nil {
		t.Fatalf("SaveConnection: %v", err)
	}
	if err := base.SetActiveConnection("test"); err != nil {
		t.Fatalf("SetActiveConnection: %v", err)
	}

	svc := NewConnectionService(base)

	const goroutines = 50
	var wg sync.WaitGroup

	type result struct {
		argocd interface{}
		git    interface{}
		err    error
	}
	results := make([]result, goroutines)

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			ac, acErr := svc.GetActiveArgocdClient()
			gp, gpErr := svc.GetActiveGitProvider()
			err := acErr
			if err == nil {
				err = gpErr
			}
			results[idx] = result{argocd: ac, git: gp, err: err}
		}(i)
	}
	wg.Wait()

	first := results[0]
	if first.err != nil {
		t.Fatalf("goroutine 0 returned error: %v", first.err)
	}
	for i, r := range results {
		if r.err != nil {
			t.Errorf("goroutine %d returned error: %v", i, r.err)
			continue
		}
		if r.argocd != first.argocd {
			t.Errorf("goroutine %d got a different *argocd.Client than goroutine 0 — cache split under concurrency (thundering rebuild)", i)
		}
		if r.git != first.git {
			t.Errorf("goroutine %d got a different GitProvider than goroutine 0 — cache split under concurrency (thundering rebuild)", i)
		}
	}
}

// latencyStore wraps a config.Store and sleeps before GetActiveConnection /
// GetConnection, standing in for the real K8s Secret GET+decrypt round-trip
// (measured ~150ms each in the live investigation this story is fixing).
// Used only to get an honest, reproducible before/after number for the perf
// win — not a correctness test.
type latencyStore struct {
	config.Store
	perCall time.Duration
}

func (l *latencyStore) GetActiveConnection() (string, error) {
	time.Sleep(l.perCall)
	return l.Store.GetActiveConnection()
}

func (l *latencyStore) GetConnection(name string) (*models.Connection, error) {
	time.Sleep(l.perCall)
	return l.Store.GetConnection(name)
}

// BenchmarkActiveConnectionCache_ColdVsWarm reports the measured perf S1
// win: BEFORE this story, every GetActiveArgocdClient call re-resolved the
// active connection (2 simulated Secret GETs) AND rebuilt the ArgoCD
// client/transport from scratch. AFTER, only the first call after a cold
// start (or an invalidation) pays that cost — every repeat call is a cache
// hit. Run with: go test -bench=ColdVsWarm -benchtime=1x -run=^$ ./internal/service/...
func BenchmarkActiveConnectionCache_ColdVsWarm(b *testing.B) {
	base := config.NewFileStore(b.TempDir() + "/conn.yaml")
	if err := base.SaveConnection(testConnection("test", "argocd-tok", "git-tok")); err != nil {
		b.Fatalf("SaveConnection: %v", err)
	}
	if err := base.SetActiveConnection("test"); err != nil {
		b.Fatalf("SetActiveConnection: %v", err)
	}
	// 150ms mirrors the story's measured ~300ms/request split across the
	// two GETs GetActiveArgocdClient alone needs (active-name + connection).
	ls := &latencyStore{Store: base, perCall: 150 * time.Millisecond}
	svc := NewConnectionService(ls)

	b.Run("cold_every_call_(pre-S1_behavior,_simulated_via_invalidate)", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			svc.InvalidateActiveCache() // force a full rebuild, like every pre-S1 request did
			if _, err := svc.GetActiveArgocdClient(); err != nil {
				b.Fatalf("GetActiveArgocdClient: %v", err)
			}
		}
	})

	svc.InvalidateActiveCache()
	if _, err := svc.GetActiveArgocdClient(); err != nil {
		b.Fatalf("warm-up GetActiveArgocdClient: %v", err)
	}
	b.Run("warm_cache_hit_(post-S1_behavior)", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := svc.GetActiveArgocdClient(); err != nil {
				b.Fatalf("GetActiveArgocdClient: %v", err)
			}
		}
	})
}
