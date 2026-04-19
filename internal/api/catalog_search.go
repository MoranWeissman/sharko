package api

// catalog_search.go — V121-3.2: blended search endpoint.
//
//   GET /api/v1/catalog/search?q=<term>&limit=20
//
// Returns curated hits (from the in-memory catalog) and ArtifactHub hits
// (proxied) in one envelope so the UI doesn't have to fan-out two requests.
// Curated hits keep the full catalog shape; external hits are slimmed to the
// fields the marketplace card renders.
//
// Failure semantics:
//   - On any ArtifactHub error we still return 200 with curated hits and
//     `artifacthub_error: "<class>"` so the UI can render the unreachable
//     banner. We don't want the "Search" tab to break just because the
//     upstream is down — curated should still work.
//   - When stale-served, `stale: true` is set and the X-Cache-Stale header
//     is added so HTTP-level callers (curl, scripts) can react too.

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/MoranWeissman/sharko/internal/catalog"
)

// catalogSearchResponse is the envelope returned by the search endpoint.
type catalogSearchResponse struct {
	Query            string                    `json:"query"`
	Curated          []catalog.CatalogEntry    `json:"curated"`
	ArtifactHub      []catalog.AHSearchPackage `json:"artifacthub"`
	ArtifactHubError string                    `json:"artifacthub_error,omitempty"`
	Stale            bool                      `json:"stale,omitempty"`
	CachedAt         string                    `json:"cached_at,omitempty"`
}

// handleSearchCatalog godoc
//
// @Summary Blended catalog search (curated + ArtifactHub)
// @Description Returns curated catalog matches and ArtifactHub Helm chart matches for the same query in one envelope. Curated hits are returned as full catalog entries; ArtifactHub hits are slimmed to the fields the marketplace UI renders. When the upstream ArtifactHub call fails, curated hits are still returned and `artifacthub_error` is set with a classification (rate_limited / server_error / timeout / not_found / malformed). Search results are cached server-side for 10 minutes; on upstream failure the last cached value is served (stale up to 24 h) with `stale: true`.
// @Tags catalog
// @Produce json
// @Security BearerAuth
// @Param q query string true "Search term (case-insensitive substring on curated; full-text on ArtifactHub)"
// @Param limit query integer false "Maximum ArtifactHub hits to return (default 20, max 60)"
// @Success 200 {object} catalogSearchResponse "Blended search response"
// @Failure 400 {object} map[string]interface{} "Empty query"
// @Failure 503 {object} map[string]interface{} "Catalog not loaded"
// @Router /catalog/search [get]
func (s *Server) handleSearchCatalog(w http.ResponseWriter, r *http.Request) {
	if s.catalog == nil {
		writeError(w, http.StatusServiceUnavailable, "catalog not loaded")
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeError(w, http.StatusBadRequest, "q is required")
		return
	}
	limit := 20
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 && v <= 60 {
			limit = v
		}
	}

	// Curated half — substring match across name/description/maintainers.
	curated := s.catalog.List(catalog.Query{Q: q})
	if len(curated) > 20 {
		curated = curated[:20]
	}

	// ArtifactHub half — cache lookup, then upstream on miss (subject to backoff).
	cacheKey := buildSearchCacheKey(q, limit)
	resp := catalogSearchResponse{Query: q, Curated: curated}

	// Fast path: fresh cache hit.
	if v, fresh, _, ok := searchCache.Get(cacheKey); ok && fresh {
		resp.ArtifactHub = v.([]catalog.AHSearchPackage)
		resp.CachedAt = time.Now().UTC().Format(time.RFC3339)
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// Backoff short-circuit: upstream is in cool-down → serve stale or error.
	if !ahBackoff.Allow() {
		if v, _, stale, ok := searchCache.Get(cacheKey); ok && (stale || v != nil) {
			resp.ArtifactHub = v.([]catalog.AHSearchPackage)
			resp.Stale = true
			resp.ArtifactHubError = "rate_limited"
			w.Header().Set("X-Cache-Stale", "true")
			writeJSON(w, http.StatusOK, resp)
			return
		}
		resp.ArtifactHubError = "rate_limited"
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// Cache miss + upstream allowed → fetch.
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	hits, err := ahClient.SearchHelm(ctx, q, limit)
	if err != nil {
		ahBackoff.Failure()
		// Upstream failed — serve stale if we have anything for this key.
		if v, _, _, ok := searchCache.Get(cacheKey); ok {
			resp.ArtifactHub = v.([]catalog.AHSearchPackage)
			resp.Stale = true
			resp.ArtifactHubError = classifyAHError(err)
			w.Header().Set("X-Cache-Stale", "true")
			writeJSON(w, http.StatusOK, resp)
			return
		}
		// No stale — return curated-only with classification.
		resp.ArtifactHubError = classifyAHError(err)
		writeJSON(w, http.StatusOK, resp)
		return
	}

	ahBackoff.Success()
	if hits == nil {
		hits = []catalog.AHSearchPackage{}
	}
	searchCache.Put(cacheKey, hits)
	resp.ArtifactHub = hits
	resp.CachedAt = time.Now().UTC().Format(time.RFC3339)
	writeJSON(w, http.StatusOK, resp)
}

// buildSearchCacheKey normalises the query so equivalent searches share a
// cache slot ("Prometheus" and "prometheus" hit the same key).
func buildSearchCacheKey(q string, limit int) string {
	return "search:" + strings.ToLower(strings.TrimSpace(q)) + ":" + strconv.Itoa(limit)
}
