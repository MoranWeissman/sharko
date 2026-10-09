package metrics

// contract_writers_test.go — every metric Sharko declares, and the code
// that puts a number in it.
//
// # The problem this exists to stop coming back
//
// A Prometheus metric can be declared and then never written to, and
// nothing complains. If it carries labels the damage is limited: a
// labelled collector with no children publishes no series at all, so a
// query returns no data and an operator can tell. If it carries NO
// labels, the damage is real: the series is published on every scrape at
// zero, and zero looks exactly like a measurement. Three of Sharko's
// metrics were in that state — sharko_active_sessions told anybody
// watching that nobody was signed in, on a server people were signed
// into.
//
// Counting the metrics does not catch it; the count is right either way.
// So this is a LIST. Every declared family is named here exactly once and
// classified one of two ways:
//
//	written    — the file that puts a number in it, and the Go identifier
//	             or helper that does it. The file must really exist and
//	             must really contain that identifier.
//	unwritten  — a deliberate, recorded gap, with the reason and where the
//	             decision lives.
//
// # Why it cannot go quietly stale
//
//	A NEW METRIC nobody writes to        fails: no entry in the list.
//	A METRIC REMOVED from the code       fails: the entry is now stale.
//	A WRITER MOVED OR RENAMED            fails: the named file no longer
//	                                     carries the identifier.
//	AN UNWRITTEN ONE QUIETLY WIRED UP    fails: the docs page and the list
//	                                     disagree about which are unwritten.
//	THE REGISTRY WALK BREAKING           fails: the vacuity checks below
//	                                     refuse to run on a short answer.
//
// The last one matters most. A check like this passes beautifully when it
// is looking at nothing at all, so it counts what it actually verified
// and fails when that number collapses.

import (
	"bytes"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// codeWithoutComments returns the Go source of path with every comment
// stripped out.
//
// This is not fussiness. The first run of this guard passed while the
// writer it names had been deleted, because the doc comment on the
// function next door still said the writer's name. A guard that a
// comment can satisfy is a guard that says nothing: comments are exactly
// where a name lingers after the code that used it has gone. Parsing
// without ParseComments and printing the tree back out leaves only what
// the compiler sees.
func codeWithoutComments(path string) (string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, file); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// writerRecord classifies one declared metric family. Exactly one of
// files or unwritten is set.
type writerRecord struct {
	// symbol is what the writer is spelled as in the source — the metric
	// variable, the helper that sets it, or (for an SLO family) the whole
	// helper call including the path constant, so that one family's writer
	// disappearing cannot be covered by a sibling family's writer still
	// being in the same file. Every file in files must contain it.
	symbol string
	// files are repo-relative NON-TEST Go files that put a number in this
	// metric. A metric written by two engines names both.
	files []string
	// unwritten is why this family carries no writer, in plain words. Set
	// instead of files, never alongside them.
	unwritten string
}

// followUp is the same sentence on every deliberately-unwritten entry, so
// the ten of them read as one recorded decision rather than ten separate
// shrugs. The warning box on docs/site/operator/metrics.md is where an
// operator meets the same fact.
const followUp = "labelled and never written, so it publishes no series at all; wiring it up or deleting it is recorded as open work in docs/site/operator/metrics.md"

// metricWriters is the list. Add a metric to the code, add it here.
var metricWriters = map[string]writerRecord{
	// --- written -----------------------------------------------------
	"sharko_catalog_entries_count": {
		symbol: "metrics.SetCatalogEntries",
		files:  []string{"internal/api/catalog_org.go"},
	},
	"sharko_active_sessions": {
		symbol: "metrics.SetActiveSessionsSource",
		files:  []string{"internal/api/router.go"},
	},
	"sharko_catalog_source_fetch_total": {
		symbol: "metrics.CatalogSourceFetchTotal",
		files:  []string{"internal/catalog/sources/fetcher.go"},
	},
	"sharko_catalog_source_last_success_timestamp": {
		symbol: "metrics.CatalogSourceLastSuccess",
		files:  []string{"internal/catalog/sources/fetcher.go"},
	},
	"sharko_catalog_source_entries": {
		symbol: "metrics.CatalogSourceEntries",
		files:  []string{"internal/catalog/sources/fetcher.go"},
	},
	"sharko_reconciler_runs_total": {
		symbol: "metrics.ReconcilerRuns",
		files:  []string{"internal/clusterreconciler/reconcile_status.go", "internal/secrets/reconciler.go"},
	},
	"sharko_reconciler_duration_seconds": {
		symbol: "metrics.ReconcilerDuration",
		files:  []string{"internal/clusterreconciler/reconcile_status.go", "internal/secrets/reconciler.go"},
	},
	"sharko_reconciler_last_run_timestamp": {
		symbol: "metrics.ReconcilerLastRun",
		files:  []string{"internal/clusterreconciler/reconcile_status.go", "internal/secrets/reconciler.go"},
	},
	"sharko_reconciler_last_success_timestamp": {
		symbol: "metrics.ReconcilerLastSuccess",
		files:  []string{"internal/clusterreconciler/reconcile_status.go", "internal/secrets/reconciler.go"},
	},
	"sharko_reconciler_items_checked_total": {
		symbol: "metrics.ReconcilerItemsChecked",
		files:  []string{"internal/clusterreconciler/reconcile_status.go", "internal/secrets/reconciler.go"},
	},
	"sharko_reconciler_items_changed_total": {
		symbol: "metrics.ReconcilerItemsChanged",
		files:  []string{"internal/clusterreconciler/reconcile_status.go", "internal/secrets/reconciler.go"},
	},
	"sharko_managed_secrets_state": {
		symbol: "metrics.ManagedSecretsState",
		files:  []string{"internal/clusterreconciler/reconcile_status.go", "internal/secrets/reconciler.go"},
	},
	"sharko_reconciler_item_failures_total": {
		symbol: "metrics.ReconcilerItemFailures",
		files:  []string{"internal/clusterreconciler/reconcile_status.go", "internal/secrets/reconciler.go"},
	},
	"sharko_reconciler_writes_total": {
		symbol: "metrics.ReconcilerWrites",
		files:  []string{"internal/clusterreconciler/reconcile_status.go", "internal/secrets/reconciler.go"},
	},
	"sharko_reconciler_fights": {
		symbol: "metrics.ReconcilerFights",
		files:  []string{"internal/clusterreconciler/reconcile_status.go", "internal/secrets/reconciler.go"},
	},
	"sharko_reconciler_enabled": {
		symbol: "metrics.ReconcilerEnabled",
		files:  []string{"internal/clusterreconciler/reconciler.go", "internal/secrets/reconciler.go"},
	},
	"sharko_api_requests_total": {
		symbol: "RecordHTTPRequest",
		files:  []string{"internal/metrics/middleware.go"},
	},
	"sharko_api_request_duration_seconds": {
		symbol: "RecordHTTPRequest",
		files:  []string{"internal/metrics/middleware.go"},
	},
	"sharko_scorecard_refresh_total": {
		symbol: "ScorecardMetricsAdapter",
		files:  []string{"cmd/sharko/serve.go"},
	},
	"sharko_scorecard_last_refresh_timestamp": {
		symbol: "ScorecardMetricsAdapter",
		files:  []string{"cmd/sharko/serve.go"},
	},
	"sharko_ai_annotate_total": {
		symbol: "metrics.AIAnnotateTotal",
		files:  []string{"internal/orchestrator/ai_annotate.go"},
	},
	"sharko_ai_annotate_latency_seconds": {
		symbol: "metrics.AIAnnotateLatencySeconds",
		files:  []string{"internal/orchestrator/ai_annotate.go"},
	},

	// --- written: the four SLO surfaces ------------------------------
	// A surface publishes THREE families and each one has its own writer:
	// metrics.Observe fills _duration_seconds, metrics.IncTotal fills
	// _total, metrics.IncError fills _errors_total. Naming only the path
	// constant here — which is what this list used to do — meant all three
	// entries were satisfied by any ONE of the three calls still being in
	// the file. Deleting the IncError branch left
	// sharko_cluster_registration_errors_total with no writer at all and
	// the whole metrics suite stayed green (proved by hand, B17). So the
	// identifier is the CALL, path constant included, and each family
	// names the call that really fills it.
	"sharko_cluster_registration_duration_seconds": {symbol: "metrics.Observe(metrics.PathClusterRegistration", files: []string{"internal/api/clusters_write.go"}},
	"sharko_cluster_registration_total":            {symbol: "metrics.IncTotal(metrics.PathClusterRegistration", files: []string{"internal/api/clusters_write.go"}},
	"sharko_cluster_registration_errors_total":     {symbol: "metrics.IncError(metrics.PathClusterRegistration", files: []string{"internal/api/clusters_write.go"}},
	"sharko_addon_cycle_duration_seconds":          {symbol: "metrics.Observe(metrics.PathAddonCycle", files: []string{"internal/api/addon_ops.go"}},
	"sharko_addon_cycle_total":                     {symbol: "metrics.IncTotal(metrics.PathAddonCycle", files: []string{"internal/api/addon_ops.go"}},
	"sharko_addon_cycle_errors_total":              {symbol: "metrics.IncError(metrics.PathAddonCycle", files: []string{"internal/api/addon_ops.go"}},
	"sharko_catalog_scan_duration_seconds":         {symbol: "metrics.Observe(metrics.PathCatalogScan", files: []string{"internal/api/addons.go"}},
	"sharko_catalog_scan_total":                    {symbol: "metrics.IncTotal(metrics.PathCatalogScan", files: []string{"internal/api/addons.go"}},
	"sharko_catalog_scan_errors_total":             {symbol: "metrics.IncError(metrics.PathCatalogScan", files: []string{"internal/api/addons.go"}},
	"sharko_dashboard_read_duration_seconds":       {symbol: "metrics.Observe(metrics.PathDashboardRead", files: []string{"internal/api/dashboard.go"}},
	"sharko_dashboard_read_total":                  {symbol: "metrics.IncTotal(metrics.PathDashboardRead", files: []string{"internal/api/dashboard.go"}},
	"sharko_dashboard_read_errors_total":           {symbol: "metrics.IncError(metrics.PathDashboardRead", files: []string{"internal/api/dashboard.go"}},

	// --- deliberately unwritten --------------------------------------
	// Ten declarations that describe things Sharko knows but has never
	// been wired to report. All ten carry labels, so none of them
	// publishes anything; a query against them returns no data rather
	// than a false number. That is why they were left alone when the
	// three unlabelled ones were dealt with in B5.
	"sharko_cluster_count":                      {unwritten: followUp},
	"sharko_cluster_status":                     {unwritten: followUp},
	"sharko_cluster_last_verified_timestamp":    {unwritten: followUp},
	"sharko_cluster_last_test_duration_seconds": {unwritten: followUp},
	"sharko_cluster_test_failures_total":        {unwritten: followUp},
	"sharko_addon_sync_status":                  {unwritten: followUp},
	"sharko_addon_health":                       {unwritten: followUp},
	"sharko_addon_version":                      {unwritten: followUp},
	"sharko_pr_tracked":                         {unwritten: followUp},
	"sharko_auth_login_total":                   {unwritten: followUp},
}

// declaredFamilies returns the name of every metric family in the two
// registries the /metrics handler serves, excluding the _bucket/_count/
// _sum series Prometheus derives from a histogram.
func declaredFamilies(t *testing.T) []string {
	t.Helper()
	var out []string
	for name, spec := range authoritativeSeries(t) {
		if spec.derived {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// TestEveryDeclaredMetricIsAccountedFor is the guard.
func TestEveryDeclaredMetricIsAccountedFor(t *testing.T) {
	root := repoRoot(t)
	families := declaredFamilies(t)

	// Vacuity check one: the registry walk has to have found the
	// registry. authoritativeSeries has its own floor; this one is about
	// THIS test having something to check.
	if len(families) < 40 {
		t.Fatalf("only %d metric families found — this guard is looking at an empty registry, not at a shrunken product", len(families))
	}

	declared := map[string]bool{}
	for _, f := range families {
		declared[f] = true
	}

	// A metric exists in the code and nobody said what writes it.
	for _, name := range families {
		rec, listed := metricWriters[name]
		if !listed {
			t.Errorf("UNACCOUNTED | %s is registered but is not in metricWriters. Add it, naming either the file that writes it or why it is deliberately left unwritten.", name)
			continue
		}
		if (len(rec.files) == 0) == (rec.unwritten == "") {
			t.Errorf("MALFORMED | %s must name writer files OR a reason it is unwritten, exactly one of the two", name)
		}
	}

	// A listed metric no longer exists.
	var stale []string
	for name := range metricWriters {
		if !declared[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	for _, name := range stale {
		t.Errorf("STALE | metricWriters lists %s, which is not registered any more. Delete the entry in the same change that deleted the metric.", name)
	}

	// Every named writer file really carries the identifier it claims.
	checkedWriters := 0
	for _, name := range families {
		rec := metricWriters[name]
		for _, rel := range rec.files {
			if strings.HasSuffix(rel, "_test.go") {
				t.Errorf("MALFORMED | %s names %s as its writer — a test is not a writer", name, rel)
				continue
			}
			code, err := codeWithoutComments(filepath.Join(root, rel))
			if err != nil {
				t.Errorf("STALE | %s names %s as its writer and that file cannot be read: %v", name, rel, err)
				continue
			}
			if !strings.Contains(code, rec.symbol) {
				t.Errorf("STALE | %s names %s as its writer, but %s does not appear in that file any more. The writer moved or was renamed — update the entry, or the metric is now unwritten.", name, rel, rec.symbol)
				continue
			}
			checkedWriters++
		}
	}

	// Vacuity check two: a run that verified no writer at all has proved
	// nothing, however green it looks.
	if checkedWriters < 20 {
		t.Errorf("this guard confirmed only %d writers — that is too few for it to be checking anything", checkedWriters)
	}
	// Vacuity check three: both halves of the list must be populated, so
	// a change that empties one of them is not a silent pass.
	written, unwritten := 0, 0
	for _, name := range families {
		if metricWriters[name].unwritten != "" {
			unwritten++
		} else {
			written++
		}
	}
	if written == 0 || unwritten == 0 {
		t.Errorf("the list has %d written and %d unwritten entries — one half is empty, so half this guard is not running", written, unwritten)
	}
}

// neverWrittenInDocs pulls the metric names out of the warning box on
// docs/site/operator/metrics.md that tells operators which metrics carry
// no number.
func neverWrittenInDocs(t *testing.T, root string) map[string]bool {
	t.Helper()
	path := filepath.Join(root, "docs", "site", "operator", "metrics.md")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	lines := strings.Split(string(body), "\n")
	start := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "!!! warning") && strings.Contains(l, "never written") {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("%s no longer has a `!!! warning` box about metrics that are never written. If the last one was wired up, delete the unwritten half of metricWriters in the same change.", path)
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "#") {
			end = i
			break
		}
	}

	names := map[string]bool{}
	re := regexp.MustCompile("`(sharko_[a-z0-9_]+)`")
	for _, m := range re.FindAllStringSubmatch(strings.Join(lines[start:end], "\n"), -1) {
		names[m[1]] = true
	}
	return names
}

// TestNeverWrittenWarningMatchesTheCode keeps the operator-facing warning
// and the list above from drifting apart. A metric that gets wired up and
// is still listed as dead scares people off a working signal; one that
// goes dead and is not listed is the false-zero problem all over again.
func TestNeverWrittenWarningMatchesTheCode(t *testing.T) {
	root := repoRoot(t)
	inDocs := neverWrittenInDocs(t, root)

	inCode := map[string]bool{}
	for name, rec := range metricWriters {
		if rec.unwritten != "" {
			inCode[name] = true
		}
	}
	if len(inCode) == 0 || len(inDocs) == 0 {
		t.Fatalf("nothing to compare: %d unwritten in code, %d named in the docs warning", len(inCode), len(inDocs))
	}

	for name := range inCode {
		if !inDocs[name] {
			t.Errorf("%s is listed as never written in metricWriters but the warning box in docs/site/operator/metrics.md does not name it", name)
		}
	}
	for name := range inDocs {
		if !inCode[name] {
			t.Errorf("docs/site/operator/metrics.md warns that %s is never written, but metricWriters says it has a writer. One of the two is out of date.", name)
		}
	}
}

// TestPRMergeDurationIsGone pins the B5 removal. The histogram was
// declared, never observed into, and unlabelled — so it published a
// confident zero on every scrape and a p95 of nothing. Sharko has no
// merged-at timestamp to build an honest one from (see the note where it
// used to be declared in metrics.go), so it was removed rather than
// faked. If it comes back, it comes back with a real end time.
func TestPRMergeDurationIsGone(t *testing.T) {
	const gone = "sharko_pr_merge_duration_seconds"
	series := authoritativeSeries(t)
	for _, suffix := range []string{"", "_bucket", "_count", "_sum"} {
		if _, found := series[gone+suffix]; found {
			t.Errorf("%s%s is registered again. It needs a real merge timestamp from the Git provider before it can publish a number that means anything.", gone, suffix)
		}
	}
}
