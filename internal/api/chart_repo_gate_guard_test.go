package api

// chart_repo_gate_guard_test.go — the LIST of sites that route through
// writeChartRepoError, and the proof that no new one has appeared beside it
// (B13).
//
// The report named two response bodies. Asking the question its own reasoning
// implies — which OTHER responses are built from an error that could have come
// from an outbound HTTP call — turned two into ten. A list that only holds the
// two named ones would have passed on the day the leak was worst.
//
// So this guard reads the source of internal/api and fails BOTH ways:
//
//   - a call to an outbound chart/AI fetch whose error is formatted into a
//     response body without going through the gate, and
//   - a listed site that no longer exists, because a stale entry classifies
//     nothing.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// chartRepoRoutedSite is one place that hands an outbound-fetch error to the
// gate. file is relative to internal/api.
type chartRepoRoutedSite struct {
	file string
	// op is the exact chartRepoOp token expected in the source line, so a
	// site that routes through the gate under the WRONG operation — and would
	// therefore tell an operator the wrong step failed — is caught too.
	op string
	// why names the outbound call whose error lands here.
	why string
}

// chartRepoRoutedSites is the list. Ten entries: the two the report named and
// the eight found by widening.
var chartRepoRoutedSites = []chartRepoRoutedSite{
	// The two named in the report.
	{"addons_changelog.go", "chartRepoListVersions", "helm.Fetcher.ListVersions — <repo>/index.yaml"},
	{"upgrade.go", "chartRepoListVersions", "UpgradeService.ListVersions, which wraps the same helm fetch"},

	// The eight the report did not name.
	{"upgrade.go", "chartRepoUpgradeCheck", "UpgradeService.CheckUpgrade on POST /upgrade/check — downloads the current and target charts"},
	{"upgrade.go", "chartRepoUpgradeCheck", "the SAME CheckUpgrade call again on POST /upgrade/ai-summary, which runs the analysis before summarising it. Two sites, one op — the count check below is what caught this one missing"},
	{"upgrade.go", "chartRepoAISummary", "UpgradeService.GetAISummary — the configured AI provider, operator-supplied base URL"},
	{"upgrade.go", "chartRepoRecommendations", "UpgradeService.GetRecommendations — reads the repository's version list"},
	{"catalog_versions.go", "chartRepoListVersions", "helm.Fetcher.ListVersions on a catalog entry's stored repo"},
	{"values_editor.go", "chartRepoFetchValues", "helm.Fetcher.FetchValues — downloads the chart archive"},
	{"values_preview_merge.go", "chartRepoFetchValues", "helm.Fetcher.FetchValues"},
	{"ai_annotate.go", "chartRepoFetchValues", "helm.Fetcher.FetchValues"},
}

// apiSourceFiles returns every non-test .go file in internal/api, with its
// contents.
func apiSourceFiles(t *testing.T) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read internal/api: %v", err)
	}
	out := map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		out[name] = string(b)
	}
	if len(out) < 20 {
		t.Fatalf("only found %d source files in internal/api — the guard is not reading what it thinks it is", len(out))
	}
	return out
}

// TestChartRepoGate_EveryListedSiteStillRoutes fails when a listed site has
// gone away, and when it routes under a different operation than the list says.
func TestChartRepoGate_EveryListedSiteStillRoutes(t *testing.T) {
	src := apiSourceFiles(t)

	// Every listed file must exist and must carry the exact call.
	for _, site := range chartRepoRoutedSites {
		body, ok := src[site.file]
		if !ok {
			t.Errorf("the guard lists %s, which is no longer a source file in internal/api — a stale entry classifies nothing", site.file)
			continue
		}
		want := "writeChartRepoError(w, r, " + site.op + ","
		if !strings.Contains(body, want) {
			t.Errorf(`%s no longer routes %s through the gate.

The list says it should, because of: %s

Either the call moved (update this list) or somebody put the error's words back
into the body (put them back through writeChartRepoError).`, site.file, site.op, site.why)
		}
	}

	// And the total must match, so entries cannot be quietly dropped.
	total := 0
	for _, body := range src {
		total += strings.Count(body, "writeChartRepoError(w, r, ")
	}
	if total != len(chartRepoRoutedSites) {
		var files []string
		for name, body := range src {
			if n := strings.Count(body, "writeChartRepoError(w, r, "); n > 0 {
				files = append(files, name)
			}
		}
		sort.Strings(files)
		t.Errorf("internal/api makes %d calls to writeChartRepoError but the guard lists %d.\n\nfiles that call it: %v\n\nA new site is good news — add it to chartRepoRoutedSites. A missing one is the leak coming back.",
			total, len(chartRepoRoutedSites), files)
	}
}

// TestChartRepoGate_NoOutboundErrorIsFormattedIntoABody is the other direction:
// it re-reads the source for the exact shapes that were the bug, at the sites
// that make an outbound chart or AI fetch.
//
// It is a text check on Sharko's OWN source, not on an error's message — the
// ban on reading error text is about values that arrive at runtime, and a
// source file in this repository is neither.
func TestChartRepoGate_NoOutboundErrorIsFormattedIntoABody(t *testing.T) {
	src := apiSourceFiles(t)

	// The exact bodies B13 was about. Each one is a writeError carrying an
	// error's words on a path that had just made an outbound fetch.
	banned := []struct {
		fragment string
		why      string
	}{
		{`fmt.Sprintf("listing chart versions: %v", err)`, "B13's first named site — the *url.Error's text keeps a username-position token"},
		{`"failed to list versions: "+err.Error()`, "the same fetch, a different endpoint"},
		{`"fetching upstream values: "+ferr.Error()`, "the chart download's error text, on three endpoints"},
	}

	for name, body := range src {
		for _, b := range banned {
			if strings.Contains(body, b.fragment) {
				t.Errorf(`%s formats an outbound fetch's error into a response body again:

  %s

%s

Route it through writeChartRepoError instead: it classifies the SAME error the
same way (502 unreachable, 504 timed out, 429 rate-limited) and says a fixed
sentence, so the operator keeps the distinction and loses only the token.`,
					name, b.fragment, b.why)
			}
		}
	}
}
