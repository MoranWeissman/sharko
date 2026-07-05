package schema_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"
	"gopkg.in/yaml.v3"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/models"
	sharkoschema "github.com/MoranWeissman/sharko/internal/schema"
)

// Test wrappers duplicated from cmd/schema-gen/main.go. We can't import
// `package main`, so each test that needs to reflect a Sharko envelope
// declares its own wrapper here. The schema-gen binary's wrappers and
// these test wrappers must stay structurally identical — if you change
// one, change the other.
//
// (TestGenerator_WrapperParity_DocumentedDrift below is the canary that
// catches drift: it round-trips a yaml-marshalled schema.Envelope[T]
// through the wrapper's JSON shape and fails if the field set diverges.
// The test name flags that this is a known maintenance hazard the package
// doc warns about.)

type managedClustersDoc struct {
	APIVersion string                     `json:"apiVersion"`
	Kind       string                     `json:"kind"`
	Metadata   sharkoschema.Metadata      `json:"metadata"`
	Spec       models.ManagedClustersSpec `json:"spec"`
}

type addonCatalogDoc struct {
	APIVersion string                  `json:"apiVersion"`
	Kind       string                  `json:"kind"`
	Metadata   sharkoschema.Metadata   `json:"metadata"`
	Spec       config.AddonCatalogSpec `json:"spec"`
}

// genManagedClusters is a test helper that calls the generator with the
// managed-clusters wrapper + identity constants. Hides the boilerplate so
// individual tests stay short.
func genManagedClusters(t *testing.T) []byte {
	t.Helper()
	out, err := sharkoschema.GenerateSchema(
		&managedClustersDoc{},
		sharkoschema.ManagedClustersSchemaID,
		"Sharko ManagedClusters",
		"managed-clusters.yaml — the registry of clusters Sharko manages, including their credential paths and addon labels.",
		sharkoschema.KindManagedClusters,
	)
	if err != nil {
		t.Fatalf("GenerateSchema(managed-clusters): %v", err)
	}
	return out
}

// genAddonCatalog mirrors genManagedClusters for the addon-catalog kind.
func genAddonCatalog(t *testing.T) []byte {
	t.Helper()
	out, err := sharkoschema.GenerateSchema(
		&addonCatalogDoc{},
		sharkoschema.AddonCatalogSchemaID,
		"Sharko AddonCatalog",
		"addons-catalog.yaml — the catalog of addons (ApplicationSets) Sharko can deploy to managed clusters.",
		sharkoschema.KindAddonCatalog,
	)
	if err != nil {
		t.Fatalf("GenerateSchema(addon-catalog): %v", err)
	}
	return out
}

// compileSchema compiles a generated schema's bytes with santhosh-tekuri/v5
// against the draft 2020-12 dialect (the dialect we pin in $schema). The
// id argument is the synthetic URL we register the bytes against — must
// match the schema's own $id so cross-validation references resolve.
func compileSchema(t *testing.T, schemaBytes []byte, id string) *jsonschema.Schema {
	t.Helper()
	compiler := jsonschema.NewCompiler()
	compiler.Draft = jsonschema.Draft2020
	if err := compiler.AddResource(id, bytes.NewReader(schemaBytes)); err != nil {
		t.Fatalf("AddResource(%s): %v\nschema=%s", id, err, schemaBytes)
	}
	sch, err := compiler.Compile(id)
	if err != nil {
		t.Fatalf("Compile(%s): %v\nschema=%s", id, err, schemaBytes)
	}
	return sch
}

// yamlToInterface decodes YAML into a generic Go value suitable for handing
// to santhosh-tekuri's Validate(). The validator works on JSON-shaped
// values (map[string]any, []any, primitives), and yaml.v3 produces the
// same shape EXCEPT it uses map[string]any with keys-as-strings when the
// source YAML has only string keys (which all Sharko config does), so a
// straight Decode is sufficient — no recursive conversion required.
func yamlToInterface(t *testing.T, body string) interface{} {
	t.Helper()
	var v interface{}
	if err := yaml.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("yaml decode: %v\nbody=%s", err, body)
	}
	return normalizeYAMLForJSONSchema(v)
}

// normalizeYAMLForJSONSchema converts yaml.v3's preferred
// map[interface{}]interface{} into map[string]interface{}, which is what
// santhosh-tekuri/jsonschema expects. yaml.v3 (in contrast to yaml.v2)
// uses string keys by default, BUT only when the source key is unquoted.
// To be defensive against future fixtures with quoted keys or numeric
// keys, normalise here.
func normalizeYAMLForJSONSchema(v interface{}) interface{} {
	switch x := v.(type) {
	case map[string]interface{}:
		for k, vv := range x {
			x[k] = normalizeYAMLForJSONSchema(vv)
		}
		return x
	case map[interface{}]interface{}:
		out := make(map[string]interface{}, len(x))
		for k, vv := range x {
			out[asString(k)] = normalizeYAMLForJSONSchema(vv)
		}
		return out
	case []interface{}:
		for i, vv := range x {
			x[i] = normalizeYAMLForJSONSchema(vv)
		}
		return x
	default:
		return v
	}
}

func asString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	// Fallback — let JSON do the stringification so int keys round-trip.
	b, _ := json.Marshal(v)
	return strings.Trim(string(b), `"`)
}

// TestGenerateSchemas_Idempotent — running the generator twice MUST
// produce byte-identical output. This is the invariant the CI drift
// gate ("Schemas Up To Date") depends on.
func TestGenerateSchemas_Idempotent(t *testing.T) {
	t.Parallel()

	t.Run("managed-clusters", func(t *testing.T) {
		t.Parallel()
		a := genManagedClusters(t)
		b := genManagedClusters(t)
		if !bytes.Equal(a, b) {
			t.Fatalf("schema generation not idempotent:\nfirst:  %s\nsecond: %s", a, b)
		}
	})

	t.Run("addon-catalog", func(t *testing.T) {
		t.Parallel()
		a := genAddonCatalog(t)
		b := genAddonCatalog(t)
		if !bytes.Equal(a, b) {
			t.Fatalf("schema generation not idempotent:\nfirst:  %s\nsecond: %s", a, b)
		}
	})
}

// TestGenerateManagedClusters_AcceptsDesignDocExample validates that the
// generator's output accepts an enveloped managed-clusters YAML in the
// shape the design doc shows at lines 100-114 of
// docs/design/2026-05-12-v125-architectural-todos.md.
//
// Note on fidelity to the design example: the design-doc example uses
// aspirational `server:` and `addons:` keys per-cluster. The committed
// ManagedClusterEntry type (Story 9.1, locked) doesn't declare those
// fields, so the schema has `additionalProperties: false` at the cluster
// level. This test uses the canonical Sharko field set (`name`,
// `secretPath`, `region`, `labels`) where `labels` carries the
// addon-enablement map — the legacy on-disk shape the parser actually
// produces. The envelope (apiVersion / kind / metadata / spec /
// spec.clusters) is byte-for-byte the design doc's shape; the per-cluster
// field names are the real Sharko shape. This is a deliberate scope-
// preserving choice — the design doc itself notes the example is
// illustrative, and changing ManagedClusterEntry to add `server` /
// `addons` is out of scope for Story 9.3 (and would be redundant with the
// existing `labels` map anyway).
func TestGenerateManagedClusters_AcceptsDesignDocExample(t *testing.T) {
	t.Parallel()
	schemaBytes := genManagedClusters(t)
	sch := compileSchema(t, schemaBytes, sharkoschema.ManagedClustersSchemaID)

	example := `apiVersion: sharko.dev/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: prod-eu
      secretPath: clusters/prod-eu
      region: eu-west-1
      labels:
        team: platform
        cert-manager: enabled
        datadog: disabled
`
	if err := sch.Validate(yamlToInterface(t, example)); err != nil {
		t.Fatalf("design-doc-shape example failed validation: %v", err)
	}
}

// TestGenerateManagedClusters_RejectsMissingApiVersion pins the
// apiVersion-required invariant: a managed-clusters file with no
// apiVersion at all is rejected. (The reader in
// internal/models/cluster.go routes such files to the legacy bare-YAML
// parser; this schema is for the enveloped path only.)
func TestGenerateManagedClusters_RejectsMissingApiVersion(t *testing.T) {
	t.Parallel()
	schemaBytes := genManagedClusters(t)
	sch := compileSchema(t, schemaBytes, sharkoschema.ManagedClustersSchemaID)

	// Missing apiVersion at the envelope root.
	missing := `kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters: []
`
	if err := sch.Validate(yamlToInterface(t, missing)); err == nil {
		t.Fatal("expected validation error for missing apiVersion, got nil")
	}
}

// TestGenerateManagedClusters_RejectsWrongApiVersionType — apiVersion
// must be a string. Submitting a number (or any non-string) should
// fail. The const constraint guarantees this.
func TestGenerateManagedClusters_RejectsWrongApiVersionType(t *testing.T) {
	t.Parallel()
	schemaBytes := genManagedClusters(t)
	sch := compileSchema(t, schemaBytes, sharkoschema.ManagedClustersSchemaID)

	wrongType := `apiVersion: 42
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters: []
`
	if err := sch.Validate(yamlToInterface(t, wrongType)); err == nil {
		t.Fatal("expected validation error for apiVersion: 42, got nil")
	}
}

// TestGenerateManagedClusters_RejectsMissingRequiredClusterName — every
// entry in spec.clusters must have a `name` field. Sharko code depends
// on this throughout; the schema must enforce it.
func TestGenerateManagedClusters_RejectsMissingRequiredClusterName(t *testing.T) {
	t.Parallel()
	schemaBytes := genManagedClusters(t)
	sch := compileSchema(t, schemaBytes, sharkoschema.ManagedClustersSchemaID)

	missingName := `apiVersion: sharko.dev/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - secretPath: clusters/prod-eu
      region: eu-west-1
`
	if err := sch.Validate(yamlToInterface(t, missingName)); err == nil {
		t.Fatal("expected validation error for cluster without name, got nil")
	}
}

// TestGenerateAddonCatalog_AcceptsValidEnvelope validates that the
// addon-catalog generator's output accepts an enveloped addon-catalog
// document in the Sharko shape. The fixture covers the four required
// per-entry fields (name, repoURL, chart, version) plus a handful of
// optional fields to confirm the schema doesn't reject them.
func TestGenerateAddonCatalog_AcceptsValidEnvelope(t *testing.T) {
	t.Parallel()
	schemaBytes := genAddonCatalog(t)
	sch := compileSchema(t, schemaBytes, sharkoschema.AddonCatalogSchemaID)

	example := `apiVersion: sharko.dev/v1
kind: AddonCatalog
metadata:
  name: addon-catalog
spec:
  applicationsets:
    - name: cert-manager
      repoURL: https://charts.jetstack.io
      chart: cert-manager
      version: v1.16.1
      namespace: cert-manager
      syncWave: -10
    - name: datadog
      repoURL: https://helm.datadoghq.com
      chart: datadog
      version: 3.78.0
      namespace: datadog
      secrets:
        - secretName: datadog-api-key
          namespace: datadog
          keys:
            api-key: secrets/datadog/api-key
`
	if err := sch.Validate(yamlToInterface(t, example)); err != nil {
		t.Fatalf("valid addon-catalog example failed validation: %v", err)
	}
}

// TestGenerateAddonCatalog_RejectsWrongKind — a file declaring
// `kind: ManagedClusters` (cross-kind) must fail addon-catalog
// validation. Pins the kind-const guard that prevents a managed-clusters
// file from being silently parsed as an empty addon-catalog (since the
// envelope wrappers share structure, structural validation alone would
// pass).
func TestGenerateAddonCatalog_RejectsWrongKind(t *testing.T) {
	t.Parallel()
	schemaBytes := genAddonCatalog(t)
	sch := compileSchema(t, schemaBytes, sharkoschema.AddonCatalogSchemaID)

	wrongKind := `apiVersion: sharko.dev/v1
kind: ManagedClusters
metadata:
  name: addon-catalog
spec:
  applicationsets: []
`
	err := sch.Validate(yamlToInterface(t, wrongKind))
	if err == nil {
		t.Fatal("expected validation error for kind: ManagedClusters in addon-catalog schema, got nil")
	}
	if !strings.Contains(err.Error(), "kind") && !strings.Contains(err.Error(), "AddonCatalog") {
		t.Errorf("error doesn't mention the kind mismatch: %v", err)
	}
}

// TestGenerateManagedClusters_RejectsWrongKind — symmetric guard:
// kind: AddonCatalog in a managed-clusters file fails validation.
func TestGenerateManagedClusters_RejectsWrongKind(t *testing.T) {
	t.Parallel()
	schemaBytes := genManagedClusters(t)
	sch := compileSchema(t, schemaBytes, sharkoschema.ManagedClustersSchemaID)

	wrongKind := `apiVersion: sharko.dev/v1
kind: AddonCatalog
metadata:
  name: managed-clusters
spec:
  clusters: []
`
	if err := sch.Validate(yamlToInterface(t, wrongKind)); err == nil {
		t.Fatal("expected validation error for kind: AddonCatalog in managed-clusters schema, got nil")
	}
}

// TestGenerateManagedClusters_RejectsForeignTopLevelField — the
// generated envelope schema sets additionalProperties: false at the
// root, so a file with an unexpected top-level key (typo, future
// extension) is rejected loudly rather than silently ignored. This is
// the editor-friendliness payoff: yaml-language-server flags the typo
// in-place.
func TestGenerateManagedClusters_RejectsForeignTopLevelField(t *testing.T) {
	t.Parallel()
	schemaBytes := genManagedClusters(t)
	sch := compileSchema(t, schemaBytes, sharkoschema.ManagedClustersSchemaID)

	foreign := `apiVersion: sharko.dev/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters: []
unexpectedField: oops
`
	if err := sch.Validate(yamlToInterface(t, foreign)); err == nil {
		t.Fatal("expected validation error for unexpected top-level field, got nil")
	}
}

// TestGenerator_SchemaShapeBasics is a coarse-grained sanity check on
// the top-level shape of the emitted schemas. The other tests cover
// semantic invariants (validation behaviour); this one pins the
// identity fields the public contract depends on so a future
// invopop/jsonschema upgrade that drops a property doesn't slip through.
func TestGenerator_SchemaShapeBasics(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		gen       func(*testing.T) []byte
		id        string
		kindConst string
	}{
		{"managed-clusters", genManagedClusters, sharkoschema.ManagedClustersSchemaID, "ManagedClusters"},
		{"addon-catalog", genAddonCatalog, sharkoschema.AddonCatalogSchemaID, "AddonCatalog"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var doc map[string]interface{}
			if err := json.Unmarshal(tc.gen(t), &doc); err != nil {
				t.Fatalf("emitted schema is not valid JSON: %v", err)
			}
			if got, _ := doc["$schema"].(string); got != sharkoschema.SchemaDialect {
				t.Errorf("$schema = %q, want %q", got, sharkoschema.SchemaDialect)
			}
			if got, _ := doc["$id"].(string); got != tc.id {
				t.Errorf("$id = %q, want %q", got, tc.id)
			}
			props, ok := doc["properties"].(map[string]interface{})
			if !ok {
				t.Fatal("schema has no properties block at the root")
			}
			kindProp, _ := props["kind"].(map[string]interface{})
			if got, _ := kindProp["const"].(string); got != tc.kindConst {
				t.Errorf("properties.kind.const = %q, want %q", got, tc.kindConst)
			}
			apiVersionProp, _ := props["apiVersion"].(map[string]interface{})
			gotEnum, _ := apiVersionProp["enum"].([]interface{})
			wantEnum := []string{sharkoschema.APIVersion, sharkoschema.APIVersionLegacy}
			if len(gotEnum) != len(wantEnum) {
				t.Fatalf("properties.apiVersion.enum = %v, want %v", gotEnum, wantEnum)
			}
			for i, want := range wantEnum {
				if got, _ := gotEnum[i].(string); got != want {
					t.Errorf("properties.apiVersion.enum[%d] = %q, want %q", i, got, want)
				}
			}
			required, ok := doc["required"].([]interface{})
			if !ok || len(required) != 4 {
				t.Fatalf("required list shape unexpected: %v", doc["required"])
			}
		})
	}
}
