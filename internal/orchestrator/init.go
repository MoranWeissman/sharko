package orchestrator

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// InitRepo scaffolds the addons repository from the embedded bootstrap templates.
// It collects all files from templateFS, commits them via a PR (using commitChanges),
// optionally registers the repo in ArgoCD, creates the bootstrap project/application,
// and polls for sync verification.
func (o *Orchestrator) InitRepo(ctx context.Context, req InitRepoRequest) (*InitRepoResult, error) {
	if o.templateFS == nil {
		return nil, fmt.Errorf("template filesystem not configured")
	}
	if o.gitops.RepoURL == "" {
		return nil, fmt.Errorf("git repo URL is required for init — set SHARKO_GITOPS_REPO_URL")
	}

	// Step 1 — Check if repo is already initialized.
	if _, err := o.git.GetFileContent(ctx, "bootstrap/root-app.yaml", o.gitops.BaseBranch); err == nil {
		return nil, fmt.Errorf("repo already initialized: bootstrap/root-app.yaml exists")
	}

	// Step 2 — Collect all files from templates.
	files := make(map[string][]byte)
	err := fs.WalkDir(o.templateFS, "bootstrap", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		content, readErr := fs.ReadFile(o.templateFS, path)
		if readErr != nil {
			return fmt.Errorf("reading template %s: %w", path, readErr)
		}

		// Replace placeholder tokens with actual config values.
		content = replacePlaceholdersFull(content, o.gitops, o.paths)

		// Keep bootstrap/ prefix for Helm chart files (Chart.yaml, templates/)
		// Strip prefix for repo-root files (configuration/, README, root-app, repository-secret)
		repoPath := path
		if !strings.HasPrefix(path, "bootstrap/Chart.yaml") &&
			!strings.HasPrefix(path, "bootstrap/templates/") {
			repoPath = strings.TrimPrefix(path, "bootstrap/")
		}
		files[repoPath] = content
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking bootstrap templates: %w", err)
	}

	// Step 3 — Commit all files via PR (using commitChanges with shared mutex).
	gitResult, err := o.commitChanges(ctx, files, nil, "initialize repository")
	if err != nil {
		return nil, fmt.Errorf("committing init files: %w", err)
	}

	filesCreated := make([]string, 0, len(files))
	for path := range files {
		filesCreated = append(filesCreated, path)
	}

	result := &InitRepoResult{
		Status: "success",
		Repo: &InitRepoInfo{
			URL:          o.gitops.RepoURL,
			Branch:       gitResult.Branch,
			FilesCreated: filesCreated,
			PRUrl:        gitResult.PRUrl,
			PRID:         gitResult.PRID,
			Merged:       gitResult.Merged,
		},
	}

	if req.BootstrapArgoCD && o.argocd != nil {
		// Step 4 — Add repository to ArgoCD.
		if req.GitUsername != "" && req.GitToken != "" {
			if addRepoErr := o.argocd.AddRepository(ctx, o.gitops.RepoURL, req.GitUsername, req.GitToken); addRepoErr != nil {
				result.ArgoCD = &InitArgocdInfo{
					Bootstrapped: false,
					RootApp:      fmt.Sprintf("failed to add repository to ArgoCD: %v", addRepoErr),
				}
				result.Status = "partial"
				return result, nil
			}
		}

		// Step 5 & 6 — Create AppProject and Application from root-app.yaml.
		rootAppContent, readErr := fs.ReadFile(o.templateFS, "bootstrap/root-app.yaml")
		if readErr != nil {
			result.ArgoCD = &InitArgocdInfo{
				Bootstrapped: false,
				RootApp:      fmt.Sprintf("failed to read root-app template: %v", readErr),
			}
			return result, nil
		}

		rootAppContent = replaceForBootstrap(rootAppContent, o.gitops, o.paths)

		bootstrapErr := o.bootstrapArgoCD(ctx, rootAppContent)
		if bootstrapErr != nil {
			result.ArgoCD = &InitArgocdInfo{
				Bootstrapped: false,
				RootApp:      fmt.Sprintf("bootstrap failed: %v", bootstrapErr),
			}
			result.Status = "partial"
			return result, nil
		}

		result.ArgoCD = &InitArgocdInfo{
			Bootstrapped: true,
			RootApp:      "addons-bootstrap",
		}

		// Step 7 — Poll for sync verification (up to 2 minutes).
		syncStatus, syncErr := o.waitForSync(ctx, "addons-bootstrap", 2*time.Minute)
		result.ArgoCD.SyncStatus = syncStatus
		result.ArgoCD.SyncError = syncErr
		if syncStatus != "synced" {
			result.Status = "syncing"
		}
	}

	return result, nil
}

// waitForSync polls ArgoCD for an application's sync/health status.
// Returns the final status ("synced", "failed", "timeout") and an optional error message.
func (o *Orchestrator) waitForSync(ctx context.Context, appName string, timeout time.Duration) (string, string) {
	check := func() (string, string, bool) {
		app, err := o.argocd.GetApplication(ctx, appName)
		if err != nil {
			return "", "", false
		}
		if app.SyncStatus == "Synced" && app.HealthStatus == "Healthy" {
			return "synced", "", true
		}
		if app.SyncStatus == "OutOfSync" && app.HealthStatus == "Degraded" {
			return "failed", "application sync failed", true
		}
		return "", "", false
	}

	// Immediate first check before entering the polling loop.
	if status, msg, done := check(); done {
		return status, msg
	}

	deadline := time.After(timeout)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			return "timeout", "sync verification timed out after " + timeout.String()
		case <-ctx.Done():
			return "timeout", "context cancelled"
		case <-ticker.C:
			if status, msg, done := check(); done {
				return status, msg
			}
		}
	}
}

// bootstrapArgoCD parses the root-app.yaml (multi-document YAML with AppProject + Application)
// and applies each resource to ArgoCD via the API.
func (o *Orchestrator) bootstrapArgoCD(ctx context.Context, rootAppYAML []byte) error {
	// Split on YAML document separator
	docs := bytes.Split(rootAppYAML, []byte("\n---"))

	for _, doc := range docs {
		doc = bytes.TrimSpace(doc)
		if len(doc) == 0 {
			continue
		}

		// Parse YAML to determine the kind
		var resource map[string]interface{}
		if err := yaml.Unmarshal(doc, &resource); err != nil {
			return fmt.Errorf("parsing YAML document: %w", err)
		}

		kind, _ := resource["kind"].(string)

		// Convert to JSON for the ArgoCD REST API
		jsonData, err := json.Marshal(resource)
		if err != nil {
			return fmt.Errorf("converting %s to JSON: %w", kind, err)
		}

		switch kind {
		case "AppProject":
			if err := o.argocd.CreateProject(ctx, jsonData); err != nil {
				return fmt.Errorf("creating AppProject: %w", err)
			}
		case "Application":
			if err := o.argocd.CreateApplication(ctx, jsonData); err != nil {
				return fmt.Errorf("creating Application: %w", err)
			}
		default:
			return fmt.Errorf("unexpected resource kind %q in root-app.yaml", kind)
		}
	}

	return nil
}

// ─── Exported step-by-step helpers for the async init flow ───────────────────
//
// These methods expose individual phases of the init workflow so that the API
// handler can drive them one step at a time, recording progress in the
// operations store between each step.

// CollectBootstrapFiles walks the templateFS and returns the ready-to-commit
// file map (placeholder tokens already substituted).
func (o *Orchestrator) CollectBootstrapFiles(_ context.Context) (map[string][]byte, error) {
	if o.templateFS == nil {
		return nil, fmt.Errorf("template filesystem not configured")
	}
	if o.gitops.RepoURL == "" {
		return nil, fmt.Errorf("git repo URL is required — set SHARKO_GITOPS_REPO_URL")
	}

	files := make(map[string][]byte)
	err := fs.WalkDir(o.templateFS, "bootstrap", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		content, readErr := fs.ReadFile(o.templateFS, path)
		if readErr != nil {
			return fmt.Errorf("reading template %s: %w", path, readErr)
		}
		content = replacePlaceholdersFull(content, o.gitops, o.paths)
		// Keep bootstrap/ prefix for Helm chart files (Chart.yaml, templates/)
		// Strip prefix for repo-root files (configuration/, README, root-app, repository-secret)
		repoPath := path
		if !strings.HasPrefix(path, "bootstrap/Chart.yaml") &&
			!strings.HasPrefix(path, "bootstrap/templates/") {
			repoPath = strings.TrimPrefix(path, "bootstrap/")
		}
		files[repoPath] = content
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking bootstrap templates: %w", err)
	}
	return files, nil
}

// CommitBootstrapFiles creates a uniquely-named branch and commits the given
// files to it. Returns the branch name. Does NOT create a PR.
func (o *Orchestrator) CommitBootstrapFiles(ctx context.Context, files map[string][]byte) (string, error) {
	if o.gitMu != nil {
		o.gitMu.Lock()
		defer o.gitMu.Unlock()
	}

	o.detectConflicts(ctx, files)

	branchName := fmt.Sprintf("%sinitialize-repository-%s", o.gitops.BranchPrefix, initBranchSuffix())

	if err := o.git.CreateBranch(ctx, branchName, o.gitops.BaseBranch); err != nil {
		return "", fmt.Errorf("creating branch %q: %w", branchName, err)
	}
	commitMsg := fmt.Sprintf("%s initialize repository", o.gitops.CommitPrefix)
	if err := o.git.BatchCreateFiles(ctx, files, branchName, commitMsg); err != nil {
		return "", fmt.Errorf("writing files on branch %q: %w", branchName, err)
	}
	return branchName, nil
}

// CreateInitPR opens a pull request for the given branch against the base branch.
// The caller is responsible for merging (or waiting for a human to merge).
func (o *Orchestrator) CreateInitPR(ctx context.Context, branch string) (*GitResult, error) {
	title := fmt.Sprintf("%s initialize repository", o.gitops.CommitPrefix)
	pr, err := o.git.CreatePullRequest(ctx, title, "initialize repository", branch, o.gitops.BaseBranch)
	if err != nil {
		return nil, fmt.Errorf("creating pull request: %w", err)
	}
	return &GitResult{
		PRUrl:  pr.URL,
		PRID:   pr.ID,
		Branch: branch,
	}, nil
}

// ReadRootAppTemplate reads and renders the root-app.yaml bootstrap template.
func (o *Orchestrator) ReadRootAppTemplate(_ context.Context) ([]byte, error) {
	if o.templateFS == nil {
		return nil, fmt.Errorf("template filesystem not configured")
	}
	content, err := fs.ReadFile(o.templateFS, "bootstrap/root-app.yaml")
	if err != nil {
		return nil, fmt.Errorf("reading root-app template: %w", err)
	}
	return replaceForBootstrap(content, o.gitops, o.paths), nil
}

// BootstrapArgoCD is the exported counterpart of bootstrapArgoCD.
func (o *Orchestrator) BootstrapArgoCD(ctx context.Context, rootAppYAML []byte) error {
	return o.bootstrapArgoCD(ctx, rootAppYAML)
}

// WaitForSync is the exported counterpart of waitForSync.
func (o *Orchestrator) WaitForSync(ctx context.Context, appName string, timeout time.Duration) (string, string) {
	return o.waitForSync(ctx, appName, timeout)
}

// initBranchSuffix returns a short random hex string for init branch name uniqueness.
func initBranchSuffix() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// replacePlaceholders substitutes well-known tokens in template content.
// Tokens replaced:
//   - SHARKO_GIT_REPO_URL  → cfg.RepoURL
//   - SHARKO_GIT_BRANCH    → cfg.BaseBranch (default "main")
//   - SHARKO_HOST_CLUSTER_NAME → repoPaths.HostClusterName (if set; otherwise token removed)
func replacePlaceholders(content []byte, cfg GitOpsConfig) []byte {
	if cfg.RepoURL != "" {
		content = bytes.ReplaceAll(content, []byte("SHARKO_GIT_REPO_URL"), []byte(cfg.RepoURL))
	}
	branch := cfg.BaseBranch
	if branch == "" {
		branch = "main"
	}
	content = bytes.ReplaceAll(content, []byte("SHARKO_GIT_BRANCH"), []byte(branch))
	return content
}

// replacePlaceholdersFull extends replacePlaceholders with repo-path tokens including
// the host cluster name used for in-cluster routing.
func replacePlaceholdersFull(content []byte, cfg GitOpsConfig, paths RepoPathsConfig) []byte {
	content = replacePlaceholders(content, cfg)
	// Always replace — if unset, use empty string so no cluster matches the in-cluster condition.
	content = bytes.ReplaceAll(content, []byte("SHARKO_HOST_CLUSTER_NAME"), []byte(paths.HostClusterName))
	return content
}

// replaceForBootstrap extends replacePlaceholdersFull by also substituting Helm template
// expressions present in root-app.yaml. Those expressions must remain intact in the
// Git-committed copy (ArgoCD renders them at deploy time), but when Sharko reads the
// file directly to call the ArgoCD API it needs plain YAML without unresolved {{ }} syntax.
func replaceForBootstrap(content []byte, cfg GitOpsConfig, paths RepoPathsConfig) []byte {
	content = replacePlaceholdersFull(content, cfg, paths)
	if cfg.RepoURL != "" {
		content = bytes.ReplaceAll(content, []byte("{{ .Values.repoURL }}"), []byte(cfg.RepoURL))
	}
	branch := cfg.BaseBranch
	if branch == "" {
		branch = "main"
	}
	content = bytes.ReplaceAll(content, []byte("{{ .Values.targetRevision }}"), []byte(branch))
	content = bytes.ReplaceAll(content, []byte("{{ .Values.hostCluster.name }}"), []byte(paths.HostClusterName))
	return content
}
