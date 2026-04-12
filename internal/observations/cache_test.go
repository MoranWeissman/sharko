package observations

import (
	"context"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/verify"
	"k8s.io/client-go/kubernetes/fake"
)

func TestCacheHit(t *testing.T) {
	client := fake.NewSimpleClientset()
	store := NewStore(client, "default")
	ctx := context.Background()

	// Record an observation.
	if err := store.RecordTestResult(ctx, "prod", verify.Result{Success: true, Stage: "stage1"}); err != nil {
		t.Fatalf("RecordTestResult: %v", err)
	}

	provider := newCachedStatusProviderWithTTL(store, 1*time.Minute)

	// First call populates the cache.
	r1, err := provider.GetStatus(ctx, "prod", false, nil)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if r1.Status != StatusConnected {
		t.Fatalf("Status = %q, want %q", r1.Status, StatusConnected)
	}

	// Record a different result to the store.
	if err := store.RecordTestResult(ctx, "prod", verify.Result{Success: false, Stage: "stage1", ErrorCode: verify.ERR_NETWORK}); err != nil {
		t.Fatalf("RecordTestResult: %v", err)
	}

	// Second call should return cached result (still Connected, not Unreachable).
	r2, err := provider.GetStatus(ctx, "prod", false, nil)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if r2.Status != StatusConnected {
		t.Errorf("Status = %q, want %q (should be cached)", r2.Status, StatusConnected)
	}
}

func TestCacheMiss(t *testing.T) {
	client := fake.NewSimpleClientset()
	store := NewStore(client, "default")
	ctx := context.Background()

	provider := newCachedStatusProviderWithTTL(store, 1*time.Minute)

	// No observation recorded — should get Unknown.
	r, err := provider.GetStatus(ctx, "nonexistent", false, nil)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if r.Status != StatusUnknown {
		t.Errorf("Status = %q, want %q", r.Status, StatusUnknown)
	}
}

func TestCacheRefreshBypass(t *testing.T) {
	client := fake.NewSimpleClientset()
	store := NewStore(client, "default")
	ctx := context.Background()

	if err := store.RecordTestResult(ctx, "prod", verify.Result{Success: true, Stage: "stage1"}); err != nil {
		t.Fatalf("RecordTestResult: %v", err)
	}

	provider := newCachedStatusProviderWithTTL(store, 1*time.Minute)

	// Populate cache.
	_, err := provider.GetStatus(ctx, "prod", false, nil)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}

	// Update the underlying store.
	if err := store.RecordTestResult(ctx, "prod", verify.Result{Success: false, Stage: "stage1", ErrorCode: verify.ERR_NETWORK}); err != nil {
		t.Fatalf("RecordTestResult: %v", err)
	}

	// With refresh=true, should bypass cache and return the new status.
	r, err := provider.GetStatus(ctx, "prod", true, nil)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if r.Status != StatusUnreachable {
		t.Errorf("Status = %q, want %q (refresh should bypass cache)", r.Status, StatusUnreachable)
	}
}

func TestCacheTTLExpiry(t *testing.T) {
	client := fake.NewSimpleClientset()
	store := NewStore(client, "default")
	ctx := context.Background()

	if err := store.RecordTestResult(ctx, "prod", verify.Result{Success: true, Stage: "stage1"}); err != nil {
		t.Fatalf("RecordTestResult: %v", err)
	}

	// Use a very short TTL.
	provider := newCachedStatusProviderWithTTL(store, 1*time.Millisecond)

	// Populate cache.
	r1, err := provider.GetStatus(ctx, "prod", false, nil)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if r1.Status != StatusConnected {
		t.Fatalf("Status = %q, want %q", r1.Status, StatusConnected)
	}

	// Wait for TTL to expire.
	time.Sleep(5 * time.Millisecond)

	// Update the underlying store.
	if err := store.RecordTestResult(ctx, "prod", verify.Result{Success: true, Stage: "stage2"}); err != nil {
		t.Fatalf("RecordTestResult: %v", err)
	}

	// Should recompute after TTL expiry.
	r2, err := provider.GetStatus(ctx, "prod", false, nil)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if r2.Status != StatusVerified {
		t.Errorf("Status = %q, want %q (TTL should have expired)", r2.Status, StatusVerified)
	}
}

func TestCacheWithHealthyAddonFn(t *testing.T) {
	client := fake.NewSimpleClientset()
	store := NewStore(client, "default")
	ctx := context.Background()

	if err := store.RecordTestResult(ctx, "prod", verify.Result{Success: true, Stage: "stage1"}); err != nil {
		t.Fatalf("RecordTestResult: %v", err)
	}

	provider := newCachedStatusProviderWithTTL(store, 1*time.Minute)

	addonFn := func(name string) bool { return true }
	r, err := provider.GetStatus(ctx, "prod", false, addonFn)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if r.Status != StatusOperational {
		t.Errorf("Status = %q, want %q", r.Status, StatusOperational)
	}
}
