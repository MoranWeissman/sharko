package api

// catalog_remote.go — V121-3.3: per-package detail proxy.
//
//   GET /api/v1/catalog/remote/{repo}/{name}
//
// Fetches /packages/helm/{repo}/{name} from ArtifactHub and returns the
// metadata (description, maintainers, available_versions, links, license, …).
// The browser uses this to pre-fill the Configure modal when the user clicks
// an ArtifactHub search result.
//
// Cache: 1 h fresh / 24 h stale, 500 entries, LRU.
//
// Why both repo + name in the URL: ArtifactHub addresses Helm packages by
// (repository name, chart name) — there is no single global "package id" in
// the URL form. We mirror their shape so the route is self-documenting.

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/MoranWeissman/sharko/internal/catalog"
)

// catalogRemotePackageResponse wraps the ArtifactHub package shape with our
// stale/cache metadata so callers don't need a second RTT to know the freshness.
type catalogRemotePackageResponse struct {
	Package  *catalog.AHPackage `json:"package"`
	Stale    bool               `json:"stale,omitempty"`
	CachedAt string             `json:"cached_at,omitempty"`
}

// handleGetRemotePackage godoc
//
// @Summary ArtifactHub package detail proxy
// @Description Proxies the ArtifactHub package detail endpoint for one Helm chart so the browser does not call ArtifactHub directly (avoids CORS issues and lets Sharko apply rate-limit handling). Returns the trimmed package shape (description, maintainers, available_versions, links, license, repo metadata). Responses are cached server-side for 1 hour; on upstream failure the last cached value is served (stale up to 24 h) with `stale: true`. The `{repo}` path segment is the ArtifactHub repository name, `{name}` is the chart name.
// @Tags catalog
// @Produce json
// @Security BearerAuth
// @Param repo path string true "ArtifactHub repository name (e.g. jetstack)"
// @Param name path string true "Chart name (e.g. cert-manager)"
// @Success 200 {object} catalogRemotePackageResponse "Package detail"
// @Failure 400 {object} map[string]interface{} "Missing repo or name"
// @Failure 404 {object} map[string]interface{} "Package not found on ArtifactHub"
// @Failure 502 {object} map[string]interface{} "ArtifactHub unreachable and no cached value available"
// @Router /catalog/remote/{repo}/{name} [get]
func (s *Server) handleGetRemotePackage(w http.ResponseWriter, r *http.Request) {
	repo := strings.TrimSpace(r.PathValue("repo"))
	name := strings.TrimSpace(r.PathValue("name"))
	if repo == "" || name == "" {
		writeError(w, http.StatusBadRequest, "repo and name are required")
		return
	}

	cacheKey := "pkg:" + repo + "/" + name

	// Fast path: fresh cache hit.
	if v, fresh, _, ok := packageCache.Get(cacheKey); ok && fresh {
		writeJSON(w, http.StatusOK, catalogRemotePackageResponse{
			Package:  v.(*catalog.AHPackage),
			CachedAt: time.Now().UTC().Format(time.RFC3339),
		})
		return
	}

	// Backoff short-circuit: serve stale if any.
	if !ahBackoff.Allow() {
		if v, _, _, ok := packageCache.Get(cacheKey); ok {
			w.Header().Set("X-Cache-Stale", "true")
			writeJSON(w, http.StatusOK, catalogRemotePackageResponse{
				Package: v.(*catalog.AHPackage),
				Stale:   true,
			})
			return
		}
		writeError(w, http.StatusBadGateway, "artifacthub temporarily unavailable")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	pkg, err := ahClient.GetPackage(ctx, repo, name)
	if err != nil {
		// 404 is a real "not found" — pass through, don't serve stale (the
		// previous cached value would be misleading).
		if catalog.IsArtifactHubClass(err, catalog.AHErrNotFound) {
			writeError(w, http.StatusNotFound, "package not found on artifacthub")
			return
		}
		ahBackoff.Failure()
		if v, _, _, ok := packageCache.Get(cacheKey); ok {
			w.Header().Set("X-Cache-Stale", "true")
			writeJSON(w, http.StatusOK, catalogRemotePackageResponse{
				Package: v.(*catalog.AHPackage),
				Stale:   true,
			})
			return
		}
		writeError(w, http.StatusBadGateway, "artifacthub unreachable: "+classifyAHError(err))
		return
	}

	ahBackoff.Success()
	packageCache.Put(cacheKey, pkg)
	writeJSON(w, http.StatusOK, catalogRemotePackageResponse{
		Package:  pkg,
		CachedAt: time.Now().UTC().Format(time.RFC3339),
	})
}
