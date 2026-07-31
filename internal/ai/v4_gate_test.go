package ai

import (
	"context"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
)

// v4PinProvider serves the engine pin (so the repo looks v4) and nothing
// else; every v3 values path 404s, which is exactly what a real v4 repo
// does.
type v4PinProvider struct {
	failingProvider
	v4        bool
	readPaths []string
}

func (p *v4PinProvider) GetFileContent(_ context.Context, path, _ string) ([]byte, error) {
	p.readPaths = append(p.readPaths, path)
	if path == V4EnginePinPath && p.v4 {
		return []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n"), nil
	}
	return nil, errFakeProvider
}

var _ gitprovider.GitProvider = (*v4PinProvider)(nil)

// TestValuesTools_SayV4LayoutIsUnsupported — on a v4 repo the v3 values
// paths hold nothing, so the old code answered "No global values file found
// for cert-manager" for an addon that has values. That is a confident wrong
// answer the model then relays as fact. The tools now say what is actually
// true.
func TestValuesTools_SayV4LayoutIsUnsupported(t *testing.T) {
	cases := []struct {
		name string
		call func(ctx context.Context, e *ToolExecutor) (string, error)
	}{
		{"get_addon_values", func(ctx context.Context, e *ToolExecutor) (string, error) {
			return e.getAddonValues(ctx, "cert-manager")
		}},
		{"get_cluster_values", func(ctx context.Context, e *ToolExecutor) (string, error) {
			return e.getClusterValues(ctx, "prod-eu")
		}},
		{"get_addon_config_on_cluster", func(ctx context.Context, e *ToolExecutor) (string, error) {
			return e.getAddonConfigOnCluster(ctx, "cert-manager", "prod-eu")
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gp := &v4PinProvider{v4: true}
			e := NewToolExecutor(gp, nil, nil, nil, "")

			out, err := tc.call(context.Background(), e)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if out != v4ValuesUnsupportedMessage {
				t.Errorf("result = %q, want the shared plain-words v4 message", out)
			}
			for _, p := range gp.readPaths {
				if strings.HasPrefix(p, "configuration/addons-") {
					t.Errorf("the tool still read a v3 values path %q on a v4 repo", p)
				}
			}
		})
	}
}

// TestValuesTools_UnchangedOnV3Repo — with no engine pin the gate is inert
// and the tools keep their existing "not found" wording.
func TestValuesTools_UnchangedOnV3Repo(t *testing.T) {
	gp := &v4PinProvider{v4: false}
	e := NewToolExecutor(gp, nil, nil, nil, "")

	out, err := e.getAddonValues(context.Background(), "cert-manager")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == v4ValuesUnsupportedMessage {
		t.Fatalf("the v4 gate fired on a v3 repo: %q", out)
	}
	if !strings.Contains(out, "No global values file found") {
		t.Errorf("result = %q, want the pre-existing not-found wording", out)
	}
}
