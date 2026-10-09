package api

// catalog_proxy_state.go — process-global state shared by the v1.21 ArtifactHub
// proxy handlers (catalog_search.go, catalog_remote.go, catalog_reprobe.go):
//
//   • searchCache    — search-result tier (10 min fresh / 24 h stale, 200 entries)
//   • packageCache   — package-detail tier (1 h fresh / 24 h stale, 500 entries)
//   • ahBackoff      — single circuit breaker for the ArtifactHub API host
//   • ahClient       — shared HTTP client; net.Transport reuse + sane defaults
//
// All four are var-level so tests in the package can swap them via the helpers
// below. Production code never replaces them.
//
// Why globals here vs. fields on *Server: the cache is part of a global
// integration with one external host; nothing else in the project gets
// per-request scope on these. Putting them on *Server would force every
// handler to hold the receiver, but they live in package-scope helpers.

import (
	"time"

	"github.com/MoranWeissman/sharko/internal/catalog"
)

// Cache TTLs and capacities — see design doc §4.5.
const (
	catalogSearchFreshTTL  = 10 * time.Minute
	catalogSearchStaleTTL  = 24 * time.Hour
	catalogSearchCap       = 200
	catalogPackageFreshTTL = 1 * time.Hour
	catalogPackageStaleTTL = 24 * time.Hour
	catalogPackageCap      = 500
)

var (
	searchCache  = catalog.NewTTLCache(catalogSearchCap, catalogSearchFreshTTL, catalogSearchStaleTTL)
	packageCache = catalog.NewTTLCache(catalogPackageCap, catalogPackageFreshTTL, catalogPackageStaleTTL)
	ahBackoff    = catalog.NewBackoff()
	ahClient     = catalog.NewArtifactHubClient(nil)
)

// resetCatalogProxyStateForTest is exported (within the package) for tests so a
// test can run with a fresh cache + healthy backoff regardless of what other
// tests left behind. Production code never calls this.
func resetCatalogProxyStateForTest() {
	searchCache.Purge()
	packageCache.Purge()
	ahBackoff.Success()
}

// setArtifactHubClientForTest swaps the shared client (used by tests that
// point the client at httptest.NewServer). Restore via the returned cleanup.
func setArtifactHubClientForTest(c *catalog.ArtifactHubClient) func() {
	prev := ahClient
	ahClient = c
	return func() { ahClient = prev }
}

// expireSearchCacheForTest backdates every search-cache entry past its fresh
// window so the next Get returns stale=true. Used to test stale-serve without
// sleeping for 10 minutes.
func expireSearchCacheForTest() {
	searchCache.ExpireAllForTest(catalogSearchFreshTTL + 1*time.Second)
}

// expirePackageCacheForTest mirrors expireSearchCacheForTest for package detail.
func expirePackageCacheForTest() {
	packageCache.ExpireAllForTest(catalogPackageFreshTTL + 1*time.Second)
}
