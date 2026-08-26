package metrics

// contract_annotate_outcomes_test.go — the sharko_ai_annotate_total Help
// string must name the outcomes the code really emits, and only those.
//
// WHY THIS TEST AND NOT A TEXT PIN. The Help string is the contract an
// operator writes an alert rule against. It used to name "opted_out" and
// "disabled", which nothing has ever written, and to leave out
// "empty_input", which is written on every empty-values pass. An alert on
// outcome="disabled" therefore sat green forever and looked like coverage.
// Pinning the sentence by exact text would have caught somebody editing the
// sentence; it would NOT catch the thing that actually goes wrong, which is
// a new outcome being added to the writer while the Help string stays put.
//
// So the emitted set is read out of the writer's own source with go/ast —
// every string literal that reaches recordAnnotate, whether it goes through
// res.SkipReason, through the local `reason` variable, or straight in as a
// literal — and compared against the names inside the Help string's
// parentheses. Add an outcome to internal/orchestrator/ai_annotate.go and
// this fails until the Help string says so.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// annotateWriterFile is the ONE writer of sharko_ai_annotate_total. If a
// second writer ever appears, TestAIAnnotateTotal_HasExactlyOneWriter below
// fails and this test's source-of-truth assumption is re-checked by a human.
const annotateWriterFile = "internal/orchestrator/ai_annotate.go"

func TestAIAnnotateTotal_HelpNamesExactlyTheEmittedOutcomes(t *testing.T) {
	emitted := annotateOutcomesFromSource(t)
	if len(emitted) == 0 {
		t.Fatalf("read no outcomes at all out of %s — the extractor is broken, "+
			"not the code under test", annotateWriterFile)
	}

	documented := outcomesInHelpString(t, AIAnnotateTotal)

	if diff := setDifference(documented, emitted); len(diff) > 0 {
		t.Errorf("the sharko_ai_annotate_total Help string names %v, which no code path emits.\n"+
			"An operator alerting on one of those label values would wait forever.\n"+
			"Either write the outcome or stop naming it.", diff)
	}
	if diff := setDifference(emitted, documented); len(diff) > 0 {
		t.Errorf("%s emits %v, which the sharko_ai_annotate_total Help string does not name.\n"+
			"The Help string is the published contract — add them.", annotateWriterFile, diff)
	}
}

// TestAIAnnotateTotal_HasExactlyOneWriter guards the assumption the test
// above rests on: that reading one file gives the whole emitted set.
func TestAIAnnotateTotal_HasExactlyOneWriter(t *testing.T) {
	root := repoRoot(t)
	hits := grepRepoForMetricIdentifier(t, root, "AIAnnotateTotal")
	// The definition in this package plus the single writer.
	want := map[string]bool{
		"internal/metrics/metrics.go": true,
		annotateWriterFile:            true,
	}
	for _, f := range hits {
		if !want[f] {
			t.Errorf("metrics.AIAnnotateTotal is referenced in %s. "+
				"TestAIAnnotateTotal_HelpNamesExactlyTheEmittedOutcomes reads the emitted "+
				"outcome set out of %s alone, so a second writer makes that check partial. "+
				"Teach the extractor about the new writer, or keep the writes in one place.",
				f, annotateWriterFile)
		}
	}
}

// annotateOutcomesFromSource returns every outcome literal that can reach
// recordAnnotate in the writer file.
func annotateOutcomesFromSource(t *testing.T) map[string]bool {
	t.Helper()
	path := filepath.Join(repoRoot(t), annotateWriterFile)
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", annotateWriterFile, err)
	}

	out := map[string]bool{}
	add := func(e ast.Expr) {
		lit, ok := e.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return
		}
		v, err := strconv.Unquote(lit.Value)
		if err == nil && v != "" {
			out[v] = true
		}
	}

	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			// res.SkipReason = "…"  and  reason := "…" / reason = "…"
			for i, lhs := range node.Lhs {
				if i >= len(node.Rhs) {
					break
				}
				if assignsAnnotateOutcome(lhs) {
					add(node.Rhs[i])
				}
			}
		case *ast.CallExpr:
			// recordAnnotate("ok", …)
			if id, ok := node.Fun.(*ast.Ident); ok && id.Name == "recordAnnotate" && len(node.Args) > 0 {
				add(node.Args[0])
			}
		}
		return true
	})
	return out
}

// assignsAnnotateOutcome reports whether this assignment target is one of
// the two names an outcome literal travels to recordAnnotate through.
func assignsAnnotateOutcome(lhs ast.Expr) bool {
	switch target := lhs.(type) {
	case *ast.SelectorExpr:
		return target.Sel != nil && target.Sel.Name == "SkipReason"
	case *ast.Ident:
		return target.Name == "reason"
	}
	return false
}

// outcomesInHelpString pulls the comma-separated names out of the metric's
// Help text, which is written as "… by outcome (a, b, c)".
func outcomesInHelpString(t *testing.T, c *prometheus.CounterVec) map[string]bool {
	t.Helper()
	// Desc().String() wraps the help text in its own punctuation, so cut to
	// the help itself before looking for the parenthesised list.
	help := c.WithLabelValues("probe").Desc().String()
	if i := strings.Index(help, `help: `); i >= 0 {
		help = help[i+len(`help: `):]
	}
	open := strings.Index(help, "(")
	close := strings.Index(help, ")")
	if open < 0 || close < open {
		t.Fatalf("the sharko_ai_annotate_total Help string no longer carries a "+
			"parenthesised outcome list, so nothing states the contract: %q", help)
	}
	out := map[string]bool{}
	for _, name := range strings.Split(help[open+1:close], ",") {
		name = strings.TrimSpace(name)
		if name != "" {
			out[name] = true
		}
	}
	return out
}

// setDifference returns the members of a that are not in b, sorted.
func setDifference(a, b map[string]bool) []string {
	var out []string
	for k := range a {
		if !b[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// grepRepoForMetricIdentifier lists the repo-relative .go files (tests
// excluded) that mention the given metrics identifier.
func grepRepoForMetricIdentifier(t *testing.T, root, ident string) []string {
	t.Helper()
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "node_modules", "ui", ".worktrees":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if !strings.Contains(string(body), ident) {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repo for %s: %v", ident, err)
	}
	sort.Strings(files)
	if len(files) == 0 {
		t.Fatalf("found no file mentioning %s at all — the walker is broken, not the code", ident)
	}
	return files
}
