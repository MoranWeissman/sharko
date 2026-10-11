package demo

import (
	"context"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// V124-2.2 — the demo must serve a managed-clusters file, or
// GET /api/v1/clusters returns 500 "file not found". The demo repo is v4
// only, so the file lives at the v4 path (orchestrator.V4ManagedClustersPath),
// not the old configuration/managed-clusters.yaml.
func TestMockGitProvider_SeedsManagedClustersYAML(t *testing.T) {
	p := NewMockGitProvider()
	data, err := p.GetFileContent(context.Background(), orchestrator.V4ManagedClustersPath, "main")
	if err != nil {
		t.Fatalf("seed missing: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("seeded file is empty")
	}

	// Sanity-check that the seed contains real cluster entries (not just
	// "clusters: []") so the demo experience shows recognisable rows.
	body := string(data)
	if !strings.Contains(body, "clusters:") {
		t.Errorf("seed missing top-level clusters: key:\n%s", body)
	}
	if !strings.Contains(body, "prod-eu") {
		t.Errorf("seed missing prod-eu cluster:\n%s", body)
	}
	if !strings.Contains(body, "staging-eu") {
		t.Errorf("seed missing staging-eu cluster:\n%s", body)
	}
}

// TestDemoRepo_IsOneLayoutOnly checks the demo repo holds the v4 layout and
// nothing from the old v3 layout. A repo with both is one Sharko refuses to
// write to (orchestrator.ErrMixedRepoLayout), and that is exactly how every
// demo write broke: the demo seeded both. This checks the default demo and
// a generated (bigger) one, since they are built by different code.
func TestDemoRepo_IsOneLayoutOnly(t *testing.T) {
	generated, err := NewMockGitProviderWithConfig(ScaleConfig{Clusters: 6, Addons: 4, Seed: 1})
	if err != nil {
		t.Fatalf("NewMockGitProviderWithConfig: %v", err)
	}
	cases := map[string]*MockGitProvider{
		"default":   NewMockGitProvider(),
		"generated": generated,
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			if pin, err := p.GetFileContent(ctx, orchestrator.EnginePinPath, "main"); err != nil || len(pin) == 0 {
				t.Fatalf("engine pin %s missing (err=%v): the demo would not look like a v4 repo", orchestrator.EnginePinPath, err)
			}
			if orchestrator.HasV3Markers(ctx, p, "main") {
				t.Errorf("demo repo has a v3 marker file next to the v4 engine pin: every write would fail with ErrMixedRepoLayout")
			}
			for path := range p.files {
				if strings.HasPrefix(path, "configuration/") || strings.HasPrefix(path, "bootstrap/") {
					t.Errorf("demo repo carries old v3 file %s", path)
				}
			}
		})
	}
}
