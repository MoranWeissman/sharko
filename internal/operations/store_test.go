package operations

import (
	"sync"
	"testing"
	"time"
)

// newTestStore creates a Store without starting the background cleanup goroutine,
// so tests are fully deterministic.
func newTestStore() *Store {
	return &Store{
		sessions: make(map[string]*Session),
	}
}

func TestCreate_GeneratesID(t *testing.T) {
	s := newTestStore()
	sess := s.Create("init", []string{"step1", "step2"})

	if sess.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if sess.Type != "init" {
		t.Errorf("expected type 'init', got %q", sess.Type)
	}
	if sess.Status != StatusPending {
		t.Errorf("expected status pending, got %q", sess.Status)
	}
	if len(sess.Steps) != 2 {
		t.Errorf("expected 2 steps, got %d", len(sess.Steps))
	}
	for i, step := range sess.Steps {
		if step.Status != StatusPending {
			t.Errorf("step %d: expected pending, got %q", i, step.Status)
		}
	}
}

func TestCreate_IDsAreUnique(t *testing.T) {
	s := newTestStore()
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		sess := s.Create("init", nil)
		if seen[sess.ID] {
			t.Fatalf("duplicate ID generated: %s", sess.ID)
		}
		seen[sess.ID] = true
	}
}

func TestGet_ReturnsSession(t *testing.T) {
	s := newTestStore()
	sess := s.Create("init", []string{"a"})

	got, ok := s.Get(sess.ID)
	if !ok {
		t.Fatal("expected to find session")
	}
	if got.ID != sess.ID {
		t.Errorf("expected ID %s, got %s", sess.ID, got.ID)
	}
}

func TestGet_MissingSession(t *testing.T) {
	s := newTestStore()
	_, ok := s.Get("nonexistent")
	if ok {
		t.Fatal("expected false for missing session")
	}
}

func TestHeartbeat_KeepsSessionAlive(t *testing.T) {
	s := newTestStore()
	sess := s.Create("init", nil)

	// Manually back-date the heartbeat
	s.mu.Lock()
	sess.LastHeartbeat = time.Now().Add(-90 * time.Second)
	s.mu.Unlock()

	// Send a heartbeat — session should now be alive with a 2-minute threshold
	if !s.Heartbeat(sess.ID) {
		t.Fatal("Heartbeat returned false")
	}

	got, _ := s.Get(sess.ID)
	if !got.IsAlive(2 * time.Minute) {
		t.Error("session should be alive after heartbeat")
	}
}

func TestHeartbeat_ReturnsFalseForMissing(t *testing.T) {
	s := newTestStore()
	if s.Heartbeat("nope") {
		t.Fatal("expected false for non-existent session")
	}
}

func TestCleanup_RemovesDeadSessions(t *testing.T) {
	s := newTestStore()
	sess := s.Create("init", nil)

	// Back-date heartbeat beyond the threshold
	s.mu.Lock()
	sess.LastHeartbeat = time.Now().Add(-3 * time.Minute)
	s.mu.Unlock()

	s.Cleanup(2 * time.Minute)

	_, ok := s.Get(sess.ID)
	if ok {
		t.Fatal("expected dead session to be removed")
	}
}

func TestCleanup_KeepsAliveSessions(t *testing.T) {
	s := newTestStore()
	sess := s.Create("init", nil)

	s.Cleanup(2 * time.Minute)

	_, ok := s.Get(sess.ID)
	if !ok {
		t.Fatal("expected live session to be kept")
	}
}

func TestUpdateStep_AdvancesCurrentStep(t *testing.T) {
	s := newTestStore()
	sess := s.Create("init", []string{"step1", "step2", "step3"})

	s.UpdateStep(sess.ID, StatusCompleted, "done step 1")

	got, _ := s.Get(sess.ID)
	if got.Steps[0].Status != StatusCompleted {
		t.Errorf("expected step0 completed, got %q", got.Steps[0].Status)
	}
	if got.Steps[0].Message != "done step 1" {
		t.Errorf("unexpected message: %q", got.Steps[0].Message)
	}
	if got.CurrentStep != 1 {
		t.Errorf("expected CurrentStep=1, got %d", got.CurrentStep)
	}
	if got.Status != StatusRunning {
		t.Errorf("expected status running, got %q", got.Status)
	}
}

func TestUpdateStep_DoesNotAdvancePastLastStep(t *testing.T) {
	s := newTestStore()
	sess := s.Create("init", []string{"only"})

	s.UpdateStep(sess.ID, StatusCompleted, "")

	got, _ := s.Get(sess.ID)
	if got.CurrentStep != 0 {
		t.Errorf("expected CurrentStep to stay at 0, got %d", got.CurrentStep)
	}
}

func TestSetWaiting_SetsStatusAndPR(t *testing.T) {
	s := newTestStore()
	sess := s.Create("init", nil)

	s.SetWaiting(sess.ID, "https://github.com/example/repo/pull/42", 42)

	got, _ := s.Get(sess.ID)
	if got.Status != StatusWaiting {
		t.Errorf("expected waiting, got %q", got.Status)
	}
	if got.PRUrl != "https://github.com/example/repo/pull/42" {
		t.Errorf("unexpected PRUrl: %q", got.PRUrl)
	}
	if got.PRID != 42 {
		t.Errorf("expected PRID=42, got %d", got.PRID)
	}
}

func TestComplete_SetsStatusAndResult(t *testing.T) {
	s := newTestStore()
	sess := s.Create("init", nil)

	result := map[string]string{"repo": "done"}
	s.Complete(sess.ID, result)

	got, _ := s.Get(sess.ID)
	if got.Status != StatusCompleted {
		t.Errorf("expected completed, got %q", got.Status)
	}
	if got.Result == nil {
		t.Error("expected non-nil result")
	}
}

func TestFail_SetsStatusAndError(t *testing.T) {
	s := newTestStore()
	sess := s.Create("init", nil)

	s.Fail(sess.ID, "something went wrong")

	got, _ := s.Get(sess.ID)
	if got.Status != StatusFailed {
		t.Errorf("expected failed, got %q", got.Status)
	}
	if got.Error != "something went wrong" {
		t.Errorf("unexpected error: %q", got.Error)
	}
}

func TestCancel_SetsStatus(t *testing.T) {
	s := newTestStore()
	sess := s.Create("init", nil)

	s.Cancel(sess.ID)

	got, _ := s.Get(sess.ID)
	if got.Status != StatusCancelled {
		t.Errorf("expected cancelled, got %q", got.Status)
	}
}

func TestFindByTypeAndStatus(t *testing.T) {
	s := newTestStore()

	a := s.Create("init", nil)
	b := s.Create("init", nil)
	c := s.Create("add-cluster", nil)

	s.SetWaiting(a.ID, "https://example.com/pr/1", 1)
	s.SetWaiting(b.ID, "https://example.com/pr/2", 2)
	// c stays pending

	results := s.FindByTypeAndStatus("init", StatusWaiting)
	if len(results) != 2 {
		t.Errorf("expected 2 waiting init sessions, got %d", len(results))
	}

	_ = c // suppress unused warning
	results2 := s.FindByTypeAndStatus("add-cluster", StatusPending)
	if len(results2) != 1 {
		t.Errorf("expected 1 pending add-cluster session, got %d", len(results2))
	}

	results3 := s.FindByTypeAndStatus("init", StatusCompleted)
	if len(results3) != 0 {
		t.Errorf("expected 0 completed init sessions, got %d", len(results3))
	}
}

func TestConcurrentAccess(t *testing.T) {
	s := newTestStore()

	const goroutines = 50
	var wg sync.WaitGroup

	// Create sessions concurrently
	ids := make([]string, goroutines)
	var mu sync.Mutex
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sess := s.Create("init", []string{"step1", "step2"})
			mu.Lock()
			ids[idx] = sess.ID
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	// Concurrently heartbeat, update, and read
	for i := 0; i < goroutines; i++ {
		wg.Add(3)
		id := ids[i]
		go func() {
			defer wg.Done()
			s.Heartbeat(id)
		}()
		go func() {
			defer wg.Done()
			s.UpdateStep(id, StatusCompleted, "ok")
		}()
		go func() {
			defer wg.Done()
			s.Get(id)
		}()
	}
	wg.Wait()

	// Concurrently complete, fail, and cancel
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		id := ids[i]
		go func(idx int) {
			defer wg.Done()
			switch idx % 3 {
			case 0:
				s.Complete(id, nil)
			case 1:
				s.Fail(id, "err")
			case 2:
				s.Cancel(id)
			}
		}(i)
	}
	wg.Wait()

	// Cleanup should not panic under concurrent access
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.Cleanup(1 * time.Millisecond) // very short threshold to exercise deletion path
	}()
	wg.Wait()
}
