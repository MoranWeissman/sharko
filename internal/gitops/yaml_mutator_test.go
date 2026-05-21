package gitops

// V125-1-8.3 / closes #257: the cluster-side line-level mutator tests
// (TestEnableAddonLabel_*, TestDisableAddonLabel_*, TestAddClusterEntry_*,
// TestPreserveComments, TestPreserveOtherClusters) lived in this file
// before the envelope-aware rewrite. They asserted byte-level outputs
// (comment preservation, blank-line separators, untouched-neighbour
// formatting) that the new parse-mutate-marshal mutators in
// yaml_mutator_cluster.go intentionally do not preserve — the
// V125-1-9 SaveManagedClusters writer emits canonical yaml.v3
// formatting.
//
// The replacement coverage lives in yaml_mutator_envelope_test.go,
// which pins the new contract: envelope/schema-header preservation,
// round-trip parse → mutate → re-parse equivalence, idempotent
// AddClusterEntry on duplicate name, error-on-not-found for the rest.
//
// Catalog-side tests (UpdateCatalogVersion, UpdateCatalogVersion_*)
// remain in yaml_mutator_catalog_test.go alongside the catalog
// mutators that still use the legacy line-level path — no V125-1-9
// envelope writer exists for addons-catalog.yaml yet.

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// addons-catalog.yaml fixtures — kept here because UpdateCatalogVersion
// (the function under test) still lives in yaml_mutator.go; the
// envelope rewrite did NOT touch the catalog mutators (no V125-1-9
// envelope writer for addons-catalog.yaml yet).
// ---------------------------------------------------------------------------

const addonsCatalogYAML = `# Addons catalog
applicationsets:
  - name: datadog
    repoURL: https://helm.datadoghq.com
    chart: datadog
    version: 3.160.1
  - name: keda
    repoURL: https://kedacore.github.io/charts
    chart: keda
    version: 2.14.2
`

func TestUpdateCatalogVersion_Existing(t *testing.T) {
	out, err := UpdateCatalogVersion([]byte(addonsCatalogYAML), "datadog", "3.170.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "    version: 3.170.0") {
		t.Errorf("expected version 3.170.0 for datadog:\n%s", s)
	}
	// keda version untouched
	if !strings.Contains(s, "    version: 2.14.2") {
		t.Errorf("keda version was modified:\n%s", s)
	}
	// comment preserved (catalog mutators still preserve comments —
	// only cluster mutators changed in V125-1-8.3)
	if !strings.Contains(s, "# Addons catalog") {
		t.Errorf("comment lost")
	}
}

func TestUpdateCatalogVersion_NotFound(t *testing.T) {
	_, err := UpdateCatalogVersion([]byte(addonsCatalogYAML), "nonexistent", "1.0.0")
	if err == nil {
		t.Fatal("expected error for nonexistent addon")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error should mention addon name: %v", err)
	}
}

func TestUpdateCatalogVersion_PreservesComments(t *testing.T) {
	out, err := UpdateCatalogVersion([]byte(addonsCatalogYAML), "keda", "2.15.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "# Addons catalog") {
		t.Errorf("comment lost")
	}
	// datadog untouched
	if !strings.Contains(s, "    version: 3.160.1") {
		t.Errorf("datadog version was modified")
	}
}

// ---------------------------------------------------------------------------
// helper — kept here because the catalog tests in
// yaml_mutator_catalog_test.go also use it and adding a duplicate would
// trigger a "redeclared" build error.
// ---------------------------------------------------------------------------

// containsInCluster checks that a given line (trimmed) appears between
// "- name: <cluster>" and the next "- name:" block. Used by the
// remaining catalog tests; the cluster-mutator tests that previously
// depended on this helper were removed in V125-1-8.3 (see file-header
// comment).
func containsInCluster(yaml, clusterName, needle string) bool {
	lines := strings.Split(yaml, "\n")
	inCluster := false
	for _, line := range lines {
		if strings.Contains(line, "- name: "+clusterName) {
			inCluster = true
			continue
		}
		if inCluster && strings.Contains(line, "- name:") {
			return false
		}
		if inCluster && strings.Contains(strings.TrimSpace(line), needle) {
			return true
		}
	}
	return false
}
