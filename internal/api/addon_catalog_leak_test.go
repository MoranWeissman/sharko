// addon_catalog_leak_test.go — the proof for B11.
//
// # What is being proved
//
// Three endpoints hand back a catalog entry as it is written in the
// repository, and a catalog entry's repository address is the operator's own
// value:
//
//	repoURL: https://x-access-token:<token>@charts.example/org/charts
//
//   - GET /api/v1/addons/list marshalled the parsed
//     models.AddonCatalogEntry values straight out, token and all — on both
//     the v3 branch (addons-catalog.yaml, read by config.Parser) and the v4
//     branch (catalog.yaml, flattened through catalog.BuildCatalogView).
//   - GET /api/v1/catalog/addons and GET /api/v1/catalog/addons/{name} did the
//     same with catalog.CatalogAddon.RepoURL, and with the repository address
//     of every extra chart source under additionalSources.
//
// Nothing had to go wrong for any of them to travel. These are the ordinary
// 200s the addon pages and the CLI call.
//
// # Why the fix could not be a strip-in-place
//
// models.AddonCatalogEntry and config.AddonCatalogEntry carry yaml tags as
// well as json ones, because they ARE the file on disk. Sharko parses the
// catalog into them, changes one field, and marshals the whole thing back
// (internal/gitops AddCatalogEntry / UpdateCatalogEntry / UpdateCatalogVersion,
// internal/orchestrator addon_configure.go and catalog_edit.go). Stripping the
// credential on the struct would have thrown the operator's stored password
// away on the next catalog write, and broken every chart-index fetch besides.
//
// So the response got its own copy. That half is proved by
// TestCatalogRoundTrip_* in internal/config and internal/gitops, which assert
// the file coming back out is byte-identical to the file that went in.
//
// # How the leak half is proved
//
// The sentinel, the sweep and the positive control are B4's, reused from
// init_status_leak_test.go — same package, same forms, and
// TestInitLeakSweep_FindsAPlantedSentinel proves the finder finds a planted
// secret before anything trusts an absence.
//
// The sentinel is planted in the catalog files themselves and the REAL
// handlers are driven through the REAL router. The body and every captured log
// line are then swept. Each case also asserts the SAFE address is present, so
// a handler that refused early fails loudly as "never reached the code under
// test" instead of passing as "no leak found".

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// catalogLeakTokenURL is the threat in its real shape, reusing B4's sentinel
// so the sweep already knows every form of it.
const catalogLeakTokenURL = initLeakRepoURL

// catalogLeakSafeURL is what SafeRepoURL leaves of it.
const catalogLeakSafeURL = initLeakRepoURLSafe

// catalogLeakExtraTokenURL is a SECOND tokenised address, for the extra chart
// source under additionalSources. It is the same sentinel in a different
// shape — the token as the whole userinfo, which is what url.URL.Redacted()
// hands straight back and what credsafe.SafeRepoURL exists to remove.
const catalogLeakExtraTokenURL = "https://" + initLeakSentinel + "@charts.example/org/extra"

// catalogLeakExtraSafeURL is what SafeRepoURL leaves of that one.
const catalogLeakExtraSafeURL = "https://charts.example/org/extra"

// v3CatalogWithToken is an addons-catalog.yaml whose one entry carries the
// sentinel in both its own repoURL and its extra source's.
const v3CatalogWithToken = `apiVersion: sharko.dev/v1
kind: AddonCatalog
metadata:
  name: addon-catalog
spec:
  applicationsets:
    - name: keda
      repoURL: ` + catalogLeakTokenURL + `
      chart: keda
      version: "2.13.0"
      namespace: keda
      additionalSources:
        - repoURL: ` + catalogLeakExtraTokenURL + `
          chart: keda-extras
          version: "1.0.0"
`

// v4CatalogWithToken is a catalog.yaml whose one entry carries the sentinel in
// its repository address, and whose extra source carries a second one.
//
// It is written out by hand rather than through config.SaveAddonCatalog,
// because that writer now refuses an address like this — see BF9. Refusing to
// WRITE one is a different question from what happens to one that is ALREADY
// in an operator's repository from before the rule existed, and this is the
// fixture for the second question: the file loads, Sharko keeps running, and
// nothing it says carries the address.
func v4CatalogWithToken(t *testing.T) []byte {
	t.Helper()
	body := `apiVersion: sharko.io/v1
kind: AddonCatalog
addons:
    keda:
        repoURL: ` + catalogLeakTokenURL + `
        chart: keda
        version: "2.13.0"
        namespace: keda
        additionalSources:
            - repoURL: ` + catalogLeakExtraTokenURL + `
              chart: keda-extras
              version: "1.0.0"
`
	// The fixture is only worth anything while it really carries the
	// sentinel, and only worth anything while it really parses.
	if !strings.Contains(body, initLeakSentinel) {
		t.Fatal("the v4 fixture does not carry the sentinel — every sweep built on it would pass for the wrong reason")
	}
	if _, err := config.LoadAddonCatalog([]byte(body)); err != nil {
		t.Fatalf("the v4 fixture does not parse, so the handler under test never sees an entry: %v", err)
	}
	return []byte(body)
}

// catalogLeakServer wires a server whose git provider serves the given files.
func catalogLeakServer(t *testing.T, files map[string][]byte) *Server {
	t.Helper()
	srv := newTestServer()
	srv.publishGitopsCfg(orchestrator.GitOpsConfig{BaseBranch: "main"})
	srv.connSvc.SetGitProviderOverride(&handlerFakeGitProvider{files: files})
	return srv
}

// catalogLeakGet drives one GET through the real router and returns the body
// and everything logged while it ran.
func catalogLeakGet(t *testing.T, srv *Server, path string) (body, logs string) {
	t.Helper()
	router := NewRouter(srv, nil)
	logs = captureSlog(t, func() {
		req := withRole(httptest.NewRequest(http.MethodGet, path, nil), "admin")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s expected 200 — anything else means the endpoint refused before the code under test ran. got %d, body %s",
				path, w.Code, w.Body.String())
		}
		body = w.Body.String()
	})
	return body, logs
}

// --- GET /addons/list, v3 branch --------------------------------------------

func TestAddonListLeak_V3_NeverShowsTheRepositoryToken(t *testing.T) {
	srv := catalogLeakServer(t, map[string][]byte{
		"configuration/addons-catalog.yaml": []byte(v3CatalogWithToken),
	})
	body, logs := catalogLeakGet(t, srv, "/api/v1/addons/list")

	assertNoInitLeak(t, "the GET /addons/list 200 body (v3 catalog)", body)
	assertNoInitLeak(t, "the log output for GET /addons/list (v3 catalog)", logs)

	entry := decodeOneListedAddon(t, body)
	if entry.RepoURL != catalogLeakSafeURL {
		t.Errorf("repoURL is\n  %q\nwant exactly\n  %q", entry.RepoURL, catalogLeakSafeURL)
	}
	if len(entry.AdditionalSources) != 1 {
		t.Fatalf("additionalSources came back with %d entries, want 1 — the nested field under test was never rendered",
			len(entry.AdditionalSources))
	}
	if entry.AdditionalSources[0].RepoURL != catalogLeakExtraSafeURL {
		t.Errorf("additionalSources[0].repoURL is\n  %q\nwant exactly\n  %q",
			entry.AdditionalSources[0].RepoURL, catalogLeakExtraSafeURL)
	}
	// The page is still useful: the chart is still named, so an operator can
	// still tell which addon this is.
	if entry.Chart != "keda" || entry.Version != "2.13.0" {
		t.Errorf("the entry lost its own identity: chart=%q version=%q", entry.Chart, entry.Version)
	}
}

// --- GET /addons/list, v4 branch --------------------------------------------

func TestAddonListLeak_V4_NeverShowsTheRepositoryToken(t *testing.T) {
	srv := catalogLeakServer(t, map[string][]byte{
		orchestrator.BootstrapRootAppPath: []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n"),
		config.AddonCatalogPath:           v4CatalogWithToken(t),
	})
	body, logs := catalogLeakGet(t, srv, "/api/v1/addons/list")

	assertNoInitLeak(t, "the GET /addons/list 200 body (v4 catalog)", body)
	assertNoInitLeak(t, "the log output for GET /addons/list (v4 catalog)", logs)

	entry := decodeOneListedAddon(t, body)
	if entry.RepoURL != catalogLeakSafeURL {
		t.Errorf("repoURL is\n  %q\nwant exactly\n  %q", entry.RepoURL, catalogLeakSafeURL)
	}
	if len(entry.AdditionalSources) != 1 ||
		entry.AdditionalSources[0].RepoURL != catalogLeakExtraSafeURL {
		t.Errorf("additionalSources is %+v, want one entry with repoURL %q",
			entry.AdditionalSources, catalogLeakExtraSafeURL)
	}
}

// decodeOneListedAddon decodes the list body and returns the single entry,
// failing when there is not exactly one — an empty list would make every
// absence above an absence of nothing.
func decodeOneListedAddon(t *testing.T, body string) models.AddonCatalogEntryView {
	t.Helper()
	var resp struct {
		ApplicationSets []models.AddonCatalogEntryView `json:"applicationsets"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decoding the addon list body: %v\n\n%s", err, body)
	}
	if len(resp.ApplicationSets) != 1 {
		t.Fatalf("the list came back with %d addons, want 1 — the catalog was never read and every absence above proves nothing.\n\nbody: %s",
			len(resp.ApplicationSets), body)
	}
	return resp.ApplicationSets[0]
}

// --- GET /catalog/addons and /catalog/addons/{name} --------------------------

func TestOrgCatalogLeak_NeverShowsTheRepositoryToken(t *testing.T) {
	srv := catalogLeakServer(t, map[string][]byte{
		config.AddonCatalogPath: v4CatalogWithToken(t),
	})

	t.Run("list", func(t *testing.T) {
		body, logs := catalogLeakGet(t, srv, "/api/v1/catalog/addons")
		assertNoInitLeak(t, "the GET /catalog/addons 200 body", body)
		assertNoInitLeak(t, "the log output for GET /catalog/addons", logs)

		var resp struct {
			Addons []catalog.CatalogAddon `json:"addons"`
			Total  int                    `json:"total"`
		}
		if err := json.Unmarshal([]byte(body), &resp); err != nil {
			t.Fatalf("decoding the org catalog body: %v\n\n%s", err, body)
		}
		if len(resp.Addons) != 1 {
			t.Fatalf("the org catalog came back with %d addons, want 1 — the file was never read.\n\nbody: %s",
				len(resp.Addons), body)
		}
		assertOrgCatalogAddonIsSafe(t, resp.Addons[0])
	})

	t.Run("one addon", func(t *testing.T) {
		body, logs := catalogLeakGet(t, srv, "/api/v1/catalog/addons/keda")
		assertNoInitLeak(t, "the GET /catalog/addons/{name} 200 body", body)
		assertNoInitLeak(t, "the log output for GET /catalog/addons/{name}", logs)

		var got catalog.CatalogAddon
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatalf("decoding the single-addon body: %v\n\n%s", err, body)
		}
		assertOrgCatalogAddonIsSafe(t, got)
	})
}

// assertOrgCatalogAddonIsSafe is the presence half: the repository is still
// named, with the credential gone. Blanking the field would pass every sweep
// and take the operator's answer with it.
func assertOrgCatalogAddonIsSafe(t *testing.T, got catalog.CatalogAddon) {
	t.Helper()
	if got.Name != "keda" {
		t.Fatalf("the addon came back as %q, want %q — the entry under test was never built", got.Name, "keda")
	}
	if got.RepoURL != catalogLeakSafeURL {
		t.Errorf("repo_url is\n  %q\nwant exactly\n  %q", got.RepoURL, catalogLeakSafeURL)
	}
	if len(got.AdditionalSources) != 1 {
		t.Fatalf("additional_sources came back with %d entries, want 1 — the nested field under test was never rendered",
			len(got.AdditionalSources))
	}
	if got.AdditionalSources[0].RepoURL != catalogLeakExtraSafeURL {
		t.Errorf("additional_sources[0].repoURL is\n  %q\nwant exactly\n  %q",
			got.AdditionalSources[0].RepoURL, catalogLeakExtraSafeURL)
	}
	// Not deployable, on purpose (BF9). The chart location is still NAMED —
	// the assertions above check that, so this is not the field being blanked
	// — but Sharko will not use an address written in this shape, and
	// deployable is the flag the rest of Sharko checks before enabling an
	// addon or migrating a repo. The field path says which field, and the
	// path is all it says.
	if got.Deployable {
		t.Error("the entry came back deployable, so Sharko would go on using an address it has refused to save")
	}
	if len(got.UnsupportedFields) == 0 {
		t.Error("nothing names the unusable field, so an operator has no way to know which one to fix")
	}
	for _, f := range got.UnsupportedFields {
		if strings.Contains(f, initLeakSentinel) {
			t.Errorf("unsupported_fields carries the sentinel: %q — it must name the field path and nothing else", f)
		}
	}
}

// --- the control on the control ---------------------------------------------

// TestCatalogLeakSweep_GoesRedWhenNothingIsPlanted proves the fixtures still
// carry the sentinel and that the finder finds it in a catalog-shaped body. If
// a fixture drifted, every assertion above would still pass having proved
// nothing.
func TestCatalogLeakSweep_GoesRedWhenNothingIsPlanted(t *testing.T) {
	for name, fixture := range map[string]string{
		"the v3 catalog file":     v3CatalogWithToken,
		"the entry's own repoURL": catalogLeakTokenURL,
		"the extra source's URL":  catalogLeakExtraTokenURL,
	} {
		if !strings.Contains(fixture, initLeakSentinel) {
			t.Errorf("%s no longer carries the sentinel — the sweep is proving nothing", name)
		}
	}
	if !strings.Contains(string(v4CatalogWithToken(t)), initLeakSentinel) {
		t.Error("the v4 catalog fixture no longer carries the sentinel — the sweep is proving nothing")
	}
	for name, safe := range map[string]string{
		"the safe entry URL":        catalogLeakSafeURL,
		"the safe extra-source URL": catalogLeakExtraSafeURL,
	} {
		if strings.Contains(safe, initLeakSentinel) {
			t.Errorf("%s carries the sentinel, so asserting its presence would also be asserting a leak", name)
		}
	}

	// A catalog-shaped body with nothing planted must come back clean.
	clean := `{"applicationsets":[{"name":"keda","repoURL":"` + catalogLeakSafeURL + `"}]}`
	if found := findInitLeak(clean); len(found) != 0 {
		t.Errorf("the sweep fired on a clean catalog body, naming %v", found)
	}
	// And the raw value planted in the same shape must come back dirty. This is
	// what break test (a) reproduces by serving the raw entry again.
	dirty := `{"applicationsets":[{"name":"keda","repoURL":"` + catalogLeakTokenURL + `"}]}`
	if found := findInitLeak(dirty); len(found) == 0 {
		t.Error("the sweep did NOT find the raw repository URL planted in a catalog body — it cannot prove anything about the real one")
	}
}
