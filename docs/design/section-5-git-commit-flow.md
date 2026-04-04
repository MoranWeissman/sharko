# Section 5 — Git Commit Flow

> Simple section. One rule, no exceptions.

---

## Decision: Every Change Goes Through a PR. Always.

No direct commits to main. Ever. No "direct mode." No per-operation override. No configuration option to bypass this.

Every write operation Sharko performs results in a Pull Request:

```
sharko add-cluster    → opens PR
sharko remove-cluster → opens PR
sharko update-cluster → opens PR
sharko add-addon      → opens PR
sharko remove-addon   → opens PR
sharko init           → opens PR (for the repo structure; ArgoCD bootstrap happens after merge)
```

The PR exists in Git history as an auditable record of every change.

---

## The Only Configuration: Auto-Merge or Manual Approval

```yaml
# Helm values
gitops:
  prAutoMerge: true    # Sharko merges PRs automatically after all pre-merge steps pass
  prAutoMerge: false   # PR stays open, human reviews and merges
```

### Manual Approval (prAutoMerge: false)

Default. Safest. The human-in-the-loop path.

```
1. Sharko opens PR
2. Sharko performs pre-merge steps (create secrets on remote cluster, etc.)
3. PR stays open in GitHub/GitLab
4. Human reviews the diff
5. Human merges
6. ArgoCD picks up the change
```

### Auto-Merge (prAutoMerge: true)

For automation and IDP workflows where the request was already validated by the calling system.

```
1. Sharko opens PR
2. Sharko performs pre-merge steps (create secrets on remote cluster, etc.)
3. Sharko merges the PR via Git API
4. ArgoCD picks up the change
```

Auto-merge is NOT a direct commit. The PR still exists. The branch still exists in history. The merge is auditable. It's just that Sharko does the merge instead of a human clicking "Merge."

---

## What This Means for the Codebase

### Remove Direct Commit Mode

The current orchestrator has `commitDirect` and `commitViaPR` methods in `internal/orchestrator/git_helpers.go`. The `commitDirect` method commits straight to the base branch without a PR.

**Action: Remove `commitDirect` entirely.** Remove the `DefaultMode` field from `GitOpsConfig`. Remove any `if mode == "direct"` branching. The `commitChanges` method always calls `commitViaPR`.

The only branching in `commitChanges` should be:
```go
func (o *Orchestrator) commitChanges(...) (*GitResult, error) {
    // Always create a PR
    result, err := o.commitViaPR(ctx, files, deletePaths, operation)
    if err != nil {
        return nil, err
    }
    
    // Auto-merge if configured
    if o.gitops.PRAutoMerge {
        if err := o.git.MergePullRequest(ctx, result.PRID); err != nil {
            // PR created but merge failed — return partial success
            result.Status = "partial"
            result.Error = "PR created but auto-merge failed: " + err.Error()
            return result, nil
        }
    }
    
    return result, nil
}
```

### Remove Related Configuration

- Remove `SHARKO_GITOPS_DEFAULT_MODE` env var
- Remove `gitops.defaultMode` from any Helm values
- Remove references to "direct mode" from docs, API contract, CLI help text
- The `GitOpsConfig` struct only needs: `PRAutoMerge bool`, `BranchPrefix string`, `CommitPrefix string`, `BaseBranch string`, `RepoURL string`

### Update API Response

The `GitResult` response no longer has a `mode` field (it's always "pr"):

```json
{
  "pr_url": "https://github.com/org/addons/pull/42",
  "pr_id": 42,
  "branch": "sharko/add-cluster-prod-eu",
  "merged": true,
  "values_file": "configuration/addons-clusters-values/prod-eu.yaml"
}
```

The `merged` boolean tells the caller whether the PR was auto-merged or is waiting for approval.

---

## PR Branch Naming

Convention:
```
{branchPrefix}{operation}-{timestamp}
```

Examples:
```
sharko/add-cluster-prod-eu-1712345678
sharko/remove-cluster-test-us-1712345679
sharko/add-addon-cert-manager-1712345680
sharko/init-1712345681
```

Configurable via:
```yaml
gitops:
  branchPrefix: sharko/     # default
  commitPrefix: "sharko:"   # used in commit messages
  baseBranch: main           # target branch for PRs
```

---

## Summary

| Question | Answer |
|----------|--------|
| Can Sharko commit directly to main? | No. Never. |
| What's the only Git operation? | Open a PR. |
| How many modes? | Two: auto-merge or manual approval. |
| Where is this configured? | Helm values: `gitops.prAutoMerge` (default: false) |
| Is the PR auditable? | Yes. Branch, diff, and merge are all in Git history. |
| What about the existing direct commit code? | Remove it. |
