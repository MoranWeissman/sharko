// Package operations provides an in-memory store for tracking long-running
// asynchronous operations (e.g. init, cluster registration).
//
// Each operation is represented as a Session that progresses through a list
// of named steps. Callers advance the session step-by-step, mark it as
// waiting (e.g. waiting for a PR to be merged), and eventually complete or
// fail it.
//
// The store is safe for concurrent use.
package operations

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// Status represents the lifecycle state of an operation session.
type Status string

const (
	StatusPending   Status = "pending"   // created, not yet started
	StatusRunning   Status = "running"   // actively executing steps
	StatusWaiting   Status = "waiting"   // paused, waiting for external event (e.g. PR merge)
	StatusCompleted Status = "completed" // finished successfully
	StatusFailed    Status = "failed"    // finished with error
	StatusCancelled Status = "cancelled" // explicitly cancelled by the client
)

// Step represents a single named step within an operation.
type Step struct {
	Name      string    `json:"name"`
	Status    Status    `json:"status"`
	Detail    string    `json:"detail,omitempty"` // e.g. PR URL, error message
	StartedAt time.Time `json:"started_at,omitempty"`
	DoneAt    time.Time `json:"done_at,omitempty"`
}

// Session is a single operation's runtime state.
type Session struct {
	ID          string    `json:"id"`
	Type        string    `json:"type"` // e.g. "init"
	Status      Status    `json:"status"`
	Steps       []Step    `json:"steps"`
	CurrentStep int       `json:"current_step"` // index into Steps
	WaitDetail  string    `json:"wait_detail,omitempty"`  // human-readable context when waiting
	WaitPayload string    `json:"wait_payload,omitempty"` // machine-readable payload (e.g. PR URL)
	Result      string    `json:"result,omitempty"`       // final result message on completion
	Error       string    `json:"error,omitempty"`        // error message on failure
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	HeartbeatAt time.Time `json:"heartbeat_at"` // last client heartbeat
}

// IsAlive returns true if the client sent a heartbeat within the given window.
// A session that has never received a heartbeat is considered alive for the
// first staleAfter period (grace period after creation).
func (s *Session) IsAlive(staleAfter time.Duration) bool {
	if s.HeartbeatAt.IsZero() {
		return time.Since(s.CreatedAt) < staleAfter
	}
	return time.Since(s.HeartbeatAt) < staleAfter
}

// Store is a thread-safe in-memory store for operation sessions.
type Store struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

// NewStore creates an empty Store.
func NewStore() *Store {
	return &Store{sessions: make(map[string]*Session)}
}

// Create initialises a new session with the given operation type and step names.
// The session starts in StatusPending; the caller should transition it to
// StatusRunning when background work begins.
func (st *Store) Create(opType string, steps []string) *Session {
	id := uuid.NewString()
	now := time.Now()

	s := &Session{
		ID:        id,
		Type:      opType,
		Status:    StatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	for _, name := range steps {
		s.Steps = append(s.Steps, Step{Name: name, Status: StatusPending})
	}

	st.mu.Lock()
	st.sessions[id] = s
	st.mu.Unlock()

	return s
}

// Get returns the session with the given ID (nil if not found).
func (st *Store) Get(id string) (*Session, bool) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	s, ok := st.sessions[id]
	if !ok {
		return nil, false
	}
	// Return a shallow copy to avoid data races on read.
	copy := *s
	return &copy, true
}

// FindByTypeAndStatus returns all sessions of the given type with the given status.
func (st *Store) FindByTypeAndStatus(opType string, status Status) []*Session {
	st.mu.RLock()
	defer st.mu.RUnlock()
	var out []*Session
	for _, s := range st.sessions {
		if s.Type == opType && s.Status == status {
			cp := *s
			out = append(out, &cp)
		}
	}
	return out
}

// Heartbeat records a client heartbeat for the given session.
func (st *Store) Heartbeat(id string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	s, ok := st.sessions[id]
	if !ok {
		return false
	}
	s.HeartbeatAt = time.Now()
	s.UpdatedAt = time.Now()
	return true
}

// Start transitions the session to StatusRunning and marks the first step as running.
func (st *Store) Start(id string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	s, ok := st.sessions[id]
	if !ok {
		return
	}
	now := time.Now()
	s.Status = StatusRunning
	s.UpdatedAt = now
	if len(s.Steps) > 0 {
		s.Steps[0].Status = StatusRunning
		s.Steps[0].StartedAt = now
	}
}

// UpdateStep marks the current step as completed (with an optional detail message)
// and advances to the next step, which is immediately set to running.
func (st *Store) UpdateStep(id string, status Status, detail string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	s, ok := st.sessions[id]
	if !ok {
		return
	}
	now := time.Now()
	if s.CurrentStep < len(s.Steps) {
		s.Steps[s.CurrentStep].Status = status
		s.Steps[s.CurrentStep].Detail = detail
		s.Steps[s.CurrentStep].DoneAt = now
	}
	s.UpdatedAt = now

	// Advance to the next step if the current one succeeded.
	if status == StatusCompleted && s.CurrentStep+1 < len(s.Steps) {
		s.CurrentStep++
		s.Steps[s.CurrentStep].Status = StatusRunning
		s.Steps[s.CurrentStep].StartedAt = now
	}
}

// SetWaiting transitions the session to StatusWaiting.
// detail is a human-readable message; payload is machine-readable (e.g. PR URL).
func (st *Store) SetWaiting(id, detail, payload string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	s, ok := st.sessions[id]
	if !ok {
		return
	}
	s.Status = StatusWaiting
	s.WaitDetail = detail
	s.WaitPayload = payload
	s.UpdatedAt = time.Now()
}

// ResumeFromWaiting transitions the session back to StatusRunning.
func (st *Store) ResumeFromWaiting(id string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	s, ok := st.sessions[id]
	if !ok {
		return
	}
	s.Status = StatusRunning
	s.WaitDetail = ""
	s.WaitPayload = ""
	s.UpdatedAt = time.Now()
}

// Complete marks the session as completed with an optional result message.
func (st *Store) Complete(id, result string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	s, ok := st.sessions[id]
	if !ok {
		return
	}
	s.Status = StatusCompleted
	s.Result = result
	s.UpdatedAt = time.Now()
}

// Fail marks the session as failed with an error message.
func (st *Store) Fail(id, errMsg string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	s, ok := st.sessions[id]
	if !ok {
		return
	}
	now := time.Now()
	s.Status = StatusFailed
	s.Error = errMsg
	s.UpdatedAt = now
	// Mark the current in-progress step as failed too.
	if s.CurrentStep < len(s.Steps) && s.Steps[s.CurrentStep].Status == StatusRunning {
		s.Steps[s.CurrentStep].Status = StatusFailed
		s.Steps[s.CurrentStep].Detail = errMsg
		s.Steps[s.CurrentStep].DoneAt = now
	}
}

// Cancel marks the session as cancelled.
func (st *Store) Cancel(id string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	s, ok := st.sessions[id]
	if !ok {
		return false
	}
	s.Status = StatusCancelled
	s.UpdatedAt = time.Now()
	return true
}
