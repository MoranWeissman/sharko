package notifications

import (
	"sync"
	"time"
)

// NotificationType categorises what a notification is about.
type NotificationType string

const (
	TypeUpgrade  NotificationType = "upgrade"
	TypeSecurity NotificationType = "security"
	TypeDrift    NotificationType = "drift"
)

// Notification is a single notification item.
type Notification struct {
	ID          string           `json:"id"`
	Type        NotificationType `json:"type"`
	Title       string           `json:"title"`
	Description string           `json:"description"`
	Timestamp   time.Time        `json:"timestamp"`
	Read        bool             `json:"read"`
}

// Store is a thread-safe in-memory ring buffer for notifications.
type Store struct {
	mu            sync.RWMutex
	notifications []Notification
	maxItems      int
}

// NewStore creates a Store that retains at most maxItems notifications.
func NewStore(maxItems int) *Store {
	return &Store{
		notifications: make([]Notification, 0),
		maxItems:      maxItems,
	}
}

// Add inserts a notification at the front. If an unread notification with the
// same title already exists it is silently dropped (deduplication).
func (s *Store) Add(n Notification) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Deduplicate by title — don't add if same title exists and is unread.
	for _, existing := range s.notifications {
		if existing.Title == n.Title && !existing.Read {
			return
		}
	}
	s.notifications = append([]Notification{n}, s.notifications...)
	if len(s.notifications) > s.maxItems {
		s.notifications = s.notifications[:s.maxItems]
	}
}

// List returns a snapshot of all notifications, newest first.
func (s *Store) List() []Notification {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Notification, len(s.notifications))
	copy(result, s.notifications)
	return result
}

// MarkAllRead marks every notification as read.
func (s *Store) MarkAllRead() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.notifications {
		s.notifications[i].Read = true
	}
}

// UnreadCount returns how many notifications have not yet been read.
func (s *Store) UnreadCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	count := 0
	for _, n := range s.notifications {
		if !n.Read {
			count++
		}
	}
	return count
}
