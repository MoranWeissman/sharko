package orchestrator

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
)

// detectConflicts checks whether any of the target files have been modified on
// the base branch since the caller last read them.  This is a best-effort,
// defensive check — it does not block the operation but logs a warning so
// operators can investigate unexpected concurrent modifications.
//
// A proper conflict will be caught by GitHub's PR merge check anyway; this
// gives an early, human-readable signal in the server logs.
func (o *Orchestrator) detectConflicts(ctx context.Context, files map[string][]byte) {
	for path, localContent := range files {
		remoteContent, err := o.git.GetFileContent(ctx, path, o.gitops.BaseBranch)
		if err != nil {
			// File doesn't exist yet on the base branch — no conflict possible.
			continue
		}
		if string(remoteContent) != string(localContent) {
			slog.Warn("Possible write conflict detected: file has changed on base branch since read",
				"file", path,
				"base_branch", o.gitops.BaseBranch,
			)
		}
	}
}

// commitChanges creates a PR for the given file changes.
// Acquires the shared Git mutex to serialize all Git operations across concurrent requests.
// If PRAutoMerge is enabled, the PR is merged immediately after creation.
func (o *Orchestrator) commitChanges(ctx context.Context, files map[string][]byte, deletePaths []string, operation string) (*GitResult, error) {
	if o.gitMu != nil {
		o.gitMu.Lock()
		defer o.gitMu.Unlock()
	}

	// Check for potential conflicts before creating the branch.
	// This is a defensive log-only check; GitHub enforces the real merge guard.
	o.detectConflicts(ctx, files)

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

	if len(files) > 0 {
		if err := o.git.BatchCreateFiles(ctx, files, branchName, commitMsg); err != nil {
			return nil, fmt.Errorf("writing files on branch %q: %w", branchName, err)
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
