package remoteclient

import (
	"errors"
	"net/url"
	"strings"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// tlsguard.go — task #152 lane C: Sharko refuses to deliver a secret value
// over a connection that skips TLS certificate checks.
//
// Why this matters: EnsureSecret puts a LIVE secret value on the wire. If
// the destination's connection is configured to skip certificate checks,
// anyone who can sit between Sharko and that cluster can terminate the TLS
// session themselves and read the value (and the bearer token) in the
// clear. client-go's own doc comment on rest.TLSClientConfig.Insecure says
// it plainly: "Server should be accessed without verifying the TLS
// certificate. For testing only."
//
// A destination can be unverified in two ways, and BOTH funnel through
// this package:
//
//   - the kubeconfig for the destination says
//     `insecure-skip-tls-verify: true`;
//   - the ArgoCD cluster config for the destination says
//     `insecure: true` — internal/providers/argocd_provider.go folds that
//     flag into the kubeconfig it synthesizes (buildTokenKubeconfig /
//     buildCertKubeconfig write `insecure-skip-tls-verify: true`), so by
//     the time the bytes reach NewClientFromKubeconfig the two shapes are
//     one shape.
//
// The refusal itself is layered the same way the foreign-secret ownership
// gate is (see ErrForeignSecret):
//
//   - NewClientFromKubeconfig marks any client it builds from a
//     skip-verify kubeconfig (unverifiedDestinationClient below), and
//     EnsureSecret — the single choke point every addon-secret write goes
//     through, for the scheduled engine, the "refresh now"/Sync door AND
//     the orchestrator's push path — refuses a marked client before making
//     any API call. No caller can forget to ask.
//   - internal/secrets/reconciler.go additionally asks CheckDestinationTLS
//     early (right after fetching credentials), so the recorded row
//     outcome is a deliberate refusal with a plain sentence rather than a
//     failed write attempt.
//
// What is deliberately NOT refused on a marked client:
//
//   - reads (Get/List/Watch) — diagnostics, doctor, discovery and the
//     managed-secrets listing keep working on a cluster that was
//     registered insecurely; no secret value leaves Sharko on a read.
//   - deletes (DeleteSecretIfManaged / DeleteManagedSecrets) — a delete
//     carries no secret value, and cleanup of an insecurely-registered
//     cluster (unadopt, remove-cluster) must keep working.
//   - writes made directly through the client by other packages
//     (internal/verify Stage1's dummy test secret, internal/diagnose's
//     probe secret) — those carry no real value; blocking them would
//     silently break registering the very clusters this guard is about,
//     which is a product decision this story does not make.

// ErrUnverifiedDestination means the destination cluster's connection is
// configured to skip TLS certificate checks — either its kubeconfig says
// `insecure-skip-tls-verify: true` or its ArgoCD cluster config says
// `insecure: true` — so Sharko refuses to send a secret value over it.
// Like ErrForeignSecret, the text is Sharko's own fixed, safe, complete
// sentence, written to be shown to a person verbatim; it never contains a
// secret value, a path, or wrapped SDK error text.
var ErrUnverifiedDestination = errors.New("this cluster's connection is set up to skip certificate checks, so Sharko will not send a secret over it")

// unverifiedDestinationClient marks a client whose underlying connection
// skips TLS certificate verification. It changes NOTHING about the
// client's behavior — every call passes straight through to the embedded
// Interface — it only lets EnsureSecret recognize, without access to the
// rest.Config, that this client must not carry a secret value.
type unverifiedDestinationClient struct {
	kubernetes.Interface
}

// destinationUnverified reports whether client was marked by
// NewClientFromKubeconfig as skipping TLS certificate verification.
func destinationUnverified(client kubernetes.Interface) bool {
	_, unverified := client.(unverifiedDestinationClient)
	return unverified
}

// connectionUnverified reports whether a destination's connection is unsafe
// to carry a secret value over. Two cases, both refused (task #152 lane C,
// and the plaintext-http gap story 152-I found afterwards):
//
//   - insecure is true — the kubeconfig (or the ArgoCD cluster config folded
//     into one) says `insecure-skip-tls-verify: true`. A MITM can terminate
//     the TLS session and read the value.
//   - the server URL is plain `http://` — there is no TLS at all, so the
//     value travels in the clear with no MITM even needed. This is strictly
//     worse than skip-verify, yet the original lane C check only looked at
//     the Insecure flag and let an http destination straight through. Reads
//     and deletes still work against such a cluster (they carry no value);
//     only the value-carrying write path refuses, exactly as for skip-verify.
//
// A schemeless or non-http(s) host is left to the pre-existing behavior (the
// Insecure flag only) — a real Kubernetes API server is always https, so
// this only ever ADDS a refusal for an explicit plaintext http destination,
// never removes one.
func connectionUnverified(insecure bool, host string) bool {
	if insecure {
		return true
	}
	if u, err := url.Parse(host); err == nil && strings.EqualFold(u.Scheme, "http") {
		return true
	}
	return false
}

// CheckDestinationTLS reports, from raw kubeconfig bytes, whether Sharko
// would refuse to deliver a secret to this destination. Returns
// ErrUnverifiedDestination when the kubeconfig skips TLS certificate
// checks (and the test-only bypass is off), nil otherwise.
//
// Callers use this to refuse EARLY — before fetching a value from the
// secrets provider, before building a client — and to record a deliberate
// refusal instead of a failed write. The hard stop in EnsureSecret does
// not depend on anyone calling this.
//
// Unparseable bytes return nil, deliberately: this function cannot build
// a client from them and neither can NewClientFromKubeconfig, whose parse
// error is the one the caller should surface. Failing "open" here cannot
// open a write path — no client, no write.
func CheckDestinationTLS(kubeconfig []byte) error {
	if allowUnverifiedDestinations {
		return nil
	}
	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return nil
	}
	if connectionUnverified(restConfig.TLSClientConfig.Insecure, restConfig.Host) {
		return ErrUnverifiedDestination
	}
	return nil
}
