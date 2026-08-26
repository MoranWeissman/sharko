package main

// fanout_cli_inventory_test.go — R2-9.
//
// The CLI's exit code is the thing a script branches on, and for a fan-out
// command it was decided by nothing at all: both `sharko add-clusters` and
// `sharko adopt` returned nil whatever came back, so a run in which every
// cluster failed exited 0.
//
// The fix is one shared decision (fanout.Outcome.ExitError). This file is
// what keeps it shared. It is a LIST by file name, never a count: it fails
// when a command that can see a per-cluster "partial" is not classified, and
// it fails just as hard when a listed file stops matching, so the list cannot
// rot into a decoration.
//
// A count-based floor ("at least N files handle partial") would answer "did I
// see enough?" — which passes quietly the day somebody moves code around, and
// gets HAPPIER as the problem spreads.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The two kinds a command can be. Every entry must say which — an unknown
// kind fails the guard, so a new entry cannot be added without deciding.
const (
	// kindFanOut is a command that asks the server to act on SEVERAL
	// clusters in one call and gets one answer per cluster back. Its exit
	// code and summary come from fanout.Count + Outcome.ExitError.
	kindFanOut = "fan-out"
	// kindSingle is a command that acts on exactly ONE cluster and gets one
	// answer. Its exit code comes from fanout.SingleStatus +
	// Single.ExitError, which takes the decision itself from
	// Outcome.ExitError — the same rule, not a second copy of it.
	kindSingle = "single-cluster"
)

type cliPartialSite struct {
	kind string
	how  string
}

// cliPartialCommands is the LIST. Key is the file in cmd/sharko.
//
// R2-10 re-classified the last three. They used to be listed as "prints the
// partial plainly and exits 0", on the reading that a single-cluster partial
// was a separate question from the B2 ruling. It is not a separate question:
// the ruling is "exit 0 only when every requested cluster completed fully",
// and one cluster that stopped half-way did not complete fully. All five
// single-cluster commands now take the same decision the fan-out ones do.
var cliPartialCommands = map[string]cliPartialSite{
	"batch.go": {kind: kindFanOut,
		how: "`sharko add-clusters`. Exit code and summary both come from " +
			"fanout.Outcome, counted from the per-cluster answers."},
	"adopt.go": {kind: kindFanOut,
		how: "`sharko adopt`. Same shared decision. A dry run is the one " +
			"exception and exits 0, because a preview adopts nothing."},

	"cluster.go": {kind: kindSingle,
		how: "add-cluster / remove-cluster / update-cluster. One cluster, one answer, " +
			"counted through fanout.SingleStatus so a run that stopped part-way — or " +
			"came back with a status this build does not know — exits non-zero and " +
			"never prints a completion word."},
	"unadopt.go": {kind: kindSingle,
		how: "`sharko unadopt-cluster`. Same shape as cluster.go. A dry run exits 0, " +
			"because a preview un-adopts nothing."},
	"takeover.go": {kind: kindSingle,
		how: "`sharko takeover`. Same shape as cluster.go. A dry run exits 0, because " +
			"a preview takes over nothing."},
}

// TestCLIPartialCommands_AreAllListed fails when a command in cmd/sharko
// starts being able to see a per-cluster "partial" without being classified
// here, when a listed file no longer sees one, and when a file classified as
// a fan-out stops going through the shared exit decision.
func TestCLIPartialCommands_AreAllListed(t *testing.T) {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}

	fset := token.NewFileSet()
	seesPartial := map[string]bool{}
	decidesExit := map[string]bool{}
	counts := map[string]bool{}
	countsSingle := map[string]bool{}
	parsed := 0

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		parsed++
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.BasicLit:
				// The literal is how a command reads a partial for itself,
				// which is the case this guard most needs to catch: a new
				// command doing it inline is a new place the rule can be
				// copied wrong. A command that reads it through the fanout
				// package no longer carries the literal, so calling
				// fanout.Count / fanout.SingleStatus counts as seeing one
				// too — see the SelectorExpr arm below.
				if x.Kind == token.STRING && x.Value == `"partial"` {
					seesPartial[name] = true
				}
			case *ast.SelectorExpr:
				switch x.Sel.Name {
				case "ExitError":
					decidesExit[name] = true
				case "Count":
					if pkg, ok := x.X.(*ast.Ident); ok && pkg.Name == "fanout" {
						counts[name] = true
						seesPartial[name] = true
					}
				case "SingleStatus":
					if pkg, ok := x.X.(*ast.Ident); ok && pkg.Name == "fanout" {
						countsSingle[name] = true
						seesPartial[name] = true
					}
				}
			}
			return true
		})
	}

	if parsed == 0 {
		t.Fatal("no source files parsed from cmd/sharko — this guard is reading nothing. " +
			"Do not treat that as a pass.")
	}
	if len(seesPartial) == 0 {
		t.Fatal("no file in cmd/sharko mentions a per-cluster \"partial\" any more. Either the " +
			"package changed shape or the scan is broken; do not treat this as a pass.")
	}

	for name := range seesPartial {
		site, listed := cliPartialCommands[name]
		if !listed {
			t.Errorf("%s can see a per-cluster \"partial\" and is NOT in cliPartialCommands.\n"+
				"Add it, and say plainly which kind it is:\n"+
				"  - %q (several clusters, one answer each) → its exit code and summary must "+
				"come from fanout.Count + Outcome.ExitError;\n"+
				"  - %q (one cluster, one answer) → its exit code must come from "+
				"fanout.SingleStatus + Single.ExitError.\n"+
				"Either way, a run that did not finish must not exit 0.", name, kindFanOut, kindSingle)
			continue
		}
		// Whichever kind it is, the exit code must come from the shared
		// decision. Without it the command returns nil whatever came back.
		if !decidesExit[name] {
			t.Errorf("%s sees a per-cluster \"partial\" but never calls ExitError. Without it the "+
				"command returns nil whatever came back, and a run that did not finish exits 0 — "+
				"the R2-9 defect for a fan-out command, the R2-10 one for a single-cluster "+
				"command.", name)
		}
		switch site.kind {
		case kindFanOut:
			if !counts[name] {
				t.Errorf("%s is listed as a fan-out command but never calls fanout.Count. Its "+
					"summary and exit code must be driven by the same count the server and the "+
					"audit trail use, not by a second reading of the response.", name)
			}
		case kindSingle:
			if !countsSingle[name] {
				t.Errorf("%s is listed as a single-cluster command but never calls "+
					"fanout.SingleStatus. Reading the one answer inline is how the rule gets "+
					"copied, and a copied rule drifts: that is exactly how these three came to "+
					"exit 0 on a partial while the fan-out commands did not.", name)
			}
		default:
			t.Errorf("%s is listed with kind %q, which is neither %q nor %q. Every entry has to "+
				"say which it is — that choice decides what its exit code must be driven by.",
				name, site.kind, kindFanOut, kindSingle)
		}
	}

	var stale []string
	for name := range cliPartialCommands {
		if !seesPartial[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	for _, name := range stale {
		t.Errorf("cliPartialCommands lists %s but no command there sees a per-cluster \"partial\" "+
			"any more. Remove the entry — a list that keeps dead names stops being read.", name)
	}
}
