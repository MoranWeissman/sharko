package models

import (
	"bytes"
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/MoranWeissman/sharko/internal/schema"
)

// ManagedClustersSchemaHeader is the yaml-language-server header line written
// as the first line of every Sharko-emitted managed-clusters.yaml file. The
// URL is the V125-1-9 locked schema URL (see
// docs/design/2026-05-12-v125-architectural-todos.md §4 and the V125-1-9
// epic's "Schema URL hosting" locked OQ for the two-track redirect plan).
const ManagedClustersSchemaHeader = "# yaml-language-server: $schema=https://sharko.io/schemas/managed-clusters.v1.json"

// ManagedClusterEntry is one row in the spec.clusters array of a
// managed-clusters.yaml document. Field semantics:
//
//   - Name: required cluster identifier; matches the ArgoCD cluster Secret
//     name (cluster-<Name>) the V125-1-8 reconciler will manage.
//   - SecretPath: optional explicit path to the cluster's credentials in the
//     credentials provider (e.g. Vault path, AWS Secrets Manager path). When
//     empty, the credentials provider derives a default path from Name.
//   - Region: optional cloud region label, surfaced in the UI / observability.
//   - Labels: optional addon-enablement map. Modelled as interface{} to
//     match the legacy parser's tolerance: the on-disk shape is either a
//     map (`labels: { cert-manager: enabled }`) or the empty-list sentinel
//     `labels: []` that older hand-authored files used to mean "no
//     labels". Downstream code reads this via config.parseLabels which
//     normalises both shapes into a map[string]string.
//
// The yaml tags mirror the existing config.clusterEntry shape exactly so
// that bytes parsed via the legacy bare-YAML path and bytes parsed via the
// new envelope path produce semantically-equivalent specs (see the
// TestRoundTrip_* tests in cluster_test.go).
type ManagedClusterEntry struct {
	Name       string      `json:"name" yaml:"name"`
	SecretPath string      `json:"secretPath,omitempty" yaml:"secretPath,omitempty"`
	Region     string      `json:"region,omitempty" yaml:"region,omitempty"`
	Labels     interface{} `json:"labels,omitempty" yaml:"labels,omitempty"`
}

// ManagedClustersSpec is the spec block of a managed-clusters.yaml envelope.
// In legacy bare-YAML files this is the WHOLE document (top-level clusters:
// key); in enveloped files it sits under spec:. LoadManagedClusters returns
// the same spec value either way — the envelope is purely a transport
// concern for the reader, and an aspiration-of-shape concern for the writer.
type ManagedClustersSpec struct {
	Clusters []ManagedClusterEntry `json:"clusters" yaml:"clusters"`
}

// ManagedClustersDoc is the on-disk shape for an enveloped
// managed-clusters.yaml (apiVersion: sharko.io/v1, kind: ManagedClusters).
// It is the canonical Save target; the reader accepts both this shape and
// the legacy bare ManagedClustersSpec.
type ManagedClustersDoc = schema.Envelope[ManagedClustersSpec]

// ManagedClustersMetadataName is the conventional metadata.name value
// emitted by SaveManagedClusters. It mirrors the example envelope in
// docs/design/2026-05-12-v125-architectural-todos.md lines 100-114. The
// V125-1-9 reader does not enforce the value — any non-empty name passes
// the structural read — but the writer always emits this constant so that
// freshly-rendered files are byte-identical regardless of which Sharko
// installation rendered them.
const ManagedClustersMetadataName = "managed-clusters"

// LoadManagedClusters parses the on-disk bytes of a managed-clusters.yaml
// document and returns its spec. The function accepts BOTH shapes during
// the V125-1-9 → V126 transition window:
//
//   - **Legacy bare YAML** (no apiVersion) — unmarshalled directly as a
//     ManagedClustersSpec. This is the shape every pre-V125-1-9 Sharko
//     installation has on disk; the reader stays back-compat until the
//     V126 cleanup (see the V125-1-9 epic's "Filename alias removal
//     window" locked OQ). Legacy bodies SKIP JSON Schema validation —
//     the V125-1-9 design contract is that pre-envelope files keep
//     working as-is; introducing validation on the legacy path would
//     break every existing user repo on upgrade day.
//   - **Enveloped YAML** (apiVersion: sharko.io/v1, kind: ManagedClusters)
//     — JSON-Schema-validated against docs/schemas/managed-clusters.v1.json
//     (Story 9.4), then unmarshalled into ManagedClustersDoc; the spec
//     field is returned. An envelope whose kind is not ManagedClusters
//     (e.g. the wrong file handed to the wrong loader) returns an
//     explicit error so the caller surfaces "wrong document type"
//     instead of silently treating it as an empty clusters list.
//
// Detection is delegated to schema.IsEnveloped so the two reader paths
// (managed-clusters and addon-catalog, the latter landing in Story 9.2)
// share one routing primitive.
//
// Read-time JSON Schema validation (V125-1-9.4): the enveloped branch
// runs the body through schema.DefaultValidator().Validate before
// unmarshal. A validation failure returns the *schema.ValidationFailure
// to the caller and emits a slog.Error with the full violation list
// (via schema.LogValidationFailure) so an operator who has corrupted
// the file sees the same audit-log entry the reconciler would have
// emitted on a silent failure. Per the design doc §4 line 167: malformed
// files are rejected with an audit-logged error rather than a silent
// reconcile failure.
func LoadManagedClusters(body []byte) (ManagedClustersSpec, error) {
	enveloped, err := schema.IsEnveloped(body)
	if err != nil {
		// IsEnveloped returns an error only on malformed top-level YAML.
		// Surfacing it here means the caller gets one error type for
		// "this is not parseable YAML at all" rather than a confusing
		// fall-through into the bare-YAML unmarshal that would produce
		// the same error twice.
		return ManagedClustersSpec{}, fmt.Errorf("parsing managed-clusters: %w", err)
	}

	if enveloped {
		// Peek the kind BEFORE schema validation. Story 9.4 contract:
		// the wrong-kind guard (an envelope with apiVersion sharko.io/v1
		// but a kind other than ManagedClusters) emits the SAME
		// actionable error message the pre-9.4 reader produced — the
		// V125-1-9.1 tests pin the format and downstream consumers
		// (Story 9.5's CLI, V125-1-8's reconciler audit log) depend
		// on it. Running validation first would surface the generic
		// "kind: value must be ..." schema violation instead.
		var doc ManagedClustersDoc
		if err := yaml.Unmarshal(body, &doc); err != nil {
			return ManagedClustersSpec{}, fmt.Errorf("parsing managed-clusters envelope: %w", err)
		}
		if doc.Kind != schema.KindManagedClusters {
			return ManagedClustersSpec{}, fmt.Errorf(
				"managed-clusters envelope kind %q, expected %q",
				doc.Kind, schema.KindManagedClusters,
			)
		}

		// Story 9.4 — JSON Schema validation on the enveloped path
		// only, AFTER the wrong-kind check so the error precedence
		// matches the pre-9.4 reader. Legacy bare YAML deliberately
		// skips this step (see docstring back-compat contract).
		//
		// If DefaultValidator returned an error (embed assets failed
		// to compile — a build-time bug, not a runtime data problem),
		// we deliberately do NOT block the read: the upstream
		// envelope/structural checks already fired above, and a
		// panic-on-read would brick the server on a corrupt build.
		// The build-time invariant (TestNewValidator passes) is the
		// actual gate.
		if validator, vErr := schema.DefaultValidator(); vErr == nil && validator != nil {
			if err := validator.Validate(schema.KindManagedClusters, body); err != nil {
				var vf *schema.ValidationFailure
				if errors.As(err, &vf) {
					schema.LogValidationFailure("managed-clusters.yaml", vf)
				}
				return ManagedClustersSpec{}, fmt.Errorf("validating managed-clusters envelope: %w", err)
			}
		}
		return doc.Spec, nil
	}

	// Legacy bare YAML: unmarshal directly as a spec. The legacy shape's
	// top-level keys are exactly ManagedClustersSpec's yaml-tagged fields
	// (clusters:), so the same struct shape works for both paths.
	// SKIPS Story 9.4's JSON Schema validation by design.
	var spec ManagedClustersSpec
	if err := yaml.Unmarshal(body, &spec); err != nil {
		return ManagedClustersSpec{}, fmt.Errorf("parsing managed-clusters: %w", err)
	}
	return spec, nil
}

// SaveManagedClusters renders spec as an enveloped managed-clusters.yaml
// document. The output ALWAYS contains:
//
//  1. ManagedClustersSchemaHeader as the first line (the
//     `# yaml-language-server: $schema=...` line that yaml-language-server
//     in editors uses to fetch the schema for inline validation +
//     auto-completion).
//  2. The full envelope: apiVersion: sharko.io/v1, kind: ManagedClusters,
//     metadata.name: managed-clusters, spec: { ... }.
//
// This is the canonical writer for new-file emission (bootstrap templates,
// fresh demo seeds, future reconciler-side writes). The orchestrator's
// existing line-level mutators in internal/gitops/yaml_mutator.go are NOT
// replaced by this function in Story 9.1 — they continue to perform
// in-place edits that preserve comments and authoring formatting. The
// reconciler-driven design landing in V125-1-8 will retire those mutator
// paths; until then, Sharko's read side handles both shapes (via
// LoadManagedClusters) and write side picks the appropriate tool for the
// edit (whole-file regenerate → SaveManagedClusters; in-place mutate →
// gitops mutator).
func SaveManagedClusters(spec ManagedClustersSpec) ([]byte, error) {
	doc := ManagedClustersDoc{
		APIVersion: schema.APIVersion,
		Kind:       schema.KindManagedClusters,
		Metadata:   schema.Metadata{Name: ManagedClustersMetadataName},
		Spec:       spec,
	}
	body, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("marshalling managed-clusters envelope: %w", err)
	}

	// Prepend the schema header as line 1. We do this here (rather than
	// asking yaml.v3 to emit a head comment on the root node) because
	// yaml.v3's HeadComment on a generic Envelope[T] would attach to the
	// document via a yaml.Node wrapper, which would force every caller to
	// construct a yaml.Node-based envelope instead of a plain
	// ManagedClustersDoc value. The simpler bytes.Buffer prepend keeps
	// the public API a plain struct.
	var buf bytes.Buffer
	buf.WriteString(ManagedClustersSchemaHeader)
	buf.WriteByte('\n')
	buf.Write(body)
	return buf.Bytes(), nil
}

// Cluster represents a Kubernetes cluster from the Git configuration.
type Cluster struct {
	Name             string            `json:"name" yaml:"name"`
	SecretPath       string            `json:"secret_path,omitempty" yaml:"secretPath,omitempty"`
	Labels           map[string]string `json:"labels" yaml:"labels"`
	Region           string            `json:"region,omitempty" yaml:"region,omitempty"`
	ServerVersion    string            `json:"server_version,omitempty"`
	ConnectionStatus string            `json:"connection_status,omitempty"`
	Managed          bool              `json:"managed"` // true if in cluster-addons.yaml
}

// ClusterHealthStats holds aggregated health statistics for the clusters overview.
type ClusterHealthStats struct {
	TotalInGit         int `json:"total_in_git"`
	Connected          int `json:"connected"`
	Failed             int `json:"failed"`
	MissingFromArgoCD  int `json:"missing_from_argocd"`
	NotInGit           int `json:"not_in_git"`
}

// PendingRegistration represents a cluster registration PR that has been
// opened but not yet merged. The cluster itself is NOT in
// managed-clusters.yaml (and may or may not yet be in ArgoCD), so it must be
// surfaced as a distinct lifecycle state — neither "managed" nor
// "discovered/not_in_git" — to avoid the V125-1.5 family of UX bugs
// (BUG-050..055) where a pending-PR cluster appeared as if it half-existed
// across multiple unrelated panels.
//
// V125-1.5: ClusterName/PRURL/Branch are populated from the GitHub provider's
// open-PRs list, filtered by the registration-PR title pattern emitted by
// the orchestrator (see internal/orchestrator/git_helpers.go's
// findOpenPRForCluster — same matching contract). OpenedAt is the upstream
// PR's createdAt timestamp (RFC3339 string from the provider).
type PendingRegistration struct {
	ClusterName string `json:"cluster_name"`
	PRURL       string `json:"pr_url"`
	Branch      string `json:"branch"`
	OpenedAt    string `json:"opened_at"`
}

// OrphanRegistration represents an ArgoCD cluster Secret that has NO
// corresponding entry in managed-clusters.yaml AND no open registration PR
// — i.e. a cluster Secret left behind in the live argocd ns after a
// registration PR was closed without merging (or after some other
// abandonment path). Surfaced as its own lifecycle state so the user has a
// recovery action ("delete the orphan Secret"); see V125-1-7 / BUG-058.
//
// Production diagnosis: internal/orchestrator/cluster.go:408's manual-mode
// register path falls through to a direct ArgoCD API RegisterCluster call
// when argoSecretManager is nil + PRAutoMerge is false, which writes the
// cluster Secret BEFORE the PR opens. Closing the PR without merging
// leaves that Secret behind. V125-1-5's pending-PR filter masked it while
// the PR was open; once closed, it surfaced in `not_in_git`. V125-1-8
// closes the bug class architecturally by deferring the ArgoCD register
// until post-PR-merge — this struct is the MVP unblock recovery surface.
//
// LastSeenAt is the response time of the orphan resolver call (i.e. "now"
// at API-handler time). The ArgoCD cluster Secret API exposes no stable
// creation timestamp, so this is a degraded approximation — it tells the
// user "as of this refresh, this orphan exists" rather than "this orphan
// has existed since X". Documented in the resolver in
// internal/api/clusters_orphans.go.
type OrphanRegistration struct {
	ClusterName string `json:"cluster_name"`
	ServerURL   string `json:"server_url"`
	LastSeenAt  string `json:"last_seen_at"`
}

// ClustersResponse is the API response for listing clusters.
//
// PendingRegistrations and OrphanRegistrations are always non-nil slices
// (default `[]`) — V125-1.4 hit a nil-array crash on the frontend's
// similar dry-run path; we do not repeat the lesson here. An empty slice
// means there are no matching items (or the underlying provider call
// degraded; see the handler for the V124-22 dignified-degrade pattern).
type ClustersResponse struct {
	Clusters             []Cluster             `json:"clusters"`
	HealthStats          *ClusterHealthStats   `json:"health_stats,omitempty"`
	PendingRegistrations []PendingRegistration `json:"pending_registrations"`
	// OrphanRegistrations: V125-1-7 / BUG-058. ArgoCD cluster Secrets that
	// have no managed-clusters.yaml entry AND no open registration PR. The
	// FE renders these in a dedicated "Cancelled / Orphan Registrations"
	// section with a per-row Delete cluster Secret button.
	OrphanRegistrations []OrphanRegistration `json:"orphan_registrations"`
}

// ClusterAddonInfo holds combined information about an addon in a specific cluster.
type ClusterAddonInfo struct {
	AddonName          string `json:"addon_name"`
	Chart              string `json:"chart"`
	RepoURL            string `json:"repo_url"`
	CurrentVersion     string `json:"current_version"`
	Enabled            bool   `json:"enabled"`
	Namespace          string `json:"namespace,omitempty"`
	EnvironmentVersion string `json:"environment_version,omitempty"`
	CustomVersion      string `json:"custom_version,omitempty"`
	HasVersionOverride bool   `json:"has_version_override"`

	// ArgoCD status fields
	ArgocdSyncStatus   string `json:"argocd_sync_status,omitempty"`
	ArgocdHealthStatus string `json:"argocd_health_status,omitempty"`
	ArgocdVersion      string `json:"argocd_version,omitempty"`
}

// ClusterDetailResponse is the API response for a single cluster's details.
type ClusterDetailResponse struct {
	Cluster Cluster          `json:"cluster"`
	Addons  []ClusterAddonInfo `json:"addons"`
}

// AddonComparisonStatus holds the comparison between Git config and ArgoCD deployment for one addon.
type AddonComparisonStatus struct {
	AddonName string `json:"addon_name"`

	// Git configuration
	GitConfigured bool   `json:"git_configured"`
	GitChart      string `json:"git_chart,omitempty"`
	GitRepoURL    string `json:"git_repo_url,omitempty"`
	GitVersion    string `json:"git_version,omitempty"`
	GitNamespace  string `json:"git_namespace,omitempty"`
	GitEnabled    bool   `json:"git_enabled"`

	// Version tracking
	EnvironmentVersion string `json:"environment_version,omitempty"`
	CustomVersion      string `json:"custom_version,omitempty"`
	HasVersionOverride bool   `json:"has_version_override"`

	// ArgoCD deployment
	ArgocdDeployed          bool   `json:"argocd_deployed"`
	ArgocdApplicationName   string `json:"argocd_application_name,omitempty"`
	ArgocdSyncStatus        string `json:"argocd_sync_status,omitempty"`
	ArgocdHealthStatus      string `json:"argocd_health_status,omitempty"`
	ArgocdDeployedVersion   string `json:"argocd_deployed_version,omitempty"`
	ArgocdNamespace         string `json:"argocd_namespace,omitempty"`
	ArgocdSourceRepoURL     string `json:"argocd_source_repo_url,omitempty"`
	ArgocdSourcePath        string `json:"argocd_source_path,omitempty"`
	ArgocdDestinationServer string `json:"argocd_destination_server,omitempty"`
	ArgocdOperationState    string `json:"argocd_operation_state,omitempty"`

	// Comparison results
	Status string   `json:"status,omitempty"`
	Issues []string `json:"issues"`

	LastSyncTime string `json:"last_sync_time,omitempty"`
	CreatedAt    string `json:"created_at,omitempty"`
}

// ClusterComparisonResponse is the API response for Git vs ArgoCD comparison.
type ClusterComparisonResponse struct {
	Cluster Cluster `json:"cluster"`

	// Git summary
	GitTotalAddons    int `json:"git_total_addons"`
	GitEnabledAddons  int `json:"git_enabled_addons"`
	GitDisabledAddons int `json:"git_disabled_addons"`

	// ArgoCD summary
	ArgocdTotalApplications      int `json:"argocd_total_applications"`
	ArgocdHealthyApplications    int `json:"argocd_healthy_applications"`
	ArgocdSyncedApplications     int `json:"argocd_synced_applications"`
	ArgocdDegradedApplications   int `json:"argocd_degraded_applications"`
	ArgocdOutOfSyncApplications  int `json:"argocd_out_of_sync_applications"`

	// Per-addon comparison
	AddonComparisons []AddonComparisonStatus `json:"addon_comparisons"`

	// Overall totals
	TotalHealthy            int `json:"total_healthy"`
	TotalWithIssues         int `json:"total_with_issues"`
	TotalMissingInArgocd    int `json:"total_missing_in_argocd"`
	TotalUntrackedInArgocd  int `json:"total_untracked_in_argocd"`
	TotalDisabledInGit      int `json:"total_disabled_in_git"`

	ClusterConnectionState  string `json:"cluster_connection_state,omitempty"`
	ArgocdConnectionStatus  string `json:"argocd_connection_status,omitempty"`  // e.g. "Successful", "Failed"
	ArgocdConnectionMessage string `json:"argocd_connection_message,omitempty"` // error details from ArgoCD
}

// ClusterValuesResponse is the API response for raw cluster values YAML.
type ClusterValuesResponse struct {
	ClusterName string `json:"cluster_name"`
	ValuesYAML  string `json:"values_yaml"`
}

// ConfigDiffEntry holds the diff between global defaults and cluster overrides for one addon.
type ConfigDiffEntry struct {
	AddonName     string `json:"addon_name"`
	HasOverrides  bool   `json:"has_overrides"`
	GlobalValues  string `json:"global_values"`
	ClusterValues string `json:"cluster_values"`
}

// ConfigDiffResponse is the API response for config diff between cluster values and global defaults.
type ConfigDiffResponse struct {
	ClusterName  string                 `json:"cluster_name"`
	GlobalValues map[string]interface{} `json:"global_values,omitempty"`
	AddonDiffs   []ConfigDiffEntry      `json:"addon_diffs"`
}
