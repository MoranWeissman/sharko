package config

import (
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/schema"
)

// V2-cleanup-22, Part 1 — validate-before-commit for MarshalAddonCatalog.
//
// MarshalAddonCatalog is the single choke point every addon-catalog writer
// funnels through. After V2-cleanup-22 it validates the marshalled output
// against the embedded addons-catalog schema BEFORE returning.

// TestMarshalAddonCatalog_OutputPassesOwnValidator is the round-trip guard:
// a normal entry set marshals to output that passes the SAME validator the
// gate uses.
func TestMarshalAddonCatalog_OutputPassesOwnValidator(t *testing.T) {
	t.Parallel()

	entries := []models.AddonCatalogEntry{
		{
			Name:    "cert-manager",
			Chart:   "cert-manager",
			RepoURL: "https://charts.jetstack.io",
			Version: "1.13.0",
		},
		{
			Name:    "metrics-server",
			Chart:   "metrics-server",
			RepoURL: "https://example.com",
			Version: "3.11.0",
		},
	}

	out, err := MarshalAddonCatalog("addon-catalog", entries)
	if err != nil {
		t.Fatalf("MarshalAddonCatalog returned error on a valid entry set (gate false-positive): %v", err)
	}

	validator, vErr := schema.DefaultValidator()
	if vErr != nil {
		t.Fatalf("DefaultValidator: %v", vErr)
	}
	if err := validator.Validate(schema.KindAddonCatalog, out); err != nil {
		t.Fatalf("MarshalAddonCatalog output failed its own validator: %v", err)
	}
}

// TestMarshalAddonCatalog_EmptyPassesValidator covers the bootstrap/empty
// case — an empty catalog must serialize to `applicationsets: []` and pass
// the gate (regression guard for the nil-slice normalisation).
func TestMarshalAddonCatalog_EmptyPassesValidator(t *testing.T) {
	t.Parallel()

	out, err := MarshalAddonCatalog("", nil)
	if err != nil {
		t.Fatalf("MarshalAddonCatalog(nil) returned error: %v", err)
	}
	validator, vErr := schema.DefaultValidator()
	if vErr != nil {
		t.Fatalf("DefaultValidator: %v", vErr)
	}
	if err := validator.Validate(schema.KindAddonCatalog, out); err != nil {
		t.Fatalf("empty MarshalAddonCatalog output failed its own validator: %v", err)
	}
}

// TestMarshalAddonCatalog_GateRejectsInvalidBody proves the gate FIRES: the
// exact validator MarshalAddonCatalog runs internally rejects an addons-
// catalog body with a wrong-typed field (syncWave as a string instead of an
// integer) — the same Validate call the writer makes before commit.
func TestMarshalAddonCatalog_GateRejectsInvalidBody(t *testing.T) {
	t.Parallel()

	bad := []byte(`apiVersion: sharko.io/v1
kind: AddonCatalog
metadata:
  name: addon-catalog
spec:
  applicationsets:
    - name: cert-manager
      syncWave: "not-an-int"
`)

	validator, vErr := schema.DefaultValidator()
	if vErr != nil {
		t.Fatalf("DefaultValidator: %v", vErr)
	}
	if err := validator.Validate(schema.KindAddonCatalog, bad); err == nil {
		t.Fatal("validate-before-commit gate did NOT reject a wrong-typed catalog body")
	}
}
