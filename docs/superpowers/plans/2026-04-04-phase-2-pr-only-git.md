# Phase 2: PR-Only Git Flow — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove direct commit mode entirely — every Git operation goes through a PR with optional auto-merge.

**Architecture:** Delete `commitDirect`, remove `DefaultMode` from config, add `PRAutoMerge` bool. `commitChanges` always calls `commitViaPR`. When `PRAutoMerge` is true, call `MergePullRequest` after PR creation. Update `GitResult` to replace `Mode` with `Merged` bool and `PRID` int.

**Tech Stack:** Go 1.25.8, existing `gitprovider.GitProvider` interface (already has `MergePullRequest`)

---

## File Map

| File | Action | Responsibility |
|------|--------|---------------|
| `internal/orchestrator/types.go` | Modify | Remove `DefaultMode` from `GitOpsConfig`, add `PRAutoMerge bool`. Replace `Mode` in `GitResult` with `Merged bool` + `PRID int`. |
| `internal/orchestrator/git_helpers.go` | Modify | Delete `commitDirect`. `commitChanges` always calls `commitViaPR`. Add auto-merge logic after PR creation. |
| `internal/orchestrator/orchestrator_test.go` | Modify | Remove all `"direct"` mode references. Update `defaultGitOps()` and `prGitOps()`. All tests use PR mode. Add auto-merge and merge-failure tests. |
| `internal/api/system.go` | Modify | Replace `default_mode` in config response with `pr_auto_merge`. |
| `cmd/sharko/serve.go` | Modify | Remove `SHARKO_GITOPS_DEFAULT_MODE`. Add `SHARKO_GITOPS_PR_AUTO_MERGE`. |
| `docs/api-contract.md` | Modify | Update GitResult shape and config endpoint response. |
| `docs/user-guide.md` | Modify | Remove `SHARKO_GITOPS_DEFAULT_MODE` from env var table. |
| `docs/architecture.md` | Modify | Remove direct mode reference. |
| `README.md` | Modify | Remove `SHARKO_GITOPS_DEFAULT_MODE` from env var table. |

---

### Task 1: Update GitOpsConfig and GitResult types

**Files:**
- Modify: `internal/orchestrator/types.go:4-10` (GitOpsConfig)
- Modify: `internal/orchestrator/types.go:47-53` (GitResult)

- [ ] **Step 1: Update GitOpsConfig — remove DefaultMode, add PRAutoMerge**

Replace the `GitOpsConfig` struct:

```go
// GitOpsConfig holds gitops preferences (from server Helm values).
type GitOpsConfig struct {
	PRAutoMerge  bool   // true = auto-merge PRs after creation; false = manual approval
	BranchPrefix string // e.g. "sharko/"
	CommitPrefix string // e.g. "sharko:"
	BaseBranch   string // e.g. "main"
	RepoURL      string // Git repo URL for placeholder replacement
}
```

- [ ] **Step 2: Update GitResult — replace Mode with Merged + PRID**

Replace the `GitResult` struct:

```go
// GitResult holds the outcome of a gitops operation.
type GitResult struct {
	PRUrl      string `json:"pr_url,omitempty"`
	PRID       int    `json:"pr_id,omitempty"`
	Branch     string `json:"branch,omitempty"`
	Merged     bool   `json:"merged"`
	CommitSHA  string `json:"commit_sha,omitempty"`
	ValuesFile string `json:"values_file,omitempty"`
}
```

- [ ] **Step 3: Verify the file compiles**

Run: `go build ./internal/orchestrator/...`
Expected: Compilation errors in git_helpers.go and tests (references to Mode and DefaultMode). This is expected — we fix those in the next tasks.

- [ ] **Step 4: Commit**

```bash
git add internal/orchestrator/types.go
git commit -m "refactor: update GitOpsConfig and GitResult types for PR-only flow"
```

---

### Task 2: Rewrite git_helpers.go — remove commitDirect, add auto-merge

**Files:**
- Modify: `internal/orchestrator/git_helpers.go` (full rewrite)

- [ ] **Step 1: Replace the entire git_helpers.go**

```go
package orchestrator

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

// commitChanges creates a PR for the given file changes.
// Acquires the shared Git mutex to serialize all Git operations across concurrent requests.
// If PRAutoMerge is enabled, the PR is merged immediately after creation.
func (o *Orchestrator) commitChanges(ctx context.Context, files map[string][]byte, deletePaths []string, operation string) (*GitResult, error) {
	if o.gitMu != nil {
		o.gitMu.Lock()
		defer o.gitMu.Unlock()
	}

	// Generate a unique branch name.
	sanitized := strings.ReplaceAll(operation, " ", "-")
	sanitized = strings.ToLower(sanitized)
	randBytes := make([]byte, 4)
	_, _ = rand.Read(randBytes)
	suffix := hex.EncodeToString(randBytes)
	branchName := fmt.Sprintf("%s%s-%s", o.gitops.BranchPrefix, sanitized, suffix)

	if err := o.git.CreateBranch(ctx, branchName, o.gitops.BaseBranch); err != nil {
		return nil, fmt.Errorf("creating branch %q: %w", branchName, err)
	}

	commitMsg := fmt.Sprintf("%s %s", o.gitops.CommitPrefix, operation)

	for path, content := range files {
		if err := o.git.CreateOrUpdateFile(ctx, path, content, branchName, commitMsg); err != nil {
			return nil, fmt.Errorf("writing file %q on branch %q: %w", path, branchName, err)
		}
	}

	for _, path := range deletePaths {
		if err := o.git.DeleteFile(ctx, path, branchName, commitMsg); err != nil {
			return nil, fmt.Errorf("deleting file %q on branch %q: %w", path, branchName, err)
		}
	}

	pr, err := o.git.CreatePullRequest(ctx, commitMsg, operation, branchName, o.gitops.BaseBranch)
	if err != nil {
		return nil, fmt.Errorf("creating pull request: %w", err)
	}

	result := &GitResult{
		PRUrl:  pr.URL,
		PRID:   pr.ID,
		Branch: branchName,
	}

	// Auto-merge if configured.
	if o.gitops.PRAutoMerge {
		if mergeErr := o.git.MergePullRequest(ctx, pr.ID); mergeErr != nil {
			// PR created but merge failed — partial success.
			// Caller decides how to handle this (e.g. return 207).
			return result, fmt.Errorf("PR created but merge failed: %w", mergeErr)
		}
		result.Merged = true
	}

	return result, nil
}
```

- [ ] **Step 2: Verify git_helpers.go compiles**

Run: `go build ./internal/orchestrator/...`
Expected: Still errors in tests (they reference `defaultGitOps()` with `DefaultMode` and assert `Mode` field). We fix those next.

- [ ] **Step 3: Commit**

```bash
git add internal/orchestrator/git_helpers.go
git commit -m "refactor: remove commitDirect, add PR auto-merge to commitChanges"
```

---

### Task 3: Update tests — all PR mode, add auto-merge tests

**Files:**
- Modify: `internal/orchestrator/orchestrator_test.go`

- [ ] **Step 1: Update defaultGitOps() — remove DefaultMode, add PRAutoMerge**

Replace `defaultGitOps()` and remove `prGitOps()`:

```go
func defaultGitOps() GitOpsConfig {
	return GitOpsConfig{
		PRAutoMerge:  false,
		BranchPrefix: "sharko/",
		CommitPrefix: "sharko:",
		BaseBranch:   "main",
	}
}

func autoMergeGitOps() GitOpsConfig {
	cfg := defaultGitOps()
	cfg.PRAutoMerge = true
	return cfg
}
```

- [ ] **Step 2: Update all existing tests that check Git.Mode**

In `TestRegisterCluster_DirectMode`:
- Rename to `TestRegisterCluster_ManualPR`
- Remove `Mode` assertion, add `Merged` assertion:
```go
if result.Git.Merged {
    t.Error("expected Merged=false for manual PR mode")
}
if result.Git.PRUrl == "" {
    t.Error("expected PR URL")
}
```

In `TestRegisterCluster_PRMode`:
- Rename to `TestRegisterCluster_AutoMergePR`
- Use `autoMergeGitOps()` instead of `prGitOps()`
- Assert `result.Git.Merged == true`

In `TestDeregisterCluster`:
- Remove `result.Git.Mode != "direct"` check
- Assert `result.Git.Merged == false` (uses defaultGitOps which has PRAutoMerge=false)

In `TestAddAddon`:
- Remove `result.Mode != "direct"` check
- Assert `result.Merged == false`

In `TestRemoveAddon`:
- Remove `result.Mode != "direct"` check
- Assert `result.Merged == false`

- [ ] **Step 3: Add TestRegisterCluster_AutoMergeFails test**

```go
func TestRegisterCluster_AutoMergeFails(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	git.mergeErr = fmt.Errorf("merge conflict")
	orch := New(nil, defaultCreds(), argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:   "prod-eu",
		Addons: map[string]bool{"monitoring": true},
	})

	// RegisterCluster should return partial success when merge fails.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "partial" {
		t.Errorf("expected status 'partial', got %q", result.Status)
	}
	if result.Git == nil || result.Git.PRUrl == "" {
		t.Error("expected PR URL in partial result")
	}
	if result.Git.Merged {
		t.Error("expected Merged=false when merge fails")
	}
}
```

Add `mergeErr` to `mockGitProvider`:
```go
type mockGitProvider struct {
	// ... existing fields
	mergeErr  error
}

func (m *mockGitProvider) MergePullRequest(_ context.Context, _ int) error {
	return m.mergeErr
}
```

- [ ] **Step 4: Run all tests**

Run: `go test -race ./internal/orchestrator/ -v`
Expected: All tests pass, no races.

- [ ] **Step 5: Commit**

```bash
git add internal/orchestrator/orchestrator_test.go
git commit -m "test: update all tests for PR-only flow, add auto-merge failure test"
```

---

### Task 4: Handle merge failure as partial success in cluster.go

**Files:**
- Modify: `internal/orchestrator/cluster.go:84-97`

- [ ] **Step 1: Update RegisterCluster to handle merge-failure error from commitChanges**

The current code treats any `commitChanges` error as a Git failure. With auto-merge, `commitChanges` can return BOTH a result (PR created) AND an error (merge failed). Update the error handling:

```go
gitResult, err := o.commitChanges(ctx, files, nil, fmt.Sprintf("register cluster %s", req.Name))
if err != nil {
	if gitResult != nil {
		// PR created but merge failed — partial success with PR info.
		result.Status = "partial"
		result.CompletedSteps = steps
		result.FailedStep = "pr_merge"
		result.Error = err.Error()
		result.Message = "Cluster registered in ArgoCD and PR created, but auto-merge failed. Merge manually: " + gitResult.PRUrl
		result.Git = gitResult
		return result, nil
	}
	// Complete Git failure (couldn't even create PR).
	result.Status = "partial"
	result.CompletedSteps = steps
	result.FailedStep = "git_commit"
	result.Error = err.Error()
	result.Message = "Cluster registered in ArgoCD but values file commit failed. Manual Git commit required."
	return result, nil
}
```

- [ ] **Step 2: Apply the same pattern to DeregisterCluster and UpdateClusterAddons**

Both have the same `commitChanges` error handling block. Update each to check for `gitResult != nil` (PR created but merge failed) vs `gitResult == nil` (total Git failure).

- [ ] **Step 3: Run tests**

Run: `go test -race ./internal/orchestrator/ -v`
Expected: All pass.

- [ ] **Step 4: Commit**

```bash
git add internal/orchestrator/cluster.go
git commit -m "feat: handle PR merge failure as partial success in cluster operations"
```

---

### Task 5: Update server config and startup

**Files:**
- Modify: `internal/api/system.go:149-154`
- Modify: `cmd/sharko/serve.go:193-199`

- [ ] **Step 1: Update system.go config endpoint**

Replace the gitops section in `handleGetConfig`:

```go
"gitops": map[string]interface{}{
    "pr_auto_merge": s.gitopsCfg.PRAutoMerge,
    "branch_prefix": s.gitopsCfg.BranchPrefix,
    "commit_prefix": s.gitopsCfg.CommitPrefix,
    "base_branch":   s.gitopsCfg.BaseBranch,
},
```

- [ ] **Step 2: Update serve.go — replace DefaultMode with PRAutoMerge**

Replace the `gitopsCfg` block:

```go
gitopsCfg := orchestrator.GitOpsConfig{
    PRAutoMerge:  os.Getenv("SHARKO_GITOPS_PR_AUTO_MERGE") == "true",
    BranchPrefix: getEnvDefault("SHARKO_GITOPS_BRANCH_PREFIX", "sharko/"),
    CommitPrefix: getEnvDefault("SHARKO_GITOPS_COMMIT_PREFIX", "sharko:"),
    BaseBranch:   getEnvDefault("SHARKO_GITOPS_BASE_BRANCH", "main"),
    RepoURL:      os.Getenv("SHARKO_GITOPS_REPO_URL"),
}
```

- [ ] **Step 3: Build and vet**

Run: `go build ./... && go vet ./...`
Expected: Clean.

- [ ] **Step 4: Run full test suite**

Run: `go test ./...`
Expected: All pass.

- [ ] **Step 5: Commit**

```bash
git add internal/api/system.go cmd/sharko/serve.go
git commit -m "feat: replace SHARKO_GITOPS_DEFAULT_MODE with SHARKO_GITOPS_PR_AUTO_MERGE"
```

---

### Task 6: Update documentation

**Files:**
- Modify: `README.md`
- Modify: `docs/user-guide.md`
- Modify: `docs/architecture.md`
- Modify: `docs/api-contract.md`

- [ ] **Step 1: README.md �� update env var table**

Remove the `SHARKO_GITOPS_DEFAULT_MODE` row. Add:
```
| `SHARKO_GITOPS_PR_AUTO_MERGE` | Auto-merge PRs after creation | `false` |
```

- [ ] **Step 2: docs/user-guide.md — update env var table**

Same replacement as README.

- [ ] **Step 3: docs/architecture.md — remove direct mode reference**

Replace the line about `SHARKO_GITOPS_DEFAULT_MODE` with:
"All Git operations go through pull requests. When `SHARKO_GITOPS_PR_AUTO_MERGE` is true, PRs are merged immediately after creation. When false (default), PRs require manual approval."

- [ ] **Step 4: docs/api-contract.md — update GitResult shape**

Replace all `"mode": "direct"` or `"mode": "pr"` occurrences with the new shape:
```json
{
    "pr_url": "https://github.com/...",
    "pr_id": 42,
    "branch": "sharko/register-cluster-prod-eu-a1b2c3d4",
    "merged": false,
    "values_file": "configuration/addons-clusters-values/prod-eu.yaml"
}
```

Update the config endpoint response to show `pr_auto_merge` instead of `default_mode`.

- [ ] **Step 5: Commit**

```bash
git add README.md docs/user-guide.md docs/architecture.md docs/api-contract.md
git commit -m "docs: update all docs for PR-only git flow"
```

---

### Task 7: Final verification

- [ ] **Step 1: Grep for any remaining direct-mode references**

Run: `grep -rn "commitDirect\|DefaultMode\|GITOPS_DEFAULT_MODE\|\"direct\"" --include="*.go" --include="*.yaml" .`
Expected: No matches in Go or YAML files. (Design docs in `docs/design/` may still reference the old pattern as historical context — that's fine.)

- [ ] **Step 2: Run full quality gates**

```bash
go build ./...
go vet ./...
go test -race ./...
```
Expected: All pass, no races.

- [ ] **Step 3: Security grep**

```bash
grep -rn "scrdairy\|merck\|msd\.com\|mahi-techlabs\|merck-ahtl" --include="*.go" --include="*.ts" --include="*.yaml" . | grep -v node_modules | grep -v .git/
```
Expected: Empty.
