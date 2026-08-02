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

// TestWriteTools_RefuseOnV4Repo — VERIFY step for the "do the AI catalog
// write tools still emit v3-shaped writes on a v4 repo" flag: on a v4 repo
// (engine pin present) all three legacy-shape write tools
// (enable_addon/disable_addon/update_addon_version, which mutate
// managed-clusters.yaml labels and addons-catalog.yaml directly — the v3
// data shape) must refuse via refuseV4Write BEFORE any git write, the same
// way TestWriteTools_RefuseOnV3RepoWithMarker proves the v3 gate does.
// TestWriteTools_DoNotDoubleRefuseOnV4Repo above already proves the v3
// message does not fire here; this test pins the positive half — that the
// v4 refusal DOES fire, with the right message, and zero writes reach the
// provider — for all three tools, not just enable_addon.
func TestWriteTools_RefuseOnV4Repo(t *testing.T) {
	cases := []struct {
		name string
		call func(ctx context.Context, e *ToolExecutor) (string, error)
	}{
		{"enable_addon", func(ctx context.Context, e *ToolExecutor) (string, error) {
			return e.enableAddon(ctx, "", "prod-eu", "keda")
		}},
		{"disable_addon", func(ctx context.Context, e *ToolExecutor) (string, error) {
			return e.disableAddon(ctx, "", "prod-eu", "keda")
		}},
		{"update_addon_version", func(ctx context.Context, e *ToolExecutor) (string, error) {
			return e.updateAddonVersion(ctx, "", "keda", "2.14.0")
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gp := &v3GateGP{files: map[string][]byte{
				V4EnginePinPath:                       []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n"),
				"configuration/managed-clusters.yaml": testManagedClustersYAML,
				"configuration/addons-catalog.yaml":   testCatalogYAML,
			}}
			e := NewToolExecutor(gp, nil, nil, nil, "")

			out, err := tc.call(context.Background(), e)
			if err != nil {
				t.Fatalf("unexpected hard error: %v", err)
			}
			if out != v4WriteUnsupportedMessage {
				t.Errorf("result = %q, want the shared v4-unsupported refusal %q", out, v4WriteUnsupportedMessage)
			}
			if n := gp.writeCallCount(); n != 0 {
				t.Errorf("gate refused but the provider still saw %d write call(s): branch=%v file=%v pr=%v — "+
					"this tool would have written the v3 shape into a v4 repo",
					n, gp.createBranchCalls, gp.createFileCalls, gp.createPRCalls)
			}
		})
	}
}
