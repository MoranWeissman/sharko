package sources

import (
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/catalog"
)

// --- Test fixtures ---------------------------------------------------

// mkEntry is a shorthand for building a CatalogEntry with enough
// required fields for the merger to work on (the merger does NOT
// re-validate — schema enforcement already happened at fetch time).
// Uniqueness is driven by Name, so the helper only varies that.
func mkEntry(name string) catalog.CatalogEntry {
	return catalog.CatalogEntry{
		Name:             name,
		Description:      name + " description",
		Chart:            name,
		Repo:             "https://charts.example.com",
		DefaultNamespace: name,
		License:          "Apache-2.0",
		Category:         "observability",
		CuratedBy:        []string{"cncf-sandbox"},
		Maintainers:      []string{"test@example.com"},
	}
}

// mkSnapshot builds a *SourceSnapshot with StatusOK so Merge will
// consume its entries. Tests that want to exercise the defensive skip
// path can pass a non-OK status explicitly.
func mkSnapshot(url string, status SourceStatus, entries ...catalog.CatalogEntry) *SourceSnapshot {
	return &SourceSnapshot{
		URL:           url,
		Status:        status,
		Entries:       entries,
		LastSuccessAt: time.Unix(1_700_000_000, 0),
		LastAttemptAt: time.Unix(1_700_000_000, 0),
	}
}

// entryNames extracts the Name slice from a MergedEntry slice so tests
// can assert ordering + membership without caring about deep equality.
func entryNames(entries []MergedEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Name
	}
	return out
}

// findEntry returns the MergedEntry whose Name equals the argument, or
// a zero value + false when no entry matches. Used heavily by the
// per-case assertions.
func findEntry(entries []MergedEntry, name string) (MergedEntry, bool) {
	for _, e := range entries {
		if e.Name == name {
			return e, true
		}
	}
	return MergedEntry{}, false
}

// findConflict returns the Conflict with the given Name, or false.
func findConflict(conflicts []Conflict, name string) (Conflict, bool) {
	for _, c := range conflicts {
		if c.Name == name {
			return c, true
		}
	}
	return Conflict{}, false
}

// --- Tests -----------------------------------------------------------

// TestMerge_EmptyInputs — Case 1: no embedded entries + no snapshots →
// empty merged output, no conflicts. Catches accidental nil-slice
// panics and asserts the zero value is usable.
func TestMerge_EmptyInputs(t *testing.T) {
	got := Merge(nil, nil)

	if len(got.Entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(got.Entries))
	}
	if len(got.Conflicts) != 0 {
		t.Fatalf("expected 0 conflicts, got %d", len(got.Conflicts))
	}
}

// TestMerge_EmbeddedOnly — Case 2: only embedded entries, no
// third-party sources. Every embedded entry must be carried through
// with Origin="embedded" and Overridden=false. No conflicts expected.
func TestMerge_EmbeddedOnly(t *testing.T) {
	embedded := []catalog.CatalogEntry{
		mkEntry("cert-manager"),
		mkEntry("argo-rollouts"),
	}

	got := Merge(embedded, nil)

	if len(got.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got.Entries))
	}
	if len(got.Conflicts) != 0 {
		t.Fatalf("expected 0 conflicts, got %d", len(got.Conflicts))
	}
	// Deterministic sort: argo-rollouts < cert-manager.
	if names := entryNames(got.Entries); !reflect.DeepEqual(names, []string{"argo-rollouts", "cert-manager"}) {
		t.Fatalf("expected sorted names [argo-rollouts cert-manager], got %v", names)
	}
	for _, e := range got.Entries {
		if e.Origin != OriginEmbedded {
			t.Errorf("entry %q: expected Origin=%q, got %q", e.Name, OriginEmbedded, e.Origin)
		}
		if e.Overridden {
			t.Errorf("entry %q: expected Overridden=false, got true", e.Name)
		}
	}
}

// TestMerge_ThirdPartyOnly — Case 3: no embedded, two third-party
// sources with disjoint names. All entries present, each Origin is the
// source URL, none overridden. No conflicts.
func TestMerge_ThirdPartyOnly(t *testing.T) {
	snapshots := []*SourceSnapshot{
		mkSnapshot("https://a.example.com/catalog.yaml", StatusOK, mkEntry("kyverno"), mkEntry("falco")),
		mkSnapshot("https://b.example.com/catalog.yaml", StatusOK, mkEntry("linkerd")),
	}

	got := Merge(nil, snapshots)

	if len(got.Entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(got.Entries))
	}
	if len(got.Conflicts) != 0 {
		t.Fatalf("expected 0 conflicts, got %d", len(got.Conflicts))
	}
	// Sorted by Name.
	wantNames := []string{"falco", "kyverno", "linkerd"}
	if names := entryNames(got.Entries); !reflect.DeepEqual(names, wantNames) {
		t.Fatalf("expected sorted names %v, got %v", wantNames, names)
	}
	// Origin must be the source URL.
	if e, ok := findEntry(got.Entries, "kyverno"); !ok || e.Origin != "https://a.example.com/catalog.yaml" {
		t.Errorf("kyverno origin: want %q, got %q (found=%v)", "https://a.example.com/catalog.yaml", e.Origin, ok)
	}
	if e, ok := findEntry(got.Entries, "linkerd"); !ok || e.Origin != "https://b.example.com/catalog.yaml" {
		t.Errorf("linkerd origin: want %q, got %q (found=%v)", "https://b.example.com/catalog.yaml", e.Origin, ok)
	}
	// None overridden.
	for _, e := range got.Entries {
		if e.Overridden {
			t.Errorf("entry %q: expected Overridden=false", e.Name)
		}
	}
}

// TestMerge_EmbeddedVsThirdPartyCollision — Case 4: embedded has
// cert-manager; one third-party source also has cert-manager. Embedded
// wins. Third-party entry is NOT in the merged list (the winner is
// embedded), but a Conflict{Reason:"embedded-wins"} is recorded with
// the losing third-party URL.
func TestMerge_EmbeddedVsThirdPartyCollision(t *testing.T) {
	embedded := []catalog.CatalogEntry{mkEntry("cert-manager")}
	thirdPartyURL := "https://vendor.example.com/catalog.yaml"
	snapshots := []*SourceSnapshot{
		mkSnapshot(thirdPartyURL, StatusOK, mkEntry("cert-manager")),
	}

	got := Merge(embedded, snapshots)

	// Exactly one merged entry; it's the embedded one.
	if len(got.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got.Entries))
	}
	e := got.Entries[0]
	if e.Name != "cert-manager" {
		t.Fatalf("expected cert-manager, got %q", e.Name)
	}
	if e.Origin != OriginEmbedded {
		t.Errorf("expected Origin=%q, got %q", OriginEmbedded, e.Origin)
	}
	if e.Overridden {
		t.Errorf("embedded winner must not be marked Overridden")
	}

	// Exactly one conflict.
	if len(got.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(got.Conflicts))
	}
	c := got.Conflicts[0]
	if c.Name != "cert-manager" {
		t.Errorf("conflict name: want cert-manager, got %q", c.Name)
	}
	if c.Winner != OriginEmbedded {
		t.Errorf("conflict winner: want %q, got %q", OriginEmbedded, c.Winner)
	}
	if c.Reason != ReasonEmbeddedWins {
		t.Errorf("conflict reason: want %q, got %q", ReasonEmbeddedWins, c.Reason)
	}
	if !reflect.DeepEqual(c.Losers, []string{thirdPartyURL}) {
		t.Errorf("conflict losers: want [%s], got %v", thirdPartyURL, c.Losers)
	}
}

// TestMerge_ThirdPartyVsThirdPartyCollision — Case 5: two third-party
// sources both define `internal-foo`. Alphabetical-URL tiebreak picks
// the lexicographically smallest URL. The loser's entry is NOT in the
// merged list (only the winner is), but a Conflict is recorded with
// Reason="alphabetical-url-tiebreak" and the winner is the smaller URL.
func TestMerge_ThirdPartyVsThirdPartyCollision(t *testing.T) {
	urlA := "https://a.example.com/catalog.yaml"
	urlB := "https://b.example.com/catalog.yaml"
	snapshots := []*SourceSnapshot{
		mkSnapshot(urlB, StatusOK, mkEntry("internal-foo")),
		mkSnapshot(urlA, StatusOK, mkEntry("internal-foo")),
	}

	got := Merge(nil, snapshots)

	if len(got.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got.Entries))
	}
	e := got.Entries[0]
	if e.Name != "internal-foo" {
		t.Fatalf("expected internal-foo, got %q", e.Name)
	}
	if e.Origin != urlA {
		t.Errorf("winner origin: want %q (smaller URL), got %q", urlA, e.Origin)
	}
	if e.Overridden {
		t.Errorf("winner must not be marked Overridden")
	}

	if len(got.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(got.Conflicts))
	}
	c := got.Conflicts[0]
	if c.Winner != urlA {
		t.Errorf("conflict winner: want %q, got %q", urlA, c.Winner)
	}
	if c.Reason != ReasonAlphabeticalURLTiebreak {
		t.Errorf("conflict reason: want %q, got %q", ReasonAlphabeticalURLTiebreak, c.Reason)
	}
	if !reflect.DeepEqual(c.Losers, []string{urlB}) {
		t.Errorf("conflict losers: want [%s], got %v", urlB, c.Losers)
	}
}

// TestMerge_ThreeWayCollision — Case 6: embedded + two third-party
// sources all define `cert-manager`. Embedded wins; losers list MUST
// contain both third-party URLs (sorted alphabetically for
// determinism).
func TestMerge_ThreeWayCollision(t *testing.T) {
	embedded := []catalog.CatalogEntry{mkEntry("cert-manager")}
	urlA := "https://a.example.com/catalog.yaml"
	urlB := "https://b.example.com/catalog.yaml"
	snapshots := []*SourceSnapshot{
		mkSnapshot(urlB, StatusOK, mkEntry("cert-manager")),
		mkSnapshot(urlA, StatusOK, mkEntry("cert-manager")),
	}

	got := Merge(embedded, snapshots)

	if len(got.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got.Entries))
	}
	if got.Entries[0].Origin != OriginEmbedded {
		t.Errorf("winner origin: want %q, got %q", OriginEmbedded, got.Entries[0].Origin)
	}

	if len(got.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(got.Conflicts))
	}
	c := got.Conflicts[0]
	if c.Winner != OriginEmbedded || c.Reason != ReasonEmbeddedWins {
		t.Errorf("conflict winner/reason: got (%q, %q)", c.Winner, c.Reason)
	}
	wantLosers := []string{urlA, urlB}
	if !reflect.DeepEqual(c.Losers, wantLosers) {
		t.Errorf("conflict losers: want %v, got %v", wantLosers, c.Losers)
	}
}

// TestMerge_StaleOrFailedSnapshotsSkipped — Case 7: snapshots whose
// Status != StatusOK must be skipped defensively. Even if a stale/
// failed snapshot carries entries (because V123-1.2 retains prior
// successful entries on HTTP failure), the merger ignores them so the
// merged catalog is always derived from fresh data only.
func TestMerge_StaleOrFailedSnapshotsSkipped(t *testing.T) {
	snapshots := []*SourceSnapshot{
		mkSnapshot("https://stale.example.com/c.yaml", StatusStale, mkEntry("stale-entry")),
		mkSnapshot("https://failed.example.com/c.yaml", StatusFailed, mkEntry("failed-entry")),
		mkSnapshot("https://ok.example.com/c.yaml", StatusOK, mkEntry("ok-entry")),
	}

	got := Merge(nil, snapshots)

	if len(got.Entries) != 1 {
		t.Fatalf("expected 1 entry (only from StatusOK snapshot), got %d", len(got.Entries))
	}
	if got.Entries[0].Name != "ok-entry" {
		t.Errorf("want ok-entry, got %q", got.Entries[0].Name)
	}
	// No conflicts because stale/failed sources are invisible to the
	// merger; they could not have collided.
	if len(got.Conflicts) != 0 {
		t.Fatalf("expected 0 conflicts, got %d", len(got.Conflicts))
	}
}

// TestMerge_DeterministicOrdering — Case 8: two calls with input order
// permuted must produce byte-identical output. Catches accidental
// reliance on map iteration order.
func TestMerge_DeterministicOrdering(t *testing.T) {
	embedded := []catalog.CatalogEntry{
		mkEntry("cert-manager"),
		mkEntry("argo-rollouts"),
	}
	urlA := "https://a.example.com/catalog.yaml"
	urlB := "https://b.example.com/catalog.yaml"
	snapA := mkSnapshot(urlA, StatusOK, mkEntry("kyverno"), mkEntry("falco"))
	snapB := mkSnapshot(urlB, StatusOK, mkEntry("linkerd"), mkEntry("cert-manager"))

	// Call order 1: A then B.
	got1 := Merge(embedded, []*SourceSnapshot{snapA, snapB})
	// Call order 2: embedded reversed, snapshots reversed.
	reversedEmbedded := []catalog.CatalogEntry{embedded[1], embedded[0]}
	got2 := Merge(reversedEmbedded, []*SourceSnapshot{snapB, snapA})

	if !reflect.DeepEqual(got1, got2) {
		t.Fatalf("Merge is not deterministic under input reordering:\n  got1=%+v\n  got2=%+v", got1, got2)
	}

	// Extra sanity: entries sorted by Name.
	names := entryNames(got1.Entries)
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	if !reflect.DeepEqual(names, sorted) {
		t.Errorf("entries not sorted by name: got %v", names)
	}
	// Conflicts sorted by Name.
	cNames := make([]string, len(got1.Conflicts))
	for i, c := range got1.Conflicts {
		cNames[i] = c.Name
	}
	cSorted := append([]string(nil), cNames...)
	sort.Strings(cSorted)
	if !reflect.DeepEqual(cNames, cSorted) {
		t.Errorf("conflicts not sorted by name: got %v", cNames)
	}
}
