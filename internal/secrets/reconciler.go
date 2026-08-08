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
	"github.com/MoranWeissman/sharko/internal/metrics"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
	"github.com/MoranWeissman/sharko/internal/remoteclient"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// engineAddonValues is this package's value for the "engine" metric label
// (P2-D) — matches the "addon_values" key of GET /system/managed-secrets'
// "engines" section. Wiring choice: a direct import of internal/metrics,
// the same as internal/clusterreconciler — see that package's
// engineClusterConnection doc comment for the full reasoning (leaf package,
// no import-cycle risk, matches the rest of the codebase's direct-call
// convention for this package).
const engineAddonValues = "addon_values"

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

// ItemOutcome classifies the result of the most recent reconcile attempt
// for a single addon-values secret (one cluster+addon pair). Mirrors the
// vocabulary shape of clusterreconciler.ReconcileOutcome, widened with an
// explicit "unchanged" state (this reconciler's most common outcome — a
// hash match means nothing was written) and a "skipped" state distinct
// from a live failure (a half-written catalog definition — nothing on the
// remote cluster was ever touched, so it is not the same kind of problem
// as a K8s API error).
type ItemOutcome string

const (
	// ItemOutcomeCreated means the Secret did not exist on the remote
	// cluster and this pass created it.
	ItemOutcomeCreated ItemOutcome = "created"
	// ItemOutcomeUpdated means the Secret existed but its content hash
	// differed from the desired value, so this pass rotated it.
	ItemOutcomeUpdated ItemOutcome = "updated"
	// ItemOutcomeUnchanged means the Secret already matched the desired
	// content hash — checked, nothing to do.
	ItemOutcomeUnchanged ItemOutcome = "unchanged"
	// ItemOutcomeSkipped means this pass deliberately did not attempt a
	// write for this item — the catalog's push definition is incomplete
	// (see models.AddonSecretRef.MissingFields). Not a live-cluster
	// failure: nothing was attempted against Kubernetes at all.
	ItemOutcomeSkipped ItemOutcome = "skipped"
	// ItemOutcomeError means this pass attempted a write (or the reads
	// leading up to one) and it failed — credentials, connecting to the
	// cluster, fetching a value from the secrets provider, or the K8s API
	// call itself.
	ItemOutcomeError ItemOutcome = "error"
	// ItemOutcomeOutOfSync means a single-item, read-only check (CheckOne —
	// S4's "Refresh" row action) found the live secret's content hash does
	// NOT match its source, WITHOUT writing a fix. Distinct from
	// ItemOutcomeUpdated: the periodic pass always writes when hashes
	// differ, but a check-only request must not. Never produced by the
	// periodic reconcile() pass itself.
	ItemOutcomeOutOfSync ItemOutcome = "out_of_sync"
	// ItemOutcomeMissing means a single-item, read-only check (CheckOne)
	// found no Secret at all on the remote cluster. Distinct from
	// ItemOutcomeCreated: the periodic pass creates it on the spot, but a
	// check-only request only observes and reports. Never produced by the
	// periodic reconcile() pass itself.
	ItemOutcomeMissing ItemOutcome = "missing"
	// ItemOutcomeForeign means a Secret with this name already exists on
	// the remote cluster and Sharko did not create it — it carries no
	// app.kubernetes.io/managed-by=sharko label (P1-A). Somebody else owns
	// it: a person, External Secrets Operator, Sealed Secrets, a vault
	// agent, a Helm chart. Sharko records it every pass and never writes to
	// it, so this is a boundary, not damage and not a failure: the item
	// carries no error text, because nothing went wrong.
	ItemOutcomeForeign ItemOutcome = "foreign"
)

// ErrForeignSecret is what SyncOne returns when the target Secret exists on
// the cluster without Sharko's ownership label. The UI already disables Sync
// for a foreign row, so reaching this is defense in depth — but the message
// is written to be shown to a person verbatim (internal/api hands it back on
// a 422), which is why it reads as a sentence rather than the usual
// lowercase error fragment.
var ErrForeignSecret = errors.New("Someone else created this one — Sharko will not touch it.")

// ErrNoGitConnection means a single-item action (CheckOne/SyncOne — S4)
// could not run because no Git connection is currently active. The
// periodic pass treats this as a quiet "setup state, skip this tick" (see
// reconcile()); a single request-driven action has a caller waiting on an
// answer, so it gets a real error instead.
var ErrNoGitConnection = errors.New("no Git connection is configured — nothing to check or push")

// ErrReconcilerDisabled is returned by CheckAll when an admin has switched
// the addon-values engine off (gitops-proud P4-I, D2 — see SetEnabledFn).
// Mirrors ErrNoGitConnection's shape: a real error for a single
// request-driven call with a caller waiting on an answer. The periodic
// pass treats "disabled" the same quiet way it treats "no Git connection
// yet" — see reconcile().
var ErrReconcilerDisabled = errors.New("the addon-values engine is switched off")

// ItemKey identifies one addon-values secret row: a single cluster+addon
// pair. Matches the granularity internal/api's addon_values_secrets table
// already reads at (one row per cluster+addon — see
// buildAddonValuesSecretRows, which resolves exactly one secret definition
// per addon name).
type ItemKey struct {
	Cluster string
	Addon   string
}

// ItemRecord is the last known reconcile outcome for one addon-values
// secret (cluster+addon pair).
type ItemRecord struct {
	// LastChecked is stamped on every pass that examined this item,
	// regardless of outcome.
	LastChecked time.Time
	Outcome     ItemOutcome
	// ChangedAt is the time of the last actual write (Created or
	// Updated) — the zero time when this server instance has never
	// written this item. An Unchanged/Skipped/Error outcome NEVER moves
	// this forward; it carries the previous value across.
	ChangedAt time.Time
	// Error is the reconcileSecret error message when Outcome is Skipped
	// or Error; empty otherwise.
	Error string
	// ConsecutiveFailures (P2-D D3) counts how many passes IN A ROW this
	// item's check or write attempt itself failed (ItemOutcomeError only —
	// never for a legitimate finding like out_of_sync, missing, or
	// foreign, which are not failures). Reset to 0 the instant a pass
	// completes without erroring on this item, whatever outcome it found.
	// In-memory only, like every other field here — a restart starts the
	// count over at 0, which only delays (never falsifies) the "the last N
	// checks failed in a row" panel line reaching 3.
	ConsecutiveFailures int
}

// ItemAuditFunc is invoked once per addon-values secret that was actually
// created or updated during a reconcile pass — never for an Unchanged
// check. At 50 clusters x 10 addons on a 5-minute cadence, an entry per
// check would flood the audit log; an entry per real change is the useful
// signal. cluster and addon identify the row; outcome is always
// ItemOutcomeCreated or ItemOutcomeUpdated.
type ItemAuditFunc func(cluster, addon string, outcome ItemOutcome)

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
//     managed-clusters.yaml, with cluster-addons/<name>.yaml saying which addons
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

	// Optional audit callbacks — set via SetAuditFunc / SetItemAuditFunc.
	auditFn     AuditFunc
	itemAuditFn ItemAuditFunc

	// Last reconcile stats (protected by mu)
	mu         sync.RWMutex
	lastRun    time.Time
	lastStats  ReconcileStats
	lastErrors []string
	// lastErrorCluster / lastErrorAt (P1-B B2) name WHICH cluster the first
	// entry in lastErrors is about, and WHEN that pass ran — mirroring
	// clusterreconciler.Reconciler.LastError's cluster+time fields (#716).
	// lastErrorCluster is empty when the failure was plan-level (the
	// catalog or managed-clusters file itself couldn't be read, before any
	// per-item work started) — there is no cluster to name in that case.
	// Both are the zero value exactly when lastErrors is empty.
	lastErrorCluster string
	lastErrorAt      time.Time
	// itemRecords holds the last known outcome per addon-values secret
	// (cluster+addon pair). Rebuilt from scratch on every pass that gets
	// far enough to compute a plan (see reconcile()) — never merged with
	// the previous map — so an item whose work has vanished from the plan
	// (addon disabled, cluster removed) stops being reported the very
	// next pass, rather than lingering forever. In-memory only, like
	// every other reconciler status here: empty after a restart, which
	// reads as "not checked since restart", never a fabricated
	// timestamp.
	itemRecords map[ItemKey]ItemRecord

	// sourceType (P2-C5) names the real secrets-provider backend this
	// reconciler's pushes are compared against — the value the caller
	// configured (e.g. "aws-sm", "k8s-secrets"), stamped verbatim into
	// every values Secret's sharko.dev/source provenance annotation. Set
	// via SetSourceType; "" is a legitimate zero value (falls back to the
	// honest generic label — see remoteclient.ValuesProvenanceAnnotations),
	// never guessed.
	sourceType string

	// enabledFn (gitops-proud P4-I, D2) is the live reader consulted at the
	// top of both reconcile() and CheckAll() — the engine's off switch. Set
	// via SetEnabledFn. nil means "no settings store wired" (out-of-cluster
	// / dev mode, or no setter ever called) — the engine stays ON by
	// default in that case, same as every install that never touches the
	// setting; see isEnabled.
	enabledFn func(ctx context.Context) bool

	// orphanMu guards orphanRecords — kept SEPARATE from mu (leftover-
	// secrets S1) so a slow orphan scan (one remote LIST call per cluster)
	// never blocks a reader of itemRecords, and vice versa. Follows this
	// file's own "own mutex per independent piece of state" style (see
	// stopOnce next to stopCh above).
	orphanMu sync.RWMutex
	// orphanRecords holds the last known orphan-secret findings, keyed by
	// cluster name. Rebuilt PER CLUSTER on every scan — never wholesale —
	// so a cluster whose LIST call fails this pass keeps its previous
	// findings (and their honest LastChecked timestamps) instead of losing
	// them or fabricating fresh ones (locked decision #10, orphans.go).
	orphanRecords map[string][]models.OrphanedSecret
}

// SetEnabledFn registers the live reader this reconciler consults, at the
// top of every pass, to decide whether it should run at all (gitops-proud
// P4-I, D2 — the addon-values engine's off switch). Pass nil to clear an
// existing callback and fall back to "always on". Wire
// settings.Store.IsAddonValuesEngineEnabled here — see cmd/sharko/serve.go.
func (r *Reconciler) SetEnabledFn(fn func(ctx context.Context) bool) {
	r.enabledFn = fn
}

// isEnabled reports whether a pass should run right now. nil enabledFn (no
// settings store wired) reads as enabled — the engine must not go silently
// dark just because it is running out-of-cluster/dev, matching every other
// nil-safe wrapper in this codebase's settings pattern.
func (r *Reconciler) isEnabled(ctx context.Context) bool {
	if r.enabledFn == nil {
		return true
	}
	return r.enabledFn(ctx)
}

// IsEnabled exports isEnabled (M6, code review) — the same answer CheckAll
// checks internally before it does anything else, but reachable
// synchronously BEFORE a caller commits to a response. Before this existed,
// handleCheckSecrets (internal/api/secrets_reconcile.go) always 202'd and
// wrote a "checked N secrets" audit entry, then started a goroutine that
// called CheckAll — which discovered ErrReconcilerDisabled and only logged
// it. The audit trail said a check ran; nothing did.
func (r *Reconciler) IsEnabled(ctx context.Context) bool {
	return r.isEnabled(ctx)
}

// SetSourceType records the real secrets-provider backend type this
// reconciler pushes addon-values secrets from, for the sharko.dev/source
// provenance annotation stamped on every write (P2-C5). Pass the same
// providers.AddonSecretProviderConfig.Type value wired into
// providers.NewAddonSecretProvider for this reconciler's secretProvider —
// see cmd/sharko/serve.go's wiring.
func (r *Reconciler) SetSourceType(sourceType string) {
	r.sourceType = sourceType
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

// SetItemAuditFunc registers an optional callback invoked once per
// addon-values secret actually created or updated (never for an unchanged
// check — see ItemAuditFunc). Pass nil to clear an existing callback.
func (r *Reconciler) SetItemAuditFunc(fn ItemAuditFunc) {
	r.itemAuditFn = fn
}

// LastItemState returns the most recently recorded reconcile outcome for
// the named cluster+addon addon-values secret pair. ok is false when no
// pass has ever processed this pair on this server instance — a fresh
// startup, or a cluster/addon combination that has never appeared in the
// plan (not registered, addon not enabled, or no secret definition).
func (r *Reconciler) LastItemState(cluster, addon string) (ItemRecord, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rec, ok := r.itemRecords[ItemKey{Cluster: cluster, Addon: addon}]
	return rec, ok
}

// LastItemChecked returns the last-checked timestamp for one addon-values
// secret's cluster+addon pair. Primitive-typed — same import-free-boundary
// reasoning as LastRunTime/LastError/Interval, so internal/api can report
// a per-row timestamp on the System page without importing this package.
// ok is false when this pair has never been reconciled on this server
// instance.
func (r *Reconciler) LastItemChecked(cluster, addon string) (lastChecked time.Time, ok bool) {
	rec, ok := r.LastItemState(cluster, addon)
	if !ok {
		return time.Time{}, false
	}
	return rec.LastChecked, true
}

// LastItemOutcome reports the last-recorded outcome for one addon-values
// secret's cluster+addon pair, as a plain string (one of the ItemOutcome
// constants above). Primitive-typed — same import-free-boundary reasoning
// as LastItemChecked, so internal/api can compute a per-row state without
// importing this package. ok is false when this pair has never been
// checked, synced, or reconciled on this server instance.
func (r *Reconciler) LastItemOutcome(cluster, addon string) (outcome string, ok bool) {
	rec, ok := r.LastItemState(cluster, addon)
	if !ok {
		return "", false
	}
	return string(rec.Outcome), true
}

// LastItemError reports the raw reconcile-failure text recorded for one
// addon-values secret's cluster+addon pair (ItemRecord.Error — populated
// when the periodic pass's reconcileSecret call returned an error; see the
// reconcile() loop above). ok is false when this pair has never been
// checked, or its last recorded outcome carried no error. Primitive-typed,
// same import-free-boundary reasoning as LastItemOutcome.
//
// SECURITY (S8): this is the RAW error text — reconcileSecret wraps
// secrets-provider errors verbatim (e.g. "fetching %q from provider: %w"),
// and a misbehaving provider SDK could in principle echo a fragment of a
// secret value inside its own error text. Callers MUST NOT render this
// string to a user directly — map it to a safe canned sentence first (see
// internal/api's addonValuesSecretCheckFailureSentence).
func (r *Reconciler) LastItemError(cluster, addon string) (errMsg string, ok bool) {
	rec, ok := r.LastItemState(cluster, addon)
	if !ok || rec.Error == "" {
		return "", false
	}
	return rec.Error, true
}

// LastItemConsecutiveFailures reports the current consecutive-failure count
// (P2-D D3) for one addon-values secret's cluster+addon pair — the ItemKey
// primitive-typed accessor internal/api reads to surface consecutive_
// failures on the row without importing this package (same import-free-
// boundary reasoning as LastItemOutcome/LastItemError). ok is false when
// this pair has never been checked or written on this server instance.
func (r *Reconciler) LastItemConsecutiveFailures(cluster, addon string) (count int, ok bool) {
	rec, ok := r.LastItemState(cluster, addon)
	if !ok {
		return 0, false
	}
	return rec.ConsecutiveFailures, true
}

// KnownItemCount reports how many cluster+addon pairs this engine holds a
// record for — the real blast radius of one CheckAll (P3-F1), so the audit
// entry a "Refresh all" writes can state how much it actually covered
// instead of saying "everything" and leaving the reader to guess.
//
// 0 means "no pass has run on this server instance yet", not "there is
// nothing to check" — a caller that wants to state a number must treat 0
// as "we cannot say" and fall back to wording with no number in it.
func (r *Reconciler) KnownItemCount() int {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.itemRecords)
}

// GetErrors returns the error messages from the last reconcile run.
func (r *Reconciler) GetErrors() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, len(r.lastErrors))
	copy(out, r.lastErrors)
	return out
}

// Interval returns the configured reconcile cadence (how often the
// background loop ticks, independent of Trigger() nudges). Primitive
// return type — same import-free-boundary reasoning as GetStats — so
// internal/api can report it on the System page without importing this
// package (System-page managed-secrets summary).
func (r *Reconciler) Interval() time.Duration {
	return r.interval
}

// LastRunTime returns the timestamp of the most recent reconcile run, or
// the zero time if the reconciler has never run yet (fresh startup, or no
// Git connection has ever been configured).
func (r *Reconciler) LastRunTime() time.Time {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastRun
}

// LastError returns the mapped, safe-to-show sentence (P1-B B2, via
// FailureSentence) for the first error message recorded during the most
// recent reconcile run, or "" when that run had no errors (including the
// "never run yet" case). NEVER the raw error text — see FailureSentence's
// doc comment for why.
func (r *Reconciler) LastError() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.lastErrors) == 0 {
		return ""
	}
	return FailureSentence(r.lastErrors[0])
}

// LastErrorCluster names the cluster LastError's sentence is ABOUT — the
// first failing cluster+addon pair's cluster name recorded during the most
// recent reconcile run (P1-B B2, mirrors clusterreconciler.Reconciler.
// LastError's cluster — #716). Empty when LastError() is empty, or when the
// failure was plan-level (there is no cluster to name).
func (r *Reconciler) LastErrorCluster() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastErrorCluster
}

// LastErrorAt is the timestamp of the reconcile run LastError was recorded
// during (P1-B B2) — an error with no "since when" isn't actionable. The
// zero time exactly when LastError() is empty.
func (r *Reconciler) LastErrorAt() time.Time {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastErrorAt
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

	// gitops-proud P4-I (D2) — the engine's off switch. Checked BEFORE the
	// "no Git connection" bail below, and treated the exact same way: a
	// silent no-op, not a failure. An admin turning this off is a deliberate
	// choice, not something a run should report as broken — rows keep
	// showing their last-known facts from whichever pass ran last.
	if !r.isEnabled(ctx) {
		log.Info("[secrets] addon-values engine is switched off — skipping reconcile")
		// M7 (code review): the pass-age and sustained-bad-state alerts key
		// off this gauge to stay silent while an admin has deliberately
		// turned the engine off — see the gauge's own doc comment in
		// internal/metrics/metrics.go.
		metrics.ReconcilerEnabled.WithLabelValues(engineAddonValues).Set(0)
		return
	}
	metrics.ReconcilerEnabled.WithLabelValues(engineAddonValues).Set(1)

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
		// Plan-level failure: nothing cluster-specific went wrong yet, so
		// there is no cluster to name (P1-B B2).
		r.recordRun(start, stats, errs, "")
		// P2-D D1: a plan-level failure aborts before any per-item work — the
		// same "failure" outcome clusterreconciler reserves for its own
		// whole-pass aborts (git read / schema validation), never "partial"
		// (partial means SOME items were attempted).
		metrics.ReconcilerRuns.WithLabelValues(engineAddonValues, "failure").Inc()
		metrics.ReconcilerDuration.WithLabelValues(engineAddonValues).Observe(time.Since(start).Seconds())
		metrics.ReconcilerLastRun.WithLabelValues(engineAddonValues).Set(float64(time.Now().Unix()))
		return
	}
	if len(plan.problems) > 0 {
		stats.Errors += len(plan.problems)
		errs = append(errs, plan.problems...)
		for _, p := range plan.problems {
			log.Error("[secrets] part of the repo could not be read — those clusters were skipped", "problem", p)
		}
	}

	// 3. Push every secret the plan asks for, recording a per-item outcome
	// alongside the aggregate counters. Snapshot the previous per-item
	// state first (under RLock) so an Unchanged/Skipped/Error outcome can
	// carry its ChangedAt forward instead of losing "when was this last
	// actually written" every time a pass finds nothing to change.
	r.mu.RLock()
	prevItems := make(map[ItemKey]ItemRecord, len(r.itemRecords))
	for k, v := range r.itemRecords {
		prevItems[k] = v
	}
	r.mu.RUnlock()

	itemResults := make(map[ItemKey]ItemRecord, len(plan.work))
	// errCluster (P1-B B2) is the cluster the FIRST per-item error in errs
	// is about — mirrors clusterreconciler.Reconciler.LastError's
	// "first/most-recent Failed record names its cluster" contract, so
	// LastErrorCluster() always agrees with what LastError()'s message is
	// actually describing.
	var errCluster string
	for _, w := range plan.work {
		stats.Checked++
		outcome, err := r.reconcileSecret(ctx, &stats, w.clusterName, w.credLookup, w.addon, w.push)

		key := ItemKey{Cluster: w.clusterName, Addon: w.addon}
		rec := ItemRecord{LastChecked: time.Now(), Outcome: outcome}
		if outcome == ItemOutcomeCreated || outcome == ItemOutcomeUpdated {
			rec.ChangedAt = rec.LastChecked
		} else if prev, ok := prevItems[key]; ok {
			rec.ChangedAt = prev.ChangedAt
		}

		// P2-D D3: consecutive-failure count — increments only on a real
		// attempt failure (err != nil below), resets to 0 on anything else,
		// including a legitimate finding like out_of_sync or missing (those
		// are not failures — the check itself succeeded at determining them).
		if err != nil {
			rec.ConsecutiveFailures = prevItems[key].ConsecutiveFailures + 1
		}

		if err != nil {
			rec.Error = err.Error()
			stats.Errors++
			errs = append(errs, fmt.Sprintf("cluster=%s addon=%s secret=%s: %v",
				w.clusterName, w.addon, w.push.SecretName, err))
			if errCluster == "" {
				errCluster = w.clusterName
			}
			log.Error("[secrets] reconcile failed",
				"cluster", w.clusterName,
				"addon", w.addon,
				"secret", w.push.SecretName,
				"error", err,
			)
			// P2-D D2: the stuck-loop counter's failure half — a SMALL fixed
			// reason set, never the raw error text.
			metrics.ReconcilerItemFailures.WithLabelValues(engineAddonValues, itemFailureReason(err)).Inc()
		} else if outcome == ItemOutcomeCreated || outcome == ItemOutcomeUpdated {
			// P2-D D1/D2: the stuck-loop counter's write half — one increment
			// per actual Kubernetes write this pass made. Shares the
			// (engine, kind) shape with the legacy items-changed counter
			// (declared since before this lane, never written until now) on
			// purpose — they describe the same events.
			kind := "created"
			if outcome == ItemOutcomeUpdated {
				kind = "updated"
			}
			metrics.ReconcilerItemsChanged.WithLabelValues(engineAddonValues, kind).Inc()
			metrics.ReconcilerWrites.WithLabelValues(engineAddonValues, kind).Inc()
			if r.itemAuditFn != nil {
				r.itemAuditFn(w.clusterName, w.addon, outcome)
			}
		}
		itemResults[key] = rec
	}

	r.mu.Lock()
	r.itemRecords = itemResults
	r.mu.Unlock()

	// leftover-secrets S1: the orphan scan runs on EVERY completed pass,
	// including the zero-work one right below — "the catalog entry was
	// hand-deleted" is exactly a zero-push plan, and that is precisely the
	// case this scan exists to catch. Never gated behind len(plan.work).
	r.scanOrphans(ctx, plan)

	if len(plan.work) == 0 && len(errs) == 0 {
		log.Info("[secrets] no addons with secret definitions — nothing to reconcile", "layout", plan.layout)
		// P2-D D1: still a completed pass (git and the catalog both read
		// fine — there is simply nothing to push), so it counts toward
		// runs_total/duration/last_run like any other clean pass. Pass-age
		// alerting depends on this: an estate with zero secret definitions
		// must not look like the engine stopped running.
		metrics.ReconcilerRuns.WithLabelValues(engineAddonValues, "success").Inc()
		metrics.ReconcilerDuration.WithLabelValues(engineAddonValues).Observe(time.Since(start).Seconds())
		now := float64(time.Now().Unix())
		metrics.ReconcilerLastRun.WithLabelValues(engineAddonValues).Set(now)
		metrics.ReconcilerLastSuccess.WithLabelValues(engineAddonValues).Set(now)
		recordAddonValuesStateGauges(itemResults, r.OrphanedSecretCount())
		return
	}

	r.recordRun(start, stats, errs, errCluster)

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

	// P2-D D1: outcome mirrors clusterreconciler's own success/partial rule
	// — reaching this line means the pass did NOT abort (that already
	// returned above), so "failure" never applies here; "partial" is any
	// pass with at least one per-item error.
	outcome := "success"
	if stats.Errors > 0 {
		outcome = "partial"
	}
	metrics.ReconcilerRuns.WithLabelValues(engineAddonValues, outcome).Inc()
	metrics.ReconcilerDuration.WithLabelValues(engineAddonValues).Observe(time.Since(start).Seconds())
	now := float64(time.Now().Unix())
	metrics.ReconcilerLastRun.WithLabelValues(engineAddonValues).Set(now)
	if outcome == "success" {
		metrics.ReconcilerLastSuccess.WithLabelValues(engineAddonValues).Set(now)
	}
	metrics.ReconcilerItemsChecked.WithLabelValues(engineAddonValues).Add(float64(stats.Checked))

	recordAddonValuesStateGauges(itemResults, r.OrphanedSecretCount())
	recordAddonValuesFightGauge(itemResults)
}

// addonValuesMetricStates is every state sharko_managed_secrets_state can
// report for this engine (P2-D D1/D4) — written every pass, in full, so a
// state that just emptied out reports 0 instead of a stale nonzero value.
// "orphaned" (leftover-secrets S1) is set from the orphan scan's own record
// count, not from an ItemOutcome — see recordAddonValuesStateGauges.
var addonValuesMetricStates = []string{"in_sync", "out_of_sync", "missing", "foreign", "unknown", "orphaned"}

// metricStateFromOutcome buckets one item's outcome into the
// sharko_managed_secrets_state vocabulary (P2-D D1). Deliberately mirrors
// internal/api/system_managed_secrets.go's addonValuesSecretRowState rule —
// api cannot be imported here (it imports this package), so the two are
// kept in sync by hand; see clusterreconciler.metricStateFromRecord's doc
// comment for the same trade-off on the connection side.
func metricStateFromOutcome(outcome ItemOutcome) string {
	switch outcome {
	case ItemOutcomeCreated, ItemOutcomeUpdated, ItemOutcomeUnchanged:
		return "in_sync"
	case ItemOutcomeOutOfSync:
		return "out_of_sync"
	case ItemOutcomeMissing:
		return "missing"
	case ItemOutcomeForeign:
		return "foreign"
	default: // ItemOutcomeError, ItemOutcomeSkipped
		return "unknown"
	}
}

// recordAddonValuesStateGauges sets sharko_managed_secrets_state for the
// addon_values engine from THIS pass's freshly rebuilt item map (P2-D D1) —
// itemResults, not r.itemRecords, since reconcile() already rebuilds the
// whole map from scratch every pass (never merged with the previous one —
// see itemRecords' own doc comment), so itemResults IS the complete current
// picture without an extra lock round-trip.
//
// orphanCount (leftover-secrets S1) is a separate fact from itemResults —
// an orphan is, by definition, a secret NO current work item claims, so it
// can never be derived from an ItemOutcome. Pass r.OrphanedSecretCount(),
// itself read from the same scan that just ran.
func recordAddonValuesStateGauges(items map[ItemKey]ItemRecord, orphanCount int) {
	counts := make(map[string]int, len(addonValuesMetricStates))
	for _, rec := range items {
		counts[metricStateFromOutcome(rec.Outcome)]++
	}
	counts["orphaned"] = orphanCount
	for _, state := range addonValuesMetricStates {
		metrics.ManagedSecretsState.WithLabelValues(engineAddonValues, state).Set(float64(counts[state]))
	}
}

// fightGaugeThreshold mirrors clusterreconciler.fightGaugeThreshold (P2-D
// D3) — the same "three in a row" bar the row warning in
// ui/src/views/ManagedSecrets.tsx uses, applied here to ConsecutiveFailures
// instead of the connection engine's revert streak.
const fightGaugeThreshold = 3

// recordAddonValuesFightGauge sets sharko_reconciler_fights for the
// addon_values engine (P2-D D3) — the count of items whose consecutive
// check/write failure streak has reached fightGaugeThreshold or more.
func recordAddonValuesFightGauge(items map[ItemKey]ItemRecord) {
	count := 0
	for _, rec := range items {
		if rec.ConsecutiveFailures >= fightGaugeThreshold {
			count++
		}
	}
	metrics.ReconcilerFights.WithLabelValues(engineAddonValues).Set(float64(count))
}

// itemFailureReason buckets a reconcileSecret error into the small fixed
// reason vocabulary sharko_reconciler_item_failures_total uses (P2-D D2).
// Mirrors FailureSentence's own stage-matching order and prefixes exactly
// (failure_sentence.go) — same classification, a short metric-label code
// instead of a sentence for a person.
func itemFailureReason(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "the secret definition in the catalog has no"):
		return "catalog_definition"
	case strings.Contains(msg, "skip certificate checks"):
		// remoteclient.ErrUnverifiedDestination (task #152 lane C) — same
		// position as FailureSentence's case, before the write-stage
		// buckets, for the same reason.
		return "unverified_connection"
	case strings.Contains(msg, "getting credentials"):
		return "credentials"
	case strings.Contains(msg, "connecting to cluster"):
		return "connect"
	case strings.Contains(msg, "provider"):
		return "provider_fetch"
	case strings.Contains(msg, "existing secret"):
		return "read_secret"
	case strings.Contains(msg, "creating secret"), strings.Contains(msg, "updating secret"):
		return "write_failed"
	default:
		return "other"
	}
}

// recordRun publishes one pass's outcome to the status fields GetStats and
// GetErrors serve. Every path that gets far enough to have an outcome —
// including a pass that failed before pushing anything — goes through here,
// so "the status says fine" and "secrets are being pushed" cannot come
// apart.
//
// errCluster (P1-B B2) is the cluster the first entry in errs is about —
// "" for a plan-level failure (nothing cluster-specific to name) or when
// errs is empty. Stamped alongside lastErrors under the same lock so
// LastError()/LastErrorCluster()/LastErrorAt() can never disagree about
// which pass they're describing.
func (r *Reconciler) recordRun(start time.Time, stats ReconcileStats, errs []string, errCluster string) {
	stats.Duration = time.Since(start).String()
	stats.LastRun = time.Now()

	r.mu.Lock()
	r.lastRun = stats.LastRun
	r.lastStats = stats
	r.lastErrors = errs
	if len(errs) > 0 {
		r.lastErrorCluster = errCluster
		r.lastErrorAt = stats.LastRun
	} else {
		r.lastErrorCluster = ""
		r.lastErrorAt = time.Time{}
	}
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
	// clusters (leftover-secrets S1) is every managed cluster this pass
	// read from the repo — name plus credential lookup key — regardless of
	// whether any addon has a push definition right now. Populated
	// UNCONDITIONALLY by planV3/planV4, even when work ends up empty: "the
	// catalog entry that used to define this push was hand-deleted" is
	// exactly the scenario orphan detection exists to catch (orphans.go),
	// and that scenario is precisely the one where work is empty but the
	// clusters — and the leftover secrets sitting on them — still need to
	// be looked at.
	clusters []clusterRef
	// problems are things that went wrong for PART of the repo — one
	// unreadable cluster file, one cluster name that cannot be a path.
	// The rest of the plan still runs; these are recorded in the status
	// so a half-working run never looks like a clean one.
	problems []string
}

// clusterRef is the minimum a cluster's identity takes to be scanned for
// orphan secrets (or reconciled for push work): its display name and the
// key to fetch its credentials with (which differs from the name when the
// cluster record has a SecretPath override — see
// models.Cluster.CredentialLookupKey).
type clusterRef struct {
	name       string
	credLookup string
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

	// The cluster list is read UNCONDITIONALLY — even when the catalog
	// currently defines no push at all (leftover-secrets S1). A catalog
	// entry's secrets block being hand-deleted is exactly the case orphan
	// detection exists for, and that case is precisely a zero-push plan;
	// bailing out before reading clusters, as this used to, would mean the
	// orphan scan never learns which clusters to look at.
	clusterData, err := gr.GetFileContent(ctx, r.managedClustersPath, r.baseBranch)
	if err != nil {
		return plan, fmt.Errorf("could not read %s: %w", r.managedClustersPath, err)
	}
	clusters, err := r.parser.ParseClusterAddons(clusterData)
	if err != nil {
		return plan, fmt.Errorf("could not read %s: %w", r.managedClustersPath, err)
	}
	for _, cluster := range clusters {
		plan.clusters = append(plan.clusters, clusterRef{name: cluster.Name, credLookup: cluster.CredentialLookupKey()})
	}

	if len(pushesByAddon) == 0 {
		return plan, nil
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
// for the clusters, and cluster-addons/<name>.yaml for which addons each one
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

	// The cluster list is read UNCONDITIONALLY — even when the catalog
	// currently defines no push at all. See planV3's matching comment
	// (leftover-secrets S1): a zero-push plan is exactly the case orphan
	// detection needs the cluster list for.
	clusterData, err := gr.GetFileContent(ctx, config.V4ManagedClustersPath, r.baseBranch)
	if err != nil {
		return plan, fmt.Errorf("could not read %s: %w", config.V4ManagedClustersPath, err)
	}
	clusters, err := r.parser.ParseClusterAddons(clusterData)
	if err != nil {
		return plan, fmt.Errorf("could not read %s: %w", config.V4ManagedClustersPath, err)
	}
	for _, cluster := range clusters {
		plan.clusters = append(plan.clusters, clusterRef{name: cluster.Name, credLookup: cluster.CredentialLookupKey()})
	}

	if len(pushesByAddon) == 0 {
		return plan, nil
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
// named remote cluster. It increments Created, Updated, or Skipped on
// stats, and returns the per-item outcome the caller records alongside
// stats (ItemOutcomeCreated/Updated/Unchanged/Skipped/Error) plus the same
// error it always returned — callers that only care about aggregate
// success/failure can ignore the outcome and check err exactly as before.
//
// clusterName is the cluster's display name — used for logging and as the
// per-item record's key. credLookup is the key to fetch credentials with,
// which differs from clusterName when the cluster record has a SecretPath
// override (models.Cluster.CredentialLookupKey). Passing credLookup into
// both used to be one parameter (a latent mislabeling bug fixed alongside
// this story: every log line here used to show the credential lookup
// key, not the cluster's real name, for any cluster with a SecretPath
// override — cosmetic today, but it would have kept this function's new
// per-item record keyed wrong).
func (r *Reconciler) reconcileSecret(
	ctx context.Context,
	stats *ReconcileStats,
	clusterName, credLookup, addonName string,
	ref models.AddonSecretRef,
) (ItemOutcome, error) {
	log := logging.LoggerFromContext(ctx)

	// A half-written definition would otherwise surface as a confusing
	// Kubernetes API error (a Secret with no name, or no namespace to put
	// it in). Say which part is missing instead. Nothing on the remote
	// cluster is touched, so this is a deliberate skip, not a failure.
	if missing := ref.MissingFields(); len(missing) > 0 {
		return ItemOutcomeSkipped, fmt.Errorf("the secret definition in the catalog has no %s — fill that in and Sharko can push it",
			strings.Join(missing, " and no "))
	}

	// Get kubeconfig for the cluster.
	log.Info("[reconciler] connecting to cluster", "cluster", clusterName)
	creds, err := r.credProvider.GetCredentials(credLookup)
	if err != nil {
		return ItemOutcomeError, fmt.Errorf("getting credentials: %w", err)
	}

	// TLS refusal (task #152 lane C), asked BEFORE building a client and
	// BEFORE any value is pulled out of the secrets store — there is no
	// reason to fetch a value Sharko is never going to send. The hard stop
	// lives in remoteclient.EnsureSecret (the choke point every write goes
	// through); this early return exists — mirroring the foreign-secret
	// early return below — so the recorded outcome is a deliberate refusal
	// carrying Sharko's own fixed sentence, not a failed write attempt.
	// SyncOne drives this same function, so the "Sync" row action inherits
	// the identical refusal.
	if tlsErr := remoteclient.CheckDestinationTLS(creds.Raw); tlsErr != nil {
		log.Info("[secrets] refusing to deliver this secret — the cluster's connection is set up to skip certificate checks",
			"cluster", clusterName, "addon", addonName,
			"namespace", ref.Namespace, "secret", ref.SecretName,
		)
		return ItemOutcomeError, tlsErr
	}

	// Build a K8s client for the remote cluster.
	client, err := r.remoteClientFn(creds.Raw)
	if err != nil {
		return ItemOutcomeError, fmt.Errorf("connecting to cluster: %w", err)
	}

	// Fetch desired values from the secrets provider and compute a hash.
	desiredData := make(map[string][]byte)
	for key, providerPath := range ref.Keys {
		val, err := r.secretProvider.GetSecretValue(ctx, providerPath)
		if err != nil {
			return ItemOutcomeError, fmt.Errorf("fetching %q from provider: %w", providerPath, err)
		}
		desiredData[key] = val
	}
	desiredHash := hashSecretData(desiredData)

	// Check whether the secret already exists on the remote cluster.
	existing, err := client.CoreV1().Secrets(ref.Namespace).Get(ctx, ref.SecretName, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return ItemOutcomeError, fmt.Errorf("checking existing secret: %w", err)
		}
		// Secret doesn't exist — create it.
		log.Info("[secrets] creating secret",
			"cluster", clusterName, "addon", addonName,
			"secret", ref.SecretName, "namespace", ref.Namespace,
		)
		provenance := remoteclient.ValuesProvenanceAnnotations(addonName, r.sourceType, time.Now())
		if createErr := remoteclient.EnsureSecret(ctx, client, ref.Namespace, ref.SecretName, desiredData, provenance); createErr != nil {
			// A Secret that appeared between this Get and the create, owned
			// by somebody else — the choke point refused it, and so does
			// this pass.
			if errors.Is(createErr, remoteclient.ErrForeignSecret) {
				return ItemOutcomeForeign, nil
			}
			return ItemOutcomeError, fmt.Errorf("creating secret: %w", createErr)
		}
		stats.Created++
		log.Info("[secrets] secret created",
			"cluster", clusterName, "addon", addonName, "secret", ref.SecretName,
		)
		return ItemOutcomeCreated, nil
	}

	// Ownership gate (P1-A). The Secret exists but Sharko did not create
	// it — no hash comparison, no write, no error. Sharko only reconciles
	// what is its own; every pass re-records this same outcome, and the row
	// says so on the page.
	//
	// remoteclient.EnsureSecret refuses the same write on its own (that is
	// the real choke point, and it covers the orchestrator's create path
	// too). This early return exists so the periodic pass never even
	// attempts a write it knows will be refused, and so the recorded
	// outcome is "foreign" rather than an error.
	if !remoteclient.IsManagedBySharko(existing) {
		log.Info("[secrets] leaving this secret alone — it exists on the cluster and Sharko did not create it",
			"cluster", clusterName, "addon", addonName,
			"namespace", ref.Namespace, "secret", ref.SecretName,
		)
		return ItemOutcomeForeign, nil
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
		return ItemOutcomeUnchanged, nil
	}

	// Hashes differ — rotate.
	log.Warn("[secrets] secret rotated, updating",
		"cluster", clusterName, "addon", addonName, "secret", ref.SecretName,
	)
	updateProvenance := remoteclient.ValuesProvenanceAnnotations(addonName, r.sourceType, time.Now())
	if updateErr := remoteclient.EnsureSecret(ctx, client, ref.Namespace, ref.SecretName, desiredData, updateProvenance); updateErr != nil {
		// Belt and braces: the ownership gate above already returned for a
		// foreign Secret, so this only fires if the label was stripped
		// between the two calls. Either way, no write happened.
		if errors.Is(updateErr, remoteclient.ErrForeignSecret) {
			return ItemOutcomeForeign, nil
		}
		return ItemOutcomeError, fmt.Errorf("updating secret: %w", updateErr)
	}
	stats.Updated++
	log.Info("[secrets] secret updated",
		"cluster", clusterName, "addon", addonName, "secret", ref.SecretName,
	)
	return ItemOutcomeUpdated, nil
}

// findWork resolves the exact secretWork one cluster+addon pair maps to, by
// re-reading the connected repo (git catalog + cluster files) the same way
// planPushes does for the periodic pass — filtered down to a single pair.
// Used by CheckOne and SyncOne (S4) so a single-item action always agrees
// with what the periodic pass would compute for the same pair, instead of
// re-implementing the v3/v4 layout logic a second time.
//
// The "not found" error deliberately names every reason the pair could be
// absent from the plan — cluster not registered, addon not enabled on it,
// or the addon's catalog entry has no push block — since findWork has no
// cheap way to tell which one actually applies without extra reads that
// duplicate planPushes' own work.
func (r *Reconciler) findWork(ctx context.Context, clusterName, addonName string) (secretWork, error) {
	gr := r.gitReader()
	if gr == nil {
		return secretWork{}, ErrNoGitConnection
	}
	plan, err := r.planPushes(ctx, gr)
	if err != nil {
		return secretWork{}, fmt.Errorf("could not read the addon catalog or managed clusters list: %w", err)
	}
	for _, w := range plan.work {
		if w.clusterName == clusterName && w.addon == addonName {
			return w, nil
		}
	}
	return secretWork{}, fmt.Errorf(
		"no addon-values secret is defined for cluster %q, addon %q — check that the cluster is registered, the addon is enabled on it, and the addon's catalog entry defines a secret to push",
		clusterName, addonName)
}

// recordItemCheck stamps the per-item record for a single key — the same
// itemRecords map the periodic pass writes wholesale on every tick, updated
// here for exactly one key instead. ChangedAt is always carried forward
// from whatever it already was: only a real write (see SyncOne) ever
// advances it, never a read-only check.
//
// ConsecutiveFailures (L10, code review) is carried forward the same way the
// periodic pass's own loop does it (see the "err != nil" branch in
// reconcile()): errMsg != "" means this call is reporting a real check
// failure (checkWork only ever passes a non-empty errMsg on its error and
// skip-due-to-incomplete-definition paths), so the streak increments; any
// real finding — unchanged, out_of_sync, missing, foreign — passes errMsg =
// "" and resets it to 0. Before this, every call here built a fresh
// ItemRecord with ConsecutiveFailures left at its zero value, so a row
// stuck failing on manual Refresh (or on CheckAll — see checkWork) never
// crossed the fight-detection threshold the periodic pass uses.
func (r *Reconciler) recordItemCheck(cluster, addon string, outcome ItemOutcome, errMsg string) {
	key := ItemKey{Cluster: cluster, Addon: addon}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.itemRecords == nil {
		r.itemRecords = make(map[ItemKey]ItemRecord)
	}
	prev := r.itemRecords[key]
	rec := ItemRecord{
		LastChecked: time.Now(),
		Outcome:     outcome,
		ChangedAt:   prev.ChangedAt,
		Error:       errMsg,
	}
	if errMsg != "" {
		rec.ConsecutiveFailures = prev.ConsecutiveFailures + 1
	}
	r.itemRecords[key] = rec
}

// CheckOne re-checks a single addon-values secret (one cluster+addon pair)
// against its source RIGHT NOW, without writing anything — S4's "Refresh"
// row action. Returns the outcome as a plain string (one of the
// ItemOutcome constants above; "" on error) so internal/api never has to
// import this package to read it back.
//
// Unlike reconcileSecret (the periodic pass's write primitive, which always
// fixes what it finds), this only reads: it fetches the desired value from
// the secrets provider, reads the live Secret if one exists, and compares
// hashes. A mismatch is reported as ItemOutcomeOutOfSync and a missing
// Secret as ItemOutcomeMissing — neither ever writes. Updates the same
// per-item record CheckOne/SyncOne/the periodic pass all share, so the
// System page and the Managed Secrets page see this check immediately.
func (r *Reconciler) CheckOne(ctx context.Context, clusterName, addonName string) (string, error) {
	w, err := r.findWork(ctx, clusterName, addonName)
	if err != nil {
		return "", err
	}
	outcome, err := r.checkWork(ctx, w)
	if err != nil {
		return "", err
	}
	return string(outcome), nil
}

// CheckAll re-checks EVERY addon-values secret against its source right now,
// without writing anything — the fleet-wide counterpart of CheckOne, and
// what the page's "Refresh all" button drives (P1-A A3).
//
// It is deliberately NOT the periodic pass with a flag: reconcile() creates
// missing Secrets and rotates changed ones, which is exactly what a button
// labelled "check" must never do. This function reuses checkWork, the same
// read-only primitive CheckOne uses, over the same plan the periodic pass
// computes — so a check and a repair always agree on which items exist,
// while only one of them ever writes.
//
// Per-item failure isolation matches the periodic pass: one unreachable
// cluster does not stop the rest being checked. The returned error is only
// for the case where nothing could be checked at all (no Git connection, or
// the catalog could not be read).
func (r *Reconciler) CheckAll(ctx context.Context) error {
	log := logging.LoggerFromContext(ctx)
	start := time.Now()

	// gitops-proud P4-I (D2) — the off switch stops BOTH passes: the write
	// pass above (reconcile) and this check pass. "Check all now" on a
	// switched-off engine has nothing to check with — same shape as
	// ErrNoGitConnection, a real error for this single request-driven call.
	if !r.isEnabled(ctx) {
		// M7 (code review): same gauge write as reconcile()'s own disabled
		// branch — a CheckAll call is a real, request-driven attempt to run
		// this engine, and it belongs in the same "is this engine even on"
		// signal the periodic pass keeps up to date.
		metrics.ReconcilerEnabled.WithLabelValues(engineAddonValues).Set(0)
		return ErrReconcilerDisabled
	}
	metrics.ReconcilerEnabled.WithLabelValues(engineAddonValues).Set(1)

	gr := r.gitReader()
	if gr == nil {
		return ErrNoGitConnection
	}
	plan, err := r.planPushes(ctx, gr)
	if err != nil {
		return fmt.Errorf("could not read the addon catalog or managed clusters list: %w", err)
	}

	// H2 (code review): before this, a failure here was counted only in the
	// local checked/failed variables below and logged once — CheckAll then
	// returned nil regardless of how many (or how few) items actually
	// succeeded, so a vault outage during "Check all now" looked identical
	// to a clean pass to everything outside this function: the engine's own
	// LastError/LastRun kept reporting whatever the LAST successful pass
	// found, and the audit entry the handler writes said "N checked" with no
	// hint that every one of them failed. stats + errs mirror reconcile()'s
	// own bookkeeping so recordRun below can publish a real run record for
	// this check pass, exactly like the periodic write pass does for
	// itself.
	stats := ReconcileStats{}
	var errs []string
	var errCluster string
	checked, failed := 0, 0
	for _, w := range plan.work {
		stats.Checked++
		if _, checkErr := r.checkWork(ctx, w); checkErr != nil {
			failed++
			stats.Errors++
			errs = append(errs, fmt.Sprintf("cluster=%s addon=%s secret=%s: %v",
				w.clusterName, w.addon, w.push.SecretName, checkErr))
			if errCluster == "" {
				errCluster = w.clusterName
			}
			log.Warn("[secrets] could not check this addon-values secret — carrying on with the rest",
				"cluster", w.clusterName, "addon", w.addon, "secret", w.push.SecretName, "error", checkErr)
			continue
		}
		checked++
	}

	// leftover-secrets S1: CheckAll is a read-only pass, and the orphan
	// scan is itself read-only (a LIST call, never a write) — it belongs
	// here on exactly the same terms it belongs at the end of reconcile().
	r.scanOrphans(ctx, plan)

	r.recordRun(start, stats, errs, errCluster)

	outcome := "success"
	if stats.Errors > 0 {
		outcome = "partial"
	}
	metrics.ReconcilerRuns.WithLabelValues(engineAddonValues, outcome).Inc()
	metrics.ReconcilerDuration.WithLabelValues(engineAddonValues).Observe(time.Since(start).Seconds())
	now := float64(time.Now().Unix())
	metrics.ReconcilerLastRun.WithLabelValues(engineAddonValues).Set(now)
	if outcome == "success" {
		metrics.ReconcilerLastSuccess.WithLabelValues(engineAddonValues).Set(now)
	}
	metrics.ReconcilerItemsChecked.WithLabelValues(engineAddonValues).Add(float64(stats.Checked))

	log.Info("[secrets] check-only pass complete — nothing was written",
		"layout", plan.layout, "checked", checked, "could_not_check", failed)
	return nil
}

// checkWork is the read-only half of the values engine: fetch what the
// secret SHOULD hold, read what is live on the cluster, compare, record.
// It never creates, updates, patches or deletes anything in Kubernetes —
// that guarantee is what makes every "Refresh" button on this page honest,
// so keep it that way.
//
// Outcomes it can record: unchanged (matches), out_of_sync (differs),
// missing (nothing there), foreign (something is there that Sharko did not
// create), skipped (the catalog definition is incomplete — paired with an
// error). A returned error means the check itself could not finish.
func (r *Reconciler) checkWork(ctx context.Context, w secretWork) (ItemOutcome, error) {
	clusterName, addonName := w.clusterName, w.addon

	if missing := w.push.MissingFields(); len(missing) > 0 {
		checkErr := fmt.Errorf("the secret definition in the catalog has no %s — fill that in before Sharko can check it",
			strings.Join(missing, " and no "))
		r.recordItemCheck(clusterName, addonName, ItemOutcomeSkipped, checkErr.Error())
		return "", checkErr
	}

	creds, err := r.credProvider.GetCredentials(w.credLookup)
	if err != nil {
		checkErr := fmt.Errorf("getting credentials for cluster %q: %w", clusterName, err)
		// H2 (code review): every failure path below used to return before
		// recordItemCheck ever ran — CheckAll counted the failure locally and
		// moved on, but the ROW never learned about it, so a vault outage
		// left every row still saying whatever its last successful check
		// found. recordItemCheck(..., ItemOutcomeError, ...) is what makes
		// this failure visible on the page and to LastItemConsecutiveFailures
		// (L10), same as a periodic-pass failure already is.
		r.recordItemCheck(clusterName, addonName, ItemOutcomeError, checkErr.Error())
		return "", checkErr
	}

	// TLS refusal (task #152 lane C) — the check side. A destination
	// Sharko refuses to deliver to is refused on the check too, so the row
	// says the same thing after a "Refresh" click as it does after the
	// periodic pass, instead of flip-flopping between a drift verdict and
	// a refusal every five minutes. Asked before the client is built and
	// before any value is fetched from the secrets store.
	if tlsErr := remoteclient.CheckDestinationTLS(creds.Raw); tlsErr != nil {
		r.recordItemCheck(clusterName, addonName, ItemOutcomeError, tlsErr.Error())
		return "", tlsErr
	}

	client, err := r.remoteClientFn(creds.Raw)
	if err != nil {
		checkErr := fmt.Errorf("connecting to cluster %q: %w", clusterName, err)
		r.recordItemCheck(clusterName, addonName, ItemOutcomeError, checkErr.Error())
		return "", checkErr
	}

	existing, err := client.CoreV1().Secrets(w.push.Namespace).Get(ctx, w.push.SecretName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			r.recordItemCheck(clusterName, addonName, ItemOutcomeMissing, "")
			return ItemOutcomeMissing, nil
		}
		checkErr := fmt.Errorf("checking the existing secret on cluster %q: %w", clusterName, err)
		r.recordItemCheck(clusterName, addonName, ItemOutcomeError, checkErr.Error())
		return "", checkErr
	}

	// Ownership gate (P1-A), asked BEFORE anything is pulled out of the
	// secrets store: there is no reason to fetch a value Sharko is never
	// going to write.
	if !remoteclient.IsManagedBySharko(existing) {
		r.recordItemCheck(clusterName, addonName, ItemOutcomeForeign, "")
		return ItemOutcomeForeign, nil
	}

	desiredData := make(map[string][]byte, len(w.push.Keys))
	for key, providerPath := range w.push.Keys {
		val, valErr := r.secretProvider.GetSecretValue(ctx, providerPath)
		if valErr != nil {
			checkErr := fmt.Errorf("fetching %q from the secrets provider: %w", providerPath, valErr)
			r.recordItemCheck(clusterName, addonName, ItemOutcomeError, checkErr.Error())
			return "", checkErr
		}
		desiredData[key] = val
	}

	outcome := ItemOutcomeUnchanged
	if hashSecretData(existing.Data) != hashSecretData(desiredData) {
		outcome = ItemOutcomeOutOfSync
	}
	r.recordItemCheck(clusterName, addonName, outcome, "")
	return outcome, nil
}

// SyncOne re-pushes a single addon-values secret (one cluster+addon pair) —
// S4's "Sync" row action. Drives the exact same write primitive the
// periodic pass uses (reconcileSecret), so a manual sync and a scheduled
// pass can never diverge on what "push this secret" means. Uses a
// throwaway *ReconcileStats scoped to this one call — it is never merged
// into r.lastStats, since that field reports the periodic pass's own
// counters, not a single on-demand action's. Fires the per-item audit
// callback on an actual write, exactly like the periodic pass does, so a
// manual Sync shows up in the audit log the same way an automatic repair
// does.
func (r *Reconciler) SyncOne(ctx context.Context, clusterName, addonName string) (string, error) {
	w, err := r.findWork(ctx, clusterName, addonName)
	if err != nil {
		return "", err
	}

	stats := &ReconcileStats{}
	outcome, syncErr := r.reconcileSecret(ctx, stats, w.clusterName, w.credLookup, w.addon, w.push)

	errMsg := ""
	if syncErr != nil {
		errMsg = syncErr.Error()
	}

	// Ownership gate (P1-A), defense in depth. reconcileSecret already
	// refused to write and came back "foreign" with no error; a caller who
	// got here anyway (an API call straight past the disabled button) is
	// told plainly why nothing happened. The RECORD stays error-free on
	// purpose — "somebody else owns this" is a boundary the row states in
	// its own status, not a failed check to report as one.
	refusedForeign := syncErr == nil && outcome == ItemOutcomeForeign

	key := ItemKey{Cluster: clusterName, Addon: addonName}
	r.mu.Lock()
	if r.itemRecords == nil {
		r.itemRecords = make(map[ItemKey]ItemRecord)
	}
	prev := r.itemRecords[key]
	rec := ItemRecord{LastChecked: time.Now(), Outcome: outcome, Error: errMsg, ChangedAt: prev.ChangedAt}
	if outcome == ItemOutcomeCreated || outcome == ItemOutcomeUpdated {
		rec.ChangedAt = rec.LastChecked
	}
	// L10 (code review): same carry-forward rule recordItemCheck now
	// applies to CheckOne/CheckAll — a real sync failure (syncErr != nil)
	// extends the streak the periodic pass would also be building on this
	// pair; any real outcome (created/updated/unchanged/foreign) resets it,
	// including the refused-foreign case, which is a boundary, not a
	// failure. Before this, every manual Sync on an already-failing row
	// reset its ConsecutiveFailures to 0, so a row a human kept retrying by
	// hand could never cross the fight-detection threshold the periodic
	// pass uses.
	if syncErr != nil {
		rec.ConsecutiveFailures = prev.ConsecutiveFailures + 1
	}
	r.itemRecords[key] = rec
	r.mu.Unlock()

	if refusedForeign {
		return "", ErrForeignSecret
	}
	if syncErr != nil {
		return "", syncErr
	}

	if r.itemAuditFn != nil && (outcome == ItemOutcomeCreated || outcome == ItemOutcomeUpdated) {
		r.itemAuditFn(clusterName, addonName, outcome)
	}

	return string(outcome), nil
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
