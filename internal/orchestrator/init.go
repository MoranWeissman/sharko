package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// InitRepo scaffolds the addons repository from the embedded starter templates.
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
	err := fs.WalkDir(o.templateFS, "starter", func(path string, d fs.DirEntry, err error) error {
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

		// Strip the "starter/" prefix — files go to the repo root.
		repoPath := strings.TrimPrefix(path, "starter/")
		files[repoPath] = content
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking starter templates: %w", err)
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
		rootAppContent, readErr := fs.ReadFile(o.templateFS, "starter/bootstrap/root-app.yaml")
		if readErr != nil {
			result.ArgoCD = &InitArgocdInfo{
				Bootstrapped: false,
				RootApp:      fmt.Sprintf("failed to read root-app template: %v", readErr),
			}
			return result, nil
		}

		rootAppContent = replacePlaceholdersFull(rootAppContent, o.gitops, o.paths)

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
