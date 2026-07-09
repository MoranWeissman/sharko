package providers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"regexp"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// ArgoCDProvider reads cluster credentials from the ArgoCD-format Secret
// in the argocd namespace (the same Secret Sharko's reconciler
// maintains).
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

	// eksTokenFn is the per-instance test seam over getEKSToken (STS
	// presigned-URL token minting). Defaulted to getEKSToken in every
	// constructor; overridden in unit tests so the AWS-auth-shape mint
	// path (V2-cleanup-88.2) can be exercised without real AWS credentials.
	// Mirrors the AWSSecretsManagerProvider.eksTokenFn seam in aws_sm.go.
	eksTokenFn func(ctx context.Context, clusterName, region, roleARN string) (string, error)
}

// argoCDClusterTypeSelector is the label selector used to find ArgoCD cluster
// secrets (matches what argocd-server itself looks for).
const argoCDClusterTypeSelector = "argocd.argoproj.io/secret-type=cluster"

// NewArgoCDProviderFromConfig creates a provider that reads ArgoCD
// cluster Secrets from the namespace specified by cfg.ArgoCDNamespace.
// Mirrors NewKubernetesSecretProvider: prefers in-cluster config, falls
// back to ~/.kube/config for local dev. The typed
// ClusterTestProviderConfig.ArgoCDNamespace field carries the namespace
// value through the type system — cross-contamination from the
// addon-secrets k8s-secrets backend is impossible by construction.
func NewArgoCDProviderFromConfig(cfg ClusterTestProviderConfig) (*ArgoCDProvider, error) {
	restCfg, err := rest.InClusterConfig()
	if err != nil {
		// Fall back to default kubeconfig (local dev).
		restCfg, err = clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
		if err != nil {
			return nil, fmt.Errorf("creating k8s config for argocd provider: %w", err)
		}
	}

	return NewArgoCDProviderWithRESTConfigFromConfig(cfg, restCfg)
}

// NewArgoCDProviderWithRESTConfigFromConfig creates a provider from an
// already-resolved *rest.Config and a typed ClusterTestProviderConfig.
// This is the seam used by the auto-default path in New() (see
// provider.go), which probes for in-cluster config via the mockable
// inClusterConfigFn — without this constructor, the typed factory
// would re-probe rest.InClusterConfig directly, bypassing the test
// seam and forcing a ~/.kube/config fallback that doesn't exist in
// CI runners.
//
// Callers that have NOT already obtained a *rest.Config should use
// NewArgoCDProviderFromConfig, which retains the probe + kubeconfig-fallback
// behavior.
func NewArgoCDProviderWithRESTConfigFromConfig(cfg ClusterTestProviderConfig, restCfg *rest.Config) (*ArgoCDProvider, error) {
	namespace := resolveArgoCDNamespaceTyped(cfg)

	client, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("creating k8s client for argocd provider: %w", err)
	}

	return &ArgoCDProvider{client: client, namespace: namespace, eksTokenFn: getEKSToken}, nil
}

// newArgoCDProviderWithClient creates a provider with an injected client (for testing).
// Mirrors the newKubernetesSecretProviderWithClient pattern. eksTokenFn defaults to
// the real getEKSToken so production-shaped construction paths stay correct; tests
// that exercise the AWS-mint path override provider.eksTokenFn directly (same
// package, unexported field).
func newArgoCDProviderWithClient(client kubernetes.Interface, namespace string) *ArgoCDProvider {
	if namespace == "" {
		namespace = "argocd"
	}
	return &ArgoCDProvider{client: client, namespace: namespace, eksTokenFn: getEKSToken}
}

// resolveArgoCDNamespaceTyped decides which K8s namespace the
// ArgoCDProvider should query. Precedence:
//
//  1. cfg.ArgoCDNamespace (non-empty) — typed canonical source from the
//     ClusterTestProviderConfig.
//  2. SHARKO_ARGOCD_NAMESPACE env var (non-empty) — DEPRECATED compat
//     alias; emits slog.Warn so operators see they're on the legacy
//     path. Scheduled for removal in the next minor release.
//  3. hardcoded "argocd" — the standard ArgoCD install location.
func resolveArgoCDNamespaceTyped(cfg ClusterTestProviderConfig) string {
	if cfg.ArgoCDNamespace != "" {
		return cfg.ArgoCDNamespace
	}
	if env := os.Getenv("SHARKO_ARGOCD_NAMESPACE"); env != "" {
		slog.Warn("[provider] SHARKO_ARGOCD_NAMESPACE env var is deprecated — set ClusterTestProviderConfig.ArgoCDNamespace (or clusterTest.argocdNamespace in Helm values) instead; env var removed in v1.26",
			"env_value", env,
		)
		return env
	}
	return "argocd"
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
	// uses awsAuthConfig, or a recognized AWS exec-plugin shape
	// (execProviderConfig with command argocd-k8s-auth/aws or
	// aws-iam-authenticator), and Sharko cannot mint a usable EKS token with
	// its own AWS identity — either because no region could be determined or
	// because the mint attempt itself failed (no IRSA/Pod Identity/instance
	// role, insufficient STS permissions, etc.). Sharko always parses these
	// shapes and attempts to mint with its own identity first (V2-cleanup-88.2)
	// — this error means that attempt did not produce usable credentials, not
	// that the shape was rejected outright.
	ErrArgoCDProviderIAMRequired = errors.New("argocd cluster requires AWS IAM credentials")
	// ErrArgoCDProviderExecUnsupported is returned when the ArgoCD cluster
	// Secret uses execProviderConfig with a command Sharko does not recognize
	// as an AWS authenticator. Sharko never shells out to exec-plugin binaries
	// — only the two known AWS shapes (argocd-k8s-auth aws, aws-iam-authenticator)
	// are parsed and minted; every other command (gcloud, az, custom scripts,
	// ...) stays unsupported.
	ErrArgoCDProviderExecUnsupported = errors.New("argocd cluster uses an unrecognized exec-plugin auth command")
	// ErrArgoCDProviderUnsupportedAuth is returned when the ArgoCD cluster
	// Secret config JSON parses but matches none of the known auth shapes.
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
	BearerToken        string                `json:"bearerToken,omitempty"`
	AWSAuthConfig      *argoCDAWSAuthConfig  `json:"awsAuthConfig,omitempty"`
	ExecProviderConfig *argoCDExecProvider   `json:"execProviderConfig,omitempty"`
	TLSClientConfig    argoCDTLSClientConfig `json:"tlsClientConfig"`
}

type argoCDAWSAuthConfig struct {
	ClusterName string `json:"clusterName,omitempty"`
	RoleARN     string `json:"roleARN,omitempty"`
	// Region is not part of ArgoCD's own awsAuthConfig schema, but some
	// operators/tooling add it. When present it takes precedence over the
	// cluster Secret's "region" label when Sharko resolves the region to
	// mint an EKS token against (V2-cleanup-88.2).
	Region string `json:"region,omitempty"`
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
	// CertData / KeyData carry the client certificate pair for the plain-TLS
	// cluster secret shape (kind / kubeadm / on-prem clusters written by
	// argosecrets.buildSecretConfig's cert branch, V2-cleanup-56.1).
	CertData string `json:"certData,omitempty"`
	KeyData  string `json:"keyData,omitempty"`
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
// cluster Secret. Routes per detected auth shape, in reuse-first order — the
// Secret's own material is always preferred over minting a new token
// (V2-cleanup-88.2 L5):
//
//   - bearerToken non-empty              → returns ready-to-use *Kubeconfig
//   - tlsClientConfig certData+keyData   → returns ready-to-use *Kubeconfig
//     (plain-TLS / client-certificate shape, V2-cleanup-56.1)
//   - awsAuthConfig non-nil              → parses clusterName/roleARN/region
//     and mints an EKS token with Sharko's OWN AWS identity (never executes
//     anything); returns ErrArgoCDProviderIAMRequired if no region can be
//     determined or the mint attempt fails (V2-cleanup-88.2)
//   - execProviderConfig non-nil, known AWS authenticator (argocd-k8s-auth aws,
//     aws-iam-authenticator) → parses --cluster-name/--role-arn/AWS_REGION and
//     mints the same way; returns ErrArgoCDProviderIAMRequired on the same
//     failure conditions (V2-cleanup-88.2)
//   - execProviderConfig non-nil, unrecognized command → returns
//     ErrArgoCDProviderExecUnsupported — Sharko never shells out to exec-plugin
//     binaries
//   - none of the above                  → returns ErrArgoCDProviderUnsupportedAuth
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

	// Cert-shape is checked before the AWS shapes so the Secret's own
	// material always wins over minting a new token (reuse-first, L5) —
	// though in practice ArgoCD/Sharko never write more than one auth shape
	// into the same Secret, this keeps the precedence explicit and correct
	// even for a hand-edited or malformed Secret that sets both.
	case cfg.TLSClientConfig.CertData != "" && cfg.TLSClientConfig.KeyData != "":
		return p.buildCertKubeconfig(storedName, server, cfg)

	case cfg.AWSAuthConfig != nil:
		return p.resolveAWSAuthConfig(context.Background(), storedName, server, secret, cfg)

	case cfg.ExecProviderConfig != nil:
		return p.resolveExecProviderConfig(context.Background(), storedName, server, secret, cfg)

	default:
		return nil, &ArgoCDProviderError{
			Code:        ArgoCDProviderCodeUnsupportedAuth,
			ClusterName: storedName,
			Server:      server,
			Detail: fmt.Sprintf("argocd cluster secret for %q has no recognised auth shape "+
				"(expected bearerToken, awsAuthConfig, execProviderConfig, or a tlsClientConfig client-certificate pair)", storedName),
		}
	}
}

// buildCertKubeconfig constructs a *Kubeconfig from the plain-TLS
// (client-certificate) shape written by argosecrets.buildSecretConfig's cert
// branch (V2-cleanup-56.1). CertData / KeyData / CAData are base64-decoded
// from tlsClientConfig. The Raw field is a synthesized kubeconfig YAML that
// round-trips through clientcmd.RESTConfigFromKubeConfig cleanly so downstream
// consumers (remoteclient.NewClientFromKubeconfig) can use it as-is.
func (p *ArgoCDProvider) buildCertKubeconfig(name, server string, cfg argoCDClusterConfig) (*Kubeconfig, error) {
	if server == "" {
		return nil, fmt.Errorf("argocd cluster secret for %q has empty server URL", name)
	}

	certBytes, err := base64.StdEncoding.DecodeString(cfg.TLSClientConfig.CertData)
	if err != nil {
		return nil, fmt.Errorf("decoding tlsClientConfig.certData for cluster %q: %w", name, err)
	}
	keyBytes, err := base64.StdEncoding.DecodeString(cfg.TLSClientConfig.KeyData)
	if err != nil {
		return nil, fmt.Errorf("decoding tlsClientConfig.keyData for cluster %q: %w", name, err)
	}

	var caBytes []byte
	if cfg.TLSClientConfig.CAData != "" {
		decoded, err := base64.StdEncoding.DecodeString(cfg.TLSClientConfig.CAData)
		if err != nil {
			return nil, fmt.Errorf("decoding tlsClientConfig.caData for cluster %q: %w", name, err)
		}
		caBytes = decoded
	}

	// Synthesize a minimal kubeconfig YAML. The original base64 strings go
	// into the *-data fields verbatim (kubeconfig spec requires base64).
	// The cluster block varies by TLS mode; the user block always carries
	// the cert pair.
	var clusterTLSLine string
	switch {
	case cfg.TLSClientConfig.Insecure:
		clusterTLSLine = "\n    insecure-skip-tls-verify: true"
	case cfg.TLSClientConfig.CAData != "":
		clusterTLSLine = "\n    certificate-authority-data: " + cfg.TLSClientConfig.CAData
	default:
		// No CA data and not insecure — system trust roots will be used by callers.
		clusterTLSLine = ""
	}
	kubeconfigYAML := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: %s%s
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
    client-certificate-data: %s
    client-key-data: %s
`, server, clusterTLSLine, name, name, name, name, name, name,
		cfg.TLSClientConfig.CertData, cfg.TLSClientConfig.KeyData)

	// Verify the synthesized YAML round-trips through clientcmd. This is the
	// same call downstream consumers make; failing here surfaces the bug at
	// fetch time instead of inside remoteclient.
	if _, err := clientcmd.RESTConfigFromKubeConfig([]byte(kubeconfigYAML)); err != nil {
		return nil, fmt.Errorf("synthesized kubeconfig for cluster %q failed to parse: %w", name, err)
	}

	slog.Info("[provider] argocd client-certificate kubeconfig built",
		"cluster", name, "server", server,
		"hasCA", len(caBytes) > 0, "insecure", cfg.TLSClientConfig.Insecure)

	return &Kubeconfig{
		Raw:      []byte(kubeconfigYAML),
		Server:   server,
		CAData:   caBytes,
		CertData: certBytes,
		KeyData:  keyBytes,
	}, nil
}

// buildBearerTokenKubeconfig constructs a *Kubeconfig from the bearerToken
// shape — the token ArgoCD itself wrote into the Secret. Thin wrapper over
// buildTokenKubeconfig, the shared builder also used by the AWS-mint paths
// (V2-cleanup-88.2) so every token-based kubeconfig (ArgoCD-supplied or
// Sharko-minted) is synthesized identically.
func (p *ArgoCDProvider) buildBearerTokenKubeconfig(name, server string, cfg argoCDClusterConfig) (*Kubeconfig, error) {
	kc, err := p.buildTokenKubeconfig(name, server, cfg.BearerToken, cfg.TLSClientConfig)
	if err != nil {
		return nil, err
	}
	slog.Info("[provider] argocd bearer-token kubeconfig built",
		"cluster", name, "server", server,
		"hasCA", len(kc.CAData) > 0, "insecure", cfg.TLSClientConfig.Insecure)
	return kc, nil
}

// buildTokenKubeconfig constructs a *Kubeconfig carrying token as the bearer
// token. CAData is base64-decoded from tls.CAData (or left empty when
// tls.Insecure). The Raw field is a synthesized kubeconfig YAML that
// round-trips through clientcmd.RESTConfigFromKubeConfig cleanly so
// downstream consumers (remoteclient.NewClientFromKubeconfig) can use it
// as-is. Used both for the ArgoCD-supplied bearerToken shape and for tokens
// Sharko mints itself against the awsAuthConfig / execProviderConfig shapes
// (V2-cleanup-88.2) — the kubeconfig shape is identical either way; only the
// token's origin differs.
func (p *ArgoCDProvider) buildTokenKubeconfig(name, server, token string, tls argoCDTLSClientConfig) (*Kubeconfig, error) {
	if server == "" {
		return nil, fmt.Errorf("argocd cluster secret for %q has empty server URL", name)
	}

	var caBytes []byte
	if tls.CAData != "" {
		decoded, err := base64.StdEncoding.DecodeString(tls.CAData)
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
	case tls.Insecure:
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
`, server, name, name, name, name, name, name, token)
	case tls.CAData != "":
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
`, server, tls.CAData, name, name, name, name, name, name, token)
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
`, server, name, name, name, name, name, name, token)
	}

	// Verify the synthesized YAML round-trips through clientcmd. This is the
	// same call downstream consumers make; failing here surfaces the bug at
	// fetch time instead of inside remoteclient.
	if _, err := clientcmd.RESTConfigFromKubeConfig([]byte(kubeconfigYAML)); err != nil {
		return nil, fmt.Errorf("synthesized kubeconfig for cluster %q failed to parse: %w", name, err)
	}

	return &Kubeconfig{
		Raw:    []byte(kubeconfigYAML),
		Server: server,
		CAData: caBytes,
		Token:  token,
	}, nil
}

// resolveAWSAuthConfig handles the awsAuthConfig shape (V2-cleanup-88.2 —
// upgrade from "unsupported" to "Sharko assumes the same role with its own
// identity"). It parses clusterName + roleARN out of the JSON and mints an
// EKS token with Sharko's OWN AWS identity via p.eksTokenFn — this never
// shells out to any binary; getEKSToken only ever calls the AWS SDK
// (STS AssumeRole + presigned GetCallerIdentity).
//
// clusterName falls back to the Secret's display name when awsAuthConfig
// omits it (real ArgoCD payloads always set it, but a hand-edited Secret
// might not). roleARN is optional — an empty roleARN mints with Sharko's
// base identity (no AssumeRole hop), mirroring getEKSToken's own contract.
// region comes from awsAuthConfig.Region if present (not part of ArgoCD's
// schema, but tolerated), else the Secret's "region" label (the same field
// ListClusters surfaces), else parsed out of the EKS server URL itself
// (regionFromEKSServerURL) — the last-resort fallback that makes Sharko's
// own EKS-registered clusters work, since Sharko's writer never persists
// region as a JSON field or a label. When no region can be determined at
// all, Sharko returns a typed error naming "region" as the missing piece
// rather than guessing.
func (p *ArgoCDProvider) resolveAWSAuthConfig(ctx context.Context, name, server string, secret *corev1.Secret, cfg argoCDClusterConfig) (*Kubeconfig, error) {
	auth := cfg.AWSAuthConfig

	clusterName := auth.ClusterName
	if clusterName == "" {
		clusterName = name
	}

	region := auth.Region
	if region == "" {
		region = secret.Labels["region"]
	}
	if region == "" {
		region = regionFromEKSServerURL(server)
	}
	if region == "" {
		slog.Warn("[provider] argocd cluster awsAuthConfig has no resolvable AWS region — cannot mint an EKS token",
			"cluster", name, "server", server, "eksClusterName", clusterName)
		return nil, &ArgoCDProviderError{
			Code:        ArgoCDProviderCodeIAMRequired,
			ClusterName: name,
			Server:      server,
			Detail: fmt.Sprintf("cluster %q uses AWS IAM authentication (awsAuthConfig) but no AWS region could be "+
				"determined (no region field in awsAuthConfig, the cluster Secret has no \"region\" label, and the "+
				"server URL %q isn't a recognizable EKS endpoint). Set the region label on the ArgoCD cluster "+
				"Secret, or add a region field to awsAuthConfig.", name, server),
		}
	}

	slog.Info("[provider] argocd cluster uses awsAuthConfig — minting an EKS token with Sharko's own AWS identity",
		"cluster", name, "server", server, "eksClusterName", clusterName, "region", region, "hasRoleARN", auth.RoleARN != "")

	return p.mintTokenKubeconfig(ctx, name, server, cfg.TLSClientConfig, clusterName, region, auth.RoleARN)
}

// resolveExecProviderConfig handles the execProviderConfig shape
// (V2-cleanup-88.2). Sharko never executes the plugin binary — instead it
// checks whether the command is one of the two well-known AWS authenticators
// (isKnownAWSExecCommand); if so it parses --cluster-name / --role-arn from
// args and AWS_REGION from env, then mints the same way as resolveAWSAuthConfig.
// Any other command (gcloud, az, custom scripts, ...) returns
// ErrArgoCDProviderExecUnsupported — parsed, recognized as "not AWS", and
// rejected outright, exactly as before for those commands.
func (p *ArgoCDProvider) resolveExecProviderConfig(ctx context.Context, name, server string, secret *corev1.Secret, cfg argoCDClusterConfig) (*Kubeconfig, error) {
	// Named execCfg (not "exec") on purpose — this file must never contain a
	// token that reads like a shell-out call, to keep the no-shellout grep
	// gate readable at a glance (V2-cleanup-88.2).
	execCfg := cfg.ExecProviderConfig

	if !isKnownAWSExecCommand(execCfg) {
		slog.Info("[provider] argocd cluster uses execProviderConfig with an unrecognized command — Sharko never executes exec-plugin binaries",
			"cluster", name, "server", server, "command", execCfg.Command)
		return nil, &ArgoCDProviderError{
			Code:        ArgoCDProviderCodeExecUnsupported,
			ClusterName: name,
			Server:      server,
			Detail: fmt.Sprintf("cluster %q uses exec-plugin auth (command %q). Sharko never executes exec-plugin "+
				"binaries; only the known AWS authenticators (argocd-k8s-auth aws, aws-iam-authenticator) are parsed "+
				"and minted with Sharko's own AWS identity — every other command stays unsupported.",
				name, execCfg.Command),
		}
	}

	clusterName, roleARN := parseExecProviderAWSArgs(execCfg.Args)
	if clusterName == "" {
		clusterName = name
	}

	// Region precedence: execProviderConfig.env.AWS_REGION (rarely set —
	// Sharko's own writer omits env entirely, see regionFromEKSServerURL's
	// doc comment) → the Secret's "region" label → parsed out of the EKS
	// server URL, the last-resort fallback that makes Sharko's own
	// EKS-registered clusters (which carry neither of the first two) work.
	region := execCfg.Env["AWS_REGION"]
	if region == "" {
		region = secret.Labels["region"]
	}
	if region == "" {
		region = regionFromEKSServerURL(server)
	}
	if region == "" {
		slog.Warn("[provider] argocd cluster execProviderConfig has no resolvable AWS region — cannot mint an EKS token",
			"cluster", name, "server", server, "command", execCfg.Command, "eksClusterName", clusterName)
		return nil, &ArgoCDProviderError{
			Code:        ArgoCDProviderCodeIAMRequired,
			ClusterName: name,
			Server:      server,
			Detail: fmt.Sprintf("cluster %q uses exec-plugin AWS authentication (command %q) but no AWS region could "+
				"be determined (no AWS_REGION in execProviderConfig.env, the cluster Secret has no \"region\" label, "+
				"and the server URL %q isn't a recognizable EKS endpoint). Set the region label on the ArgoCD "+
				"cluster Secret, or add AWS_REGION to execProviderConfig.env.",
				name, execCfg.Command, server),
		}
	}

	slog.Info("[provider] argocd cluster uses execProviderConfig with a known AWS authenticator — parsing args and minting with Sharko's own AWS identity (never executing the plugin)",
		"cluster", name, "server", server, "command", execCfg.Command, "eksClusterName", clusterName, "region", region, "hasRoleARN", roleARN != "")

	return p.mintTokenKubeconfig(ctx, name, server, cfg.TLSClientConfig, clusterName, region, roleARN)
}

// mintTokenKubeconfig mints an EKS token via p.eksTokenFn (Sharko's own AWS
// identity, optionally assuming roleARN) and builds the resulting
// *Kubeconfig. A mint failure — no IRSA/Pod Identity/instance role, STS
// AssumeRole denied, etc. — is wrapped into the same typed
// ArgoCDProviderCodeIAMRequired error used for the missing-region case, since
// both mean the same thing to a caller: the shape parsed, but Sharko could
// not produce usable AWS-backed credentials for it.
func (p *ArgoCDProvider) mintTokenKubeconfig(ctx context.Context, name, server string, tls argoCDTLSClientConfig, eksClusterName, region, roleARN string) (*Kubeconfig, error) {
	token, err := p.eksTokenFn(ctx, eksClusterName, region, roleARN)
	if err != nil {
		slog.Error("[provider] EKS token mint failed for argocd cluster — Sharko has no usable AWS identity for this cluster",
			"cluster", name, "server", server, "eksClusterName", eksClusterName, "region", region, "error", err)
		return nil, &ArgoCDProviderError{
			Code:        ArgoCDProviderCodeIAMRequired,
			ClusterName: name,
			Server:      server,
			Detail: fmt.Sprintf("cluster %q needs Sharko's own AWS identity (IRSA/Pod Identity) to use this "+
				"cluster's IAM-based connection, and minting an EKS token failed: %v", name, err),
		}
	}
	return p.buildTokenKubeconfig(name, server, token, tls)
}

// eksServerHostRegion matches the region segment out of a standard EKS API
// server hostname: <cluster-id>.gr7.<region>.eks.amazonaws.com. The "gr7"
// disambiguator segment (present on every EKS cluster created since ~2020)
// is optional so the older <cluster-id>.<region>.eks.amazonaws.com shape
// still matches.
var eksServerHostRegion = regexp.MustCompile(`^[^.]+\.(?:gr7\.)?([a-z0-9-]+)\.eks\.amazonaws\.com$`)

// regionFromEKSServerURL extracts the AWS region from an EKS API server URL.
// This is the LAST-RESORT region fallback for the awsAuthConfig /
// execProviderConfig mint paths (V2-cleanup-88.2): Sharko's own writer
// (argosecrets.buildSecretConfig) never persists region as a JSON field, an
// env var, or a Secret label — its execProviderConfig output carries only
// command/args/apiVersion, no env, because "ArgoCD v2.14 cannot unmarshal
// the env field in execProviderConfig" (see that function's doc comment). So
// for Sharko's own EKS-registered clusters, the server URL is the only
// region signal actually present on the Secret at read time — it is always
// available because GetCredentials already requires a non-empty server to
// build any kubeconfig. Returns "" when server isn't a recognizable EKS
// endpoint (non-AWS host, custom DNS, GovCloud/China partitions with a
// different suffix, ...) so callers can still surface an honest error
// instead of guessing.
func regionFromEKSServerURL(server string) string {
	u, err := url.Parse(server)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	m := eksServerHostRegion.FindStringSubmatch(u.Hostname())
	if len(m) != 2 {
		return ""
	}
	return m[1]
}

// isKnownAWSExecCommand reports whether cfg is a recognized AWS-authenticator
// exec-plugin shape that Sharko can PARSE and mint against with its own AWS
// identity — WITHOUT ever executing the plugin binary. Sharko never shells
// out to run it; this package never imports Go's process-execution package.
// Only these two well-known shapes are treated as "AWS auth in disguise":
//
//   - command "argocd-k8s-auth" with args[0] == "aws" — ArgoCD's built-in
//     AWS helper subcommand
//   - command "aws-iam-authenticator" — the standalone binary
//
// Any other command (gcloud, az, custom scripts, ...) stays unsupported.
func isKnownAWSExecCommand(cfg *argoCDExecProvider) bool {
	switch cfg.Command {
	case "aws-iam-authenticator":
		return true
	case "argocd-k8s-auth":
		return len(cfg.Args) > 0 && cfg.Args[0] == "aws"
	default:
		return false
	}
}

// parseExecProviderAWSArgs extracts --cluster-name and --role-arn from an
// exec-plugin args slice, tolerating both "--flag value" and "--flag=value"
// forms. Missing or malformed flags simply leave the corresponding return
// value empty — callers apply their own fallback (the cluster's display
// name) for clusterName, and treat an empty roleARN as "mint with Sharko's
// base identity" (role assumption is optional, matching getEKSToken's own
// contract).
func parseExecProviderAWSArgs(args []string) (clusterName, roleARN string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--cluster-name" && i+1 < len(args):
			clusterName = args[i+1]
			i++
		case strings.HasPrefix(arg, "--cluster-name="):
			clusterName = strings.TrimPrefix(arg, "--cluster-name=")
		case arg == "--role-arn" && i+1 < len(args):
			roleARN = args[i+1]
			i++
		case strings.HasPrefix(arg, "--role-arn="):
			roleARN = strings.TrimPrefix(arg, "--role-arn=")
		}
	}
	return clusterName, roleARN
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
