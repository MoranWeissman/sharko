package orchestrator

import (
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/verify"
)

// GitOpsConfig holds gitops preferences (from server Helm values).
type GitOpsConfig struct {
	PRAutoMerge  bool   // true = auto-merge PRs after creation; false = manual approval
	BranchPrefix string // e.g. "sharko/"
	CommitPrefix string // e.g. "sharko:"
	BaseBranch   string // e.g. "main"
	RepoURL      string // Git repo URL for placeholder replacement
}

// RepoPathsConfig holds the addons repo directory layout (from server Helm values).
type RepoPathsConfig struct {
	ClusterValues   string // e.g. "configuration/addons-clusters-values"
	GlobalValues    string // e.g. "configuration/addons-global-values"
	Catalog         string // e.g. "configuration/addons-catalog.yaml"
	Charts          string // e.g. "charts/"
	Bootstrap       string // e.g. "bootstrap/"
	HostClusterName string // e.g. "management" — the cluster running ArgoCD (uses in-cluster)
	ManagedClusters string // e.g. "configuration/managed-clusters.yaml"
}

// CredsSource is the explicit, honest axis describing WHERE a cluster's
// credentials come from. It is the creds-reframe-1 keystone contract:
// instead of inferring the credential origin from Provider (and from how
// the secret backend happens to sniff its payload), the caller may state
// it directly. The field is optional and additive — when empty, the
// effective source is DERIVED from the existing fields so every request
// that works today keeps working byte-for-byte (see deriveCredsSource).
type CredsSource string

const (
	// CredsSourceInlineKubeconfig — the caller pastes a raw kubeconfig YAML
	// in the Kubeconfig field; ParseInlineKubeconfig handles it and the
	// credProvider backend is NOT required.
	CredsSourceInlineKubeconfig CredsSource = "inline-kubeconfig"
	// CredsSourceSecretKubeconfig — a kubeconfig stored in the secret backend
	// (AWS-SM / k8s-secrets). Resolved through credProvider.GetCredentials.
	CredsSourceSecretKubeconfig CredsSource = "secret-kubeconfig"
	// CredsSourceEKSToken — structured EKS JSON stored in the secret backend
	// that mints a short-lived STS token. Resolved through the same
	// credProvider.GetCredentials path; v1 does NOT split the backend sniff,
	// so secret-kubeconfig and eks-token share the orchestrator route — the
	// win is the explicit, validated, honest label, not a routing change.
	CredsSourceEKSToken CredsSource = "eks-token"
)

// RegisterClusterRequest is the input for cluster registration.
//
// Kubeconfig is required when the effective creds source is inline (either
// Provider == "kubeconfig" or CredsSource == inline-kubeconfig) and MUST be
// empty for any backend source. SecretPath / Region / RoleARN (carried
// separately on the API surface) are EKS-only and MUST be empty when the
// effective source is inline. The handler enforces those cross-field
// exclusions and returns 400 on violation.
type RegisterClusterRequest struct {
	Name       string          `json:"name"`
	Provider   string          `json:"provider,omitempty"`
	SecretPath string          `json:"secret_path,omitempty"`
	Addons     map[string]bool `json:"addons"`
	Region     string          `json:"region"`
	DryRun     bool            `json:"dry_run,omitempty"`

	// CredsSource is the explicit, optional, additive declaration of where
	// the cluster credentials come from: "inline-kubeconfig",
	// "secret-kubeconfig", or "eks-token". When empty it is derived from
	// Provider so existing callers are unaffected (see deriveCredsSource).
	// When set it is the authoritative axis: if it disagrees with Provider,
	// CredsSource wins and Provider becomes optional cluster-type metadata.
	CredsSource CredsSource `json:"creds_source,omitempty"`

	// RoleARN is the per-cluster IAM role Sharko assumes when minting EKS
	// tokens for this cluster (V2-cleanup-62.2). Only meaningful for the
	// eks-token creds source — the discovery flow passes the cross-account
	// role that found the cluster so token minting uses the same identity;
	// it is rejected (400) for an inline-kubeconfig registration. Persisted
	// on the cluster's managed-clusters.yaml entry as roleArn and read back
	// at every credential fetch. Token-mint precedence: the structured SM
	// secret's own roleArn > this per-cluster value > the connection-level
	// provider default. Empty keeps today's behavior byte-identical.
	RoleARN string `json:"role_arn,omitempty"`

	// Kubeconfig is the raw kubeconfig YAML supplied inline by the caller
	// when the effective creds source is inline-kubeconfig. Bearer-token
	// authentication only in v1.25 — cert-based and exec-plugin auth return
	// a 400 with guidance to generate a token via `kubectl create token`.
	// Ignored for any backend (secret) source.
	Kubeconfig string `json:"kubeconfig,omitempty"`

	// AutoMerge is the per-request auto-merge decision. nil means "fall
	// back to the connection-level PRAutoMerge default"; a non-nil
	// value overrides the default for this operation only. Resolved via
	// resolveAutoMerge — never mutate o.gitops.PRAutoMerge.
	AutoMerge *bool `json:"auto_merge,omitempty"`

	// ConnectionManagedBy declares who owns this cluster's ArgoCD cluster
	// Secret (V2-cleanup-57.2): "" or "sharko" (default) — Sharko writes and
	// rotates the Secret exactly as before; "user" — the caller creates and
	// maintains the Secret by hand and Sharko NEVER writes it (registration
	// skips the direct Secret write; the reconcilers only sync addon labels
	// onto the existing user-created Secret). For a self-managed
	// registration, credentials become OPTIONAL: when supplied (inline
	// kubeconfig or a resolvable backend secret) Stage-1 connectivity
	// verification still runs as a fail-fast courtesy; when absent the
	// verification is skipped and registration proceeds straight to the Git
	// record. The value is recorded in the cluster's managed-clusters.yaml
	// entry as connectionManagedBy.
	ConnectionManagedBy string `json:"connection_managed_by,omitempty"`
}

// UpdateClusterAddonsRequest is the input for PATCH /clusters/{name}.
type UpdateClusterAddonsRequest struct {
	Addons     map[string]bool `json:"addons,omitempty"`
	SecretPath *string         `json:"secret_path,omitempty"`

	// AutoMerge is the per-request auto-merge decision. nil means "fall
	// back to the connection-level PRAutoMerge default".
	AutoMerge *bool `json:"auto_merge,omitempty"`
}

// RegisterClusterResult is the output of a successful cluster registration.
type RegisterClusterResult struct {
	Status         string        `json:"status"` // "success" or "partial"
	Cluster        ClusterResult `json:"cluster"`
	Git            *GitResult    `json:"git,omitempty"`
	Secrets        []string      `json:"secrets_created,omitempty"` // names of created secrets
	FailedSecrets  []SecretError `json:"failed_secrets,omitempty"`  // secrets that failed to create
	CompletedSteps []string      `json:"completed_steps,omitempty"`
	FailedStep     string        `json:"failed_step,omitempty"`
	Error          string        `json:"error,omitempty"`
	Message        string        `json:"message,omitempty"`
	Adopted        bool          `json:"adopted,omitempty"` // true if cluster was already in ArgoCD

	// Verification holds the Stage1 connectivity verification result (if run).
	Verification *verify.Result `json:"verification,omitempty"`

	// DryRun holds the preview result when dry_run=true. No side effects occur.
	DryRun *DryRunResult `json:"dry_run,omitempty"`

	// ArgoCD cluster secret outcome. Set by the API handler after calling Manager.Ensure().
	// Possible values: "created", "adopted", "updated", "skipped", "error".
	ArgoSecretStatus string `json:"argocd_secret_status,omitempty"`
	// ArgoSecretError holds the error message if the ArgoCD secret step failed (non-fatal).
	ArgoSecretError string `json:"argocd_secret_error,omitempty"`

	// Warnings holds plain-English advisories that do NOT fail the
	// operation — e.g. a self-managed connection's ArgoCD cluster Secret
	// turning out to be rendered by another ArgoCD Application
	// (V2-cleanup-89.5). Empty/omitted when there is nothing to warn about.
	Warnings []string `json:"warnings,omitempty"`
}

// DryRunResult holds the preview information returned when dry_run=true.
// No writes (Git, ArgoCD, secrets) are performed.
type DryRunResult struct {
	EffectiveAddons []string       `json:"effective_addons"`
	FilesToWrite    []FilePreview  `json:"files_to_write"`
	PRTitle         string         `json:"pr_title"`
	SecretsToCreate []string       `json:"secrets_to_create"`
	Verification    *verify.Result `json:"verification,omitempty"`
}

// FilePreview describes a file that would be written during a non-dry-run operation.
type FilePreview struct {
	Path   string `json:"path"`
	Action string `json:"action"` // "create" or "update"
}

// SecretError records a secret that failed to create on the remote cluster.
type SecretError struct {
	Name  string `json:"name"`
	Error string `json:"error"`
}

// ClusterResult holds cluster details in operation results.
type ClusterResult struct {
	Name          string          `json:"name"`
	Server        string          `json:"server"`
	ServerVersion string          `json:"server_version,omitempty"`
	Addons        map[string]bool `json:"addons,omitempty"`
}

// GitResult holds the outcome of a gitops operation.
type GitResult struct {
	PRUrl      string `json:"pr_url,omitempty"`
	PRID       int    `json:"pr_id,omitempty"`
	Branch     string `json:"branch,omitempty"`
	Merged     bool   `json:"merged"`
	CommitSHA  string `json:"commit_sha,omitempty"`
	ValuesFile string `json:"values_file,omitempty"`

	// DryRun holds the preview result when the caller requested dry_run=true.
	// When set, NO git side effects occurred (no branch, no commit, no PR) —
	// the other GitResult fields are empty. omitempty keeps the non-dry-run
	// response shape byte-identical to before this field existed. Mirrors the
	// DryRun field on RegisterClusterResult so the UI reuses the same preview
	// render for both flows.
	DryRun *DryRunResult `json:"dry_run,omitempty"`
}

// AddAddonRequest is the input for adding an addon to the catalog.
type AddAddonRequest struct {
	Name              string                   `json:"name"`
	Chart             string                   `json:"chart"`
	RepoURL           string                   `json:"repo_url"`
	Version           string                   `json:"version"`
	Namespace         string                   `json:"namespace"`
	SelfHeal          *bool                    `json:"self_heal,omitempty"`
	SyncOptions       []string                 `json:"sync_options,omitempty"`
	AdditionalSources []models.AddonSource     `json:"additional_sources,omitempty"`
	IgnoreDifferences []map[string]interface{} `json:"ignore_differences,omitempty"`
	ExtraHelmValues   map[string]string        `json:"extra_helm_values,omitempty"`

	// AutoMerge is the per-request auto-merge decision. nil means "fall back
	// to the connection-level PRAutoMerge default"; a non-nil value overrides
	// the default for this operation only. Routed to PRMetadata.AutoMergeOverride
	// and resolved via resolveAutoMerge — never mutate o.gitops.PRAutoMerge.
	// Mirrors the field on init/register/remove/disable so add-to-catalog
	// behaves identically.
	AutoMerge *bool `json:"auto_merge,omitempty"`

	// DryRun, when true, makes AddAddon compute and return the files it WOULD
	// write (as a DryRunResult on the returned GitResult) with NO side effects
	// — no branch, no commit, no PR. Mirrors register-cluster's dry-run so the
	// Marketplace UI can preview the change before committing.
	DryRun bool `json:"dry_run,omitempty"`

	// Source identifies the originating UI flow for audit/observability.
	// Optional. Examples: "marketplace" (curated catalog Configure modal),
	// "manual" (raw Add Addon form), "" (caller didn't say — handler treats
	// as "manual" for the audit detail).
	Source string `json:"source,omitempty"`

	// UpstreamValues is the raw chart `values.yaml` bytes that the API
	// handler pre-fetched. When non-empty, AddAddon runs the smart-values
	// pipeline and writes an annotated global values file with a
	// per-cluster template block. When empty, AddAddon falls back to a
	// minimal stub (`<name>:\n  enabled: false`). Not part of the wire
	// schema — handlers populate it after `helm.FetchValues` and the
	// smart-parser layer.
	UpstreamValues []byte `json:"-"`

	// AIAnnotated is set by the API handler after the AI annotate pass
	// succeeds. The orchestrator stamps the
	// `# AI annotation: enabled` line in the file header based on this
	// flag — see WriteSmartValuesHeader. False means heuristic-only
	// (or AI was skipped: not configured, secret blocked, timeout, opt-out).
	AIAnnotated bool `json:"-"`

	// AIOptOut is set by the API handler when the user has explicitly
	// opted this addon out of AI annotation via the per-addon toggle.
	// The orchestrator stamps the `# sharko: ai-annotate=off` line in
	// the file header so that the later refresh-from-upstream path
	// preserves the opt-out.
	AIOptOut bool `json:"-"`

	// ExtraClusterSpecificPaths is the union-additive set of cluster-
	// specific dotted paths from the AI annotate pass. The smart-values
	// splitter treats this as additive — it never subtracts from the
	// heuristic's classification. Empty when AI was skipped.
	ExtraClusterSpecificPaths []string `json:"-"`
}

// ConfigureAddonRequest is the input for updating an addon's catalog configuration.
type ConfigureAddonRequest struct {
	Name              string                   `json:"name"`
	Version           string                   `json:"version,omitempty"`
	SelfHeal          *bool                    `json:"self_heal,omitempty"`
	SyncOptions       []string                 `json:"sync_options,omitempty"`
	AdditionalSources []models.AddonSource     `json:"additional_sources,omitempty"`
	IgnoreDifferences []map[string]interface{} `json:"ignore_differences,omitempty"`
	ExtraHelmValues   map[string]string        `json:"extra_helm_values,omitempty"`
	// AutoMerge overrides the connection-level PRAutoMerge default for the
	// configure PR only. nil = fall back to the connection default. Routed to
	// PRMetadata.AutoMergeOverride via the prMeta builder and resolved by
	// resolveAutoMerge — never mutate o.gitops.PRAutoMerge.
	AutoMerge *bool `json:"auto_merge,omitempty"`
}

// AdoptClustersRequest is the input for adopting existing ArgoCD clusters.
//
// AutoMerge is a pointer so callers can distinguish "not set" (fall back
// to connection-level PRAutoMerge default) from explicit true/false
// overrides. Resolved via resolveAutoMerge — never mutate
// o.gitops.PRAutoMerge (shared global state across concurrent requests).
type AdoptClustersRequest struct {
	Clusters  []string `json:"clusters"`
	AutoMerge *bool    `json:"auto_merge,omitempty"`
	DryRun    bool     `json:"dry_run,omitempty"`
}

// AdoptClusterResult holds the outcome for a single cluster adoption.
type AdoptClusterResult struct {
	Name         string         `json:"name"`
	Status       string         `json:"status"` // "success", "partial", "failed", "skipped"
	Verification *verify.Result `json:"verification,omitempty"`
	Git          *GitResult     `json:"git,omitempty"`
	Error        string         `json:"error,omitempty"`
	Message      string         `json:"message,omitempty"`
	DryRun       *DryRunResult  `json:"dry_run,omitempty"`

	// Warnings holds plain-English advisories that do NOT fail the
	// adoption — e.g. this cluster's ArgoCD cluster Secret turning out to
	// be rendered by another ArgoCD Application (V2-cleanup-89.5). Empty/
	// omitted when there is nothing to warn about.
	Warnings []string `json:"warnings,omitempty"`
}

// AdoptClustersResult is the aggregate response from adopting multiple clusters.
type AdoptClustersResult struct {
	Results []AdoptClusterResult `json:"results"`
}

// UnadoptClusterRequest is the input for un-adopting a cluster.
type UnadoptClusterRequest struct {
	Yes    bool `json:"yes"`
	DryRun bool `json:"dry_run,omitempty"`
	// AutoMerge overrides the connection-level PRAutoMerge default for the
	// unadopt PR only. nil = fall back to the connection default. Routed to
	// PRMetadata.AutoMergeOverride via the prMeta builder and resolved by
	// resolveAutoMerge — never mutate o.gitops.PRAutoMerge.
	AutoMerge *bool `json:"auto_merge,omitempty"`
}

// UnadoptClusterResult is the output of an un-adopt operation.
type UnadoptClusterResult struct {
	Name    string        `json:"name"`
	Status  string        `json:"status"` // "success", "partial", "failed"
	Git     *GitResult    `json:"git,omitempty"`
	Error   string        `json:"error,omitempty"`
	Message string        `json:"message,omitempty"`
	DryRun  *DryRunResult `json:"dry_run,omitempty"`
}

// RemoveClusterRequest is the input for cluster removal with configurable cleanup.
type RemoveClusterRequest struct {
	Name    string `json:"name"`
	Cleanup string `json:"cleanup"` // "all" (default), "git", "none"
	DryRun  bool   `json:"dry_run,omitempty"`
	Yes     bool   `json:"yes"` // confirmation required
	// AutoMerge overrides the connection-level PRAutoMerge default for the
	// removal PR only. nil = fall back to the connection default. Mirrors the
	// field on init/register/UpdateClusterAddons so removal behaves identically.
	AutoMerge *bool `json:"auto_merge,omitempty"`
}

// RemoveClusterResult is the output of a cluster removal operation.
type RemoveClusterResult struct {
	Name           string        `json:"name"`
	Status         string        `json:"status"` // "success", "partial", "failed"
	Cleanup        string        `json:"cleanup"`
	Git            *GitResult    `json:"git,omitempty"`
	CompletedSteps []string      `json:"completed_steps,omitempty"`
	FailedStep     string        `json:"failed_step,omitempty"`
	Error          string        `json:"error,omitempty"`
	Message        string        `json:"message,omitempty"`
	DryRun         *DryRunResult `json:"dry_run,omitempty"`
}

// DisableAddonRequest is the input for disabling an addon on a cluster.
type DisableAddonRequest struct {
	Cluster string `json:"cluster"`
	Addon   string `json:"addon"`
	Cleanup string `json:"cleanup"` // "all" (default), "labels", "none"
	DryRun  bool   `json:"dry_run,omitempty"`
	Yes     bool   `json:"yes"` // confirmation required
	// AutoMerge overrides the connection-level PRAutoMerge default for the
	// disable PR only. nil = fall back to the connection default. Mirrors the
	// field on init/register/UpdateClusterAddons so disable behaves identically.
	AutoMerge *bool `json:"auto_merge,omitempty"`
}

// DisableAddonResult is the output of an addon disable operation.
type DisableAddonResult struct {
	Cluster        string        `json:"cluster"`
	Addon          string        `json:"addon"`
	Status         string        `json:"status"` // "success", "partial", "failed"
	Cleanup        string        `json:"cleanup"`
	Git            *GitResult    `json:"git,omitempty"`
	CompletedSteps []string      `json:"completed_steps,omitempty"`
	FailedStep     string        `json:"failed_step,omitempty"`
	Error          string        `json:"error,omitempty"`
	Message        string        `json:"message,omitempty"`
	DryRun         *DryRunResult `json:"dry_run,omitempty"`
}

// EnableAddonRequest is the input for enabling an addon on a cluster.
type EnableAddonRequest struct {
	Cluster string `json:"cluster"`
	Addon   string `json:"addon"`
	DryRun  bool   `json:"dry_run,omitempty"`
	Yes     bool   `json:"yes"` // confirmation required
	// AutoMerge overrides the connection-level PRAutoMerge default for the
	// enable PR only. nil = fall back to the connection default. Mirrors the
	// field on DisableAddonRequest so enable/disable behave identically.
	// Routed to PRMetadata.AutoMergeOverride via the prMeta builder.
	AutoMerge *bool `json:"auto_merge,omitempty"`
}

// EnableAddonResult is the output of an addon enable operation.
type EnableAddonResult struct {
	Cluster        string        `json:"cluster"`
	Addon          string        `json:"addon"`
	Status         string        `json:"status"` // "success", "partial", "failed"
	Git            *GitResult    `json:"git,omitempty"`
	CompletedSteps []string      `json:"completed_steps,omitempty"`
	FailedStep     string        `json:"failed_step,omitempty"`
	Error          string        `json:"error,omitempty"`
	Message        string        `json:"message,omitempty"`
	DryRun         *DryRunResult `json:"dry_run,omitempty"`
}

// InitRepoRequest is the input for initializing the addons repository.
//
// AutoMerge is a pointer so callers can distinguish "not set" (fall back
// to connection-level PRAutoMerge default) from explicit true/false
// overrides. The init handler resolves it via resolveAutoMerge before
// deciding whether to merge the bootstrap PR.
type InitRepoRequest struct {
	BootstrapArgoCD bool   `json:"bootstrap_argocd"`
	AutoMerge       *bool  `json:"auto_merge,omitempty"`
	GitUsername     string `json:"git_username,omitempty"`
	GitToken        string `json:"git_token,omitempty"`
}

// InitRepoResult is the output of a successful repo initialization.
type InitRepoResult struct {
	Status string          `json:"status"`
	Repo   *InitRepoInfo   `json:"repo,omitempty"`
	ArgoCD *InitArgocdInfo `json:"argocd,omitempty"`
}

// InitRepoInfo holds Git repo details in the init response.
type InitRepoInfo struct {
	URL          string   `json:"url,omitempty"`
	Branch       string   `json:"branch,omitempty"`
	FilesCreated []string `json:"files_created"`
	PRUrl        string   `json:"pr_url,omitempty"`
	PRID         int      `json:"pr_id,omitempty"`
	Merged       bool     `json:"merged"`
}

// InitArgocdInfo holds ArgoCD bootstrap details in the init response.
type InitArgocdInfo struct {
	Bootstrapped bool   `json:"bootstrapped"`
	RootApp      string `json:"root_app,omitempty"`
	SyncStatus   string `json:"sync_status,omitempty"`
	SyncError    string `json:"sync_error,omitempty"`
}
