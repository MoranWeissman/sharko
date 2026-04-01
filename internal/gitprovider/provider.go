package gitprovider

import "context"

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
	DeleteFile(ctx context.Context, path, branch, commitMessage string) error
	CreatePullRequest(ctx context.Context, title, body, head, base string) (*PullRequest, error)
	MergePullRequest(ctx context.Context, prNumber int) error
	DeleteBranch(ctx context.Context, branchName string) error
}
