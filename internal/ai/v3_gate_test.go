package ai

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
)

// v3GateGP is a GitProvider fake that serves fixed file content by path
// (ignoring branch on reads — the ref actually requested is what matters
// for the tests, recorded in getFileContentCalls) and records every write
// call the v3 write tools could make, so a refused write can be proven to
// have touched the provider zero times.
type v3GateGP struct {
	files map[string][]byte

	getFileContentCalls []refCall
	createBranchCalls   []refCall // Ref field holds the "from" branch
	createFileCalls     []string
	createPRCalls       []refCall // Ref field holds the base branch

	nextPRID int
}

type refCall struct {
	path string
	ref  string
}

func (g *v3GateGP) GetFileContent(_ context.Context, path, ref string) ([]byte, error) {
	g.getFileContentCalls = append(g.getFileContentCalls, refCall{path, ref})
	if b, ok := g.files[path]; ok && len(b) > 0 {
		return b, nil
	}
	return nil, errFakeProvider
}
func (g *v3GateGP) ListDirectory(_ context.Context, _ string, _ string) ([]string, error) {
	return nil, errFakeProvider
}
func (g *v3GateGP) ListPullRequests(_ context.Context, _ string) ([]gitprovider.PullRequest, error) {
	return nil, errFakeProvider
}
func (g *v3GateGP) TestConnection(_ context.Context) error { return errFakeProvider }
func (g *v3GateGP) CreateBranch(_ context.Context, name, from string) error {
	g.createBranchCalls = append(g.createBranchCalls, refCall{name, from})
	return nil
}
func (g *v3GateGP) CreateOrUpdateFile(_ context.Context, path string, content []byte, _ string, _ string) error {
	g.createFileCalls = append(g.createFileCalls, path)
	if g.files == nil {
		g.files = map[string][]byte{}
	}
	g.files[path] = content
	return nil
}
func (g *v3GateGP) BatchCreateFiles(_ context.Context, _ map[string][]byte, _ string, _ string) error {
	return errFakeProvider
}
func (g *v3GateGP) DeleteFile(_ context.Context, _ string, _ string, _ string) error {
	return errFakeProvider
}
func (g *v3GateGP) CreatePullRequest(_ context.Context, _, _, head, base string) (*gitprovider.PullRequest, error) {
	g.nextPRID++
	g.createPRCalls = append(g.createPRCalls, refCall{head, base})
	return &gitprovider.PullRequest{ID: g.nextPRID, URL: "https://example.invalid/pr/" + strconv.Itoa(g.nextPRID)}, nil
}
func (g *v3GateGP) MergePullRequest(_ context.Context, _ int) error { return errFakeProvider }
func (g *v3GateGP) GetPullRequestStatus(_ context.Context, _ int) (string, error) {
	return "", errFakeProvider
}
func (g *v3GateGP) DeleteBranch(_ context.Context, _ string) error { return errFakeProvider }

func (g *v3GateGP) writeCallCount() int {
	return len(g.createBranchCalls) + len(g.createFileCalls) + len(g.createPRCalls)
}

var _ gitprovider.GitProvider = (*v3GateGP)(nil)

var testManagedClustersYAML = []byte(`apiVersion: sharko.dev/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: prod-eu
      labels:
        keda: disabled
`)

var testCatalogYAML = []byte(`apiVersion: sharko.dev/v1
kind: AddonCatalog
metadata:
  name: addons-catalog
spec:
  applicationsets:
    - name: keda
      version: "2.13.0"
      chart: keda
      repoURL: https://kedacore.github.io/charts
      namespace: keda
`)

// TestWriteTools_RefuseOnV3RepoWithMarker — a v3 marker present (here, the
// default managed-clusters.yaml path every already-set-up v3 repo has) with
// no v4 engine pin is exactly orchestrator.RefuseOnV3Repo's "migrate to
// continue" state (PR #636). Before this fix these three tools had no gate
// at all and would write configuration/managed-clusters.yaml or
// configuration/addons-catalog.yaml directly, bypassing the same refusal
// every REST v3 writer already enforces.
func TestWriteTools_RefuseOnV3RepoWithMarker(t *testing.T) {
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
				"configuration/managed-clusters.yaml": testManagedClustersYAML,
				"configuration/addons-catalog.yaml":   testCatalogYAML,
			}}
			e := NewToolExecutor(gp, nil, nil, nil, "")

			out, err := tc.call(context.Background(), e)
			if err != nil {
				t.Fatalf("unexpected hard error: %v", err)
			}
			if out != v3MigrationRequiredMessage {
				t.Errorf("result = %q, want the shared v3-migration refusal %q", out, v3MigrationRequiredMessage)
			}
			if n := gp.writeCallCount(); n != 0 {
				t.Errorf("gate refused but the provider still saw %d write call(s): branch=%v file=%v pr=%v",
					n, gp.createBranchCalls, gp.createFileCalls, gp.createPRCalls)
			}
		})
	}
}

// TestWriteTools_ProceedWhenNoV3MarkersPresent — orchestrator.RefuseOnV3Repo's
// "empty repo" case: neither the v4 engine pin nor a v3 marker is present,
// so the gate is inert and the tool proceeds exactly as before this fix,
// all the way through to a real PR.
func TestWriteTools_ProceedWhenNoV3MarkersPresent(t *testing.T) {
	t.Run("enable_addon", func(t *testing.T) {
		// A non-default managedClustersPath keeps the data file distinct
		// from the fixed v3 marker path (configuration/managed-
		// clusters.yaml), so this fixture genuinely carries no v3 marker.
		gp := &v3GateGP{files: map[string][]byte{"custom/clusters.yaml": testManagedClustersYAML}}
		e := NewToolExecutor(gp, nil, nil, nil, "custom/clusters.yaml")

		out, err := e.enableAddon(context.Background(), "", "prod-eu", "keda")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(out, "Pull request created:") {
			t.Errorf("result = %q, want a created-PR confirmation", out)
		}
		if len(gp.createBranchCalls) != 1 || len(gp.createFileCalls) != 1 || len(gp.createPRCalls) != 1 {
			t.Errorf("expected exactly one branch/file/PR write, got branch=%v file=%v pr=%v",
				gp.createBranchCalls, gp.createFileCalls, gp.createPRCalls)
		}
	})

	t.Run("update_addon_version", func(t *testing.T) {
		// update_addon_version never touches managed-clusters.yaml at all,
		// so the default executor (no marker files present) already has
		// no v3 marker in this fixture.
		gp := &v3GateGP{files: map[string][]byte{"configuration/addons-catalog.yaml": testCatalogYAML}}
		e := NewToolExecutor(gp, nil, nil, nil, "")

		out, err := e.updateAddonVersion(context.Background(), "", "keda", "2.14.0")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(out, "Pull request created:") {
			t.Errorf("result = %q, want a created-PR confirmation", out)
		}
		if len(gp.createBranchCalls) != 1 || len(gp.createFileCalls) != 1 || len(gp.createPRCalls) != 1 {
			t.Errorf("expected exactly one branch/file/PR write, got branch=%v file=%v pr=%v",
				gp.createBranchCalls, gp.createFileCalls, gp.createPRCalls)
		}
	})
}

// TestWriteTools_DoNotDoubleRefuseOnV4Repo — mirrors the doc comment on
// orchestrator.RefuseOnV3Repo: "v4 repo (engine pin present) -> nil. The v4
// guards own that case; this gate must not double-refuse." A v4 engine pin
// present (even alongside a leftover v3 marker file) must never produce the
// v3-migration message.
func TestWriteTools_DoNotDoubleRefuseOnV4Repo(t *testing.T) {
	gp := &v3GateGP{files: map[string][]byte{
		V4EnginePinPath:                       []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n"),
		"configuration/managed-clusters.yaml": testManagedClustersYAML,
	}}
	e := NewToolExecutor(gp, nil, nil, nil, "")

	out, err := e.enableAddon(context.Background(), "", "prod-eu", "keda")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == v3MigrationRequiredMessage {
		t.Errorf("the v3 gate fired on a v4-pinned repo: %q", out)
	}
}

// TestWriteTools_UseConfiguredBaseBranch proves the three write tools read,
// branch from, and open pull requests against the connection's configured
// GitOps base branch (wired via SetBaseBranchFn) rather than a hardcoded
// "main" — the second half of H-3.
func TestWriteTools_UseConfiguredBaseBranch(t *testing.T) {
	gp := &v3GateGP{files: map[string][]byte{"custom/clusters.yaml": testManagedClustersYAML}}
	e := NewToolExecutor(gp, nil, nil, nil, "custom/clusters.yaml")
	e.SetBaseBranchFn(func() string { return "release" })

	out, err := e.enableAddon(context.Background(), "", "prod-eu", "keda")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Pull request created:") {
		t.Fatalf("result = %q, want a created-PR confirmation", out)
	}

	for _, c := range gp.getFileContentCalls {
		if c.ref != "release" {
			t.Errorf("GetFileContent(%q) used ref %q, want the configured branch %q", c.path, c.ref, "release")
		}
	}
	if len(gp.createBranchCalls) != 1 {
		t.Fatalf("expected exactly one CreateBranch call, got %v", gp.createBranchCalls)
	}
	if gp.createBranchCalls[0].ref != "release" {
		t.Errorf("CreateBranch was cut from %q, want %q", gp.createBranchCalls[0].ref, "release")
	}
	if len(gp.createPRCalls) != 1 {
		t.Fatalf("expected exactly one CreatePullRequest call, got %v", gp.createPRCalls)
	}
	if gp.createPRCalls[0].ref != "release" {
		t.Errorf("CreatePullRequest base = %q, want %q", gp.createPRCalls[0].ref, "release")
	}
}

// TestToolExecutor_BranchDefaultsToMain confirms the zero-value seam (no
// SetBaseBranchFn call — e.g. tests and any caller that never wires it in)
// falls back to "main", matching AddonService.branch()'s identical contract.
func TestToolExecutor_BranchDefaultsToMain(t *testing.T) {
	e := &ToolExecutor{}
	if got := e.branch(); got != "main" {
		t.Errorf("branch() with no seam wired = %q, want %q", got, "main")
	}
	e.SetBaseBranchFn(func() string { return "" })
	if got := e.branch(); got != "main" {
		t.Errorf("branch() with a seam resolving to empty = %q, want %q", got, "main")
	}
}
