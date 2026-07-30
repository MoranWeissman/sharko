package models

import (
	"reflect"
	"strings"
	"testing"
)

// TestClusterAssignmentAddonSettings_NoPreserveResourcesOnDeletionField
// pins the six-field shape of ClusterAssignmentAddonSettings via
// reflection, so a future edit that re-adds PreserveResourcesOnDeletion
// (or any other per-ApplicationSet-only field) to the per-cluster
// settings struct fails this test with a clear, reviewable diff instead
// of silently reopening the footgun the design doc's §3.2 "Two tiers"
// split exists to close.
func TestClusterAssignmentAddonSettings_NoPreserveResourcesOnDeletionField(t *testing.T) {
	t.Parallel()
	typ := reflect.TypeOf(ClusterAssignmentAddonSettings{})
	const wantFieldCount = 6 // namespace, createNamespace, syncOptions, ignoreDifferences, prune, selfHeal
	if typ.NumField() != wantFieldCount {
		t.Errorf("ClusterAssignmentAddonSettings has %d fields, want %d", typ.NumField(), wantFieldCount)
	}
	for i := 0; i < typ.NumField(); i++ {
		if typ.Field(i).Name == "PreserveResourcesOnDeletion" {
			t.Error("ClusterAssignmentAddonSettings must NOT have a PreserveResourcesOnDeletion field — it's per-ApplicationSet, not per-Application, and can only be set fleet-wide in config.AddonSettings (catalog/addons.yaml)")
		}
	}
}

// TestLoadClusterAssignment_ValidEnvelope_Accept mirrors the design doc's
// worked example (docs/design/2026-07-30-v4-data-file-format.md §2.1):
// cert-manager pinned with a per-cluster ignoreDifferences override,
// metrics-server following the catalog default, external-dns disabled
// but its entry kept.
func TestLoadClusterAssignment_ValidEnvelope_Accept(t *testing.T) {
	t.Parallel()

	body := []byte(`apiVersion: sharko.dev/v1
kind: ClusterAssignment
metadata:
  name: prod-eu
spec:
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

	spec, err := LoadClusterAssignment(body)
	if err != nil {
		t.Fatalf("LoadClusterAssignment: %v", err)
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

// TestLoadClusterAssignment_MissingRequiredEnabled_Reject — spec.addons.<name>
// with no `enabled` key fails schema validation. This is the field the
// design doc marks required (§2.1).
func TestLoadClusterAssignment_MissingRequiredEnabled_Reject(t *testing.T) {
	t.Parallel()

	body := []byte(`apiVersion: sharko.dev/v1
kind: ClusterAssignment
metadata:
  name: prod-eu
spec:
  cluster: prod-eu
  addons:
    cert-manager:
      version: "1.12.0"
`)
	_, err := LoadClusterAssignment(body)
	if err == nil {
		t.Fatal("LoadClusterAssignment: want error for missing enabled, got nil")
	}
	if !strings.Contains(err.Error(), "enabled") {
		t.Errorf("error %q: want substring \"enabled\"", err.Error())
	}
}

// TestLoadClusterAssignment_PreserveResourcesOnDeletion_SchemaReject is
// the defense-in-depth layer: even without cmd/sharko's friendlier
// message, the generated JSON Schema alone must reject
// preserveResourcesOnDeletion inside a cluster settings block, because
// ClusterAssignmentAddonSettings has additionalProperties:false and no
// such field (design doc §3.2 "Two tiers").
func TestLoadClusterAssignment_PreserveResourcesOnDeletion_SchemaReject(t *testing.T) {
	t.Parallel()

	body := []byte(`apiVersion: sharko.dev/v1
kind: ClusterAssignment
metadata:
  name: prod-eu
spec:
  cluster: prod-eu
  addons:
    cert-manager:
      enabled: true
      settings:
        preserveResourcesOnDeletion: false
`)
	_, err := LoadClusterAssignment(body)
	if err == nil {
		t.Fatal("LoadClusterAssignment: want error for preserveResourcesOnDeletion in cluster settings, got nil")
	}
	if !strings.Contains(err.Error(), "preserveResourcesOnDeletion") {
		t.Errorf("error %q: want substring \"preserveResourcesOnDeletion\"", err.Error())
	}
}

// TestLoadClusterAssignment_WrongKind_Reject mirrors
// TestLoadManagedClusters_EnvelopedWrongKind_Reject: an envelope with
// apiVersion: sharko.dev/v1 but a foreign kind must fail loudly rather
// than silently parsing into an empty ClusterAssignmentSpec.
func TestLoadClusterAssignment_WrongKind_Reject(t *testing.T) {
	t.Parallel()

	body := []byte(`apiVersion: sharko.dev/v1
kind: AddonCatalogDelta
metadata:
  name: addon-catalog-delta
spec:
  addons: {}
`)
	_, err := LoadClusterAssignment(body)
	if err == nil {
		t.Fatal("LoadClusterAssignment wrong kind: want error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, `envelope kind "AddonCatalogDelta"`) {
		t.Errorf("error %q: want substring %q", msg, `envelope kind "AddonCatalogDelta"`)
	}
	if !strings.Contains(msg, `expected "ClusterAssignment"`) {
		t.Errorf("error %q: want substring %q", msg, `expected "ClusterAssignment"`)
	}
}

// TestLoadClusterAssignment_NotEnveloped_Reject — ClusterAssignment is a
// v4-only kind with no legacy bare-YAML precedent (unlike
// ManagedClusters). A bare-YAML body must be a hard error, never a
// silent "zero addons" result.
func TestLoadClusterAssignment_NotEnveloped_Reject(t *testing.T) {
	t.Parallel()

	body := []byte(`cluster: prod-eu
addons:
  cert-manager:
    enabled: true
`)
	_, err := LoadClusterAssignment(body)
	if err == nil {
		t.Fatal("LoadClusterAssignment: want error for non-enveloped body, got nil")
	}
	if !strings.Contains(err.Error(), "not a Sharko-enveloped document") {
		t.Errorf("error %q: want substring \"not a Sharko-enveloped document\"", err.Error())
	}
}

// TestSaveClusterAssignment_RoundTrip pins the writer contract: line 1 is
// the schema header, metadata.name equals spec.Cluster (design doc §2.1
// worked example), and Load(Save(spec)) reproduces the same spec.
func TestSaveClusterAssignment_RoundTrip(t *testing.T) {
	t.Parallel()

	createNamespace := true
	spec := ClusterAssignmentSpec{
		Cluster: "prod-eu",
		Addons: map[string]ClusterAssignmentAddon{
			"cert-manager": {
				Enabled: true,
				Version: "1.12.0",
				Settings: &ClusterAssignmentAddonSettings{
					Namespace:       "cert-manager",
					CreateNamespace: &createNamespace,
				},
			},
			"external-dns": {Enabled: false},
		},
	}

	body, err := SaveClusterAssignment(spec)
	if err != nil {
		t.Fatalf("SaveClusterAssignment: %v", err)
	}

	lines := strings.SplitN(string(body), "\n", 2)
	if lines[0] != ClusterAssignmentSchemaHeader {
		t.Errorf("line 1 = %q, want schema header %q", lines[0], ClusterAssignmentSchemaHeader)
	}
	if !strings.Contains(string(body), "name: prod-eu") {
		t.Errorf("output missing metadata.name: prod-eu:\n%s", body)
	}

	roundTripped, err := LoadClusterAssignment(body)
	if err != nil {
		t.Fatalf("LoadClusterAssignment(SaveClusterAssignment(spec)): %v", err)
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

// TestSaveClusterAssignment_EmptyAddonsMap — spec.addons may be an empty
// map (design doc §2.1: "May be empty ({})"). Saving and reloading a
// spec with zero addon entries must not error.
func TestSaveClusterAssignment_EmptyAddonsMap(t *testing.T) {
	t.Parallel()

	spec := ClusterAssignmentSpec{Cluster: "prod-eu", Addons: map[string]ClusterAssignmentAddon{}}
	body, err := SaveClusterAssignment(spec)
	if err != nil {
		t.Fatalf("SaveClusterAssignment with empty addons: %v", err)
	}
	roundTripped, err := LoadClusterAssignment(body)
	if err != nil {
		t.Fatalf("LoadClusterAssignment round-trip with empty addons: %v", err)
	}
	if len(roundTripped.Addons) != 0 {
		t.Errorf("round-trip Addons = %+v, want empty", roundTripped.Addons)
	}
}
