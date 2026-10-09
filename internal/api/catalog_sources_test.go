package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/catalog/sources"
	"github.com/MoranWeissman/sharko/internal/credsafe"
)

// callListSources invokes the GET /catalog/sources handler and returns the
// recorder + decoded body. Keeps each test case short.
func callListSources(t *testing.T, s *Server) (*httptest.ResponseRecorder, []catalogSourceRecord) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/sources", nil)
	rw := httptest.NewRecorder()
	s.handleListCatalogSources(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rw.Code, rw.Body.String())
	}
	var body []catalogSourceRecord
	if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body = %s", err, rw.Body.String())
	}
	return rw, body
}

// assertEmbeddedRecord checks the invariants every response must satisfy
// on the first element (the embedded pseudo-source row).
func assertEmbeddedRecord(t *testing.T, rec catalogSourceRecord, wantCount int) {
	t.Helper()
	if rec.URL != "embedded" {
		t.Errorf("embedded URL = %q, want literal \"embedded\"", rec.URL)
	}
	if rec.Status != "ok" {
		t.Errorf("embedded Status = %q, want \"ok\"", rec.Status)
	}
	if rec.LastFetched != nil {
		t.Errorf("embedded LastFetched = %v, want nil", rec.LastFetched)
	}
	if rec.EntryCount != wantCount {
		t.Errorf("embedded EntryCount = %d, want %d", rec.EntryCount, wantCount)
	}
	if !rec.Verified {
		t.Errorf("embedded Verified = false, want true (binary trusts its own bundled catalog)")
	}
}

// TestListCatalogSources_EmbeddedOnly_NoFetcher — no fetcher wired at all
// (embedded-only deployment). Expect exactly one record: the embedded row.
// The handler MUST NOT 503 just because s.sourcesFetcher is nil.
func TestListCatalogSources_EmbeddedOnly_NoFetcher(t *testing.T) {
	c := testCatalog(t)
	s := serverWithCatalog(t, c)

	_, body := callListSources(t, s)
	if len(body) != 1 {
		t.Fatalf("len(body) = %d, want 1 (embedded-only, no fetcher)", len(body))
	}
	assertEmbeddedRecord(t, body[0], c.Len())
}

// TestListCatalogSources_EmbeddedOnly_FetcherNoSnapshots — fetcher exists
// but no snapshots configured (no SHARKO_CATALOG_URLS). Expect the same
// single-element response as the no-fetcher case.
func TestListCatalogSources_EmbeddedOnly_FetcherNoSnapshots(t *testing.T) {
	c := testCatalog(t)
	s := serverWithCatalog(t, c)
	s.SetSourcesFetcher(makeFetcherWithSnapshots(t, map[string]*sources.SourceSnapshot{}))

	_, body := callListSources(t, s)
	if len(body) != 1 {
		t.Fatalf("len(body) = %d, want 1 (fetcher with empty snapshots)", len(body))
	}
	assertEmbeddedRecord(t, body[0], c.Len())
}

// TestListCatalogSources_WithOKSnapshot — one healthy third-party source.
// Expect embedded + one third-party row with Status=ok, LastFetched non-nil
// equal to the snapshot's LastSuccessAt, EntryCount matching the injected
// Entries length, and Verified pulled through from the snapshot.
func TestListCatalogSources_WithOKSnapshot(t *testing.T) {
	c := testCatalog(t)
	s := serverWithCatalog(t, c)

	url := "https://internal.example.com/catalog.yaml"
	success := time.Date(2026, 4, 22, 10, 0, 0, 0, time.UTC)
	snaps := map[string]*sources.SourceSnapshot{
		url: {
			URL:           url,
			Status:        sources.StatusOK,
			LastSuccessAt: success,
			LastAttemptAt: success,
			Verified:      true,
			Issuer:        "https://github.com/internal-platform-team",
			Entries: []catalog.CatalogEntry{
				{Name: "a", Chart: "a", Repo: "https://x"},
				{Name: "b", Chart: "b", Repo: "https://x"},
				{Name: "c", Chart: "c", Repo: "https://x"},
			},
		},
	}
	s.SetSourcesFetcher(makeFetcherWithSnapshots(t, snaps))

	_, body := callListSources(t, s)
	if len(body) != 2 {
		t.Fatalf("len(body) = %d, want 2 (embedded + 1 third-party)", len(body))
	}
	assertEmbeddedRecord(t, body[0], c.Len())

	tp := body[1]
	if tp.URL != credsafe.RedactedSourceLabel {
		t.Errorf("third-party URL = %q, want the fixed word %q — the configured address never reaches the wire", tp.URL, credsafe.RedactedSourceLabel)
	}
	if tp.Status != "ok" {
		t.Errorf("third-party Status = %q, want \"ok\"", tp.Status)
	}
	if tp.LastFetched == nil {
		t.Fatalf("third-party LastFetched = nil, want non-nil")
	}
	if !tp.LastFetched.Equal(success) {
		t.Errorf("third-party LastFetched = %v, want %v", *tp.LastFetched, success)
	}
	if tp.EntryCount != 3 {
		t.Errorf("third-party EntryCount = %d, want 3", tp.EntryCount)
	}
	if !tp.Verified {
		t.Errorf("third-party Verified = false, want true")
	}
	if tp.Issuer != "https://github.com/internal-platform-team" {
		t.Errorf("third-party Issuer = %q, want the injected OIDC subject", tp.Issuer)
	}
}

// TestListCatalogSources_WithStaleSnapshot — a source that had a prior
// success but whose most recent fetch failed; the fetcher records it as
// StatusStale, and the handler surfaces "stale" on the wire. LastFetched
// reflects the prior success so the UI can show "last seen N min ago".
func TestListCatalogSources_WithStaleSnapshot(t *testing.T) {
	s := serverWithCatalog(t, testCatalog(t))

	url := "https://stale.example.com/catalog.yaml"
	priorSuccess := time.Date(2026, 4, 22, 9, 0, 0, 0, time.UTC)
	snaps := map[string]*sources.SourceSnapshot{
		url: {
			URL:           url,
			Status:        sources.StatusStale,
			LastSuccessAt: priorSuccess,
			LastAttemptAt: priorSuccess.Add(time.Hour),
			Entries: []catalog.CatalogEntry{
				{Name: "stale-a", Chart: "a", Repo: "https://x"},
			},
		},
	}
	s.SetSourcesFetcher(makeFetcherWithSnapshots(t, snaps))

	_, body := callListSources(t, s)
	if len(body) != 2 {
		t.Fatalf("len(body) = %d, want 2", len(body))
	}
	tp := body[1]
	if tp.Status != "stale" {
		t.Errorf("stale row Status = %q, want \"stale\"", tp.Status)
	}
	if tp.LastFetched == nil || !tp.LastFetched.Equal(priorSuccess) {
		t.Errorf("stale row LastFetched = %v, want %v (prior success preserved)", tp.LastFetched, priorSuccess)
	}
	if tp.EntryCount != 1 {
		t.Errorf("stale row EntryCount = %d, want 1 (last-good data retained)", tp.EntryCount)
	}
}

// TestListCatalogSources_WithFailedSnapshot — a fresh-start failure with
// no prior success: Status=failed, no entries, LastFetched null. Covers
// the fetcher's recordFailure path when LastSuccessAt is still zero.
func TestListCatalogSources_WithFailedSnapshot(t *testing.T) {
	s := serverWithCatalog(t, testCatalog(t))

	url := "https://broken.example.com/catalog.yaml"
	snaps := map[string]*sources.SourceSnapshot{
		url: {
			URL:           url,
			Status:        sources.StatusFailed,
			LastAttemptAt: time.Now(),
			// LastSuccessAt intentionally zero — never fetched cleanly
			// Entries intentionally nil — nothing to serve
		},
	}
	s.SetSourcesFetcher(makeFetcherWithSnapshots(t, snaps))

	_, body := callListSources(t, s)
	if len(body) != 2 {
		t.Fatalf("len(body) = %d, want 2", len(body))
	}
	tp := body[1]
	if tp.Status != "failed" {
		t.Errorf("failed row Status = %q, want \"failed\"", tp.Status)
	}
	if tp.LastFetched != nil {
		t.Errorf("failed row LastFetched = %v, want nil (zero time → JSON null)", tp.LastFetched)
	}
	if tp.EntryCount != 0 {
		t.Errorf("failed row EntryCount = %d, want 0", tp.EntryCount)
	}
	if tp.Verified {
		t.Errorf("failed row Verified = true, want false")
	}
}

// TestListCatalogSources_MultipleSourcesStableOrderAndByteIdentical —
// inject 3 snapshots in a deliberately non-alphabetical order. Every
// third-party row's url on the wire is the same fixed word, so the sort
// cannot use the wire value — the handler sorts the SNAPSHOTS by their
// internal address before projecting. Each source is given a different
// entry count so the order is still observable on the wire, and the whole
// body must come back byte-identical across two consecutive calls
// (acceptance item 7): with equal wire keys, an order left to Go's
// randomised map iteration would make two identical requests answer
// differently.
func TestListCatalogSources_MultipleSourcesStableOrderAndByteIdentical(t *testing.T) {
	s := serverWithCatalog(t, testCatalog(t))

	// Alphabetical order of the internal addresses: alpha (1 entry),
	// mid (2 entries), zeta (3 entries).
	entryCountByURL := map[string]int{
		"https://zeta.example.com/catalog.yaml":  3,
		"https://alpha.example.com/catalog.yaml": 1,
		"https://mid.example.com/catalog.yaml":   2,
	}
	fixed := time.Date(2026, 4, 22, 10, 0, 0, 0, time.UTC)
	snaps := make(map[string]*sources.SourceSnapshot, len(entryCountByURL))
	for u, n := range entryCountByURL {
		entries := make([]catalog.CatalogEntry, 0, n)
		for i := 0; i < n; i++ {
			entries = append(entries, catalog.CatalogEntry{Name: "x", Chart: "x", Repo: "https://x"})
		}
		snaps[u] = &sources.SourceSnapshot{
			URL:           u,
			Status:        sources.StatusOK,
			LastSuccessAt: fixed,
			LastAttemptAt: fixed,
			Entries:       entries,
		}
	}
	s.SetSourcesFetcher(makeFetcherWithSnapshots(t, snaps))

	rw, body := callListSources(t, s)
	if len(body) != 4 {
		t.Fatalf("len(body) = %d, want 4 (embedded + 3 third-party)", len(body))
	}
	if body[0].URL != "embedded" {
		t.Errorf("body[0].URL = %q, embedded must always be first", body[0].URL)
	}
	// Every third-party row carries the fixed word, and the order follows
	// the internal addresses alphabetically — visible here through the
	// per-source entry counts.
	wantCounts := []int{1, 2, 3}
	for i, want := range wantCounts {
		row := body[i+1]
		if row.URL != credsafe.RedactedSourceLabel {
			t.Errorf("body[%d].URL = %q, want %q", i+1, row.URL, credsafe.RedactedSourceLabel)
		}
		if row.EntryCount != want {
			t.Errorf("body[%d].EntryCount = %d, want %d (rows must follow the internal addresses alphabetically)", i+1, row.EntryCount, want)
		}
	}

	// Byte-identical across two consecutive calls with the same sources.
	rw2, _ := callListSources(t, s)
	if rw.Body.String() != rw2.Body.String() {
		t.Errorf("two consecutive GET /catalog/sources calls answered different bytes:\nfirst:\n%s\nsecond:\n%s", rw.Body.String(), rw2.Body.String())
	}
}

// TestListCatalogSources_503OnNilCatalog — the embedded catalog never
// loaded (startup path not wired), so even though the endpoint is
// read-only it returns 503 to make the misconfiguration obvious. Matches
// the contract used by the sibling /catalog/addons handlers.
func TestListCatalogSources_503OnNilCatalog(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/sources", nil)
	rw := httptest.NewRecorder()
	s.handleListCatalogSources(rw, req)

	if rw.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body = %s", rw.Code, rw.Body.String())
	}
	var errBody map[string]string
	if err := json.Unmarshal(rw.Body.Bytes(), &errBody); err != nil {
		t.Fatalf("decode: %v; body = %s", err, rw.Body.String())
	}
	if errBody["error"] == "" {
		t.Errorf("503 response body missing \"error\" key; got %+v", errBody)
	}
}

// TestListCatalogSources_JSONShape — round-trip the response through
// json.Unmarshal into []catalogSourceRecord and assert the expected
// field types show up (string URL/Status, *time.Time LastFetched, int
// EntryCount, bool Verified, string Issuer). Cheap structural guard
// against accidental rename/type drift of the wire contract.
func TestListCatalogSources_JSONShape(t *testing.T) {
	c := testCatalog(t)
	s := serverWithCatalog(t, c)

	url := "https://shape.example.com/catalog.yaml"
	success := time.Date(2026, 4, 22, 10, 0, 0, 0, time.UTC)
	snaps := map[string]*sources.SourceSnapshot{
		url: {
			URL:           url,
			Status:        sources.StatusOK,
			LastSuccessAt: success,
			LastAttemptAt: success,
			Verified:      true,
			Issuer:        "https://github.com/shape-team",
			Entries:       []catalog.CatalogEntry{{Name: "a", Chart: "a", Repo: "https://x"}},
		},
	}
	s.SetSourcesFetcher(makeFetcherWithSnapshots(t, snaps))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/sources", nil)
	rw := httptest.NewRecorder()
	s.handleListCatalogSources(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rw.Code)
	}

	// Decode into []catalogSourceRecord — this IS the type assertion; if a
	// field's JSON tag is wrong or the type changed, Unmarshal either fails
	// or fills the wrong field and the downstream checks catch it.
	var body []catalogSourceRecord
	if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode []catalogSourceRecord: %v; body = %s", err, rw.Body.String())
	}
	if len(body) != 2 {
		t.Fatalf("len(body) = %d, want 2", len(body))
	}

	// Embedded row: nil LastFetched must survive the round-trip (JSON null
	// → *time.Time nil). Verified must stay true. Issuer must stay empty
	// (,omitempty drops it on the wire; Unmarshal leaves it zero).
	emb := body[0]
	if emb.URL != "embedded" || emb.Status != "ok" {
		t.Errorf("embedded round-trip: got URL=%q Status=%q", emb.URL, emb.Status)
	}
	if emb.LastFetched != nil {
		t.Errorf("embedded LastFetched survived round-trip as %v, want nil", emb.LastFetched)
	}
	if emb.EntryCount != c.Len() {
		t.Errorf("embedded EntryCount = %d, want %d", emb.EntryCount, c.Len())
	}
	if !emb.Verified {
		t.Errorf("embedded Verified = false after round-trip, want true")
	}
	if emb.Issuer != "" {
		t.Errorf("embedded Issuer = %q after round-trip, want empty (omitempty)", emb.Issuer)
	}

	// Third-party row: non-nil *time.Time, int EntryCount, bool Verified,
	// string Issuer preserved. The url is the fixed word, never the
	// configured address.
	tp := body[1]
	if tp.URL != credsafe.RedactedSourceLabel {
		t.Errorf("third-party URL round-trip: got %q, want %q", tp.URL, credsafe.RedactedSourceLabel)
	}
	if tp.LastFetched == nil {
		t.Fatalf("third-party LastFetched decoded as nil; want *time.Time")
	}
	if !tp.LastFetched.Equal(success) {
		t.Errorf("third-party LastFetched = %v, want %v", *tp.LastFetched, success)
	}
	if tp.EntryCount != 1 {
		t.Errorf("third-party EntryCount = %d, want 1", tp.EntryCount)
	}
	if !tp.Verified {
		t.Errorf("third-party Verified = false after round-trip, want true")
	}
	if tp.Issuer != "https://github.com/shape-team" {
		t.Errorf("third-party Issuer = %q after round-trip", tp.Issuer)
	}

	// Raw-JSON spot-check: LastFetched must serialise as a RFC3339 string
	// (not an object, not a nested time struct). Decode into a permissive
	// map and inspect the raw type.
	var raw []map[string]interface{}
	if err := json.Unmarshal(rw.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode []map: %v", err)
	}
	if raw[0]["last_fetched"] != nil {
		t.Errorf("embedded last_fetched = %v, want JSON null", raw[0]["last_fetched"])
	}
	ts, ok := raw[1]["last_fetched"].(string)
	if !ok || ts == "" {
		t.Fatalf("third-party last_fetched = %v (type %T), want RFC3339 string", raw[1]["last_fetched"], raw[1]["last_fetched"])
	}
	if _, err := time.Parse(time.RFC3339, ts); err != nil {
		t.Errorf("third-party last_fetched %q failed RFC3339 parse: %v", ts, err)
	}
}
