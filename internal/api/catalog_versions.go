package api

// catalog_versions.go — read-only endpoint that surfaces chart versions
// for a curated catalog entry. Powers the version picker in the
// Marketplace Configure modal.
//
// Why a separate endpoint vs. the existing /api/v1/upgrade/{addonName}/versions:
//   • upgrade/versions is keyed by an addon already present in the user's GitOps
//     repo. The Marketplace Configure step runs BEFORE the addon exists in the
//     repo, so we look the chart up via the curated catalog entry instead.
//   • Caching is local to this handler (15 min, in-memory) so an empty Helm
//     repo round-trip per modal open isn't required.

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MoranWeissman/sharko/internal/helm"
)

// catalogVersionEntry is the trimmed version shape the UI consumes. Mirroring
// helm.ChartVersion 1:1 would expose URLs the browser doesn't need.
type catalogVersionEntry struct {
	Version    string `json:"version"`
	AppVersion string `json:"app_version,omitempty"`
	Created    string `json:"created,omitempty"`
	Prerelease bool   `json:"prerelease"`
}

// catalogVersionsResponse is the envelope. `latest_stable` is the first
// non-prerelease version in the descending list; empty when none exists.
//
// VersionCheckUnknown is true when Sharko could not determine any versions
// for the chart — today that's specifically an oci:// repo, which needs
// registry credentials this handler doesn't have (v4 wave 1 Story 3.3:
// "version checking degrades gracefully without registry credentials —
// shows unknown, never errors"). Versions/LatestStable are empty in that
// case; the response is still a 200, never a 502, so the UI can render an
// "unknown" pill instead of an error banner.
type catalogVersionsResponse struct {
	Addon               string                `json:"addon"`
	Chart               string                `json:"chart"`
	Repo                string                `json:"repo"`
	Versions            []catalogVersionEntry `json:"versions"`
	LatestStable        string                `json:"latest_stable,omitempty"`
	CachedAt            string                `json:"cached_at"`
	VersionCheckUnknown bool                  `json:"version_check_unknown,omitempty"`
	// NoDataReason is a complete, plain-English sentence for why there is
	// no version list, empty when there is one. The UI renders it verbatim
	// so a chart repo with no readable index says so, instead of leaving a
	// blank that reads like "nothing newer out there".
	NoDataReason string `json:"no_data_reason,omitempty"`
}

// catalogVersionsCacheEntry holds a previously-fetched version list plus the
// timestamp used for TTL eviction.
type catalogVersionsCacheEntry struct {
	resp     catalogVersionsResponse
	cachedAt time.Time
}

// catalogVersionsCache is process-global. The fetcher itself caches the parsed
// index per repo URL — we layer a second cache here so we can:
//   - answer with a single map lookup (no parsing) on warm hits, and
//   - TTL-evict the parsed index without rebuilding the helm.Fetcher.
//
// Capped at 200 entries to bound memory; oldest-first eviction.
var (
	catalogVersionsMu       sync.Mutex
	catalogVersionsCacheMap = make(map[string]*catalogVersionsCacheEntry)
	catalogVersionsTTL      = 15 * time.Minute
	catalogVersionsCap      = 200

	// One shared fetcher across all callers — the underlying http.Client and
	// in-memory caches are safe to reuse.
	catalogVersionsFetcher = helm.NewFetcher()
)

// handleListCatalogVersions godoc
//
// @Summary List chart versions for a curated catalog addon
// @Description Returns the chart versions reported by the upstream Helm repo `index.yaml` for the curated catalog entry identified by `name`. Versions are sorted newest-first and tagged with `prerelease=true` when the SemVer build/prerelease segment is present. The first non-prerelease version is returned in `latest_stable`. Responses are cached server-side for 15 minutes per repo+chart pair. Read-only; requires authentication.
// @Tags catalog
// @Produce json
// @Security BearerAuth
// @Param name path string true "Curated catalog addon name"
// @Param include_prereleases query boolean false "Include prerelease versions in the response (default true; the UI filters by the per-entry prerelease flag)"
// @Success 200 {object} catalogVersionsResponse "Sorted chart versions for the addon"
// @Failure 404 {object} map[string]interface{} "Addon not found in curated catalog"
// @Failure 502 {object} map[string]interface{} "Upstream Helm repo unreachable"
// @Failure 503 {object} map[string]interface{} "Catalog not loaded"
// @Router /marketplace/addons/{name}/versions [get]
func (s *Server) handleListCatalogVersions(w http.ResponseWriter, r *http.Request) {
	if s.catalog == nil {
		writeError(w, http.StatusServiceUnavailable, "catalog not loaded")
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "addon name is required")
		return
	}
	// V123-PR-B (H1): merged-catalog lookup so third-party snapshot entries
	// resolve here too. Same wording on the 404 — clients comparing the
	// error string keep working.
	entry, ok := s.mergedCatalogGet(name)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	includePrereleases := true
	if raw := strings.TrimSpace(r.URL.Query().Get("include_prereleases")); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "include_prereleases must be true or false")
			return
		}
		includePrereleases = v
	}

	// v4 wave 1 Story 3.4: prefer the freshness scheduler's daily snapshot
	// over the ad hoc 15-minute request cache. The snapshot's CheckedAt is
	// a real, durable "Sharko last checked this" timestamp — not merely
	// "the last time somebody happened to open this modal" — so every
	// caller (catalog list, addon detail, version picker) sees the same
	// honest cadence. Only curated catalog entries are covered (the
	// scheduler walks s.catalog, not the merged third-party view); a miss
	// here (scheduler unset, entry not yet processed, or a third-party
	// entry) falls through to the on-demand path unchanged.
	if s.freshness != nil {
		if snap, ok := s.freshness.VersionSnapshot(entry.Name); ok {
			if snap.Unknown {
				resp := catalogVersionsResponse{
					Addon:               entry.Name,
					Chart:               entry.Chart,
					Repo:                entry.Repo,
					CachedAt:            snap.CheckedAt.UTC().Format(time.RFC3339),
					VersionCheckUnknown: true,
					NoDataReason:        snap.NoDataReason,
				}
				writeJSON(w, http.StatusOK, resp)
				return
			}
			resp := buildVersionsResponse(entry.Name, entry.Chart, entry.Repo, snap.Versions)
			resp.CachedAt = snap.CheckedAt.UTC().Format(time.RFC3339)
			writeJSON(w, http.StatusOK, filterVersions(resp, includePrereleases))
			return
		}
	}

	cacheKey := entry.Repo + "|" + entry.Chart
	if cached, ok := lookupCachedVersions(cacheKey); ok {
		writeJSON(w, http.StatusOK, filterVersions(cached, includePrereleases))
		return
	}

	// Cache miss — call upstream. 8s budget per request keeps the modal
	// responsive even if the repo is slow.
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	versions, err := catalogVersionsFetcher.ListVersions(ctx, entry.Repo, entry.Chart)
	if err != nil {
		// Graceful degrade for oci:// repos without registry credentials
		// (v4 wave 1 Story 3.3) — "unknown", not an error. Every other
		// fetch failure (classic repo unreachable, malformed index, chart
		// not found) is still a genuine 502.
		if errors.Is(err, helm.ErrOCIVersionCheckUnsupported) {
			resp := catalogVersionsResponse{
				Addon:               entry.Name,
				Chart:               entry.Chart,
				Repo:                entry.Repo,
				CachedAt:            time.Now().UTC().Format(time.RFC3339),
				VersionCheckUnknown: true,
				NoDataReason:        "no freshness data for this source — " + entry.Repo + " has no version index Sharko can read",
			}
			// Deliberately NOT cached — a registry that gains credentials
			// later (Story 3.4) should be re-checked on the next call
			// rather than serving a stale "unknown" for 15 minutes.
			writeJSON(w, http.StatusOK, resp)
			return
		}
		writeError(w, http.StatusBadGateway, "failed to list versions: "+err.Error())
		return
	}

	resp := buildVersionsResponse(entry.Name, entry.Chart, entry.Repo, versions)
	storeCachedVersions(cacheKey, resp)
	writeJSON(w, http.StatusOK, filterVersions(resp, includePrereleases))
}

// resolveLatestChartVersion answers "what is the newest version Sharko
// knows for this chart?" — the lookup a Marketplace add uses when it
// arrives without a version (review finding B-1).
//
// It reads the same three places the version picker reads, in the same
// order: the freshness scheduler's durable daily snapshot, then the
// 15-minute request cache, then the chart repo's index. The newest
// NON-prerelease version wins; a chart that only ever publishes
// prereleases falls back to the newest of those, because refusing to
// approve such a chart at all would be worse than pinning what it has.
//
// false means Sharko genuinely has no version data — an oci:// registry it
// cannot read an index from, or a repo that was unreachable. The caller
// turns that into plain words asking the person to pick one; it never
// guesses and never errors.
func (s *Server) resolveLatestChartVersion(ctx context.Context, addonName, repoURL, chart string) (string, bool) {
	if repoURL == "" || chart == "" {
		return "", false
	}

	pick := func(resp catalogVersionsResponse) (string, bool) {
		if resp.LatestStable != "" {
			return resp.LatestStable, true
		}
		if len(resp.Versions) > 0 && resp.Versions[0].Version != "" {
			return resp.Versions[0].Version, true
		}
		return "", false
	}

	if s.freshness != nil && addonName != "" {
		if snap, ok := s.freshness.VersionSnapshot(addonName); ok && !snap.Unknown {
			if v, ok := pick(buildVersionsResponse(addonName, chart, repoURL, snap.Versions)); ok {
				return v, true
			}
		}
	}

	cacheKey := repoURL + "|" + chart
	if cached, ok := lookupCachedVersions(cacheKey); ok {
		return pick(cached)
	}

	fetchCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	versions, err := catalogVersionsFetcher.ListVersions(fetchCtx, repoURL, chart)
	if err != nil || len(versions) == 0 {
		return "", false
	}
	resp := buildVersionsResponse(addonName, chart, repoURL, versions)
	storeCachedVersions(cacheKey, resp)
	return pick(resp)
}

// buildVersionsResponse converts helm.ChartVersion entries into the API shape
// and computes `latest_stable`. Sort is descending by SemVer (best-effort —
// invalid versions sink to the end).
func buildVersionsResponse(addon, chart, repo string, in []helm.ChartVersion) catalogVersionsResponse {
	out := make([]catalogVersionEntry, 0, len(in))
	for _, v := range in {
		out = append(out, catalogVersionEntry{
			Version:    v.Version,
			AppVersion: v.AppVersion,
			Created:    v.Created,
			Prerelease: isPrerelease(v.Version),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return compareSemverDesc(out[i].Version, out[j].Version)
	})

	resp := catalogVersionsResponse{
		Addon:    addon,
		Chart:    chart,
		Repo:     repo,
		Versions: out,
		CachedAt: time.Now().UTC().Format(time.RFC3339),
	}
	for _, v := range out {
		if !v.Prerelease {
			resp.LatestStable = v.Version
			break
		}
	}
	return resp
}

// isPrerelease reports whether a SemVer-ish string carries a prerelease label
// (anything after `-` per SemVer 2.0). Build metadata (after `+`) is ignored
// because it is informational, not a release-status signal.
func isPrerelease(version string) bool {
	v := strings.TrimPrefix(strings.TrimSpace(version), "v")
	// strip build metadata first
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	return strings.IndexByte(v, '-') >= 0
}

// compareSemverDesc returns true when a should sort before b in a descending
// list. Versions that don't parse as numeric major.minor.patch sink to the end.
func compareSemverDesc(a, b string) bool {
	pa := parseSemverParts(a)
	pb := parseSemverParts(b)
	if pa == nil && pb == nil {
		return a > b // lexical fallback
	}
	if pa == nil {
		return false
	}
	if pb == nil {
		return true
	}
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	// Equal numeric parts — non-prerelease beats prerelease (so 1.2.3 > 1.2.3-rc.1).
	preA := isPrerelease(a)
	preB := isPrerelease(b)
	if preA != preB {
		return !preA
	}
	return a > b
}

// parseSemverParts returns [major, minor, patch] (numeric, prerelease stripped),
// or nil when the version is unparseable. Mirrors helm.parseVersion's behaviour
// but without exporting an extra symbol.
func parseSemverParts(version string) []int {
	v := strings.TrimPrefix(strings.TrimSpace(version), "v")
	// strip build metadata then prerelease
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	if i := strings.IndexByte(v, '-'); i >= 0 {
		v = v[:i]
	}
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 3 {
		return nil
	}
	out := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		out[i] = n
	}
	return out
}

// filterVersions returns a shallow copy with prereleases stripped when the
// caller asked to exclude them. The cached entry stays untouched.
func filterVersions(resp catalogVersionsResponse, includePrereleases bool) catalogVersionsResponse {
	if includePrereleases {
		return resp
	}
	filtered := make([]catalogVersionEntry, 0, len(resp.Versions))
	for _, v := range resp.Versions {
		if !v.Prerelease {
			filtered = append(filtered, v)
		}
	}
	clone := resp
	clone.Versions = filtered
	return clone
}

// lookupCachedVersions returns the cached response when present and not yet
// expired. TTL evictions happen lazily here to avoid a background goroutine.
func lookupCachedVersions(key string) (catalogVersionsResponse, bool) {
	catalogVersionsMu.Lock()
	defer catalogVersionsMu.Unlock()
	c, ok := catalogVersionsCacheMap[key]
	if !ok {
		return catalogVersionsResponse{}, false
	}
	if time.Since(c.cachedAt) > catalogVersionsTTL {
		delete(catalogVersionsCacheMap, key)
		return catalogVersionsResponse{}, false
	}
	return c.resp, true
}

// storeCachedVersions writes a fresh entry, evicting the oldest if the cache
// has reached its cap. This keeps the bound at exactly catalogVersionsCap.
func storeCachedVersions(key string, resp catalogVersionsResponse) {
	catalogVersionsMu.Lock()
	defer catalogVersionsMu.Unlock()
	if len(catalogVersionsCacheMap) >= catalogVersionsCap {
		evictOldestVersionsLocked()
	}
	catalogVersionsCacheMap[key] = &catalogVersionsCacheEntry{
		resp:     resp,
		cachedAt: time.Now(),
	}
}

// evictOldestVersionsLocked removes the single oldest entry. Caller must hold
// catalogVersionsMu. Linear scan is fine at a 200-entry cap.
func evictOldestVersionsLocked() {
	var oldestKey string
	var oldest time.Time
	first := true
	for k, v := range catalogVersionsCacheMap {
		if first || v.cachedAt.Before(oldest) {
			oldestKey = k
			oldest = v.cachedAt
			first = false
		}
	}
	if oldestKey != "" {
		delete(catalogVersionsCacheMap, oldestKey)
	}
}

// resetCatalogVersionsCacheForTest is exported for tests in the same package.
// We deliberately do not surface a public Reset method on the cache — the cache
// is implementation detail and production code should never need to flush it.
func resetCatalogVersionsCacheForTest() {
	catalogVersionsMu.Lock()
	defer catalogVersionsMu.Unlock()
	catalogVersionsCacheMap = make(map[string]*catalogVersionsCacheEntry)
}
