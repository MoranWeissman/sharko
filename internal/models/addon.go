package models

import (
	"github.com/invopop/jsonschema"

	"github.com/MoranWeissman/sharko/internal/credsafe"
)

// AddonSecretRef describes a Kubernetes Secret that an addon needs on remote clusters.
// Keys maps the secret data key (as it will appear in the K8s Secret) to the
// provider path that holds the actual value (e.g. "secrets/datadog/api-key").
//
// This is the PUSH definition: the three things Sharko needs before it can
// put a credential on a cluster by itself. It is the shape a v3 catalog's
// `secrets:` block carried, and — since the v4 wave 2.5 secrets fix — the
// shape a v4 catalog entry carries too, under
// config.AddonSecretRequirement.Push. One type, one set of yaml keys, one
// push implementation in internal/secrets for both repo layouts.
type AddonSecretRef struct {
	SecretName string            `json:"secretName" yaml:"secretName"`
	Namespace  string            `json:"namespace" yaml:"namespace"`
	Keys       map[string]string `json:"keys" yaml:"keys"`
}

// MissingFields names, in a fixed order, the parts of a push definition
// that are blank. An empty result means Sharko can act on it.
//
// The JSON Schema already requires all three on any file that goes through
// validate-on-read, so in practice this fires only where the schema cannot:
// a definition built in memory, or a read that happened with no compiled
// validator. It exists so those cases produce a sentence naming the missing
// key instead of a confusing Kubernetes API error at push time.
func (r AddonSecretRef) MissingFields() []string {
	var missing []string
	if r.SecretName == "" {
		missing = append(missing, "secretName")
	}
	if r.Namespace == "" {
		missing = append(missing, "namespace")
	}
	if len(r.Keys) == 0 {
		missing = append(missing, "keys")
	}
	return missing
}

// Complete reports whether this push definition has everything Sharko needs
// to create the Secret on a cluster.
func (r AddonSecretRef) Complete() bool {
	return len(r.MissingFields()) == 0
}

// AddonSource represents an additional Helm chart or manifest source for an addon.
type AddonSource struct {
	RepoURL    string            `json:"repoURL,omitempty" yaml:"repoURL,omitempty"`
	Path       string            `json:"path,omitempty" yaml:"path,omitempty"`
	Chart      string            `json:"chart,omitempty" yaml:"chart,omitempty"`
	Version    string            `json:"version,omitempty" yaml:"version,omitempty"`
	Parameters map[string]string `json:"parameters,omitempty" yaml:"parameters,omitempty"`
	ValueFiles []string          `json:"valueFiles,omitempty" yaml:"valueFiles,omitempty"`
}

// AddonCatalogEntry represents an addon definition from addons-catalog.yaml.
type AddonCatalogEntry struct {
	// Basic (required)
	Name    string `json:"name" yaml:"name"`
	RepoURL string `json:"repoURL" yaml:"repoURL"`
	Chart   string `json:"chart" yaml:"chart"`
	Version string `json:"version" yaml:"version"`

	// Basic (optional)
	Namespace string `json:"namespace,omitempty" yaml:"namespace,omitempty"`

	// Advanced — deployment behavior
	SelfHeal    *bool    `json:"selfHeal,omitempty" yaml:"selfHeal,omitempty"`
	SyncOptions []string `json:"syncOptions,omitempty" yaml:"syncOptions,omitempty"`

	// Advanced — additional sources
	AdditionalSources []AddonSource `json:"additionalSources,omitempty" yaml:"additionalSources,omitempty"`

	// Advanced — ArgoCD behavior
	IgnoreDifferences []map[string]interface{} `json:"ignoreDifferences,omitempty" yaml:"ignoreDifferences,omitempty"`

	// Advanced — extra Helm configuration
	ExtraHelmValues map[string]string `json:"extraHelmValues,omitempty" yaml:"extraHelmValues,omitempty"`

	// Secret requirements — Sharko creates these K8s Secrets on remote clusters
	Secrets []AddonSecretRef `json:"secrets,omitempty" yaml:"secrets,omitempty"`
}

// JSONSchemaExtend relaxes the generated addons-catalog.v1.json schema so
// entries tolerate unknown keys (invopop/jsonschema defaults every struct
// to additionalProperties: false, which internal/config.Parser's
// validate-on-read gate would otherwise enforce strictly).
//
// addons-catalog.yaml is a living, hand-editable config file: operators
// author entries directly, and Sharko itself has removed fields from this
// struct before (V2-cleanup-67.1 dropped syncWave + dependsOn — dead,
// since Sharko's one-ApplicationSet-per-addon model can never use them to
// order one addon against another). Without this relaxation, an existing
// catalog file that still carries a since-removed key would fail the
// read-time schema gate and Sharko would be unable to parse its own
// catalog after upgrading — exactly the silent-breakage this method
// prevents. Go's yaml.Unmarshal already ignores unknown keys by default;
// this keeps the JSON Schema gate consistent with that behavior for this
// one type. ManagedClusters and other envelope specs are unaffected —
// this override is scoped to AddonCatalogEntry only.
func (AddonCatalogEntry) JSONSchemaExtend(s *jsonschema.Schema) {
	s.AdditionalProperties = jsonschema.TrueSchema
}

// AddonDeploymentInfo holds information about an addon's deployment in a specific cluster.
type AddonDeploymentInfo struct {
	ClusterName        string `json:"cluster_name"`
	ClusterEnvironment string `json:"cluster_environment,omitempty"`
	Enabled            bool   `json:"enabled"`
	ConfiguredVersion  string `json:"configured_version,omitempty"`
	DeployedVersion    string `json:"deployed_version,omitempty"`
	Namespace          string `json:"namespace,omitempty"`

	// ArgoCD status
	SyncStatus      string `json:"sync_status,omitempty"`
	HealthStatus    string `json:"health_status,omitempty"`
	ApplicationName string `json:"application_name,omitempty"`

	Status string `json:"status"`
}

// AddonCatalogItem is the catalog view of an addon with stats across clusters.
type AddonCatalogItem struct {
	AddonName string `json:"addon_name"`
	Chart     string `json:"chart"`
	RepoURL   string `json:"repo_url"`
	Namespace string `json:"namespace,omitempty"`
	Version   string `json:"version"`

	// Stats
	TotalClusters        int `json:"total_clusters"`
	EnabledClusters      int `json:"enabled_clusters"`
	HealthyApplications  int `json:"healthy_applications"`
	DegradedApplications int `json:"degraded_applications"`
	MissingApplications  int `json:"missing_applications"`

	// Paired counts that drive the tile-level "Running on N/M clusters"
	// badge. N = clusters where the ArgoCD Application for this addon
	// is Synced + Healthy. M = clusters where the addon is labelled
	// enabled in managed-clusters.yaml. Kept separate from the existing
	// HealthyApplications / EnabledClusters fields so the tile copy can
	// distinguish "addon is in catalog" from "addon is actually
	// running" without changing the historical stat semantics other
	// tiles depend on.
	DeployedClusterCount    int `json:"deployed_cluster_count"`
	TotalTargetClusterCount int `json:"total_target_cluster_count"`

	// Per-cluster details
	Applications []AddonDeploymentInfo `json:"applications"`
}

// AddonCatalogResponse is the API response for the addon catalog.
type AddonCatalogResponse struct {
	Addons          []AddonCatalogItem `json:"addons"`
	TotalAddons     int                `json:"total_addons"`
	TotalClusters   int                `json:"total_clusters"`
	AddonsOnlyInGit int                `json:"addons_only_in_git"`
}

// ApplicationSetCondition holds a single condition from an ArgoCD ApplicationSet status.
type ApplicationSetCondition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// ApplicationSetStatusInfo holds status information for an ArgoCD ApplicationSet.
type ApplicationSetStatusInfo struct {
	Name          string                    `json:"name"`
	Conditions    []ApplicationSetCondition `json:"conditions"`
	GeneratedApps int                       `json:"generated_apps"`
}

// AddonDetailResponse is the API response for a single addon's details.
type AddonDetailResponse struct {
	Addon          AddonCatalogItem          `json:"addon"`
	ApplicationSet *ApplicationSetStatusInfo `json:"application_set,omitempty"`
}

// AddonValuesResponse is the API response for raw addon global values YAML.
type AddonValuesResponse struct {
	AddonName  string `json:"addon_name"`
	ValuesYAML string `json:"values_yaml"`
}

// AddonValuesSchemaResponse is the API response for the values editor — the
// current global values YAML plus an optional parsed JSON Schema fetched
// from the chart's `values.schema.json`. The schema may be nil when the
// chart does not publish one (most charts do not); the UI then falls back
// to plain YAML mode without autocomplete.
//
// ValuesVersionMismatch, when non-nil, signals that the chart version
// pinned in `addons-catalog.yaml` is ahead of the version stamped in
// the values file's smart-values header — the UI renders a refresh
// banner. The field is `omitempty` so legacy files without a
// `# sharko: managed=true` header keep working without a banner.
type AddonValuesSchemaResponse struct {
	AddonName             string                 `json:"addon_name"`
	CurrentValues         string                 `json:"current_values"`
	Schema                map[string]interface{} `json:"schema,omitempty"`
	ValuesVersionMismatch *ValuesVersionMismatch `json:"values_version_mismatch,omitempty"`

	// Header-derived AI annotation state. The UI uses these to gate the
	// "AI not configured" banner (rendered when AIAnnotated=false AND
	// Sharko's AI provider is `none`) and the per-addon opt-out toggle
	// (mirrors AIOptOut). Both default-false for legacy files without
	// a smart-values header.
	AIAnnotated bool `json:"ai_annotated"`
	AIOptOut    bool `json:"ai_opt_out"`

	// LegacyWrapDetected: true when the current values file is wrapped
	// under a legacy `<addonName>:` (or `<chartName>:`) root key —
	// Helm receives this file directly via `valueFiles:` in the
	// ApplicationSet template and silently ignores everything nested
	// under that root. The Values tab uses this to render a migration
	// banner with a "Migrate this file" button.
	LegacyWrapDetected bool `json:"legacy_wrap_detected,omitempty"`
}

// ValuesVersionMismatch is set when the catalog version differs from the
// values-file header version. Both fields are non-empty strings on
// instantiation; the UI compares them and surfaces the refresh banner.
type ValuesVersionMismatch struct {
	CatalogVersion string `json:"catalog_version"`
	ValuesVersion  string `json:"values_version"`
}

// ClusterAddonValuesResponse is the API response for the per-cluster
// overrides editor — the YAML for the addon's section in the cluster's
// overrides file, plus the same optional schema. CurrentOverrides is the
// empty string when no overrides exist for this addon yet.
type ClusterAddonValuesResponse struct {
	ClusterName      string                 `json:"cluster_name"`
	AddonName        string                 `json:"addon_name"`
	CurrentOverrides string                 `json:"current_overrides"`
	Schema           map[string]interface{} `json:"schema,omitempty"`
}

// VersionMatrixCell holds version and health info for one addon on one cluster.
type VersionMatrixCell struct {
	Version          string `json:"version"`            // Deployed or configured version
	Health           string `json:"health"`             // Healthy, Degraded, Progressing, Unknown, missing, not_enabled
	DriftFromCatalog bool   `json:"drift_from_catalog"` // True if version differs from catalog default
}

// VersionMatrixRow represents one addon across all clusters.
type VersionMatrixRow struct {
	AddonName      string                       `json:"addon_name"`
	CatalogVersion string                       `json:"catalog_version"`
	Chart          string                       `json:"chart"`
	Cells          map[string]VersionMatrixCell `json:"cells"` // key = cluster name
	// NewestAvailable is the highest chart version Sharko has last seen in
	// the addon's Helm repo, per internal/catalog.FreshnessScheduler's
	// background snapshot (v4 Wave 2 Epic 7 Story 7.1) — never a live
	// per-request Helm fetch, so loading the matrix stays cheap regardless
	// of addon count. Empty when no snapshot exists yet (fresh install) or
	// the last check failed.
	NewestAvailable string `json:"newest_available,omitempty"`
	// LastChecked is when the freshness scheduler last checked this
	// addon's chart versions (RFC3339). Empty when no snapshot exists yet.
	LastChecked string `json:"last_checked,omitempty"`
}

// VersionMatrixResponse is the API response.
type VersionMatrixResponse struct {
	Clusters []string           `json:"clusters"` // Column headers (cluster names)
	Addons   []VersionMatrixRow `json:"addons"`   // Rows
}

// --- B11: the response copy of a catalog entry ------------------------------
//
// AddonCatalogEntry above carries BOTH json and yaml tags, because it is the
// shape of the file on disk: Sharko reads addons-catalog.yaml into it, mutates
// one field, and writes the whole thing back out (internal/gitops
// AddCatalogEntry / UpdateCatalogEntry / UpdateCatalogVersion, and
// internal/orchestrator addon_configure.go, all via
// config.ParseAddonsCatalog + config.MarshalAddonCatalog).
//
// RepoURL on that struct is routinely written with the credential inside it:
//
//	https://x-access-token:<token>@github.example/org/charts
//
// and GET /api/v1/addons/list used to marshal the parsed entries straight onto
// the response, token and all.
//
// Stripping the credential on the struct itself would have been a DATA-LOSS
// bug, not a fix: the very next catalog write would have marshalled the
// stripped value back over the operator's file and thrown their password away.
// The same value is also what internal/helm dials to fetch the chart index, so
// a stripped copy in the parse path would break every version lookup too.
//
// So the response gets its OWN type. AddonCatalogEntryView has json tags only —
// nothing marshals it to yaml, nothing writes it to git, and the compiler is
// what keeps the two apart. This is the same asymmetry AddonCatalogItem
// already has against this struct, made explicit for the raw-entry endpoint.
//
// The json tags are byte-for-byte the ones AddonCatalogEntry emitted, so the
// wire shape of /addons/list does not move — only the credential leaves.

// AddonSourceView is the response copy of AddonSource. Same reason, same
// shape: an additional source carries its own repository address, and
// AddonSource is a json+yaml on-disk type that round-trips through the
// catalog file.
type AddonSourceView struct {
	RepoURL    string            `json:"repoURL,omitempty"`
	Path       string            `json:"path,omitempty"`
	Chart      string            `json:"chart,omitempty"`
	Version    string            `json:"version,omitempty"`
	Parameters map[string]string `json:"parameters,omitempty"`
	ValueFiles []string          `json:"valueFiles,omitempty"`
}

// AddonCatalogEntryView is the read-only copy of AddonCatalogEntry that goes
// out on a response. Every field is carried across unchanged except the two
// repository addresses, which go through internal/credsafe.
type AddonCatalogEntryView struct {
	Name      string `json:"name"`
	RepoURL   string `json:"repoURL"`
	Chart     string `json:"chart"`
	Version   string `json:"version"`
	Namespace string `json:"namespace,omitempty"`

	SelfHeal    *bool    `json:"selfHeal,omitempty"`
	SyncOptions []string `json:"syncOptions,omitempty"`

	AdditionalSources []AddonSourceView `json:"additionalSources,omitempty"`

	IgnoreDifferences []map[string]interface{} `json:"ignoreDifferences,omitempty"`

	ExtraHelmValues map[string]string `json:"extraHelmValues,omitempty"`

	Secrets []AddonSecretRef `json:"secrets,omitempty"`
}

// NewAddonCatalogEntryView builds the response copy of one catalog entry.
//
// It never touches the entry it was handed: the caller keeps the raw value for
// everything that has to fetch from it or write it back.
func NewAddonCatalogEntryView(e AddonCatalogEntry) AddonCatalogEntryView {
	v := AddonCatalogEntryView{
		Name:              e.Name,
		RepoURL:           credsafe.SafeRepoURL(e.RepoURL),
		Chart:             e.Chart,
		Version:           e.Version,
		Namespace:         e.Namespace,
		SelfHeal:          e.SelfHeal,
		SyncOptions:       append([]string(nil), e.SyncOptions...),
		IgnoreDifferences: append([]map[string]interface{}(nil), e.IgnoreDifferences...),
		Secrets:           append([]AddonSecretRef(nil), e.Secrets...),
	}
	if len(e.ExtraHelmValues) > 0 {
		v.ExtraHelmValues = make(map[string]string, len(e.ExtraHelmValues))
		for k, val := range e.ExtraHelmValues {
			v.ExtraHelmValues[k] = val
		}
	}
	for _, s := range e.AdditionalSources {
		v.AdditionalSources = append(v.AdditionalSources, NewAddonSourceView(s))
	}
	return v
}

// NewAddonSourceView builds the response copy of one additional source.
func NewAddonSourceView(s AddonSource) AddonSourceView {
	out := AddonSourceView{
		RepoURL:    credsafe.SafeRepoURL(s.RepoURL),
		Path:       s.Path,
		Chart:      s.Chart,
		Version:    s.Version,
		ValueFiles: append([]string(nil), s.ValueFiles...),
	}
	if len(s.Parameters) > 0 {
		out.Parameters = make(map[string]string, len(s.Parameters))
		for k, v := range s.Parameters {
			out.Parameters[k] = v
		}
	}
	return out
}

// NewAddonCatalogEntryViews is the slice form, for a list endpoint.
func NewAddonCatalogEntryViews(entries []AddonCatalogEntry) []AddonCatalogEntryView {
	out := make([]AddonCatalogEntryView, 0, len(entries))
	for _, e := range entries {
		out = append(out, NewAddonCatalogEntryView(e))
	}
	return out
}
