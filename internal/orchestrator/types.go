package orchestrator

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
	Charts          string // e.g. "charts/"
	Bootstrap       string // e.g. "bootstrap/"
	HostClusterName string // e.g. "management" — the cluster running ArgoCD (uses in-cluster)
}

// RegisterClusterRequest is the input for cluster registration.
type RegisterClusterRequest struct {
	Name   string          `json:"name"`
	Addons map[string]bool `json:"addons"`
	Region string          `json:"region"`
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
	Name      string `json:"name"`
	Chart     string `json:"chart"`
	RepoURL   string `json:"repo_url"`
	Version   string `json:"version"`
	Namespace string `json:"namespace"`
	SyncWave  int    `json:"sync_wave,omitempty"`
}

// InitRepoRequest is the input for initializing the addons repository.
type InitRepoRequest struct {
	BootstrapArgoCD bool   `json:"bootstrap_argocd"`
	GitUsername      string `json:"git_username,omitempty"`
	GitToken         string `json:"git_token,omitempty"`
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
