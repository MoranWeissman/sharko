package orchestrator

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

// commitChanges handles the gitops flow: direct commit or PR based on config.
func (o *Orchestrator) commitChanges(ctx context.Context, files map[string][]byte, deletePaths []string, operation string) (*GitResult, error) {
	if o.gitops.DefaultMode == "pr" {
		return o.commitViaPR(ctx, files, deletePaths, operation)
	}
	return o.commitDirect(ctx, files, deletePaths, operation)
}

// commitDirect commits changes directly to the base branch.
func (o *Orchestrator) commitDirect(ctx context.Context, files map[string][]byte, deletePaths []string, operation string) (*GitResult, error) {
	commitMsg := fmt.Sprintf("%s %s", o.gitops.CommitPrefix, operation)
	branch := o.gitops.BaseBranch

	for path, content := range files {
		if err := o.git.CreateOrUpdateFile(ctx, path, content, branch, commitMsg); err != nil {
			return nil, fmt.Errorf("writing file %q: %w", path, err)
		}
	}

	for _, path := range deletePaths {
		if err := o.git.DeleteFile(ctx, path, branch, commitMsg); err != nil {
			return nil, fmt.Errorf("deleting file %q: %w", path, err)
		}
	}

	return &GitResult{
		Mode:   "direct",
		Branch: branch,
	}, nil
}

// commitViaPR creates a feature branch, commits changes, and opens a PR.
func (o *Orchestrator) commitViaPR(ctx context.Context, files map[string][]byte, deletePaths []string, operation string) (*GitResult, error) {
	// Generate a unique branch name.
	sanitized := strings.ReplaceAll(operation, " ", "-")
	sanitized = strings.ToLower(sanitized)
	randBytes := make([]byte, 4)
	rand.Read(randBytes)
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

	return &GitResult{
		Mode:   "pr",
		PRUrl:  pr.URL,
		Branch: branchName,
	}, nil
}
