package providers

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// KubernetesSecretProvider reads kubeconfigs from Kubernetes Secrets.
// Secret name = cluster name, data key = "kubeconfig".
type KubernetesSecretProvider struct {
	client    kubernetes.Interface
	namespace string
}

// NewKubernetesSecretProvider creates a provider that reads from K8s Secrets.
// Uses in-cluster config when running inside Kubernetes, falls back to default kubeconfig for local dev.
func NewKubernetesSecretProvider(cfg Config) (*KubernetesSecretProvider, error) {
	namespace := cfg.Namespace
	if namespace == "" {
		namespace = "sharko"
	}

	restCfg, err := rest.InClusterConfig()
	if err != nil {
		// Fall back to default kubeconfig (local dev)
		restCfg, err = clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
		if err != nil {
			return nil, fmt.Errorf("creating k8s config: %w", err)
		}
	}

	client, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("creating k8s client: %w", err)
	}

	return &KubernetesSecretProvider{client: client, namespace: namespace}, nil
}

// newKubernetesSecretProviderWithClient creates a provider with an injected client (for testing).
func newKubernetesSecretProviderWithClient(client kubernetes.Interface, namespace string) *KubernetesSecretProvider {
	return &KubernetesSecretProvider{client: client, namespace: namespace}
}

// GetSecretValue retrieves a raw secret value from a Kubernetes Secret.
// path has the form "namespace/secret-name/key". If namespace is omitted the
// provider's default namespace is used.
//
// Supported formats:
//   - "secret-name/key"              — uses provider namespace
//   - "namespace/secret-name/key"    — explicit namespace
func (p *KubernetesSecretProvider) GetSecretValue(ctx context.Context, path string) ([]byte, error) {
	slog.Debug("[provider] GetSecretValue called (k8s)", "path", path)
	parts := strings.Split(path, "/")
	var namespace, secretName, key string
	switch len(parts) {
	case 2:
		namespace = p.namespace
		secretName = parts[0]
		key = parts[1]
	case 3:
		namespace = parts[0]
		secretName = parts[1]
		key = parts[2]
	default:
		return nil, fmt.Errorf("invalid secret path %q: expected \"secret/key\" or \"namespace/secret/key\"", path)
	}

	secret, err := p.client.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting secret %q in namespace %q: %w", secretName, namespace, err)
	}
	val, ok := secret.Data[key]
	if !ok {
		return nil, fmt.Errorf("secret %q/%q has no key %q", namespace, secretName, key)
	}
	slog.Debug("[provider] GetSecretValue success (k8s)", "path", path, "size", len(val))
	return val, nil
}

// fetchK8sSecret retrieves and parses a kubeconfig from a Kubernetes Secret by exact name.
func (p *KubernetesSecretProvider) fetchK8sSecret(secretName string) (*Kubeconfig, error) {
	slog.Debug("[provider] fetching k8s secret", "namespace", p.namespace, "name", secretName)
	secret, err := p.client.CoreV1().Secrets(p.namespace).Get(context.Background(), secretName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting secret %q in namespace %q: %w", secretName, p.namespace, err)
	}

	raw, ok := secret.Data["kubeconfig"]
	if !ok {
		return nil, fmt.Errorf("secret %q has no 'kubeconfig' key", secretName)
	}
	slog.Info("[provider] k8s secret fetched", "name", secretName, "keys", len(secret.Data))

	kc := &Kubeconfig{Raw: raw}

	config, err := clientcmd.RESTConfigFromKubeConfig(raw)
	if err != nil {
		return nil, fmt.Errorf("parsing kubeconfig from secret %q: %w", secretName, err)
	}

	kc.Server = config.Host
	kc.CAData = config.TLSClientConfig.CAData
	kc.Token = config.BearerToken

	return kc, nil
}

// GetCredentials fetches credentials for the named cluster. It tries the exact
// secret name first; if not found it searches for secrets whose name contains
// the cluster name as a substring and returns them as suggestions.
func (p *KubernetesSecretProvider) GetCredentials(clusterName string) (*Kubeconfig, error) {
	slog.Info("[provider] GetCredentials called (k8s)", "cluster", clusterName)

	// Step 1: Try exact name.
	if kc, err := p.fetchK8sSecret(clusterName); err == nil {
		return kc, nil
	}

	// Step 2: Search for similar names and include them in the error.
	suggestions, searchErr := p.searchSimilarK8s(clusterName)
	if searchErr == nil && len(suggestions) > 0 {
		slog.Info("[provider] found similar secrets", "query", clusterName, "found", len(suggestions))
		return nil, fmt.Errorf("secret for cluster %q not found in namespace %q. Similar secrets: %s. "+
			"Set --secret-path to specify the exact secret name",
			clusterName, p.namespace, strings.Join(suggestions, ", "))
	}

	slog.Error("[provider] GetCredentials failed (k8s)", "cluster", clusterName, "step", "fetch", "error", "secret not found in namespace "+p.namespace)
	return nil, fmt.Errorf("secret for cluster %q not found in namespace %q. "+
		"Set --secret-path to specify the exact secret name", clusterName, p.namespace)
}

// searchSimilarK8s returns secret names in the provider namespace that contain
// query as a substring and have a 'kubeconfig' data key.
func (p *KubernetesSecretProvider) searchSimilarK8s(query string) ([]string, error) {
	secrets, err := p.client.CoreV1().Secrets(p.namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing secrets in namespace %q: %w", p.namespace, err)
	}

	var matches []string
	for _, s := range secrets.Items {
		if strings.Contains(s.Name, query) {
			if _, ok := s.Data["kubeconfig"]; ok {
				matches = append(matches, s.Name)
			}
		}
	}
	return matches, nil
}

// SearchSecrets returns secret names in the provider namespace that contain
// query as a substring. Delegates to the existing searchSimilarK8s method.
func (p *KubernetesSecretProvider) SearchSecrets(query string) ([]string, error) {
	return p.searchSimilarK8s(query)
}

func (p *KubernetesSecretProvider) ListClusters() ([]ClusterInfo, error) {
	secrets, err := p.client.CoreV1().Secrets(p.namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/managed-by=sharko",
	})
	if err != nil {
		return nil, fmt.Errorf("listing secrets in namespace %q: %w", p.namespace, err)
	}

	var clusters []ClusterInfo
	for _, s := range secrets.Items {
		if _, ok := s.Data["kubeconfig"]; !ok {
			continue
		}
		clusters = append(clusters, ClusterInfo{
			Name:   s.Name,
			Region: s.Labels["region"],
			Tags:   s.Labels,
		})
	}
	return clusters, nil
}
