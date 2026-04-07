package providers

import (
	"context"
	"fmt"
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

func (p *KubernetesSecretProvider) GetCredentials(clusterName string) (*Kubeconfig, error) {
	secret, err := p.client.CoreV1().Secrets(p.namespace).Get(context.Background(), clusterName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting secret %q in namespace %q: %w", clusterName, p.namespace, err)
	}

	raw, ok := secret.Data["kubeconfig"]
	if !ok {
		return nil, fmt.Errorf("secret %q has no 'kubeconfig' key", clusterName)
	}

	kc := &Kubeconfig{Raw: raw}

	// Parse the kubeconfig to extract server URL, CA data, and token
	config, err := clientcmd.RESTConfigFromKubeConfig(raw)
	if err != nil {
		return nil, fmt.Errorf("parsing kubeconfig from secret %q: %w", clusterName, err)
	}

	kc.Server = config.Host
	kc.CAData = config.TLSClientConfig.CAData
	kc.Token = config.BearerToken

	return kc, nil
}

// GetSecretValue reads a secret value from a K8s Secret.
// Path format: "<secret-name>/<key>" — reads the specified key from the named Secret.
func (p *KubernetesSecretProvider) GetSecretValue(ctx context.Context, path string) ([]byte, error) {
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid k8s secret path %q — expected <secret-name>/<key>", path)
	}
	secretName, key := parts[0], parts[1]

	secret, err := p.client.CoreV1().Secrets(p.namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting secret %s/%s: %w", p.namespace, secretName, err)
	}

	val, ok := secret.Data[key]
	if !ok {
		return nil, fmt.Errorf("key %q not found in secret %s/%s", key, p.namespace, secretName)
	}
	return val, nil
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
