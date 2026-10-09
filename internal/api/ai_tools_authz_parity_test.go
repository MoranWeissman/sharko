package api

// ai_tools_authz_parity_test.go — mechanical proof that every AI write tool's
// authz action (1) is a real, known action in authz.ActionRequirements, and
// (2) is ALSO the action enforced by at least one real REST mutating
// handler — i.e. the tool doesn't gate on a stale/orphan action string that
// only ever existed in the AI layer. Together with TestAuthzCoverage (which
// proves every REST route maps to a table action) this closes the loop: REST
// routes and AI write tools are proven to share the SAME action vocabulary,
// not just two hand-written lists that happen to agree today.
//
// internal/api already depends on internal/ai in production code (agent.go,
// router.go), so this direction of test-only dependency introduces no cycle.
//
// Story v4-8-4 (Wave 2, Epic 8) — the role gate audit.

import (
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/ai"
	"github.com/MoranWeissman/sharko/internal/authz"
)

func TestAIWriteToolsAuthzParity(t *testing.T) {
	toolActions := ai.WriteToolActions()
	if len(toolActions) == 0 {
		t.Fatal("ai.WriteToolActions() returned empty — is the write-tool table still wired up?")
	}

	// Re-derive the full set of actions actually enforced by REST mutating
	// handlers, using the same AST-walk seam as TestAuthzCoverage.
	restActions := collectAllRESTActions(t)

	var unmapped []string // tool action missing from the authz table entirely
	var orphaned []string // tool action in the table but no REST handler uses it
	toolNames := make([]string, 0, len(toolActions))
	for tool := range toolActions {
		toolNames = append(toolNames, tool)
	}
	sort.Strings(toolNames)

	for _, tool := range toolNames {
		action := toolActions[tool]

		if _, ok := authz.ActionRequirements[action]; !ok {
			unmapped = append(unmapped, tool+` -> "`+action+`" (not in authz.ActionRequirements)`)
			continue
		}
		if !restActions[action] {
			orphaned = append(orphaned, tool+` -> "`+action+`" (no REST mutating handler enforces this action)`)
		}
	}

	if len(unmapped) > 0 {
		t.Errorf("\nAI write tools naming an action absent from authz.ActionRequirements (fail-closed to admin, undocumented):\n")
		for _, e := range unmapped {
			t.Errorf("  - %s", e)
		}
	}
	if len(orphaned) > 0 {
		t.Errorf("\nAI write tools naming an action no REST route enforces — parity with \"the equivalent REST")
		t.Errorf("endpoint\" (see writeToolActions doc comment in internal/ai/tools_write.go) cannot be proven:\n")
		for _, e := range orphaned {
			t.Errorf("  - %s", e)
		}
	}

	t.Logf("AI write tools checked: %d, sharing %d distinct actions with REST handlers", len(toolActions), len(restActions))
}

// collectAllRESTActions returns the set of every action literal enforced by
// any registered mutating REST handler in this package (the union of what
// TestAuthzCoverage checks per-handler).
func collectAllRESTActions(t *testing.T) map[string]bool {
	t.Helper()

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

	actions := map[string]bool{}
	for handler := range collectMutatingHandlers(t) {
		for _, action := range collectAuthzActions(pkg, handler) {
			actions[action] = true
		}
	}

	return actions
}
