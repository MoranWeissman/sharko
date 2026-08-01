package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

// seedClusterAddons writes an already-enabled cluster-addons/<cluster>.yaml with
// one addon entry at the given version, using the real
// models.SaveClusterAddons writer so the fixture is byte-identical to what
// EnableAddonV4 would have produced.
func seedClusterAddons(t *testing.T, git *mockGitProvider, cluster, addon, version string) {
	t.Helper()
	body, err := models.SaveClusterAddons(models.ClusterAddonsSpec{
		Cluster: cluster,
		Addons: map[string]models.ClusterAddonsAddon{
			addon: {Enabled: true, Version: version},
		},
	})
	if err != nil {
		t.Fatalf("seeding cluster-addons/%s.yaml: %v", cluster, err)
	}
	git.files[assignPath(t, cluster)] = body
}

func TestUpgradeAddonClustersV4_DryRun_TouchesOnlySelectedClusters(t *testing.T) {
	git := newMockGitProvider()
	orch := newV4TestOrchestrator(t, git)

	seedClusterAddons(t, git, "prod-eu", "cert-manager", "1.12.0")
	seedClusterAddons(t, git, "staging-us", "cert-manager", "1.12.0")
	seedClusterAddons(t, git, "dev", "cert-manager", "1.12.0")

	result, err := orch.UpgradeAddonClustersV4(context.Background(), UpgradeAddonClustersV4Request{
		Addon:    "cert-manager",
		Clusters: []string{"staging-us", "prod-eu"}, // deliberately unsorted + a dup below
		Version:  "1.14.5",
		DryRun:   true,
	})
	if err != nil {
		t.Fatalf("UpgradeAddonClustersV4: %v", err)
	}
	if result.DryRun == nil {
		t.Fatal("expected a DryRun result")
	}
	if len(result.DryRun.FilesToWrite) != 2 {
		t.Fatalf("expected exactly 2 files in the preview, got %d: %+v", len(result.DryRun.FilesToWrite), result.DryRun.FilesToWrite)
	}
	gotPaths := map[string]bool{}
	for _, f := range result.DryRun.FilesToWrite {
		gotPaths[f.Path] = true
		if f.Action != "update" {
			t.Errorf("file %s: expected action=update, got %s", f.Path, f.Action)
		}
	}
	if !gotPaths[assignPath(t, "prod-eu")] || !gotPaths[assignPath(t, "staging-us")] {
		t.Errorf("expected prod-eu and staging-us in the preview, got %v", gotPaths)
	}
	if gotPaths[assignPath(t, "dev")] {
		t.Error("dev was not selected — it must not appear in the preview")
	}

	// Dry run must not write anything for real.
	if len(git.branches) != 0 {
		t.Errorf("expected no branch on dry run, got %v", git.branches)
	}
	if string(git.files[assignPath(t, "dev")]) == "" {
		t.Fatal("dev's file should still exist unchanged")
	}
	if strings.Contains(string(git.files[assignPath(t, "dev")]), "1.14.5") {
		t.Error("dev's file must be untouched by a dry run that didn't select it")
	}
}

func TestUpgradeAddonClustersV4_Apply_OnePRAllSelectedClusters(t *testing.T) {
	git := newMockGitProvider()
	orch := newV4TestOrchestrator(t, git)

	seedClusterAddons(t, git, "prod-eu", "cert-manager", "1.12.0")
	seedClusterAddons(t, git, "staging-us", "cert-manager", "1.12.0")
	seedClusterAddons(t, git, "dev", "cert-manager", "1.12.0")

	result, err := orch.UpgradeAddonClustersV4(context.Background(), UpgradeAddonClustersV4Request{
		Addon:    "cert-manager",
		Clusters: []string{"prod-eu", "staging-us"},
		Version:  "1.14.5",
		Yes:      true,
	})
	if err != nil {
		t.Fatalf("UpgradeAddonClustersV4: %v", err)
	}
	if result.DryRun != nil {
		t.Fatal("expected a real commit, not a dry-run result")
	}

	// One PR, one branch — not one per cluster.
	if len(git.branches) != 1 {
		t.Fatalf("expected exactly 1 branch (one PR), got %d: %v", len(git.branches), git.branches)
	}
	if len(git.prs) != 1 {
		t.Fatalf("expected exactly 1 PR, got %d", len(git.prs))
	}

	prodSpec, err := models.LoadClusterAddons(git.files[assignPath(t, "prod-eu")])
	if err != nil {
		t.Fatalf("reading back prod-eu: %v", err)
	}
	if prodSpec.Addons["cert-manager"].Version != "1.14.5" {
		t.Errorf("prod-eu cert-manager version = %q, want 1.14.5", prodSpec.Addons["cert-manager"].Version)
	}
	if !prodSpec.Addons["cert-manager"].Enabled {
		t.Error("prod-eu cert-manager must stay enabled after an upgrade")
	}

	stagingSpec, err := models.LoadClusterAddons(git.files[assignPath(t, "staging-us")])
	if err != nil {
		t.Fatalf("reading back staging-us: %v", err)
	}
	if stagingSpec.Addons["cert-manager"].Version != "1.14.5" {
		t.Errorf("staging-us cert-manager version = %q, want 1.14.5", stagingSpec.Addons["cert-manager"].Version)
	}

	// The unselected cluster keeps its old pin.
	devSpec, err := models.LoadClusterAddons(git.files[assignPath(t, "dev")])
	if err != nil {
		t.Fatalf("reading back dev: %v", err)
	}
	if devSpec.Addons["cert-manager"].Version != "1.12.0" {
		t.Errorf("dev cert-manager version = %q, want unchanged 1.12.0", devSpec.Addons["cert-manager"].Version)
	}
}

func TestUpgradeAddonClustersV4_RejectsClusterWithoutAddonEnabled_AllOrNothing(t *testing.T) {
	git := newMockGitProvider()
	orch := newV4TestOrchestrator(t, git)

	seedClusterAddons(t, git, "prod-eu", "cert-manager", "1.12.0")
	// staging-us has NO cert-manager entry at all.
	body, err := models.SaveClusterAddons(models.ClusterAddonsSpec{Cluster: "staging-us", Addons: map[string]models.ClusterAddonsAddon{}})
	if err != nil {
		t.Fatalf("seeding staging-us: %v", err)
	}
	git.files[assignPath(t, "staging-us")] = body

	_, err = orch.UpgradeAddonClustersV4(context.Background(), UpgradeAddonClustersV4Request{
		Addon:    "cert-manager",
		Clusters: []string{"prod-eu", "staging-us"},
		Version:  "1.14.5",
		Yes:      true,
	})
	if err == nil {
		t.Fatal("expected an error naming the cluster that doesn't have the addon enabled")
	}
	if !strings.Contains(err.Error(), "staging-us") || !strings.Contains(err.Error(), "cert-manager") {
		t.Errorf("error should name both the cluster and the addon, got: %v", err)
	}

	// All-or-nothing: prod-eu (which WAS valid) must not have been touched either.
	if len(git.branches) != 0 {
		t.Errorf("expected no branch — validation must run before any git write, got %v", git.branches)
	}
	prodSpec, loadErr := models.LoadClusterAddons(git.files[assignPath(t, "prod-eu")])
	if loadErr != nil {
		t.Fatalf("reading back prod-eu: %v", loadErr)
	}
	if prodSpec.Addons["cert-manager"].Version != "1.12.0" {
		t.Error("prod-eu must be untouched when a sibling cluster in the same request fails validation")
	}
}

func TestUpgradeAddonClustersV4_MissingClusterFile(t *testing.T) {
	git := newMockGitProvider()
	orch := newV4TestOrchestrator(t, git)

	_, err := orch.UpgradeAddonClustersV4(context.Background(), UpgradeAddonClustersV4Request{
		Addon:    "cert-manager",
		Clusters: []string{"never-registered"},
		Version:  "1.14.5",
		Yes:      true,
	})
	if err == nil || !strings.Contains(err.Error(), "never-registered") {
		t.Fatalf("expected an error naming the missing cluster, got: %v", err)
	}
}

func TestUpgradeAddonClustersV4_RequiresConfirmation(t *testing.T) {
	git := newMockGitProvider()
	orch := newV4TestOrchestrator(t, git)
	seedClusterAddons(t, git, "prod-eu", "cert-manager", "1.12.0")

	_, err := orch.UpgradeAddonClustersV4(context.Background(), UpgradeAddonClustersV4Request{
		Addon:    "cert-manager",
		Clusters: []string{"prod-eu"},
		Version:  "1.14.5",
		// Yes not set, DryRun not set.
	})
	if err == nil || !strings.Contains(err.Error(), "confirmation required") {
		t.Fatalf("expected a confirmation-required error, got: %v", err)
	}
}

func TestUpgradeAddonClustersV4_ValidatesInput(t *testing.T) {
	git := newMockGitProvider()
	orch := newV4TestOrchestrator(t, git)

	cases := []struct {
		name string
		req  UpgradeAddonClustersV4Request
		want string
	}{
		{"no addon", UpgradeAddonClustersV4Request{Clusters: []string{"prod-eu"}, Version: "1.0.0"}, "addon name"},
		{"no version", UpgradeAddonClustersV4Request{Addon: "cert-manager", Clusters: []string{"prod-eu"}}, "version"},
		{"no clusters", UpgradeAddonClustersV4Request{Addon: "cert-manager", Version: "1.0.0"}, "cluster"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := orch.UpgradeAddonClustersV4(context.Background(), tc.req)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected an error containing %q, got: %v", tc.want, err)
			}
		})
	}
}

func TestDedupSortedClusters(t *testing.T) {
	got := dedupSortedClusters([]string{"b", "a", "b", "", "c", "a"})
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
