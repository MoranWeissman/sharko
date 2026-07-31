package orchestrator

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/models"
)

func newTestOrchestratorForCatalog(t *testing.T, git *mockGitProvider) *Orchestrator {
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

// registerV4Cluster puts a cluster in managed-clusters.yaml so the enable
// paths accept it.
func registerV4Cluster(t *testing.T, git *mockGitProvider, name string) {
	t.Helper()
	body, err := models.SaveManagedClusters(models.ManagedClustersSpec{
		Clusters: []models.ManagedClusterEntry{{Name: name, Region: "eu-west-1"}},
	})
	if err != nil {
		t.Fatalf("SaveManagedClusters: %v", err)
	}
	git.files[V4ManagedClustersPath] = body
}

// TestAddToCatalog_CreatesFileWhenMissing is day zero: a fresh repo has no
// catalog.yaml at all, and the first add creates it rather than failing on
// the read.
func TestAddToCatalog_CreatesFileWhenMissing(t *testing.T) {
	git := newMockGitProvider()
	o := newTestOrchestratorForCatalog(t, git)

	result, err := o.AddToCatalog(context.Background(), AddToCatalogRequest{
		Addons: []CatalogAddonInput{{
			Name:      "billing-api",
			RepoURL:   "oci://registry.example.com/charts",
			Chart:     "billing-api",
			Version:   "2.4.0",
			Namespace: "billing",
		}},
	})
	if err != nil {
		t.Fatalf("AddToCatalog: %v", err)
	}
	if result == nil || result.PRUrl == "" {
		t.Fatalf("expected a PR result, got %+v", result)
	}
	if len(result.Added) != 1 || result.Added[0] != "billing-api" {
		t.Errorf("Added should name the addon, got %+v", result.Added)
	}
	if len(result.Enabled) != 0 {
		t.Errorf("a catalog-only add must not report anything enabled, got %+v", result.Enabled)
	}

	spec := readWrittenCatalog(t, git)
	entry, ok := spec.Addons["billing-api"]
	if !ok {
		t.Fatalf("expected billing-api in the written catalog, got %+v", spec.Addons)
	}
	if entry.RepoURL != "oci://registry.example.com/charts" || entry.Chart != "billing-api" ||
		entry.Version != "2.4.0" || entry.Namespace != "billing" {
		t.Errorf("written entry fields wrong: %+v", entry)
	}
}

// TestAddToCatalog_BatchIsOnePullRequest is what the first-run wizard needs:
// ticking several addons makes ONE pull request, never one per addon.
func TestAddToCatalog_BatchIsOnePullRequest(t *testing.T) {
	git := newMockGitProvider()
	o := newTestOrchestratorForCatalog(t, git)

	before := len(git.prs)
	result, err := o.AddToCatalog(context.Background(), AddToCatalogRequest{
		Addons: []CatalogAddonInput{
			{Name: "cert-manager", RepoURL: "https://charts.jetstack.io", Chart: "cert-manager", Version: "1.14.5"},
			{Name: "metrics-server", RepoURL: "https://example.test/charts", Chart: "metrics-server", Version: "3.12.1"},
			{Name: "vault", RepoURL: "https://example.test/charts", Chart: "vault", Version: "0.28.0"},
		},
	})
	if err != nil {
		t.Fatalf("AddToCatalog: %v", err)
	}
	if got := len(git.prs) - before; got != 1 {
		t.Errorf("three addons must produce exactly one pull request, got %d", got)
	}
	if len(result.Added) != 3 {
		t.Errorf("expected 3 added, got %+v", result.Added)
	}

	spec := readWrittenCatalog(t, git)
	for _, name := range []string{"cert-manager", "metrics-server", "vault"} {
		if _, ok := spec.Addons[name]; !ok {
			t.Errorf("%s missing from the written catalog", name)
		}
	}
}

// TestAddToCatalog_ComboWritesBothFilesInOnePR is the add-and-enable case:
// one pull request touching catalog.yaml and clusters/<name>.yaml, so the
// reviewer sees both halves in one diff.
func TestAddToCatalog_ComboWritesBothFilesInOnePR(t *testing.T) {
	git := newMockGitProvider()
	registerV4Cluster(t, git, "prod-eu")
	o := newTestOrchestratorForCatalog(t, git)

	before := len(git.prs)
	result, err := o.AddToCatalog(context.Background(), AddToCatalogRequest{
		Addons: []CatalogAddonInput{{
			Name: "cert-manager", RepoURL: "https://charts.jetstack.io",
			Chart: "cert-manager", Version: "1.14.5",
		}},
		EnableOnCluster: "prod-eu",
	})
	if err != nil {
		t.Fatalf("AddToCatalog: %v", err)
	}
	if got := len(git.prs) - before; got != 1 {
		t.Errorf("add-and-enable must be one pull request, got %d", got)
	}
	if result.Cluster != "prod-eu" || len(result.Enabled) != 1 {
		t.Errorf("expected cert-manager enabled on prod-eu, got cluster=%q enabled=%+v", result.Cluster, result.Enabled)
	}

	if _, ok := readWrittenCatalog(t, git).Addons["cert-manager"]; !ok {
		t.Error("cert-manager missing from the written catalog")
	}
	clusterBody, ok := git.files["clusters/prod-eu.yaml"]
	if !ok {
		t.Fatalf("expected clusters/prod-eu.yaml to be written, files: %v", mapKeys(git.files))
	}
	ca, err := models.LoadClusterAddons(clusterBody)
	if err != nil {
		t.Fatalf("LoadClusterAddons: %v", err)
	}
	if !ca.Addons["cert-manager"].Enabled {
		t.Errorf("cert-manager should be enabled on prod-eu, got %+v", ca.Addons)
	}
}

// TestAddToCatalog_ComboRefusesUnknownCluster: the cluster check runs before
// anything is written, so a typo leaves the repo untouched.
func TestAddToCatalog_ComboRefusesUnknownCluster(t *testing.T) {
	git := newMockGitProvider()
	o := newTestOrchestratorForCatalog(t, git)

	_, err := o.AddToCatalog(context.Background(), AddToCatalogRequest{
		Addons: []CatalogAddonInput{{
			Name: "cert-manager", RepoURL: "https://charts.jetstack.io",
			Chart: "cert-manager", Version: "1.14.5",
		}},
		EnableOnCluster: "nope",
	})
	if !errors.Is(err, ErrV4ClusterNotFound) {
		t.Fatalf("expected ErrV4ClusterNotFound, got %v", err)
	}
	if _, written := git.files[config.AddonCatalogPath]; written {
		t.Error("nothing should have been written for an unknown cluster")
	}
}

// TestAddToCatalog_UpsertsAndKeepsEverythingElse: re-adding an addon updates
// it in place and leaves every other approved addon alone.
func TestAddToCatalog_UpsertsAndKeepsEverythingElse(t *testing.T) {
	git := newMockGitProvider()
	git.files[config.AddonCatalogPath] = mustSaveCatalog(t, config.AddonCatalogSpec{
		Addons: map[string]config.AddonCatalogEntry{
			"cert-manager": {RepoURL: "https://charts.jetstack.io", Chart: "cert-manager", Version: "1.14.5"},
			"billing-api":  {RepoURL: "oci://registry.example.com/charts", Chart: "billing-api", Version: "2.0.0"},
		},
	})

	o := newTestOrchestratorForCatalog(t, git)
	if _, err := o.AddToCatalog(context.Background(), AddToCatalogRequest{
		Addons: []CatalogAddonInput{{
			Name: "billing-api", RepoURL: "oci://registry.example.com/charts",
			Chart: "billing-api", Version: "2.4.0",
		}},
	}); err != nil {
		t.Fatalf("AddToCatalog: %v", err)
	}

	spec := readWrittenCatalog(t, git)
	if spec.Addons["billing-api"].Version != "2.4.0" {
		t.Errorf("billing-api version not updated: %+v", spec.Addons["billing-api"])
	}
	if spec.Addons["cert-manager"].Version != "1.14.5" {
		t.Errorf("cert-manager lost on upsert: %+v", spec.Addons["cert-manager"])
	}
}

// TestAddToCatalog_EveryEntryNeedsItsChartLocation: there is no shipped list
// underneath the file any more, so an entry missing repoURL/chart/version
// is refused with a message naming what is missing.
func TestAddToCatalog_EveryEntryNeedsItsChartLocation(t *testing.T) {
	cases := []struct {
		name string
		in   CatalogAddonInput
		want string
	}{
		{"no version", CatalogAddonInput{Name: "x", RepoURL: "oci://x", Chart: "x"}, "version"},
		{"no repo", CatalogAddonInput{Name: "x", Chart: "x", Version: "1.0.0"}, "chart repository"},
		{"no chart", CatalogAddonInput{Name: "x", RepoURL: "oci://x", Version: "1.0.0"}, "chart name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			git := newMockGitProvider()
			o := newTestOrchestratorForCatalog(t, git)
			_, err := o.AddToCatalog(context.Background(), AddToCatalogRequest{
				Addons: []CatalogAddonInput{tc.in},
			})
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err.Error(), tc.want)
			}
			if _, written := git.files[config.AddonCatalogPath]; written {
				t.Error("nothing should have been written")
			}
		})
	}
}

// TestAddToCatalog_NeedsAtLeastOneAddon.
func TestAddToCatalog_NeedsAtLeastOneAddon(t *testing.T) {
	git := newMockGitProvider()
	o := newTestOrchestratorForCatalog(t, git)
	if _, err := o.AddToCatalog(context.Background(), AddToCatalogRequest{}); err == nil {
		t.Fatal("expected an error for an empty request")
	}
}

// TestAddToCatalog_FromMarketplaceNeedsACuratedEntry: the pre-filled
// shortcut fails loudly on a name the Marketplace does not carry, rather
// than quietly writing an empty entry.
func TestAddToCatalog_FromMarketplaceNeedsACuratedEntry(t *testing.T) {
	git := newMockGitProvider()
	o := newTestOrchestratorForCatalog(t, git)

	_, err := o.AddToCatalog(context.Background(), AddToCatalogRequest{
		Addons: []CatalogAddonInput{{Name: "cert-manager", FromMarketplace: true, Version: "1.14.5"}},
	})
	if !errors.Is(err, ErrAddonNotInMarketplace) {
		t.Fatalf("expected ErrAddonNotInMarketplace with no curated catalog wired, got %v", err)
	}
}

// TestAddToCatalog_RejectsDuplicateNames.
func TestAddToCatalog_RejectsDuplicateNames(t *testing.T) {
	git := newMockGitProvider()
	o := newTestOrchestratorForCatalog(t, git)
	_, err := o.AddToCatalog(context.Background(), AddToCatalogRequest{
		Addons: []CatalogAddonInput{
			{Name: "vault", RepoURL: "https://x.test", Chart: "vault", Version: "0.28.0"},
			{Name: "vault", RepoURL: "https://x.test", Chart: "vault", Version: "0.29.0"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("expected a duplicate-name error, got %v", err)
	}
}

func readWrittenCatalog(t *testing.T, git *mockGitProvider) config.AddonCatalogSpec {
	t.Helper()
	body, ok := git.files[config.AddonCatalogPath]
	if !ok {
		t.Fatalf("expected %s to be written, files: %v", config.AddonCatalogPath, mapKeys(git.files))
	}
	spec, err := config.LoadAddonCatalog(body)
	if err != nil {
		t.Fatalf("LoadAddonCatalog on the written file: %v", err)
	}
	return spec
}

func mustSaveCatalog(t *testing.T, spec config.AddonCatalogSpec) []byte {
	t.Helper()
	body, err := config.SaveAddonCatalog(spec)
	if err != nil {
		t.Fatalf("SaveAddonCatalog: %v", err)
	}
	return body
}

func mapKeys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
