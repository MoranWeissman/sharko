package catalog

// cache.go — generic LRU + TTL cache with stale-serve and exponential backoff
// support. Used by the v1.21 ArtifactHub proxy endpoints (search / package /
// versions) to keep latency low and to degrade gracefully when ArtifactHub
// hiccups (rate-limits, 5xx, network).
//
// Design notes:
//   - One cache instance per "tier" (search / package / versions) so capacity
//     and TTL can be tuned independently. See NewTTLCache.
//   - Entries carry both a freshTTL (the normal cache window) and a hard
//     staleTTL (how long we still serve a stale value when upstream fails).
//     `Get` returns (value, fresh, stale, ok) so callers can decide what to do.
//   - LRU eviction is implemented with container/list — O(1) on hit and on
//     insert. Capacity bounds memory; oldest entry is evicted when full.
//   - The cache is package-internal because callers should compose it (e.g.,
//     "fetch from upstream on miss; on error fall back to stale").
//
// The cache is goroutine-safe under sync.Mutex — fine grained enough for the
// expected QPS (single-replica Sharko, single user typing in a search box).

import (
	"container/list"
	"math/rand"
	"sync"
	"time"
)

// TTLCache is a fixed-capacity LRU cache with per-entry timestamps. Values are
// stored as `any` so the cache stays tier-agnostic; callers cast on the way out.
type TTLCache struct {
	mu        sync.Mutex
	capacity  int
	freshTTL  time.Duration
	staleTTL  time.Duration // 0 disables stale-serve; entries vanish at freshTTL
	entries   map[string]*list.Element
	evictList *list.List
	now       func() time.Time // injected for tests
}

type cacheEntry struct {
	key      string
	value    any
	storedAt time.Time
}

// NewTTLCache creates a cache with the given capacity and freshness TTL.
// staleTTL is the maximum age at which a value is still served on upstream
// failure — set to 0 to disable stale-serve.
func NewTTLCache(capacity int, freshTTL, staleTTL time.Duration) *TTLCache {
	if capacity <= 0 {
		capacity = 1
	}
	return &TTLCache{
		capacity:  capacity,
		freshTTL:  freshTTL,
		staleTTL:  staleTTL,
		entries:   make(map[string]*list.Element, capacity),
		evictList: list.New(),
		now:       time.Now,
	}
}

// Get returns the cached value (if present) along with two bools:
//   - fresh: the entry is within freshTTL (caller should serve directly).
//   - stale: the entry is older than freshTTL but within staleTTL (caller may
//     serve as a fallback when upstream fails). When freshTTL == staleTTL == 0
//     stale is always false.
//
// `ok` is false when the entry is missing or expired beyond staleTTL.
//
// Side effect: a hit (fresh OR stale) bumps the entry to the front of the LRU.
func (c *TTLCache) Get(key string) (value any, fresh, stale, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, hit := c.entries[key]
	if !hit {
		return nil, false, false, false
	}
	entry := el.Value.(*cacheEntry)
	age := c.now().Sub(entry.storedAt)
	switch {
	case age <= c.freshTTL:
		c.evictList.MoveToFront(el)
		return entry.value, true, false, true
	case c.staleTTL > 0 && age <= c.staleTTL:
		c.evictList.MoveToFront(el)
		return entry.value, false, true, true
	default:
		// Expired beyond stale window — purge.
		c.removeElementLocked(el)
		return nil, false, false, false
	}
}

// Put inserts or updates the entry. Capacity is enforced by LRU eviction.
func (c *TTLCache) Put(key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, hit := c.entries[key]; hit {
		entry := el.Value.(*cacheEntry)
		entry.value = value
		entry.storedAt = c.now()
		c.evictList.MoveToFront(el)
		return
	}
	entry := &cacheEntry{key: key, value: value, storedAt: c.now()}
	el := c.evictList.PushFront(entry)
	c.entries[key] = el
	if c.evictList.Len() > c.capacity {
		c.evictOldestLocked()
	}
}

// Purge removes every entry. Used by the reprobe endpoint.
func (c *TTLCache) Purge() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*list.Element, c.capacity)
	c.evictList = list.New()
}

// Len returns the current entry count (cheap; useful for tests + metrics).
func (c *TTLCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.evictList.Len()
}

// ExpireAllForTest backdates every entry's storedAt by the given duration so
// callers can simulate the passage of time without sleeping. Intended for
// tests only. A duration past freshTTL but under staleTTL pushes entries into
// the stale window; past staleTTL evicts them on the next Get.
func (c *TTLCache) ExpireAllForTest(by time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, el := range c.entries {
		entry := el.Value.(*cacheEntry)
		entry.storedAt = entry.storedAt.Add(-by)
	}
}

func (c *TTLCache) evictOldestLocked() {
	el := c.evictList.Back()
	if el == nil {
		return
	}
	c.removeElementLocked(el)
}

func (c *TTLCache) removeElementLocked(el *list.Element) {
	c.evictList.Remove(el)
	delete(c.entries, el.Value.(*cacheEntry).key)
}

// ─── Backoff (per-host circuit) ────────────────────────────────────────────
//
// When upstream returns 429 or 5xx in a row, callers should wait before
// hitting it again. Backoff tracks "next attempt allowed at" per host with
// exponential growth: 1s → 2s → 4s → 8s → 16s → 32s (capped at maxBackoff).
// On a successful call, the backoff is reset.

const (
	defaultBackoffStart = 1 * time.Second
	defaultBackoffMax   = 60 * time.Second
)

// Backoff coordinates "should I call upstream now?" decisions across goroutines
// for one logical destination (e.g., ArtifactHub). It is intentionally minimal:
// callers test Allow() before fetching; on success they call Success(); on
// failure (429 / 5xx / network) they call Failure() to extend the cool-down.
type Backoff struct {
	mu          sync.Mutex
	nextAllowed time.Time
	currentWait time.Duration
	start       time.Duration
	max         time.Duration
	now         func() time.Time
	jitter      func(time.Duration) time.Duration
}

// NewBackoff builds a Backoff with the documented defaults (1s → 60s).
func NewBackoff() *Backoff {
	return &Backoff{
		start: defaultBackoffStart,
		max:   defaultBackoffMax,
		now:   time.Now,
		jitter: func(d time.Duration) time.Duration {
			// ±25% jitter so a thundering herd doesn't all retry at once.
			if d <= 0 {
				return d
			}
			delta := time.Duration(rand.Int63n(int64(d) / 2))
			if rand.Intn(2) == 0 {
				return d + delta
			}
			return d - delta
		},
	}
}

// Allow reports whether a new upstream call may run right now.
func (b *Backoff) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return !b.now().Before(b.nextAllowed)
}

// Success resets the cool-down so the next call goes through immediately.
func (b *Backoff) Success() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextAllowed = time.Time{}
	b.currentWait = 0
}

// Failure extends the cool-down using exponential growth (1s → 2s → 4s → … →
// max). Returns the resulting wait duration so callers can log it.
func (b *Backoff) Failure() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.currentWait == 0 {
		b.currentWait = b.start
	} else {
		b.currentWait *= 2
		if b.currentWait > b.max {
			b.currentWait = b.max
		}
	}
	wait := b.jitter(b.currentWait)
	if wait < 0 {
		wait = 0
	}
	b.nextAllowed = b.now().Add(wait)
	return wait
}

// CurrentWait returns the most recent backoff duration (zero when healthy).
func (b *Backoff) CurrentWait() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.currentWait
}

// NextAllowed returns the absolute time at which the next upstream call may
// run. Zero value means "now."
func (b *Backoff) NextAllowed() time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.nextAllowed
}
