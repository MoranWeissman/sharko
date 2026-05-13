package api

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
)

// PRListResponse is the response for GET /api/v1/prs.
//
// V125-1-6: gains a Limit field so the FE can render a "View all on
// GitHub →" escape hatch when the server response is at the limit cap.
// Limit reflects the effective limit (clamped to prsMaxLimit).
type PRListResponse struct {
	PRs   []PRItem `json:"prs"`
	Limit int      `json:"limit,omitempty"`
}

// PRItem is a single PR in list/detail responses.
type PRItem struct {
	PRID       int    `json:"pr_id"`
	PRUrl      string `json:"pr_url"`
	PRBranch   string `json:"pr_branch"`
	PRTitle    string `json:"pr_title"`
	PRBase     string `json:"pr_base"`
	Cluster    string `json:"cluster,omitempty"`
	Addon      string `json:"addon,omitempty"`
	Operation  string `json:"operation"`
	User       string `json:"user"`
	Source     string `json:"source"`
	CreatedAt  string `json:"created_at"`
	LastStatus string `json:"last_status"`
	LastPolled string `json:"last_polled_at"`
}

// Default and hard cap for the ?limit= query parameter on /api/v1/prs.
// Defaults align with the FE PullRequestsPanel: small enough that an org
// with many PRs doesn't render hundreds of rows by default, large enough
// that the typical case never hits the cap. Hard cap protects the
// response from runaway clients.
const (
	prsDefaultLimit = 100
	prsMaxLimit     = 500
)

// handleListPRs handles GET /api/v1/prs
//
// @Summary List tracked pull requests
// @Description Returns all tracked pull requests with optional filters. Sorted by created_at descending.
// @Tags prs
// @Produce json
// @Security BearerAuth
// @Param status query string false "Filter by status (open, merged, closed)"
// @Param cluster query string false "Filter by cluster name"
// @Param addon query string false "Filter by addon name"
// @Param user query string false "Filter by user"
// @Param operation query string false "Comma-separated list of operation codes to include (e.g. addon-add,values-edit). Empty = all."
// @Param limit query int false "Maximum entries to return (default 100, hard cap 500)"
// @Success 200 {object} PRListResponse "List of tracked PRs (sorted newest-first)"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Router /prs [get]
func (s *Server) handleListPRs(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "pr.list") {
		return
	}

	if s.prTracker == nil {
		writeJSON(w, http.StatusOK, PRListResponse{PRs: []PRItem{}})
		return
	}

	status := r.URL.Query().Get("status")
	cluster := r.URL.Query().Get("cluster")
	addon := r.URL.Query().Get("addon")
	user := r.URL.Query().Get("user")

	// V125-1-6: ?operation=<csv> filter. Empty = all. Whitespace
	// segments are dropped silently.
	var operations []string
	if raw := r.URL.Query().Get("operation"); raw != "" {
		for _, op := range strings.Split(raw, ",") {
			op = strings.TrimSpace(op)
			if op != "" {
				operations = append(operations, op)
			}
		}
	}

	limit := parseLimit(r, prsDefaultLimit, prsMaxLimit)

	prs, err := s.prTracker.ListPRsFiltered(r.Context(), status, cluster, addon, user, operations)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Sort newest-first by CreatedAt — the dashboard renders top-down,
	// so the freshest PRs always lead. Stable order matters for the
	// FE's escape-hatch "view all on GitHub" link (it shows when the
	// server response equals the limit cap).
	sort.SliceStable(prs, func(i, j int) bool {
		return prs[i].CreatedAt.After(prs[j].CreatedAt)
	})

	if len(prs) > limit {
		prs = prs[:limit]
	}

	items := make([]PRItem, 0, len(prs))
	for _, pr := range prs {
		items = append(items, PRItem{
			PRID:       pr.PRID,
			PRUrl:      pr.PRUrl,
			PRBranch:   pr.PRBranch,
			PRTitle:    pr.PRTitle,
			PRBase:     pr.PRBase,
			Cluster:    pr.Cluster,
			Addon:      pr.Addon,
			Operation:  pr.Operation,
			User:       pr.User,
			Source:     pr.Source,
			CreatedAt:  pr.CreatedAt.Format("2006-01-02T15:04:05Z"),
			LastStatus: pr.LastStatus,
			LastPolled: pr.LastPolled.Format("2006-01-02T15:04:05Z"),
		})
	}

	writeJSON(w, http.StatusOK, PRListResponse{PRs: items, Limit: limit})
}

// handleGetPR handles GET /api/v1/prs/{id}
//
// @Summary Get a tracked pull request
// @Description Returns details for a single tracked PR
// @Tags prs
// @Produce json
// @Security BearerAuth
// @Param id path int true "PR ID"
// @Success 200 {object} PRItem "PR details"
// @Failure 400 {object} map[string]interface{} "Invalid PR ID"
// @Failure 404 {object} map[string]interface{} "PR not found"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Router /prs/{id} [get]
func (s *Server) handleGetPR(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "pr.detail") {
		return
	}

	if s.prTracker == nil {
		writeError(w, http.StatusNotFound, "PR tracking not enabled")
		return
	}

	idStr := r.PathValue("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid PR ID")
		return
	}

	pr, err := s.prTracker.GetPR(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if pr == nil {
		writeError(w, http.StatusNotFound, "PR not tracked")
		return
	}

	writeJSON(w, http.StatusOK, PRItem{
		PRID:       pr.PRID,
		PRUrl:      pr.PRUrl,
		PRBranch:   pr.PRBranch,
		PRTitle:    pr.PRTitle,
		PRBase:     pr.PRBase,
		Cluster:    pr.Cluster,
		Operation:  pr.Operation,
		User:       pr.User,
		Source:     pr.Source,
		CreatedAt:  pr.CreatedAt.Format("2006-01-02T15:04:05Z"),
		LastStatus: pr.LastStatus,
		LastPolled: pr.LastPolled.Format("2006-01-02T15:04:05Z"),
	})
}

// handleRefreshPR handles POST /api/v1/prs/{id}/refresh
//
// @Summary Force refresh a tracked PR
// @Description Immediately polls the Git provider for this PR's current status
// @Tags prs
// @Produce json
// @Security BearerAuth
// @Param id path int true "PR ID"
// @Success 200 {object} PRItem "Updated PR details"
// @Failure 400 {object} map[string]interface{} "Invalid PR ID"
// @Failure 404 {object} map[string]interface{} "PR not found"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Router /prs/{id}/refresh [post]
func (s *Server) handleRefreshPR(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "pr.refresh") {
		return
	}

	if s.prTracker == nil {
		writeError(w, http.StatusNotFound, "PR tracking not enabled")
		return
	}

	idStr := r.PathValue("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid PR ID")
		return
	}

	pr, err := s.prTracker.PollSinglePR(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "pr_refreshed",
		Resource: fmt.Sprintf("pr:%d", id),
	})
	writeJSON(w, http.StatusOK, PRItem{
		PRID:       pr.PRID,
		PRUrl:      pr.PRUrl,
		PRBranch:   pr.PRBranch,
		PRTitle:    pr.PRTitle,
		PRBase:     pr.PRBase,
		Cluster:    pr.Cluster,
		Operation:  pr.Operation,
		User:       pr.User,
		Source:     pr.Source,
		CreatedAt:  pr.CreatedAt.Format("2006-01-02T15:04:05Z"),
		LastStatus: pr.LastStatus,
		LastPolled: pr.LastPolled.Format("2006-01-02T15:04:05Z"),
	})
}

// handleDeletePR handles DELETE /api/v1/prs/{id}
//
// @Summary Stop tracking a pull request
// @Description Removes a PR from tracking (admin only)
// @Tags prs
// @Produce json
// @Security BearerAuth
// @Param id path int true "PR ID"
// @Success 200 {object} map[string]string "PR removed from tracking"
// @Failure 400 {object} map[string]interface{} "Invalid PR ID"
// @Failure 403 {object} map[string]interface{} "Forbidden"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Router /prs/{id} [delete]
func (s *Server) handleDeletePR(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "pr.delete") {
		return
	}

	if s.prTracker == nil {
		writeError(w, http.StatusNotFound, "PR tracking not enabled")
		return
	}

	idStr := r.PathValue("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid PR ID")
		return
	}

	if err := s.prTracker.StopTracking(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "pr_deleted",
		Resource: fmt.Sprintf("pr:%d", id),
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

// ─── Merged PRs (v1.21 QA Bundle 3) ──────────────────────────────────────────
//
// Maintainer feedback: "in dashboard we have 'Pending PRs' but i want also to
// be able to switch from there to see merged PRs with the PR description/user
// and etc and a link to github."
//
// The prtracker DELETES merged PRs from its store as soon as it sees them
// (see tracker.go::PollOnce), so /api/v1/prs returns only OPEN PRs. This
// endpoint goes back to the Git provider and lists CLOSED PRs (GitHub uses
// state=closed for both merged and closed-without-merge), filters to those
// with Status=="merged", and returns the recent N. Tier 1 read; results are
// cached for 60s to keep ListPullRequests calls bounded under the 5000/hr
// GitHub PAT rate limit.

// MergedPRItem is one row in the merged-PRs response. Mirrors PRItem but uses
// the gitprovider PR shape — the Sharko prtracker doesn't retain merged PRs
// once observed, so cluster/addon/operation are best-effort: we infer them
// from the title or the source branch when the PR follows Sharko's deterministic
// naming scheme. Unknown fields are returned as empty strings so the UI can
// fall back to "—" without special-casing.
type MergedPRItem struct {
	PRID        int    `json:"pr_id"`
	PRURL       string `json:"pr_url"`
	PRTitle     string `json:"pr_title"`
	PRBranch    string `json:"pr_branch"`
	Description string `json:"description,omitempty"`
	Author      string `json:"author,omitempty"`
	Cluster     string `json:"cluster,omitempty"`
	Addon       string `json:"addon,omitempty"`
	Operation   string `json:"operation,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
	MergedAt    string `json:"merged_at,omitempty"`
}

// MergedPRsResponse wraps the list with a cap-aware count so the UI can show
// "showing the last N" without needing a separate metadata call.
type MergedPRsResponse struct {
	PRs   []MergedPRItem `json:"prs"`
	Limit int            `json:"limit"`
}

// mergedPRsCacheTTL is the in-memory cache TTL for the merged-PRs list. GitHub's
// list-PRs endpoint is rate-limited (5000/hr per PAT) and the Dashboard polls
// frequently; 60s keeps the call cost bounded while still feeling fresh.
const mergedPRsCacheTTL = 60 * time.Second

type mergedPRsCacheEntry struct {
	fetchedAt time.Time
	items     []MergedPRItem
}

var (
	mergedPRsCacheMu  sync.RWMutex
	mergedPRsCacheVal = map[string]mergedPRsCacheEntry{}
)

// handleListMergedPRs handles GET /api/v1/prs/merged
//
// @Summary List recently-merged pull requests
// @Description Returns merged Sharko-authored PRs from the Git provider. The prtracker drops PRs once merged, so this endpoint queries the provider directly. Cached for 60s. Optional filters narrow by cluster or addon (best-effort: inferred from PR title/branch).
// @Tags prs
// @Produce json
// @Security BearerAuth
// @Param cluster query string false "Filter by cluster name (best-effort title/branch match)"
// @Param addon query string false "Filter by addon name (best-effort title/branch match)"
// @Param limit query int false "Maximum entries (default 20, max 100)"
// @Success 200 {object} MergedPRsResponse "List of merged PRs"
// @Failure 503 {object} map[string]interface{} "No active Git connection"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Router /prs/merged [get]
func (s *Server) handleListMergedPRs(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "pr.list") {
		return
	}

	cluster := strings.ToLower(r.URL.Query().Get("cluster"))
	addon := strings.ToLower(r.URL.Query().Get("addon"))
	limit := parseLimit(r, 20, 100)

	// Cache key independent of filters/limit — we always fetch the full set
	// and filter client-side. This keeps the upstream call shared across
	// callers (e.g. Dashboard + per-cluster page).
	const cacheKey = "all"
	mergedPRsCacheMu.RLock()
	entry, ok := mergedPRsCacheVal[cacheKey]
	mergedPRsCacheMu.RUnlock()

	var items []MergedPRItem
	if ok && time.Since(entry.fetchedAt) < mergedPRsCacheTTL {
		items = entry.items
	} else {
		gp, err := s.connSvc.GetActiveGitProvider()
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, err.Error())
			return
		}

		// "closed" includes merged PRs on GitHub (state=closed + merged=true).
		// The provider implementations set pr.Status = "merged" for those.
		prs, err := gp.ListPullRequests(r.Context(), "closed")
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		items = make([]MergedPRItem, 0, len(prs))
		for _, pr := range prs {
			if pr.Status != "merged" {
				continue
			}
			items = append(items, MergedPRItem{
				PRID:        pr.ID,
				PRURL:       pr.URL,
				PRTitle:     pr.Title,
				PRBranch:    pr.SourceBranch,
				Description: pr.Description,
				Author:      pr.Author,
				Cluster:     inferCluster(pr.Title, pr.SourceBranch),
				Addon:       inferAddon(pr.Title, pr.SourceBranch),
				Operation:   inferOperation(pr.Title, pr.SourceBranch),
				CreatedAt:   pr.CreatedAt,
				MergedAt:    pr.ClosedAt,
			})
		}

		mergedPRsCacheMu.Lock()
		mergedPRsCacheVal[cacheKey] = mergedPRsCacheEntry{fetchedAt: time.Now(), items: items}
		mergedPRsCacheMu.Unlock()
	}

	// Apply optional filters (best-effort substring match on title+branch).
	filtered := make([]MergedPRItem, 0, len(items))
	for _, it := range items {
		if cluster != "" {
			hay := strings.ToLower(it.PRTitle + " " + it.PRBranch + " " + it.Cluster)
			if !strings.Contains(hay, cluster) {
				continue
			}
		}
		if addon != "" {
			hay := strings.ToLower(it.PRTitle + " " + it.PRBranch + " " + it.Addon)
			if !strings.Contains(hay, addon) {
				continue
			}
		}
		filtered = append(filtered, it)
		if len(filtered) >= limit {
			break
		}
	}

	writeJSON(w, http.StatusOK, MergedPRsResponse{PRs: filtered, Limit: limit})
}

// inferCluster pulls the cluster name out of a Sharko-generated PR title or
// branch. Sharko branch convention: "sharko/<op>-<cluster>[-...]" and titles
// like "Register cluster <name>" or "Update <addon> overrides on cluster <name>".
// Returns "" when nothing matches — the UI shows "—" then.
func inferCluster(title, branch string) string {
	// Try title patterns first ("on cluster <name>").
	low := strings.ToLower(title)
	if i := strings.Index(low, "cluster "); i >= 0 {
		rest := title[i+len("cluster "):]
		// Take the first whitespace-delimited token.
		if j := strings.IndexAny(rest, " \t"); j > 0 {
			return strings.Trim(rest[:j], " ,.;:\"'")
		}
		return strings.Trim(rest, " ,.;:\"'")
	}
	// Fallback: branch like "sharko/register-prod" — last segment.
	if strings.HasPrefix(branch, "sharko/") {
		parts := strings.Split(strings.TrimPrefix(branch, "sharko/"), "-")
		if len(parts) >= 2 {
			return parts[len(parts)-1]
		}
	}
	return ""
}

// inferAddon pulls an addon name out of a Sharko PR title. Sharko op titles
// include "<op> <addon>" or "Update <addon> overrides", so the second word of
// the title is a reasonable best guess. Falls back to "" when ambiguous.
func inferAddon(title, _ string) string {
	words := strings.Fields(title)
	if len(words) < 2 {
		return ""
	}
	// Skip leading verbs that don't carry an addon name.
	skip := map[string]bool{"register": true, "deregister": true, "adopt": true, "unadopt": true, "init": true}
	if skip[strings.ToLower(words[0])] {
		return ""
	}
	return strings.Trim(words[1], " ,.;:\"'")
}

// inferOperation returns a coarse op label ("upgrade", "values", "register", …)
// derived from the title's first word. Used purely for UI display.
func inferOperation(title, _ string) string {
	words := strings.Fields(title)
	if len(words) == 0 {
		return ""
	}
	return strings.ToLower(words[0])
}
