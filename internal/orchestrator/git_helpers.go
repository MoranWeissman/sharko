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
