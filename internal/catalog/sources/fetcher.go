// Fetcher — periodic HTTP puller for third-party catalog URLs.
//
// Lifecycle:
//
//	f := NewFetcher(cfg, verifier, clock)  // verifier/clock may be nil
//	f.Start(ctx)                            // non-blocking; fires initial fetch + ticker
//	...
//	f.Stop()                                // drains in-flight, stops ticker
//
// Reads:
//
//	snaps := f.Snapshots()                  // map[url]*SourceSnapshot (deep copy)
//	f.ForceRefresh(ctx, urls...)            // blocking refresh; empty = all
//
// Design notes
//
//   - "Fresh start" (AC #5): the Fetcher allocates an empty snapshot per
//     configured URL up front with Status == StatusFailed so callers that
//     hit Snapshots() before the first fetch returns get a stable shape
//     (never a nil map entry). The initial fetch then overwrites each
//     snapshot.
//   - Per-URL fetches are concurrent (one goroutine per URL per tick).
//     The supervisor loop is a single goroutine that triggers the fanout
//     on each ticker event.
//   - Runtime SSRF guard: even though startup validated configured URLs
//     against the SSRF allowlist, a public hostname can re-resolve to an
//     internal IP between startup and any given fetch. We therefore
//     re-check the resolved IPs on every fetch attempt. When
//     cfg.AllowPrivate is set the check is skipped (matches the startup
//     escape hatch).
//   - No URL is ever logged. Catalog URL paths may encode auth tokens
//     (Gotcha #1 from V123-1.1). Log with a short host fingerprint
//     instead where a log line is genuinely needed.
//   - Schema validation reuses catalog.LoadBytes — same parser + rule
//     set the embedded catalog goes through, so third-party feeds get
//     identical treatment. No refactor of loader.go was needed.
package sources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sync"
	"time"

	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/metrics"
)

// SourceStatus is the coarse last-fetch outcome for a single URL.
// It maps to the `status` field in the future `GET /api/v1/catalog/sources`
// payload (V123-1.5). Lower-case string values match design §2.6.
type SourceStatus string

const (
	// StatusOK — the most recent fetch parsed + validated cleanly.
	// Entries in the snapshot are current.
	StatusOK SourceStatus = "ok"

	// StatusStale — the most recent fetch failed (HTTP 5xx, timeout,
	// transport error) but a previous fetch had succeeded, so the
	// snapshot still carries last-known-good entries.
	StatusStale SourceStatus = "stale"

	// StatusFailed — the most recent fetch failed AND there is no
	// prior success to fall back on (fresh start), or a non-transport
	// failure (schema violation, SSRF block) invalidated the snapshot.
	// AC #3 keeps the prior successful entries retained when one exists
	// — StatusFailed on a snapshot that already has Entries means "last
	// attempt was bad, but we're still serving the old data".
	StatusFailed SourceStatus = "failed"
)

// SourceSnapshot is the per-URL state the fetcher exposes. All fields
// are set atomically under the Fetcher's mutex; callers receive deep
// copies from Snapshots() to avoid races on the Entries slice.
type SourceSnapshot struct {
	// URL is the canonical source URL (copied from config). Used as
	// the map key in Fetcher.Snapshots() too.
	URL string

	// Status is the outcome of the most recent fetch attempt.
	Status SourceStatus

	// LastSuccessAt is the timestamp of the most recent SUCCESSFUL
	// fetch — NOT the most recent attempt. Zero value means "never
	// succeeded since process start".
	LastSuccessAt time.Time

	// LastAttemptAt is the timestamp of the most recent fetch attempt
	// (regardless of outcome).
	LastAttemptAt time.Time

	// LastErr records the most recent fetch error, for surfacing on
	// the GET /api/v1/catalog/sources payload. nil when Status ==
	// StatusOK. An error on a stale/failed snapshot does NOT mean the
	// Entries are stale w.r.t. schema — Entries are only ever replaced
	// on a fully-successful parse.
	LastErr error

	// Entries is the last-successfully-fetched catalog entry slice.
	// Empty on a fresh-start failure (AC #5); retained across stale
	// fetches (AC #2) and schema failures (AC #3).
	Entries []catalog.CatalogEntry

	// Verified records whether the sidecar signature was validated
	// against the trust policy. False when: no verifier configured, no
	// sidecar discovered, sidecar failed verification. The UI treats
	// false as "Unsigned" / "Unverified".
	Verified bool

	// Issuer is the human-readable OIDC subject of the signer, when
	// Verified is true. Empty otherwise. Lined up with design §3.3.
	Issuer string
}

// Clock abstracts time-of-day and tickers so tests can drive the
// fetcher deterministically. Production uses realClock which just wraps
// time.Now / time.NewTicker.
//
// The fetcher only needs Now and NewTicker — enough for "every N
// minutes, fetch all sources". Fake clocks in tests can implement a
// channel they fire manually to simulate a tick.
type Clock interface {
	Now() time.Time
	NewTicker(d time.Duration) Ticker
}

// Ticker is the subset of time.Ticker the fetcher consumes. Abstracted
// for the same reason Clock is.
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

// realClock is the production Clock impl.
type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
func (realClock) NewTicker(d time.Duration) Ticker {
	return &realTicker{t: time.NewTicker(d)}
}

type realTicker struct{ t *time.Ticker }

func (r *realTicker) C() <-chan time.Time { return r.t.C }
func (r *realTicker) Stop()               { r.t.Stop() }

// defaultClock is exposed as a package-level var for consistency with
// the existing lookupHostFn pattern in internal/config — tests can
// substitute a fake clock via the NewFetcher constructor, which is the
// canonical API, but package-level access here makes that substitution
// testable.
var defaultClock Clock = realClock{}

// lookupHostFn wraps net.LookupHost so the runtime SSRF guard is
// stubbable in tests. Mirrors the pattern in internal/config.
var lookupHostFn = net.LookupHost

// Tunables for HTTP + sidecar probing. Kept as package vars (not
// per-Fetcher fields) so tests can shrink them for fast iteration
// without growing the constructor signature.
var (
	// httpTimeout is the wall-clock ceiling for a full catalog GET,
	// including TLS handshake + body read. Third-party catalogs are
	// expected to be small (tens of KB at most); 30s is generous
	// without being dangerous.
	httpTimeout = 30 * time.Second

	// sidecarProbeTimeout is the per-probe ceiling for HEAD requests
	// used to detect `.sig` / `.bundle` siblings. Tight because the
	// common case is "no sidecar" and we don't want to block the main
	// fetch on a slow probe.
	sidecarProbeTimeout = 5 * time.Second

	// maxCatalogBytes caps how much the fetcher will read from a
	// response body. A hostile or misconfigured source could otherwise
	// stream forever. 8 MiB is ~4x the largest realistic curated
	// catalog; bigger than that is a configuration problem.
	maxCatalogBytes int64 = 8 << 20
)

// Sidecar suffixes probed in priority order. `.sig` covers the plain
// cosign signature file; `.bundle` covers the Sigstore bundle
// (preferred per design §3.2). Checked in `.bundle`-first order so a
// bundle (if present) is used over a bare signature.
var sidecarSuffixes = []string{".bundle", ".sig"}

// Fetcher is the periodic HTTP puller for a CatalogSourcesConfig. Zero
// value is not usable — use NewFetcher.
type Fetcher struct {
	cfg        *config.CatalogSourcesConfig
	verifier   SidecarVerifier
	clock      Clock
	httpClient *http.Client
	log        *slog.Logger

	// trustPolicy is derived once at construction from the
	// SHARKO_CATALOG_TRUSTED_IDENTITIES env var (design §3.4). It is
	// passed through to every verifier.Verify call.
	trustPolicy TrustPolicy

	// entryVerifyFn is the per-entry verification callback the fetcher
	// hands to catalog.LoadBytesWithVerifier so that a third-party
	// catalog YAML carrying `signature.bundle` URLs on individual
	// entries gets each entry verified at load time. Nil means "no
	// per-entry verification" — the fetcher falls back to catalog.LoadBytes
	// (back-compat for tests + builds that haven't wired the verifier).
	//
	// V123-2.4 / B3 BLOCKER fix: pre-fix the fetcher only verified the
	// whole-file sidecar (`.bundle` next to the catalog YAML) and never
	// looked at per-entry signature URLs, letting a compromised
	// third-party curator flip an entry's bundle URL to a hostile target
	// and have Sharko serve it as if signed. The closure baked here
	// closes over the same trust policy the embedded catalog uses, so
	// the trust surface is unified across the two ingestion paths.
	//
	// IMPORTANT: this field is a `catalog.VerifyEntryFunc` (defined in
	// internal/catalog), NOT anything from internal/catalog/signing —
	// preserves the design §3.3.1 invariant that sources never imports
	// signing.
	entryVerifyFn catalog.VerifyEntryFunc

	// stopCh is closed by Stop to unblock the supervisor loop.
	stopCh chan struct{}
	// stopOnce guards stopCh close + WaitGroup wait.
	stopOnce sync.Once

	// supervisorWG tracks the single supervisor goroutine Start spawns.
	supervisorWG sync.WaitGroup
	// fetchWG tracks in-flight per-URL fetch goroutines so Stop can
	// drain them.
	fetchWG sync.WaitGroup

	// mu guards snapshots. Reads acquire RLock; writes acquire Lock.
	mu        sync.RWMutex
	snapshots map[string]*SourceSnapshot

	// refreshMu serializes the fetchAll (ticker) and fetchMany
	// (ForceRefresh) fanouts. V123-PR-B (H2): pre-fix a tick that fired
	// while ForceRefresh was in flight (or vice versa) ran two
	// simultaneous fanouts that both tried to overwrite the same per-URL
	// snapshot — racy, and -race flagged the writes during stress runs.
	// This is a coarser lock than mu (which protects the snapshot map);
	// it wraps the *entire* fanout so a slow fetch can serialize with a
	// concurrent ticker tick.
	//
	// Tradeoff: a tick that fires while a long ForceRefresh is in flight
	// will sit on this lock for up to one fetch cycle (httpTimeout = 30s
	// in production); likewise a POST /refresh arriving mid-tick has to
	// wait. That's a deliberate accept — small catalog source counts
	// (single digits in practice) make the simpler stdlib mutex
	// preferable to per-URL singleflight for the v1.23 ship. See
	// V123-pretag-highs §H2 design notes.
	refreshMu sync.Mutex

	// started is flipped the first time Start runs so a double-Start
	// is a no-op rather than a double-goroutine leak.
	started bool
}

// NewFetcher constructs a Fetcher ready to Start. Nil verifier is
// supported — the fetcher will skip sidecar verification and every
// entry will inherit Verified=false. Nil clock is supported — a
// production wall-clock is used.
//
// NewFetcher does NOT perform I/O — it only allocates. The first HTTP
// call happens inside Start.
func NewFetcher(cfg *config.CatalogSourcesConfig, verifier SidecarVerifier, clock Clock) *Fetcher {
	if cfg == nil {
		// A nil config is a programmer error — callers should at
		// minimum pass an empty CatalogSourcesConfig{}. But we guard
		// anyway so tests + misconfigured callers see a clean panic
		// nowhere (the supervisor loop would otherwise crash).
		cfg = &config.CatalogSourcesConfig{}
	}
	if clock == nil {
		clock = defaultClock
	}

	f := &Fetcher{
		cfg:       cfg,
		verifier:  verifier,
		clock:     clock,
		log:       slog.Default().With("component", "catalog-sources"),
		stopCh:    make(chan struct{}),
		snapshots: make(map[string]*SourceSnapshot, len(cfg.Sources)),
		// trustPolicy intentionally left as the zero value — V123-PR-F1 / M5
		// removed the auto-load from SHARKO_CATALOG_TRUSTED_IDENTITIES so
		// that the canonical loader in cmd/sharko/serve.go (via
		// signing.LoadTrustPolicyFromEnv) is the single source of truth.
		// Callers wire the policy explicitly via SetTrustPolicy after
		// construction; tests can use SetTrustPolicyForTest.
	}

	// Build a conservative HTTP client. Proxy support via
	// ProxyFromEnvironment satisfies AC #4 (HTTPS_PROXY / HTTP_PROXY /
	// NO_PROXY). IdleConnTimeout trims idle keep-alives so we don't
	// hold file descriptors against unreachable hosts.
	f.httpClient = &http.Client{
		Timeout: httpTimeout,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			MaxIdleConns:          10,
			IdleConnTimeout:       90 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 20 * time.Second,
			DisableCompression:    false,
		},
	}

	// Seed snapshots with a failed placeholder per configured URL so
	// callers that read Snapshots() before the first fetch completes
	// get a stable, non-nil map value per URL.
	for _, src := range cfg.Sources {
		f.snapshots[src.URL] = &SourceSnapshot{
			URL:    src.URL,
			Status: StatusFailed,
		}
	}

	return f
}

// HTTPClientForTest exposes the internal HTTP client so tests can
// inspect its transport config (e.g. the Proxy function for AC #4).
// Test-only — production code has no reason to read this.
func (f *Fetcher) HTTPClientForTest() *http.Client { return f.httpClient }

// SetHTTPClientForTest overrides the internal HTTP client. Test-only
// helper — production code never calls this. Needed so httptest.Server
// (which uses a self-signed cert) can be reached without disabling TLS
// verification in the production code path. Must be called BEFORE
// Start; calling after Start produces undefined behaviour because an
// in-flight fetch may already hold the old client.
func (f *Fetcher) SetHTTPClientForTest(c *http.Client) {
	f.httpClient = c
}

// SetTrustPolicyForTest overrides the trust policy without acquiring
// the fetcher's mutex. Test-only sibling of SetTrustPolicy — kept for
// hermetic unit tests that want to override the policy on a fetcher
// they constructed in-process and never start. Production wiring goes
// through SetTrustPolicy (V123-PR-F1 / M5), which serializes against
// in-flight fetches via the same lock pattern as SetEntryVerifyFunc.
func (f *Fetcher) SetTrustPolicyForTest(tp TrustPolicy) {
	f.trustPolicy = tp
}

// SetTrustPolicy installs the catalog trust policy on the fetcher.
// Production callers in cmd/sharko/serve.go pass the policy loaded by
// signing.LoadTrustPolicyFromEnv so the third-party fetcher and the
// embedded catalog share a single trust surface
// (V123-PR-F1 / M5: pre-fix the fetcher loaded the policy itself
// from SHARKO_CATALOG_TRUSTED_IDENTITIES with no <defaults> expansion,
// diverging from signing.LoadTrustPolicyFromEnv on the same env var).
//
// Mirrors the SetEntryVerifyFunc lock convention so a concurrent
// caller cannot trip a data-race against an in-flight fetch reading
// the field. Safe to call before or after Start.
func (f *Fetcher) SetTrustPolicy(tp TrustPolicy) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.trustPolicy = tp
}

// SetEntryVerifyFunc installs the per-entry verification callback used
// by fetchOne when a third-party catalog's individual entries carry
// `signature.bundle` URLs (V123-2.4 / B3 BLOCKER fix). Production callers
// in cmd/sharko/serve.go pass `catalogVerifier.VerifyEntryFunc(trustPolicy)`
// — the same closure that gates the embedded catalog so both paths
// share one trust surface.
//
// Nil is acceptable and resets the fetcher to the no-verification fast
// path (catalog.LoadBytes). Tests that never wire a verifier rely on
// this back-compat — see the existing fetcher tests.
//
// Mirrors the SetTrustPolicyForTest lock convention so a concurrent
// caller cannot trip a data-race against an in-flight fetch reading
// the field. Safe to call before or after Start.
func (f *Fetcher) SetEntryVerifyFunc(fn catalog.VerifyEntryFunc) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entryVerifyFn = fn
}

// SetSnapshotsForTest replaces the in-memory snapshot map. Test-only
// helper so callers (V123-1.4 merged-catalog handler tests, etc.) can
// construct a Fetcher with pre-populated snapshots without running a
// full HTTP fetch loop. Production code has no reason to call this — the
// supervisor + fetchOne path is the only writer in real deployments.
//
// The provided map is stored by reference; callers should not mutate it
// after passing it in. Safe to call before or instead of Start.
func (f *Fetcher) SetSnapshotsForTest(snaps map[string]*SourceSnapshot) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if snaps == nil {
		f.snapshots = make(map[string]*SourceSnapshot)
		return
	}
	f.snapshots = snaps
}

// Start fires an initial fetch of every configured URL and then spawns
// a supervisor goroutine that re-fetches on the configured interval
// until Stop is called or ctx is cancelled. Safe to call once; a
// second Start is a no-op.
//
// Start is non-blocking — it kicks off the initial fetch in a goroutine
// so a slow upstream doesn't block server boot.
func (f *Fetcher) Start(ctx context.Context) {
	f.mu.Lock()
	if f.started {
		f.mu.Unlock()
		return
	}
	f.started = true
	f.mu.Unlock()

	// No sources configured → nothing to fetch. Supervisor is a no-op
	// but we still register Stop as safe-to-call by returning cleanly.
	if len(f.cfg.Sources) == 0 {
		return
	}

	f.supervisorWG.Add(1)
	go f.supervise(ctx)
}

// supervise runs the initial fetch fanout + the ticker-driven
// periodic fanout. Returns when ctx is cancelled or Stop is called.
func (f *Fetcher) supervise(ctx context.Context) {
	defer f.supervisorWG.Done()

	// Initial fetch — happens immediately, not after the first tick,
	// so a freshly-started server has entries as soon as possible.
	f.fetchAll(ctx)

	interval := f.cfg.RefreshInterval
	if interval <= 0 {
		interval = config.DefaultRefreshInterval
	}
	ticker := f.clock.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-f.stopCh:
			return
		case <-ticker.C():
			// Check cancellation again after the tick — an immediate
			// Stop right after a tick fire shouldn't kick off a new
			// fanout.
			select {
			case <-ctx.Done():
				return
			case <-f.stopCh:
				return
			default:
			}
			f.fetchAll(ctx)
		}
	}
}

// Stop drains in-flight fetches and unblocks the supervisor loop. Safe
// to call multiple times (second+ calls are no-ops).
//
// Stop does NOT cancel a caller's ctx — it only signals the supervisor.
// If you want hard cancellation of an in-flight HTTP call, pass a
// cancellable ctx to Start and cancel it.
func (f *Fetcher) Stop() {
	f.stopOnce.Do(func() {
		close(f.stopCh)
	})
	f.supervisorWG.Wait()
	f.fetchWG.Wait()
}

// Snapshots returns a deep copy of the current per-URL state. Safe for
// concurrent callers. The returned map + all *SourceSnapshot values +
// Entries slices are fresh allocations, so callers can mutate them
// without affecting the fetcher.
//
// Deterministic iteration order is NOT guaranteed (map). Callers that
// need a stable order should sort by URL; the V123-1.3 merge story
// will do exactly that.
func (f *Fetcher) Snapshots() map[string]*SourceSnapshot {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make(map[string]*SourceSnapshot, len(f.snapshots))
	for k, v := range f.snapshots {
		out[k] = copySnapshot(v)
	}
	return out
}

// ForceRefresh triggers an immediate refresh. With no URLs provided,
// every configured source is refreshed. With URLs, only those matching
// configured sources are refreshed (unknown URLs are silently ignored,
// since configured-URL drift would otherwise turn a benign API call
// into a cryptic error).
//
// ForceRefresh blocks until every targeted fetch completes so the
// future V123-1.6 admin endpoint can return a deterministic response
// body.
func (f *Fetcher) ForceRefresh(ctx context.Context, urls ...string) {
	targets := f.resolveTargets(urls)
	if len(targets) == 0 {
		return
	}
	f.fetchMany(ctx, targets)
}

// resolveTargets narrows the provided URL list to configured sources,
// or returns every configured source when the list is empty.
func (f *Fetcher) resolveTargets(urls []string) []string {
	if len(urls) == 0 {
		out := make([]string, 0, len(f.cfg.Sources))
		for _, s := range f.cfg.Sources {
			out = append(out, s.URL)
		}
		return out
	}
	configured := make(map[string]struct{}, len(f.cfg.Sources))
	for _, s := range f.cfg.Sources {
		configured[s.URL] = struct{}{}
	}
	var out []string
	for _, u := range urls {
		if _, ok := configured[u]; ok {
			out = append(out, u)
		}
	}
	return out
}

// fetchAll is the internal helper for the supervisor's per-tick fanout
// over every configured source. It blocks for the duration of the
// fanout (changed in V123-PR-B/H2): the supervisor calls fetchAll
// serially, so we hold f.refreshMu for the whole tick to keep
// ticker-driven and ForceRefresh-driven fanouts from racing on the same
// snapshot map. In-flight per-URL fetches are still tracked via fetchWG
// so Stop can drain them.
//
// Pre-H2 the function was non-blocking — it spawned goroutines and
// returned. The supervisor still ran serially (one tick at a time) so
// nothing observable changed for ticker callers; the difference matters
// only when ForceRefresh races against a tick.
func (f *Fetcher) fetchAll(ctx context.Context) {
	f.refreshMu.Lock()
	defer f.refreshMu.Unlock()

	var wg sync.WaitGroup
	for _, src := range f.cfg.Sources {
		src := src
		wg.Add(1)
		f.fetchWG.Add(1)
		go func() {
			defer wg.Done()
			defer f.fetchWG.Done()
			f.fetchOne(ctx, src.URL)
		}()
	}
	wg.Wait()
}

// fetchMany is the blocking equivalent of fetchAll used by
// ForceRefresh. It spins up one goroutine per URL, waits for all to
// finish, and returns. V123-PR-B (H2) added the f.refreshMu lock so a
// ForceRefresh queues behind any in-flight ticker fanout (and vice
// versa).
func (f *Fetcher) fetchMany(ctx context.Context, urls []string) {
	f.refreshMu.Lock()
	defer f.refreshMu.Unlock()

	var wg sync.WaitGroup
	for _, u := range urls {
		u := u
		wg.Add(1)
		f.fetchWG.Add(1)
		go func() {
			defer wg.Done()
			defer f.fetchWG.Done()
			f.fetchOne(ctx, u)
		}()
	}
	wg.Wait()
}

// fetchOne does a single source pull: runtime SSRF check, HTTP GET,
// schema validation, optional sidecar verification, snapshot update.
// Always returns normally — errors are recorded on the snapshot.
func (f *Fetcher) fetchOne(ctx context.Context, rawURL string) {
	startAt := f.clock.Now()
	fingerprint := urlFingerprint(rawURL)

	// Runtime SSRF guard. Startup validated the URL was not resolving
	// to a private IP at boot; a public hostname can re-resolve to an
	// internal IP via DNS rebinding or ops-level mistake between
	// startup and now. Skip when cfg.AllowPrivate is set (matches the
	// startup escape hatch).
	if !f.cfg.AllowPrivate {
		if err := runtimeSSRFCheck(rawURL); err != nil {
			f.log.Warn("catalog source blocked by runtime SSRF guard",
				"source_fp", fingerprint, "err", err.Error())
			f.recordFailure(rawURL, startAt, err)
			return
		}
	}

	// Main GET.
	body, err := f.httpGet(ctx, rawURL)
	if err != nil {
		f.log.Warn("catalog source fetch failed",
			"source_fp", fingerprint, "err", err.Error())
		f.recordFailure(rawURL, startAt, err)
		return
	}

	// Schema validation — reuse the existing loader. On failure the
	// prior snapshot's entries are retained (AC #3) but status flips
	// to StatusFailed and the error is stored.
	//
	// V123-2.4 / B3 BLOCKER fix: when an entry-level verify function
	// is wired (the production path) we use LoadBytesWithVerifier so
	// per-entry `signature.bundle` URLs get verified inline at load
	// time. The nil-fn fallback to LoadBytes preserves behaviour for
	// tests that construct a Fetcher without a signing-package wrapper
	// — those tests never wire SetEntryVerifyFunc and continue to see
	// Verified=false on every entry, matching the pre-fix behaviour
	// they were calibrated against.
	f.mu.RLock()
	entryFn := f.entryVerifyFn
	f.mu.RUnlock()

	var cat *catalog.Catalog
	if entryFn != nil {
		cat, err = catalog.LoadBytesWithVerifierAndSource(ctx, body, entryFn, rawURL)
	} else {
		cat, err = catalog.LoadBytesWithSource(body, rawURL)
	}
	if err != nil {
		validationErr := fmt.Errorf("schema validation: %w", err)
		f.log.Warn("catalog source schema validation failed",
			"source_fp", fingerprint, "err", validationErr.Error())
		f.recordSchemaFailure(rawURL, startAt, validationErr)
		return
	}
	entries := cat.Entries()

	// Sidecar verification (optional). Only probed when a verifier is
	// wired in — keeps fetcher independent of the signing package when
	// Subsystem B isn't compiled in yet. On any verifier error (not
	// just "bad signature" but also "sidecar fetch errored"), fall back
	// to Verified=false — the story contract is "successful fetch +
	// best-effort verification". A schema-valid catalog with a broken
	// signature still merges; it's just flagged unverified.
	var verified bool
	var issuer string
	if f.verifier != nil {
		if sidecarURL, found := f.findSidecar(ctx, rawURL); found {
			v, iss, verr := f.verifier.Verify(ctx, body, sidecarURL, f.trustPolicy)
			if verr != nil {
				f.log.Warn("catalog source sidecar verification errored",
					"source_fp", fingerprint, "err", verr.Error())
			} else {
				verified = v
				issuer = iss
			}
		}
	}

	// Record success.
	f.recordSuccess(rawURL, startAt, entries, verified, issuer)
}

// httpGet performs the main GET with body-size clamp.
func (f *Fetcher) httpGet(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/yaml, text/yaml, */*;q=0.5")
	req.Header.Set("User-Agent", "sharko-catalog-fetcher/1.0")

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Drain a small portion of body for debugging but don't store it.
		_, _ = io.CopyN(io.Discard, resp.Body, 1024)
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCatalogBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if int64(len(body)) > maxCatalogBytes {
		return nil, fmt.Errorf("response exceeds %d bytes", maxCatalogBytes)
	}
	return body, nil
}

// findSidecar HEAD-probes the candidate sidecar suffixes. Returns the
// first 2xx match (by suffix priority) and true. A probe error or a
// non-2xx response is silently treated as "no sidecar", which matches
// the "unsigned catalogs are a valid state" rule.
func (f *Fetcher) findSidecar(ctx context.Context, rawURL string) (string, bool) {
	probeCtx, cancel := context.WithTimeout(ctx, sidecarProbeTimeout)
	defer cancel()

	for _, suffix := range sidecarSuffixes {
		candidate := rawURL + suffix
		req, err := http.NewRequestWithContext(probeCtx, http.MethodHead, candidate, nil)
		if err != nil {
			continue
		}
		resp, err := f.httpClient.Do(req)
		if err != nil {
			continue
		}
		// Drain + close — HEAD bodies should be empty but be defensive.
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return candidate, true
		}
	}
	return "", false
}

// recordSuccess replaces the snapshot's Entries + marks Status OK.
func (f *Fetcher) recordSuccess(rawURL string, at time.Time, entries []catalog.CatalogEntry, verified bool, issuer string) {
	f.mu.Lock()
	snap := f.snapshots[rawURL]
	if snap == nil {
		snap = &SourceSnapshot{URL: rawURL}
		f.snapshots[rawURL] = snap
	}
	snap.Status = StatusOK
	snap.LastAttemptAt = at
	snap.LastSuccessAt = at
	snap.LastErr = nil
	snap.Entries = entries
	snap.Verified = verified
	snap.Issuer = issuer
	entryCount := len(entries)
	f.mu.Unlock()

	metrics.CatalogSourceFetchTotal.WithLabelValues(rawURL, string(StatusOK)).Inc()
	metrics.CatalogSourceLastSuccess.WithLabelValues(rawURL).Set(float64(at.Unix()))
	metrics.CatalogSourceEntries.WithLabelValues(rawURL).Set(float64(entryCount))
}

// recordFailure marks Status stale (prior success exists) or failed
// (no prior success). AC #2 + AC #5.
func (f *Fetcher) recordFailure(rawURL string, at time.Time, err error) {
	f.mu.Lock()
	snap := f.snapshots[rawURL]
	if snap == nil {
		snap = &SourceSnapshot{URL: rawURL}
		f.snapshots[rawURL] = snap
	}
	snap.LastAttemptAt = at
	snap.LastErr = err
	if !snap.LastSuccessAt.IsZero() {
		snap.Status = StatusStale
	} else {
		snap.Status = StatusFailed
	}
	status := snap.Status
	f.mu.Unlock()

	metrics.CatalogSourceFetchTotal.WithLabelValues(rawURL, string(status)).Inc()
}

// recordSchemaFailure is the dedicated path for AC #3: validation
// failure retains prior Entries but surfaces StatusFailed so the
// future API payload exposes the problem. Distinct from
// recordFailure because an HTTP 5xx with a prior success is "stale"
// (last good data still valid), whereas a schema violation is
// "failed" — the upstream returned something, but it's broken.
func (f *Fetcher) recordSchemaFailure(rawURL string, at time.Time, err error) {
	f.mu.Lock()
	snap := f.snapshots[rawURL]
	if snap == nil {
		snap = &SourceSnapshot{URL: rawURL}
		f.snapshots[rawURL] = snap
	}
	snap.Status = StatusFailed
	snap.LastAttemptAt = at
	snap.LastErr = err
	// Entries + LastSuccessAt preserved intentionally — serving
	// last-good data is the whole point of a "last-successful snapshot".
	f.mu.Unlock()

	metrics.CatalogSourceFetchTotal.WithLabelValues(rawURL, string(StatusFailed)).Inc()
}

// runtimeSSRFCheck re-resolves the host and fails if any IP is
// private/loopback/link-local. Uses the same classification rules as
// the startup guard in internal/config so an attacker can't bypass the
// guard by configuring a public hostname that later resolves to a
// private IP (DNS rebinding). Literal IPs are re-checked too for
// defense in depth.
func runtimeSSRFCheck(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse url: %w", err)
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("missing host")
	}

	// Literal IP case.
	if addr, perr := netip.ParseAddr(host); perr == nil {
		if isPrivateAddr(addr) {
			return fmt.Errorf("resolves to private address %s", addr)
		}
		return nil
	}

	// Hostname case.
	ips, err := lookupHostFn(host)
	if err != nil {
		// DNS failure is NOT an SSRF rejection — let the HTTP layer
		// surface the lookup failure via its own error path. Refusing
		// to fetch because DNS briefly failed would turn transient
		// resolver hiccups into a catalog blackout.
		return nil
	}
	for _, ip := range ips {
		addr, perr := netip.ParseAddr(ip)
		if perr != nil {
			continue
		}
		if isPrivateAddr(addr) {
			return fmt.Errorf("host %s resolves to private address %s", host, addr)
		}
	}
	return nil
}

// isPrivateAddr mirrors internal/config.isPrivateAddr — kept local so
// the fetcher does not import a private helper from internal/config.
func isPrivateAddr(addr netip.Addr) bool {
	if addr.Is4In6() {
		addr = addr.Unmap()
	}
	return addr.IsPrivate() ||
		addr.IsLoopback() ||
		addr.IsLinkLocalUnicast() ||
		addr.IsUnspecified()
}

// urlFingerprint returns a short, non-reversible identifier for a URL
// that's safe to include in logs. Never log the URL itself — paths may
// encode auth tokens (Gotcha #1). The fingerprint is a 10-char prefix
// of SHA-256(url).
func urlFingerprint(u string) string {
	sum := sha256.Sum256([]byte(u))
	return hex.EncodeToString(sum[:])[:10]
}

// copySnapshot deep-copies a *SourceSnapshot so callers can mutate
// without racing the fetcher.
func copySnapshot(in *SourceSnapshot) *SourceSnapshot {
	if in == nil {
		return nil
	}
	out := *in
	if in.Entries != nil {
		out.Entries = make([]catalog.CatalogEntry, len(in.Entries))
		copy(out.Entries, in.Entries)
	}
	return &out
}

