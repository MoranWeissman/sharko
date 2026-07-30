package config

import (
	"strings"
	"testing"
)

// TestLoadAddonCatalogDelta_ValidEnvelope_Accept mirrors the design
// doc's worked example (docs/design/2026-07-30-v4-data-file-format.md
// §2.3): two version-only overrides of shipped addons plus one
// in-house chart with all three required-after-merge fields.
func TestLoadAddonCatalogDelta_ValidEnvelope_Accept(t *testing.T) {
	t.Parallel()

	body := []byte(`apiVersion: sharko.dev/v1
kind: AddonCatalogDelta
metadata:
  name: addon-catalog-delta
spec:
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

	spec, err := LoadAddonCatalogDelta(body)
	if err != nil {
		t.Fatalf("LoadAddonCatalogDelta: %v", err)
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

// TestLoadAddonCatalogDelta_PreserveResourcesOnDeletion_Allowed — unlike
// a clusters/*.yaml ClusterAssignment file, catalog/addons.yaml's
// AddonSettings DOES permit preserveResourcesOnDeletion (design doc
// §3.2: it's per-ApplicationSet, addon-wide, and this is the only place
// it can be set).
func TestLoadAddonCatalogDelta_PreserveResourcesOnDeletion_Allowed(t *testing.T) {
	t.Parallel()

	body := []byte(`apiVersion: sharko.dev/v1
kind: AddonCatalogDelta
metadata:
  name: addon-catalog-delta
spec:
  addons:
    cert-manager:
      version: "1.14.5"
      settings:
        preserveResourcesOnDeletion: false
`)
	spec, err := LoadAddonCatalogDelta(body)
	if err != nil {
		t.Fatalf("LoadAddonCatalogDelta: unexpected error for fleet-wide preserveResourcesOnDeletion: %v", err)
	}
	cm := spec.Addons["cert-manager"]
	if cm.Settings == nil || cm.Settings.PreserveResourcesOnDeletion == nil || *cm.Settings.PreserveResourcesOnDeletion != false {
		t.Errorf("cert-manager.Settings.PreserveResourcesOnDeletion = %+v, want pointer to false", cm.Settings)
	}
}

// TestLoadAddonCatalogDelta_MissingRequiredAddons_Reject — spec.addons
// is required (design doc §2.3), even though it may be an empty map.
// Omitting the key entirely (not the same as spec: { addons: {} }) must
// fail schema validation.
func TestLoadAddonCatalogDelta_MissingRequiredAddons_Reject(t *testing.T) {
	t.Parallel()

	body := []byte(`apiVersion: sharko.dev/v1
kind: AddonCatalogDelta
metadata:
  name: addon-catalog-delta
spec: {}
`)
	_, err := LoadAddonCatalogDelta(body)
	if err == nil {
		t.Fatal("LoadAddonCatalogDelta: want error for missing spec.addons, got nil")
	}
	if !strings.Contains(err.Error(), "addons") {
		t.Errorf("error %q: want substring \"addons\"", err.Error())
	}
}

// TestLoadAddonCatalogDelta_EmptyAddonsMap_Accept — an explicit empty
// map is valid (design doc decision D16: "missing means empty", and an
// empty map is the written-out form of that for a file that DOES exist
// on disk).
func TestLoadAddonCatalogDelta_EmptyAddonsMap_Accept(t *testing.T) {
	t.Parallel()

	body := []byte(`apiVersion: sharko.dev/v1
kind: AddonCatalogDelta
metadata:
  name: addon-catalog-delta
spec:
  addons: {}
`)
	spec, err := LoadAddonCatalogDelta(body)
	if err != nil {
		t.Fatalf("LoadAddonCatalogDelta: unexpected error for empty addons map: %v", err)
	}
	if len(spec.Addons) != 0 {
		t.Errorf("spec.Addons = %+v, want empty", spec.Addons)
	}
}

// TestLoadAddonCatalogDelta_WrongKind_Reject — an envelope carrying the
// v3 AddonCatalog kind must be rejected by the v4 AddonCatalogDelta
// loader (design doc decision D5: same apiVersion, different kind,
// different shape — never silently cross-parsed).
func TestLoadAddonCatalogDelta_WrongKind_Reject(t *testing.T) {
	t.Parallel()

	body := []byte(`apiVersion: sharko.dev/v1
kind: AddonCatalog
metadata:
  name: addon-catalog
spec:
  applicationsets: []
`)
	_, err := LoadAddonCatalogDelta(body)
	if err == nil {
		t.Fatal("LoadAddonCatalogDelta wrong kind: want error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, `envelope kind "AddonCatalog"`) {
		t.Errorf("error %q: want substring %q", msg, `envelope kind "AddonCatalog"`)
	}
	if !strings.Contains(msg, `expected "AddonCatalogDelta"`) {
		t.Errorf("error %q: want substring %q", msg, `expected "AddonCatalogDelta"`)
	}
}

// TestLoadAddonCatalogDelta_NotEnveloped_Reject — AddonCatalogDelta is a
// v4-only kind with no legacy bare-YAML precedent.
func TestLoadAddonCatalogDelta_NotEnveloped_Reject(t *testing.T) {
	t.Parallel()

	body := []byte(`addons:
  cert-manager:
    version: "1.14.5"
`)
	_, err := LoadAddonCatalogDelta(body)
	if err == nil {
		t.Fatal("LoadAddonCatalogDelta: want error for non-enveloped body, got nil")
	}
	if !strings.Contains(err.Error(), "not a Sharko-enveloped document") {
		t.Errorf("error %q: want substring \"not a Sharko-enveloped document\"", err.Error())
	}
}

// TestSaveAddonCatalogDelta_RoundTrip pins the writer contract: line 1
// is the schema header, metadata.name is the conventional
// "addon-catalog-delta" (design doc §2.3 worked example), and
// Load(Save(spec)) reproduces the same spec — including AdditionalSources
// reusing the existing models.AddonSource type (design doc: "Carried
// over from v3 unchanged").
func TestSaveAddonCatalogDelta_RoundTrip(t *testing.T) {
	t.Parallel()

	preserve := true
	spec := AddonCatalogDeltaSpec{
		Addons: map[string]AddonCatalogDeltaEntry{
			"cert-manager": {
				Version: "1.14.5",
				Settings: &AddonSettings{
					PreserveResourcesOnDeletion: &preserve,
				},
			},
		},
	}

	body, err := SaveAddonCatalogDelta(spec)
	if err != nil {
		t.Fatalf("SaveAddonCatalogDelta: %v", err)
	}

	lines := strings.SplitN(string(body), "\n", 2)
	if lines[0] != AddonCatalogDeltaSchemaHeader {
		t.Errorf("line 1 = %q, want schema header %q", lines[0], AddonCatalogDeltaSchemaHeader)
	}
	if !strings.Contains(string(body), "name: addon-catalog-delta") {
		t.Errorf("output missing metadata.name: addon-catalog-delta:\n%s", body)
	}

	roundTripped, err := LoadAddonCatalogDelta(body)
	if err != nil {
		t.Fatalf("LoadAddonCatalogDelta(SaveAddonCatalogDelta(spec)): %v", err)
	}
	cm := roundTripped.Addons["cert-manager"]
	if cm.Version != "1.14.5" || cm.Settings == nil || cm.Settings.PreserveResourcesOnDeletion == nil || !*cm.Settings.PreserveResourcesOnDeletion {
		t.Errorf("round-trip cert-manager entry = %+v, want version 1.14.5 + preserveResourcesOnDeletion=true", cm)
	}
}
