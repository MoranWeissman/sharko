// Package audit provides a lightweight in-memory ring buffer for recording
// significant events that originate outside the API — webhooks, init runs,
// cluster registrations, and secret reconciliations.
package audit

import (
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Entry is a single audit log record.
type Entry struct {
	ID         string    `json:"id"`
	Timestamp  time.Time `json:"timestamp"`
	Level      string    `json:"level"`                // info, warn, error
	Event      string    `json:"event"`                // cluster_registered, pr_created, etc.
	User       string    `json:"user"`                 // username or "system"
	Action     string    `json:"action"`               // register, remove, update, test
	Resource   string    `json:"resource"`             // cluster:prod-eu, addon:cert-manager
	Source     string    `json:"source"`               // ui, cli, api, reconciler, webhook
	Result     string    `json:"result"`               // success, failure, partial
	DurationMs int64     `json:"duration_ms"`
	Error      string    `json:"error,omitempty"`
	RequestID  string    `json:"request_id,omitempty"`
}

// AuditFilter holds optional filter criteria for ListFiltered.
type AuditFilter struct {
	User    string
	Action  string
	Source  string
	Result  string
	Since   time.Time
	Cluster string // matches "cluster:NAME" in Resource field
	Limit   int    // default 50
}

// Log is a thread-safe in-memory ring buffer of audit entries.
type Log struct {
	mu          sync.RWMutex
	entries     []Entry
	maxSize     int
	subscribers []chan Entry
}

// NewLog creates a Log that retains at most maxSize entries.
// When maxSize <= 0 it defaults to 1000.
func NewLog(maxSize int) *Log {
	if maxSize <= 0 {
		maxSize = 1000
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

	// Fan out to SSE subscribers (non-blocking).
	for _, ch := range l.subscribers {
		select {
		case ch <- entry:
		default: // drop if subscriber is slow
		}
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

// ListFiltered returns entries matching the given filter criteria, newest first.
func (l *Log) ListFiltered(filter AuditFilter) []Entry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}

	var out []Entry
	for _, e := range l.entries {
		if filter.User != "" && !strings.EqualFold(e.User, filter.User) {
			continue
		}
		if filter.Action != "" && !strings.EqualFold(e.Action, filter.Action) {
			continue
		}
		if filter.Source != "" && !strings.EqualFold(e.Source, filter.Source) {
			continue
		}
		if filter.Result != "" && !strings.EqualFold(e.Result, filter.Result) {
			continue
		}
		if !filter.Since.IsZero() && e.Timestamp.Before(filter.Since) {
			continue
		}
		if filter.Cluster != "" && !strings.Contains(e.Resource, "cluster:"+filter.Cluster) {
			continue
		}
		out = append(out, e)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// Subscribe returns a read-only channel that receives every new audit entry,
// and an unsubscribe function that removes the subscriber and closes the channel.
func (l *Log) Subscribe() (<-chan Entry, func()) {
	ch := make(chan Entry, 64) // buffered to avoid blocking Add()
	l.mu.Lock()
	l.subscribers = append(l.subscribers, ch)
	l.mu.Unlock()
	unsub := func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		for i, s := range l.subscribers {
			if s == ch {
				l.subscribers = append(l.subscribers[:i], l.subscribers[i+1:]...)
				close(ch)
				break
			}
		}
	}
	return ch, unsub
}
