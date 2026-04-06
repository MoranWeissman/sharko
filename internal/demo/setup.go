package demo

import (
	"fmt"
	"log"
	"sync"

	"github.com/MoranWeissman/sharko/internal/api"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// SetupDemoServer wires up full mock backends and configures the API server
// for QA testing without any external dependencies. It returns a cleanup
// function that should be called on shutdown.
//
// Users created:
//   - admin / admin    (admin role)
//   - qa    / sharko   (viewer role)
func SetupDemoServer(srv *api.Server, _ int) (cleanup func(), err error) {
	// 1. Start the mock ArgoCD HTTP server.
	mockArgocd, err := NewMockArgocdServer()
	if err != nil {
		return nil, fmt.Errorf("starting mock argocd server: %w", err)
	}
	log.Printf("DEMO: mock ArgoCD listening at %s", mockArgocd.URL())

	// 2. Build an in-memory config store with a demo connection pointing at the mock.
	store := newInMemoryStore()
	conn := models.Connection{
		Name:        "demo/sharko-addons",
		Description: "Demo connection — mock ArgoCD + in-memory Git",
		Git: models.GitRepoConfig{
			Provider: models.GitProviderGitHub,
			Owner:    "demo",
			Repo:     "sharko-addons",
			Token:    "demo-token",
		},
		Argocd: models.ArgocdConfig{
			ServerURL: mockArgocd.URL(),
			Token:     "demo-argocd-token",
			Namespace: "argocd",
			Insecure:  true,
		},
		IsDefault: true,
	}
	if err := store.SaveConnection(conn); err != nil {
		mockArgocd.Close()
		return nil, fmt.Errorf("saving demo connection: %w", err)
	}

	// 3. Replace the connection service's store with the in-memory one.
	// The demo store has the correct connection pointing at our mock ArgoCD server.
	srv.SetDemoConnectionService(store)

	// 4. Set the mock Git provider so write operations work without real GitHub calls.
	mockGit := NewMockGitProvider()
	srv.SetDemoGitProvider(mockGit)

	// 5. Configure the secrets provider for cluster registration.
	credProvider := &MockClusterCredentialsProvider{}
	provCfg := &providers.Config{
		Type:   "demo",
		Region: "demo",
	}
	repoPaths := orchestrator.RepoPathsConfig{
		ClusterValues: "configuration/addons-clusters-values",
		GlobalValues:  "configuration/addons-global-values",
		Catalog:       "configuration/addons-catalog.yaml",
		Charts:        "charts/",
		Bootstrap:     "bootstrap/",
	}
	gitopsCfg := orchestrator.GitOpsConfig{
		PRAutoMerge:  false,
		BranchPrefix: "sharko/",
		CommitPrefix: "sharko:",
		BaseBranch:   "main",
		RepoURL:      "https://github.com/demo/sharko-addons",
	}
	srv.SetWriteAPIDeps(credProvider, provCfg, repoPaths, gitopsCfg)

	// 6. Addon secret definitions — 2 demo definitions.
	addonSecretDefs := map[string]orchestrator.AddonSecretDefinition{
		"datadog": {
			AddonName:  "datadog",
			SecretName: "datadog-secrets",
			Namespace:  "datadog",
			Keys: map[string]string{
				"api-key": "secrets/datadog/api-key",
				"app-key": "secrets/datadog/app-key",
			},
		},
		"vault": {
			AddonName:  "vault",
			SecretName: "vault-unseal-keys",
			Namespace:  "vault",
			Keys: map[string]string{
				"unseal-key": "secrets/vault/unseal-key",
			},
		},
	}
	srv.SetAddonSecretDefs(addonSecretDefs)

	// 7. Default addons.
	srv.SetDefaultAddons(map[string]bool{
		"cert-manager":  true,
		"metrics-server": true,
	})

	// 8. Create demo users: admin/admin and qa/sharko.
	// We use the AddDemoUser method which the api.Server exposes for demo mode.
	if err := srv.AddDemoUser("admin", "admin", "admin"); err != nil {
		log.Printf("DEMO: warning — could not create admin user: %v", err)
	}
	if err := srv.AddDemoUser("qa", "sharko", "viewer"); err != nil {
		log.Printf("DEMO: warning — could not create qa user: %v", err)
	}

	cleanup = func() {
		mockArgocd.Close()
	}
	return cleanup, nil
}

// --- In-memory config.Store implementation ---

// inMemoryStore implements config.Store entirely in memory.
// Used by the demo setup to avoid touching the filesystem.
type inMemoryStore struct {
	mu               sync.RWMutex
	connections      []models.Connection
	activeConnection string
}

func newInMemoryStore() *inMemoryStore {
	return &inMemoryStore{}
}

func (s *inMemoryStore) ListConnections() ([]models.Connection, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]models.Connection, len(s.connections))
	copy(result, s.connections)
	return result, nil
}

func (s *inMemoryStore) GetConnection(name string) (*models.Connection, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.connections {
		if s.connections[i].Name == name {
			c := s.connections[i]
			return &c, nil
		}
	}
	return nil, nil
}

func (s *inMemoryStore) SaveConnection(conn models.Connection) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.connections {
		if s.connections[i].Name == conn.Name {
			s.connections[i] = conn
			return nil
		}
	}
	s.connections = append(s.connections, conn)

	// First connection is auto-active.
	if len(s.connections) == 1 {
		s.activeConnection = conn.Name
	}
	return nil
}

func (s *inMemoryStore) DeleteConnection(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	filtered := s.connections[:0]
	for _, c := range s.connections {
		if c.Name != name {
			filtered = append(filtered, c)
		}
	}
	s.connections = filtered
	if s.activeConnection == name {
		s.activeConnection = ""
		if len(s.connections) > 0 {
			s.activeConnection = s.connections[0].Name
		}
	}
	return nil
}

func (s *inMemoryStore) GetActiveConnection() (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.activeConnection != "" {
		return s.activeConnection, nil
	}
	if len(s.connections) > 0 {
		return s.connections[0].Name, nil
	}
	return "", nil
}

func (s *inMemoryStore) SetActiveConnection(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activeConnection = name
	return nil
}
