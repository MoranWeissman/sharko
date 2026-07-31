package config

import (
	"strings"
	"testing"
)

// TestLoadAddonCatalog_ValidEnvelope_Accept mirrors the design
// doc's worked example (docs/design/2026-07-30-v4-data-file-format.md
// §2.3): two version-only overrides of shipped addons plus one
// in-house chart with all three required-after-merge fields.
func TestLoadAddonCatalog_ValidEnvelope_Accept(t *testing.T) {
	t.Parallel()

	body := []byte(`apiVersion: sharko.dev/v1
kind: AddonCatalog
addons:
  cert-manager:
    version: "1.14.5"
  metrics-server:
    version: "3.12.1"
  billing-api:
    repoURL: oci://registry.example.com/charts
    chart: billing-api
    version: "2.4.0"
    namespace: billing
`)

	spec, err := LoadAddonCatalog(body)
	if err != nil {
		t.Fatalf("LoadAddonCatalog: %v", err)
	}
	if len(spec.Addons) != 3 {
		t.Fatalf("len(spec.Addons) = %d, want 3", len(spec.Addons))
	}
	cm, ok := spec.Addons["cert-manager"]
	if !ok || cm.Version != "1.14.5" {
		t.Errorf("cert-manager entry = %+v, want version 1.14.5", cm)
	}
	billing, ok := spec.Addons["billing-api"]
	if !ok {
		t.Fatal("missing billing-api entry")
	}
	if billing.RepoURL != "oci://registry.example.com/charts" || billing.Chart != "billing-api" || billing.Namespace != "billing" {
		t.Errorf("billing-api entry = %+v, want the in-house chart fields from the design doc example", billing)
	}
}

// TestLoadAddonCatalog_PreserveResourcesOnDeletion_Allowed — unlike
// a clusters/*.yaml ClusterAddons file, catalog/addons.yaml's
// AddonSettings DOES permit preserveResourcesOnDeletion (design doc
// §3.2: it's per-ApplicationSet, addon-wide, and this is the only place
// it can be set).
func TestLoadAddonCatalog_PreserveResourcesOnDeletion_Allowed(t *testing.T) {
	t.Parallel()

	body := []byte(`apiVersion: sharko.dev/v1
kind: AddonCatalog
addons:
  cert-manager:
    version: "1.14.5"
    settings:
      preserveResourcesOnDeletion: false
`)
	spec, err := LoadAddonCatalog(body)
	if err != nil {
		t.Fatalf("LoadAddonCatalog: unexpected error for fleet-wide preserveResourcesOnDeletion: %v", err)
	}
	cm := spec.Addons["cert-manager"]
	if cm.Settings == nil || cm.Settings.PreserveResourcesOnDeletion == nil || *cm.Settings.PreserveResourcesOnDeletion != false {
		t.Errorf("cert-manager.Settings.PreserveResourcesOnDeletion = %+v, want pointer to false", cm.Settings)
	}
}

// TestLoadAddonCatalog_MissingRequiredAddons_Reject — addons: is
// required (design doc §2.3), even though it may be an empty map.
// Omitting the key entirely (not the same as addons: {}) must fail
// schema validation.
func TestLoadAddonCatalog_MissingRequiredAddons_Reject(t *testing.T) {
	t.Parallel()

	body := []byte(`apiVersion: sharko.dev/v1
kind: AddonCatalog
`)
	_, err := LoadAddonCatalog(body)
	if err == nil {
		t.Fatal("LoadAddonCatalog: want error for missing addons, got nil")
	}
	if !strings.Contains(err.Error(), "addons") {
		t.Errorf("error %q: want substring \"addons\"", err.Error())
	}
}

// TestLoadAddonCatalog_EmptyAddonsMap_Accept — an explicit empty
// map is valid (design doc decision D16: "missing means empty", and an
// empty map is the written-out form of that for a file that DOES exist
// on disk).
func TestLoadAddonCatalog_EmptyAddonsMap_Accept(t *testing.T) {
	t.Parallel()

	body := []byte(`apiVersion: sharko.dev/v1
kind: AddonCatalog
addons: {}
`)
	spec, err := LoadAddonCatalog(body)
	if err != nil {
		t.Fatalf("LoadAddonCatalog: unexpected error for empty addons map: %v", err)
	}
	if len(spec.Addons) != 0 {
		t.Errorf("spec.Addons = %+v, want empty", spec.Addons)
	}
}

// TestLoadAddonCatalog_WrongKind_Reject — a v3 addons-catalog.yaml body
// (wrapped, applicationsets:) must be rejected by the v4 catalog.yaml
// loader (design doc decision D5). The v3 and v4 catalog formats share
// the literal kind string "AddonCatalog" (see schema.KindAddonCatalog's
// doc comment) — they are told apart by SHAPE, not by name — so a v3
// body handed to LoadAddonCatalog is rejected by schema validation
// (missing addons:, additionalProperties rejects metadata:/spec:) rather
// than by a kind-mismatch error.
func TestLoadAddonCatalog_WrongKind_Reject(t *testing.T) {
	t.Parallel()

	body := []byte(`apiVersion: sharko.dev/v1
kind: AddonCatalog
metadata:
  name: addon-catalog
spec:
  applicationsets: []
`)
	_, err := LoadAddonCatalog(body)
	if err == nil {
		t.Fatal("LoadAddonCatalog wrong shape (v3 body): want error, got nil")
	}
	if !strings.Contains(err.Error(), "addons") {
		t.Errorf("error %q: want substring %q", err.Error(), "addons")
	}
}

// TestLoadAddonCatalog_NotEnveloped_Reject — AddonCatalog is a
// v4-only kind with no legacy bare-YAML precedent.
func TestLoadAddonCatalog_NotEnveloped_Reject(t *testing.T) {
	t.Parallel()

	body := []byte(`addons:
  cert-manager:
    version: "1.14.5"
`)
	_, err := LoadAddonCatalog(body)
	if err == nil {
		t.Fatal("LoadAddonCatalog: want error for non-enveloped body, got nil")
	}
	if !strings.Contains(err.Error(), "not a Sharko document") {
		t.Errorf("error %q: want substring \"not a Sharko document\"", err.Error())
	}
}

// TestSaveAddonCatalog_RoundTrip pins the writer contract: line 1
// is the schema header, the addons: map sits at the top level (no
// metadata.name any more — the file's identity is its path on disk,
// design doc §9), and Load(Save(spec)) reproduces the same spec —
// including AdditionalSources reusing the existing models.AddonSource
// type (design doc: "Carried over from v3 unchanged").
func TestSaveAddonCatalog_RoundTrip(t *testing.T) {
	t.Parallel()

	preserve := true
	spec := AddonCatalogSpec{
		Addons: map[string]AddonCatalogEntry{
			"cert-manager": {
				Version: "1.14.5",
				Settings: &AddonSettings{
					PreserveResourcesOnDeletion: &preserve,
				},
			},
		},
	}

	body, err := SaveAddonCatalog(spec)
	if err != nil {
		t.Fatalf("SaveAddonCatalog: %v", err)
	}

	lines := strings.SplitN(string(body), "\n", 2)
	if lines[0] != AddonCatalogSchemaHeaderV4 {
		t.Errorf("line 1 = %q, want schema header %q", lines[0], AddonCatalogSchemaHeaderV4)
	}
	if !strings.Contains(string(body), "cert-manager:") {
		t.Errorf("output missing addons.cert-manager:\n%s", body)
	}

	roundTripped, err := LoadAddonCatalog(body)
	if err != nil {
		t.Fatalf("LoadAddonCatalog(SaveAddonCatalog(spec)): %v", err)
	}
	cm := roundTripped.Addons["cert-manager"]
	if cm.Version != "1.14.5" || cm.Settings == nil || cm.Settings.PreserveResourcesOnDeletion == nil || !*cm.Settings.PreserveResourcesOnDeletion {
		t.Errorf("round-trip cert-manager entry = %+v, want version 1.14.5 + preserveResourcesOnDeletion=true", cm)
	}
}
