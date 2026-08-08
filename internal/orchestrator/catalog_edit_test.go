package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/config"
)

// newV4TestOrchestratorForCatalog seeds the v4 engine pin (so isV4Repo
// answers true) on top of newTestOrchestratorForCatalog's plain mock —
// EditCatalogEntry/DeleteFromCatalog refuse a v3 repo before anything
// else, so every test exercising the real edit/delete path needs the pin
// present. Tests that specifically exercise the v3-repo refusal build
// their own bare mockGitProvider instead of going through this helper.
func newV4TestOrchestratorForCatalog(t *testing.T, git *mockGitProvider) *Orchestrator {
	t.Helper()
	if _, ok := git.files[EnginePinPath]; !ok {
		git.files[EnginePinPath] = []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n")
	}
	return newTestOrchestratorForCatalog(t, git)
}

func seedCatalogForEdit(t *testing.T, git *mockGitProvider, spec config.AddonCatalogSpec) {
	t.Helper()
	git.files[config.AddonCatalogPath] = mustSaveCatalog(t, spec)
}

func strPtr(s string) *string { return &s }

// TestEditCatalogEntry_MergeSemantics_OnlyProvidedFieldsChange is the core
// PATCH contract: sending only `version` must leave repoURL/chart/namespace
// exactly as they were on disk, never rebuild the entry from scratch.
func TestEditCatalogEntry_MergeSemantics_OnlyProvidedFieldsChange(t *testing.T) {
	git := newMockGitProvider()
	o := newV4TestOrchestratorForCatalog(t, git)
	seedCatalogForEdit(t, git, config.AddonCatalogSpec{
		Addons: map[string]config.AddonCatalogEntry{
			"cert-manager": {
				RepoURL: "https://charts.jetstack.io", Chart: "cert-manager",
				Version: "1.14.5", Namespace: "cert-manager",
			},
		},
	})

	result, err := o.EditCatalogEntry(context.Background(), EditCatalogEntryRequest{
		Name:    "cert-manager",
		Version: strPtr("1.15.0"),
	})
	if err != nil {
		t.Fatalf("EditCatalogEntry: %v", err)
	}
	if result == nil || result.PRUrl == "" {
		t.Fatalf("expected a PR result, got %+v", result)
	}

	spec := readWrittenCatalog(t, git)
	entry, ok := spec.Addons["cert-manager"]
	if !ok {
		t.Fatalf("cert-manager missing after edit: %+v", spec.Addons)
	}
	if entry.Version != "1.15.0" {
		t.Errorf("version not updated, got %q", entry.Version)
	}
	if entry.RepoURL != "https://charts.jetstack.io" {
		t.Errorf("repoURL must survive an edit that didn't mention it, got %q", entry.RepoURL)
	}
	if entry.Chart != "cert-manager" {
		t.Errorf("chart must survive an edit that didn't mention it, got %q", entry.Chart)
	}
	if entry.Namespace != "cert-manager" {
		t.Errorf("namespace must survive an edit that didn't mention it, got %q", entry.Namespace)
	}
}

// TestEditCatalogEntry_SettingsMergeFieldByField: sending one settings
// field must not clear the others already on the entry.
func TestEditCatalogEntry_SettingsMergeFieldByField(t *testing.T) {
	git := newMockGitProvider()
	o := newV4TestOrchestratorForCatalog(t, git)
	selfHeal := true
	seedCatalogForEdit(t, git, config.AddonCatalogSpec{
		Addons: map[string]config.AddonCatalogEntry{
			"cert-manager": {
				RepoURL: "https://charts.jetstack.io", Chart: "cert-manager", Version: "1.14.5",
				Settings: &config.AddonSettings{Namespace: "cert-manager-ns", SelfHeal: &selfHeal},
			},
		},
	})

	_, err := o.EditCatalogEntry(context.Background(), EditCatalogEntryRequest{
		Name:     "cert-manager",
		Settings: &config.AddonSettings{SyncOptions: []string{"CreateNamespace=true"}},
	})
	if err != nil {
		t.Fatalf("EditCatalogEntry: %v", err)
	}

	spec := readWrittenCatalog(t, git)
	entry := spec.Addons["cert-manager"]
	if entry.Settings == nil {
		t.Fatalf("settings block lost entirely")
	}
	if entry.Settings.Namespace != "cert-manager-ns" {
		t.Errorf("existing settings.namespace must survive an edit that didn't mention it, got %q", entry.Settings.Namespace)
	}
	if entry.Settings.SelfHeal == nil || !*entry.Settings.SelfHeal {
		t.Errorf("existing settings.selfHeal must survive an edit that didn't mention it, got %+v", entry.Settings.SelfHeal)
	}
	if len(entry.Settings.SyncOptions) != 1 || entry.Settings.SyncOptions[0] != "CreateNamespace=true" {
		t.Errorf("new settings.syncOptions not applied, got %+v", entry.Settings.SyncOptions)
	}
}

// TestEditCatalogEntry_PreservesCommentsAndOrder_ViaSplice proves the edit
// uses the same surgical splice as AddToCatalog: a hand-written comment
// next to an unrelated entry, and the entries' relative order, must both
// survive an edit to a different field.
func TestEditCatalogEntry_PreservesCommentsAndOrder_ViaSplice(t *testing.T) {
	git := newMockGitProvider()
	o := newV4TestOrchestratorForCatalog(t, git)

	// Build a real catalog.yaml via AddToCatalog (guarantees valid
	// formatting), then splice in a hand-written comment the way a human
	// would — this is exactly the scenario the surgical writer exists to
	// protect (design doc §3: hand-editing is a supported door).
	if _, err := o.AddToCatalog(context.Background(), AddToCatalogRequest{
		Addons: []CatalogAddonInput{
			{Name: "cert-manager", RepoURL: "https://charts.jetstack.io", Chart: "cert-manager", Version: "1.14.5"},
			{Name: "vault", RepoURL: "https://helm.releases.hashicorp.com", Chart: "vault", Version: "0.27.0"},
		},
	}); err != nil {
		t.Fatalf("AddToCatalog: %v", err)
	}
	original := string(git.files[config.AddonCatalogPath])
	// Match the file's own 4-space entry indent (yaml.v3's default) rather
	// than hard-coding one, so this test does not silently start producing
	// invalid YAML if the encoder's default indent ever changes.
	lines := strings.Split(original, "\n")
	var pad string
	for _, l := range lines {
		if strings.TrimSpace(l) == "cert-manager:" {
			pad = l[:len(l)-len("cert-manager:")]
			break
		}
	}
	if pad == "" {
		t.Fatalf("could not find the cert-manager entry line to inject a comment above, got:\n%s", original)
	}
	withComment := strings.Replace(original, pad+"cert-manager:", pad+"# hand-written note about cert-manager\n"+pad+"cert-manager:", 1)
	git.files[config.AddonCatalogPath] = []byte(withComment)

	result, err := o.EditCatalogEntry(context.Background(), EditCatalogEntryRequest{
		Name:    "vault",
		Version: strPtr("0.28.0"),
	})
	if err != nil {
		t.Fatalf("EditCatalogEntry: %v", err)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("expected a surgical (non-reformatted) edit, got warnings %+v", result.Warnings)
	}

	body := string(git.files[config.AddonCatalogPath])
	if !strings.Contains(body, "# hand-written note about cert-manager") {
		t.Errorf("hand-written comment on an untouched entry was lost, got:\n%s", body)
	}
	if strings.Index(body, "cert-manager:") > strings.Index(body, "vault:") {
		t.Errorf("entry order changed by the edit, got:\n%s", body)
	}
	spec := readWrittenCatalog(t, git)
	if spec.Addons["vault"].Version != "0.28.0" {
		t.Errorf("vault version not updated, got %+v", spec.Addons["vault"])
	}
	if spec.Addons["cert-manager"].Version != "1.14.5" {
		t.Errorf("cert-manager must be untouched, got %+v", spec.Addons["cert-manager"])
	}
}

// TestEditCatalogEntry_ReadBackFallback_ReformatsWhenSpliceCannotVerify:
// when the on-disk document is a shape the splice cannot edit safely (here,
// a catalog.yaml with an anchor/alias — something spliceCatalogEntries
// explicitly refuses), the edit must still succeed via the whole-file
// re-render fallback, and the caller must be told about it.
func TestEditCatalogEntry_ReadBackFallback_ReformatsWhenSpliceCannotVerify(t *testing.T) {
	git := newMockGitProvider()
	o := newV4TestOrchestratorForCatalog(t, git)
	// A flow-style addons mapping: spliceCatalogEntries requires a BLOCK
	// mapping (addonsVal.Style != 0 is refused), so this forces the
	// fallback path deterministically instead of relying on a coincidental
	// shape.
	git.files[config.AddonCatalogPath] = []byte(
		"apiVersion: sharko.dev/v1\nkind: AddonCatalog\naddons: {cert-manager: {repoURL: https://charts.jetstack.io, chart: cert-manager, version: 1.14.5}}\n",
	)

	result, err := o.EditCatalogEntry(context.Background(), EditCatalogEntryRequest{
		Name:    "cert-manager",
		Version: strPtr("1.15.0"),
	})
	if err != nil {
		t.Fatalf("EditCatalogEntry: %v", err)
	}
	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "rewrote the whole file") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected the reformat warning when the splice cannot verify, got %+v", result.Warnings)
	}
	spec := readWrittenCatalog(t, git)
	if spec.Addons["cert-manager"].Version != "1.15.0" {
		t.Errorf("the fallback re-render must still apply the edit, got %+v", spec.Addons["cert-manager"])
	}
}

// TestEditCatalogEntry_DryRun_NoWrite: dry_run computes the same body and
// diff as the real write but leaves the repo untouched.
func TestEditCatalogEntry_DryRun_NoWrite(t *testing.T) {
	git := newMockGitProvider()
	o := newV4TestOrchestratorForCatalog(t, git)
	seedCatalogForEdit(t, git, config.AddonCatalogSpec{
		Addons: map[string]config.AddonCatalogEntry{
			"cert-manager": {RepoURL: "https://charts.jetstack.io", Chart: "cert-manager", Version: "1.14.5"},
		},
	})
	before := append([]byte(nil), git.files[config.AddonCatalogPath]...)

	result, err := o.EditCatalogEntry(context.Background(), EditCatalogEntryRequest{
		Name:    "cert-manager",
		Version: strPtr("1.15.0"),
		DryRun:  true,
	})
	if err != nil {
		t.Fatalf("EditCatalogEntry dry-run: %v", err)
	}
	if result.DryRun == nil {
		t.Fatalf("expected a DryRun preview, got %+v", result)
	}
	if len(result.DryRun.FilesToWrite) != 1 || result.DryRun.FilesToWrite[0].Path != config.AddonCatalogPath {
		t.Fatalf("expected exactly one file preview for catalog.yaml, got %+v", result.DryRun.FilesToWrite)
	}
	if !strings.Contains(result.DryRun.FilesToWrite[0].Diff, "1.15.0") {
		t.Errorf("expected the diff to show the new version, got:\n%s", result.DryRun.FilesToWrite[0].Diff)
	}
	if string(git.files[config.AddonCatalogPath]) != string(before) {
		t.Error("dry_run must not write anything")
	}
	if len(git.branches) != 0 {
		t.Error("dry_run must not create a branch")
	}
}

// TestEditCatalogEntry_NotFound is the PATCH-specific 404: it edits, it
// never creates.
func TestEditCatalogEntry_NotFound(t *testing.T) {
	git := newMockGitProvider()
	o := newV4TestOrchestratorForCatalog(t, git)
	seedCatalogForEdit(t, git, config.AddonCatalogSpec{Addons: map[string]config.AddonCatalogEntry{}})

	_, err := o.EditCatalogEntry(context.Background(), EditCatalogEntryRequest{
		Name:    "does-not-exist",
		Version: strPtr("1.0.0"),
	})
	if !errors.Is(err, ErrV4AddonNotInCatalog) {
		t.Fatalf("expected ErrV4AddonNotInCatalog, got %v", err)
	}
}

// TestEditCatalogEntry_EmptyBody: nothing to change is a caller mistake,
// not a silent no-op PR.
func TestEditCatalogEntry_EmptyBody(t *testing.T) {
	git := newMockGitProvider()
	o := newV4TestOrchestratorForCatalog(t, git)
	seedCatalogForEdit(t, git, config.AddonCatalogSpec{
		Addons: map[string]config.AddonCatalogEntry{
			"cert-manager": {RepoURL: "https://charts.jetstack.io", Chart: "cert-manager", Version: "1.14.5"},
		},
	})

	_, err := o.EditCatalogEntry(context.Background(), EditCatalogEntryRequest{Name: "cert-manager"})
	if !errors.Is(err, ErrCatalogRequestInvalid) {
		t.Fatalf("expected ErrCatalogRequestInvalid, got %v", err)
	}
}

// TestEditCatalogEntry_EmptyCatalogFile: a present-but-blank catalog.yaml
// is the caller's repo to fix, not a 404 or a 502.
func TestEditCatalogEntry_EmptyCatalogFile(t *testing.T) {
	git := newMockGitProvider()
	o := newV4TestOrchestratorForCatalog(t, git)
	git.files[config.AddonCatalogPath] = []byte("")

	_, err := o.EditCatalogEntry(context.Background(), EditCatalogEntryRequest{
		Name:    "cert-manager",
		Version: strPtr("1.0.0"),
	})
	if !errors.Is(err, ErrCatalogFileEmpty) {
		t.Fatalf("expected ErrCatalogFileEmpty, got %v", err)
	}
}

// TestEditCatalogEntry_RefusesHalfConvertedRepo mirrors
// TestAddToCatalog_RefusesHalfConvertedRepo: a repo carrying both layouts
// must not be edited by either door.
func TestEditCatalogEntry_RefusesHalfConvertedRepo(t *testing.T) {
	git := newMockGitProvider()
	git.files[EnginePinPath] = []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n")
	git.files[V3SecondaryMarkerPath] = []byte("clusters:\n  - name: prod-eu\n")
	o := newTestOrchestratorForCatalog(t, git)
	seedCatalogForEdit(t, git, config.AddonCatalogSpec{
		Addons: map[string]config.AddonCatalogEntry{
			"cert-manager": {RepoURL: "https://charts.jetstack.io", Chart: "cert-manager", Version: "1.14.5"},
		},
	})

	_, err := o.EditCatalogEntry(context.Background(), EditCatalogEntryRequest{
		Name:    "cert-manager",
		Version: strPtr("1.15.0"),
	})
	if !errors.Is(err, ErrMixedRepoLayout) {
		t.Fatalf("expected ErrMixedRepoLayout, got %v", err)
	}
}

// TestEditCatalogEntry_RefusesV3Repo is the mirror image: a repo that has
// not adopted the v4 layout at all (no engine pin) has no catalog.yaml for
// this door to edit.
func TestEditCatalogEntry_RefusesV3Repo(t *testing.T) {
	git := newMockGitProvider() // no EnginePinPath seeded — plain v3 repo
	o := newTestOrchestratorForCatalog(t, git)

	_, err := o.EditCatalogEntry(context.Background(), EditCatalogEntryRequest{
		Name:    "cert-manager",
		Version: strPtr("1.15.0"),
	})
	if !IsV3RepoUnsupported(err) {
		t.Fatalf("expected ErrV3RepoUnsupported, got %v", err)
	}
}
