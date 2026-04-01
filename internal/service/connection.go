package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/moran/argocd-addons-platform/internal/argocd"
	"github.com/moran/argocd-addons-platform/internal/config"
	"github.com/moran/argocd-addons-platform/internal/gitprovider"
	"github.com/moran/argocd-addons-platform/internal/models"
)

// ConnectionService manages connections and provides active provider instances.
type ConnectionService struct {
	store   config.Store
	devMode bool // when true, falls back to env vars for missing credentials
}

// NewConnectionService creates a new ConnectionService.
// devMode enables env var fallback for credentials (set AAP_DEV_MODE=true or config.devMode in Helm).
func NewConnectionService(store config.Store) *ConnectionService {
	devMode := os.Getenv("AAP_DEV_MODE") == "true"
	if devMode {
		slog.Info("connection service running in DEV MODE — env var credential fallback enabled")
	}
	return &ConnectionService{store: store, devMode: devMode}
}

// List returns all connections with masked tokens.
func (s *ConnectionService) List() (*models.ConnectionsListResponse, error) {
	connections, err := s.store.ListConnections()
	if err != nil {
		return nil, err
	}

	activeName, err := s.store.GetActiveConnection()
	if err != nil {
		return nil, err
	}

	responses := make([]models.ConnectionResponse, 0, len(connections))
	for _, c := range connections {
		responses = append(responses, c.ToResponse(c.Name == activeName))
	}

	return &models.ConnectionsListResponse{
		Connections:      responses,
		ActiveConnection: activeName,
	}, nil
}

// Create adds a new connection.
func (s *ConnectionService) Create(req models.CreateConnectionRequest) error {
	// Parse repo URL into provider/owner/repo if provided
	if err := req.Git.ParseRepoURL(); err != nil {
		return fmt.Errorf("invalid git URL: %w", err)
	}
	conn := models.Connection{
		Name:        req.Name,
		Description: req.Description,
		Git:         req.Git,
		Argocd:      req.Argocd,
		IsDefault:   req.SetAsDefault,
	}
	// Auto-derive connection name from git repo if not provided
	if conn.Name == "" || conn.Name == "default" {
		conn.Name = deriveConnectionName(conn.Git)
	}
	return s.store.SaveConnection(conn)
}

// deriveConnectionName builds a connection name from the git config.
func deriveConnectionName(git models.GitRepoConfig) string {
	switch git.Provider {
	case models.GitProviderGitHub:
		if git.Owner != "" && git.Repo != "" {
			return git.Owner + "/" + git.Repo
		}
	case models.GitProviderAzureDevOps:
		if git.Organization != "" && git.Project != "" && git.Repository != "" {
			return git.Organization + "/" + git.Project + "/" + git.Repository
		}
	}
	return "default"
}

// GetConnection returns a connection by name.
func (s *ConnectionService) GetConnection(name string) (*models.Connection, error) {
	return s.store.GetConnection(name)
}

// Delete removes a connection.
func (s *ConnectionService) Delete(name string) error {
	return s.store.DeleteConnection(name)
}

// SetActive sets the active connection.
func (s *ConnectionService) SetActive(name string) error {
	slog.Info("active connection changed", "connection", name)
	return s.store.SetActiveConnection(name)
}

// GetActiveGitProvider returns a GitProvider for the currently active connection.
func (s *ConnectionService) GetActiveGitProvider() (gitprovider.GitProvider, error) {
	conn, err := s.getActiveConn()
	if err != nil {
		return nil, err
	}
	return s.buildGitProvider(conn)
}

// GetActiveArgocdClient returns an ArgoCD client for the currently active connection.
// If server_url is empty, it uses in-cluster mode with the pod's ServiceAccount token.
func (s *ConnectionService) GetActiveArgocdClient() (*argocd.Client, error) {
	conn, err := s.getActiveConn()
	if err != nil {
		return nil, err
	}
	return s.buildArgocdClient(conn)
}

func (s *ConnectionService) buildArgocdClient(conn *models.Connection) (*argocd.Client, error) {
	token := conn.Argocd.Token
	serverURL := conn.Argocd.ServerURL

	// Dev mode: fall back to env vars
	if s.devMode {
		if token == "" {
			token = os.Getenv("ARGOCD_TOKEN")
			if token != "" {
				slog.Info("argocd: using ARGOCD_TOKEN env var (dev mode fallback)")
			}
		}
		if serverURL == "" {
			serverURL = os.Getenv("ARGOCD_SERVER_URL")
		}
	}

	// Auto-discover server URL if still empty
	if serverURL == "" {
		ns := conn.Argocd.Namespace
		if ns == "" {
			ns = "argocd"
		}
		serverURL = argocd.DiscoverServerURL(ns)
	}

	if token == "" {
		return nil, fmt.Errorf("ArgoCD token not configured. Provide it via Settings UI or set ARGOCD_TOKEN env var with AAP_DEV_MODE=true")
	}

	return argocd.NewClient(serverURL, token, true), nil
}

// GetGitProviderForConnection returns a GitProvider for a specific named connection.
func (s *ConnectionService) GetGitProviderForConnection(name string) (gitprovider.GitProvider, error) {
	conn, err := s.store.GetConnection(name)
	if err != nil {
		return nil, err
	}
	if conn == nil {
		return nil, fmt.Errorf("connection %q not found", name)
	}
	return s.buildGitProvider(conn)
}

// TestConnection tests both Git and ArgoCD connectivity for the active connection.
func (s *ConnectionService) TestConnection(ctx context.Context) (gitErr, argocdErr error) {
	conn, err := s.getActiveConn()
	if err != nil {
		return err, err
	}

	gp, err := s.buildGitProvider(conn)
	if err != nil {
		gitErr = err
	} else {
		gitErr = gp.TestConnection(ctx)
	}

	ac, err := s.buildArgocdClient(conn)
	if err != nil {
		argocdErr = err
	} else {
		argocdErr = ac.TestConnection(ctx)
	}

	return gitErr, argocdErr
}

// AuthInfo describes which auth method was used for each service.
type AuthInfo struct {
	GitSource    string // "provided", "env:GITHUB_TOKEN", ""
	ArgocdSource string // "provided", "env:ARGOCD_TOKEN", "serviceaccount", ""
}

// TestCredentials tests Git and ArgoCD connectivity for unsaved credentials.
func (s *ConnectionService) TestCredentials(ctx context.Context, conn *models.Connection) (gitErr, argocdErr error, auth AuthInfo) {
	// Parse repo URL if provided
	if err := conn.Git.ParseRepoURL(); err != nil {
		return err, nil, auth
	}

	// Track git auth source
	if conn.Git.Token != "" {
		auth.GitSource = "provided"
	} else if os.Getenv("GITHUB_TOKEN") != "" {
		auth.GitSource = "env:GITHUB_TOKEN"
	}

	gp, err := s.buildGitProvider(conn)
	if err != nil {
		gitErr = err
	} else {
		gitErr = gp.TestConnection(ctx)
	}

	// Track argocd auth source
	if conn.Argocd.Token != "" {
		auth.ArgocdSource = "provided"
	} else if os.Getenv("ARGOCD_TOKEN") != "" {
		auth.ArgocdSource = "env:ARGOCD_TOKEN"
	} else {
		auth.ArgocdSource = "serviceaccount"
	}

	ac, err := s.buildArgocdClient(conn)
	if err != nil {
		argocdErr = err
	} else {
		argocdErr = ac.TestConnection(ctx)
	}

	return gitErr, argocdErr, auth
}

// DiscoverArgocdURL finds the ArgoCD server URL for the given namespace.
func (s *ConnectionService) DiscoverArgocdURL(namespace string) string {
	return argocd.DiscoverServerURL(namespace)
}

func (s *ConnectionService) getActiveConn() (*models.Connection, error) {
	activeName, err := s.store.GetActiveConnection()
	if err != nil {
		return nil, err
	}
	if activeName == "" {
		return nil, fmt.Errorf("no active connection configured")
	}

	conn, err := s.store.GetConnection(activeName)
	if err != nil {
		return nil, err
	}
	if conn == nil {
		return nil, fmt.Errorf("active connection %q not found", activeName)
	}

	return conn, nil
}

func (s *ConnectionService) buildGitProvider(conn *models.Connection) (gitprovider.GitProvider, error) {
	switch conn.Git.Provider {
	case models.GitProviderGitHub:
		token := conn.Git.Token
		if token == "" && s.devMode {
			token = os.Getenv("GITHUB_TOKEN")
			if token != "" {
				slog.Info("git: using GITHUB_TOKEN env var (dev mode fallback)")
			}
		}
		if token == "" {
			return nil, fmt.Errorf("GitHub token not configured. Provide it via Settings UI or set GITHUB_TOKEN env var with AAP_DEV_MODE=true")
		}
		return gitprovider.NewGitHubProvider(conn.Git.Owner, conn.Git.Repo, token), nil
	case models.GitProviderAzureDevOps:
		pat := conn.Git.PAT
		if pat == "" && s.devMode {
			pat = os.Getenv("AZURE_DEVOPS_PAT")
			if pat != "" {
				slog.Info("git: using AZURE_DEVOPS_PAT env var (dev mode fallback)")
			}
		}
		if pat == "" {
			return nil, fmt.Errorf("Azure DevOps PAT not configured. Provide it via Settings UI or set AZURE_DEVOPS_PAT env var with AAP_DEV_MODE=true")
		}
		return gitprovider.NewAzureDevOpsProvider(conn.Git.Organization, conn.Git.Project, conn.Git.Repository, pat), nil
	default:
		return nil, fmt.Errorf("unsupported git provider: %s", conn.Git.Provider)
	}
}
