package orchestrator

// Tests for the wave 2.5 review fixes on the catalog write path:
// B-1 (a Marketplace add with no version), M1 (the surgical file edit),
// M2 (validate only what the request touches), F4 (a half-converted repo),
// and the confirm flag on the combined add-and-enable.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/config"
)

// curatedForTest builds a one-entry Marketplace list so from_marketplace
// has something to copy.
func curatedForTest(t *testing.T, name, repo, chart string) *catalog.Catalog {
	t.Helper()
	y := "version: \"1\"\naddons:\n" +
		"  - name: " + name + "\n" +
		"    description: Test entry.\n" +
		"    chart: " + chart + "\n" +
		"    repo: " + repo + "\n" +
		"    default_namespace: " + name + "\n" +
		"    license: Apache-2.0\n" +
		"    category: security\n" +
		"    maintainers: [\"test\"]\n" +
		"    curated_by: [\"cncf-graduated\"]\n"
	c, err := catalog.LoadBytes([]byte(y))
	if err != nil {
		t.Fatalf("building the test Marketplace list: %v", err)
	}
	return c
}

// TestAddToCatalog_MarketplaceAddResolvesVersion is review B-1: the wizard
// and the combo both send from_marketplace with no version, because the
// curated list ships none. The server fills in the newest version it knows
// and that resolved pin lands in the entry, so the pull request diff shows
// exactly what the org is approving.
func TestAddToCatalog_MarketplaceAddResolvesVersion(t *testing.T) {
	git := newMockGitProvider()
	o := newTestOrchestratorForCatalog(t, git)
	o.SetCuratedCatalog(curatedForTest(t, "cert-manager", "https://charts.jetstack.io", "cert-manager"))

	var askedFor [3]string
	o.SetLatestVersionResolver(func(_ context.Context, addon, repo, chart string) (string, bool) {
		askedFor = [3]string{addon, repo, chart}
		return "1.14.5", true
	})

	if _, err := o.AddToCatalog(context.Background(), AddToCatalogRequest{
		Addons: []CatalogAddonInput{{Name: "cert-manager", FromMarketplace: true}},
	}); err != nil {
		t.Fatalf("AddToCatalog: %v", err)
	}

	if askedFor != [3]string{"cert-manager", "https://charts.jetstack.io", "cert-manager"} {
		t.Errorf("the resolver was asked about %v — it needs the addon name and the chart location", askedFor)
	}
	entry := readWrittenCatalog(t, git).Addons["cert-manager"]
	if entry.Version != "1.14.5" {
		t.Errorf("the resolved version must land in the entry, got %q", entry.Version)
	}
}

// TestAddToCatalog_MarketplaceAddWithNoVersionData is the other half of
// B-1: when Sharko genuinely knows no versions for the chart, it says so in
// plain words and asks the person to pick one, instead of the old
// gateway-error dead end.
func TestAddToCatalog_MarketplaceAddWithNoVersionData(t *testing.T) {
	git := newMockGitProvider()
	o := newTestOrchestratorForCatalog(t, git)
	o.SetCuratedCatalog(curatedForTest(t, "cert-manager", "oci://registry.example.com/charts", "cert-manager"))
	o.SetLatestVersionResolver(func(context.Context, string, string, string) (string, bool) {
		return "", false
	})

	_, err := o.AddToCatalog(context.Background(), AddToCatalogRequest{
		Addons: []CatalogAddonInput{{Name: "cert-manager", FromMarketplace: true}},
	})
	if !errors.Is(err, ErrCatalogVersionUnknown) {
		t.Fatalf("expected ErrCatalogVersionUnknown, got %v", err)
	}
	if !strings.Contains(err.Error(), "pick a version") {
		t.Errorf("the message must ask for a version in plain words, got %q", err.Error())
	}
	if _, written := git.files[config.AddonCatalogPath]; written {
		t.Error("nothing should have been written")
	}
}

// TestAddToCatalog_NoResolverStillAsks: a server with no version data
// wired at all behaves the same way — it asks, it never guesses.
func TestAddToCatalog_NoResolverStillAsks(t *testing.T) {
	git := newMockGitProvider()
	o := newTestOrchestratorForCatalog(t, git)
	o.SetCuratedCatalog(curatedForTest(t, "cert-manager", "https://charts.jetstack.io", "cert-manager"))

	_, err := o.AddToCatalog(context.Background(), AddToCatalogRequest{
		Addons: []CatalogAddonInput{{Name: "cert-manager", FromMarketplace: true}},
	})
	if !errors.Is(err, ErrCatalogVersionUnknown) {
		t.Fatalf("expected ErrCatalogVersionUnknown, got %v", err)
	}
}

// TestAddToCatalog_OwnChartStillNeedsAVersion: the type-it-yourself door
// has a version box, so an empty one is a request problem, not a
// "Sharko doesn't know" problem — and it must not be a 502 either.
func TestAddToCatalog_OwnChartStillNeedsAVersion(t *testing.T) {
	git := newMockGitProvider()
	o := newTestOrchestratorForCatalog(t, git)

	_, err := o.AddToCatalog(context.Background(), AddToCatalogRequest{
		Addons: []CatalogAddonInput{{
			Name: "billing-api", RepoURL: "oci://registry.example.com/charts", Chart: "billing-api",
		}},
	})
	if !errors.Is(err, ErrCatalogEntryIncomplete) {
		t.Fatalf("expected ErrCatalogEntryIncomplete, got %v", err)
	}
}

// TestAddToCatalog_ComboNeedsConfirmation: the combined form changes what
// runs on a real cluster, so it asks for the same yes the v4 enable asks
// for. The catalog-only form does not.
func TestAddToCatalog_ComboNeedsConfirmation(t *testing.T) {
	git := newMockGitProvider()
	registerV4Cluster(t, git, "prod-eu")
	o := newTestOrchestratorForCatalog(t, git)

	req := AddToCatalogRequest{
		Addons: []CatalogAddonInput{{
			Name: "cert-manager", RepoURL: "https://charts.jetstack.io",
			Chart: "cert-manager", Version: "1.14.5",
		}},
		EnableOnCluster: "prod-eu",
	}
	_, err := o.AddToCatalog(context.Background(), req)
	if !errors.Is(err, ErrCatalogConfirmationRequired) {
		t.Fatalf("expected ErrCatalogConfirmationRequired, got %v", err)
	}
	if _, written := git.files[config.AddonCatalogPath]; written {
		t.Error("nothing should have been written without the confirmation")
	}

	// A dry run is allowed to skip it — that IS the "show me first" step.
	req.DryRun = true
	if _, err := o.AddToCatalog(context.Background(), req); err != nil {
		t.Fatalf("a dry run must not need confirmation: %v", err)
	}
}

// TestAddToCatalog_CatalogOnlyNeedsNoConfirmation: adding to the approved
// list only opens a pull request against a list — the merge is the
// decision, so there is nothing extra to confirm here.
func TestAddToCatalog_CatalogOnlyNeedsNoConfirmation(t *testing.T) {
	git := newMockGitProvider()
	o := newTestOrchestratorForCatalog(t, git)

	if _, err := o.AddToCatalog(context.Background(), AddToCatalogRequest{
		Addons: []CatalogAddonInput{{
			Name: "cert-manager", RepoURL: "https://charts.jetstack.io",
			Chart: "cert-manager", Version: "1.14.5",
		}},
	}); err != nil {
		t.Fatalf("AddToCatalog: %v", err)
	}
}

// TestAddToCatalog_BrokenNeighbourDoesNotBlockTheAdd is review M2. The read
// path deliberately tolerates a half-written hand-edited entry (it comes
// back deployable: false with missing_fields), so a person can save a
// draft. Validating the WHOLE file on write meant that draft refused every
// unrelated add — and named an addon the caller had never mentioned.
func TestAddToCatalog_BrokenNeighbourDoesNotBlockTheAdd(t *testing.T) {
	git := newMockGitProvider()
	git.files[config.AddonCatalogPath] = []byte(
		"apiVersion: sharko.dev/v1\n" +
			"kind: AddonCatalog\n" +
			"addons:\n" +
			"    half-finished:\n" +
			"        chart: half-finished\n")
	o := newTestOrchestratorForCatalog(t, git)

	if _, err := o.AddToCatalog(context.Background(), AddToCatalogRequest{
		Addons: []CatalogAddonInput{{
			Name: "cert-manager", RepoURL: "https://charts.jetstack.io",
			Chart: "cert-manager", Version: "1.14.5",
		}},
	}); err != nil {
		t.Fatalf("a half-written neighbour must not block an unrelated add: %v", err)
	}

	spec := readWrittenCatalog(t, git)
	if _, ok := spec.Addons["cert-manager"]; !ok {
		t.Error("cert-manager missing from the written catalog")
	}
	if _, ok := spec.Addons["half-finished"]; !ok {
		t.Error("the half-written entry must survive untouched, not be dropped")
	}
}

// TestAddToCatalog_OwnEntryIsStillChecked: narrowing the check to the
// request must not mean nothing is checked. An entry the request itself
// leaves incomplete is still refused before anything is written.
func TestAddToCatalog_OwnEntryIsStillChecked(t *testing.T) {
	git := newMockGitProvider()
	o := newTestOrchestratorForCatalog(t, git)

	_, err := o.AddToCatalog(context.Background(), AddToCatalogRequest{
		Addons: []CatalogAddonInput{{Name: "cert-manager", Chart: "cert-manager", Version: "1.14.5"}},
	})
	if err == nil {
		t.Fatal("an entry with no chart location must be refused")
	}
	if _, written := git.files[config.AddonCatalogPath]; written {
		t.Error("nothing should have been written")
	}
}

// TestAddToCatalog_EmptyCatalogFileSaysWhatIsMissing: a catalog.yaml
// somebody created and left blank used to come back as a gateway error,
// which points at the git host for a problem that is in the repo.
func TestAddToCatalog_EmptyCatalogFileSaysWhatIsMissing(t *testing.T) {
	git := newMockGitProvider()
	git.files[config.AddonCatalogPath] = []byte("\n   \n")
	o := newTestOrchestratorForCatalog(t, git)

	_, err := o.AddToCatalog(context.Background(), AddToCatalogRequest{
		Addons: []CatalogAddonInput{{
			Name: "cert-manager", RepoURL: "https://charts.jetstack.io",
			Chart: "cert-manager", Version: "1.14.5",
		}},
	})
	if !errors.Is(err, ErrCatalogFileEmpty) {
		t.Fatalf("expected ErrCatalogFileEmpty, got %v", err)
	}
	if !strings.Contains(err.Error(), "apiVersion") || !strings.Contains(err.Error(), "kind") {
		t.Errorf("the message must name the two header lines, got %q", err.Error())
	}
}

// TestAddToCatalog_KeepsCommentsAndOrder is review M1: the file is edited
// where the change belongs, not re-rendered from scratch, so hand-written
// comments and the person's own ordering survive.
func TestAddToCatalog_KeepsCommentsAndOrder(t *testing.T) {
	git := newMockGitProvider()
	original := "apiVersion: sharko.dev/v1\n" +
		"kind: AddonCatalog\n" +
		"\n" +
		"# Approved by the platform team, 2026-05.\n" +
		"addons:\n" +
		"    vault:\n" +
		"        # pinned until the 1.28 CRD change lands\n" +
		"        repoURL: https://helm.releases.hashicorp.com\n" +
		"        chart: vault\n" +
		"        version: 0.27.0\n" +
		"    cert-manager:\n" +
		"        repoURL: https://charts.jetstack.io\n" +
		"        chart: cert-manager\n" +
		"        version: 1.14.5\n" +
		"# nothing below here yet\n"
	git.files[config.AddonCatalogPath] = []byte(original)
	o := newTestOrchestratorForCatalog(t, git)

	result, err := o.AddToCatalog(context.Background(), AddToCatalogRequest{
		Addons: []CatalogAddonInput{{
			Name: "metrics-server", RepoURL: "https://kubernetes-sigs.github.io/metrics-server",
			Chart: "metrics-server", Version: "3.12.1",
		}},
	})
	if err != nil {
		t.Fatalf("AddToCatalog: %v", err)
	}

	got := string(git.files[config.AddonCatalogPath])
	for _, keep := range []string{
		"# Approved by the platform team, 2026-05.",
		"# pinned until the 1.28 CRD change lands",
		"# nothing below here yet",
	} {
		if !strings.Contains(got, keep) {
			t.Errorf("comment lost from catalog.yaml: %q\n---\n%s", keep, got)
		}
	}
	if strings.Index(got, "vault:") > strings.Index(got, "cert-manager:") {
		t.Errorf("the person's own ordering was re-sorted:\n%s", got)
	}
	spec := readWrittenCatalog(t, git)
	for _, name := range []string{"vault", "cert-manager", "metrics-server"} {
		if _, ok := spec.Addons[name]; !ok {
			t.Errorf("%s missing from the written catalog:\n%s", name, got)
		}
	}
	for _, w := range result.Warnings {
		if strings.Contains(w, "rewrote the whole file") {
			t.Errorf("a surgical edit was possible, so there should be no rewrite warning: %q", w)
		}
	}
}

// TestAddToCatalog_ReplacesInPlaceKeepingNeighbourComments: re-adding an
// addon rewrites only its own block.
func TestAddToCatalog_ReplacesInPlaceKeepingNeighbourComments(t *testing.T) {
	git := newMockGitProvider()
	git.files[config.AddonCatalogPath] = []byte(
		"apiVersion: sharko.dev/v1\n" +
			"kind: AddonCatalog\n" +
			"addons:\n" +
			"    vault:\n" +
			"        # keep this note\n" +
			"        repoURL: https://helm.releases.hashicorp.com\n" +
			"        chart: vault\n" +
			"        version: 0.27.0\n" +
			"    cert-manager:\n" +
			"        repoURL: https://charts.jetstack.io\n" +
			"        chart: cert-manager\n" +
			"        version: 1.14.5\n")
	o := newTestOrchestratorForCatalog(t, git)

	if _, err := o.AddToCatalog(context.Background(), AddToCatalogRequest{
		Addons: []CatalogAddonInput{{
			Name: "cert-manager", RepoURL: "https://charts.jetstack.io",
			Chart: "cert-manager", Version: "1.15.0",
		}},
	}); err != nil {
		t.Fatalf("AddToCatalog: %v", err)
	}

	got := string(git.files[config.AddonCatalogPath])
	if !strings.Contains(got, "# keep this note") {
		t.Errorf("the untouched neighbour's comment was lost:\n%s", got)
	}
	if v := readWrittenCatalog(t, git).Addons["cert-manager"].Version; v != "1.15.0" {
		t.Errorf("cert-manager version = %q, want 1.15.0\n%s", v, got)
	}
	if v := readWrittenCatalog(t, git).Addons["vault"].Version; v != "0.27.0" {
		t.Errorf("vault was changed by an unrelated add: version = %q", v)
	}
}

// TestAddToCatalog_WarnsWhenItHadToRewrite: the surgical edit is not always
// possible. When it is not, the whole file is re-rendered — which is always
// correct and sometimes ugly — and the caller is told so it can say so in
// the pull request instead of the reviewer discovering it.
func TestAddToCatalog_WarnsWhenItHadToRewrite(t *testing.T) {
	git := newMockGitProvider()
	// A flow-style addons block: valid YAML, loads fine, but not a shape
	// the line-splicer can edit safely.
	git.files[config.AddonCatalogPath] = []byte(
		"apiVersion: sharko.dev/v1\n" +
			"kind: AddonCatalog\n" +
			"addons: {vault: {repoURL: https://helm.releases.hashicorp.com, chart: vault, version: 0.27.0}}\n")
	o := newTestOrchestratorForCatalog(t, git)

	result, err := o.AddToCatalog(context.Background(), AddToCatalogRequest{
		Addons: []CatalogAddonInput{{
			Name: "cert-manager", RepoURL: "https://charts.jetstack.io",
			Chart: "cert-manager", Version: "1.14.5",
		}},
	})
	if err != nil {
		t.Fatalf("AddToCatalog: %v", err)
	}

	warned := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "rewrote the whole file") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("a whole-file rewrite must be warned about, got warnings %+v", result.Warnings)
	}
	spec := readWrittenCatalog(t, git)
	if _, ok := spec.Addons["vault"]; !ok {
		t.Error("vault lost in the rewrite")
	}
	if _, ok := spec.Addons["cert-manager"]; !ok {
		t.Error("cert-manager missing after the rewrite")
	}
}

// TestAddToCatalog_RefusesHalfConvertedRepo is review F4: a repo carrying
// engine.yaml AND the old v3 files writes into the half nothing reads, so
// nothing is written at all.
func TestAddToCatalog_RefusesHalfConvertedRepo(t *testing.T) {
	git := newMockGitProvider()
	git.files[EnginePinPath] = []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n")
	git.files[V3SecondaryMarkerPath] = []byte("clusters:\n  - name: prod-eu\n")
	o := newTestOrchestratorForCatalog(t, git)

	_, err := o.AddToCatalog(context.Background(), AddToCatalogRequest{
		Addons: []CatalogAddonInput{{
			Name: "cert-manager", RepoURL: "https://charts.jetstack.io",
			Chart: "cert-manager", Version: "1.14.5",
		}},
	})
	if !errors.Is(err, ErrMixedRepoLayout) {
		t.Fatalf("expected ErrMixedRepoLayout, got %v", err)
	}
	if _, written := git.files[config.AddonCatalogPath]; written {
		t.Error("nothing should have been written to a half-converted repo")
	}
}

// TestMigrationStatus_MixedLayoutSaysSo: the status a person reads must
// name the ambiguity instead of saying "already the current format", which
// sends them away from the only thing that would fix it.
func TestMigrationStatus_MixedLayoutSaysSo(t *testing.T) {
	git := newMockGitProvider()
	git.files[EnginePinPath] = []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n")
	git.files[V3BootstrapMarkerPath] = []byte("apiVersion: v2\nname: cluster-addons\n")
	o := newTestOrchestratorForCatalog(t, git)

	status, err := o.MigrationStatus(context.Background())
	if err != nil {
		t.Fatalf("MigrationStatus: %v", err)
	}
	if status.Format != RepoFormatMixed {
		t.Errorf("format = %q, want %q", status.Format, RepoFormatMixed)
	}
	if status.MigrationAvailable {
		t.Error("a half-converted repo has no clean migration to offer")
	}
	if !strings.Contains(status.Message, "both the old and the new layout") {
		t.Errorf("the message must name the ambiguity, got %q", status.Message)
	}
}

// TestHasV3Markers_ConfiguredRegistryPath is review F3: an operator who
// keeps the cluster registry somewhere other than the default still has a
// live v3 repo, and calling it "empty" is what invites a v4 init on top of
// a running fleet.
func TestHasV3Markers_ConfiguredRegistryPath(t *testing.T) {
	git := newMockGitProvider()
	git.files["gitops/my-clusters.yaml"] = []byte("clusters:\n  - name: prod-eu\n")

	if HasV3Markers(context.Background(), git, "main") {
		t.Fatal("without the configured path there is nothing to find — the test fixture is wrong")
	}
	if !HasV3Markers(context.Background(), git, "main", "gitops/my-clusters.yaml") {
		t.Error("a configured registry path must count as a v3 marker")
	}
}
