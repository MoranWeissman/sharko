package gitops

import (
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

func strPtr(s string) *string { return &s }

func TestSetClusterAssignmentAddon_BootstrapsEmptyDoc(t *testing.T) {
	out, err := SetClusterAssignmentAddon(nil, "prod-eu", "cert-manager", true, strPtr("1.12.0"), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	spec, err := models.LoadClusterAssignment(out)
	if err != nil {
		t.Fatalf("round-trip parse failed: %v", err)
	}
	if spec.Cluster != "prod-eu" {
		t.Errorf("spec.Cluster = %q, want %q", spec.Cluster, "prod-eu")
	}
	entry, ok := spec.Addons["cert-manager"]
	if !ok {
		t.Fatal("expected cert-manager entry")
	}
	if !entry.Enabled || entry.Version != "1.12.0" {
		t.Errorf("entry = %+v, want Enabled=true Version=1.12.0", entry)
	}
}

func TestSetClusterAssignmentAddon_DisableKeepsVersion(t *testing.T) {
	seeded, err := SetClusterAssignmentAddon(nil, "prod-eu", "cert-manager", true, strPtr("1.12.0"), nil)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Disable WITHOUT mentioning a version (version=nil) — the pin must survive.
	out, err := SetClusterAssignmentAddon(seeded, "prod-eu", "cert-manager", false, nil, nil)
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	spec, err := models.LoadClusterAssignment(out)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	entry := spec.Addons["cert-manager"]
	if entry.Enabled {
		t.Error("expected Enabled=false after disable")
	}
	if entry.Version != "1.12.0" {
		t.Errorf("disable must not clear the pin: Version = %q, want %q", entry.Version, "1.12.0")
	}
}

func TestSetClusterAssignmentAddon_ExplicitEmptyVersionClearsPin(t *testing.T) {
	seeded, err := SetClusterAssignmentAddon(nil, "prod-eu", "cert-manager", true, strPtr("1.12.0"), nil)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	// design doc §6's upgrade PR: removing the version line makes the
	// cluster follow the catalog again — modelled as an explicit empty
	// string, distinct from nil ("don't touch it").
	out, err := SetClusterAssignmentAddon(seeded, "prod-eu", "cert-manager", true, strPtr(""), nil)
	if err != nil {
		t.Fatalf("clear pin: %v", err)
	}
	spec, err := models.LoadClusterAssignment(out)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if v := spec.Addons["cert-manager"].Version; v != "" {
		t.Errorf("expected the pin to be cleared, got %q", v)
	}
}

func TestSetClusterAssignmentAddon_PreservesOtherAddons(t *testing.T) {
	seeded, err := SetClusterAssignmentAddon(nil, "prod-eu", "cert-manager", true, strPtr("1.12.0"), nil)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	out, err := SetClusterAssignmentAddon(seeded, "prod-eu", "metrics-server", true, nil, nil)
	if err != nil {
		t.Fatalf("add second addon: %v", err)
	}
	spec, err := models.LoadClusterAssignment(out)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(spec.Addons) != 2 {
		t.Fatalf("expected 2 addons, got %d: %+v", len(spec.Addons), spec.Addons)
	}
	if !spec.Addons["cert-manager"].Enabled || spec.Addons["cert-manager"].Version != "1.12.0" {
		t.Errorf("cert-manager entry mutated unexpectedly: %+v", spec.Addons["cert-manager"])
	}
}

func TestSetClusterAssignmentAddon_SettingsReplaceWholesale(t *testing.T) {
	firstSettings := &models.ClusterAssignmentAddonSettings{
		SyncOptions: []string{"ServerSideApply=true"},
	}
	seeded, err := SetClusterAssignmentAddon(nil, "prod-eu", "cert-manager", true, nil, firstSettings)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	// settings=nil on the next call must leave the existing block alone.
	out, err := SetClusterAssignmentAddon(seeded, "prod-eu", "cert-manager", true, nil, nil)
	if err != nil {
		t.Fatalf("no-op settings update: %v", err)
	}
	spec, err := models.LoadClusterAssignment(out)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if spec.Addons["cert-manager"].Settings == nil || len(spec.Addons["cert-manager"].Settings.SyncOptions) != 1 {
		t.Errorf("settings=nil should not have cleared the existing block: %+v", spec.Addons["cert-manager"].Settings)
	}

	// A non-nil settings replaces wholesale.
	replaced := &models.ClusterAssignmentAddonSettings{Namespace: "custom-ns"}
	out2, err := SetClusterAssignmentAddon(out, "prod-eu", "cert-manager", true, nil, replaced)
	if err != nil {
		t.Fatalf("replace settings: %v", err)
	}
	spec2, err := models.LoadClusterAssignment(out2)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := spec2.Addons["cert-manager"].Settings
	if got == nil || got.Namespace != "custom-ns" || len(got.SyncOptions) != 0 {
		t.Errorf("expected settings replaced wholesale to %+v, got %+v", replaced, got)
	}
}

func TestRemoveClusterAssignmentAddon(t *testing.T) {
	seeded, err := SetClusterAssignmentAddon(nil, "prod-eu", "cert-manager", true, strPtr("1.12.0"), nil)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	out, err := RemoveClusterAssignmentAddon(seeded, "prod-eu", "cert-manager")
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	spec, err := models.LoadClusterAssignment(out)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, ok := spec.Addons["cert-manager"]; ok {
		t.Error("expected cert-manager entry to be gone entirely")
	}
}

func TestRemoveClusterAssignmentAddon_NotFound(t *testing.T) {
	seeded, err := SetClusterAssignmentAddon(nil, "prod-eu", "cert-manager", true, nil, nil)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := RemoveClusterAssignmentAddon(seeded, "prod-eu", "does-not-exist"); err == nil {
		t.Error("expected an error removing a non-existent addon")
	}
}
