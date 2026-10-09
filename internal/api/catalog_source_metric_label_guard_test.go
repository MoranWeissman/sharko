package api

// catalog_source_metric_label_guard_test.go — a catalog source address
// never reaches a Prometheus label carrying a credential.
//
// # What this is about
//
// An operator points Sharko at extra addon catalogs with
// SHARKO_CATALOG_URLS, and a private catalog is addressed by writing a
// token into the address itself. The developer guide says so in as many
// words: the variable exists precisely so a tokened address need not be
// committed to Git. So a credential in that string is the expected shape,
// not an accident.
//
// GET /metrics needs no login. Anything that becomes a metric label is
// therefore readable by anyone who can reach the port. The JSON view of
// the same value already goes through credsafe, and the log lines beside
// the fetch already print a fingerprint instead of the address. The
// metric labels are the wire that was left bare.
//
// # Why the answer is one fixed word for EVERY address
//
// This guard used to accept a 10-character SHA-256 prefix of the address in
// place of the address. That was ruled out: the same address always produces
// the same prefix, and it is published where nobody has to log in, so anyone
// holding a list of candidate tokens can work out each prefix themselves and
// see which one Sharko is publishing. A later version still showed the
// address itself when the URL grammar could vouch it carried no credential.
// That allowance was withdrawn too: the documented private-catalog shape
// hides the token in the address's own PATH — /private/<token>/catalog.yaml
// — and no grammar can tell that apart from an ordinary path, so an address
// that looks clean can still be the key to someone's private catalog.
//
// So the label is now the one fixed word credsafe.RedactedSourceLabel for
// every configured source, clean-looking or not, and nothing derived from
// the address — no hash, no length, no partial, no mask whose width follows
// the address — is allowed on any surface.
//
// # Why this is a list and not a count
//
// A count is right whichever way the code moves, so it says nothing. This
// walks the tree, finds every place a catalog-source metric is given a
// label, and holds that set against a written list. A new place that
// nobody guarded fails. A listed place that no longer exists fails. A
// place that hands the metric the raw address instead of the shared
// helper fails, by name.
//
// And it refuses to pass on nothing: parsing no files at all is fatal,
// exercising no cases at all is fatal, and the number of label sites is
// compared for exact equality, never "at least".

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/MoranWeissman/sharko/internal/catalog/sources"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/credsafe"
	"github.com/MoranWeissman/sharko/internal/logging"
	"github.com/MoranWeissman/sharko/internal/metrics"
)

// ---------------------------------------------------------------------
// the list of label sites
// ---------------------------------------------------------------------

// catalogSourceLabelMetrics are the metric families whose first label is
// a catalog source address. Any call that hands one of these a label is a
// site this guard cares about, wherever in the tree it lives.
var catalogSourceLabelMetrics = map[string]bool{
	"CatalogSourceFetchTotal":  true,
	"CatalogSourceLastSuccess": true,
	"CatalogSourceEntries":     true,
}

// catalogLabelHelperCall is the one way a raw address is allowed to
// become label text, and catalogLabelArg is what every site must then
// hand the metric. Spelling the assignment out means a site cannot
// quietly reach past the helper.
const (
	catalogLabelHelperCall = "label := metricLabelForURL()"
	catalogLabelArg        = "label"
)

// catalogLabelSite is one place in the tree where a catalog source
// address becomes a metric label.
type catalogLabelSite struct {
	file   string // repo-relative
	fn     string // the function the call sits in
	metric string // the metric family being labelled
}

func (s catalogLabelSite) key() string {
	return s.file + " | " + s.fn + " | " + s.metric
}

// catalogSourceLabelSites is the list. Add a label site to the code, add
// it here — and the new site has to go through the shared helper or the
// guard names it.
var catalogSourceLabelSites = []catalogLabelSite{
	{file: "internal/catalog/sources/fetcher.go", fn: "recordSuccess", metric: "CatalogSourceFetchTotal"},
	{file: "internal/catalog/sources/fetcher.go", fn: "recordSuccess", metric: "CatalogSourceLastSuccess"},
	{file: "internal/catalog/sources/fetcher.go", fn: "recordSuccess", metric: "CatalogSourceEntries"},
	{file: "internal/catalog/sources/fetcher.go", fn: "recordFailure", metric: "CatalogSourceFetchTotal"},
	{file: "internal/catalog/sources/fetcher.go", fn: "recordSchemaFailure", metric: "CatalogSourceFetchTotal"},
}

// catalogSourceLabelSiteCount is how many there are, written out so the
// comparison below can be an exact one. "At least" would pass on growth,
// which is the thing this guard is for.
const catalogSourceLabelSiteCount = 5

// foundLabelSite is a site as the tree actually has it.
type foundLabelSite struct {
	catalogLabelSite
	line    int
	arg     string // the label argument, printed back from the tree
	helper  bool   // the enclosing function assigns label from the helper
	rawFile string
}

// repoRootForMetricLabelGuard walks up from the working directory to the
// directory holding go.mod.
func repoRootForMetricLabelGuard(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 12; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not find go.mod walking up from the working directory")
	return ""
}

// metricNameOfLabelCall returns the metric family a WithLabelValues call
// is being made on, and whether it is one this guard watches. It reads
// both the qualified form (metrics.CatalogSourceEntries) and the bare one
// (CatalogSourceEntries), so moving the call into package metrics does
// not lose it.
func metricNameOfLabelCall(call *ast.CallExpr) (string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "WithLabelValues" {
		return "", false
	}
	var name string
	switch recv := sel.X.(type) {
	case *ast.SelectorExpr:
		name = recv.Sel.Name
	case *ast.Ident:
		name = recv.Name
	default:
		return "", false
	}
	if !catalogSourceLabelMetrics[name] {
		return "", false
	}
	return name, true
}

// printNode prints one piece of the tree back as source.
func printNode(fset *token.FileSet, n ast.Node) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, n); err != nil {
		return "<unprintable>"
	}
	return buf.String()
}

// walkCatalogLabelSites parses every non-test Go file under root and
// returns every catalog-source label site in it, plus how many files it
// actually read.
//
// The files are parsed WITHOUT comments and printed back from the tree,
// so a name that only survives in a doc comment cannot satisfy anything
// here.
func walkCatalogLabelSites(t *testing.T, root string) ([]foundLabelSite, int) {
	t.Helper()

	skipDir := map[string]bool{
		".git": true, "node_modules": true, "ui": true,
		"_dist": true, ".worktrees": true, "vendor": true,
	}

	var found []foundLabelSite
	filesParsed := 0

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && skipDir[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			t.Errorf("cannot parse %s: %v", path, perr)
			return nil
		}
		filesParsed++

		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)

		// Where each function starts and ends, so a call can be told
		// which function it sits in.
		type fnSpan struct {
			name       string
			start, end token.Pos
			body       string
		}
		var spans []fnSpan
		for _, decl := range file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			spans = append(spans, fnSpan{
				name:  fd.Name.Name,
				start: fd.Pos(),
				end:   fd.End(),
				body:  printNode(fset, fd.Body),
			})
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			metric, watched := metricNameOfLabelCall(call)
			if !watched {
				return true
			}
			fn, body := "<file scope>", ""
			for _, s := range spans {
				if call.Pos() >= s.start && call.Pos() < s.end {
					fn, body = s.name, s.body
					break
				}
			}
			arg := "<no argument>"
			if len(call.Args) > 0 {
				arg = printNode(fset, call.Args[0])
			}
			found = append(found, foundLabelSite{
				catalogLabelSite: catalogLabelSite{file: rel, fn: fn, metric: metric},
				line:             fset.Position(call.Pos()).Line,
				arg:              arg,
				helper:           strings.Contains(body, catalogLabelHelperCall),
				rawFile:          path,
			})
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	return found, filesParsed
}

// TestCatalogSourceMetricLabelSitesAllGoThroughTheHelper is the static
// half of the guard: every place in the tree that labels a catalog-source
// metric, held against the written list.
func TestCatalogSourceMetricLabelSitesAllGoThroughTheHelper(t *testing.T) {
	root := repoRootForMetricLabelGuard(t)
	found, filesParsed := walkCatalogLabelSites(t, root)

	// Nothing read means nothing checked, however green it looks.
	if filesParsed == 0 {
		t.Fatalf("this guard parsed no Go files at all — it is looking at an empty tree, not at a product with no label sites")
	}
	if len(catalogSourceLabelSites) != catalogSourceLabelSiteCount {
		t.Fatalf("the written list holds %d label sites but says there are %d — the two have to agree before anything below means anything", len(catalogSourceLabelSites), catalogSourceLabelSiteCount)
	}
	if len(found) == 0 {
		t.Fatalf("this guard found no catalog-source label site anywhere in the tree, having read %d files. Either the walk is broken or the metrics were renamed; both need looking at before this can pass.", filesParsed)
	}

	// Growth and shrinkage, said plainly and exactly.
	if len(found) != catalogSourceLabelSiteCount {
		var names []string
		for _, f := range found {
			names = append(names, f.key())
		}
		sort.Strings(names)
		t.Errorf("COUNT | the tree has %d catalog-source label sites, the list has %d. Every one of them has to go through metricLabelForURL and be written down here. Found:\n  %s", len(found), catalogSourceLabelSiteCount, strings.Join(names, "\n  "))
	}

	expected := map[string]int{}
	for _, s := range catalogSourceLabelSites {
		expected[s.key()]++
	}
	actual := map[string]int{}
	for _, f := range found {
		actual[f.key()]++
	}

	for key, n := range actual {
		if expected[key] != n {
			t.Errorf("UNGUARDED | %s appears %d time(s) in the tree and %d time(s) in catalogSourceLabelSites. A new place that labels a catalog-source metric has to be added to the list in the same change that adds it to the code.", key, n, expected[key])
		}
	}
	var stale []string
	for key := range expected {
		if actual[key] == 0 {
			stale = append(stale, key)
		}
	}
	sort.Strings(stale)
	for _, key := range stale {
		t.Errorf("STALE | catalogSourceLabelSites lists %s, which is not in the tree any more. Delete the entry in the same change that deleted the call.", key)
	}

	// The point of the whole thing: what each site actually hands over.
	checked := 0
	for _, f := range found {
		if f.arg != catalogLabelArg {
			t.Errorf("RAW LABEL | %s:%d hands %s to %s. The only value allowed in that label is %q, taken from the shared helper — an address the classifier will not vouch for must never appear in a label in any form.", f.file, f.line, f.arg, f.metric, catalogLabelArg)
			continue
		}
		if !f.helper {
			t.Errorf("RAW LABEL | %s:%d hands %q to %s, but %s does not contain %q. The name alone proves nothing; the value has to come from the shared helper.", f.file, f.line, catalogLabelArg, f.metric, f.fn, catalogLabelHelperCall)
			continue
		}
		checked++
	}
	if checked != catalogSourceLabelSiteCount {
		t.Errorf("this guard confirmed %d label sites, not %d — a run that verified fewer than all of them has proved nothing about the rest", checked, catalogSourceLabelSiteCount)
	}
}

// ---------------------------------------------------------------------
// the real scrape
// ---------------------------------------------------------------------

// guardValidCatalogYAML is the smallest body the catalog loader accepts,
// so a fetch of it lands on the success path and writes all three of the
// success-path labels.
const guardValidCatalogYAML = `addons:
  - name: guard-one
    description: Addon one for the metric label guard.
    chart: guard-one
    repo: https://charts.example.com
    default_namespace: guard-one
    license: Apache-2.0
    category: observability
    curated_by: [cncf-sandbox]
    maintainers: [test@example.com]
  - name: guard-two
    description: Addon two for the metric label guard.
    chart: guard-two
    repo: https://charts.example.com
    default_namespace: guard-two
    license: MIT
    category: security
    curated_by: [cncf-incubating]
    maintainers: [test@example.com]
`

// guardInvalidCatalogYAML is missing the required chart field, so a fetch
// of it lands on the schema-failure path.
const guardInvalidCatalogYAML = `addons:
  - name: guard-broken
    description: Missing a required field on purpose.
    repo: https://charts.example.com
    default_namespace: guard-broken
    license: MIT
    category: security
    curated_by: [cncf-sandbox]
    maintainers: [test@example.com]
`

// hasSeries reports whether the scrape body publishes the named metric
// with the given url label.
func hasSeries(body, metric, label string) bool {
	needle := `url="` + label + `"`
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, metric+"{") && strings.Contains(line, needle) {
			return true
		}
	}
	return false
}

// scrapeMetrics drives the real GET /metrics handler and returns the body.
func scrapeMetrics(t *testing.T) string {
	t.Helper()
	rec := httptest.NewRecorder()
	metricsHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /metrics answered %d, so there is nothing to read", rec.Code)
	}
	return rec.Body.String()
}

// ---------------------------------------------------------------------
// what a leak would look like, in every form it could take
// ---------------------------------------------------------------------

// leakForm is one shape the synthetic credential could take if it escaped.
// Checking only the raw text would miss a re-encoded copy, and checking
// only the raw text is how the hashed label passed review the first time.
type leakForm struct {
	what string
	text string
}

// credentialLeakForms is every shape this test refuses to find. The raw
// credential, two ordinary re-encodings of it, and — because the ruling is
// specifically about a value COMPUTED from the address — a SHA-256 of the
// credential and of the whole address, the 10-character prefix that used to
// be the label, and a plain middle chunk of the credential.
func credentialLeakForms(secret, rawURL string) []leakForm {
	secretSum := sha256.Sum256([]byte(secret))
	secretHex := hex.EncodeToString(secretSum[:])
	urlSum := sha256.Sum256([]byte(rawURL))
	urlHex := hex.EncodeToString(urlSum[:])

	part := secret
	if len(part) > 16 {
		part = part[4 : len(part)-4]
	}

	forms := []leakForm{
		{"the credential exactly as it was written", secret},
		{"the credential percent-encoded", url.QueryEscape(secret)},
		{"the credential base64-encoded", base64.StdEncoding.EncodeToString([]byte(secret))},
		{"a full SHA-256 of the credential", secretHex},
		{"the first ten characters of that SHA-256", secretHex[:10]},
		{"a middle chunk of the credential", part},
		{"a full SHA-256 of the whole address", urlHex},
		{"the first ten characters of the address SHA-256 — the label this fix removed", urlHex[:10]},
	}
	return forms
}

// credentialLeakFormCount is written out so a run that checked fewer forms
// than it was meant to says so instead of passing.
const credentialLeakFormCount = 8

// assertNoLeak checks every form against one body of text and reports how
// many it actually compared.
func assertNoLeak(t *testing.T, where, caseName, body, secret, rawURL string) (formsChecked int, leaked bool) {
	t.Helper()
	forms := credentialLeakForms(secret, rawURL)
	if len(forms) != credentialLeakFormCount {
		t.Fatalf("this check is meant to compare %d forms of the credential and is comparing %d", credentialLeakFormCount, len(forms))
	}
	for _, f := range forms {
		if f.text == "" {
			t.Fatalf("the %s form came out empty, so comparing it would find nothing whatever the code did", f.what)
		}
		if strings.Contains(body, f.text) {
			leaked = true
			t.Errorf("LEAK | %s: %s carries %s. That surface needs no login, so it is readable by anyone who can reach it.", caseName, where, f.what)
		}
	}
	return len(forms), leaked
}

// ---------------------------------------------------------------------
// reading the scrape
// ---------------------------------------------------------------------

// seriesValue returns the value published for one metric under one url
// label (and, when status is non-empty, one status), plus how many lines
// matched. More than one match on the same label set would mean the reading
// below is ambiguous, so the count is returned rather than hidden.
func seriesValue(body, metric, label, status string) (float64, int) {
	var (
		val     float64
		matches int
	)
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, metric+"{") {
			continue
		}
		if !strings.Contains(line, `url="`+label+`"`) {
			continue
		}
		if status != "" && !strings.Contains(line, `status="`+status+`"`) {
			continue
		}
		sp := strings.LastIndex(line, " ")
		if sp < 0 {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(line[sp+1:]), 64)
		if err != nil {
			continue
		}
		val = v
		matches++
	}
	return val, matches
}

// ---------------------------------------------------------------------
// the addresses these tests drive
// ---------------------------------------------------------------------

// The synthetic credentials. Nothing here is real, and nothing here is
// derived from anything real. They carry "+" and "=" so the percent-encoded
// form of each is genuinely a different string from the raw one.
const (
	secretUserinfo    = "bf14-userinfo-synthetic+not=a=real=token"
	secretQuery       = "bf14-query-synthetic+not=a=real=token"
	secretFragment    = "bf14-fragment-synthetic+not=a=real=token"
	secretUnreadPort  = "bf14-unreadable-port-synthetic+not=a=real=token"
	secretUnreadColon = "bf14-unreadable-colon-synthetic+not=a=real=token"
)

// unreadableByPort has a port that is not a number, so the grammar cannot
// finish reading the address and nothing about it may be shown.
func unreadableByPort(secret string) string {
	return "https://x-access-token:" + secret + "@catalog.example:notaport/private.yaml"
}

// unreadableByMissingColon is the shape BF13-6's guard is about — the colon
// after the scheme is missing, so what looks like a host is really a path
// segment. It lives in both case lists on purpose, so the two guards defend
// each other: if either package ever starts reading this shape, both say so.
func unreadableByMissingColon(secret string) string {
	return "https//x-access-token:" + secret + "@catalog.example/private.yaml"
}

// requireStillUnreadable asserts for itself that credsafe still refuses the
// address, so a case cannot quietly turn into a different case.
func requireStillUnreadable(t *testing.T, rawURL string) {
	t.Helper()
	if safe := credsafe.SafeRepoURL(rawURL); safe != "" {
		t.Fatalf("this case is no longer unreadable: credsafe now reads %q as %q. Pick a shape credsafe still refuses, or drop the case.", rawURL, safe)
	}
}

// credentialShape is one way an operator's private catalog address can
// carry a credential.
type credentialShape struct {
	name string
	// url is the address as SHARKO_CATALOG_URLS would carry it.
	url string
	// secret is the part of it that must never leave the process.
	secret string
}

// TestMetricsScrapeCarriesNoCatalogCredential drives the real fetcher over
// six addresses — five that carry a credential somewhere and one that is
// completely clean — then reads the real /metrics handler and checks the
// body.
//
// It proves it can see what it is looking for before it claims anything is
// absent: a plain address is planted straight into the metric first and has
// to come back in the scrape. Then the whole run has to show up as a DELTA
// on the shared "redacted" series — the counters are process-global, so an
// absolute reading would be somebody else's test, and a run that moved no
// counter would make its own absence claims meaningless.
//
// The clean address is the case that pins the withdrawn allowance
// (acceptance item 5): an earlier version of the code showed an address on
// the label when the URL grammar vouched it carried no credential. The
// documented private-catalog shape hides the token in the PATH, where no
// grammar can spot it — so a clean-looking address must come out as the
// fixed word too. If it ever appears in the scrape, the by-type rule was
// not applied.
func TestMetricsScrapeCarriesNoCatalogCredential(t *testing.T) {
	// Positive control, checked before anything is claimed absent. If a
	// plain catalog address planted straight into the metric does not come
	// back in the scrape, this probe cannot tell present from absent.
	const controlLabel = "http://bf14-positive-control.example/plain-catalog.yaml"
	metrics.CatalogSourceFetchTotal.WithLabelValues(controlLabel, "failed").Inc()
	if body := scrapeMetrics(t); !hasSeries(body, "sharko_catalog_source_fetch_total", controlLabel) {
		t.Fatalf("POSITIVE CONTROL FAILED | the scrape does not carry the plain catalog address %q that was just written to the metric, so this test cannot tell present from absent", controlLabel)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		switch {
		case strings.HasPrefix(r.URL.Path, "/ok/"):
			_, _ = w.Write([]byte(guardValidCatalogYAML))
		case strings.HasPrefix(r.URL.Path, "/bad/"):
			_, _ = w.Write([]byte(guardInvalidCatalogYAML))
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	// The two shapes the URL grammar cannot read at all. The test asserts
	// that for itself, so if credsafe ever learns to read one of them the
	// case is retired here rather than quietly weakening.
	portURL := unreadableByPort(secretUnreadPort)
	colonURL := unreadableByMissingColon(secretUnreadColon)
	requireStillUnreadable(t, portURL)
	requireStillUnreadable(t, colonURL)

	cases := []credentialShape{
		{
			name:   "credential in the userinfo",
			url:    "http://x-access-token:" + secretUserinfo + "@" + strings.TrimPrefix(srv.URL, "http://") + "/ok/catalog.yaml",
			secret: secretUserinfo,
		},
		{
			name:   "credential in the query string",
			url:    srv.URL + "/bad/catalog.yaml?access_token=" + secretQuery,
			secret: secretQuery,
		},
		{
			name:   "credential in the fragment",
			url:    srv.URL + "/boom/catalog.yaml#" + secretFragment,
			secret: secretFragment,
		},
		{
			name:   "an address the grammar will not read — the port is not a number",
			url:    portURL,
			secret: secretUnreadPort,
		},
		{
			name:   "an address the grammar will not read — the colon after the scheme is missing",
			url:    colonURL,
			secret: secretUnreadColon,
		},
	}
	if len(cases) != 5 {
		t.Fatalf("this test is meant to drive five credential shapes and is driving %d", len(cases))
	}

	// The withdrawn-allowance pin: no credential anywhere in this address.
	// The old grammar check would have vouched for it and shown it; the
	// by-type rule must not.
	cleanURL := srv.URL + "/ok/bf14-clean-catalog.yaml"
	if safe := credsafe.SafeRepoURL(cleanURL); safe == "" {
		t.Fatalf("the clean address %q is one the grammar refuses, so it cannot pin the withdrawn allowance — pick a shape SafeRepoURL vouches for", cleanURL)
	}

	allURLs := make([]string, 0, len(cases)+1)
	for _, c := range cases {
		allURLs = append(allURLs, c.url)
	}
	allURLs = append(allURLs, cleanURL)

	// The counters are process-global and other tests in this package
	// write to the same shared series, so the whole reading below is a
	// before-and-after delta, never an absolute.
	const metric = "sharko_catalog_source_fetch_total"
	beforeBody := scrapeMetrics(t)
	okBefore, _ := seriesValue(beforeBody, metric, credsafe.RedactedSourceLabel, "ok")
	failedBefore, _ := seriesValue(beforeBody, metric, credsafe.RedactedSourceLabel, "failed")

	srcs := make([]config.CatalogSource, 0, len(allURLs))
	for _, u := range allURLs {
		srcs = append(srcs, config.CatalogSource{URL: u})
	}
	fetcher := sources.NewFetcher(&config.CatalogSourcesConfig{
		Sources:         srcs,
		RefreshInterval: config.MinRefreshInterval,
		// The addresses point at a loopback test server, so the SSRF
		// guard would refuse them before the fetch ever happened.
		AllowPrivate: true,
	}, nil, nil)
	fetcher.ForceRefresh(t.Context())

	body := scrapeMetrics(t)

	// Non-vacuity first: the run has to have moved the shared line, and by
	// exactly the amount six fetches produce — two successes (the userinfo
	// address and the clean address both serve valid YAML) and four
	// failures (schema violation, HTTP 500, and the two unreadable
	// shapes). A wrong delta means some fetch never reached the metric,
	// and every absence claim below would be about a run that did not
	// happen.
	okAfter, okLines := seriesValue(body, metric, credsafe.RedactedSourceLabel, "ok")
	failedAfter, failedLines := seriesValue(body, metric, credsafe.RedactedSourceLabel, "failed")
	if okLines != 1 || failedLines != 1 {
		t.Fatalf("the scrape publishes %d ok line(s) and %d failed line(s) under url=%q, want exactly 1 of each — the fixed word means every configured source shares one line per status", okLines, failedLines, credsafe.RedactedSourceLabel)
	}
	if got := okAfter - okBefore; got != 2 {
		t.Errorf("NOT EXERCISED | six sources were fetched and two should have succeeded, but the shared ok line moved by %v (%v to %v)", got, okBefore, okAfter)
	}
	if got := failedAfter - failedBefore; got != 4 {
		t.Errorf("NOT EXERCISED | six sources were fetched and four should have failed, but the shared failed line moved by %v (%v to %v)", got, failedBefore, failedAfter)
	}

	// The credential, in every form it could take, is absent for every case.
	for _, c := range cases {
		formsChecked, _ := assertNoLeak(t, "the scrape of GET /metrics", c.name, body, c.secret, c.url)
		if formsChecked != credentialLeakFormCount {
			t.Errorf("%s: only %d forms were compared", c.name, formsChecked)
		}
	}

	// The addresses themselves — clean one included — never appear. This
	// is acceptance item 5: a clean address in the scrape means the
	// grammar question came back.
	for _, u := range allURLs {
		if strings.Contains(body, u) {
			t.Errorf("ADDRESS ON THE SCRAPE | the configured address %q appears in GET /metrics. The address is sensitive by type — even one with no credential in it must come out as %q.", u, credsafe.RedactedSourceLabel)
		}
	}
	if safe := credsafe.SafeRepoURL(cleanURL); safe != "" && safe != cleanURL && strings.Contains(body, safe) {
		t.Errorf("ADDRESS ON THE SCRAPE | the grammar-vouched form %q of the clean address appears in GET /metrics — the withdrawn allowance is back", safe)
	}

	// The success path writes three labels, not one. The two gauges must
	// carry the shared redacted line (the successes wrote them) and no
	// line for any configured address.
	for _, m := range []string{"sharko_catalog_source_last_success_timestamp", "sharko_catalog_source_entries"} {
		if !hasSeries(body, m, credsafe.RedactedSourceLabel) {
			t.Errorf("NOT EXERCISED | %s never published the shared %q line, so the success-path label sites were not read live", m, credsafe.RedactedSourceLabel)
		}
		for _, u := range allURLs {
			if hasSeries(body, m, u) {
				t.Errorf("ADDRESS ON THE SCRAPE | %s carries a line labelled with the configured address %q", m, u)
			}
		}
	}
}

// ---------------------------------------------------------------------
// the captured log sink
// ---------------------------------------------------------------------

// logSink is a writer a slog handler can be pointed at while more than one
// fetch goroutine is writing to it.
type logSink struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *logSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *logSink) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// captureFetcherLogs points the default logger at a sink THROUGH THE REAL
// PRODUCTION HANDLER CHAIN — logging.NewHandler, the one `sharko serve`
// and the CLI actually install — builds a fetcher (which takes its logger
// from the default at construction), runs one refresh over the given
// addresses, and hands back everything logged.
//
// The handler choice is the whole point. The fetcher hands its fetch
// errors to slog as error VALUES, and it is logging.NewHandler's
// redaction pass — which replaces an error's words with a type-built
// class — that keeps an address inside net/url's and net/http's own error
// text off the log. A capture through a bare slog.NewJSONHandler would be
// measuring a pipeline nothing installs, and everything read out of it
// would prove nothing about the shipping one.
//
// It plants a sentinel line first and refuses to return until that line has
// come back, so a caller can never read an empty sink as "nothing leaked".
func captureFetcherLogs(t *testing.T, urls []string) string {
	t.Helper()

	const sentinel = "bf14-log-positive-control-sentinel"

	sink := &logSink{}
	previous := slog.Default()
	slog.SetDefault(slog.New(logging.NewHandler(sink, slog.LevelDebug)))
	defer slog.SetDefault(previous)

	slog.Default().Info("planted so this probe can prove it sees log lines", "marker", sentinel)
	if !strings.Contains(sink.String(), sentinel) {
		t.Fatalf("POSITIVE CONTROL FAILED | a line written straight to the captured log sink did not come back out of it, so this probe cannot tell a logged credential from a quiet run")
	}

	srcs := make([]config.CatalogSource, 0, len(urls))
	for _, u := range urls {
		srcs = append(srcs, config.CatalogSource{URL: u})
	}
	fetcher := sources.NewFetcher(&config.CatalogSourcesConfig{
		Sources:         srcs,
		RefreshInterval: config.MinRefreshInterval,
		AllowPrivate:    true,
	}, nil, nil)
	fetcher.ForceRefresh(t.Context())

	out := sink.String()
	if !strings.Contains(out, "catalog source") {
		t.Fatalf("NOT EXERCISED | the captured sink holds no catalog-source log line at all, so nothing below would prove anything. Sink held:\n%s", out)
	}
	return out
}

// TestCatalogFetchLogsCarryNoCatalogCredential is the log half of the same
// question the scrape probe asks. The ruling named the log path explicitly:
// it is the same source-identity problem, on a different surface.
func TestCatalogFetchLogsCarryNoCatalogCredential(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		switch {
		case strings.HasPrefix(r.URL.Path, "/bad/"):
			_, _ = w.Write([]byte(guardInvalidCatalogYAML))
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	portURL := unreadableByPort(secretUnreadPort)
	colonURL := unreadableByMissingColon(secretUnreadColon)
	requireStillUnreadable(t, portURL)
	requireStillUnreadable(t, colonURL)

	// A dead port on loopback, so the failure is a real dial error rather
	// than an HTTP status — that is the path where net/http builds an error
	// out of the address itself.
	deadURL := "http://x-access-token:" + secretUserinfo + "@127.0.0.1:1/dead-catalog.yaml"

	cases := []credentialShape{
		{name: "a dial failure on an address with the credential in the userinfo", url: deadURL, secret: secretUserinfo},
		{name: "a schema failure on an address with the credential in the query string", url: srv.URL + "/bad/catalog.yaml?access_token=" + secretQuery, secret: secretQuery},
		{name: "an address whose port is not a number", url: portURL, secret: secretUnreadPort},
		{name: "an address missing the colon after its scheme", url: colonURL, secret: secretUnreadColon},
	}
	if len(cases) != 4 {
		t.Fatalf("this probe is meant to drive four addresses and is driving %d", len(cases))
	}

	urls := make([]string, 0, len(cases))
	for _, c := range cases {
		urls = append(urls, c.url)
	}
	out := captureFetcherLogs(t, urls)

	// The fetches have to have reached a log line at all, or the absence
	// checks say nothing.
	if !strings.Contains(out, `"source":"`+credsafe.RedactedSourceLabel+`"`) {
		t.Errorf("NOT EXERCISED | no log line carried source=%q, so the redacted log path was never taken. Sink held:\n%s", credsafe.RedactedSourceLabel, out)
	}

	// The dial failure is the case the central sink exists for: net/http
	// builds its error text out of the address itself, credential and
	// all, and the fetcher hands that error to slog as a VALUE for
	// logging.NewHandler to replace by type. Two things prove the sink
	// actually ran on it, rather than the line quietly not being written:
	// the fetch-failed line is in the sink, and its err field carries the
	// type-built class (a "chain=" description), not the error's words.
	if !strings.Contains(out, "catalog source fetch failed") {
		t.Fatalf("NOT EXERCISED | the dial failure never produced a fetch-failed log line, so nothing here measured the sink. Sink held:\n%s", out)
	}
	if !strings.Contains(out, "chain=") {
		t.Errorf("SINK DID NOT RUN | no err field carries the type-built class the real sink writes in place of an error's words — the capture is not going through logging.NewHandler's redaction. Sink held:\n%s", out)
	}

	for _, c := range cases {
		if forms, _ := assertNoLeak(t, "a captured log sink", c.name, out, c.secret, c.url); forms != credentialLeakFormCount {
			t.Errorf("%s: only %d forms were compared against the log sink", c.name, forms)
		}
		// The address itself — not just the credential inside it — must be
		// absent. The address is sensitive by type, whole.
		if strings.Contains(out, c.url) {
			t.Errorf("ADDRESS IN THE LOG | %s: the configured address appears in the captured sink", c.name)
		}
	}
}

// ---------------------------------------------------------------------
// two addresses that differ only in their credential
// ---------------------------------------------------------------------

// requestIDNoise matches the per-tick request id, which is built from the
// clock and so differs between two runs for reasons that have nothing to do
// with the address being fetched.
var requestIDNoise = regexp.MustCompile(`"request_id":"[^"]*"`)

// TestTwoAddressesDifferingOnlyInTheCredentialLookIdentical is the point of
// the whole fix said as one sentence: what Sharko publishes must not change
// when only the secret changes. If it did, the published value would be a
// way of telling one secret from another.
func TestTwoAddressesDifferingOnlyInTheCredentialLookIdentical(t *testing.T) {
	const (
		secretA = "bf14-pair-first-synthetic+not=a=real=token"
		secretB = "bf14-pair-second-synthetic+not=a=real=token"
	)
	if secretA == secretB {
		t.Fatalf("the two credentials are the same string, so this test would pass whatever the code did")
	}

	pairs := []struct {
		what string
		a, b string
	}{
		{"an address whose port is not a number", unreadableByPort(secretA), unreadableByPort(secretB)},
		{"an address missing the colon after its scheme", unreadableByMissingColon(secretA), unreadableByMissingColon(secretB)},
		{
			"an ordinary address with the credential in the query string",
			"http://127.0.0.1:9/pair-catalog.yaml?access_token=" + secretA,
			"http://127.0.0.1:9/pair-catalog.yaml?access_token=" + secretB,
		},
		{
			// The documented private-catalog shape: the token IS a path
			// segment. This pair is the load-bearing one. The central log
			// sink independently blanks a credential it can SPOT — a
			// userinfo section, a query string — so the three pairs above
			// would come out identical even if the source field carried
			// the raw address. A path token looks like an ordinary path,
			// the sink leaves it alone, and only the fixed-word rule keeps
			// the two runs identical. A regression that puts the address
			// back on the source field is caught HERE and nowhere above.
			"the documented shape — the credential is a path segment",
			"https://catalogs.example/private/" + secretA + "/catalog.yaml",
			"https://catalogs.example/private/" + secretB + "/catalog.yaml",
		},
	}
	if len(pairs) != 4 {
		t.Fatalf("this test is meant to compare four pairs and is comparing %d", len(pairs))
	}

	// The label. PublicSourceLabel no longer even takes the address — the
	// signature is the proof that nothing published can vary with it. What
	// is left to check is that the one answer really is the fixed word and
	// carries nothing.
	if got := credsafe.PublicSourceLabel(); got != credsafe.RedactedSourceLabel {
		t.Errorf("PublicSourceLabel() = %q, want the fixed word %q", got, credsafe.RedactedSourceLabel)
	}
	for _, p := range pairs {
		if p.a == p.b {
			t.Fatalf("%s: the two addresses are the same string, so comparing their runs proves nothing", p.what)
		}
	}

	// The log line, read out of a real captured sink rather than reasoned
	// about. Each address is fetched on its own so the two sinks can be
	// compared line for line.
	strip := func(s string) string {
		var kept []string
		for _, line := range strings.Split(s, "\n") {
			if !strings.Contains(line, "catalog source") {
				continue
			}
			// The timestamp and the per-tick request id differ between two
			// runs for reasons that have nothing to do with the address.
			if i := strings.Index(line, `"level"`); i > 0 {
				line = line[i:]
			}
			line = requestIDNoise.ReplaceAllString(line, `"request_id":""`)
			kept = append(kept, line)
		}
		sort.Strings(kept)
		return strings.Join(kept, "\n")
	}

	for _, p := range pairs {
		la := strip(captureFetcherLogs(t, []string{p.a}))
		lb := strip(captureFetcherLogs(t, []string{p.b}))
		if la == "" || lb == "" {
			t.Fatalf("NOT EXERCISED | %s produced no catalog-source log line, so comparing the two runs proves nothing", p.what)
		}
		if la != lb {
			t.Errorf("DIFFERENT | %s: the two addresses differ only in the credential, but their log lines differ.\nfirst:\n%s\nsecond:\n%s", p.what, la, lb)
		}
		if strings.Contains(la, secretA) || strings.Contains(lb, secretB) {
			t.Errorf("LEAK | %s: a log line carries the credential", p.what)
		}
	}
}

// ---------------------------------------------------------------------
// several unreadable sources still add up truthfully
// ---------------------------------------------------------------------

// TestSeveralUnreadableSourcesStillCountTruthfully is the cost of the fixed
// word, checked rather than assumed. Three unreadable sources share one
// label, so they share one line — and that line has to hold the true total
// across all three, not one of them.
func TestSeveralUnreadableSourcesStillCountTruthfully(t *testing.T) {
	const n = 3

	urls := []string{
		unreadableByPort("bf14-count-one-synthetic+not=a=real=token"),
		unreadableByPort("bf14-count-two-synthetic+not=a=real=token"),
		unreadableByMissingColon("bf14-count-three-synthetic+not=a=real=token"),
	}
	if len(urls) != n {
		t.Fatalf("this test is meant to drive %d unreadable sources and is driving %d", n, len(urls))
	}
	seen := map[string]bool{}
	for _, u := range urls {
		requireStillUnreadable(t, u)
		if seen[u] {
			t.Fatalf("two of the addresses are the same string, so the total below would not be a total across %d distinct sources", n)
		}
		seen[u] = true
	}
	if got := credsafe.PublicSourceLabel(); got != credsafe.RedactedSourceLabel {
		t.Fatalf("PublicSourceLabel() = %q, not %q — this test is about sources that share the one label", got, credsafe.RedactedSourceLabel)
	}

	// The counter is process-global and other tests in this package write
	// to the same series, so the reading is a delta measured either side of
	// this one refresh, never an absolute.
	const metric = "sharko_catalog_source_fetch_total"
	before, beforeLines := seriesValue(scrapeMetrics(t), metric, credsafe.RedactedSourceLabel, "failed")
	if beforeLines > 1 {
		t.Fatalf("the scrape already publishes %d lines for %s{url=%q,status=\"failed\"}, so a before-and-after reading would be ambiguous", beforeLines, metric, credsafe.RedactedSourceLabel)
	}

	srcs := make([]config.CatalogSource, 0, len(urls))
	for _, u := range urls {
		srcs = append(srcs, config.CatalogSource{URL: u})
	}
	fetcher := sources.NewFetcher(&config.CatalogSourcesConfig{
		Sources:         srcs,
		RefreshInterval: config.MinRefreshInterval,
		AllowPrivate:    true,
	}, nil, nil)
	fetcher.ForceRefresh(t.Context())

	body := scrapeMetrics(t)
	after, afterLines := seriesValue(body, metric, credsafe.RedactedSourceLabel, "failed")

	if afterLines != 1 {
		t.Fatalf("after the refresh the scrape publishes %d lines for %s{url=%q,status=\"failed\"}, want exactly 1 — the whole point of the fixed word is that these sources share one line", afterLines, metric, credsafe.RedactedSourceLabel)
	}
	if got := after - before; got != float64(n) {
		t.Errorf("WRONG TOTAL | %d unreadable sources each failed once, so the shared line should have gone up by %d. It went up by %v (%v to %v). A shared label that loses counts is worse than a separate label, not safer.", n, n, got, before, after)
	}

	// Each source is still its own entry internally — the snapshot map is
	// keyed by the raw address, which is where the raw address is allowed
	// to stay.
	snaps := fetcher.Snapshots()
	if len(snaps) != n {
		t.Errorf("the fetcher holds %d snapshots for %d sources — internally the sources must stay apart even though they share one public label", len(snaps), n)
	}
	for _, u := range urls {
		if _, ok := snaps[u]; !ok {
			t.Errorf("the snapshot map has no entry keyed by one of the addresses, so the internal keying changed along with the public label")
		}
	}

	// The two gauges are written only on the success path, and an address
	// nobody can read cannot succeed — so they must not have grown a
	// redacted line out of this run. When they do carry one (from a source
	// that succeeded), it is a single shared line, exactly as documented.
	for _, m := range []string{"sharko_catalog_source_last_success_timestamp", "sharko_catalog_source_entries"} {
		if _, lines := seriesValue(body, m, credsafe.RedactedSourceLabel, ""); lines > 1 {
			t.Errorf("%s publishes %d lines under url=%q. Every source Sharko cannot read shares that one line — more than one means the label stopped being the fixed word.", m, lines, credsafe.RedactedSourceLabel)
		}
	}
}

// ---------------------------------------------------------------------
// the fingerprint helper does not come back
// ---------------------------------------------------------------------

// catalogAddressFingerprintSites is the list of places non-test code is
// allowed to compute a short identifier from a catalog source address.
// It is empty, and it is a list rather than a count so that adding one back
// has to be a visible edit here as well as in the code.
var catalogAddressFingerprintSites = []string{}

// fingerprintWalkControl is a name the walk below must find in the tree.
// A walk that finds no identifiers at all would report "no fingerprints"
// for the same reason it would report anything else — because it read
// nothing. Finding this proves it is reading real code.
const fingerprintWalkControl = "metricLabelForURL"

// TestNoAddressFingerprintInNonTestCode walks the tree and fails if any
// non-test file computes a fingerprint of a source address again.
func TestNoAddressFingerprintInNonTestCode(t *testing.T) {
	root := repoRootForMetricLabelGuard(t)

	skipDir := map[string]bool{
		".git": true, "node_modules": true, "ui": true,
		"_dist": true, ".worktrees": true, "vendor": true,
	}

	var (
		filesParsed int
		found       []string
		sawControl  bool
	)

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && skipDir[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			t.Errorf("cannot parse %s: %v", path, perr)
			return nil
		}
		filesParsed++

		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)

		ast.Inspect(file, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok {
				return true
			}
			if id.Name == fingerprintWalkControl {
				sawControl = true
			}
			if strings.Contains(strings.ToLower(id.Name), "fingerprint") {
				found = append(found, rel+":"+strconv.Itoa(fset.Position(id.Pos()).Line)+" | "+id.Name)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}

	// Nothing read means nothing checked, however green it looks.
	if filesParsed == 0 {
		t.Fatalf("this guard parsed no Go files at all — it is looking at an empty tree, not at a product with no fingerprints")
	}
	if !sawControl {
		t.Fatalf("this guard read %d files and never saw %q, which is in the tree. It is not reading identifiers, so its finding nothing means nothing.", filesParsed, fingerprintWalkControl)
	}

	allowed := map[string]bool{}
	for _, a := range catalogAddressFingerprintSites {
		allowed[a] = true
	}
	if len(allowed) != len(catalogAddressFingerprintSites) {
		t.Fatalf("catalogAddressFingerprintSites holds a duplicate, so the comparison below is not the one it looks like")
	}

	sort.Strings(found)
	for _, f := range found {
		if allowed[f] {
			continue
		}
		t.Errorf("FINGERPRINT IS BACK | %s. A short value computed from a catalog source address is the thing this fix removed: the same address always gives the same value, and it is published where nobody has to log in, so it lets someone test guesses at the credential offline. Use credsafe.PublicSourceLabel instead.", f)
	}
	var stale []string
	for _, a := range catalogAddressFingerprintSites {
		seen := false
		for _, f := range found {
			if f == a {
				seen = true
				break
			}
		}
		if !seen {
			stale = append(stale, a)
		}
	}
	for _, a := range stale {
		t.Errorf("STALE | catalogAddressFingerprintSites lists %s, which is not in the tree any more. Delete the entry in the same change that deleted the code.", a)
	}
}
