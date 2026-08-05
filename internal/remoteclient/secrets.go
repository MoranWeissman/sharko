package remoteclient

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const managedByLabel = "app.kubernetes.io/managed-by"
const managedByValue = "sharko"

// ErrForeignSecret means a Secret with the wanted name already exists on the
// remote cluster and does NOT carry app.kubernetes.io/managed-by=sharko —
// somebody else created it (a person, External Secrets Operator, Sealed
// Secrets, a vault agent, a Helm chart). Sharko refuses to change or remove
// it.
//
// This package is the SINGLE CHOKE POINT for that rule on the addon-values
// side: every write and every delete Sharko performs on a remote cluster's
// addon secret goes through EnsureSecret or DeleteSecretIfManaged below, so
// putting the ownership question here means no caller can forget to ask it.
// The same rule already guards the cluster-connection side in
// internal/clusterreconciler (IsManagedBySharko + the orphan-delete gate);
// this is that rule finally applied to the other engine too.
//
// Creating a Secret that does not exist yet is still allowed: a new Secret is
// labeled at birth, and that label is exactly how ownership is established.
var ErrForeignSecret = errors.New("this secret already exists on the cluster and Sharko did not create it, so Sharko will not change or remove it")

// ManagedSecretInfo describes a Sharko-managed secret on a remote cluster.
type ManagedSecretInfo struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

// IsManagedBySharko reports whether a Secret carries Sharko's ownership
// label. Nil-safe (a nil Secret is not Sharko's). Mirrors
// clusterreconciler.IsManagedBySharko — same question, other engine.
func IsManagedBySharko(secret *corev1.Secret) bool {
	if secret == nil {
		return false
	}
	return secret.Labels[managedByLabel] == managedByValue
}

// defaultSourceLabel is the honest, generic fallback used when a caller
// does not know the real store product name — never a guessed product,
// never blank.
const defaultSourceLabel = "secrets store"

// ValuesProvenanceAnnotations builds the sharko.dev/* provenance
// annotations stamped on every addon-values Secret Sharko creates or
// updates (P2-C5): which addon it belongs to, what store its content is
// compared against, and when this write happened.
//
// HARD RULE, enforced by construction: this function only ever takes an
// addon name, a source label, and a timestamp — none of which can carry a
// secret value, a hash of one, or a store path that embeds a credential.
// Per-key store paths (the addon's AddonSecretRef.Keys map) never reach
// this function — those stay in the operator-gated live-read response only
// (internal/api/secret_resource.go), never in an annotation any
// authenticated user's `kubectl describe` could read.
func ValuesProvenanceAnnotations(addon, source string, at time.Time) map[string]string {
	if source == "" {
		source = defaultSourceLabel
	}
	return map[string]string{
		"sharko.dev/addon":      addon,
		"sharko.dev/source":     source,
		"sharko.dev/written-at": at.UTC().Format(time.RFC3339),
	}
}

// EnsureSecret creates a K8s Secret on the remote cluster when none exists,
// or updates one Sharko already owns. The secret is labeled with
// app.kubernetes.io/managed-by=sharko.
//
// It REFUSES, with ErrForeignSecret, to update a Secret that exists without
// that label — see ErrForeignSecret. Before this gate existed, an addon
// secret owned by someone else was silently overwritten every five minutes
// and re-labeled as Sharko's, which took ownership of a resource Sharko had
// never created.
//
// annotations (P2-C5) are the caller's provenance facts (see
// ValuesProvenanceAnnotations) — merged onto the Secret's existing
// annotations on update (never replacing them wholesale), set directly on
// create. nil/empty leaves annotation handling byte-identical to before
// this parameter existed.
func EnsureSecret(ctx context.Context, client kubernetes.Interface, namespace, name string, data map[string][]byte, annotations map[string]string) error {
	slog.Info("[remoteclient] EnsureSecret called", "namespace", namespace, "name", name, "keys", len(data))
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				managedByLabel: managedByValue,
			},
			Annotations: annotations,
		},
		Data: data,
		Type: corev1.SecretTypeOpaque,
	}

	existing, err := client.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, createErr := client.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
		if createErr != nil {
			return fmt.Errorf("creating secret %s/%s: %w", namespace, name, createErr)
		}
		slog.Info("[remoteclient] secret created", "namespace", namespace, "name", name)
		return nil
	}
	if err != nil {
		return fmt.Errorf("checking secret %s/%s: %w", namespace, name, err)
	}

	// Ownership gate — the whole point of this function existing as one
	// choke point. No write of any kind happens below this line for a Secret
	// Sharko did not create.
	if !IsManagedBySharko(existing) {
		slog.Info("[remoteclient] leaving this secret alone — it exists on the cluster and Sharko did not create it",
			"namespace", namespace, "name", name)
		return fmt.Errorf("secret %s/%s: %w", namespace, name, ErrForeignSecret)
	}

	// Update existing secret.
	existing.Data = data
	if existing.Labels == nil {
		existing.Labels = make(map[string]string)
	}
	existing.Labels[managedByLabel] = managedByValue
	if len(annotations) > 0 {
		if existing.Annotations == nil {
			existing.Annotations = make(map[string]string, len(annotations))
		}
		for k, v := range annotations {
			existing.Annotations[k] = v
		}
	}
	_, err = client.CoreV1().Secrets(namespace).Update(ctx, existing, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("updating secret %s/%s: %w", namespace, name, err)
	}
	slog.Info("[remoteclient] secret updated", "namespace", namespace, "name", name)
	return nil
}

// DeleteSecretIfManaged deletes one named Secret from a remote cluster ONLY
// when it carries Sharko's ownership label. The delete side of the same rule
// EnsureSecret enforces for writes, and the more dangerous direction: an
// unguarded delete of somebody else's secret cannot be undone by the next
// reconcile pass.
//
// Returns deleted=false with a nil error in the two ordinary cases — the
// Secret is already gone, or it belongs to somebody else — so a best-effort
// cleanup loop can carry straight on to the next addon. Only a real API
// failure comes back as an error.
func DeleteSecretIfManaged(ctx context.Context, client kubernetes.Interface, namespace, name string) (deleted bool, err error) {
	existing, getErr := client.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if getErr != nil {
		if apierrors.IsNotFound(getErr) {
			return false, nil
		}
		return false, fmt.Errorf("checking secret %s/%s before deleting it: %w", namespace, name, getErr)
	}
	if !IsManagedBySharko(existing) {
		return false, fmt.Errorf("secret %s/%s: %w", namespace, name, ErrForeignSecret)
	}
	if delErr := client.CoreV1().Secrets(namespace).Delete(ctx, name, metav1.DeleteOptions{}); delErr != nil {
		if apierrors.IsNotFound(delErr) {
			return false, nil
		}
		return false, fmt.Errorf("deleting secret %s/%s: %w", namespace, name, delErr)
	}
	return true, nil
}

// DeleteManagedSecrets deletes all Sharko-managed secrets in a namespace.
// Returns the names of deleted secrets.
func DeleteManagedSecrets(ctx context.Context, client kubernetes.Interface, namespace string) ([]string, error) {
	secrets, err := client.CoreV1().Secrets(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: managedByLabel + "=" + managedByValue,
	})
	if err != nil {
		return nil, fmt.Errorf("listing managed secrets in %s: %w", namespace, err)
	}

	var deleted []string
	for _, s := range secrets.Items {
		if err := client.CoreV1().Secrets(namespace).Delete(ctx, s.Name, metav1.DeleteOptions{}); err != nil {
			return deleted, fmt.Errorf("deleting secret %s/%s: %w", namespace, s.Name, err)
		}
		deleted = append(deleted, s.Name)
	}
	return deleted, nil
}

// ListManagedSecrets lists all Sharko-managed secrets across all namespaces (or a specific one).
func ListManagedSecrets(ctx context.Context, client kubernetes.Interface, namespace string) ([]ManagedSecretInfo, error) {
	slog.Debug("[remoteclient] ListManagedSecrets", "namespace", namespace)
	opts := metav1.ListOptions{
		LabelSelector: managedByLabel + "=" + managedByValue,
	}

	secrets, err := client.CoreV1().Secrets(namespace).List(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("listing managed secrets: %w", err)
	}

	result := make([]ManagedSecretInfo, 0, len(secrets.Items))
	for _, s := range secrets.Items {
		result = append(result, ManagedSecretInfo{
			Name:      s.Name,
			Namespace: s.Namespace,
		})
	}
	return result, nil
}
