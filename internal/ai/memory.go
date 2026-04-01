package ai

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

// MemoryEntry represents a single learned fact or observation.
type MemoryEntry struct {
	Content   string `json:"content"`
	Category  string `json:"category"` // e.g. "user_preference", "platform_observation", "addon_info"
	CreatedAt string `json:"created_at"`
}

// MemoryStore persists agent learnings to a JSON file.
type MemoryStore struct {
	mu      sync.RWMutex
	entries []MemoryEntry
	path    string
}

// NewMemoryStore creates or loads a memory store from the given file path.
func NewMemoryStore(path string) *MemoryStore {
	m := &MemoryStore{path: path}
	m.load()
	return m
}

func (m *MemoryStore) load() {
	data, err := os.ReadFile(m.path)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("failed to load agent memory", "path", m.path, "error", err)
		}
		return
	}
	if err := json.Unmarshal(data, &m.entries); err != nil {
		slog.Warn("failed to parse agent memory", "path", m.path, "error", err)
	}
	slog.Info("agent memory loaded", "entries", len(m.entries))
}

func (m *MemoryStore) save() {
	data, err := json.MarshalIndent(m.entries, "", "  ")
	if err != nil {
		slog.Error("failed to marshal agent memory", "error", err)
		return
	}
	if err := os.WriteFile(m.path, data, 0600); err != nil {
		slog.Error("failed to save agent memory", "path", m.path, "error", err)
	}
}

// Add stores a new memory entry.
func (m *MemoryStore) Add(content, category string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Avoid duplicates — skip if very similar content already exists
	contentLower := strings.ToLower(content)
	for _, e := range m.entries {
		if strings.ToLower(e.Content) == contentLower {
			return
		}
	}

	m.entries = append(m.entries, MemoryEntry{
		Content:   content,
		Category:  category,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})

	// Cap at 100 entries — remove oldest if over limit
	if len(m.entries) > 100 {
		m.entries = m.entries[len(m.entries)-100:]
	}

	m.save()
	slog.Info("agent memory saved", "content", content, "category", category)
}

// GetAll returns all memories formatted as a string for the LLM context.
func (m *MemoryStore) GetAll() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.entries) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, e := range m.entries {
		fmt.Fprintf(&sb, "- [%s] %s\n", e.Category, e.Content)
	}
	return sb.String()
}

// Search returns memories matching a query string.
func (m *MemoryStore) Search(query string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	queryLower := strings.ToLower(query)
	var sb strings.Builder
	count := 0
	for _, e := range m.entries {
		if strings.Contains(strings.ToLower(e.Content), queryLower) ||
			strings.Contains(strings.ToLower(e.Category), queryLower) {
			count++
			fmt.Fprintf(&sb, "- [%s] %s\n", e.Category, e.Content)
		}
	}

	if count == 0 {
		return "No memories found matching: " + query
	}
	return fmt.Sprintf("Found %d memories:\n%s", count, sb.String())
}
