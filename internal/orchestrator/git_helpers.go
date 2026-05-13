package orchestrator

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
)

// fileAction returns "update" if the file exists on the base branch, "create" otherwise.
func (o *Orchestrator) fileAction(ctx context.Context, filePath string) string {
	_, err := o.git.GetFileContent(ctx, filePath, o.gitops.BaseBranch)
	if err != nil {
		return "create"
	}
	return "update"
}

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

// commitChanges creates a PR for the given file changes WITHOUT tracking it
// in the prtracker. Use commitChangesWithMeta when the call site has the
// canonical operation code, cluster, and addon — which is the new default
// for V125-1-6. Kept as a thin wrapper for paths that genuinely cannot
// supply metadata (none currently expected — every commitChanges caller
// has been migrated to commitChangesWithMeta in V125-1-6).
func (o *Orchestrator) commitChanges(ctx context.Context, files map[string][]byte, deletePaths []string, operation string) (*GitResult, error) {
	return o.commitChangesWithMeta(ctx, files, deletePaths, operation, PRMetadata{})
}

// commitChangesWithMeta creates a PR for the given file changes and (when
// o.prTracker is set AND meta.OperationCode is non-empty) tracks the PR
// in the dashboard. Acquires the shared Git mutex to serialize all Git
// operations across concurrent requests. If PRAutoMerge is enabled, the
// PR is merged immediately after creation.
//
// V125-1-6: this is the funnel that ensures every Sharko-created PR
// surfaces on the dashboard PR panel — previously, only ad-hoc handler-
// level TrackPR calls did, so register-cluster, adopt-cluster, init,
// batch-register, and 5 orchestrator addon ops were silently missing
// from the dashboard.
func (o *Orchestrator) commitChangesWithMeta(ctx context.Context, files map[string][]byte, deletePaths []string, operation string, meta PRMetadata) (*GitResult, error) {
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

	// Track the PR centrally (V125-1-6). Best-effort — a tracker write
	// failure must not fail the operation; the user-visible PR has
	// already been created at this point. nil tracker (test seam) and
	// empty OperationCode (legacy commitChanges path) are silent skips.
	if o.prTracker != nil && meta.OperationCode != "" {
		title := meta.Title
		if title == "" {
			title = commitMsg
		}
		user := meta.User
		if user == "" {
			user = "system"
		}
		source := meta.Source
		if source == "" {
			source = "api"
		}
		_ = o.prTracker.TrackPR(ctx, TrackedPR{
			PRID:       pr.ID,
			PRUrl:      pr.URL,
			PRBranch:   branchName,
			PRTitle:    title,
			PRBase:     o.gitops.BaseBranch,
			Cluster:    meta.Cluster,
			Addon:      meta.Addon,
			Operation:  meta.OperationCode,
			User:       user,
			Source:     source,
			CreatedAt:  time.Now(),
			LastStatus: "open",
		})
	}

	// Auto-merge if configured.
	if o.gitops.PRAutoMerge {
		if mergeErr := o.git.MergePullRequest(ctx, pr.ID); mergeErr != nil {
			// PR created but merge failed — partial success.
			// Caller decides how to handle this (e.g. return 207).
			return result, fmt.Errorf("PR created but merge failed: %w", mergeErr)
		}
		result.Merged = true
		// Clean up branch after merge (best-effort).
		if delErr := o.git.DeleteBranch(ctx, branchName); delErr != nil {
			slog.Warn("failed to delete branch after merge", "branch", branchName, "error", delErr)
		}
	}

	return result, nil
}

// findOpenPRForCluster searches for an existing open PR that matches a specific
// cluster operation (e.g. "register", "remove", "adopt"). This enables idempotent
// retry: if a previous attempt created a PR but failed later, re-running the
// operation finds the existing PR instead of creating a duplicate.
func (o *Orchestrator) findOpenPRForCluster(ctx context.Context, clusterName, operation string) (*gitprovider.PullRequest, error) {
	prs, err := o.git.ListPullRequests(ctx, "open")
	if err != nil {
		return nil, fmt.Errorf("listing open PRs: %w", err)
	}

	// Match PRs by title pattern: "<prefix> <operation> cluster <name>"
	pattern := fmt.Sprintf("%s %s cluster %s", o.gitops.CommitPrefix, operation, clusterName)
	for i := range prs {
		if strings.Contains(prs[i].Title, pattern) {
			return &prs[i], nil
		}
	}
	return nil, nil
}
