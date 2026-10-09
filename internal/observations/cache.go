package observations

import (
	"context"
	"os"
	"sync"
	"time"
)

const defaultCacheTTL = 30 * time.Second

// CachedStatusProvider wraps the observations Store with an in-memory TTL cache.
type CachedStatusProvider struct {
	store *Store
	ttl   time.Duration

	mu    sync.Mutex
	cache map[string]cachedEntry
}

type cachedEntry struct {
	result    StatusResult
	expiresAt time.Time
}

// NewCachedStatusProvider creates a new cached status provider.
// TTL is read from SHARKO_CLUSTER_STATUS_CACHE_TTL (Go duration string, e.g. "30s").
// Falls back to 30s if unset or invalid.
func NewCachedStatusProvider(store *Store) *CachedStatusProvider {
	ttl := defaultCacheTTL
	if raw := os.Getenv("SHARKO_CLUSTER_STATUS_CACHE_TTL"); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil {
			ttl = parsed
		}
	}
	return &CachedStatusProvider{
		store: store,
		ttl:   ttl,
		cache: make(map[string]cachedEntry),
	}
}

// newCachedStatusProviderWithTTL is a test helper that accepts an explicit TTL.
func newCachedStatusProviderWithTTL(store *Store, ttl time.Duration) *CachedStatusProvider {
	return &CachedStatusProvider{
		store: store,
		ttl:   ttl,
		cache: make(map[string]cachedEntry),
	}
}

// GetStatus returns the computed status for a cluster, using the cache when possible.
// If refresh is true, the cache is bypassed and a fresh status is computed.
// hasHealthyAddonFn is called to determine if the cluster has a healthy addon.
func (c *CachedStatusProvider) GetStatus(ctx context.Context, clusterName string, refresh bool, hasHealthyAddonFn func(string) bool) (StatusResult, error) {
	now := time.Now()

	if !refresh {
		c.mu.Lock()
		entry, ok := c.cache[clusterName]
		c.mu.Unlock()
		if ok && now.Before(entry.expiresAt) {
			return entry.result, nil
		}
	}

	obs, err := c.store.GetObservation(ctx, clusterName)
	if err != nil {
		return StatusResult{Status: StatusUnknown}, err
	}

	hasAddon := false
	if hasHealthyAddonFn != nil {
		hasAddon = hasHealthyAddonFn(clusterName)
	}

	result := ComputeStatus(obs, hasAddon)

	c.mu.Lock()
	c.cache[clusterName] = cachedEntry{
		result:    result,
		expiresAt: now.Add(c.ttl),
	}
	c.mu.Unlock()

	return result, nil
}
