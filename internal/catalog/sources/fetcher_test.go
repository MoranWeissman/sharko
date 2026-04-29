package sources

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/config"
)

// --- Test fixtures ---------------------------------------------------

// validCatalogYAML is a minimal payload matching catalog/schema.json
// v1.1 requirements — two entries so "empty addons" doesn't kick in
// and every required field is present. Kept tiny on purpose.
const validCatalogYAML = `addons:
  - name: example-one
    description: Example addon one for fetcher tests.
    chart: example-one
    repo: https://charts.example.com
    default_namespace: example-one
    default_sync_wave: 10
    license: Apache-2.0
    category: observability
    curated_by: [cncf-sandbox]
    maintainers: [test@example.com]
  - name: example-two
    description: Example addon two for fetcher tests.
    chart: example-two
    repo: https://charts.example.com
    default_namespace: example-two
    default_sync_wave: 10
    license: MIT
    category: security
    curated_by: [cncf-incubating]
    maintainers: [test@example.com]
`

// invalidCatalogYAML violates the schema — missing required `chart`
// field on the only entry. The loader's validateEntry rejects it.
const invalidCatalogYAML = `addons:
  - name: broken
    description: Missing required fields.
    repo: https://charts.example.com
    default_namespace: broken
    license: MIT
    category: security
    curated_by: [cncf-sandbox]
    maintainers: [test@example.com]
`

// --- Helpers ---------------------------------------------------------

// startYAMLServer returns an httptest.NewTLSServer that serves a
// rotating body under mutex so tests can flip its response mid-run.
type flippable struct {
	mu       sync.Mutex
	body     string
	status   int
	delay    time.Duration
	hitCount int64
}

func (f *flippable) set(status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status = status
	f.body = body
}

func (f *flippable) hits() int64 { return atomic.LoadInt64(&f.hitCount) }

func (f *flippable) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&f.hitCount, 1)
		f.mu.Lock()
		status := f.status
		body := f.body
		delay := f.delay
		f.mu.Unlock()
		if delay > 0 {
			time.Sleep(delay)
		}
		// HEAD requests (sidecar probes) — respond with the configured
		// status but no body. Sidecar probes for URLs we didn't set up
		// default to 404 via the `status` field.
		w.Header().Set("Content-Type", "application/yaml")
		w.WriteHeader(status)
		if r.Method != http.MethodHead {
			_, _ = w.Write([]byte(body))
		}
	})
}

// newTestFetcher builds a Fetcher configured for the given test
// server(s), with AllowPrivate=true (so the runtime SSRF guard does
// not block 127.0.0.1 httptest URLs) and a trusted HTTP client that
// accepts httptest's self-signed cert.
func newTestFetcher(t *testing.T, servers []*httptest.Server, verifier SidecarVerifier) *Fetcher {
	t.Helper()
	sources := make([]config.CatalogSource, 0, len(servers))
	for _, s := range servers {
		sources = append(sources, config.CatalogSource{URL: s.URL + "/catalog.yaml"})
	}
	cfg := &config.CatalogSourcesConfig{
		Sources:         sources,
		RefreshInterval: config.MinRefreshInterval,
		AllowPrivate:    true,
	}
	f := NewFetcher(cfg, verifier, nil)
	if len(servers) > 0 {
		f.SetHTTPClientForTest(servers[0].Client())
	}
	return f
}

// --- AC #1 -----------------------------------------------------------

// TestFetcher_HappyPath covers AC #1: valid YAML → entries parsed,
// status StatusOK.
func TestFetcher_HappyPath(t *testing.T) {
	ff := &flippable{status: http.StatusOK, body: validCatalogYAML}
	srv := httptest.NewTLSServer(ff.handler())
	t.Cleanup(srv.Close)

	f := newTestFetcher(t, []*httptest.Server{srv}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Use ForceRefresh rather than Start so the test is fully
	// deterministic — no ticker, no initial-fetch race.
	f.ForceRefresh(ctx)

	snaps := f.Snapshots()
	snap, ok := snaps[srv.URL+"/catalog.yaml"]
	if !ok {
		t.Fatalf("expected snapshot for configured URL, got keys: %v", keysOf(snaps))
	}
	if snap.Status != StatusOK {
		t.Fatalf("expected StatusOK, got %q (err=%v)", snap.Status, snap.LastErr)
	}
	if len(snap.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(snap.Entries))
	}
	if snap.LastSuccessAt.IsZero() {
		t.Fatal("LastSuccessAt should be set after a successful fetch")
	}
	if snap.LastErr != nil {
		t.Fatalf("LastErr should be nil on success, got %v", snap.LastErr)
	}
}

// --- AC #2 -----------------------------------------------------------

// TestFetcher_5xxRetainsSnapshot covers AC #2: after a successful
// fetch, a subsequent 500 leaves the Entries intact and flips status
// to StatusStale.
func TestFetcher_5xxRetainsSnapshot(t *testing.T) {
	ff := &flippable{status: http.StatusOK, body: validCatalogYAML}
	srv := httptest.NewTLSServer(ff.handler())
	t.Cleanup(srv.Close)

	f := newTestFetcher(t, []*httptest.Server{srv}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// First fetch — healthy.
	f.ForceRefresh(ctx)
	snaps := f.Snapshots()
	url := srv.URL + "/catalog.yaml"
	if snaps[url].Status != StatusOK {
		t.Fatalf("expected StatusOK on first fetch, got %q", snaps[url].Status)
	}

	// Flip to 500 and fetch again.
	ff.set(http.StatusInternalServerError, "upstream oops")
	f.ForceRefresh(ctx)
	snaps = f.Snapshots()
	snap := snaps[url]
	if snap.Status != StatusStale {
		t.Fatalf("expected StatusStale after 5xx, got %q (err=%v)", snap.Status, snap.LastErr)
	}
	if len(snap.Entries) != 2 {
		t.Fatalf("expected prior entries retained after 5xx, got %d", len(snap.Entries))
	}
	if snap.LastErr == nil {
		t.Fatal("expected LastErr to record the 5xx")
	}
}

// --- AC #3 -----------------------------------------------------------

// TestFetcher_SchemaViolationRetainsSnapshot covers AC #3: after a
// successful fetch, a subsequent schema-invalid payload retains the
// prior Entries and flips status to StatusFailed.
func TestFetcher_SchemaViolationRetainsSnapshot(t *testing.T) {
	ff := &flippable{status: http.StatusOK, body: validCatalogYAML}
	srv := httptest.NewTLSServer(ff.handler())
	t.Cleanup(srv.Close)

	f := newTestFetcher(t, []*httptest.Server{srv}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	f.ForceRefresh(ctx)
	url := srv.URL + "/catalog.yaml"
	if f.Snapshots()[url].Status != StatusOK {
		t.Fatal("expected StatusOK on first fetch")
	}

	ff.set(http.StatusOK, invalidCatalogYAML)
	f.ForceRefresh(ctx)

	snap := f.Snapshots()[url]
	if snap.Status != StatusFailed {
		t.Fatalf("expected StatusFailed after schema violation, got %q", snap.Status)
	}
	if len(snap.Entries) != 2 {
		t.Fatalf("expected prior entries retained after schema fail, got %d", len(snap.Entries))
	}
	if snap.LastErr == nil || !strings.Contains(snap.LastErr.Error(), "schema") {
		t.Fatalf("expected schema-flavoured error, got %v", snap.LastErr)
	}
}

// --- AC #4 -----------------------------------------------------------

// TestFetcher_HTTPProxyRespected covers AC #4: the HTTP transport uses
// http.ProxyFromEnvironment. Asserting by function identity (reflect)
// because spinning a mock proxy against TLS requires a CONNECT
// handshake which is overkill for the "is the hook wired?" check.
func TestFetcher_HTTPProxyRespected(t *testing.T) {
	cfg := &config.CatalogSourcesConfig{RefreshInterval: config.MinRefreshInterval}
	f := NewFetcher(cfg, nil, nil)

	client := f.HTTPClientForTest()
	tr, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", client.Transport)
	}
	if tr.Proxy == nil {
		t.Fatal("Transport.Proxy must be set (expected http.ProxyFromEnvironment)")
	}
	// Compare the function pointer.
	want := reflect.ValueOf(http.ProxyFromEnvironment).Pointer()
	got := reflect.ValueOf(tr.Proxy).Pointer()
	if want != got {
		t.Fatalf("Transport.Proxy is not http.ProxyFromEnvironment")
	}

	// Additional sanity: HTTPS_PROXY env reaches ProxyFromEnvironment.
	// We set the env + build a fake request and confirm the Proxy fn
	// returns the proxy URL we configured. This proves the wiring
	// end-to-end without actually tunnelling through a proxy.
	t.Setenv("HTTPS_PROXY", "http://proxy.example.invalid:3128")
	req, _ := http.NewRequest(http.MethodGet, "https://catalogs.example.com/cat.yaml", nil)
	pu, err := tr.Proxy(req)
	if err != nil {
		t.Fatalf("Proxy hook returned error: %v", err)
	}
	if pu == nil {
		t.Fatalf("Proxy hook returned nil — HTTPS_PROXY env not consulted")
	}
	if pu.Host != "proxy.example.invalid:3128" {
		t.Fatalf("Proxy hook returned %q, want proxy.example.invalid:3128", pu.Host)
	}
}

// --- AC #5 -----------------------------------------------------------

// TestFetcher_FreshStartNoSuccess covers AC #5: with a 500 on first
// fetch, snapshot Entries is empty and Status is StatusFailed (not
// StatusStale — there's no prior success to fall back to).
func TestFetcher_FreshStartNoSuccess(t *testing.T) {
	ff := &flippable{status: http.StatusInternalServerError, body: "oops"}
	srv := httptest.NewTLSServer(ff.handler())
	t.Cleanup(srv.Close)

	f := newTestFetcher(t, []*httptest.Server{srv}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	f.ForceRefresh(ctx)
	snap := f.Snapshots()[srv.URL+"/catalog.yaml"]
	if snap.Status != StatusFailed {
		t.Fatalf("expected StatusFailed, got %q", snap.Status)
	}
	if len(snap.Entries) != 0 {
		t.Fatalf("expected no entries on fresh-start failure, got %d", len(snap.Entries))
	}
	if !snap.LastSuccessAt.IsZero() {
		t.Fatal("LastSuccessAt should be zero on fresh-start failure")
	}
}

// --- AC #6 -----------------------------------------------------------

// fakeVerifier is a stub SidecarVerifier whose return values are set
// per-test.
type fakeVerifier struct {
	called      atomic.Int32
	verified    bool
	issuer      string
	err         error
	lastSidecar string
	mu          sync.Mutex
}

func (v *fakeVerifier) Verify(_ context.Context, _ []byte, sidecarURL string, _ TrustPolicy) (bool, string, error) {
	v.called.Add(1)
	v.mu.Lock()
	v.lastSidecar = sidecarURL
	v.mu.Unlock()
	return v.verified, v.issuer, v.err
}

// sidecarServer returns a test server that serves the main catalog at
// /catalog.yaml AND responds to HEAD /catalog.yaml.bundle with 200.
func sidecarServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/catalog.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write([]byte(validCatalogYAML))
		}
	})
	mux.HandleFunc("/catalog.yaml.bundle", func(w http.ResponseWriter, r *http.Request) {
		// HEAD returns 200; GET not needed (verifier is stubbed) but be
		// friendly anyway.
		w.WriteHeader(http.StatusOK)
	})
	return httptest.NewTLSServer(mux)
}

// TestFetcher_SidecarDelegation covers AC #6 (positive): when a
// verifier is wired AND a sidecar is discoverable, the fetcher invokes
// Verify and records the result.
func TestFetcher_SidecarDelegation(t *testing.T) {
	srv := sidecarServer(t)
	t.Cleanup(srv.Close)

	ver := &fakeVerifier{verified: true, issuer: "https://github.com/example/actions/.github/workflows/ci.yml@refs/tags/v1.0.0"}
	f := newTestFetcher(t, []*httptest.Server{srv}, ver)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	f.ForceRefresh(ctx)

	if ver.called.Load() != 1 {
		t.Fatalf("expected verifier.Verify to be called exactly once, got %d", ver.called.Load())
	}
	snap := f.Snapshots()[srv.URL+"/catalog.yaml"]
	if snap.Status != StatusOK {
		t.Fatalf("expected StatusOK, got %q", snap.Status)
	}
	if !snap.Verified {
		t.Fatal("expected Verified=true after a successful verification")
	}
	if !strings.Contains(snap.Issuer, "github.com/example") {
		t.Fatalf("expected issuer to be populated, got %q", snap.Issuer)
	}
	if !strings.HasSuffix(ver.lastSidecar, ".bundle") {
		t.Fatalf("expected .bundle to be probed first, got sidecar=%q", ver.lastSidecar)
	}
}

// TestFetcher_NilVerifierSidecarIgnored covers AC #6 (negative path):
// nil verifier + sidecar present must NOT call verifier (there is no
// verifier) AND must leave Verified=false.
func TestFetcher_NilVerifierSidecarIgnored(t *testing.T) {
	srv := sidecarServer(t)
	t.Cleanup(srv.Close)

	f := newTestFetcher(t, []*httptest.Server{srv}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	f.ForceRefresh(ctx)
	snap := f.Snapshots()[srv.URL+"/catalog.yaml"]
	if snap.Status != StatusOK {
		t.Fatalf("expected StatusOK, got %q (err=%v)", snap.Status, snap.LastErr)
	}
	if snap.Verified {
		t.Fatal("Verified should be false when no verifier is wired")
	}
	if snap.Issuer != "" {
		t.Fatalf("Issuer should be empty when not verified, got %q", snap.Issuer)
	}
}

// --- Concurrency -----------------------------------------------------

// TestFetcher_ConcurrentFetchesInParallel covers the "multiple URLs
// fetched concurrently" requirement: 3 servers each delaying 200ms
// should finish in roughly one delay window, not three.
func TestFetcher_ConcurrentFetchesInParallel(t *testing.T) {
	const perDelay = 200 * time.Millisecond
	servers := make([]*httptest.Server, 0, 3)
	for i := 0; i < 3; i++ {
		ff := &flippable{status: http.StatusOK, body: validCatalogYAML, delay: perDelay}
		srv := httptest.NewTLSServer(ff.handler())
		t.Cleanup(srv.Close)
		servers = append(servers, srv)
	}

	f := newTestFetcher(t, servers, nil)
	// Inject each server's trusted client. Since all three use
	// different self-signed certs, use a pooled client that trusts any
	// (just configure one transport with InsecureSkipVerify = true is
	// cleanest for this test, but we can avoid that by adding every
	// server's cert to a shared pool).
	client := newPooledClient(servers)
	f.SetHTTPClientForTest(client)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	start := time.Now()
	f.ForceRefresh(ctx)
	elapsed := time.Since(start)

	// Parallel: ~perDelay. Sequential: 3*perDelay. Pick a ceiling well
	// below sequential but above jitter.
	if elapsed >= 2*perDelay {
		t.Fatalf("fetches were not concurrent: elapsed %v, want < %v", elapsed, 2*perDelay)
	}

	for _, srv := range servers {
		snap := f.Snapshots()[srv.URL+"/catalog.yaml"]
		if snap == nil || snap.Status != StatusOK {
			t.Fatalf("expected StatusOK for %s, got %+v", srv.URL, snap)
		}
	}
}

// TestFetcher_ConcurrentForceRefreshSerialized covers V123-PR-B (H2):
// two ForceRefresh calls that race against each other must execute
// sequentially — the second call's first hit on the upstream must
// happen after the first call's last hit completes. Pre-fix, the two
// fanouts ran concurrently and could double-overwrite the snapshot
// during a single fetch window.
//
// Strategy: serve a 200ms-delayed handler so each fetch takes roughly
// that long; track per-request start timestamps; assert that no request
// from the second fanout starts before any request from the first
// fanout finishes. We use one server (one source) so the comparison
// is direct: with one in-flight request per fanout, the lock either
// holds (fanouts serialize) or it doesn't (timestamps interleave).
func TestFetcher_ConcurrentForceRefreshSerialized(t *testing.T) {
	const perDelay = 200 * time.Millisecond

	type span struct{ start, end time.Time }
	var (
		spansMu sync.Mutex
		spans   []span
	)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// HEAD probes (sidecar lookups) get a non-200 fast — we don't
		// want to pollute the timing data with sidecar calls.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		s := span{start: time.Now()}
		time.Sleep(perDelay)
		s.end = time.Now()
		spansMu.Lock()
		spans = append(spans, s)
		spansMu.Unlock()
		w.Header().Set("Content-Type", "application/yaml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(validCatalogYAML))
	})
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)

	f := newTestFetcher(t, []*httptest.Server{srv}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Fire two ForceRefresh calls concurrently. Without H2 they would
	// run in parallel (both ~200ms). With H2 they serialize and total
	// wall-clock time is ~2*perDelay.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); f.ForceRefresh(ctx) }()
	go func() { defer wg.Done(); f.ForceRefresh(ctx) }()
	wg.Wait()

	spansMu.Lock()
	defer spansMu.Unlock()
	if len(spans) != 2 {
		t.Fatalf("expected exactly 2 main GETs, got %d", len(spans))
	}
	// Sort by start time so we compare first→second deterministically.
	first, second := spans[0], spans[1]
	if second.start.Before(first.start) {
		first, second = second, first
	}
	if !second.start.After(first.end) || second.start.Equal(first.end) {
		t.Fatalf("ForceRefresh calls overlapped — H2 lock not serializing: first=[%v..%v] second=[%v..%v]",
			first.start, first.end, second.start, second.end)
	}
}

// --- SSRF ------------------------------------------------------------

// TestFetcher_RuntimeSSRFGuardBlocksPrivateIP covers the runtime SSRF
// check: a public hostname that resolves to an RFC1918 address at
// fetch time is blocked even though startup validation passed
// (simulated by overriding lookupHostFn and flipping AllowPrivate to
// false).
func TestFetcher_RuntimeSSRFGuardBlocksPrivateIP(t *testing.T) {
	origLookup := lookupHostFn
	t.Cleanup(func() { lookupHostFn = origLookup })
	lookupHostFn = func(host string) ([]string, error) {
		if host == "public-looking.example.com" {
			return []string{"10.0.0.5"}, nil
		}
		return origLookup(host)
	}

	cfg := &config.CatalogSourcesConfig{
		Sources: []config.CatalogSource{
			{URL: "https://public-looking.example.com/catalog.yaml"},
		},
		RefreshInterval: config.MinRefreshInterval,
		AllowPrivate:    false, // SSRF guard ON
	}
	f := NewFetcher(cfg, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	f.ForceRefresh(ctx)

	snap := f.Snapshots()["https://public-looking.example.com/catalog.yaml"]
	if snap == nil {
		t.Fatal("expected snapshot to exist even when SSRF-blocked")
	}
	if snap.Status != StatusFailed {
		t.Fatalf("expected StatusFailed after SSRF block, got %q", snap.Status)
	}
	if snap.LastErr == nil || !strings.Contains(snap.LastErr.Error(), "private") {
		t.Fatalf("expected SSRF-flavoured error, got %v", snap.LastErr)
	}
}

// --- Cancellation + goroutine leak ----------------------------------

// TestFetcher_CtxCancelStopsSupervisor covers graceful shutdown:
// cancelling the supervisor ctx + calling Stop drains the fetcher's
// own goroutines. To avoid counting httptest's accept/handle
// goroutines (which only stop when the server itself closes) we
// close the test servers BEFORE taking the final snapshot.
func TestFetcher_CtxCancelStopsSupervisor(t *testing.T) {
	baseline := runtime.NumGoroutine()

	for i := 0; i < 3; i++ {
		ff := &flippable{status: http.StatusOK, body: validCatalogYAML}
		srv := httptest.NewTLSServer(ff.handler())

		f := newTestFetcher(t, []*httptest.Server{srv}, nil)
		ctx, cancel := context.WithCancel(context.Background())
		f.Start(ctx)
		// Let the supervisor + initial fetch tick once.
		time.Sleep(50 * time.Millisecond)
		cancel()
		f.Stop()
		srv.Close() // reap httptest's own goroutines before counting
	}

	// Give the scheduler a moment to reap goroutines (httptest's
	// idle-connection goroutines, real ticker.Stop, etc.).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		// Allow a tiny headroom — Go's scheduler can keep transient
		// goroutines around for a few ms even after Close.
		if runtime.NumGoroutine() <= baseline+3 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: baseline=%d, current=%d", baseline, runtime.NumGoroutine())
}

// TestFetcher_StartThenStopIsClean proves Start + Stop without any
// ticker firing leaves no goroutines behind — the supervisor must
// return on stopCh even when the ticker hasn't produced a tick yet.
func TestFetcher_StartThenStopIsClean(t *testing.T) {
	ff := &flippable{status: http.StatusOK, body: validCatalogYAML}
	srv := httptest.NewTLSServer(ff.handler())

	baseline := runtime.NumGoroutine()
	f := newTestFetcher(t, []*httptest.Server{srv}, nil)
	f.Start(context.Background())
	// No wait — immediate Stop.
	f.Stop()
	// Double-Stop is safe (AC: Stop should be idempotent).
	f.Stop()
	srv.Close() // reap httptest's own goroutines before counting

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= baseline+3 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("goroutine leak after Start/Stop: baseline=%d, current=%d", baseline, runtime.NumGoroutine())
}

// --- Misc ------------------------------------------------------------

// TestFetcher_NilVerifierNilClockOK proves the "NewFetcher accepts nil
// verifier + nil clock" contract — no panics on construction.
func TestFetcher_NilVerifierNilClockOK(t *testing.T) {
	cfg := &config.CatalogSourcesConfig{RefreshInterval: config.MinRefreshInterval}
	f := NewFetcher(cfg, nil, nil)
	if f == nil {
		t.Fatal("NewFetcher returned nil")
	}
	// Start with no sources should be a no-op.
	f.Start(context.Background())
	f.Stop()
}

// TestFetcher_ForceRefreshEmptyURLsMeansAll exercises the "empty URL
// list = all sources" contract of ForceRefresh.
func TestFetcher_ForceRefreshEmptyURLsMeansAll(t *testing.T) {
	ff := &flippable{status: http.StatusOK, body: validCatalogYAML}
	srv := httptest.NewTLSServer(ff.handler())
	t.Cleanup(srv.Close)

	f := newTestFetcher(t, []*httptest.Server{srv}, nil)
	ctx := context.Background()

	f.ForceRefresh(ctx /* no URLs */)
	if ff.hits() < 1 {
		t.Fatal("expected at least one hit from ForceRefresh()")
	}
}

// TestFetcher_ForceRefreshUnknownURLIgnored confirms ForceRefresh
// silently ignores URLs not in the configured Sources — preserves
// idempotent semantics for API callers.
func TestFetcher_ForceRefreshUnknownURLIgnored(t *testing.T) {
	ff := &flippable{status: http.StatusOK, body: validCatalogYAML}
	srv := httptest.NewTLSServer(ff.handler())
	t.Cleanup(srv.Close)

	f := newTestFetcher(t, []*httptest.Server{srv}, nil)
	ctx := context.Background()

	before := ff.hits()
	f.ForceRefresh(ctx, "https://unknown.example.com/cat.yaml")
	after := ff.hits()
	if before != after {
		t.Fatalf("expected no hits for unknown URL, got %d → %d", before, after)
	}
}

// TestFetcher_SnapshotsReturnsDeepCopy proves Snapshots() callers
// cannot corrupt the fetcher's internal state by mutating returned
// slices / maps.
func TestFetcher_SnapshotsReturnsDeepCopy(t *testing.T) {
	ff := &flippable{status: http.StatusOK, body: validCatalogYAML}
	srv := httptest.NewTLSServer(ff.handler())
	t.Cleanup(srv.Close)

	f := newTestFetcher(t, []*httptest.Server{srv}, nil)
	f.ForceRefresh(context.Background())

	snaps := f.Snapshots()
	url := srv.URL + "/catalog.yaml"
	// Mutate caller's copy.
	snaps[url].Entries = nil
	snaps[url].Status = StatusFailed
	delete(snaps, url)

	// Fetcher's view should be unchanged.
	fresh := f.Snapshots()
	if fresh[url] == nil {
		t.Fatal("deletion on caller's map leaked into fetcher")
	}
	if fresh[url].Status != StatusOK {
		t.Fatalf("mutation on caller's snapshot leaked: status=%q", fresh[url].Status)
	}
	if len(fresh[url].Entries) != 2 {
		t.Fatalf("mutation on caller's Entries slice leaked: got %d", len(fresh[url].Entries))
	}
}

// --- pooled TLS client for multi-server tests ------------------------

// newPooledClient builds an http.Client that trusts every httptest
// server's self-signed cert. Needed because each NewTLSServer
// generates a fresh CA, and a single server.Client() only trusts its
// own.
func newPooledClient(servers []*httptest.Server) *http.Client {
	pool := x509.NewCertPool()
	for _, srv := range servers {
		pool.AddCert(srv.Certificate())
	}
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = http.ProxyFromEnvironment
	base.TLSClientConfig = &tls.Config{RootCAs: pool}
	return &http.Client{Transport: base, Timeout: 5 * time.Second}
}

// keysOf is a tiny test helper.
func keysOf(m map[string]*SourceSnapshot) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// --- V123-1.9 gap cases ---------------------------------------------

// clientWithTimeout copies the test server's *http.Client (which already
// trusts the server's self-signed TLS cert) and overrides the wall-clock
// Timeout. Using srv.Client() as the base is what keeps the TLS handshake
// working against httptest's ephemeral CA — a plain http.Client{Timeout:…}
// would fail the TLS verification before the Timeout even came into play.
func clientWithTimeout(srv *httptest.Server, timeout time.Duration) *http.Client {
	base := srv.Client()
	c := *base
	c.Timeout = timeout
	return &c
}

// TestFetcher_ClientTimeoutMarksFailed — V123-1.9 gap fill.
//
// An http.Client.Timeout shorter than the server's response delay must
// end the request with a deadline-exceeded error. The snapshot should
// land in StatusFailed (no prior success → no stale fallback) and the
// LastErr should mention "deadline" or "timeout". Distinct from the
// existing TestFetcher_5xxRetainsSnapshot (HTTP 5xx) and
// TestFetcher_SchemaViolationRetainsSnapshot (parseable but invalid)
// cases — this specifically exercises the transport-layer timeout path.
func TestFetcher_ClientTimeoutMarksFailed(t *testing.T) {
	ff := &flippable{status: http.StatusOK, body: validCatalogYAML, delay: 500 * time.Millisecond}
	srv := httptest.NewTLSServer(ff.handler())
	t.Cleanup(srv.Close)

	f := newTestFetcher(t, []*httptest.Server{srv}, nil)
	// Override with a tight-timeout client. newTestFetcher already set an
	// srv.Client() on the fetcher; we overwrite with a copy that has a
	// 100ms Timeout — still trusts the test TLS CA.
	f.SetHTTPClientForTest(clientWithTimeout(srv, 100*time.Millisecond))

	// Generous ctx so the failure path is the client Timeout firing, not
	// the caller's context ending.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	f.ForceRefresh(ctx)

	url := srv.URL + "/catalog.yaml"
	snap := f.Snapshots()[url]
	if snap == nil {
		t.Fatalf("expected snapshot for %s", url)
	}
	if snap.Status != StatusFailed {
		t.Fatalf("expected StatusFailed after timeout, got %q (err=%v)", snap.Status, snap.LastErr)
	}
	if snap.LastErr == nil {
		t.Fatal("expected LastErr to record the timeout")
	}
	// Tolerant match — Go's net/http wraps transport timeouts variously
	// ("context deadline exceeded", "Client.Timeout exceeded while
	// awaiting headers", "deadline exceeded (Client.Timeout exceeded
	// while ...)"). Accept any of the canonical tokens.
	msg := strings.ToLower(snap.LastErr.Error())
	if !strings.Contains(msg, "deadline") && !strings.Contains(msg, "timeout") {
		t.Fatalf("expected timeout/deadline-flavoured error, got %v", snap.LastErr)
	}
	// LastSuccessAt must remain zero — there is no prior success to fall
	// back on, which is why Status is Failed rather than Stale.
	if !snap.LastSuccessAt.IsZero() {
		t.Fatalf("LastSuccessAt should be zero on fresh-start timeout, got %v", snap.LastSuccessAt)
	}
}

// TestFetcher_InvalidYAMLMarksFailed — V123-1.9 gap fill.
//
// The server returns bytes that fail at the YAML *parse* stage (not the
// schema-validation stage). The snapshot must land in StatusFailed with
// a non-empty LastErr. Distinct from TestFetcher_SchemaViolationRetainsSnapshot,
// which tests well-formed YAML that fails schema checks — here the bytes
// cannot even be parsed as YAML. Tolerant match on the error message
// because the fetcher wraps the loader error as "schema validation: %w"
// even when the underlying cause is a parse error, so we assert on
// either "yaml" / "parse" / "unmarshal" / "schema" to cover the wrapper.
func TestFetcher_InvalidYAMLMarksFailed(t *testing.T) {
	// Unterminated flow-style sequence → yaml.Unmarshal parse error.
	const unparseableYAML = "addons:\n  - name: broken\nfoo: [\n"

	ff := &flippable{status: http.StatusOK, body: unparseableYAML}
	srv := httptest.NewTLSServer(ff.handler())
	t.Cleanup(srv.Close)

	f := newTestFetcher(t, []*httptest.Server{srv}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	f.ForceRefresh(ctx)

	url := srv.URL + "/catalog.yaml"
	snap := f.Snapshots()[url]
	if snap == nil {
		t.Fatalf("expected snapshot for %s", url)
	}
	if snap.Status != StatusFailed {
		t.Fatalf("expected StatusFailed after invalid YAML, got %q (err=%v)", snap.Status, snap.LastErr)
	}
	if snap.LastErr == nil {
		t.Fatal("expected LastErr to record the parse failure")
	}
	// Tolerant match — the fetcher wraps loader errors as
	// "schema validation: %w" regardless of the underlying cause.
	// Accept the canonical parse-flavoured tokens or the wrapper prefix.
	msg := strings.ToLower(snap.LastErr.Error())
	if !strings.Contains(msg, "yaml") &&
		!strings.Contains(msg, "parse") &&
		!strings.Contains(msg, "unmarshal") &&
		!strings.Contains(msg, "schema") {
		t.Fatalf("expected yaml/parse/unmarshal/schema-flavoured error, got %v", snap.LastErr)
	}
	// Fresh start — no prior successful Entries to retain.
	if len(snap.Entries) != 0 {
		t.Fatalf("expected no entries on fresh-start parse failure, got %d", len(snap.Entries))
	}
}

// --- V123-2.4 / B3 BLOCKER fix: per-entry verification ----------------
//
// The fetcher's pre-fix behaviour called `catalog.LoadBytes(body)` and
// silently retained any `signature.bundle` URL on third-party entries
// without verifying it — letting a compromised third-party curator flip
// an entry's bundle URL and have Sharko serve it as if signed. The fix
// wires an explicit `catalog.VerifyEntryFunc` callback through
// SetEntryVerifyFunc; the fetcher dispatches to LoadBytesWithVerifier
// when the callback is set. The two cases below exercise both outcomes:
// trusted (Verified=true, identity recorded) and untrusted (Verified=false).

// signedEntryYAML is the smallest valid third-party catalog payload that
// carries an entry with a `signature.bundle` URL — enough to flip the
// per-entry verifier path on inside LoadBytesWithVerifier.
const signedEntryYAML = `addons:
  - name: signed-one
    description: Signed addon for per-entry verifier tests.
    chart: signed-one
    repo: https://charts.example.com
    default_namespace: signed-one
    default_sync_wave: 10
    license: Apache-2.0
    category: observability
    curated_by: [cncf-sandbox]
    maintainers: [test@example.com]
    signature:
      bundle: https://example.com/signed-one.bundle
`

// TestFetcher_PerEntryVerify_Trusted (AC-3 happy path) — when an entry
// has a `signature.bundle` URL AND the wired entryVerifyFn returns
// (true, "issuer@example", nil), the resulting snapshot's entry must
// surface Verified=true with SignatureIdentity set.
func TestFetcher_PerEntryVerify_Trusted(t *testing.T) {
	ff := &flippable{status: http.StatusOK, body: signedEntryYAML}
	srv := httptest.NewTLSServer(ff.handler())
	t.Cleanup(srv.Close)

	var calls atomic.Int32
	verifyFn := func(_ context.Context, _ []byte, bundleURL string) (bool, string, error) {
		calls.Add(1)
		if bundleURL != "https://example.com/signed-one.bundle" {
			t.Errorf("verifyFn got bundleURL %q, want stub URL", bundleURL)
		}
		return true, "issuer@example", nil
	}

	f := newTestFetcher(t, []*httptest.Server{srv}, nil)
	f.SetEntryVerifyFunc(verifyFn)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	f.ForceRefresh(ctx)

	if got := calls.Load(); got != 1 {
		t.Fatalf("entry verifyFn calls = %d, want exactly 1", got)
	}
	snap := f.Snapshots()[srv.URL+"/catalog.yaml"]
	if snap == nil {
		t.Fatal("expected snapshot to exist")
	}
	if snap.Status != StatusOK {
		t.Fatalf("status = %q, want %q (err=%v)", snap.Status, StatusOK, snap.LastErr)
	}
	if len(snap.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(snap.Entries))
	}
	e := snap.Entries[0]
	if !e.Verified {
		t.Errorf("entry Verified = false, want true (verifyFn returned (true, ...))")
	}
	if e.SignatureIdentity != "issuer@example" {
		t.Errorf("entry SignatureIdentity = %q, want %q", e.SignatureIdentity, "issuer@example")
	}
	// Sanity: the type assertion that loader populated this entry is
	// the catalog.CatalogEntry shape, not a fetcher-local copy.
	var _ catalog.CatalogEntry = e
}

// TestNewFetcher_DoesNotAutoLoadTrustPolicy (V123-PR-F1 / M5) pins the
// regression: NewFetcher MUST NOT read SHARKO_CATALOG_TRUSTED_IDENTITIES
// itself. The canonical loader is signing.LoadTrustPolicyFromEnv, called
// once at startup in cmd/sharko/serve.go and pushed into the fetcher via
// SetTrustPolicy. Pre-fix the fetcher had its own divergent loader that
// applied no <defaults> expansion, producing a different policy from the
// embedded catalog's verifier on the same env var.
func TestNewFetcher_DoesNotAutoLoadTrustPolicy(t *testing.T) {
	// Set the env var that the OLD divergent loader would have read.
	// If the regression returns, the new fetcher would pick it up here
	// and the assertion below would fail.
	t.Setenv("SHARKO_CATALOG_TRUSTED_IDENTITIES", "https://github.com/foo/.*,https://github.com/bar/.*")

	cfg := &config.CatalogSourcesConfig{RefreshInterval: config.MinRefreshInterval}
	f := NewFetcher(cfg, nil, nil)

	if got := len(f.trustPolicy.Identities); got != 0 {
		t.Fatalf("trustPolicy.Identities len = %d, want 0 — NewFetcher must not auto-load from SHARKO_CATALOG_TRUSTED_IDENTITIES",
			got)
	}

	// Sanity: the explicit setter still works after construction so
	// production wiring (serve.go) can hand the canonical policy in.
	f.SetTrustPolicy(TrustPolicy{Identities: []string{"https://github.com/explicit/.*"}})
	if got := len(f.trustPolicy.Identities); got != 1 {
		t.Fatalf("after SetTrustPolicy: trustPolicy.Identities len = %d, want 1", got)
	}
}

// --- V123-PR-F2 / M6: DialContext pinning -----------------------------

// TestFetcher_DialerPinning_RejectsPostResolveRebinding (M6) covers the
// TOCTOU window between runtimeSSRFCheckResolvedIPs and the actual TCP
// connect. We stub lookupHostFn to return a public IP first (which the
// SSRF guard validates and the fetcher pins) and a private IP on the
// second resolve (the dialer's own lookup). The pinned dialer must
// reject the private IP — closing the DNS rebinding window — and the
// fetch must record a failure whose error blames the pre-validated IP
// set, NOT a generic "connection refused" or a successful connect to
// the private address.
func TestFetcher_DialerPinning_RejectsPostResolveRebinding(t *testing.T) {
	const host = "rebinding.example.com"
	const sourceURL = "http://" + host + "/catalog.yaml"

	// Sequence the resolver so call #1 returns the validated public IP
	// (203.0.113.50 = TEST-NET-3, classified as public by isPrivateAddr)
	// and call #2+ returns a private IP (10.0.0.5). The SSRF guard runs
	// the first call; the pinned dialer's hostname resolve runs the
	// second; any subsequent resolve also gets the private IP, so even
	// a retry stays rejected.
	var calls atomic.Int32
	origLookup := lookupHostFn
	t.Cleanup(func() { lookupHostFn = origLookup })
	lookupHostFn = func(h string) ([]string, error) {
		if h != host {
			return origLookup(h)
		}
		n := calls.Add(1)
		if n == 1 {
			return []string{"203.0.113.50"}, nil
		}
		return []string{"10.0.0.5"}, nil
	}

	cfg := &config.CatalogSourcesConfig{
		Sources:         []config.CatalogSource{{URL: sourceURL}},
		RefreshInterval: config.MinRefreshInterval,
		AllowPrivate:    false, // SSRF guard ON → pinning ON
	}
	f := NewFetcher(cfg, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	f.ForceRefresh(ctx)

	snap := f.Snapshots()[sourceURL]
	if snap == nil {
		t.Fatalf("expected snapshot for %s", sourceURL)
	}
	if snap.Status != StatusFailed {
		t.Fatalf("expected StatusFailed after pinned-dialer rejection, got %q (err=%v)", snap.Status, snap.LastErr)
	}
	if snap.LastErr == nil {
		t.Fatal("expected LastErr to record the pinning rejection")
	}
	msg := snap.LastErr.Error()
	if !strings.Contains(msg, "pre-validated") {
		t.Fatalf("expected pinning-flavoured error mentioning pre-validated set, got %v", snap.LastErr)
	}
	// The private rebinding IP must NOT appear in any "successful connect"
	// trace — the only legitimate use of "10.0.0.5" in the error is when
	// it surfaces as the rejected IP itself, which is fine. Confirm at
	// minimum that no successful HTTP code (any 2xx) leaked through.
	if snap.Status == StatusOK {
		t.Fatal("fetch unexpectedly succeeded — TOCTOU window not closed")
	}
	// Sanity: lookupHostFn was called at least twice (once for SSRF check,
	// once for the dialer). Less than 2 means the pinned path didn't
	// actually re-resolve, which would be a regression on the test seam.
	if got := calls.Load(); got < 2 {
		t.Fatalf("expected lookupHostFn to be called at least twice (SSRF + dialer), got %d", got)
	}
}

// TestFetcher_DialerPinning_LiteralPublicIPSucceeds (M6 sanity) — when
// the source URL is a literal public IP (no DNS), runtimeSSRFCheckResolvedIPs
// returns the parsed IP and the pinned dialer must accept it on the
// dial path. We use httptest.NewServer (loopback) but route through the
// pinning code path with AllowPrivate=true so the loopback isn't
// rejected by the SSRF guard. Without AllowPrivate the pinning path is
// bypassed entirely (httpGet, not httpGetPinned), so this test
// specifically exercises the literal-IP arm of httpGetPinned via a
// helper that exposes it directly.
func TestFetcher_DialerPinning_LiteralPublicIPSucceeds(t *testing.T) {
	ff := &flippable{status: http.StatusOK, body: validCatalogYAML}
	srv := httptest.NewServer(ff.handler())
	t.Cleanup(srv.Close)

	cfg := &config.CatalogSourcesConfig{
		Sources:         []config.CatalogSource{{URL: srv.URL + "/catalog.yaml"}},
		RefreshInterval: config.MinRefreshInterval,
		AllowPrivate:    true, // skip SSRF rejection on 127.0.0.1
	}
	f := NewFetcher(cfg, nil, nil)
	f.SetHTTPClientForTest(srv.Client())

	// Resolve the literal IP from srv.URL and call httpGetPinned directly
	// with that IP pinned. This exercises the literal-IP arm without
	// fighting the AllowPrivate=false SSRF rejection on a loopback test
	// server.
	parsed, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse srv URL: %v", err)
	}
	ip, perr := netip.ParseAddr(parsed.Hostname())
	if perr != nil {
		t.Fatalf("parse srv host as IP: %v", perr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	body, err := f.httpGetPinned(ctx, srv.URL+"/catalog.yaml", []netip.Addr{ip})
	if err != nil {
		t.Fatalf("httpGetPinned with matching pinned literal IP failed: %v", err)
	}
	if !strings.Contains(string(body), "addons:") {
		t.Fatalf("expected catalog body, got %q", string(body))
	}
}

// TestFetcher_DialerPinning_LiteralRejectsUnpinnedIP (M6 negative) —
// httpGetPinned with a pinned set that does NOT include the URL's
// literal IP must reject the dial. Direct invocation exercises the
// literal-IP arm of the pinning DialContext without fighting AllowPrivate.
func TestFetcher_DialerPinning_LiteralRejectsUnpinnedIP(t *testing.T) {
	ff := &flippable{status: http.StatusOK, body: validCatalogYAML}
	srv := httptest.NewServer(ff.handler())
	t.Cleanup(srv.Close)

	cfg := &config.CatalogSourcesConfig{
		Sources:         []config.CatalogSource{{URL: srv.URL + "/catalog.yaml"}},
		RefreshInterval: config.MinRefreshInterval,
		AllowPrivate:    true,
	}
	f := NewFetcher(cfg, nil, nil)
	f.SetHTTPClientForTest(srv.Client())

	otherIP := netip.MustParseAddr("203.0.113.99")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := f.httpGetPinned(ctx, srv.URL+"/catalog.yaml", []netip.Addr{otherIP})
	if err == nil {
		t.Fatal("expected httpGetPinned to reject unpinned literal IP")
	}
	if !strings.Contains(err.Error(), "pre-validated") {
		t.Fatalf("expected pinning-flavoured error, got %v", err)
	}
}

// --- V123-PR-F2 / L1: CheckRedirect SSRF re-check ---------------------

// TestFetcher_CheckRedirect_RejectsPrivateRedirectTarget (L1) covers
// the redirect-time SSRF re-check. An httptest server returns 302 with
// a Location pointing at http://10.0.0.5/secret. The fetcher's
// CheckRedirect callback must re-run the SSRF guard on the redirect
// target and abort the chain BEFORE any TCP connect to 10.0.0.5
// happens. The fetch records the abort as a failure with an error
// mentioning the redirect rejection.
//
// Test wiring: to drive the production code path (httpGetPinned, which
// is what fetchOne uses under AllowPrivate=false), we stub lookupHostFn
// so a fake-public hostname resolves to a TEST-NET-3 IP (passes SSRF
// check, ends up in the pin set), AND we override pinnedBaseDialFn so
// the post-pin dial reroutes back to the real httptest listener. The
// override is intentionally AFTER the pin check, so a misuse of this
// seam cannot weaken the security invariant. ANY attempted dial to
// 10.0.0.5 increments privateHits — that's the regression detector.
func TestFetcher_CheckRedirect_RejectsPrivateRedirectTarget(t *testing.T) {
	mux := http.NewServeMux()
	var redirectHits atomic.Int32
	var privateHits atomic.Int32
	mux.HandleFunc("/catalog.yaml", func(w http.ResponseWriter, r *http.Request) {
		redirectHits.Add(1)
		w.Header().Set("Location", "http://10.0.0.5/secret")
		w.WriteHeader(http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	const fakeHost = "public-redirect.example.invalid"
	srvURL, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse srv URL: %v", err)
	}
	srvHost := srvURL.Hostname()
	srvPort := srvURL.Port()

	const fakePublicIP = "203.0.113.77"
	origLookup := lookupHostFn
	t.Cleanup(func() { lookupHostFn = origLookup })
	lookupHostFn = func(h string) ([]string, error) {
		if h == fakeHost {
			return []string{fakePublicIP}, nil
		}
		return origLookup(h)
	}

	origDial := pinnedBaseDialFn
	t.Cleanup(func() { pinnedBaseDialFn = origDial })
	pinnedBaseDialFn = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, _ := net.SplitHostPort(addr)
		if host == "10.0.0.5" {
			privateHits.Add(1)
			return nil, errors.New("test dialer: refused to connect to private redirect target")
		}
		// Reroute the fake-public IP to the real httptest listener.
		return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, network, net.JoinHostPort(srvHost, srvPort))
	}

	cfg := &config.CatalogSourcesConfig{
		Sources:         []config.CatalogSource{{URL: "http://" + fakeHost + ":" + srvPort + "/catalog.yaml"}},
		RefreshInterval: config.MinRefreshInterval,
		AllowPrivate:    false, // CheckRedirect guard ON
	}
	f := NewFetcher(cfg, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	f.ForceRefresh(ctx)

	if redirectHits.Load() < 1 {
		t.Fatal("expected the httptest server to receive at least one request")
	}
	if got := privateHits.Load(); got != 0 {
		t.Fatalf("CheckRedirect failed to abort: dialer was asked to connect to 10.0.0.5 %d time(s)", got)
	}

	snap := f.Snapshots()["http://"+fakeHost+":"+srvPort+"/catalog.yaml"]
	if snap == nil {
		t.Fatal("expected snapshot to exist after redirect rejection")
	}
	if snap.Status != StatusFailed {
		t.Fatalf("expected StatusFailed after redirect rejection, got %q (err=%v)", snap.Status, snap.LastErr)
	}
	if snap.LastErr == nil {
		t.Fatal("expected LastErr to record the redirect rejection")
	}
	msg := snap.LastErr.Error()
	if !strings.Contains(msg, "redirect to private address") &&
		!strings.Contains(msg, "10.0.0.5") {
		t.Fatalf("expected redirect-rejection-flavoured error, got %v", snap.LastErr)
	}
}

// TestFetcher_CheckRedirect_PublicChainAllowed (L1 sanity) — a chain of
// public-target redirects must still succeed. We set up two httptest
// servers; A 302's to B (both fronted by a fake-public hostname so the
// SSRF check passes), and B serves a valid catalog. The fetch must end
// in StatusOK with parsed entries.
func TestFetcher_CheckRedirect_PublicChainAllowed(t *testing.T) {
	const fakeHostA = "alpha.example.invalid"
	const fakeHostB = "bravo.example.invalid"

	muxB := http.NewServeMux()
	muxB.HandleFunc("/catalog.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(validCatalogYAML))
	})
	srvB := httptest.NewServer(muxB)
	t.Cleanup(srvB.Close)
	srvBURL, _ := url.Parse(srvB.URL)
	srvBHost := srvBURL.Hostname()
	srvBPort := srvBURL.Port()

	muxA := http.NewServeMux()
	muxA.HandleFunc("/catalog.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "http://"+fakeHostB+":"+srvBPort+"/catalog.yaml")
		w.WriteHeader(http.StatusFound)
	})
	srvA := httptest.NewServer(muxA)
	t.Cleanup(srvA.Close)
	srvAURL, _ := url.Parse(srvA.URL)
	srvAHost := srvAURL.Hostname()
	srvAPort := srvAURL.Port()

	const fakePublicIP = "203.0.113.88"
	origLookup := lookupHostFn
	t.Cleanup(func() { lookupHostFn = origLookup })
	lookupHostFn = func(h string) ([]string, error) {
		switch h {
		case fakeHostA, fakeHostB:
			return []string{fakePublicIP}, nil
		}
		return origLookup(h)
	}

	origDial := pinnedBaseDialFn
	t.Cleanup(func() { pinnedBaseDialFn = origDial })
	pinnedBaseDialFn = func(ctx context.Context, network, addr string) (net.Conn, error) {
		_, port, _ := net.SplitHostPort(addr)
		switch port {
		case srvAPort:
			return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, network, net.JoinHostPort(srvAHost, srvAPort))
		case srvBPort:
			return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, network, net.JoinHostPort(srvBHost, srvBPort))
		}
		return nil, errors.New("unexpected dial in public-chain test")
	}

	cfg := &config.CatalogSourcesConfig{
		Sources:         []config.CatalogSource{{URL: "http://" + fakeHostA + ":" + srvAPort + "/catalog.yaml"}},
		RefreshInterval: config.MinRefreshInterval,
		AllowPrivate:    false,
	}
	f := NewFetcher(cfg, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	f.ForceRefresh(ctx)

	snap := f.Snapshots()["http://"+fakeHostA+":"+srvAPort+"/catalog.yaml"]
	if snap == nil {
		t.Fatal("expected snapshot to exist after redirect chain")
	}
	if snap.Status != StatusOK {
		t.Fatalf("expected StatusOK after public→public redirect, got %q (err=%v)", snap.Status, snap.LastErr)
	}
	if len(snap.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(snap.Entries))
	}
}

// TestFetcher_PerEntryVerify_Untrusted (AC-3 negative path) — same
// payload, but the verifier returns (false, "", nil) (sig-mismatch /
// untrusted-identity outcome). The entry must be retained with
// Verified=false; the load itself MUST NOT fail. This is the gap the B3
// fix closes: pre-fix every signed third-party entry surfaced as
// Verified=false because the loader never even called a verifier; the
// new test makes "verifier returned untrusted" the explicit reason and
// rules out a regression where untrusted=true leaks through.
func TestFetcher_PerEntryVerify_Untrusted(t *testing.T) {
	ff := &flippable{status: http.StatusOK, body: signedEntryYAML}
	srv := httptest.NewTLSServer(ff.handler())
	t.Cleanup(srv.Close)

	var calls atomic.Int32
	verifyFn := func(_ context.Context, _ []byte, _ string) (bool, string, error) {
		calls.Add(1)
		return false, "", nil
	}

	f := newTestFetcher(t, []*httptest.Server{srv}, nil)
	f.SetEntryVerifyFunc(verifyFn)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	f.ForceRefresh(ctx)

	if got := calls.Load(); got != 1 {
		t.Fatalf("entry verifyFn calls = %d, want exactly 1", got)
	}
	snap := f.Snapshots()[srv.URL+"/catalog.yaml"]
	if snap == nil {
		t.Fatal("expected snapshot to exist")
	}
	if snap.Status != StatusOK {
		t.Fatalf("status = %q, want %q (err=%v)", snap.Status, StatusOK, snap.LastErr)
	}
	if len(snap.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(snap.Entries))
	}
	e := snap.Entries[0]
	if e.Verified {
		t.Errorf("entry Verified = true, want false (verifyFn returned (false, ...))")
	}
	if e.SignatureIdentity != "" {
		t.Errorf("entry SignatureIdentity = %q, want \"\"", e.SignatureIdentity)
	}
}
