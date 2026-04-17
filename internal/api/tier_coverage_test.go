package api

// tier_coverage_test.go — CI guard that fails if any mutating handler in this
// package is missing from HandlerTier (in tier_registry.go) or, when intentional,
// from tierAllowlist.
//
// Mirrors audit_coverage_test.go in approach: parse the package AST, find every
// mux.HandleFunc("POST|PUT|PATCH|DELETE ...", srv.handleXXX), check that the
// handler is classified.

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// tierAllowlist contains handler names that legitimately have no tier
// classification. Today every mutating handler should appear in HandlerTier
// instead — this allowlist is reserved for unusual cases (e.g. inline closures
// that can't be classified statically).
var tierAllowlist = map[string]string{
	// (intentionally empty — all mutating handlers should be in HandlerTier)
}

func TestTierCoverage(t *testing.T) {
	pkgDir, err := findPackageDir()
	if err != nil {
		t.Fatalf("cannot locate internal/api package: %v", err)
	}

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, pkgDir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing package: %v", err)
	}

	pkg, ok := pkgs["api"]
	if !ok {
		t.Fatal("package 'api' not found in parsed directory")
	}

	mutatingHandlers := collectMutatingHandlers(pkg)

	var missing []string
	for handler := range mutatingHandlers {
		if reason, allowed := tierAllowlist[handler]; allowed {
			t.Logf("SKIP %s — %s", handler, reason)
			continue
		}
		if _, ok := HandlerTier[handler]; !ok {
			missing = append(missing, handler)
		}
	}

	if len(missing) > 0 {
		t.Errorf("\nThe following mutating handlers are missing tier classification in HandlerTier:\n")
		for _, h := range missing {
			t.Errorf("  - %s", h)
		}
		t.Error("\nAdd an entry to HandlerTier in tier_registry.go classifying the handler as Tier1, Tier2,")
		t.Error("TierPersonal, TierAuth, or TierWebhook. See internal/audit/tier.go for the model.")
	}
}
