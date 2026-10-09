package providers

import (
	"errors"
	"fmt"
	"strings"

	"k8s.io/client-go/tools/clientcmd"
)

// ErrUnsupportedKubeconfigAuth is returned when an inline kubeconfig uses an
// authentication method this release cannot consume safely (exec-plugin auth,
// or a half cert pair). It is wrapped with a descriptive error message that
// callers can pass through to the API as a 400-level validation failure.
//
// Supported auth methods:
//   - Bearer token (Token / TokenFile)
//   - Client certificate + key pair, inline data only (V2-cleanup-56.1 —
//     kind / kubeadm / on-prem kubeconfigs; persisted as ArgoCD's plain-TLS
//     cluster secret shape)
//
// Exec-plugin auth (aws-iam-authenticator, gke-gcloud-auth-plugin) stays
// unsupported: running arbitrary auth binaries inside the Sharko pod is a
// security boundary we do not cross.
var ErrUnsupportedKubeconfigAuth = errors.New("unsupported kubeconfig auth method")

// ParseInlineKubeconfig parses a raw kubeconfig YAML supplied inline by
// an API caller and extracts the credentials needed for ArgoCD cluster
// registration: either a bearer token or a client certificate + key pair.
//
// The function rejects auth methods this release does not support:
//   - Exec-plugin auth (AuthInfo.Exec non-nil, e.g. aws-iam-authenticator
//     or gke-gcloud-auth-plugin): returns ErrUnsupportedKubeconfigAuth.
//   - A half cert pair (cert without key or key without cert, no bearer
//     token present): returns ErrUnsupportedKubeconfigAuth.
//   - Cert/key referenced by FILE PATH (client-certificate / client-key):
//     the files live on the caller's machine, not in the Sharko pod, so the
//     bytes cannot be resolved server-side. Returns ErrUnsupportedKubeconfigAuth
//     with guidance to embed the data inline (kubectl config view --flatten).
//
// On success it returns a *Kubeconfig populated with Server, CAData, Raw
// (for downstream Stage1 verification via remoteclient) and either Token or
// the CertData/KeyData pair.
func ParseInlineKubeconfig(raw string) (*Kubeconfig, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("kubeconfig is empty")
	}

	rawBytes := []byte(raw)

	// Step 1: structural parse (no auth resolution) so we can inspect the
	// chosen context's AuthInfo and reject unsupported methods before they
	// reach RESTConfigFromKubeConfig.
	apiCfg, err := clientcmd.Load(rawBytes)
	if err != nil {
		return nil, fmt.Errorf("invalid kubeconfig: %w", err)
	}

	if apiCfg.CurrentContext == "" {
		// Some kubeconfigs ship with multiple contexts and no default.
		// Without a current-context selection we can't deterministically
		// pick AuthInfo / Cluster — surface a clear error rather than
		// silently picking one.
		return nil, fmt.Errorf("kubeconfig has no current-context set; specify one before pasting")
	}
	ctx, ok := apiCfg.Contexts[apiCfg.CurrentContext]
	if !ok || ctx == nil {
		return nil, fmt.Errorf("kubeconfig current-context %q does not resolve to a context entry", apiCfg.CurrentContext)
	}
	if ctx.Cluster == "" {
		return nil, fmt.Errorf("kubeconfig context %q has no cluster reference", apiCfg.CurrentContext)
	}
	if _, ok := apiCfg.Clusters[ctx.Cluster]; !ok {
		return nil, fmt.Errorf("kubeconfig references missing cluster %q", ctx.Cluster)
	}
	if ctx.AuthInfo == "" {
		return nil, fmt.Errorf("kubeconfig context %q has no user reference", apiCfg.CurrentContext)
	}
	authInfo, ok := apiCfg.AuthInfos[ctx.AuthInfo]
	if !ok || authInfo == nil {
		return nil, fmt.Errorf("kubeconfig references missing user %q", ctx.AuthInfo)
	}

	// Step 2: enforce the supported-auth constraint (bearer token OR a
	// complete inline cert pair).
	if authInfo.Exec != nil {
		return nil, fmt.Errorf("%w: exec-plugin auth (e.g. aws-iam-authenticator, gke-gcloud-auth-plugin) is not supported; use a bearer token (`kubectl create token <serviceaccount> --duration=24h`) or a client-certificate kubeconfig with inline data", ErrUnsupportedKubeconfigAuth)
	}
	hasCertData := len(authInfo.ClientCertificateData) > 0
	hasKeyData := len(authInfo.ClientKeyData) > 0
	hasCertFile := authInfo.ClientCertificate != ""
	hasKeyFile := authInfo.ClientKey != ""
	hasToken := authInfo.Token != "" || authInfo.TokenFile != ""
	if !hasToken {
		if (hasCertFile || hasKeyFile) && !(hasCertData && hasKeyData) {
			// Cert/key referenced by file path — the files are on the
			// caller's machine, unreadable from the Sharko pod.
			return nil, fmt.Errorf("%w: kubeconfig references client certificate/key by file path; embed the data inline first (`kubectl config view --flatten`) or use a bearer token", ErrUnsupportedKubeconfigAuth)
		}
		if (hasCertData || hasKeyData) && !(hasCertData && hasKeyData) {
			// Half pair — cert without key or key without cert.
			return nil, fmt.Errorf("%w: kubeconfig carries an incomplete client certificate pair (need both client-certificate-data and client-key-data); fix the kubeconfig or use a bearer token", ErrUnsupportedKubeconfigAuth)
		}
	}

	// Step 3: resolve to a *rest.Config — at this point the config carries a
	// bearer token, a complete inline cert pair, or nothing usable.
	restCfg, err := clientcmd.RESTConfigFromKubeConfig(rawBytes)
	if err != nil {
		return nil, fmt.Errorf("resolving kubeconfig: %w", err)
	}

	if restCfg.Host == "" {
		return nil, fmt.Errorf("kubeconfig resolved to empty server URL")
	}

	kc := &Kubeconfig{
		Raw:    rawBytes,
		Server: restCfg.Host,
		CAData: restCfg.TLSClientConfig.CAData,
		Token:  restCfg.BearerToken,
	}
	if len(restCfg.TLSClientConfig.CertData) > 0 && len(restCfg.TLSClientConfig.KeyData) > 0 {
		kc.CertData = restCfg.TLSClientConfig.CertData
		kc.KeyData = restCfg.TLSClientConfig.KeyData
	}

	if kc.Token == "" && (len(kc.CertData) == 0 || len(kc.KeyData) == 0) {
		// A token file (TokenFile) on AuthInfo is resolved by clientcmd at
		// build time, so an empty BearerToken here means the token file
		// pointed nowhere. Treat the same as a missing-credentials kubeconfig.
		return nil, fmt.Errorf("%w: kubeconfig contains no usable bearer token or client certificate pair; generate a token via `kubectl create token <serviceaccount> --duration=24h`", ErrUnsupportedKubeconfigAuth)
	}

	return kc, nil
}
