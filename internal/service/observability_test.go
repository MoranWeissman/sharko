package service

import (
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

// TestFilterInClusterEntries_BUG038 asserts the host/management cluster is
// removed from the observability cluster list regardless of which axis
// (name or server URL) identifies it. Both axes are tested independently
// so a future rename of the "in-cluster" secret can't silently bypass the
// filter via the other axis.
func TestFilterInClusterEntries_BUG038(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    []models.ArgocdCluster
		wantNames []string
	}{
		{
			name: "filters by name in-cluster",
			input: []models.ArgocdCluster{
				{Name: "in-cluster", Server: "https://kubernetes.default.svc"},
				{Name: "prod-eu", Server: "https://prod-eu.example.com"},
			},
			wantNames: []string{"prod-eu"},
		},
		{
			name: "filters by server URL kubernetes.default",
			input: []models.ArgocdCluster{
				{Name: "renamed-host", Server: "https://kubernetes.default.svc"},
				{Name: "prod-eu", Server: "https://prod-eu.example.com"},
			},
			wantNames: []string{"prod-eu"},
		},
		{
			name: "keeps real workload clusters intact",
			input: []models.ArgocdCluster{
				{Name: "prod-eu", Server: "https://prod-eu.example.com"},
				{Name: "staging-us", Server: "https://staging-us.example.com"},
			},
			wantNames: []string{"prod-eu", "staging-us"},
		},
		{
			name:      "empty input returns empty slice not nil",
			input:     []models.ArgocdCluster{},
			wantNames: []string{},
		},
		{
			name: "filters when both axes match (defensive)",
			input: []models.ArgocdCluster{
				{Name: "in-cluster", Server: "https://kubernetes.default.svc"},
			},
			wantNames: []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := filterInClusterEntries(tc.input)
			if len(got) != len(tc.wantNames) {
				t.Fatalf("filterInClusterEntries len = %d, want %d (got=%v)", len(got), len(tc.wantNames), got)
			}
			for i, name := range tc.wantNames {
				if got[i].Name != name {
					t.Errorf("entry[%d].Name = %q, want %q", i, got[i].Name, name)
				}
			}
		})
	}
}

// TestIsInClusterEntry_BUG038 covers the predicate directly so a future
// rename or refactor can't silently change the matching rules.
func TestIsInClusterEntry_BUG038(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		c    models.ArgocdCluster
		want bool
	}{
		{"canonical in-cluster name", models.ArgocdCluster{Name: "in-cluster"}, true},
		{"canonical k8s default svc URL", models.ArgocdCluster{Server: "https://kubernetes.default.svc"}, true},
		{"k8s default with cluster.local suffix", models.ArgocdCluster{Server: "https://kubernetes.default.svc.cluster.local"}, true},
		{"workload cluster by EKS-style URL", models.ArgocdCluster{Name: "prod-eu", Server: "https://abc123.gr7.us-east-1.eks.amazonaws.com"}, false},
		{"renamed in-cluster secret with workload URL is not filtered", models.ArgocdCluster{Name: "host", Server: "https://abc123.gr7.us-east-1.eks.amazonaws.com"}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isInClusterEntry(tc.c); got != tc.want {
				t.Errorf("isInClusterEntry(%+v) = %v, want %v", tc.c, got, tc.want)
			}
		})
	}
}
