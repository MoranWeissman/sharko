package notifications

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/MoranWeissman/sharko/internal/cmstore"
)

// NotificationType categorises what a notification is about.
type NotificationType string

const (
	TypeUpgrade    NotificationType = "upgrade"
	TypeSecurity   NotificationType = "security"
	TypeDrift      NotificationType = "drift"
	TypeConnection NotificationType = "connection"
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

// notificationsKey is the field name under which the notifications slice is
// stored inside the ConfigMap's JSON state (see internal/cmstore).
const notificationsKey = "notifications"

// Store is a thread-safe in-memory ring buffer for notifications. When
// cmStore is non-nil, every mutation is also persisted into a Kubernetes
// ConfigMap via cmstore so notifications survive pod restarts — the
// in-memory slice remains the working copy that handlers read. When
// cmStore is nil (no k8s client available — local/dev mode, or a unit test
// without a fake clientset), the store runs in-memory only, matching the
// old file-absent fallback.
type Store struct {
	mu            sync.RWMutex
	notifications []Notification
	maxItems      int
	cmStore       *cmstore.Store // nil = in-memory only
}

// NewStore creates a Store that retains at most maxItems notifications.
// cmStore controls persistence: pass nil for in-memory only, or a
// cmstore.Store (conventionally backed by a "sharko-notifications"
// ConfigMap — see cmd/sharko/serve.go) to persist across pod restarts. When
// cmStore is non-nil, any previously-persisted notifications are loaded
// immediately so a restart restores prior read/cleared state.
func NewStore(maxItems int, cmStore *cmstore.Store) *Store {
	s := &Store{
		notifications: make([]Notification, 0),
		maxItems:      maxItems,
		cmStore:       cmStore,
	}

	if cmStore != nil {
		if err := s.loadFromCMStore(context.Background()); err != nil {
			slog.Warn("could not load persisted notifications", "error", err, "component", "notifications")
		}
	}

	return s
}

// AttachCMStore wires ConfigMap-backed persistence onto a Store that was
// constructed before the in-cluster k8s client was available. serve.go
// builds the Server (and its notification Store) well before it builds the
// in-cluster clientset used for the PR tracker's cmstore, so the notification
// store starts in-memory-only and this method upgrades it once the same
// clientset is ready — mirroring SetPRTracker/SetObservationsStore.
//
// It loads any state already persisted in the ConfigMap and merges it with
// whatever accumulated in memory since construction (e.g. an early
// notification-checker tick that ran before this call), deduplicated by
// title. The persisted copy wins on a title collision so read/cleared flags
// are never clobbered by an in-memory duplicate. The merged result is
// persisted immediately so all future mutations flow through cmStore.
// No-op if cmStore is nil.
func (s *Store) AttachCMStore(ctx context.Context, cmStore *cmstore.Store) error {
	if cmStore == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.cmStore = cmStore

	data, err := cmStore.Read(ctx)
	if err != nil {
		return err
	}
	persisted := extractNotifications(data)

	merged := persisted
	for _, n := range s.notifications {
		exists := false
		for _, p := range persisted {
			if p.Title == n.Title {
				exists = true
				break
			}
		}
		if !exists {
			merged = append(merged, n)
		}
	}
	if len(merged) > s.maxItems {
		merged = merged[:s.maxItems]
	}
	s.notifications = merged

	return s.persistLocked(ctx)
}

// Add inserts a notification at the front. If a notification with the same
// title already exists — read or unread — it is silently dropped
// (deduplication).
func (s *Store) Add(n Notification) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Deduplicate by title regardless of read state. Marking a notification
	// read is an acknowledgement, not an invitation to re-nag: the periodic
	// checker (checker.go) re-scans and calls Add with the same title every
	// tick as long as the underlying condition (e.g. a newer version) still
	// holds. If dedup only blocked unread duplicates, "mark all as read"
	// would flip the existing entry to read, and the very next tick would
	// see no unread match and re-add the identical title — resurrecting an
	// alert the user just cleared. A genuinely new development (e.g. an
	// even newer version) produces a different title, so it is unaffected
	// and still gets through. Titles that must re-fire after being cleared
	// (e.g. a connection that recovers and later breaks again) go through
	// Resolve first, which removes the old entry so a later Add is not a
	// duplicate.
	for _, existing := range s.notifications {
		if existing.Title == n.Title {
			return
		}
	}
	s.notifications = append([]Notification{n}, s.notifications...)
	if len(s.notifications) > s.maxItems {
		s.notifications = s.notifications[:s.maxItems]
	}
	if err := s.persistLocked(context.Background()); err != nil {
		slog.Warn("could not persist after add", "error", err, "component", "notifications")
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
	if err := s.persistLocked(context.Background()); err != nil {
		slog.Warn("could not persist after mark all read", "error", err, "component", "notifications")
	}
}

// Resolve removes every notification whose Title matches the given title,
// regardless of read/unread state. It is how a previously-reported problem
// clears itself once the underlying condition recovers (e.g. a broken
// connection that comes back healthy). A title with no matches is a no-op.
func (s *Store) Resolve(title string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.notifications[:0]
	for _, n := range s.notifications {
		if n.Title != title {
			kept = append(kept, n)
		}
	}
	s.notifications = kept
	if err := s.persistLocked(context.Background()); err != nil {
		slog.Warn("could not persist after resolve", "error", err, "component", "notifications")
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

// persistLocked writes the current in-memory notifications slice into the
// ConfigMap. Must be called with s.mu held (read or write — JSON marshal
// does not mutate state, but every current caller holds the write lock).
// It is a no-op when cmStore is nil (in-memory only).
func (s *Store) persistLocked(ctx context.Context) error {
	if s.cmStore == nil {
		return nil
	}
	snapshot := s.notifications
	return s.cmStore.ReadModifyWrite(ctx, func(data map[string]interface{}) error {
		return encodeNotifications(data, snapshot)
	})
}

// loadFromCMStore reads persisted notifications from the ConfigMap into the
// store. Called once from NewStore when cmStore is supplied at construction
// time (no lock needed — the store is not yet visible to other goroutines).
func (s *Store) loadFromCMStore(ctx context.Context) error {
	data, err := s.cmStore.Read(ctx)
	if err != nil {
		return err
	}
	loaded := extractNotifications(data)
	if len(loaded) > s.maxItems {
		loaded = loaded[:s.maxItems]
	}
	s.notifications = loaded
	return nil
}

// extractNotifications reads the notifications slice out of the ConfigMap's
// generic JSON state map. Mirrors internal/prtracker's extractPRs pattern.
func extractNotifications(data map[string]interface{}) []Notification {
	raw, ok := data[notificationsKey]
	if !ok {
		return nil
	}

	// data comes from a generic JSON unmarshal into map[string]interface{},
	// so re-marshal and unmarshal into the typed slice.
	b, err := json.Marshal(raw)
	if err != nil {
		return nil
	}

	var result []Notification
	if err := json.Unmarshal(b, &result); err != nil {
		slog.Warn("failed to unmarshal notifications from state", "error", err, "component", "notifications")
		return nil
	}
	return result
}

// encodeNotifications writes the notifications slice back into the
// ConfigMap's generic JSON state map. Mirrors internal/prtracker's
// encodePRs pattern.
func encodeNotifications(data map[string]interface{}, notifications []Notification) error {
	b, err := json.Marshal(notifications)
	if err != nil {
		return err
	}
	var raw interface{}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	data[notificationsKey] = raw
	return nil
}
