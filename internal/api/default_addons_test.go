package api

import (
	"testing"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/schema"
	"gopkg.in/yaml.v3"
)

// TestMarshalDefaultAddons_RoundTrip verifies that marshalling produces valid enveloped YAML.
func TestMarshalDefaultAddons_RoundTrip(t *testing.T) {
	addons := []string{"cert-manager", "external-dns"}
	body, err := marshalDefaultAddons(addons)
	if err != nil {
		t.Fatalf("marshalDefaultAddons failed: %v", err)
	}

	// Parse back.
	parsed, err := parseDefaultAddons(body)
	if err != nil {
		t.Fatalf("parseDefaultAddons failed: %v", err)
	}

	if len(parsed) != 2 || parsed[0] != "cert-manager" || parsed[1] != "external-dns" {
		t.Errorf("round-trip mismatch: got %v, want [cert-manager external-dns]", parsed)
	}
}

// TestParseDefaultAddons_SchemaValidation verifies that the schema validator accepts the marshalled envelope.
func TestParseDefaultAddons_SchemaValidation(t *testing.T) {
	addons := []string{"addon-a"}
	body, err := marshalDefaultAddons(addons)
	if err != nil {
		t.Fatalf("marshalDefaultAddons failed: %v", err)
	}

	// Validate.
	validator, vErr := schema.DefaultValidator()
	if vErr != nil {
		t.Skipf("validator not available: %v", vErr)
	}
	if err := validator.Validate(schema.KindDefaultAddons, body); err != nil {
		t.Errorf("schema validation failed: %v", err)
	}
}

// TestParseDefaultAddons_WrongKind rejects a non-DefaultAddons envelope.
func TestParseDefaultAddons_WrongKind(t *testing.T) {
	wrongKind := `apiVersion: sharko.dev/v1
kind: ManagedClusters
metadata:
  name: wrong
spec:
  addons: []
`
	_, err := parseDefaultAddons([]byte(wrongKind))
	if err == nil {
		t.Fatal("parseDefaultAddons should reject wrong kind")
	}
	// The error could be from schema validation OR from the kind check — both are acceptable rejections.
	// Just verify that it rejects.
}

// TestNormalizeAddonNames verifies whitespace trimming, deduplication, and empty filtering.
func TestNormalizeAddonNames(t *testing.T) {
	input := []string{"  cert-manager ", "external-dns", "", "cert-manager", "   "}
	normalized := normalizeAddonNames(input)
	if len(normalized) != 2 || normalized[0] != "cert-manager" || normalized[1] != "external-dns" {
		t.Errorf("normalizeAddonNames: got %v, want [cert-manager external-dns]", normalized)
	}
}

// TestParseDefaultAddons_EmptyFile verifies empty addons list parses correctly.
func TestParseDefaultAddons_EmptyFile(t *testing.T) {
	emptyDoc := schema.Envelope[config.DefaultAddonsSpec]{
		APIVersion: schema.APIVersion,
		Kind:       schema.KindDefaultAddons,
		Metadata:   schema.Metadata{Name: "default-addons"},
		Spec:       config.DefaultAddonsSpec{Addons: []string{}},
	}
	body, _ := yaml.Marshal(emptyDoc)

	parsed, err := parseDefaultAddons(body)
	if err != nil {
		t.Fatalf("parseDefaultAddons(empty): %v", err)
	}
	if len(parsed) != 0 {
		t.Errorf("empty file should parse to empty slice, got %v", parsed)
	}
}
