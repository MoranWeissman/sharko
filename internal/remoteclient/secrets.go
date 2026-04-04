package remoteclient

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const managedByLabel = "app.kubernetes.io/managed-by"
const managedByValue = "sharko"

// ManagedSecretInfo describes a Sharko-managed secret on a remote cluster.
type ManagedSecretInfo struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

// EnsureSecret creates or updates a K8s Secret on the remote cluster.
// The secret is labeled with app.kubernetes.io/managed-by=sharko.
func EnsureSecret(ctx context.Context, client kubernetes.Interface, namespace, name string, data map[string][]byte) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				managedByLabel: managedByValue,
			},
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
		return nil
	}
	if err != nil {
		return fmt.Errorf("checking secret %s/%s: %w", namespace, name, err)
	}

	// Update existing secret.
	existing.Data = data
	if existing.Labels == nil {
		existing.Labels = make(map[string]string)
	}
	existing.Labels[managedByLabel] = managedByValue
	_, err = client.CoreV1().Secrets(namespace).Update(ctx, existing, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("updating secret %s/%s: %w", namespace, name, err)
	}
	return nil
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
