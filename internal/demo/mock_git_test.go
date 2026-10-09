package demo

import (
	"context"
	"strings"
	"testing"
)

// V124-2.2 — confirm the demo mock provider now serves
// configuration/managed-clusters.yaml. Before the fix the demo only seeded
// configuration/cluster-addons.yaml, which made GET /api/v1/clusters
// return 500 with "file not found: configuration/managed-clusters.yaml".
// This test pins the shape of the seeded file so a future rename of the
// expected path immediately surfaces in CI.
func TestMockGitProvider_SeedsManagedClustersYAML(t *testing.T) {
	p := NewMockGitProvider()
	data, err := p.GetFileContent(context.Background(), "configuration/managed-clusters.yaml", "main")
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
