package secrets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/logging"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
	"github.com/MoranWeissman/sharko/internal/remoteclient"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// v3CatalogPath is where a v3 repo keeps its addon catalog. The v4 repo's
// two files are config.AddonCatalogPath (catalog.yaml) and
// config.V4ManagedClustersPath (managed-clusters.yaml) — imported, never
// re-typed here, so this package cannot drift away from what the rest of
// Sharko reads and quietly conclude a v4 repo has no secrets in it.
const v3CatalogPath = "configuration/addons-catalog.yaml"

// Repo layouts this reconciler knows how to read. Used in log lines only.
const (
	layoutV3 = "v3"
	layoutV4 = "v4"
)

// GitReader abstracts the read-only Git operations needed by the reconciler.
type GitReader interface {
	GetFileContent(ctx context.Context, path, ref string) ([]byte, error)
}

// RemoteClientFactory builds a kubernetes.Interface from raw kubeconfig bytes.
// Abstracted for testing — production uses remoteclient.NewClientFromKubeconfig.
type RemoteClientFactory func(kubeconfig []byte) (kubernetes.Interface, error)

// AuditFunc is a callback the Reconciler invokes after reconciling secrets so
// that callers can record audit entries without creating an import cycle.
// clusterName is the cluster that was affected; created and updated are counts
// from this reconcile pass.
type AuditFunc func(clusterName string, created, updated int)

// Reconciler periodically fetches secret definitions from the Git catalog and
// ensures the corresponding K8s Secrets exist and are up-to-date on every
// remote cluster that has the owning addon enabled.
//
// It reads BOTH repo layouts:
//
//   - v3 — configuration/addons-catalog.yaml (an entry's `secrets:` block)
//     plus the configured managed-clusters path, with the cluster's addon
//     labels saying which addons it runs.
//   - v4 — catalog.yaml (a requirement's `push:` block) plus the root
//     managed-clusters.yaml, with clusters/<name>.yaml saying which addons
//     it runs.
//
// Both flatten into the same list of pushes, so a rotation lands on a
// migrated repo exactly as it did before the migration.
//
// It supports three triggers:
//  1. Periodic timer (default 5 min)
//  2. Explicit Trigger() call (e.g. after a webhook push)
//  3. Initial run on Start()
type Reconciler struct {
	credProvider        providers.ClusterCredentialsProvider
	secretProvider      providers.SecretProvider
	gitReader           func() GitReader // lazy — resolved from active connection
	remoteClientFn      RemoteClientFactory
	parser              *config.Parser
	baseBranch          string
	managedClustersPath string // path in Git repo to managed-clusters.yaml
	interval            time.Duration
	triggerCh           chan struct{}
	stopCh              chan struct{}
	stopOnce            sync.Once

	// Optional audit callback — set via SetAuditFunc.
	auditFn AuditFunc

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
// managedClustersPath is the path in the Git repo to the managed clusters YAML
// (e.g. "configuration/managed-clusters.yaml"). An empty string defaults to
// "configuration/managed-clusters.yaml".
// interval <= 0 defaults to 5 minutes.
func NewReconciler(
	credProvider providers.ClusterCredentialsProvider,
	secretProvider providers.SecretProvider,
	gitReaderFn func() GitReader,
	remoteClientFn RemoteClientFactory,
	parser *config.Parser,
	baseBranch string,
	managedClustersPath string,
	interval time.Duration,
) *Reconciler {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	if managedClustersPath == "" {
		managedClustersPath = "configuration/managed-clusters.yaml"
	}
	return &Reconciler{
		credProvider:        credProvider,
		secretProvider:      secretProvider,
		gitReader:           gitReaderFn,
		remoteClientFn:      remoteClientFn,
		parser:              parser,
		baseBranch:          baseBranch,
		managedClustersPath: managedClustersPath,
		interval:            interval,
		triggerCh:           make(chan struct{}, 1),
		stopCh:              make(chan struct{}),
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

// SetAuditFunc registers an optional callback invoked after each successful
// reconcile cycle. Pass nil to clear an existing callback.
func (r *Reconciler) SetAuditFunc(fn AuditFunc) {
	r.auditFn = fn
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
//
// Each pass gets a synthetic correlation ID (`secrets-<unix_ts>`) attached to
// the per-pass context so every slog line emitted carries the same
// request_id. V2-2.2.
func (r *Reconciler) reconcile() {
	start := time.Now()
	stats := ReconcileStats{}
	var errs []string

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	ctx = logging.WithRequestID(ctx, fmt.Sprintf("secrets-%d", time.Now().Unix()))
	log := logging.LoggerFromContext(ctx)

	log.Info("[secrets] reconcile started")

	// 1. Get Git reader — bail when no connection is configured. This one
	// stays a silent no-op: "no git connection yet" is a setup state, not a
	// failure of a run, and Connections already says so on its own page.
	gr := r.gitReader()
	if gr == nil {
		log.Warn("[secrets] no Git connection — skipping reconcile")
		return
	}

	// 2. Work out what should be pushed where, on whichever repo layout
	// this is.
	//
	// A failure here used to warn-log and return BEFORE the status fields
	// were written, which meant the API's secrets status kept showing the
	// last good run with no errors while nothing was being pushed at all
	// (v4 wave 2.5 review, finding F1). Now it lands in lastErrors, where
	// the status endpoint and the UI can see it.
	plan, err := r.planPushes(ctx, gr)
	if err != nil {
		stats.Errors = 1
		errs = []string{err.Error()}
		log.Error("[secrets] could not work out which secrets to push — nothing was pushed this run", "error", err)
		r.recordRun(start, stats, errs)
		return
	}
	if len(plan.problems) > 0 {
		stats.Errors += len(plan.problems)
		errs = append(errs, plan.problems...)
		for _, p := range plan.problems {
			log.Error("[secrets] part of the repo could not be read — those clusters were skipped", "problem", p)
		}
	}

	if len(plan.work) == 0 && len(errs) == 0 {
		log.Info("[secrets] no addons with secret definitions — nothing to reconcile", "layout", plan.layout)
		return
	}

	// 3. Push every secret the plan asks for.
	for _, w := range plan.work {
		stats.Checked++
		if err := r.reconcileSecret(ctx, &stats, w.credLookup, w.addon, w.push); err != nil {
			stats.Errors++
			errs = append(errs, fmt.Sprintf("cluster=%s addon=%s secret=%s: %v",
				w.clusterName, w.addon, w.push.SecretName, err))
			log.Error("[secrets] reconcile failed",
				"cluster", w.clusterName,
				"addon", w.addon,
				"secret", w.push.SecretName,
				"error", err,
			)
		}
	}

	r.recordRun(start, stats, errs)

	log.Info("[secrets] reconcile complete",
		"layout", plan.layout,
		"checked", stats.Checked,
		"created", stats.Created,
		"updated", stats.Updated,
		"deleted", stats.Deleted,
		"skipped", stats.Skipped,
		"errors", stats.Errors,
		"duration", stats.Duration,
	)

	// Invoke audit callback when secrets were created or updated.
	if r.auditFn != nil && (stats.Created > 0 || stats.Updated > 0) {
		r.auditFn("*", stats.Created, stats.Updated)
	}
}

// recordRun publishes one pass's outcome to the status fields GetStats and
// GetErrors serve. Every path that gets far enough to have an outcome —
// including a pass that failed before pushing anything — goes through here,
// so "the status says fine" and "secrets are being pushed" cannot come
// apart.
func (r *Reconciler) recordRun(start time.Time, stats ReconcileStats, errs []string) {
	stats.Duration = time.Since(start).String()
	stats.LastRun = time.Now()

	r.mu.Lock()
	r.lastRun = stats.LastRun
	r.lastStats = stats
	r.lastErrors = errs
	r.mu.Unlock()
}

// secretWork is one thing to push: a single Kubernetes Secret, for one
// addon, onto one cluster. Both repo layouts are flattened into this, so
// the push itself has exactly one implementation.
type secretWork struct {
	clusterName string
	// credLookup is the key to fetch the cluster's credentials with — the
	// stored secretPath when the cluster record has one, else the plain
	// name (shared resolver, V2-cleanup-55.1).
	credLookup string
	addon      string
	push       models.AddonSecretRef
}

// pushPlan is everything one reconcile pass intends to do.
type pushPlan struct {
	layout string
	work   []secretWork
	// problems are things that went wrong for PART of the repo — one
	// unreadable cluster file, one cluster name that cannot be a path.
	// The rest of the plan still runs; these are recorded in the status
	// so a half-working run never looks like a clean one.
	problems []string
}

// planPushes reads the connected repo and works out every secret that
// should be on every cluster.
//
// The repo is one layout or the other. v3 is checked first — the same
// order internal/config's credential resolver and the cluster reconciler
// use — so a v3 repo behaves exactly as it always has, and a repo that
// somehow has both files keeps its v3 answer rather than switching
// underneath the other readers.
//
// An error return means nothing could be read at all and nothing will be
// pushed this pass; the caller puts it in the status.
func (r *Reconciler) planPushes(ctx context.Context, gr GitReader) (pushPlan, error) {
	v3Data, v3Err := gr.GetFileContent(ctx, v3CatalogPath, r.baseBranch)
	if v3Err == nil && len(v3Data) > 0 {
		return r.planV3(ctx, gr, v3Data)
	}

	v4Data, v4Err := gr.GetFileContent(ctx, config.AddonCatalogPath, r.baseBranch)
	if v4Err == nil && len(v4Data) > 0 {
		return r.planV4(ctx, gr, v4Data)
	}

	return pushPlan{}, fmt.Errorf(
		"no addon catalog could be read on branch %q, so no addon secrets can be pushed: %s: %v; %s: %v",
		r.baseBranch,
		config.AddonCatalogPath, readProblem(v4Data, v4Err),
		v3CatalogPath, readProblem(v3Data, v3Err),
	)
}

// readProblem turns a read result into the reason it was not usable, so
// the "no catalog" message says what actually happened to each file rather
// than just naming them.
func readProblem(data []byte, err error) error {
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return errors.New("the file is empty")
	}
	return nil
}

// planV3 builds the plan from a v3 repo: configuration/addons-catalog.yaml
// for the push definitions, the configured managed-clusters path for the
// clusters, and the cluster's addon LABELS for which addons it runs.
// Unchanged behaviour — this is the pre-v4 path, moved into its own
// function.
func (r *Reconciler) planV3(ctx context.Context, gr GitReader, catalogData []byte) (pushPlan, error) {
	plan := pushPlan{layout: layoutV3}

	catalogEntries, err := r.parser.ParseAddonsCatalog(catalogData)
	if err != nil {
		return plan, fmt.Errorf("could not read %s: %w", v3CatalogPath, err)
	}

	pushesByAddon := make(map[string][]models.AddonSecretRef)
	for _, entry := range catalogEntries {
		if len(entry.Secrets) > 0 {
			pushesByAddon[entry.Name] = entry.Secrets
		}
	}
	if len(pushesByAddon) == 0 {
		return plan, nil
	}

	clusterData, err := gr.GetFileContent(ctx, r.managedClustersPath, r.baseBranch)
	if err != nil {
		return plan, fmt.Errorf("could not read %s: %w", r.managedClustersPath, err)
	}
	clusters, err := r.parser.ParseClusterAddons(clusterData)
	if err != nil {
		return plan, fmt.Errorf("could not read %s: %w", r.managedClustersPath, err)
	}

	for _, cluster := range clusters {
		for _, enabled := range r.parser.GetEnabledAddons(cluster, catalogEntries) {
			for _, ref := range pushesByAddon[enabled.AddonName] {
				plan.work = append(plan.work, secretWork{
					clusterName: cluster.Name,
					credLookup:  cluster.CredentialLookupKey(),
					addon:       enabled.AddonName,
					push:        ref,
				})
			}
		}
	}
	return plan, nil
}

// planV4 builds the plan from a v4 repo: catalog.yaml for the push
// definitions each approved addon carries, the root managed-clusters.yaml
// for the clusters, and clusters/<name>.yaml for which addons each one
// actually runs.
//
// A cluster with no assignment file yet is not an error — it is a
// registered cluster that has not been given any addon. Anything else that
// stops one cluster being read is recorded as a problem and the other
// clusters still get their secrets.
func (r *Reconciler) planV4(ctx context.Context, gr GitReader, catalogData []byte) (pushPlan, error) {
	plan := pushPlan{layout: layoutV4}

	spec, err := config.LoadAddonCatalog(catalogData)
	if err != nil {
		return plan, fmt.Errorf("could not read %s: %w", config.AddonCatalogPath, err)
	}

	// Only the requirements carrying a push block are Sharko's to create.
	// A plain-English requirement with no push block is a note for a
	// person; there is nothing for the reconciler to do about it.
	pushesByAddon := make(map[string][]models.AddonSecretRef)
	for addonName, entry := range spec.Addons {
		for _, req := range entry.Secrets {
			if req.Push == nil {
				continue
			}
			pushesByAddon[addonName] = append(pushesByAddon[addonName], *req.Push)
		}
	}
	if len(pushesByAddon) == 0 {
		return plan, nil
	}

	clusterData, err := gr.GetFileContent(ctx, config.V4ManagedClustersPath, r.baseBranch)
	if err != nil {
		return plan, fmt.Errorf("could not read %s: %w", config.V4ManagedClustersPath, err)
	}
	clusters, err := r.parser.ParseClusterAddons(clusterData)
	if err != nil {
		return plan, fmt.Errorf("could not read %s: %w", config.V4ManagedClustersPath, err)
	}

	for _, cluster := range clusters {
		assignPath, pathErr := config.V4ClusterAddonsPath(cluster.Name)
		if pathErr != nil {
			plan.problems = append(plan.problems, fmt.Sprintf(
				"cluster %q in %s cannot be looked up (%v), so no addon secrets were pushed to it",
				cluster.Name, config.V4ManagedClustersPath, pathErr))
			continue
		}

		body, readErr := gr.GetFileContent(ctx, assignPath, r.baseBranch)
		if readErr != nil {
			if errors.Is(readErr, gitprovider.ErrFileNotFound) {
				// Registered, but no addon has been switched on for it yet.
				continue
			}
			plan.problems = append(plan.problems, fmt.Sprintf(
				"could not read %s (%v), so no addon secrets were pushed to cluster %q",
				assignPath, readErr, cluster.Name))
			continue
		}
		if len(body) == 0 {
			continue
		}

		assignment, parseErr := models.LoadClusterAddons(body)
		if parseErr != nil {
			plan.problems = append(plan.problems, fmt.Sprintf(
				"could not read %s (%v), so no addon secrets were pushed to cluster %q",
				assignPath, parseErr, cluster.Name))
			continue
		}

		for _, addonName := range enabledAddonNames(assignment) {
			for _, ref := range pushesByAddon[addonName] {
				plan.work = append(plan.work, secretWork{
					clusterName: cluster.Name,
					credLookup:  cluster.CredentialLookupKey(),
					addon:       addonName,
					push:        ref,
				})
			}
		}
	}
	return plan, nil
}

// enabledAddonNames lists the addons a v4 cluster actually runs, sorted so
// two identical repos produce the same work order (and the same log lines)
// every pass.
func enabledAddonNames(spec models.ClusterAddonsSpec) []string {
	names := make([]string, 0, len(spec.Addons))
	for name, entry := range spec.Addons {
		if entry.Enabled {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// reconcileSecret ensures a single K8s Secret exists and is current on the
// named remote cluster. It increments Created, Updated, or Skipped on stats.
func (r *Reconciler) reconcileSecret(
	ctx context.Context,
	stats *ReconcileStats,
	clusterName, addonName string,
	ref models.AddonSecretRef,
) error {
	log := logging.LoggerFromContext(ctx)

	// A half-written definition would otherwise surface as a confusing
	// Kubernetes API error (a Secret with no name, or no namespace to put
	// it in). Say which part is missing instead.
	if missing := ref.MissingFields(); len(missing) > 0 {
		return fmt.Errorf("the secret definition in the catalog has no %s — fill that in and Sharko can push it",
			strings.Join(missing, " and no "))
	}

	// Get kubeconfig for the cluster.
	log.Info("[reconciler] connecting to cluster", "cluster", clusterName)
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
		log.Info("[secrets] creating secret",
			"cluster", clusterName, "addon", addonName,
			"secret", ref.SecretName, "namespace", ref.Namespace,
		)
		if createErr := remoteclient.EnsureSecret(ctx, client, ref.Namespace, ref.SecretName, desiredData); createErr != nil {
			return fmt.Errorf("creating secret: %w", createErr)
		}
		stats.Created++
		log.Info("[secrets] secret created",
			"cluster", clusterName, "addon", addonName, "secret", ref.SecretName,
		)
		return nil
	}

	// Secret exists — compare hashes to decide whether an update is needed.
	existingHash := hashSecretData(existing.Data)
	log.Debug("[reconciler] hash comparison",
		"cluster", clusterName,
		"secret", ref.SecretName,
		"match", desiredHash == existingHash,
	)
	if existingHash == desiredHash {
		log.Info("[secrets] secret up-to-date",
			"cluster", clusterName, "addon", addonName, "secret", ref.SecretName,
		)
		stats.Skipped++
		return nil
	}

	// Hashes differ — rotate.
	log.Warn("[secrets] secret rotated, updating",
		"cluster", clusterName, "addon", addonName, "secret", ref.SecretName,
	)
	if updateErr := remoteclient.EnsureSecret(ctx, client, ref.Namespace, ref.SecretName, desiredData); updateErr != nil {
		return fmt.Errorf("updating secret: %w", updateErr)
	}
	stats.Updated++
	log.Info("[secrets] secret updated",
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
