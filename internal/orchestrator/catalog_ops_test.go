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

// TestAddToCatalog_CreatesFileWithPlainEnglishHeader is v4 naming polish
// item 3: the very first catalog.yaml Sharko writes for a repo opens with a
// short, plain-English comment explaining what the file is for — headers
// ride creation only, so this is the one write path that must carry it.
func TestAddToCatalog_CreatesFileWithPlainEnglishHeader(t *testing.T) {
	git := newMockGitProvider()
	o := newTestOrchestratorForCatalog(t, git)

	if _, err := o.AddToCatalog(context.Background(), AddToCatalogRequest{
		Addons: []CatalogAddonInput{{
			Name: "cert-manager", RepoURL: "https://charts.jetstack.io", Chart: "cert-manager", Version: "1.14.5",
		}},
	}); err != nil {
		t.Fatalf("AddToCatalog: %v", err)
	}

	body := string(git.files[config.AddonCatalogPath])
	if !strings.HasPrefix(body, catalogYAMLHeader) {
		t.Fatalf("expected catalog.yaml to open with the plain-English header, got:\n%s", body)
	}
	// Still loads cleanly — the header is a comment, not part of the model.
	if _, err := config.LoadAddonCatalog(git.files[config.AddonCatalogPath]); err != nil {
		t.Errorf("catalog.yaml with header does not load: %v", err)
	}
}

// TestAddToCatalog_HeaderSurvivesSpliceEdit is the other half of item 3: the
// header written on creation must survive an ORDINARY later edit, because
// catalog.yaml's writer is surgical (spliceCatalogEntries only rewrites the
// lines belonging to the addon being changed) rather than a full remarshal —
// so a header that rode in on creation keeps riding along exactly like any
// hand-written comment would.
func TestAddToCatalog_HeaderSurvivesSpliceEdit(t *testing.T) {
	git := newMockGitProvider()
	o := newTestOrchestratorForCatalog(t, git)

	// First add: creates the file, header included.
	if _, err := o.AddToCatalog(context.Background(), AddToCatalogRequest{
		Addons: []CatalogAddonInput{{
			Name: "cert-manager", RepoURL: "https://charts.jetstack.io", Chart: "cert-manager", Version: "1.14.5",
		}},
	}); err != nil {
		t.Fatalf("AddToCatalog (create): %v", err)
	}
	if !strings.HasPrefix(string(git.files[config.AddonCatalogPath]), catalogYAMLHeader) {
		t.Fatalf("header missing right after creation")
	}

	// Second add: an ordinary edit to the same file. The splice path must
	// leave the header (lines it never touches) exactly where it was.
	if _, err := o.AddToCatalog(context.Background(), AddToCatalogRequest{
		Addons: []CatalogAddonInput{{
			Name: "metrics-server", RepoURL: "https://example.test/charts", Chart: "metrics-server", Version: "3.12.1",
		}},
	}); err != nil {
		t.Fatalf("AddToCatalog (edit): %v", err)
	}

	body := string(git.files[config.AddonCatalogPath])
	if !strings.HasPrefix(body, catalogYAMLHeader) {
		t.Errorf("header did not survive the splice edit, got:\n%s", body)
	}
	spec := readWrittenCatalog(t, git)
	if _, ok := spec.Addons["cert-manager"]; !ok {
		t.Errorf("cert-manager lost after the second add: %+v", spec.Addons)
	}
	if _, ok := spec.Addons["metrics-server"]; !ok {
		t.Errorf("metrics-server missing after the second add: %+v", spec.Addons)
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
// one pull request touching catalog.yaml and cluster-addons/<name>.yaml, so
// the reviewer sees both halves in one diff.
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
		Yes:             true,
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
	clusterBody, ok := git.files["cluster-addons/prod-eu.yaml"]
	if !ok {
		t.Fatalf("expected cluster-addons/prod-eu.yaml to be written, files: %v", mapKeys(git.files))
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
		Yes:             true,
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

// ─── v4 smart-values generation on add (v4 smartvalues wave) ───────────────

// fakeChartValuesFetcher returns a canned ChartValuesFetcherFn: success
// returns body for every call, or every call fails with err when body is
// nil.
func fakeChartValuesFetcher(body []byte, err error) ChartValuesFetcherFn {
	return func(_ context.Context, _, _, _, _ string) (ChartValuesFetchResult, error) {
		if err != nil {
			return ChartValuesFetchResult{}, err
		}
		return ChartValuesFetchResult{UpstreamValues: body}, nil
	}
}

// TestAddToCatalog_GeneratesSmartValuesWhenFetcherWired: with a fetcher
// wired, the scaffolded values/global/<addon>.yaml is a REAL smart-values
// generation from the chart's official values.yaml — not the comment-only
// stub — with cluster-specific fields marked and the v4 (unwrapped)
// per-cluster template block appended.
func TestAddToCatalog_GeneratesSmartValuesWhenFetcherWired(t *testing.T) {
	git := newMockGitProvider()
	o := newTestOrchestratorForCatalog(t, git)
	upstream := []byte("installCRDs: true\ningress:\n  host: example.com\n")
	o.SetChartValuesFetcher(fakeChartValuesFetcher(upstream, nil))

	_, err := o.AddToCatalog(context.Background(), AddToCatalogRequest{
		Addons: []CatalogAddonInput{{
			Name: "cert-manager", RepoURL: "https://charts.jetstack.io",
			Chart: "cert-manager", Version: "1.14.5",
		}},
	})
	if err != nil {
		t.Fatalf("AddToCatalog: %v", err)
	}

	got := string(git.files["values/global/cert-manager.yaml"])
	if !strings.Contains(got, "installCRDs: true") {
		t.Errorf("expected a real chart field to survive into the generated file, got:\n%s", got)
	}
	if !strings.Contains(got, "# host: <cluster-specific>") {
		t.Errorf("expected the cluster-specific field marked, got:\n%s", got)
	}
	if !strings.Contains(got, "values/clusters/<cluster>/cert-manager.yaml") {
		t.Errorf("expected the v4 (unwrapped) template block naming the v4 per-cluster path, got:\n%s", got)
	}
	if strings.Contains(got, "# cert-manager:\n") {
		t.Errorf("v4 generation must not wrap the template block under the addon name, got:\n%s", got)
	}
}

// TestAddToCatalog_FallsBackToStubWhenFetchFails: the add never fails
// because of the chart-values fetch. On a fetch error, the scaffold falls
// back to a comment-only file that still parses as an empty YAML document
// AND carries an honest one-line explanation.
func TestAddToCatalog_FallsBackToStubWhenFetchFails(t *testing.T) {
	git := newMockGitProvider()
	o := newTestOrchestratorForCatalog(t, git)
	o.SetChartValuesFetcher(fakeChartValuesFetcher(nil, errors.New("registry unreachable")))

	_, err := o.AddToCatalog(context.Background(), AddToCatalogRequest{
		Addons: []CatalogAddonInput{{
			Name: "cert-manager", RepoURL: "https://charts.jetstack.io",
			Chart: "cert-manager", Version: "1.14.5",
		}},
	})
	if err != nil {
		t.Fatalf("AddToCatalog must not fail because the chart fetch failed: %v", err)
	}

	got := git.files["values/global/cert-manager.yaml"]
	valuesMap, perr := parseYAMLMap(got)
	if perr != nil {
		t.Fatalf("fallback stub is not valid YAML: %v", perr)
	}
	if len(valuesMap) != 0 {
		t.Errorf("fallback stub must carry no invented values, got %v", valuesMap)
	}
	if !strings.Contains(string(got), "could not fetch") {
		t.Errorf("expected an honest explanation that the fetch failed, got:\n%s", got)
	}
	if !strings.Contains(string(got), "annotate") {
		t.Errorf("expected a pointer to running annotate later, got:\n%s", got)
	}
}

// TestAddToCatalog_NoFetcherWiredKeepsPlainStub: the pre-v4-smartvalues
// behaviour (no fetcher wired at all — every orchestrator unit test that
// doesn't call SetChartValuesFetcher) must be completely unchanged: the
// plain comment-only W1 stub, nothing more.
func TestAddToCatalog_NoFetcherWiredKeepsPlainStub(t *testing.T) {
	git := newMockGitProvider()
	o := newTestOrchestratorForCatalog(t, git) // no SetChartValuesFetcher call

	_, err := o.AddToCatalog(context.Background(), AddToCatalogRequest{
		Addons: []CatalogAddonInput{{
			Name: "cert-manager", RepoURL: "https://charts.jetstack.io",
			Chart: "cert-manager", Version: "1.14.5",
		}},
	})
	if err != nil {
		t.Fatalf("AddToCatalog: %v", err)
	}
	got := string(git.files["values/global/cert-manager.yaml"])
	if strings.Contains(got, "could not fetch") {
		t.Errorf("no fetcher wired should not produce the fetch-failed message, got:\n%s", got)
	}
	if !strings.Contains(got, "cert-manager") {
		t.Errorf("plain stub should still name the addon, got:\n%s", got)
	}
}

// TestAddToCatalog_GeneratesSmartValuesForEachInBatch: a batch add
// generates real smart values for every addon, not just the first —
// exercising the per-entry chart/version resolution (buildCatalogEntry
// runs once per addon before the fetch loop).
func TestAddToCatalog_GeneratesSmartValuesForEachInBatch(t *testing.T) {
	git := newMockGitProvider()
	o := newTestOrchestratorForCatalog(t, git)
	o.SetChartValuesFetcher(fakeChartValuesFetcher([]byte("installCRDs: true\n"), nil))

	_, err := o.AddToCatalog(context.Background(), AddToCatalogRequest{
		Addons: []CatalogAddonInput{
			{Name: "cert-manager", RepoURL: "https://charts.jetstack.io", Chart: "cert-manager", Version: "1.14.5"},
			{Name: "metrics-server", RepoURL: "https://example.test/charts", Chart: "metrics-server", Version: "3.12.1"},
		},
	})
	if err != nil {
		t.Fatalf("AddToCatalog: %v", err)
	}
	for _, addon := range []string{"cert-manager", "metrics-server"} {
		got := string(git.files["values/global/"+addon+".yaml"])
		if !strings.Contains(got, "installCRDs: true") {
			t.Errorf("%s: expected the real chart field, got:\n%s", addon, got)
		}
	}
}

// TestAddToCatalog_ComboSeedsClusterValuesFromGlobalTemplate: the
// add-and-enable combo's cluster-values scaffold, when the global file has
// a per-cluster template block, seeds a commented hint for the
// cluster-specific field instead of the plain empty stub — proving
// seedV4ClusterValuesStub is wired into the combo path too.
func TestAddToCatalog_ComboSeedsClusterValuesFromGlobalTemplate(t *testing.T) {
	git := newMockGitProvider()
	registerV4Cluster(t, git, "prod-eu")
	o := newTestOrchestratorForCatalog(t, git)
	upstream := []byte("replicaCount: 1\ningress:\n  host: example.com\n")
	o.SetChartValuesFetcher(fakeChartValuesFetcher(upstream, nil))

	_, err := o.AddToCatalog(context.Background(), AddToCatalogRequest{
		Addons: []CatalogAddonInput{{
			Name: "cert-manager", RepoURL: "https://charts.jetstack.io",
			Chart: "cert-manager", Version: "1.14.5",
		}},
		EnableOnCluster: "prod-eu",
		Yes:             true,
	})
	if err != nil {
		t.Fatalf("AddToCatalog: %v", err)
	}

	clusterValues := string(git.files["values/clusters/prod-eu/cert-manager.yaml"])
	if !strings.Contains(clusterValues, `"ingress.host": <set per cluster>`) {
		t.Errorf("expected the cluster-values scaffold to carry the template hint, got:\n%s", clusterValues)
	}
	// Still comments, never live values (V2-cleanup-19 rule).
	valuesMap, perr := parseYAMLMap(git.files["values/clusters/prod-eu/cert-manager.yaml"])
	if perr != nil {
		t.Fatalf("cluster values scaffold is not valid YAML: %v", perr)
	}
	if len(valuesMap) != 0 {
		t.Errorf("cluster values scaffold must carry no live values, got %v", valuesMap)
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

// TestAddToCatalog_ScaffoldsGlobalValuesFile is v4-walkfix W1 item 5: a
// single-addon add also creates values/global/<addon>.yaml in the SAME
// pull request — a comment-only stub, never an invented default.
func TestAddToCatalog_ScaffoldsGlobalValuesFile(t *testing.T) {
	git := newMockGitProvider()
	o := newTestOrchestratorForCatalog(t, git)

	_, err := o.AddToCatalog(context.Background(), AddToCatalogRequest{
		Addons: []CatalogAddonInput{{
			Name: "cert-manager", RepoURL: "https://charts.jetstack.io",
			Chart: "cert-manager", Version: "1.14.5",
		}},
	})
	if err != nil {
		t.Fatalf("AddToCatalog: %v", err)
	}

	valuesBytes, ok := git.files["values/global/cert-manager.yaml"]
	if !ok {
		t.Fatalf("expected values/global/cert-manager.yaml to be scaffolded, files: %v", mapKeys(git.files))
	}
	valuesMap, err := parseYAMLMap(valuesBytes)
	if err != nil {
		t.Fatalf("scaffolded stub is not valid YAML: %v", err)
	}
	if len(valuesMap) != 0 {
		t.Errorf("scaffolded stub must carry no invented values, got %v", valuesMap)
	}
	if !strings.Contains(string(valuesBytes), "cert-manager") {
		t.Errorf("scaffolded stub should name the addon in plain English, got:\n%s", valuesBytes)
	}
}

// TestAddToCatalog_BatchScaffoldsGlobalValuesForEach: a batch add scaffolds
// one values/global/<addon>.yaml per addon in the request, not just the
// first.
func TestAddToCatalog_BatchScaffoldsGlobalValuesForEach(t *testing.T) {
	git := newMockGitProvider()
	o := newTestOrchestratorForCatalog(t, git)

	_, err := o.AddToCatalog(context.Background(), AddToCatalogRequest{
		Addons: []CatalogAddonInput{
			{Name: "cert-manager", RepoURL: "https://charts.jetstack.io", Chart: "cert-manager", Version: "1.14.5"},
			{Name: "metrics-server", RepoURL: "https://example.test/charts", Chart: "metrics-server", Version: "3.12.1"},
		},
	})
	if err != nil {
		t.Fatalf("AddToCatalog: %v", err)
	}
	for _, addon := range []string{"cert-manager", "metrics-server"} {
		path := "values/global/" + addon + ".yaml"
		if _, ok := git.files[path]; !ok {
			t.Errorf("expected %s to be scaffolded, files: %v", path, mapKeys(git.files))
		}
	}
}

// TestAddToCatalog_SkipsExistingGlobalValuesFile: a hand-created (or
// previously scaffolded and then edited) global values file must never be
// overwritten by a later add-to-catalog request for the same addon.
func TestAddToCatalog_SkipsExistingGlobalValuesFile(t *testing.T) {
	git := newMockGitProvider()
	o := newTestOrchestratorForCatalog(t, git)

	handCrafted := []byte("installCRDs: true\n")
	git.files["values/global/cert-manager.yaml"] = handCrafted

	_, err := o.AddToCatalog(context.Background(), AddToCatalogRequest{
		Addons: []CatalogAddonInput{{
			Name: "cert-manager", RepoURL: "https://charts.jetstack.io",
			Chart: "cert-manager", Version: "1.15.0", // re-add / version bump
		}},
	})
	if err != nil {
		t.Fatalf("AddToCatalog: %v", err)
	}
	got := git.files["values/global/cert-manager.yaml"]
	if string(got) != string(handCrafted) {
		t.Errorf("existing global values file was overwritten: got %q, want unchanged %q", got, handCrafted)
	}
}

// TestAddToCatalog_ComboScaffoldsBothGlobalAndClusterValuesStubs is
// v4-walkfix W1 item 6's combo case: the add+enable combo scaffolds BOTH
// values/global/<addon>.yaml (item 5) AND
// values/clusters/<cluster>/<addon>.yaml (item 6) in the same pull request
// when the enable half carries no explicit values.
func TestAddToCatalog_ComboScaffoldsBothGlobalAndClusterValuesStubs(t *testing.T) {
	git := newMockGitProvider()
	registerV4Cluster(t, git, "prod-eu")
	o := newTestOrchestratorForCatalog(t, git)

	_, err := o.AddToCatalog(context.Background(), AddToCatalogRequest{
		Addons: []CatalogAddonInput{{
			Name: "cert-manager", RepoURL: "https://charts.jetstack.io",
			Chart: "cert-manager", Version: "1.14.5",
		}},
		EnableOnCluster: "prod-eu",
		Yes:             true,
	})
	if err != nil {
		t.Fatalf("AddToCatalog: %v", err)
	}

	if _, ok := git.files["values/global/cert-manager.yaml"]; !ok {
		t.Errorf("expected the global values stub, files: %v", mapKeys(git.files))
	}
	if _, ok := git.files["values/clusters/prod-eu/cert-manager.yaml"]; !ok {
		t.Errorf("expected the per-cluster values stub, files: %v", mapKeys(git.files))
	}
}

// TestAddToCatalog_DryRunListsValuesScaffolds proves the preview naturally
// includes the scaffolded files (item 5's global stub and item 6's
// per-cluster stub on the combo), each marked "create", rather than the
// dry run silently promising a PR that differs from what a real call
// would write.
func TestAddToCatalog_DryRunListsValuesScaffolds(t *testing.T) {
	git := newMockGitProvider()
	registerV4Cluster(t, git, "prod-eu")
	o := newTestOrchestratorForCatalog(t, git)
	filesBefore := len(git.files) // managed-clusters.yaml only, from registerV4Cluster's seed

	result, err := o.AddToCatalog(context.Background(), AddToCatalogRequest{
		Addons: []CatalogAddonInput{{
			Name: "cert-manager", RepoURL: "https://charts.jetstack.io",
			Chart: "cert-manager", Version: "1.14.5",
		}},
		EnableOnCluster: "prod-eu",
		Yes:             true,
		DryRun:          true,
	})
	if err != nil {
		t.Fatalf("AddToCatalog: %v", err)
	}
	if result.DryRun == nil {
		t.Fatal("expected a DryRun result")
	}
	if len(git.prs) != 0 || len(git.files) != filesBefore {
		t.Errorf("dry run must have zero git side effects: prs=%d, files went from %d to %d (%v)",
			len(git.prs), filesBefore, len(git.files), mapKeys(git.files))
	}

	previews := map[string]string{} // path -> action
	for _, f := range result.DryRun.FilesToWrite {
		previews[f.Path] = f.Action
	}
	for _, path := range []string{
		"values/global/cert-manager.yaml",
		"values/clusters/prod-eu/cert-manager.yaml",
	} {
		if action, ok := previews[path]; !ok {
			t.Errorf("expected the preview to list %s, got %v", path, previews)
		} else if action != "create" {
			t.Errorf("expected %s to preview as create, got %q", path, action)
		}
	}
}
