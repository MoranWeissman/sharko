package config

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/moran/argocd-addons-platform/internal/crypto"
	"github.com/moran/argocd-addons-platform/internal/models"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// connData is the in-memory representation of the connection store persisted in a K8s Secret.
type connData struct {
	Connections      []models.Connection `json:"connections"`
	ActiveConnection string              `json:"active_connection,omitempty"`
}

// K8sStore implements Store using an encrypted K8s Secret.
// Connections are JSON-marshaled and encrypted with AES-256-GCM before storage.
type K8sStore struct {
	client        kubernetes.Interface
	namespace     string
	secretName    string
	encryptionKey string
	mu            sync.RWMutex
}

// NewK8sStore creates a K8sStore backed by an in-cluster K8s Secret.
func NewK8sStore(namespace, secretName, encryptionKey string) (*K8sStore, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("loading in-cluster config: %w", err)
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating k8s client: %w", err)
	}
	return newK8sStoreWithClient(client, namespace, secretName, encryptionKey)
}

// newK8sStoreWithClient creates a K8sStore with a provided client — used for testing.
func newK8sStoreWithClient(client kubernetes.Interface, namespace, secretName, encryptionKey string) (*K8sStore, error) {
	return &K8sStore{
		client:        client,
		namespace:     namespace,
		secretName:    secretName,
		encryptionKey: encryptionKey,
	}, nil
}

// load reads and decrypts the K8s Secret, returning the connData and its resourceVersion.
// Returns empty connData when the Secret does not yet exist.
// Caller must hold s.mu.
func (s *K8sStore) load() (*connData, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	secret, err := s.client.CoreV1().Secrets(s.namespace).Get(ctx, s.secretName, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return &connData{}, "", nil
		}
		return nil, "", fmt.Errorf("getting connection secret: %w", err)
	}

	resourceVersion := secret.ResourceVersion

	data := &connData{}

	// Decrypt connections
	if encConns, ok := secret.Data["connections"]; ok && len(encConns) > 0 {
		plaintext, err := crypto.Decrypt(string(encConns), s.encryptionKey)
		if err != nil {
			return nil, "", fmt.Errorf("decrypting connections: %w", err)
		}
		if err := json.Unmarshal(plaintext, &data.Connections); err != nil {
			return nil, "", fmt.Errorf("unmarshaling connections: %w", err)
		}
	}

	// Active connection is stored as plaintext
	if active, ok := secret.Data["active"]; ok {
		data.ActiveConnection = string(active)
	}

	return data, resourceVersion, nil
}

// save encrypts and persists connData to the K8s Secret.
// It tries Update first (with resourceVersion for optimistic concurrency),
// falls back to Create on NotFound, and retries Update on AlreadyExists.
// Caller must hold s.mu.
func (s *K8sStore) save(data *connData, resourceVersion string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Encrypt connections
	connsJSON, err := json.Marshal(data.Connections)
	if err != nil {
		return fmt.Errorf("marshaling connections: %w", err)
	}
	encConns, err := crypto.Encrypt(connsJSON, s.encryptionKey)
	if err != nil {
		return fmt.Errorf("encrypting connections: %w", err)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            s.secretName,
			Namespace:       s.namespace,
			ResourceVersion: resourceVersion, // optimistic concurrency for updates
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "aap",
				"app.kubernetes.io/component":  "connection-config",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"connections": []byte(encConns),
			"active":      []byte(data.ActiveConnection),
		},
	}

	// Try Update first
	_, err = s.client.CoreV1().Secrets(s.namespace).Update(ctx, secret, metav1.UpdateOptions{})
	if err == nil {
		return nil
	}

	if !k8serrors.IsNotFound(err) {
		return fmt.Errorf("updating connection secret: %w", err)
	}

	// Secret doesn't exist — Create it
	_, err = s.client.CoreV1().Secrets(s.namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err == nil {
		return nil
	}

	// Race condition: another writer created it between our Update and Create
	if k8serrors.IsAlreadyExists(err) {
		_, err = s.client.CoreV1().Secrets(s.namespace).Update(ctx, secret, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("updating connection secret (retry): %w", err)
		}
		return nil
	}

	return fmt.Errorf("creating connection secret: %w", err)
}

// ListConnections returns all stored connections.
func (s *K8sStore) ListConnections() ([]models.Connection, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, _, err := s.load()
	if err != nil {
		return nil, err
	}
	return data.Connections, nil
}

// GetConnection returns the named connection or nil if not found.
func (s *K8sStore) GetConnection(name string) (*models.Connection, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, _, err := s.load()
	if err != nil {
		return nil, err
	}

	for i := range data.Connections {
		if data.Connections[i].Name == name {
			return &data.Connections[i], nil
		}
	}
	return nil, nil
}

// SaveConnection creates or updates a connection in the store.
func (s *K8sStore) SaveConnection(conn models.Connection) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, rv, err := s.load()
	if err != nil {
		return err
	}

	now := time.Now().UTC().Format(time.RFC3339)

	// Update existing or append new
	found := false
	for i := range data.Connections {
		if data.Connections[i].Name == conn.Name {
			conn.UpdatedAt = now
			if conn.CreatedAt == "" {
				conn.CreatedAt = data.Connections[i].CreatedAt
			}
			data.Connections[i] = conn
			found = true
			break
		}
	}

	if !found {
		conn.CreatedAt = now
		conn.UpdatedAt = now
		data.Connections = append(data.Connections, conn)
	}

	// If this is the default, unset others
	if conn.IsDefault {
		for i := range data.Connections {
			if data.Connections[i].Name != conn.Name {
				data.Connections[i].IsDefault = false
			}
		}
	}

	// If this is the first connection, make it default and active
	if len(data.Connections) == 1 {
		data.Connections[0].IsDefault = true
		data.ActiveConnection = data.Connections[0].Name
	}

	return s.save(data, rv)
}

// DeleteConnection removes a connection by name. Returns an error if not found.
func (s *K8sStore) DeleteConnection(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, rv, err := s.load()
	if err != nil {
		return err
	}

	connections := make([]models.Connection, 0, len(data.Connections))
	for _, c := range data.Connections {
		if c.Name != name {
			connections = append(connections, c)
		}
	}

	if len(connections) == len(data.Connections) {
		return fmt.Errorf("connection %q not found", name)
	}

	data.Connections = connections

	if data.ActiveConnection == name {
		data.ActiveConnection = ""
		if len(data.Connections) > 0 {
			data.ActiveConnection = data.Connections[0].Name
		}
	}

	return s.save(data, rv)
}

// GetActiveConnection returns the active connection name, falling back to the default
// or first connection if no active is explicitly set.
func (s *K8sStore) GetActiveConnection() (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, _, err := s.load()
	if err != nil {
		return "", err
	}

	if data.ActiveConnection != "" {
		return data.ActiveConnection, nil
	}

	// Fall back to default connection
	for _, c := range data.Connections {
		if c.IsDefault {
			return c.Name, nil
		}
	}

	// Fall back to first connection
	if len(data.Connections) > 0 {
		return data.Connections[0].Name, nil
	}

	return "", nil
}

// SetActiveConnection sets the active connection by name. Returns an error if not found.
func (s *K8sStore) SetActiveConnection(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, rv, err := s.load()
	if err != nil {
		return err
	}

	// Verify connection exists
	found := false
	for _, c := range data.Connections {
		if c.Name == name {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("connection %q not found", name)
	}

	data.ActiveConnection = name
	return s.save(data, rv)
}
