package api

// route_registry_guard_test.go — the guard on the guards' own inventory (B16).
//
// TestAuthzCoverage, TestAuditCoverage, TestTierCoverage and
// TestReadEndpointRoleGateCoverage all ask routeInventory() what routes exist.
// That answer is now the load-bearing fact under four safety guards, so it
// needs its own proof — otherwise a change that quietly stops routes being
// recorded would make all four pass by finding nothing, which is exactly the
// failure mode the guards were written to prevent.
//
// This file proves four things:
//
//  1. The inventory is not empty and matches the tree EXACTLY. Both counts are
//     exact, not floors: a floor with room in it is a hole, and this tree has
//     already been bitten by one that sat three below the real number.
//  2. Every mutating route has a NAMED handler. An anonymous func literal
//     cannot be keyed in HandlerTier, auditAllowlist or authzAllowlist, so it
//     would be an endpoint nothing can classify.
//  3. Every name the inventory reports is a real function in this package —
//     so a name that stops resolving (a rename, a refactor into a closure)
//     fails here rather than silently emptying a table lookup.
//  4. routeMux is the only way to build a ServeMux in this package's shipping
//     files, and nothing reaches past it to the embedded one. Those are the
//     two structural rules that make "a route that skips the registrar is not
//     served" true rather than merely intended.

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

// wantRegisteredRoutes is the EXACT number of routes registerRoutes puts on
// the mux when staticFS is nil (the SPA catch-all is the only route a non-nil
// staticFS adds). Exact, never a floor: this number is what proves the walk
// still reaches the router at all.
//
// Adding a route means bumping this by hand. That is the point — the bump is
// the moment somebody looks at the new endpoint.
const wantRegisteredRoutes = 178

// wantMutatingHandlers is the EXACT number of distinct handler functions
// registered under POST/PUT/PATCH/DELETE. Same rule: exact, and a change here
// is a decision, not a maintenance chore.
const wantMutatingHandlers = 94

func TestRouteInventoryIsExactAndNamed(t *testing.T) {
	routes := routeInventory()

	if len(routes) != wantRegisteredRoutes {
		t.Errorf(`the router registered %d routes, want exactly %d.

Going DOWN means routes stopped being recorded — every coverage guard in this
package would then pass by having nothing to check. Going UP means a new
endpoint arrived; bump this number deliberately once you have classified it in
HandlerTier, and in the audit and authz tables or their allowlists.`,
			len(routes), wantRegisteredRoutes)
	}

	seenPatterns := map[string]bool{}
	mutating := map[string]bool{}
	for _, route := range routes {
		if seenPatterns[route.Pattern] {
			t.Errorf("route pattern %q was registered twice — the second registration is unreachable, and whichever handler it names is not the one that runs", route.Pattern)
		}
		seenPatterns[route.Pattern] = true

		if !mutatingMethods[route.Method] {
			continue
		}
		if route.Anonymous {
			t.Errorf(`%s is a mutating route registered with an anonymous function.

Every table that says who may call an endpoint (authzAllowlist), whether it is
audited (auditAllowlist) and which tier it belongs to (HandlerTier) is keyed by
handler NAME. A closure has no name, so this route cannot be classified in any
of them — and an endpoint nothing can classify is an endpoint nothing is
checking. Give it a method, the way handleLoginRateLimited is one.`, route.Pattern)
			continue
		}
		mutating[route.HandlerName] = true
	}

	if len(mutating) != wantMutatingHandlers {
		t.Errorf(`%d distinct handlers are registered for POST/PUT/PATCH/DELETE, want exactly %d.

Same rule as the route count above: exact, both directions.`,
			len(mutating), wantMutatingHandlers)
	}
}

// TestRouteInventoryNamesResolveToRealFunctions cross-checks the runtime names
// against the package source. The inventory is produced by the runtime's
// function table; the tables it feeds are keyed by hand-written strings. If
// the two ever describe different things, every lookup silently misses.
func TestRouteInventoryNamesResolveToRealFunctions(t *testing.T) {
	declared := declaredFuncNames(t)
	if len(declared) < 200 {
		t.Fatalf("only %d function declarations parsed out of internal/api — the cross-check is not reading the package, so it would agree with anything", len(declared))
	}

	checked := 0
	for _, route := range routeInventory() {
		if route.Anonymous {
			continue
		}
		checked++
		if !declared[route.HandlerName] {
			t.Errorf("route %q resolves at runtime to %q, which is not a function declared in this package — the name the coverage tables are keyed by does not exist",
				route.Pattern, route.HandlerName)
		}
	}
	if checked < wantMutatingHandlers {
		t.Fatalf("only %d named routes were cross-checked; that is fewer than the %d mutating handlers alone, so the resolution step is broken", checked, wantMutatingHandlers)
	}
}

// TestOnlyRouteMuxBuildsTheServeMux pins the two structural rules that make
// the registrar unavoidable.
//
// Rule 1: exactly one http.NewServeMux in the package's shipping files, inside
// newRouteMux. A second one would be a router nothing records.
//
// Rule 2: nobody writes `.ServeMux.Handle` / `.ServeMux.HandleFunc` outside
// route_registry.go. routeMux embeds *http.ServeMux so it can BE one; that
// embedding is also the one way to register without being recorded, and this
// is the rule that closes it.
func TestOnlyRouteMuxBuildsTheServeMux(t *testing.T) {
	dir, err := findPackageDir()
	if err != nil {
		t.Fatalf("cannot locate internal/api package: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}

	newMuxSites := map[string]int{}
	embeddedSites := map[string]int{}
	filesRead := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, readErr := os.ReadFile(filepath.Join(dir, name))
		if readErr != nil {
			t.Fatalf("reading %s: %v", name, readErr)
		}
		filesRead++
		text := string(body)
		newMuxSites[name] = strings.Count(text, "http.NewServeMux(")
		embeddedSites[name] = strings.Count(text, ".ServeMux.Handle")
	}
	if filesRead < 50 {
		t.Fatalf("only %d shipping files read out of internal/api — the scan is not reaching the package", filesRead)
	}

	totalNewMux := 0
	for file, count := range newMuxSites {
		totalNewMux += count
		if count > 0 && file != "route_registry.go" {
			t.Errorf(`%s calls http.NewServeMux.

routeMux (route_registry.go) is the only ServeMux this package builds, because
it is the thing that records every route for the permission, audit and tier
guards. A second mux is a set of routes none of them can see.`, file)
		}
	}
	if totalNewMux != 1 {
		t.Errorf("http.NewServeMux appears %d time(s) in the package's shipping files, want exactly 1 (in newRouteMux)", totalNewMux)
	}

	for file, count := range embeddedSites {
		if count > 0 && file != "route_registry.go" {
			t.Errorf(`%s reaches through routeMux to the embedded ServeMux (%d site(s)).

Registering there skips the recording, which is the whole point of the wrapper.
Call mux.HandleFunc / mux.Handle instead.`, file, count)
		}
	}
}

// declaredFuncNames returns the name of every function and method declared in
// the package's shipping files.
func declaredFuncNames(t *testing.T) map[string]bool {
	t.Helper()
	dir, err := findPackageDir()
	if err != nil {
		t.Fatalf("cannot locate internal/api package: %v", err)
	}
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing package: %v", err)
	}
	pkg, ok := pkgs["api"]
	if !ok {
		t.Fatal("package 'api' not found in parsed directory")
	}
	names := map[string]bool{}
	for _, file := range pkg.Files {
		for _, decl := range file.Decls {
			if fn, isFn := decl.(*ast.FuncDecl); isFn {
				names[fn.Name.Name] = true
			}
		}
	}
	return names
}

// TestClassificationTablesHaveNoStaleEntries is the other direction of every
// coverage guard in this package.
//
// TestAuditCoverage, TestAuthzCoverage and TestTierCoverage all ask the same
// question — "is every registered mutating handler classified?" — and none of
// them asks the reverse. So an entry could name a handler that no longer
// exists, or that is no longer a route, and sit there forever looking like
// coverage. A stale entry is worse than a missing one: it is a written
// statement that somebody looked, about something nobody looks at.
//
// The rule is the same for all four tables: an entry must name a handler the
// router actually registers for a mutating method.
func TestClassificationTablesHaveNoStaleEntries(t *testing.T) {
	registered := collectMutatingHandlers(t)
	if len(registered) != wantMutatingHandlers {
		t.Fatalf("the mutating-handler inventory has %d entries, want %d — nothing below can be trusted until that agrees", len(registered), wantMutatingHandlers)
	}

	check := func(table string, keys []string) {
		for _, key := range keys {
			if _, isRegistered := registered[key]; !isRegistered {
				t.Errorf(`%s names %q, which the router does not register for any mutating method.

Either the handler was renamed or deleted, or its route moved to a different
handler. Remove the entry: a table row about a route that does not exist is a
claim that something was checked when nothing was.`, table, key)
			}
		}
	}

	check("auditAllowlist", sortedAllowlistKeys(auditAllowlist))
	check("authzAllowlist", sortedAllowlistKeys(authzAllowlist))
	check("tierAllowlist", sortedAllowlistKeys(tierAllowlist))

	tierKeys := make([]string, 0, len(HandlerTier))
	for k := range HandlerTier {
		tierKeys = append(tierKeys, k)
	}
	sort.Strings(tierKeys)
	check("HandlerTier", tierKeys)
}

func sortedAllowlistKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
