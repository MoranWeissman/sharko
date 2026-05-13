package api

import (
	"context"
	"log/slog"
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
// V125-1.5 (BUG-050..055): the V125-1.1 manual-mode register flow opens a
// PR but does NOT mutate managed-clusters.yaml until merge. Without this
// surface, multiple UI panels (Dashboard "Clusters needing attention",
// ClustersOverview "Discovered clusters", ClusterDetail) treated the
// pending cluster as if it half-existed, producing six related bugs in
// the same UX hole. Surfacing the PR explicitly closes the loop.
//
// Defensive degrade (V124-22 pattern): a provider error (rate limit,
// transient 5xx, auth blip) returns an EMPTY slice + a log warning rather
// than failing the entire /clusters endpoint. The cost of "we couldn't
// list PRs right now" is a missing pending-registrations section on the
// next refresh — not a 500 that takes down the whole clusters page.
//
// The return type is always a non-nil slice. Callers do NOT need to
// nil-check. V125-1.4 lesson: never let a nil array reach the FE.
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
			"err", err.Error())
		return out
	}

	// Title pattern emitted by the orchestrator. Mirrors the existing
	// matcher in internal/orchestrator/git_helpers.go.
	//
	// "<commit-prefix> register cluster <name>"
	//   |-------- common prefix -------|
	//
	// Anything after that header is suffix decoration (e.g.,
	// " (kubeconfig provider)") — we extract the cluster name as the
	// first whitespace-bounded token after the header.
	commonHeader := strings.TrimSpace(commitPrefix) + " register cluster "

	for i := range prs {
		title := prs[i].Title
		idx := strings.Index(title, commonHeader)
		if idx == -1 {
			continue
		}
		rest := title[idx+len(commonHeader):]
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
			continue
		}

		out = append(out, models.PendingRegistration{
			ClusterName: name,
			PRURL:       prs[i].URL,
			Branch:      prs[i].SourceBranch,
			OpenedAt:    prs[i].CreatedAt,
		})
	}
	return out
}
