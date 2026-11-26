package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/models"
)

// These tests cover pre-release walk 4 (upgrades on the v4 repo layout):
//
//   - the main Upgrade button / upgrade-addon / POST /addons/{name}/upgrade
//     ended in "operation not supported on a v4 repo";
//   - after an upgrade the catalog had not moved, so every screen called the
//     upgraded clusters "behind";
//   - a version that does not exist ("99.0.0", "abc") opened a real pull
//     request;
//   - the seeded global values file kept the old chart's image tag, so the
//     chart moved and the image did not.

const podinfoRepo = "https://stefanprodan.github.io/podinfo"

// podinfoChartValues is the part of podinfo's own values.yaml that matters
// here: the image tag is the chart's version, and it changes every release.
func podinfoChartValues(version string) []byte {
	return []byte(fmt.Sprintf(`# Default values for podinfo.
logLevel: info
faults:
  delay: false

image:
  repository: ghcr.io/stefanprodan/podinfo
  tag: %s
  pullPolicy: IfNotPresent

ui:
  color: "#34577c"
  message: ""
`, version))
}

// seedPodinfoV4Repo builds a v4 repo with podinfo in the catalog at
// catalogVersion and a Sharko-generated global values file made from that
// chart version.
func seedPodinfoV4Repo(t *testing.T, git *mockGitProvider, catalogVersion string) *Orchestrator {
	t.Helper()
	git.files[EnginePinPath] = []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n")
	git.files[V4ManagedClustersPath] = []byte("clusters:\n  - name: spoke-1\n  - name: spoke-2\n")
	git.files[config.AddonCatalogPath] = mustSaveCatalog(t, config.AddonCatalogSpec{
		Addons: map[string]config.AddonCatalogEntry{
			"podinfo":  {RepoURL: podinfoRepo, Chart: "podinfo", Version: catalogVersion, Namespace: "podinfo"},
			"reloader": {RepoURL: "https://stakater.github.io/stakater-charts", Chart: "reloader", Version: "2.2.21", Namespace: "reloader"},
		},
	})
	gPath, err := v4GlobalValuesPath("podinfo")
	if err != nil {
		t.Fatal(err)
	}
	git.files[gPath] = GenerateGlobalValuesFileV4("podinfo", "podinfo", catalogVersion, podinfoRepo, podinfoChartValues(catalogVersion), false, false)

	orch := New(nil, nil, newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)
	// The chart repository in these tests publishes 6.7.1, 6.8.0 and 6.9.0.
	orch.SetChartVersionCheck(func(_ context.Context, _, chart, version string) (bool, bool, error) {
		if chart != "podinfo" {
			return true, true, nil
		}
		switch version {
		case "6.7.1", "6.8.0", "6.9.0":
			return true, true, nil
		}
		return false, true, nil
	})
	orch.SetChartDefaultsFetcher(func(_ context.Context, _, _, version string) ([]byte, error) {
		return podinfoChartValues(version), nil
	})
	return orch
}

func seedClusterAddonsEntry(t *testing.T, git *mockGitProvider, cluster, addon string, entry models.ClusterAddonsAddon) {
	t.Helper()
	p := assignPath(t, cluster)
	spec := models.ClusterAddonsSpec{Cluster: cluster, Addons: map[string]models.ClusterAddonsAddon{}}
	if existing, ok := git.files[p]; ok {
		loaded, err := models.LoadClusterAddons(existing)
		if err != nil {
			t.Fatal(err)
		}
		spec = loaded
	}
	spec.Addons[addon] = entry
	body, err := models.SaveClusterAddons(spec)
	if err != nil {
		t.Fatal(err)
	}
	git.files[p] = body
}

func catalogVersionOf(t *testing.T, git *mockGitProvider, addon string) string {
	t.Helper()
	spec, err := config.LoadAddonCatalog(git.files[config.AddonCatalogPath])
	if err != nil {
		t.Fatalf("reading catalog.yaml back: %v", err)
	}
	return spec.Addons[addon].Version
}

func clusterAddonOf(t *testing.T, git *mockGitProvider, cluster, addon string) models.ClusterAddonsAddon {
	t.Helper()
	spec, err := models.LoadClusterAddons(git.files[assignPath(t, cluster)])
	if err != nil {
		t.Fatalf("reading cluster-addons/%s.yaml back: %v", cluster, err)
	}
	return spec.Addons[addon]
}

// The main Upgrade button on a v4 repo: one pull request that really moves
// the addon for all clusters. No "operation not supported on a v4 repo".
func TestUpgradeAddon_V4_AllClusters_MovesCatalogAndEveryCluster(t *testing.T) {
	git := newMockGitProvider()
	orch := seedPodinfoV4Repo(t, git, "6.7.1")
	// spoke-1 follows the catalog. spoke-2 was moved ahead on its own
	// earlier and carries a pin. spoke-3 has the addon switched off with an
	// old pin left in the file.
	seedClusterAddonsEntry(t, git, "spoke-1", "podinfo", models.ClusterAddonsAddon{Enabled: true})
	seedClusterAddonsEntry(t, git, "spoke-1", "reloader", models.ClusterAddonsAddon{Enabled: true, Version: "2.2.20"})
	seedClusterAddonsEntry(t, git, "spoke-2", "podinfo", models.ClusterAddonsAddon{Enabled: true, Version: "6.7.1"})
	seedClusterAddonsEntry(t, git, "spoke-3", "podinfo", models.ClusterAddonsAddon{Enabled: false, Version: "6.7.1"})
	spoke3Before := string(git.files[assignPath(t, "spoke-3")])

	result, err := orch.UpgradeAddon(context.Background(), UpgradeAddonRequest{Addon: "podinfo", Version: "6.8.0"})
	if err != nil {
		t.Fatalf("an all-clusters upgrade on a v4 repo must work, got: %v", err)
	}
	if IsV4RepoUnsupported(err) {
		t.Fatal("the v4 refusal is back")
	}
	if result == nil || result.PRUrl == "" {
		t.Fatalf("expected a pull request, got %+v", result)
	}
	if len(git.prs) != 1 {
		t.Fatalf("expected exactly ONE pull request, got %d", len(git.prs))
	}
	if got, want := git.prs[0].Title, "sharko: upgrade addon podinfo to 6.8.0 on all clusters"; got != want {
		t.Errorf("pull request title = %q, want %q", got, want)
	}

	if got := catalogVersionOf(t, git, "podinfo"); got != "6.8.0" {
		t.Errorf("catalog version = %q, want 6.8.0 — the catalog has to move or every screen calls the clusters behind", got)
	}
	if got := catalogVersionOf(t, git, "reloader"); got != "2.2.21" {
		t.Errorf("another addon's catalog version changed: %q", got)
	}
	if pin := clusterAddonOf(t, git, "spoke-2", "podinfo"); pin.Version != "" || !pin.Enabled {
		t.Errorf("spoke-2 must lose its own pin and stay switched on, got %+v", pin)
	}
	if other := clusterAddonOf(t, git, "spoke-1", "reloader"); other.Version != "2.2.20" {
		t.Errorf("another addon's pin on spoke-1 was touched: %+v", other)
	}
	if got := string(git.files[assignPath(t, "spoke-3")]); got != spoke3Before {
		t.Errorf("a cluster with the addon switched off must not be touched:\n%s", got)
	}
}

// The seeded global values file: the image tag Sharko copied from the old
// chart follows the new chart, a setting a person changed does not.
func TestUpgradeAddon_V4_AllClusters_SeededImageTagFollowsTheChart(t *testing.T) {
	git := newMockGitProvider()
	orch := seedPodinfoV4Repo(t, git, "6.7.1")
	seedClusterAddonsEntry(t, git, "spoke-1", "podinfo", models.ClusterAddonsAddon{Enabled: true})
	gPath, _ := v4GlobalValuesPath("podinfo")
	// A person changed two things in the generated file.
	edited := strings.Replace(string(git.files[gPath]), "  delay: false", "  delay: true", 1)
	edited = strings.Replace(edited, "logLevel: info", "logLevel: debug # we want more logs", 1)
	if !strings.Contains(edited, "  delay: true") || !strings.Contains(edited, "logLevel: debug") {
		t.Fatalf("fixture: the two hand edits did not apply:\n%s", edited)
	}
	git.files[gPath] = []byte(edited)

	result, err := orch.UpgradeAddon(context.Background(), UpgradeAddonRequest{Addon: "podinfo", Version: "6.8.0"})
	if err != nil {
		t.Fatalf("UpgradeAddon: %v", err)
	}
	after := string(git.files[gPath])
	if !strings.Contains(after, "  tag: 6.8.0\n") {
		t.Errorf("image.tag must follow the new chart (6.8.0) — otherwise the chart moves and the old image keeps running:\n%s", after)
	}
	if strings.Contains(after, "tag: 6.7.1") {
		t.Errorf("the old image tag is still in the file:\n%s", after)
	}
	for _, kept := range []string{"  delay: true", "logLevel: debug # we want more logs", `color: "#34577c"`} {
		if !strings.Contains(after, kept) {
			t.Errorf("a setting a person chose (or that did not change) was lost: %q\n%s", kept, after)
		}
	}
	if !strings.Contains(after, "# Generated by Sharko from podinfo@6.8.0 on ") {
		t.Errorf("the header must say which chart version the file now matches:\n%s", after)
	}
	// Only two lines of the file differ: the header and the tag.
	if n := countDifferentLines(edited, after); n != 2 {
		t.Errorf("expected exactly 2 changed lines (header + image tag), got %d:\n%s", n, after)
	}
	if len(result.Warnings) != 0 {
		t.Errorf("nothing was left on the old chart's value, so no warning is due: %v", result.Warnings)
	}
}

// The preview shows exactly the files the real pull request writes.
func TestUpgradeAddon_V4_AllClusters_DryRunMatchesTheRealPullRequest(t *testing.T) {
	git := newMockGitProvider()
	orch := seedPodinfoV4Repo(t, git, "6.7.1")
	seedClusterAddonsEntry(t, git, "spoke-1", "podinfo", models.ClusterAddonsAddon{Enabled: true})
	seedClusterAddonsEntry(t, git, "spoke-2", "podinfo", models.ClusterAddonsAddon{Enabled: true, Version: "6.7.1"})
	before := map[string]string{}
	for p, c := range git.files {
		before[p] = string(c)
	}

	preview, err := orch.UpgradeAddon(context.Background(), UpgradeAddonRequest{Addon: "podinfo", Version: "6.8.0", DryRun: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if preview.DryRun == nil {
		t.Fatal("expected a dry-run result")
	}
	if len(git.prs) != 0 || len(git.branches) != 0 {
		t.Fatal("a dry run must not open a pull request or a branch")
	}
	for p, c := range git.files {
		if before[p] != string(c) {
			t.Fatalf("a dry run changed %s", p)
		}
	}
	gPath, _ := v4GlobalValuesPath("podinfo")
	wantPaths := []string{config.AddonCatalogPath, assignPath(t, "spoke-2"), gPath}
	var gotPaths []string
	for _, f := range preview.DryRun.FilesToWrite {
		gotPaths = append(gotPaths, f.Path)
	}
	if strings.Join(gotPaths, ",") != strings.Join(wantPaths, ",") {
		t.Fatalf("preview files = %v, want %v", gotPaths, wantPaths)
	}
	previewTitle := preview.DryRun.PRTitle

	if _, err := orch.UpgradeAddon(context.Background(), UpgradeAddonRequest{Addon: "podinfo", Version: "6.8.0"}); err != nil {
		t.Fatalf("real run: %v", err)
	}
	var changed []string
	for p, c := range git.files {
		if before[p] != string(c) {
			changed = append(changed, p)
		}
	}
	if len(changed) != len(wantPaths) {
		t.Errorf("the real pull request changed %v, the preview promised %v", changed, wantPaths)
	}
	if git.prs[0].Title != previewTitle {
		t.Errorf("preview title %q is not the real pull request title %q", previewTitle, git.prs[0].Title)
	}
}

// A version that does not exist is refused before any pull request, with a
// sentence that names the version and says it was not found.
func TestUpgrade_V4_VersionThatDoesNotExist_IsRefusedBeforeAnyPullRequest(t *testing.T) {
	for _, version := range []string{"99.0.0", "abc"} {
		for _, shape := range []string{"all clusters", "one cluster", "chosen clusters", "all clusters dry run", "chosen clusters dry run"} {
			t.Run(version+"/"+shape, func(t *testing.T) {
				git := newMockGitProvider()
				orch := seedPodinfoV4Repo(t, git, "6.7.1")
				seedClusterAddonsEntry(t, git, "spoke-1", "podinfo", models.ClusterAddonsAddon{Enabled: true})

				var err error
				switch shape {
				case "all clusters":
					_, err = orch.UpgradeAddon(context.Background(), UpgradeAddonRequest{Addon: "podinfo", Version: version})
				case "all clusters dry run":
					_, err = orch.UpgradeAddon(context.Background(), UpgradeAddonRequest{Addon: "podinfo", Version: version, DryRun: true})
				case "one cluster":
					_, err = orch.UpgradeAddon(context.Background(), UpgradeAddonRequest{Addon: "podinfo", Version: version, Cluster: "spoke-1"})
				case "chosen clusters":
					_, err = orch.UpgradeAddonClustersV4(context.Background(), UpgradeAddonClustersV4Request{Addon: "podinfo", Version: version, Clusters: []string{"spoke-1"}, Yes: true})
				case "chosen clusters dry run":
					_, err = orch.UpgradeAddonClustersV4(context.Background(), UpgradeAddonClustersV4Request{Addon: "podinfo", Version: version, Clusters: []string{"spoke-1"}, DryRun: true})
				}
				if err == nil {
					t.Fatal("a version that is not in the chart repository must be refused")
				}
				if !errors.Is(err, ErrChartVersionNotFound) {
					t.Fatalf("expected ErrChartVersionNotFound, got %v", err)
				}
				want := "Version " + version + " of podinfo was not found in its chart repository (" + podinfoRepo + "). Nothing was changed. Pick a version that the repository lists."
				if err.Error() != want {
					t.Errorf("sentence =\n  %q\nwant\n  %q", err.Error(), want)
				}
				if len(git.prs) != 0 || len(git.branches) != 0 {
					t.Error("nothing may be written for a version that does not exist")
				}
				if got := catalogVersionOf(t, git, "podinfo"); got != "6.7.1" {
					t.Errorf("catalog changed to %q", got)
				}
			})
		}
	}
}

// A chart repository that cannot be reached is said as that. The version is
// not blamed, because Sharko does not know whether it exists.
func TestUpgrade_V4_ChartRepositoryUnreachable_DoesNotBlameTheVersion(t *testing.T) {
	git := newMockGitProvider()
	orch := seedPodinfoV4Repo(t, git, "6.7.1")
	seedClusterAddonsEntry(t, git, "spoke-1", "podinfo", models.ClusterAddonsAddon{Enabled: true})
	orch.SetChartVersionCheck(func(context.Context, string, string, string) (bool, bool, error) {
		return false, true, errors.New(`Get "https://user:hunter2@stefanprodan.github.io/podinfo/index.yaml": dial tcp: no such host`)
	})

	_, err := orch.UpgradeAddon(context.Background(), UpgradeAddonRequest{Addon: "podinfo", Version: "6.8.0"})
	if err == nil {
		t.Fatal("expected the upgrade to stop")
	}
	if !errors.Is(err, ErrChartVersionLookupFailed) {
		t.Fatalf("expected ErrChartVersionLookupFailed, got %v", err)
	}
	if errors.Is(err, ErrChartVersionNotFound) {
		t.Fatal("a lookup that failed must not be reported as a version that was not found")
	}
	want := "Sharko could not reach the chart repository of podinfo (" + podinfoRepo + "), so it could not check that version 6.8.0 exists. Nothing was changed. Try again in a moment."
	if err.Error() != want {
		t.Errorf("sentence =\n  %q\nwant\n  %q", err.Error(), want)
	}
	for _, banned := range []string{"was not found", "hunter2", "dial tcp"} {
		if strings.Contains(err.Error(), banned) {
			t.Errorf("the sentence must not contain %q: %q", banned, err.Error())
		}
	}
	if len(git.prs) != 0 {
		t.Error("no pull request may be opened")
	}
}

// An oci:// chart repository cannot be listed, so nothing is checked and the
// upgrade goes on — the long-standing rule for those.
func TestUpgrade_V4_RepositoryThatCannotBeListed_IsNotChecked(t *testing.T) {
	git := newMockGitProvider()
	orch := seedPodinfoV4Repo(t, git, "6.7.1")
	seedClusterAddonsEntry(t, git, "spoke-1", "podinfo", models.ClusterAddonsAddon{Enabled: true})
	orch.SetChartVersionCheck(func(context.Context, string, string, string) (bool, bool, error) {
		return false, false, nil
	})
	if _, err := orch.UpgradeAddon(context.Background(), UpgradeAddonRequest{Addon: "podinfo", Version: "6.8.0"}); err != nil {
		t.Fatalf("expected the upgrade to go on, got %v", err)
	}
}

// One cluster moves ahead of the rest: the catalog stays, the shared values
// file stays, and Sharko says the image tag in it is still the old one.
func TestUpgradeAddon_V4_OneCluster_WarnsThatSharedValuesKeepTheOldImage(t *testing.T) {
	git := newMockGitProvider()
	orch := seedPodinfoV4Repo(t, git, "6.7.1")
	seedClusterAddonsEntry(t, git, "spoke-1", "podinfo", models.ClusterAddonsAddon{Enabled: true})
	seedClusterAddonsEntry(t, git, "spoke-2", "podinfo", models.ClusterAddonsAddon{Enabled: true})
	gPath, _ := v4GlobalValuesPath("podinfo")
	globalBefore := string(git.files[gPath])

	result, err := orch.UpgradeAddon(context.Background(), UpgradeAddonRequest{Addon: "podinfo", Version: "6.8.0", Cluster: "spoke-1"})
	if err != nil {
		t.Fatalf("UpgradeAddon: %v", err)
	}
	if got := catalogVersionOf(t, git, "podinfo"); got != "6.7.1" {
		t.Errorf("a one-cluster upgrade must leave the catalog alone, got %q", got)
	}
	if pin := clusterAddonOf(t, git, "spoke-1", "podinfo"); pin.Version != "6.8.0" {
		t.Errorf("spoke-1 pin = %q, want 6.8.0", pin.Version)
	}
	if pin := clusterAddonOf(t, git, "spoke-2", "podinfo"); pin.Version != "" {
		t.Errorf("spoke-2 was not chosen and must be untouched, got pin %q", pin.Version)
	}
	if string(git.files[gPath]) != globalBefore {
		t.Error("the shared global values file must not change for a one-cluster upgrade")
	}
	want := "values/global/podinfo.yaml is shared by every cluster and still sets the old chart's value for: image.tag (6.7.1, new chart default 6.8.0). The chosen clusters get the new chart but keep these old values. To move them too, upgrade podinfo for all clusters, or set them in the cluster's own values file."
	if len(result.Warnings) != 1 || result.Warnings[0] != want {
		t.Fatalf("warnings =\n  %q\nwant exactly\n  %q", result.Warnings, want)
	}
	if got, wantTitle := git.prs[0].Title, "sharko: upgrade addon podinfo to 6.8.0 on clusters spoke-1"; got != wantTitle {
		t.Errorf("pull request title = %q, want %q", got, wantTitle)
	}
	if strings.Contains(git.prs[0].Title, "(v4)") {
		t.Errorf("the title must not carry an inner label: %q", git.prs[0].Title)
	}
}

// Going back down is called a downgrade in the pull request title.
func TestUpgrade_V4_GoingDown_IsTitledDowngrade(t *testing.T) {
	git := newMockGitProvider()
	orch := seedPodinfoV4Repo(t, git, "6.8.0")
	seedClusterAddonsEntry(t, git, "spoke-1", "podinfo", models.ClusterAddonsAddon{Enabled: true})

	if _, err := orch.UpgradeAddon(context.Background(), UpgradeAddonRequest{Addon: "podinfo", Version: "6.7.1"}); err != nil {
		t.Fatalf("UpgradeAddon: %v", err)
	}
	if got, want := git.prs[0].Title, "sharko: downgrade addon podinfo to 6.7.1 on all clusters"; got != want {
		t.Errorf("title = %q, want %q", got, want)
	}

	git2 := newMockGitProvider()
	orch2 := seedPodinfoV4Repo(t, git2, "6.8.0")
	seedClusterAddonsEntry(t, git2, "spoke-1", "podinfo", models.ClusterAddonsAddon{Enabled: true})
	if _, err := orch2.UpgradeAddonClustersV4(context.Background(), UpgradeAddonClustersV4Request{Addon: "podinfo", Version: "6.7.1", Clusters: []string{"spoke-1"}, Yes: true}); err != nil {
		t.Fatalf("UpgradeAddonClustersV4: %v", err)
	}
	if got, want := git2.prs[0].Title, "sharko: downgrade addon podinfo to 6.7.1 on clusters spoke-1"; got != want {
		t.Errorf("title = %q, want %q", got, want)
	}
}

// "Upgrade all clusters to 6.8.0" must not move a cluster that is already on
// 6.9.0 back down. It keeps its own version and is named in a warning.
func TestUpgradeAddon_V4_AllClusters_NeverMovesAClusterDown(t *testing.T) {
	git := newMockGitProvider()
	orch := seedPodinfoV4Repo(t, git, "6.7.1")
	seedClusterAddonsEntry(t, git, "spoke-1", "podinfo", models.ClusterAddonsAddon{Enabled: true})
	seedClusterAddonsEntry(t, git, "spoke-2", "podinfo", models.ClusterAddonsAddon{Enabled: true, Version: "6.9.0"})
	spoke2Before := string(git.files[assignPath(t, "spoke-2")])

	result, err := orch.UpgradeAddon(context.Background(), UpgradeAddonRequest{Addon: "podinfo", Version: "6.8.0"})
	if err != nil {
		t.Fatalf("UpgradeAddon: %v", err)
	}
	if got := catalogVersionOf(t, git, "podinfo"); got != "6.8.0" {
		t.Errorf("catalog version = %q, want 6.8.0", got)
	}
	if got := string(git.files[assignPath(t, "spoke-2")]); got != spoke2Before {
		t.Errorf("spoke-2 is on 6.9.0, newer than 6.8.0 — its file must not change:\n%s", got)
	}
	want := "These clusters already run a newer version of podinfo than 6.8.0 and were left as they are: spoke-2 (6.9.0)."
	found := false
	for _, w := range result.Warnings {
		if w == want {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %q, want one to be exactly %q", result.Warnings, want)
	}
}

// The same version again changes nothing and opens nothing.
func TestUpgrade_V4_SameVersion_SaysNothingToChange(t *testing.T) {
	git := newMockGitProvider()
	orch := seedPodinfoV4Repo(t, git, "6.7.1")
	seedClusterAddonsEntry(t, git, "spoke-1", "podinfo", models.ClusterAddonsAddon{Enabled: true})

	_, err := orch.UpgradeAddon(context.Background(), UpgradeAddonRequest{Addon: "podinfo", Version: "6.7.1"})
	if !errors.Is(err, ErrUpgradeNothingToDo) {
		t.Fatalf("expected ErrUpgradeNothingToDo, got %v", err)
	}
	if want := "podinfo is already on version 6.7.1 on every cluster. Nothing to change."; err.Error() != want {
		t.Errorf("sentence = %q, want %q", err.Error(), want)
	}

	_, err = orch.UpgradeAddon(context.Background(), UpgradeAddonRequest{Addon: "podinfo", Version: "6.7.1", Cluster: "spoke-1"})
	if !errors.Is(err, ErrUpgradeNothingToDo) {
		t.Fatalf("expected ErrUpgradeNothingToDo for one cluster, got %v", err)
	}
	if want := "podinfo is already on version 6.7.1 on spoke-1. Nothing to change."; err.Error() != want {
		t.Errorf("sentence = %q, want %q", err.Error(), want)
	}
	if len(git.prs) != 0 {
		t.Error("no pull request may be opened when nothing changes")
	}
}

// Several addons, all clusters, ONE pull request.
func TestUpgradeAddons_V4_Batch_OnePullRequest(t *testing.T) {
	git := newMockGitProvider()
	orch := seedPodinfoV4Repo(t, git, "6.7.1")
	seedClusterAddonsEntry(t, git, "spoke-1", "podinfo", models.ClusterAddonsAddon{Enabled: true})
	seedClusterAddonsEntry(t, git, "spoke-1", "reloader", models.ClusterAddonsAddon{Enabled: true, Version: "2.2.20"})

	v3CatalogBefore := string(git.files["configuration/addons-catalog.yaml"])

	if _, err := orch.UpgradeAddons(context.Background(), map[string]string{"podinfo": "6.9.0", "reloader": "2.2.22"}, nil); err != nil {
		t.Fatalf("UpgradeAddons: %v", err)
	}
	if len(git.prs) != 1 {
		t.Fatalf("expected one pull request, got %d", len(git.prs))
	}
	if got := catalogVersionOf(t, git, "podinfo"); got != "6.9.0" {
		t.Errorf("podinfo = %q", got)
	}
	if got := catalogVersionOf(t, git, "reloader"); got != "2.2.22" {
		t.Errorf("reloader = %q", got)
	}
	if pin := clusterAddonOf(t, git, "spoke-1", "reloader"); pin.Version != "" {
		t.Errorf("reloader's pin on spoke-1 must be gone, got %q", pin.Version)
	}
	if string(git.files["configuration/addons-catalog.yaml"]) != v3CatalogBefore {
		t.Error("a v4 repo's upgrade must never write the old catalog file")
	}
}

// Add-to-catalog (walk 1 L11): a version that was never released is refused.
func TestAddToCatalog_VersionThatDoesNotExist_IsRefused(t *testing.T) {
	git := newMockGitProvider()
	orch := seedPodinfoV4Repo(t, git, "6.7.1")
	orch.SetChartVersionCheck(func(_ context.Context, _, _, version string) (bool, bool, error) {
		return version != "9.9.9", true, nil
	})

	for _, dryRun := range []bool{false, true} {
		_, err := orch.AddToCatalog(context.Background(), AddToCatalogRequest{
			DryRun: dryRun,
			Addons: []CatalogAddonInput{{
				Name: "metrics-server", RepoURL: "https://kubernetes-sigs.github.io/metrics-server",
				Chart: "metrics-server", Version: "9.9.9", Namespace: "kube-system",
			}},
		})
		if !errors.Is(err, ErrChartVersionNotFound) {
			t.Fatalf("dryRun=%v: expected ErrChartVersionNotFound, got %v", dryRun, err)
		}
		want := "Version 9.9.9 of metrics-server was not found in its chart repository (https://kubernetes-sigs.github.io/metrics-server). Nothing was changed. Pick a version that the repository lists."
		if err.Error() != want {
			t.Errorf("sentence =\n  %q\nwant\n  %q", err.Error(), want)
		}
	}
	if len(git.prs) != 0 {
		t.Error("no pull request may be opened")
	}
	if strings.Contains(string(git.files[config.AddonCatalogPath]), "9.9.9") {
		t.Error("the version reached catalog.yaml")
	}
}

// Add-to-catalog with an unreachable chart repository still adds (Sharko may
// have no route to a repository ArgoCD can reach) and says the version was
// not checked.
func TestAddToCatalog_ChartRepositoryUnreachable_AddsAndSaysSo(t *testing.T) {
	git := newMockGitProvider()
	orch := seedPodinfoV4Repo(t, git, "6.7.1")
	orch.SetChartVersionCheck(func(context.Context, string, string, string) (bool, bool, error) {
		return false, true, errors.New("dial tcp: i/o timeout")
	})
	result, err := orch.AddToCatalog(context.Background(), AddToCatalogRequest{
		Addons: []CatalogAddonInput{{
			Name: "metrics-server", RepoURL: "https://kubernetes-sigs.github.io/metrics-server",
			Chart: "metrics-server", Version: "3.12.1", Namespace: "kube-system",
		}},
	})
	if err != nil {
		t.Fatalf("the add must still go through: %v", err)
	}
	want := "Sharko could not reach the chart repository of metrics-server, so it did not check that version 3.12.1 exists. If the version is wrong, ArgoCD will not be able to install it."
	found := false
	for _, w := range result.Warnings {
		if w == want {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %q, want one to be exactly %q", result.Warnings, want)
	}
}

// Editing the catalog entry's version goes through the same check.
func TestEditCatalogEntry_VersionThatDoesNotExist_IsRefused(t *testing.T) {
	git := newMockGitProvider()
	orch := seedPodinfoV4Repo(t, git, "6.7.1")
	bad := "99.0.0"
	_, err := orch.EditCatalogEntry(context.Background(), EditCatalogEntryRequest{Name: "podinfo", Version: &bad})
	if !errors.Is(err, ErrChartVersionNotFound) {
		t.Fatalf("expected ErrChartVersionNotFound, got %v", err)
	}
	ns := "podinfo-system"
	if _, err := orch.EditCatalogEntry(context.Background(), EditCatalogEntryRequest{Name: "podinfo", Namespace: &ns, DryRun: true}); err != nil {
		t.Fatalf("an edit that does not touch the version must not be checked: %v", err)
	}
}

func countDifferentLines(a, b string) int {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	if len(al) != len(bl) {
		return -1
	}
	n := 0
	for i := range al {
		if al[i] != bl[i] {
			n++
		}
	}
	return n
}
