// Package gitops provides envelope-aware YAML mutation utilities for the
// Sharko configuration files (addons-catalog.yaml, managed-clusters.yaml).
//
// Cluster-side mutators live in yaml_mutator_cluster.go and route
// through models.LoadManagedClusters + SaveManagedClusters. Catalog-side
// mutators live in yaml_mutator_catalog.go and route through
// config.NewParser().ParseAddonsCatalog + config.MarshalAddonCatalog.
// Both use the envelope reader/writer pair so the line-level scanner
// pitfalls (indent-dependent parsing that silently breaks against
// `spec.clusters:` / `spec.applicationsets:`) are avoided.
//
// This file only carries the public input struct shared by the catalog
// mutators (CatalogEntryInput).
package gitops

// CatalogEntryInput holds the fields for a new addons-catalog.yaml entry.
// Namespace, SyncWave and DependsOn are optional (zero value means not
// set). The orchestrator builds this struct from AddAddonRequest and
// passes it to AddCatalogEntry.
//
// Path field is intentionally absent: the typed
// models.AddonCatalogEntry has no Path field, and the envelope writer
// would have no canonical place to emit it. Git-sourced addons carry
// their path via AdditionalSources in the typed model.
type CatalogEntryInput struct {
	Name      string
	RepoURL   string
	Chart     string
	Version   string
	Namespace string   // optional
	SyncWave  int      // optional, 0 = not set
	DependsOn []string // optional, addon names this entry depends on
}
