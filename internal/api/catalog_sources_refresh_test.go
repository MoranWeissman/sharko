package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/catalog/sources"
)

// callRefreshSources invokes POST /api/v1/catalog/sources/refresh with an
// enrichment context attached (mirrors what auditMiddleware does in the real
// stack) so the tests can read back the audit fields the handler stamped.
// Returns the recorder, the decoded response body, and the in-flight audit
// Fields so each case can assert Event + Detail as documented in the AC.
func callRefreshSources(t *testing.T, s *Server) (*httptest.ResponseRecorder, []catalogSourceRecord, *audit.Fields) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/catalog/sources/refresh", nil)
	ctx, fields := audit.WithEnrichment(req.Context())
	req = req.WithContext(ctx)
	rw := httptest.NewRecorder()
	s.handleRefreshCatalogSources(rw, req)
	if rw.Code != http.StatusOK {
		// Return what we have so the caller can assert on non-200 cases.
		return rw, nil, fields
	}
	var body []catalogSourceRecord
	if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body = %s", err, rw.Body.String())
	}
	return rw, body, fields
}

// auditDetail parses the Detail JSON string back into its map shape so tests
// can assert on the urls / status payload without string-matching.
func auditDetail(t *testing.T, fields *audit.Fields) (urls []string, statusByURL map[string]string) {
	t.Helper()
	if fields == nil {
		t.Fatal("audit fields is nil — enrichment context was not attached")
	}
	if fields.Event != "catalog_sources_refreshed" {
		t.Errorf("audit Event = %q, want \"catalog_sources_refreshed\"", fields.Event)
	}
	if fields.Detail == "" {
		t.Fatal("audit Detail is empty; want JSON payload")
	}
	var payload struct {
		URLs   []string          `json:"urls"`
		Status map[string]string `json:"status"`
	}
	if err := json.Unmarshal([]byte(fields.Detail), &payload); err != nil {
		t.Fatalf("decode audit Detail: %v; detail = %s", err, fields.Detail)
	}
	return payload.URLs, payload.Status
}

// --- V123-2.4 / B2 BLOCKER fix: admin-only authz gate ---
//
// The refresh endpoint is classified Tier-2 (admin-only, audit-logged).
// The new authz call lives at the top of the handler (before the
// catalog-loaded check) so that operators / viewers see a clean 403
// regardless of catalog state. The pre-existing tests above intentionally
// do NOT send role headers — `authz.Require` treats "no X-Sharko-User
// AND no X-Sharko-Role" as no-auth mode and lets the request through, so
// those tests keep exercising the success path. The cases below cover
// the new gate.

// TestRefreshCatalogSources_AuthzDeniesViewer — a viewer-role caller
// must be rejected with HTTP 403 + JSON error body before any catalog
// work happens. This is the load-bearing assertion for B2: pre-fix,
// non-admins could drive force-refreshes; post-fix, only admins can.
func TestRefreshCatalogSources_AuthzDeniesViewer(t *testing.T) {
	s := serverWithCatalog(t, testCatalog(t))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/catalog/sources/refresh", nil)
	req.Header.Set("X-Sharko-User", "viewer-user")
	req.Header.Set("X-Sharko-Role", "viewer")
	rw := httptest.NewRecorder()
	s.handleRefreshCatalogSources(rw, req)

	if rw.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", rw.Code, rw.Body.String())
	}
	if ct := rw.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var errBody map[string]interface{}
	if err := json.Unmarshal(rw.Body.Bytes(), &errBody); err != nil {
		t.Fatalf("decode 403 body: %v; body = %s", err, rw.Body.String())
	}
	if errBody["error"] == nil {
		t.Errorf("403 body missing \"error\" key; got %+v", errBody)
	}
}

// TestRefreshCatalogSources_AuthzDeniesOperator — an operator-role
// caller is also denied. Operators have write-but-not-admin scope; the
// refresh endpoint is admin-only because it generates significant
// outbound traffic + audit-log noise.
func TestRefreshCatalogSources_AuthzDeniesOperator(t *testing.T) {
	s := serverWithCatalog(t, testCatalog(t))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/catalog/sources/refresh", nil)
	req.Header.Set("X-Sharko-User", "operator-user")
	req.Header.Set("X-Sharko-Role", "operator")
	rw := httptest.NewRecorder()
	s.handleRefreshCatalogSources(rw, req)

	if rw.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", rw.Code, rw.Body.String())
	}
}

// TestRefreshCatalogSources_AuthzAllowsAdmin — admin role passes the
// gate and reaches the embedded-only success path (no fetcher → 200 with
// the embedded pseudo-source). Mirrors
// TestRefreshCatalogSources_NoFetcher_ReturnsEmbeddedOnly but with the
// authz headers explicit so future readers see the contract.
func TestRefreshCatalogSources_AuthzAllowsAdmin(t *testing.T) {
	c := testCatalog(t)
	s := serverWithCatalog(t, c)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/catalog/sources/refresh", nil)
	req.Header.Set("X-Sharko-User", "admin-user")
	req.Header.Set("X-Sharko-Role", "admin")
	ctx, _ := audit.WithEnrichment(req.Context())
	req = req.WithContext(ctx)
	rw := httptest.NewRecorder()
	s.handleRefreshCatalogSources(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rw.Code, rw.Body.String())
	}
	var body []catalogSourceRecord
	if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body = %s", err, rw.Body.String())
	}
	if len(body) != 1 || body[0].URL != "embedded" {
		t.Errorf("admin success body = %+v, want embedded-only single-row", body)
	}
}

// TestRefreshCatalogSources_503OnNilCatalog — when the embedded catalog
// never loaded (misconfiguration), the force-refresh endpoint surfaces 503
// with an error JSON body. Matches the V123-1.5 GET contract for the same
// failure mode so API consumers see identical semantics on both routes.
func TestRefreshCatalogSources_503OnNilCatalog(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/catalog/sources/refresh", nil)
	ctx, _ := audit.WithEnrichment(req.Context())
	req = req.WithContext(ctx)
	rw := httptest.NewRecorder()
	s.handleRefreshCatalogSources(rw, req)

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

// TestRefreshCatalogSources_NoFetcher_ReturnsEmbeddedOnly — embedded-only
// deployment (no fetcher wired). Expect 200, response contains exactly the
// embedded pseudo-source row, and the audit event is still emitted with an
// empty urls / status payload so "admin clicked refresh on embedded-only"
// is captured in the audit log. This IS a user action worth recording even
// when the operation is a no-op.
func TestRefreshCatalogSources_NoFetcher_ReturnsEmbeddedOnly(t *testing.T) {
	c := testCatalog(t)
	s := serverWithCatalog(t, c)

	_, body, fields := callRefreshSources(t, s)
	if len(body) != 1 {
		t.Fatalf("len(body) = %d, want 1 (embedded-only, no fetcher)", len(body))
	}
	if body[0].URL != "embedded" {
		t.Errorf("body[0].URL = %q, want \"embedded\"", body[0].URL)
	}
	if body[0].EntryCount != c.Len() {
		t.Errorf("body[0].EntryCount = %d, want %d", body[0].EntryCount, c.Len())
	}

	urls, statusByURL := auditDetail(t, fields)
	if len(urls) != 0 {
		t.Errorf("audit urls = %v, want empty slice (no fetcher configured)", urls)
	}
	if len(statusByURL) != 0 {
		t.Errorf("audit statusByURL = %v, want empty map", statusByURL)
	}
}

// TestRefreshCatalogSources_FetcherEmpty_NoSourcesConfigured — fetcher is
// wired but has no configured URLs (SHARKO_CATALOG_URLS unset). ForceRefresh
// is a no-op (resolveTargets returns an empty list) and Snapshots() yields
// an empty map. Response and audit payload match the no-fetcher case so the
// two embedded-only topologies behave identically on the wire.
func TestRefreshCatalogSources_FetcherEmpty_NoSourcesConfigured(t *testing.T) {
	c := testCatalog(t)
	s := serverWithCatalog(t, c)
	s.SetSourcesFetcher(makeFetcherWithSnapshots(t, map[string]*sources.SourceSnapshot{}))

	_, body, fields := callRefreshSources(t, s)
	if len(body) != 1 {
		t.Fatalf("len(body) = %d, want 1 (fetcher with empty snapshots)", len(body))
	}
	if body[0].URL != "embedded" {
		t.Errorf("body[0].URL = %q, want \"embedded\"", body[0].URL)
	}

	urls, statusByURL := auditDetail(t, fields)
	if len(urls) != 0 {
		t.Errorf("audit urls = %v, want empty (no sources configured)", urls)
	}
	if len(statusByURL) != 0 {
		t.Errorf("audit statusByURL = %v, want empty map", statusByURL)
	}
}

// TestRefreshCatalogSources_SingleOKSnapshot — pre-populated OK snapshot on a
// fetcher built with an empty Sources list. ForceRefresh becomes a no-op
// (resolveTargets ignores URLs that aren't in cfg.Sources) so the injected
// snapshot survives, and the handler returns it as a third-party record
// with status:"ok". Audit payload mirrors the snapshot state.
func TestRefreshCatalogSources_SingleOKSnapshot(t *testing.T) {
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
			},
		},
	}
	s.SetSourcesFetcher(makeFetcherWithSnapshots(t, snaps))

	_, body, fields := callRefreshSources(t, s)
	if len(body) != 2 {
		t.Fatalf("len(body) = %d, want 2 (embedded + 1 third-party)", len(body))
	}
	if body[0].URL != "embedded" {
		t.Errorf("body[0].URL = %q, want \"embedded\"", body[0].URL)
	}
	tp := body[1]
	if tp.URL != url {
		t.Errorf("body[1].URL = %q, want %q", tp.URL, url)
	}
	if tp.Status != "ok" {
		t.Errorf("body[1].Status = %q, want \"ok\"", tp.Status)
	}
	if tp.LastFetched == nil || !tp.LastFetched.Equal(success) {
		t.Errorf("body[1].LastFetched = %v, want %v", tp.LastFetched, success)
	}

	urls, statusByURL := auditDetail(t, fields)
	if len(urls) != 1 || urls[0] != url {
		t.Errorf("audit urls = %v, want [%q]", urls, url)
	}
	if statusByURL[url] != "ok" {
		t.Errorf("audit statusByURL[%q] = %q, want \"ok\"", url, statusByURL[url])
	}
}

// TestRefreshCatalogSources_SingleFailedSnapshot — pre-populated failure
// snapshot (never-succeeded source). Response row surfaces status:"failed"
// and the audit payload records the same per-URL state. Exercises the
// recordFailure → response projection for the refresh path.
func TestRefreshCatalogSources_SingleFailedSnapshot(t *testing.T) {
	s := serverWithCatalog(t, testCatalog(t))

	url := "https://broken.example.com/catalog.yaml"
	snaps := map[string]*sources.SourceSnapshot{
		url: {
			URL:           url,
			Status:        sources.StatusFailed,
			LastAttemptAt: time.Now(),
			// LastSuccessAt intentionally zero — never fetched cleanly.
			// Entries intentionally nil — nothing to serve.
		},
	}
	s.SetSourcesFetcher(makeFetcherWithSnapshots(t, snaps))

	_, body, fields := callRefreshSources(t, s)
	if len(body) != 2 {
		t.Fatalf("len(body) = %d, want 2", len(body))
	}
	tp := body[1]
	if tp.Status != "failed" {
		t.Errorf("body[1].Status = %q, want \"failed\"", tp.Status)
	}
	if tp.LastFetched != nil {
		t.Errorf("body[1].LastFetched = %v, want nil (zero time → JSON null)", tp.LastFetched)
	}
	if tp.EntryCount != 0 {
		t.Errorf("body[1].EntryCount = %d, want 0", tp.EntryCount)
	}

	urls, statusByURL := auditDetail(t, fields)
	if len(urls) != 1 || urls[0] != url {
		t.Errorf("audit urls = %v, want [%q]", urls, url)
	}
	if statusByURL[url] != "failed" {
		t.Errorf("audit statusByURL[%q] = %q, want \"failed\"", url, statusByURL[url])
	}
}

// TestRefreshCatalogSources_MultipleSources_AlphabeticalSort — three
// snapshots injected in a deliberately non-alphabetical order; asserts that
// both the response's third-party rows AND the audit Detail's urls array
// come back alphabetically sorted. Deterministic ordering is load-bearing:
// without it, Go's randomised map iteration would leak into the response
// (response test flakiness) and into the audit log (making log diffs +
// alerting rules non-deterministic).
func TestRefreshCatalogSources_MultipleSources_AlphabeticalSort(t *testing.T) {
	s := serverWithCatalog(t, testCatalog(t))

	urls := []string{
		"https://zeta.example.com/catalog.yaml",
		"https://alpha.example.com/catalog.yaml",
		"https://mid.example.com/catalog.yaml",
	}
	snaps := make(map[string]*sources.SourceSnapshot, len(urls))
	for _, u := range urls {
		snaps[u] = &sources.SourceSnapshot{
			URL:           u,
			Status:        sources.StatusOK,
			LastSuccessAt: time.Now(),
			LastAttemptAt: time.Now(),
			Entries:       []catalog.CatalogEntry{{Name: "x", Chart: "x", Repo: "https://x"}},
		}
	}
	s.SetSourcesFetcher(makeFetcherWithSnapshots(t, snaps))

	_, body, fields := callRefreshSources(t, s)
	if len(body) != 4 {
		t.Fatalf("len(body) = %d, want 4 (embedded + 3 third-party)", len(body))
	}
	if body[0].URL != "embedded" {
		t.Errorf("body[0].URL = %q, embedded must always be first", body[0].URL)
	}
	wantOrder := []string{
		"https://alpha.example.com/catalog.yaml",
		"https://mid.example.com/catalog.yaml",
		"https://zeta.example.com/catalog.yaml",
	}
	for i, want := range wantOrder {
		got := body[i+1].URL
		if got != want {
			t.Errorf("body[%d].URL = %q, want %q (third-party must be alphabetical)", i+1, got, want)
		}
	}

	auditURLs, statusByURL := auditDetail(t, fields)
	if len(auditURLs) != 3 {
		t.Fatalf("audit urls length = %d, want 3", len(auditURLs))
	}
	for i, want := range wantOrder {
		if auditURLs[i] != want {
			t.Errorf("audit urls[%d] = %q, want %q (audit list must be alphabetical too)", i, auditURLs[i], want)
		}
	}
	// Status map is unordered on the wire, but every URL must have an entry.
	for _, u := range wantOrder {
		if statusByURL[u] != "ok" {
			t.Errorf("audit statusByURL[%q] = %q, want \"ok\"", u, statusByURL[u])
		}
	}
}
