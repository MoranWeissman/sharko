package catalog

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/helm"
)

// testCatalogYAML is a minimal two-entry catalog — enough to exercise the
// scheduler without pulling in the full embedded catalog/addons.yaml.
const testCatalogYAML = `
addons:
  - name: alpha
    description: test addon alpha
    chart: alpha
    repo: https://charts.example.com/alpha
    default_namespace: alpha
    maintainers: [example]
    license: Apache-2.0
    category: security
    curated_by: [cncf-graduated]
  - name: beta
    description: test addon beta
    chart: beta
    repo: oci://registry.example.com/beta
    default_namespace: beta
    maintainers: [example]
    license: Apache-2.0
    category: security
    curated_by: [cncf-graduated]
`

// fakeVersionsLister is a hand-rolled test double for VersionsLister —
// keyed by "repo|chart" so different entries can be scripted independently.
// Safe for concurrent use since FreshnessScheduler.refresh iterates
// sequentially, but a mutex costs nothing and keeps `go test -race` happy
// if a future test calls it from goroutines.
type fakeVersionsLister struct {
	mu      sync.Mutex
	results map[string]fakeResult
	calls   int
}

type fakeResult struct {
	versions []helm.ChartVersion
	err      error
}

func newFakeVersionsLister() *fakeVersionsLister {
	return &fakeVersionsLister{results: make(map[string]fakeResult)}
}

func (f *fakeVersionsLister) set(repo, chart string, versions []helm.ChartVersion, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.results[repo+"|"+chart] = fakeResult{versions: versions, err: err}
}

func (f *fakeVersionsLister) ListVersions(_ context.Context, repo, chart string) ([]helm.ChartVersion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	r, ok := f.results[repo+"|"+chart]
	if !ok {
		return nil, errors.New("no fake result configured")
	}
	return r.versions, r.err
}

func loadTestCatalog(t *testing.T) *Catalog {
	t.Helper()
	cat, err := LoadBytes([]byte(testCatalogYAML))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	return cat
}

func TestFreshnessScheduler_RefreshPopulatesSnapshots(t *testing.T) {
	cat := loadTestCatalog(t)
	lister := newFakeVersionsLister()
	lister.set("https://charts.example.com/alpha", "alpha",
		[]helm.ChartVersion{{Version: "1.2.3"}, {Version: "1.2.2"}}, nil)
	lister.set("oci://registry.example.com/beta", "beta",
		nil, helm.ErrOCIVersionCheckUnsupported)

	sched := NewFreshnessScheduler(cat, lister, nil, time.Hour)
	sched.refresh()

	alpha, ok := sched.VersionSnapshot("alpha")
	if !ok {
		t.Fatal("expected a snapshot for alpha")
	}
	if alpha.Unknown {
		t.Error("alpha: expected Unknown=false (fetch succeeded)")
	}
	if len(alpha.Versions) != 2 {
		t.Errorf("alpha: expected 2 versions, got %d", len(alpha.Versions))
	}
	if alpha.CheckedAt.IsZero() {
		t.Error("alpha: expected a non-zero CheckedAt")
	}

	beta, ok := sched.VersionSnapshot("beta")
	if !ok {
		t.Fatal("expected a snapshot for beta")
	}
	if !beta.Unknown {
		t.Error("beta: expected Unknown=true for an oci:// repo (graceful degrade, v4 wave 1 Story 3.3 contract)")
	}
	if beta.Err != "" {
		t.Errorf("beta: expected no Err for the documented oci:// sentinel (not a real failure), got %q", beta.Err)
	}

	if _, ok := sched.VersionSnapshot("does-not-exist"); ok {
		t.Error("expected no snapshot for an unknown addon name")
	}

	if sched.LastRun().IsZero() {
		t.Error("expected LastRun to be set after refresh()")
	}
}

func TestFreshnessScheduler_FetchFailureIsGracefulNotFatal(t *testing.T) {
	cat := loadTestCatalog(t)
	lister := newFakeVersionsLister()
	lister.set("https://charts.example.com/alpha", "alpha", nil, errors.New("connection refused"))
	lister.set("oci://registry.example.com/beta", "beta",
		[]helm.ChartVersion{{Version: "9.9.9"}}, nil)

	sched := NewFreshnessScheduler(cat, lister, nil, time.Hour)
	sched.refresh() // must not panic or abort the rest of the pass

	alpha, ok := sched.VersionSnapshot("alpha")
	if !ok {
		t.Fatal("expected a snapshot for alpha even though its fetch failed")
	}
	if !alpha.Unknown || alpha.Err == "" {
		t.Errorf("alpha: expected Unknown=true and a non-empty Err, got Unknown=%v Err=%q", alpha.Unknown, alpha.Err)
	}

	// beta must still have been processed — one bad entry does not stall
	// the pass.
	beta, ok := sched.VersionSnapshot("beta")
	if !ok || beta.Unknown {
		t.Errorf("beta: expected a successful snapshot despite alpha's failure, got ok=%v Unknown=%v", ok, beta.Unknown)
	}
}

func TestFreshnessScheduler_EnginePinCheck(t *testing.T) {
	cat := loadTestCatalog(t)
	lister := newFakeVersionsLister()
	lister.set("https://charts.example.com/alpha", "alpha", []helm.ChartVersion{{Version: "1.0.0"}}, nil)
	lister.set("oci://registry.example.com/beta", "beta", []helm.ChartVersion{{Version: "1.0.0"}}, nil)

	calls := 0
	checkFn := func(ctx context.Context) (*EnginePinStatus, error) {
		calls++
		return &EnginePinStatus{
			V4Repo:           true,
			BundledVersion:   "4.2.0",
			PinnedVersion:    "4.1.0",
			UpgradeAvailable: true,
			Message:          "engine upgrade available: 4.1.0 -> 4.2.0",
		}, nil
	}

	sched := NewFreshnessScheduler(cat, lister, checkFn, time.Hour)
	sched.refresh()

	if calls != 1 {
		t.Fatalf("expected the engine pin check function to be called once per refresh, got %d calls", calls)
	}
	snap, ok := sched.EnginePinSnapshot()
	if !ok {
		t.Fatal("expected an engine pin snapshot after refresh")
	}
	if snap.Err != "" {
		t.Errorf("expected no Err, got %q", snap.Err)
	}
	if !snap.Status.UpgradeAvailable || snap.Status.BundledVersion != "4.2.0" {
		t.Errorf("unexpected engine pin status: %+v", snap.Status)
	}
}

func TestFreshnessScheduler_EnginePinCheckErrorIsGraceful(t *testing.T) {
	cat := loadTestCatalog(t)
	lister := newFakeVersionsLister()
	lister.set("https://charts.example.com/alpha", "alpha", []helm.ChartVersion{{Version: "1.0.0"}}, nil)
	lister.set("oci://registry.example.com/beta", "beta", []helm.ChartVersion{{Version: "1.0.0"}}, nil)

	checkFn := func(ctx context.Context) (*EnginePinStatus, error) {
		return nil, errors.New("no active Git connection")
	}

	sched := NewFreshnessScheduler(cat, lister, checkFn, time.Hour)
	sched.refresh() // must not panic despite the engine pin check failing

	snap, ok := sched.EnginePinSnapshot()
	if !ok {
		t.Fatal("expected a snapshot to be recorded even when the check function errors")
	}
	if snap.Err == "" {
		t.Error("expected a non-empty Err")
	}

	// Version snapshots must still have been populated — engine pin
	// failure does not stall the version-freshness half of the pass.
	if _, ok := sched.VersionSnapshot("alpha"); !ok {
		t.Error("expected alpha's version snapshot despite the engine pin check failing")
	}
}

func TestFreshnessScheduler_NoEnginePinCheckFn(t *testing.T) {
	cat := loadTestCatalog(t)
	lister := newFakeVersionsLister()
	lister.set("https://charts.example.com/alpha", "alpha", []helm.ChartVersion{{Version: "1.0.0"}}, nil)
	lister.set("oci://registry.example.com/beta", "beta", []helm.ChartVersion{{Version: "1.0.0"}}, nil)

	sched := NewFreshnessScheduler(cat, lister, nil, time.Hour)
	sched.refresh()

	if _, ok := sched.EnginePinSnapshot(); ok {
		t.Error("expected no engine pin snapshot when enginePinCheckFn is nil")
	}
}

func TestFreshnessScheduler_TriggerCausesRefresh(t *testing.T) {
	cat := loadTestCatalog(t)
	lister := newFakeVersionsLister()
	lister.set("https://charts.example.com/alpha", "alpha", []helm.ChartVersion{{Version: "1.0.0"}}, nil)
	lister.set("oci://registry.example.com/beta", "beta", []helm.ChartVersion{{Version: "1.0.0"}}, nil)

	// Long interval so only Start()'s immediate first pass and the
	// Trigger()-driven pass happen within the test's lifetime.
	sched := NewFreshnessScheduler(cat, lister, nil, time.Hour)
	sched.Start()
	defer sched.Stop()

	waitFor(t, func() bool { return !sched.LastRun().IsZero() })
	firstRun := sched.LastRun()

	sched.Trigger()
	waitFor(t, func() bool { return sched.LastRun().After(firstRun) })
}

func TestFreshnessScheduler_StopIsIdempotent(t *testing.T) {
	cat := loadTestCatalog(t)
	lister := newFakeVersionsLister()
	lister.set("https://charts.example.com/alpha", "alpha", []helm.ChartVersion{{Version: "1.0.0"}}, nil)
	lister.set("oci://registry.example.com/beta", "beta", []helm.ChartVersion{{Version: "1.0.0"}}, nil)

	sched := NewFreshnessScheduler(cat, lister, nil, time.Hour)
	sched.Start()
	waitFor(t, func() bool { return !sched.LastRun().IsZero() })

	sched.Stop()
	sched.Stop() // must not panic (sync.Once)
}

func TestFreshnessScheduler_DefaultIntervalIsDaily(t *testing.T) {
	cat := loadTestCatalog(t)
	lister := newFakeVersionsLister()
	sched := NewFreshnessScheduler(cat, lister, nil, 0)
	if sched.Interval() != DefaultFreshnessInterval {
		t.Errorf("expected default interval %v, got %v", DefaultFreshnessInterval, sched.Interval())
	}
	if DefaultFreshnessInterval != 24*time.Hour {
		t.Errorf("expected DefaultFreshnessInterval to be 24h (story AC: daily default), got %v", DefaultFreshnessInterval)
	}
}

func TestFreshnessScheduler_NextRun(t *testing.T) {
	cat := loadTestCatalog(t)
	lister := newFakeVersionsLister()
	lister.set("https://charts.example.com/alpha", "alpha", []helm.ChartVersion{{Version: "1.0.0"}}, nil)
	lister.set("oci://registry.example.com/beta", "beta", []helm.ChartVersion{{Version: "1.0.0"}}, nil)

	fixedNow := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	sched := NewFreshnessScheduler(cat, lister, nil, 24*time.Hour).WithNowFunc(func() time.Time { return fixedNow })

	if !sched.NextRun().IsZero() {
		t.Error("expected zero NextRun before any pass has completed")
	}

	sched.refresh()

	want := fixedNow.Add(24 * time.Hour)
	if got := sched.NextRun(); !got.Equal(want) {
		t.Errorf("NextRun() = %v, want %v", got, want)
	}
}

// waitFor polls cond every millisecond for up to 2 seconds, failing the test
// if it never becomes true. Avoids a fixed sleep racing the background
// goroutine on slow CI runners.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	if !cond() {
		t.Fatal("condition not met within timeout")
	}
}
