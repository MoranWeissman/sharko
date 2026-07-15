package orchestrator

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/MoranWeissman/sharko/internal/logging"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
)

// readFileIfExists attempts to read a file from the base branch. Returns the
// content and true if the file exists, or nil and false if the file does not
// exist or any error occurs during retrieval. This is intended for dry-run
// previews where missing files are an expected, non-error condition (creates).
func (o *Orchestrator) readFileIfExists(ctx context.Context, filePath string) ([]byte, bool) {
	content, err := o.git.GetFileContent(ctx, filePath, o.gitops.BaseBranch)
	if err != nil {
		return nil, false
	}
	return content, true
}

// fileAction returns "update" if the file exists on the base branch, "create" otherwise.
// Reuses readFileIfExists so the existence check and content read for dry-run diffs
// share a single round-trip to the git provider.
func (o *Orchestrator) fileAction(ctx context.Context, filePath string) string {
	if _, exists := o.readFileIfExists(ctx, filePath); exists {
		return "update"
	}
	return "create"
}

// detectConflicts checks whether any of the target files have been modified on
// the base branch since the caller last read them.  This is a best-effort,
// defensive check — it does not block the operation but logs a warning so
// operators can investigate unexpected concurrent modifications.
//
// A proper conflict will be caught by GitHub's PR merge check anyway; this
// gives an early, human-readable signal in the server logs.
func (o *Orchestrator) detectConflicts(ctx context.Context, files map[string][]byte) {
	log := logging.LoggerFromContext(ctx)
	for path, localContent := range files {
		remoteContent, err := o.git.GetFileContent(ctx, path, o.gitops.BaseBranch)
		if err != nil {
			// File doesn't exist yet on the base branch — no conflict possible.
			continue
		}
		if string(remoteContent) != string(localContent) {
			log.Warn("Possible write conflict detected: file has changed on base branch since read",
				"file", path,
				"base_branch", o.gitops.BaseBranch,
			)
		}
	}
}

// prMeta is the single anti-drift constructor for PRMetadata. It REQUIRES
// the auto-merge choice as its first parameter so a new PR-opening call
// site physically cannot forget to thread it — the historical drift that
// produced "sometimes auto-merged, sometimes not" (V2-cleanup-23). Pass
// nil for autoMerge to mean "fall back to the connection default"; pass a
// non-nil *bool to honor an explicit per-request choice.
//
// opCode and title are always set; cluster and addon are passed as ""
// when the operation has no associated cluster/addon. User and Source are
// intentionally NOT parameters — they are derived/defaulted inside
// commitChangesWithMeta ("system"/"api") and no current call site sets
// them, so keeping them out of the signature keeps every call site a
// readable one-liner.
//
// EVERY PRMetadata value in this package is built here. Constructing a
// bare PRMetadata{} literal at a new call site is the exact mistake this
// constructor exists to prevent — route through prMeta instead.
func (o *Orchestrator) prMeta(autoMerge *bool, opCode, title, cluster, addon string) PRMetadata {
	return PRMetadata{
		OperationCode:     opCode,
		Cluster:           cluster,
		Addon:             addon,
		Title:             title,
		AutoMergeOverride: autoMerge,
	}
}

// commitChanges creates a PR for the given file changes WITHOUT tracking it
// in the prtracker. Use commitChangesWithMeta when the call site has the
// canonical operation code, cluster, and addon (the default). Kept as a
// thin wrapper for paths that genuinely cannot supply metadata.
//
// The empty operation code means the PR is not tracked; the nil auto-merge
// choice means it follows the connection default. Both are deliberate for
// this metadata-less escape hatch — every metadata-bearing path uses
// prMeta + commitChangesWithMeta instead.
func (o *Orchestrator) commitChanges(ctx context.Context, files map[string][]byte, deletePaths []string, operation string) (*GitResult, error) {
	return o.commitChangesWithMeta(ctx, files, deletePaths, operation, o.prMeta(nil, "", "", "", ""))
}

// commitChangesWithMeta creates a PR for the given file changes and (when
// o.prTracker is set AND meta.OperationCode is non-empty) tracks the PR
// in the dashboard. Acquires the shared Git mutex to serialize all Git
// operations across concurrent requests. If PRAutoMerge is enabled, the
// PR is merged immediately after creation.
//
// This is the funnel that ensures every Sharko-created PR surfaces on
// the dashboard PR panel.
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

	// Track the PR centrally. Best-effort — a tracker write failure must
	// not fail the operation; the user-visible PR has already been
	// created at this point. nil tracker (test seam) and empty
	// OperationCode (legacy commitChanges path) are silent skips.
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

	// Auto-merge if configured. A per-request AutoMergeOverride wins over
	// the connection-level default — resolveAutoMerge centralises the
	// precedence rule so every call site uses the same semantics.
	autoMerge := resolveAutoMerge(meta.AutoMergeOverride, o.gitops.PRAutoMerge)
	if autoMerge {
		if mergeErr := o.git.MergePullRequest(ctx, pr.ID); mergeErr != nil {
			// PR created but merge failed — partial success.
			// Caller decides how to handle this (e.g. return 207).
			return result, fmt.Errorf("PR created but merge failed: %w", mergeErr)
		}
		result.Merged = true
		// Clean up the source branch after every Sharko-merged PR.
		// Best-effort — a DeleteBranch failure (e.g. AzureDevOps's "not
		// yet implemented", branch already deleted, transient API
		// hiccup) is logged but never fails the merge operation. The
		// branch is hygienic, not load-bearing.
		if delErr := o.git.DeleteBranch(ctx, branchName); delErr != nil {
			logging.LoggerFromContext(ctx).Warn("failed to delete branch after merge",
				"branch", branchName, "error", delErr)
		}
	}

	return result, nil
}

// ResolveAutoMerge picks the effective auto-merge decision for a single
// operation. Per-request override (when non-nil) wins over the connection
// default. nil means "fall back to the connection default" — preserves
// back-compat for legacy clients that don't send the field.
//
// Pure function, safe to call from any handler without mutating shared
// state. NEVER assign to o.gitops.PRAutoMerge — that's a global shared
// across concurrent requests and would race.
//
// Exported so the init handler (and any other non-orchestrator callers
// of MergePullRequest) can reuse the same precedence rule without
// reimplementing it.
func ResolveAutoMerge(reqAutoMerge *bool, connDefault bool) bool {
	if reqAutoMerge != nil {
		return *reqAutoMerge
	}
	return connDefault
}

// resolveAutoMerge is the unexported alias used internally by the
// orchestrator package. Kept as a thin wrapper so the package's own
// call sites stay short and the public surface (ResolveAutoMerge) is
// the canonical name.
func resolveAutoMerge(reqAutoMerge *bool, connDefault bool) bool {
	return ResolveAutoMerge(reqAutoMerge, connDefault)
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
