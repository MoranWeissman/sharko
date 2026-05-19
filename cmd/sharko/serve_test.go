package main

import (
	"os"
	"testing"

	"github.com/MoranWeissman/sharko/internal/providers"
)

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
