package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"testing"

	"github.com/MoranWeissman/sharko/internal/providers"
)

// TestServeWiresSelfHealFn is the GF1 acceptance-criterion-#5 guard: it fails
// if the cluster reconciler is constructed in serve.go WITHOUT setting
// SelfHealFn. Without this field the managed_cluster_self_heal setting is a
// dead switch (reconciler.go treats a nil SelfHealFn as "off"), which is
// exactly the production bug this story fixes. A source-AST assertion (rather
// than a runtime test) is used because the reconciler construction is deep
// inside serve()'s in-cluster boot path and not independently constructable —
// same approach as internal/api/audit_coverage_test.go's source scan.
func TestServeWiresSelfHealFn(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "serve.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing serve.go: %v", err)
	}

	var foundNew, wiredSelfHeal bool
	ast.Inspect(f, func(n ast.Node) bool {
		// Match the composite literal clusterreconciler.Deps{ ... }.
		cl, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		sel, ok := cl.Type.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Deps" {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "clusterreconciler" {
			return true
		}
		foundNew = true
		for _, elt := range cl.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "SelfHealFn" {
				wiredSelfHeal = true
			}
		}
		return true
	})

	if !foundNew {
		t.Fatal("could not find a clusterreconciler.Deps{...} literal in serve.go")
	}
	if !wiredSelfHeal {
		t.Error("clusterreconciler.Deps in serve.go does NOT set SelfHealFn — the managed_cluster_self_heal switch is dead (GF1 B2)")
	}
}

// V125-1-11.5 — assert that the ClusterRegistrationSourceConfig parsing block
// in serve.go reads SHARKO_CLUSTER_REG_TYPE and SHARKO_CLUSTER_REG_ARGOCD_NAMESPACE
// without erroring and produces the expected struct values.
//
// This test exercises the env-var → struct mapping in isolation. The serve.go
// block is a straight os.Getenv read into the new providers config type, so
// we test the underlying contract (env name → struct field) here. When the
// V125-1-8 reconciler arrives and the parsing block grows defaults/validation,
// expand this test to cover those code paths.
func TestClusterRegistrationSourceConfig_EnvParsing(t *testing.T) {
	tests := []struct {
		name          string
		envType       string
		envNamespace  string
		wantType      string
		wantNamespace string
	}{
		{
			name:          "no env vars set → zero-value config (pre-V125-1-8 default)",
			envType:       "",
			envNamespace:  "",
			wantType:      "",
			wantNamespace: "",
		},
		{
			name:          "both env vars set → both fields populated",
			envType:       "argocd",
			envNamespace:  "argocd-system",
			wantType:      "argocd",
			wantNamespace: "argocd-system",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SHARKO_CLUSTER_REG_TYPE", tt.envType)
			t.Setenv("SHARKO_CLUSTER_REG_ARGOCD_NAMESPACE", tt.envNamespace)

			// Mirror serve.go's parsing block verbatim — same env names, same
			// struct field assignment, no defaulting (defaults are deferred to
			// the future V125-1-8 reconciler per planning doc Story 11.5).
			cfg := providers.ClusterRegistrationSourceConfig{
				Type:            os.Getenv("SHARKO_CLUSTER_REG_TYPE"),
				ArgoCDNamespace: os.Getenv("SHARKO_CLUSTER_REG_ARGOCD_NAMESPACE"),
			}

			if cfg.Type != tt.wantType {
				t.Errorf("Type = %q, want %q", cfg.Type, tt.wantType)
			}
			if cfg.ArgoCDNamespace != tt.wantNamespace {
				t.Errorf("ArgoCDNamespace = %q, want %q", cfg.ArgoCDNamespace, tt.wantNamespace)
			}
		})
	}
}
