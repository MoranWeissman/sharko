package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
	"github.com/MoranWeissman/sharko/internal/providers"
	"github.com/MoranWeissman/sharko/internal/secrets"
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

// TestTriggerMergeReconcilers is the "secrets push on enable-merge" story's
// direct behavioral test: an enable-PR merge must nudge the addon-secrets
// reconciler immediately, not just the ArgoCD cluster-Secret reconciler —
// before this fix, a freshly-enabled addon's secret could sit unwritten on
// the remote cluster for up to SHARKO_SECRET_RECONCILE_INTERVAL (5m default)
// waiting on the timer.
//
// Trigger() on both reconcilers is a non-blocking send on a buffered-1
// channel (see internal/clusterreconciler and internal/secrets), so this
// test proves two things without needing a running server: nil reconcilers
// (not wired for this run) are skipped without panicking, and real
// reconcilers accept the nudge — twice in a row, which is exactly what a
// merge followed by another quick merge would do — without blocking.
func TestTriggerMergeReconcilers(t *testing.T) {
	t.Run("nil reconcilers are skipped, not a panic", func(t *testing.T) {
		triggerMergeReconcilers(nil, nil)
	})

	t.Run("real reconcilers accept the trigger without blocking, even twice", func(t *testing.T) {
		clusterRecon := clusterreconciler.New(clusterreconciler.Deps{})
		secretRecon := secrets.NewReconciler(nil, nil, func() secrets.GitReader { return nil }, nil, nil, "main", "", 0)

		done := make(chan struct{})
		go func() {
			triggerMergeReconcilers(clusterRecon, secretRecon)
			// A second merge landing before the first tick fires must not
			// block on the already-full buffered-1 channel.
			triggerMergeReconcilers(clusterRecon, secretRecon)
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("triggerMergeReconcilers blocked — Trigger() is no longer a non-blocking send")
		}
	})
}

// TestServeWiresSecretReconTrigger is the source-scan companion to
// TestTriggerMergeReconcilers: it proves the fan-out is actually WIRED into
// the merge callback in serve.go, not just correct in isolation. Same
// approach as TestServeWiresSelfHealFn above — the wiring lives deep inside
// serve()'s RunE closure and is not independently constructable.
func TestServeWiresSecretReconTrigger(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "serve.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing serve.go: %v", err)
	}

	var foundOnMergeFn, callsHelperWithBothArgs bool
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "SetOnMergeFn" {
			return true
		}
		foundOnMergeFn = true
		// Walk the SetOnMergeFn(func(pr prtracker.PRInfo) { ... }) body
		// looking for a call to triggerMergeReconcilers naming both
		// clusterRecon and secretRecon.
		ast.Inspect(call, func(inner ast.Node) bool {
			innerCall, ok := inner.(*ast.CallExpr)
			if !ok {
				return true
			}
			ident, ok := innerCall.Fun.(*ast.Ident)
			if !ok || ident.Name != "triggerMergeReconcilers" {
				return true
			}
			var sawCluster, sawSecret bool
			for _, arg := range innerCall.Args {
				if argIdent, ok := arg.(*ast.Ident); ok {
					switch argIdent.Name {
					case "clusterRecon":
						sawCluster = true
					case "secretRecon":
						sawSecret = true
					}
				}
			}
			if sawCluster && sawSecret {
				callsHelperWithBothArgs = true
			}
			return true
		})
		return true
	})

	if !foundOnMergeFn {
		t.Fatal("could not find a prTracker.SetOnMergeFn(...) call in serve.go")
	}
	if !callsHelperWithBothArgs {
		t.Error("SetOnMergeFn's callback does NOT call triggerMergeReconcilers(clusterRecon, secretRecon) — " +
			"an enable-PR merge would not nudge the addon-secrets reconciler")
	}
}
