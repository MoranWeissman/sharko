// Package argosecrets manages ArgoCD cluster secrets in the argocd namespace.
// ArgoCD discovers clusters via K8s Secrets labelled with
// argocd.argoproj.io/secret-type: cluster. This package creates and updates
// those secrets so that ArgoCD's ApplicationSet cluster generator can discover
// Sharko-managed clusters without storing static credentials.
//
// Ownership split (V2-cleanup-28):
//
//   - Sharko-CREATED secrets (managed-by label, NO adopted annotation):
//     fully managed — Ensure rewrites connection data on drift; orphan sweeps
//     delete them when they leave managed-clusters.yaml.
//
//   - Sharko-ADOPTED secrets (managed-by label + adopted annotation):
//     labels managed only — Ensure merges desired labels but never touches
//     Data/StringData; orphan sweeps skip them; removal requires an explicit
//     Unadopt call.
package argosecrets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/MoranWeissman/sharko/internal/models"
)

const (
	// LabelSecretType is the ArgoCD label that marks a secret as a cluster secret.
	LabelSecretType = "argocd.argoproj.io/secret-type"
	// LabelManagedBy is the standard K8s label indicating which tool manages the resource.
	LabelManagedBy = "app.kubernetes.io/managed-by"
	// ManagedByValue is the value applied to LabelManagedBy for all Sharko-managed resources.
	ManagedByValue = "sharko"
)

// Annotation keys used by Sharko on ArgoCD cluster secrets.
const (
	// AnnotationAdopted marks a cluster as adopted (vs. registered from scratch).
	// Canonical key as of V2-cleanup-60.5 (L10): the V2-cleanup-59 rename
	// landed "sharko.sharko.dev/adopted", carrying a historical doubled
	// "sharko." prefix into the new domain. Zero adopters existed while that
	// spelling was live, so this is the one and only chance to correct it
	// for free. Writers stamp ONLY this key.
	AnnotationAdopted = "sharko.dev/adopted"

	// AnnotationAdoptedDoubledPrefixLegacy is the short-lived V2-cleanup-59
	// canonical spelling ("sharko.sharko.dev/adopted"), superseded by
	// AnnotationAdopted (L10, V2-cleanup-60.5). Only ever READ.
	AnnotationAdoptedDoubledPrefixLegacy = "sharko.sharko.dev/adopted"

	// AnnotationAdoptedLegacy is the pre-V2-cleanup-59 adopted key
	// (sharko.io — a domain the project never owned). Only ever READ:
	// clusters adopted before the group rename carry it on their live
	// ArgoCD Secret, and it must keep protecting them from the orphan
	// sweeps for all of v2.x. Writers stamp only AnnotationAdopted;
	// Unadopt removes all three spellings. Use IsAdopted for every
	// presence check.
	AnnotationAdoptedLegacy = "sharko.sharko.io/adopted"
)

// IsAdopted reports whether annotations mark the cluster Secret as adopted,
// under ANY of the three key spellings (canonical, the short-lived
// doubled-prefix spelling, or the original pre-rename legacy key —
// V2-cleanup-60.5 READ-ALL-THREE). nil-safe. This is the single shared
// predicate for the adopted state — internal/clusterreconciler's orphan
// sweep uses it too, so the two reconcilers can never disagree about what
// "adopted" means.
func IsAdopted(annotations map[string]string) bool {
	if annotations == nil {
		return false
	}
	return annotations[AnnotationAdopted] == "true" ||
		annotations[AnnotationAdoptedDoubledPrefixLegacy] == "true" ||
		annotations[AnnotationAdoptedLegacy] == "true"
}

// Tracking markers ArgoCD itself stamps on a resource when ANOTHER
// Application's sync renders it from Git (V2-cleanup-89.5). These are
// ArgoCD's own markers, never written by Sharko — Sharko only READS them,
// to detect the "someone else's Application also owns this Secret" case
// for user-managed connections (connectionManagedBy: user). ArgoCD
// supports two mutually exclusive tracking methods, configured cluster-wide
// via argocd-cm's application.resourceTrackingMethod: "annotation" (the
// default since ArgoCD 2.x) stamps AnnotationTrackingID; "label" stamps
// LabelAppInstance instead. Both are checked so Sharko works regardless of
// which method the maintainer's ArgoCD install uses.
const (
	// AnnotationTrackingID is the annotation ArgoCD stamps under
	// trackingMethod=annotation (the default). Its value has the form
	// "<app-name>:<group>/<kind>:<namespace>/<name>" — see
	// ParseTrackingAppName.
	AnnotationTrackingID = "argocd.argoproj.io/tracking-id"
	// LabelAppInstance is the label ArgoCD stamps under trackingMethod=label.
	// Its value IS the owning Application's name directly — no parsing needed.
	LabelAppInstance = "app.kubernetes.io/instance"
)

// ParseTrackingAppName extracts the owning Application's name from an
// ArgoCD tracking-id annotation value. The format is
// "<app-name>:<group>/<kind>:<namespace>/<name>" (e.g.
// "my-app:/Secret:argocd/prod-eu" for a core-group Secret) — the app name
// is everything before the FIRST colon, since the app name itself cannot
// contain a colon but the group/kind/namespace/name segments that follow
// do. Returns ("", false) when trackingID is empty or has no colon at all
// (not a value ArgoCD would ever produce, but defensive against a
// hand-edited or malformed annotation).
func ParseTrackingAppName(trackingID string) (string, bool) {
	idx := strings.Index(trackingID, ":")
	if idx <= 0 {
		return "", false
	}
	return trackingID[:idx], true
}

// ParseTrackingID parses an ArgoCD tracking-id annotation value of the
// form "<app-name>:<group>/<kind>:<namespace>/<name>" into its app name
// and its "<namespace>/<name>" suffix. It is a SplitN(_, ":", 3) rather
// than two separate Index scans: the app name cannot itself contain a
// colon, and neither can a K8s API group or Kind name, so the FIRST two
// colons reliably separate "<app-name>" from "<group>/<kind>" from
// everything after — the third field is taken whole as nsName, so a
// hand-edited or unusual value that happens to embed extra colons inside
// its namespace/name segment is captured intact (not truncated or
// panicked on). Such a value will then simply fail an exact-match compare
// against a real "<namespace>/<name>" (see DetectForeignOwner) — a safe,
// conservative outcome rather than a crash or a false positive.
//
// Returns ok=false when trackingID has fewer than two colons (not a value
// ArgoCD would ever produce, but defensive against a malformed or empty
// annotation).
func ParseTrackingID(trackingID string) (appName, nsName string, ok bool) {
	parts := strings.SplitN(trackingID, ":", 3)
	if len(parts) != 3 || parts[0] == "" {
		return "", "", false
	}
	return parts[0], parts[2], true
}

// OwnerConfidence classifies how sure DetectForeignOwner is that the
// application name it found really identifies THIS secret's renderer, as
// opposed to a marker that merely happens to be present (V2-cleanup-90.1 —
// review finding H1: the label-only signal is also the standard Helm
// release label, so treating it as equally certain as a verified
// tracking-id match false-positived every plain Helm-created secret).
type OwnerConfidence string

const (
	// ConfidenceHard: the tracking-id annotation is present AND its own
	// "<namespace>/<name>" suffix matches the secret being inspected.
	// ArgoCD always stamps this annotation with the resource's own
	// coordinates, so a match means an ArgoCD Application really does
	// render THIS secret from Git.
	ConfidenceHard OwnerConfidence = "hard"
	// ConfidenceSoft covers two weaker signals:
	//   - the tracking-id annotation is present but its "<namespace>/<name>"
	//     suffix does NOT match this secret — most likely copied or cloned
	//     from another resource (e.g. `kubectl get -o yaml | kubectl apply`),
	//     so the app name found is not reliably THIS secret's owner; or
	//   - only the app.kubernetes.io/instance label is present. That label
	//     is ALSO the standard `helm.sh`-adjacent release label every plain
	//     `helm install` stamps on everything it creates, with no ArgoCD
	//     involvement at all — its presence alone is not proof of ArgoCD
	//     ownership.
	ConfidenceSoft OwnerConfidence = "soft"
)

// DetectForeignOwner inspects secret's tracking markers (both possible
// ArgoCD trackingMethod spellings) and returns the name of the application
// that may render it from Git, if any, plus a confidence signal
// distinguishing a verified ArgoCD match (ConfidenceHard) from a weaker,
// possibly-Helm-only signal (ConfidenceSoft — see OwnerConfidence). Checks
// the tracking-id annotation first (the default trackingMethod), then
// falls back to the app.kubernetes.io/instance label. nil-safe.
func DetectForeignOwner(secret *corev1.Secret) (appName string, confidence OwnerConfidence, found bool) {
	if secret == nil {
		return "", "", false
	}
	if trackingID := secret.Annotations[AnnotationTrackingID]; trackingID != "" {
		if name, nsName, ok := ParseTrackingID(trackingID); ok {
			if nsName == secret.Namespace+"/"+secret.Name {
				return name, ConfidenceHard, true
			}
			return name, ConfidenceSoft, true
		}
	}
	if instance := secret.Labels[LabelAppInstance]; instance != "" {
		return instance, ConfidenceSoft, true
	}
	return "", "", false
}

// GetTrackingOwner fetches the named secret and reports whether it carries
// a tracking marker that may belong to another application's sync (see
// DetectForeignOwner), along with the confidence of that signal. Mirrors
// SyncLabelsOnly's not-found handling: a missing secret is NOT an error —
// found=false, err=nil — because this is commonly called before the user
// has created their self-managed connection's Secret yet.
func (m *Manager) GetTrackingOwner(ctx context.Context, name string) (appName string, confidence OwnerConfidence, found bool, err error) {
	existing, getErr := m.client.CoreV1().Secrets(m.namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(getErr) {
		return "", "", false, nil
	}
	if getErr != nil {
		return "", "", false, fmt.Errorf("getting secret %q in namespace %q: %w", name, m.namespace, getErr)
	}
	appName, confidence, found = DetectForeignOwner(existing)
	return appName, confidence, found, nil
}

// SecretOwnership is the ownership signal for an ArgoCD cluster Secret,
// read from a single Get. It exists so callers that need BOTH the
// managed-by label and the foreign-tracking-owner signal (e.g. the
// connection doctor's check 5) do not have to issue two separate Gets for
// the same object — the pre-90.1 pattern was a double API cost and a
// (small) race window between the two reads.
type SecretOwnership struct {
	// ManagedBy is the app.kubernetes.io/managed-by label value ("" if unset).
	ManagedBy string
	// ForeignOwnerAppName / ForeignOwnerConfidence / ForeignOwnerFound mirror
	// DetectForeignOwner's return values for this same secret.
	ForeignOwnerAppName    string
	ForeignOwnerConfidence OwnerConfidence
	ForeignOwnerFound      bool
}

// GetSecretOwnership fetches the named secret ONCE and derives both the
// managed-by label and the foreign-tracking-owner signal from that single
// object. Returns (ownership, found, err): found=false, err=nil when the
// Secret does not exist yet — not an error, same convention as
// GetTrackingOwner — so callers can tell "nothing to check yet" apart from
// a real read failure (permission, timeout, etc.) without inspecting err.
func (m *Manager) GetSecretOwnership(ctx context.Context, name string) (ownership SecretOwnership, found bool, err error) {
	existing, getErr := m.client.CoreV1().Secrets(m.namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(getErr) {
		return SecretOwnership{}, false, nil
	}
	if getErr != nil {
		return SecretOwnership{}, false, fmt.Errorf("getting secret %q in namespace %q: %w", name, m.namespace, getErr)
	}
	appName, confidence, foreignFound := DetectForeignOwner(existing)
	return SecretOwnership{
		ManagedBy:              existing.Labels[LabelManagedBy],
		ForeignOwnerAppName:    appName,
		ForeignOwnerConfidence: confidence,
		ForeignOwnerFound:      foreignFound,
	}, true, nil
}

// ClusterSecretSpec is the desired state for an ArgoCD cluster secret.
type ClusterSecretSpec struct {
	// Name is the cluster name. Used as both the K8s Secret name and stringData.name.
	Name string
	// Server is the API server URL (e.g. https://XXXXX.gr7.us-east-1.eks.amazonaws.com).
	Server string
	// Region is the AWS region used by argocd-k8s-auth for EKS token generation.
	Region string
	// RoleARN is the IAM role ARN passed to argocd-k8s-auth via --role-arn.
	// When empty the --role-arn flag is omitted from execProviderConfig.args.
	RoleARN string
	// Token is a static bearer token for direct cluster authentication.
	// When non-empty, the secret config is written in ArgoCD's bearerToken
	// shape ({"bearerToken": ..., "tlsClientConfig": {...}}) instead of the
	// execProviderConfig (argocd-k8s-auth) shape. This is the path used by
	// kubeconfig-registered clusters, whose pasted credentials carry a
	// bearer token that ArgoCDProvider.GetCredentials reads back directly.
	// When empty, the execProviderConfig (EKS / IAM) shape is used.
	Token string
	// CertData is the base64-encoded PEM client certificate for mTLS
	// authentication (kind / kubeadm / on-prem clusters). When BOTH CertData
	// and KeyData are non-empty the secret config is written in ArgoCD's
	// plain-TLS shape ({"tlsClientConfig": {"certData": ..., "keyData": ...,
	// "caData": ...}}) — no exec, no bearer token. Cert-pair precedence is
	// ABOVE Token: a spec carrying both a cert pair and a token emits the
	// cert shape (V2-cleanup-56.1).
	CertData string
	// KeyData is the base64-encoded PEM client key paired with CertData.
	// A half pair (cert without key, or key without cert) never takes the
	// cert branch — the spec falls through to the token / exec precedence
	// exactly as before.
	KeyData string
	// CAData is the base64-encoded PEM CA certificate for TLS verification of the cluster API server.
	// When non-empty it is written into tlsClientConfig.caData so ArgoCD can verify the server cert.
	// When empty, ArgoCD falls back to system trust roots.
	CAData string
	// Labels contains addon labels from cluster-addons.yaml (e.g. addon-datadog: "true").
	// These are merged with system labels before writing to the secret.
	Labels map[string]string
	// Annotations contains optional annotations to set on the secret (e.g. adopted marker).
	Annotations map[string]string
}

// Manager creates and reconciles ArgoCD cluster secrets in a target namespace.
type Manager struct {
	client    kubernetes.Interface
	namespace string
}

// NewManager returns a Manager that writes cluster secrets into namespace.
// namespace is typically "argocd".
func NewManager(client kubernetes.Interface, namespace string) *Manager {
	return &Manager{
		client:    client,
		namespace: namespace,
	}
}

// execProviderConfig is the JSON structure written into secret stringData.config.
type execProviderConfig struct {
	ExecProviderConfig execProvider `json:"execProviderConfig"`
	TLSClientConfig    tlsConfig    `json:"tlsClientConfig"`
}

// bearerTokenConfig is the JSON structure written into secret
// stringData.config for clusters that authenticate with a static bearer
// token (the kubeconfig registration path). It matches the shape ArgoCD
// itself writes for bearer-token clusters and the shape
// providers.ArgoCDProvider.GetCredentials reads back via
// buildBearerTokenKubeconfig.
type bearerTokenConfig struct {
	BearerToken     string    `json:"bearerToken"`
	TLSClientConfig tlsConfig `json:"tlsClientConfig"`
}

// certTLSConfig is the JSON structure written into secret stringData.config
// for clusters that authenticate with a client certificate + key pair
// (kind / kubeadm / on-prem clusters registered from a cert-based
// kubeconfig). It is ArgoCD's plain-TLS shape: no execProviderConfig, no
// bearerToken — just tlsClientConfig carrying the cert pair and CA bundle
// (V2-cleanup-56.1).
type certTLSConfig struct {
	TLSClientConfig certTLSClientConfig `json:"tlsClientConfig"`
}

// certTLSClientConfig extends the tlsConfig fields with certData/keyData.
// Declared separately so the bearer and exec shapes stay byte-identical to
// their pre-56.1 output (their tlsClientConfig never gains new keys).
type certTLSClientConfig struct {
	Insecure bool   `json:"insecure"`
	CertData string `json:"certData"`
	KeyData  string `json:"keyData"`
	CAData   string `json:"caData,omitempty"`
}

type execProvider struct {
	Command    string   `json:"command"`
	Args       []string `json:"args"`
	APIVersion string   `json:"apiVersion"`
}

type tlsConfig struct {
	Insecure bool   `json:"insecure"`
	CAData   string `json:"caData,omitempty"`
}

// BuildSecretConfigJSON is the public wrapper around buildSecretConfig.
// It exists so the clusterreconciler (internal/clusterreconciler) can
// construct the exact same config payload (cert / bearer / exec shape,
// per the cert > token > exec precedence) this package's
// Manager.Ensure writes — without going through Ensure's adoption path
// (which the reconciler deliberately avoids per the ownership policy).
// Output is byte-identical to Ensure's output for the same spec, so
// ArgoCD's auth code path resolves the same regardless of which writer
// mutated the Secret.
func BuildSecretConfigJSON(spec ClusterSecretSpec) (string, error) {
	return buildSecretConfig(spec)
}

// BuildClusterSecretLabels is the public wrapper around buildLabels.
// Same motivation as BuildSecretConfigJSON — the clusterreconciler
// constructs Secrets without going through Ensure, and must apply the
// identical label set (system labels + caller-supplied addon labels
// with system labels winning conflicts) so its writes are
// indistinguishable from this package's writes.
func BuildClusterSecretLabels(spec ClusterSecretSpec) map[string]string {
	return buildLabels(spec)
}

// buildSecretConfig constructs the ArgoCD cluster Secret data["config"] JSON.
//
// Shape precedence (V2-cleanup-56.1): cert pair > token > exec.
//
// When BOTH spec.CertData and spec.KeyData are non-empty the plain-TLS shape
// is emitted: tlsClientConfig carrying certData + keyData + caData, with no
// execProviderConfig and no bearerToken. This is the shape ArgoCD needs for
// client-certificate kubeconfigs (kind / kubeadm / on-prem); without it those
// clusters were silently written as EKS exec and argocd-k8s-auth failed
// forever from a non-AWS environment. A half pair (cert without key or vice
// versa) never takes this branch.
//
// Else, when spec.Token is non-empty the bearerToken shape is emitted (the
// kubeconfig registration path): a static token plus a tlsClientConfig that
// carries the CA bundle (or insecure:true when no CA is present). This is the
// shape providers.ArgoCDProvider.GetCredentials can read back directly, which
// is what makes a kubeconfig-registered cluster reachable.
//
// Otherwise the execProviderConfig (argocd-k8s-auth / EKS) shape is emitted.
// The --role-arn arg is only included when spec.RoleARN is non-empty.
// No env vars are set: argocd-k8s-auth inherits the environment from ArgoCD and
// ArgoCD v2.14 cannot unmarshal the env field in execProviderConfig.
//
// The bearer and exec outputs are byte-identical to their pre-56.1 shapes —
// pinned by golden tests — so existing EKS / bearer-token clusters see no
// secret churn from this change.
func buildSecretConfig(spec ClusterSecretSpec) (string, error) {
	if spec.CertData != "" && spec.KeyData != "" {
		cfg := certTLSConfig{
			TLSClientConfig: certTLSClientConfig{
				Insecure: false,
				CertData: spec.CertData,
				KeyData:  spec.KeyData,
				CAData:   spec.CAData,
			},
		}
		b, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return "", fmt.Errorf("marshalling certTLSConfig: %w", err)
		}
		return string(b), nil
	}

	if spec.Token != "" {
		cfg := bearerTokenConfig{
			BearerToken: spec.Token,
			TLSClientConfig: tlsConfig{
				// No CA bundle → fall back to skipping TLS verification, the
				// same choice ArgoCDProvider.buildBearerTokenKubeconfig makes
				// when caData is absent.
				Insecure: spec.CAData == "",
				CAData:   spec.CAData,
			},
		}
		b, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return "", fmt.Errorf("marshalling bearerTokenConfig: %w", err)
		}
		return string(b), nil
	}

	args := []string{"aws", "--cluster-name", spec.Name}
	if spec.RoleARN != "" {
		args = append(args, "--role-arn", spec.RoleARN)
	}

	cfg := execProviderConfig{
		ExecProviderConfig: execProvider{
			Command:    "argocd-k8s-auth",
			Args:       args,
			APIVersion: "client.authentication.k8s.io/v1beta1",
		},
		TLSClientConfig: tlsConfig{
			Insecure: false,
			CAData:   spec.CAData,
		},
	}

	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshalling execProviderConfig: %w", err)
	}
	return string(b), nil
}

// buildLabels merges system labels with the caller-supplied addon labels.
// System labels always take precedence, preventing callers from overriding them.
func buildLabels(spec ClusterSecretSpec) map[string]string {
	labels := make(map[string]string, len(spec.Labels)+2)
	for k, v := range spec.Labels {
		labels[k] = v
	}
	// System labels applied last so they cannot be overridden.
	labels[LabelSecretType] = "cluster"
	labels[LabelManagedBy] = ManagedByValue
	return labels
}

// hashSecretState returns a deterministic SHA-256 hex digest covering both
// the secret's labels and its data bytes. Keys are sorted before hashing so
// map-iteration order has no effect on the result.
//
// When reading an existing secret from the K8s API, values are returned as
// Data ([]byte), not StringData. Pass secret.Data here.
// For the desired state, convert StringData values to []byte before passing.
func hashSecretState(labels map[string]string, data map[string][]byte) string {
	h := sha256.New()

	// Hash labels (sorted).
	lkeys := make([]string, 0, len(labels))
	for k := range labels {
		lkeys = append(lkeys, k)
	}
	sort.Strings(lkeys)
	for _, k := range lkeys {
		h.Write([]byte(k))
		h.Write([]byte{0})
		h.Write([]byte(labels[k]))
		h.Write([]byte{0})
	}

	// Hash data (sorted).
	dkeys := make([]string, 0, len(data))
	for k := range data {
		dkeys = append(dkeys, k)
	}
	sort.Strings(dkeys)
	for _, k := range dkeys {
		h.Write([]byte(k))
		h.Write([]byte{0})
		h.Write(data[k])
		h.Write([]byte{0})
	}

	return hex.EncodeToString(h.Sum(nil))
}

// Ensure creates or updates the ArgoCD cluster secret for spec.
// It skips the K8s API write if the existing secret already matches the
// desired state (labels + config), preventing unnecessary churn.
// Returns (changed bool, err error): changed is true on create, adopt, or update paths;
// false on the skip path.
func (m *Manager) Ensure(ctx context.Context, spec ClusterSecretSpec) (bool, error) {
	configJSON, err := buildSecretConfig(spec)
	if err != nil {
		return false, fmt.Errorf("building secret config for cluster %q: %w", spec.Name, err)
	}

	desiredLabels := buildLabels(spec)
	desiredStringData := map[string]string{
		"name":   spec.Name,
		"server": spec.Server,
		"config": configJSON,
	}
	// Convert to []byte for hashing — mirrors what K8s returns in secret.Data.
	desiredData := make(map[string][]byte, len(desiredStringData))
	for k, v := range desiredStringData {
		desiredData[k] = []byte(v)
	}
	desiredHash := hashSecretState(desiredLabels, desiredData)

	existing, err := m.client.CoreV1().Secrets(m.namespace).Get(ctx, spec.Name, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return false, fmt.Errorf("getting secret %q in namespace %q: %w", spec.Name, m.namespace, err)
	}

	if apierrors.IsNotFound(err) {
		// Secret does not exist — create it.
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:        spec.Name,
				Namespace:   m.namespace,
				Labels:      desiredLabels,
				Annotations: spec.Annotations,
			},
			Type:       corev1.SecretTypeOpaque,
			StringData: desiredStringData,
		}
		if _, createErr := m.client.CoreV1().Secrets(m.namespace).Create(ctx, secret, metav1.CreateOptions{}); createErr != nil {
			return false, fmt.Errorf("creating secret %q in namespace %q: %w", spec.Name, m.namespace, createErr)
		}
		slog.Info("[argosecrets] cluster secret created",
			"cluster", spec.Name, "namespace", m.namespace,
		)
		return true, nil
	}

	// Secret exists — check whether we manage it or need to adopt it.
	if existing.Labels[LabelManagedBy] != ManagedByValue {
		// Adoption path: secret exists but was not created by Sharko.
		// Apply the managed-by label, merge desired labels (foreign labels kept),
		// stamp the adopted annotation, and preserve the existing Data/StringData
		// so we do not wipe connection config we do not model.
		// Always write — adoption is itself a meaningful state change.
		adopted := existing.DeepCopy()
		// Merge desired labels into existing labels; desired labels win on conflict,
		// but existing foreign labels not in the desired set are kept (guest semantics).
		if adopted.Labels == nil {
			adopted.Labels = make(map[string]string, len(desiredLabels))
		}
		for k, v := range desiredLabels {
			adopted.Labels[k] = v
		}
		// Adopted gate (V2-cleanup-29): adopted clusters are guests in a shared
		// hub-and-spoke ArgoCD; NEVER stamp the connectivity-check label on them,
		// not even for one tick. Strip it here (both key spellings) — the
		// takeover write is the ONLY reliable place the existing secret and its
		// AnnotationAdopted are both in hand simultaneously, so this is the
		// canonical adopted gate.
		models.RemoveConnectivityCheckLabels(adopted.Labels)
		// Always stamp the adopted annotation so the orphan sweeps recognise the
		// secret as adopted even before the orchestrator's SetAnnotation call
		// completes (closes the reconciler-fires-first race window).
		if adopted.Annotations == nil {
			adopted.Annotations = make(map[string]string)
		}
		adopted.Annotations[AnnotationAdopted] = "true"
		// Normalise any older adopted marker while we are writing anyway:
		// the canonical key above supersedes both (V2-cleanup-59, V2-cleanup-60.5).
		delete(adopted.Annotations, AnnotationAdoptedDoubledPrefixLegacy)
		delete(adopted.Annotations, AnnotationAdoptedLegacy)
		if spec.Annotations != nil {
			adopted.Annotations = mergeAnnotations(adopted.Annotations, spec.Annotations)
		}
		// IMPORTANT: do NOT touch adopted.Data or adopted.StringData — the existing
		// connection config (TLS extras, shard, namespaces, exec details) must be
		// preserved verbatim. K8s Update replaces the whole object; we mutate only
		// labels and annotations on the DeepCopy.
		if _, adoptErr := m.client.CoreV1().Secrets(m.namespace).Update(ctx, adopted, metav1.UpdateOptions{}); adoptErr != nil {
			return false, fmt.Errorf("adopting secret %q in namespace %q: %w", spec.Name, m.namespace, adoptErr)
		}
		slog.Info("[argosecrets] cluster secret adopted — labels merged, connection data preserved",
			"cluster", spec.Name, "namespace", m.namespace,
		)
		return true, nil
	}

	// Secret is managed by Sharko. Branch on whether it is adopted.
	// IsAdopted recognises the legacy annotation key too, so clusters
	// adopted before the group rename stay on the labels-only path.
	isAdopted := IsAdopted(existing.Annotations)

	if isAdopted {
		// Adopted secret — converge LABELS only. Never touch Data/StringData.
		// "Labels match" means all desired labels are present in the current
		// labels with the correct values. Foreign labels (not in desired) are
		// always kept, so they do not trigger a write.
		//
		// Adopted gate (V2-cleanup-29): strip the connectivity-check label from
		// the desired set before comparing and merging. Adopted clusters are guests;
		// the check label must never appear on them, even when the caller's spec
		// inadvertently included it (e.g. both reconcilers running concurrently).
		adoptedDesired := make(map[string]string, len(desiredLabels))
		for k, v := range desiredLabels {
			adoptedDesired[k] = v
		}
		models.RemoveConnectivityCheckLabels(adoptedDesired)

		// A lingering check label (either key spelling) on the live Secret
		// must also force a write so the strip below actually runs — the
		// subset check alone would skip when the addon labels match.
		if !models.HasConnectivityCheckLabel(existing.Labels) &&
			desiredLabelsSubset(existing.Labels, adoptedDesired) {
			slog.Debug("[argosecrets] adopted cluster secret labels up-to-date, skipping",
				"cluster", spec.Name, "namespace", m.namespace,
			)
			return false, nil
		}
		updated := existing.DeepCopy()
		// Merge: desired wins on conflict, existing foreign keys kept.
		// Remove the check label (both key spellings) if it somehow ended
		// up on the secret.
		for k, v := range adoptedDesired {
			updated.Labels[k] = v
		}
		models.RemoveConnectivityCheckLabels(updated.Labels)
		if spec.Annotations != nil {
			updated.Annotations = mergeAnnotations(updated.Annotations, spec.Annotations)
		}
		// Do NOT touch updated.Data or updated.StringData.
		if _, updateErr := m.client.CoreV1().Secrets(m.namespace).Update(ctx, updated, metav1.UpdateOptions{}); updateErr != nil {
			return false, fmt.Errorf("updating labels on adopted secret %q in namespace %q: %w", spec.Name, m.namespace, updateErr)
		}
		slog.Info("[argosecrets] adopted cluster secret labels converged — connection data untouched",
			"cluster", spec.Name, "namespace", m.namespace,
		)
		return true, nil
	}

	// Sharko-created secret (managed-by label present, NO adopted annotation):
	// compare hashes to decide whether a full update is needed.
	existingHash := hashSecretState(existing.Labels, existing.Data)
	if existingHash == desiredHash {
		slog.Debug("[argosecrets] cluster secret up-to-date, skipping",
			"cluster", spec.Name, "namespace", m.namespace,
		)
		return false, nil
	}

	// Hashes differ — update in place, preserving any fields we did not set.
	updated := existing.DeepCopy()
	updated.Labels = desiredLabels
	if spec.Annotations != nil {
		updated.Annotations = mergeAnnotations(updated.Annotations, spec.Annotations)
	}
	updated.Data = nil
	updated.StringData = desiredStringData
	if _, updateErr := m.client.CoreV1().Secrets(m.namespace).Update(ctx, updated, metav1.UpdateOptions{}); updateErr != nil {
		return false, fmt.Errorf("updating secret %q in namespace %q: %w", spec.Name, m.namespace, updateErr)
	}
	slog.Info("[argosecrets] cluster secret updated",
		"cluster", spec.Name, "namespace", m.namespace,
	)
	return true, nil
}

// SyncLabelsOnly converges ONLY the addon labels on an existing ArgoCD
// cluster Secret — the write primitive for self-managed connections
// (connectionManagedBy: user, V2-cleanup-57.2). The user creates and
// maintains the Secret; Sharko is a guest that merges addon labels onto it
// and touches NOTHING else:
//
//   - Data / StringData / annotations are never modified — the connection
//     config (bearer token, cert pair, exec details, shard, namespaces) is
//     the user's, verbatim.
//   - Sharko does NOT stamp the managed-by ownership label and does NOT
//     stamp argocd.argoproj.io/secret-type — the user authored the Secret;
//     claiming ownership would put it back in scope for the orphan sweeps.
//   - The connectivity-check label is stripped from both the desired set
//     and the live Secret (same guest stance as adopted clusters —
//     V2-cleanup-29).
//   - Mode-switch handover (sharko → user): if the Secret still carries the
//     managed-by=sharko label from its earlier Sharko-managed life AND does
//     NOT carry the adopted annotation, the label is removed so the orphan
//     sweeps can never reclaim (delete) the user's connection later. An
//     ADOPTED Secret keeps its label + annotation — the adopt rail is
//     already delete-proof and Unadopt depends on them.
//   - Legacy value normalization (V2-cleanup-60 H3): every addon-label value
//     is passed through models.NormalizeAddonLabelValue BEFORE it is
//     compared against the live Secret or written. This is the single choke
//     point both reconcilers converge through — the legacy
//     internal/argosecrets reconciler still passes raw cluster.Labels (which
//     may carry the old "true"/"false" values) while
//     internal/clusterreconciler's syncSelfManaged pre-normalizes before
//     calling in. Without normalizing here too, a cluster hand-switched to
//     self-managed with old-style labels would converge to
//     "enabled"/"disabled" on whichever reconciler ran the write, then the
//     OTHER reconciler would see its own (unnormalized) desired set as
//     mismatched and rewrite it back — an infinite flip that toggles the
//     addon ApplicationSet's selection and makes ArgoCD deploy/prune the
//     addon forever. Normalizing on every call, regardless of what the
//     caller passes, means both callers' desired sets collapse to the same
//     canonical map and the write converges in one pass.
//
// Merge semantics match the adopted path: desired addon labels win on
// conflict, foreign labels are kept. The mutation is metadata-only on a
// DeepCopy followed by a single Update — the same "label-only patch" idiom
// Ensure's adopted branch uses.
//
// Returns:
//   - found == false → no Secret with that name exists yet. NOT an error:
//     the caller surfaces a visible "waiting for user-created Secret"
//     pending state instead of an error loop.
//   - changed == true → a label write occurred.
//   - changed == false, found == true → labels already converged; no write.
func (m *Manager) SyncLabelsOnly(ctx context.Context, name string, addonLabels map[string]string) (changed bool, found bool, err error) {
	existing, getErr := m.client.CoreV1().Secrets(m.namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(getErr) {
		return false, false, nil
	}
	if getErr != nil {
		return false, false, fmt.Errorf("getting secret %q in namespace %q: %w", name, m.namespace, getErr)
	}

	// Desired = the caller's addon labels, minus the connectivity-check
	// label (guest stance — never stamped on a connection Sharko does not
	// own). We deliberately do NOT add LabelManagedBy or LabelSecretType.
	//
	// Normalize legacy "true"/"false" values to the canonical
	// "enabled"/"disabled" vocabulary HERE, regardless of which reconciler
	// called in or whether it already normalized (H3 — see doc comment
	// above). Normalizing an already-canonical value is a no-op, so this is
	// safe to run unconditionally on every call.
	desired := make(map[string]string, len(addonLabels))
	for k, v := range addonLabels {
		if normalized, changed := models.NormalizeAddonLabelValue(v); changed {
			v = normalized
		}
		desired[k] = v
	}
	models.RemoveConnectivityCheckLabels(desired)

	// IsAdopted recognises the legacy annotation key too (V2-cleanup-59).
	isAdopted := IsAdopted(existing.Annotations)
	// Handover check: strip Sharko's ownership label on a plain (non-adopted)
	// Secret so the orphan sweeps never treat the user's connection as
	// Sharko-owned again.
	stripManagedBy := !isAdopted && existing.Labels[LabelManagedBy] == ManagedByValue
	// The check label (either key spelling) must not linger from a
	// Sharko-managed past either.
	stripCheckLabel := models.HasConnectivityCheckLabel(existing.Labels)

	if !stripManagedBy && !stripCheckLabel && desiredLabelsSubset(existing.Labels, desired) {
		slog.Debug("[argosecrets] self-managed cluster secret labels up-to-date, skipping",
			"cluster", name, "namespace", m.namespace,
		)
		return false, true, nil
	}

	updated := existing.DeepCopy()
	if updated.Labels == nil {
		updated.Labels = make(map[string]string, len(desired))
	}
	for k, v := range desired {
		updated.Labels[k] = v
	}
	models.RemoveConnectivityCheckLabels(updated.Labels)
	if stripManagedBy {
		delete(updated.Labels, LabelManagedBy)
	}
	// Do NOT touch updated.Data, updated.StringData, or updated.Annotations.
	if _, updateErr := m.client.CoreV1().Secrets(m.namespace).Update(ctx, updated, metav1.UpdateOptions{}); updateErr != nil {
		return false, true, fmt.Errorf("syncing labels on self-managed secret %q in namespace %q: %w", name, m.namespace, updateErr)
	}
	slog.Info("[argosecrets] self-managed cluster secret labels converged — connection data untouched",
		"cluster", name, "namespace", m.namespace, "ownership_label_stripped", stripManagedBy,
	)
	return true, true, nil
}

// IsAddonLabelKey reports whether a cluster-Secret label key is one of
// Sharko's own addon-enablement keys — the keys Sharko is allowed to
// ADD / UPDATE / DELETE when converging a Sharko-MANAGED cluster's addon
// labels to match git (SyncManagedClusterLabels).
//
// The boundary is structural, not a fixed prefix: addon-enablement keys are
// written as the BARE addon name (e.g. "datadog", "datadog-version") — see
// the bootstrap template's `<addon-name>: enabled`, the gitops mutator's
// `labels[addonName] = value`, and models.AddonLabelValue. EVERY system,
// ownership, and foreign label Sharko or ArgoCD stamps is DNS-qualified and
// therefore contains a "/":
//
//   - app.kubernetes.io/managed-by   (ownership)
//   - argocd.argoproj.io/secret-type (ArgoCD cluster-secret type)
//   - sharko.dev/connectivity-check  (derived, per-cluster)
//   - app.kubernetes.io/instance     (ArgoCD tracking)
//
// So "no slash" == "an unqualified addon-enablement key Sharko owns", and
// any key containing "/" is foreign/system and is PRESERVED verbatim by the
// managed-cluster self-heal. This is the exact scope git is the source of
// truth for; the connection credential material (Data/StringData) and every
// annotation are outside it and never touched. An empty key is never an
// addon key.
func IsAddonLabelKey(key string) bool {
	return key != "" && !strings.Contains(key, "/")
}

// ManagedLabelSyncResult reports the outcome of SyncManagedClusterLabels.
type ManagedLabelSyncResult struct {
	// Found is true when the named cluster Secret existed. false means no
	// write was attempted (the caller treats it as a skip, not an error —
	// same not-found contract as SyncLabelsOnly).
	Found bool
	// Changed is true when a label write actually occurred (the converged
	// label set differed from the live one).
	Changed bool
	// Adopted records whether the Secret carried an adopted annotation
	// (argosecrets.IsAdopted). Managed (non-adopted) Secrets get their
	// ownership + secret-type labels defensively re-applied; adopted Secrets
	// are guests and never have ownership stamped by self-heal.
	Adopted bool
	// Converged is the sorted set of Sharko addon-label keys present after
	// convergence (== the git-desired addon keys). Returned so the caller can
	// report honestly which keys are now in force.
	Converged []string
}

// SyncManagedClusterLabels converges the Sharko addon-enablement labels on an
// existing Sharko-OWNED ArgoCD cluster Secret so they EXACTLY match git —
// the write primitive for opt-in managed-cluster self-heal (V3 GF1). Unlike
// SyncLabelsOnly (the self-MANAGED / user-owned guest primitive, which merges
// and can hand ownership BACK to the user by stripping managed-by), this
// primitive is for Secrets Sharko owns and must KEEP owning:
//
//   - Full convergence over Sharko's own addon-label keys (IsAddonLabelKey —
//     the unqualified, no-"/" keys): ADD keys git declares, UPDATE changed
//     values, and DELETE addon keys git no longer declares. This is what
//     makes "drift corrected -> Synced" honest — a removed-in-git addon
//     label is actually removed, not left to flap forever.
//   - managed-by=sharko + argocd.argoproj.io/secret-type=cluster are
//     PRESERVED and, for a MANAGED (non-adopted) Secret, defensively
//     RE-APPLIED — so a Secret that previously LOST its ownership label (the
//     data-loss bug this story fixes) is repaired by the first heal and
//     survives listManagedSecrets on the next tick.
//   - An ADOPTED Secret (argosecrets.IsAdopted) is a guest of another owner:
//     Sharko converges ONLY its own addon-label keys and NEVER stamps
//     managed-by or secret-type. Whatever ownership/foreign labels the other
//     owner set are preserved (they carry a "/", so IsAddonLabelKey excludes
//     them from convergence automatically).
//   - Foreign labels (any key with a "/"), Data, StringData, and Annotations
//     are NEVER touched.
//   - Legacy "true"/"false" addon values are normalized to the canonical
//     "enabled"/"disabled" vocabulary before compare/write (same choke point
//     as SyncLabelsOnly / createOne), so an old-style value converges instead
//     of flip-flopping between the two writers.
//
// No churn: when the converged label set already equals the live one, no
// K8s write is issued (Changed=false). Not-found is NOT an error
// (Found=false) — the caller records a skip.
func (m *Manager) SyncManagedClusterLabels(ctx context.Context, name string, desiredAddonLabels map[string]string) (ManagedLabelSyncResult, error) {
	existing, getErr := m.client.CoreV1().Secrets(m.namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(getErr) {
		return ManagedLabelSyncResult{Found: false}, nil
	}
	if getErr != nil {
		return ManagedLabelSyncResult{}, fmt.Errorf("getting secret %q in namespace %q: %w", name, m.namespace, getErr)
	}

	adopted := IsAdopted(existing.Annotations)

	// Desired addon labels in the canonical vocabulary. Only keys that are
	// genuinely Sharko addon-label keys (unqualified) are honored — a
	// stray "/"-qualified key in the git labels block is ignored here so it
	// can never be written into the addon-key namespace.
	desired := make(map[string]string, len(desiredAddonLabels))
	for k, v := range desiredAddonLabels {
		if !IsAddonLabelKey(k) {
			continue
		}
		if normalized, changed := models.NormalizeAddonLabelValue(v); changed {
			v = normalized
		}
		desired[k] = v
	}

	// Build the converged label set on a DeepCopy. Start from the live labels
	// (so every foreign/system "/"-qualified label is preserved verbatim),
	// then converge ONLY the addon-key namespace.
	updated := existing.DeepCopy()
	if updated.Labels == nil {
		updated.Labels = make(map[string]string, len(desired)+2)
	}
	// DELETE Sharko addon keys git no longer declares (full convergence).
	for k := range updated.Labels {
		if IsAddonLabelKey(k) {
			if _, want := desired[k]; !want {
				delete(updated.Labels, k)
			}
		}
	}
	// ADD / UPDATE every git-desired addon key.
	for k, v := range desired {
		updated.Labels[k] = v
	}
	// Ownership + type: preserve for everyone (they carry "/", untouched by
	// the loops above); defensively RE-APPLY for managed (non-adopted)
	// Secrets so a Secret that lost its ownership label is repaired. Never
	// stamp ownership on an adopted guest Secret.
	if !adopted {
		updated.Labels[LabelManagedBy] = ManagedByValue
		updated.Labels[LabelSecretType] = "cluster"
	}

	// No-op when the converged set already matches — avoid needless churn.
	if labelsEqual(existing.Labels, updated.Labels) {
		slog.Debug("[argosecrets] managed cluster secret labels already converged, skipping",
			"cluster", name, "namespace", m.namespace,
		)
		return ManagedLabelSyncResult{Found: true, Changed: false, Adopted: adopted, Converged: sortedKeys(desired)}, nil
	}

	// Data / StringData / Annotations are deliberately NOT touched — DeepCopy
	// preserved them and the loops above only mutate updated.Labels.
	if _, updateErr := m.client.CoreV1().Secrets(m.namespace).Update(ctx, updated, metav1.UpdateOptions{}); updateErr != nil {
		return ManagedLabelSyncResult{Found: true}, fmt.Errorf("converging managed cluster labels on secret %q in namespace %q: %w", name, m.namespace, updateErr)
	}
	slog.Info("[argosecrets] managed cluster secret addon labels converged — ownership preserved, connection data untouched",
		"cluster", name, "namespace", m.namespace, "adopted", adopted,
	)
	return ManagedLabelSyncResult{Found: true, Changed: true, Adopted: adopted, Converged: sortedKeys(desired)}, nil
}

// labelsEqual reports whether two label maps have exactly the same keys and
// values. nil and empty are treated as equal.
func labelsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

// sortedKeys returns the map's keys in sorted order (deterministic reporting).
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// desiredLabelsSubset reports whether all key-value pairs in desired are
// present in current. Foreign keys in current (not in desired) are ignored —
// they are kept by the merge semantics of the adopted path.
func desiredLabelsSubset(current, desired map[string]string) bool {
	for k, want := range desired {
		if current[k] != want {
			return false
		}
	}
	return true
}

// mergeAnnotations merges new annotations into existing, overwriting on conflict.
func mergeAnnotations(existing, additions map[string]string) map[string]string {
	if existing == nil {
		existing = make(map[string]string, len(additions))
	}
	for k, v := range additions {
		existing[k] = v
	}
	return existing
}

// List returns the names of all ArgoCD cluster secrets managed by Sharko.
// The selector includes both the managed-by label and the ArgoCD secret-type label so that
// non-cluster secrets that happen to carry the managed-by label are excluded.
func (m *Manager) List(ctx context.Context) ([]string, error) {
	secrets, err := m.client.CoreV1().Secrets(m.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: LabelManagedBy + "=" + ManagedByValue + "," + LabelSecretType + "=cluster",
	})
	if err != nil {
		return nil, fmt.Errorf("listing managed secrets in namespace %q: %w", m.namespace, err)
	}

	names := make([]string, len(secrets.Items))
	for i, s := range secrets.Items {
		names[i] = s.Name
	}
	return names, nil
}

// ListSecrets returns the full corev1.Secret objects for all ArgoCD cluster
// secrets managed by Sharko. Use this instead of List when callers need to
// inspect annotations (e.g. the orphan sweep that must skip adopted secrets).
// Same label selector as List.
func (m *Manager) ListSecrets(ctx context.Context) ([]corev1.Secret, error) {
	list, err := m.client.CoreV1().Secrets(m.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: LabelManagedBy + "=" + ManagedByValue + "," + LabelSecretType + "=cluster",
	})
	if err != nil {
		return nil, fmt.Errorf("listing managed secrets in namespace %q: %w", m.namespace, err)
	}
	return list.Items, nil
}

// Delete removes the named ArgoCD cluster secret, but only if it is managed by Sharko.
// Returns nil if the secret does not exist (idempotent).
// Returns an error if the secret exists but is not managed by Sharko.
func (m *Manager) Delete(ctx context.Context, name string) error {
	existing, err := m.client.CoreV1().Secrets(m.namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil // idempotent — already gone
	}
	if err != nil {
		return fmt.Errorf("getting secret %q in namespace %q: %w", name, m.namespace, err)
	}

	// Safety: never delete a secret we don't manage.
	if existing.Labels[LabelManagedBy] != ManagedByValue {
		return fmt.Errorf("secret %q exists but is not managed by sharko (missing %s=%s label)",
			name, LabelManagedBy, ManagedByValue)
	}

	if err := m.client.CoreV1().Secrets(m.namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		return fmt.Errorf("deleting secret %q in namespace %q: %w", name, m.namespace, err)
	}

	slog.Info("[argosecrets] cluster secret deleted",
		"cluster", name, "namespace", m.namespace,
	)
	return nil
}

// SetAnnotation adds or updates a single annotation on the named secret.
// Returns an error if the secret does not exist.
func (m *Manager) SetAnnotation(ctx context.Context, name, key, value string) error {
	existing, err := m.client.CoreV1().Secrets(m.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("getting secret %q in namespace %q: %w", name, m.namespace, err)
	}

	updated := existing.DeepCopy()
	if updated.Annotations == nil {
		updated.Annotations = make(map[string]string)
	}
	updated.Annotations[key] = value
	if _, updateErr := m.client.CoreV1().Secrets(m.namespace).Update(ctx, updated, metav1.UpdateOptions{}); updateErr != nil {
		return fmt.Errorf("setting annotation %q on secret %q: %w", key, name, updateErr)
	}
	slog.Info("[argosecrets] annotation set on cluster secret",
		"cluster", name, "annotation", key, "value", value,
	)
	return nil
}

// GetAnnotation returns the value of a specific annotation on the named secret.
// Returns ("", nil) if the secret exists but the annotation is not set.
// Returns an error if the secret does not exist.
func (m *Manager) GetAnnotation(ctx context.Context, name, key string) (string, error) {
	existing, err := m.client.CoreV1().Secrets(m.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("getting secret %q in namespace %q: %w", name, m.namespace, err)
	}
	return existing.Annotations[key], nil
}

// GetManagedByLabel returns the managed-by label value for the named secret.
// Returns ("", nil) if the secret exists but the label is not set.
func (m *Manager) GetManagedByLabel(ctx context.Context, name string) (string, error) {
	existing, err := m.client.CoreV1().Secrets(m.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("getting secret %q in namespace %q: %w", name, m.namespace, err)
	}
	return existing.Labels[LabelManagedBy], nil
}

// StripOwnershipLabel removes Sharko's ownership label
// (app.kubernetes.io/managed-by: sharko) from the named cluster secret
// WITHOUT deleting the secret and without touching anything else — no
// annotations, no other labels, no connection data. It is the handover
// primitive for self-managed connections at removal time (V2-cleanup-60.1):
// once the cluster's git entry is gone, the reconciler tick that would
// normally strip the label on a mode switch never sees the entry again, so
// RemoveCluster strips eagerly instead. Without the strip, the orphan sweep
// would see a sharko-labeled secret with no git entry and delete the user's
// connection.
//
// Idempotent. Returns (stripped, error):
//   - (false, nil) — secret does not exist, or exists without the sharko
//     ownership label (nothing to hand over; a foreign managed-by value is
//     never touched).
//   - (true, nil) — the label was removed.
func (m *Manager) StripOwnershipLabel(ctx context.Context, name string) (bool, error) {
	existing, err := m.client.CoreV1().Secrets(m.namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("getting secret %q in namespace %q: %w", name, m.namespace, err)
	}
	if existing.Labels[LabelManagedBy] != ManagedByValue {
		return false, nil
	}

	updated := existing.DeepCopy()
	delete(updated.Labels, LabelManagedBy)
	if _, updateErr := m.client.CoreV1().Secrets(m.namespace).Update(ctx, updated, metav1.UpdateOptions{}); updateErr != nil {
		return false, fmt.Errorf("stripping ownership label from secret %q in namespace %q: %w", name, m.namespace, updateErr)
	}
	slog.Info("[argosecrets] sharko ownership label stripped from cluster secret — connection handed over to the user",
		"cluster", name, "namespace", m.namespace,
	)
	return true, nil
}

// Unadopt removes the managed-by label and adopted annotation from the named secret
// without deleting it. The secret remains in the argocd namespace so ArgoCD can still
// connect to the cluster.
func (m *Manager) Unadopt(ctx context.Context, name string) error {
	existing, err := m.client.CoreV1().Secrets(m.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("getting secret %q in namespace %q: %w", name, m.namespace, err)
	}

	updated := existing.DeepCopy()
	delete(updated.Labels, LabelManagedBy)
	delete(updated.Annotations, AnnotationAdopted)
	// A cluster adopted before either rename carries an older key spelling —
	// remove both, or the Secret would stay orphan-sweep-immune forever.
	delete(updated.Annotations, AnnotationAdoptedDoubledPrefixLegacy)
	delete(updated.Annotations, AnnotationAdoptedLegacy)
	if _, updateErr := m.client.CoreV1().Secrets(m.namespace).Update(ctx, updated, metav1.UpdateOptions{}); updateErr != nil {
		return fmt.Errorf("unadopting secret %q in namespace %q: %w", name, m.namespace, updateErr)
	}
	slog.Info("[argosecrets] cluster secret unadopted — managed-by label and adopted annotation removed",
		"cluster", name, "namespace", m.namespace,
	)
	return nil
}
