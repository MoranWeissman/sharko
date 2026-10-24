package orchestrator

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/MoranWeissman/sharko/internal/events"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/logging"
)

// readFileIfExists attempts to read a file from the base branch. Returns the
// content and true if the file exists, or nil and false if the file does not
// exist or any error occurs during retrieval. This is intended for dry-run
// previews where missing files are an expected, non-error condition (creates).
//
// It is LENIENT ON PURPOSE and therefore only safe where a swallowed error
// costs nothing worse than a slightly-wrong preview or an "" default. If the
// value you are reading becomes the BASE of a file you then write back, use
// readFileForRewrite instead — see the note on that function for what goes
// wrong otherwise.
func (o *Orchestrator) readFileIfExists(ctx context.Context, filePath string) ([]byte, bool) {
	content, err := o.git.GetFileContent(ctx, filePath, o.gitops.BaseBranch)
	if err != nil {
		return nil, false
	}
	return content, true
}

// readFileForRewrite reads a file that is about to be edited and written back,
// and refuses to guess. It is the fail-closed sibling of readFileIfExists.
//
// The distinction it draws is the whole point: a genuinely missing file
// (gitprovider.ErrFileNotFound) is an ordinary answer — exists is false, err
// is nil, and the caller starts from an empty document. ANY other failure
// (rate limit, expired token, network blip, a 500 from the Git host) returns
// an error, because the caller cannot tell the two apart from a nil body.
//
// Why that matters: a read-modify-write built on a swallowed error rewrites
// the file from an empty default. For the fleet record that means a pull
// request replacing every registered cluster with the single one being added
// — and once it merges, the reconciler's orphan sweep deletes the ArgoCD
// connection Secret of every other cluster in the fleet. One transient read
// error, the whole fleet gone. Stopping with a plain-English error costs an
// operator one retry.
func (o *Orchestrator) readFileForRewrite(ctx context.Context, filePath string) (content []byte, exists bool, err error) {
	content, err = o.git.GetFileContent(ctx, filePath, o.gitops.BaseBranch)
	if err == nil {
		return content, true, nil
	}
	if errors.Is(err, gitprovider.ErrFileNotFound) {
		return nil, false, nil
	}
	return nil, false, fmt.Errorf(
		"could not read %s from the %s branch, so Sharko stopped instead of rewriting that file from scratch and losing whatever is in it: %w",
		filePath, o.gitops.BaseBranch, err)
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

// buildPRBody writes the plain-English pull-request description every
// commitChangesWithMeta call opens: a one-line summary of what the
// operation string already says, followed by the exact list of files this
// PR touches, so a reviewer sees what changed without opening the diff
// first.
//
// Before this (PR #657 follow-up), the body was just the raw lowercase
// operation string repeated verbatim ("add addon cert-manager", "enable
// addon keda on cluster prod-eu") with no file list and no structure — the
// same string that was already the commit message, read a second time.
//
// This is prose for a human, not a machine-read format: nothing in
// Sharko parses a PR body back (confirmed — the only field any code reads
// off a PullRequest after creation is the dashboard's plain display of
// Description/pr.Body; nothing greps it for a marker), so there is nothing
// here that has to stay byte-for-byte.
func buildPRBody(operation string, files map[string][]byte, deletePaths []string) string {
	var sb strings.Builder
	sb.WriteString(capitalizeFirst(operation))
	sb.WriteString(".\n")

	if len(files) > 0 || len(deletePaths) > 0 {
		sb.WriteString("\nFiles changed:\n")

		changed := make([]string, 0, len(files))
		for p := range files {
			changed = append(changed, p)
		}
		sort.Strings(changed)
		for _, p := range changed {
			sb.WriteString("- " + p + "\n")
		}

		removed := append([]string(nil), deletePaths...)
		sort.Strings(removed)
		for _, p := range removed {
			sb.WriteString("- " + p + " (removed)\n")
		}
	}

	sb.WriteString("\nOpened automatically by Sharko.\n")
	return sb.String()
}

// capitalizeFirst upper-cases the first rune of s and leaves the rest
// untouched — every operation string passed to commitChangesWithMeta is
// already a short, well-formed English phrase ("add addon cert-manager"),
// so this is the entire job: turn it into a sentence.
func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// PRNumber is a pull request's number. A Git provider that left the number
// out gets it from the end of the pull request's address (".../pull/12").
// When neither has it, it is 0: callers then leave the number out and say
// "a pull request", so no answer ever says "PR #0" (EP3 dry run round S,
// S14).
func PRNumber(id int, url string) int {
	if id > 0 {
		return id
	}
	tail := strings.TrimRight(url, "/")
	if i := strings.LastIndex(tail, "/"); i >= 0 {
		tail = tail[i+1:]
	}
	if n, err := strconv.Atoi(tail); err == nil && n > 0 {
		return n
	}
	return 0
}

// PullRequestPhrase names a pull request inside a sentence: "pull request
// #12", or "a pull request" when the number is not known (0).
func PullRequestPhrase(n int) string {
	if n > 0 {
		return fmt.Sprintf("pull request #%d", n)
	}
	return "a pull request"
}

// ErrPRNotRecorded means Sharko opened a pull request that carries
// after-merge work but could not record it in the PR tracker, and the pull
// request did not merge at once. Nothing would ever finish that work, so
// the caller must say so (EP3 dry run round R, R1). The GitResult that
// comes with it names the pull request.
var ErrPRNotRecorded = errors.New("the pull request could not be recorded in the PR tracker")

// ErrAutoMergeFailed means the pull request was opened (and recorded) but
// the merge Sharko tried at once did not go through. The pull request stays
// open, so the GitResult that comes with it names it, and a later merge by
// anyone still finishes the work (EP3 dry run round S, S13). The error text
// still starts "PR created but merge failed:" as before.
var ErrAutoMergeFailed = errors.New("PR created but merge failed")

// PRNotRecordedMessage is the answer when ErrPRNotRecorded comes back.
// action is what the pull request was for, in plain words ("removal",
// "un-adopt", "adopt", "take over"). Exported so tests pin it by exact
// text.
func PRNotRecordedMessage(action, prURL string) string {
	return fmt.Sprintf("Sharko opened pull request %s but could not record it, so it could not finish the %s after the merge. Nothing has changed on the cluster's ArgoCD connection. Close that pull request without merging it, then try again.", prURL, action)
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

	pr, err := o.git.CreatePullRequest(ctx, commitMsg, buildPRBody(operation, files, deletePaths), branchName, o.gitops.BaseBranch)
	if err != nil {
		// V3 E1: surface the PR-open failure as a k8s Warning event so
		// operators see it via `kubectl get events`. Message is a plain-English
		// operational statement — the operation name is a Sharko-internal label
		// (e.g. "register cluster prod-eu"), never any secret material.
		o.emitWarning(events.ReasonPROpenFailed,
			fmt.Sprintf("Failed to open a pull request for operation %q: the Git provider rejected the request.", operation))
		return nil, fmt.Errorf("creating pull request: %w", err)
	}

	// A provider that left the number out still gives the address, so the
	// number is read from there. Everything below (the tracker, the merge,
	// the answer) uses this one number.
	pr.ID = PRNumber(pr.ID, pr.URL)
	result := &GitResult{
		PRUrl:  pr.URL,
		PRID:   pr.ID,
		Branch: branchName,
	}

	// Track the PR centrally. A tracker write failure does not fail an
	// ordinary operation; the user-visible PR has already been created at
	// this point. It does matter for a PR that carries after-merge work
	// (meta.FollowUp): nothing would ever do that work, so unless the PR
	// merges right here the caller gets ErrPRNotRecorded (EP3 dry run round
	// R, R1). nil tracker (test seam) and empty OperationCode (legacy
	// commitChanges path) are silent skips.
	var trackErr error
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
		trackErr = o.prTracker.TrackPR(ctx, TrackedPR{
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
			FollowUp:   meta.FollowUp,
		})
	}

	// Auto-merge if configured. A per-request AutoMergeOverride wins over
	// the connection-level default — resolveAutoMerge centralises the
	// precedence rule so every call site uses the same semantics.
	autoMerge := resolveAutoMerge(meta.AutoMergeOverride, o.gitops.PRAutoMerge)
	notRecorded := trackErr != nil && meta.FollowUp != nil
	if trackErr != nil {
		logging.LoggerFromContext(ctx).Warn("could not record a pull request in the PR tracker",
			"pr_id", pr.ID, "operation", meta.OperationCode, "cluster", meta.Cluster, "error", trackErr)
	}
	if !autoMerge && notRecorded {
		return result, fmt.Errorf("%w: %v", ErrPRNotRecorded, trackErr)
	}
	if autoMerge {
		if mergeErr := o.git.MergePullRequest(ctx, pr.ID); mergeErr != nil {
			if notRecorded {
				return result, fmt.Errorf("%w: %v (and the merge failed: %v)", ErrPRNotRecorded, trackErr, mergeErr)
			}
			// PR created but merge failed — partial success.
			// Caller decides how to handle this (e.g. return 207).
			return result, fmt.Errorf("%w: %w", ErrAutoMergeFailed, mergeErr)
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
		if titleNamesCluster(prs[i].Title, pattern) {
			return &prs[i], nil
		}
	}
	return nil, nil
}

// titleNamesCluster reports whether title contains pattern AND the cluster
// name in it ends there. A plain substring check let "take over cluster
// prod" match the pull request for "prod-eu", so a retry for one cluster
// was answered with another cluster's pull request.
func titleNamesCluster(title, pattern string) bool {
	for start := 0; start <= len(title); {
		idx := strings.Index(title[start:], pattern)
		if idx == -1 {
			return false
		}
		end := start + idx + len(pattern)
		if end == len(title) || !isClusterNameChar(title[end]) {
			return true
		}
		start += idx + 1
	}
	return false
}

func isClusterNameChar(c byte) bool {
	return c == '-' || c == '.' || c == '_' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}
