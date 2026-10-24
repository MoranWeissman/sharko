package api

import (
	"context"
	"log/slog"
	"strconv"
	"strings"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
)

// resolvePendingRegistrations queries the Git provider for open pull
// requests and returns the subset that match the orchestrator's
// registration-PR title pattern: "<CommitPrefix> register cluster <name>"
// (with an optional " (kubeconfig provider)" suffix when the PR is for a
// kubeconfig-based register). This is intentionally identical to the
// matcher used by internal/orchestrator/git_helpers.go's
// findOpenPRForCluster — same contract, derived from the same emitter.
//
// The manual-mode register flow opens a PR but does NOT mutate
// managed-clusters.yaml until merge. Without this surface, UI panels
// would treat the pending cluster as if it half-existed.
//
// Defensive degrade: a provider error (rate limit, transient 5xx, auth
// blip) returns an EMPTY slice + a log warning rather than failing the
// entire /clusters endpoint. The cost of "we couldn't list PRs right
// now" is a missing pending-registrations section on the next refresh
// — not a 500 that takes down the whole clusters page.
//
// The return type is always a non-nil slice. Callers do NOT need to
// nil-check (never let a nil array reach the FE).
func resolvePendingRegistrations(
	ctx context.Context,
	gp gitprovider.GitProvider,
	commitPrefix string,
) []models.PendingRegistration {
	out := []models.PendingRegistration{}

	if gp == nil {
		return out
	}

	prs, err := gp.ListPullRequests(ctx, "open")
	if err != nil {
		// Defensive: don't 500 the entire /clusters endpoint because the
		// PR-list call hit a rate limit. Log and degrade to empty.
		slog.Warn("list_open_prs_for_pending_registrations: degrading to empty",
			"err", err)
		return out
	}

	// Title patterns emitted by the orchestrator, one per operation.
	// Mirrors the matcher in internal/orchestrator/git_helpers.go.
	//
	// "<commit-prefix> register cluster <name>"
	// "<commit-prefix> remove cluster <name>"
	// "<commit-prefix> adopt cluster <name>"
	// "<commit-prefix> take over cluster <name>"
	//   |-------- common header -------|
	//
	// The header includes the commit prefix and the space after it, so
	// "unadopt cluster" never matches "adopt cluster". Anything after the header is suffix
	// decoration (e.g. " (kubeconfig provider)") — the cluster name is
	// the first whitespace-bounded token after the header.
	prefix := strings.TrimSpace(commitPrefix)
	headers := []struct {
		header    string
		operation string
	}{
		{prefix + " register cluster ", models.PendingOperationRegister},
		{prefix + " remove cluster ", models.PendingOperationRemove},
		{prefix + " adopt cluster ", models.PendingOperationAdopt},
		{prefix + " take over cluster ", models.PendingOperationTakeover},
	}

	for i := range prs {
		title := prs[i].Title
		for _, h := range headers {
			idx := strings.Index(title, h.header)
			if idx == -1 {
				continue
			}
			rest := title[idx+len(h.header):]
			// Cluster name = first whitespace-bounded token of `rest`.
			// Examples:
			//   "sharko: register cluster prod-eu"              -> "prod-eu"
			//   "sharko: register cluster prod-eu (kubeconfig provider)"
			//                                                   -> "prod-eu"
			name := rest
			if sp := strings.IndexAny(rest, " \t"); sp != -1 {
				name = rest[:sp]
			}
			name = strings.TrimSpace(name)
			if name == "" {
				break
			}
			out = append(out, models.PendingRegistration{
				ClusterName: name,
				PRURL:       prs[i].URL,
				Branch:      prs[i].SourceBranch,
				OpenedAt:    prs[i].CreatedAt,
				Operation:   h.operation,
				PRNumber:    pendingPRNumber(prs[i]),
			})
			break
		}
	}
	return out
}

// pendingPRNumber is the pull request's number. A provider that left the
// number out gets it from the end of the pull request's address
// (".../pull/12"). When neither has it, it is 0 and pr_number is left out
// of the answer, so no screen ever shows "PR #0" (EP3 dry run round R, R7).
func pendingPRNumber(pr gitprovider.PullRequest) int {
	if pr.ID > 0 {
		return pr.ID
	}
	tail := strings.TrimRight(pr.URL, "/")
	if i := strings.LastIndex(tail, "/"); i >= 0 {
		tail = tail[i+1:]
	}
	if n, err := strconv.Atoi(tail); err == nil && n > 0 {
		return n
	}
	return 0
}

// dropPendingForManagedClusters is the LW-18 (Part 2) filter: a register
// pull request for a cluster that is already in Git is an idempotent
// re-run and only noise, so it is dropped. A remove pull request is the
// opposite — its cluster is always still in Git until it merges — so it is
// kept (EP3-DRYRUN-FIXES finding 19: open remove PRs show as pending).
func dropPendingForManagedClusters(pending []models.PendingRegistration, managedNames map[string]struct{}) []models.PendingRegistration {
	out := make([]models.PendingRegistration, 0, len(pending))
	for _, p := range pending {
		if p.Operation == models.PendingOperationRegister {
			if _, alreadyManaged := managedNames[p.ClusterName]; alreadyManaged {
				continue
			}
		}
		out = append(out, p)
	}
	return out
}
