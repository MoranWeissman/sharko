package providers

import (
	"context"
	"fmt"
)

// SecretProvider abstracts fetching raw secret values from an external backend
// (e.g. AWS Secrets Manager, Kubernetes Secrets). The path argument is the
// provider-specific identifier for the secret (e.g. "secrets/datadog/api-key").
type SecretProvider interface {
	GetSecretValue(ctx context.Context, path string) ([]byte, error)
}

// ClusterCredentialsProvider abstracts how cluster kubeconfigs are fetched.
// The server uses this to retrieve credentials when registering clusters.
type ClusterCredentialsProvider interface {
	// GetCredentials fetches the kubeconfig for the named cluster.
	GetCredentials(clusterName string) (*Kubeconfig, error)
	// ListClusters returns all clusters available in this secrets backend.
	ListClusters() ([]ClusterInfo, error)
}

// Kubeconfig holds the raw kubeconfig YAML and extracted connection info.
type Kubeconfig struct {
	Raw    []byte // Full kubeconfig YAML bytes
	Server string // API server URL (extracted for ArgoCD registration)
	CAData []byte // CA certificate data
	Token  string // Bearer token or client cert, if present
}

// ClusterInfo is a lightweight cluster descriptor from the secrets backend.
type ClusterInfo struct {
	Name   string
	Region string
	Tags   map[string]string
}

// Config holds provider configuration, read from server-side env vars / Helm values.
type Config struct {
	Type      string // "aws-sm" or "k8s-secrets"
	Region    string // AWS region (for aws-sm)
	Prefix    string // Secret name prefix, e.g. "clusters/" (for aws-sm)
	Namespace string // K8s namespace holding secrets (for k8s-secrets)
}

// NewSecretProvider creates the appropriate SecretProvider for the given config.
func NewSecretProvider(cfg Config) (SecretProvider, error) {
	switch cfg.Type {
	case "k8s-secrets", "kubernetes":
		return NewKubernetesSecretProvider(cfg)
	case "aws-sm", "aws-secrets-manager":
		return NewAWSSecretsManagerProvider(cfg)
	case "":
		return nil, fmt.Errorf("no secrets provider configured")
	default:
		return nil, fmt.Errorf("unknown provider type %q", cfg.Type)
	}
}

// New creates the appropriate ClusterCredentialsProvider for the given config.
func New(cfg Config) (ClusterCredentialsProvider, error) {
	switch cfg.Type {
	case "k8s-secrets", "kubernetes":
		return NewKubernetesSecretProvider(cfg)
	case "aws-sm", "aws-secrets-manager":
		return NewAWSSecretsManagerProvider(cfg)
	case "":
		return nil, fmt.Errorf("no secrets provider configured — configure provider in Settings or via API")
	default:
		return nil, fmt.Errorf("unknown provider type %q — valid options: aws-sm, k8s-secrets", cfg.Type)
	}
}
