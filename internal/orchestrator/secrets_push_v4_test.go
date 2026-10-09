package orchestrator

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// These tests cover the v4 wave 2.5 review's F1 and F2 on the orchestrator
// side: a migrated repo keeps the machine-readable push definitions its v3
// catalog carried, and an addon that has one can actually be switched on.
//
// The half that lives in internal/secrets (the reconciler reading those
// definitions and pushing them) is tested there.

// TestMigration_PushDefinitionSurvivesTheFileRoundTrip is the one that
// matters most: the definition has to survive being WRITTEN to catalog.yaml
// and read back, through the real writer, the real reader and the JSON
// Schema both of them enforce. Converting it correctly in memory is worth
// nothing if the file format then drops it.
func TestMigration_PushDefinitionSurvivesTheFileRoundTrip(t *testing.T) {
	v3Ref := models.AddonSecretRef{
		SecretName: "datadog-keys",
		Namespace:  "monitoring",
		Keys: map[string]string{
			"api-key": "secrets/datadog/api-key",
			"app-key": "secrets/datadog/app-key",
		},
	}

	spec, _ := buildCatalogFromV3([]models.AddonCatalogEntry{{
		Name:    "datadog",
		RepoURL: "https://helm.datadoghq.com",
		Chart:   "datadog",
		Version: "3.50.0",
		Secrets: []models.AddonSecretRef{v3Ref},
	}})

	body, err := config.SaveAddonCatalog(spec)
	if err != nil {
		t.Fatalf("writing the converted catalog: %v", err)
	}
	if !strings.Contains(string(body), "push:") {
		t.Fatalf("the written catalog has no push block — the definition only exists in git history:\n%s", body)
	}

	reloaded, err := config.LoadAddonCatalog(body)
	if err != nil {
		t.Fatalf("reading the converted catalog back: %v", err)
	}
	entry, ok := reloaded.Addons["datadog"]
	if !ok {
		t.Fatalf("datadog is missing from the reloaded catalog: %+v", reloaded.Addons)
	}
	if len(entry.Secrets) != 1 {
		t.Fatalf("entry.Secrets = %+v, want exactly 1", entry.Secrets)
	}

	push, complete := entry.Secrets[0].PushDefinition()
	if !complete {
		t.Fatalf("the push definition came back incomplete: %+v", entry.Secrets[0].Push)
	}
	if !reflect.DeepEqual(push, v3Ref) {
		t.Errorf("push definition changed across the migration:\n got %+v\nwant %+v", push, v3Ref)
	}
}

// TestMigration_PushDefinitionIsACopy: the converted entry must not share
// its Keys map with the parsed v3 document, or a later edit to one shows up
// in the other.
func TestMigration_PushDefinitionIsACopy(t *testing.T) {
	v3 := models.AddonCatalogEntry{
		Name:    "datadog",
		RepoURL: "https://helm.datadoghq.com",
		Chart:   "datadog",
		Version: "3.50.0",
		Secrets: []models.AddonSecretRef{{
			SecretName: "datadog-keys",
			Namespace:  "monitoring",
			Keys:       map[string]string{"api-key": "secrets/datadog/api-key"},
		}},
	}

	spec, _ := buildCatalogFromV3([]models.AddonCatalogEntry{v3})
	converted := spec.Addons["datadog"].Secrets[0].Push
	converted.Keys["api-key"] = "somewhere/else"

	if got := v3.Secrets[0].Keys["api-key"]; got != "secrets/datadog/api-key" {
		t.Errorf("editing the converted copy reached back into the v3 entry: %q", got)
	}
}

// TestMigration_NoteSaysTheSecretsKeepBeingPushed: the pull-request note is
// read by somebody deciding whether to merge. It has to describe what the
// conversion actually did.
func TestMigration_NoteSaysTheSecretsKeepBeingPushed(t *testing.T) {
	_, notes := buildCatalogFromV3([]models.AddonCatalogEntry{{
		Name:    "datadog",
		RepoURL: "https://helm.datadoghq.com",
		Chart:   "datadog",
		Version: "3.50.0",
		Secrets: []models.AddonSecretRef{{
			SecretName: "datadog-keys",
			Namespace:  "monitoring",
			Keys:       map[string]string{"api-key": "secrets/datadog/api-key"},
		}},
	}})

	if len(notes) == 0 {
		t.Fatal("expected a note about the secrets")
	}
	joined := strings.Join(notes, " ")
	if !strings.Contains(joined, "pushing") {
		t.Errorf("the note does not say the secrets keep being pushed: %q", joined)
	}
}

// ---- the enable gate (F2) ----

func pushRequirement(name string) catalog.SecretRequirement {
	return catalog.SecretRequirement{
		Name:        name,
		Description: "the API key Sharko puts on the cluster",
		Push: &models.AddonSecretRef{
			SecretName: "datadog-keys",
			Namespace:  "monitoring",
			Keys:       map[string]string{"api-key": "secrets/datadog/api-key"},
		},
	}
}

// TestValidateV4AddonInputs_EntryPushSatisfiesTheGate: with the definition
// in the catalog entry there is nothing left for Sharko to be told, so the
// enable must not be blocked — and no server-side definition is registered
// here on purpose. This is exactly the migrated-addon case that used to
// have no working way through (review F2).
func TestValidateV4AddonInputs_EntryPushSatisfiesTheGate(t *testing.T) {
	o := &Orchestrator{}
	addon := catalog.CatalogAddon{
		Name:    "datadog",
		Secrets: []catalog.SecretRequirement{pushRequirement("datadog-keys")},
	}

	problems, warnings := o.validateV4AddonInputs(addon, nil)
	if len(problems) != 0 {
		t.Errorf("an entry that carries its own push definition must not be blocked: %v", problems)
	}
	if len(warnings) != 0 {
		t.Errorf("nor warned about — the secret gets pushed: %v", warnings)
	}
}

// TestValidateV4AddonInputs_ProseOnlyStillBlocks: a requirement with no
// push block and no server-side definition keeps blocking, in the same
// plain words as before.
func TestValidateV4AddonInputs_ProseOnlyStillBlocks(t *testing.T) {
	o := &Orchestrator{}
	addon := catalog.CatalogAddon{
		Name: "datadog",
		Secrets: []catalog.SecretRequirement{{
			Name:        "datadog-keys",
			Description: "an API key from your Datadog account",
		}},
	}

	problems, _ := o.validateV4AddonInputs(addon, nil)
	if len(problems) != 1 {
		t.Fatalf("problems = %v, want exactly one", problems)
	}
	if !strings.Contains(problems[0], "no secret definition is configured") {
		t.Errorf("the wording changed: %q", problems[0])
	}
}

// TestValidateV4AddonInputs_HalfWrittenPushSaysWhatIsMissing: a push block
// somebody started and did not finish gets its own message naming the
// missing key — the fix is different from "there is no definition at all",
// so the sentence has to be different too.
func TestValidateV4AddonInputs_HalfWrittenPushSaysWhatIsMissing(t *testing.T) {
	o := &Orchestrator{}
	addon := catalog.CatalogAddon{
		Name: "datadog",
		Secrets: []catalog.SecretRequirement{{
			Name:        "datadog-keys",
			Description: "the API key Sharko puts on the cluster",
			Push:        &models.AddonSecretRef{SecretName: "datadog-keys"},
		}},
	}

	problems, _ := o.validateV4AddonInputs(addon, nil)
	if len(problems) != 1 {
		t.Fatalf("problems = %v, want exactly one", problems)
	}
	for _, want := range []string{"namespace", "keys", config.AddonCatalogPath} {
		if !strings.Contains(problems[0], want) {
			t.Errorf("problem %q should mention %q", problems[0], want)
		}
	}
}

// TestV4PushSecretCount_OnlyCountsWhatSharkoPushes: a plain-English
// requirement is somebody else's job, so it must not drag the credentials
// gate in.
func TestV4PushSecretCount_OnlyCountsWhatSharkoPushes(t *testing.T) {
	addon := catalog.CatalogAddon{
		Name: "datadog",
		Secrets: []catalog.SecretRequirement{
			pushRequirement("datadog-keys"),
			{Name: "a note", Description: "you set this one up yourself"},
			{Name: "half-written", Push: &models.AddonSecretRef{Namespace: "monitoring"}},
		},
	}
	if got := v4PushSecretCount(addon); got != 1 {
		t.Errorf("v4PushSecretCount = %d, want 1", got)
	}
}

// v4CatalogWithPush renders a catalog.yaml approving one addon whose entry
// carries a complete push definition — a migrated repo, in other words.
func v4CatalogWithPush(t *testing.T) []byte {
	t.Helper()
	body, err := config.SaveAddonCatalog(config.AddonCatalogSpec{
		Addons: map[string]config.AddonCatalogEntry{
			"datadog": {
				RepoURL: "https://helm.datadoghq.com", Chart: "datadog",
				Version: "3.50.0", Namespace: "monitoring",
				Secrets: []config.AddonSecretRequirement{{
					Name:        "datadog-keys",
					Description: "Sharko creates this Secret on every cluster running datadog.",
					Push: &models.AddonSecretRef{
						SecretName: "datadog-keys",
						Namespace:  "monitoring",
						Keys:       map[string]string{"api-key": "secrets/datadog/api-key"},
					},
				}},
			},
		},
	})
	if err != nil {
		t.Fatalf("SaveAddonCatalog: %v", err)
	}
	return body
}

// TestEnableAddonV4_RefusesWhenSharkoCannotReachTheCluster: Sharko has to
// push this addon's Secret itself, so enabling it on a cluster it has no
// credentials for would deploy a workload whose credential never arrives.
// Same refusal the v3 path has always made, and it comes back as a
// plain-English problem (which the API renders as a 422) rather than an
// "upstream failed".
func TestEnableAddonV4_RefusesWhenSharkoCannotReachTheCluster(t *testing.T) {
	git := newMockGitProvider()
	git.files[config.AddonCatalogPath] = v4CatalogWithPush(t)
	orch := newV4TestOrchestrator(t, git)
	// No credentials provider at all — Sharko cannot reach prod-eu.

	_, err := orch.EnableAddonV4(context.Background(), EnableAddonV4Request{
		Cluster: "prod-eu",
		Addon:   "datadog",
		Yes:     true,
	})
	if err == nil {
		t.Fatal("expected the enable to be refused")
	}
	var verr *V4SemanticValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected *V4SemanticValidationError (the API turns that into a 422), got %T: %v", err, err)
	}
	if len(verr.Problems) != 1 || !strings.Contains(verr.Problems[0], "no credentials for cluster") {
		t.Errorf("problems = %v, want one naming the missing credentials", verr.Problems)
	}
	if len(git.branches) != 0 || len(git.prs) != 0 {
		t.Errorf("nothing may be written when the enable is refused: branches=%v prs=%d",
			git.branches, len(git.prs))
	}
}

// TestEnableAddonV4_ProceedsWhenSharkoCanReachTheCluster is the other half:
// with credentials in hand the same enable goes through and opens its pull
// request.
func TestEnableAddonV4_ProceedsWhenSharkoCanReachTheCluster(t *testing.T) {
	git := newMockGitProvider()
	git.files[config.AddonCatalogPath] = v4CatalogWithPush(t)
	orch := newV4TestOrchestrator(t, git)
	creds := &mockCredProvider{creds: map[string]*providers.Kubeconfig{
		"prod-eu": {Raw: []byte("fake-kubeconfig")},
	}}
	orch.credProvider = creds
	orch.SetCredsRouter(providers.NewClusterCredsRouter(creds, providers.ClusterTestProviderConfig{}))

	result, err := orch.EnableAddonV4(context.Background(), EnableAddonV4Request{
		Cluster: "prod-eu",
		Addon:   "datadog",
		Yes:     true,
	})
	if err != nil {
		t.Fatalf("enable should go through: %v", err)
	}
	if result == nil || result.PRID == 0 {
		t.Errorf("expected a pull request, got %+v", result)
	}
}

// TestLockstep_V4ClusterAddonsPath pins internal/config's copy of the
// per-cluster assignment path. internal/secrets uses it to find out which
// addons a cluster runs on a v4 repo; if its copy fell behind, the secrets
// reconciler would find no assignment file for any cluster, conclude every
// cluster runs nothing, and quietly stop pushing every addon secret in the
// fleet — with no error anywhere, which is the failure this whole fix
// exists to end.
func TestLockstep_V4ClusterAddonsPath(t *testing.T) {
	t.Parallel()
	if config.V4ClustersDir != V4ClustersDir {
		t.Errorf("config.V4ClustersDir = %q, want %q (orchestrator.V4ClustersDir)",
			config.V4ClustersDir, V4ClustersDir)
	}
	for _, name := range []string{"prod-eu", "staging-us", "a", "cluster-1"} {
		want, wantErr := v4ClusterAddonsPath(name)
		got, gotErr := config.V4ClusterAddonsPath(name)
		if wantErr != nil || gotErr != nil {
			t.Fatalf("both builders must accept %q: orchestrator=%v config=%v", name, wantErr, gotErr)
		}
		if got != want {
			t.Errorf("config.V4ClusterAddonsPath(%q) = %q, want %q — the reader and the writer must agree on the file",
				name, got, want)
		}
	}
	// And both must refuse the same dangerous names.
	for _, name := range []string{"", "../../engine", "a/b", "Bad Name"} {
		_, wantErr := v4ClusterAddonsPath(name)
		_, gotErr := config.V4ClusterAddonsPath(name)
		if (wantErr == nil) != (gotErr == nil) {
			t.Errorf("disagreement on %q: orchestrator err=%v, config err=%v", name, wantErr, gotErr)
		}
	}
}
