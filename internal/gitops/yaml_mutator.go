// Package gitops provides envelope-aware YAML mutation utilities for the
// Sharko configuration files (addon-catalog.yaml, managed-clusters.yaml).
//
// V125-1-8.3 / closes #257: the cluster-side mutators that used to live
// here (EnableAddonLabel, DisableAddonLabel, AddClusterEntry,
// RemoveClusterEntry, UpdateClusterSecretPath, plus the setAddonLabel
// shared helper) were replaced by V125-1-9 envelope-aware versions in
// yaml_mutator_cluster.go. They route through models.LoadManagedClusters
// + SaveManagedClusters instead of the byte-level line scanner that
// silently broke against the apiVersion/kind/spec envelope (the indent
// assumption — cluster entries at indent 2 — became wrong once the
// envelope wrapped them at indent 4 under `spec.clusters:`).
//
// ZG1-A.264 / closes #264: the catalog-side mutators (AddCatalogEntry,
// RemoveCatalogEntry, UpdateCatalogEntry, UpdateCatalogVersion) were
// retired here too — they now live in yaml_mutator_catalog.go and
// route through config.NewParser().ParseAddonsCatalog +
// config.MarshalAddonCatalog (the V125-1-9.2 envelope reader/writer).
// Same brittleness class as the cluster-side bug: the legacy line-level
// scanners assumed bare YAML / list items at indent 2, which broke
// silently against the V125-1-9.2 addon-catalog envelope (list items
// at indent 4 under `spec.applicationsets:`).
//
// This file now only carries the public input struct shared by the
// catalog mutators (CatalogEntryInput) so callers in
// internal/orchestrator/addon.go keep compiling without an import shuffle.
package gitops

// CatalogEntryInput holds the fields for a new addon-catalog.yaml entry.
// Namespace, SyncWave and DependsOn are optional (zero value means not
// set). The orchestrator builds this struct from AddAddonRequest and
// passes it to AddCatalogEntry.
//
// Path field deliberately removed in ZG1-A.264: no production caller set
// it, the typed models.AddonCatalogEntry has no Path field, and the
// V125-1-9.2 envelope writer would have had no canonical place to emit
// it. Git-sourced addons carry their path via AdditionalSources in the
// typed model.
type CatalogEntryInput struct {
	Name      string
	RepoURL   string
	Chart     string
	Version   string
	Namespace string   // optional
	SyncWave  int      // optional, 0 = not set
	DependsOn []string // optional, addon names this entry depends on
}
