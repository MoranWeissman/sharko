package sources

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
