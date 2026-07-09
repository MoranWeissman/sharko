package models

import (
	"bytes"
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/MoranWeissman/sharko/internal/schema"
)

// ManagedClustersSchemaHeader is the yaml-language-server header line
// written as the first line of every Sharko-emitted
// managed-clusters.yaml file.
const ManagedClustersSchemaHeader = "# yaml-language-server: $schema=https://raw.githubusercontent.com/MoranWeissman/sharko/main/docs/schemas/managed-clusters.v1.json"

// ManagedClusterEntry is one row in the spec.clusters array of a
// managed-clusters.yaml document. Field semantics:
//
//   - Name: required cluster identifier; matches the ArgoCD cluster
//     Secret name (cluster-<Name>) the reconciler manages.
//   - SecretPath: optional explicit path to the cluster's credentials in the
//     credentials provider (e.g. Vault path, AWS Secrets Manager path). When
//     empty, the credentials provider derives a default path from Name.
//   - Region: optional cloud region label, surfaced in the UI / observability.
//   - Labels: optional addon-enablement map. Modelled as map[string]string
//     because that is the shape EVERY Sharko producer emits (the gitops
//     cluster mutators build a map[string]string of addon→"enabled"/
//     "disabled"; the bootstrap template documents `<addon-name>: enabled`).
//     V2-cleanup-22 (Part 3 / decision #5) tightened this from interface{}
//     to map[string]string so the GENERATED JSON Schema (see cmd/schema-gen)
//     requires `labels` to be an object with string values and rejects the
//     scalar footgun (`labels: "oops"` previously passed the `"labels": true`
//     schema and silently yielded zero addons via config.parseLabels).
//     The legacy `labels: []` empty-array sentinel that older HAND-AUTHORED
//     bare-YAML files used is still tolerated on the legacy bare read path
//     (config.clusterEntry keeps interface{} + config.parseLabels handles
//     the array form) — that path skips schema validation by design. Only
//     the enveloped read/write path, where no producer ever emitted the
//     array form, is now constrained.
//
// The yaml tags mirror the existing config.clusterEntry shape exactly so
// that bytes parsed via the legacy bare-YAML path and bytes parsed via the
// new envelope path produce semantically-equivalent specs (see the
// TestRoundTrip_* tests in cluster_test.go).
type ManagedClusterEntry struct {
	Name       string        `json:"name" yaml:"name"`
	SecretPath string        `json:"secretPath,omitempty" yaml:"secretPath,omitempty"`
	Region     string        `json:"region,omitempty" yaml:"region,omitempty"`
	Labels     ClusterLabels `json:"labels,omitempty" yaml:"labels,omitempty"`

	// ConnectionManagedBy declares who owns this cluster's ArgoCD cluster
	// Secret (V2-cleanup-57.2). Absent/empty means "sharko" (the default —
	// zero migration for pre-field files). "user" means the user creates and
	// maintains the Secret by hand and Sharko only syncs addon labels onto
	// it — never writing, rotating, or deleting the credential material.
	// The jsonschema enum keeps hand-authored enveloped files honest: a
	// typo'd value is rejected at validation time instead of silently
	// falling back to Sharko-managed. See internal/models/connection_mode.go
	// for the canonical constants + predicate.
	ConnectionManagedBy string `json:"connectionManagedBy,omitempty" yaml:"connectionManagedBy,omitempty" jsonschema:"enum=sharko,enum=user"`

	// CredsSource records WHERE this cluster's credentials live
	// (V2-cleanup-60.4): "inline-kubeconfig" — pasted at registration, the
	// credentials exist ONLY in the ArgoCD cluster Secret and every fetch
	// must use the ArgoCD reader regardless of the configured backend;
	// "secret-kubeconfig" / "eks-token" — the secrets backend holds them.
	// Absent/empty means the record predates the field (unknown) — readers
	// fall back to backend-first-then-ArgoCD-read. See
	// models.CredentialRoutingFor + internal/providers.ClusterCredsRouter.
	CredsSource string `json:"credsSource,omitempty" yaml:"credsSource,omitempty" jsonschema:"enum=inline-kubeconfig,enum=secret-kubeconfig,enum=eks-token"`

	// RoleARN is the per-cluster IAM role Sharko assumes when minting EKS
	// tokens for this cluster (V2-cleanup-62.2). Only meaningful for the
	// eks-token creds source (a discovery-registered cross-account cluster
	// records the role that discovered it here, so token minting uses the
	// same identity). Role-ARN precedence at token-mint time: the
	// structured SM secret's own roleArn (most specific, per-secret) >
	// this per-cluster value > the connection-level provider default.
	// Absent/empty keeps today's behavior byte-identical.
	RoleARN string `json:"roleArn,omitempty" yaml:"roleArn,omitempty"`
}

// ClusterLabels is the addon-enablement map on a managed-cluster entry. Its
// underlying type is map[string]string, which drives the GENERATED JSON
// Schema (cmd/schema-gen reflects it to `{type: object,
// additionalProperties: {type: string}}` — V2-cleanup-22 Part 3, the object
// minimum that closes the scalar footgun).
//
// The custom UnmarshalYAML preserves READ tolerance for older hand-authored
// bare-YAML files: yaml.v3 alone would reject the legacy `labels: []`
// empty-array sentinel ("cannot unmarshal !!seq into map[string]string"),
// silently breaking pre-envelope user repos. We treat an empty sequence and
// an explicit null as "no labels" (empty map), exactly as config.parseLabels
// always has. A scalar (`labels: oops`) is still a parse error — that is the
// footgun we are closing — and on the enveloped path the schema validator
// also rejects it before the reader is reached.
type ClusterLabels map[string]string

// UnmarshalYAML implements yaml.Unmarshaler with legacy tolerance. See the
// ClusterLabels doc for the back-compat rationale.
func (l *ClusterLabels) UnmarshalYAML(value *yaml.Node) error {
	switch {
	case value == nil, value.Tag == "!!null":
		*l = ClusterLabels{}
		return nil
	case value.Kind == yaml.SequenceNode:
		// Legacy `labels: []` empty-array sentinel → no labels. A
		// non-empty sequence is nonsensical for a label map; reject it so
		// genuinely malformed input still surfaces.
		if len(value.Content) != 0 {
			return fmt.Errorf("labels must be a mapping of string→string, got a non-empty sequence")
		}
		*l = ClusterLabels{}
		return nil
	default:
		var m map[string]string
		if err := value.Decode(&m); err != nil {
			return fmt.Errorf("labels must be a mapping of string→string: %w", err)
		}
		*l = m
		return nil
	}
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
// managed-clusters.yaml (apiVersion: sharko.dev/v1, kind: ManagedClusters).
// It is the canonical Save target; the reader accepts both this shape and
// the legacy bare ManagedClustersSpec.
type ManagedClustersDoc = schema.Envelope[ManagedClustersSpec]

// ManagedClustersMetadataName is the conventional metadata.name value
// emitted by SaveManagedClusters. It mirrors the example envelope in
// docs/design/2026-05-12-v125-architectural-todos.md lines 100-114. The
// reader does not enforce the value — any non-empty name passes the
// structural read — but the writer always emits this constant so that
// freshly-rendered files are byte-identical regardless of which Sharko
// installation rendered them.
const ManagedClustersMetadataName = "managed-clusters"

// LoadManagedClusters parses the on-disk bytes of a managed-clusters.yaml
// document and returns its spec. The function accepts BOTH shapes:
//
//   - **Legacy bare YAML** (no apiVersion) — unmarshalled directly as a
//     ManagedClustersSpec. Legacy bodies SKIP JSON Schema validation;
//     pre-envelope files keep working as-is.
//   - **Enveloped YAML** (apiVersion: sharko.dev/v1, kind: ManagedClusters)
//     — JSON-Schema-validated against docs/schemas/managed-clusters.v1.json,
//     then unmarshalled into ManagedClustersDoc; the spec field is
//     returned. An envelope whose kind is not ManagedClusters (e.g.
//     the wrong file handed to the wrong loader) returns an explicit
//     error so the caller surfaces "wrong document type" instead of
//     silently treating it as an empty clusters list.
//
// Detection is delegated to schema.IsEnveloped so the two reader paths
// (managed-clusters and addon-catalog) share one routing primitive.
//
// Read-time JSON Schema validation: the enveloped branch runs the body
// through schema.DefaultValidator().Validate before unmarshal. A
// validation failure returns the *schema.ValidationFailure to the
// caller and emits a slog.Error with the full violation list. Malformed
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
		// Peek the kind BEFORE schema validation. The wrong-kind guard
		// (an envelope with apiVersion sharko.dev/v1 but a kind other
		// than ManagedClusters) must emit the canonical actionable
		// error message that downstream consumers (validate-config
		// CLI, reconciler audit log) depend on — running validation
		// first would surface the generic "kind: value must be ..."
		// schema violation instead.
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

		// JSON Schema validation on the enveloped path only, AFTER the
		// wrong-kind check so the error precedence is stable. Legacy
		// bare YAML deliberately skips this step.
		//
		// If DefaultValidator returned an error (embed assets failed
		// to compile — a build-time bug, not runtime data), we
		// deliberately do NOT block the read: upstream
		// envelope/structural checks already fired and a panic-on-read
		// would brick the server on a corrupt build. The build-time
		// invariant (TestNewValidator) is the actual gate.
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
	// SKIPS JSON Schema validation by design.
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
//  2. The full envelope: apiVersion: sharko.dev/v1, kind: ManagedClusters,
//     metadata.name: managed-clusters, spec: { ... }.
//
// This is the canonical writer for new-file emission (bootstrap templates,
// fresh demo seeds, future reconciler-side writes). The orchestrator's
// existing line-level mutators in internal/gitops/yaml_mutator.go are NOT
// replaced by this function in Story 9.1 — they continue to perform
// in-place edits that preserve comments and authoring formatting. The
// The reconciler-driven design will eventually retire those mutator
// paths; until then, Sharko's read side handles both shapes (via
// LoadManagedClusters) and the write side picks the appropriate tool
// for the edit (whole-file regenerate → SaveManagedClusters; in-place
// mutate → gitops mutator).
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

	// Validate-before-commit safety net (V2-cleanup-22, Part 1 / decisions
	// #1+#2). SaveManagedClusters is the single choke point every
	// managed-clusters writer funnels through (the gitops cluster mutators
	// in internal/gitops/yaml_mutator_cluster.go all end in this call), so
	// validating here means a new caller cannot bypass the gate. We run the
	// SAME embedded validator the readers use against the marshalled bytes
	// BEFORE any commit/PR happens — a failure means a Sharko bug or a
	// genuinely bad in-memory spec, not legitimate user data, so we FAIL the
	// operation and emit nothing. By construction Sharko-generated content
	// passes; this only fires on a regression. If DefaultValidator itself
	// fails to compile (a build-time bug, never runtime data) we do not block
	// the write — the build-time invariant (TestNewValidator) is the real
	// gate, same stance the reader paths take.
	if validator, vErr := schema.DefaultValidator(); vErr == nil && validator != nil {
		if err := validator.Validate(schema.KindManagedClusters, body); err != nil {
			var vf *schema.ValidationFailure
			if errors.As(err, &vf) {
				schema.LogValidationFailure("managed-clusters.yaml (write)", vf)
			}
			return nil, fmt.Errorf("validating managed-clusters before write: %w", err)
		}
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
	Name          string            `json:"name" yaml:"name"`
	SecretPath    string            `json:"secret_path,omitempty" yaml:"secretPath,omitempty"`
	Labels        map[string]string `json:"labels" yaml:"labels"`
	Region        string            `json:"region,omitempty" yaml:"region,omitempty"`
	ServerVersion string            `json:"server_version,omitempty"`
	// ServerURL is the cluster's ArgoCD-registered API-server URL (V2-cleanup-74.1).
	// Sourced from the matching models.ArgocdCluster.Server — never stored in Git,
	// always populated at read time. The UI's ClusterTypeBadge parses this hostname
	// to classify EKS/AKS/GKE/kind/minikube/self-hosted. Left empty for the
	// hub-local "in-cluster" entry (see ListClusters/GetClusterDetail skip logic).
	ServerURL string `json:"server_url,omitempty" yaml:"serverUrl,omitempty"`

	// TargetPlatform is Sharko's auto-detected guess at whether this
	// specific cluster looks like EKS (V2-cleanup-88.1, design L11) —
	// distinct from the server's OWN AWS identity (see
	// GET /api/v1/system/capabilities). Computed at read time from
	// ServerURL and CredsSource, the same way DerivedHealthStatus is
	// computed from live state below — never stored in Git. One of "eks"
	// or "unknown". The register-cluster screen uses this (via the list
	// of already-registered clusters) plus system/capabilities to decide
	// what to ask the user versus what it can already tell them.
	TargetPlatform string `json:"target_platform,omitempty"`

	ConnectionStatus string `json:"connection_status,omitempty"`
	Managed          bool   `json:"managed"` // true if in cluster-addons.yaml

	// ConnectionManagedBy mirrors the managed-clusters.yaml entry's
	// connectionManagedBy field onto the API surface (V2-cleanup-57.2).
	// Empty means Sharko-managed (the default); "user" means the user
	// created and maintains the ArgoCD cluster Secret and Sharko only
	// syncs addon labels onto it. The UI renders a read-only
	// "connection: managed by you" caption off this field.
	ConnectionManagedBy string `json:"connection_managed_by,omitempty" yaml:"connectionManagedBy,omitempty"`

	// CredsSource mirrors the managed-clusters.yaml entry's credsSource
	// field (V2-cleanup-60.4): "inline-kubeconfig" (credentials live only in
	// the ArgoCD cluster Secret), "secret-kubeconfig" / "eks-token" (the
	// secrets backend holds them), or "" (record predates the field). The
	// credential-fetch routing (Test / Diagnose / secrets / addon ops) keys
	// off this so inline-registered clusters work under any backend.
	CredsSource string `json:"creds_source,omitempty" yaml:"credsSource,omitempty"`

	// RoleARN mirrors the managed-clusters.yaml entry's roleArn field
	// (V2-cleanup-62.2): the per-cluster IAM role to assume when minting
	// EKS tokens (eks-token creds source only). Empty for records that
	// predate the field or clusters that use the connection-level default.
	RoleARN string `json:"role_arn,omitempty" yaml:"roleArn,omitempty"`

	// Connectivity check fields (V2-cleanup-29). Flat primitives only —
	// computed at the API layer from ArgoCD application state. The models
	// package must not import internal/observations (no import cycle).
	//
	// connectivity_status values:
	//   "verified_argocd" — ArgoCD ConnectionStatus == "Successful"
	//   "verified_check"  — connectivity-check Application is Synced+Healthy
	//   "check_pending"   — check Application exists but not yet Synced+Healthy (deploying)
	//   "check_failed"    — check Application has honest failure signals (Degraded,
	//                       OperationPhase Failed/Error, or Error-type conditions)
	//   ""               — nothing known (ArgoCD "Unknown" stands untouched)
	ConnectivityStatus string `json:"connectivity_status,omitempty"`
	ConnectivityDetail string `json:"connectivity_detail,omitempty"` // detail for check_pending or check_failed

	// DerivedHealthStatus is the auto-derived Sharko reachability verdict
	// (V2-cleanup-85.4). Unlike SharkoStatus below — which stays empty
	// forever until someone clicks "Test connection" — this field is
	// computed fresh on every read from live ArgoCD state, with NO manual
	// step required. Priority order (first match wins):
	//   1. any addon on this cluster is Synced+Healthy in ArgoCD -> "healthy"
	//   2. the connectivity-check application is Synced+Healthy  -> "reachable"
	//   3. ArgoCD's own connection to the cluster is "Successful" -> "reachable"
	//   4. none of the above                                      -> "unknown"
	// The frontend should read this field (not SharkoStatus) to answer
	// "is this cluster healthy" without requiring a manual test click.
	DerivedHealthStatus string `json:"derived_health_status,omitempty"`

	// AddonSecretsReady reports whether Sharko currently has resolvable
	// credentials for this cluster (V2-cleanup-88.3 — lazy credentials).
	// Computed on every read via CredentialsResolvable — a cheap
	// presence-of-config check (creds_source / connection mode / whether a
	// secrets backend is configured), NOT a live probe, mirroring the
	// DerivedHealthStatus precedent above. true means a secret-bearing
	// addon can likely be enabled on this cluster without hitting the
	// EnableAddon pre-flight rejection; false means the UI should surface
	// "add connection credentials before enabling an addon with secrets"
	// instead of letting the request round-trip into a 422. Registration
	// itself never requires credentials — this field only matters once the
	// operator tries to enable a secret-bearing addon.
	AddonSecretsReady bool `json:"addon_secrets_ready"`

	// Sharko observability fields (V2-cleanup-27 folded into V2-cleanup-29).
	// Populated when obsStore is available; absent otherwise (omitempty).
	SharkoStatus  string `json:"sharko_status,omitempty"`
	LastTestAt    string `json:"last_test_at,omitempty"` // RFC3339
	TestFailing   bool   `json:"test_failing,omitempty"`
	TestErrorCode string `json:"test_error_code,omitempty"`

	// LastReconcile is the most recent cluster-secret reconciler outcome for
	// this cluster (V2-cleanup-89.4 — reconcile results used to be
	// server-log-only; ArgoCD shows a failed apply, Sharko showed nothing).
	// Computed at read time from clusterreconciler.Reconciler's in-memory
	// per-cluster record — never stored in git. nil when the reconciler
	// hasn't processed this cluster on this server instance yet (fresh
	// startup, a registration PR that hasn't merged, or no reconciler wired
	// in this deployment mode).
	LastReconcile *ClusterLastReconcile `json:"last_reconcile,omitempty"`
}

// ClusterLastReconcile is the read-model shape of a single cluster's most
// recent reconcile attempt (V2-cleanup-89.4). Kept as a plain struct here
// — rather than reusing clusterreconciler.ClusterReconcileRecord directly —
// because internal/clusterreconciler already imports internal/models, so
// models cannot import it back; the API layer copies field-by-field from
// the reconciler's record onto this type.
type ClusterLastReconcile struct {
	Time    string `json:"time"`              // RFC3339
	Outcome string `json:"outcome"`           // "succeeded" | "failed" | "skipped"
	Message string `json:"message,omitempty"` // plain-English detail; set on failed/skipped
}

// ClusterHealthStats holds aggregated health statistics for the clusters overview.
type ClusterHealthStats struct {
	TotalInGit        int `json:"total_in_git"`
	Connected         int `json:"connected"`
	Failed            int `json:"failed"`
	MissingFromArgoCD int `json:"missing_from_argocd"`
	NotInGit          int `json:"not_in_git"`
}

// PendingRegistration represents a cluster registration PR that has been
// opened but not yet merged. The cluster itself is NOT in
// managed-clusters.yaml (and may or may not yet be in ArgoCD), so it must be
// surfaced as a distinct lifecycle state — neither "managed" nor
// "discovered/not_in_git" — to avoid the UX bugs where a pending-PR
// cluster appeared as if it half-existed across multiple panels.
//
// ClusterName/PRURL/Branch are populated from the GitHub provider's
// open-PRs list, filtered by the registration-PR title pattern emitted
// by the orchestrator (see findOpenPRForCluster — same matching
// contract). OpenedAt is the upstream PR's createdAt timestamp.
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
// recovery action ("delete the orphan Secret").
//
// Background: an older manual-mode register path would fall through to
// a direct ArgoCD API RegisterCluster call, writing the cluster Secret
// BEFORE the PR opened. Closing the PR without merging left that
// Secret behind. The current reconciler defers the ArgoCD register
// until post-PR-merge so new orphans should not be created — this
// struct remains as the recovery surface for any orphans that exist.
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
// (default `[]`) — never let a nil array reach the FE. An empty slice
// means no matching items (or the underlying provider call degraded;
// see the handler for the dignified-degrade pattern).
type ClustersResponse struct {
	Clusters             []Cluster             `json:"clusters"`
	HealthStats          *ClusterHealthStats   `json:"health_stats,omitempty"`
	PendingRegistrations []PendingRegistration `json:"pending_registrations"`
	// OrphanRegistrations: ArgoCD cluster Secrets that have no
	// managed-clusters.yaml entry AND no open registration PR. The FE
	// renders these in a dedicated "Cancelled / Orphan Registrations"
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
	Cluster Cluster            `json:"cluster"`
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
	// ArgocdOperationMessage carries the full operationState.message (capped at
	// 4000 chars) when the operation has a sync failure. The UI uses this to
	// render the complete error in the expanded comparison row. The issues[]
	// field carries the short first-line version for badges/lists. Empty when
	// there is no active failing operation.
	ArgocdOperationMessage string `json:"argocd_operation_message,omitempty"`

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
	ArgocdTotalApplications     int `json:"argocd_total_applications"`
	ArgocdHealthyApplications   int `json:"argocd_healthy_applications"`
	ArgocdSyncedApplications    int `json:"argocd_synced_applications"`
	ArgocdDegradedApplications  int `json:"argocd_degraded_applications"`
	ArgocdOutOfSyncApplications int `json:"argocd_out_of_sync_applications"`

	// Per-addon comparison
	AddonComparisons []AddonComparisonStatus `json:"addon_comparisons"`

	// Overall totals
	TotalHealthy           int `json:"total_healthy"`
	TotalWithIssues        int `json:"total_with_issues"`
	TotalMissingInArgocd   int `json:"total_missing_in_argocd"`
	TotalUntrackedInArgocd int `json:"total_untracked_in_argocd"`
	TotalDisabledInGit     int `json:"total_disabled_in_git"`

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
