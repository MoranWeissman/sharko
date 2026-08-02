// Package readcache is a tiny process/instance-local TTL cache for the
// expensive, fleet-wide READ computations behind six hot endpoints (perf
// M1): dashboard stats, clusters list, fleet status, catalog list, version
// matrix, observability overview. All six are pure aggregations over the
// active Git repo + ArgoCD — nothing in them is per-user or per-role, so a
// single cache entry is safe to serve to every authenticated caller (see
// the per-call-site doc comments for the one place this was double
// checked).
//
// # Correctness over cleverness
//
// Cache.Set stores a JSON-encoded snapshot of the value, and Get decodes a
// FRESH copy on every call. This costs an extra marshal/unmarshal per read
// (trivial next to the network round trips this cache exists to avoid) but
// buys real safety: several of the cached handlers (handleListClusters,
// handleGetVersionMatrix) mutate the response object in place after
// fetching it (pagination, sorting, freshness enrichment). If Get handed
// back a shared pointer, one request's in-place mutation would corrupt
// every other request's cached view. The JSON round trip makes that
// mistake structurally impossible instead of relying on every future
// caller to remember not to mutate a cached pointer.
//
// # Invalidation
//
// A stale-forever cache is a bug; a TTL-bounded stale read is fine (see
// DefaultTTL). Invalidation is therefore the load-bearing half of this
// package, same discipline as service.ConnectionService.activeCache:
//
//   - internal/api's auditMiddleware calls InvalidateAll on every mutating
//     (POST/PUT/PATCH/DELETE) request to an /api/ path that completes
//     without a 4xx/5xx — this is the broad net that covers addon
//     enable/disable/add/remove, cluster register/unregister/update,
//     connection save/set-active, and migration/takeover operations
//     without needing every individual handler to remember to call it.
//   - cmd/sharko/serve.go calls InvalidateAll explicitly from the PR
//     tracker's SetOnMergeFn callback — that path fires from a background
//     poll loop, not an HTTP request, so the middleware net can't see it.
//
// # Per-instance, not global
//
// Cache is a plain struct, not a package-level singleton. Production
// wiring wires ONE instance into every service that reads or invalidates
// it (api.Server constructs it in NewServer and threads it into
// ClusterService/AddonService/DashboardService/ObservabilityService via
// their SetCache setters — the same "per-instance test seam" pattern the
// codebase already uses for SetBaseBranchFn). Tests that construct fresh
// services get a fresh, unshared cache automatically and never leak state
// across test cases.
package readcache

import (
	"encoding/json"
	"sync"
	"time"
)

// DefaultTTL is the freshness window for every cache entry in this
// package. 15s is long enough that a dashboard auto-refresh or a user
// bouncing between pages mostly hits a warm cache instead of re-paying
// several upstream round trips, and short enough that a stale read never
// survives more than one refresh cycle even if some future write path
// forgets to invalidate.
const DefaultTTL = 15 * time.Second

type entry struct {
	data      []byte
	expiresAt time.Time
}

// Cache is a small keyed TTL cache. The zero value is not usable — build
// one with New.
type Cache struct {
	mu      sync.RWMutex
	entries map[string]entry
	ttl     time.Duration

	// nowFn is the per-instance test seam for the clock (mirrors the
	// nowFn/tickInterval convention used elsewhere in the codebase — see
	// go-expert.md). nil means time.Now.
	nowFn func() time.Time
}

// New creates a Cache whose entries live for ttl after being Set.
func New(ttl time.Duration) *Cache {
	return &Cache{entries: make(map[string]entry), ttl: ttl}
}

func (c *Cache) now() time.Time {
	if c.nowFn != nil {
		return c.nowFn()
	}
	return time.Now()
}

func (c *Cache) getRaw(key string) ([]byte, bool) {
	c.mu.RLock()
	e, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok || !c.now().Before(e.expiresAt) {
		return nil, false
	}
	return e.data, true
}

func (c *Cache) setRaw(key string, data []byte) {
	c.mu.Lock()
	c.entries[key] = entry{data: data, expiresAt: c.now().Add(c.ttl)}
	c.mu.Unlock()
}

// Invalidate drops a single key. Safe to call when the key isn't cached
// (no-op) and safe to call concurrently with in-flight Get/Set calls.
func (c *Cache) Invalidate(key string) {
	c.mu.Lock()
	delete(c.entries, key)
	c.mu.Unlock()
}

// InvalidateAll drops every cached entry, forcing the next Get for any key
// to miss and the caller to recompute. This is the primary invalidation
// entry point — see the package doc's "Invalidation" section for every
// wired call site.
func (c *Cache) InvalidateAll() {
	c.mu.Lock()
	c.entries = make(map[string]entry)
	c.mu.Unlock()
}

// Len reports the number of entries currently stored, expired or not.
// Test helper only.
func (c *Cache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// Get is a package-level generic function (Go does not allow a generic
// method on a non-generic receiver) that returns a freshly decoded,
// independent copy of the value cached under key, and whether it was
// present and unexpired. A decode failure (should not happen — Set is the
// only writer and always encodes the same T) is treated as a miss.
func Get[T any](c *Cache, key string) (T, bool) {
	var zero T
	data, ok := c.getRaw(key)
	if !ok {
		return zero, false
	}
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return zero, false
	}
	return v, true
}

// Set stores an independent JSON snapshot of value under key, valid for
// the Cache's configured TTL. A marshal failure is a silent no-op — every
// value cached by this package's call sites is already an HTTP response
// model that round-trips through json.Marshal on every request, so a
// failure here would mean the handler's own writeJSON call is about to
// fail too; caching is a pure optimization and must never turn into a
// request-affecting error path.
func Set[T any](c *Cache, key string, value T) {
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	c.setRaw(key, data)
}

// GetOrCompute returns the cached value for key if present and fresh;
// otherwise it calls compute, caches a successful result, and returns it.
// compute's error is returned as-is and NEVER cached — a transient
// upstream failure (Git/ArgoCD timeout) must not wedge the cache in a
// failed state for the rest of the TTL window.
func GetOrCompute[T any](c *Cache, key string, compute func() (T, error)) (T, error) {
	if v, ok := Get[T](c, key); ok {
		return v, nil
	}
	v, err := compute()
	if err != nil {
		var zero T
		return zero, err
	}
	Set(c, key, v)
	return v, nil
}
