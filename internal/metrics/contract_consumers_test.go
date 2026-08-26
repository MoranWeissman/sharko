package metrics

// contract_consumers_test.go — everything that tells somebody a Sharko
// metric exists, read with the real PromQL parser.
//
// Four consumers:
//
//	chart-rule    the alerting and recording rules the Helm chart ships,
//	              rendered with helm so the conditionals are resolved
//	dashboard     every panel target in charts/sharko/dashboards/
//	promql-fence  every ```promql block in docs/
//	inline-doc    every backticked sharko_… reference in markdown prose
//
// The first three are parsed with github.com/prometheus/prometheus's own
// PromQL parser and walked as an AST, so a metric name is read out of a
// *parser.VectorSelector and every label matcher comes with it. A regex
// extractor would be the decorative version of this: it cannot tell
// sharko_x{a="b"} inside a real query from the same characters inside a
// sentence, and it cannot see the label matchers of a selector written
// across several lines.
//
// The fourth exists because of one sentence. docs/site/operator/
// catalog-trust-policy.md tells an operator to alert on
// sharko_catalog_source_entries{verified="false"} > 0. That is a REAL
// metric with a label it does not have, written in prose rather than in a
// promql fence. A name-only check passes it; a PromQL-only check never
// looks at it; the operator deploys an alert that can never fire and
// believes they are covered.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/prometheus/prometheus/promql/parser"
	"gopkg.in/yaml.v3"
)

// The four consumer names, as constants rather than as strings written out
// at each site. The exemption list is scoped to ONE of them, so a rename
// that left the scope test reading a name nothing produces would silently
// widen the exemptions back to everything.
const (
	sourceChartRule           = "chart-rule"
	sourceDashboard           = "dashboard"
	sourcePromQLFence         = "promql-fence"
	sourcePromQLFenceUnparsed = "promql-fence(unparsed)"
	sourceInlineDoc           = "inline-doc"
)

// allSources is every source name a reference may carry.
var allSources = []string{
	sourceChartRule, sourceDashboard, sourcePromQLFence,
	sourcePromQLFenceUnparsed, sourceInlineDoc,
}

// reference is one place something claims a metric series exists.
type reference struct {
	source string // one of the source constants above
	file   string // repo-relative; for chart rules, the template path
	line   int    // best effort, for the human reading the failure
	metric string
	labels []string
}

// ---------------------------------------------------------------------
// repo layout
// ---------------------------------------------------------------------

func repoRoot(t *testing.T) string {
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

// docFileExempt are paths whose sharko_ mentions are not claims about
// what an operator can query today.
var docFileExempt = []string{
	filepath.Join("docs", "design"),  // dated design records; not published, not instructions
	filepath.Join("docs", "swagger"), // generated from Go annotations
}

func isExemptDocPath(rel string) bool {
	for _, p := range docFileExempt {
		if rel == p || strings.HasPrefix(rel, p+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------
// PromQL
// ---------------------------------------------------------------------

// refsFromPromQL parses expr and returns every sharko_ vector selector in
// it, with the label matchers actually written on that selector. ok is
// false when expr is not parseable PromQL at all.
func refsFromPromQL(expr string) (refs []reference, ok bool) {
	parsed, err := parser.ParseExpr(expr)
	if err != nil {
		return nil, false
	}
	parser.Inspect(parsed, func(node parser.Node, _ []parser.Node) error {
		vs, isSelector := node.(*parser.VectorSelector)
		if !isSelector {
			return nil
		}
		name := vs.Name
		var lbls []string
		for _, m := range vs.LabelMatchers {
			if m.Name == "__name__" {
				if name == "" {
					name = m.Value
				}
				continue
			}
			lbls = append(lbls, m.Name)
		}
		if !strings.HasPrefix(name, metricNamePrefix) {
			return nil
		}
		refs = append(refs, reference{metric: name, labels: lbls})
		return nil
	})
	return refs, true
}

// ---------------------------------------------------------------------
// consumer 1 — the rendered chart rules
// ---------------------------------------------------------------------

type promRuleFile struct {
	Spec struct {
		Groups []struct {
			Name  string `yaml:"name"`
			Rules []struct {
				Record string `yaml:"record"`
				Alert  string `yaml:"alert"`
				Expr   string `yaml:"expr"`
			} `yaml:"rules"`
		} `yaml:"groups"`
	} `yaml:"spec"`
}

// chartRuleRefs renders the chart with the PrometheusRule template turned
// on and reads every rule expression out of it.
//
// helm is required, not optional. A missing helm binary must not turn
// this consumer into a silent skip: the shipped alert rules are the
// consumer an operator is most likely to deploy verbatim.
func chartRuleRefs(t *testing.T, root string) []reference {
	t.Helper()
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Fatalf("helm is not on PATH, so the shipped alert rules cannot be checked: %v", err)
	}
	cmd := exec.Command(helm, "template", "sharko", filepath.Join(root, "charts", "sharko"),
		"--set", "monitoring.prometheusRules.enabled=true",
		"--show-only", "templates/prometheusrules.yaml")
	cmd.Dir = root
	// Only stdout. helm writes kubeconfig-permission warnings to stderr,
	// and folding those into the YAML makes the render look unparseable.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("helm template failed: %v\n%s", err, stderr.String())
	}

	relPath := filepath.Join("charts", "sharko", "templates", "prometheusrules.yaml")
	var file promRuleFile
	if err := yaml.Unmarshal(out, &file); err != nil {
		t.Fatalf("rendered %s is not parseable YAML: %v", relPath, err)
	}

	var refs []reference
	rules := 0
	for _, g := range file.Spec.Groups {
		for _, r := range g.Rules {
			rules++
			name := r.Alert
			if name == "" {
				name = r.Record
			}
			found, ok := refsFromPromQL(r.Expr)
			if !ok {
				t.Errorf("%s: rule %q in group %q is not parseable PromQL:\n%s", relPath, name, g.Name, r.Expr)
				continue
			}
			for _, ref := range found {
				ref.source = sourceChartRule
				ref.file = relPath + " (" + g.Name + " / " + name + ")"
				refs = append(refs, ref)
			}
		}
	}
	if rules == 0 {
		t.Fatalf("%s rendered zero rules — the render is broken, not the rules", relPath)
	}
	return refs
}

// ---------------------------------------------------------------------
// consumer 2 — Grafana dashboards
// ---------------------------------------------------------------------

type grafanaPanel struct {
	Title   string         `json:"title"`
	Panels  []grafanaPanel `json:"panels"`
	Targets []struct {
		Expr string `json:"expr"`
	} `json:"targets"`
}

type grafanaDashboard struct {
	Panels     []grafanaPanel `json:"panels"`
	Templating struct {
		List []struct {
			Query any `json:"query"`
		} `json:"list"`
	} `json:"templating"`
}

func dashboardRefs(t *testing.T, root string) []reference {
	t.Helper()
	dir := filepath.Join(root, "charts", "sharko", "dashboards")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}

	var refs []reference
	seenTargets := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		rel := filepath.Join("charts", "sharko", "dashboards", e.Name())
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		var dash grafanaDashboard
		if err := json.Unmarshal(body, &dash); err != nil {
			t.Fatalf("%s is not parseable JSON: %v", rel, err)
		}
		var walk func(panels []grafanaPanel, path string)
		walk = func(panels []grafanaPanel, path string) {
			for _, p := range panels {
				title := path + p.Title
				for _, tgt := range p.Targets {
					if strings.TrimSpace(tgt.Expr) == "" {
						continue
					}
					seenTargets++
					found, ok := refsFromPromQL(tgt.Expr)
					if !ok {
						t.Errorf("%s: panel %q target is not parseable PromQL:\n%s", rel, title, tgt.Expr)
						continue
					}
					for _, ref := range found {
						ref.source = sourceDashboard
						ref.file = rel + " (panel: " + title + ")"
						refs = append(refs, ref)
					}
				}
				walk(p.Panels, title+" / ")
			}
		}
		walk(dash.Panels, "")

		for _, v := range dash.Templating.List {
			q, isString := v.Query.(string)
			if !isString || !strings.Contains(q, metricNamePrefix) {
				continue
			}
			if found, ok := refsFromPromQL(q); ok {
				for _, ref := range found {
					ref.source = sourceDashboard
					ref.file = rel + " (template variable)"
					refs = append(refs, ref)
				}
			}
		}
	}
	if seenTargets == 0 {
		t.Fatalf("no dashboard panel targets found under charts/sharko/dashboards — the walk is broken")
	}
	return refs
}

// ---------------------------------------------------------------------
// consumer 3 — ```promql fences in the docs
// ---------------------------------------------------------------------

// fenceOpenRe matches a promql fence opener, indented or not: the
// runbooks put several of them inside numbered list items.
var fenceOpenRe = regexp.MustCompile("^(\\s*)```+\\s*promql\\s*$")
var fenceCloseRe = regexp.MustCompile("^\\s*```+\\s*$")

func docFenceRefs(t *testing.T, root string) []reference {
	t.Helper()
	var refs []reference
	fences := 0

	forEachMarkdownFile(t, root, func(rel string, lines []string) {
		for i := 0; i < len(lines); i++ {
			if !fenceOpenRe.MatchString(lines[i]) {
				continue
			}
			start := i + 1
			end := start
			for end < len(lines) && !fenceCloseRe.MatchString(lines[end]) {
				end++
			}
			body := strings.Join(lines[start:end], "\n")
			i = end
			if !strings.Contains(body, metricNamePrefix) {
				continue
			}
			fences++
			found, ok := refsFromPromQL(strings.TrimSpace(dedent(body)))
			if !ok {
				// Not valid PromQL — often a fragment, or an
				// expression with a placeholder in it. Fall back to
				// the textual reader so the names are still checked,
				// and say so, because an unparseable published query
				// is itself worth a human look.
				t.Logf("%s:%d: promql fence does not parse; falling back to the textual reader", rel, start)
				for _, ref := range textualRefs(body) {
					ref.source = sourcePromQLFenceUnparsed
					ref.file = rel
					ref.line = start
					refs = append(refs, ref)
				}
				continue
			}
			for _, ref := range found {
				ref.source = sourcePromQLFence
				ref.file = rel
				ref.line = start
				refs = append(refs, ref)
			}
		}
	})

	if fences == 0 {
		t.Fatalf("no promql fences mentioning %s found under docs/ — the fence scanner is broken", metricNamePrefix)
	}
	return refs
}

// dedent removes the common leading whitespace a fence inside a list item
// carries, which the PromQL parser does not care about but which makes
// the logged snippets unreadable.
func dedent(s string) string {
	lines := strings.Split(s, "\n")
	indent := -1
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		n := len(l) - len(strings.TrimLeft(l, " \t"))
		if indent < 0 || n < indent {
			indent = n
		}
	}
	if indent <= 0 {
		return s
	}
	for i, l := range lines {
		if len(l) >= indent {
			lines[i] = l[indent:]
		}
	}
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------
// consumer 4 — inline backticked references in prose
// ---------------------------------------------------------------------

// inlineCodeRe matches a markdown inline code span.
var inlineCodeRe = regexp.MustCompile("`+([^`]+)`+")

// metricTokenRe matches a metric name with its optional label block. The
// label block is what makes this worth doing: it is how
// {verified="false"} on a real metric gets caught. The name is required
// to end in an alphanumeric so a wildcard mention like
// `sharko_login_*_total` does not produce a finding named "sharko_login_".
var metricTokenRe = regexp.MustCompile(`\bsharko_[a-z0-9_]*[a-z0-9](\{[^}]*\})?`)

// labelListItemRe pulls one label name off a {…} entry. It accepts both
// shapes the docs use: a real matcher (verified="false") and a bare list
// of label names in a table cell ({reconciler,result}). The second shape
// is not PromQL, but it is how the user guide and the developer guide
// tell a reader what a metric is partitioned by — and both of them name
// labels sharko_reconciler_runs_total does not have.
var labelListItemRe = regexp.MustCompile(`^([a-zA-Z_][a-zA-Z0-9_]*)`)

// textualRefs reads metric references straight out of text.
func textualRefs(text string) []reference {
	var refs []reference
	for _, m := range metricTokenRe.FindAllStringSubmatch(text, -1) {
		name := m[0]
		var lbls []string
		if m[1] != "" {
			name = strings.TrimSuffix(name, m[1])
			inner := strings.TrimSuffix(strings.TrimPrefix(m[1], "{"), "}")
			for _, part := range strings.Split(inner, ",") {
				part = strings.TrimSpace(part)
				if hit := labelListItemRe.FindStringSubmatch(part); hit != nil {
					lbls = append(lbls, hit[1])
				}
			}
		}
		refs = append(refs, reference{metric: name, labels: lbls})
	}
	return refs
}

func inlineDocRefs(t *testing.T, root string) []reference {
	t.Helper()
	var refs []reference
	spans := 0

	forEachMarkdownFile(t, root, func(rel string, lines []string) {
		inFence := false
		for i, line := range lines {
			// Skip fenced blocks: consumer 3 owns them, and a fenced
			// query read as prose would double-report the same defect
			// under a second source name.
			if fenceCloseRe.MatchString(line) || fenceOpenRe.MatchString(line) ||
				strings.HasPrefix(strings.TrimSpace(line), "```") {
				inFence = !inFence
				continue
			}
			if inFence {
				continue
			}
			for _, span := range inlineCodeRe.FindAllStringSubmatch(line, -1) {
				if !strings.Contains(span[1], metricNamePrefix) {
					continue
				}
				spans++
				for _, ref := range textualRefs(span[1]) {
					ref.source = sourceInlineDoc
					ref.file = rel
					ref.line = i + 1
					refs = append(refs, ref)
				}
			}
		}
	})

	if spans == 0 {
		t.Fatalf("no inline `sharko_…` code spans found in the docs — the inline scanner is broken")
	}
	return refs
}

// forEachMarkdownFile visits every published markdown file: docs/ (minus
// the two exempt trees) plus README.md.
func forEachMarkdownFile(t *testing.T, root string, fn func(rel string, lines []string)) {
	t.Helper()
	visit := func(path string) {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return
		}
		if isExemptDocPath(rel) {
			return
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		fn(rel, strings.Split(string(body), "\n"))
	}

	err := filepath.Walk(filepath.Join(root, "docs"), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			rel, relErr := filepath.Rel(root, path)
			if relErr == nil && isExemptDocPath(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".md") {
			visit(path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking docs/: %v", err)
	}
	visit(filepath.Join(root, "README.md"))
}

// allReferences gathers every consumer.
func allReferences(t *testing.T, root string) []reference {
	t.Helper()
	var refs []reference
	refs = append(refs, chartRuleRefs(t, root)...)
	refs = append(refs, dashboardRefs(t, root)...)
	refs = append(refs, docFenceRefs(t, root)...)
	refs = append(refs, inlineDocRefs(t, root)...)
	return refs
}

func (r reference) String() string {
	where := r.file
	if r.line > 0 {
		where = fmt.Sprintf("%s:%d", r.file, r.line)
	}
	return fmt.Sprintf("[%s] %s", r.source, where)
}
