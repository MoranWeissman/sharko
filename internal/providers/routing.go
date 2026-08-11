package providers

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/MoranWeissman/sharko/internal/models"
)

// ClusterCredsRouter routes per-cluster credential fetches by the cluster's
// own creds source (V2-cleanup-60.4 / review finding H4).
//
// Background: since the aws-sm / k8s-secrets cluster-credentials arms were
// restored (V2-cleanup-53.1), configuring a backend connection routed ALL
// cluster credential fetches to that backend. A cluster registered with an
// INLINE kubeconfig has no backend secret — its credentials live only in
// the ArgoCD cluster Secret written at registration — so Test / Diagnose /
// secrets / addon ops answered "secret not found" for it whenever a backend
// was configured. The router restores per-cluster correctness:
//
//   - credsSource == inline-kubeconfig → read via the ArgoCD provider (the
//     in-cluster ArgoCD-secret reader), REGARDLESS of the configured
//     backend type. The ArgoCD Secret is keyed by cluster NAME (never by a
//     secretPath override — that is a backend concept).
//   - credsSource == secret-kubeconfig / eks-token → the configured backend
//     with the resolved lookup key, exactly as before.
//   - credsSource == "" (record predates the field, or cluster unknown) →
//     backend first; when the backend errors, fall back to the ArgoCD
//     reader so pre-60.4 inline-registered clusters heal without a
//     migration. When both fail, the ORIGINAL backend error is returned so
//     backend-registered clusters keep their existing failure surface.
//
// When Backend is itself the ArgoCD provider (type argocd, or the
// in-cluster auto-default) every route reads the same place — the router
// short-circuits to a single Backend call, byte-identical to today.
type ClusterCredsRouter struct {
	// Backend is the configured cluster-test credentials provider. May be
	// nil (no backend configured) — inline routes still work via the ArgoCD
	// reader; backend routes return an explicit error.
	Backend ClusterCredentialsProvider

	// ArgoCDReaderFn lazily builds the ArgoCD-secret reader used for the
	// inline route. Per-instance seam so tests can inject a fake without
	// an in-cluster environment. Nil disables the inline route (fetches
	// behave exactly as before the router existed).
	ArgoCDReaderFn func() (ClusterCredentialsProvider, error)
}

// NewClusterCredsRouter builds the production router for the given backend
// provider + typed cluster-test config. The ArgoCD reader is constructed
// lazily on first use and cached (it builds a K8s client); construction
// failures are returned per-fetch, never at router-build time, so an
// out-of-cluster server with a pure-backend fleet is unaffected.
func NewClusterCredsRouter(backend ClusterCredentialsProvider, base ClusterTestProviderConfig) *ClusterCredsRouter {
	return &ClusterCredsRouter{
		Backend:        backend,
		ArgoCDReaderFn: DefaultArgoCDReaderFn(base),
	}
}

// DefaultArgoCDReaderFn returns a lazy, cached constructor for the ArgoCD
// cluster-Secret reader. Only ArgoCDNamespace is carried over from base —
// backend-shaped fields (Region / Prefix / Namespace) must not leak into
// the ArgoCD reader (V125-1-10.8 cross-contamination stance). For backend
// connection types the mapper leaves ArgoCDNamespace empty on purpose, so
// the reader falls through SHARKO_ARGOCD_NAMESPACE → "argocd" exactly like
// the auto-default path.
func DefaultArgoCDReaderFn(base ClusterTestProviderConfig) func() (ClusterCredentialsProvider, error) {
	return sync.OnceValues(func() (ClusterCredentialsProvider, error) {
		cfg := ClusterTestProviderConfig{Type: "argocd", ArgoCDNamespace: base.ArgoCDNamespace}
		return NewArgoCDProviderFromConfig(cfg)
	})
}

// ErrNoIndependentCredentialSource means the only place these credentials
// could be read from is the ArgoCD cluster Secret itself.
//
// It exists for the read-only connection comparison. Fetch (below) is allowed
// to fall back to reading the ArgoCD Secret, and should be — its callers want
// credentials that work and do not care where they came from. A comparison
// cannot use that route: building the expected Secret out of the live Secret
// compares the Secret with itself, matches every time, and reports a broken
// connection as fine. So the comparison calls StoredFactsIndependentOfArgoCDSecret,
// which refuses with this error instead of falling back.
var ErrNoIndependentCredentialSource = errors.New("this cluster's credentials are only available from the ArgoCD cluster Secret itself, so there is no independent copy to compare against")

// StoredFactsIndependentOfArgoCDSecret reports what the secrets BACKEND has
// STORED for the named cluster — never the ArgoCD cluster Secret, and never
// anything freshly created.
//
// # It cannot mint, and that is the point
//
// This is the ONLY route the read-only connection comparison takes to a
// backend, and it goes through StoredConnectionFactsProvider, which is a
// read-only capability. There is no branch in here that reaches GetCredentials,
// so there is no branch that reaches the STS token mint in getEKSToken. For an
// EKS cluster the backend holds metadata, not a credential; asking
// GetCredentials for it would create a real, usable, short-lived sign-in
// credential — a thing with a real blast radius — purely so the comparison
// could throw it away, because the mode policy has already ruled that the
// credential blob is not comparable. A read-only check must not create
// credentials as a side effect of checking.
//
// A backend that does NOT implement the read-only capability is refused rather
// than fetched from. Falling back to GetCredentials would reintroduce the mint
// through the back door.
//
// It refuses, with ErrNoIndependentCredentialSource, when:
//
//   - there is no backend at all;
//   - the backend IS the ArgoCD cluster-Secret reader (connection type argocd,
//     or the in-cluster auto-default), in which case "the backend" and "the
//     live Secret" are the same place;
//   - the backend cannot answer read-only (no StoredConnectionFacts method);
//   - credsSource is anything other than secret-kubeconfig or eks-token —
//     inline means the pasted kubeconfig only lives in the ArgoCD Secret, and
//     any other value means Sharko does not know, and never guesses.
//
// Unlike Fetch there is no healing fallback and no second attempt. A refusal
// is the right answer: the caller reports a narrower comparison scope rather
// than a comparison built on a guess.
//
// roleARN is deliberately NOT a parameter. A role is something you assume in
// order to mint; there is nothing here to assume it for.
func (r *ClusterCredsRouter) StoredFactsIndependentOfArgoCDSecret(lookupKey, credsSource string) (*StoredConnectionFacts, error) {
	if r == nil || r.Backend == nil {
		return nil, ErrNoIndependentCredentialSource
	}
	if _, backendIsArgo := r.Backend.(*ArgoCDProvider); backendIsArgo {
		return nil, ErrNoIndependentCredentialSource
	}
	switch credsSource {
	case models.CredsSourceSecretKubeconfig, models.CredsSourceEKSToken:
	default:
		return nil, ErrNoIndependentCredentialSource
	}
	reader, ok := r.Backend.(StoredConnectionFactsProvider)
	if !ok {
		return nil, ErrNoIndependentCredentialSource
	}
	return reader.StoredConnectionFacts(lookupKey)
}

// Fetch fetches credentials for the named cluster, routing by credsSource
// (see the type doc). name is the cluster name (the ArgoCD Secret key);
// lookupKey is the backend key (secretPath override when stored, else the
// name); credsSource is the stored creds source ("" = unknown/legacy).
//
// roleARN (V2-cleanup-62.2) is the per-cluster IAM role recorded on the
// cluster's managed-clusters.yaml entry ("" = none stored). It is forwarded
// to backend fetches through the RoleARNCredentialsProvider capability so
// EKS token minting assumes the cluster's own role; it never applies to the
// ArgoCD-reader route (inline clusters mint nothing).
func (r *ClusterCredsRouter) Fetch(name, lookupKey, credsSource, roleARN string) (*Kubeconfig, error) {
	if r == nil {
		return nil, fmt.Errorf("no credentials provider configured")
	}

	// Single-path short-circuit: the backend IS the ArgoCD reader (type
	// argocd or in-cluster auto-default) — routing cannot change anything.
	if _, backendIsArgo := r.Backend.(*ArgoCDProvider); backendIsArgo || r.ArgoCDReaderFn == nil {
		if r.Backend == nil {
			return nil, fmt.Errorf("no credentials provider configured")
		}
		return GetCredentialsWithOptionalRole(r.Backend, lookupKey, roleARN)
	}

	switch credsSource {
	case models.CredsSourceInlineKubeconfig:
		reader, err := r.ArgoCDReaderFn()
		if err != nil {
			return nil, fmt.Errorf("cluster %q was registered with an inline kubeconfig (credentials live in the ArgoCD cluster Secret, not the configured secrets backend) but the ArgoCD reader is unavailable: %w", name, err)
		}
		slog.Info("[creds-router] inline-registered cluster — reading credentials from the ArgoCD cluster Secret",
			"cluster", name)
		return reader.GetCredentials(name)

	case models.CredsSourceSecretKubeconfig, models.CredsSourceEKSToken:
		if r.Backend == nil {
			return nil, fmt.Errorf("cluster %q has creds_source %s but no secrets backend is configured", name, credsSource)
		}
		return GetCredentialsWithOptionalRole(r.Backend, lookupKey, roleARN)

	default:
		// Unknown / pre-60.4 record: backend first (today's behavior), then
		// heal via the ArgoCD reader — an inline-registered cluster from
		// before the credsSource field existed has nothing in the backend.
		if r.Backend == nil {
			return nil, fmt.Errorf("no credentials provider configured")
		}
		creds, backendErr := GetCredentialsWithOptionalRole(r.Backend, lookupKey, roleARN)
		if backendErr == nil {
			return creds, nil
		}
		if reader, readerErr := r.ArgoCDReaderFn(); readerErr == nil {
			if argoCreds, argoErr := reader.GetCredentials(name); argoErr == nil {
				slog.Warn("[creds-router] backend lookup failed but the ArgoCD cluster Secret has credentials — the cluster was likely registered with an inline kubeconfig before creds_source was recorded; using the ArgoCD read path",
					"cluster", name, "lookupKey", lookupKey, "backendError", backendErr)
				return argoCreds, nil
			}
		}
		// Both routes failed — surface the ORIGINAL backend error so
		// backend-registered clusters keep their existing failure surface
		// (typed errors, "not found" substring for the suggestion search).
		return nil, backendErr
	}
}
