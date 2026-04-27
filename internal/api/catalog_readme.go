package api

// catalog_readme.go — v1.21 QA Bundle 2: README proxy for curated catalog
// entries.
//
//   GET /api/v1/catalog/addons/{name}/readme
//
// Why this exists: the in-page Marketplace detail view (Bundle 2) renders
// the upstream README so operators can read the install/usage notes without
// leaving Sharko. For ArtifactHub-search entries the browser already has
// /catalog/remote/{repo}/{name} (returns the package detail incl. README).
// Curated entries don't carry the ArtifactHub repo name in the catalog YAML
// (only the Helm repo URL — which is a different identifier), so the
// frontend can't construct that URL. This endpoint resolves the curated
// chart → ArtifactHub package via SearchHelm + a best-match heuristic, then
// reuses the existing GetPackage path.
//
// Match heuristic for SearchHelm hits:
//   1. Hit's chart name == curated chart name (exact, case-insensitive)
//   2. Prefer verified_publisher
//   3. Tie-break by GitHub stars (highest first)
// Falls back to the first hit when nothing matches the chart name.
//
// Cache: piggy-backs on packageCache (1 h fresh / 24 h stale, 500 entries)
// keyed by "readme:" + curated addon name so a repeat fetch is a single
// memory lookup. The cached value is the trimmed shape we return to the
// browser, not the full AHPackage — README markdown is verbose and we don't
// want to keep the rest of the package metadata twice in the LRU.

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/MoranWeissman/sharko/internal/catalog"
)

// catalogReadmeResponse is the trimmed payload returned to the browser.
//
//   readme: markdown content from ArtifactHub (may be empty when the upstream
//           chart didn't ship a README — surfaced by the UI as an empty state)
//   source: "artifacthub" today; reserved for "fallback" when we add a
//           direct chart-tarball README extractor in v1.22
//   ah_repo + ah_chart: the ArtifactHub coordinates we resolved to (handy
//                       for the UI to deep-link "View on ArtifactHub")
//   stale + cached_at: same semantics as the package-detail proxy
type catalogReadmeResponse struct {
	Readme   string `json:"readme"`
	Source   string `json:"source"`
	AHRepo   string `json:"ah_repo,omitempty"`
	AHChart  string `json:"ah_chart,omitempty"`
	Stale    bool   `json:"stale,omitempty"`
	CachedAt string `json:"cached_at,omitempty"`
}

// handleGetCatalogReadme godoc
//
// @Summary Get the README markdown for a curated catalog addon
// @Description Resolves a curated catalog entry to its ArtifactHub package and returns the README markdown for the in-page Marketplace detail view (v1.21 QA Bundle 2). The lookup uses the chart name (case-insensitive) against ArtifactHub's `/packages/search`, prefers verified-publisher hits, and tie-breaks by stars. Returns 200 with `readme: ""` when the chart was found but didn't ship a README. Returns 404 only when no ArtifactHub package matches the curated chart name. Read-only; requires authentication.
// @Tags catalog
// @Produce json
// @Security BearerAuth
// @Param name path string true "Curated addon name (case-sensitive, matches catalog.name)"
// @Success 200 {object} catalogReadmeResponse "README content + ArtifactHub coordinates"
// @Failure 400 {object} map[string]interface{} "Missing addon name"
// @Failure 404 {object} map[string]interface{} "Addon not in curated catalog or no ArtifactHub match"
// @Failure 502 {object} map[string]interface{} "ArtifactHub unreachable and no cached value available"
// @Failure 503 {object} map[string]interface{} "Catalog not loaded"
// @Router /catalog/addons/{name}/readme [get]
func (s *Server) handleGetCatalogReadme(w http.ResponseWriter, r *http.Request) {
	if s.catalog == nil {
		writeError(w, http.StatusServiceUnavailable, "catalog not loaded")
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "addon name is required")
		return
	}
	// V123-PR-B (H1): use the merged-catalog lookup so third-party snapshot
	// entries resolve here too — pre-fix the readme/versions/project-readme
	// handlers used s.catalog.Get(name) which only sees the embedded slice
	// and 404'd on every third-party-only entry.
	entry, ok := s.mergedCatalogGet(name)
	if !ok {
		writeError(w, http.StatusNotFound, "not found in curated catalog")
		return
	}

	cacheKey := "readme:" + entry.Name

	// Fast path: fresh cache hit. Cached value is the trimmed response so
	// we can return it directly (after stamping CachedAt).
	if v, fresh, _, ok := packageCache.Get(cacheKey); ok && fresh {
		cached := v.(catalogReadmeResponse)
		cached.CachedAt = time.Now().UTC().Format(time.RFC3339)
		writeJSON(w, http.StatusOK, cached)
		return
	}

	// Backoff short-circuit: serve stale if any.
	if !ahBackoff.Allow() {
		if v, _, _, ok := packageCache.Get(cacheKey); ok {
			cached := v.(catalogReadmeResponse)
			cached.Stale = true
			w.Header().Set("X-Cache-Stale", "true")
			writeJSON(w, http.StatusOK, cached)
			return
		}
		writeError(w, http.StatusBadGateway, "artifacthub temporarily unavailable")
		return
	}

	// Resolve via SearchHelm (chart name → AH (repo, chart)).
	// We bound the search to 20 hits — a typical chart name (e.g.
	// "cert-manager") returns 1-3 packages so 20 is plenty of headroom.
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	hits, err := ahClient.SearchHelm(ctx, entry.Chart, 20)
	if err != nil {
		ahBackoff.Failure()
		if v, _, _, ok := packageCache.Get(cacheKey); ok {
			cached := v.(catalogReadmeResponse)
			cached.Stale = true
			w.Header().Set("X-Cache-Stale", "true")
			writeJSON(w, http.StatusOK, cached)
			return
		}
		writeError(w, http.StatusBadGateway, "artifacthub unreachable: "+classifyAHError(err))
		return
	}
	ahBackoff.Success()

	best := pickBestAHMatch(hits, entry.Chart)
	if best == nil {
		// No match — cache an empty README so we don't hammer ArtifactHub
		// every time the user re-opens the same detail page.
		empty := catalogReadmeResponse{
			Readme:   "",
			Source:   "artifacthub",
			CachedAt: time.Now().UTC().Format(time.RFC3339),
		}
		packageCache.Put(cacheKey, empty)
		writeJSON(w, http.StatusOK, empty)
		return
	}

	// Fetch the package detail to get the README.
	pkg, err := ahClient.GetPackage(ctx, best.Repository.Name, best.Name)
	if err != nil {
		ahBackoff.Failure()
		if v, _, _, ok := packageCache.Get(cacheKey); ok {
			cached := v.(catalogReadmeResponse)
			cached.Stale = true
			w.Header().Set("X-Cache-Stale", "true")
			writeJSON(w, http.StatusOK, cached)
			return
		}
		writeError(w, http.StatusBadGateway, "artifacthub unreachable: "+classifyAHError(err))
		return
	}
	ahBackoff.Success()

	out := catalogReadmeResponse{
		Readme:   pkg.Readme,
		Source:   "artifacthub",
		AHRepo:   best.Repository.Name,
		AHChart:  best.Name,
		CachedAt: time.Now().UTC().Format(time.RFC3339),
	}
	packageCache.Put(cacheKey, out)
	writeJSON(w, http.StatusOK, out)
}

// pickBestAHMatch returns the best matching ArtifactHub search hit for the
// given chart name. Preference order:
//
//   1. Exact case-insensitive name match + verified publisher
//   2. Exact case-insensitive name match
//   3. Verified publisher (any name match)
//   4. First hit in the list
//
// Within each tier, ties break by stars descending. Returns nil when the
// hit list is empty.
func pickBestAHMatch(hits []catalog.AHSearchPackage, chartName string) *catalog.AHSearchPackage {
	if len(hits) == 0 {
		return nil
	}
	target := strings.ToLower(strings.TrimSpace(chartName))

	var (
		exactVerified *catalog.AHSearchPackage
		exactAny      *catalog.AHSearchPackage
		verifiedAny   *catalog.AHSearchPackage
	)
	for i := range hits {
		h := &hits[i]
		nameMatch := strings.EqualFold(h.Name, target) ||
			strings.EqualFold(h.NormalizedName, target)
		if nameMatch && h.Repository.VerifiedPublisher {
			if exactVerified == nil || h.Stars > exactVerified.Stars {
				exactVerified = h
			}
			continue
		}
		if nameMatch {
			if exactAny == nil || h.Stars > exactAny.Stars {
				exactAny = h
			}
			continue
		}
		if h.Repository.VerifiedPublisher {
			if verifiedAny == nil || h.Stars > verifiedAny.Stars {
				verifiedAny = h
			}
		}
	}
	switch {
	case exactVerified != nil:
		return exactVerified
	case exactAny != nil:
		return exactAny
	case verifiedAny != nil:
		return verifiedAny
	default:
		return &hits[0]
	}
}
