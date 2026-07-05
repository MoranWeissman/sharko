// Package clusterreconciler reconciles ArgoCD cluster Secret state from
// configuration/managed-clusters.yaml in git. It mirrors the
// internal/prtracker pattern: a single goroutine drives reconciliation, a
// 30s safety-net tick catches drift, and a non-blocking Trigger() channel
// provides low-latency post-merge convergence when prTracker observes a
// Sharko-opened PR being merged.
//
// pollOnce:
//
//  1. Reads managed-clusters.yaml from git via models.LoadManagedClusters
//     (envelope-aware + JSON-Schema-validated reader).
//  2. Lists ArgoCD cluster Secrets in the argocd namespace filtered by
//     app.kubernetes.io/managed-by=sharko (ownership label).
//  3. Computes a set diff (in-git ∖ in-argocd → create; in-argocd ∖ in-git
//     → delete; with-label-only on delete so foreign Secrets are never
//     touched — Adopt territory).
//  4. Per-cluster + per-secret error isolation: a vault failure on one
//     cluster does NOT block reconciliation of the others.
//
// Coexistence with the legacy argosecrets.Reconciler (V2-cleanup-28):
//
// In a standard in-cluster install BOTH this reconciler AND the legacy
// argosecrets.Reconciler run concurrently (see cmd/sharko/serve.go for the
// wiring). After V2-cleanup-28 their adoption-safety semantics are aligned:
//
//   - Orphan sweeps in both reconcilers skip secrets that carry the
//     sharko.sharko.dev/adopted annotation — those secrets are owned by the
//     Adopt flow and can only be removed via an explicit Unadopt call.
//   - internal/argosecrets.Manager.Ensure preserves the connection Data of
//     adopted secrets and only converges their labels.
//
// Single-writer consolidation (retiring the legacy argosecrets reconciler)
// is a tracked follow-up item.
//
// See:
//   - docs/design/2026-05-11-cluster-secret-reconciler-and-gitops-stance.md
//     §7 (Option E), §8 (pattern), §9 (two-direction policy), §10 (REST
//     git read; failure modes).
//   - internal/prtracker/tracker.go (lifecycle pattern this package mirrors).
//   - internal/argosecrets/manager.go (the Secret payload shape — execProvider
//     config — that the legacy reconciler writes; this reconciler writes the
//     same shape so ArgoCD's auth code path is unchanged across writers).
package clusterreconciler

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/cmstore"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/logging"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// syntheticTickID returns the canonical "recon-<unix_ts>" correlation ID
// for a single reconciler tick. The unix timestamp is captured at the tick
// boundary so every slog line emitted during the tick shares the same ID.
// V2-2.2.
func syntheticTickID() string {
	return fmt.Sprintf("recon-%d", time.Now().Unix())
}

// syntheticFanoutID returns the canonical "recon-fanout-<unix_ts>"
// correlation ID for the post-merge fanout triggered by prtracker observing
// a Sharko-opened PR being merged. Distinguishes a low-latency nudge from
// the routine 30s drift safety-net tick. V2-2.2.
func syntheticFanoutID() string {
	return fmt.Sprintf("recon-fanout-%d", time.Now().Unix())
}

const (
	// DefaultTickInterval is the reconciler's safety-net poll cadence.
	//
	// Low-latency post-merge convergence is driven by prTracker.SetOnMergeFn
	// calling Reconciler.Trigger() — see Story 8.4 wiring. The periodic tick
	// catches drift that did NOT originate from a Sharko-opened PR (e.g. a
	// human editing managed-clusters.yaml directly in the repo UI, or an
	// ArgoCD cluster Secret being mutated out-of-band).
	DefaultTickInterval = 30 * time.Second

	// DefaultManagedClustersPath is the in-repo location of the source-of-truth
	// managed clusters file. Overridable via Deps.ManagedClustersPath.
	DefaultManagedClustersPath = "configuration/managed-clusters.yaml"

	// DefaultArgoCDNamespace is the namespace the reconciler writes cluster
	// Secrets into. Overridable via Deps.Namespace.
	DefaultArgoCDNamespace = "argocd"

	// DefaultBranch is the git ref the reconciler reads managed-clusters.yaml
	// from when Deps.Branch is empty. Matches the design doc §10 default and
	// the existing secrets reconciler's "main" fallback.
	DefaultBranch = "main"
)

// ArgoClient is the kubernetes.Interface the reconciler uses to List, Get,
// Create, and Delete ArgoCD cluster Secrets in the argocd namespace.
//
// Story 8.1 widens A0's empty-interface placeholder to kubernetes.Interface so
// production wiring passes the same clientset that powers argosecrets.Manager
// + the rest of Sharko's K8s access, and tests use k8s.io/client-go/kubernetes/
// fake.NewSimpleClientset() without an adapter layer. A narrower Sharko-
// specific interface was considered (cleaner method surface) but would have
// required a fake adapter that re-implements list-by-selector semantics — the
// fake clientset already gets that exactly right, so the broader interface
// pays off in test simplicity.
type ArgoClient = kubernetes.Interface

// Vault is the providers.ClusterCredentialsProvider Sharko uses everywhere
// else to fetch per-cluster credentials (server URL, CA, bearer token /
// kubeconfig bytes). Widened from A0's empty-interface placeholder so the
// reconciler can call GetCredentials directly and tests can substitute the
// existing internal/demo.MockClusterCredentialsProvider (or a one-off mock).
type Vault = providers.ClusterCredentialsProvider

// Deps holds the reconciler's external dependencies. Constructor-injected so
// tests can substitute fakes (k8s.io/client-go/kubernetes/fake, gitprovider
// mocks, audit no-ops). Using a struct (rather than positional args) means
// Story 8.1's added fields are non-breaking for the caller in serve.go.
type Deps struct {
	// CMStore persists reconciler state across restarts. Required.
	CMStore *cmstore.Store

	// GitProvider is a lazy accessor returning the currently-active provider
	// (or nil when no connection is configured). Matches the prtracker idiom
	// — reconciler must tolerate "no provider yet" without panicking.
	GitProvider func() gitprovider.GitProvider

	// ArgoClient is the ArgoCD cluster-Secret API. Required at Start time;
	// nil-checked by pollOnce in Story 8.1. See ArgoClient interface above.
	ArgoClient ArgoClient

	// Vault provides per-cluster credentials at reconcile time. Required at
	// Start time; nil-checked by pollOnce in Story 8.1. See Vault interface.
	Vault Vault

	// AuditFn is called to emit audit events for each reconcile action
	// (create / update / delete of an ArgoCD cluster Secret). Must be
	// non-nil; the constructor does not enforce this — callers are expected
	// to wire audit.Log.Add or equivalent.
	AuditFn func(audit.Entry)

	// TickInterval overrides the periodic poll cadence. Zero (or negative)
	// means DefaultTickInterval (30s).
	TickInterval time.Duration

	// ManagedClustersPath overrides the in-repo source-of-truth file.
	// Empty string means DefaultManagedClustersPath.
	ManagedClustersPath string

	// Namespace is the K8s namespace the reconciler writes ArgoCD cluster
	// Secrets into. Empty string means DefaultArgoCDNamespace ("argocd").
	Namespace string

	// Branch is the git ref the reconciler reads managed-clusters.yaml from.
	// Empty string means DefaultBranch ("main").
	Branch string

	// DefaultRoleARN is the AWS IAM role ARN passed to argocd-k8s-auth via
	// --role-arn for clusters whose entry does NOT specify one. Empty means
	// "no --role-arn flag" (parity with argosecrets.Reconciler's behavior so
	// the Secret payload shape matches across both writers during the
	// transition window).
	DefaultRoleARN string

	// DisableConnectivityCheck opts out of the connectivity-check label
	// (sharko.dev/connectivity-check: enabled) that Sharko applies to
	// newly-created cluster Secrets for zero-addon clusters. When false
	// (the zero value, i.e. the default), the feature is ON. Set to true
	// to disable (wired from SHARKO_CONNECTIVITY_CHECK=false/0 in serve.go).
	DisableConnectivityCheck bool
}

// Reconciler is a background reconciler that converges ArgoCD cluster Secret
// state with managed-clusters.yaml. See package doc for behaviour.
type Reconciler struct {
	deps                Deps
	tickInterval        time.Duration
	managedClustersPath string
	namespace           string
	branch              string

	triggerCh chan struct{} // buffered(1) — Trigger() never blocks
	stopCh    chan struct{}

	startOnce sync.Once
	stopOnce  sync.Once

	// pollFn is the per-instance test seam invoked by run() on every tick
	// or trigger. Production initializes it to (*Reconciler).pollOnce in
	// New(); tests may swap it before calling Start to observe ticks /
	// triggers without depending on the stub pollOnce's slog output.
	//
	// Per-instance (rather than a package-level var) so parallel tests do
	// not race on a shared seam.
	pollFn func(context.Context)

	// nowFn is the per-instance clock seam used when evaluating the
	// registration-pending grace window (V2-cleanup-11.1). Production
	// initializes it to time.Now in New(); tests override it to drive a
	// within-window / expired Secret deterministically. Per-instance (not a
	// package-level var) so parallel tests do not race on a shared seam.
	nowFn func() time.Time
}

// New constructs a Reconciler with the given dependencies. It does NOT start
// the background goroutine — call Start(ctx) for that. The two-step
// New/Start split mirrors prtracker.NewTracker / (*Tracker).Start so wiring
// in cmd/sharko/serve.go is symmetric.
//
// Defaults applied:
//   - deps.TickInterval <= 0 → DefaultTickInterval (30s)
//   - deps.ManagedClustersPath == "" → DefaultManagedClustersPath
func New(deps Deps) *Reconciler {
	tick := deps.TickInterval
	if tick <= 0 {
		tick = DefaultTickInterval
	}
	path := deps.ManagedClustersPath
	if path == "" {
		path = DefaultManagedClustersPath
	}
	ns := deps.Namespace
	if ns == "" {
		ns = DefaultArgoCDNamespace
	}
	branch := deps.Branch
	if branch == "" {
		branch = DefaultBranch
	}
	r := &Reconciler{
		deps:                deps,
		tickInterval:        tick,
		managedClustersPath: path,
		namespace:           ns,
		branch:              branch,
		triggerCh:           make(chan struct{}, 1),
		stopCh:              make(chan struct{}),
		nowFn:               time.Now,
	}
	r.pollFn = r.pollOnce
	return r
}

// Start launches the reconcile goroutine. Calling Start more than once is a
// no-op (sync.Once). Cancel via Stop() or by cancelling ctx.
//
// Unlike prtracker.Start, this scaffold does NOT run an immediate
// reconcile before entering the loop — Story 8.1 will revisit that
// decision when the real pollOnce lands (it may want a startup reconcile
// to recover from drift accumulated while the pod was down).
func (r *Reconciler) Start(ctx context.Context) {
	r.startOnce.Do(func() {
		go r.run(ctx)
	})
}

// Stop signals the reconcile goroutine to exit. Idempotent — calling Stop
// multiple times is safe and never causes a close-of-closed-channel panic.
func (r *Reconciler) Stop() {
	r.stopOnce.Do(func() {
		close(r.stopCh)
	})
}

// Trigger requests an immediate reconcile on top of the periodic tick.
// Used by prTracker.SetOnMergeFn for low-latency post-merge convergence
// (Story 8.4 wiring). Never blocks: if a trigger is already queued, the
// call is a no-op (the pending tick will cover it).
//
// Safe to call before Start. The buffered channel will hold the request
// until the goroutine starts and drains it.
func (r *Reconciler) Trigger() {
	select {
	case r.triggerCh <- struct{}{}:
	default:
	}
}

// run is the reconcile loop. Internal — the only entry point is Start.
//
// Each tick (whether ticker-driven or trigger-driven) gets a fresh synthetic
// correlation ID attached to the per-tick context. Ticker ticks use
// `recon-<unix_ts>`, trigger ticks use `recon-fanout-<unix_ts>` so operators
// can distinguish the routine drift safety-net from the low-latency post-
// merge nudge in log queries. V2-2.2.
func (r *Reconciler) run(ctx context.Context) {
	ticker := time.NewTicker(r.tickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.pollFn(logging.WithRequestID(ctx, syntheticTickID()))
		case <-r.triggerCh:
			r.pollFn(logging.WithRequestID(ctx, syntheticFanoutID()))
		}
	}
}

// reconcileStats holds the per-tick counters used in the summary audit
// entry. Intentionally minimal — the design doc §10 calls for an
// operational signal ("what happened this tick"), not a full metrics
// surface. Counters that need to feed Prometheus / Grafana can ride on a
// future MetricsFn dep without changing this struct.
type reconcileStats struct {
	Created          int
	Deleted          int
	SkippedUnlabeled int // existing same-name unlabeled Secret — Adopt territory
	SkippedPending   int // orphan candidate skipped — registration-pending within grace (V2-cleanup-11.1)
	SkippedAdopted   int // orphan candidate skipped — adopted annotation present (V2-cleanup-28)
	ClearedPending   int // pending annotation stripped — cluster became managed (V2-cleanup-11.1)
	UserLabelSynced  int // self-managed connection — addon labels merged onto the user's Secret (V2-cleanup-57.2)
	UserPending      int // self-managed connection — user's Secret not created yet; visible wait, no write (V2-cleanup-57.2)
	Errors           int
}

// pollOnce is the reconcile body — one tick of the git → ArgoCD diff
// + act loop. Invoked via r.pollFn (per-instance test seam set up in
// New) so tests can observe tick / trigger events without depending on
// the production audit + slog output of this function.
//
// Failure isolation contract (design doc §10):
//   - A git fetch error aborts the tick BEFORE any K8s mutation, logs +
//     audits the failure, and leaves all live state intact.
//   - A schema-validation error from models.LoadManagedClusters has the
//     same shape — the reader already audits the violation list
//     via slog; we add a single audit.Entry so the operator sees the
//     rejection in /api/v1/audit alongside the slog spam.
//   - A vault error on cluster X logs + audits + CONTINUES to cluster X+1.
//   - A K8s create / delete error on Secret Y logs + audits + CONTINUES to
//     the next item in the same set.
//
// The final summary entry fires unconditionally so an operator can tell
// at a glance whether the tick was a no-op or did meaningful work.
func (r *Reconciler) pollOnce(ctx context.Context) {
	log := logging.LoggerFromContext(ctx)
	stats := reconcileStats{}

	// Step 0: dependency precondition. The reconciler is wired into the
	// process at Start time but the git provider is resolved lazily (no
	// active connection on first boot is normal — wait for the operator
	// to configure one). This branch matches secrets.Reconciler's idiom.
	if r.deps.GitProvider == nil {
		log.Warn("[clusterreconciler] no GitProvider getter configured, skipping reconcile")
		return
	}
	gp := r.deps.GitProvider()
	if gp == nil {
		log.Debug("[clusterreconciler] no active git provider, skipping reconcile",
			"managed_clusters_path", r.managedClustersPath,
		)
		return
	}
	if r.deps.ArgoClient == nil {
		log.Warn("[clusterreconciler] no ArgoClient (k8s clientset) configured, skipping reconcile")
		return
	}
	if r.deps.Vault == nil {
		log.Warn("[clusterreconciler] no Vault (cluster-credentials provider) configured, skipping reconcile")
		return
	}

	// Step 1: read managed-clusters.yaml from git.
	body, err := gp.GetFileContent(ctx, r.managedClustersPath, r.branch)
	if err != nil {
		// ErrFileNotFound is not exceptional — a freshly-bootstrapped repo
		// has zero clusters in managed-clusters.yaml until the first
		// register-cluster PR merges. Treat it as "empty desired state"
		// rather than an error: the diff against argocd will compute
		// in-argocd ∖ in-git correctly (no creates, only the deletes that
		// would have happened anyway). Without this carve-out a fresh repo
		// would log noise on every tick and a sharko-labeled Secret
		// orphaned in argocd would never be cleaned up.
		if errors.Is(err, gitprovider.ErrFileNotFound) {
			log.Info("[clusterreconciler] managed-clusters.yaml not in git — treating as empty desired state",
				"path", r.managedClustersPath, "branch", r.branch,
			)
			// fall through with body == nil; LoadManagedClusters([]byte{})
			// would still error, so short-circuit to empty spec instead.
			// fileNonEmpty=false: a missing file is a legitimate fresh
			// install / pre-first-registration state, so the orphan-sweep
			// sanity guard must NOT hold the sweep (V2-cleanup-60.2).
			r.reconcileDiff(ctx, nil, false, &stats)
			r.emitSummaryAudit(ctx, stats)
			return
		}
		log.Error("[clusterreconciler] git read failed — aborting tick (no state mutated)",
			"path", r.managedClustersPath, "branch", r.branch, "error", err,
		)
		r.audit(audit.Entry{
			Level:     "error",
			Event:     "cluster_secret_reconcile",
			User:      "sharko",
			Action:    "git_read",
			Resource:  fmt.Sprintf("file:%s ref:%s", r.managedClustersPath, r.branch),
			Source:    "reconciler",
			Result:    "failure",
			Error:     err.Error(),
			RequestID: logging.RequestID(ctx),
		})
		return
	}

	// Step 2: parse + schema-validate.
	spec, err := models.LoadManagedClusters(body)
	if err != nil {
		// schema.LogValidationFailure already fired slog.Error with the
		// full violation list inside LoadManagedClusters; mirror it onto
		// the audit log so the rejection is visible alongside other
		// reconciler events.
		log.Error("[clusterreconciler] managed-clusters.yaml rejected — aborting tick (no state mutated)",
			"path", r.managedClustersPath, "error", err,
		)
		r.audit(audit.Entry{
			Level:     "error",
			Event:     "cluster_secret_reconcile",
			User:      "sharko",
			Action:    "schema_validation",
			Resource:  fmt.Sprintf("file:%s", r.managedClustersPath),
			Source:    "reconciler",
			Result:    "failure",
			Error:     err.Error(),
			RequestID: logging.RequestID(ctx),
		})
		return
	}

	// fileNonEmpty feeds the orphan-sweep sanity guard (V2-cleanup-60.2):
	// a file that EXISTS with content but parses to zero clusters is the
	// signature of a version/format mismatch, not of an intentionally
	// emptied fleet — see orphanSweepHeld.
	r.reconcileDiff(ctx, &spec, len(bytes.TrimSpace(body)) > 0, &stats)
	r.emitSummaryAudit(ctx, stats)
}

// orphanSweepHeld is the orphan-sweep sanity guard (H2 forward guard,
// V2-cleanup-60.2). It reports whether the reconciler must WITHHOLD every
// orphan deletion this tick because the desired state looks like a silent
// misread rather than a real "delete everything" instruction:
//
//   - desiredCount == 0    — the parsed managed-clusters.yaml declares zero
//     clusters, and
//   - fileNonEmpty         — yet the file EXISTS in git with content (a
//     missing file or a genuinely empty body is a
//     normal fresh-install state and never holds), and
//   - observedManaged >= 1 — and at least one sharko-labeled ArgoCD cluster
//     Secret is live.
//
// All three together mean "git suddenly says nothing while the live fleet
// says something" — historically caused by a binary silently parsing a
// newer-format file as empty (the v2.1.x-reads-sharko.dev/v1 incident).
// Deleting every managed Secret on that signal is unrecoverable for
// inline-registered clusters, so the guard fails safe: skip the sweep for
// this tick, scream (Error log + orphan_sweep_held audit event), and let
// the operator resolve the mismatch. An operator who really wants a
// zero-cluster fleet removes the clusters through Sharko (which deletes
// their Secrets as part of removal) or deletes the leftover Secrets by
// hand — both make observedManaged reach zero and the guard disarm.
func orphanSweepHeld(desiredCount int, fileNonEmpty bool, observedManaged int) bool {
	return desiredCount == 0 && fileNonEmpty && observedManaged > 0
}

// reconcileDiff drives the create / delete decisions from the parsed
// desired state and the live ArgoCD secret list. Extracted so the
// "empty managed-clusters.yaml" branch in pollOnce can share the logic.
// A nil spec is treated as "no clusters desired".
//
// fileNonEmpty reports whether managed-clusters.yaml existed in git with
// non-whitespace content — one of the three inputs to the orphan-sweep
// sanity guard (orphanSweepHeld, V2-cleanup-60.2).
func (r *Reconciler) reconcileDiff(ctx context.Context, spec *models.ManagedClustersSpec, fileNonEmpty bool, stats *reconcileStats) {
	// Build the desired set (in-git names).
	desired := make(map[string]models.ManagedClusterEntry)
	if spec != nil {
		for _, c := range spec.Clusters {
			if c.Name == "" {
				// Schema validation should have caught this; defensive
				// skip so a stray "" entry doesn't try to create a Secret
				// with an empty name.
				continue
			}
			desired[c.Name] = c
		}
	}

	// List the actual set (in-argocd, sharko-labeled only).
	existing, err := r.listManagedSecrets(ctx)
	if err != nil {
		logging.LoggerFromContext(ctx).Error("[clusterreconciler] listing managed cluster Secrets failed — aborting tick",
			"namespace", r.namespace, "error", err,
		)
		r.audit(audit.Entry{
			Level:     "error",
			Event:     "cluster_secret_reconcile",
			User:      "sharko",
			Action:    "list_secrets",
			Resource:  fmt.Sprintf("namespace:%s", r.namespace),
			Source:    "reconciler",
			Result:    "failure",
			Error:     err.Error(),
			RequestID: logging.RequestID(ctx),
		})
		return
	}

	// Compute set diffs in O(n+m) via map lookups.
	//
	// Self-managed connections (connectionManagedBy: user — V2-cleanup-57.2)
	// are partitioned OUT of the create set: Sharko never creates (or
	// rotates, or deletes) their ArgoCD cluster Secret. Instead each gets a
	// label-only sync onto the user-created Secret every tick — see
	// syncSelfManaged. They stay in `desired` so the orphan sweep below can
	// never treat their Secret as an orphan while the cluster is in git.
	toCreate := make([]models.ManagedClusterEntry, 0, len(desired))
	selfManaged := make([]models.ManagedClusterEntry, 0)
	for name, entry := range desired {
		if entry.UserManagedConnection() {
			selfManaged = append(selfManaged, entry)
			continue
		}
		if _, present := existing[name]; !present {
			toCreate = append(toCreate, entry)
		}
	}

	now := r.now()
	toDelete := make([]string, 0)
	for name := range existing {
		if _, present := desired[name]; present {
			continue // in both sets — handled by the clear-pending pass below.
		}
		// in-argocd ∖ in-git → orphan candidate.

		secret := existing[name]

		// V2-cleanup-28: adopted secrets are delete-proof from the automatic
		// sweep. They can only be removed via an explicit Unadopt call.
		// The full corev1.Secret is already cached in existing[name] — no
		// extra Get required.
		// argosecrets.IsAdopted recognises the pre-rename legacy annotation
		// key too — a cluster adopted before V2-cleanup-59 must stay
		// delete-proof (this is the annotation that protects it).
		if argosecrets.IsAdopted(annotationsOf(secret)) {
			stats.SkippedAdopted++
			logging.LoggerFromContext(ctx).Warn(
				"[clusterreconciler] skipping orphan delete — Secret has adopted annotation; remove via Unadopt",
				"cluster", name, "namespace", r.namespace,
			)
			r.audit(audit.Entry{
				Level:     "warn",
				Event:     "cluster_secret_skip_adopted",
				User:      "sharko",
				Action:    "skip",
				Resource:  fmt.Sprintf("cluster:%s", name),
				Source:    "reconciler",
				Result:    "partial",
				Detail:    "adopted secret not in git — left in place; remove via Unadopt",
				RequestID: logging.RequestID(ctx),
			})
			continue
		}

		// BUT: a Secret that was direct-written during registration Stage 1
		// carries the registration-pending annotation and is NOT yet in git
		// because its registration PR has not merged. Skip it while the grace
		// window is open so the orphan sweep does not race the PR merge
		// (V2-cleanup-11.1). Once the window expires (PR never merged) it
		// falls through and is reaped — no permanent leak.
		pending, malformed := models.IsRegistrationPending(annotationsOf(secret), now)
		if malformed {
			logging.LoggerFromContext(ctx).Warn(
				"[clusterreconciler] registration-pending annotation is unparseable — treating Secret as a normal orphan candidate",
				"cluster", name, "namespace", r.namespace,
				"annotation", models.AnnotationRegistrationPending,
				"value", func() string {
					v, _ := models.RegistrationPendingValue(annotationsOf(secret))
					return v
				}(),
			)
		}
		if pending {
			stats.SkippedPending++
			logging.LoggerFromContext(ctx).Info(
				"[clusterreconciler] skipping orphan delete — Secret is registration-pending within grace window",
				"cluster", name, "namespace", r.namespace,
			)
			continue
		}
		toDelete = append(toDelete, name)
	}

	// Create missing Secrets (in-git ∖ in-argocd).
	for _, entry := range toCreate {
		r.createOne(ctx, entry, stats)
	}

	// Orphan-sweep sanity guard (H2 forward guard, V2-cleanup-60.2): when
	// the desired state reads as ZERO clusters even though the file exists
	// non-empty in git AND sharko-labeled Secrets are live, the most likely
	// explanation is a silent misread (version/format mismatch), not a real
	// fleet-wide removal. Withhold every deletion this tick and scream. The
	// per-candidate classification above (adopted / pending skips) already
	// ran, so those operator signals still fire; only the destructive step
	// is held. Fresh installs (file missing, or genuinely empty body) never
	// trip this — fileNonEmpty is false there.
	if len(toDelete) > 0 && orphanSweepHeld(len(desired), fileNonEmpty, len(existing)) {
		stats.Errors++
		logging.LoggerFromContext(ctx).Error(
			"[clusterreconciler] orphan sweep HELD — managed-clusters.yaml is non-empty but parsed to zero clusters while sharko-labeled cluster Secrets exist; refusing to delete anything this tick. Check for a Sharko version/format mismatch on this repo (see operator guide: upgrade & rollback safety)",
			"namespace", r.namespace,
			"path", r.managedClustersPath,
			"branch", r.branch,
			"held_deletions", len(toDelete),
			"observed_managed_secrets", len(existing),
		)
		r.audit(audit.Entry{
			Level:     "error",
			Event:     "orphan_sweep_held",
			User:      "sharko",
			Action:    "hold_orphan_sweep",
			Resource:  fmt.Sprintf("namespace:%s held_deletions:%d observed_managed:%d", r.namespace, len(toDelete), len(existing)),
			Source:    "reconciler",
			Result:    "partial",
			Detail:    "desired state parsed to zero clusters while the managed-clusters file exists non-empty and sharko-labeled cluster Secrets are live — orphan sweep withheld this tick; investigate a version/format mismatch before any Secret is deleted",
			RequestID: logging.RequestID(ctx),
		})
		toDelete = nil
	}

	// Delete orphan Secrets (in-argocd ∖ in-git). Pass the cached corev1.Secret
	// values so we can re-verify the ownership label as a defensive last check.
	for _, name := range toDelete {
		secret := existing[name]
		r.deleteOne(ctx, name, secret, stats)
	}

	// Label-only sync for self-managed connections (V2-cleanup-57.2). Runs
	// every tick so addon toggles converge onto the user's Secret with the
	// same latency Sharko-managed clusters get.
	for _, entry := range selfManaged {
		r.syncSelfManaged(ctx, entry, stats)
	}

	// Convert pending → managed: any Secret that is now in BOTH git and
	// argocd AND still carries the registration-pending annotation has had
	// its registration PR merged — strip the annotation so it becomes a
	// normal managed Secret (and is no longer immune to a future orphan
	// sweep). Idempotent: Secrets without the annotation are untouched.
	//
	// Self-managed entries are EXCLUDED: their Secret is the user's (the
	// registration flow never direct-writes one for them), and this pass
	// updates from the pre-sync cached object — running it after
	// syncSelfManaged could clobber the label handover with stale metadata.
	for name := range desired {
		if desired[name].UserManagedConnection() {
			continue
		}
		secret, present := existing[name]
		if !present {
			continue // will be created above; nothing to clear.
		}
		if _, has := models.RegistrationPendingValue(annotationsOf(secret)); has {
			r.clearRegistrationPending(ctx, name, secret, stats)
		}
	}
}

// syncSelfManaged converges ONLY the addon labels onto the user-created
// ArgoCD cluster Secret of a self-managed connection (connectionManagedBy:
// user — V2-cleanup-57.2). Delegates to argosecrets.Manager.SyncLabelsOnly
// so both reconcilers share one label-only write primitive:
//
//   - Data / StringData / annotations are never touched (the connection
//     credentials are the user's, verbatim).
//   - No managed-by ownership label, no secret-type label, no
//     connectivity-check label is ever stamped.
//   - A leftover managed-by=sharko label from a Sharko-managed past is
//     stripped (non-adopted Secrets only) so the orphan sweep can never
//     reclaim the user's connection.
//   - Secret missing → a VISIBLE pending state (Info log + audit entry +
//     UserPending counter), not an error loop. The user creates the Secret
//     per the operator guide; the next tick picks it up.
//
// Per-cluster error isolation matches createOne: failures log + audit +
// count, and the next cluster still gets its turn.
func (r *Reconciler) syncSelfManaged(ctx context.Context, entry models.ManagedClusterEntry, stats *reconcileStats) {
	log := logging.LoggerFromContext(ctx)

	// Addon labels in the canonical vocabulary, self-healing legacy
	// "true"/"false" values exactly like the create path does — the label
	// payload must be identical no matter who owns the connection.
	clusterLabels := normalizeLabels(entry.Labels)
	for k, v := range clusterLabels {
		if normalized, changed := models.NormalizeAddonLabelValue(v); changed {
			clusterLabels[k] = normalized
		}
	}
	// NOTE: no ApplyConnectivityCheckLabel here — the check label is never
	// stamped on a connection Sharko does not own (guest stance, same as
	// adopted clusters). SyncLabelsOnly strips it defensively as well.

	mgr := argosecrets.NewManager(r.deps.ArgoClient, r.namespace)
	changed, found, err := mgr.SyncLabelsOnly(ctx, entry.Name, clusterLabels)
	if err != nil {
		stats.Errors++
		log.Error("[clusterreconciler] self-managed label sync failed — continuing to next cluster",
			"cluster", entry.Name, "namespace", r.namespace, "error", err,
		)
		r.audit(audit.Entry{
			Level:     "error",
			Event:     "cluster_secret_user_label_sync",
			User:      "sharko",
			Action:    "sync_labels",
			Resource:  fmt.Sprintf("cluster:%s", entry.Name),
			Source:    "reconciler",
			Result:    "failure",
			Error:     err.Error(),
			RequestID: logging.RequestID(ctx),
		})
		return
	}
	if !found {
		stats.UserPending++
		log.Info("[clusterreconciler] self-managed connection: ArgoCD cluster Secret not created yet — waiting for the user (no write attempted)",
			"cluster", entry.Name, "namespace", r.namespace,
		)
		r.audit(audit.Entry{
			Level:     "info",
			Event:     "cluster_secret_user_pending",
			User:      "sharko",
			Action:    "wait_user_secret",
			Resource:  fmt.Sprintf("cluster:%s", entry.Name),
			Source:    "reconciler",
			Result:    "partial",
			Detail:    "connection is managed by the user; create the ArgoCD cluster Secret by hand (see operator guide: self-managed connections)",
			RequestID: logging.RequestID(ctx),
		})
		return
	}
	if changed {
		stats.UserLabelSynced++
		log.Info("[clusterreconciler] self-managed connection: addon labels synced — connection data untouched",
			"cluster", entry.Name, "namespace", r.namespace,
		)
		r.audit(audit.Entry{
			Level:     "info",
			Event:     "cluster_secret_user_label_sync",
			User:      "sharko",
			Action:    "sync_labels",
			Resource:  fmt.Sprintf("cluster:%s", entry.Name),
			Source:    "reconciler",
			Result:    "success",
			RequestID: logging.RequestID(ctx),
		})
	}
}

// now returns the current time via the per-instance clock seam. Defaulted to
// time.Now in New(); overridable in tests for deterministic grace-window
// evaluation.
func (r *Reconciler) now() time.Time {
	if r.nowFn == nil {
		return time.Now()
	}
	return r.nowFn()
}

// annotationsOf is a nil-safe accessor for a Secret's annotations.
func annotationsOf(secret *corev1.Secret) map[string]string {
	if secret == nil {
		return nil
	}
	return secret.Annotations
}

// clearRegistrationPending removes the registration-pending annotation from a
// now-managed cluster Secret. Best-effort + isolated: a failure is logged +
// audited but does not abort the tick. Re-applies the ownership label
// defensively (the annotation strip must never drop the managed-by label).
func (r *Reconciler) clearRegistrationPending(ctx context.Context, name string, cached *corev1.Secret, stats *reconcileStats) {
	log := logging.LoggerFromContext(ctx)
	updated := cached.DeepCopy()
	delete(updated.Annotations, models.AnnotationRegistrationPending)
	// A registration that was in flight across the V2-cleanup-59 upgrade
	// carries the legacy key — clear it too so the Secret cannot stay
	// sweep-immune under the old spelling.
	delete(updated.Annotations, models.AnnotationRegistrationPendingLegacy)
	// Strip the K8s-managed Data/StringData mismatch concern: we only mutate
	// metadata here, so keep Data as-is (DeepCopy preserved it).
	ApplyManagedBySharkoLabel(updated)
	if _, err := r.deps.ArgoClient.CoreV1().Secrets(r.namespace).Update(ctx, updated, metav1.UpdateOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("[clusterreconciler] registration-pending Secret already gone before annotation clear — nothing to do",
				"cluster", name, "namespace", r.namespace,
			)
			return
		}
		stats.Errors++
		log.Error("[clusterreconciler] clearing registration-pending annotation failed — continuing",
			"cluster", name, "namespace", r.namespace, "error", err,
		)
		r.audit(audit.Entry{
			Level:     "error",
			Event:     "cluster_secret_clear_pending",
			User:      "sharko",
			Action:    "clear_registration_pending",
			Resource:  fmt.Sprintf("cluster:%s", name),
			Source:    "reconciler",
			Result:    "failure",
			Error:     err.Error(),
			RequestID: logging.RequestID(ctx),
		})
		return
	}
	stats.ClearedPending++
	log.Info("[clusterreconciler] registration-pending annotation cleared — cluster is now managed",
		"cluster", name, "namespace", r.namespace,
	)
	r.audit(audit.Entry{
		Level:     "info",
		Event:     "cluster_secret_clear_pending",
		User:      "sharko",
		Action:    "clear_registration_pending",
		Resource:  fmt.Sprintf("cluster:%s", name),
		Source:    "reconciler",
		Result:    "success",
		RequestID: logging.RequestID(ctx),
	})
}

// listManagedSecrets fetches all sharko-labeled cluster Secrets from the
// argocd namespace as a name→Secret map. Filtered by
// app.kubernetes.io/managed-by=sharko so externally-owned Secrets are
// invisible to the reconciler — the cornerstone of the ownership model
// (design doc §9: "without sharko label → never touched").
func (r *Reconciler) listManagedSecrets(ctx context.Context) (map[string]*corev1.Secret, error) {
	list, err := r.deps.ArgoClient.CoreV1().Secrets(r.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: LabelManagedBy + "=" + LabelValueSharko,
	})
	if err != nil {
		return nil, fmt.Errorf("listing secrets in namespace %q: %w", r.namespace, err)
	}
	out := make(map[string]*corev1.Secret, len(list.Items))
	for i := range list.Items {
		s := &list.Items[i]
		out[s.Name] = s
	}
	return out, nil
}

// createOne builds and writes a single ArgoCD cluster Secret. Per-cluster
// errors (vault fetch, K8s create) are logged + audited but NEVER bubble
// up — the next cluster in the toCreate list still gets its turn.
//
// The "skip if same-name unlabeled Secret exists" branch implements
// design doc §9: an unlabeled Secret is Adopt territory; this
// reconciler must not silently overwrite it.
func (r *Reconciler) createOne(ctx context.Context, entry models.ManagedClusterEntry, stats *reconcileStats) {
	log := logging.LoggerFromContext(ctx)
	// Defensive: a same-name Secret may already exist without our label
	// (operator-created, or adopted-by-another-tool). The list step
	// filtered those out, so we re-check via Get before we Create.
	existing, getErr := r.deps.ArgoClient.CoreV1().Secrets(r.namespace).Get(ctx, entry.Name, metav1.GetOptions{})
	if getErr != nil && !apierrors.IsNotFound(getErr) {
		stats.Errors++
		log.Error("[clusterreconciler] pre-create Get failed — skipping cluster",
			"cluster", entry.Name, "namespace", r.namespace, "error", getErr,
		)
		r.audit(audit.Entry{
			Level:     "error",
			Event:     "cluster_secret_create",
			User:      "sharko",
			Action:    "get_secret",
			Resource:  fmt.Sprintf("cluster:%s", entry.Name),
			Source:    "reconciler",
			Result:    "failure",
			Error:     getErr.Error(),
			RequestID: logging.RequestID(ctx),
		})
		return
	}
	if getErr == nil && !IsManagedBySharko(existing) {
		// Adopt territory — do not touch.
		stats.SkippedUnlabeled++
		log.Info("[clusterreconciler] same-name Secret exists without sharko label — skipping (Adopt territory)",
			"cluster", entry.Name, "namespace", r.namespace,
		)
		r.audit(audit.Entry{
			Level:     "warn",
			Event:     "cluster_secret_skip_unlabeled",
			User:      "sharko",
			Action:    "skip",
			Resource:  fmt.Sprintf("cluster:%s", entry.Name),
			Source:    "reconciler",
			Result:    "partial",
			Detail:    "unlabeled Secret exists in argocd namespace; defer to Adopt flow",
			RequestID: logging.RequestID(ctx),
		})
		return
	}

	// Resolve credentials. SecretPath overrides Name for the vault lookup
	// (matches argosecrets.Reconciler — same contract so a single repo's
	// managed-clusters.yaml works across both writers during the
	// transition window; shared resolver — V2-cleanup-55.1).
	credKey := entry.CredentialLookupKey()
	creds, vaultErr := r.deps.Vault.GetCredentials(credKey)
	if vaultErr != nil {
		stats.Errors++
		log.Error("[clusterreconciler] vault GetCredentials failed — skipping cluster (others still reconcile)",
			"cluster", entry.Name, "cred_key", credKey, "error", vaultErr,
		)
		r.audit(audit.Entry{
			Level:     "error",
			Event:     "cluster_secret_create",
			User:      "sharko",
			Action:    "get_credentials",
			Resource:  fmt.Sprintf("cluster:%s", entry.Name),
			Source:    "reconciler",
			Result:    "failure",
			Error:     vaultErr.Error(),
			RequestID: logging.RequestID(ctx),
		})
		return
	}

	// Build the Secret. We reuse argosecrets.ClusterSecretSpec + the
	// package's payload builders so the Secret shape is byte-identical
	// to what argosecrets.Manager.Ensure writes — ArgoCD's auth code
	// path is unchanged regardless of which writer mutated the Secret.
	clusterLabels := normalizeLabels(entry.Labels)
	// Self-heal legacy addon labels: clusters registered before V2-cleanup-20
	// carry "true"/"false" addon labels in managed-clusters.yaml, which the
	// ArgoCD ApplicationSet selector reads as NOT-enabled — so their addons
	// never deploy. Upgrade those values to the canonical "enabled"/"disabled"
	// on this write so an already-registered cluster converges with no manual
	// re-register. Values that are already canonical (or non-addon labels) are
	// left untouched. (V2-cleanup-20, decision #4.)
	for k, v := range clusterLabels {
		if normalized, changed := models.NormalizeAddonLabelValue(v); changed {
			clusterLabels[k] = normalized
		}
	}
	// Apply the connectivity-check label (V2-cleanup-29). The label is DERIVED
	// here — never stored in managed-clusters.yaml — so no schema regen needed.
	// DisableConnectivityCheck is the zero-value-safe inverted sentinel: false
	// (zero value = default) means "feature on"; true means "feature off".
	models.ApplyConnectivityCheckLabel(clusterLabels, !r.deps.DisableConnectivityCheck)
	spec := argosecrets.ClusterSecretSpec{
		Name:    entry.Name,
		Server:  creds.Server,
		Region:  entry.Region,
		RoleARN: r.deps.DefaultRoleARN,
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

	secret, buildErr := buildClusterSecret(spec, r.namespace)
	if buildErr != nil {
		stats.Errors++
		log.Error("[clusterreconciler] building Secret payload failed — skipping cluster",
			"cluster", entry.Name, "error", buildErr,
		)
		r.audit(audit.Entry{
			Level:     "error",
			Event:     "cluster_secret_create",
			User:      "sharko",
			Action:    "build_payload",
			Resource:  fmt.Sprintf("cluster:%s", entry.Name),
			Source:    "reconciler",
			Result:    "failure",
			Error:     buildErr.Error(),
			RequestID: logging.RequestID(ctx),
		})
		return
	}

	// Defense-in-depth: re-apply the label even though buildClusterSecret
	// already set it. Cheap, idempotent, and lock-in for the invariant
	// "every Secret this reconciler writes carries the sharko label".
	ApplyManagedBySharkoLabel(secret)

	if _, createErr := r.deps.ArgoClient.CoreV1().Secrets(r.namespace).Create(ctx, secret, metav1.CreateOptions{}); createErr != nil {
		stats.Errors++
		log.Error("[clusterreconciler] Secret Create failed — skipping cluster",
			"cluster", entry.Name, "namespace", r.namespace, "error", createErr,
		)
		r.audit(audit.Entry{
			Level:     "error",
			Event:     "cluster_secret_create",
			User:      "sharko",
			Action:    "create",
			Resource:  fmt.Sprintf("cluster:%s", entry.Name),
			Source:    "reconciler",
			Result:    "failure",
			Error:     createErr.Error(),
			RequestID: logging.RequestID(ctx),
		})
		return
	}

	stats.Created++
	log.Info("[clusterreconciler] cluster Secret created",
		"cluster", entry.Name, "namespace", r.namespace, "server", creds.Server,
	)
	r.audit(audit.Entry{
		Level:     "info",
		Event:     "cluster_secret_create",
		User:      "sharko",
		Action:    "create",
		Resource:  fmt.Sprintf("cluster:%s", entry.Name),
		Source:    "reconciler",
		Result:    "success",
		RequestID: logging.RequestID(ctx),
	})
}

// deleteOne removes a single orphan ArgoCD cluster Secret. The list step
// already filtered by the sharko label, but we re-verify defensively in
// case of a label race (operator stripped the label between list and
// delete) — paranoia is cheap and the design doc §9 invariant is
// "Sharko never touches what it doesn't own."
func (r *Reconciler) deleteOne(ctx context.Context, name string, cached *corev1.Secret, stats *reconcileStats) {
	log := logging.LoggerFromContext(ctx)
	if !IsManagedBySharko(cached) {
		// Should be impossible (LabelSelector pre-filtered), but the
		// invariant is too important to trust the pre-filter. Skip + log.
		log.Warn("[clusterreconciler] cached secret missing sharko label between list and delete — skipping (invariant guard)",
			"cluster", name, "namespace", r.namespace,
		)
		return
	}

	if err := r.deps.ArgoClient.CoreV1().Secrets(r.namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			// Already gone (concurrent delete by an operator). Idempotent.
			log.Info("[clusterreconciler] orphan Secret already deleted",
				"cluster", name, "namespace", r.namespace,
			)
			return
		}
		stats.Errors++
		log.Error("[clusterreconciler] orphan Secret delete failed — continuing to next",
			"cluster", name, "namespace", r.namespace, "error", err,
		)
		r.audit(audit.Entry{
			Level:     "error",
			Event:     "cluster_secret_delete",
			User:      "sharko",
			Action:    "delete",
			Resource:  fmt.Sprintf("cluster:%s", name),
			Source:    "reconciler",
			Result:    "failure",
			Error:     err.Error(),
			RequestID: logging.RequestID(ctx),
		})
		return
	}

	stats.Deleted++
	log.Info("[clusterreconciler] orphan Secret deleted",
		"cluster", name, "namespace", r.namespace,
	)
	r.audit(audit.Entry{
		Level:     "info",
		Event:     "cluster_secret_delete",
		User:      "sharko",
		Action:    "delete",
		Resource:  fmt.Sprintf("cluster:%s", name),
		Source:    "reconciler",
		Result:    "success",
		RequestID: logging.RequestID(ctx),
	})
}

// emitSummaryAudit fires one audit entry per tick describing the net
// effect (counts of created / deleted / skipped / errors). Operationally
// critical per design doc §10 — the operator's only signal that the
// reconciler is alive AND making (or refusing to make) decisions.
func (r *Reconciler) emitSummaryAudit(ctx context.Context, stats reconcileStats) {
	level := "info"
	result := "success"
	if stats.Errors > 0 {
		level = "warn"
		result = "partial"
	}
	r.audit(audit.Entry{
		Level:  level,
		Event:  "cluster_secret_reconcile_tick",
		User:   "sharko",
		Action: "reconcile",
		Resource: fmt.Sprintf("created:%d deleted:%d skipped_unlabeled:%d skipped_pending:%d skipped_adopted:%d cleared_pending:%d user_label_synced:%d user_pending:%d errors:%d",
			stats.Created, stats.Deleted, stats.SkippedUnlabeled, stats.SkippedPending, stats.SkippedAdopted, stats.ClearedPending, stats.UserLabelSynced, stats.UserPending, stats.Errors),
		Source:    "reconciler",
		Result:    result,
		RequestID: logging.RequestID(ctx),
	})
}

// audit is a nil-safe wrapper around deps.AuditFn. The constructor does
// not enforce AuditFn != nil (callers are expected to wire it) but a
// missing wire-up should NOT panic the reconciler loop — log a warning
// and continue.
func (r *Reconciler) audit(entry audit.Entry) {
	if r.deps.AuditFn == nil {
		slog.Warn("[clusterreconciler] AuditFn not wired — dropping audit entry",
			"event", entry.Event, "action", entry.Action,
		)
		return
	}
	r.deps.AuditFn(entry)
}

// normalizeLabels coerces the interface{} Labels field of a
// ManagedClusterEntry into the map[string]string shape argosecrets
// expects. Mirrors config.parseLabels: a yaml map becomes a string map;
// the legacy `labels: []` empty-list sentinel becomes the empty map;
// anything else (including nil) becomes the empty map.
//
// We intentionally do NOT depend on internal/config here (which would
// pull a much bigger graph just for the helper) — the logic is small
// and the duplication cost is low.
func normalizeLabels(raw interface{}) map[string]string {
	if raw == nil {
		return map[string]string{}
	}
	switch v := raw.(type) {
	case models.ClusterLabels:
		// V2-cleanup-22: ManagedClusterEntry.Labels is now the named
		// models.ClusterLabels (underlying map[string]string); a type switch
		// does not match it against the unnamed map case.
		out := make(map[string]string, len(v))
		for k, val := range v {
			out[k] = val
		}
		return out
	case map[string]string:
		out := make(map[string]string, len(v))
		for k, val := range v {
			out[k] = val
		}
		return out
	case map[string]interface{}:
		out := make(map[string]string, len(v))
		for k, val := range v {
			out[k] = fmt.Sprintf("%v", val)
		}
		return out
	default:
		return map[string]string{}
	}
}

// buildClusterSecret constructs the corev1.Secret payload that
// argosecrets.Manager.Ensure would have built — but as a standalone
// helper so the reconciler can call Create directly without going
// through Ensure's adoption path (which is deliberately avoided per
// the §9 ownership policy).
//
// The Secret shape (labels, stringData keys, execProviderConfig JSON)
// MUST stay byte-identical to argosecrets.Manager's output — both
// writers coexist until the legacy argosecrets.Reconciler is retired,
// and ArgoCD's auth code path resolves the same way for both.
func buildClusterSecret(spec argosecrets.ClusterSecretSpec, namespace string) (*corev1.Secret, error) {
	configJSON, err := argosecrets.BuildSecretConfigJSON(spec)
	if err != nil {
		return nil, fmt.Errorf("building exec-provider config for cluster %q: %w", spec.Name, err)
	}
	labels := argosecrets.BuildClusterSecretLabels(spec)
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        spec.Name,
			Namespace:   namespace,
			Labels:      labels,
			Annotations: spec.Annotations,
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"name":   spec.Name,
			"server": spec.Server,
			"config": configJSON,
		},
	}, nil
}
