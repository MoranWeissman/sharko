package api

// authz_read_coverage_test.go — the read half of the role-gate audit (B14).
//
// # Why this file exists
//
// authz_coverage_test.go audits every route registered under POST, PUT, PATCH
// or DELETE. That is its whole inventory, by construction: collectMutatingHandlers
// skips anything else. So half the API — the eighty-one GET routes — was
// outside every role-gate guard in this package, and a GET registered with no
// authz.Require at all produced no failure anywhere.
//
// That is how GET /api/v1/upgrade/{addonName}/versions and
// GET /api/v1/marketplace/addons/{name}/versions came to have no role gate
// while handing back a chart repository address with the access token inside
// it. Nothing was broken; there was simply nothing looking.
//
// # What this guard does, and what it deliberately does not
//
// It does NOT demand a role gate on every read. Most reads in Sharko are open
// to any authenticated caller on purpose, and inventing gates for fifty-five
// endpoints inside a security fix would be a behaviour change dressed up as a
// guard.
//
// What it does is make the open set VISIBLE and DELIBERATE. Every GET route is
// either gated on a named action from authz.ActionRequirements, or listed
// below in readEndpointsWithoutRoleGate. The list is exact and it fails both
// ways:
//
//   - a NEW ungated GET route fails as "not in the list" — somebody has to
//     look at it and decide, which is the step that did not happen before;
//   - a listed route that has GAINED a gate, or that no longer exists, fails
//     as a stale entry, so the list cannot rot into a comforting fiction.
//
// It also refuses to pass vacuously: the count of GATED read routes has an
// exact floor, so the gates that exist cannot be quietly removed while every
// route still "appears" in one of the two sets.
//
// # An honest note about what a viewer-level gate buys
//
// Both actions added in B14 (addon.list, catalog.freshness.read) are
// viewer-and-above, so the gate does NOT stop a read-only user reading those
// endpoints. What stops the credential leaving is internal/credsafe, not this.
// The gate buys three other things: the route is named in the role table, so a
// later tightening is a one-line table edit rather than a code hunt; an
// unknown action fails closed at admin; and the route stops being invisible
// here.

import (
	"go/parser"
	"os"
	"sort"
	"strings"
	"testing"

	"go/token"

	"github.com/MoranWeissman/sharko/internal/authz"
)

// readEndpointsWithoutRoleGate is the LIST of GET routes that carry no
// per-request role gate today. Being on this list is a statement that any
// authenticated caller may read this endpoint.
//
// Adding a route here is a decision, not a formality. Removing one means the
// route now has a gate, and the test says so.
var readEndpointsWithoutRoleGate = map[string]struct{}{
	"GET /api/v1/addons/catalog":                                  {},
	"GET /api/v1/addons/list":                                     {},
	"GET /api/v1/addons/version-matrix":                           {},
	"GET /api/v1/addons/{name}":                                   {},
	"GET /api/v1/addons/{name}/changelog":                         {},
	"GET /api/v1/addons/{name}/values":                            {},
	"GET /api/v1/ai/config":                                       {},
	"GET /api/v1/audit":                                           {},
	"GET /api/v1/audit/stream":                                    {},
	"GET /api/v1/catalog/addons":                                  {},
	"GET /api/v1/catalog/addons/{name}":                           {},
	"GET /api/v1/catalog/repo-charts":                             {},
	"GET /api/v1/catalog/validate":                                {},
	"GET /api/v1/cluster/home":                                    {},
	"GET /api/v1/cluster/nodes":                                   {},
	"GET /api/v1/clusters":                                        {},
	"GET /api/v1/clusters/{name}":                                 {},
	"GET /api/v1/clusters/{name}/changes":                         {},
	"GET /api/v1/clusters/{name}/comparison":                      {},
	"GET /api/v1/clusters/{name}/config-diff":                     {},
	"GET /api/v1/clusters/{name}/history":                         {},
	"GET /api/v1/clusters/{name}/values":                          {},
	"GET /api/v1/config":                                          {},
	"GET /api/v1/connections/":                                    {},
	"GET /api/v1/connections/discover-argocd":                     {},
	"GET /api/v1/dashboard/attention":                             {},
	"GET /api/v1/dashboard/pull-requests":                         {},
	"GET /api/v1/dashboard/stats":                                 {},
	"GET /api/v1/default-addons":                                  {},
	"GET /api/v1/docs/list":                                       {},
	"GET /api/v1/docs/{slug}":                                     {},
	"GET /api/v1/embedded-dashboards":                             {},
	"GET /api/v1/fleet/status":                                    {},
	"GET /api/v1/health":                                          {},
	"GET /api/v1/marketplace/addons":                              {},
	"GET /api/v1/marketplace/addons/{name}":                       {},
	"GET /api/v1/marketplace/addons/{name}/project-readme":        {},
	"GET /api/v1/marketplace/addons/{name}/readme":                {},
	"GET /api/v1/marketplace/remote/{repo}/{name}":                {},
	"GET /api/v1/marketplace/remote/{repo}/{name}/project-readme": {},
	"GET /api/v1/marketplace/search":                              {},
	"GET /api/v1/marketplace/sources":                             {},
	"GET /api/v1/notifications":                                   {},
	"GET /api/v1/observability/overview":                          {},
	"GET /api/v1/operations/{id}":                                 {},
	"GET /api/v1/providers":                                       {},
	"GET /api/v1/repo/status":                                     {},
	"GET /api/v1/secrets/status":                                  {},
	"GET /api/v1/settings/addon-values-engine-enabled":            {},
	"GET /api/v1/settings/allow-inline-credentials":               {},
	"GET /api/v1/settings/managed-cluster-self-heal":              {},
	"GET /api/v1/settings/probe-mode":                             {},
	"GET /api/v1/system/capabilities":                             {},
	"GET /api/v1/upgrade/ai-status":                               {},
	"GET /api/v1/upgrade/{addonName}/recommendations":             {},
}

// wantGatedReadRoutes is the EXACT number of GET routes carrying a role gate
// today. Not a round number under it: a floor with room in it is a hole, and
// three gates could then be deleted with every route still accounted for by
// one of the two sets. Re-measure when you add or remove a gate; do not round.
const wantGatedReadRoutes = 26

// TestReadEndpointRoleGateCoverage is the guard.
func TestReadEndpointRoleGateCoverage(t *testing.T) {
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

	routes := collectReadRoutes(t)

	// It must not be able to pass by finding nothing. A parser change or a
	// router refactor that made this walk return an empty inventory would
	// otherwise read as a clean run.
	if len(routes) < len(readEndpointsWithoutRoleGate)+wantGatedReadRoutes {
		t.Fatalf("only %d GET routes were found, but the guard accounts for %d — the route walk has been hollowed out and every result below is meaningless",
			len(routes), len(readEndpointsWithoutRoleGate)+wantGatedReadRoutes)
	}

	gated := 0
	seenUngated := map[string]bool{}

	patterns := make([]string, 0, len(routes))
	for pattern := range routes {
		patterns = append(patterns, pattern)
	}
	sort.Strings(patterns)

	for _, pattern := range patterns {
		handler := routes[pattern]
		actions := collectAuthzActions(pkg, handler)
		if len(actions) == 0 {
			seenUngated[pattern] = true
			if _, listed := readEndpointsWithoutRoleGate[pattern]; !listed {
				t.Errorf(`%s (%s) is a read endpoint with NO role gate, and it is not in readEndpointsWithoutRoleGate.

Either gate it:
    if !authz.RequireWithResponse(w, r, "<action>") { return }
naming an action that is already a key in internal/authz's ActionRequirements,
or add the route to readEndpointsWithoutRoleGate in authz_read_coverage_test.go
to say plainly that any authenticated caller may read it.

A read endpoint that is in neither place is exactly how
GET /api/v1/upgrade/{addonName}/versions came to hand back a chart
repository's access token with nothing checking who asked.`, pattern, handler)
			}
			continue
		}
		gated++
		if _, listed := readEndpointsWithoutRoleGate[pattern]; listed {
			t.Errorf("%s (%s) IS gated on %v, but it is still listed in readEndpointsWithoutRoleGate — a stale entry claims a gate does not exist when it does. Remove the entry.",
				pattern, handler, actions)
		}
		for _, action := range actions {
			if _, ok := authz.ActionRequirements[action]; !ok {
				t.Errorf(`%s (%s) gates on %q, which is NOT a key in authz.ActionRequirements — it silently falls back to fail-closed admin-only. Add it to the table explicitly.`,
					pattern, handler, action)
			}
		}
	}

	// Stale entries the other way: a listed route that no longer exists at all.
	for pattern := range readEndpointsWithoutRoleGate {
		if _, stillARoute := routes[pattern]; !stillARoute {
			t.Errorf("readEndpointsWithoutRoleGate lists %s, which is no longer a registered GET route — a stale entry classifies nothing.", pattern)
		}
	}

	if gated != wantGatedReadRoutes {
		t.Errorf(`%d read routes carry a role gate, want exactly %d.

Going DOWN means a gate was removed. Going UP means one was added and this
number was not re-measured — update it deliberately, and never to a round
number with room in it.`, gated, wantGatedReadRoutes)
	}

	t.Logf("read-endpoint role gates: %d GET routes, %d gated, %d open to any authenticated caller", len(routes), gated, len(seenUngated))
}

// collectReadRoutes returns every GET route the ROUTER registered, as
// pattern -> handler function name. It is the read-side twin of
// collectMutatingHandlers, and it keeps the PATTERN as well as the handler
// because two routes can share one handler and the operator-facing fact is
// the route.
//
// Like its mutating twin it reads the router's own registration record rather
// than the package source, so a GET registered through a helper is counted
// (B16). A route with no nameable handler is skipped here and failed by name
// in route_registry_guard_test.go.
func collectReadRoutes(t *testing.T) map[string]string {
	t.Helper()
	routes := make(map[string]string)
	for _, route := range routeInventory() {
		if route.Method != "GET" || route.Anonymous {
			continue
		}
		routes[route.Pattern] = route.HandlerName
	}
	return routes
}
