package api

// connection_sentence_routing_test.go — the guard that stops a routing
// decision ever keying on a human sentence again.
//
// # The failure it exists to stop
//
// buildReconciliationConditions used to decide which failed STEP to show with
// a switch whose case values were full paragraphs:
//
//	switch v.FailureReason {
//	case failGitRead, failNoGitConnection:
//
// So a copy edit picked the branch. Reword failGitRead by one comma and the
// whole answer drops into default and names the wrong failed step — no
// compiler error, no failing test, nothing anywhere to notice. The words a
// person reads and the structure of what they are shown were the same object.
//
// The product owner's ruling: "presentation structure must follow typed facts,
// never equality between human sentences." That switch now keys on
// v.failure, a typed connectioncompare.CheckFailure. This guard is what stops
// the old shape coming back quietly in a NEW routing site.
//
// # What it looks at
//
// The AST of every non-test Go file in internal/api and internal/connectioncompare
// — walked, never listed, so a new file is covered the day it is written. It
// finds every place a sentence CONSTANT is used to choose a branch: a switch
// case, either side of an == / !=, a map literal's key, or a map lookup's
// index. The constant may be written bare (failGitRead) or qualified
// (connectioncompare.LimitReasonAdopted); both are the same decision and both
// are found. Each hit is reported by file, line and constant name.
//
// # It is a LIST, never a count, and never a floor
//
// A count-based guard ("no more than N sentence comparisons") gets HAPPIER as
// the bug appears: delete one old site, add two new ones, and a floor passes.
// A ceiling is no better — it says nothing about WHICH site, so whoever hits
// it has to go looking. This guard names every hit, and the reviewed
// exceptions below are named too, so a stale exception fails just as loudly as
// a new violation.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"unicode"
)

// sentenceRoutingDirs are the package directories this guard reads. Every
// non-test .go file under them is walked — there is NO hand-written file list.
//
// There used to be one, naming five files, and it was a hole of exactly the
// shape this whole round is about: a routing switch on a sentence constant
// written into internal/api/connection_messages.go — a sixth file in the same
// package, holding the same constants — passed clean (proved by hand, B17).
// A guard whose reach is a list somebody has to remember to extend is a guard
// that is one file behind whoever forgets.
//
// internal/connectioncompare is here because the catalog covers its sentences
// too (connectionContractFiles in connection_sentence_catalog_test.go), so a
// routing decision on one of them is the same defect wherever it is written.
var sentenceRoutingDirs = []string{
	"internal/api",
	"internal/connectioncompare",
}

// sentenceRoutingGoFiles lists every non-test .go file under the swept
// directories, repo-relative. A directory that has vanished is fatal rather
// than skipped: silently covering less is the failure mode.
func sentenceRoutingGoFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	for _, dir := range sentenceRoutingDirs {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			t.Fatalf("swept directory %q cannot be read — the guard would silently cover less than it claims: %v", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			out = append(out, filepath.ToSlash(filepath.Join(dir, e.Name())))
		}
	}
	sort.Strings(out)
	return out
}

// routingSite is one place a value is used to choose a branch.
type routingSite struct {
	constName string
	file      string
	line      int
	shape     string // "switch case", "== / != comparison", "map key" or "map lookup"
}

func (r routingSite) String() string {
	return fmt.Sprintf("  %s:%d  %s in a %s", r.file, r.line, r.constName, r.shape)
}

// allowedSentenceRouting names the routing sites on a sentence constant that
// have been looked at and kept, one line each with the reason.
//
// It is EMPTY on purpose. Every site that existed was converted in R2-4, and
// keeping the map rather than deleting it makes the next exception a decision
// somebody writes down rather than a line somebody adds to a switch. A stale
// entry fails too — see the second half of the test.
var allowedSentenceRouting = map[string]string{}

// sentenceConstantNames returns the identifiers in these packages whose value
// is prose a person reads.
//
// It is derived from ConnectionSentences, which the catalog guard already
// proves covers every sentence constant in the contract files BY NAME in both
// directions. So this guard cannot quietly lose reach: a sentence that escapes
// the catalog fails there first.
//
// BOTH SPELLINGS ARE RETURNED. The catalog identifier is the constant's name
// with its first letter lowercased. That IS the constant name for the
// unexported ones in internal/api, but sixteen of the catalogued sentences are
// EXPORTED constants in internal/connectioncompare, so their real name starts
// with a capital and is written from internal/api as a qualified selector.
// Matching only the lowercase form meant the guard had zero reach over those
// sixteen sentences in every file it walked (proved by hand, B17).
func sentenceConstantNames() map[string]bool {
	names := map[string]bool{}
	for id := range ConnectionSentences {
		names[id] = true
		r := []rune(id)
		r[0] = unicode.ToUpper(r[0])
		names[string(r)] = true
	}
	return names
}

// routingIdent returns the identifier a routing expression names, or nil.
// A bare `failGitRead` and a qualified `connectioncompare.LimitReasonAdopted`
// are the same decision written two ways, so both resolve here.
func routingIdent(e ast.Expr) *ast.Ident {
	switch v := e.(type) {
	case *ast.Ident:
		return v
	case *ast.SelectorExpr:
		return v.Sel
	case *ast.ParenExpr:
		return routingIdent(v.X)
	}
	return nil
}

// routingSitesIn is THE detector. Both the sweep and the self-proof below run
// this same function, so a change that blinds the detector blinds the proof
// too and cannot leave a green suite behind.
func routingSitesIn(fset *token.FileSet, file *ast.File, rel string, names map[string]bool) []routingSite {
	var sites []routingSite
	note := func(e ast.Expr, pos token.Pos, shape string) {
		id := routingIdent(e)
		if id == nil || !names[id.Name] {
			return
		}
		sites = append(sites, routingSite{id.Name, rel, fset.Position(pos).Line, shape})
	}

	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CaseClause:
			// `case failGitRead, failNoGitConnection:` — the exact shape
			// that shipped.
			for _, expr := range node.List {
				note(expr, expr.Pos(), "switch case")
			}
		case *ast.BinaryExpr:
			// `if v.FailureReason == failGitRead` and its negation. Any
			// other operator on a sentence is not a routing decision.
			if node.Op != token.EQL && node.Op != token.NEQ {
				return true
			}
			for _, side := range []ast.Expr{node.X, node.Y} {
				note(side, side.Pos(), "== / != comparison")
			}
		case *ast.CompositeLit:
			// `map[string]X{failGitRead: …}` — a lookup table keyed on a
			// sentence picks a branch just as a switch does, and a copy
			// edit breaks it just as silently.
			for _, elt := range node.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				note(kv.Key, kv.Key.Pos(), "map key")
			}
		case *ast.IndexExpr:
			// `table[failGitRead]` — the read side of the same table.
			note(node.Index, node.Index.Pos(), "map lookup")
		}
		return true
	})
	return sites
}

func TestConnectionSentences_NothingRoutesOnAHumanSentence(t *testing.T) {
	root := repoRootForSweep(t)
	names := sentenceConstantNames()
	if len(names) == 0 {
		t.Fatal("no sentence constant names were derived — this guard would pass vacuously")
	}

	var hits []routingSite
	sawAllowed := map[string]bool{}

	files := sentenceRoutingGoFiles(t, root)
	if len(files) == 0 {
		t.Fatal("no files to walk — this guard would pass vacuously")
	}
	for _, rel := range files {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filepath.Join(root, rel), nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", rel, err)
		}
		for _, site := range routingSitesIn(fset, file, rel, names) {
			key := fmt.Sprintf("%s:%s", rel, site.constName)
			if _, ok := allowedSentenceRouting[key]; ok {
				sawAllowed[key] = true
				continue
			}
			hits = append(hits, site)
		}
	}

	if len(hits) > 0 {
		lines := make([]string, 0, len(hits))
		for _, h := range hits {
			lines = append(lines, h.String())
		}
		sort.Strings(lines)
		t.Errorf("%d routing decision(s) key on a sentence a person reads, instead of on a typed fact.\n"+
			"Rewording any of these sentences would silently change which branch runs — no compiler\n"+
			"error, no failing test. Route on connectioncompare.CheckFailure (or another typed value)\n"+
			"and derive the words from it, the way finishView does:\n%s",
			len(hits), strings.Join(lines, "\n"))
	}

	// A kept exception that no longer exists is worse than no list: it reads
	// as a reviewed decision covering something that is gone.
	for key := range allowedSentenceRouting {
		if !sawAllowed[key] {
			t.Errorf("allowedSentenceRouting lists %q, but no such routing site exists any more — remove the stale entry.", key)
		}
	}
}

// TestConnectionSentences_RoutingGuardSeesASentenceSwitch proves the guard's
// detector actually fires.
//
// A sweep that reports nothing is indistinguishable from a sweep that looks at
// nothing, and this round has already produced several of the second kind. So
// the REAL detector — routingSitesIn, the same function the sweep above calls —
// runs over a snippet that routes on sentence constants in all four shapes it
// claims to see, in both the bare and the qualified spelling, and must find
// every one.
func TestConnectionSentences_RoutingGuardSeesASentenceSwitch(t *testing.T) {
	const src = `package api

func routeOnWords(reason string) string {
	switch reason {
	case failGitRead, failNoGitConnection:
		return "git"
	}
	switch reason {
	case connectioncompare.LimitReasonAdopted:
		return "adopted"
	}
	if reason == failLiveRead {
		return "live"
	}
	if reason == connectioncompare.LimitReasonSelfManaged {
		return "self"
	}
	table := map[string]string{repairFailWrite: "write"}
	return table[repairFailBuild]
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "probe.go", src, 0)
	if err != nil {
		t.Fatalf("parsing the probe: %v", err)
	}

	var found []string
	for _, site := range routingSitesIn(fset, file, "probe.go", sentenceConstantNames()) {
		found = append(found, site.constName+" ("+site.shape+")")
	}

	sort.Strings(found)
	want := []string{
		"LimitReasonAdopted (switch case)",
		"LimitReasonSelfManaged (== / != comparison)",
		"failGitRead (switch case)",
		"failLiveRead (== / != comparison)",
		"failNoGitConnection (switch case)",
		"repairFailBuild (map lookup)",
		"repairFailWrite (map key)",
	}
	if strings.Join(found, ",") != strings.Join(want, ",") {
		t.Errorf("the routing detector found %v on a snippet that plainly routes on seven sentences, want %v.\n"+
			"The real sweep's silence would mean nothing.", found, want)
	}
}
