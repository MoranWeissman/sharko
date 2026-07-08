package notifications

import (
	"context"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/cmstore"
	"k8s.io/client-go/kubernetes/fake"
)

// newTestCMStore builds a cmstore.Store backed by a fake clientset, mirroring
// internal/prtracker's newTestTracker helper.
func newTestCMStore() *cmstore.Store {
	client := fake.NewSimpleClientset()
	return cmstore.NewStore(client, "default", "sharko-notifications")
}

func TestStore_InMemory_AddAndList(t *testing.T) {
	s := NewStore(5, nil)

	s.Add(Notification{ID: "1", Title: "First", Type: TypeUpgrade, Timestamp: time.Now()})
	s.Add(Notification{ID: "2", Title: "Second", Type: TypeDrift, Timestamp: time.Now()})

	items := s.List()
	if len(items) != 2 {
		t.Fatalf("expected 2 notifications, got %d", len(items))
	}
	// Newest first.
	if items[0].ID != "2" {
		t.Errorf("expected newest first, got %s", items[0].ID)
	}
}

func TestStore_Deduplication(t *testing.T) {
	s := NewStore(10, nil)

	s.Add(Notification{ID: "1", Title: "Same title", Type: TypeUpgrade, Timestamp: time.Now()})
	s.Add(Notification{ID: "2", Title: "Same title", Type: TypeUpgrade, Timestamp: time.Now()})

	items := s.List()
	if len(items) != 1 {
		t.Errorf("expected 1 notification after dedup, got %d", len(items))
	}
}

func TestStore_MaxItems(t *testing.T) {
	s := NewStore(3, nil)

	for i := 0; i < 5; i++ {
		s.Add(Notification{
			ID:    string(rune('a' + i)),
			Title: string(rune('a'+i)) + " title",
			Type:  TypeUpgrade,
		})
	}

	items := s.List()
	if len(items) != 3 {
		t.Errorf("expected 3 items (maxItems), got %d", len(items))
	}
}

func TestStore_MarkAllRead(t *testing.T) {
	s := NewStore(10, nil)

	s.Add(Notification{ID: "1", Title: "A", Type: TypeUpgrade, Timestamp: time.Now()})
	s.Add(Notification{ID: "2", Title: "B", Type: TypeDrift, Timestamp: time.Now()})

	if s.UnreadCount() != 2 {
		t.Fatalf("expected 2 unread, got %d", s.UnreadCount())
	}

	s.MarkAllRead()

	if s.UnreadCount() != 0 {
		t.Errorf("expected 0 unread after MarkAllRead, got %d", s.UnreadCount())
	}
}

func TestStore_DedupBlocksReAddAfterRead(t *testing.T) {
	s := NewStore(10, nil)

	s.Add(Notification{ID: "1", Title: "Same title", Type: TypeUpgrade, Timestamp: time.Now()})
	s.MarkAllRead()
	// Marking read is an acknowledgement, not permission to re-nag: the same
	// title must still be deduplicated after the existing entry is read.
	s.Add(Notification{ID: "2", Title: "Same title", Type: TypeUpgrade, Timestamp: time.Now()})

	items := s.List()
	if len(items) != 1 {
		t.Errorf("expected 1 notification (dedup by title regardless of read state), got %d", len(items))
	}
}

// TestStore_ClearedNotificationDoesNotReappear is the regression test for the
// "newer version available" nag: a checker tick re-adds the identical title
// every 30 minutes as long as the condition holds. Before this fix, marking
// the notification read let the next tick re-add it as fresh and unread,
// nagging the user forever until they upgraded. Add → MarkAllRead → Add
// (same title) must leave exactly one entry, and it must still be read.
func TestStore_ClearedNotificationDoesNotReappear(t *testing.T) {
	s := NewStore(10, nil)

	s.Add(Notification{ID: "1", Title: "podinfo 6.9.3 available", Type: TypeUpgrade, Timestamp: time.Now()})
	s.MarkAllRead()
	s.Add(Notification{ID: "2", Title: "podinfo 6.9.3 available", Type: TypeUpgrade, Timestamp: time.Now()})

	items := s.List()
	if len(items) != 1 {
		t.Fatalf("expected exactly 1 notification with title %q, got %d: %+v", "podinfo 6.9.3 available", len(items), items)
	}
	if !items[0].Read {
		t.Errorf("expected the surviving notification to remain Read == true, got Read == false")
	}
}

func TestStore_Resolve_RemovesReadAndUnread(t *testing.T) {
	s := NewStore(10, nil)

	s.Add(Notification{ID: "1", Title: "Broken connection", Type: TypeConnection, Timestamp: time.Now()})
	s.Add(Notification{ID: "2", Title: "Other alert", Type: TypeUpgrade, Timestamp: time.Now()})
	// Mark everything read so we prove Resolve removes a READ entry too, not
	// just unread ones. (Store.Add now dedups by title regardless of read
	// state, so a same-title re-add here would just be a no-op — the unread
	// case is already covered by TestStore_CMStore_ResolvePersists, which
	// resolves a freshly-added, still-unread notification.)
	s.MarkAllRead()

	s.Resolve("Broken connection")

	items := s.List()
	if len(items) != 1 {
		t.Fatalf("expected 1 notification left after resolve, got %d: %+v", len(items), items)
	}
	if items[0].Title != "Other alert" {
		t.Errorf("Resolve removed the wrong notification; left %q", items[0].Title)
	}
}

func TestStore_Resolve_UnknownTitleIsNoOp(t *testing.T) {
	s := NewStore(10, nil)
	s.Add(Notification{ID: "1", Title: "Keep me", Type: TypeUpgrade, Timestamp: time.Now()})

	s.Resolve("does not exist")

	if got := len(s.List()); got != 1 {
		t.Fatalf("expected Resolve of unknown title to be a no-op, got %d items", got)
	}
}

// TestStore_ResolveThenAdd_Readds is the recovered-then-recurred path: once a
// title has been Resolved (the connection came back healthy, or whatever it
// was tracking cleared), a later Add with the same title must succeed —
// Resolve already removed the old entry, so there is nothing left to dedup
// against.
func TestStore_ResolveThenAdd_Readds(t *testing.T) {
	s := NewStore(10, nil)

	s.Add(Notification{ID: "1", Title: "Broken connection", Type: TypeConnection, Timestamp: time.Now()})
	s.Resolve("Broken connection")
	s.Add(Notification{ID: "2", Title: "Broken connection", Type: TypeConnection, Timestamp: time.Now()})

	items := s.List()
	if len(items) != 1 {
		t.Fatalf("expected 1 notification after resolve+re-add, got %d: %+v", len(items), items)
	}
	if items[0].ID != "2" {
		t.Errorf("expected the re-added notification (ID 2) to be present, got ID %q", items[0].ID)
	}
	if items[0].Read {
		t.Errorf("expected the re-added notification to be unread, got Read == true")
	}
}

func TestStore_Add_DedupStillWorks(t *testing.T) {
	s := NewStore(10, nil)
	s.Add(Notification{ID: "1", Title: "Broken connection", Type: TypeConnection, Timestamp: time.Now()})
	s.Add(Notification{ID: "2", Title: "Broken connection", Type: TypeConnection, Timestamp: time.Now()})

	if got := len(s.List()); got != 1 {
		t.Fatalf("expected Add dedup on unread same-title, got %d items", got)
	}
}

// ---------------------------------------------------------------------------
// ConfigMap-backed persistence (cmstore), fake clientset
// ---------------------------------------------------------------------------

func TestStore_CMStore_AddPersists(t *testing.T) {
	cm := newTestCMStore()
	s := NewStore(100, cm)

	s.Add(Notification{ID: "p1", Title: "Persisted", Type: TypeUpgrade, Timestamp: time.Now()})
	s.Add(Notification{ID: "p2", Title: "Also persisted", Type: TypeDrift, Timestamp: time.Now()})

	items := s.List()
	if len(items) != 2 {
		t.Fatalf("expected 2 notifications, got %d", len(items))
	}

	data, err := cm.Read(context.Background())
	if err != nil {
		t.Fatalf("reading configmap state: %v", err)
	}
	stored := extractNotifications(data)
	if len(stored) != 2 {
		t.Fatalf("expected 2 notifications stored in configmap, got %d: %+v", len(stored), stored)
	}
}

// TestStore_CMStore_RestartPersistence is the core V2-cleanup-82.1
// regression test: a new Store built over the SAME ConfigMap (simulating a
// pod restart against the same backing store) must see the prior
// notifications, including their read/cleared flags — not just an empty
// slice, and not the disk-file behavior this replaces.
func TestStore_CMStore_RestartPersistence(t *testing.T) {
	cm := newTestCMStore()

	s1 := NewStore(100, cm)
	s1.Add(Notification{ID: "r1", Title: "Readable", Type: TypeUpgrade, Timestamp: time.Now()})
	s1.Add(Notification{ID: "r2", Title: "Unread one", Type: TypeDrift, Timestamp: time.Now()})
	s1.MarkAllRead()

	// Simulate a pod restart: build a fresh Store over the same ConfigMap.
	s2 := NewStore(100, cm)
	items := s2.List()
	if len(items) != 2 {
		t.Fatalf("expected 2 notifications to survive restart, got %d: %+v", len(items), items)
	}
	for _, n := range items {
		if !n.Read {
			t.Errorf("expected notification %q to remain Read == true after restart, got false", n.Title)
		}
	}
}

// TestStore_CMStore_ResolvePersists confirms Resolve's removal is durable
// across a restart, not just an in-memory effect.
func TestStore_CMStore_ResolvePersists(t *testing.T) {
	cm := newTestCMStore()

	s1 := NewStore(10, cm)
	s1.Add(Notification{ID: "1", Title: "Broken connection", Type: TypeConnection, Timestamp: time.Now()})
	s1.Resolve("Broken connection")

	s2 := NewStore(10, cm)
	if got := len(s2.List()); got != 0 {
		t.Errorf("expected resolved notification to stay gone after restart, got %d items", got)
	}
}

// TestStore_CMStore_DedupSurvivesRestart is #477's dedup guarantee carried
// through a restart: a read entry loaded from the ConfigMap still blocks a
// same-title re-add.
func TestStore_CMStore_DedupSurvivesRestart(t *testing.T) {
	cm := newTestCMStore()

	s1 := NewStore(10, cm)
	s1.Add(Notification{ID: "1", Title: "podinfo 6.9.3 available", Type: TypeUpgrade, Timestamp: time.Now()})
	s1.MarkAllRead()

	s2 := NewStore(10, cm)
	s2.Add(Notification{ID: "2", Title: "podinfo 6.9.3 available", Type: TypeUpgrade, Timestamp: time.Now()})

	items := s2.List()
	if len(items) != 1 {
		t.Fatalf("expected dedup to survive restart, got %d items: %+v", len(items), items)
	}
	if !items[0].Read {
		t.Errorf("expected the surviving notification to remain Read == true after restart, got false")
	}
}

// TestStore_AttachCMStore_MergesInMemoryState covers the serve.go startup
// seam: the store is constructed in-memory only (client not available yet),
// something gets added, and then AttachCMStore wires the ConfigMap in. The
// in-memory addition must survive the attach and be persisted going forward.
func TestStore_AttachCMStore_MergesInMemoryState(t *testing.T) {
	s := NewStore(10, nil)
	s.Add(Notification{ID: "early", Title: "Added before attach", Type: TypeUpgrade, Timestamp: time.Now()})

	cm := newTestCMStore()
	if err := s.AttachCMStore(context.Background(), cm); err != nil {
		t.Fatalf("AttachCMStore: %v", err)
	}

	items := s.List()
	if len(items) != 1 || items[0].Title != "Added before attach" {
		t.Fatalf("expected the pre-attach notification to survive, got %+v", items)
	}

	// Further mutations must now persist through the ConfigMap.
	s.Add(Notification{ID: "later", Title: "Added after attach", Type: TypeDrift, Timestamp: time.Now()})
	data, err := cm.Read(context.Background())
	if err != nil {
		t.Fatalf("reading configmap state: %v", err)
	}
	stored := extractNotifications(data)
	if len(stored) != 2 {
		t.Fatalf("expected 2 notifications persisted after attach, got %d: %+v", len(stored), stored)
	}
}

func TestStore_AttachCMStore_NilIsNoOp(t *testing.T) {
	s := NewStore(10, nil)
	s.Add(Notification{ID: "1", Title: "Solo", Type: TypeUpgrade, Timestamp: time.Now()})

	if err := s.AttachCMStore(context.Background(), nil); err != nil {
		t.Fatalf("AttachCMStore(nil) should be a no-op, got error: %v", err)
	}
	if len(s.List()) != 1 {
		t.Fatalf("expected store to be unaffected by a nil AttachCMStore")
	}
}

// TestStore_Persistence_MissingClient_FallsBackToMemory mirrors the old
// missing-PVC-directory fallback test: a nil cmStore (no in-cluster k8s
// client — local/dev, or a unit test that never sets one up) must still work
// as a fully-functional in-memory store, with no error and no panic.
func TestStore_Persistence_MissingClient_FallsBackToMemory(t *testing.T) {
	s := NewStore(10, nil)

	s.Add(Notification{ID: "m1", Title: "Memory only", Type: TypeDrift, Timestamp: time.Now()})
	if s.UnreadCount() != 1 {
		t.Errorf("expected 1 unread in fallback memory mode, got %d", s.UnreadCount())
	}
}
