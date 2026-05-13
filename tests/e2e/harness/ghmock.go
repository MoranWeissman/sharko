//go:build e2e

// Package harness — GitHub REST API mock.
//
// # Why a mock at all? — Option B (provider injection)
//
// Sharko writes to GitHub via the GitHub REST API (the high-level
// `internal/gitprovider/github.go` and `github_write.go`), NOT via
// raw git transport. The HTTP smart-protocol GitFake provided by
// `gitfake.go` therefore cannot intercept sharko's writes — sharko
// never opens a git connection in production.
//
// To intercept those writes in tests we need to replace the
// `gitprovider.GitProvider` instance returned by
// `connection.GetActiveGitProvider`. Sharko already exposes the hook:
// `*Server.SetDemoGitProvider(gp)` (see `internal/api/router.go`)
// installs a `gitprovider.GitProvider` override on the connection
// service. That same hook is reused by demo mode and by handler tests
// (`clusters_orphan_delete_test.go`, `clusters_pending_test.go`).
//
// We therefore went with **Option B (provider injection)** over
// **Option A (HTTP-level mock)**:
//
//   - No new HTTP server to manage; the mock is a plain in-memory
//     struct with mutex-guarded state.
//   - Test assertions can read internal state directly via
//     `MockGitProvider.FileAt`, `ListBranches`, etc. — lossless and
//     allocation-free, no JSON round-trips.
//   - Sharko's existing override hook means ZERO product code changes
//     to wire the mock in.
//   - The mock's surface == the real `gitprovider.GitProvider`
//     interface, so adding a method to the interface forces a compile
//     break here that downstream stories will catch immediately.
//
// The trade-off: Option A would also have caught bugs in the
// `internal/gitprovider/github.go` HTTP-translation layer itself. We
// cover that layer with the existing unit tests in
// `internal/gitprovider/github_write_test.go` and accept Option B's
// reduced scope as a deliberate choice — the e2e suite is for sharko's
// orchestration surface, not for the GitHub-client wrapper.
package harness

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
)

// MockPR is a test-side view of a pull request held by MockGitProvider.
// Mirrors the public fields of gitprovider.PullRequest plus the source/
// destination branch SHAs so tests can assert merge state precisely.
type MockPR struct {
	Number       int
	Title        string
	Body         string
	HeadBranch   string
	BaseBranch   string
	State        string // "open" | "closed" | "merged"
	URL          string
	HeadSHA      string
	MergeCommit  string // populated after MergePullRequest
	CreatedAt    time.Time
	ClosedAt     time.Time
	MergedAt     time.Time
}

// fileBlob is one branch's view of one path. SHA is a synthetic content
// hash sufficient for sharko's "did the file change?" callers.
type fileBlob struct {
	content []byte
	sha     string
}

// MockGitProvider is an in-memory implementation of
// gitprovider.GitProvider. State is a per-branch map of path → blob plus
// a list of PRs. All operations are mutex-guarded; the mock is safe for
// concurrent use from multiple sharko goroutines.
//
// The mock is constructed empty and pre-seeded with a "main" branch
// holding a single empty README.md, mirroring what GitHub does when you
// `gh repo create --add-readme`. This keeps sharko's bootstrap path
// (BatchCreateFiles → CreatePullRequest → MergePullRequest) reachable
// without each test having to seed manually.
type MockGitProvider struct {
	owner string
	repo  string

	mu       sync.Mutex
	branches map[string]map[string]fileBlob // branch → path → blob
	prs      []*MockPR
	nextPR   int
	commits  int // monotonic for synthetic SHAs
}

// Compile-time check: MockGitProvider satisfies the production interface.
var _ gitprovider.GitProvider = (*MockGitProvider)(nil)

// MockGitProviderOption tunes the constructor.
type MockGitProviderOption func(*MockGitProvider)

// WithOwnerRepo sets the synthetic owner/repo identity. Defaults to
// "sharko-e2e/sharko-addons" when not set; tests almost never need
// to override this.
func WithOwnerRepo(owner, repo string) MockGitProviderOption {
	return func(m *MockGitProvider) {
		m.owner = owner
		m.repo = repo
	}
}

// StartGitMock constructs an in-memory MockGitProvider with the
// bootstrap "main" branch pre-seeded with a README.md (matching
// gh repo create --add-readme semantics). Auto-cleanup is registered
// via t.Cleanup; the mock has no external resources so cleanup is a
// no-op today, but the symmetry with StartGitFake/StartSharko keeps
// every harness primitive interchangeable.
func StartGitMock(t *testing.T, opts ...MockGitProviderOption) *MockGitProvider {
	t.Helper()
	m := &MockGitProvider{
		owner:    "sharko-e2e",
		repo:     "sharko-addons",
		branches: make(map[string]map[string]fileBlob),
	}
	for _, o := range opts {
		o(m)
	}
	// Seed main with a README so sharko's "is this repo initialized?"
	// probes (CreateBranch / GetFileContent on main) succeed without
	// each test having to call BatchCreateFiles first. Mirrors the
	// gh repo create --add-readme behaviour.
	m.branches["main"] = map[string]fileBlob{
		"README.md": {content: []byte("# sharko-e2e\n"), sha: m.newSHA()},
	}
	t.Cleanup(func() { /* nothing to release */ })
	t.Logf("harness: ghmock ready [%s/%s, branches=main with README.md]", m.owner, m.repo)
	return m
}

// ---------------------------------------------------------------------------
// gitprovider.GitProvider implementation
// ---------------------------------------------------------------------------

// GetFileContent returns the bytes of path on branch ref. Returns
// ErrFileNotFound (wrapped) when either the branch or the file is
// missing — this matches the real GitHub provider's contract that
// callers detect missing files via errors.Is.
func (m *MockGitProvider) GetFileContent(_ context.Context, path, ref string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	branch := m.resolveBranch(ref)
	files, ok := m.branches[branch]
	if !ok {
		return nil, fmt.Errorf("ghmock get %q on %q: %w", path, ref, gitprovider.ErrFileNotFound)
	}
	blob, ok := files[path]
	if !ok {
		return nil, fmt.Errorf("ghmock get %q on %q: %w", path, ref, gitprovider.ErrFileNotFound)
	}
	out := make([]byte, len(blob.content))
	copy(out, blob.content)
	return out, nil
}

// ListDirectory returns immediate children of dir on ref (no recursion).
// "" lists everything at the root.
func (m *MockGitProvider) ListDirectory(_ context.Context, dir, ref string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	branch := m.resolveBranch(ref)
	files, ok := m.branches[branch]
	if !ok {
		return nil, fmt.Errorf("ghmock listdir %q on %q: %w", dir, ref, gitprovider.ErrFileNotFound)
	}
	prefix := dir
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	seen := make(map[string]struct{})
	for path := range files {
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		rest := strings.TrimPrefix(path, prefix)
		if rest == "" {
			continue
		}
		seg := rest
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			seg = rest[:i]
		}
		seen[seg] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

// ListPullRequests returns PRs filtered by state.
//
// Accepted states: "open", "closed", "merged", "all" (and "" treated as
// "open" for parity with GitHub). The real provider's state filter maps
// "closed" to "closed-or-merged" — we mirror that so test assertions
// match production semantics.
func (m *MockGitProvider) ListPullRequests(_ context.Context, state string) ([]gitprovider.PullRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]gitprovider.PullRequest, 0, len(m.prs))
	wantOpen, wantClosed, wantMerged := stateFilter(state)
	for _, pr := range m.prs {
		switch pr.State {
		case "open":
			if !wantOpen {
				continue
			}
		case "closed":
			if !wantClosed {
				continue
			}
		case "merged":
			if !wantMerged {
				continue
			}
		}
		out = append(out, mockPRToPublic(pr))
	}
	return out, nil
}

func stateFilter(state string) (open, closed, merged bool) {
	switch strings.ToLower(state) {
	case "", "open":
		return true, false, false
	case "closed":
		// GitHub conflates closed + merged for state=closed; mirror that.
		return false, true, true
	case "merged":
		return false, false, true
	case "all":
		return true, true, true
	default:
		return true, false, false
	}
}

// TestConnection always succeeds for the mock — there is no remote.
func (m *MockGitProvider) TestConnection(_ context.Context) error { return nil }

// CreateBranch creates branchName from fromRef. If fromRef does not
// exist we initialise it with an empty README on `main` first (mirroring
// the real provider's "empty repo" recovery in github_write.go).
func (m *MockGitProvider) CreateBranch(_ context.Context, branchName, fromRef string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	src, ok := m.branches[m.resolveBranch(fromRef)]
	if !ok {
		// Empty-repo bootstrap: seed main with README, then branch from it.
		m.branches["main"] = map[string]fileBlob{
			"README.md": {content: []byte("# sharko-e2e\n"), sha: m.newSHA()},
		}
		src = m.branches["main"]
	}
	if _, exists := m.branches[branchName]; exists {
		return fmt.Errorf("ghmock create branch %q: already exists", branchName)
	}
	clone := make(map[string]fileBlob, len(src))
	for k, v := range src {
		clone[k] = v
	}
	m.branches[branchName] = clone
	return nil
}

// CreateOrUpdateFile writes/replaces path on branch with content.
// Auto-creates the branch from main when missing — convenience for
// tests that haven't called CreateBranch first.
func (m *MockGitProvider) CreateOrUpdateFile(_ context.Context, path string, content []byte, branch, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.branches[branch]; !ok {
		m.branches[branch] = make(map[string]fileBlob)
	}
	cp := make([]byte, len(content))
	copy(cp, content)
	m.branches[branch][path] = fileBlob{content: cp, sha: m.newSHA()}
	return nil
}

// BatchCreateFiles writes every entry in files to branch in a single
// (atomic, from the test's POV) operation.
func (m *MockGitProvider) BatchCreateFiles(_ context.Context, files map[string][]byte, branch, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.branches[branch]; !ok {
		m.branches[branch] = make(map[string]fileBlob)
	}
	for path, content := range files {
		cp := make([]byte, len(content))
		copy(cp, content)
		m.branches[branch][path] = fileBlob{content: cp, sha: m.newSHA()}
	}
	return nil
}

// DeleteFile removes path from branch.
func (m *MockGitProvider) DeleteFile(_ context.Context, path, branch, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	files, ok := m.branches[branch]
	if !ok {
		return fmt.Errorf("ghmock delete file %q on %q: branch not found", path, branch)
	}
	if _, ok := files[path]; !ok {
		return fmt.Errorf("ghmock delete file %q on %q: %w", path, branch, gitprovider.ErrFileNotFound)
	}
	delete(files, path)
	return nil
}

// CreatePullRequest opens a PR from head into base. Returns the public
// PullRequest type so callers (sharko handlers) get a stable shape.
func (m *MockGitProvider) CreatePullRequest(_ context.Context, title, body, head, base string) (*gitprovider.PullRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.branches[head]; !ok {
		return nil, fmt.Errorf("ghmock create PR: head branch %q not found", head)
	}
	if _, ok := m.branches[base]; !ok {
		return nil, fmt.Errorf("ghmock create PR: base branch %q not found", base)
	}
	m.nextPR++
	now := time.Now().UTC()
	pr := &MockPR{
		Number:     m.nextPR,
		Title:      title,
		Body:       body,
		HeadBranch: head,
		BaseBranch: base,
		State:      "open",
		URL:        fmt.Sprintf("https://github.com/%s/%s/pull/%d", m.owner, m.repo, m.nextPR),
		HeadSHA:    m.headSHA(head),
		CreatedAt:  now,
	}
	m.prs = append(m.prs, pr)
	return mockPRToPublicPtr(pr), nil
}

// MergePullRequest merges PR #prNumber by copying head's files onto
// base. Sets State=merged, MergedAt=now, populates MergeCommit.
//
// Squash semantics: a single synthetic commit lands on base. We do not
// model merge conflicts — tests should compose branches such that the
// merge would succeed in the real world.
func (m *MockGitProvider) MergePullRequest(_ context.Context, prNumber int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	pr := m.findPR(prNumber)
	if pr == nil {
		return fmt.Errorf("ghmock merge PR #%d: not found", prNumber)
	}
	if pr.State != "open" {
		return fmt.Errorf("ghmock merge PR #%d: state=%q (want open)", prNumber, pr.State)
	}
	headFiles, ok := m.branches[pr.HeadBranch]
	if !ok {
		return fmt.Errorf("ghmock merge PR #%d: head branch %q vanished", prNumber, pr.HeadBranch)
	}
	baseFiles, ok := m.branches[pr.BaseBranch]
	if !ok {
		return fmt.Errorf("ghmock merge PR #%d: base branch %q vanished", prNumber, pr.BaseBranch)
	}
	// Squash: copy each path from head onto base.
	for path, blob := range headFiles {
		baseFiles[path] = blob
	}
	pr.State = "merged"
	now := time.Now().UTC()
	pr.MergedAt = now
	pr.ClosedAt = now
	pr.MergeCommit = m.newSHA()
	return nil
}

// GetPullRequestStatus returns "open" | "closed" | "merged".
func (m *MockGitProvider) GetPullRequestStatus(_ context.Context, prNumber int) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pr := m.findPR(prNumber)
	if pr == nil {
		return "", fmt.Errorf("ghmock get PR status #%d: not found", prNumber)
	}
	return pr.State, nil
}

// DeleteBranch removes a branch. No-op when missing (idempotent — same
// behaviour as the real provider when DeleteRef returns 404).
func (m *MockGitProvider) DeleteBranch(_ context.Context, branchName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.branches, branchName)
	return nil
}

// ---------------------------------------------------------------------------
// inspection helpers — for test assertions
// ---------------------------------------------------------------------------

// FileAt returns the content of path on branch as a string. Empty string
// when the file does not exist (so tests can write `if got := ...; got
// != ""` without an error juggle); use the GitProvider GetFileContent
// method when you need to assert ErrFileNotFound.
func (m *MockGitProvider) FileAt(branch, path string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	files, ok := m.branches[branch]
	if !ok {
		return ""
	}
	blob, ok := files[path]
	if !ok {
		return ""
	}
	return string(blob.content)
}

// FileExists reports whether path exists on branch.
func (m *MockGitProvider) FileExists(branch, path string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	files, ok := m.branches[branch]
	if !ok {
		return false
	}
	_, ok = files[path]
	return ok
}

// ListBranches returns every branch name in sorted order.
func (m *MockGitProvider) ListBranches() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.branches))
	for name := range m.branches {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ListMockPRs returns the test-side view of PRs filtered by state. Use
// state "all" to see everything. Returns a defensive copy so callers
// can't mutate internal state.
func (m *MockGitProvider) ListMockPRs(state string) []MockPR {
	m.mu.Lock()
	defer m.mu.Unlock()
	wantOpen, wantClosed, wantMerged := stateFilter(state)
	out := make([]MockPR, 0, len(m.prs))
	for _, pr := range m.prs {
		switch pr.State {
		case "open":
			if !wantOpen {
				continue
			}
		case "closed":
			if !wantClosed {
				continue
			}
		case "merged":
			if !wantMerged {
				continue
			}
		}
		out = append(out, *pr)
	}
	return out
}

// PRsForBranch returns PRs whose head branch equals headBranch
// (independent of state).
func (m *MockGitProvider) PRsForBranch(headBranch string) []MockPR {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []MockPR
	for _, pr := range m.prs {
		if pr.HeadBranch == headBranch {
			out = append(out, *pr)
		}
	}
	return out
}

// MergePR is the test-side simulator: marks PR as merged WITHOUT going
// through sharko. Useful for the auto-merge=false flow where the test
// represents a human approving + merging the PR on GitHub. Wraps
// MergePullRequest with a t.Helper / t.Fatalf for ergonomics.
func (m *MockGitProvider) MergePR(t *testing.T, prNumber int) {
	t.Helper()
	if err := m.MergePullRequest(context.Background(), prNumber); err != nil {
		t.Fatalf("MockGitProvider.MergePR(%d): %v", prNumber, err)
	}
}

// ClosePR marks PR as closed (without merging). For the orphan/cancel
// flow assertions.
func (m *MockGitProvider) ClosePR(t *testing.T, prNumber int) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	pr := m.findPR(prNumber)
	if pr == nil {
		t.Fatalf("MockGitProvider.ClosePR(%d): not found", prNumber)
	}
	if pr.State != "open" {
		t.Fatalf("MockGitProvider.ClosePR(%d): state=%q (want open)", prNumber, pr.State)
	}
	pr.State = "closed"
	pr.ClosedAt = time.Now().UTC()
}

// ---------------------------------------------------------------------------
// internal helpers
// ---------------------------------------------------------------------------

// resolveBranch maps "" to "main" so callers passing an empty ref hit
// the default branch (matches GitHub's behaviour when ref is omitted).
func (m *MockGitProvider) resolveBranch(ref string) string {
	if ref == "" {
		return "main"
	}
	// Strip leading "refs/heads/" if a caller passes the full ref.
	return strings.TrimPrefix(ref, "refs/heads/")
}

func (m *MockGitProvider) findPR(num int) *MockPR {
	for _, pr := range m.prs {
		if pr.Number == num {
			return pr
		}
	}
	return nil
}

func (m *MockGitProvider) headSHA(branch string) string {
	files, ok := m.branches[branch]
	if !ok || len(files) == 0 {
		return ""
	}
	// Synthetic — return the SHA of any one file. Sharko callers don't
	// use the head SHA for cryptographic purposes; they only check
	// equality across calls.
	for _, blob := range files {
		return blob.sha
	}
	return ""
}

// newSHA returns a synthetic 40-char hex string for content addressing.
// The format mirrors a real git SHA so any test assertion that uses a
// regex like /^[0-9a-f]{40}$/ continues to pass.
func (m *MockGitProvider) newSHA() string {
	m.commits++
	// Deterministic + monotonic so test logs read clearly. Format:
	// "ghmock0000000000000000000000000000000001" (40 chars).
	const padding = "ghmock00000000000000000000000000"
	tail := fmt.Sprintf("%08x", m.commits)
	return padding + tail
}

// mockPRToPublic / Ptr convert internal MockPR into the public type
// callers (sharko handlers) expect.
func mockPRToPublic(pr *MockPR) gitprovider.PullRequest {
	out := gitprovider.PullRequest{
		ID:           pr.Number,
		Title:        pr.Title,
		Description:  pr.Body,
		Author:       "sharko-e2e",
		Status:       pr.State,
		SourceBranch: pr.HeadBranch,
		TargetBranch: pr.BaseBranch,
		URL:          pr.URL,
	}
	if !pr.CreatedAt.IsZero() {
		out.CreatedAt = pr.CreatedAt.Format("2006-01-02T15:04:05Z")
	}
	if !pr.MergedAt.IsZero() {
		out.UpdatedAt = pr.MergedAt.Format("2006-01-02T15:04:05Z")
		out.ClosedAt = pr.MergedAt.Format("2006-01-02T15:04:05Z")
	} else if !pr.ClosedAt.IsZero() {
		out.UpdatedAt = pr.ClosedAt.Format("2006-01-02T15:04:05Z")
		out.ClosedAt = pr.ClosedAt.Format("2006-01-02T15:04:05Z")
	}
	return out
}

func mockPRToPublicPtr(pr *MockPR) *gitprovider.PullRequest {
	v := mockPRToPublic(pr)
	return &v
}
