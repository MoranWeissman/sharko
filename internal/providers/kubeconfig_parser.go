package providers

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// ErrUnsupportedKubeconfigAuth is returned when a kubeconfig uses an
// authentication method this release cannot consume safely (exec-plugin or
// auth-provider sign-in, a half cert pair, or any field that makes client-go
// read a file from the Sharko pod's own disk). It is wrapped with a
// descriptive error message that callers can pass through to the API.
//
// Supported auth methods:
//   - Bearer token, written in the kubeconfig itself (`token`)
//   - Client certificate + key pair, inline data only (V2-cleanup-56.1 —
//     kind / kubeadm / on-prem kubeconfigs; persisted as ArgoCD's plain-TLS
//     cluster secret shape)
//
// Exec-plugin auth (aws-iam-authenticator, gke-gcloud-auth-plugin) and
// auth-provider plugins stay unsupported: running auth code inside the Sharko
// pod is a security boundary we do not cross. File references (`tokenFile`,
// `client-certificate`, `client-key`, `certificate-authority`) are refused
// for the same reason: client-go would open that path INSIDE the Sharko pod,
// so a kubeconfig naming, say, the pod's own ServiceAccount token file would
// make Sharko send its own token to whatever server the kubeconfig names.
var ErrUnsupportedKubeconfigAuth = errors.New(unsupportedKubeconfigAuthText)

// unsupportedKubeconfigAuthText is the text of ErrUnsupportedKubeconfigAuth.
// KubeconfigRefusal builds its message from this constant, not by calling
// .Error() on the sentinel: the credential-path guard in internal/verify
// forbids reading an error's words directly in this package.
const unsupportedKubeconfigAuthText = "unsupported kubeconfig auth method"

// The refusal sentences, one per refused field. Each is wrapped behind
// ErrUnsupportedKubeconfigAuth ("unsupported kubeconfig auth method: ...").
// Pinned by exact text in kubeconfig_parser_test.go.
const (
	refuseExecSentence         = "exec-plugin auth (e.g. aws-iam-authenticator, gke-gcloud-auth-plugin) is not supported; use a bearer token (`kubectl create token <serviceaccount> --duration=24h`) or a client-certificate kubeconfig with inline data"
	refuseAuthProviderSentence = "auth-provider sign-in (e.g. oidc, gcp, azure) is not supported; use a bearer token (`kubectl create token <serviceaccount> --duration=24h`) or a client-certificate kubeconfig with inline data"
	refuseTokenFileSentence    = "kubeconfig reads its bearer token from a file path (tokenFile); Sharko does not read a kubeconfig's files from its own disk, so put the token itself in the kubeconfig (token) or generate one with `kubectl create token <serviceaccount> --duration=24h`"
	refuseCertFileSentence     = "kubeconfig references client certificate/key by file path; embed the data inline first (`kubectl config view --flatten --minify`) or use a bearer token"
	refuseCAFileSentence       = "kubeconfig references the cluster certificate authority by file path (certificate-authority); embed the data inline first (`kubectl config view --flatten --minify`)"
)

// The kubeconfig keys a KubeconfigRefusal can name. A fixed set, so the
// field a log line or a caller reads off a refusal is always one of these
// words and never anything taken from the kubeconfig itself.
const (
	RefusedFieldExec              = "exec"
	RefusedFieldAuthProvider      = "auth-provider"
	RefusedFieldTokenFile         = "tokenFile"
	RefusedFieldClientCertificate = "client-certificate"
	RefusedFieldClientKey         = "client-key"
	RefusedFieldCertAuthority     = "certificate-authority"
)

// KubeconfigRefusal is the error refusePodLocalReferences returns: the
// kubeconfig has a field that would make client-go read a file or run a
// program inside the Sharko pod.
//
// It is a type, not just text, so a caller can tell a refused kubeconfig
// apart from a missing one without reading any words. The stored-kubeconfig
// providers wrap it on the way out and then put a credsafe mark on top,
// which replaces the text with the fixed credentials sentence. errors.As
// still finds the refusal under the mark, and both of its parts are
// Sharko's own fixed text: Field is one of the RefusedField constants and
// Sentence is one of the refuse*Sentence constants. Nothing in a
// KubeconfigRefusal comes from the kubeconfig, so it is safe to show.
//
// errors.Is(refusal, ErrUnsupportedKubeconfigAuth) is true, and Error()
// reads exactly as the fmt.Errorf("%w: %s") form it replaced.
type KubeconfigRefusal struct {
	field    string
	sentence string
}

// Field is the kubeconfig key that was refused (a RefusedField constant).
func (r *KubeconfigRefusal) Field() string { return r.field }

// Sentence is the plain refusal sentence, without the
// ErrUnsupportedKubeconfigAuth prefix.
func (r *KubeconfigRefusal) Sentence() string { return r.sentence }

func (r *KubeconfigRefusal) Error() string {
	return unsupportedKubeconfigAuthText + ": " + r.sentence
}

func (r *KubeconfigRefusal) Unwrap() error { return ErrUnsupportedKubeconfigAuth }

// AsKubeconfigRefusal finds a KubeconfigRefusal anywhere in err's chain,
// including under a credsafe mark.
func AsKubeconfigRefusal(err error) (*KubeconfigRefusal, bool) {
	var r *KubeconfigRefusal
	if errors.As(err, &r) && r != nil {
		return r, true
	}
	return nil, false
}

func refuse(field, sentence string) error {
	return &KubeconfigRefusal{field: field, sentence: sentence}
}

// logStoredKubeconfigRefusal writes one log line when a kubeconfig read from
// a secrets backend was refused, naming the refused field. Without it the
// operator only ever saw the fixed credentials sentence and had no way to
// learn that the stored kubeconfig was found but cannot be used.
//
// The line carries only fixed text: the backend word the caller passes, the
// secret name (the operator's own configuration), and the refused field
// (a RefusedField constant). No error value and nothing from the kubeconfig.
func logStoredKubeconfigRefusal(backend, secretName string, err error) {
	r, ok := AsKubeconfigRefusal(err)
	if !ok {
		return
	}
	slog.Warn("[provider] stored kubeconfig refused: it would make Sharko read a file or run a program inside its own pod",
		"backend", backend, "secretName", secretName, "field", r.Field())
}

// BuildRESTConfig is the one way Sharko turns kubeconfig bytes into a
// *rest.Config. It is clientcmd.RESTConfigFromKubeConfig with one step added
// before anything is resolved: refusePodLocalReferences, which refuses every
// kubeconfig field that would make client-go open a file or run a command
// inside the Sharko pod. Every shipping caller — the inline paste parser, the
// Kubernetes Secrets and AWS Secrets Manager providers, the ArgoCD provider's
// synthesized kubeconfigs, and remoteclient — goes through here, so the check
// cannot be skipped by taking a different entry point.
//
// A refusal wraps ErrUnsupportedKubeconfigAuth and is returned before
// client-go has looked at any path.
func BuildRESTConfig(raw []byte) (*rest.Config, error) {
	apiCfg, err := clientcmd.Load(raw)
	if err != nil {
		return nil, err
	}
	return restConfigFromAPIConfig(apiCfg)
}

// restConfigFromAPIConfig runs the pod-local check and then resolves the
// config exactly the way clientcmd.RESTConfigFromKubeConfig does (current
// context, no overrides, no prompting, no kubeconfig file access).
func restConfigFromAPIConfig(apiCfg *clientcmdapi.Config) (*rest.Config, error) {
	if err := refusePodLocalReferences(apiCfg); err != nil {
		return nil, err
	}
	return clientcmd.NewNonInteractiveClientConfig(*apiCfg, "", &clientcmd.ConfigOverrides{}, nil).ClientConfig()
}

// refusePodLocalReferences refuses a kubeconfig whose selected user or
// cluster carries a field client-go would turn into a local file read or a
// local command inside the Sharko pod:
//
//   - user.exec                  — runs a command
//   - user.auth-provider         — runs auth-provider plugin code (gcp's
//     cmd-path ran a command; oidc refreshes and writes tokens back)
//   - user.tokenFile             — read at build time AND re-read on every
//     request (client-go's cached file token source), even when a token is
//     also set
//   - user.client-certificate / user.client-key — opened at validation,
//     read when the transport is built
//   - cluster.certificate-authority — opened at validation, read when the
//     transport is built
//
// "Selected" mirrors client-go with no overrides: the context named by
// current-context (an absent one resolves to an empty context), then the
// user and cluster that context names. Entries no context selects are never
// used by client-go and are not checked, so a multi-context kubeconfig with
// an unused exec user still works the way it did.
func refusePodLocalReferences(apiCfg *clientcmdapi.Config) error {
	var userName, clusterName string
	if c, ok := apiCfg.Contexts[apiCfg.CurrentContext]; ok && c != nil {
		userName, clusterName = c.AuthInfo, c.Cluster
	}
	if authInfo := apiCfg.AuthInfos[userName]; authInfo != nil {
		switch {
		case authInfo.Exec != nil:
			return refuse(RefusedFieldExec, refuseExecSentence)
		case authInfo.AuthProvider != nil:
			return refuse(RefusedFieldAuthProvider, refuseAuthProviderSentence)
		case authInfo.TokenFile != "":
			return refuse(RefusedFieldTokenFile, refuseTokenFileSentence)
		case authInfo.ClientCertificate != "":
			return refuse(RefusedFieldClientCertificate, refuseCertFileSentence)
		case authInfo.ClientKey != "":
			return refuse(RefusedFieldClientKey, refuseCertFileSentence)
		}
	}
	if cluster := apiCfg.Clusters[clusterName]; cluster != nil && cluster.CertificateAuthority != "" {
		return refuse(RefusedFieldCertAuthority, refuseCAFileSentence)
	}
	return nil
}

// ParseInlineKubeconfig parses a raw kubeconfig YAML supplied inline by
// an API caller and extracts the credentials needed for ArgoCD cluster
// registration: either a bearer token or a client certificate + key pair.
//
// The function rejects auth methods this release does not support, all
// wrapping ErrUnsupportedKubeconfigAuth:
//   - Everything refusePodLocalReferences refuses: exec-plugin and
//     auth-provider auth, tokenFile, client-certificate / client-key and
//     certificate-authority by file path. Those paths would be opened inside
//     the Sharko pod, not on the caller's machine.
//   - A half cert pair (cert without key or key without cert, no bearer
//     token present).
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
	// complete inline cert pair). The pod-local refusals come first — the
	// same check every entry point runs through BuildRESTConfig (it runs
	// again inside restConfigFromAPIConfig below, which costs nothing). Only
	// the half-pair check is specific to this parser.
	if err := refusePodLocalReferences(apiCfg); err != nil {
		return nil, err
	}
	hasCertData := len(authInfo.ClientCertificateData) > 0
	hasKeyData := len(authInfo.ClientKeyData) > 0
	hasToken := authInfo.Token != ""
	if !hasToken && (hasCertData || hasKeyData) && !(hasCertData && hasKeyData) {
		// Half pair — cert without key or key without cert.
		return nil, fmt.Errorf("%w: kubeconfig carries an incomplete client certificate pair (need both client-certificate-data and client-key-data); fix the kubeconfig or use a bearer token", ErrUnsupportedKubeconfigAuth)
	}

	// Step 3: resolve to a *rest.Config — at this point the config carries a
	// bearer token, a complete inline cert pair, or nothing usable.
	restCfg, err := restConfigFromAPIConfig(apiCfg)
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
		// No inline token and no complete inline cert pair (tokenFile was
		// already refused above). Treat as a missing-credentials kubeconfig.
		return nil, fmt.Errorf("%w: kubeconfig contains no usable bearer token or client certificate pair; generate a token via `kubectl create token <serviceaccount> --duration=24h`", ErrUnsupportedKubeconfigAuth)
	}

	return kc, nil
}
