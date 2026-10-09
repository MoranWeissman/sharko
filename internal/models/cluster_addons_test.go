package models

import (
	"reflect"
	"strings"
	"testing"
)

// TestClusterAddonsAddonSettings_NoPreserveResourcesOnDeletionField
// pins the six-field shape of ClusterAddonsAddonSettings via
// reflection, so a future edit that re-adds PreserveResourcesOnDeletion
// (or any other per-ApplicationSet-only field) to the per-cluster
// settings struct fails this test with a clear, reviewable diff instead
// of silently reopening the footgun the design doc's §3.2 "Two tiers"
// split exists to close.
func TestClusterAddonsAddonSettings_NoPreserveResourcesOnDeletionField(t *testing.T) {
	t.Parallel()
	typ := reflect.TypeOf(ClusterAddonsAddonSettings{})
	const wantFieldCount = 6 // namespace, createNamespace, syncOptions, ignoreDifferences, prune, selfHeal
	if typ.NumField() != wantFieldCount {
		t.Errorf("ClusterAddonsAddonSettings has %d fields, want %d", typ.NumField(), wantFieldCount)
	}
	for i := 0; i < typ.NumField(); i++ {
		if typ.Field(i).Name == "PreserveResourcesOnDeletion" {
			t.Error("ClusterAddonsAddonSettings must NOT have a PreserveResourcesOnDeletion field — it's per-ApplicationSet, not per-Application, and can only be set fleet-wide in config.AddonSettings (catalog/addons.yaml)")
		}
	}
}

// TestLoadClusterAddons_ValidEnvelope_Accept mirrors the design doc's
// worked example (docs/design/2026-07-30-v4-data-file-format.md §2.1):
// cert-manager pinned with a per-cluster ignoreDifferences override,
// metrics-server following the catalog default, external-dns disabled
// but its entry kept.
func TestLoadClusterAddons_ValidEnvelope_Accept(t *testing.T) {
	t.Parallel()

	body := []byte(`apiVersion: sharko.dev/v1
kind: ClusterAddons
cluster: prod-eu
addons:
  cert-manager:
    enabled: true
    version: "1.12.0"
    settings:
      ignoreDifferences:
        - group: admissionregistration.k8s.io
          kind: ValidatingWebhookConfiguration
          jsonPointers:
            - /webhooks/0/clientConfig/caBundle
  metrics-server:
    enabled: true
  external-dns:
    enabled: false
`)

	spec, err := LoadClusterAddons(body)
	if err != nil {
		t.Fatalf("LoadClusterAddons: %v", err)
	}
	if spec.Cluster != "prod-eu" {
		t.Errorf("spec.Cluster = %q, want prod-eu", spec.Cluster)
	}
	if len(spec.Addons) != 3 {
		t.Fatalf("len(spec.Addons) = %d, want 3", len(spec.Addons))
	}
	cm, ok := spec.Addons["cert-manager"]
	if !ok {
		t.Fatal("missing cert-manager entry")
	}
	if !cm.Enabled {
		t.Error("cert-manager.Enabled = false, want true")
	}
	if cm.Version != "1.12.0" {
		t.Errorf("cert-manager.Version = %q, want 1.12.0", cm.Version)
	}
	if cm.Settings == nil || len(cm.Settings.IgnoreDifferences) != 1 {
		t.Fatalf("cert-manager.Settings.IgnoreDifferences: got %+v, want 1 entry", cm.Settings)
	}

	ed, ok := spec.Addons["external-dns"]
	if !ok {
		t.Fatal("missing external-dns entry")
	}
	if ed.Enabled {
		t.Error("external-dns.Enabled = true, want false (kept but disabled)")
	}

	ms, ok := spec.Addons["metrics-server"]
	if !ok {
		t.Fatal("missing metrics-server entry")
	}
	if ms.Version != "" {
		t.Errorf("metrics-server.Version = %q, want empty (follows catalog default)", ms.Version)
	}
}

// TestLoadClusterAddons_MissingRequiredEnabled_Reject — spec.addons.<name>
// with no `enabled` key fails schema validation. This is the field the
// design doc marks required (§2.1).
func TestLoadClusterAddons_MissingRequiredEnabled_Reject(t *testing.T) {
	t.Parallel()

	body := []byte(`apiVersion: sharko.dev/v1
kind: ClusterAddons
cluster: prod-eu
addons:
  cert-manager:
    version: "1.12.0"
`)
	_, err := LoadClusterAddons(body)
	if err == nil {
		t.Fatal("LoadClusterAddons: want error for missing enabled, got nil")
	}
	if !strings.Contains(err.Error(), "enabled") {
		t.Errorf("error %q: want substring \"enabled\"", err.Error())
	}
}

// TestLoadClusterAddons_PreserveResourcesOnDeletion_SchemaReject is
// the defense-in-depth layer: even without cmd/sharko's friendlier
// message, the generated JSON Schema alone must reject
// preserveResourcesOnDeletion inside a cluster settings block, because
// ClusterAddonsAddonSettings has additionalProperties:false and no
// such field (design doc §3.2 "Two tiers").
func TestLoadClusterAddons_PreserveResourcesOnDeletion_SchemaReject(t *testing.T) {
	t.Parallel()

	body := []byte(`apiVersion: sharko.dev/v1
kind: ClusterAddons
cluster: prod-eu
addons:
  cert-manager:
    enabled: true
    settings:
      preserveResourcesOnDeletion: false
`)
	_, err := LoadClusterAddons(body)
	if err == nil {
		t.Fatal("LoadClusterAddons: want error for preserveResourcesOnDeletion in cluster settings, got nil")
	}
	if !strings.Contains(err.Error(), "preserveResourcesOnDeletion") {
		t.Errorf("error %q: want substring \"preserveResourcesOnDeletion\"", err.Error())
	}
}

// TestLoadClusterAddons_WrongKind_Reject mirrors
// TestLoadManagedClusters_EnvelopedWrongKind_Reject: an envelope with
// apiVersion: sharko.dev/v1 but a foreign kind must fail loudly rather
// than silently parsing into an empty ClusterAddonsSpec.
func TestLoadClusterAddons_WrongKind_Reject(t *testing.T) {
	t.Parallel()

	body := []byte(`apiVersion: sharko.dev/v1
kind: AddonCatalog
addons: {}
`)
	_, err := LoadClusterAddons(body)
	if err == nil {
		t.Fatal("LoadClusterAddons wrong kind: want error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, `kind "AddonCatalog"`) {
		t.Errorf("error %q: want substring %q", msg, `kind "AddonCatalog"`)
	}
	if !strings.Contains(msg, `expected "ClusterAddons"`) {
		t.Errorf("error %q: want substring %q", msg, `expected "ClusterAddons"`)
	}
}

// TestLoadClusterAddons_NotEnveloped_Reject — ClusterAddons is a
// v4-only kind with no legacy bare-YAML precedent (unlike
// ManagedClusters). A bare-YAML body must be a hard error, never a
// silent "zero addons" result.
func TestLoadClusterAddons_NotEnveloped_Reject(t *testing.T) {
	t.Parallel()

	body := []byte(`cluster: prod-eu
addons:
  cert-manager:
    enabled: true
`)
	_, err := LoadClusterAddons(body)
	if err == nil {
		t.Fatal("LoadClusterAddons: want error for non-enveloped body, got nil")
	}
	if !strings.Contains(err.Error(), "not a Sharko document") {
		t.Errorf("error %q: want substring \"not a Sharko document\"", err.Error())
	}
}

// TestSaveClusterAddons_RoundTrip pins the writer contract: line 1 is
// the schema header, the flat cluster: field carries spec.Cluster (there
// is no metadata.name any more — the file's identity is its path on
// disk, design doc §9), and Load(Save(spec)) reproduces the same spec.
func TestSaveClusterAddons_RoundTrip(t *testing.T) {
	t.Parallel()

	createNamespace := true
	spec := ClusterAddonsSpec{
		Cluster: "prod-eu",
		Addons: map[string]ClusterAddonsAddon{
			"cert-manager": {
				Enabled: true,
				Version: "1.12.0",
				Settings: &ClusterAddonsAddonSettings{
					Namespace:       "cert-manager",
					CreateNamespace: &createNamespace,
				},
			},
			"external-dns": {Enabled: false},
		},
	}

	body, err := SaveClusterAddons(spec)
	if err != nil {
		t.Fatalf("SaveClusterAddons: %v", err)
	}

	lines := strings.SplitN(string(body), "\n", 2)
	if lines[0] != ClusterAddonsSchemaHeader {
		t.Errorf("line 1 = %q, want schema header %q", lines[0], ClusterAddonsSchemaHeader)
	}
	if !strings.Contains(string(body), "cluster: prod-eu") {
		t.Errorf("output missing cluster: prod-eu:\n%s", body)
	}

	roundTripped, err := LoadClusterAddons(body)
	if err != nil {
		t.Fatalf("LoadClusterAddons(SaveClusterAddons(spec)): %v", err)
	}
	if roundTripped.Cluster != spec.Cluster {
		t.Errorf("round-trip Cluster = %q, want %q", roundTripped.Cluster, spec.Cluster)
	}
	if len(roundTripped.Addons) != len(spec.Addons) {
		t.Fatalf("round-trip has %d addons, want %d", len(roundTripped.Addons), len(spec.Addons))
	}
	cm := roundTripped.Addons["cert-manager"]
	if cm.Version != "1.12.0" || cm.Settings == nil || cm.Settings.Namespace != "cert-manager" {
		t.Errorf("round-trip cert-manager entry = %+v, want version 1.12.0 + namespace cert-manager", cm)
	}
}

// TestSaveClusterAddons_EmptyAddonsMap — spec.addons may be an empty
// map (design doc §2.1: "May be empty ({})"). Saving and reloading a
// spec with zero addon entries must not error.
func TestSaveClusterAddons_EmptyAddonsMap(t *testing.T) {
	t.Parallel()

	spec := ClusterAddonsSpec{Cluster: "prod-eu", Addons: map[string]ClusterAddonsAddon{}}
	body, err := SaveClusterAddons(spec)
	if err != nil {
		t.Fatalf("SaveClusterAddons with empty addons: %v", err)
	}
	roundTripped, err := LoadClusterAddons(body)
	if err != nil {
		t.Fatalf("LoadClusterAddons round-trip with empty addons: %v", err)
	}
	if len(roundTripped.Addons) != 0 {
		t.Errorf("round-trip Addons = %+v, want empty", roundTripped.Addons)
	}
}
