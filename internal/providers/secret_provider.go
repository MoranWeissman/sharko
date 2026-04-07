package providers

import (
	"context"
	"fmt"
)

// SecretProvider abstracts fetching raw secret values from a backend.
// Paths are provider-specific references (e.g., "secrets/datadog/api-key" for AWS SM,
// "datadog-keys/api-key" for K8s Secrets).
type SecretProvider interface {
	GetSecretValue(ctx context.Context, path string) ([]byte, error)
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
