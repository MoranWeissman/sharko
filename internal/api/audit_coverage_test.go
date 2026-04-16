package api

// audit_coverage_test.go — CI guard that fails if any mutating handler lacks
// audit.Enrich( in its body.
//
// The test:
//  1. Reads all *.go files in internal/api/ (skipping test files).
//  2. Finds every mux.HandleFunc("POST|PUT|PATCH|DELETE ...", srv.handleXXX) route.
//  3. Locates func (s *Server) handleXXX in the package files.
//  4. Checks that the function body contains audit.Enrich(.
//  5. Handlers in the allowlist are skipped.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// auditAllowlist contains handler names that legitimately skip handler-level
// audit.Enrich calls. Each entry must have a justification comment.
var auditAllowlist = map[string]string{
	// Auth — emits fine-grained login/login_failed/logout via s.auditLog.Add;
	// middleware skips these paths explicitly so there is no double-emission.
	"handleLogin":  "emits login / login_failed directly; middleware skips /auth/login",
	"handleLogout": "emits logout directly; middleware skips /auth/logout",

	// Hash — utility endpoint, only available when auth is disabled; not a meaningful audit event.
	"handleHashPassword": "utility endpoint, no meaningful audit event",

	// Heartbeat — system noise (client keep-alive pings on long-running operations).
	"handleOperationHeartbeat": "system noise; no semantic audit value",

	// Mark-all-read — UI state update, not an operator action.
	"handleMarkAllNotificationsRead": "UI state update, not an operator action",

	// Agent chat — potentially high-frequency; skipped per design decision.
	"handleAgentChat": "potentially high-frequency; skip per design decision",

	// Webhooks — emits webhook_received with HMAC context; middleware skips this path.
	"handleGitWebhook": "emits webhook_received directly; middleware skips /webhooks/git",

	// Read-like POSTs — these are queries/analysis that don't mutate state.
	"handleGetAISummary":  "read-only analysis endpoint; POST because it accepts a large body",
	"handleTestAIConfig":  "test-only endpoint that does not persist changes; not a mutating action",
}

// mutatingMethods is the set of HTTP methods we treat as mutating.
var mutatingMethods = map[string]bool{
	"POST":   true,
	"PUT":    true,
	"PATCH":  true,
	"DELETE": true,
}

func TestAuditCoverage(t *testing.T) {
	pkgDir, err := findPackageDir()
	if err != nil {
		t.Fatalf("cannot locate internal/api package: %v", err)
	}

	// Parse all non-test .go files in the package.
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

	// Step 1: collect handler names registered for mutating methods.
	mutatingHandlers := collectMutatingHandlers(pkg)

	// Step 2: for each handler, verify audit.Enrich presence.
	var missing []string
	for handler := range mutatingHandlers {
		if reason, allowed := auditAllowlist[handler]; allowed {
			t.Logf("SKIP %s — %s", handler, reason)
			continue
		}
		if !handlerHasEnrich(pkg, handler) {
			missing = append(missing, handler)
		}
	}

	if len(missing) > 0 {
		t.Errorf("\nThe following mutating handlers are missing audit.Enrich(...) calls:\n")
		for _, h := range missing {
			t.Errorf("  - %s", h)
		}
		t.Error("\nAdd audit.Enrich(r.Context(), audit.Fields{Event: \"...\", ...}) before writing the response,")
		t.Error("or add the handler to auditAllowlist in audit_coverage_test.go with a justification.")
	}
}

// collectMutatingHandlers scans AST for mux.HandleFunc calls and extracts
// handler names for POST/PUT/PATCH/DELETE routes.
func collectMutatingHandlers(pkg *ast.Package) map[string]struct{} {
	handlers := make(map[string]struct{})

	for _, file := range pkg.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			// Match mux.HandleFunc(...)
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "HandleFunc" {
				return true
			}
			if len(call.Args) < 2 {
				return true
			}

			// First arg is the pattern, e.g. "POST /api/v1/clusters"
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok {
				return true
			}
			pattern := strings.Trim(lit.Value, `"`)
			parts := strings.Fields(pattern)
			if len(parts) < 2 {
				return true
			}
			method := parts[0]
			if !mutatingMethods[method] {
				return true
			}

			// Second arg is srv.handleXXX — extract the handler name.
			handlerExpr := call.Args[1]
			handlerName := extractHandlerName(handlerExpr)
			if handlerName != "" {
				handlers[handlerName] = struct{}{}
			}
			return true
		})
	}
	return handlers
}

// extractHandlerName extracts the function name from a selector expression like
// srv.handleXXX, or from a func literal wrapping a direct call.
func extractHandlerName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.SelectorExpr:
		// srv.handleXXX
		return e.Sel.Name
	case *ast.FuncLit:
		// Inline func literal — scan for inner handleXXX call
		var found string
		ast.Inspect(e.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				name := sel.Sel.Name
				if strings.HasPrefix(name, "handle") {
					found = name
					return false
				}
			}
			return true
		})
		return found
	}
	return ""
}

// handlerHasEnrich checks whether the function body for handlerName contains
// a call to audit.Enrich(.
func handlerHasEnrich(pkg *ast.Package, handlerName string) bool {
	for _, file := range pkg.Files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != handlerName || fn.Body == nil {
				continue
			}
			if bodyContainsEnrich(fn.Body) {
				return true
			}
		}
	}
	return false
}

// bodyContainsEnrich walks the function body AST and returns true if any
// call to audit.Enrich is present.
func bodyContainsEnrich(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if sel.Sel.Name == "Enrich" {
			// Check that the receiver is the "audit" package.
			if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "audit" {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

// findPackageDir locates the internal/api directory relative to this test file.
func findPackageDir() (string, error) {
	// __file__ is not available in Go tests, so we walk up from cwd.
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	// Tests run with cwd = package directory, so we can check relative paths.
	candidate := filepath.Clean(dir)
	if _, err := os.Stat(filepath.Join(candidate, "router.go")); err == nil {
		return candidate, nil
	}
	// Fall back: walk up looking for internal/api
	for i := 0; i < 6; i++ {
		candidate := filepath.Join(dir, "internal", "api")
		if _, err := os.Stat(filepath.Join(candidate, "router.go")); err == nil {
			return candidate, nil
		}
		dir = filepath.Dir(dir)
	}
	return "", os.ErrNotExist
}
