package readcache

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type sample struct {
	Value int `json:"value"`
}

func TestGetOrCompute_MissThenHit(t *testing.T) {
	t.Parallel()
	c := New(DefaultTTL)

	var calls int32
	compute := func() (sample, error) {
		atomic.AddInt32(&calls, 1)
		return sample{Value: 42}, nil
	}

	v, err := GetOrCompute(c, "key", compute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Value != 42 {
		t.Fatalf("value = %d, want 42", v.Value)
	}
	if calls != 1 {
		t.Fatalf("compute called %d times, want 1", calls)
	}

	// Second call within TTL must hit the cache — compute not called again.
	v2, err := GetOrCompute(c, "key", compute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v2.Value != 42 {
		t.Fatalf("value = %d, want 42", v2.Value)
	}
	if calls != 1 {
		t.Fatalf("compute called %d times on cache hit, want 1 (still)", calls)
	}
}

func TestGetOrCompute_ReturnsIndependentCopies(t *testing.T) {
	t.Parallel()
	c := New(DefaultTTL)

	type withSlice struct {
		Items []string `json:"items"`
	}

	_, err := GetOrCompute(c, "k", func() (withSlice, error) {
		return withSlice{Items: []string{"a", "b"}}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// First reader mutates its copy.
	v1, ok := Get[withSlice](c, "k")
	if !ok {
		t.Fatalf("expected cache hit")
	}
	v1.Items[0] = "MUTATED"
	v1.Items = append(v1.Items, "extra")

	// A second, independent read must be unaffected by the first
	// caller's mutation — this is the whole reason Get decodes a fresh
	// copy from the stored JSON snapshot rather than handing back a
	// shared pointer/slice.
	v2, ok := Get[withSlice](c, "k")
	if !ok {
		t.Fatalf("expected cache hit")
	}
	if v2.Items[0] != "a" || len(v2.Items) != 2 {
		t.Fatalf("second read was corrupted by first reader's mutation: %+v", v2)
	}
}

func TestCache_Expiry(t *testing.T) {
	t.Parallel()
	c := New(10 * time.Millisecond)

	var calls int32
	compute := func() (sample, error) {
		atomic.AddInt32(&calls, 1)
		return sample{Value: int(calls)}, nil
	}

	v, err := GetOrCompute(c, "k", compute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Value != 1 {
		t.Fatalf("value = %d, want 1", v.Value)
	}

	time.Sleep(20 * time.Millisecond)

	v2, err := GetOrCompute(c, "k", compute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v2.Value != 2 {
		t.Fatalf("value after expiry = %d, want 2 (compute should re-run)", v2.Value)
	}
	if calls != 2 {
		t.Fatalf("compute called %d times, want 2", calls)
	}
}

func TestCache_ExpiryBoundary_UsesTestClockSeam(t *testing.T) {
	t.Parallel()
	c := New(time.Second)
	now := time.Now()
	c.nowFn = func() time.Time { return now }

	Set(c, "k", sample{Value: 1})

	// One nanosecond before expiry must still be a hit.
	c.nowFn = func() time.Time { return now.Add(time.Second - time.Nanosecond) }
	if v, ok := Get[sample](c, "k"); !ok || v.Value != 1 {
		t.Fatalf("expected a hit one nanosecond before expiry, got ok=%v v=%+v", ok, v)
	}

	// Exactly at expiry (now + ttl) the entry must be treated as expired
	// (getRaw uses !Before(expiresAt), i.e. strictly-before-expiry is the
	// only "fresh" condition).
	c.nowFn = func() time.Time { return now.Add(time.Second) }
	if _, ok := Get[sample](c, "k"); ok {
		t.Fatalf("expected entry to be expired exactly at its TTL boundary")
	}
}

func TestCache_InvalidateSingleKey(t *testing.T) {
	t.Parallel()
	c := New(DefaultTTL)
	Set(c, "a", sample{Value: 1})
	Set(c, "b", sample{Value: 2})

	c.Invalidate("a")

	if _, ok := Get[sample](c, "a"); ok {
		t.Fatalf("expected key %q to be invalidated", "a")
	}
	if v, ok := Get[sample](c, "b"); !ok || v.Value != 2 {
		t.Fatalf("expected key %q to survive, got ok=%v v=%+v", "b", ok, v)
	}
}

func TestCache_InvalidateAll(t *testing.T) {
	t.Parallel()
	c := New(DefaultTTL)
	Set(c, "a", sample{Value: 1})
	Set(c, "b", sample{Value: 2})

	if c.Len() != 2 {
		t.Fatalf("Len() = %d, want 2", c.Len())
	}

	c.InvalidateAll()

	if c.Len() != 0 {
		t.Fatalf("Len() after InvalidateAll = %d, want 0", c.Len())
	}
	if _, ok := Get[sample](c, "a"); ok {
		t.Fatalf("expected key %q to be gone after InvalidateAll", "a")
	}
	if _, ok := Get[sample](c, "b"); ok {
		t.Fatalf("expected key %q to be gone after InvalidateAll", "b")
	}
}

func TestGetOrCompute_ErrorNeverCached(t *testing.T) {
	t.Parallel()
	c := New(DefaultTTL)

	sentinel := errors.New("upstream timeout")
	var calls int32
	failing := func() (sample, error) {
		n := atomic.AddInt32(&calls, 1)
		if n <= 2 {
			return sample{}, sentinel
		}
		return sample{Value: 99}, nil
	}

	if _, err := GetOrCompute(c, "k", failing); !errors.Is(err, sentinel) {
		t.Fatalf("call 1: err = %v, want sentinel", err)
	}
	if _, err := GetOrCompute(c, "k", failing); !errors.Is(err, sentinel) {
		t.Fatalf("call 2: err = %v, want sentinel", err)
	}
	v, err := GetOrCompute(c, "k", failing)
	if err != nil {
		t.Fatalf("call 3: unexpected error: %v", err)
	}
	if v.Value != 99 {
		t.Fatalf("call 3: value = %d, want 99", v.Value)
	}
	if calls != 3 {
		t.Fatalf("compute called %d times, want 3 (every failed call must retry)", calls)
	}
}

// TestCache_ConcurrentReadWrite races many goroutines reading and writing
// (and periodically invalidating) the same set of keys. Run with -race;
// the test itself only asserts "no panic / no data race", which the race
// detector enforces — there is no separate correctness assertion here
// because concurrent Set/Invalidate calls are expected to interleave
// non-deterministically.
func TestCache_ConcurrentReadWrite(t *testing.T) {
	c := New(50 * time.Millisecond)
	keys := []string{"k1", "k2", "k3"}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Writers.
	for _, k := range keys {
		k := k
		wg.Add(1)
		go func() {
			defer wg.Done()
			i := 0
			for {
				select {
				case <-stop:
					return
				default:
					Set(c, k, sample{Value: i})
					i++
				}
			}
		}()
	}

	// Readers.
	for _, k := range keys {
		k := k
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_, _ = Get[sample](c, k)
				}
			}
		}()
	}

	// Invalidators.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				c.Invalidate(keys[0])
				c.InvalidateAll()
			}
		}
	}()

	time.Sleep(100 * time.Millisecond)
	close(stop)
	wg.Wait()
}
