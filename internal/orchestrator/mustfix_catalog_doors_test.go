package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/models"
)

// Review must-fix: catalog edit and add-to-catalog (for an add-on that is
// already there) move the chart for every cluster, like an upgrade. They left
// the global values file alone, so the old image tag Sharko had copied into it
// kept the old image running, and nothing was said.

func TestMustFix_CatalogEditVersionMovesTheSeededValuesToo(t *testing.T) {
	gPath, _ := v4GlobalValuesPath("podinfo")
	for _, dryRun := range []bool{true, false} {
		git := newMockGitProvider()
		orch := seedPodinfoV4Repo(t, git, "6.7.1")
		seedClusterAddonsEntry(t, git, "spoke-1", "podinfo", models.ClusterAddonsAddon{Enabled: true})
		before := string(git.files[gPath])
		if !strings.Contains(before, "tag: 6.7.1") {
			t.Fatalf("test setup: the seeded file should hold tag: 6.7.1:\n%s", before)
		}

		v := "6.8.0"
		res, err := orch.EditCatalogEntry(context.Background(), EditCatalogEntryRequest{Name: "podinfo", Version: &v, DryRun: dryRun})
		if err != nil {
			t.Fatal(err)
		}
		if dryRun {
			if string(git.files[gPath]) != before || len(git.prs) != 0 {
				t.Fatal("a preview wrote something")
			}
			var diff string
			for _, f := range res.DryRun.FilesToWrite {
				if f.Path == gPath {
					diff = f.Diff
				}
			}
			if !strings.Contains(diff, "tag: 6.8.0") {
				t.Errorf("the preview does not show the values file moving to tag: 6.8.0. Files: %+v", res.DryRun.FilesToWrite)
			}
			continue
		}
		if got := catalogVersionOf(t, git, "podinfo"); got != "6.8.0" {
			t.Fatalf("catalog version = %q, want 6.8.0", got)
		}
		after := string(git.files[gPath])
		if strings.Contains(after, "tag: 6.7.1") || !strings.Contains(after, "tag: 6.8.0") {
			t.Errorf("the chart moved to 6.8.0 and the values file still sets the old image tag:\n%s", after)
		}
		if !strings.Contains(after, "podinfo@6.8.0") {
			t.Errorf("the header line of the values file does not name the new chart version:\n%s", after)
		}
		if len(git.prs) != 1 {
			t.Errorf("want one pull request with both files, got %d", len(git.prs))
		}
	}
}

func TestMustFix_AddToCatalogNewVersionMovesTheSeededValuesToo(t *testing.T) {
	gPath, _ := v4GlobalValuesPath("podinfo")
	for _, dryRun := range []bool{true, false} {
		git := newMockGitProvider()
		orch := seedPodinfoV4Repo(t, git, "6.7.1")
		seedClusterAddonsEntry(t, git, "spoke-1", "podinfo", models.ClusterAddonsAddon{Enabled: true})
		before := string(git.files[gPath])

		res, err := orch.AddToCatalog(context.Background(), AddToCatalogRequest{
			Addons: []CatalogAddonInput{{Name: "podinfo", RepoURL: podinfoRepo, Chart: "podinfo", Version: "6.8.0", Namespace: "podinfo"}},
			DryRun: dryRun,
		})
		if err != nil {
			t.Fatal(err)
		}
		if dryRun {
			if string(git.files[gPath]) != before || len(git.prs) != 0 {
				t.Fatal("a preview wrote something")
			}
			var diff string
			for _, f := range res.DryRun.FilesToWrite {
				if f.Path == gPath {
					diff = f.Diff
				}
			}
			if !strings.Contains(diff, "tag: 6.8.0") {
				t.Errorf("the preview does not show the values file moving to tag: 6.8.0. Files: %+v", res.DryRun.FilesToWrite)
			}
			continue
		}
		after := string(git.files[gPath])
		if strings.Contains(after, "tag: 6.7.1") || !strings.Contains(after, "tag: 6.8.0") {
			t.Errorf("the chart moved to 6.8.0 and the values file still sets the old image tag:\n%s", after)
		}
		if len(git.prs) != 1 {
			t.Errorf("want one pull request with both files, got %d", len(git.prs))
		}
	}
}

// A setting a person changed is never moved.
func TestMustFix_VersionMoveByOtherDoorsKeepsAUserEdit(t *testing.T) {
	gPath, _ := v4GlobalValuesPath("podinfo")
	doors := map[string]func(orch *Orchestrator) ([]string, error){
		"catalog edit": func(orch *Orchestrator) ([]string, error) {
			v := "6.8.0"
			res, err := orch.EditCatalogEntry(context.Background(), EditCatalogEntryRequest{Name: "podinfo", Version: &v})
			if err != nil {
				return nil, err
			}
			return res.Warnings, nil
		},
		"add to catalog": func(orch *Orchestrator) ([]string, error) {
			res, err := orch.AddToCatalog(context.Background(), AddToCatalogRequest{
				Addons: []CatalogAddonInput{{Name: "podinfo", RepoURL: podinfoRepo, Chart: "podinfo", Version: "6.8.0", Namespace: "podinfo"}},
			})
			if err != nil {
				return nil, err
			}
			return res.Warnings, nil
		},
	}
	for name, door := range doors {
		t.Run(name, func(t *testing.T) {
			git := newMockGitProvider()
			orch := seedPodinfoV4Repo(t, git, "6.7.1")
			// The user pinned the image by hand.
			git.files[gPath] = []byte(strings.Replace(string(git.files[gPath]), "tag: 6.7.1", "tag: 6.7.0-custom", 1))

			warnings, err := door(orch)
			if err != nil {
				t.Fatal(err)
			}
			after := string(git.files[gPath])
			if !strings.Contains(after, "tag: 6.7.0-custom") {
				t.Errorf("a value the user set was changed:\n%s", after)
			}
			// Same as the upgrade: a value the user chose is theirs, so
			// there is nothing to warn about.
			for _, w := range warnings {
				if strings.Contains(w, "image.tag") {
					t.Errorf("a value the user set on purpose was reported as left over: %q", w)
				}
			}
			// Everything else Sharko had copied still follows the chart.
			if !strings.Contains(after, "podinfo@6.8.0") {
				t.Errorf("the header line does not name the new chart version:\n%s", after)
			}
		})
	}
}

// A file Sharko did not write is never edited by these doors either.
func TestMustFix_VersionMoveByOtherDoorsLeavesAUserWrittenFileAlone(t *testing.T) {
	gPath, _ := v4GlobalValuesPath("podinfo")
	git := newMockGitProvider()
	orch := seedPodinfoV4Repo(t, git, "6.7.1")
	own := "image:\n  tag: 6.7.1\n"
	git.files[gPath] = []byte(own)
	v := "6.8.0"
	res, err := orch.EditCatalogEntry(context.Background(), EditCatalogEntryRequest{Name: "podinfo", Version: &v})
	if err != nil {
		t.Fatal(err)
	}
	if string(git.files[gPath]) != own {
		t.Errorf("a values file a person wrote was edited:\n%s", git.files[gPath])
	}
	want := gPath + " still sets the old chart's value for: image.tag (6.7.1, new chart default 6.8.0). Sharko did not change these. They stay as they are after the upgrade until you edit that file."
	if len(res.Warnings) != 1 || res.Warnings[0] != want {
		t.Errorf("warnings = %q, want exactly %q", res.Warnings, want)
	}
}

// An edit that does not touch the version does not touch the values file.
func TestMustFix_CatalogEditWithoutAVersionChangeLeavesValuesAlone(t *testing.T) {
	gPath, _ := v4GlobalValuesPath("podinfo")
	git := newMockGitProvider()
	orch := seedPodinfoV4Repo(t, git, "6.7.1")
	before := string(git.files[gPath])
	ns := "podinfo-2"
	res, err := orch.EditCatalogEntry(context.Background(), EditCatalogEntryRequest{Name: "podinfo", Namespace: &ns})
	if err != nil {
		t.Fatal(err)
	}
	if string(git.files[gPath]) != before || len(res.Warnings) != 0 {
		t.Errorf("a namespace edit changed the values file or warned: %q", res.Warnings)
	}
}

// Review must-fix: add-to-catalog on an add-on already in the catalog built a
// whole new entry from the request. Everything the request did not carry was
// deleted, and the answer said "added".
func TestMustFix_ReAddKeepsTheRestOfTheEntry(t *testing.T) {
	seed := func(t *testing.T) (*mockGitProvider, *Orchestrator, config.AddonCatalogEntry) {
		git := newMockGitProvider()
		orch := seedPodinfoV4Repo(t, git, "6.7.1")
		full := config.AddonCatalogEntry{
			RepoURL: podinfoRepo, Chart: "podinfo", Version: "6.7.1", Namespace: "podinfo",
			ExtraHelmValues: map[string]string{"replicaCount": "3"},
			Secrets: []config.AddonSecretRequirement{{
				Name: "podinfo-token", Description: "The token podinfo calls its backend with.",
				Push: &models.AddonSecretRef{SecretName: "podinfo-token", Namespace: "podinfo", Keys: map[string]string{"token": "secret/podinfo/token"}},
			}},
			AdditionalSources: []models.AddonSource{{RepoURL: "https://charts.example.com", Chart: "extra", Version: "1.0.0"}},
		}
		git.files[config.AddonCatalogPath] = mustSaveCatalog(t, config.AddonCatalogSpec{
			Addons: map[string]config.AddonCatalogEntry{"podinfo": full},
		})
		return git, orch, full
	}
	entryNow := func(t *testing.T, git *mockGitProvider) config.AddonCatalogEntry {
		t.Helper()
		spec, err := parseCatalogBody(git.files[config.AddonCatalogPath])
		if err != nil {
			t.Fatal(err)
		}
		return spec.Addons["podinfo"]
	}

	t.Run("same chart, version and namespace: already there, nothing opened", func(t *testing.T) {
		git, orch, full := seed(t)
		before := string(git.files[config.AddonCatalogPath])
		res, err := orch.AddToCatalog(context.Background(), AddToCatalogRequest{
			Addons: []CatalogAddonInput{{Name: "podinfo", RepoURL: podinfoRepo, Chart: "podinfo", Version: "6.7.1", Namespace: "podinfo"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		const want = "podinfo is already in your catalog, with the same chart, version and namespace. Nothing to do, and no pull request was opened."
		if res.Message != want {
			t.Errorf("message = %q, want %q", res.Message, want)
		}
		if len(res.Added) != 0 || len(res.AlreadyInCatalog) != 1 || len(git.prs) != 0 {
			t.Errorf("added=%v already=%v pull requests=%d, want nothing added and nothing opened", res.Added, res.AlreadyInCatalog, len(git.prs))
		}
		if string(git.files[config.AddonCatalogPath]) != before {
			t.Error("catalog.yaml was changed")
		}
		_ = full
	})

	t.Run("another version: the version moves, the rest stays", func(t *testing.T) {
		git, orch, full := seed(t)
		if _, err := orch.AddToCatalog(context.Background(), AddToCatalogRequest{
			Addons: []CatalogAddonInput{{Name: "podinfo", RepoURL: podinfoRepo, Chart: "podinfo", Version: "6.8.0", Namespace: "podinfo"}},
		}); err != nil {
			t.Fatal(err)
		}
		got := entryNow(t, git)
		if got.Version != "6.8.0" {
			t.Errorf("version = %q, want 6.8.0", got.Version)
		}
		if got.ExtraHelmValues["replicaCount"] != "3" {
			t.Errorf("extraHelmValues were deleted: %v", got.ExtraHelmValues)
		}
		if len(got.Secrets) != 1 || got.Secrets[0].Name != full.Secrets[0].Name || got.Secrets[0].Push == nil {
			t.Errorf("secrets were deleted: %+v", got.Secrets)
		}
		if len(got.AdditionalSources) != 1 {
			t.Errorf("additionalSources were deleted: %+v", got.AdditionalSources)
		}
		if !strings.Contains(string(git.files[config.AddonCatalogPath]), "replicaCount") {
			t.Errorf("catalog.yaml lost extraHelmValues:\n%s", git.files[config.AddonCatalogPath])
		}
	})

	t.Run("a part the request does set wins", func(t *testing.T) {
		git, orch, _ := seed(t)
		if _, err := orch.AddToCatalog(context.Background(), AddToCatalogRequest{
			Addons: []CatalogAddonInput{{Name: "podinfo", RepoURL: podinfoRepo, Chart: "podinfo", Version: "6.7.1", Namespace: "podinfo",
				ExtraHelmValues: map[string]string{"replicaCount": "5"}}},
		}); err != nil {
			t.Fatal(err)
		}
		got := entryNow(t, git)
		if got.ExtraHelmValues["replicaCount"] != "5" {
			t.Errorf("the value the request set did not win: %v", got.ExtraHelmValues)
		}
		if len(got.Secrets) != 1 {
			t.Errorf("secrets were deleted: %+v", got.Secrets)
		}
	})
}

func TestKeepUnsetCatalogFields(t *testing.T) {
	old := config.AddonCatalogEntry{
		RepoURL: "https://old.example.com", Chart: "c", Version: "1.0.0", Namespace: "ns",
		Settings:        &config.AddonSettings{},
		Secrets:         []config.AddonSecretRequirement{{Name: "s"}},
		ExtraHelmValues: map[string]string{"a": "b"},
	}
	// What the Marketplace door builds when the request names nothing but
	// the add-on: its own guesses for every part.
	built := config.AddonCatalogEntry{
		RepoURL: "https://market.example.com", Chart: "c2", Version: "9.9.9", Namespace: "market-ns",
		Secrets: []config.AddonSecretRequirement{{Name: "from-market"}},
	}
	got := keepUnsetCatalogFields(old, built, CatalogAddonInput{Name: "x", FromMarketplace: true})
	if got.RepoURL != old.RepoURL || got.Chart != old.Chart || got.Version != old.Version || got.Namespace != old.Namespace {
		t.Errorf("a Marketplace guess replaced what the catalog says: %+v", got)
	}
	if got.Settings != old.Settings || len(got.Secrets) != 1 || got.Secrets[0].Name != "s" || got.ExtraHelmValues["a"] != "b" {
		t.Errorf("a part the request did not set was not kept: %+v", got)
	}
}
