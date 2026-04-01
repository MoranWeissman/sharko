package argocd

import (
	"context"
	"fmt"
	"strings"

	"github.com/moran/argocd-addons-platform/internal/models"
)

// Service wraps the ArgoCD Client and adds business logic on top of the
// raw API calls.
type Service struct {
	client *Client
}

// NewService creates a new Service backed by the given Client.
func NewService(client *Client) *Service {
	return &Service{client: client}
}

// GetClusterApplications returns all ArgoCD applications whose destination
// server matches the given clusterName (compared against both the cluster
// name and the server URL registered in ArgoCD).
func (s *Service) GetClusterApplications(ctx context.Context, clusterName string) ([]models.ArgocdApplication, error) {
	clusters, err := s.client.ListClusters(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching clusters: %w", err)
	}

	// Build a set of server URLs that belong to the requested cluster so we
	// can match applications by destination server.
	serverURLs := make(map[string]struct{})
	for _, c := range clusters {
		if c.Name == clusterName || c.Server == clusterName {
			serverURLs[c.Server] = struct{}{}
		}
	}

	apps, err := s.client.ListApplications(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching applications: %w", err)
	}

	var matched []models.ArgocdApplication
	for _, app := range apps {
		// Match by destination server URL
		if _, ok := serverURLs[app.DestinationServer]; ok {
			matched = append(matched, app)
			continue
		}
		// Match by destination name (used when apps reference clusters by name)
		if app.DestinationName == clusterName {
			matched = append(matched, app)
			continue
		}
		// Match by app name containing cluster name (covers in-cluster and other patterns)
		if strings.HasSuffix(app.Name, "-"+clusterName) {
			matched = append(matched, app)
		}
	}

	return matched, nil
}

// GetApplicationsByNames fetches the specified applications and returns them
// as a map keyed by application name for easy lookup. Applications that are
// not found are silently omitted from the result.
func (s *Service) GetApplicationsByNames(ctx context.Context, names []string) (map[string]models.ArgocdApplication, error) {
	result := make(map[string]models.ArgocdApplication, len(names))

	for _, name := range names {
		app, err := s.client.GetApplication(ctx, name)
		if err != nil {
			// Treat individual fetch errors as non-fatal; the application
			// may have been deleted or the name may be incorrect.
			continue
		}
		result[app.Name] = *app
	}

	return result, nil
}

// GetClusterConnectionState looks up the named cluster in ArgoCD and returns
// its connection state (e.g., "Successful", "Failed"). If the cluster is not
// found, an error is returned.
func (s *Service) GetClusterConnectionState(ctx context.Context, clusterName string) (string, error) {
	clusters, err := s.client.ListClusters(ctx)
	if err != nil {
		return "", fmt.Errorf("fetching clusters: %w", err)
	}

	for _, c := range clusters {
		if c.Name == clusterName || c.Server == clusterName {
			return c.ConnectionState, nil
		}
	}

	return "", fmt.Errorf("cluster %q not found in ArgoCD", clusterName)
}
