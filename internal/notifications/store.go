package notifications

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
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
// When filePath is non-empty and the parent directory exists, notifications
// are persisted to disk so they survive pod restarts.
type Store struct {
	mu            sync.RWMutex
	notifications []Notification
	maxItems      int
	filePath      string // empty = in-memory only
}

// DefaultNotificationsPath is the file used when persistence is enabled.
// The Sharko PVC mounts at /data.
const DefaultNotificationsPath = "/data/notifications.json"

// NewStore creates a Store that retains at most maxItems notifications.
// filePath controls persistence: pass an empty string for in-memory only,
// or a path (e.g. defaultNotificationsPath) to enable disk persistence.
// If the parent directory of filePath does not exist (no PVC mounted),
// the store silently falls back to in-memory mode.
func NewStore(maxItems int, filePath string) *Store {
	if filePath != "" {
		dir := filepath.Dir(filePath)
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			log.Printf("[notifications] store: persistence directory %q not available, running in-memory only", dir)
			filePath = ""
		}
	}

	s := &Store{
		notifications: make([]Notification, 0),
		maxItems:      maxItems,
		filePath:      filePath,
	}

	if err := s.Load(); err != nil {
		log.Printf("[notifications] store: could not load persisted notifications: %v", err)
	}

	return s
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
	if err := s.saveLocked(); err != nil {
		log.Printf("[notifications] store: could not persist after Add: %v", err)
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
	if err := s.saveLocked(); err != nil {
		log.Printf("[notifications] store: could not persist after MarkAllRead: %v", err)
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

// Save persists the current notifications to disk. It is a no-op when
// filePath is empty. Callers that hold the write lock should use saveLocked.
func (s *Store) Save() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.saveLocked()
}

// saveLocked writes notifications to disk. Must be called with at least a
// read lock held (or a write lock — both are fine because JSON marshal does
// not mutate state). We accept a write lock here from Add/MarkAllRead.
func (s *Store) saveLocked() error {
	if s.filePath == "" {
		return nil
	}

	data, err := json.Marshal(s.notifications)
	if err != nil {
		return err
	}

	// Write atomically via a temp file in the same directory.
	dir := filepath.Dir(s.filePath)
	tmp, err := os.CreateTemp(dir, ".notifications-*.json.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}

	return os.Rename(tmpName, s.filePath)
}

// Load reads persisted notifications from disk into the store. It is a no-op
// when filePath is empty or the file does not yet exist.
func (s *Store) Load() error {
	if s.filePath == "" {
		return nil
	}

	data, err := os.ReadFile(s.filePath)
	if os.IsNotExist(err) {
		return nil // first run — no file yet
	}
	if err != nil {
		return err
	}

	var loaded []Notification
	if err := json.Unmarshal(data, &loaded); err != nil {
		return err
	}

	// Enforce maxItems on load.
	if len(loaded) > s.maxItems {
		loaded = loaded[:s.maxItems]
	}
	s.notifications = loaded
	return nil
}
