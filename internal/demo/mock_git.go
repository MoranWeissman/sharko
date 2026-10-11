package demo

import (
	"context"
	"fmt"
	"sync"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// MockGitProvider implements gitprovider.GitProvider entirely in memory.
// All write operations are accepted and stored in-process — no real Git calls made.
type MockGitProvider struct {
	mu       sync.RWMutex
	files    map[string][]byte // path → content
	branches map[string]bool   // branch name → exists
	prs      []gitprovider.PullRequest
	nextPRID int
}

// NewMockGitProvider creates a new in-memory git provider with pre-seeded content.
//
// panics if the v4 fixture set (buildV4DemoFiles) fails to render — the
// same "this is a build-time invariant, not a runtime condition" stance
// NewMockArgocdServer's caller takes for its own setup errors. The v4
// fixtures are built entirely from static, hand-written Go values, so a
// failure here can only mean a real bug in this package, never bad
// input from an operator or a live repo.
func NewMockGitProvider() *MockGitProvider {
	p := &MockGitProvider{
		files:    make(map[string][]byte),
		branches: map[string]bool{"main": true},
		nextPRID: 43, // start after the 2 pre-seeded PRs (LW-18: removed perf-asia phantom PR)
	}
	p.seedFiles()
	v4Files, err := buildV4DemoFiles()
	if err != nil {
		panic(fmt.Sprintf("demo: building v4 fixture files: %v", err))
	}
	for path, content := range v4Files {
		p.files[path] = content
	}
	p.seedPRs()
	return p
}

// NewMockGitProviderWithConfig builds a MockGitProvider sized per cfg. For
// the default size (cfg.IsDefault()) it defers to NewMockGitProvider
// unchanged — same hand-written fixture, byte-for-byte, so the small
// estate's shape never depends on the generator. For any other size it
// generates a fresh estate and renders the v4 layout from it instead of
// the hand-written fixture.
func NewMockGitProviderWithConfig(cfg ScaleConfig) (*MockGitProvider, error) {
	if cfg.IsDefault() {
		return NewMockGitProvider(), nil
	}
	estate, err := GenerateEstate(cfg)
	if err != nil {
		return nil, fmt.Errorf("demo: generating estate for mock git provider: %w", err)
	}
	return NewMockGitProviderFromEstate(estate)
}

// NewMockGitProviderFromEstate builds a MockGitProvider directly from an
// already-generated estate — used by NewMockGitProviderWithConfig, and by
// SetupDemoServer when it needs the SAME estate shared across the mock git
// provider, the mock ArgoCD server, and the mock credentials provider
// (generating it once rather than once per constructor).
func NewMockGitProviderFromEstate(estate *GeneratedEstate) (*MockGitProvider, error) {
	p := &MockGitProvider{
		files:    make(map[string][]byte),
		branches: map[string]bool{"main": true},
		nextPRID: 1000, // clear of both the default estate's 41-43 range and TrackedPRs' 100+ range
	}

	// Same one-layout rule as seedFiles: the engine pin plus the v4 files,
	// and nothing from the old v3 layout.
	p.files[orchestrator.BootstrapRootAppPath] = []byte(demoEnginePin)

	v4Files, err := buildV4DemoFilesFromEstate(estate)
	if err != nil {
		return nil, fmt.Errorf("demo: building v4 fixture files from generated estate: %w", err)
	}
	for path, content := range v4Files {
		p.files[path] = content
	}

	p.prs = append([]gitprovider.PullRequest(nil), estate.GitPRs...)

	return p, nil
}

// demoEnginePin is the engine pin (sharko-engine.yaml) every demo repo
// carries. It is what makes the repo a v4 repo: every v4-aware read and
// write asks whether this file exists before it decides which files to use.
const demoEnginePin = `apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: sharko-engine
  namespace: argocd
spec:
  project: default
  sources:
    - repoURL: ghcr.io/moranweissman/sharko
      chart: sharko-engine
      targetRevision: 0.1.0
    - repoURL: https://github.com/demo/sharko-addons
      targetRevision: main
      ref: values
`

// seedFiles writes the engine pin. The rest of the repo (catalog.yaml,
// cluster-addons/, managed-clusters.yaml, values/) comes from
// buildV4DemoFiles (v4_fixtures.go), which NewMockGitProvider merges in
// right after this.
//
// The demo repo holds ONLY the v4 layout, the same as a repo a real
// v4 Sharko user has. It used to carry the old v3 files too
// (configuration/managed-clusters.yaml, configuration/addons-catalog.yaml,
// configuration/addons-*-values/), and that made every write in the demo
// fail: Sharko refuses to change a repo that has both layouts in it
// (orchestrator.ErrMixedRepoLayout). Do not add v3 files back here.
// TestDemoRepo_IsOneLayoutOnly fails if anyone does.
//
// The path comes from the constant rather than a copied literal on
// purpose. Every v4-aware path decides "is this a v4 repo?" by asking
// whether a file exists HERE, and answers no when it cannot find one. So
// a copy that fell behind would not fail — the whole demo would quietly
// present itself as a v3 repo, and every v4 screen would show the wrong
// thing with nothing in the logs to say why.
func (p *MockGitProvider) seedFiles() {
	p.files[orchestrator.BootstrapRootAppPath] = []byte(demoEnginePin)
}

func (p *MockGitProvider) seedPRs() {
	p.prs = []gitprovider.PullRequest{
		{
			ID:           41,
			Title:        "sharko: upgrade cert-manager 1.13.6 → 1.14.4 on staging-eu",
			Description:  "Automated upgrade by Sharko",
			Author:       "sharko-bot",
			Status:       "open",
			SourceBranch: "sharko/upgrade-cert-manager-staging-eu",
			TargetBranch: "main",
			URL:          "https://github.com/demo/sharko-addons/pull/41",
			CreatedAt:    "2025-01-18T09:00:00Z",
			UpdatedAt:    "2025-01-18T09:00:00Z",
		},
		// Pending registration PR matching the seeded unregistered cluster dr-eu
		// (LW-9, Story LW-F gap #2, fixed LW-18). Title follows the orchestrator's
		// registration pattern "<CommitPrefix> register cluster <name>" so
		// resolvePendingRegistrations parses it correctly. This is the ONLY pending
		// registration in the demo (LW-18: perf-asia is the disconnected in-git
		// cluster, NOT a pending registration).
		{
			ID:           42,
			Title:        "sharko: register cluster dr-eu",
			Description:  "Automated cluster registration by Sharko",
			Author:       "sharko-bot",
			Status:       "open",
			SourceBranch: "sharko/register-dr-eu",
			TargetBranch: "main",
			URL:          "https://github.com/demo/sharko-addons/pull/42",
			CreatedAt:    "2025-01-19T14:22:00Z",
			UpdatedAt:    "2025-01-19T14:22:00Z",
		},
	}
}

// GetFileContent returns the content of a file at the given path and ref.
//
// Missing files return an error wrapping gitprovider.ErrFileNotFound so
// callers can detect the missing-file case via errors.Is — substring matching
// the message is unsafe (review finding H2).
func (p *MockGitProvider) GetFileContent(_ context.Context, path, _ string) ([]byte, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if content, ok := p.files[path]; ok {
		return content, nil
	}
	return nil, fmt.Errorf("mock git: %s: %w", path, gitprovider.ErrFileNotFound)
}

// ListDirectory returns the names of items under a directory path.
func (p *MockGitProvider) ListDirectory(_ context.Context, dirPath, _ string) ([]string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	prefix := dirPath
	if prefix != "" && prefix[len(prefix)-1] != '/' {
		prefix += "/"
	}

	seen := make(map[string]bool)
	var entries []string
	for path := range p.files {
		if dirPath == "" || path == dirPath || (len(path) > len(prefix) && path[:len(prefix)] == prefix) {
			// Extract the immediate child name
			rest := path[len(prefix):]
			if idx := indexOf(rest, '/'); idx >= 0 {
				rest = rest[:idx]
			}
			if !seen[rest] {
				seen[rest] = true
				entries = append(entries, rest)
			}
		}
	}
	return entries, nil
}

// ListPullRequests returns pull requests filtered by state.
func (p *MockGitProvider) ListPullRequests(_ context.Context, state string) ([]gitprovider.PullRequest, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var result []gitprovider.PullRequest
	for _, pr := range p.prs {
		if state == "" || state == "all" || pr.Status == state {
			result = append(result, pr)
		}
	}
	return result, nil
}

// TestConnection always succeeds for the demo provider.
func (p *MockGitProvider) TestConnection(_ context.Context) error {
	return nil
}

// CreateBranch creates an in-memory branch.
func (p *MockGitProvider) CreateBranch(_ context.Context, branchName, _ string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.branches[branchName] = true
	return nil
}

// CreateOrUpdateFile upserts a file in the in-memory store.
func (p *MockGitProvider) CreateOrUpdateFile(_ context.Context, path string, content []byte, branch, _ string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.files[path] = content
	return nil
}

// BatchCreateFiles upserts multiple files in the in-memory store atomically.
func (p *MockGitProvider) BatchCreateFiles(_ context.Context, files map[string][]byte, _, _ string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for path, content := range files {
		p.files[path] = content
	}
	return nil
}

// DeleteFile removes a file from the in-memory store.
func (p *MockGitProvider) DeleteFile(_ context.Context, path, _, _ string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.files, path)
	return nil
}

// CreatePullRequest creates a mock PR and returns it with a demo URL.
func (p *MockGitProvider) CreatePullRequest(_ context.Context, title, body, head, base string) (*gitprovider.PullRequest, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	id := p.nextPRID
	p.nextPRID++

	pr := gitprovider.PullRequest{
		ID:           id,
		Title:        title,
		Description:  body,
		Author:       "sharko-bot",
		Status:       "open",
		SourceBranch: head,
		TargetBranch: base,
		URL:          fmt.Sprintf("https://github.com/demo/sharko-addons/pull/%d", id),
		CreatedAt:    "2025-01-20T10:00:00Z",
		UpdatedAt:    "2025-01-20T10:00:00Z",
	}
	p.prs = append(p.prs, pr)
	return &pr, nil
}

// MergePullRequest marks a PR as merged (no-op success).
func (p *MockGitProvider) MergePullRequest(_ context.Context, prNumber int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.prs {
		if p.prs[i].ID == prNumber {
			p.prs[i].Status = "merged"
		}
	}
	return nil
}

// GetPullRequestStatus returns the status of a pull request.
func (p *MockGitProvider) GetPullRequestStatus(_ context.Context, prNumber int) (string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, pr := range p.prs {
		if pr.ID == prNumber {
			return pr.Status, nil
		}
	}
	return "", fmt.Errorf("PR #%d not found", prNumber)
}

// demoBranchHeadSHA is the fixed, obviously-fake 40-char commit SHA the
// demo git provider reports as every branch's head (P2-C4). Well-formed
// (real hex, real length) so it renders exactly like a real SHA would —
// only its content ("deaddead...") says it is fake. One fixed value on
// purpose: the demo's whole managed-clusters.yaml is a static, hand-written
// fixture that never actually changes commit-to-commit, so a single
// unchanging SHA is the honest answer, not a randomly-varying one.
const demoBranchHeadSHA = "deaddead0102030405060708090a0b0c0d0e0f1"

// GetBranchHeadSHA implements gitprovider.BranchRevisioner so this provider
// answers the same question a real GitHub, Azure DevOps, or Gitea
// connection does. Demo mode's cluster reconciler is constructed but never
// Started (see setup.go — the state the page shows is direct-seeded, the
// same reasoning as SeedReconcileRecordForDemo), so this method is not what
// puts a revision on the demo's rows; connection_secrets_demo.go seeds
// demoBranchHeadSHA directly for that. This method exists so any test or
// future code path that DOES run a real tick against MockGitProvider gets
// an honest, well-formed answer instead of "unimplemented".
func (p *MockGitProvider) GetBranchHeadSHA(_ context.Context, _ string) (string, error) {
	return demoBranchHeadSHA, nil
}

// DeleteBranch removes a branch from the in-memory store.
func (p *MockGitProvider) DeleteBranch(_ context.Context, branchName string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.branches, branchName)
	return nil
}

// indexOf returns the index of sep in s, or -1.
func indexOf(s string, sep byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			return i
		}
	}
	return -1
}
