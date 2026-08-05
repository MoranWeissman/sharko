package orchestrator

import (
	"context"
	"errors"
	"fmt"

	"github.com/MoranWeissman/sharko/internal/logging"
	"github.com/MoranWeissman/sharko/internal/remoteclient"
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
// Returns the list of created secret names and an error if the remote client fails.
// Individual secret failures are recorded in the result but do not cause a top-level error.
func (o *Orchestrator) CreateAddonSecretsForCluster(ctx context.Context, kubeconfig []byte, addons map[string]bool) ([]string, []SecretError, error) {
	res, err := o.createAddonSecrets(ctx, kubeconfig, addons)
	if err != nil {
		return nil, nil, err
	}
	return res.Created, res.Failed, nil
}

// secretCreationResult holds the outcome of a partial-success-aware secret creation loop.
type secretCreationResult struct {
	Created []string
	Failed  []SecretError
}

// createAddonSecrets creates K8s Secrets on a remote cluster for all addons that have secret definitions.
// Uses partial-success semantics: individual failures are recorded but do not stop the loop.
// Returns a secretCreationResult with both created and failed secret names.
func (o *Orchestrator) createAddonSecrets(ctx context.Context, kubeconfig []byte, addons map[string]bool) (*secretCreationResult, error) {
	log := logging.LoggerFromContext(ctx)
	if o.remoteClientFn == nil || o.secretDefs == nil || o.secretFetcher == nil {
		return &secretCreationResult{}, nil // no secret management configured
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
		return &secretCreationResult{}, nil
	}

	log.Info("[secrets] createAddonSecrets called", "addonCount", len(toCreate))

	client, err := o.remoteClientFn(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("connecting to remote cluster: %w", err)
	}

	result := &secretCreationResult{}
	for _, def := range toCreate {
		data := make(map[string][]byte)
		var fetchFailed bool
		for key, providerPath := range def.Keys {
			log.Info("[secrets] fetching secret value", "addon", def.AddonName, "key", key, "path", providerPath)
			val, fetchErr := o.secretFetcher.GetSecretValue(ctx, providerPath)
			if fetchErr != nil {
				result.Failed = append(result.Failed, SecretError{
					Name:  def.SecretName,
					Error: fmt.Sprintf("fetching key %q from %q: %v", key, providerPath, fetchErr),
				})
				fetchFailed = true
				break
			}
			data[key] = val
		}
		if fetchFailed {
			continue
		}

		log.Info("[secrets] pushing secret to cluster", "addon", def.AddonName, "secret", def.SecretName, "namespace", def.Namespace)
		if err := remoteclient.EnsureSecret(ctx, client, def.Namespace, def.SecretName, data); err != nil {
			log.Error("[secrets] failed to create secret, continuing", "addon", def.AddonName, "error", err)
			result.Failed = append(result.Failed, SecretError{
				Name:  def.SecretName,
				Error: fmt.Sprintf("creating secret for addon %s: %v", def.AddonName, err),
			})
			continue
		}
		result.Created = append(result.Created, def.SecretName)
	}
	return result, nil
}

// listSecretsToCreate returns the secret names that would be created for the given addons,
// without actually creating them. Used by dry-run mode.
func (o *Orchestrator) listSecretsToCreate(addons map[string]bool) []string {
	if o.secretDefs == nil {
		return nil
	}
	var names []string
	for addonName, enabled := range addons {
		if !enabled {
			continue
		}
		if def, ok := o.secretDefs[addonName]; ok {
			names = append(names, def.SecretName)
		}
	}
	return names
}

// deleteAddonSecrets deletes Sharko-managed secrets for specific addons from a remote cluster.
//
// Ownership gate (P1-A): every delete goes through
// remoteclient.DeleteSecretIfManaged, so a secret with the right name that
// Sharko did not create is left exactly where it is. clusterName is carried
// only so the log line names the cluster the secret sits on — never a value,
// never a key.
func (o *Orchestrator) deleteAddonSecrets(ctx context.Context, clusterName string, kubeconfig []byte, addons map[string]bool) ([]string, error) {
	log := logging.LoggerFromContext(ctx)
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
		removed, delErr := remoteclient.DeleteSecretIfManaged(ctx, client, def.Namespace, def.SecretName)
		if delErr != nil {
			if errors.Is(delErr, remoteclient.ErrForeignSecret) {
				log.Info("leaving this secret alone — Sharko did not create it",
					"cluster", clusterName, "addon", addonName,
					"namespace", def.Namespace, "secret", def.SecretName)
			} else {
				log.Warn("failed to delete secret",
					"cluster", clusterName, "addon", addonName, "secret", def.SecretName, "error", delErr)
			}
			continue
		}
		if !removed {
			log.Info("secret already gone", "cluster", clusterName, "addon", addonName, "secret", def.SecretName)
			continue
		}
		deleted = append(deleted, def.SecretName)
	}
	return deleted, nil
}

// deleteAllAddonSecrets deletes all known addon secrets from a remote cluster (used during deregister).
// Best-effort: continues on individual delete failures, logs errors but doesn't abort.
//
// Same ownership gate as deleteAddonSecrets above — a secret Sharko did not
// create survives a deregister untouched.
func (o *Orchestrator) deleteAllAddonSecrets(ctx context.Context, clusterName string, kubeconfig []byte) ([]string, error) {
	log := logging.LoggerFromContext(ctx)
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
		removed, delErr := remoteclient.DeleteSecretIfManaged(ctx, client, def.Namespace, def.SecretName)
		if delErr != nil {
			if errors.Is(delErr, remoteclient.ErrForeignSecret) {
				log.Info("leaving this secret alone — Sharko did not create it",
					"cluster", clusterName, "addon", def.AddonName,
					"namespace", def.Namespace, "secret", def.SecretName)
			} else {
				log.Warn("failed to delete secret",
					"cluster", clusterName, "addon", def.AddonName, "secret", def.SecretName, "error", delErr)
			}
			continue
		}
		if !removed {
			log.Info("secret already gone", "cluster", clusterName, "addon", def.AddonName, "secret", def.SecretName)
			continue
		}
		deleted = append(deleted, def.SecretName)
	}
	return deleted, nil
}
