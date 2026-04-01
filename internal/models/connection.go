package models

import (
	"fmt"
	"net/url"
	"strings"
)

// GitProviderType identifies which Git provider to use.
type GitProviderType string

const (
	GitProviderGitHub      GitProviderType = "github"
	GitProviderAzureDevOps GitProviderType = "azuredevops"
)

// GitRepoConfig holds Git repository configuration.
type GitRepoConfig struct {
	Provider GitProviderType `json:"provider" yaml:"provider"`

	// URL-based input (parsed into owner/repo or org/project/repo)
	RepoURL string `json:"repo_url,omitempty" yaml:"repo_url,omitempty"`

	// GitHub fields
	Owner string `json:"owner,omitempty" yaml:"owner,omitempty"`
	Repo  string `json:"repo,omitempty" yaml:"repo,omitempty"`
	Token string `json:"token,omitempty" yaml:"token,omitempty"`

	// Azure DevOps fields
	Organization string `json:"organization,omitempty" yaml:"organization,omitempty"`
	Project      string `json:"project,omitempty" yaml:"project,omitempty"`
	Repository   string `json:"repository,omitempty" yaml:"repository,omitempty"`
	PAT          string `json:"pat,omitempty" yaml:"pat,omitempty"`
}

// ParseRepoURL populates provider, owner/repo (or org/project/repo) from a Git URL.
// Supports:
//   - https://github.com/owner/repo
//   - https://github.example.com/owner/repo (GitHub Enterprise)
//   - https://dev.azure.com/org/project/_git/repo
//   - https://org.visualstudio.com/project/_git/repo
func (g *GitRepoConfig) ParseRepoURL() error {
	if g.RepoURL == "" {
		return nil // nothing to parse, fields must be set directly
	}

	u, err := url.Parse(strings.TrimSuffix(g.RepoURL, ".git"))
	if err != nil {
		return fmt.Errorf("invalid git URL: %w", err)
	}

	host := strings.ToLower(u.Hostname())
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")

	// Azure DevOps: dev.azure.com/org/project/_git/repo
	if host == "dev.azure.com" {
		if len(parts) < 4 || parts[2] != "_git" {
			return fmt.Errorf("Azure DevOps URL must be https://dev.azure.com/org/project/_git/repo")
		}
		g.Provider = GitProviderAzureDevOps
		g.Organization = parts[0]
		g.Project = parts[1]
		g.Repository = parts[3]
		if g.Token != "" && g.PAT == "" {
			g.PAT = g.Token
		}
		return nil
	}

	// Azure DevOps: org.visualstudio.com/project/_git/repo
	if strings.HasSuffix(host, ".visualstudio.com") {
		g.Provider = GitProviderAzureDevOps
		g.Organization = strings.TrimSuffix(host, ".visualstudio.com")
		if len(parts) >= 3 && parts[1] == "_git" {
			g.Project = parts[0]
			g.Repository = parts[2]
		} else {
			return fmt.Errorf("Azure DevOps URL must be https://org.visualstudio.com/project/_git/repo")
		}
		if g.Token != "" && g.PAT == "" {
			g.PAT = g.Token
		}
		return nil
	}

	// GitHub (github.com or any other host = GitHub Enterprise)
	if len(parts) < 2 {
		return fmt.Errorf("Git URL must contain owner/repo (got: %s)", u.Path)
	}
	g.Provider = GitProviderGitHub
	g.Owner = parts[0]
	g.Repo = strings.Join(parts[1:], "/") // handle nested paths
	return nil
}

// ArgocdConfig holds ArgoCD connection configuration.
type ArgocdConfig struct {
	ServerURL string `json:"server_url" yaml:"server_url"`
	Token     string `json:"token,omitempty" yaml:"token,omitempty"`
	Namespace string `json:"namespace" yaml:"namespace"`
	Insecure  bool   `json:"insecure,omitempty" yaml:"insecure,omitempty"`
}

// Connection combines Git repo and ArgoCD settings.
type Connection struct {
	Name        string       `json:"name" yaml:"name"`
	Description string       `json:"description,omitempty" yaml:"description,omitempty"`
	Git         GitRepoConfig `json:"git" yaml:"git"`
	Argocd      ArgocdConfig `json:"argocd" yaml:"argocd"`
	IsDefault   bool         `json:"is_default" yaml:"default,omitempty"`
	CreatedAt   string       `json:"created_at,omitempty" yaml:"-"`
	UpdatedAt   string       `json:"updated_at,omitempty" yaml:"-"`
}

// ConnectionResponse is a connection with masked sensitive data for API responses.
type ConnectionResponse struct {
	Name              string          `json:"name"`
	Description       string          `json:"description,omitempty"`
	GitProvider       GitProviderType `json:"git_provider"`
	GitRepoIdentifier string          `json:"git_repo_identifier"`
	GitTokenMasked    string          `json:"git_token_masked"`
	ArgocdServerURL   string          `json:"argocd_server_url"`
	ArgocdTokenMasked string          `json:"argocd_token_masked"`
	ArgocdNamespace   string          `json:"argocd_namespace"`
	IsDefault         bool            `json:"is_default"`
	IsActive          bool            `json:"is_active"`
	CreatedAt         string          `json:"created_at,omitempty"`
	UpdatedAt         string          `json:"updated_at,omitempty"`
}

// ConnectionsListResponse is the API response for listing connections.
type ConnectionsListResponse struct {
	Connections      []ConnectionResponse `json:"connections"`
	ActiveConnection string               `json:"active_connection,omitempty"`
}

// CreateConnectionRequest is the API request to create a new connection.
type CreateConnectionRequest struct {
	Name         string       `json:"name"`
	Description  string       `json:"description,omitempty"`
	Git          GitRepoConfig `json:"git"`
	Argocd       ArgocdConfig `json:"argocd"`
	SetAsDefault bool         `json:"set_as_default"`
}

// SetActiveConnectionRequest is the API request to set the active connection.
type SetActiveConnectionRequest struct {
	ConnectionName string `json:"connection_name"`
}

// MaskToken masks a token/PAT for display, showing first 4 and last 4 characters.
func MaskToken(token string) string {
	if token == "" {
		return ""
	}
	if len(token) <= 8 {
		masked := make([]byte, len(token))
		for i := range masked {
			masked[i] = '*'
		}
		return string(masked)
	}
	middle := make([]byte, len(token)-8)
	for i := range middle {
		middle[i] = '*'
	}
	return token[:4] + string(middle) + token[len(token)-4:]
}

// ToResponse converts a Connection to a ConnectionResponse with masked tokens.
func (c *Connection) ToResponse(isActive bool) ConnectionResponse {
	repoID := ""
	token := ""
	switch c.Git.Provider {
	case GitProviderGitHub:
		repoID = c.Git.Owner + "/" + c.Git.Repo
		token = c.Git.Token
	case GitProviderAzureDevOps:
		repoID = c.Git.Organization + "/" + c.Git.Project + "/" + c.Git.Repository
		token = c.Git.PAT
	}

	return ConnectionResponse{
		Name:              c.Name,
		Description:       c.Description,
		GitProvider:       c.Git.Provider,
		GitRepoIdentifier: repoID,
		GitTokenMasked:    MaskToken(token),
		ArgocdServerURL:   c.Argocd.ServerURL,
		ArgocdTokenMasked: MaskToken(c.Argocd.Token),
		ArgocdNamespace:   c.Argocd.Namespace,
		IsDefault:         c.IsDefault,
		IsActive:          isActive,
		CreatedAt:         c.CreatedAt,
		UpdatedAt:         c.UpdatedAt,
	}
}
