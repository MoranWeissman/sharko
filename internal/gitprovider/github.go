package gitprovider

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/go-github/v68/github"
	"golang.org/x/oauth2"
)

// GitHubProvider implements GitProvider using the GitHub API.
type GitHubProvider struct {
	client *github.Client
	owner  string
	repo   string
}

// NewGitHubProvider creates a new GitHub-backed GitProvider.
func NewGitHubProvider(owner, repo, token string) *GitHubProvider {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(context.Background(), ts)

	return &GitHubProvider{
		client: github.NewClient(tc),
		owner:  owner,
		repo:   repo,
	}
}

// GetFileContent retrieves the raw content of a single file at the given ref.
func (g *GitHubProvider) GetFileContent(ctx context.Context, path, ref string) ([]byte, error) {
	opts := &github.RepositoryContentGetOptions{Ref: ref}

	fileContent, _, resp, err := g.client.Repositories.GetContents(ctx, g.owner, g.repo, path, opts)
	if err != nil {
		slog.Error("github get file content failed", "error", err, "path", path, "ref", ref)
		return nil, fmt.Errorf("get file content: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get file content: unexpected status %d", resp.StatusCode)
	}
	if fileContent == nil {
		return nil, fmt.Errorf("get file content: path %q is not a file", path)
	}

	content, err := fileContent.GetContent()
	if err != nil {
		return nil, fmt.Errorf("decode file content: %w", err)
	}
	slog.Info("github file fetched", "path", path, "ref", ref, "size", len(content))
	return []byte(content), nil
}

// ListDirectory returns the names of entries in a directory at the given ref.
func (g *GitHubProvider) ListDirectory(ctx context.Context, path, ref string) ([]string, error) {
	opts := &github.RepositoryContentGetOptions{Ref: ref}

	_, dirContents, resp, err := g.client.Repositories.GetContents(ctx, g.owner, g.repo, path, opts)
	if err != nil {
		return nil, fmt.Errorf("list directory: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("list directory: unexpected status %d", resp.StatusCode)
	}
	if dirContents == nil {
		return nil, fmt.Errorf("list directory: path %q is not a directory", path)
	}

	names := make([]string, 0, len(dirContents))
	for _, entry := range dirContents {
		names = append(names, entry.GetName())
	}
	return names, nil
}

// ListPullRequests returns pull requests filtered by state ("open", "closed", or "all").
func (g *GitHubProvider) ListPullRequests(ctx context.Context, state string) ([]PullRequest, error) {
	opts := &github.PullRequestListOptions{
		State: state,
		ListOptions: github.ListOptions{
			PerPage: 100,
		},
	}

	var allPRs []PullRequest

	for {
		prs, resp, err := g.client.PullRequests.List(ctx, g.owner, g.repo, opts)
		if err != nil {
			slog.Error("github list pull requests failed", "error", err, "state", state)
			return nil, fmt.Errorf("list pull requests: %w", err)
		}

		for _, pr := range prs {
			pullRequest := PullRequest{
				ID:           pr.GetNumber(),
				Title:        pr.GetTitle(),
				Description:  pr.GetBody(),
				Author:       pr.GetUser().GetLogin(),
				SourceBranch: pr.GetHead().GetRef(),
				TargetBranch: pr.GetBase().GetRef(),
				URL:          pr.GetHTMLURL(),
			}

			switch {
			case pr.GetMerged():
				pullRequest.Status = "merged"
			case pr.GetState() == "closed":
				pullRequest.Status = "closed"
			default:
				pullRequest.Status = "open"
			}

			if t := pr.GetCreatedAt(); !t.IsZero() {
				pullRequest.CreatedAt = t.Format("2006-01-02T15:04:05Z")
			}
			if t := pr.GetUpdatedAt(); !t.IsZero() {
				pullRequest.UpdatedAt = t.Format("2006-01-02T15:04:05Z")
			}
			if t := pr.GetClosedAt(); !t.IsZero() {
				pullRequest.ClosedAt = t.Format("2006-01-02T15:04:05Z")
			}

			allPRs = append(allPRs, pullRequest)
		}

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	slog.Info("github pull requests listed", "state", state, "count", len(allPRs))
	return allPRs, nil
}

// TestConnection verifies that the configured repository is accessible.
func (g *GitHubProvider) TestConnection(ctx context.Context) error {
	_, resp, err := g.client.Repositories.Get(ctx, g.owner, g.repo)
	if err != nil {
		if resp != nil {
			switch resp.StatusCode {
			case 401:
				return fmt.Errorf("invalid GitHub token — check that the token is correct and not expired")
			case 403:
				return fmt.Errorf("GitHub access denied — the token does not have permission for this repository")
			case 404:
				return fmt.Errorf("GitHub repository not found — check the URL, or the token may not have access to %s/%s", g.owner, g.repo)
			}
		}
		return fmt.Errorf("GitHub connection failed: %w", err)
	}
	return nil
}
