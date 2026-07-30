package orchestrator

import (
	"context"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

// TestRegisterCluster_V4Repo_WritesFleetConnectionsNoValuesFile is the v4
// Wave 1 Story 4.4 regression guard: on a v4 repo (engine pin present),
// RegisterCluster must write the connection record to
// fleet/connections.yaml (design doc §2.4) instead of
// configuration/managed-clusters.yaml, must NOT write a combined
// per-cluster values file (that concept does not exist in v4 — values
// live under values/global|clusters/, written by EnableAddonV4), and must
// NOT author any addon labels on the connection record (v4 addon state
// lives in clusters/<name>.yaml exclusively).
func TestRegisterCluster_V4Repo_WritesFleetConnectionsNoValuesFile(t *testing.T) {
	git := newMockGitProvider()
	git.files[EnginePinPath] = []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n")

	orch := New(nil, defaultCreds(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name: "prod-eu",
		// Addons supplied but must be silently ignored on a v4 repo.
		Addons: map[string]bool{"cert-manager": true},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status == "partial" && result.Error != "" {
		t.Fatalf("unexpected partial result: %+v", result)
	}

	// fleet/connections.yaml written, and it round-trips through the same
	// ManagedClusters reader v3 uses (design doc §2.4 — "same shape").
	connBytes, ok := git.files[V4ConnectionsPath]
	if !ok {
		t.Fatalf("expected %s to be written; got files: %v", V4ConnectionsPath, keysOf(git.files))
	}
	spec, err := models.LoadManagedClusters(connBytes)
	if err != nil {
		t.Fatalf("fleet/connections.yaml failed to parse as ManagedClusters: %v", err)
	}
	found := false
	for _, c := range spec.Clusters {
		if c.Name == "prod-eu" {
			found = true
			if len(c.Labels) != 0 {
				t.Errorf("expected no addon labels on the v4 connection record, got %v", c.Labels)
			}
		}
	}
	if !found {
		t.Fatal("expected a prod-eu entry in fleet/connections.yaml")
	}

	// The v3 file must be untouched.
	if _, ok := git.files["configuration/managed-clusters.yaml"]; ok {
		t.Error("v3 configuration/managed-clusters.yaml must not be written on a v4 repo")
	}

	// No combined per-cluster values file.
	valuesPath := defaultPaths().ClusterValues + "/prod-eu.yaml"
	if _, ok := git.files[valuesPath]; ok {
		t.Errorf("expected no combined values file at %s on a v4 repo", valuesPath)
	}
}

// TestRegisterCluster_V3Repo_UnaffectedByV4Detection is the regression
// guard in the other direction: a v3 repo (no engine pin) must behave
// exactly as before — combined values file written, addon labels
// authored on managed-clusters.yaml.
func TestRegisterCluster_V3Repo_UnaffectedByV4Detection(t *testing.T) {
	git := newMockGitProvider()
	orch := New(nil, defaultCreds(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)

	_, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:   "prod-eu",
		Addons: map[string]bool{"cert-manager": true},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := git.files["configuration/managed-clusters.yaml"]; !ok {
		t.Error("expected v3 configuration/managed-clusters.yaml to be written")
	}
	if _, ok := git.files[V4ConnectionsPath]; ok {
		t.Error("v4 fleet/connections.yaml must not be written on a v3 repo")
	}
	valuesPath := defaultPaths().ClusterValues + "/prod-eu.yaml"
	if _, ok := git.files[valuesPath]; !ok {
		t.Errorf("expected the v3 combined values file at %s", valuesPath)
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
