package changelog

import (
	"context"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/cmstore"
	"k8s.io/client-go/kubernetes/fake"
)

// newTestCMStore builds a cmstore.Store backed by a fake clientset, mirroring
// internal/notifications and internal/prtracker's test helpers.
func newTestCMStore() *cmstore.Store {
	client := fake.NewSimpleClientset()
	return cmstore.NewStore(client, "default", "sharko-cluster-changes")
}

func TestStore_InMemory_RecordAndList(t *testing.T) {
	s := NewStore(100, nil)

	s.Record(Entry{Cluster: "prod", Addon: "cert-manager", PRID: 1, Status: StatusMerged, CompletedAt: time.Now()})
	s.Record(Entry{Cluster: "prod", Addon: "external-dns", PRID: 2, Status: StatusMerged, CompletedAt: time.Now()})

	entries := s.List("prod")
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	// Newest first.
	if entries[0].PRID != 2 {
		t.Errorf("expected newest (PRID 2) first, got %d", entries[0].PRID)
	}
	if entries[1].PRID != 1 {
		t.Errorf("expected oldest (PRID 1) second, got %d", entries[1].PRID)
	}
}

func TestStore_RollingCapEviction(t *testing.T) {
	s := NewStore(100, nil)

	for i := 1; i <= 105; i++ {
		s.Record(Entry{Cluster: "prod", PRID: i, Status: StatusMerged, CompletedAt: time.Now()})
	}

	entries := s.List("prod")
	if len(entries) != 100 {
		t.Fatalf("expected 100 entries (cap), got %d", len(entries))
	}
	// Newest first: PRID 105 recorded last, should be at index 0.
	if entries[0].PRID != 105 {
		t.Errorf("expected newest entry (PRID 105) first, got %d", entries[0].PRID)
	}
	// Oldest surviving entry should be PRID 6 (1-5 evicted).
	if entries[len(entries)-1].PRID != 6 {
		t.Errorf("expected oldest surviving entry (PRID 6) last, got %d", entries[len(entries)-1].PRID)
	}
}

func TestStore_PerClusterIsolation(t *testing.T) {
	s := NewStore(3, nil)

	// Fill cluster A past its cap.
	for i := 1; i <= 5; i++ {
		s.Record(Entry{Cluster: "cluster-a", PRID: i, Status: StatusMerged, CompletedAt: time.Now()})
	}
	// Cluster B gets a single entry.
	s.Record(Entry{Cluster: "cluster-b", PRID: 100, Status: StatusMerged, CompletedAt: time.Now()})

	entriesA := s.List("cluster-a")
	if len(entriesA) != 3 {
		t.Fatalf("expected cluster-a capped at 3, got %d", len(entriesA))
	}

	entriesB := s.List("cluster-b")
	if len(entriesB) != 1 {
		t.Fatalf("expected cluster-b to have 1 entry (unaffected by cluster-a's churn), got %d", len(entriesB))
	}
	if entriesB[0].PRID != 100 {
		t.Errorf("expected cluster-b entry PRID 100, got %d", entriesB[0].PRID)
	}
}

func TestStore_UnknownCluster_ReturnsEmpty(t *testing.T) {
	s := NewStore(100, nil)
	entries := s.List("does-not-exist")
	if entries == nil {
		t.Error("expected non-nil empty slice for unknown cluster")
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestStore_BlankCluster_IsNoOp(t *testing.T) {
	s := NewStore(100, nil)
	s.Record(Entry{Cluster: "", PRID: 1, Status: StatusMerged})
	if len(s.List("")) != 0 {
		t.Error("expected blank-cluster Record to be a no-op")
	}
}

func TestStore_ConfigMapPersistence(t *testing.T) {
	cmStore := newTestCMStore()
	s := NewStore(100, cmStore)
	ctx := context.Background()

	s.Record(Entry{Cluster: "prod", Addon: "cert-manager", PRID: 1, PRUrl: "https://example.invalid/pr/1", Status: StatusMerged, CompletedAt: time.Now()})

	// Simulate a pod restart: build a fresh Store from the same ConfigMap.
	restarted := NewStore(100, cmStore)
	entries := restarted.List("prod")
	if len(entries) != 1 {
		t.Fatalf("expected 1 persisted entry after restart, got %d", len(entries))
	}
	if entries[0].PRID != 1 || entries[0].Addon != "cert-manager" {
		t.Errorf("persisted entry mismatch: %+v", entries[0])
	}

	_ = ctx
}

func TestStore_AttachCMStore_MergesInMemoryAndPersisted(t *testing.T) {
	cmStore := newTestCMStore()
	ctx := context.Background()

	// Pre-populate the ConfigMap directly (simulating a prior process).
	seed := NewStore(100, cmStore)
	seed.Record(Entry{Cluster: "prod", PRID: 1, Status: StatusMerged, CompletedAt: time.Now()})

	// A fresh in-memory-only store accumulates one entry before attach.
	s := NewStore(100, nil)
	s.Record(Entry{Cluster: "prod", PRID: 2, Status: StatusMerged, CompletedAt: time.Now()})

	if err := s.AttachCMStore(ctx, cmStore); err != nil {
		t.Fatalf("AttachCMStore: %v", err)
	}

	entries := s.List("prod")
	if len(entries) != 2 {
		t.Fatalf("expected 2 merged entries, got %d", len(entries))
	}
}

func TestStore_AttachCMStore_NilIsNoOp(t *testing.T) {
	s := NewStore(100, nil)
	if err := s.AttachCMStore(context.Background(), nil); err != nil {
		t.Fatalf("AttachCMStore(nil): %v", err)
	}
}

func TestPrettyOperation(t *testing.T) {
	got := PrettyOperation("addon-enable")
	want := "addon enable"
	if got != want {
		t.Errorf("PrettyOperation(%q) = %q, want %q", "addon-enable", got, want)
	}
}
