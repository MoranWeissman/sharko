package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/catalog/sources"
	"github.com/MoranWeissman/sharko/internal/config"
)

// SetCatalog wires the curated catalog into the Server. Handlers that call
// s.catalog with a nil pointer return 503 so the failure mode is explicit
// rather than panicking.
func (s *Server) SetCatalog(c *catalog.Catalog) {
	s.catalog = c
}

// Catalog returns the curated catalog (may be nil if not set).
func (s *Server) Catalog() *catalog.Catalog {
	return s.catalog
}

// SetCatalogSources stashes the parsed third-party catalog source config
// (v1.23 / Story V123-1.1). The V123-1.2 fetcher is the primary consumer;
// for now this is write-only from startup.
func (s *Server) SetCatalogSources(cfg *config.CatalogSourcesConfig) {
	s.catalogSources = cfg
}

// CatalogSources returns the parsed third-party catalog source config.
// Returns nil when SHARKO_CATALOG_URLS was unset AND the startup wiring
// never called SetCatalogSources; returns a non-nil config with an empty
// Sources slice when the env was parsed cleanly but no URLs were provided.
func (s *Server) CatalogSources() *config.CatalogSourcesConfig {
	return s.catalogSources
}

// SetSourcesFetcher wires the V123-1.2 third-party catalog fetcher onto
// the Server. The fetcher is the authoritative source for merged
// snapshots consumed by V123-1.3 (merge under embedded catalog),
// V123-1.5 (GET /api/v1/catalog/sources), and V123-1.6 (force-refresh).
// Nil is accepted — embedded-only mode keeps this unset.
func (s *Server) SetSourcesFetcher(f *sources.Fetcher) {
	s.sourcesFetcher = f
}

// SourcesFetcher returns the wired third-party catalog fetcher, or nil
// when embedded-only mode is active. Consumers must tolerate nil.
func (s *Server) SourcesFetcher() *sources.Fetcher {
	return s.sourcesFetcher
}

// catalogListResponse is the envelope the UI consumes. Keeping it typed
// rather than `map[string]interface{}` gives swagger an exact shape.
type catalogListResponse struct {
	Addons []catalog.CatalogEntry `json:"addons"`
	Total  int                    `json:"total"`
}

// handleListCatalogAddons godoc
//
// @Summary List curated catalog addons
// @Description Returns the Sharko-native curated catalog of addons that ships embedded in the binary. The list is filterable by category, curated_by tag, license, minimum OpenSSF Scorecard score, and by free-text over name/description/maintainers. Read-only; requires authentication.
// @Tags catalog
// @Produce json
// @Security BearerAuth
// @Param category query string false "Primary category (e.g. security, observability)"
// @Param curated_by query string false "Comma-separated curation tags; entry must carry ALL tags"
// @Param license query string false "SPDX license identifier (exact match, case-insensitive)"
// @Param q query string false "Free-text substring match on name/description/maintainers"
// @Param min_score query number false "Minimum OpenSSF Scorecard aggregate score (0-10); entries with unknown score are excluded when > 0"
// @Param min_k8s_version query string false "Caller's Kubernetes version; entries requiring a newer cluster are excluded"
// @Param include_deprecated query boolean false "Include deprecated entries (default false)"
// @Success 200 {object} catalogListResponse "Filtered curated catalog"
// @Failure 503 {object} map[string]interface{} "Catalog not loaded"
// @Router /catalog/addons [get]
func (s *Server) handleListCatalogAddons(w http.ResponseWriter, r *http.Request) {
	if s.catalog == nil {
		writeError(w, http.StatusServiceUnavailable, "catalog not loaded")
		return
	}
	q := catalog.Query{
		Q:             strings.TrimSpace(r.URL.Query().Get("q")),
		Category:      strings.TrimSpace(r.URL.Query().Get("category")),
		License:       strings.TrimSpace(r.URL.Query().Get("license")),
		MinK8sVersion: strings.TrimSpace(r.URL.Query().Get("min_k8s_version")),
	}
	if curated := strings.TrimSpace(r.URL.Query().Get("curated_by")); curated != "" {
		parts := strings.Split(curated, ",")
		q.CuratedBy = make([]string, 0, len(parts))
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				q.CuratedBy = append(q.CuratedBy, p)
			}
		}
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("min_score")); raw != "" {
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil || v < 0 || v > 10 {
			writeError(w, http.StatusBadRequest, "min_score must be a number between 0 and 10")
			return
		}
		q.MinScore = v
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("include_deprecated")); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "include_deprecated must be true or false")
			return
		}
		q.IncludeDeprecated = v
	}

	entries := s.catalog.List(q)
	writeJSON(w, http.StatusOK, catalogListResponse{
		Addons: entries,
		Total:  len(entries),
	})
}

// handleGetCatalogAddon godoc
//
// @Summary Get a curated catalog addon by name
// @Description Returns the full curated catalog entry for a single addon, including its OpenSSF Scorecard security score + derived tier label (Strong / Moderate / Weak) and last-refresh date. Read-only; requires authentication.
// @Tags catalog
// @Produce json
// @Security BearerAuth
// @Param name path string true "Addon name (case-sensitive, matches catalog.name)"
// @Success 200 {object} catalog.CatalogEntry "Curated catalog entry"
// @Failure 404 {object} map[string]interface{} "Addon not found in curated catalog"
// @Failure 503 {object} map[string]interface{} "Catalog not loaded"
// @Router /catalog/addons/{name} [get]
func (s *Server) handleGetCatalogAddon(w http.ResponseWriter, r *http.Request) {
	if s.catalog == nil {
		writeError(w, http.StatusServiceUnavailable, "catalog not loaded")
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "addon name is required")
		return
	}
	entry, ok := s.catalog.Get(name)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, entry)
}
