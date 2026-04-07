package operations

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

const (
	// cleanupInterval is how often the background goroutine scans for dead sessions.
	cleanupInterval = 1 * time.Minute
	// defaultHeartbeatTimeout is used by the background cleanup goroutine.
	defaultHeartbeatTimeout = 2 * time.Minute
)

// Store is an in-memory session store. It is safe for concurrent use.
type Store struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

// NewStore creates a new Store and starts a background cleanup goroutine that
// removes sessions with no heartbeat for 2 minutes.
func NewStore() *Store {
	s := &Store{
		sessions: make(map[string]*Session),
	}
	go s.runCleanup()
	return s
}

// runCleanup periodically removes dead sessions.
func (s *Store) runCleanup() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for range ticker.C {
		s.Cleanup(defaultHeartbeatTimeout)
	}
}

// generateID returns a cryptographically random hex ID.
func generateID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback: use timestamp-based ID (should not happen in practice)
		return hex.EncodeToString([]byte(time.Now().String()))
	}
	return hex.EncodeToString(b)
}

// Create generates a new session with a random ID and the given step names,
// all initialised to StatusPending.
func (s *Store) Create(opType string, steps []string) *Session {
	now := time.Now()
	sess := &Session{
		ID:            generateID(),
		Type:          opType,
		Status:        StatusPending,
		Steps:         make([]Step, len(steps)),
		CurrentStep:   0,
		CreatedAt:     now,
		LastHeartbeat: now,
	}
	for i, name := range steps {
		sess.Steps[i] = Step{Name: name, Status: StatusPending}
	}

	s.mu.Lock()
	s.sessions[sess.ID] = sess
	s.mu.Unlock()

	return sess
}

// Get returns a session by ID. Returns nil, false if not found.
func (s *Store) Get(id string) (*Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[id]
	return sess, ok
}

// Heartbeat updates the last heartbeat time. Returns false if the session is not found.
func (s *Store) Heartbeat(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return false
	}
	sess.LastHeartbeat = time.Now()
	return true
}

// UpdateStep marks the current step with the given status and message, then
// advances CurrentStep to the next index. If already at the last step the
// index is not advanced beyond the slice bounds.
func (s *Store) UpdateStep(id string, status Status, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return
	}
	if sess.CurrentStep < len(sess.Steps) {
		sess.Steps[sess.CurrentStep].Status = status
		sess.Steps[sess.CurrentStep].Message = message
		if sess.CurrentStep < len(sess.Steps)-1 {
			sess.CurrentStep++
		}
	}
	sess.Status = StatusRunning
}

// SetWaiting puts the session in waiting state, recording the PR URL and ID.
func (s *Store) SetWaiting(id string, prURL string, prID int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return
	}
	sess.Status = StatusWaiting
	sess.PRUrl = prURL
	sess.PRID = prID
}

// Complete marks the session as completed and stores the result.
func (s *Store) Complete(id string, result any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return
	}
	sess.Status = StatusCompleted
	sess.Result = result
}

// Fail marks the session as failed with an error message.
func (s *Store) Fail(id string, err string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return
	}
	sess.Status = StatusFailed
	sess.Error = err
}

// Cancel marks the session as cancelled.
func (s *Store) Cancel(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return
	}
	sess.Status = StatusCancelled
}

// Cleanup removes sessions that have not received a heartbeat within the
// given threshold. It should be called periodically.
func (s *Store) Cleanup(threshold time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, sess := range s.sessions {
		if !sess.IsAlive(threshold) {
			delete(s.sessions, id)
		}
	}
}

// FindByTypeAndStatus returns all sessions matching the given type and status.
// The returned slice is a snapshot — callers must not mutate the Session values.
func (s *Store) FindByTypeAndStatus(opType string, status Status) []*Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*Session
	for _, sess := range s.sessions {
		if sess.Type == opType && sess.Status == status {
			result = append(result, sess)
		}
	}
	return result
}
