package argosecrets

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// GitReader abstracts the read-only Git operations needed by the reconciler.
// Defined locally (not imported from internal/secrets) per anti-pattern rules.
type GitReader interface {
	GetFileContent(ctx context.Context, path, ref string) ([]byte, error)
}

// ClusterCredentialsProvider abstracts how we get cluster connection info.
// Matches providers.ClusterCredentialsProvider but defined as a local interface
// to keep dependency boundaries clean.
type ClusterCredentialsProvider interface {
	GetCredentials(clusterName string) (*providers.Kubeconfig, error)
}

// AuditFunc is invoked after each reconcile pass with change counts.
type AuditFunc func(created, updated, deleted int)

// ReconcileStats holds counters from the most recent reconcile cycle.
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

// Reconciler syncs ArgoCD cluster secrets with managed-clusters.yaml.
type Reconciler struct {
	manager              *Manager
	credProvider         ClusterCredentialsProvider
	gitReader            func() GitReader // lazy — resolved from active connection
	parser               *config.Parser
	baseBranch           string
	defaultRoleARN       string // connection-level default from providers.Config.RoleARN
	managedClustersPath  string // path to managed-clusters.yaml in the Git repo
	interval             time.Duration
	triggerCh            chan struct{}
	stopCh               chan struct{}
	stopOnce             sync.Once

	// Optional audit callback — set via SetAuditFunc.
	// Protected by mu.
	auditFn AuditFunc

	// Content hash of last reconciled managed-clusters.yaml to skip no-ops.
	// Protected by mu.
	lastContentHash string

	// previousOrphans holds the set of cluster names that were orphaned on the
	// previous reconcile pass. A secret is only deleted if it appears in both
	// the current orphan set AND this set — i.e., orphaned for two consecutive
	// cycles. This gives a pending PR time to merge before deletion occurs.
	// Protected by mu.
	previousOrphans map[string]bool

	// Last reconcile stats, run time, and errors (all protected by mu).
	mu         sync.RWMutex
	lastRun    time.Time
	lastStats  ReconcileStats
	lastErrors []string
}

// NewReconciler creates a Reconciler. gitReaderFn is a lazy accessor that
// returns the currently-active GitReader, or nil when no connection is live.
// managedClustersPath is the path in the Git repo to the managed clusters YAML
// file (e.g. "configuration/managed-clusters.yaml"). An empty string defaults
// to "configuration/managed-clusters.yaml".
// interval <= 0 defaults to 3 minutes.
func NewReconciler(
	manager *Manager,
	credProvider ClusterCredentialsProvider,
	gitReaderFn func() GitReader,
	parser *config.Parser,
	baseBranch string,
	defaultRoleARN string,
	managedClustersPath string,
	interval time.Duration,
) *Reconciler {
	if interval <= 0 {
		interval = 3 * time.Minute
	}
	if managedClustersPath == "" {
		managedClustersPath = "configuration/managed-clusters.yaml"
	}
	return &Reconciler{
		manager:             manager,
		credProvider:        credProvider,
		gitReader:           gitReaderFn,
		parser:              parser,
		baseBranch:          baseBranch,
		defaultRoleARN:      defaultRoleARN,
		managedClustersPath: managedClustersPath,
		interval:            interval,
		triggerCh:           make(chan struct{}, 1),
		stopCh:              make(chan struct{}),
		previousOrphans:     make(map[string]bool),
	}
}

// Start launches the background reconcile loop. Runs one reconcile immediately,
// then repeats on every tick or Trigger() call.
// Stopped by cancelling ctx or calling Stop().
func (r *Reconciler) Start(ctx context.Context) {
	go func() {
		r.ReconcileOnce(ctx)
		ticker := time.NewTicker(r.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				r.ReconcileOnce(ctx)
			case <-r.triggerCh:
				r.ReconcileOnce(ctx)
			case <-ctx.Done():
				return
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

// ReconcileOnce performs one full reconcile pass. It is public and independently
// testable. Concurrent calls are supported by the caller's single-goroutine loop
// in Start(); this method itself is not safe for concurrent execution.
func (r *Reconciler) ReconcileOnce(ctx context.Context) {
	start := time.Now()
	stats := ReconcileStats{}
	var errors []string

	slog.Info("[argosecrets] reconcile started")

	// 1. Get Git reader — bail early when no connection is configured.
	gr := r.gitReader()
	if gr == nil {
		slog.Warn("[argosecrets] no Git connection — skipping reconcile")
		return
	}

	// 2. Read managed-clusters.yaml from Git.
	clusterData, err := gr.GetFileContent(ctx, r.managedClustersPath, r.baseBranch)
	if err != nil {
		slog.Error("[argosecrets] failed to read managed-clusters.yaml", "error", err, "path", r.managedClustersPath)
		return
	}

	// 3. Content hash check — skip if file unchanged since last pass.
	contentHash := sha256Hex(clusterData)
	r.mu.RLock()
	lastHash := r.lastContentHash
	r.mu.RUnlock()
	if contentHash == lastHash {
		slog.Debug("[argosecrets] managed-clusters.yaml unchanged, skipping reconcile")
		return
	}

	// 4. Parse clusters from YAML.
	clusters, err := r.parser.ParseClusterAddons(clusterData)
	if err != nil {
		slog.Error("[argosecrets] failed to parse managed-clusters.yaml", "error", err)
		return
	}

	// 5. Pre-fetch the set of already-managed secrets ONCE (Fix 2: avoid N+1 List() calls).
	existingManaged, listErr := r.manager.List(ctx)
	existingSet := make(map[string]bool, len(existingManaged))
	if listErr == nil {
		for _, n := range existingManaged {
			existingSet[n] = true
		}
	}

	// 6. Build desired set and reconcile each cluster.
	desiredNames := make(map[string]bool, len(clusters))
	for _, cluster := range clusters {
		desiredNames[cluster.Name] = true
		stats.Checked++

		changed, created, err := r.reconcileCluster(ctx, cluster, existingSet)
		if err != nil {
			stats.Errors++
			errMsg := fmt.Sprintf("cluster=%s: %v", cluster.Name, err)
			errors = append(errors, errMsg)
			slog.Error("[argosecrets] reconcile failed",
				"cluster", cluster.Name, "error", err,
			)
			continue // skip-and-continue — never block the rest
		}

		if !changed {
			stats.Skipped++
		} else if created {
			stats.Created++
		} else {
			stats.Updated++
		}
	}

	// 7. Orphan cleanup — delete managed secrets not in Git.
	// Reuse existingManaged from step 5 (no second List() call).
	// Two-cycle deferral: a secret is only deleted if it was also orphaned on
	// the previous pass. This gives an in-flight PR time to merge before the
	// reconciler treats the adopted secret as an orphan and deletes it.
	if listErr != nil {
		slog.Error("[argosecrets] failed to list managed secrets", "error", listErr)
	} else {
		currentOrphans := make(map[string]bool)
		for _, name := range existingManaged {
			if !desiredNames[name] {
				currentOrphans[name] = true
			}
		}

		r.mu.RLock()
		prevOrphans := r.previousOrphans
		r.mu.RUnlock()

		for name := range currentOrphans {
			if prevOrphans[name] {
				// Orphaned on two consecutive cycles — safe to delete.
				if delErr := r.manager.Delete(ctx, name); delErr != nil {
					stats.Errors++
					errors = append(errors, fmt.Sprintf("delete orphan %s: %v", name, delErr))
					slog.Error("[argosecrets] orphan delete failed",
						"cluster", name, "error", delErr,
					)
				} else {
					stats.Deleted++
					slog.Info("[argosecrets] orphan secret deleted", "cluster", name)
				}
			} else {
				slog.Info("[argosecrets] orphan detected, deferring deletion to next cycle", "cluster", name)
			}
		}

		// Update previousOrphans for the next cycle.
		r.mu.Lock()
		r.previousOrphans = currentOrphans
		r.mu.Unlock()
	}

	// 8. Update content hash only when there were no errors (Fix 5: don't suppress retries on partial failure).
	if stats.Errors == 0 {
		r.mu.Lock()
		r.lastContentHash = contentHash
		r.mu.Unlock()
	}

	// 9. Record stats.
	stats.Duration = time.Since(start).String()
	stats.LastRun = time.Now()
	r.mu.Lock()
	r.lastRun = time.Now()
	r.lastStats = stats
	r.lastErrors = errors
	r.mu.Unlock()

	slog.Info("[argosecrets] reconcile complete",
		"checked", stats.Checked,
		"created", stats.Created,
		"updated", stats.Updated,
		"deleted", stats.Deleted,
		"skipped", stats.Skipped,
		"errors", stats.Errors,
		"duration", stats.Duration,
	)

	// 10. Invoke audit callback when changes occurred (Fix 4: read auditFn under lock).
	r.mu.RLock()
	auditFn := r.auditFn
	r.mu.RUnlock()
	if auditFn != nil && (stats.Created > 0 || stats.Updated > 0 || stats.Deleted > 0) {
		auditFn(stats.Created, stats.Updated, stats.Deleted)
	}
}

// reconcileCluster builds a ClusterSecretSpec from provider credentials and
// cluster-addons.yaml data, then calls Manager.Ensure().
//
// It returns (changed bool, created bool, err error).
// changed reflects whether Ensure reported a state change.
// created is true only when the secret was newly created (not in existingSet before the call).
// existingSet is the pre-fetched map of already-managed secret names.
func (r *Reconciler) reconcileCluster(ctx context.Context, cluster models.Cluster, existingSet map[string]bool) (changed, created bool, err error) {
	// 1. Resolve credential lookup key — secretPath overrides name.
	credLookup := cluster.Name
	if cluster.SecretPath != "" {
		credLookup = cluster.SecretPath
	}

	secretExistedBefore := existingSet[cluster.Name]

	// 2. Get credentials from provider.
	creds, err := r.credProvider.GetCredentials(credLookup)
	if err != nil {
		return false, false, fmt.Errorf("getting credentials: %w", err)
	}

	// 3. Build ClusterSecretSpec.
	spec := ClusterSecretSpec{
		Name:    cluster.Name,
		Server:  creds.Server,
		Region:  cluster.Region,
		RoleARN: r.defaultRoleARN,
		CAData:  base64.StdEncoding.EncodeToString(creds.CAData),
		Labels:  cluster.Labels,
	}

	// 4. Call Manager.Ensure() — returns whether it changed anything.
	changed, ensureErr := r.manager.Ensure(ctx, spec)
	if ensureErr != nil {
		return false, false, fmt.Errorf("ensuring secret: %w", ensureErr)
	}

	if !changed {
		return false, false, nil // skipped
	}
	// changed=true: determine whether this was a create or update.
	return true, !secretExistedBefore, nil
}

// SetAuditFunc registers an optional callback invoked after each reconcile
// pass that produced changes. Pass nil to clear.
func (r *Reconciler) SetAuditFunc(fn AuditFunc) {
	r.mu.Lock()
	r.auditFn = fn
	r.mu.Unlock()
}

// GetStats returns a snapshot of the last reconcile run's statistics.
func (r *Reconciler) GetStats() ReconcileStats {
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

// sha256Hex returns the SHA-256 hex digest of raw bytes.
func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
