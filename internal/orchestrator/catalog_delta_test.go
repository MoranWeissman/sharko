package orchestrator

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/MoranWeissman/sharko/internal/config"
)

func newTestOrchestratorForCatalogDelta(t *testing.T, git *mockGitProvider) *Orchestrator {
	t.Helper()
	return New(
		&sync.Mutex{},
		nil,
		newMockArgocd(),
		git,
		GitOpsConfig{BranchPrefix: "sharko/", CommitPrefix: "sharko:", BaseBranch: "main"},
		RepoPathsConfig{},
		nil,
	)
}

// TestAddInternalAddon_CreatesDeltaFile_MissingFileTreatedAsEmpty is the
// Story 3.3 AC's "private OCI chart reference added through any door" case,
// on a repo with no catalog/addons.yaml yet — design doc D16, "missing
// means empty": the write must succeed, creating the file rather than
// failing on the read.
func TestAddInternalAddon_CreatesDeltaFile_MissingFileTreatedAsEmpty(t *testing.T) {
	git := newMockGitProvider()
	o := newTestOrchestratorForCatalogDelta(t, git)

	result, err := o.AddInternalAddon(context.Background(), AddInternalAddonRequest{
		Name:      "billing-api",
		RepoURL:   "oci://registry.example.com/charts",
		Chart:     "billing-api",
		Version:   "2.4.0",
		Namespace: "billing",
	})
	if err != nil {
		t.Fatalf("AddInternalAddon: %v", err)
	}
	if result == nil || result.PRUrl == "" {
		t.Fatalf("expected a PR result, got %+v", result)
	}

	written, ok := git.files[config.AddonCatalogDeltaPath]
	if !ok {
		t.Fatalf("expected %s to be written", config.AddonCatalogDeltaPath)
	}
	spec, err := config.LoadAddonCatalogDelta(written)
	if err != nil {
		t.Fatalf("LoadAddonCatalogDelta on written file: %v", err)
	}
	entry, ok := spec.Addons["billing-api"]
	if !ok {
		t.Fatalf("expected billing-api entry in written delta, got %+v", spec.Addons)
	}
	if entry.RepoURL != "oci://registry.example.com/charts" || entry.Chart != "billing-api" || entry.Version != "2.4.0" || entry.Namespace != "billing" {
		t.Errorf("written entry fields wrong: %+v", entry)
	}
}

// TestAddInternalAddon_UpsertsExistingDelta confirms a second call updates
// the entry in place and preserves any OTHER addon already in the delta —
// the file "never holds a copy of the curated set" but it certainly holds
// every addon the user has already added.
func TestAddInternalAddon_UpsertsExistingDelta(t *testing.T) {
	git := newMockGitProvider()
	existing := `# yaml-language-server: $schema=https://raw.githubusercontent.com/MoranWeissman/sharko/main/docs/schemas/addon-catalog-delta.v1.json
apiVersion: sharko.dev/v1
kind: AddonCatalogDelta
metadata:
  name: addon-catalog-delta
spec:
  addons:
    cert-manager:
      version: "1.14.5"
    billing-api:
      repoURL: oci://registry.example.com/charts
      chart: billing-api
      version: "2.0.0"
`
	git.files[config.AddonCatalogDeltaPath] = []byte(existing)

	o := newTestOrchestratorForCatalogDelta(t, git)
	_, err := o.AddInternalAddon(context.Background(), AddInternalAddonRequest{
		Name:    "billing-api",
		RepoURL: "oci://registry.example.com/charts",
		Chart:   "billing-api",
		Version: "2.4.0",
	})
	if err != nil {
		t.Fatalf("AddInternalAddon: %v", err)
	}

	written := git.files[config.AddonCatalogDeltaPath]
	spec, err := config.LoadAddonCatalogDelta(written)
	if err != nil {
		t.Fatalf("LoadAddonCatalogDelta: %v", err)
	}
	if spec.Addons["billing-api"].Version != "2.4.0" {
		t.Errorf("billing-api version not updated: %+v", spec.Addons["billing-api"])
	}
	if spec.Addons["cert-manager"].Version != "1.14.5" {
		t.Errorf("cert-manager entry lost on upsert: %+v", spec.Addons["cert-manager"])
	}
}

// TestAddInternalAddon_RequiredFields locks the request-time validation:
// name, repo_url, chart, and version are all required — mirrors the
// merge-time enforcement in catalog.MergeDelta so a caller gets the clearer
// error as early as possible.
func TestAddInternalAddon_RequiredFields(t *testing.T) {
	cases := []struct {
		name string
		req  AddInternalAddonRequest
		want string
	}{
		{"missing name", AddInternalAddonRequest{RepoURL: "oci://x", Chart: "x", Version: "1.0.0"}, "name"},
		{"missing repo_url", AddInternalAddonRequest{Name: "x", Chart: "x", Version: "1.0.0"}, "repo_url"},
		{"missing chart", AddInternalAddonRequest{Name: "x", RepoURL: "oci://x", Version: "1.0.0"}, "chart"},
		{"missing version", AddInternalAddonRequest{Name: "x", RepoURL: "oci://x", Chart: "x"}, "version"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			git := newMockGitProvider()
			o := newTestOrchestratorForCatalogDelta(t, git)
			_, err := o.AddInternalAddon(context.Background(), tc.req)
			if err == nil {
				t.Fatalf("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err.Error(), tc.want)
			}
		})
	}
}
