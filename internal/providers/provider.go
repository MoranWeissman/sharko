package providers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"k8s.io/client-go/rest"
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
	// SearchSecrets returns secret names that contain query as a substring.
	// Used by the UI to suggest correct secret paths when GetCredentials fails.
	// Implementations that don't support search should return (nil, nil).
	SearchSecrets(query string) ([]string, error)
	// HealthCheck performs a lightweight connectivity check — enough to confirm
	// the provider credentials work, without enumerating all secrets.
	// Returns nil on success, an error describing the failure otherwise.
	HealthCheck(ctx context.Context) error
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
//
// Type values (case-sensitive):
//   - "argocd"                                 — default in v1.25+ when in-cluster; reads from ArgoCD cluster Secrets in argocd namespace
//   - "k8s-secrets" / "kubernetes"             — reads kubeconfigs from K8s Secrets (deprecated for cluster creds in v1.25; remove in V125-2)
//   - "aws-sm" / "aws-secrets-manager"         — AWS Secrets Manager
//   - "gcp" / "gcp-sm" / "google-secret-manager" — GCP Secret Manager
//   - "azure" / "azure-kv" / "azure-key-vault" — Azure Key Vault
//   - ""                                       — auto-default: returns "argocd" when running in-cluster (rest.InClusterConfig succeeds), else returns the legacy "no provider configured" error so out-of-cluster dev installs still surface the existing actionable message
type Config struct {
	Type      string // see doc comment for valid values
	Region    string // AWS region (for aws-sm)
	Prefix    string // Secret name prefix, e.g. "clusters/" (for aws-sm)
	Namespace string // K8s namespace holding secrets (for k8s-secrets / argocd)
	RoleARN   string // default IAM role to assume for EKS token generation (aws-sm only)
}

// inClusterConfigFn is indirection over rest.InClusterConfig so tests can
// simulate "running in-cluster" / "running outside" without mutating the
// KUBERNETES_SERVICE_HOST env var (which would race other tests in the same
// binary). The auto-default branch in New() reads through this var.
var inClusterConfigFn = rest.InClusterConfig

// NewSecretProvider creates the appropriate SecretProvider for the given config.
//
// Note: Type "argocd" is REJECTED here on purpose. ArgoCDProvider is a
// cluster-credentials-only provider (it supplies kubeconfigs from ArgoCD
// cluster Secrets); it does NOT serve addon secret VALUES. Per the V125-1-10
// design (OQ #2), addon secret values must continue to come from a real
// secrets backend (vault / aws-sm / k8s-secrets / gcp-sm / azure-kv).
func NewSecretProvider(cfg Config) (SecretProvider, error) {
	switch cfg.Type {
	case "k8s-secrets", "kubernetes":
		return NewKubernetesSecretProvider(cfg)
	case "aws-sm", "aws-secrets-manager":
		return NewAWSSecretsManagerProvider(cfg)
	case "gcp", "gcp-sm", "google-secret-manager":
		return NewGCPSecretManagerProvider(cfg)
	case "azure", "azure-kv", "azure-key-vault":
		return NewAzureKeyVaultProvider(cfg)
	case "argocd":
		return nil, fmt.Errorf("argocd provider is cluster-credentials-only; configure a separate SecretProvider (vault, aws-sm, k8s-secrets, gcp-sm, azure-kv) for addon secret values")
	case "":
		return nil, fmt.Errorf("no secrets provider configured")
	default:
		return nil, fmt.Errorf("unknown provider type %q", cfg.Type)
	}
}

// New creates the appropriate ClusterCredentialsProvider for the given config.
//
// Auto-default behavior (V125-1-10.2): when cfg.Type is empty, New() probes for
// in-cluster K8s access via inClusterConfigFn (rest.InClusterConfig). On
// success, it returns an ArgoCDProvider so dev installs work out of the box
// without an explicit provider configured. When the probe returns
// rest.ErrNotInCluster (running outside K8s), the legacy "no secrets provider
// configured" error is preserved verbatim so existing out-of-cluster callers
// still get an actionable message. Other probe errors (malformed in-cluster
// config) are surfaced as today.
func New(cfg Config) (ClusterCredentialsProvider, error) {
	switch cfg.Type {
	case "k8s-secrets", "kubernetes":
		return NewKubernetesSecretProvider(cfg)
	case "aws-sm", "aws-secrets-manager":
		return NewAWSSecretsManagerProvider(cfg)
	case "gcp", "gcp-sm", "google-secret-manager":
		return NewGCPSecretManagerProvider(cfg)
	case "azure", "azure-kv", "azure-key-vault":
		return NewAzureKeyVaultProvider(cfg)
	case "argocd":
		return NewArgoCDProvider(cfg)
	case "":
		// Auto-default: argocd when in-cluster, legacy error otherwise.
		// We capture the *rest.Config returned by the probe and pass it through
		// to NewArgoCDProviderWithRESTConfig so the test seam (inClusterConfigFn)
		// flows end-to-end. Calling NewArgoCDProvider here would re-probe
		// rest.InClusterConfig directly and bypass the seam (V125-1-10.9).
		if restCfg, err := inClusterConfigFn(); err == nil {
			slog.Info("[provider] auto-defaulting to argocd (no provider configured + in-cluster K8s detected)", "namespace", "argocd")
			return NewArgoCDProviderWithRESTConfig(cfg, restCfg)
		} else if !errors.Is(err, rest.ErrNotInCluster) {
			// Distinguishable from "not in cluster": the in-cluster probe
			// failed for some other reason (bad SA token, malformed config).
			// Surface it so operators can fix it instead of silently falling
			// back to the legacy error.
			return nil, fmt.Errorf("auto-default provider probe failed: %w", err)
		}
		return nil, fmt.Errorf("no secrets provider configured — configure provider in Settings or via API")
	default:
		return nil, fmt.Errorf("unknown provider type %q — valid options: argocd, aws-sm, k8s-secrets, gcp-sm, azure-kv", cfg.Type)
	}
}
