package api

// catalog_source_outward_surfaces_test.go — the configured catalog source
// address never leaves the process through an API body or an audit record.
//
// The metric and log surfaces have their own guard
// (catalog_source_metric_label_guard_test.go). This file covers the other
// three outward surfaces the address used to reach:
//
//   - GET /marketplace/sources (and the refresh POST, same projection):
//     the `url` field used to carry the address whenever the URL grammar
//     vouched it held no credential. That allowance was withdrawn — the
//     documented private-catalog shape hides the token in the address's
//     own PATH, where no grammar can spot it — so every third-party row
//     now reads the fixed word.
//   - GET /marketplace/addons (and every handler serving a CatalogEntry):
//     the `source` field used to carry the exact configured address, raw,
//     token and all, and the UI printed it on screen for any signed-in
//     account down to a viewer.
//   - the catalog_sources_refreshed audit record: its Detail used to list
//     the attempted addresses.
//
// Every absence claim here is made only after a positive control has
// proved the probe can find a planted value in the same kind of body.

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

// The synthetic credentials and addresses these probes drive. Nothing here
// is real. The secrets carry "+" and "=" so their percent-encoded forms are
// genuinely different strings from the raw ones.
const (
	surfaceSecret = "bf14r2-surface-synthetic+not=a=real=token"

	// surfacePathTokenURL is the documented private-catalog shape: the
	// token IS a path segment. The old grammar check vouched for exactly
	// this shape, which is why the ruling withdrew it.
	surfacePathTokenURL = "https://catalogs.example/private/" + surfaceSecret + "/catalog.yaml"

	// surfaceCleanURL carries no credential anywhere. Under the by-type
	// rule it must come out as the fixed word too (acceptance item 5).
	surfaceCleanURL = "https://plain.example/catalog.yaml"
)

// tpTestEntry builds a schema-shaped third-party entry for a snapshot.
func tpTestEntry(name string) catalog.CatalogEntry {
	return catalog.CatalogEntry{
		Name:             name,
		Description:      "Synthetic third-party addon for the outward-surface probe.",
		Chart:            name,
		Repo:             "https://charts.example/" + name,
		DefaultNamespace: name,
		Maintainers:      []string{"platform"},
		License:          "Apache-2.0",
		Category:         "networking",
		CuratedBy:        []string{"artifacthub-verified"},
	}
}

// serverWithTPSources builds a Server with the embedded test catalog plus
// one OK snapshot per given address, each contributing one entry named
// after its index. Timestamps are fixed so two builds produce identical
// wire bodies.
func serverWithTPSources(t *testing.T, urlsToEntryName map[string]string) *Server {
	t.Helper()
	s := serverWithCatalog(t, testCatalog(t))
	fixed := time.Date(2026, 4, 22, 10, 0, 0, 0, time.UTC)
	snaps := make(map[string]*sources.SourceSnapshot, len(urlsToEntryName))
	for u, name := range urlsToEntryName {
		snaps[u] = &sources.SourceSnapshot{
			URL:           u,
			Status:        sources.StatusOK,
			LastSuccessAt: fixed,
			LastAttemptAt: fixed,
			Entries:       []catalog.CatalogEntry{tpTestEntry(name)},
		}
	}
	s.SetSourcesFetcher(makeFetcherWithSnapshots(t, snaps))
	return s
}

// surfaceLeakForms is every shape of the address and its credential this
// probe refuses to find in a body. Mirrors the forms the metric guard
// checks, minus the hash forms it already owns — an API body carrying a
// hash of the address would still fail item 10, so the hash forms are
// included here too via the guard's shared helper.
func surfaceLeakForms(secret, rawURL string) []leakForm {
	return credentialLeakForms(secret, rawURL)
}

// assertProbeSeesPlantedValue is the positive control every body check
// runs FIRST: serialize a value carrying the secret through the same JSON
// writer the handlers use and require the probe to find it. A probe that
// cannot find a planted sentinel proves nothing by finding nothing.
func assertProbeSeesPlantedValue(t *testing.T) {
	t.Helper()
	rw := httptest.NewRecorder()
	writeJSON(rw, http.StatusOK, catalogSourceRecord{URL: surfacePathTokenURL})
	planted := rw.Body.String()
	found := false
	for _, f := range surfaceLeakForms(surfaceSecret, surfacePathTokenURL) {
		if strings.Contains(planted, f.text) {
			found = true
		}
	}
	if !found {
		t.Fatalf("POSITIVE CONTROL FAILED | a body that was deliberately built to carry the synthetic token shows none of the forms this probe checks, so its absence checks would prove nothing. Body: %s", planted)
	}
}

// TestMarketplaceSourcesBodyCarriesNoAddress — surface 4. Rows are
// "embedded" then the fixed word, and no form of either address — path
// token or completely clean — appears in the bytes.
func TestMarketplaceSourcesBodyCarriesNoAddress(t *testing.T) {
	assertProbeSeesPlantedValue(t)

	s := serverWithTPSources(t, map[string]string{
		surfacePathTokenURL: "tp-tokened",
		surfaceCleanURL:     "tp-clean",
	})
	rw, body := callListSources(t, s)
	if len(body) != 3 {
		t.Fatalf("len(body) = %d, want 3 (embedded + 2 third-party)", len(body))
	}
	if body[0].URL != "embedded" {
		t.Errorf("body[0].URL = %q, want \"embedded\" — the embedded pseudo-source keeps its name", body[0].URL)
	}
	for i := 1; i < len(body); i++ {
		if body[i].URL != credsafe.RedactedSourceLabel {
			t.Errorf("body[%d].URL = %q, want %q", i, body[i].URL, credsafe.RedactedSourceLabel)
		}
	}

	raw := rw.Body.String()
	for _, f := range surfaceLeakForms(surfaceSecret, surfacePathTokenURL) {
		if strings.Contains(raw, f.text) {
			t.Errorf("LEAK | GET /marketplace/sources carries %s", f.what)
		}
	}
	// Acceptance item 5, on this surface: the clean address is absent too.
	if strings.Contains(raw, surfaceCleanURL) {
		t.Errorf("ADDRESS ON THE WIRE | the clean address %q appears in GET /marketplace/sources — the withdrawn allowance is back", surfaceCleanURL)
	}
}

// TestMarketplaceAddonsBodyCarriesNoAddress — surface 5, the one the UI
// prints on screen. Third-party entries carry source="redacted", embedded
// entries keep source="embedded", and no form of either address appears in
// the response bytes.
func TestMarketplaceAddonsBodyCarriesNoAddress(t *testing.T) {
	assertProbeSeesPlantedValue(t)

	s := serverWithTPSources(t, map[string]string{
		surfacePathTokenURL: "tp-tokened",
		surfaceCleanURL:     "tp-clean",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/addons", nil)
	rw := httptest.NewRecorder()
	s.handleListCatalogAddons(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rw.Code, rw.Body.String())
	}

	var resp struct {
		Addons []catalog.CatalogEntry `json:"addons"`
		Total  int                    `json:"total"`
	}
	if err := json.Unmarshal(rw.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body = %s", err, rw.Body.String())
	}
	if len(resp.Addons) != 4 {
		t.Fatalf("len(addons) = %d, want 4 (2 embedded + 2 third-party)", len(resp.Addons))
	}

	embeddedSeen, thirdPartySeen := 0, 0
	for _, e := range resp.Addons {
		switch e.Name {
		case "cert-manager", "grafana":
			embeddedSeen++
			if e.Source != catalog.SourceEmbedded {
				t.Errorf("embedded entry %q: source = %q, want %q", e.Name, e.Source, catalog.SourceEmbedded)
			}
		case "tp-tokened", "tp-clean":
			thirdPartySeen++
			if e.Source != credsafe.RedactedSourceLabel {
				t.Errorf("third-party entry %q: source = %q, want %q — the configured address must not be printed next to an addon", e.Name, e.Source, credsafe.RedactedSourceLabel)
			}
		default:
			t.Errorf("unexpected entry %q in the merged view", e.Name)
		}
	}
	if embeddedSeen != 2 || thirdPartySeen != 2 {
		t.Fatalf("NOT EXERCISED | saw %d embedded and %d third-party entries, want 2 and 2 — a view missing the third-party entries would make the absence checks below meaningless", embeddedSeen, thirdPartySeen)
	}

	raw := rw.Body.String()
	for _, f := range surfaceLeakForms(surfaceSecret, surfacePathTokenURL) {
		if strings.Contains(raw, f.text) {
			t.Errorf("LEAK | GET /marketplace/addons carries %s", f.what)
		}
	}
	if strings.Contains(raw, surfaceCleanURL) {
		t.Errorf("ADDRESS ON THE WIRE | the clean address %q appears in GET /marketplace/addons", surfaceCleanURL)
	}
}

// TestTwoSourcesDifferingOnlyInTheCredentialAnswerIdenticalBodies —
// acceptance item 1 on the API surfaces: swap ONLY the credential inside
// the configured address and every body must stay byte-identical. If any
// byte moved with the secret, the response would be a way of telling one
// secret from another.
func TestTwoSourcesDifferingOnlyInTheCredentialAnswerIdenticalBodies(t *testing.T) {
	const (
		secretA = "bf14r2-pair-first-synthetic+not=a=real=token"
		secretB = "bf14r2-pair-second-synthetic+not=a=real=token"
	)
	if secretA == secretB {
		t.Fatalf("the two credentials are the same string, so this test would pass whatever the code did")
	}
	urlFor := func(secret string) string {
		return "https://catalogs.example/private/" + secret + "/catalog.yaml"
	}
	if urlFor(secretA) == urlFor(secretB) {
		t.Fatalf("the two addresses are the same string, so comparing their bodies proves nothing")
	}

	bodyOf := func(secret string) (sourcesBody, addonsBody, auditDetailStr string) {
		s := serverWithTPSources(t, map[string]string{urlFor(secret): "tp-pair"})

		rw, _ := callListSources(t, s)
		sourcesBody = rw.Body.String()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/addons", nil)
		rw2 := httptest.NewRecorder()
		s.handleListCatalogAddons(rw2, req)
		if rw2.Code != http.StatusOK {
			t.Fatalf("addons status = %d, want 200", rw2.Code)
		}
		addonsBody = rw2.Body.String()

		refreshReq := httptest.NewRequest(http.MethodPost, "/api/v1/catalog/sources/refresh", nil)
		ctx, fields := audit.WithEnrichment(refreshReq.Context())
		refreshReq = refreshReq.WithContext(ctx)
		rw3 := httptest.NewRecorder()
		s.handleRefreshCatalogSources(rw3, refreshReq)
		if rw3.Code != http.StatusOK {
			t.Fatalf("refresh status = %d, want 200", rw3.Code)
		}
		return sourcesBody, addonsBody, fields.Detail
	}

	srcA, addonsA, auditA := bodyOf(secretA)
	srcB, addonsB, auditB := bodyOf(secretB)
	if srcA == "" || addonsA == "" || auditA == "" {
		t.Fatalf("NOT EXERCISED | one of the first run's bodies came back empty, so comparing the runs proves nothing")
	}
	if srcA != srcB {
		t.Errorf("DIFFERENT | GET /marketplace/sources answered different bytes for two addresses differing only in the credential.\nfirst:\n%s\nsecond:\n%s", srcA, srcB)
	}
	if addonsA != addonsB {
		t.Errorf("DIFFERENT | GET /marketplace/addons answered different bytes for two addresses differing only in the credential.\nfirst:\n%s\nsecond:\n%s", addonsA, addonsB)
	}
	if auditA != auditB {
		t.Errorf("DIFFERENT | the refresh audit Detail differs for two addresses differing only in the credential.\nfirst:  %s\nsecond: %s", auditA, auditB)
	}
	for _, secret := range []string{secretA, secretB} {
		for _, body := range []string{srcA, srcB, addonsA, addonsB, auditA, auditB} {
			if strings.Contains(body, secret) {
				t.Errorf("LEAK | a body carries the synthetic credential %q", secret)
			}
		}
	}
}

// TestRefreshAuditDetailCarriesNoAddress — surface 6 with a token-bearing
// address specifically, probed with the full set of leak forms after a
// positive control on the same Detail-shaped channel.
func TestRefreshAuditDetailCarriesNoAddress(t *testing.T) {
	// Positive control: the probe must find a planted value in a JSON
	// Detail string built the same way the handler builds one.
	plantedJSON, err := json.Marshal(map[string]interface{}{"urls": []string{surfacePathTokenURL}})
	if err != nil {
		t.Fatalf("building the planted Detail failed: %v", err)
	}
	found := false
	for _, f := range surfaceLeakForms(surfaceSecret, surfacePathTokenURL) {
		if strings.Contains(string(plantedJSON), f.text) {
			found = true
		}
	}
	if !found {
		t.Fatalf("POSITIVE CONTROL FAILED | a Detail deliberately built to carry the address shows none of the probe's forms, so the absence check below would prove nothing")
	}

	s := serverWithTPSources(t, map[string]string{surfacePathTokenURL: "tp-tokened"})
	_, body, fields := callRefreshSources(t, s)
	if len(body) != 2 {
		t.Fatalf("len(body) = %d, want 2 (embedded + 1 third-party)", len(body))
	}
	attempted, statusCounts := auditDetail(t, fields)
	if attempted != 1 || statusCounts["ok"] != 1 {
		t.Errorf("audit counts = (%d, %v), want (1, {\"ok\": 1}) — a Detail that stopped counting would be hiding the refresh, not protecting it", attempted, statusCounts)
	}
	for _, f := range surfaceLeakForms(surfaceSecret, surfacePathTokenURL) {
		if strings.Contains(fields.Detail, f.text) {
			t.Errorf("LEAK | the audit Detail carries %s: %s", f.what, fields.Detail)
		}
	}
}
