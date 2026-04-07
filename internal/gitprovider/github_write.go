package gitprovider

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/google/go-github/v68/github"
)

// getContentsRaw fetches the RepositoryContent for a single file, returning
// nil when the file does not exist (404).
func (g *GitHubProvider) getContentsRaw(ctx context.Context, path, ref string) (*github.RepositoryContent, error) {
	opts := &github.RepositoryContentGetOptions{Ref: ref}
	fileContent, _, resp, err := g.client.Repositories.GetContents(ctx, g.owner, g.repo, path, opts)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("get contents raw: %w", err)
	}
	if fileContent == nil {
		return nil, fmt.Errorf("get contents raw: path %q is not a file", path)
	}
	return fileContent, nil
}

// CreateBranch creates a new branch from the given ref (branch name or SHA).
// If the repository is empty (no commits yet), it initialises the repo with an
// initial commit on fromRef before creating the target branch.
func (g *GitHubProvider) CreateBranch(ctx context.Context, branchName, fromRef string) error {
	// Resolve fromRef to a SHA.
	ref, _, err := g.client.Git.GetRef(ctx, g.owner, g.repo, "refs/heads/"+fromRef)
	if err != nil {
		// If the base branch doesn't exist the repo is empty — bootstrap it.
		if ghErr, ok := err.(*github.ErrorResponse); ok && ghErr.Response.StatusCode == http.StatusNotFound {
			slog.Info("base branch not found, initializing empty repo", "branch", fromRef)
			sha, initErr := g.initEmptyRepo(ctx, fromRef)
			if initErr != nil {
				return fmt.Errorf("create branch: init empty repo: %w", initErr)
			}
			newRef := &github.Reference{
				Ref:    github.Ptr("refs/heads/" + branchName),
				Object: &github.GitObject{SHA: github.Ptr(sha)},
			}
			_, _, err = g.client.Git.CreateRef(ctx, g.owner, g.repo, newRef)
			if err != nil {
				return fmt.Errorf("create branch after init: %w", err)
			}
			slog.Info("github branch created (after repo init)", "branch", branchName, "from", fromRef)
			return nil
		}
		return fmt.Errorf("create branch: get ref %q: %w", fromRef, err)
	}

	newRef := &github.Reference{
		Ref:    github.Ptr("refs/heads/" + branchName),
		Object: &github.GitObject{SHA: ref.Object.SHA},
	}

	_, _, err = g.client.Git.CreateRef(ctx, g.owner, g.repo, newRef)
	if err != nil {
		return fmt.Errorf("create branch: create ref %q: %w", branchName, err)
	}

	slog.Info("github branch created", "branch", branchName, "from", fromRef)
	return nil
}

// initEmptyRepo bootstraps an empty GitHub repository by creating an initial
// README commit on the given branch via the Contents API.  It returns the SHA
// of the new commit so the caller can branch from it.
func (g *GitHubProvider) initEmptyRepo(ctx context.Context, branchName string) (string, error) {
	author := &github.CommitAuthor{
		Name:  github.Ptr("Sharko Bot"),
		Email: github.Ptr("sharko-bot@users.noreply.github.com"),
	}
	_, _, err := g.client.Repositories.CreateFile(ctx, g.owner, g.repo, "README.md", &github.RepositoryContentFileOptions{
		Message:   github.Ptr("Initial commit"),
		Content:   []byte("# Sharko Addons Repository\n\nManaged by [Sharko](https://github.com/MoranWeissman/sharko).\n"),
		Author:    author,
		Committer: author,
	})
	if err != nil {
		return "", fmt.Errorf("creating initial commit: %w", err)
	}
	// The Contents API auto-creates the repo's default branch; fetch its SHA.
	ref, _, err := g.client.Git.GetRef(ctx, g.owner, g.repo, "refs/heads/"+branchName)
	if err != nil {
		return "", fmt.Errorf("getting ref after init: %w", err)
	}
	return ref.GetObject().GetSHA(), nil
}

// CreateOrUpdateFile creates a new file or updates an existing one on the given branch.
func (g *GitHubProvider) CreateOrUpdateFile(ctx context.Context, path string, content []byte, branch, commitMessage string) error {
	author := &github.CommitAuthor{
		Name:  github.Ptr("Sharko Bot"),
		Email: github.Ptr("sharko-bot@users.noreply.github.com"),
	}

	existing, err := g.getContentsRaw(ctx, path, branch)
	if err != nil {
		return fmt.Errorf("create or update file: %w", err)
	}

	opts := &github.RepositoryContentFileOptions{
		Message:   github.Ptr(commitMessage),
		Content:   content,
		Branch:    github.Ptr(branch),
		Author:    author,
		Committer: author,
	}

	if existing != nil {
		opts.SHA = existing.SHA
	}

	_, _, err = g.client.Repositories.CreateFile(ctx, g.owner, g.repo, path, opts)
	if err != nil {
		// On 422 SHA mismatch, retry once by re-fetching the SHA.
		if ghErr, ok := err.(*github.ErrorResponse); ok && ghErr.Response.StatusCode == http.StatusUnprocessableEntity {
			slog.Warn("github SHA mismatch, retrying", "path", path, "branch", branch)
			existing, fetchErr := g.getContentsRaw(ctx, path, branch)
			if fetchErr != nil {
				return fmt.Errorf("create or update file: retry fetch: %w", fetchErr)
			}
			if existing != nil {
				opts.SHA = existing.SHA
			} else {
				opts.SHA = nil
			}
			_, _, err = g.client.Repositories.CreateFile(ctx, g.owner, g.repo, path, opts)
			if err != nil {
				return fmt.Errorf("create or update file: retry: %w", err)
			}
			slog.Info("github file written (after retry)", "path", path, "branch", branch)
			return nil
		}
		return fmt.Errorf("create or update file: %w", err)
	}

	slog.Info("github file written", "path", path, "branch", branch)
	return nil
}

// DeleteFile removes a file from the given branch.
func (g *GitHubProvider) DeleteFile(ctx context.Context, path, branch, commitMessage string) error {
	existing, err := g.getContentsRaw(ctx, path, branch)
	if err != nil {
		return fmt.Errorf("delete file: %w", err)
	}
	if existing == nil {
		return fmt.Errorf("delete file: file %q not found on branch %q", path, branch)
	}

	author := &github.CommitAuthor{
		Name:  github.Ptr("Sharko Bot"),
		Email: github.Ptr("sharko-bot@users.noreply.github.com"),
	}

	opts := &github.RepositoryContentFileOptions{
		Message:   github.Ptr(commitMessage),
		SHA:       existing.SHA,
		Branch:    github.Ptr(branch),
		Author:    author,
		Committer: author,
	}

	_, _, err = g.client.Repositories.DeleteFile(ctx, g.owner, g.repo, path, opts)
	if err != nil {
		return fmt.Errorf("delete file: %w", err)
	}

	slog.Info("github file deleted", "path", path, "branch", branch)
	return nil
}

// BatchCreateFiles creates or updates multiple files in a single Git commit
// using the Git Data API (tree + commit).  This is significantly faster than
// calling CreateOrUpdateFile once per file for bulk operations such as init.
func (g *GitHubProvider) BatchCreateFiles(ctx context.Context, files map[string][]byte, branch, commitMessage string) error {
	// 1. Resolve the current HEAD of the branch.
	ref, _, err := g.client.Git.GetRef(ctx, g.owner, g.repo, "refs/heads/"+branch)
	if err != nil {
		return fmt.Errorf("batch create: get ref: %w", err)
	}
	baseSHA := ref.GetObject().GetSHA()

	// 2. Fetch the tree SHA of that commit.
	commit, _, err := g.client.Git.GetCommit(ctx, g.owner, g.repo, baseSHA)
	if err != nil {
		return fmt.Errorf("batch create: get commit: %w", err)
	}
	baseTreeSHA := commit.GetTree().GetSHA()

	// 3. Build tree entries — one per file.
	entries := make([]*github.TreeEntry, 0, len(files))
	for path, content := range files {
		entries = append(entries, &github.TreeEntry{
			Path:    github.Ptr(path),
			Mode:    github.Ptr("100644"),
			Type:    github.Ptr("blob"),
			Content: github.Ptr(string(content)),
		})
	}

	// 4. Create a new tree on top of the base tree.
	tree, _, err := g.client.Git.CreateTree(ctx, g.owner, g.repo, baseTreeSHA, entries)
	if err != nil {
		return fmt.Errorf("batch create: create tree: %w", err)
	}

	// 5. Create a commit that points to the new tree.
	author := &github.CommitAuthor{
		Name:  github.Ptr("Sharko Bot"),
		Email: github.Ptr("sharko-bot@users.noreply.github.com"),
	}
	newCommit, _, err := g.client.Git.CreateCommit(ctx, g.owner, g.repo, &github.Commit{
		Message: github.Ptr(commitMessage),
		Tree:    tree,
		Parents: []*github.Commit{{SHA: github.Ptr(baseSHA)}},
		Author:  author,
	}, nil)
	if err != nil {
		return fmt.Errorf("batch create: create commit: %w", err)
	}

	// 6. Fast-forward the branch ref to the new commit.
	_, _, err = g.client.Git.UpdateRef(ctx, g.owner, g.repo, &github.Reference{
		Ref:    github.Ptr("refs/heads/" + branch),
		Object: &github.GitObject{SHA: newCommit.SHA},
	}, false)
	if err != nil {
		return fmt.Errorf("batch create: update ref: %w", err)
	}

	slog.Info("github batch files written", "count", len(files), "branch", branch)
	return nil
}

// CreatePullRequest opens a new pull request.
func (g *GitHubProvider) CreatePullRequest(ctx context.Context, title, body, head, base string) (*PullRequest, error) {
	pr, _, err := g.client.PullRequests.Create(ctx, g.owner, g.repo, &github.NewPullRequest{
		Title:               github.Ptr(title),
		Body:                github.Ptr(body),
		Head:                github.Ptr(head),
		Base:                github.Ptr(base),
		MaintainerCanModify: github.Ptr(true),
	})
	if err != nil {
		return nil, fmt.Errorf("create pull request: %w", err)
	}

	result := &PullRequest{
		ID:           pr.GetNumber(),
		Title:        pr.GetTitle(),
		Description:  pr.GetBody(),
		Author:       pr.GetUser().GetLogin(),
		Status:       "open",
		SourceBranch: pr.GetHead().GetRef(),
		TargetBranch: pr.GetBase().GetRef(),
		URL:          pr.GetHTMLURL(),
	}

	if t := pr.GetCreatedAt(); !t.IsZero() {
		result.CreatedAt = t.Format("2006-01-02T15:04:05Z")
	}

	slog.Info("github pull request created", "number", result.ID, "url", result.URL)
	return result, nil
}

// MergePullRequest merges an open pull request by number.
func (g *GitHubProvider) MergePullRequest(ctx context.Context, prNumber int) error {
	_, _, err := g.client.PullRequests.Merge(ctx, g.owner, g.repo, prNumber, "", &github.PullRequestOptions{
		MergeMethod: "squash",
	})
	if err != nil {
		return fmt.Errorf("merge pull request #%d: %w", prNumber, err)
	}
	slog.Info("github pull request merged", "number", prNumber)
	return nil
}

// DeleteBranch removes a branch by name.
func (g *GitHubProvider) DeleteBranch(ctx context.Context, branchName string) error {
	_, err := g.client.Git.DeleteRef(ctx, g.owner, g.repo, "refs/heads/"+branchName)
	if err != nil {
		return fmt.Errorf("delete branch %q: %w", branchName, err)
	}
	slog.Info("github branch deleted", "branch", branchName)
	return nil
}
