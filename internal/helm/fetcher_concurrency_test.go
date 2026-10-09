// fetcher_concurrency_test.go — one Fetcher, many goroutines.
//
// A Fetcher is shared and long-lived on three live paths:
// internal/api/catalog_versions.go keeps a package-level one behind the
// chart-versions endpoint, internal/service/upgrade.go holds one for the life
// of the service, and cmd/sharko/serve.go hands one to the catalog freshness
// scheduler. Two HTTP requests therefore land in the same Fetcher routinely.
//
// Its three caches were plain maps with no lock, which is not merely a torn
// read: concurrent writes to a Go map are "fatal error: concurrent map writes",
// an unrecoverable runtime abort that takes the whole process down. Sharko runs
// as a single instance holding sessions in an in-memory map, so one of those
// signs every user out.
//
// CI has run `go test -race ./...` since long before this file existed
// (.github/workflows/ci.yml) and it stayed green the whole time — not because
// the code was safe, but because nothing in this package ever put two
// goroutines into one Fetcher. This test is what gives that gate something to
// catch. It fails under -race without the mutex in fetcher.go and passes with
// it.
package helm

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// TestFetcher_SharedBetweenGoroutines drives ONE Fetcher through overlapping
// index, values and chart-metadata work from many goroutines at once.
//
// The shape is deliberate, because a race test that only ever warms a cache
// once proves very little:
//
//   - A start barrier releases every goroutine together, so they all arrive at
//     a COLD index cache in the same instant. That is the read-miss-then-write
//     pile-up that produced the crash. It is a barrier, not a sleep — there is
//     no timing assumption anywhere in this file.
//   - Two repositories, so the index map takes more than one write.
//   - Each goroutine also fetches a version nobody else asks for, so fresh
//     writes to the values and chart-metadata maps keep arriving while other
//     goroutines are reading those same maps. Without this the maps would be
//     warm after the first instant and the writer side would go quiet.
//   - Shared versions on top of that, in both spellings, so the read side is
//     busy too and the resolver's two-spelling path is exercised concurrently.
func TestFetcher_SharedBetweenGoroutines(t *testing.T) {
	const (
		goroutines = 16
		iterations = 8
	)

	// Shared versions every goroutine asks for, in both spellings, plus one
	// private version per goroutine to keep the write side busy throughout.
	entries := []indexEntry{
		{"v1.2.3", "shared"},
		{"1.2.4", "shared"},
	}
	for g := 0; g < goroutines; g++ {
		entries = append(entries, indexEntry{fmt.Sprintf("9.0.%d", g), "shared"})
	}

	repoA := startFakeRepo(t, entries)
	repoB := startFakeRepo(t, entries)
	repos := []string{repoA.url, repoB.url}

	// ONE Fetcher. This is the whole point of the test.
	f := NewFetcher()
	ctx := context.Background()

	start := make(chan struct{})
	errs := make(chan error, goroutines*iterations*8)
	var wg sync.WaitGroup

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			repo := repos[g%len(repos)]
			private := fmt.Sprintf("9.0.%d", g)

			<-start // released all together

			for i := 0; i < iterations; i++ {
				// indexCache: read on every call, written on the first
				// miss per repository.
				if _, err := f.ListVersions(ctx, repo, spellingChartName); err != nil {
					errs <- fmt.Errorf("ListVersions: %w", err)
				}
				if _, err := f.ListCharts(ctx, repo); err != nil {
					errs <- fmt.Errorf("ListCharts: %w", err)
				}
				if _, err := f.FindNearestVersion(ctx, repo, spellingChartName, "1.2.5"); err != nil {
					errs <- fmt.Errorf("FindNearestVersion: %w", err)
				}

				// valuesCache: the private version is a fresh write from
				// this goroutine while others read the same map.
				if _, err := f.FetchValues(ctx, repo, spellingChartName, private); err != nil {
					errs <- fmt.Errorf("FetchValues(private %s): %w", private, err)
				}
				// Shared keys, both spellings — heavy concurrent reads,
				// and both resolve onto the same cache entry.
				if _, err := f.FetchValues(ctx, repo, spellingChartName, "1.2.3"); err != nil {
					errs <- fmt.Errorf("FetchValues(1.2.3): %w", err)
				}
				if _, err := f.FetchValues(ctx, repo, spellingChartName, "v1.2.3"); err != nil {
					errs <- fmt.Errorf("FetchValues(v1.2.3): %w", err)
				}
				if _, err := f.FetchValues(ctx, repo, spellingChartName, "v1.2.4"); err != nil {
					errs <- fmt.Errorf("FetchValues(v1.2.4): %w", err)
				}

				// chartCache, through fetchChartYAML's only caller. The
				// fake Chart.yaml points sources: and home: at .invalid
				// hosts, so this reaches no real network.
				if _, err := f.FetchReleaseNotes(ctx, repo, spellingChartName, private); err != nil {
					errs <- fmt.Errorf("FetchReleaseNotes(private %s): %w", private, err)
				}
				if _, err := f.FetchReleaseNotes(ctx, repo, spellingChartName, "1.2.3"); err != nil {
					errs <- fmt.Errorf("FetchReleaseNotes(1.2.3): %w", err)
				}
			}
		}(g)
	}

	close(start)
	wg.Wait()
	close(errs)

	var failures []string
	for err := range errs {
		failures = append(failures, err.Error())
		if len(failures) == 10 {
			failures = append(failures, "... (more suppressed)")
			break
		}
	}
	if len(failures) > 0 {
		t.Fatalf("%d concurrent call(s) failed:\n  %s", len(failures), strings.Join(failures, "\n  "))
	}

	// Sanity: the run really did populate the caches, so a green result
	// cannot come from every call having bailed out early.
	if got := len(f.valuesCache); got == 0 {
		t.Error("the values cache is empty, so this test fetched nothing and proves nothing")
	}
	if got := len(f.chartCache); got == 0 {
		t.Error("the chart-metadata cache is empty, so the fetchChartYAML path never ran")
	}
	if got := len(f.indexCache); got != len(repos) {
		t.Errorf("index cache holds %d entries, want %d (one per repository)", got, len(repos))
	}
}

// TestFetcher_ConcurrentColdIndexReadsAndWrites is the narrowest version of the
// crash: every goroutine hits the same cold index at once, so the read-miss and
// the store overlap on one map. It is separate from the broader test above so a
// failure says which pairing broke.
func TestFetcher_ConcurrentColdIndexReadsAndWrites(t *testing.T) {
	const goroutines = 24

	repo := startFakeRepo(t, []indexEntry{{"1.2.3", "shared"}})
	f := NewFetcher()
	ctx := context.Background()

	start := make(chan struct{})
	errs := make(chan error, goroutines)
	var wg sync.WaitGroup

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := f.ListVersions(ctx, repo.url, spellingChartName); err != nil {
				errs <- err
			}
		}()
	}

	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("cold-index ListVersions failed: %v", err)
	}
	if len(f.indexCache) != 1 {
		t.Errorf("index cache holds %d entries, want exactly 1", len(f.indexCache))
	}
}
