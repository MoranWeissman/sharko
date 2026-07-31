package catalog

import (
	"errors"
	"testing"

	"github.com/MoranWeissman/sharko/internal/config"
)

const viewTestCuratedYAML = `
addons:
  - name: cert-manager
    description: X.509 certificate management for Kubernetes.
    chart: cert-manager
    repo: https://charts.jetstack.io
    default_namespace: cert-manager
    license: Apache-2.0
    category: security
    maintainers: ["jetstack"]
    curated_by: ["cncf-graduated"]
    docs_url: https://cert-manager.io/docs/
    required_values:
      - key: installCRDs
        description: Whether to install cert-manager's CRDs.
    secrets:
      - name: acme-dns-token
        description: DNS provider API token for ACME DNS-01 challenges.
    quirks:
      - CRDs are large — needs ServerSideApply.
  - name: grafana
    description: Dashboards for your metrics.
    chart: grafana
    repo: https://grafana.github.io/helm-charts
    default_namespace: monitoring
    license: AGPL-3.0
    category: observability
    maintainers: ["grafana"]
    curated_by: ["cncf-incubating"]
`

func viewTestCurated(t *testing.T) *Catalog {
	t.Helper()
	cat, err := LoadBytes([]byte(viewTestCuratedYAML))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	return cat
}

// TestBuildCatalogView_EmptyCatalogApprovesNothing is the bug that started
// the rebuild, as a test. A repo with no approved addons must produce an
// EMPTY view — not one entry per addon Sharko happens to ship. If this ever
// goes green with a non-zero count, the curated auto-seeding is back.
func TestBuildCatalogView_EmptyCatalogApprovesNothing(t *testing.T) {
	got := BuildCatalogView(viewTestCurated(t), config.AddonCatalogSpec{})
	if len(got) != 0 {
		t.Fatalf("an empty catalog must approve nothing, got %d entries: %+v", len(got), got)
	}
}

// TestBuildCatalogView_OnlyWhatTheFileSays: an addon the Marketplace
// carries but the org did not approve must not appear.
func TestBuildCatalogView_OnlyWhatTheFileSays(t *testing.T) {
	got := BuildCatalogView(viewTestCurated(t), config.AddonCatalogSpec{
		Addons: map[string]config.AddonCatalogEntry{
			"cert-manager": {RepoURL: "https://charts.jetstack.io", Chart: "cert-manager", Version: "1.14.5"},
		},
	})
	if len(got) != 1 {
		t.Fatalf("expected exactly the one approved addon, got %+v", got)
	}
	if _, seeded := got["grafana"]; seeded {
		t.Error("grafana is curated but not approved — it must not appear")
	}
}

// TestBuildCatalogView_DeploymentFieldsComeFromTheFileOnly: the curated
// entry has a repo and a chart for cert-manager, and neither of them may
// leak into a file that says something different.
func TestBuildCatalogView_DeploymentFieldsComeFromTheFileOnly(t *testing.T) {
	got := BuildCatalogView(viewTestCurated(t), config.AddonCatalogSpec{
		Addons: map[string]config.AddonCatalogEntry{
			"cert-manager": {
				RepoURL:   "oci://mirror.example.com/charts",
				Chart:     "cert-manager-fork",
				Version:   "1.14.5",
				Namespace: "certs",
			},
		},
	})
	cm := got["cert-manager"]
	if cm.RepoURL != "oci://mirror.example.com/charts" || cm.Chart != "cert-manager-fork" || cm.Namespace != "certs" {
		t.Errorf("the file must win on every deployment field: %+v", cm)
	}
	if cm.Origin != OriginCurated {
		t.Errorf("Origin = %q, want %q — the Marketplace knows this name", cm.Origin, OriginCurated)
	}
	if cm.Description == "" || cm.DocsURL == "" || len(cm.RequiredValues) == 0 || len(cm.Quirks) == 0 {
		t.Errorf("the Marketplace's knowledge fields should be filled in: %+v", cm)
	}
}

// TestBuildCatalogView_InternalAddonHasNoKnowledgeFields.
func TestBuildCatalogView_InternalAddonHasNoKnowledgeFields(t *testing.T) {
	got := BuildCatalogView(viewTestCurated(t), config.AddonCatalogSpec{
		Addons: map[string]config.AddonCatalogEntry{
			"billing-api": {RepoURL: "oci://registry.example.com/charts", Chart: "billing-api", Version: "2.4.0"},
		},
	})
	b := got["billing-api"]
	if b.Origin != OriginInternal {
		t.Errorf("Origin = %q, want %q", b.Origin, OriginInternal)
	}
	if b.Description != "" || b.DocsURL != "" || len(b.RequiredValues) != 0 {
		t.Errorf("an addon nothing else describes has no knowledge fields: %+v", b)
	}
	if !b.Deployable {
		t.Errorf("a complete entry is deployable: %+v", b)
	}
}

// TestBuildCatalogView_NilCuratedIsSafe — a build with no embedded
// Marketplace list still shows the org's own catalog.
func TestBuildCatalogView_NilCuratedIsSafe(t *testing.T) {
	got := BuildCatalogView(nil, config.AddonCatalogSpec{
		Addons: map[string]config.AddonCatalogEntry{
			"cert-manager": {RepoURL: "https://charts.jetstack.io", Chart: "cert-manager", Version: "1.14.5"},
		},
	})
	if len(got) != 1 || got["cert-manager"].Origin != OriginInternal {
		t.Errorf("expected one internal-origin entry, got %+v", got)
	}
}

// TestBuildCatalogView_NullEntryIsFlaggedNotACrash is the relocated
// null-entry guard. A hand-edit can leave a bare `<addon>:` key with no
// value, which YAML reads as null. That must come back as an entry with
// nothing filled in and a plain list of what is missing — never a panic,
// and never a silently deployable entry.
func TestBuildCatalogView_NullEntryIsFlaggedNotACrash(t *testing.T) {
	got := BuildCatalogView(viewTestCurated(t), config.AddonCatalogSpec{
		Addons: map[string]config.AddonCatalogEntry{
			"cert-manager": {}, // the zero value a `cert-manager:` null key parses to
		},
	})
	cm, ok := got["cert-manager"]
	if !ok {
		t.Fatal("the entry must still be listed so somebody can see it is broken")
	}
	if cm.Deployable {
		t.Error("an empty entry is not deployable")
	}
	want := []string{"repoURL", "chart", "version"}
	if len(cm.MissingFields) != len(want) {
		t.Fatalf("MissingFields = %v, want %v", cm.MissingFields, want)
	}
	for i, f := range want {
		if cm.MissingFields[i] != f {
			t.Errorf("MissingFields[%d] = %q, want %q", i, cm.MissingFields[i], f)
		}
	}
}

// TestBuildCatalogView_MutatingTheResultCannotReachTheFile.
func TestBuildCatalogView_MutatingTheResultCannotReachTheFile(t *testing.T) {
	spec := config.AddonCatalogSpec{
		Addons: map[string]config.AddonCatalogEntry{
			"cert-manager": {
				RepoURL: "https://charts.jetstack.io", Chart: "cert-manager", Version: "1.14.5",
				ExtraHelmValues: map[string]string{"a": "1"},
			},
		},
	}
	got := BuildCatalogView(viewTestCurated(t), spec)
	got["cert-manager"].ExtraHelmValues["a"] = "changed"
	if spec.Addons["cert-manager"].ExtraHelmValues["a"] != "1" {
		t.Error("mutating the view reached back into the parsed file")
	}

	cm := got["cert-manager"]
	if len(cm.Quirks) > 0 {
		cm.Quirks[0] = "changed"
		if viewTestCurated(t).Entries()[0].Quirks[0] == "changed" {
			t.Error("mutating the view reached back into the curated list")
		}
	}
}

// TestBuildCatalogView_EntrySecretsWinOverTheCuratedOnes.
func TestBuildCatalogView_EntrySecretsWinOverTheCuratedOnes(t *testing.T) {
	got := BuildCatalogView(viewTestCurated(t), config.AddonCatalogSpec{
		Addons: map[string]config.AddonCatalogEntry{
			"cert-manager": {
				RepoURL: "https://charts.jetstack.io", Chart: "cert-manager", Version: "1.14.5",
				Secrets: []config.AddonSecretRequirement{{
					Name: "our-dns-token", Description: "our own DNS provider token",
					RequiredFor: config.SecretRequiredForRuntime,
				}},
			},
		},
	})
	cm := got["cert-manager"]
	if len(cm.Secrets) != 1 || cm.Secrets[0].Name != "our-dns-token" {
		t.Fatalf("the entry's own secrets list must win: %+v", cm.Secrets)
	}
	if cm.Secrets[0].EffectiveRequiredFor() != SecretRequiredForRuntime {
		t.Errorf("required_for lost in translation: %+v", cm.Secrets[0])
	}
}

// TestBuildCatalogView_CuratedSecretsFillInWhenTheEntryHasNone: an entry
// somebody hand-wrote without a secrets block still gets the heads-up.
func TestBuildCatalogView_CuratedSecretsFillInWhenTheEntryHasNone(t *testing.T) {
	got := BuildCatalogView(viewTestCurated(t), config.AddonCatalogSpec{
		Addons: map[string]config.AddonCatalogEntry{
			"cert-manager": {RepoURL: "https://charts.jetstack.io", Chart: "cert-manager", Version: "1.14.5"},
		},
	})
	cm := got["cert-manager"]
	if len(cm.Secrets) != 1 || cm.Secrets[0].Name != "acme-dns-token" {
		t.Errorf("expected the Marketplace's secrets list to stand in: %+v", cm.Secrets)
	}
}

// TestValidateCatalogSpec reports the first incomplete entry in sorted-name
// order, so the same file always names the same problem.
func TestValidateCatalogSpec(t *testing.T) {
	cases := []struct {
		name      string
		spec      config.AddonCatalogSpec
		wantAddon string
		wantField string
	}{
		{
			"complete", config.AddonCatalogSpec{Addons: map[string]config.AddonCatalogEntry{
				"a": {RepoURL: "oci://x", Chart: "a", Version: "1.0.0"},
			}}, "", "",
		},
		{
			"missing repo", config.AddonCatalogSpec{Addons: map[string]config.AddonCatalogEntry{
				"a": {Chart: "a", Version: "1.0.0"},
			}}, "a", "repoURL",
		},
		{
			"missing chart", config.AddonCatalogSpec{Addons: map[string]config.AddonCatalogEntry{
				"a": {RepoURL: "oci://x", Version: "1.0.0"},
			}}, "a", "chart",
		},
		{
			"missing version", config.AddonCatalogSpec{Addons: map[string]config.AddonCatalogEntry{
				"a": {RepoURL: "oci://x", Chart: "a"},
			}}, "a", "version",
		},
		{
			"reports the first by name", config.AddonCatalogSpec{Addons: map[string]config.AddonCatalogEntry{
				"zeta":  {Chart: "zeta", Version: "1.0.0"},
				"alpha": {Chart: "alpha", Version: "1.0.0"},
			}}, "alpha", "repoURL",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateCatalogSpec(tc.spec)
			if tc.wantAddon == "" {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			var mf *MissingRequiredFieldError
			if !errors.As(err, &mf) {
				t.Fatalf("expected *MissingRequiredFieldError, got %T: %v", err, err)
			}
			if mf.Addon != tc.wantAddon || mf.Field != tc.wantField {
				t.Errorf("got addon=%q field=%q, want addon=%q field=%q", mf.Addon, mf.Field, tc.wantAddon, tc.wantField)
			}
		})
	}
}

// TestCuratedSecretsForEntry round-trips the secrets list the other way —
// what the add-to-catalog operation copies out of the Marketplace.
func TestCuratedSecretsForEntry(t *testing.T) {
	in := []SecretRequirement{{Name: "n", Description: "d", RequiredFor: SecretRequiredForRuntime}}
	got := CuratedSecretsForEntry(in)
	if len(got) != 1 || got[0].Name != "n" || got[0].RequiredFor != config.SecretRequiredForRuntime {
		t.Fatalf("round trip lost something: %+v", got)
	}
	if CuratedSecretsForEntry(nil) != nil {
		t.Error("an empty list converts to nil, not an empty slice")
	}
}
