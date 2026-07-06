package notifications

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStore_InMemory_AddAndList(t *testing.T) {
	s := NewStore(5, "")

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
	s := NewStore(10, "")

	s.Add(Notification{ID: "1", Title: "Same title", Type: TypeUpgrade, Timestamp: time.Now()})
	s.Add(Notification{ID: "2", Title: "Same title", Type: TypeUpgrade, Timestamp: time.Now()})

	items := s.List()
	if len(items) != 1 {
		t.Errorf("expected 1 notification after dedup, got %d", len(items))
	}
}

func TestStore_MaxItems(t *testing.T) {
	s := NewStore(3, "")

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
	s := NewStore(10, "")

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
	s := NewStore(10, "")

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
	s := NewStore(10, "")

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

func TestStore_Persistence_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "notifications.json")

	s1 := NewStore(100, filePath)
	s1.Add(Notification{ID: "p1", Title: "Persisted", Type: TypeUpgrade, Timestamp: time.Now()})
	s1.Add(Notification{ID: "p2", Title: "Also persisted", Type: TypeDrift, Timestamp: time.Now()})

	// Verify file was written.
	if _, err := os.Stat(filePath); err != nil {
		t.Fatalf("expected persistence file to exist: %v", err)
	}

	// Create a new store pointing to the same file — it should load the data.
	s2 := NewStore(100, filePath)
	items := s2.List()
	if len(items) != 2 {
		t.Fatalf("expected 2 loaded notifications, got %d", len(items))
	}
	if items[0].ID != "p2" {
		t.Errorf("expected newest-first ordering after load, got id=%s", items[0].ID)
	}
}

func TestStore_Persistence_MarkAllReadPersists(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "notifications.json")

	s1 := NewStore(100, filePath)
	s1.Add(Notification{ID: "r1", Title: "Readable", Type: TypeUpgrade, Timestamp: time.Now()})
	s1.MarkAllRead()

	// Load into a fresh store and verify read flag is preserved.
	s2 := NewStore(100, filePath)
	items := s2.List()
	if len(items) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(items))
	}
	if !items[0].Read {
		t.Errorf("expected notification to be marked read after reload")
	}
}

func TestStore_Persistence_MissingDir_FallsBackToMemory(t *testing.T) {
	// A path whose parent directory does not exist should cause the store to
	// fall back to in-memory mode gracefully.
	filePath := "/nonexistent-dir-12345/notifications.json"
	s := NewStore(10, filePath)

	// Should work fine in memory.
	s.Add(Notification{ID: "m1", Title: "Memory only", Type: TypeDrift, Timestamp: time.Now()})
	if s.UnreadCount() != 1 {
		t.Errorf("expected 1 unread in fallback memory mode, got %d", s.UnreadCount())
	}
}

func TestStore_Persistence_LoadNonExistentFile(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "doesnotexist.json")

	// Should not error — first run scenario.
	s := NewStore(100, filePath)
	if len(s.List()) != 0 {
		t.Errorf("expected empty store on first load, got %d items", len(s.List()))
	}
}

func TestStore_Persistence_FileContentsAreValidJSON(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "notifications.json")

	s := NewStore(100, filePath)
	s.Add(Notification{
		ID:          "json1",
		Title:       "JSON test",
		Type:        TypeUpgrade,
		Description: "check the file",
		Timestamp:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}

	var items []Notification
	if err := json.Unmarshal(data, &items); err != nil {
		t.Fatalf("file is not valid JSON: %v", err)
	}
	if len(items) != 1 || items[0].ID != "json1" {
		t.Errorf("unexpected file contents: %+v", items)
	}
}

func TestStore_Resolve_RemovesReadAndUnread(t *testing.T) {
	s := NewStore(10, "")

	s.Add(Notification{ID: "1", Title: "Broken connection", Type: TypeConnection, Timestamp: time.Now()})
	s.Add(Notification{ID: "2", Title: "Other alert", Type: TypeUpgrade, Timestamp: time.Now()})
	// Mark everything read so we prove Resolve removes a READ entry too, not
	// just unread ones. (Store.Add now dedups by title regardless of read
	// state, so a same-title re-add here would just be a no-op — the unread
	// case is already covered by TestStore_Resolve_Persists, which resolves
	// a freshly-added, still-unread notification.)
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
	s := NewStore(10, "")
	s.Add(Notification{ID: "1", Title: "Keep me", Type: TypeUpgrade, Timestamp: time.Now()})

	s.Resolve("does not exist")

	if got := len(s.List()); got != 1 {
		t.Fatalf("expected Resolve of unknown title to be a no-op, got %d items", got)
	}
}

func TestStore_Resolve_Persists(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "notifications.json")

	s := NewStore(10, filePath)
	s.Add(Notification{ID: "1", Title: "Broken connection", Type: TypeConnection, Timestamp: time.Now()})
	s.Resolve("Broken connection")

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}
	var items []Notification
	if err := json.Unmarshal(data, &items); err != nil {
		t.Fatalf("file is not valid JSON: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected resolved notification to be persisted as removed, got %+v", items)
	}
}

// TestStore_ResolveThenAdd_Readds is the recovered-then-recurred path: once a
// title has been Resolved (the connection came back healthy, or whatever it
// was tracking cleared), a later Add with the same title must succeed —
// Resolve already removed the old entry, so there is nothing left to dedup
// against.
func TestStore_ResolveThenAdd_Readds(t *testing.T) {
	s := NewStore(10, "")

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
	s := NewStore(10, "")
	s.Add(Notification{ID: "1", Title: "Broken connection", Type: TypeConnection, Timestamp: time.Now()})
	s.Add(Notification{ID: "2", Title: "Broken connection", Type: TypeConnection, Timestamp: time.Now()})

	if got := len(s.List()); got != 1 {
		t.Fatalf("expected Add dedup on unread same-title, got %d items", got)
	}
}
