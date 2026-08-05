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

// LastError returns the first error message recorded during the most
// recent reconcile run, or "" when that run had no errors (including the
// "never run yet" case).
func (r *Reconciler) LastError() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.lastErrors) == 0 {
		return ""
	}
	return r.lastErrors[0]
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

		if err != nil {
			rec.Error = err.Error()
			stats.Errors++
			errs = append(errs, fmt.Sprintf("cluster=%s addon=%s secret=%s: %v",
				w.clusterName, w.addon, w.push.SecretName, err))
			log.Error("[secrets] reconcile failed",
				"cluster", w.clusterName,
				"addon", w.addon,
				"secret", w.push.SecretName,
				"error", err,
			)
		} else if r.itemAuditFn != nil && (outcome == ItemOutcomeCreated || outcome == ItemOutcomeUpdated) {
			r.itemAuditFn(w.clusterName, w.addon, outcome)
		}
		itemResults[key] = rec
	}

	r.mu.Lock()
	r.itemRecords = itemResults
	r.mu.Unlock()

	if len(plan.work) == 0 && len(errs) == 0 {
		log.Info("[secrets] no addons with secret definitions — nothing to reconcile", "layout", plan.layout)
		return
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
		if createErr := remoteclient.EnsureSecret(ctx, client, ref.Namespace, ref.SecretName, desiredData); createErr != nil {
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
	if updateErr := remoteclient.EnsureSecret(ctx, client, ref.Namespace, ref.SecretName, desiredData); updateErr != nil {
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
func (r *Reconciler) recordItemCheck(cluster, addon string, outcome ItemOutcome, errMsg string) {
	key := ItemKey{Cluster: cluster, Addon: addon}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.itemRecords == nil {
		r.itemRecords = make(map[ItemKey]ItemRecord)
	}
	prev := r.itemRecords[key]
	r.itemRecords[key] = ItemRecord{
		LastChecked: time.Now(),
		Outcome:     outcome,
		ChangedAt:   prev.ChangedAt,
		Error:       errMsg,
	}
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

	gr := r.gitReader()
	if gr == nil {
		return ErrNoGitConnection
	}
	plan, err := r.planPushes(ctx, gr)
	if err != nil {
		return fmt.Errorf("could not read the addon catalog or managed clusters list: %w", err)
	}

	checked, failed := 0, 0
	for _, w := range plan.work {
		if _, checkErr := r.checkWork(ctx, w); checkErr != nil {
			failed++
			log.Warn("[secrets] could not check this addon-values secret — carrying on with the rest",
				"cluster", w.clusterName, "addon", w.addon, "secret", w.push.SecretName, "error", checkErr)
			continue
		}
		checked++
	}
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
		return "", fmt.Errorf("getting credentials for cluster %q: %w", clusterName, err)
	}
	client, err := r.remoteClientFn(creds.Raw)
	if err != nil {
		return "", fmt.Errorf("connecting to cluster %q: %w", clusterName, err)
	}

	existing, err := client.CoreV1().Secrets(w.push.Namespace).Get(ctx, w.push.SecretName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			r.recordItemCheck(clusterName, addonName, ItemOutcomeMissing, "")
			return ItemOutcomeMissing, nil
		}
		return "", fmt.Errorf("checking the existing secret on cluster %q: %w", clusterName, err)
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
			return "", fmt.Errorf("fetching %q from the secrets provider: %w", providerPath, valErr)
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
