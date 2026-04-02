package orchestrator

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"strings"
)

// InitRepo scaffolds the addons repository from the embedded starter templates.
// It walks the templateFS, replaces placeholder tokens, and creates each file
// via the Git provider (either directly or through a PR branch).
func (o *Orchestrator) InitRepo(ctx context.Context, req InitRepoRequest) (*InitRepoResult, error) {
	if o.templateFS == nil {
		return nil, fmt.Errorf("template filesystem not configured")
	}
	if o.gitops.RepoURL == "" {
		return nil, fmt.Errorf("git repo URL is required for init — set SHARKO_GITOPS_REPO_URL")
	}

	branch := o.gitops.BaseBranch
	commitPrefix := o.gitops.CommitPrefix

	var filesCreated []string

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

		// Replace placeholder tokens with actual config values
		content = replacePlaceholders(content, o.gitops)

		// Strip the "starter/" prefix — files go to the repo root
		repoPath := strings.TrimPrefix(path, "starter/")

		commitMsg := fmt.Sprintf("%s init %s", commitPrefix, repoPath)
		if createErr := o.git.CreateOrUpdateFile(ctx, repoPath, content, branch, commitMsg); createErr != nil {
			return fmt.Errorf("creating %s: %w", repoPath, createErr)
		}

		filesCreated = append(filesCreated, repoPath)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking starter templates: %w", err)
	}

	result := &InitRepoResult{
		Status:       "success",
		FilesCreated: filesCreated,
	}

	if req.BootstrapArgoCD {
		// TODO: Apply root-app to ArgoCD via the ArgoCD client.
		// For now, the root-app.yaml is created in the repo but not applied.
		// The user must apply it manually: kubectl apply -f bootstrap/root-app.yaml
		result.ArgoCD = &struct {
			Bootstrapped bool   `json:"bootstrapped"`
			RootApp      string `json:"root_app,omitempty"`
		}{
			Bootstrapped: false,
			RootApp:      "addons-bootstrap (created in repo, apply manually)",
		}
	}

	return result, nil
}

// replacePlaceholders substitutes well-known tokens in template content.
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
