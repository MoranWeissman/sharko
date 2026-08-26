package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/catalog/sources"
	"github.com/MoranWeissman/sharko/internal/credsafe"
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

// auditDetail parses the Detail JSON string back into its counts shape so
// tests can assert on the payload without string-matching.
//
// The payload is counts only — how many sources were attempted and how
// many ended in each state. It used to carry the attempted addresses and
// a per-address status map; a configured catalog source address can carry
// an auth token inside it, so no address may reach an audit record in any
// form, and the tests below also assert the addresses are absent from the
// Detail string itself.
func auditDetail(t *testing.T, fields *audit.Fields) (attempted int, statusCounts map[string]int) {
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
		SourcesAttempted int            `json:"sources_attempted"`
		StatusCounts     map[string]int `json:"status_counts"`
	}
	if err := json.Unmarshal([]byte(fields.Detail), &payload); err != nil {
		t.Fatalf("decode audit Detail: %v; detail = %s", err, fields.Detail)
	}
	return payload.SourcesAttempted, payload.StatusCounts
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

	attempted, statusCounts := auditDetail(t, fields)
	if attempted != 0 {
		t.Errorf("audit sources_attempted = %d, want 0 (no fetcher configured)", attempted)
	}
	if len(statusCounts) != 0 {
		t.Errorf("audit status_counts = %v, want empty map", statusCounts)
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

	attempted, statusCounts := auditDetail(t, fields)
	if attempted != 0 {
		t.Errorf("audit sources_attempted = %d, want 0 (no sources configured)", attempted)
	}
	if len(statusCounts) != 0 {
		t.Errorf("audit status_counts = %v, want empty map", statusCounts)
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
	if tp.URL != credsafe.RedactedSourceLabel {
		t.Errorf("body[1].URL = %q, want %q — the configured address never reaches the wire", tp.URL, credsafe.RedactedSourceLabel)
	}
	if tp.Status != "ok" {
		t.Errorf("body[1].Status = %q, want \"ok\"", tp.Status)
	}
	if tp.LastFetched == nil || !tp.LastFetched.Equal(success) {
		t.Errorf("body[1].LastFetched = %v, want %v", tp.LastFetched, success)
	}

	attempted, statusCounts := auditDetail(t, fields)
	if attempted != 1 {
		t.Errorf("audit sources_attempted = %d, want 1", attempted)
	}
	if statusCounts["ok"] != 1 || len(statusCounts) != 1 {
		t.Errorf("audit status_counts = %v, want exactly {\"ok\": 1}", statusCounts)
	}
	if strings.Contains(fields.Detail, url) {
		t.Errorf("ADDRESS IN THE AUDIT | the configured address %q appears in the audit Detail: %s", url, fields.Detail)
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

	attempted, statusCounts := auditDetail(t, fields)
	if attempted != 1 {
		t.Errorf("audit sources_attempted = %d, want 1", attempted)
	}
	if statusCounts["failed"] != 1 || len(statusCounts) != 1 {
		t.Errorf("audit status_counts = %v, want exactly {\"failed\": 1}", statusCounts)
	}
	if strings.Contains(fields.Detail, url) {
		t.Errorf("ADDRESS IN THE AUDIT | the configured address %q appears in the audit Detail: %s", url, fields.Detail)
	}
}

// TestRefreshCatalogSources_MultipleSources_CountsAndNoAddress — three
// snapshots injected in a deliberately non-alphabetical order. Every
// third-party row on the wire carries the fixed word, the audit Detail
// carries only counts, and no configured address appears anywhere in the
// response body or the Detail string. The Detail is also byte-identical
// across two refreshes of the same source set — that determinism used to
// come from sorting the address list, and now comes from encoding/json
// writing map keys in sorted order.
func TestRefreshCatalogSources_MultipleSources_CountsAndNoAddress(t *testing.T) {
	s := serverWithCatalog(t, testCatalog(t))

	urls := []string{
		"https://zeta.example.com/catalog.yaml",
		"https://alpha.example.com/catalog.yaml",
		"https://mid.example.com/catalog.yaml",
	}
	fixed := time.Date(2026, 4, 22, 10, 0, 0, 0, time.UTC)
	snaps := make(map[string]*sources.SourceSnapshot, len(urls))
	for _, u := range urls {
		snaps[u] = &sources.SourceSnapshot{
			URL:           u,
			Status:        sources.StatusOK,
			LastSuccessAt: fixed,
			LastAttemptAt: fixed,
			Entries:       []catalog.CatalogEntry{{Name: "x", Chart: "x", Repo: "https://x"}},
		}
	}
	s.SetSourcesFetcher(makeFetcherWithSnapshots(t, snaps))

	rw, body, fields := callRefreshSources(t, s)
	if len(body) != 4 {
		t.Fatalf("len(body) = %d, want 4 (embedded + 3 third-party)", len(body))
	}
	if body[0].URL != "embedded" {
		t.Errorf("body[0].URL = %q, embedded must always be first", body[0].URL)
	}
	for i := 1; i < len(body); i++ {
		if body[i].URL != credsafe.RedactedSourceLabel {
			t.Errorf("body[%d].URL = %q, want %q", i, body[i].URL, credsafe.RedactedSourceLabel)
		}
	}

	attempted, statusCounts := auditDetail(t, fields)
	if attempted != 3 {
		t.Errorf("audit sources_attempted = %d, want 3", attempted)
	}
	if statusCounts["ok"] != 3 || len(statusCounts) != 1 {
		t.Errorf("audit status_counts = %v, want exactly {\"ok\": 3}", statusCounts)
	}

	// No configured address, anywhere: not in the response bytes, not in
	// the audit Detail.
	for _, u := range urls {
		if strings.Contains(rw.Body.String(), u) {
			t.Errorf("ADDRESS IN THE RESPONSE | %q appears in the refresh response body", u)
		}
		if strings.Contains(fields.Detail, u) {
			t.Errorf("ADDRESS IN THE AUDIT | %q appears in the audit Detail: %s", u, fields.Detail)
		}
	}

	// Byte-identical Detail across two refreshes of the same source set.
	_, _, fields2 := callRefreshSources(t, s)
	if fields.Detail != fields2.Detail {
		t.Errorf("two refreshes of the same source set produced different audit Details:\nfirst:  %s\nsecond: %s", fields.Detail, fields2.Detail)
	}
}
