// Package api — v1.20.1 value-editor supporting endpoints.
//
// Three endpoints added alongside the existing values editor:
//
//   POST /api/v1/addons/{name}/values/pull-upstream   — Tier 2: replace global
//         values with the chart's upstream values.yaml (comments preserved).
//
//   GET  /api/v1/addons/{name}/values/recent-prs      — Tier 1 read: list the
//         5 most recently merged PRs that touched the addon's values file.
//
//   GET  /api/v1/clusters/{cluster}/addons/{name}/values/recent-prs
//                                                     — same, scoped to a
//         cluster overrides file.
//
// Implementation notes:
//   - Upstream fetch reuses helm.Fetcher.FetchValues which already unpacks
//     the chart tarball and returns the raw YAML (comments preserved).
//   - Recent-PRs goes to the GitProvider's ListPullRequests (state=merged)
//     and filters by title/branch heuristic, with a 5-minute memory cache
//     keyed by the repo-file path. We stop short of calling GitHub's
//     per-PR file API: that requires N+1 round-trips and the heuristic
//     (branch/title) catches Sharko-authored PRs which is the 99% case.

package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/helm"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/prtracker"
)

// pullUpstreamRequest is the body of POST /api/v1/addons/{name}/values/pull-upstream.
type pullUpstreamRequest struct {
	// Version is the chart version to pull (e.g. "v1.14.4"). When empty, the
	// handler resolves the addon's currently-configured version from the
	// catalog — the expected default for the "I want to refresh to whatever
	// we're pointing at" workflow.
	Version string `json:"version"`

	// MergeStrategy controls how upstream values are merged into the current
	// values file. "replace" (the only supported strategy for v1.20.1)
	// overwrites the file entirely with upstream defaults. A future
	// "merge_keep_overrides" strategy is tracked as a TODO in the handler.
	MergeStrategy string `json:"merge_strategy,omitempty"`
}

// handlePullUpstreamValues godoc
//
// @Summary Pull upstream chart values.yaml
// @Description Replaces the addon's global Helm values file with the chart's upstream `values.yaml` (comments preserved) wrapped under the `<addonName>:` key. Opens a PR. Tier 2 (configuration) — uses the caller's personal GitHub PAT when configured, otherwise the service token with a `Co-authored-by:` trailer.
// @Tags addons
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Addon name"
// @Param body body api.pullUpstreamRequest true "Pull upstream request"
// @Success 200 {object} map[string]interface{} "PR created"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 404 {object} map[string]interface{} "Addon not found"
// @Failure 502 {object} map[string]interface{} "Helm or Git failure"
// @Router /addons/{name}/values/pull-upstream [post]
func (s *Server) handlePullUpstreamValues(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "addon.update-catalog") {
		return
	}

	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "addon name is required")
		return
	}

	var req pullUpstreamRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	strategy := req.MergeStrategy
	if strategy == "" {
		strategy = "replace"
	}
	if strategy != "replace" {
		// TODO(v1.21): implement "merge_keep_overrides" — deep-merge upstream
		// defaults over a structured parse of the current file, preserving
		// user edits and only adding NEW upstream keys.
		writeError(w, http.StatusBadRequest, "merge_strategy=\"merge_keep_overrides\" is not yet supported; use \"replace\"")
		return
	}

	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active ArgoCD connection: "+err.Error())
		return
	}

	ctx, git, tokRes, err := s.GitProviderForTier(r.Context(), r, audit.Tier2)
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active Git connection: "+err.Error())
		return
	}

	// Look up the addon entry to learn its repo + chart + configured version.
	detail, gerr := s.addonSvc.GetAddonDetail(ctx, name, git, ac)
	if gerr != nil {
		writeError(w, http.StatusBadGateway, "looking up addon: "+gerr.Error())
		return
	}
	if detail == nil {
		writeError(w, http.StatusNotFound, "addon not found in catalog: "+name)
		return
	}
	chart := detail.Addon.Chart
	repoURL := detail.Addon.RepoURL
	version := strings.TrimSpace(req.Version)
	if version == "" {
		version = detail.Addon.Version
	}
	if version == "" {
		writeError(w, http.StatusBadRequest, "addon has no configured version; specify version in the request body")
		return
	}
	if repoURL == "" || chart == "" {
		writeError(w, http.StatusBadRequest, "addon is missing chart/repo metadata required to fetch upstream values")
		return
	}

	fetcher := helm.NewFetcher()
	rawValues, err := fetcher.FetchValues(ctx, repoURL, chart, version)
	if err != nil {
		writeError(w, http.StatusBadGateway, "fetching upstream values: "+err.Error())
		return
	}

	orch := orchestrator.New(&s.gitMu, nil, ac, git, s.gitopsCfg, s.repoPaths, nil)
	result, err := orch.PullUpstreamAddonValues(ctx, name, rawValues)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	if s.prTracker != nil && result != nil && result.PRID > 0 {
		user := r.Header.Get("X-Sharko-User")
		if user == "" {
			user = "system"
		}
		_ = s.prTracker.TrackPR(ctx, prtracker.PRInfo{
			PRID:       result.PRID,
			PRUrl:      result.PRUrl,
			PRBranch:   result.Branch,
			PRTitle:    fmt.Sprintf("Pull upstream defaults for %s@%s", chart, version),
			PRBase:     "main",
			Addon:      name,
			Operation:  "values-pull-upstream",
			User:       user,
			Source:     "api",
			CreatedAt:  time.Now(),
			LastStatus: "open",
		})
	}

	audit.Enrich(ctx, audit.Fields{
		Event:    "addon_values_pulled_upstream",
		Resource: fmt.Sprintf("addon:%s", name),
		Detail:   fmt.Sprintf("chart=%s version=%s strategy=%s bytes=%d", chart, version, strategy, len(rawValues)),
	})

	writeJSON(w, http.StatusOK, withAttributionWarning(map[string]interface{}{
		"pr_url":         result.PRUrl,
		"pr_id":          result.PRID,
		"branch":         result.Branch,
		"merged":         result.Merged,
		"values_file":    result.ValuesFile,
		"chart":          chart,
		"chart_version":  version,
		"strategy":       strategy,
		"upstream_bytes": len(rawValues),
	}, tokRes))
}

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
// merged-PR list and filters by title heuristic. The filter map is used to
// narrow matches when a single file (the cluster overrides) is touched by
// many addons — we match on PR title containing "addon" or branch prefix.
func (s *Server) fetchRecentPRs(r *http.Request, valuesFile string, limit int, filter map[string]string) recentPRsResponse {
	cacheKey := valuesFile + "|" + filter["addon"]
	if cached, ok := recentPRsStore.get(cacheKey); ok {
		cached.Entries = trimEntries(cached.Entries, limit)
		return cached
	}

	resp := recentPRsResponse{ValuesFile: valuesFile, Entries: []recentPRsEntry{}}

	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		return resp
	}

	prs, err := gp.ListPullRequests(r.Context(), "merged")
	if err != nil {
		return resp
	}

	// Heuristic filter: Sharko's PR titles and branches include the addon
	// name (e.g. "Update global values for foo", branch "update-values-foo").
	// We also match on the literal phrase "values" or "overrides" to avoid
	// pulling unrelated PRs when a repo has broad activity.
	addon := filter["addon"]
	cluster := filter["cluster"]
	needles := []string{"values", "overrides", "upstream defaults"}
	if addon != "" {
		needles = append(needles, addon)
	}

	for _, pr := range prs {
		if !matchesAny(pr.Title, needles) && !matchesAny(pr.SourceBranch, needles) {
			continue
		}
		if addon != "" && !strings.Contains(strings.ToLower(pr.Title+" "+pr.SourceBranch), strings.ToLower(addon)) {
			continue
		}
		if cluster != "" && !strings.Contains(strings.ToLower(pr.Title+" "+pr.SourceBranch), strings.ToLower(cluster)) {
			// When scoped to a cluster file, require the cluster name in the
			// PR title or branch. Global-values PRs won't match and will be
			// filtered out of per-cluster endpoints.
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

func matchesAny(s string, needles []string) bool {
	l := strings.ToLower(s)
	for _, n := range needles {
		if strings.Contains(l, strings.ToLower(n)) {
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

