package api

// authz_coverage_test.go — CI guard that mechanically proves every mutating
// REST endpoint maps to a known authz action with a defined role.
//
// Mirrors audit_coverage_test.go / tier_coverage_test.go in approach (same
// collectMutatingHandlers seam, defined once in audit_coverage_test.go):
//  1. Parse the internal/api package AST.
//  2. Find every mux.HandleFunc("POST|PUT|PATCH|DELETE ...", srv.handleXXX)
//     route registered in router.go (collectMutatingHandlers already walks
//     func-literal wrappers too, e.g. the rate-limited /auth/login route).
//  3. For each handler's func body, find every call to
//     authz.RequireWithResponse(w, r, "<action>") or authz.Require(r, "<action>")
//     and extract the literal action string(s).
//  4. A handler must call one of those with a NON-empty literal action, and
//     that action must be a key in authz.ActionRequirements (the fail-closed
//     table in internal/authz/authz.go) — an action used in code but absent
//     from the table silently falls back to admin-only, which is easy to get
//     backwards without noticing.
//  5. Handlers that legitimately have no per-request role gate (auth
//     bootstrapping routes, HMAC-verified webhooks, per-tool-gated AI chat)
//     are named in authzAllowlist with a justification.
//
// This test is deliberately independent of any hand-maintained endpoint
// list (unlike authz_unguarded_endpoints_test.go, which asserts specific
// handler behavior for a fixed set of routes) — it re-derives the route
// inventory from the router itself, so a newly added mutating handler that
// forgets the authz gate fails CI without anyone updating this file.
//
// Story v4-8-4 (Wave 2, Epic 8) — the role gate audit.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/authz"
)

// authzAllowlist contains mutating handlers that legitimately have no
// per-request authz.Require*/RequireWithResponse gate at the handler entry.
// Each entry must have a justification comment; an empty allowlist is the
// goal, not the default.
var authzAllowlist = map[string]string{
	// Auth bootstrap — these routes exist so a caller CAN establish a role in
	// the first place; gating them on a role would be circular.
	"handleLogin":           "unauthenticated by design — this IS the auth entry point; rate-limited separately",
	"handleLogout":          "any authenticated session may end itself; no role distinction meaningful",
	"handleStaleLoginRoute": "stale dead-route 404 stub, not a real action (see router.go comment)",
	"handleHashPassword":    "utility endpoint, only reachable when auth is disabled (no roles exist yet)",
	"handleUpdatePassword":  "self-service: changes the caller's OWN password, identity-scoped not role-scoped",

	// Webhooks — authenticated via HMAC signature, not a user role.
	"handleGitWebhook": "HMAC-SHA256 signature verified inside the handler; no user role applies",

	// AI chat — write actions are gated per-tool-call via
	// ai.authorizeWriteTool (see internal/ai/tools_write.go), not with a
	// single blanket check at the HTTP handler entry, because one chat
	// turn can invoke zero, one, or several tools with different actions.
	"handleAgentChat":  "gates per-tool-call via ai.authorizeWriteTool, not at handler entry",
	"handleAgentReset": "clears the caller's own conversation memory; identity-scoped not role-scoped",

	// Verified: read-only "test"/"check" endpoints registered under a
	// mutating HTTP method (POST/PUT) only because they accept a request
	// body — none of these persist anything (confirmed by reading each
	// handler body during the v4-8-4 audit). No role distinction is
	// meaningful for a probe that changes nothing.
	//
	// The four connection/provider tests that used to live here came OUT in
	// the v4-wave2 security review (finding B1). "Persists nothing" turned
	// out to be the wrong question for them: they send real credentials —
	// including stored ones the caller never typed — to an address that
	// comes out of the request body. They now gate on connection.test /
	// provider.test (operator+) like any other action that reaches out with
	// a secret. Do NOT move them back here.
	"handleTestAI":       "AI connectivity probe (one summarize call); no persisted state",
	"handleTestAIConfig": "AI config connectivity probe; no persisted state (also audit-exempt, same reason)",
	"handleGetAISummary": "read-only analysis endpoint, POST only because it accepts a large body (also audit-exempt)",
	"handleCheckUpgrade": "read-only version/compatibility analysis (upgradeSvc.CheckUpgrade); no persisted state",
}

func TestAuthzCoverage(t *testing.T) {
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

	// Reuse the same route-inventory seam audit_coverage_test.go and
	// tier_coverage_test.go already rely on — this is the "enumerate every
	// registered mutating route" mechanism for the whole package.
	mutatingHandlers := collectMutatingHandlers(pkg)

	var noGate []string              // handler has zero authz.Require*/RequireWithResponse call
	var unmappedAction []string      // handler's action literal is not a key in ActionRequirements
	actionsSeen := map[string]bool{} // every action literal actually used by a handler

	for handler := range mutatingHandlers {
		if reason, allowed := authzAllowlist[handler]; allowed {
			t.Logf("SKIP %s — %s", handler, reason)
			continue
		}

		actions := collectAuthzActions(pkg, handler)
		if len(actions) == 0 {
			noGate = append(noGate, handler)
			continue
		}

		for _, action := range actions {
			actionsSeen[action] = true
			if _, ok := authz.ActionRequirements[action]; !ok {
				unmappedAction = append(unmappedAction, handler+` -> "`+action+`"`)
			}
		}
	}

	if len(noGate) > 0 {
		t.Errorf("\nThe following mutating handlers call neither authz.RequireWithResponse(...) nor authz.Require(...):\n")
		for _, h := range noGate {
			t.Errorf("  - %s", h)
		}
		t.Error("\nAdd an authz.RequireWithResponse(w, r, \"<action>\") gate naming an action present in")
		t.Error("internal/authz/authz.go's ActionRequirements, or add the handler to authzAllowlist in")
		t.Error("authz_coverage_test.go with a justification.")
	}

	if len(unmappedAction) > 0 {
		t.Errorf("\nThe following handlers gate on an action that is NOT a key in authz.ActionRequirements")
		t.Errorf("(it silently falls back to fail-closed admin-only — add it to the table explicitly):\n")
		for _, entry := range unmappedAction {
			t.Errorf("  - %s", entry)
		}
	}

	t.Logf("authz coverage: %d mutating handlers checked, %d distinct actions in use", len(mutatingHandlers), len(actionsSeen))
}

// collectAuthzActions walks the body of func (s *Server) handlerName(...) and
// returns every literal action string passed to authz.RequireWithResponse or
// authz.Require. A handler may legitimately gate more than one action across
// different branches (rare, but collectAuthzActions doesn't assume exactly
// one).
func collectAuthzActions(pkg *ast.Package, handlerName string) []string {
	var actions []string
	for _, file := range pkg.Files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != handlerName || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkgIdent, ok := sel.X.(*ast.Ident)
				if !ok || pkgIdent.Name != "authz" {
					return true
				}

				var actionArgIdx = -1
				switch sel.Sel.Name {
				case "RequireWithResponse":
					actionArgIdx = 2 // (w, r, action)
				case "Require":
					actionArgIdx = 1 // (r, action)
				default:
					return true
				}
				if len(call.Args) <= actionArgIdx {
					return true
				}
				lit, ok := call.Args[actionArgIdx].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				action, err := strconv.Unquote(lit.Value)
				if err != nil {
					return true
				}
				actions = append(actions, action)
				return true
			})
		}
	}
	return actions
}
