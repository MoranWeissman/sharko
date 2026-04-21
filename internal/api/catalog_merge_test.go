package api

import (
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/catalog/sources"
	"github.com/MoranWeissman/sharko/internal/config"
)

// makeFetcherWithSnapshots builds a sources.Fetcher with the given pre-populated
// snapshots and no configured URLs. No goroutines are started — Start is never
// called — so there are no HTTP calls or tickers to clean up.
func makeFetcherWithSnapshots(t *testing.T, snaps map[string]*sources.SourceSnapshot) *sources.Fetcher {
	t.Helper()
	f := sources.NewFetcher(&config.CatalogSourcesConfig{}, nil, nil)
	f.SetSnapshotsForTest(snaps)
	return f
}

func TestMergedCatalogEntries_NilCatalog(t *testing.T) {
	// No s.catalog wired → helper returns nil so the caller 503s. A nil
	// fetcher is fine (the embedded-only default).
	s := &Server{}
	if got := s.mergedCatalogEntries(); got != nil {
		t.Errorf("expected nil for missing catalog, got %+v", got)
	}
}

func TestMergedCatalogEntries_EmbeddedOnly(t *testing.T) {
	// s.catalog set, s.sourcesFetcher nil → return embedded entries as-is.
	// Every entry must carry Source="embedded" (set at Load time by the loader).
	s := serverWithCatalog(t, testCatalog(t))

	got := s.mergedCatalogEntries()
	if len(got) != 2 {
		t.Fatalf("expected 2 embedded entries, got %d", len(got))
	}
	for _, e := range got {
		if e.Source != catalog.SourceEmbedded {
			t.Errorf("entry %q: Source = %q, want %q", e.Name, e.Source, catalog.SourceEmbedded)
		}
	}
}

func TestMergedCatalogEntries_DisjointThirdParty(t *testing.T) {
	// embedded = {cert-manager, grafana}; third-party = {internal-foo}
	// Expect 3 entries: embedded two stay "embedded", internal-foo carries
	// the third-party URL as Source.
	s := serverWithCatalog(t, testCatalog(t))

	tpURL := "https://internal.example.com/catalog.yaml"
	tpEntry := catalog.CatalogEntry{
		Name:             "internal-foo",
		Description:      "Proprietary internal addon.",
		Chart:            "foo",
		Repo:             "https://internal.example.com/charts",
		DefaultNamespace: "foo",
		Maintainers:      []string{"platform"},
		License:          "Apache-2.0",
		Category:         "networking",
		CuratedBy:        []string{"artifacthub-verified"},
		// Source is intentionally set to something plausible-but-wrong here.
		// sources.Merge overwrites Origin to the snapshot URL, and the helper
		// then copies Origin back onto CatalogEntry.Source — so whatever
		// value the snapshot entry carried gets replaced. This proves the
		// helper doesn't trust upstream's self-declared Source.
		Source: "malicious-upstream-declaration",
	}
	snaps := map[string]*sources.SourceSnapshot{
		tpURL: {
			URL:           tpURL,
			Status:        sources.StatusOK,
			LastSuccessAt: time.Now(),
			LastAttemptAt: time.Now(),
			Entries:       []catalog.CatalogEntry{tpEntry},
		},
	}
	s.SetSourcesFetcher(makeFetcherWithSnapshots(t, snaps))

	got := s.mergedCatalogEntries()
	if len(got) != 3 {
		t.Fatalf("expected 3 merged entries, got %d: %+v", len(got), got)
	}
	byName := make(map[string]catalog.CatalogEntry, len(got))
	for _, e := range got {
		byName[e.Name] = e
	}
	for _, embeddedName := range []string{"cert-manager", "grafana"} {
		e, ok := byName[embeddedName]
		if !ok {
			t.Fatalf("missing embedded entry %q", embeddedName)
		}
		if e.Source != catalog.SourceEmbedded {
			t.Errorf("embedded entry %q: Source = %q, want %q", embeddedName, e.Source, catalog.SourceEmbedded)
		}
	}
	tp, ok := byName["internal-foo"]
	if !ok {
		t.Fatalf("missing third-party entry internal-foo")
	}
	if tp.Source != tpURL {
		t.Errorf("third-party entry: Source = %q, want %q (helper must not trust upstream's Source)", tp.Source, tpURL)
	}
}

func TestMergedCatalogEntries_OverlappingNameEmbeddedWins(t *testing.T) {
	// embedded + third-party both define cert-manager. Embedded wins under
	// the merger contract (V123-1.3 §2.7 + §3.2) — the returned entry has
	// Source="embedded", never the third-party URL.
	s := serverWithCatalog(t, testCatalog(t))

	tpURL := "https://evil.example.com/shadow.yaml"
	hostileEntry := catalog.CatalogEntry{
		Name:             "cert-manager", // same name as the embedded entry
		Description:      "Hostile shadowed entry — should never win.",
		Chart:            "evil-cert-manager",
		Repo:             "https://evil.example.com/charts",
		DefaultNamespace: "evil",
		Maintainers:      []string{"attacker"},
		License:          "Proprietary",
		Category:         "security",
		CuratedBy:        []string{"artifacthub-verified"},
	}
	snaps := map[string]*sources.SourceSnapshot{
		tpURL: {
			URL:           tpURL,
			Status:        sources.StatusOK,
			LastSuccessAt: time.Now(),
			LastAttemptAt: time.Now(),
			Entries:       []catalog.CatalogEntry{hostileEntry},
		},
	}
	s.SetSourcesFetcher(makeFetcherWithSnapshots(t, snaps))

	got := s.mergedCatalogEntries()
	// Expect 2 entries — embedded {cert-manager, grafana}; the third-party
	// cert-manager is shadowed and dropped from the merged view entirely.
	if len(got) != 2 {
		t.Fatalf("expected 2 entries after embedded-wins collision, got %d: %+v", len(got), got)
	}
	var cm catalog.CatalogEntry
	var found bool
	for _, e := range got {
		if e.Name == "cert-manager" {
			cm = e
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("cert-manager missing from merged view")
	}
	if cm.Source != catalog.SourceEmbedded {
		t.Errorf("cert-manager: Source = %q, want %q — embedded must win", cm.Source, catalog.SourceEmbedded)
	}
	// Sanity: the fields that come through are the embedded ones (repo is
	// the Jetstack repo, not the evil one).
	if cm.Repo != "https://charts.jetstack.io" {
		t.Errorf("cert-manager: Repo = %q; embedded fields should survive the merge, not the attacker's", cm.Repo)
	}
}

// TestMergedCatalogEntries_StaleSnapshotIgnored covers the defensive skip:
// a snapshot whose Status is not StatusOK must not contribute entries to
// the merged view even if Entries happens to carry last-good data. The
// merger's rule (merger.go step 2) drops them; the helper just plumbs it
// through.
func TestMergedCatalogEntries_StaleSnapshotIgnored(t *testing.T) {
	s := serverWithCatalog(t, testCatalog(t))

	tpURL := "https://stale.example.com/catalog.yaml"
	snaps := map[string]*sources.SourceSnapshot{
		tpURL: {
			URL:           tpURL,
			Status:        sources.StatusStale, // not StatusOK → skipped
			LastSuccessAt: time.Now().Add(-time.Hour),
			LastAttemptAt: time.Now(),
			Entries: []catalog.CatalogEntry{
				{Name: "stale-ghost", Description: "d", Chart: "c", Repo: "https://x",
					DefaultNamespace: "n", Maintainers: []string{"m"}, License: "Apache-2.0",
					Category: "security", CuratedBy: []string{"artifacthub-verified"}},
			},
		},
	}
	s.SetSourcesFetcher(makeFetcherWithSnapshots(t, snaps))

	got := s.mergedCatalogEntries()
	for _, e := range got {
		if e.Name == "stale-ghost" {
			t.Fatalf("stale-snapshot entry leaked into merged view: %+v", e)
		}
	}
	// Embedded entries still present.
	if len(got) != 2 {
		t.Errorf("expected 2 embedded entries with stale snapshot ignored, got %d", len(got))
	}
}
