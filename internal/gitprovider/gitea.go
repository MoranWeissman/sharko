package gitprovider

import (
	"context"
	"encoding/base64"
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

// base64Encode encodes content to base64 string (required by Gitea SDK).
func base64Encode(content []byte) string {
	return base64.StdEncoding.EncodeToString(content)
}

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

// ---------- Write operations ----------

// CreateBranch creates a new branch from the given ref.
func (g *GiteaProvider) CreateBranch(ctx context.Context, branchName, fromRef string) error {
	opt := gitea.CreateBranchOption{
		BranchName:    branchName,
		OldBranchName: fromRef,
	}
	_, resp, err := g.client.CreateBranch(g.owner, g.repo, opt)
	if err != nil {
		return fmt.Errorf("create branch: %w", err)
	}
	if resp == nil {
		return fmt.Errorf("create branch: nil response")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("create branch: unexpected status %d", resp.StatusCode)
	}
	slog.Info("gitea branch created", "branch", branchName, "from", fromRef)
	return nil
}

// CreateOrUpdateFile creates a new file or updates an existing one on the given branch.
// It automatically decides between create and update by first checking if the file exists.
func (g *GiteaProvider) CreateOrUpdateFile(ctx context.Context, filePath string, content []byte, branch, commitMessage string) error {
	// First, check if the file exists to decide between create and update.
	existing, resp, err := g.client.GetContents(g.owner, g.repo, branch, filePath)
	if err != nil {
		// If 404, the file doesn't exist — use CreateFile.
		if resp != nil && resp.StatusCode == 404 {
			return g.createFile(ctx, filePath, content, branch, commitMessage)
		}
		return fmt.Errorf("create or update file: check existence: %w", err)
	}
	// File exists — use UpdateFile with the existing SHA.
	if existing == nil {
		return fmt.Errorf("create or update file: path %q is not a file", filePath)
	}
	return g.updateFile(ctx, filePath, content, branch, commitMessage, existing.SHA)
}

// createFile creates a new file using the Gitea SDK CreateFile.
func (g *GiteaProvider) createFile(ctx context.Context, filePath string, content []byte, branch, commitMessage string) error {
	encoded := base64Encode(content)
	opt := gitea.CreateFileOptions{
		FileOptions: gitea.FileOptions{
			Message:    commitMessage,
			BranchName: branch,
		},
		Content: encoded,
	}
	_, resp, err := g.client.CreateFile(g.owner, g.repo, filePath, opt)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	if resp == nil {
		return fmt.Errorf("create file: nil response")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("create file: unexpected status %d", resp.StatusCode)
	}
	slog.Info("gitea file created", "path", filePath, "branch", branch)
	return nil
}

// updateFile updates an existing file using the Gitea SDK UpdateFile.
func (g *GiteaProvider) updateFile(ctx context.Context, filePath string, content []byte, branch, commitMessage, sha string) error {
	encoded := base64Encode(content)
	opt := gitea.UpdateFileOptions{
		FileOptions: gitea.FileOptions{
			Message:    commitMessage,
			BranchName: branch,
		},
		SHA:     sha,
		Content: encoded,
	}
	_, resp, err := g.client.UpdateFile(g.owner, g.repo, filePath, opt)
	if err != nil {
		return fmt.Errorf("update file: %w", err)
	}
	if resp == nil {
		return fmt.Errorf("update file: nil response")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("update file: unexpected status %d", resp.StatusCode)
	}
	slog.Info("gitea file updated", "path", filePath, "branch", branch)
	return nil
}

// BatchCreateFiles writes multiple files in a single commit.
// Gitea lacks a native batch API, so this falls back to sequential CreateOrUpdateFile calls
// (one commit per file). The interface contract explicitly permits this.
func (g *GiteaProvider) BatchCreateFiles(ctx context.Context, files map[string][]byte, branch, commitMessage string) error {
	for filePath, content := range files {
		if err := g.CreateOrUpdateFile(ctx, filePath, content, branch, commitMessage); err != nil {
			return err
		}
	}
	slog.Info("gitea batch files written", "count", len(files), "branch", branch)
	return nil
}

// DeleteFile removes a file from the given branch.
func (g *GiteaProvider) DeleteFile(ctx context.Context, filePath, branch, commitMessage string) error {
	// Fetch the current file to get its SHA (required by Gitea).
	existing, resp, err := g.client.GetContents(g.owner, g.repo, branch, filePath)
	if err != nil {
		// If 404, the file doesn't exist. Match github_write.go semantics: error on missing file.
		if resp != nil && resp.StatusCode == 404 {
			return fmt.Errorf("delete file: file %q not found on branch %q", filePath, branch)
		}
		return fmt.Errorf("delete file: get SHA: %w", err)
	}
	if existing == nil {
		return fmt.Errorf("delete file: path %q is not a file", filePath)
	}

	opt := gitea.DeleteFileOptions{
		FileOptions: gitea.FileOptions{
			Message:    commitMessage,
			BranchName: branch,
		},
		SHA: existing.SHA,
	}
	resp, err = g.client.DeleteFile(g.owner, g.repo, filePath, opt)
	if err != nil {
		return fmt.Errorf("delete file: %w", err)
	}
	if resp == nil {
		return fmt.Errorf("delete file: nil response")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("delete file: unexpected status %d", resp.StatusCode)
	}
	slog.Info("gitea file deleted", "path", filePath, "branch", branch)
	return nil
}

// CreatePullRequest opens a new pull request.
func (g *GiteaProvider) CreatePullRequest(ctx context.Context, title, body, head, base string) (*PullRequest, error) {
	opt := gitea.CreatePullRequestOption{
		Title: title,
		Body:  body,
		Head:  head,
		Base:  base,
	}
	pr, resp, err := g.client.CreatePullRequest(g.owner, g.repo, opt)
	if err != nil {
		return nil, fmt.Errorf("create pull request: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("create pull request: nil response")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("create pull request: unexpected status %d", resp.StatusCode)
	}

	result := &PullRequest{
		ID:           int(pr.Index),
		Title:        pr.Title,
		Description:  pr.Body,
		Status:       "open",
		SourceBranch: head,
		TargetBranch: base,
		URL:          pr.HTMLURL,
	}
	if pr.Poster != nil {
		result.Author = pr.Poster.UserName
	}
	if pr.Created != nil {
		result.CreatedAt = pr.Created.Format("2006-01-02T15:04:05Z")
	}

	slog.Info("gitea pull request created", "number", result.ID, "url", result.URL)
	return result, nil
}

// MergePullRequest merges an open pull request by number.
func (g *GiteaProvider) MergePullRequest(ctx context.Context, prNumber int) error {
	opt := gitea.MergePullRequestOption{
		Style: gitea.MergeStyleMerge,
	}
	_, resp, err := g.client.MergePullRequest(g.owner, g.repo, int64(prNumber), opt)
	if err != nil {
		return fmt.Errorf("merge pull request #%d: %w", prNumber, err)
	}
	if resp == nil {
		return fmt.Errorf("merge pull request: nil response")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("merge pull request: unexpected status %d", resp.StatusCode)
	}
	slog.Info("gitea pull request merged", "number", prNumber)
	return nil
}

// GetPullRequestStatus returns the status of a pull request: "open", "merged", or "closed".
//
// When Gitea returns HTTP 404 the pull request no longer exists (it was deleted,
// or the repository was recreated and the old PR number is gone). In that case
// the error is wrapped with gitprovider.ErrPullRequestNotFound so callers can use
// errors.Is to distinguish a definitively-gone PR from a transient/auth failure.
// Only a 404 maps to the sentinel — every other error stays generic so the caller
// keeps retrying.
func (g *GiteaProvider) GetPullRequestStatus(ctx context.Context, prNumber int) (string, error) {
	pr, resp, err := g.client.GetPullRequest(g.owner, g.repo, int64(prNumber))
	if err != nil {
		if resp != nil && resp.StatusCode == 404 {
			return "", fmt.Errorf("get pull request #%d: %w", prNumber, ErrPullRequestNotFound)
		}
		return "", fmt.Errorf("get pull request #%d: %w", prNumber, err)
	}
	if resp == nil {
		return "", fmt.Errorf("get pull request: nil response")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("get pull request: unexpected status %d", resp.StatusCode)
	}

	// Match GitHub + AzureDevOps vocabulary: "merged", "closed", "open".
	if pr.HasMerged {
		return "merged", nil
	}
	if pr.State == gitea.StateClosed {
		return "closed", nil
	}
	return "open", nil
}

// DeleteBranch removes a branch by name.
func (g *GiteaProvider) DeleteBranch(ctx context.Context, branchName string) error {
	_, resp, err := g.client.DeleteRepoBranch(g.owner, g.repo, branchName)
	if err != nil {
		return fmt.Errorf("delete branch %q: %w", branchName, err)
	}
	if resp == nil {
		return fmt.Errorf("delete branch: nil response")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("delete branch: unexpected status %d", resp.StatusCode)
	}
	slog.Info("gitea branch deleted", "branch", branchName)
	return nil
}
