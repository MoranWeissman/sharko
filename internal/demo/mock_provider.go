package demo

import (
	"context"
	"fmt"

	"github.com/MoranWeissman/sharko/internal/providers"
)

// MockClusterCredentialsProvider implements providers.ClusterCredentialsProvider in memory.
// GetCredentials returns a fake kubeconfig for any cluster name.
// ListClusters returns the 5 registered + 2 unregistered demo clusters.
//
// clusters/unregisteredClusters are nil on the zero value (the shape every
// existing caller and test constructs via &MockClusterCredentialsProvider{}),
// in which case every method falls back to the package-level
// demoClusters/demoUnregisteredClusters — unchanged default behavior.
// NewMockClusterCredentialsProviderWithConfig populates both fields for a
// generated (non-default) estate.
type MockClusterCredentialsProvider struct {
	clusters             []Cluster
	unregisteredClusters []Cluster
}

// NewMockClusterCredentialsProviderWithConfig builds a credentials provider
// sized per cfg. For the default size it returns the same zero-value
// provider NewMockGitProvider's caller has always constructed directly.
func NewMockClusterCredentialsProviderWithConfig(cfg ScaleConfig) (*MockClusterCredentialsProvider, error) {
	if cfg.IsDefault() {
		return &MockClusterCredentialsProvider{}, nil
	}
	estate, err := GenerateEstate(cfg)
	if err != nil {
		return nil, fmt.Errorf("demo: generating estate for mock credentials provider: %w", err)
	}
	return NewMockClusterCredentialsProviderFromEstate(estate), nil
}

// NewMockClusterCredentialsProviderFromEstate builds a credentials provider
// directly from an already-generated estate — see
// NewMockGitProviderFromEstate's doc for why SetupDemoServer prefers this
// over NewMockClusterCredentialsProviderWithConfig.
func NewMockClusterCredentialsProviderFromEstate(estate *GeneratedEstate) *MockClusterCredentialsProvider {
	return &MockClusterCredentialsProvider{
		clusters:             estate.Clusters,
		unregisteredClusters: estate.UnregisteredClusters,
	}
}

// allClusters returns this provider's registered clusters, falling back to
// the package-level default estate on the zero value.
func (p *MockClusterCredentialsProvider) allClusters() []Cluster {
	if p.clusters != nil {
		return p.clusters
	}
	return demoClusters
}

// allUnregisteredClusters returns this provider's unregistered clusters,
// falling back to the package-level default estate on the zero value.
func (p *MockClusterCredentialsProvider) allUnregisteredClusters() []Cluster {
	if p.unregisteredClusters != nil {
		return p.unregisteredClusters
	}
	return demoUnregisteredClusters
}

// GetCredentials returns a minimal fake kubeconfig for the given cluster name.
func (p *MockClusterCredentialsProvider) GetCredentials(clusterName string) (*providers.Kubeconfig, error) {
	server := fmt.Sprintf("https://k8s.%s.demo.example.com", clusterName)

	// Look up the real server URL from seed data
	for _, c := range append(append([]Cluster(nil), p.allClusters()...), p.allUnregisteredClusters()...) {
		if c.Name == clusterName {
			server = c.Server
			break
		}
	}

	kubeconfig := buildFakeKubeconfig(clusterName, server)
	return &providers.Kubeconfig{
		Raw:    kubeconfig,
		Server: server,
		Token:  "demo-token-" + clusterName,
	}, nil
}

// ListClusters returns all clusters available in the demo secrets backend.
// This includes registered clusters plus 2 unregistered ones for the "discover" flow.
func (p *MockClusterCredentialsProvider) ListClusters() ([]providers.ClusterInfo, error) {
	var clusters []providers.ClusterInfo

	for _, c := range p.allClusters() {
		clusters = append(clusters, providers.ClusterInfo{
			Name:   c.Name,
			Region: c.Region,
			Tags: map[string]string{
				"env":    c.Env,
				"region": c.Region,
			},
		})
	}

	for _, c := range p.allUnregisteredClusters() {
		clusters = append(clusters, providers.ClusterInfo{
			Name:   c.Name,
			Region: c.Region,
			Tags: map[string]string{
				"env":    c.Env,
				"region": c.Region,
			},
		})
	}

	return clusters, nil
}

// SearchSecrets returns an empty list — the demo provider doesn't need secret suggestions.
func (p *MockClusterCredentialsProvider) SearchSecrets(query string) ([]string, error) {
	return nil, nil
}

// HealthCheck always returns nil — the demo provider is always "connected".
func (p *MockClusterCredentialsProvider) HealthCheck(ctx context.Context) error {
	return nil
}

// buildFakeKubeconfig returns a minimal valid kubeconfig YAML for a demo cluster.
func buildFakeKubeconfig(clusterName, server string) []byte {
	return []byte(fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: %s
    insecure-skip-tls-verify: true
  name: %s
contexts:
- context:
    cluster: %s
    user: demo-user
  name: %s
current-context: %s
users:
- name: demo-user
  user:
    token: demo-token-%s
`, server, clusterName, clusterName, clusterName, clusterName, clusterName))
}
