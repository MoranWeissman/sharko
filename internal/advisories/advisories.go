// Package advisories provides security and release advisory data for Helm charts.
// It queries ArtifactHub as the primary source, falling back to local Helm repo
// annotation parsing when ArtifactHub is unreachable.
package advisories

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// Advisory describes the security and breaking-change profile of a single chart version.
type Advisory struct {
	Version             string // semver string
	ContainsSecurityFix bool   // true if ArtifactHub flags contains_security_updates or keyword hit
	ContainsBreaking    bool   // true if major bump or "breaking" keyword detected
	Summary             string // human-readable short text, e.g. "2 security fixes"
	ReleaseNotes        string // chart's annotation/release notes for this version
}

// Source is an interface that returns advisories for a given chart.
// Implementations must be safe for concurrent use.
type Source interface {
	Get(ctx context.Context, repoURL, chart string) ([]Advisory, error)
}

// cachedEntry holds a cached slice of advisories with its expiry.
type cachedEntry struct {
	data    []Advisory
	fetchAt time.Time
}

// lruEntry is used for LRU eviction tracking.
type lruEntry struct {
	key string
	at  time.Time
}

// Service wraps a primary and fallback Source with in-memory caching.
// Cache is bounded to maxEntries; oldest entry is evicted when full.
type Service struct {
	primary  Source
	fallback Source
	mu       sync.Mutex
	cache    map[string]cachedEntry
	lru      []lruEntry // tracks insertion order for eviction
	cacheTTL time.Duration
}

const (
	maxCacheEntries = 256
	defaultCacheTTL = 1 * time.Hour
)

// NewService constructs a Service using ArtifactHub as primary and the
// release-notes keyword parser as fallback.
func NewService(httpClient *http.Client) *Service {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}
	return &Service{
		primary:  newArtifactHubSource(httpClient),
		fallback: newReleaseNotesSource(httpClient),
		cache:    make(map[string]cachedEntry),
		cacheTTL: defaultCacheTTL,
	}
}

// newServiceWithSources is used by tests to inject custom sources.
func newServiceWithSources(primary, fallback Source, ttl time.Duration) *Service {
	return &Service{
		primary:  primary,
		fallback: fallback,
		cache:    make(map[string]cachedEntry),
		cacheTTL: ttl,
	}
}

// Get returns advisories for the given chart, using the cache when warm.
// On primary failure it falls back to the release-notes source.
// Successful results (from either source) are cached.
func (s *Service) Get(ctx context.Context, repoURL, chart string) ([]Advisory, error) {
	key := repoURL + "\x00" + chart

	s.mu.Lock()
	entry, found := s.cache[key]
	s.mu.Unlock()

	if found && time.Since(entry.fetchAt) < s.cacheTTL {
		return entry.data, nil
	}

	// Try primary.
	data, err := s.primary.Get(ctx, repoURL, chart)
	if err != nil {
		// Fall back gracefully — a missing ArtifactHub entry is non-fatal.
		data, err = s.fallback.Get(ctx, repoURL, chart)
		if err != nil {
			// Both failed — return empty, don't cache.
			return nil, nil //nolint:nilerr
		}
	}

	s.mu.Lock()
	s.evictIfFull()
	s.cache[key] = cachedEntry{data: data, fetchAt: time.Now()}
	s.lru = append(s.lru, lruEntry{key: key, at: time.Now()})
	s.mu.Unlock()

	return data, nil
}

// evictIfFull removes the oldest cache entry when the cache is at capacity.
// Must be called with s.mu held.
func (s *Service) evictIfFull() {
	if len(s.cache) < maxCacheEntries {
		return
	}
	if len(s.lru) == 0 {
		return
	}
	oldest := s.lru[0]
	s.lru = s.lru[1:]
	delete(s.cache, oldest.key)
}
