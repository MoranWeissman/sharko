package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

// ---- walk 1 L10: adding an addon that is already in the catalog ----

func TestAddToCatalog_AlreadyThere_SaysSoAndOpensNothing(t *testing.T) {
	git := newMockGitProvider()
	o := newTestOrchestratorForCatalog(t, git)
	req := AddToCatalogRequest{Addons: []CatalogAddonInput{{
		Name: "billing-api", RepoURL: "oci://registry.example.com/charts",
		Chart: "billing-api", Version: "2.4.0", Namespace: "billing",
	}}}
	if _, err := o.AddToCatalog(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	prs := len(git.prs)
	branches := len(git.branches)

	for _, dry := range []bool{true, false} {
		req.DryRun = dry
		res, err := o.AddToCatalog(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.AlreadyInCatalog) != 1 || res.AlreadyInCatalog[0] != "billing-api" {
			t.Fatalf("dry=%v: AlreadyInCatalog = %v", dry, res.AlreadyInCatalog)
		}
		if len(res.Added) != 0 {
			t.Fatalf("dry=%v: it says it added %v, but nothing changed", dry, res.Added)
		}
		want := "billing-api is already in your catalog, with the same chart, version and namespace. Nothing to do, and no pull request was opened."
		if res.Message != want {
			t.Fatalf("dry=%v: message:\n%s\nwant:\n%s", dry, res.Message, want)
		}
		if res.PRUrl != "" {
			t.Fatalf("dry=%v: a pull request address came back: %q", dry, res.PRUrl)
		}
	}
	if len(git.prs) != prs || len(git.branches) != branches {
		t.Fatalf("a branch or pull request was opened for an addon that is already there")
	}
}

// A different version is a real change (the documented "re-adding is an
// update") and still opens a pull request.
func TestAddToCatalog_NewVersion_StillOpensAPullRequest(t *testing.T) {
	git := newMockGitProvider()
	o := newTestOrchestratorForCatalog(t, git)
	in := CatalogAddonInput{
		Name: "billing-api", RepoURL: "oci://registry.example.com/charts",
		Chart: "billing-api", Version: "2.4.0", Namespace: "billing",
	}
	if _, err := o.AddToCatalog(context.Background(), AddToCatalogRequest{Addons: []CatalogAddonInput{in}}); err != nil {
		t.Fatal(err)
	}
	in.Version = "2.5.0"
	res, err := o.AddToCatalog(context.Background(), AddToCatalogRequest{Addons: []CatalogAddonInput{in}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.AlreadyInCatalog) != 0 || res.PRUrl == "" {
		t.Fatalf("a changed version must open a pull request: %+v", res)
	}
}

// ---- walk 8 L01: a chart address that is not a Helm address ----

func TestAddToCatalog_RefusesAddressesHelmCannotPullFrom(t *testing.T) {
	for _, repo := range []string{"ftp://bad.example.com/charts", "file:///tmp/charts", "ssh://git@example.com/x"} {
		git := newMockGitProvider()
		o := newTestOrchestratorForCatalog(t, git)
		_, err := o.AddToCatalog(context.Background(), AddToCatalogRequest{Addons: []CatalogAddonInput{{
			Name: "x", RepoURL: repo, Chart: "x", Version: "1.0.0",
		}}})
		if !errors.Is(err, ErrCatalogEntryIncomplete) {
			t.Fatalf("%s: err = %v, want the entry refused", repo, err)
		}
		want := `addon "x" has a chart repository address that is not one Sharko can use. It must start with https:// or oci://`
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: message %q does not contain %q", repo, err.Error(), want)
		}
		if len(git.prs) != 0 {
			t.Fatalf("%s: a pull request was opened", repo)
		}
	}
	for _, ok := range []string{"https://charts.example.com", "oci://registry.example.com/charts", "HTTPS://Charts.Example.com"} {
		if !chartRepoSchemeOK(ok) {
			t.Errorf("%s must be accepted", ok)
		}
	}
}

// ---- walk 1 L12, walk 2 finding 6, walk 8 L12: turning off an unknown addon ----

func TestDisableAddonV4_AddonNotInCatalogAndNotOnCluster_IsRefused(t *testing.T) {
	git := newMockGitProvider()
	orch := newV4TestOrchestrator(t, git)
	if _, err := orch.EnableAddonV4(context.Background(), EnableAddonV4Request{
		Cluster: "prod-eu", Addon: "metrics-server", Yes: true,
	}); err != nil {
		t.Fatal(err)
	}
	before := string(git.files[assignPath(t, "prod-eu")])
	prs := len(git.prs)

	for _, dry := range []bool{true, false} {
		_, err := orch.DisableAddonV4(context.Background(), DisableAddonV4Request{
			Cluster: "prod-eu", Addon: "nosuchaddon", Yes: true, DryRun: dry,
		})
		if !errors.Is(err, ErrV4AddonNotInCatalog) {
			t.Fatalf("dry=%v: err = %v, want ErrV4AddonNotInCatalog", dry, err)
		}
		want := "nosuchaddon is not in your catalog and is not turned on for prod-eu, so there is nothing to turn off. Check the name."
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("dry=%v: message %q does not contain %q", dry, err.Error(), want)
		}
	}
	if string(git.files[assignPath(t, "prod-eu")]) != before || len(git.prs) != prs {
		t.Fatal("an unknown addon was written into the cluster's file")
	}
}

// An addon that is still listed on the cluster can always be turned off, even
// when it has left the catalog since.
func TestDisableAddonV4_ListedOnClusterButGoneFromCatalog_StillTurnsOff(t *testing.T) {
	git := newMockGitProvider()
	orch := newV4TestOrchestrator(t, git)
	if _, err := orch.EnableAddonV4(context.Background(), EnableAddonV4Request{
		Cluster: "prod-eu", Addon: "metrics-server", Yes: true,
	}); err != nil {
		t.Fatal(err)
	}
	delete(git.files, "catalog.yaml")
	git.files["catalog.yaml"] = []byte("apiVersion: sharko.dev/v1\nkind: AddonCatalog\naddons: {}\n")

	if _, err := orch.DisableAddonV4(context.Background(), DisableAddonV4Request{
		Cluster: "prod-eu", Addon: "metrics-server", Yes: true,
	}); err != nil {
		t.Fatalf("turning off an addon that is listed on the cluster failed: %v", err)
	}
	spec, _ := models.LoadClusterAddons(git.files[assignPath(t, "prod-eu")])
	if spec.Addons["metrics-server"].Enabled {
		t.Fatal("metrics-server is still on")
	}
}

// ---- walk 1 L13, walk 2 finding 9: the header of cluster-addons/<name>.yaml ----

func TestDisableAddonV4_KeepsTheFileHeader(t *testing.T) {
	git := newMockGitProvider()
	orch := newV4TestOrchestrator(t, git)
	if _, err := orch.EnableAddonV4(context.Background(), EnableAddonV4Request{
		Cluster: "prod-eu", Addon: "metrics-server", Yes: true,
	}); err != nil {
		t.Fatal(err)
	}
	path := assignPath(t, "prod-eu")
	header := "# Which approved addons run on prod-eu.\n# Turning an addon on or off for this cluster is done by editing this file.\n"
	git.files[path] = append([]byte(header), git.files[path]...)

	if _, err := orch.DisableAddonV4(context.Background(), DisableAddonV4Request{
		Cluster: "prod-eu", Addon: "metrics-server", Yes: true,
	}); err != nil {
		t.Fatal(err)
	}
	got := string(git.files[path])
	if !strings.HasPrefix(got, header) {
		t.Fatalf("the two header lines were dropped:\n%s", got)
	}
	if strings.Count(got, "yaml-language-server") != 1 {
		t.Fatalf("the schema line is doubled or gone:\n%s", got)
	}
}

// ---- walk 2 finding 11: "(v4)" and brackets in the pull request title and branch ----

func TestEnableAndDisableAddonV4_PullRequestTitleAndBranchCarryNoVersionTag(t *testing.T) {
	git := newMockGitProvider()
	orch := newV4TestOrchestrator(t, git)
	if _, err := orch.EnableAddonV4(context.Background(), EnableAddonV4Request{
		Cluster: "prod-eu", Addon: "metrics-server", Yes: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := orch.DisableAddonV4(context.Background(), DisableAddonV4Request{
		Cluster: "prod-eu", Addon: "metrics-server", Yes: true,
	}); err != nil {
		t.Fatal(err)
	}
	if len(git.prs) != 2 {
		t.Fatalf("pull requests = %d, want 2", len(git.prs))
	}
	wantTitles := []string{
		"sharko: enable addon metrics-server on cluster prod-eu",
		"sharko: disable addon metrics-server on cluster prod-eu",
	}
	for i, pr := range git.prs {
		if pr.Title != wantTitles[i] {
			t.Errorf("title %d = %q, want %q", i, pr.Title, wantTitles[i])
		}
		for _, bad := range []string{"(v4)", "(", ")"} {
			if strings.Contains(pr.Title, bad) || strings.Contains(pr.SourceBranch, bad) {
				t.Errorf("title %q or branch %q still carries %q", pr.Title, pr.SourceBranch, bad)
			}
		}
	}
}
