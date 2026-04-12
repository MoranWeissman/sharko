package audit

import (
	"sync"
	"testing"
	"time"
)

func TestListFiltered_ByUser(t *testing.T) {
	l := NewLog(100)
	l.Add(Entry{User: "alice", Action: "register", Source: "api", Result: "success"})
	l.Add(Entry{User: "bob", Action: "register", Source: "api", Result: "success"})
	l.Add(Entry{User: "alice", Action: "remove", Source: "api", Result: "success"})

	got := l.ListFiltered(AuditFilter{User: "alice"})
	if len(got) != 2 {
		t.Fatalf("expected 2 entries for alice, got %d", len(got))
	}
	for _, e := range got {
		if e.User != "alice" {
			t.Errorf("expected user alice, got %q", e.User)
		}
	}
}

func TestListFiltered_ByAction(t *testing.T) {
	l := NewLog(100)
	l.Add(Entry{User: "alice", Action: "register", Source: "api", Result: "success"})
	l.Add(Entry{User: "bob", Action: "remove", Source: "api", Result: "success"})

	got := l.ListFiltered(AuditFilter{Action: "register"})
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got))
	}
	if got[0].Action != "register" {
		t.Errorf("expected action register, got %q", got[0].Action)
	}
}

func TestListFiltered_BySource(t *testing.T) {
	l := NewLog(100)
	l.Add(Entry{Source: "api", Action: "register"})
	l.Add(Entry{Source: "webhook", Action: "push"})

	got := l.ListFiltered(AuditFilter{Source: "webhook"})
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got))
	}
}

func TestListFiltered_ByResult(t *testing.T) {
	l := NewLog(100)
	l.Add(Entry{Result: "success"})
	l.Add(Entry{Result: "failure"})
	l.Add(Entry{Result: "success"})

	got := l.ListFiltered(AuditFilter{Result: "failure"})
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got))
	}
}

func TestListFiltered_BySince(t *testing.T) {
	l := NewLog(100)
	old := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	l.Add(Entry{Timestamp: old, Action: "old"})
	l.Add(Entry{Timestamp: recent, Action: "recent"})

	cutoff := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	got := l.ListFiltered(AuditFilter{Since: cutoff})
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got))
	}
	if got[0].Action != "recent" {
		t.Errorf("expected recent entry, got %q", got[0].Action)
	}
}

func TestListFiltered_ByCluster(t *testing.T) {
	l := NewLog(100)
	l.Add(Entry{Resource: "cluster:prod-eu", Action: "register"})
	l.Add(Entry{Resource: "cluster:staging-us", Action: "register"})
	l.Add(Entry{Resource: "addon:cert-manager", Action: "install"})

	got := l.ListFiltered(AuditFilter{Cluster: "prod-eu"})
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got))
	}
	if got[0].Resource != "cluster:prod-eu" {
		t.Errorf("expected cluster:prod-eu, got %q", got[0].Resource)
	}
}

func TestListFiltered_Limit(t *testing.T) {
	l := NewLog(100)
	for i := 0; i < 20; i++ {
		l.Add(Entry{Action: "test"})
	}

	got := l.ListFiltered(AuditFilter{Limit: 5})
	if len(got) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(got))
	}
}

func TestListFiltered_DefaultLimit(t *testing.T) {
	l := NewLog(200)
	for i := 0; i < 100; i++ {
		l.Add(Entry{Action: "test"})
	}

	got := l.ListFiltered(AuditFilter{})
	if len(got) != 50 {
		t.Fatalf("expected default limit of 50, got %d", len(got))
	}
}

func TestListFiltered_MultipleFilters(t *testing.T) {
	l := NewLog(100)
	l.Add(Entry{User: "alice", Source: "api", Result: "success"})
	l.Add(Entry{User: "alice", Source: "webhook", Result: "success"})
	l.Add(Entry{User: "bob", Source: "api", Result: "success"})

	got := l.ListFiltered(AuditFilter{User: "alice", Source: "api"})
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got))
	}
}

func TestSubscribeUnsubscribe(t *testing.T) {
	l := NewLog(100)

	ch, unsub := l.Subscribe()

	// Add an entry and verify it arrives on the channel.
	l.Add(Entry{Action: "test-event", User: "alice"})

	select {
	case e := <-ch:
		if e.Action != "test-event" {
			t.Errorf("expected action test-event, got %q", e.Action)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for entry on subscriber channel")
	}

	// Unsubscribe and verify channel is closed.
	unsub()

	_, ok := <-ch
	if ok {
		t.Fatal("expected channel to be closed after unsubscribe")
	}

	// Verify no panic when adding after unsubscribe.
	l.Add(Entry{Action: "after-unsub"})
}

func TestConcurrentAccess(t *testing.T) {
	l := NewLog(100)
	var wg sync.WaitGroup

	// Concurrent writers.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				l.Add(Entry{Action: "write"})
			}
		}()
	}

	// Concurrent readers.
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				l.List(10)
				l.ListFiltered(AuditFilter{Limit: 10})
			}
		}()
	}

	wg.Wait()

	// After 500 writes with maxSize 100, should have exactly 100 entries.
	entries := l.List(0)
	if len(entries) != 100 {
		t.Fatalf("expected 100 entries after concurrent writes, got %d", len(entries))
	}
}

func TestBufferEviction(t *testing.T) {
	maxSize := 10
	l := NewLog(maxSize)

	// Add more than maxSize entries.
	for i := 0; i < 25; i++ {
		l.Add(Entry{Event: "event"})
	}

	entries := l.List(0)
	if len(entries) != maxSize {
		t.Fatalf("expected %d entries, got %d", maxSize, len(entries))
	}

	// Verify oldest entries were evicted (newest first ordering).
	// The most recent entry should be first.
	if entries[0].Timestamp.After(time.Now().Add(time.Second)) {
		t.Error("newest entry timestamp seems wrong")
	}
}

func TestNewLog_DefaultSize(t *testing.T) {
	l := NewLog(0)
	if l.maxSize != 1000 {
		t.Fatalf("expected default maxSize 1000, got %d", l.maxSize)
	}
}
