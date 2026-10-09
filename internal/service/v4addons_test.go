package service

import (
	"testing"

	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/models"
)

// TestV4ClusterLabels_EnabledAddonGetsEnabledLabel locks down the
// synthesis this fix depends on: an enabled v4 cluster-addons entry must
// produce the same `<addon>: enabled` label a v3 managed-clusters.yaml
// entry would carry, because config.Parser.GetEnabledAddons and the UI's
// addon-count logic both read labels, never the v4 spec directly.
func TestV4ClusterLabels_EnabledAddonGetsEnabledLabel(t *testing.T) {
	spec := models.ClusterAddonsSpec{
		Cluster: "prod-eu",
		Addons: map[string]models.ClusterAddonsAddon{
			"cert-manager": {Enabled: true},
			"external-dns": {Enabled: false},
		},
	}

	labels := v4ClusterLabels(spec)

	if labels["cert-manager"] != "enabled" {
		t.Errorf(`labels["cert-manager"] = %q, want "enabled"`, labels["cert-manager"])
	}
	if labels["external-dns"] != "disabled" {
		t.Errorf(`labels["external-dns"] = %q, want "disabled"`, labels["external-dns"])
	}
	if _, ok := labels["cert-manager-version"]; ok {
		t.Error("no version override was set — cert-manager-version label should be absent")
	}
}

// TestV4ClusterLabels_VersionOverrideBecomesVersionLabel locks down the
// version-pin half of the same synthesis: a per-cluster version pin in
// cluster-addons/<name>.yaml must become the `<addon>-version` label that
// config.Parser.GetEnabledAddons already knows how to read.
func TestV4ClusterLabels_VersionOverrideBecomesVersionLabel(t *testing.T) {
	spec := models.ClusterAddonsSpec{
		Cluster: "prod-eu",
		Addons: map[string]models.ClusterAddonsAddon{
			"cert-manager": {Enabled: true, Version: "1.12.0"},
		},
	}

	labels := v4ClusterLabels(spec)

	if labels["cert-manager"] != "enabled" {
		t.Errorf(`labels["cert-manager"] = %q, want "enabled"`, labels["cert-manager"])
	}
	if labels["cert-manager-version"] != "1.12.0" {
		t.Errorf(`labels["cert-manager-version"] = %q, want "1.12.0"`, labels["cert-manager-version"])
	}
}

// TestV4CatalogEntries_FlattensAndSorts checks the shape conversion from a
// merged v4 catalog view into the v3 []models.AddonCatalogEntry shape that
// config.Parser.GetEnabledAddons consumes, and that output order is
// deterministic (sorted by name) regardless of map iteration order.
func TestV4CatalogEntries_FlattensAndSorts(t *testing.T) {
	merged := map[string]catalog.CatalogAddon{
		"external-dns": {
			Name:    "external-dns",
			RepoURL: "https://kubernetes-sigs.github.io/external-dns",
			Chart:   "external-dns",
			Version: "1.14.0",
		},
		"cert-manager": {
			Name:      "cert-manager",
			RepoURL:   "https://charts.jetstack.io",
			Chart:     "cert-manager",
			Version:   "1.14.5",
			Namespace: "cert-manager",
		},
	}

	entries := v4CatalogEntries(merged)

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d: %+v", len(entries), entries)
	}
	if entries[0].Name != "cert-manager" || entries[1].Name != "external-dns" {
		t.Errorf("expected sorted [cert-manager external-dns], got [%s %s]", entries[0].Name, entries[1].Name)
	}
	if entries[0].RepoURL != "https://charts.jetstack.io" || entries[0].Chart != "cert-manager" || entries[0].Version != "1.14.5" || entries[0].Namespace != "cert-manager" {
		t.Errorf("cert-manager entry fields not carried through: %+v", entries[0])
	}
}
