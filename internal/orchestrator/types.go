package orchestrator

import "github.com/MoranWeissman/sharko/internal/models"

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
	SecretPath string          `json:"secret_path,omitempty"`
	Addons     map[string]bool `json:"addons"`
	Region     string          `json:"region"`
}

// RegisterClusterResult is the output of a successful cluster registration.
type RegisterClusterResult struct {
	Status         string        `json:"status"` // "success" or "partial"
	Cluster        ClusterResult `json:"cluster"`
	Git            *GitResult    `json:"git,omitempty"`
	Secrets        []string      `json:"secrets_created,omitempty"` // names of created secrets
	CompletedSteps []string      `json:"completed_steps,omitempty"`
	FailedStep     string        `json:"failed_step,omitempty"`
	Error          string        `json:"error,omitempty"`
	Message        string        `json:"message,omitempty"`
	Adopted        bool          `json:"adopted,omitempty"` // true if cluster was already in ArgoCD

	// ArgoCD cluster secret outcome. Set by the API handler after calling Manager.Ensure().
	// Possible values: "created", "adopted", "updated", "skipped", "error".
	ArgoSecretStatus string `json:"argocd_secret_status,omitempty"`
	// ArgoSecretError holds the error message if the ArgoCD secret step failed (non-fatal).
	ArgoSecretError string `json:"argocd_secret_error,omitempty"`
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
