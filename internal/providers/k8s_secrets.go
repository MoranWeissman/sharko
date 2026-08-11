package providers

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/MoranWeissman/sharko/internal/credsafe"
)

// KubernetesSecretProvider reads kubeconfigs from Kubernetes Secrets.
// Secret name = cluster name, data key = "kubeconfig".
type KubernetesSecretProvider struct {
	client    kubernetes.Interface
	namespace string
}

// newKubernetesSecretProviderForNamespace is the shared builder behind both
// public constructors. Uses in-cluster config when running inside Kubernetes,
// falls back to default kubeconfig for local dev. Empty namespace defaults to
// "sharko" (the provider convention: cluster kubeconfig Secrets live in the
// Sharko namespace, secret name = cluster name, data key "kubeconfig").
func newKubernetesSecretProviderForNamespace(namespace string) (*KubernetesSecretProvider, error) {
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

// NewKubernetesSecretProviderFromAddonConfig creates a provider that reads from
// K8s Secrets from the canonical AddonSecretProviderConfig.
//
// Only AddonSecretProviderConfig.Namespace is read — Type is consumed by the
// upstream dispatcher (NewAddonSecretProvider) and Region/Prefix/RoleARN are
// AWS-SM specific and ignored here.
func NewKubernetesSecretProviderFromAddonConfig(cfg AddonSecretProviderConfig) (*KubernetesSecretProvider, error) {
	return newKubernetesSecretProviderForNamespace(cfg.Namespace)
}

// NewKubernetesSecretProviderFromClusterTestConfig creates a provider that
// reads cluster kubeconfig Secrets from the canonical ClusterTestProviderConfig
// — the cluster-credentials arm restored by V2-cleanup-53.1 so registrations
// with creds_source=secret-kubeconfig reach the configured K8s namespace.
//
// Only ClusterTestProviderConfig.Namespace is read (default "sharko") — NOT
// ArgoCDNamespace, which belongs to the argocd backend and must stay isolated
// (V125-1-10.8 cross-contamination guard).
func NewKubernetesSecretProviderFromClusterTestConfig(cfg ClusterTestProviderConfig) (*KubernetesSecretProvider, error) {
	return newKubernetesSecretProviderForNamespace(cfg.Namespace)
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
//   - "namespace/secret-name/key"    — explicit namespace, and it must be
//     the SAME namespace the provider is configured for
//
// Boundary (task #152 story B): the configured namespace is the whole
// area this connection may read. The explicit-namespace form is still
// accepted as spelled-out clarity, but it can no longer point anywhere
// else — a path naming a different namespace is refused BEFORE any
// Kubernetes API call, so the read never happens. The check lives here in
// the provider so every trigger (the scheduled reconciler, "refresh now",
// the doctor) inherits it without asking.
func (p *KubernetesSecretProvider) GetSecretValue(ctx context.Context, path string) ([]byte, error) {
	slog.Debug("[provider] GetSecretValue called (k8s)", "path", path)
	if p.namespace == "" {
		// Fail closed: the public constructors always default the
		// namespace, so this only fires for a construction path that
		// skipped the default — refuse rather than read from an
		// unpredictable namespace.
		slog.Warn("[provider] GetSecretValue refused (k8s): no namespace configured", "path", path)
		return nil, k8sNoNamespaceRefusal(path)
	}
	parts := strings.Split(path, "/")
	var namespace, secretName, key string
	switch len(parts) {
	case 2:
		namespace = p.namespace
		secretName = parts[0]
		key = parts[1]
	case 3:
		if parts[0] != p.namespace {
			slog.Warn("[provider] GetSecretValue refused (k8s): path outside the configured namespace", "path", path, "namespace", p.namespace)
			return nil, k8sOutsideNamespaceRefusal(path, parts[0], p.namespace)
		}
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
	slog.Debug("[provider] GetSecretValue success (k8s)", "path", path)
	return val, nil
}

// fetchK8sSecret retrieves and parses a kubeconfig from a Kubernetes Secret by exact name.
func (p *KubernetesSecretProvider) fetchK8sSecret(secretName string) (*Kubeconfig, error) {
	slog.Debug("[provider] fetching k8s secret", "namespace", p.namespace, "name", secretName)
	secret, err := p.client.CoreV1().Secrets(p.namespace).Get(context.Background(), secretName, metav1.GetOptions{})
	if err != nil {
		wrapped := fmt.Errorf("getting secret %q in namespace %q: %w", secretName, p.namespace, err)
		// THIS is where "the Secret is not there" is actually KNOWN — the
		// Kubernetes API said so and apierrors reads its typed Status, not its
		// words. Recorded as a marker so the cluster-test handler can offer
		// secret-name suggestions without any substring matching. A Forbidden,
		// a timeout or an unreachable API server deliberately does NOT get the
		// marker: Sharko could not look, which is not the same as absent.
		if apierrors.IsNotFound(err) {
			return nil, credsafe.MarkNotFound(wrapped)
		}
		return nil, wrapped
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
	// Carry the client cert pair for cert-based kubeconfigs (kind / kubeadm /
	// on-prem). Only a complete pair is propagated — a half pair would never
	// take the cert branch in the ArgoCD secret writers anyway (V2-cleanup-56.1).
	if len(config.TLSClientConfig.CertData) > 0 && len(config.TLSClientConfig.KeyData) > 0 {
		kc.CertData = config.TLSClientConfig.CertData
		kc.KeyData = config.TLSClientConfig.KeyData
	}

	return kc, nil
}

// GetCredentials fetches credentials for the named cluster. It tries the exact
// secret name first; if not found it searches for secrets whose name contains
// the cluster name as a substring and returns them as suggestions.
// credsafe.Mark is applied at the boundary — see the ArgoCDProvider's
// GetCredentials for why every return goes through one place. After Mark the
// error's Error() is the fixed safe sentence; the one thing a caller can still
// learn from it is credsafe.IsNotFound, set only when the Kubernetes API
// actually said the Secret does not exist.
func (p *KubernetesSecretProvider) GetCredentials(clusterName string) (*Kubeconfig, error) {
	kc, err := p.getCredentials(clusterName)
	return kc, credsafe.Mark(err)
}

func (p *KubernetesSecretProvider) getCredentials(clusterName string) (*Kubeconfig, error) {
	slog.Info("[provider] GetCredentials called (k8s)", "cluster", clusterName)

	// Step 1: Try exact name.
	kc, fetchErr := p.fetchK8sSecret(clusterName)
	if fetchErr == nil {
		return kc, nil
	}
	// fetchK8sSecret already marked a genuine Kubernetes NotFound. Anything
	// else — Forbidden, a timeout, a Secret that exists but has no kubeconfig
	// key, an unparseable kubeconfig — is NOT "missing", and must not end up
	// offering the operator a list of names to pick from.
	missing := credsafe.IsNotFound(fetchErr)

	// Step 2: Search for similar names and include them in the error.
	suggestions, searchErr := p.searchSimilarK8s(clusterName)
	if searchErr == nil && len(suggestions) > 0 {
		slog.Info("[provider] found similar secrets", "query", clusterName, "found", len(suggestions))
		withSuggestions := fmt.Errorf("secret for cluster %q not found in namespace %q. Similar secrets: %s. "+
			"Set --secret-path to specify the exact secret name",
			clusterName, p.namespace, strings.Join(suggestions, ", "))
		if missing {
			return nil, credsafe.MarkNotFound(withSuggestions)
		}
		return nil, withSuggestions
	}

	slog.Error("[provider] GetCredentials failed (k8s)", "cluster", clusterName, "namespace", p.namespace,
		"step", "fetch", "missing", missing)
	failure := fmt.Errorf("secret for cluster %q not found in namespace %q. "+
		"Set --secret-path to specify the exact secret name", clusterName, p.namespace)
	if missing {
		return nil, credsafe.MarkNotFound(failure)
	}
	return nil, failure
}

// StoredConnectionFacts reports what this backend has stored for the named
// cluster, without minting anything.
//
// Nothing in this provider ever mints: a Kubernetes Secret holds a whole
// kubeconfig with a fixed credential in it, so reading and parsing it is the
// whole job. The method exists so this backend can serve the read-only
// connection comparison through the same never-minting capability the AWS
// Secrets Manager backend does, instead of the comparison having to know which
// backend it is talking to.
func (p *KubernetesSecretProvider) StoredConnectionFacts(lookupKey string) (*StoredConnectionFacts, error) {
	kc, err := p.fetchK8sSecret(lookupKey)
	if err != nil {
		return nil, credsafe.Mark(err)
	}
	return &StoredConnectionFacts{
		Server:   kc.Server,
		CAData:   kc.CAData,
		Token:    kc.Token,
		CertData: kc.CertData,
		KeyData:  kc.KeyData,
	}, nil
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

// HealthCheck confirms Kubernetes Secret access by listing at most one secret
// in the provider namespace with the managed-by-sharko label selector.
func (p *KubernetesSecretProvider) HealthCheck(ctx context.Context) error {
	limit := int64(1)
	_, err := p.client.CoreV1().Secrets(p.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/managed-by=sharko",
		Limit:         limit,
	})
	if err != nil {
		return credsafe.Mark(fmt.Errorf("Kubernetes Secrets health check failed: %w", err))
	}
	return nil
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
