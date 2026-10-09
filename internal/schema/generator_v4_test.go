// v4 Wave 1 Story 2.6 — generator coverage for the two new kinds
// (ClusterAddons, AddonCatalog). Kept in a separate file from
// generator_test.go (rather than growing that file's tables) so the
// v3-kind and v4-kind test suites can evolve independently; the helpers
// here deliberately mirror genManagedClusters/genAddonCatalogV4's shape.
package schema_test

import (
	"bytes"
	"testing"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/models"
	sharkoschema "github.com/MoranWeissman/sharko/internal/schema"
)

// Test wrappers duplicated from cmd/schema-gen/main.go — same rationale
// as managedClustersDoc/addonCatalogDoc above: package main can't be
// imported, so each test file that needs to reflect an envelope
// declares its own structurally-identical wrapper.
type clusterAddonsDoc struct {
	APIVersion string                   `json:"apiVersion"`
	Kind       string                   `json:"kind"`
	Metadata   sharkoschema.Metadata    `json:"metadata"`
	Spec       models.ClusterAddonsSpec `json:"spec"`
}

type addonCatalogDeltaDoc struct {
	APIVersion string                  `json:"apiVersion"`
	Kind       string                  `json:"kind"`
	Metadata   sharkoschema.Metadata   `json:"metadata"`
	Spec       config.AddonCatalogSpec `json:"spec"`
}

func genClusterAddons(t *testing.T) []byte {
	t.Helper()
	out, err := sharkoschema.GenerateSchema(
		&clusterAddonsDoc{},
		sharkoschema.ClusterAddonsSchemaID,
		"Sharko ClusterAddons",
		"cluster-addons/<cluster-name>.yaml — which addons run on this cluster, at which version, tuned how (v4).",
		sharkoschema.KindClusterAddons,
	)
	if err != nil {
		t.Fatalf("GenerateSchema(cluster-addons): %v", err)
	}
	return out
}

func genAddonCatalogV4(t *testing.T) []byte {
	t.Helper()
	out, err := sharkoschema.GenerateSchema(
		&addonCatalogDeltaDoc{},
		sharkoschema.AddonCatalogV4SchemaID,
		"Sharko AddonCatalog",
		"catalog/addons.yaml — the user's delta against the shipped addon catalog (v4).",
		sharkoschema.KindAddonCatalog,
	)
	if err != nil {
		t.Fatalf("GenerateSchema(addon-catalog-delta): %v", err)
	}
	return out
}

// TestGenerateSchemas_V4Kinds_Idempotent mirrors
// TestGenerateSchemas_Idempotent for the two new v4 kinds — the same
// invariant the CI "Schemas Up To Date" drift gate depends on.
func TestGenerateSchemas_V4Kinds_Idempotent(t *testing.T) {
	t.Parallel()

	t.Run("cluster-addons", func(t *testing.T) {
		t.Parallel()
		a := genClusterAddons(t)
		b := genClusterAddons(t)
		if !bytes.Equal(a, b) {
			t.Fatalf("schema generation not idempotent:\nfirst:  %s\nsecond: %s", a, b)
		}
	})

	t.Run("addon-catalog-delta", func(t *testing.T) {
		t.Parallel()
		a := genAddonCatalogV4(t)
		b := genAddonCatalogV4(t)
		if !bytes.Equal(a, b) {
			t.Fatalf("schema generation not idempotent:\nfirst:  %s\nsecond: %s", a, b)
		}
	})
}

// TestGenerateClusterAddons_AcceptsDesignDocExample validates the
// generator's output against the design doc's §2.1 worked example.
func TestGenerateClusterAddons_AcceptsDesignDocExample(t *testing.T) {
	t.Parallel()
	schemaBytes := genClusterAddons(t)
	sch := compileSchema(t, schemaBytes, sharkoschema.ClusterAddonsSchemaID)

	example := `apiVersion: sharko.dev/v1
kind: ClusterAddons
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
`
	if err := sch.Validate(yamlToInterface(t, example)); err != nil {
		t.Fatalf("design-doc-shape example failed validation: %v", err)
	}
}

// TestGenerateClusterAddons_RejectsPreserveResourcesOnDeletion pins
// the schema-level half of design doc §3.2's "Two tiers" enforcement:
// preserveResourcesOnDeletion inside spec.addons.*.settings must be
// rejected because ClusterAddonsAddonSettings has no such field and
// additionalProperties is false.
func TestGenerateClusterAddons_RejectsPreserveResourcesOnDeletion(t *testing.T) {
	t.Parallel()
	schemaBytes := genClusterAddons(t)
	sch := compileSchema(t, schemaBytes, sharkoschema.ClusterAddonsSchemaID)

	body := `apiVersion: sharko.dev/v1
kind: ClusterAddons
metadata:
  name: prod-eu
spec:
  cluster: prod-eu
  addons:
    cert-manager:
      enabled: true
      settings:
        preserveResourcesOnDeletion: false
`
	if err := sch.Validate(yamlToInterface(t, body)); err == nil {
		t.Fatal("expected validation error for preserveResourcesOnDeletion in a per-cluster settings block, got nil")
	}
}

// TestGenerateClusterAddons_RejectsMissingEnabled — every addon
// entry requires `enabled` (design doc §2.1).
func TestGenerateClusterAddons_RejectsMissingEnabled(t *testing.T) {
	t.Parallel()
	schemaBytes := genClusterAddons(t)
	sch := compileSchema(t, schemaBytes, sharkoschema.ClusterAddonsSchemaID)

	body := `apiVersion: sharko.dev/v1
kind: ClusterAddons
metadata:
  name: prod-eu
spec:
  cluster: prod-eu
  addons:
    cert-manager:
      version: "1.12.0"
`
	if err := sch.Validate(yamlToInterface(t, body)); err == nil {
		t.Fatal("expected validation error for addon entry missing enabled, got nil")
	}
}

// TestGenerateAddonCatalogV4_AcceptsDesignDocExample validates the
// generator's output against the design doc's §2.3 worked example.
func TestGenerateAddonCatalogV4_AcceptsDesignDocExample(t *testing.T) {
	t.Parallel()
	schemaBytes := genAddonCatalogV4(t)
	sch := compileSchema(t, schemaBytes, sharkoschema.AddonCatalogV4SchemaID)

	example := `apiVersion: sharko.dev/v1
kind: AddonCatalog
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
`
	if err := sch.Validate(yamlToInterface(t, example)); err != nil {
		t.Fatalf("design-doc-shape example failed validation: %v", err)
	}
}

// TestGenerateAddonCatalogV4_AllowsPreserveResourcesOnDeletion — the
// AddonCatalog settings block DOES allow this field (it's the only
// legal place for it — design doc §3.2).
func TestGenerateAddonCatalogV4_AllowsPreserveResourcesOnDeletion(t *testing.T) {
	t.Parallel()
	schemaBytes := genAddonCatalogV4(t)
	sch := compileSchema(t, schemaBytes, sharkoschema.AddonCatalogV4SchemaID)

	body := `apiVersion: sharko.dev/v1
kind: AddonCatalog
metadata:
  name: addon-catalog-delta
spec:
  addons:
    cert-manager:
      version: "1.14.5"
      settings:
        preserveResourcesOnDeletion: false
`
	if err := sch.Validate(yamlToInterface(t, body)); err != nil {
		t.Fatalf("expected preserveResourcesOnDeletion to be legal on AddonCatalog settings, got: %v", err)
	}
}

// TestGenerateAddonCatalogV4_RejectsWrongKind — a v3 AddonCatalog
// body must fail against the v4 AddonCatalog schema (design doc
// decision D5: distinct kinds, never silently cross-validated).
func TestGenerateAddonCatalogV4_RejectsWrongKind(t *testing.T) {
	t.Parallel()
	schemaBytes := genAddonCatalogV4(t)
	sch := compileSchema(t, schemaBytes, sharkoschema.AddonCatalogV4SchemaID)

	wrongKind := `apiVersion: sharko.dev/v1
kind: AddonCatalog
metadata:
  name: addon-catalog
spec:
  applicationsets: []
`
	if err := sch.Validate(yamlToInterface(t, wrongKind)); err == nil {
		t.Fatal("expected validation error for kind: AddonCatalog against the AddonCatalog schema, got nil")
	}
}

// TestGenerateAddonCatalogV4_EmptyAddonsMap_Accept — spec.addons may
// be an empty map (design doc decision D16).
func TestGenerateAddonCatalogV4_EmptyAddonsMap_Accept(t *testing.T) {
	t.Parallel()
	schemaBytes := genAddonCatalogV4(t)
	sch := compileSchema(t, schemaBytes, sharkoschema.AddonCatalogV4SchemaID)

	body := `apiVersion: sharko.dev/v1
kind: AddonCatalog
metadata:
  name: addon-catalog-delta
spec:
  addons: {}
`
	if err := sch.Validate(yamlToInterface(t, body)); err != nil {
		t.Fatalf("expected empty addons map to validate, got: %v", err)
	}
}
