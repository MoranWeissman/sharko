// Package audit provides a lightweight in-memory ring buffer for recording
// significant events that originate outside the API — webhooks, init runs,
// cluster registrations, and secret reconciliations.
package audit

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// Entry is a single audit log record.
type Entry struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Source    string    `json:"source"` // "webhook", "api", "reconciler"
	Action    string    `json:"action"` // "push", "pr_merge", "secret_push", "cluster_register"
	Actor     string    `json:"actor"`  // git username or "sharko"
	Details   string    `json:"details"`
}

// Log is a thread-safe in-memory ring buffer of audit entries.
type Log struct {
	mu      sync.RWMutex
	entries []Entry
	maxSize int
}

// NewLog creates a Log that retains at most maxSize entries.
// When maxSize <= 0 it defaults to 200.
func NewLog(maxSize int) *Log {
	if maxSize <= 0 {
		maxSize = 200
	}
	return &Log{
		entries: make([]Entry, 0, maxSize),
		maxSize: maxSize,
	}
}

// Add prepends entry (newest first) and trims to maxSize.
// A new UUID and current timestamp are assigned automatically when the entry
// does not already carry them.
func (l *Log) Add(entry Entry) {
	if entry.ID == "" {
		entry.ID = uuid.NewString()
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now().UTC()
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	l.entries = append([]Entry{entry}, l.entries...)
	if len(l.entries) > l.maxSize {
		l.entries = l.entries[:l.maxSize]
	}
}

// List returns up to limit entries, newest first. If limit <= 0 all entries
// are returned.
func (l *Log) List(limit int) []Entry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	src := l.entries
	if limit > 0 && limit < len(src) {
		src = src[:limit]
	}
	out := make([]Entry, len(src))
	copy(out, src)
	return out
}
