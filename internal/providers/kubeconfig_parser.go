package providers

import (
	"errors"
	"fmt"
	"strings"

	"k8s.io/client-go/tools/clientcmd"
)

// ErrUnsupportedKubeconfigAuth is returned when an inline kubeconfig uses an
// authentication method this release cannot consume safely (cert-based or
// exec-plugin auth). It is wrapped with a descriptive error message that
// callers can pass through to the API as a 400-level validation failure.
//
// V125-1.1 ships bearer-token-only support. Cert-based and exec-plugin auth
// (aws-iam-authenticator, gke-gcloud-auth-plugin, etc.) need follow-on work
// to either:
//   - run the exec plugin server-side at registration time (security boundary
//     concerns: we'd be executing arbitrary binaries from the user's caller
//     environment in the Sharko pod), OR
//   - persist the cert + key as the ArgoCD cluster Secret config payload
//     (different ArgoCD secret schema from bearer-token, plus rotation
//     concerns).
//
// Both paths are V125-1.x material. For now we surface a clear error with
// the kubectl-create-token recipe so the maintainer can unblock kind testing.
var ErrUnsupportedKubeconfigAuth = errors.New("unsupported kubeconfig auth method")

// ParseInlineKubeconfig parses a raw kubeconfig YAML supplied inline by an
// API caller (V125-1.1) and extracts the bearer-token credentials needed
// for ArgoCD cluster registration.
//
// The function rejects auth methods this release does not support:
//   - Cert-based auth (TLSClientConfig.CertData / KeyData populated, no
//     bearer token present): returns ErrUnsupportedKubeconfigAuth wrapped
//     with a message pointing at `kubectl create token`.
//   - Exec-plugin auth (AuthInfo.Exec non-nil, e.g. aws-iam-authenticator
//     or gke-gcloud-auth-plugin): returns ErrUnsupportedKubeconfigAuth
//     wrapped with the same guidance.
//
// On success it returns a *Kubeconfig populated with Server, CAData, Token
// and Raw (for downstream Stage1 verification via remoteclient).
func ParseInlineKubeconfig(raw string) (*Kubeconfig, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("kubeconfig is empty")
	}

	rawBytes := []byte(raw)

	// Step 1: structural parse (no auth resolution) so we can inspect the
	// chosen context's AuthInfo and reject unsupported methods before they
	// reach RESTConfigFromKubeConfig (which would happily resolve a cert-
	// based config and return one we can't push to ArgoCD as bearer-token).
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

	// Step 2: enforce the bearer-token-only constraint.
	if authInfo.Exec != nil {
		return nil, fmt.Errorf("%w: exec-plugin auth (e.g. aws-iam-authenticator, gke-gcloud-auth-plugin) is not supported in v1.25; this release supports bearer-token only — generate a token via `kubectl create token <serviceaccount> --duration=24h` and paste a kubeconfig that uses it (full exec-plugin support coming in V125-1.x)", ErrUnsupportedKubeconfigAuth)
	}
	hasCert := len(authInfo.ClientCertificateData) > 0 || authInfo.ClientCertificate != ""
	hasKey := len(authInfo.ClientKeyData) > 0 || authInfo.ClientKey != ""
	hasToken := authInfo.Token != "" || authInfo.TokenFile != ""
	if (hasCert || hasKey) && !hasToken {
		return nil, fmt.Errorf("%w: cert-based auth is not supported in v1.25; this release supports bearer-token only — generate a token via `kubectl create token <serviceaccount> --duration=24h` and paste a kubeconfig that uses it (cert-based auth coming in V125-1.x)", ErrUnsupportedKubeconfigAuth)
	}

	// Step 3: resolve to a *rest.Config — at this point we've ruled out
	// cert-only / exec-only kubeconfigs, so the resolved config will carry
	// a bearer token (and possibly the parsed CA bundle).
	restCfg, err := clientcmd.RESTConfigFromKubeConfig(rawBytes)
	if err != nil {
		return nil, fmt.Errorf("resolving kubeconfig: %w", err)
	}

	if restCfg.Host == "" {
		return nil, fmt.Errorf("kubeconfig resolved to empty server URL")
	}
	if restCfg.BearerToken == "" {
		// A token file (TokenFile) on AuthInfo is resolved by clientcmd at
		// build time, so an empty BearerToken here means the token file
		// pointed nowhere. Treat the same as a missing-token kubeconfig.
		return nil, fmt.Errorf("%w: kubeconfig contains no usable bearer token; generate one via `kubectl create token <serviceaccount> --duration=24h`", ErrUnsupportedKubeconfigAuth)
	}

	return &Kubeconfig{
		Raw:    rawBytes,
		Server: restCfg.Host,
		CAData: restCfg.TLSClientConfig.CAData,
		Token:  restCfg.BearerToken,
	}, nil
}
