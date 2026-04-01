package config

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/moran/argocd-addons-platform/internal/models"
	"gopkg.in/yaml.v3"
)

// Store defines the interface for connection configuration storage.
type Store interface {
	ListConnections() ([]models.Connection, error)
	GetConnection(name string) (*models.Connection, error)
	SaveConnection(conn models.Connection) error
	DeleteConnection(name string) error
	GetActiveConnection() (string, error)
	SetActiveConnection(name string) error
}

// configFile represents the on-disk YAML config file structure.
type configFile struct {
	Connections      []models.Connection `yaml:"connections"`
	ActiveConnection string              `yaml:"active_connection,omitempty"`
}

// FileStore implements Store using a local YAML file.
// Used for local development / minikube.
type FileStore struct {
	path string
	mu   sync.RWMutex
}

// NewFileStore creates a FileStore backed by the given YAML file.
// If the file doesn't exist, it will be created on first write.
func NewFileStore(path string) *FileStore {
	return &FileStore{path: path}
}

func (s *FileStore) load() (*configFile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return &configFile{}, nil
		}
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	// Expand environment variables in the file content
	expanded := os.ExpandEnv(string(data))

	var cfg configFile
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	// Auto-derive connection name from git owner/repo if name is generic
	for i := range cfg.Connections {
		c := &cfg.Connections[i]
		if (c.Name == "" || c.Name == "default") && c.Git.Owner != "" && c.Git.Repo != "" {
			c.Name = c.Git.Owner + "/" + c.Git.Repo
		}
	}

	return &cfg, nil
}

func (s *FileStore) save(cfg *configFile) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	if err := os.WriteFile(s.path, data, 0600); err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}

	return nil
}

func (s *FileStore) ListConnections() ([]models.Connection, error) {
	cfg, err := s.load()
	if err != nil {
		return nil, err
	}
	return cfg.Connections, nil
}

func (s *FileStore) GetConnection(name string) (*models.Connection, error) {
	cfg, err := s.load()
	if err != nil {
		return nil, err
	}

	for i := range cfg.Connections {
		if cfg.Connections[i].Name == name {
			return &cfg.Connections[i], nil
		}
	}

	return nil, nil
}

func (s *FileStore) SaveConnection(conn models.Connection) error {
	cfg, err := s.load()
	if err != nil {
		return err
	}

	now := time.Now().UTC().Format(time.RFC3339)

	// Update existing or append new
	found := false
	for i := range cfg.Connections {
		if cfg.Connections[i].Name == conn.Name {
			conn.UpdatedAt = now
			if conn.CreatedAt == "" {
				conn.CreatedAt = cfg.Connections[i].CreatedAt
			}
			cfg.Connections[i] = conn
			found = true
			break
		}
	}

	if !found {
		conn.CreatedAt = now
		conn.UpdatedAt = now
		cfg.Connections = append(cfg.Connections, conn)
	}

	// If this is the default, unset others
	if conn.IsDefault {
		for i := range cfg.Connections {
			if cfg.Connections[i].Name != conn.Name {
				cfg.Connections[i].IsDefault = false
			}
		}
	}

	// If this is the first connection, make it default and active
	if len(cfg.Connections) == 1 {
		cfg.Connections[0].IsDefault = true
		cfg.ActiveConnection = cfg.Connections[0].Name
	}

	return s.save(cfg)
}

func (s *FileStore) DeleteConnection(name string) error {
	cfg, err := s.load()
	if err != nil {
		return err
	}

	connections := make([]models.Connection, 0, len(cfg.Connections))
	for _, c := range cfg.Connections {
		if c.Name != name {
			connections = append(connections, c)
		}
	}

	if len(connections) == len(cfg.Connections) {
		return fmt.Errorf("connection %q not found", name)
	}

	cfg.Connections = connections

	if cfg.ActiveConnection == name {
		cfg.ActiveConnection = ""
		if len(cfg.Connections) > 0 {
			cfg.ActiveConnection = cfg.Connections[0].Name
		}
	}

	return s.save(cfg)
}

func (s *FileStore) GetActiveConnection() (string, error) {
	cfg, err := s.load()
	if err != nil {
		return "", err
	}

	if cfg.ActiveConnection != "" {
		return cfg.ActiveConnection, nil
	}

	// Fall back to default connection
	for _, c := range cfg.Connections {
		if c.IsDefault {
			return c.Name, nil
		}
	}

	// Fall back to first connection
	if len(cfg.Connections) > 0 {
		return cfg.Connections[0].Name, nil
	}

	return "", nil
}

func (s *FileStore) SetActiveConnection(name string) error {
	cfg, err := s.load()
	if err != nil {
		return err
	}

	// Verify connection exists
	found := false
	for _, c := range cfg.Connections {
		if c.Name == name {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("connection %q not found", name)
	}

	cfg.ActiveConnection = name
	return s.save(cfg)
}
