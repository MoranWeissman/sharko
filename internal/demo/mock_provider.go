package demo

import (
	"fmt"

	"github.com/MoranWeissman/sharko/internal/providers"
)

// MockClusterCredentialsProvider implements providers.ClusterCredentialsProvider in memory.
// GetCredentials returns a fake kubeconfig for any cluster name.
// ListClusters returns the 5 registered + 2 unregistered demo clusters.
type MockClusterCredentialsProvider struct{}

// GetCredentials returns a minimal fake kubeconfig for the given cluster name.
func (p *MockClusterCredentialsProvider) GetCredentials(clusterName string) (*providers.Kubeconfig, error) {
	server := fmt.Sprintf("https://k8s.%s.demo.example.com", clusterName)

	// Look up the real server URL from seed data
	for _, c := range append(demoClusters, demoUnregisteredClusters...) {
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

	for _, c := range demoClusters {
		clusters = append(clusters, providers.ClusterInfo{
			Name:   c.Name,
			Region: c.Region,
			Tags: map[string]string{
				"env":    c.Env,
				"region": c.Region,
			},
		})
	}

	for _, c := range demoUnregisteredClusters {
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
