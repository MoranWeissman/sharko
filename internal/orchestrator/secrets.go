package orchestrator

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/MoranWeissman/sharko/internal/remoteclient"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// AddonSecretDefinition maps an addon to the K8s Secret it needs on remote clusters.
type AddonSecretDefinition struct {
	AddonName  string            `json:"addon_name"`
	SecretName string            `json:"secret_name"`
	Namespace  string            `json:"namespace"`
	Keys       map[string]string `json:"keys"` // secret data key → provider path (e.g. "api-key" → "secrets/datadog/api-key")
}

// SecretValueFetcher abstracts fetching raw secret values from the secrets provider.
// The provider path (e.g. "secrets/datadog/api-key") maps to a secret in AWS SM or K8s Secrets.
type SecretValueFetcher interface {
	GetSecretValue(ctx context.Context, path string) ([]byte, error)
}

// RemoteClientFactory builds a kubernetes.Interface from raw kubeconfig bytes.
// Abstracted for testing — production uses remoteclient.NewClientFromKubeconfig.
type RemoteClientFactory func(kubeconfig []byte) (kubernetes.Interface, error)

// SetSecretManagement configures remote cluster secret operations.
// Called after New() when the server has addon secret definitions configured.
func (o *Orchestrator) SetSecretManagement(defs map[string]AddonSecretDefinition, fetcher SecretValueFetcher, clientFn RemoteClientFactory) {
	o.secretDefs = defs
	o.secretFetcher = fetcher
	o.remoteClientFn = clientFn
}

// CreateAddonSecretsForCluster is a public wrapper for the refresh API endpoint.
func (o *Orchestrator) CreateAddonSecretsForCluster(ctx context.Context, kubeconfig []byte, addons map[string]bool) ([]string, error) {
	return o.createAddonSecrets(ctx, kubeconfig, addons)
}

// createAddonSecrets creates K8s Secrets on a remote cluster for all addons that have secret definitions.
// Returns the list of created secret names. If any fail, returns partial results and the error.
func (o *Orchestrator) createAddonSecrets(ctx context.Context, kubeconfig []byte, addons map[string]bool) ([]string, error) {
	if o.remoteClientFn == nil || o.secretDefs == nil || o.secretFetcher == nil {
		return nil, nil // no secret management configured
	}

	// Filter to addons that are enabled AND have secret definitions.
	var toCreate []AddonSecretDefinition
	for addonName, enabled := range addons {
		if !enabled {
			continue
		}
		if def, ok := o.secretDefs[addonName]; ok {
			toCreate = append(toCreate, def)
		}
	}
	if len(toCreate) == 0 {
		return nil, nil
	}

	client, err := o.remoteClientFn(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("connecting to remote cluster: %w", err)
	}

	var created []string
	for _, def := range toCreate {
		data := make(map[string][]byte)
		for key, providerPath := range def.Keys {
			val, fetchErr := o.secretFetcher.GetSecretValue(ctx, providerPath)
			if fetchErr != nil {
				return created, fmt.Errorf("fetching secret value for %s key %q from %q: %w", def.AddonName, key, providerPath, fetchErr)
			}
			data[key] = val
		}

		if err := remoteclient.EnsureSecret(ctx, client, def.Namespace, def.SecretName, data); err != nil {
			return created, fmt.Errorf("creating secret for addon %s: %w", def.AddonName, err)
		}
		created = append(created, def.SecretName)
	}
	return created, nil
}

// deleteAddonSecrets deletes Sharko-managed secrets for specific addons from a remote cluster.
func (o *Orchestrator) deleteAddonSecrets(ctx context.Context, kubeconfig []byte, addons map[string]bool) ([]string, error) {
	if o.remoteClientFn == nil || o.secretDefs == nil {
		return nil, nil
	}

	client, err := o.remoteClientFn(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("connecting to remote cluster: %w", err)
	}

	var deleted []string
	for addonName, enabled := range addons {
		if enabled {
			continue // only delete secrets for disabled addons
		}
		def, ok := o.secretDefs[addonName]
		if !ok {
			continue
		}
		// Delete only the specific secret for this addon, not all managed secrets in the namespace.
		err = client.CoreV1().Secrets(def.Namespace).Delete(ctx, def.SecretName, metav1.DeleteOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				slog.Info("secret already gone", "addon", addonName, "secret", def.SecretName)
			} else {
				slog.Warn("failed to delete secret", "addon", addonName, "secret", def.SecretName, "error", err)
			}
			continue
		}
		deleted = append(deleted, def.SecretName)
	}
	return deleted, nil
}

// deleteAllAddonSecrets deletes all known addon secrets from a remote cluster (used during deregister).
// Best-effort: continues on individual delete failures, logs errors but doesn't abort.
func (o *Orchestrator) deleteAllAddonSecrets(ctx context.Context, kubeconfig []byte) ([]string, error) {
	if o.remoteClientFn == nil || o.secretDefs == nil {
		return nil, nil
	}

	client, err := o.remoteClientFn(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("connecting to remote cluster: %w", err)
	}

	var deleted []string
	for _, def := range o.secretDefs {
		// Delete by specific secret name, not namespace sweep, to avoid cross-addon deletion.
		err = client.CoreV1().Secrets(def.Namespace).Delete(ctx, def.SecretName, metav1.DeleteOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				slog.Info("secret already gone", "addon", def.AddonName, "secret", def.SecretName)
			} else {
				slog.Warn("failed to delete secret", "addon", def.AddonName, "secret", def.SecretName, "error", err)
			}
			continue
		}
		deleted = append(deleted, def.SecretName)
	}
	return deleted, nil
}
