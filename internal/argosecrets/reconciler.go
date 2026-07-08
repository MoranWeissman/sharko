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

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/logging"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// argosecretsSyntheticTickID returns the canonical per-tick correlation ID
// for the legacy argosecrets reconciler. Distinguished from the V125-1-8
// canonical reconciler's `recon-<unix_ts>` so the operator can tell which
// reconciler emitted a given line.
func argosecretsSyntheticTickID() string {
	return fmt.Sprintf("argosecrets-%d", time.Now().Unix())
}

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

// getCredentialsForCluster fetches credentials, forwarding the cluster's
// stored per-cluster roleArn (V2-cleanup-62.2) when the backend supports
// per-call role assumption (providers.RoleARNCredentialsProvider). The local
// ClusterCredentialsProvider interface is narrower than the providers one,
// so the capability assert happens here instead of through
// providers.GetCredentialsWithOptionalRole.
func (r *Reconciler) getCredentialsForCluster(credLookup, roleARN string) (*providers.Kubeconfig, error) {
	if roleARN != "" {
		if rp, ok := r.credProvider.(providers.RoleARNCredentialsProvider); ok {
			return rp.GetCredentialsWithRoleARN(credLookup, roleARN)
		}
	}
	return r.credProvider.GetCredentials(credLookup)
}

// AuditFunc is invoked after each reconcile pass with change counts.
type AuditFunc func(created, updated, deleted int)

// EntryAuditFunc is invoked to emit a single structured audit.Entry — for
// events AuditFunc's aggregate created/updated/deleted counters cannot
// express, such as the self-managed "waiting for the user to create the
// Secret" pending state (V2-cleanup-60 L11), which is neither a create nor
// an update. Optional; nil-safe (see emitEntryAudit).
type EntryAuditFunc func(audit.Entry)

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
	manager             *Manager
	credProvider        ClusterCredentialsProvider
	gitReader           func() GitReader // lazy — resolved from active connection
	parser              *config.Parser
	baseBranch          string
	defaultRoleARN      string // connection-level default from providers.AddonSecretProviderConfig.RoleARN
	managedClustersPath string // path to managed-clusters.yaml in the Git repo
	interval            time.Duration
	triggerCh           chan struct{}
	stopCh              chan struct{}
	stopOnce            sync.Once

	// connectivityCheckEnabled controls whether the connectivity-check label
	// (sharko.dev/connectivity-check: enabled) is applied to newly-written
	// cluster Secrets for zero-addon clusters. Default true.
	// Protected by mu.
	connectivityCheckEnabled bool

	// probeModeFn, when non-nil, is consulted once per ReconcileOnce call
	// (V2-cleanup-85.4) to combine the live, server-wide probe_mode setting
	// with connectivityCheckEnabled — returning true means probe_mode is
	// "api-test" (no connectivity-check app should ever be deployed). nil
	// means "no settings store wired" — connectivityCheckEnabled alone
	// decides, matching pre-85.4 behavior. Wired via SetProbeModeFn.
	// Protected by mu, matching auditFn/entryAuditFn.
	probeModeFn func(ctx context.Context) bool

	// Optional audit callback — set via SetAuditFunc.
	// Protected by mu.
	auditFn AuditFunc

	// Optional structured single-entry audit callback — set via
	// SetEntryAuditFunc. Used for events auditFn's aggregate counters cannot
	// express (V2-cleanup-60 L11). Protected by mu.
	entryAuditFn EntryAuditFunc

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
// connectivityCheckEnabled controls whether the connectivity-check label
// (sharko.dev/connectivity-check: enabled) is applied to new cluster Secrets
// for zero-addon clusters. Defaults to true when not explicitly set — use
// SetConnectivityCheck(false) after construction to disable.
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
		manager:                  manager,
		credProvider:             credProvider,
		gitReader:                gitReaderFn,
		parser:                   parser,
		baseBranch:               baseBranch,
		defaultRoleARN:           defaultRoleARN,
		managedClustersPath:      managedClustersPath,
		interval:                 interval,
		triggerCh:                make(chan struct{}, 1),
		stopCh:                   make(chan struct{}),
		previousOrphans:          make(map[string]bool),
		connectivityCheckEnabled: true, // on by default; disabled via SetConnectivityCheck
	}
}

// SetConnectivityCheck enables or disables the connectivity-check label for
// this reconciler instance. Thread-safe. Call before Start() for the typical
// case; safe to call after Start() but the next tick picks up the new value.
func (r *Reconciler) SetConnectivityCheck(enabled bool) {
	r.mu.Lock()
	r.connectivityCheckEnabled = enabled
	r.mu.Unlock()
}

// SetProbeModeFn registers the live, server-wide probe_mode reader
// (V2-cleanup-85.4). fn should return true when probe_mode is "api-test"
// (settings.Store.IsAPITest already has this shape and swallows its own
// read errors, defaulting to false/check-app). Pass nil to clear — the
// static connectivityCheckEnabled toggle then decides alone, matching
// pre-85.4 behavior. Thread-safe; the next ReconcileOnce call picks up the
// new function.
func (r *Reconciler) SetProbeModeFn(fn func(ctx context.Context) bool) {
	r.mu.Lock()
	r.probeModeFn = fn
	r.mu.Unlock()
}

// Start launches the background reconcile loop. Runs one reconcile immediately,
// then repeats on every tick or Trigger() call.
// Stopped by cancelling ctx or calling Stop().
//
// Each tick gets a fresh synthetic correlation ID (`argosecrets-<unix_ts>`)
// attached to the per-tick context. V2-2.2.
func (r *Reconciler) Start(ctx context.Context) {
	go func() {
		r.ReconcileOnce(logging.WithRequestID(ctx, argosecretsSyntheticTickID()))
		ticker := time.NewTicker(r.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				r.ReconcileOnce(logging.WithRequestID(ctx, argosecretsSyntheticTickID()))
			case <-r.triggerCh:
				r.ReconcileOnce(logging.WithRequestID(ctx, argosecretsSyntheticTickID()))
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
	log := logging.LoggerFromContext(ctx)
	start := time.Now()
	stats := ReconcileStats{}
	var errors []string

	log.Info("[argosecrets] reconcile started")

	// 1. Get Git reader — bail early when no connection is configured.
	gr := r.gitReader()
	if gr == nil {
		log.Warn("[argosecrets] no Git connection — skipping reconcile")
		return
	}

	// 2. Read managed-clusters.yaml from Git.
	clusterData, err := gr.GetFileContent(ctx, r.managedClustersPath, r.baseBranch)
	if err != nil {
		log.Error("[argosecrets] failed to read managed-clusters.yaml", "error", err, "path", r.managedClustersPath)
		return
	}

	// 3. Content hash check — skip if file unchanged since last pass.
	contentHash := sha256Hex(clusterData)
	r.mu.RLock()
	lastHash := r.lastContentHash
	r.mu.RUnlock()
	if contentHash == lastHash {
		log.Debug("[argosecrets] managed-clusters.yaml unchanged, skipping reconcile")
		return
	}

	// 4. Parse clusters from YAML.
	clusters, err := r.parser.ParseClusterAddons(clusterData)
	if err != nil {
		log.Error("[argosecrets] failed to parse managed-clusters.yaml", "error", err)
		return
	}

	// 5. Pre-fetch the set of already-managed secrets ONCE (Fix 2: avoid N+1 List() calls).
	// Use ListSecrets (returns full objects) so the orphan sweep can inspect
	// annotations without an extra Get per secret (Story 28.1).
	existingSecrets, listErr := r.manager.ListSecrets(ctx)
	existingManaged := make([]string, 0, len(existingSecrets))
	existingSet := make(map[string]bool, len(existingSecrets))
	// adoptedSecrets maps cluster name → true for O(1) lookup in the orphan loop.
	adoptedSecrets := make(map[string]bool, len(existingSecrets))
	if listErr == nil {
		for i := range existingSecrets {
			s := &existingSecrets[i]
			existingManaged = append(existingManaged, s.Name)
			existingSet[s.Name] = true
			if IsAdopted(s.Annotations) {
				adoptedSecrets[s.Name] = true
			}
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
			log.Error("[argosecrets] reconcile failed",
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
		log.Error("[argosecrets] failed to list managed secrets", "error", listErr)
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
			// Adopted secrets are delete-proof from the automatic sweep.
			// They can only be removed via an explicit Unadopt call (Story 28.1).
			if adoptedSecrets[name] {
				log.Warn("[argosecrets] orphan secret has adopted annotation — skipping auto-delete; remove via Unadopt",
					"cluster", name,
				)
				r.mu.RLock()
				auditFnNow := r.auditFn
				r.mu.RUnlock()
				if auditFnNow != nil {
					auditFnNow(0, 0, 0) // signal that a reconcile ran but the adopted skip is notable
				}
				stats.Skipped++
				continue
			}
			if prevOrphans[name] {
				// Orphaned on two consecutive cycles — safe to delete.
				if delErr := r.manager.Delete(ctx, name); delErr != nil {
					stats.Errors++
					errors = append(errors, fmt.Sprintf("delete orphan %s: %v", name, delErr))
					log.Error("[argosecrets] orphan delete failed",
						"cluster", name, "error", delErr,
					)
				} else {
					stats.Deleted++
					log.Info("[argosecrets] orphan secret deleted", "cluster", name)
				}
			} else {
				log.Info("[argosecrets] orphan detected, deferring deletion to next cycle", "cluster", name)
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

	log.Info("[argosecrets] reconcile complete",
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
	log := logging.LoggerFromContext(ctx)

	// Self-managed connection (connectionManagedBy: user — V2-cleanup-57.2):
	// the user creates and maintains the ArgoCD cluster Secret; Sharko NEVER
	// writes, rotates, or deletes its credential material. No credentials
	// fetch (self-managed clusters routinely have nothing in the secrets
	// backend — a lookup failure here must not error-loop), no Ensure —
	// only a label-only merge onto the existing user-created Secret. A
	// missing Secret is a visible pending state, not an error.
	if cluster.UserManagedConnection() {
		labelsChanged, secretFound, syncErr := r.manager.SyncLabelsOnly(ctx, cluster.Name, cluster.Labels)
		if syncErr != nil {
			return false, false, fmt.Errorf("syncing labels on self-managed connection: %w", syncErr)
		}
		if !secretFound {
			log.Info("[argosecrets] self-managed connection: ArgoCD cluster Secret not created yet — waiting for the user (no write attempted)",
				"cluster", cluster.Name,
			)
			// Mirror the audit event internal/clusterreconciler's syncSelfManaged
			// emits for the identical pending state (V2-cleanup-60 L11) — the
			// operator docs promise this event regardless of which reconciler
			// is active, so both must emit it.
			r.emitEntryAudit(audit.Entry{
				Level:     "info",
				Event:     "cluster_secret_user_pending",
				User:      "sharko",
				Action:    "wait_user_secret",
				Resource:  fmt.Sprintf("cluster:%s", cluster.Name),
				Source:    "reconciler",
				Result:    "partial",
				Detail:    "connection is managed by the user; create the ArgoCD cluster Secret by hand (see operator guide: self-managed connections)",
				RequestID: logging.RequestID(ctx),
			})
			return false, false, nil
		}
		return labelsChanged, false, nil
	}

	// 1. Resolve credential lookup key — secretPath overrides name
	// (shared resolver — V2-cleanup-55.1).
	credLookup := cluster.CredentialLookupKey()

	secretExistedBefore := existingSet[cluster.Name]

	// 2. Get credentials from provider. When the cluster record carries a
	// per-cluster roleArn (V2-cleanup-62.2) and the backend supports per-call
	// role assumption, EKS token minting uses the cluster's own role; every
	// other combination is byte-identical to a plain GetCredentials call.
	creds, err := r.getCredentialsForCluster(credLookup, cluster.RoleARN)
	if err != nil {
		return false, false, fmt.Errorf("getting credentials: %w", err)
	}

	// 3. Build ClusterSecretSpec.
	// Derive the connectivity-check label before building the spec so it is
	// included in the payload sent to Manager.Ensure. The feature flag and
	// the live probe_mode reader are both read under the lock to be
	// consistent with SetConnectivityCheck / SetProbeModeFn calls.
	clusterLabels := make(map[string]string, len(cluster.Labels))
	for k, v := range cluster.Labels {
		clusterLabels[k] = v
	}
	r.mu.RLock()
	checkEnabled := r.connectivityCheckEnabled
	probeModeFn := r.probeModeFn
	r.mu.RUnlock()
	// V2-cleanup-85.4: the live, server-wide probe_mode setting can turn the
	// check OFF even when connectivityCheckEnabled is true — either signal
	// disabling the check wins.
	if probeModeFn != nil && probeModeFn(ctx) {
		checkEnabled = false
	}
	models.ApplyConnectivityCheckLabel(clusterLabels, checkEnabled)

	// Per-cluster roleArn from the cluster record wins over the
	// connection-level default (V2-cleanup-62.2) — the same identity the
	// argocd-k8s-auth exec shape must assume for a cross-account cluster.
	specRoleARN := cluster.RoleARN
	if specRoleARN == "" {
		specRoleARN = r.defaultRoleARN
	}
	spec := ClusterSecretSpec{
		Name:    cluster.Name,
		Server:  creds.Server,
		Region:  cluster.Region,
		RoleARN: specRoleARN,
		// Carry ALL the credential material through so buildSecretConfig can
		// pick the right shape (precedence: cert pair > token > exec,
		// V2-cleanup-56.1):
		//   - CertData+KeyData set (client-certificate kubeconfig — kind /
		//     kubeadm / on-prem): plain-TLS shape. Without this the spec fell
		//     into the exec branch and ArgoCD ran argocd-k8s-auth against a
		//     non-AWS cluster — connection Failed forever.
		//   - Token set (bearer-token kubeconfig): bearerToken shape. Without
		//     this the exec branch would clobber the good bearer-token Secret
		//     written at registration.
		//   - Neither (EKS / IAM clusters): exec shape (RoleARN/Region
		//     preserved).
		Token: creds.Token,
		// EncodeToString(nil) == "" so clusters without a cert pair leave
		// these fields empty and never take the cert branch.
		CertData: base64.StdEncoding.EncodeToString(creds.CertData),
		KeyData:  base64.StdEncoding.EncodeToString(creds.KeyData),
		CAData:   base64.StdEncoding.EncodeToString(creds.CAData),
		Labels:   clusterLabels,
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

// SetEntryAuditFunc registers an optional callback for single structured
// audit.Entry events (V2-cleanup-60 L11) — currently only the self-managed
// "waiting for the user" pending state. Pass nil to clear.
func (r *Reconciler) SetEntryAuditFunc(fn EntryAuditFunc) {
	r.mu.Lock()
	r.entryAuditFn = fn
	r.mu.Unlock()
}

// emitEntryAudit is a nil-safe wrapper around entryAuditFn, mirroring
// internal/clusterreconciler's r.audit: a missing wire-up logs a warning and
// continues rather than panicking or silently dropping the signal.
func (r *Reconciler) emitEntryAudit(entry audit.Entry) {
	r.mu.RLock()
	fn := r.entryAuditFn
	r.mu.RUnlock()
	if fn == nil {
		slog.Warn("[argosecrets] entry audit func not wired — dropping audit entry",
			"event", entry.Event, "action", entry.Action,
		)
		return
	}
	fn(entry)
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
