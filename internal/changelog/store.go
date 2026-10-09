// Package changelog holds a durable, capped, per-cluster record of
// completed cluster changes — the "did this happen" log that Sharko never
// used to keep. internal/prtracker fires audit events (pr_merged /
// pr_closed_without_merge) and then removes the PR from its own ConfigMap
// once it observes the terminal status; that removal is intentional (the
// tracker only cares about PRs still in flight) but it meant Sharko forgot
// every past change the instant it landed. This package is the seam that
// captures a tiny Entry right before that removal happens, so a cluster's
// "History" view has something durable to read.
package changelog

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/MoranWeissman/sharko/internal/cmstore"
)

// Status values recorded on a completed change. These intentionally mirror
// the terminal internal/prtracker.PRInfo.LastStatus values ("merged" /
// "closed") so callers can pass the tracker's own status string straight
// through without translation.
const (
	StatusMerged = "merged"
	StatusClosed = "closed"
)

// changesKey is the field name under which the per-cluster entries map is
// stored inside the ConfigMap's JSON state (see internal/cmstore).
const changesKey = "changes"

// DefaultMaxPerCluster is the rolling cap applied to each cluster's
// change-log slice independently. Per-cluster (not a shared budget) so one
// busy cluster's churn cannot evict a quiet cluster's history.
const DefaultMaxPerCluster = 100

// Entry is a single completed cluster change, recorded when its backing
// pull request transitions to merged or closed. Deliberately tiny — no PR
// body, diff, or discussion thread. This is a change LOG (what happened,
// when, and how it landed), not a change ARCHIVE.
type Entry struct {
	// Operation is the plain-English operation label — see PrettyOperation.
	Operation   string    `json:"operation"`
	Addon       string    `json:"addon,omitempty"`
	Cluster     string    `json:"cluster"`
	PRID        int       `json:"pr_id"`
	PRUrl       string    `json:"pr_url"`
	OpenedAt    time.Time `json:"opened_at"`
	CompletedAt time.Time `json:"completed_at"`
	Status      string    `json:"status"` // "merged" | "closed"
}

// PrettyOperation turns the raw hyphenated Operation enum (see the Op*
// constants in internal/prtracker/types.go, e.g. "addon-enable") into the
// same plain-English phrase the UI already renders in toasts
// (ui/src/lib/utils.ts prettyOperation) — hyphens become spaces. Kept in
// sync deliberately: both sides do nothing more than
// strings.ReplaceAll(op, "-", " ").
func PrettyOperation(operation string) string {
	return strings.ReplaceAll(operation, "-", " ")
}

// Store is a thread-safe, per-cluster rolling change log. Each cluster's
// slice is capped independently at maxPerCluster (newest entry first).
// When cmStore is non-nil, every mutation is also persisted into a
// Kubernetes ConfigMap via cmstore so the log survives pod restarts — the
// in-memory map remains the working copy that handlers read. This mirrors
// internal/notifications.Store closely; when cmStore is nil (no k8s
// client — local/dev mode, or a unit test without a fake clientset) the
// store runs in-memory only.
type Store struct {
	mu            sync.RWMutex
	entries       map[string][]Entry // cluster -> entries, newest first
	maxPerCluster int
	cmStore       *cmstore.Store // nil = in-memory only
}

// NewStore creates a Store that retains at most maxPerCluster entries per
// cluster. cmStore controls persistence: pass nil for in-memory only, or a
// cmstore.Store (conventionally backed by a "sharko-cluster-changes"
// ConfigMap — see cmd/sharko/serve.go) to persist across pod restarts.
// When cmStore is non-nil, any previously-persisted entries are loaded
// immediately so a restart restores prior history.
func NewStore(maxPerCluster int, cmStore *cmstore.Store) *Store {
	s := &Store{
		entries:       make(map[string][]Entry),
		maxPerCluster: maxPerCluster,
		cmStore:       cmStore,
	}

	if cmStore != nil {
		if err := s.loadFromCMStore(context.Background()); err != nil {
			slog.Warn("could not load persisted change log", "error", err, "component", "changelog")
		}
	}

	return s
}

// AttachCMStore wires ConfigMap-backed persistence onto a Store that was
// constructed before the in-cluster k8s client was available. serve.go
// builds the Server (and its change-log Store) well before it builds the
// in-cluster clientset used for the PR tracker's cmstore, so this store
// starts in-memory-only and this method upgrades it once the same
// clientset is ready — mirroring notifications.Store.AttachCMStore.
//
// It loads any state already persisted in the ConfigMap and merges it,
// per cluster, with whatever accumulated in memory since construction,
// deduplicated by PRID (the persisted copy wins on a collision so a
// restart never gets a duplicate of the same PR). The merged result is
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
	persisted := extractEntries(data)

	merged := make(map[string][]Entry, len(persisted))
	for cluster, entries := range persisted {
		merged[cluster] = append([]Entry(nil), entries...)
	}
	for cluster, entries := range s.entries {
		for _, e := range entries {
			exists := false
			for _, p := range merged[cluster] {
				if p.PRID == e.PRID {
					exists = true
					break
				}
			}
			if !exists {
				merged[cluster] = append(merged[cluster], e)
			}
		}
		if len(merged[cluster]) > s.maxPerCluster {
			merged[cluster] = merged[cluster][:s.maxPerCluster]
		}
	}
	s.entries = merged

	return s.persistLocked(ctx)
}

// Record appends a completed change to its cluster's log (newest first),
// evicting the oldest entry once that cluster's slice exceeds
// maxPerCluster. A blank Cluster is a no-op — every recorded change must
// be attributable to a cluster.
func (s *Store) Record(entry Entry) {
	if entry.Cluster == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[entry.Cluster] = append([]Entry{entry}, s.entries[entry.Cluster]...)
	if len(s.entries[entry.Cluster]) > s.maxPerCluster {
		s.entries[entry.Cluster] = s.entries[entry.Cluster][:s.maxPerCluster]
	}
	if err := s.persistLocked(context.Background()); err != nil {
		slog.Warn("could not persist after record", "error", err, "component", "changelog", "cluster", entry.Cluster)
	}
}

// List returns a snapshot of the given cluster's change log, newest
// first. Returns an empty (non-nil) slice for a cluster with no recorded
// changes.
func (s *Store) List(cluster string) []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries := s.entries[cluster]
	result := make([]Entry, len(entries))
	copy(result, entries)
	return result
}

// persistLocked writes the current in-memory entries map into the
// ConfigMap. Must be called with s.mu held. No-op when cmStore is nil
// (in-memory only).
func (s *Store) persistLocked(ctx context.Context) error {
	if s.cmStore == nil {
		return nil
	}
	snapshot := make(map[string][]Entry, len(s.entries))
	for cluster, entries := range s.entries {
		snapshot[cluster] = entries
	}
	return s.cmStore.ReadModifyWrite(ctx, func(data map[string]interface{}) error {
		return encodeEntries(data, snapshot)
	})
}

// loadFromCMStore reads persisted entries from the ConfigMap into the
// store. Called once from NewStore when cmStore is supplied at
// construction time (no lock needed — the store is not yet visible to
// other goroutines).
func (s *Store) loadFromCMStore(ctx context.Context) error {
	data, err := s.cmStore.Read(ctx)
	if err != nil {
		return err
	}
	loaded := extractEntries(data)
	for cluster, entries := range loaded {
		if len(entries) > s.maxPerCluster {
			loaded[cluster] = entries[:s.maxPerCluster]
		}
	}
	s.entries = loaded
	return nil
}

// extractEntries reads the per-cluster entries map out of the ConfigMap's
// generic JSON state map. Mirrors internal/notifications' extractNotifications
// pattern.
func extractEntries(data map[string]interface{}) map[string][]Entry {
	raw, ok := data[changesKey]
	if !ok {
		return make(map[string][]Entry)
	}

	// data comes from a generic JSON unmarshal into map[string]interface{},
	// so re-marshal and unmarshal into the typed map.
	b, err := json.Marshal(raw)
	if err != nil {
		return make(map[string][]Entry)
	}

	var result map[string][]Entry
	if err := json.Unmarshal(b, &result); err != nil {
		slog.Warn("failed to unmarshal change log from state", "error", err, "component", "changelog")
		return make(map[string][]Entry)
	}
	if result == nil {
		result = make(map[string][]Entry)
	}
	return result
}

// encodeEntries writes the per-cluster entries map back into the
// ConfigMap's generic JSON state map. Mirrors internal/notifications'
// encodeNotifications pattern.
func encodeEntries(data map[string]interface{}, entries map[string][]Entry) error {
	b, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	var raw interface{}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	data[changesKey] = raw
	return nil
}
