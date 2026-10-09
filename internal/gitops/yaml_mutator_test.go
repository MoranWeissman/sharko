package gitops

// V125-1-8.3 / closes #257: the cluster-side line-level mutator tests
// (TestEnableAddonLabel_*, TestDisableAddonLabel_*, TestAddClusterEntry_*,
// TestPreserveComments, TestPreserveOtherClusters) lived in this file
// before the envelope-aware rewrite. They asserted byte-level outputs
// (comment preservation, blank-line separators, untouched-neighbour
// formatting) that the new parse-mutate-marshal mutators in
// yaml_mutator_cluster.go intentionally do not preserve — the
// V125-1-9 SaveManagedClusters writer emits canonical yaml.v3 formatting.
//
// The replacement coverage lives in yaml_mutator_envelope_test.go,
// which pins the new contract: envelope/schema-header preservation,
// round-trip parse → mutate → re-parse equivalence, idempotent
// AddClusterEntry on duplicate name, error-on-not-found for the rest.
//
// ZG1-A.264 / closes #264: the catalog-side line-level mutator tests
// (TestUpdateCatalogVersion_*, TestAddCatalogEntry_*,
// TestRemoveCatalogEntry_*, TestUpdateCatalogEntry_*) that previously
// asserted line-level byte outputs were retired alongside the catalog
// mutators themselves. The replacement coverage lives in
// yaml_mutator_catalog_test.go and asserts envelope-aware round-trip
// equivalence via the V125-1-9.2 ParseAddonsCatalog +
// MarshalAddonCatalog reader/writer. The stale `containsInCluster`
// helper that lived in this file was removed in the same change — it
// had no remaining call sites.
//
// This file is intentionally empty (modulo the file-header context
// comment) so future maintainers can find the audit trail above when
// they ask "why did the line-level catalog tests disappear?".
