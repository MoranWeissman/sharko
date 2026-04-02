package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"

	"gopkg.in/yaml.v3"
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

	// Check if repo is already initialized by looking for bootstrap/root-app.yaml.
	if _, err := o.git.GetFileContent(ctx, "bootstrap/root-app.yaml", o.gitops.BaseBranch); err == nil {
		return nil, fmt.Errorf("repo already initialized: bootstrap/root-app.yaml exists")
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
		Status: "success",
		Repo: &InitRepoInfo{
			URL:          o.gitops.RepoURL,
			Branch:       branch,
			FilesCreated: filesCreated,
		},
	}

	if req.BootstrapArgoCD && o.argocd != nil {
		rootAppContent, readErr := fs.ReadFile(o.templateFS, "starter/bootstrap/root-app.yaml")
		if readErr != nil {
			result.ArgoCD = &InitArgocdInfo{
				Bootstrapped: false,
				RootApp:      fmt.Sprintf("failed to read root-app template: %v", readErr),
			}
			return result, nil
		}

		// Replace placeholders in the root-app content
		rootAppContent = replacePlaceholders(rootAppContent, o.gitops)

		// Split multi-document YAML (AppProject + Application)
		bootstrapErr := o.bootstrapArgoCD(ctx, rootAppContent)
		if bootstrapErr != nil {
			result.ArgoCD = &InitArgocdInfo{
				Bootstrapped: false,
				RootApp:      fmt.Sprintf("bootstrap failed: %v", bootstrapErr),
			}
			return result, nil
		}

		result.ArgoCD = &InitArgocdInfo{
			Bootstrapped: true,
			RootApp:      "addons-bootstrap",
		}
	}

	return result, nil
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
