package catalog

// freshness.go — v4 Wave 1 Story 3.4: the version-freshness scheduler.
//
// Background: catalog_versions.go's per-request handler already fetches
// Helm repo indexes on demand (15-minute local cache, keyed per repo+chart).
// That answers "what versions exist" fast when a user has a modal open, but
// it gives no honest answer to "when did Sharko last actually check" for an
// addon nobody has opened yet — the TTL cache is silent until the first
// request warms it, and every warm entry's age is bounded by whoever
// happened to click last, not by a real operational cadence.
//
// FreshnessScheduler is a background job — mirrors internal/secrets.
// Reconciler's shape (ticker + non-blocking Trigger() channel + sync.Once
// stop, per go-expert.md conventions) — that walks the curated catalog on a
// fixed cadence (daily by default) and keeps a durable "last checked"
// snapshot per addon, independent of who's browsing. It also runs the v4
// engine pin-bump check (orchestrator.CheckEnginePin) on the same cycle, so
// GET /api/v1/engine/pin can serve a recent result instead of failing hard
// when there is no live Git connection at request time (never an error page
// for stale-but-dated data — the story's own framing).
//
// Every fetch failure degrades to Unknown/Err rather than being surfaced as
// an error to a caller: a scheduler tick with one broken repo must not stall
// or corrupt the snapshots for every other addon.
import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/MoranWeissman/sharko/internal/credsafe"
	"github.com/MoranWeissman/sharko/internal/helm"
)

// DefaultFreshnessInterval is the default refresh cadence — daily, per the
// story's acceptance criteria ("the schedule is configurable with daily as
// default").
const DefaultFreshnessInterval = 24 * time.Hour

// VersionsLister is the subset of *helm.Fetcher the scheduler depends on.
// An interface (not a direct *helm.Fetcher field) so tests can inject a
// fake without touching a real Helm repo.
type VersionsLister interface {
	ListVersions(ctx context.Context, repo, chart string) ([]helm.ChartVersion, error)
}

// VersionSnapshot is the last-known state of one addon's chart versions, as
// of CheckedAt. Unknown mirrors the graceful-degrade contract
// catalog_versions.go already established for oci:// repos (v4 wave 1 Story
// 3.3) — extended here to ANY fetch failure, so a transient repo outage
// during a scheduled pass shows "last checked at <time>, unknown" rather
// than silently keeping (or worse, discarding) the previous snapshot's
// version list without saying so.
type VersionSnapshot struct {
	Versions  []helm.ChartVersion
	CheckedAt time.Time
	Unknown   bool
	Err       string // informational only; never surfaced as an HTTP error

	// Scope says which list this snapshot came from: ScopeCatalog for an
	// addon the org approved (its repoURL, read out of catalog.yaml) or
	// ScopeMarketplace for one that only exists in the list Sharko ships.
	// An approved addon wins when both lists carry the name — what the org
	// actually deploys is the entry in its own file.
	Scope string

	// NoDataReason is a complete, plain-English sentence for why there is
	// no version list, empty when there is one. It exists so the UI can say
	// "no freshness data for this source" and mean it, instead of a blank
	// cell that reads like "up to date". Never invent a result: a chart
	// repo Sharko cannot read an index for has no answer, and saying so is
	// the answer.
	NoDataReason string
}

// Scope values for VersionSnapshot.Scope.
const (
	// ScopeCatalog — the addon is in the org's catalog.yaml.
	ScopeCatalog = "catalog"
	// ScopeMarketplace — the addon is only in the list Sharko ships.
	ScopeMarketplace = "marketplace"
)

// ApprovedAddon is one entry from the org's catalog.yaml, reduced to what
// a version check needs.
type ApprovedAddon struct {
	Name    string
	RepoURL string
	Chart   string
}

// ApprovedAddonsFunc returns the org's approved addons for one scheduler
// pass. It is a closure over the live Git connection, supplied by
// cmd/sharko/serve.go, because this package has no business knowing about
// Git providers.
//
// Returning an error is the ordinary case when no Git connection is
// configured yet — the pass logs it and carries on with the Marketplace's
// own entries; it never stops.
type ApprovedAddonsFunc func(ctx context.Context) ([]ApprovedAddon, error)

// EnginePinStatus mirrors orchestrator.EnginePinCheckResult's fields
// without importing internal/orchestrator — this package has no business
// knowing about Git providers or PR flows, and orchestrator already has a
// wide dependency footprint. cmd/sharko/serve.go, which imports both
// packages, adapts orchestrator.EnginePinCheckResult into this shape when
// it wires EnginePinCheckFunc.
type EnginePinStatus struct {
	V4Repo           bool
	BundledVersion   string
	PinnedVersion    string
	UpgradeAvailable bool
	Message          string
}

// EnginePinSnapshot is the last-known engine pin check outcome, as of
// CheckedAt. Err is set (Status left zero) when the check itself failed —
// most commonly "no active Git connection", which is not a fresh-vs-stale
// problem so much as "there was nothing to check"; callers should treat a
// non-empty Err as "no usable cached result" rather than displaying it.
type EnginePinSnapshot struct {
	Status    EnginePinStatus
	CheckedAt time.Time
	Err       string
}

// EnginePinCheckFunc is a closure over the live orchestrator + active Git
// connection, invoked once per scheduler cycle. Returning (nil, err) is the
// ordinary case when no Git connection is configured — the scheduler stores
// the error and moves on; it does not stop the version-freshness pass.
type EnginePinCheckFunc func(ctx context.Context) (*EnginePinStatus, error)

// FreshnessScheduler runs the daily (default) catalog version-freshness
// pass. It supports three triggers, exactly mirroring internal/secrets.
// Reconciler:
//  1. Periodic timer (default DefaultFreshnessInterval)
//  2. Explicit Trigger() call (e.g. the UI's refresh button)
//  3. Initial run on Start()
type FreshnessScheduler struct {
	cat              *Catalog
	versions         VersionsLister
	enginePinCheckFn EnginePinCheckFunc // nil = engine pin check disabled this cycle
	approvedFn       ApprovedAddonsFunc // nil = the org's own catalog is not scanned
	interval         time.Duration
	now              func() time.Time // injected for tests

	triggerCh chan struct{}
	stopCh    chan struct{}
	stopOnce  sync.Once

	mu           sync.RWMutex
	snapshots    map[string]VersionSnapshot
	enginePin    *EnginePinSnapshot // nil until the first engine-pin check runs
	lastRun      time.Time
	catalogCount int // approved addons walked on the last pass
}

// NewFreshnessScheduler builds a FreshnessScheduler. enginePinCheckFn may be
// nil — the engine pin check is then simply skipped every cycle (e.g. in
// demo mode, or any build that never wires a Git connection). interval <= 0
// defaults to DefaultFreshnessInterval (24h).
func NewFreshnessScheduler(cat *Catalog, versions VersionsLister, enginePinCheckFn EnginePinCheckFunc, interval time.Duration) *FreshnessScheduler {
	if interval <= 0 {
		interval = DefaultFreshnessInterval
	}
	return &FreshnessScheduler{
		cat:              cat,
		versions:         versions,
		enginePinCheckFn: enginePinCheckFn,
		interval:         interval,
		now:              time.Now,
		triggerCh:        make(chan struct{}, 1),
		stopCh:           make(chan struct{}),
		snapshots:        make(map[string]VersionSnapshot),
	}
}

// WithNowFunc overrides the clock. Test-only seam (V125-1-8-style per-
// instance seam, not a package-level var — go-expert.md convention).
func (s *FreshnessScheduler) WithNowFunc(now func() time.Time) *FreshnessScheduler {
	if now != nil {
		s.now = now
	}
	return s
}

// WithApprovedAddons wires in the org's own catalog.yaml so the scheduler
// watches the charts the fleet actually runs, not only the ones Sharko
// happens to ship.
//
// Without this, an org that added its own chart got no "a newer version is
// out" signal at all — the scheduler only ever walked the shipped list.
// Nothing here can invent an answer: a repo whose index cannot be read gets
// a snapshot that says so in plain words.
func (s *FreshnessScheduler) WithApprovedAddons(fn ApprovedAddonsFunc) *FreshnessScheduler {
	s.approvedFn = fn
	return s
}

// CatalogAddonsChecked returns how many approved addons the last pass
// walked. Zero when the org's catalog is not wired, is empty, or could not
// be read.
func (s *FreshnessScheduler) CatalogAddonsChecked() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.catalogCount
}

// Start launches the background refresh loop. Runs one pass immediately,
// then repeats on every tick or Trigger() call. Safe to call once; call it
// again only after Stop().
func (s *FreshnessScheduler) Start() {
	go func() {
		s.refresh()
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.refresh()
			case <-s.triggerCh:
				s.refresh()
			case <-s.stopCh:
				return
			}
		}
	}()
}

// Stop shuts down the refresh loop. Safe to call multiple times.
func (s *FreshnessScheduler) Stop() {
	s.stopOnce.Do(func() {
		close(s.stopCh)
	})
}

// Trigger requests an immediate refresh (the UI's "refresh" action, and any
// other on-demand caller). Non-blocking: if a trigger is already queued the
// request is dropped — the pending run covers it.
func (s *FreshnessScheduler) Trigger() {
	select {
	case s.triggerCh <- struct{}{}:
	default: // already triggered
	}
}

// VersionSnapshot returns the last-known version snapshot for the named
// catalog entry, and whether one exists yet (false before the first pass
// completes for that entry, or for a name outside the curated catalog).
func (s *FreshnessScheduler) VersionSnapshot(name string) (VersionSnapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap, ok := s.snapshots[name]
	return snap, ok
}

// EnginePinSnapshot returns the last-known engine pin check result, and
// whether the scheduler has ever run one (false when enginePinCheckFn is
// nil, or before the first cycle completes).
func (s *FreshnessScheduler) EnginePinSnapshot() (EnginePinSnapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.enginePin == nil {
		return EnginePinSnapshot{}, false
	}
	return *s.enginePin, true
}

// LastRun returns the timestamp of the most recently completed full pass —
// the honest "catalog last checked" answer for a view that has not opened
// any single addon's detail (e.g. a Marketplace list header). Zero value
// means no pass has completed yet.
func (s *FreshnessScheduler) LastRun() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastRun
}

// NextRun returns the estimated time of the next scheduled pass — LastRun
// plus the configured interval. Zero value (not "now") when no pass has run
// yet, so callers can distinguish "never checked" from "due now".
func (s *FreshnessScheduler) NextRun() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.lastRun.IsZero() {
		return time.Time{}
	}
	return s.lastRun.Add(s.interval)
}

// Interval returns the configured refresh cadence.
func (s *FreshnessScheduler) Interval() time.Duration {
	return s.interval
}

// RefreshForTest runs one synchronous refresh pass without going through
// Start()'s background goroutine or a Trigger() round-trip. Exported so
// tests in OTHER packages (e.g. internal/api, which cannot call the
// unexported refresh() method directly) can populate a scheduler
// deterministically. Mirrors TTLCache.ExpireAllForTest's "test-only escape
// hatch" naming convention in cache.go, in this same package.
func (s *FreshnessScheduler) RefreshForTest() {
	s.refresh()
}

// refresh runs one full pass: every catalog entry's version list, then (if
// configured) one engine pin check. Safe to call concurrently but only ever
// called from the single loop goroutine in Start() plus test code calling
// it directly — mirrors internal/secrets.Reconciler.reconcile().
func (s *FreshnessScheduler) refresh() {
	log := slog.Default().With("component", "catalog-freshness")
	log.Info("[freshness] refresh started")

	now := s.now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// The Marketplace's own list first, then the org's approved addons on
	// top: when a name is in both, what the org actually deploys is the
	// repoURL in its own file, so that one wins.
	checked := 0
	if s.cat != nil && s.versions != nil {
		for _, e := range s.cat.Entries() {
			snap := s.fetchOne(ctx, e.Name, e.Repo, e.Chart, ScopeMarketplace)
			s.mu.Lock()
			s.snapshots[e.Name] = snap
			s.mu.Unlock()
			checked++
		}
	}

	approvedChecked := 0
	if s.approvedFn != nil && s.versions != nil {
		approved, err := s.approvedFn(ctx)
		if err != nil {
			log.Warn("[freshness] could not read the org's catalog", "err", err)
		}
		for _, a := range approved {
			snap := s.fetchOne(ctx, a.Name, a.RepoURL, a.Chart, ScopeCatalog)
			s.mu.Lock()
			s.snapshots[a.Name] = snap
			s.mu.Unlock()
			approvedChecked++
		}
	}

	if s.enginePinCheckFn != nil {
		status, err := s.enginePinCheckFn(ctx)
		snap := EnginePinSnapshot{CheckedAt: now}
		switch {
		case err != nil:
			// BF1: this field is returned in the body of GET
			// /api/v1/catalog/freshness, so err.Error() here was a
			// provider's own words on a user-facing surface. Today's
			// feeder happens to be safe; the feeder is a closure wired
			// in cmd/sharko/serve.go and it changes, and nobody rechecks
			// this line when it does. credsafe.LogClass is built from
			// sentinels and Go type names only — never from the error's
			// words — so the surface is closed whatever feeds it.
			snap.Err = credsafe.LogClass(err)
			log.Warn("[freshness] engine pin check failed", "err", err)
		case status != nil:
			snap.Status = *status
		}
		s.mu.Lock()
		s.enginePin = &snap
		s.mu.Unlock()
	}

	s.mu.Lock()
	s.lastRun = now
	s.catalogCount = approvedChecked
	s.mu.Unlock()

	log.Info("[freshness] refresh complete",
		"marketplace_addons_checked", checked,
		"catalog_addons_checked", approvedChecked)
}

// fetchOne resolves one addon's version list from its chart repo. Every
// failure degrades to Unknown with a plain-English NoDataReason rather than
// propagating an error: one broken repo must not stall the pass or wipe
// every other addon's snapshot, and a person looking at the screen should
// read why there is nothing rather than see a blank that looks like "fine".
func (s *FreshnessScheduler) fetchOne(ctx context.Context, name, repoURL, chart, scope string) VersionSnapshot {
	snap := VersionSnapshot{CheckedAt: s.now(), Scope: scope}

	// B14: every sentence below names the repository, NoDataReason is
	// rendered verbatim in the browser, and repoURL comes straight out of
	// the org's catalog.yaml — where the access token is routinely written
	// inside the address. safeRepo is the only spelling of it allowed past
	// this line: host and path, so a person still knows which repository is
	// meant, and never the userinfo section. The raw repoURL is used only to
	// dial, never to describe.
	safeRepo := credsafe.SafeRepoURLPhrase(repoURL)

	if repoURL == "" || chart == "" {
		snap.Unknown = true
		snap.NoDataReason = fmt.Sprintf(
			"no freshness data for %s — its entry has no chart repository to check", name)
		return snap
	}

	versions, err := s.versions.ListVersions(ctx, repoURL, chart)
	if err != nil {
		snap.Unknown = true
		if errors.Is(err, helm.ErrOCIVersionCheckUnsupported) {
			snap.NoDataReason = fmt.Sprintf(
				"no freshness data for this source — %s has no version index Sharko can read", safeRepo)
			return snap
		}
		// BF1: same rule as the engine-pin snapshot above. This one comes
		// straight out of a chart repository fetch, where the address —
		// token and all — is routinely quoted verbatim in the error text.
		snap.Err = credsafe.LogClass(err)
		snap.NoDataReason = fmt.Sprintf(
			"no freshness data for this source — Sharko could not read the version index at %s", safeRepo)
		slog.Default().With("component", "catalog-freshness").Warn(
			"[freshness] version fetch failed", "addon", name, "repo", safeRepo, "err", err)
		return snap
	}

	snap.Versions = versions
	if len(versions) == 0 {
		snap.Unknown = true
		snap.NoDataReason = fmt.Sprintf(
			"no freshness data for this source — %s lists no versions of %s", safeRepo, chart)
	}
	return snap
}
