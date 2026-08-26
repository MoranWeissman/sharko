package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/MoranWeissman/sharko/internal/credsafe"
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
				// PUBLIC BOUNDARY. This used to be
				// fmt.Sprintf("fetching key %q from %q: %v", key, providerPath, fetchErr)
				// — the secrets backend's own words plus the location of the
				// secret inside it, on a field that rides out as
				// `failed_secrets` in the API response and gets printed by
				// `sharko cluster register`.
				//
				// The catalog sentence says which step failed and names the
				// addon and key, both of which come from Sharko's catalog in
				// Git. The backend's words and the path go to the log line
				// below and stop there.
				//
				// The log line carries NO error text at all, and that is
				// positional classification rather than type-based, for the
				// reason internal/api/connection_repair.go:989 writes down
				// after this exact mechanism leaked once: everything reaching
				// this branch is a secrets-backend failure BY CONSTRUCTION,
				// because the only call above it is GetSecretValue. Asking
				// credsafe.Is instead would come back false for any backend
				// that does not mark its own errors — a stub, an
				// unimplemented backend, a future one — and put its raw text
				// straight into the log. Where we are is stronger evidence
				// than what the error turned out to be, and it cannot be
				// wrong.
				//
				// The step, addon, key and path are enough to find the
				// matching entry in the secrets backend's own log, which is
				// where the backend's words belong.
				log.Error("[secrets] fetching secret value failed",
					"step", "fetch-secret-value",
					"addon", def.AddonName, "key", key, "path", providerPath)
				result.Failed = append(result.Failed, newSecretFetchFailure(def, key))
				fetchFailed = true
				break
			}
			data[key] = val
		}
		if fetchFailed {
			continue
		}

		log.Info("[secrets] pushing secret to cluster", "addon", def.AddonName, "secret", def.SecretName, "namespace", def.Namespace)
		// Provenance (P2-C5): this registration-time path doesn't know the
		// configured provider's real backend name (that lives in
		// internal/api's typed provider config, out of scope for the
		// orchestrator) — the generic "secrets store" label
		// remoteclient.ValuesProvenanceAnnotations falls back to is still
		// honest, and the periodic internal/secrets.Reconciler pass (which
		// DOES know the real name) refreshes this same annotation on its
		// very next 5-minute tick.
		provenance := remoteclient.ValuesProvenanceAnnotations(def.AddonName, "", time.Now())
		if err := remoteclient.EnsureSecret(ctx, client, def.Namespace, def.SecretName, data, provenance); err != nil {
			// PUBLIC BOUNDARY, same as the fetch branch above. The Kubernetes
			// API server's own text — which can name the ServiceAccount out
			// of a 403, the API server host, or whatever an admission webhook
			// felt like saying — stays in this log line.
			log.Error("[secrets] failed to create secret, continuing",
				"addon", def.AddonName, "namespace", def.Namespace, "secret", def.SecretName,
				"error", credsafe.Sentence(err))
			result.Failed = append(result.Failed, newSecretWriteFailure(def))
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
