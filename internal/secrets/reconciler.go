package secrets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
	"github.com/MoranWeissman/sharko/internal/remoteclient"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// GitReader abstracts the read-only Git operations needed by the reconciler.
type GitReader interface {
	GetFileContent(ctx context.Context, path, ref string) ([]byte, error)
}

// RemoteClientFactory builds a kubernetes.Interface from raw kubeconfig bytes.
// Abstracted for testing — production uses remoteclient.NewClientFromKubeconfig.
type RemoteClientFactory func(kubeconfig []byte) (kubernetes.Interface, error)

// Reconciler periodically fetches secret definitions from the Git catalog and
// ensures the corresponding K8s Secrets exist and are up-to-date on every
// remote cluster that has the owning addon enabled.
//
// It supports three triggers:
//  1. Periodic timer (default 5 min)
//  2. Explicit Trigger() call (e.g. after a webhook push)
//  3. Initial run on Start()
type Reconciler struct {
	credProvider   providers.ClusterCredentialsProvider
	secretProvider providers.SecretProvider
	gitReader      func() GitReader // lazy — resolved from active connection
	remoteClientFn RemoteClientFactory
	parser         *config.Parser
	baseBranch     string
	interval       time.Duration
	triggerCh      chan struct{}
	stopCh         chan struct{}
	stopOnce       sync.Once

	// Last reconcile stats (protected by mu)
	mu         sync.RWMutex
	lastRun    time.Time
	lastStats  ReconcileStats
	lastErrors []string
}

// ReconcileStats holds counters and timing from the most recent reconcile cycle.
type ReconcileStats struct {
	Checked  int       `json:"checked"`
	Created  int       `json:"created"`
	Updated  int       `json:"updated"`
	Deleted  int       `json:"deleted"`
	Skipped  int       `json:"skipped"`
	Errors   int       `json:"errors"`
	Duration string    `json:"duration"`
	LastRun  time.Time `json:"last_run"`
}

// NewReconciler creates a Reconciler. gitReaderFn is a lazy accessor that
// returns the currently-active GitReader, or nil when no connection is live.
// interval <= 0 defaults to 5 minutes.
func NewReconciler(
	credProvider providers.ClusterCredentialsProvider,
	secretProvider providers.SecretProvider,
	gitReaderFn func() GitReader,
	remoteClientFn RemoteClientFactory,
	parser *config.Parser,
	baseBranch string,
	interval time.Duration,
) *Reconciler {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	return &Reconciler{
		credProvider:   credProvider,
		secretProvider: secretProvider,
		gitReader:      gitReaderFn,
		remoteClientFn: remoteClientFn,
		parser:         parser,
		baseBranch:     baseBranch,
		interval:       interval,
		triggerCh:      make(chan struct{}, 1),
		stopCh:         make(chan struct{}),
	}
}

// Start launches the background reconcile loop. It runs one reconcile
// immediately, then repeats on every tick or Trigger() call.
func (r *Reconciler) Start() {
	go func() {
		r.reconcile()
		ticker := time.NewTicker(r.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				r.reconcile()
			case <-r.triggerCh:
				r.reconcile()
			case <-r.stopCh:
				return
			}
		}
	}()
}

// Stop shuts down the reconcile loop. Safe to call multiple times.
func (r *Reconciler) Stop() {
	r.stopOnce.Do(func() {
		close(r.stopCh)
	})
}

// Trigger requests an immediate reconcile. Non-blocking: if a trigger is
// already queued the request is dropped (the pending run covers it).
func (r *Reconciler) Trigger() {
	select {
	case r.triggerCh <- struct{}{}:
	default: // already triggered
	}
}

// GetStats returns a snapshot of the last reconcile run's statistics.
// It returns interface{} to satisfy the api.SecretReconciler interface without
// creating an import cycle; callers within this package should type-assert to ReconcileStats.
func (r *Reconciler) GetStats() interface{} {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastStats
}

// GetErrors returns the error messages from the last reconcile run.
func (r *Reconciler) GetErrors() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, len(r.lastErrors))
	copy(out, r.lastErrors)
	return out
}

// reconcile is the main reconcile cycle. It is safe to call concurrently but
// will run sequentially via the single-goroutine loop in Start().
func (r *Reconciler) reconcile() {
	start := time.Now()
	stats := ReconcileStats{}
	var errors []string

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	slog.Info("[secrets] reconcile started")

	// 1. Get Git reader — bail early when no connection is configured.
	gr := r.gitReader()
	if gr == nil {
		slog.Warn("[secrets] no Git connection — skipping reconcile")
		return
	}

	// 2. Read catalog.
	catalogData, err := gr.GetFileContent(ctx, "configuration/addons-catalog.yaml", r.baseBranch)
	if err != nil {
		slog.Warn("[secrets] failed to read catalog", "error", err)
		return
	}
	catalog, err := r.parser.ParseAddonsCatalog(catalogData)
	if err != nil {
		slog.Warn("[secrets] failed to parse catalog", "error", err)
		return
	}

	// 3. Build addon→secrets map (only addons that declare secrets).
	type addonSecrets struct {
		addon   models.AddonCatalogEntry
		secrets []models.AddonSecretRef
	}
	secretAddons := make(map[string]addonSecrets)
	for _, entry := range catalog {
		if len(entry.Secrets) > 0 {
			secretAddons[entry.Name] = addonSecrets{addon: entry, secrets: entry.Secrets}
		}
	}
	if len(secretAddons) == 0 {
		slog.Info("[secrets] no addons with secret definitions — nothing to reconcile")
		return
	}

	// 4. Read cluster-addons.yaml.
	clusterData, err := gr.GetFileContent(ctx, "configuration/cluster-addons.yaml", r.baseBranch)
	if err != nil {
		slog.Warn("[secrets] failed to read cluster-addons", "error", err)
		return
	}
	clusters, err := r.parser.ParseClusterAddons(clusterData)
	if err != nil {
		slog.Warn("[secrets] failed to parse cluster-addons", "error", err)
		return
	}

	// 5. For each cluster reconcile every secret defined for its enabled addons.
	for _, cluster := range clusters {
		enabledAddons := r.parser.GetEnabledAddons(cluster, catalog)
		for _, enabledAddon := range enabledAddons {
			as, ok := secretAddons[enabledAddon.AddonName]
			if !ok {
				continue
			}
			for _, secretRef := range as.secrets {
				stats.Checked++
				// Use secretPath for credential lookup when explicitly set on the cluster.
				credLookup := cluster.Name
				if cluster.SecretPath != "" {
					credLookup = cluster.SecretPath
				}
				if err := r.reconcileSecret(ctx, &stats, credLookup, as.addon.Name, secretRef); err != nil {
					stats.Errors++
					errMsg := fmt.Sprintf("cluster=%s addon=%s secret=%s: %v",
						cluster.Name, as.addon.Name, secretRef.SecretName, err)
					errors = append(errors, errMsg)
					slog.Error("[secrets] reconcile failed",
						"cluster", cluster.Name,
						"addon", as.addon.Name,
						"secret", secretRef.SecretName,
						"error", err,
					)
				}
			}
		}
	}

	stats.Duration = time.Since(start).String()
	stats.LastRun = time.Now()

	r.mu.Lock()
	r.lastRun = time.Now()
	r.lastStats = stats
	r.lastErrors = errors
	r.mu.Unlock()

	slog.Info("[secrets] reconcile complete",
		"checked", stats.Checked,
		"created", stats.Created,
		"updated", stats.Updated,
		"deleted", stats.Deleted,
		"skipped", stats.Skipped,
		"errors", stats.Errors,
		"duration", stats.Duration,
	)
}

// reconcileSecret ensures a single K8s Secret exists and is current on the
// named remote cluster. It increments Created, Updated, or Skipped on stats.
func (r *Reconciler) reconcileSecret(
	ctx context.Context,
	stats *ReconcileStats,
	clusterName, addonName string,
	ref models.AddonSecretRef,
) error {
	// Get kubeconfig for the cluster.
	creds, err := r.credProvider.GetCredentials(clusterName)
	if err != nil {
		return fmt.Errorf("getting credentials: %w", err)
	}

	// Build a K8s client for the remote cluster.
	client, err := r.remoteClientFn(creds.Raw)
	if err != nil {
		return fmt.Errorf("connecting to cluster: %w", err)
	}

	// Fetch desired values from the secrets provider and compute a hash.
	desiredData := make(map[string][]byte)
	for key, providerPath := range ref.Keys {
		val, err := r.secretProvider.GetSecretValue(ctx, providerPath)
		if err != nil {
			return fmt.Errorf("fetching %q from provider: %w", providerPath, err)
		}
		desiredData[key] = val
	}
	desiredHash := hashSecretData(desiredData)

	// Check whether the secret already exists on the remote cluster.
	existing, err := client.CoreV1().Secrets(ref.Namespace).Get(ctx, ref.SecretName, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("checking existing secret: %w", err)
		}
		// Secret doesn't exist — create it.
		slog.Info("[secrets] creating secret",
			"cluster", clusterName, "addon", addonName,
			"secret", ref.SecretName, "namespace", ref.Namespace,
		)
		if createErr := remoteclient.EnsureSecret(ctx, client, ref.Namespace, ref.SecretName, desiredData); createErr != nil {
			return fmt.Errorf("creating secret: %w", createErr)
		}
		stats.Created++
		slog.Info("[secrets] secret created",
			"cluster", clusterName, "addon", addonName, "secret", ref.SecretName,
		)
		return nil
	}

	// Secret exists — compare hashes to decide whether an update is needed.
	existingHash := hashSecretData(existing.Data)
	if existingHash == desiredHash {
		slog.Info("[secrets] secret up-to-date",
			"cluster", clusterName, "addon", addonName, "secret", ref.SecretName,
		)
		stats.Skipped++
		return nil
	}

	// Hashes differ — rotate.
	slog.Warn("[secrets] secret rotated, updating",
		"cluster", clusterName, "addon", addonName, "secret", ref.SecretName,
	)
	if updateErr := remoteclient.EnsureSecret(ctx, client, ref.Namespace, ref.SecretName, desiredData); updateErr != nil {
		return fmt.Errorf("updating secret: %w", updateErr)
	}
	stats.Updated++
	slog.Info("[secrets] secret updated",
		"cluster", clusterName, "addon", addonName, "secret", ref.SecretName,
	)
	return nil
}

// hashSecretData returns a deterministic SHA-256 hex digest of secret data.
// Keys are sorted before hashing to ensure map-iteration order has no effect.
func hashSecretData(data map[string][]byte) string {
	h := sha256.New()
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write(data[k])
	}
	return hex.EncodeToString(h.Sum(nil))
}
