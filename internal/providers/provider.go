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
	Token  string // Bearer token, if present
	// CertData / KeyData carry the client certificate + key pair (raw PEM
	// bytes) for cert-based kubeconfigs (kind / kubeadm / on-prem). The
	// ArgoCD secret writers need these to emit the plain-TLS cluster secret
	// shape instead of misclassifying the cluster as EKS exec
	// (V2-cleanup-56.1). Both fields are set together or not at all.
	CertData []byte // Client certificate data (PEM)
	KeyData  []byte // Client key data (PEM)
}

// ClusterInfo is a lightweight cluster descriptor from the secrets backend.
type ClusterInfo struct {
	Name   string
	Region string
	Tags   map[string]string
}

// inClusterConfigFn is indirection over rest.InClusterConfig so tests can
// simulate "running in-cluster" / "running outside" without mutating the
// KUBERNETES_SERVICE_HOST env var (which would race other tests in the same
// binary). The auto-default branch in NewClusterTestProvider reads through
// this var.
var inClusterConfigFn = rest.InClusterConfig

// NewAddonSecretProvider creates the appropriate SecretProvider for the
// given AddonSecretProviderConfig. This is the canonical factory for
// addon-secret backends — it consumes the typed config so the compiler
// enforces single-mechanism scope (no cross-contamination between
// addon-secret and cluster-test).
//
// Note: Type "argocd" is REJECTED on purpose. ArgoCDProvider is a
// cluster-credentials-only provider; it does NOT serve addon-secret VALUES.
// For cluster-credentials (cluster-test), use NewClusterTestProvider.
func NewAddonSecretProvider(cfg AddonSecretProviderConfig) (SecretProvider, error) {
	switch cfg.Type {
	case "k8s-secrets", "kubernetes":
		return NewKubernetesSecretProviderFromAddonConfig(cfg)
	case "aws-sm", "aws-secrets-manager":
		return NewAWSSecretsManagerProviderFromAddonConfig(cfg)
	case "gcp", "gcp-sm", "google-secret-manager":
		return NewGCPSecretManagerProviderFromAddonConfig(cfg)
	case "azure", "azure-kv", "azure-key-vault":
		return NewAzureKeyVaultProviderFromAddonConfig(cfg)
	case "argocd":
		return nil, fmt.Errorf("argocd provider is cluster-credentials-only; configure a separate SecretProvider (vault, aws-sm, k8s-secrets, gcp-sm, azure-kv) for addon secret values")
	case "":
		return nil, fmt.Errorf("no secrets provider configured")
	default:
		return nil, fmt.Errorf("unknown provider type %q", cfg.Type)
	}
}

// NewClusterTestProvider creates the appropriate
// ClusterCredentialsProvider for the given ClusterTestProviderConfig.
// This is the canonical factory for cluster-connectivity-test backends
// — it consumes the typed config so the compiler enforces
// single-mechanism scope.
//
// Auto-default behavior: when cfg.Type is empty, NewClusterTestProvider
// probes for in-cluster K8s access via inClusterConfigFn. On success it
// returns an ArgoCDProvider so dev installs work out of the box. When
// the probe returns rest.ErrNotInCluster, the "no secrets provider
// configured" error is returned so out-of-cluster callers get an
// actionable message.
//
// Accepted types: argocd, aws-sm, k8s-secrets, and "" (auto-default).
// The aws-sm / k8s-secrets cluster-credentials arms were retired in the
// V125-1-10.x redesign and restored in V2-cleanup-53.1 — without them,
// registrations with creds_source=secret-kubeconfig / eks-token could
// never reach the configured secret backend. gcp-sm / azure-kv stay
// retired for cluster credentials; addon-secret consumers of every
// backend remain fully functional via NewAddonSecretProvider.
func NewClusterTestProvider(cfg ClusterTestProviderConfig) (ClusterCredentialsProvider, error) {
	switch cfg.Type {
	case "argocd":
		return NewArgoCDProviderFromConfig(cfg)
	case "aws-sm", "aws-secrets-manager":
		return NewAWSSecretsManagerProviderFromClusterTestConfig(cfg)
	case "k8s-secrets", "kubernetes":
		return NewKubernetesSecretProviderFromClusterTestConfig(cfg)
	case "":
		// Auto-default: argocd when in-cluster, legacy error otherwise.
		// We capture the *rest.Config returned by the probe and pass it through
		// to NewArgoCDProviderWithRESTConfigFromConfig so the test seam
		// (inClusterConfigFn) flows end-to-end. Calling NewArgoCDProviderFromConfig
		// here would re-probe rest.InClusterConfig directly and bypass the seam
		// directly and bypass the test seam.
		if restCfg, err := inClusterConfigFn(); err == nil {
			slog.Info("[provider] auto-defaulting to argocd (no provider configured + in-cluster K8s detected)", "namespace", "argocd")
			return NewArgoCDProviderWithRESTConfigFromConfig(cfg, restCfg)
		} else if !errors.Is(err, rest.ErrNotInCluster) {
			// Distinguishable from "not in cluster": the in-cluster probe
			// failed for some other reason (bad SA token, malformed config).
			// Surface it so operators can fix it instead of silently falling
			// back to the legacy error.
			return nil, fmt.Errorf("auto-default provider probe failed: %w", err)
		}
		return nil, fmt.Errorf("no secrets provider configured — configure provider in Settings or via API")
	default:
		return nil, fmt.Errorf("unknown cluster-test provider type %q — valid options: argocd, aws-sm, k8s-secrets, \"\" (auto-default)", cfg.Type)
	}
}
