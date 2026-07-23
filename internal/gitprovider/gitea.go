package gitprovider

import (
	"context"
	"fmt"
	"log/slog"
	"path"

	"code.gitea.io/sdk/gitea"
)

// GiteaProvider implements GitProvider for Gitea repositories.
type GiteaProvider struct {
	client *gitea.Client
	owner  string
	repo   string
}

// NewGiteaProvider creates a new Gitea-backed GitProvider.
// The baseURL should be the scheme+host (e.g. "https://gitea.example.com");
// the SDK appends /api/v1 automatically.
// The token is used for authentication.
func NewGiteaProvider(baseURL, owner, repo, token string) (*GiteaProvider, error) {
	client, err := gitea.NewClient(baseURL, gitea.SetToken(token))
	if err != nil {
		return nil, fmt.Errorf("create gitea client: %w", err)
	}
	return &GiteaProvider{
		client: client,
		owner:  owner,
		repo:   repo,
	}, nil
}

// Compile-time assertion that GiteaProvider implements GitProvider.
var _ GitProvider = (*GiteaProvider)(nil)

// ---------- Read operations ----------

// TestConnection verifies that the configured repository is accessible.
func (g *GiteaProvider) TestConnection(ctx context.Context) error {
	_, resp, err := g.client.GetRepo(g.owner, g.repo)
	if err != nil {
		return fmt.Errorf("test connection: %w", err)
	}
	if resp == nil {
		return fmt.Errorf("test connection: nil response")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("test connection: unexpected status %d", resp.StatusCode)
	}
	slog.Info("gitea connection ok", "owner", g.owner, "repo", g.repo)
	return nil
}

// GetFileContent retrieves the raw content of a single file at the given ref.
//
// When the path does not exist Gitea returns 404; the error is wrapped
// with gitprovider.ErrFileNotFound so callers can use errors.Is to detect the
// missing-file case (review finding H2).
func (g *GiteaProvider) GetFileContent(ctx context.Context, filePath, ref string) ([]byte, error) {
	content, resp, err := g.client.GetFile(g.owner, g.repo, ref, filePath)
	if err != nil {
		if resp != nil && resp.StatusCode == 404 {
			return nil, fmt.Errorf("get file content: path %q at ref %q: %w", filePath, ref, ErrFileNotFound)
		}
		return nil, fmt.Errorf("get file content: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("get file content: nil response")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get file content: unexpected status %d", resp.StatusCode)
	}
	slog.Info("gitea file fetched", "path", filePath, "ref", ref, "size", len(content))
	return content, nil
}

// ListDirectory returns the names of entries in a directory at the given ref.
func (g *GiteaProvider) ListDirectory(ctx context.Context, dirPath, ref string) ([]string, error) {
	contents, resp, err := g.client.ListContents(g.owner, g.repo, ref, dirPath)
	if err != nil {
		return nil, fmt.Errorf("list directory: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("list directory: nil response")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("list directory: unexpected status %d", resp.StatusCode)
	}

	names := make([]string, 0, len(contents))
	for _, entry := range contents {
		// Return just the basename, consistent with GitHub and AzureDevOps providers.
		names = append(names, path.Base(entry.Path))
	}
	return names, nil
}

// ListPullRequests returns pull requests filtered by state ("open", "closed", or "all").
func (g *GiteaProvider) ListPullRequests(ctx context.Context, state string) ([]PullRequest, error) {
	// Map generic state to Gitea StateType.
	var giteaState gitea.StateType
	switch state {
	case "open":
		giteaState = gitea.StateOpen
	case "closed":
		giteaState = gitea.StateClosed
	case "all":
		giteaState = gitea.StateAll
	default:
		slog.Warn("gitea: unknown PR state, defaulting to 'all'", "state", state)
		giteaState = gitea.StateAll
	}

	opt := gitea.ListPullRequestsOptions{
		State: giteaState,
	}

	prs, resp, err := g.client.ListRepoPullRequests(g.owner, g.repo, opt)
	if err != nil {
		return nil, fmt.Errorf("list pull requests: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("list pull requests: nil response")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("list pull requests: unexpected status %d", resp.StatusCode)
	}

	result := make([]PullRequest, 0, len(prs))
	for _, p := range prs {
		// Map Gitea PullRequest to the interface PullRequest.
		pr := PullRequest{
			ID:           int(p.Index), // Use Index (the PR number) as ID
			Title:        p.Title,
			Description:  p.Body,
			Author:       "", // Will be set below if Poster is not nil
			Status:       string(p.State),
			SourceBranch: "",
			TargetBranch: "",
			URL:          p.HTMLURL,
			CreatedAt:    "",
			UpdatedAt:    "",
			ClosedAt:     "",
		}

		if p.Poster != nil {
			pr.Author = p.Poster.UserName // Gitea SDK User struct field
		}
		if p.Base != nil {
			pr.TargetBranch = p.Base.Ref
		}
		if p.Head != nil {
			pr.SourceBranch = p.Head.Ref
		}
		if p.Created != nil {
			pr.CreatedAt = p.Created.String()
		}
		if p.Updated != nil {
			pr.UpdatedAt = p.Updated.String()
		}
		if p.Closed != nil {
			pr.ClosedAt = p.Closed.String()
		}

		result = append(result, pr)
	}

	return result, nil
}

// ---------- Write operations (stubs for Story 2) ----------

// CreateBranch creates a new branch from the given ref.
func (g *GiteaProvider) CreateBranch(ctx context.Context, branchName, fromRef string) error {
	return fmt.Errorf("gitea provider: CreateBranch not yet implemented (Story 2)")
}

// CreateOrUpdateFile creates or updates a file in the repository.
func (g *GiteaProvider) CreateOrUpdateFile(ctx context.Context, path string, content []byte, branch, commitMessage string) error {
	return fmt.Errorf("gitea provider: CreateOrUpdateFile not yet implemented (Story 2)")
}

// BatchCreateFiles writes multiple files in a single commit.
func (g *GiteaProvider) BatchCreateFiles(ctx context.Context, files map[string][]byte, branch, commitMessage string) error {
	return fmt.Errorf("gitea provider: BatchCreateFiles not yet implemented (Story 2)")
}

// DeleteFile deletes a file from the repository.
func (g *GiteaProvider) DeleteFile(ctx context.Context, path, branch, commitMessage string) error {
	return fmt.Errorf("gitea provider: DeleteFile not yet implemented (Story 2)")
}

// CreatePullRequest creates a new pull request.
func (g *GiteaProvider) CreatePullRequest(ctx context.Context, title, body, head, base string) (*PullRequest, error) {
	return nil, fmt.Errorf("gitea provider: CreatePullRequest not yet implemented (Story 2)")
}

// MergePullRequest merges a pull request.
func (g *GiteaProvider) MergePullRequest(ctx context.Context, prNumber int) error {
	return fmt.Errorf("gitea provider: MergePullRequest not yet implemented (Story 2)")
}

// GetPullRequestStatus retrieves the status of a pull request.
func (g *GiteaProvider) GetPullRequestStatus(ctx context.Context, prNumber int) (string, error) {
	return "", fmt.Errorf("gitea provider: GetPullRequestStatus not yet implemented (Story 2)")
}

// DeleteBranch deletes a branch from the repository.
func (g *GiteaProvider) DeleteBranch(ctx context.Context, branchName string) error {
	return fmt.Errorf("gitea provider: DeleteBranch not yet implemented (Story 2)")
}
