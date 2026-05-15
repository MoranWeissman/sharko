package providers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// ArgoCDProvider reads cluster credentials from the ArgoCD-format Secret in the
// argocd namespace (the same Secret Sharko's reconciler maintains after V125-1-8).
//
// It implements ClusterCredentialsProvider for the bearer-token auth shape and
// returns stable, typed errors for the AWS-IAM and exec-plugin shapes so the
// API/UI layers (Stories 10.3 + 10.5) can dispatch on them via errors.Is.
//
// ArgoCDProvider does NOT implement SecretProvider — it is a credentials-only
// provider for cluster connectivity. Addon secret VALUE flow continues to use
// the existing SecretProvider implementations untouched.
type ArgoCDProvider struct {
	client    kubernetes.Interface
	namespace string
}

// argoCDClusterTypeSelector is the label selector used to find ArgoCD cluster
// secrets (matches what argocd-server itself looks for).
const argoCDClusterTypeSelector = "argocd.argoproj.io/secret-type=cluster"

// NewArgoCDProvider creates a provider that reads ArgoCD cluster Secrets from
// the namespace where ArgoCD itself is installed. Mirrors NewKubernetesSecretProvider:
// prefers in-cluster config, falls back to ~/.kube/config for local dev.
//
// IMPORTANT: cfg.Namespace is intentionally IGNORED here (V125-1-10.8). The
// ProviderConfig.Namespace field is shared by two orthogonal concerns:
//
//   - For the "k8s-secrets" backend it means "the K8s namespace where the
//     operator pre-populates kubeconfig Secrets" (default "sharko").
//   - For the "argocd" backend it would mean "the K8s namespace where ArgoCD
//     itself runs" (always "argocd" on standard installs).
//
// Reusing the same field for both leads to cross-contamination: a user who
// switches the UI dropdown from k8s-secrets to argocd ends up with a stale
// "sharko" value carried over, and ArgoCDProvider then looks in the wrong
// namespace. Until ProviderConfig is split into per-concern fields (scheduled
// for V125-1-11+), this constructor resolves the namespace independently:
//
//  1. SHARKO_ARGOCD_NAMESPACE env var (for non-standard ArgoCD installs)
//  2. hardcoded "argocd" (the standard install location)
func NewArgoCDProvider(cfg Config) (*ArgoCDProvider, error) {
	restCfg, err := rest.InClusterConfig()
	if err != nil {
		// Fall back to default kubeconfig (local dev).
		restCfg, err = clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
		if err != nil {
			return nil, fmt.Errorf("creating k8s config for argocd provider: %w", err)
		}
	}

	return NewArgoCDProviderWithRESTConfig(cfg, restCfg)
}

// NewArgoCDProviderWithRESTConfig creates a provider from an already-resolved
// *rest.Config. This is the seam used by the auto-default path in New() (see
// provider.go), which probes for in-cluster config via the mockable
// inClusterConfigFn — without this constructor, NewArgoCDProvider would
// re-probe rest.InClusterConfig directly, bypassing the test seam and forcing
// a ~/.kube/config fallback that doesn't exist in CI runners (V125-1-10.9).
//
// Callers that have NOT already obtained a *rest.Config should use
// NewArgoCDProvider, which retains the probe + kubeconfig-fallback behavior.
func NewArgoCDProviderWithRESTConfig(cfg Config, restCfg *rest.Config) (*ArgoCDProvider, error) {
	namespace := resolveArgoCDNamespace(cfg)

	client, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("creating k8s client for argocd provider: %w", err)
	}

	return &ArgoCDProvider{client: client, namespace: namespace}, nil
}

// newArgoCDProviderWithClient creates a provider with an injected client (for testing).
// Mirrors the newKubernetesSecretProviderWithClient pattern.
func newArgoCDProviderWithClient(client kubernetes.Interface, namespace string) *ArgoCDProvider {
	if namespace == "" {
		namespace = "argocd"
	}
	return &ArgoCDProvider{client: client, namespace: namespace}
}

// resolveArgoCDNamespace decides which K8s namespace the ArgoCDProvider should
// query, ignoring cfg.Namespace (see NewArgoCDProvider's docstring for why).
// Resolution order:
//
//  1. SHARKO_ARGOCD_NAMESPACE env var (for non-standard ArgoCD installs)
//  2. hardcoded "argocd" (the standard install location)
//
// Emits a one-shot WARN log when cross-contamination is detected
// (cfg.Namespace is non-empty AND not equal to the resolved namespace) so a
// future operator hitting the same gotcha sees exactly what happened.
func resolveArgoCDNamespace(cfg Config) string {
	resolved := os.Getenv("SHARKO_ARGOCD_NAMESPACE")
	if resolved == "" {
		resolved = "argocd"
	}

	if cfg.Namespace != "" && cfg.Namespace != resolved {
		slog.Warn("[provider] cfg.Namespace ignored for argocd type — argocd reads from resolved namespace (cross-contamination from prior k8s-secrets config; will be resolved when ProviderConfig is split in V125-1-11+)",
			"cfg_namespace", cfg.Namespace,
			"resolved_namespace", resolved,
		)
	}

	return resolved
}

// Stable wire-error codes returned in API responses (Story 10.3 will lift these
// into the JSON envelope; Story 10.5 will dispatch UI copy on them). DO NOT
// rename without bumping the API contract.
const (
	ArgoCDProviderCodeIAMRequired     = "argocd_provider_iam_required"
	ArgoCDProviderCodeExecUnsupported = "argocd_provider_exec_unsupported"
	ArgoCDProviderCodeUnsupportedAuth = "argocd_provider_unsupported_auth"
)

// Sentinel errors used by errors.Is matching at the API/UI layer.
var (
	// ErrArgoCDProviderIAMRequired is returned when the ArgoCD cluster Secret
	// uses awsAuthConfig — minting a token requires AWS IAM credentials on the
	// Sharko pod, which is out-of-scope for v1.x.
	ErrArgoCDProviderIAMRequired = errors.New("argocd cluster requires AWS IAM credentials")
	// ErrArgoCDProviderExecUnsupported is returned when the ArgoCD cluster
	// Secret uses execProviderConfig — Sharko does not support shelling out to
	// exec-plugin auth helpers in v1.x.
	ErrArgoCDProviderExecUnsupported = errors.New("argocd cluster uses exec-plugin auth (not supported in v1.x)")
	// ErrArgoCDProviderUnsupportedAuth is returned when the ArgoCD cluster
	// Secret config JSON parses but matches none of the three known auth shapes.
	ErrArgoCDProviderUnsupportedAuth = errors.New("argocd cluster has unrecognized auth shape")
)

// ArgoCDProviderError wraps an unsupported / unsuitable auth shape with enough
// context for API and UI layers to render an actionable message and a deep link
// to the matching docs page.
type ArgoCDProviderError struct {
	// Code is one of the ArgoCDProviderCode* constants (stable across versions).
	Code string
	// ClusterName is the cluster name from the secret's data["name"] field.
	ClusterName string
	// Server is the cluster's API server URL from data["server"].
	Server string
	// Detail is a human-readable detail string. Returned by Error().
	Detail string
}

// Error returns the human-readable detail. Use errors.Is(err, ErrArgoCDProvider*)
// to test for the routing branch in callers.
func (e *ArgoCDProviderError) Error() string {
	return e.Detail
}

// Is allows errors.Is(err, ErrArgoCDProvider*) to match any ArgoCDProviderError
// whose Code maps to the corresponding sentinel.
func (e *ArgoCDProviderError) Is(target error) bool {
	switch e.Code {
	case ArgoCDProviderCodeIAMRequired:
		return target == ErrArgoCDProviderIAMRequired
	case ArgoCDProviderCodeExecUnsupported:
		return target == ErrArgoCDProviderExecUnsupported
	case ArgoCDProviderCodeUnsupportedAuth:
		return target == ErrArgoCDProviderUnsupportedAuth
	}
	return false
}

// argoCDClusterConfig mirrors the JSON shape ArgoCD writes into the cluster
// Secret's data["config"] field. Only the subset Sharko inspects is modelled.
type argoCDClusterConfig struct {
	BearerToken        string                  `json:"bearerToken,omitempty"`
	AWSAuthConfig      *argoCDAWSAuthConfig    `json:"awsAuthConfig,omitempty"`
	ExecProviderConfig *argoCDExecProvider     `json:"execProviderConfig,omitempty"`
	TLSClientConfig    argoCDTLSClientConfig   `json:"tlsClientConfig"`
}

type argoCDAWSAuthConfig struct {
	ClusterName string `json:"clusterName,omitempty"`
	RoleARN     string `json:"roleARN,omitempty"`
}

type argoCDExecProvider struct {
	Command    string            `json:"command,omitempty"`
	Args       []string          `json:"args,omitempty"`
	APIVersion string            `json:"apiVersion,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
}

type argoCDTLSClientConfig struct {
	Insecure bool   `json:"insecure,omitempty"`
	CAData   string `json:"caData,omitempty"`
}

// listClusterSecrets returns all cluster-typed Secrets in the configured
// namespace. The list is filtered server-side by the ArgoCD secret-type label.
func (p *ArgoCDProvider) listClusterSecrets(ctx context.Context) (*corev1.SecretList, error) {
	return p.client.CoreV1().Secrets(p.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: argoCDClusterTypeSelector,
	})
}

// findClusterSecret returns the ArgoCD cluster Secret whose data["name"]
// matches clusterName. Falls back to data["server"] equality match if no
// name match is found. Returns a NotFound k8s error when neither matches.
func (p *ArgoCDProvider) findClusterSecret(ctx context.Context, clusterName string) (*corev1.Secret, error) {
	secrets, err := p.listClusterSecrets(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing argocd cluster secrets in namespace %q: %w", p.namespace, err)
	}

	for i := range secrets.Items {
		s := &secrets.Items[i]
		if string(s.Data["name"]) == clusterName {
			return s, nil
		}
	}
	// Fallback: try matching on data["server"] in case the caller passed a server URL.
	for i := range secrets.Items {
		s := &secrets.Items[i]
		if string(s.Data["server"]) == clusterName {
			return s, nil
		}
	}

	// Mirror the existing not-found shape: wrap a k8s NotFound so callers
	// (and apierrors.IsNotFound) can recognise it.
	return nil, apierrors.NewNotFound(
		corev1.Resource("secrets"),
		clusterName,
	)
}

// GetCredentials fetches credentials for the named cluster from the ArgoCD
// cluster Secret. Routes per detected auth shape:
//
//   - bearerToken non-empty       → returns ready-to-use *Kubeconfig
//   - awsAuthConfig non-nil        → returns ErrArgoCDProviderIAMRequired
//   - execProviderConfig non-nil   → returns ErrArgoCDProviderExecUnsupported
//   - none of the above            → returns ErrArgoCDProviderUnsupportedAuth
//
// The caller can dispatch via errors.Is(err, ErrArgoCDProvider*) and pull the
// stable Code from *ArgoCDProviderError via errors.As for API/UI surfacing.
func (p *ArgoCDProvider) GetCredentials(clusterName string) (*Kubeconfig, error) {
	slog.Info("[provider] GetCredentials called (argocd)", "cluster", clusterName, "namespace", p.namespace)

	secret, err := p.findClusterSecret(context.Background(), clusterName)
	if err != nil {
		slog.Error("[provider] argocd cluster secret not found", "cluster", clusterName, "namespace", p.namespace, "error", err)
		return nil, fmt.Errorf("argocd cluster secret for %q not found in namespace %q: %w", clusterName, p.namespace, err)
	}

	server := string(secret.Data["server"])
	storedName := string(secret.Data["name"])
	if storedName == "" {
		// Some ops tools omit the name field; fall back to the secret's K8s name.
		storedName = secret.Name
	}

	rawConfig, ok := secret.Data["config"]
	if !ok || len(rawConfig) == 0 {
		return nil, &ArgoCDProviderError{
			Code:        ArgoCDProviderCodeUnsupportedAuth,
			ClusterName: storedName,
			Server:      server,
			Detail:      fmt.Sprintf("argocd cluster secret for %q has no config field", storedName),
		}
	}

	var cfg argoCDClusterConfig
	if err := json.Unmarshal(rawConfig, &cfg); err != nil {
		return nil, fmt.Errorf("parsing argocd cluster config JSON for %q: %w", storedName, err)
	}

	switch {
	case cfg.BearerToken != "":
		return p.buildBearerTokenKubeconfig(storedName, server, cfg)

	case cfg.AWSAuthConfig != nil:
		slog.Info("[provider] argocd cluster uses awsAuthConfig — IAM credentials required",
			"cluster", storedName, "server", server, "roleARN", cfg.AWSAuthConfig.RoleARN)
		return nil, &ArgoCDProviderError{
			Code:        ArgoCDProviderCodeIAMRequired,
			ClusterName: storedName,
			Server:      server,
			Detail: fmt.Sprintf("cluster %q uses AWS IAM authentication (awsAuthConfig). "+
				"Configure AWS credentials for the Sharko pod's role to enable connectivity tests.",
				storedName),
		}

	case cfg.ExecProviderConfig != nil:
		slog.Info("[provider] argocd cluster uses execProviderConfig — exec-plugin auth not supported in v1.x",
			"cluster", storedName, "server", server, "command", cfg.ExecProviderConfig.Command)
		return nil, &ArgoCDProviderError{
			Code:        ArgoCDProviderCodeExecUnsupported,
			ClusterName: storedName,
			Server:      server,
			Detail: fmt.Sprintf("cluster %q uses exec-plugin auth (command %q). "+
				"Exec plugins are not supported in v1.x; tracked for v2.",
				storedName, cfg.ExecProviderConfig.Command),
		}

	default:
		return nil, &ArgoCDProviderError{
			Code:        ArgoCDProviderCodeUnsupportedAuth,
			ClusterName: storedName,
			Server:      server,
			Detail: fmt.Sprintf("argocd cluster secret for %q has no recognised auth shape "+
				"(expected bearerToken, awsAuthConfig, or execProviderConfig)", storedName),
		}
	}
}

// buildBearerTokenKubeconfig constructs a *Kubeconfig from the bearerToken
// shape. CAData is base64-decoded from tlsClientConfig.caData (or left empty
// when insecure:true). The Raw field is a synthesized kubeconfig YAML that
// round-trips through clientcmd.RESTConfigFromKubeConfig cleanly so downstream
// consumers (remoteclient.NewClientFromKubeconfig) can use it as-is.
func (p *ArgoCDProvider) buildBearerTokenKubeconfig(name, server string, cfg argoCDClusterConfig) (*Kubeconfig, error) {
	if server == "" {
		return nil, fmt.Errorf("argocd cluster secret for %q has empty server URL", name)
	}

	var caBytes []byte
	if cfg.TLSClientConfig.CAData != "" {
		decoded, err := base64.StdEncoding.DecodeString(cfg.TLSClientConfig.CAData)
		if err != nil {
			return nil, fmt.Errorf("decoding tlsClientConfig.caData for cluster %q: %w", name, err)
		}
		caBytes = decoded
	}

	// Synthesize a minimal kubeconfig YAML. Use the original base64 string for
	// certificate-authority-data (kubeconfig spec requires base64), and the
	// `insecure-skip-tls-verify` flag when the Secret declares insecure:true.
	//
	// The synthesized YAML is constructed so RESTConfigFromKubeConfig parses it
	// and yields a config with Host==server, BearerToken set, and CAData set
	// (when not insecure) — verified by tests.
	var kubeconfigYAML string
	switch {
	case cfg.TLSClientConfig.Insecure:
		kubeconfigYAML = fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: %s
    insecure-skip-tls-verify: true
  name: %s
contexts:
- context:
    cluster: %s
    user: %s
  name: %s
current-context: %s
users:
- name: %s
  user:
    token: %s
`, server, name, name, name, name, name, name, cfg.BearerToken)
	case cfg.TLSClientConfig.CAData != "":
		kubeconfigYAML = fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: %s
    certificate-authority-data: %s
  name: %s
contexts:
- context:
    cluster: %s
    user: %s
  name: %s
current-context: %s
users:
- name: %s
  user:
    token: %s
`, server, cfg.TLSClientConfig.CAData, name, name, name, name, name, name, cfg.BearerToken)
	default:
		// No CA data and not insecure — system trust roots will be used by callers.
		kubeconfigYAML = fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: %s
  name: %s
contexts:
- context:
    cluster: %s
    user: %s
  name: %s
current-context: %s
users:
- name: %s
  user:
    token: %s
`, server, name, name, name, name, name, name, cfg.BearerToken)
	}

	// Verify the synthesized YAML round-trips through clientcmd. This is the
	// same call downstream consumers make; failing here surfaces the bug at
	// fetch time instead of inside remoteclient.
	if _, err := clientcmd.RESTConfigFromKubeConfig([]byte(kubeconfigYAML)); err != nil {
		return nil, fmt.Errorf("synthesized kubeconfig for cluster %q failed to parse: %w", name, err)
	}

	slog.Info("[provider] argocd bearer-token kubeconfig built",
		"cluster", name, "server", server,
		"hasCA", len(caBytes) > 0, "insecure", cfg.TLSClientConfig.Insecure)

	return &Kubeconfig{
		Raw:    []byte(kubeconfigYAML),
		Server: server,
		CAData: caBytes,
		Token:  cfg.BearerToken,
	}, nil
}

// ListClusters returns every ArgoCD cluster Secret in the configured namespace
// as a ClusterInfo. The Region field is populated from the well-known
// `region` label when present; Tags carries the full label set for callers
// that want richer context.
func (p *ArgoCDProvider) ListClusters() ([]ClusterInfo, error) {
	secrets, err := p.listClusterSecrets(context.Background())
	if err != nil {
		return nil, fmt.Errorf("listing argocd cluster secrets in namespace %q: %w", p.namespace, err)
	}

	clusters := make([]ClusterInfo, 0, len(secrets.Items))
	for i := range secrets.Items {
		s := &secrets.Items[i]
		name := string(s.Data["name"])
		if name == "" {
			name = s.Name
		}
		clusters = append(clusters, ClusterInfo{
			Name:   name,
			Region: s.Labels["region"],
			Tags:   s.Labels,
		})
	}
	return clusters, nil
}

// SearchSecrets returns Secret names (in the configured namespace, scoped to
// ArgoCD cluster-type secrets) that contain query as a substring. Mirrors the
// searchSimilarK8s pattern in KubernetesSecretProvider.
func (p *ArgoCDProvider) SearchSecrets(query string) ([]string, error) {
	secrets, err := p.listClusterSecrets(context.Background())
	if err != nil {
		return nil, fmt.Errorf("listing argocd cluster secrets in namespace %q: %w", p.namespace, err)
	}

	var matches []string
	for _, s := range secrets.Items {
		if strings.Contains(s.Name, query) {
			matches = append(matches, s.Name)
		}
	}
	return matches, nil
}

// HealthCheck confirms the provider can list ArgoCD cluster Secrets. Uses
// Limit:1 to avoid pulling the full list when the namespace is large.
func (p *ArgoCDProvider) HealthCheck(ctx context.Context) error {
	limit := int64(1)
	_, err := p.client.CoreV1().Secrets(p.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: argoCDClusterTypeSelector,
		Limit:         limit,
	})
	if err != nil {
		return fmt.Errorf("argocd provider health check failed: %w", err)
	}
	return nil
}
