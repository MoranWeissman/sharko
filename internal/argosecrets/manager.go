// Package argosecrets manages ArgoCD cluster secrets in the argocd namespace.
// ArgoCD discovers clusters via K8s Secrets labelled with
// argocd.argoproj.io/secret-type: cluster. This package creates and updates
// those secrets so that ArgoCD's ApplicationSet cluster generator can discover
// Sharko-managed clusters without storing static credentials.
package argosecrets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
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
	AnnotationAdopted = "sharko.sharko.io/adopted"
)

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

type execProvider struct {
	Command    string   `json:"command"`
	Args       []string `json:"args"`
	APIVersion string   `json:"apiVersion"`
}

type tlsConfig struct {
	Insecure bool   `json:"insecure"`
	CAData   string `json:"caData,omitempty"`
}

// buildSecretConfig constructs the ArgoCD execProviderConfig JSON string.
// The --role-arn arg is only included when spec.RoleARN is non-empty.
func buildSecretConfig(spec ClusterSecretSpec) (string, error) {
	args := []string{"aws", "--cluster-name", spec.Name, "--region", spec.Region}
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
		// Explicitly adopt it by applying the managed-by label, desired labels, and credentials.
		// Always write — do not compare hashes. Adoption is itself a meaningful state change.
		adopted := existing.DeepCopy()
		adopted.Labels = desiredLabels
		if spec.Annotations != nil {
			adopted.Annotations = mergeAnnotations(adopted.Annotations, spec.Annotations)
		}
		adopted.Data = nil
		adopted.StringData = desiredStringData
		if _, adoptErr := m.client.CoreV1().Secrets(m.namespace).Update(ctx, adopted, metav1.UpdateOptions{}); adoptErr != nil {
			return false, fmt.Errorf("adopting secret %q in namespace %q: %w", spec.Name, m.namespace, adoptErr)
		}
		slog.Info("[argosecrets] cluster secret adopted",
			"cluster", spec.Name, "namespace", m.namespace,
		)
		return true, nil
	}

	// Secret is already managed by Sharko — compare hashes to decide whether an update is needed.
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
	if _, updateErr := m.client.CoreV1().Secrets(m.namespace).Update(ctx, updated, metav1.UpdateOptions{}); updateErr != nil {
		return fmt.Errorf("unadopting secret %q in namespace %q: %w", name, m.namespace, updateErr)
	}
	slog.Info("[argosecrets] cluster secret unadopted — managed-by label and adopted annotation removed",
		"cluster", name, "namespace", m.namespace,
	)
	return nil
}
