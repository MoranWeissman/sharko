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

// RegisterClusterRequest is the input for cluster registration.
type RegisterClusterRequest struct {
	Name       string          `json:"name"`
	Provider   string          `json:"provider,omitempty"`
	SecretPath string          `json:"secret_path,omitempty"`
	Addons     map[string]bool `json:"addons"`
	Region     string          `json:"region"`
	DryRun     bool            `json:"dry_run,omitempty"`
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
}

// DryRunResult holds the preview information returned when dry_run=true.
// No writes (Git, ArgoCD, secrets) are performed.
type DryRunResult struct {
	EffectiveAddons []string      `json:"effective_addons"`
	FilesToWrite    []FilePreview `json:"files_to_write"`
	PRTitle         string        `json:"pr_title"`
	SecretsToCreate []string      `json:"secrets_to_create"`
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
}

// AddAddonRequest is the input for adding an addon to the catalog.
type AddAddonRequest struct {
	Name              string                   `json:"name"`
	Chart             string                   `json:"chart"`
	RepoURL           string                   `json:"repo_url"`
	Version           string                   `json:"version"`
	Namespace         string                   `json:"namespace"`
	SyncWave          int                      `json:"sync_wave,omitempty"`
	SelfHeal          *bool                    `json:"self_heal,omitempty"`
	SyncOptions       []string                 `json:"sync_options,omitempty"`
	AdditionalSources []models.AddonSource     `json:"additional_sources,omitempty"`
	IgnoreDifferences []map[string]interface{} `json:"ignore_differences,omitempty"`
	ExtraHelmValues   map[string]string        `json:"extra_helm_values,omitempty"`
	DependsOn         []string                 `json:"depends_on,omitempty"`
	// Source identifies the originating UI flow for audit/observability.
	// Optional. Examples: "marketplace" (curated catalog Configure modal),
	// "manual" (raw Add Addon form), "" (caller didn't say — handler treats
	// as "manual" for the audit detail).
	Source string `json:"source,omitempty"`

	// UpstreamValues is the raw chart `values.yaml` bytes that the API
	// handler pre-fetched. When non-empty, AddAddon runs the smart-values
	// pipeline (V121-6) and writes an annotated global values file with a
	// per-cluster template block. When empty, AddAddon falls back to the
	// pre-v1.21 minimal stub (`<name>:\n  enabled: false`). Not part of
	// the wire schema — handlers populate it after `helm.FetchValues` and
	// the smart-parser layer.
	UpstreamValues []byte `json:"-"`
}

// ConfigureAddonRequest is the input for updating an addon's catalog configuration.
type ConfigureAddonRequest struct {
	Name              string                   `json:"name"`
	Version           string                   `json:"version,omitempty"`
	SyncWave          *int                     `json:"sync_wave,omitempty"`
	SelfHeal          *bool                    `json:"self_heal,omitempty"`
	SyncOptions       []string                 `json:"sync_options,omitempty"`
	AdditionalSources []models.AddonSource     `json:"additional_sources,omitempty"`
	IgnoreDifferences []map[string]interface{} `json:"ignore_differences,omitempty"`
	ExtraHelmValues   map[string]string        `json:"extra_helm_values,omitempty"`
}

// AdoptClustersRequest is the input for adopting existing ArgoCD clusters.
type AdoptClustersRequest struct {
	Clusters  []string `json:"clusters"`
	AutoMerge bool     `json:"auto_merge"` // override per-request; if false, PRs are left open
	DryRun    bool     `json:"dry_run,omitempty"`
}

// AdoptClusterResult holds the outcome for a single cluster adoption.
type AdoptClusterResult struct {
	Name         string        `json:"name"`
	Status       string        `json:"status"` // "success", "partial", "failed", "skipped"
	Verification *verify.Result `json:"verification,omitempty"`
	Git          *GitResult    `json:"git,omitempty"`
	Error        string        `json:"error,omitempty"`
	Message      string        `json:"message,omitempty"`
	DryRun       *DryRunResult `json:"dry_run,omitempty"`
}

// AdoptClustersResult is the aggregate response from adopting multiple clusters.
type AdoptClustersResult struct {
	Results []AdoptClusterResult `json:"results"`
}

// UnadoptClusterRequest is the input for un-adopting a cluster.
type UnadoptClusterRequest struct {
	Yes    bool `json:"yes"`
	DryRun bool `json:"dry_run,omitempty"`
}

// UnadoptClusterResult is the output of an un-adopt operation.
type UnadoptClusterResult struct {
	Name    string     `json:"name"`
	Status  string     `json:"status"` // "success", "partial", "failed"
	Git     *GitResult `json:"git,omitempty"`
	Error   string     `json:"error,omitempty"`
	Message string     `json:"message,omitempty"`
	DryRun  *DryRunResult `json:"dry_run,omitempty"`
}

// RemoveClusterRequest is the input for cluster removal with configurable cleanup.
type RemoveClusterRequest struct {
	Name    string `json:"name"`
	Cleanup string `json:"cleanup"` // "all" (default), "git", "none"
	DryRun  bool   `json:"dry_run,omitempty"`
	Yes     bool   `json:"yes"` // confirmation required
}

// RemoveClusterResult is the output of a cluster removal operation.
type RemoveClusterResult struct {
	Name           string     `json:"name"`
	Status         string     `json:"status"` // "success", "partial", "failed"
	Cleanup        string     `json:"cleanup"`
	Git            *GitResult `json:"git,omitempty"`
	CompletedSteps []string   `json:"completed_steps,omitempty"`
	FailedStep     string     `json:"failed_step,omitempty"`
	Error          string     `json:"error,omitempty"`
	Message        string     `json:"message,omitempty"`
	DryRun         *DryRunResult `json:"dry_run,omitempty"`
}

// DisableAddonRequest is the input for disabling an addon on a cluster.
type DisableAddonRequest struct {
	Cluster string `json:"cluster"`
	Addon   string `json:"addon"`
	Cleanup string `json:"cleanup"` // "all" (default), "labels", "none"
	DryRun  bool   `json:"dry_run,omitempty"`
	Yes     bool   `json:"yes"` // confirmation required
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
type InitRepoRequest struct {
	BootstrapArgoCD bool   `json:"bootstrap_argocd"`
	AutoMerge       bool   `json:"auto_merge"`
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
