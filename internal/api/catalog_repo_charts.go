package api

// catalog_repo_charts.go — v1.21 QA Bundle 1: lists the chart names available
// in an arbitrary Helm repository's index.yaml. Powers the chart dropdown in
// the manual "Add Addon" form so operators can pick from real chart names
// instead of typing them blind.
//
// Distinct from /catalog/validate: that endpoint takes a (repo, chart) pair
// and confirms the specific chart exists. This one takes only a repo URL and
// returns every chart available — useful when the operator knows the repo
// but not the exact chart name.
//
// Read-only; no audit.Enrich call (only mutating handlers are tracked in the
// HandlerTier registry). The response is cached server-side for 15 minutes
// per repo URL — same TTL semantics as /catalog/addons/{name}/versions and
// /catalog/validate, and they share the underlying helm.Fetcher index cache
// so a follow-up validate call hits warm.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/MoranWeissman/sharko/internal/security"
)

// repoChartsResponse is the success envelope. On `valid: false` only the
// error code + message are populated; the same failure taxonomy as
// /catalog/validate so the UI's switch-on-error_code table is reusable.
type repoChartsResponse struct {
	Valid     bool              `json:"valid"`
	Repo      string            `json:"repo"`
	Charts    []string          `json:"charts,omitempty"`
	CachedAt  string            `json:"cached_at,omitempty"`
	ErrorCode validateErrorCode `json:"error_code,omitempty"`
	Message   string            `json:"message,omitempty"`
}

// repoChartsCacheEntry mirrors catalogVersionsCacheEntry — kept private to
// this file so the two caches can evolve independently without coupling.
type repoChartsCacheEntry struct {
	resp     repoChartsResponse
	cachedAt time.Time
}

var (
	repoChartsMu       sync.Mutex
	repoChartsCacheMap = make(map[string]*repoChartsCacheEntry)
	repoChartsTTL      = 15 * time.Minute
	repoChartsCap      = 200
)

// handleListRepoCharts godoc
//
// @Summary List chart names available in an arbitrary Helm repository
// @Description v1.21 QA Bundle 1 — given a Helm repo URL, fetches `<repo>/index.yaml` and returns the chart names listed under `entries:`. Powers the chart-name dropdown in the manual "Add Addon" form so operators can pick from real chart names instead of typing blind. On failure returns HTTP 200 with `valid: false` and a structured `error_code` (invalid_input, repo_unreachable, index_parse_error, ssrf_blocked, timeout) so the UI's existing switch table can be reused. Responses are cached server-side for 15 minutes per repo URL. Read-only; requires authentication.
// @Tags catalog
// @Produce json
// @Security BearerAuth
// @Param repo query string true "Helm repository base URL (e.g. https://charts.jetstack.io)"
// @Success 200 {object} repoChartsResponse "Chart list — `valid` distinguishes success vs structured failure"
// @Failure 400 {object} map[string]interface{} "Missing or malformed repo query parameter"
// @Router /catalog/repo-charts [get]
func (s *Server) handleListRepoCharts(w http.ResponseWriter, r *http.Request) {
	repo := strings.TrimSpace(r.URL.Query().Get("repo"))

	if repo == "" {
		writeError(w, http.StatusBadRequest, "repo query parameter is required")
		return
	}
	if err := validateRepoURL(repo); err != nil {
		writeJSON(w, http.StatusOK, repoChartsResponse{
			Valid:     false,
			Repo:      repo,
			ErrorCode: validateErrInvalidInput,
			Message:   err.Error(),
		})
		return
	}
	// SSRF guard — same policy as /catalog/validate (Story V121-8.2).
	if err := security.ValidateExternalURL(repo); err != nil {
		writeJSON(w, http.StatusOK, repoChartsResponse{
			Valid:     false,
			Repo:      repo,
			ErrorCode: validateErrSSRFBlocked,
			Message:   "this URL is not allowed: " + err.Error(),
		})
		return
	}

	normalisedRepo := strings.TrimRight(repo, "/")
	cacheKey := normalisedRepo

	if cached, ok := lookupRepoCharts(cacheKey); ok {
		writeJSON(w, http.StatusOK, cached)
		return
	}

	// 8s budget — matches /catalog/validate. Long enough for slow public
	// repos, short enough that the form doesn't feel stuck.
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	charts, err := catalogVersionsFetcher.ListCharts(ctx, normalisedRepo)
	if err != nil {
		writeJSON(w, http.StatusOK, classifyRepoChartsError(err, normalisedRepo))
		return
	}

	resp := repoChartsResponse{
		Valid:    true,
		Repo:     normalisedRepo,
		Charts:   charts,
		CachedAt: time.Now().UTC().Format(time.RFC3339),
	}
	storeRepoCharts(cacheKey, resp)
	writeJSON(w, http.StatusOK, resp)
}

// classifyRepoChartsError reuses the validate error taxonomy so the UI can
// branch on a single error_code switch.
func classifyRepoChartsError(err error, repo string) repoChartsResponse {
	out := repoChartsResponse{Valid: false, Repo: repo, Message: err.Error()}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		out.ErrorCode = validateErrTimeout
		out.Message = "timed out fetching index.yaml from " + repo
	case strings.Contains(err.Error(), "parsing index"):
		out.ErrorCode = validateErrIndexParseError
		out.Message = "index.yaml at " + repo + " is malformed"
	case strings.Contains(err.Error(), "fetching index"),
		strings.Contains(err.Error(), "reading index"):
		out.ErrorCode = validateErrRepoUnreachable
		out.Message = "could not fetch index.yaml from " + repo
	default:
		out.ErrorCode = validateErrRepoUnreachable
	}
	return out
}

func lookupRepoCharts(key string) (repoChartsResponse, bool) {
	repoChartsMu.Lock()
	defer repoChartsMu.Unlock()
	entry, ok := repoChartsCacheMap[key]
	if !ok {
		return repoChartsResponse{}, false
	}
	if time.Since(entry.cachedAt) > repoChartsTTL {
		delete(repoChartsCacheMap, key)
		return repoChartsResponse{}, false
	}
	return entry.resp, true
}

func storeRepoCharts(key string, resp repoChartsResponse) {
	repoChartsMu.Lock()
	defer repoChartsMu.Unlock()
	if len(repoChartsCacheMap) >= repoChartsCap {
		// Oldest-first eviction. Cheap O(N) scan — N capped at 200.
		var oldestKey string
		var oldest time.Time
		for k, v := range repoChartsCacheMap {
			if oldestKey == "" || v.cachedAt.Before(oldest) {
				oldestKey = k
				oldest = v.cachedAt
			}
		}
		if oldestKey != "" {
			delete(repoChartsCacheMap, oldestKey)
		}
	}
	repoChartsCacheMap[key] = &repoChartsCacheEntry{
		resp:     resp,
		cachedAt: time.Now(),
	}
}

// resetRepoChartsCacheForTest exists so tests can run independently of any
// previous TTL state. Kept package-private and only invoked from _test.go.
func resetRepoChartsCacheForTest() {
	repoChartsMu.Lock()
	defer repoChartsMu.Unlock()
	repoChartsCacheMap = make(map[string]*repoChartsCacheEntry)
}
