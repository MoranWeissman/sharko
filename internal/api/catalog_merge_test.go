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

// --- mergedCatalogGet (V123-PR-B / H1) -------------------------------
//
// The handlers GET /catalog/addons/{name}/readme, /versions, and
// /project-readme used s.catalog.Get(name) prior to PR-B — which only sees
// embedded entries and 404s on every third-party-only entry. The fix routes
// them through s.mergedCatalogGet(name); these cases lock in the contract.

// TestMergedCatalogGet_NilCatalog: s.catalog == nil → (zero, false). Same
// short-circuit semantics as mergedCatalogEntries — callers should already
// be 503'ing with a "catalog not loaded" guard before they reach the lookup.
func TestMergedCatalogGet_NilCatalog(t *testing.T) {
	s := &Server{}
	got, ok := s.mergedCatalogGet("anything")
	if ok {
		t.Errorf("expected ok=false for nil catalog, got entry=%+v", got)
	}
	if got.Name != "" {
		t.Errorf("expected zero CatalogEntry, got Name=%q", got.Name)
	}
}

// TestMergedCatalogGet_EmbeddedHit_NoFetcher: with no fetcher wired, the
// helper falls back to the embedded-only fast path. Embedded names hit;
// names that don't exist anywhere miss.
func TestMergedCatalogGet_EmbeddedHit_NoFetcher(t *testing.T) {
	s := serverWithCatalog(t, testCatalog(t))

	entry, ok := s.mergedCatalogGet("cert-manager")
	if !ok {
		t.Fatalf("expected cert-manager to resolve via embedded fast path")
	}
	if entry.Name != "cert-manager" {
		t.Errorf("got Name=%q, want cert-manager", entry.Name)
	}
	if entry.Source != catalog.SourceEmbedded {
		t.Errorf("expected Source=%q, got %q", catalog.SourceEmbedded, entry.Source)
	}

	if _, ok := s.mergedCatalogGet("does-not-exist"); ok {
		t.Errorf("expected ok=false for nonexistent name")
	}
}

// TestMergedCatalogGet_ThirdPartyOnly: the load-bearing case for PR-B/H1.
// A name that exists only in a third-party snapshot must resolve through
// mergedCatalogGet — pre-fix this returned (zero, false) and the readme/
// versions/project-readme handlers 404'd.
func TestMergedCatalogGet_ThirdPartyOnly(t *testing.T) {
	s := serverWithCatalog(t, testCatalog(t))

	tpURL := "https://internal.example.com/catalog.yaml"
	tpEntry := catalog.CatalogEntry{
		Name:             "third-party-only",
		Description:      "Third-party-only addon for the H1 lookup test.",
		Chart:            "tpo",
		Repo:             "https://internal.example.com/charts",
		DefaultNamespace: "tpo",
		Maintainers:      []string{"platform"},
		License:          "Apache-2.0",
		Category:         "networking",
		CuratedBy:        []string{"artifacthub-verified"},
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

	got, ok := s.mergedCatalogGet("third-party-only")
	if !ok {
		t.Fatalf("expected third-party-only to resolve through mergedCatalogGet (H1 regression: pre-fix readme/versions/project-readme 404'd)")
	}
	if got.Name != "third-party-only" {
		t.Errorf("got Name=%q, want third-party-only", got.Name)
	}
	if got.Source != tpURL {
		t.Errorf("expected Source=%q (third-party URL), got %q", tpURL, got.Source)
	}
	if got.Chart != "tpo" {
		t.Errorf("expected the merger to surface the snapshot's Chart=%q, got %q", "tpo", got.Chart)
	}

	// Embedded names continue to resolve (and embedded wins source).
	em, ok := s.mergedCatalogGet("cert-manager")
	if !ok {
		t.Fatalf("expected embedded cert-manager to still resolve through merged lookup")
	}
	if em.Source != catalog.SourceEmbedded {
		t.Errorf("expected embedded Source for cert-manager, got %q", em.Source)
	}

	// Unknown names still miss.
	if _, ok := s.mergedCatalogGet("nope"); ok {
		t.Errorf("expected ok=false for unknown name")
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
