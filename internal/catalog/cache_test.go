package catalog

import (
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock is a deterministic time source for cache TTL testing.
type fakeClock struct{ now atomic.Int64 }

func (c *fakeClock) Now() time.Time { return time.Unix(0, c.now.Load()) }
func (c *fakeClock) Advance(d time.Duration) {
	c.now.Add(int64(d))
}

func newClock(start time.Time) *fakeClock {
	c := &fakeClock{}
	c.now.Store(start.UnixNano())
	return c
}

func TestTTLCache_FreshHit(t *testing.T) {
	c := NewTTLCache(10, 1*time.Minute, 1*time.Hour)
	clock := newClock(time.Unix(1_700_000_000, 0))
	c.now = clock.Now

	c.Put("k", "v")

	v, fresh, stale, ok := c.Get("k")
	if !ok || !fresh || stale {
		t.Fatalf("Get fresh: ok=%v fresh=%v stale=%v", ok, fresh, stale)
	}
	if v.(string) != "v" {
		t.Fatalf("value = %v", v)
	}
}

func TestTTLCache_StaleHit(t *testing.T) {
	c := NewTTLCache(10, 1*time.Minute, 1*time.Hour)
	clock := newClock(time.Unix(1_700_000_000, 0))
	c.now = clock.Now

	c.Put("k", "v")
	clock.Advance(2 * time.Minute) // past freshTTL but inside staleTTL

	v, fresh, stale, ok := c.Get("k")
	if !ok || fresh || !stale {
		t.Fatalf("Get stale: ok=%v fresh=%v stale=%v", ok, fresh, stale)
	}
	if v.(string) != "v" {
		t.Fatalf("value = %v", v)
	}
}

func TestTTLCache_PastStaleTTL(t *testing.T) {
	c := NewTTLCache(10, 1*time.Minute, 5*time.Minute)
	clock := newClock(time.Unix(1_700_000_000, 0))
	c.now = clock.Now

	c.Put("k", "v")
	clock.Advance(10 * time.Minute)

	if _, _, _, ok := c.Get("k"); ok {
		t.Fatalf("expected miss past stale TTL")
	}
}

func TestTTLCache_LRUEviction(t *testing.T) {
	c := NewTTLCache(2, 1*time.Hour, 0)
	c.Put("a", 1)
	c.Put("b", 2)
	if _, _, _, ok := c.Get("a"); !ok {
		t.Fatalf("a should still be present")
	}
	// "a" is now most-recent; inserting "c" should evict "b"
	c.Put("c", 3)
	if _, _, _, ok := c.Get("b"); ok {
		t.Fatalf("b should have been evicted")
	}
	if _, _, _, ok := c.Get("a"); !ok {
		t.Fatalf("a should still be present")
	}
	if _, _, _, ok := c.Get("c"); !ok {
		t.Fatalf("c should be present")
	}
}

func TestTTLCache_PurgeRemovesAll(t *testing.T) {
	c := NewTTLCache(3, 1*time.Hour, 0)
	c.Put("a", 1)
	c.Put("b", 2)
	c.Purge()
	if c.Len() != 0 {
		t.Fatalf("len after purge = %d", c.Len())
	}
	if _, _, _, ok := c.Get("a"); ok {
		t.Fatalf("a present after purge")
	}
}

func TestTTLCache_OverwriteRefreshesTimestamp(t *testing.T) {
	c := NewTTLCache(2, 100*time.Millisecond, 0)
	clock := newClock(time.Unix(1_700_000_000, 0))
	c.now = clock.Now

	c.Put("k", "v1")
	clock.Advance(80 * time.Millisecond)
	c.Put("k", "v2") // refresh
	clock.Advance(80 * time.Millisecond)

	v, fresh, _, ok := c.Get("k")
	if !ok || !fresh {
		t.Fatalf("expected fresh after overwrite")
	}
	if v.(string) != "v2" {
		t.Fatalf("expected v2 got %v", v)
	}
}

func TestBackoff_AllowInitially(t *testing.T) {
	b := NewBackoff()
	if !b.Allow() {
		t.Fatalf("backoff should allow initially")
	}
}

func TestBackoff_FailureBlocks(t *testing.T) {
	b := NewBackoff()
	// Disable jitter so timing is deterministic.
	b.jitter = func(d time.Duration) time.Duration { return d }
	clock := newClock(time.Unix(1_700_000_000, 0))
	b.now = clock.Now

	b.Failure()
	if b.Allow() {
		t.Fatalf("backoff should block immediately after failure")
	}
	clock.Advance(2 * time.Second)
	if !b.Allow() {
		t.Fatalf("backoff should allow after wait")
	}
}

func TestBackoff_ExponentialGrowth(t *testing.T) {
	b := NewBackoff()
	b.jitter = func(d time.Duration) time.Duration { return d }
	clock := newClock(time.Unix(1_700_000_000, 0))
	b.now = clock.Now

	// Three consecutive failures: 1s → 2s → 4s.
	w1 := b.Failure()
	if w1 != 1*time.Second {
		t.Fatalf("first wait = %v, want 1s", w1)
	}
	w2 := b.Failure()
	if w2 != 2*time.Second {
		t.Fatalf("second wait = %v, want 2s", w2)
	}
	w3 := b.Failure()
	if w3 != 4*time.Second {
		t.Fatalf("third wait = %v, want 4s", w3)
	}
}

func TestBackoff_SuccessResets(t *testing.T) {
	b := NewBackoff()
	b.jitter = func(d time.Duration) time.Duration { return d }
	clock := newClock(time.Unix(1_700_000_000, 0))
	b.now = clock.Now

	b.Failure()
	b.Failure()
	b.Success()
	if !b.Allow() {
		t.Fatalf("Success should clear backoff")
	}
	// Next failure starts at 1s again.
	w := b.Failure()
	if w != 1*time.Second {
		t.Fatalf("wait after reset = %v, want 1s", w)
	}
}

func TestBackoff_CapsAtMax(t *testing.T) {
	b := NewBackoff()
	b.jitter = func(d time.Duration) time.Duration { return d }
	for i := 0; i < 20; i++ {
		b.Failure()
	}
	if b.CurrentWait() > defaultBackoffMax {
		t.Fatalf("wait exceeded max: %v", b.CurrentWait())
	}
}
