// Package api — v1.20.1 / v1.21 value-editor supporting endpoints.
//
// Two read endpoints live here:
//
//   GET  /api/v1/addons/{name}/values/recent-prs      — Tier 1 read: list the
//         5 most recently merged PRs that touched the addon's values file.
//
//   GET  /api/v1/clusters/{cluster}/addons/{name}/values/recent-prs
//                                                     — same, scoped to a
//         cluster overrides file.
//
// History:
//   - v1.20.1 added a `POST /api/v1/addons/{name}/values/pull-upstream`
//     endpoint that replaced the global values file with the chart's
//     upstream values.yaml.
//   - v1.21 (Story V121-6.4 + V121-6.5) deletes that endpoint. The same
//     functionality moved into a `refresh_from_upstream: true` flag on
//     the existing `PUT /api/v1/addons/{name}/values` handler — see
//     `values_editor.go::handleSetAddonValues`. Locked decision (Moran):
//     no new endpoint, no fragmented audit/tier surface for "edit values".
//
// Implementation notes:
//   - Recent-PRs goes to the GitProvider's ListPullRequests (state=closed)
//     and filters by Status=="merged" plus a title/branch heuristic, with a
//     5-minute memory cache keyed by the repo-file path. We stop short of
//     calling GitHub's per-PR file API: that requires N+1 round-trips and
//     the heuristic (branch/title) catches Sharko-authored PRs which is the
//     99% case. Note: GitHub's list-PRs API doesn't accept state="merged" —
//     merged PRs appear under state="closed" with merged=true; the provider
//     surfaces that as Status="merged".

package api

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/models"
)

// ─── recent-PRs panel ────────────────────────────────────────────────────

// recentPRsEntry is the wire shape of one row in the recent-changes panel.
type recentPRsEntry struct {
	PRID     int    `json:"pr_id"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Author   string `json:"author"`
	MergedAt string `json:"merged_at"`
}

// recentPRsResponse is the response body for both the global and per-cluster
// variants. `view_all_url` is the repo's PRs search filtered by the values
// file, so the UI can render a "View all on GitHub →" link.
type recentPRsResponse struct {
	Entries    []recentPRsEntry `json:"entries"`
	ViewAllURL string           `json:"view_all_url,omitempty"`
	ValuesFile string           `json:"values_file"`
}

// recentPRsCache is a tiny 5-minute TTL cache keyed by the values file path.
// GitHub's list-merged-PRs endpoint is rate-limited (~60 req/hr for anonymous
// and 5000/hr for a PAT). The cache keeps the UI responsive while allowing the
// audit log to surface fresh data via the standard PR refresh path.
type recentPRsCache struct {
	mu      sync.RWMutex
	entries map[string]recentPRsCacheEntry
}

type recentPRsCacheEntry struct {
	fetchedAt time.Time
	data      recentPRsResponse
}

var recentPRsStore = &recentPRsCache{entries: map[string]recentPRsCacheEntry{}}

const recentPRsTTL = 5 * time.Minute

func (c *recentPRsCache) get(key string) (recentPRsResponse, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[key]
	if !ok || time.Since(e.fetchedAt) > recentPRsTTL {
		return recentPRsResponse{}, false
	}
	return e.data, true
}

func (c *recentPRsCache) put(key string, data recentPRsResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = recentPRsCacheEntry{fetchedAt: time.Now(), data: data}
}

// reset clears the cache. Used by tests to avoid cross-test bleed.
func (c *recentPRsCache) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = map[string]recentPRsCacheEntry{}
}

// handleGetAddonValuesRecentPRs godoc
//
// @Summary Recent PRs that touched an addon's global values
// @Description Returns up to `limit` (default 5, max 20) recently-merged PRs that modified the addon's global values file. Backed by a 5-minute in-memory cache.
// @Tags addons
// @Produce json
// @Security BearerAuth
// @Param name path string true "Addon name"
// @Param limit query int false "Maximum entries (default 5, max 20)"
// @Success 200 {object} api.recentPRsResponse
// @Router /addons/{name}/values/recent-prs [get]
func (s *Server) handleGetAddonValuesRecentPRs(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "addon.list") {
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "addon name is required")
		return
	}

	dir := strings.TrimSuffix(s.repoPaths.GlobalValues, "/")
	if dir == "" {
		dir = "configuration/addons-global-values"
	}
	valuesFile := dir + "/" + name + ".yaml"

	limit := parseLimit(r, 5, 20)
	resp := s.fetchRecentPRs(r, valuesFile, limit, map[string]string{"addon": name})
	writeJSON(w, http.StatusOK, resp)
}

// handleGetClusterAddonValuesRecentPRs godoc
//
// @Summary Recent PRs that touched per-cluster addon overrides
// @Description Returns up to `limit` (default 5, max 20) recently-merged PRs that modified the cluster overrides file, filtered to the addon name.
// @Tags clusters
// @Produce json
// @Security BearerAuth
// @Param cluster path string true "Cluster name"
// @Param name path string true "Addon name"
// @Param limit query int false "Maximum entries (default 5, max 20)"
// @Success 200 {object} api.recentPRsResponse
// @Router /clusters/{cluster}/addons/{name}/values/recent-prs [get]
func (s *Server) handleGetClusterAddonValuesRecentPRs(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "cluster.detail") {
		return
	}
	cluster := r.PathValue("cluster")
	name := r.PathValue("name")
	if cluster == "" || name == "" {
		writeError(w, http.StatusBadRequest, "cluster and addon name are required")
		return
	}

	dir := strings.TrimSuffix(s.repoPaths.ClusterValues, "/")
	if dir == "" {
		dir = "configuration/addons-clusters-values"
	}
	valuesFile := dir + "/" + cluster + ".yaml"

	limit := parseLimit(r, 5, 20)
	resp := s.fetchRecentPRs(r, valuesFile, limit, map[string]string{"addon": name, "cluster": cluster})
	writeJSON(w, http.StatusOK, resp)
}

// fetchRecentPRs is the shared body for the two recent-PRs endpoints. It
// serves from cache when possible, else falls back to the Git provider's
// closed-PR list (filtered to Status=="merged") and applies a title/branch
// heuristic scoped to Sharko's deterministic naming.
//
// Root-cause fix (v1.20): previously we called ListPullRequests(ctx, "merged").
// GitHub's list-PRs API does not accept "merged" as a state — only open/closed/
// all. The go-github library forwards the bogus value and the response was
// empty (or erroring silently), which is why the maintainer's Recent changes
// panel stayed empty even though pr:25 had been merged. We now ask for
// "closed" and keep entries where pr.Status == "merged".
//
// Matching heuristic: Sharko's orchestrator generates PRs with deterministic
// titles and branches (see internal/orchestrator/git_helpers.go). The PR that
// edits `<addon>.yaml` under global-values has operation
//   "update global values for <addon>"
// and pull-upstream has
//   "pull upstream defaults for <addon>"
// The per-cluster overrides file uses
//   "update <addon> overrides on cluster <cluster>"
// Branches are the sanitized/hex variant of the same. So presence of the
// addon name plus a "values"/"overrides"/"upstream" keyword catches Sharko-
// authored PRs while rejecting unrelated repo activity.
func (s *Server) fetchRecentPRs(r *http.Request, valuesFile string, limit int, filter map[string]string) recentPRsResponse {
	cacheKey := valuesFile + "|" + filter["addon"] + "|" + filter["cluster"]
	if cached, ok := recentPRsStore.get(cacheKey); ok {
		cached.Entries = trimEntries(cached.Entries, limit)
		return cached
	}

	resp := recentPRsResponse{ValuesFile: valuesFile, Entries: []recentPRsEntry{}}

	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		return resp
	}

	// "closed" includes merged PRs on GitHub (state=closed + merged=true).
	// The provider fills pr.Status = "merged" for those.
	prs, err := gp.ListPullRequests(r.Context(), "closed")
	if err != nil {
		return resp
	}

	addon := filter["addon"]
	cluster := filter["cluster"]

	for _, pr := range prs {
		if pr.Status != "merged" {
			continue
		}
		if !matchesValuesPR(pr.Title, pr.SourceBranch, addon, cluster) {
			continue
		}
		resp.Entries = append(resp.Entries, recentPRsEntry{
			PRID:     pr.ID,
			Title:    pr.Title,
			URL:      pr.URL,
			Author:   pr.Author,
			MergedAt: pr.ClosedAt,
		})
		if len(resp.Entries) >= limit*4 {
			// Over-fetch a bit so trimEntries gets the N most recent, but
			// don't scan the whole PR list.
			break
		}
	}

	// View-all URL — built from the connection's repo info when available.
	if conn, cerr := s.connSvc.GetActiveConnectionInfo(); cerr == nil && conn != nil {
		if u := buildGitHubViewAllURL(conn, valuesFile); u != "" {
			resp.ViewAllURL = u
		}
	}

	recentPRsStore.put(cacheKey, resp)
	resp.Entries = trimEntries(resp.Entries, limit)
	return resp
}

// matchesValuesPR returns true when the PR title or source branch looks like
// a Sharko values/overrides PR for the given addon (and optional cluster).
//
// The check is forgiving: we accept any PR where the combined title+branch
// text (lowercased) contains the addon name AND at least one of the Sharko
// values-editor keywords ("values", "overrides", "upstream"). When a cluster
// is supplied we additionally require the cluster name in the same text,
// which is how we keep per-cluster endpoints from echoing global PRs.
func matchesValuesPR(title, branch, addon, cluster string) bool {
	hay := strings.ToLower(title + " " + branch)
	if addon != "" {
		if !strings.Contains(hay, strings.ToLower(addon)) {
			return false
		}
	}
	if cluster != "" {
		if !strings.Contains(hay, strings.ToLower(cluster)) {
			return false
		}
	}
	keywords := []string{"values", "overrides", "upstream"}
	for _, k := range keywords {
		if strings.Contains(hay, k) {
			return true
		}
	}
	return false
}

func trimEntries(entries []recentPRsEntry, limit int) []recentPRsEntry {
	if limit <= 0 || len(entries) <= limit {
		return entries
	}
	return entries[:limit]
}

func parseLimit(r *http.Request, def, max int) int {
	if q := r.URL.Query().Get("limit"); q != "" {
		var n int
		if _, err := fmt.Sscanf(q, "%d", &n); err == nil && n > 0 {
			if n > max {
				return max
			}
			return n
		}
	}
	return def
}

// buildGitHubViewAllURL builds a GitHub PRs search URL filtered by the values
// file path. Returns "" for non-GitHub providers — they get a blank link.
func buildGitHubViewAllURL(conn *models.Connection, valuesFile string) string {
	if conn == nil || conn.Git.Provider != models.GitProviderGitHub ||
		conn.Git.Owner == "" || conn.Git.Repo == "" {
		return ""
	}
	// GitHub's PR search syntax: is:pr is:merged <path>. The file path needs
	// to be URL-encoded enough to survive a query string.
	q := "is:pr is:merged " + valuesFile
	return fmt.Sprintf("https://github.com/%s/%s/pulls?q=%s", conn.Git.Owner, conn.Git.Repo, urlQueryEscape(q))
}

// urlQueryEscape is a tiny URL escaper that mimics url.QueryEscape without
// pulling net/url just for this one place. Space → '+', and a few common
// reserved chars are %-encoded.
func urlQueryEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == ' ':
			b.WriteByte('+')
		case r == '/' || r == '-' || r == '.' || r == '_' || r == '~' ||
			(r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		case r == ':':
			b.WriteString("%3A")
		default:
			b.WriteString(fmt.Sprintf("%%%02X", r))
		}
	}
	return b.String()
}

