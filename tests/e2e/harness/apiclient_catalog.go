//go:build e2e

package harness

// apiclient_catalog.go — typed wrappers for the catalog/marketplace surface
// (V2 Epic 7-1.5). These mirror the in-process JSON shapes from
// internal/api/catalog*.go without re-importing the package's private response
// types: the api package uses lowercase response structs for most endpoints,
// so we re-declare the shape locally with the same JSON field names. Where
// the api package exports a type (catalog.CatalogEntry, catalog.AHSearchPackage,
// catalog.AHPackage), we import and reuse it.
//
// Only test-facing wrappers live here. Lower-level Get/Post helpers belong on
// *Client (apiclient.go); shared assertion helpers (catalog-specific) live in
// catalog_helpers.go in the lifecycle package.

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/catalog"
)

// CatalogListResponse is the wire shape of GET /api/v1/catalog/addons. Mirrors
// the unexported catalogListResponse in internal/api/catalog.go.
type CatalogListResponse struct {
	Addons []catalog.CatalogEntry `json:"addons"`
	Total  int                    `json:"total"`
}

// CatalogSourceRecord is one row of GET /api/v1/catalog/sources and the
// POST /catalog/sources/refresh response. Mirrors the unexported
// catalogSourceRecord in internal/api/catalog_sources.go.
type CatalogSourceRecord struct {
	URL         string     `json:"url"`
	Status      string     `json:"status"`
	LastFetched *time.Time `json:"last_fetched"`
	EntryCount  int        `json:"entry_count"`
	Verified    bool       `json:"verified"`
	Issuer      string     `json:"issuer,omitempty"`
}

// CatalogVersionEntry mirrors the unexported catalogVersionEntry in
// internal/api/catalog_versions.go.
type CatalogVersionEntry struct {
	Version    string `json:"version"`
	AppVersion string `json:"app_version,omitempty"`
	Created    string `json:"created,omitempty"`
	Prerelease bool   `json:"prerelease"`
}

// CatalogVersionsResponse mirrors the unexported catalogVersionsResponse.
type CatalogVersionsResponse struct {
	Addon        string                `json:"addon"`
	Chart        string                `json:"chart"`
	Repo         string                `json:"repo"`
	Versions     []CatalogVersionEntry `json:"versions"`
	LatestStable string                `json:"latest_stable,omitempty"`
	CachedAt     string                `json:"cached_at"`
}

// CatalogReadmeResponse mirrors the unexported catalogReadmeResponse.
type CatalogReadmeResponse struct {
	Readme   string `json:"readme"`
	Source   string `json:"source"`
	AHRepo   string `json:"ah_repo,omitempty"`
	AHChart  string `json:"ah_chart,omitempty"`
	Stale    bool   `json:"stale,omitempty"`
	CachedAt string `json:"cached_at,omitempty"`
}

// ProjectReadmeResponse mirrors the unexported projectReadmeResponse used by
// both /catalog/addons/{name}/project-readme and the remote variant.
type ProjectReadmeResponse struct {
	Readme    string `json:"readme"`
	SourceURL string `json:"source_url,omitempty"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// CatalogSearchResponse mirrors the unexported catalogSearchResponse.
type CatalogSearchResponse struct {
	Query            string                    `json:"query"`
	Curated          []catalog.CatalogEntry    `json:"curated"`
	ArtifactHub      []catalog.AHSearchPackage `json:"artifacthub"`
	ArtifactHubError string                    `json:"artifacthub_error,omitempty"`
	Stale            bool                      `json:"stale,omitempty"`
	CachedAt         string                    `json:"cached_at,omitempty"`
}

// CatalogValidateResponse mirrors the unexported catalogValidateResponse.
type CatalogValidateResponse struct {
	Valid        bool                  `json:"valid"`
	Chart        string                `json:"chart"`
	Repo         string                `json:"repo"`
	Description  string                `json:"description,omitempty"`
	IconURL      string                `json:"icon_url,omitempty"`
	Versions     []CatalogVersionEntry `json:"versions,omitempty"`
	LatestStable string                `json:"latest_stable,omitempty"`
	CachedAt     string                `json:"cached_at,omitempty"`
	ErrorCode    string                `json:"error_code,omitempty"`
	Message      string                `json:"message,omitempty"`
}

// RepoChartsResponse mirrors the unexported repoChartsResponse.
type RepoChartsResponse struct {
	Valid     bool     `json:"valid"`
	Repo      string   `json:"repo"`
	Charts    []string `json:"charts,omitempty"`
	CachedAt  string   `json:"cached_at,omitempty"`
	ErrorCode string   `json:"error_code,omitempty"`
	Message   string   `json:"message,omitempty"`
}

// CatalogRemotePackageResponse mirrors the unexported catalogRemotePackageResponse.
type CatalogRemotePackageResponse struct {
	Package  *catalog.AHPackage `json:"package"`
	Stale    bool               `json:"stale,omitempty"`
	CachedAt string             `json:"cached_at,omitempty"`
}

// CatalogReprobeResponse mirrors the unexported catalogReprobeResponse.
type CatalogReprobeResponse struct {
	Reachable bool   `json:"reachable"`
	LastError string `json:"last_error,omitempty"`
	ProbedAt  string `json:"probed_at"`
}

// ---------------------------------------------------------------------------
// typed wrappers — keep these thin; tests assert on the typed shape.
// ---------------------------------------------------------------------------

// ListCatalogAddons fetches GET /api/v1/catalog/addons (no filters).
func (c *Client) ListCatalogAddons(t *testing.T) CatalogListResponse {
	t.Helper()
	var out CatalogListResponse
	c.GetJSON(t, "/api/v1/catalog/addons", &out)
	return out
}

// GetCatalogAddon fetches GET /api/v1/catalog/addons/{name}.
func (c *Client) GetCatalogAddon(t *testing.T, name string) catalog.CatalogEntry {
	t.Helper()
	var out catalog.CatalogEntry
	c.GetJSON(t, "/api/v1/catalog/addons/"+url.PathEscape(name), &out)
	return out
}

// ListCatalogVersions fetches GET /api/v1/catalog/addons/{name}/versions.
//
// Network-touching: hits the upstream Helm repo for the named chart. Tests
// that exercise this should gate on E2E_OFFLINE.
func (c *Client) ListCatalogVersions(t *testing.T, name string) CatalogVersionsResponse {
	t.Helper()
	var out CatalogVersionsResponse
	c.GetJSON(t, "/api/v1/catalog/addons/"+url.PathEscape(name)+"/versions", &out)
	return out
}

// GetCatalogReadme fetches GET /api/v1/catalog/addons/{name}/readme.
//
// Network-touching: resolves via ArtifactHub. Returns 200 even on upstream
// failure (the response carries an empty body); tests can gate on E2E_OFFLINE
// to skip when the network is not available.
func (c *Client) GetCatalogReadme(t *testing.T, name string) CatalogReadmeResponse {
	t.Helper()
	var out CatalogReadmeResponse
	c.GetJSON(t, "/api/v1/catalog/addons/"+url.PathEscape(name)+"/readme", &out)
	return out
}

// GetCatalogProjectReadme fetches GET /api/v1/catalog/addons/{name}/project-readme.
//
// Network-touching: hits the GitHub README API. Always returns 200 (degrades
// to Available=false on failure).
func (c *Client) GetCatalogProjectReadme(t *testing.T, name string) ProjectReadmeResponse {
	t.Helper()
	var out ProjectReadmeResponse
	c.GetJSON(t, "/api/v1/catalog/addons/"+url.PathEscape(name)+"/project-readme", &out)
	return out
}

// SearchCatalog fetches GET /api/v1/catalog/search?q=<q>.
//
// Network-tolerant: curated half always populated; artifacthub half may carry
// `artifacthub_error` when the upstream is unreachable. Always 200 on a
// non-empty q.
func (c *Client) SearchCatalog(t *testing.T, q string) CatalogSearchResponse {
	t.Helper()
	var out CatalogSearchResponse
	c.GetJSON(t, "/api/v1/catalog/search?q="+url.QueryEscape(q), &out)
	return out
}

// ListCatalogSources fetches GET /api/v1/catalog/sources.
//
// Local-only: embedded pseudo-source plus any wired third-party fetcher
// snapshots. In the in-process boot path no fetcher is wired so the response
// is a single embedded record.
func (c *Client) ListCatalogSources(t *testing.T) []CatalogSourceRecord {
	t.Helper()
	var out []CatalogSourceRecord
	c.GetJSON(t, "/api/v1/catalog/sources", &out)
	return out
}

// RefreshCatalogSources POSTs /api/v1/catalog/sources/refresh.
//
// Admin-only (catalog.sources.refresh action). Local-only when no fetcher is
// wired. Returns the same shape as ListCatalogSources after the refresh.
func (c *Client) RefreshCatalogSources(t *testing.T) []CatalogSourceRecord {
	t.Helper()
	var out []CatalogSourceRecord
	c.PostJSON(t, "/api/v1/catalog/sources/refresh", nil, &out)
	return out
}

// ValidateCatalogChart fetches GET /api/v1/catalog/validate?repo=<repo>&chart=<chart>.
//
// Returns 200 with `valid: false` on shape errors, SSRF blocks, and upstream
// failures (with structured `error_code`). Returns 400 only on entirely
// missing required params. Tests that expect a successful (valid=true)
// response must gate on E2E_OFFLINE because the handler hits the public
// Helm repo to confirm the chart exists.
func (c *Client) ValidateCatalogChart(t *testing.T, repo, chart string) CatalogValidateResponse {
	t.Helper()
	var out CatalogValidateResponse
	q := "?repo=" + url.QueryEscape(repo) + "&chart=" + url.QueryEscape(chart)
	c.GetJSON(t, "/api/v1/catalog/validate"+q, &out)
	return out
}

// ListRepoCharts fetches GET /api/v1/catalog/repo-charts?repo=<repo>.
//
// Same status-code semantics as ValidateCatalogChart: 200 for both success
// and structured failure; 400 only on missing repo.
func (c *Client) ListRepoCharts(t *testing.T, repo string) RepoChartsResponse {
	t.Helper()
	var out RepoChartsResponse
	c.GetJSON(t, "/api/v1/catalog/repo-charts?repo="+url.QueryEscape(repo), &out)
	return out
}

// GetRemotePackage fetches GET /api/v1/catalog/remote/{repo}/{name}.
//
// Network-touching: proxies ArtifactHub. Hermetic test runs should gate via
// E2E_OFFLINE since 502 (no cache) is the offline outcome.
func (c *Client) GetRemotePackage(t *testing.T, repo, name string) CatalogRemotePackageResponse {
	t.Helper()
	var out CatalogRemotePackageResponse
	c.GetJSON(t, "/api/v1/catalog/remote/"+url.PathEscape(repo)+"/"+url.PathEscape(name), &out)
	return out
}

// GetRemoteProjectReadme fetches /api/v1/catalog/remote/{repo}/{name}/project-readme.
//
// Network-touching but gracefully degrades: always 200; Available=false on
// upstream failure.
func (c *Client) GetRemoteProjectReadme(t *testing.T, repo, name string) ProjectReadmeResponse {
	t.Helper()
	var out ProjectReadmeResponse
	c.GetJSON(t, "/api/v1/catalog/remote/"+url.PathEscape(repo)+"/"+url.PathEscape(name)+"/project-readme", &out)
	return out
}

// ReprobeArtifactHub POSTs /api/v1/catalog/reprobe. Tier-1 (admin operational).
//
// Network-touching: probes ArtifactHub. Always 200; Reachable=false on
// upstream failure.
func (c *Client) ReprobeArtifactHub(t *testing.T) CatalogReprobeResponse {
	t.Helper()
	var out CatalogReprobeResponse
	c.PostJSON(t, "/api/v1/catalog/reprobe", nil, &out)
	return out
}

// RefreshCatalogSourcesStatus POSTs /api/v1/catalog/sources/refresh and returns
// the raw HTTP status — for RBAC negative tests that intentionally drive a 403.
func (c *Client) RefreshCatalogSourcesStatus(t *testing.T) int {
	t.Helper()
	resp := c.Do(t, http.MethodPost, "/api/v1/catalog/sources/refresh", nil)
	defer resp.Body.Close()
	return resp.StatusCode
}
