package gitprovider

import (
	"context"
	"errors"
)

// ErrFileNotFound is the canonical sentinel returned by GitProvider
// implementations when a requested path does not exist in the repository at
// the requested ref. Callers MUST detect missing-file conditions via
// errors.Is(err, gitprovider.ErrFileNotFound) rather than substring-matching
// the error message — substring matching silently masks legitimate
// auth/branch/perm errors that happen to contain the words "not found" or
// "404" (review finding H2 against PR #318).
//
// Implementations should wrap the sentinel with additional context using
// fmt.Errorf("...: %w", gitprovider.ErrFileNotFound) so logs retain the path
// and provider while errors.Is still works.
var ErrFileNotFound = errors.New("file not found")

// PullRequest represents a pull request from any Git provider.
type PullRequest struct {
	ID           int
	Title        string
	Description  string
	Author       string
	Status       string // "open", "closed", "merged"
	SourceBranch string
	TargetBranch string
	URL          string
	CreatedAt    string
	UpdatedAt    string
	ClosedAt     string
}

// GitProvider defines the operations supported against a Git hosting service.
type GitProvider interface {
	// Read operations
	GetFileContent(ctx context.Context, path, ref string) ([]byte, error)
	ListDirectory(ctx context.Context, path, ref string) ([]string, error)
	ListPullRequests(ctx context.Context, state string) ([]PullRequest, error)
	TestConnection(ctx context.Context) error

	// Write operations
	CreateBranch(ctx context.Context, branchName, fromRef string) error
	CreateOrUpdateFile(ctx context.Context, path string, content []byte, branch, commitMessage string) error
	// BatchCreateFiles writes multiple files in a single commit.
	// Implementations that lack a native batch API may fall back to sequential calls.
	BatchCreateFiles(ctx context.Context, files map[string][]byte, branch, commitMessage string) error
	DeleteFile(ctx context.Context, path, branch, commitMessage string) error
	CreatePullRequest(ctx context.Context, title, body, head, base string) (*PullRequest, error)
	MergePullRequest(ctx context.Context, prNumber int) error
	GetPullRequestStatus(ctx context.Context, prNumber int) (string, error)
	DeleteBranch(ctx context.Context, branchName string) error
}
