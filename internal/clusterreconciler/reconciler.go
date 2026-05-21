// Package clusterreconciler reconciles ArgoCD cluster Secret state from
// configuration/managed-clusters.yaml in git. It mirrors the
// internal/prtracker pattern: a single goroutine drives reconciliation, a
// 30s safety-net tick catches drift, and a non-blocking Trigger() channel
// provides low-latency post-merge convergence when prTracker observes a
// Sharko-opened PR being merged.
//
// V125-1-8.1 (this file): the real git → ArgoCD diff + act logic landed
// on top of V125-1-8.0's scaffold. pollOnce now:
//
//  1. Reads managed-clusters.yaml from git via models.LoadManagedClusters
//     (V125-1-9 envelope-aware + JSON-Schema-validated reader).
//  2. Lists ArgoCD cluster Secrets in the argocd namespace filtered by
//     app.kubernetes.io/managed-by=sharko (ownership label, V125-1-8.0).
//  3. Computes a set diff (in-git ∖ in-argocd → create; in-argocd ∖ in-git
//     → delete; with-label-only on delete so foreign Secrets are never
//     touched — V125-2 Adopt territory).
//  4. Per-cluster + per-secret error isolation: a vault failure on one
//     cluster does NOT block reconciliation of the others (design §10).
//
// See:
//   - docs/design/2026-05-11-cluster-secret-reconciler-and-gitops-stance.md
//     §7 (Option E), §8 (pattern), §9 (two-direction policy), §10 (REST
//     git read; failure modes), §12 (V125-1-8 deltas).
//   - internal/prtracker/tracker.go (lifecycle pattern this package mirrors).
//   - internal/argosecrets/manager.go (the Secret payload shape — execProvider
//     config — that the existing reconciler writes; V125-1-8 writes the same
//     shape so ArgoCD's auth code path is unchanged across the two writers).
package clusterreconciler

import (
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
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
)

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
			r.pollFn(ctx)
		case <-r.triggerCh:
			r.pollFn(ctx)
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
	SkippedUnlabeled int // existing same-name unlabeled Secret — V125-2 Adopt territory
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
//     same shape — the V125-1-9 reader already audits the violation list
//     via slog; we add a single audit.Entry so the operator sees the
//     rejection in /api/v1/audit alongside the slog spam.
//   - A vault error on cluster X logs + audits + CONTINUES to cluster X+1.
//   - A K8s create / delete error on Secret Y logs + audits + CONTINUES to
//     the next item in the same set.
//
// The final summary entry fires unconditionally so an operator can tell
// at a glance whether the tick was a no-op or did meaningful work.
func (r *Reconciler) pollOnce(ctx context.Context) {
	stats := reconcileStats{}

	// Step 0: dependency precondition. The reconciler is wired into the
	// process at Start time but the git provider is resolved lazily (no
	// active connection on first boot is normal — wait for the operator
	// to configure one). This branch matches secrets.Reconciler's idiom.
	if r.deps.GitProvider == nil {
		slog.Warn("[clusterreconciler] no GitProvider getter configured, skipping reconcile")
		return
	}
	gp := r.deps.GitProvider()
	if gp == nil {
		slog.Debug("[clusterreconciler] no active git provider, skipping reconcile",
			"managed_clusters_path", r.managedClustersPath,
		)
		return
	}
	if r.deps.ArgoClient == nil {
		slog.Warn("[clusterreconciler] no ArgoClient (k8s clientset) configured, skipping reconcile")
		return
	}
	if r.deps.Vault == nil {
		slog.Warn("[clusterreconciler] no Vault (cluster-credentials provider) configured, skipping reconcile")
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
			slog.Info("[clusterreconciler] managed-clusters.yaml not in git — treating as empty desired state",
				"path", r.managedClustersPath, "branch", r.branch,
			)
			// fall through with body == nil; LoadManagedClusters([]byte{})
			// would still error, so short-circuit to empty spec instead.
			r.reconcileDiff(ctx, nil, &stats)
			r.emitSummaryAudit(stats)
			return
		}
		slog.Error("[clusterreconciler] git read failed — aborting tick (no state mutated)",
			"path", r.managedClustersPath, "branch", r.branch, "error", err,
		)
		r.audit(audit.Entry{
			Level:    "error",
			Event:    "cluster_secret_reconcile",
			User:     "sharko",
			Action:   "git_read",
			Resource: fmt.Sprintf("file:%s ref:%s", r.managedClustersPath, r.branch),
			Source:   "reconciler",
			Result:   "failure",
			Error:    err.Error(),
		})
		return
	}

	// Step 2: parse + schema-validate (V125-1-9).
	spec, err := models.LoadManagedClusters(body)
	if err != nil {
		// schema.LogValidationFailure already fired slog.Error with the
		// full violation list inside LoadManagedClusters; mirror it onto
		// the audit log so the rejection is visible alongside other
		// reconciler events.
		slog.Error("[clusterreconciler] managed-clusters.yaml rejected — aborting tick (no state mutated)",
			"path", r.managedClustersPath, "error", err,
		)
		r.audit(audit.Entry{
			Level:    "error",
			Event:    "cluster_secret_reconcile",
			User:     "sharko",
			Action:   "schema_validation",
			Resource: fmt.Sprintf("file:%s", r.managedClustersPath),
			Source:   "reconciler",
			Result:   "failure",
			Error:    err.Error(),
		})
		return
	}

	r.reconcileDiff(ctx, &spec, &stats)
	r.emitSummaryAudit(stats)
}

// reconcileDiff drives the create / delete decisions from the parsed
// desired state and the live ArgoCD secret list. Extracted so the
// "empty managed-clusters.yaml" branch in pollOnce can share the logic.
// A nil spec is treated as "no clusters desired".
func (r *Reconciler) reconcileDiff(ctx context.Context, spec *models.ManagedClustersSpec, stats *reconcileStats) {
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
		slog.Error("[clusterreconciler] listing managed cluster Secrets failed — aborting tick",
			"namespace", r.namespace, "error", err,
		)
		r.audit(audit.Entry{
			Level:    "error",
			Event:    "cluster_secret_reconcile",
			User:     "sharko",
			Action:   "list_secrets",
			Resource: fmt.Sprintf("namespace:%s", r.namespace),
			Source:   "reconciler",
			Result:   "failure",
			Error:    err.Error(),
		})
		return
	}

	// Compute set diffs in O(n+m) via map lookups.
	toCreate := make([]models.ManagedClusterEntry, 0, len(desired))
	for name, entry := range desired {
		if _, present := existing[name]; !present {
			toCreate = append(toCreate, entry)
		}
	}
	toDelete := make([]string, 0)
	for name := range existing {
		if _, present := desired[name]; !present {
			toDelete = append(toDelete, name)
		}
	}

	// Create missing Secrets (in-git ∖ in-argocd).
	for _, entry := range toCreate {
		r.createOne(ctx, entry, stats)
	}

	// Delete orphan Secrets (in-argocd ∖ in-git). Pass the cached corev1.Secret
	// values so we can re-verify the ownership label as a defensive last check.
	for _, name := range toDelete {
		secret := existing[name]
		r.deleteOne(ctx, name, secret, stats)
	}
}

// listManagedSecrets fetches all sharko-labeled cluster Secrets from the
// argocd namespace as a name→Secret map. Filtered by
// app.kubernetes.io/managed-by=sharko so externally-owned Secrets are
// invisible to the reconciler — the cornerstone of the V125-1-8 ownership
// model (design doc §9: "without sharko label → never touched").
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
// The "skip if same-name unlabeled Secret exists" branch implements design
// doc §9: an unlabeled Secret is V125-2 Adopt territory; this reconciler
// must not silently overwrite it (the legacy argosecrets.Manager.Ensure
// path adopts, which is the wrong behavior for V125-1-8 since it strips
// foreign ownership intent without operator consent).
func (r *Reconciler) createOne(ctx context.Context, entry models.ManagedClusterEntry, stats *reconcileStats) {
	// Defensive: a same-name Secret may already exist without our label
	// (operator-created, or adopted-by-another-tool). The list step
	// filtered those out, so we re-check via Get before we Create.
	existing, getErr := r.deps.ArgoClient.CoreV1().Secrets(r.namespace).Get(ctx, entry.Name, metav1.GetOptions{})
	if getErr != nil && !apierrors.IsNotFound(getErr) {
		stats.Errors++
		slog.Error("[clusterreconciler] pre-create Get failed — skipping cluster",
			"cluster", entry.Name, "namespace", r.namespace, "error", getErr,
		)
		r.audit(audit.Entry{
			Level:    "error",
			Event:    "cluster_secret_create",
			User:     "sharko",
			Action:   "get_secret",
			Resource: fmt.Sprintf("cluster:%s", entry.Name),
			Source:   "reconciler",
			Result:   "failure",
			Error:    getErr.Error(),
		})
		return
	}
	if getErr == nil && !IsManagedBySharko(existing) {
		// V125-2 Adopt territory — do not touch.
		stats.SkippedUnlabeled++
		slog.Info("[clusterreconciler] same-name Secret exists without sharko label — skipping (V125-2 Adopt)",
			"cluster", entry.Name, "namespace", r.namespace,
		)
		r.audit(audit.Entry{
			Level:    "warn",
			Event:    "cluster_secret_skip_unlabeled",
			User:     "sharko",
			Action:   "skip",
			Resource: fmt.Sprintf("cluster:%s", entry.Name),
			Source:   "reconciler",
			Result:   "partial",
			Detail:   "unlabeled Secret exists in argocd namespace; defer to V125-2 Adopt flow",
		})
		return
	}

	// Resolve credentials. SecretPath overrides Name for the vault lookup
	// (matches argosecrets.Reconciler — same contract so a single repo's
	// managed-clusters.yaml works across both writers during the
	// transition window).
	credKey := entry.Name
	if entry.SecretPath != "" {
		credKey = entry.SecretPath
	}
	creds, vaultErr := r.deps.Vault.GetCredentials(credKey)
	if vaultErr != nil {
		stats.Errors++
		slog.Error("[clusterreconciler] vault GetCredentials failed — skipping cluster (others still reconcile)",
			"cluster", entry.Name, "cred_key", credKey, "error", vaultErr,
		)
		r.audit(audit.Entry{
			Level:    "error",
			Event:    "cluster_secret_create",
			User:     "sharko",
			Action:   "get_credentials",
			Resource: fmt.Sprintf("cluster:%s", entry.Name),
			Source:   "reconciler",
			Result:   "failure",
			Error:    vaultErr.Error(),
		})
		return
	}

	// Build the Secret. We reuse argosecrets.ClusterSecretSpec + the
	// package's payload builders so the Secret shape is byte-identical
	// to what argosecrets.Manager.Ensure writes — ArgoCD's auth code
	// path is unchanged regardless of which writer mutated the Secret.
	clusterLabels := normalizeLabels(entry.Labels)
	spec := argosecrets.ClusterSecretSpec{
		Name:    entry.Name,
		Server:  creds.Server,
		Region:  entry.Region,
		RoleARN: r.deps.DefaultRoleARN,
		CAData:  base64.StdEncoding.EncodeToString(creds.CAData),
		Labels:  clusterLabels,
	}

	secret, buildErr := buildClusterSecret(spec, r.namespace)
	if buildErr != nil {
		stats.Errors++
		slog.Error("[clusterreconciler] building Secret payload failed — skipping cluster",
			"cluster", entry.Name, "error", buildErr,
		)
		r.audit(audit.Entry{
			Level:    "error",
			Event:    "cluster_secret_create",
			User:     "sharko",
			Action:   "build_payload",
			Resource: fmt.Sprintf("cluster:%s", entry.Name),
			Source:   "reconciler",
			Result:   "failure",
			Error:    buildErr.Error(),
		})
		return
	}

	// Defense-in-depth: re-apply the label even though buildClusterSecret
	// already set it. Cheap, idempotent, and lock-in for the invariant
	// "every Secret this reconciler writes carries the sharko label".
	ApplyManagedBySharkoLabel(secret)

	if _, createErr := r.deps.ArgoClient.CoreV1().Secrets(r.namespace).Create(ctx, secret, metav1.CreateOptions{}); createErr != nil {
		stats.Errors++
		slog.Error("[clusterreconciler] Secret Create failed — skipping cluster",
			"cluster", entry.Name, "namespace", r.namespace, "error", createErr,
		)
		r.audit(audit.Entry{
			Level:    "error",
			Event:    "cluster_secret_create",
			User:     "sharko",
			Action:   "create",
			Resource: fmt.Sprintf("cluster:%s", entry.Name),
			Source:   "reconciler",
			Result:   "failure",
			Error:    createErr.Error(),
		})
		return
	}

	stats.Created++
	slog.Info("[clusterreconciler] cluster Secret created",
		"cluster", entry.Name, "namespace", r.namespace, "server", creds.Server,
	)
	r.audit(audit.Entry{
		Level:    "info",
		Event:    "cluster_secret_create",
		User:     "sharko",
		Action:   "create",
		Resource: fmt.Sprintf("cluster:%s", entry.Name),
		Source:   "reconciler",
		Result:   "success",
	})
}

// deleteOne removes a single orphan ArgoCD cluster Secret. The list step
// already filtered by the sharko label, but we re-verify defensively in
// case of a label race (operator stripped the label between list and
// delete) — paranoia is cheap and the design doc §9 invariant is
// "Sharko never touches what it doesn't own."
func (r *Reconciler) deleteOne(ctx context.Context, name string, cached *corev1.Secret, stats *reconcileStats) {
	if !IsManagedBySharko(cached) {
		// Should be impossible (LabelSelector pre-filtered), but the
		// invariant is too important to trust the pre-filter. Skip + log.
		slog.Warn("[clusterreconciler] cached secret missing sharko label between list and delete — skipping (invariant guard)",
			"cluster", name, "namespace", r.namespace,
		)
		return
	}

	if err := r.deps.ArgoClient.CoreV1().Secrets(r.namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			// Already gone (concurrent delete by an operator). Idempotent.
			slog.Info("[clusterreconciler] orphan Secret already deleted",
				"cluster", name, "namespace", r.namespace,
			)
			return
		}
		stats.Errors++
		slog.Error("[clusterreconciler] orphan Secret delete failed — continuing to next",
			"cluster", name, "namespace", r.namespace, "error", err,
		)
		r.audit(audit.Entry{
			Level:    "error",
			Event:    "cluster_secret_delete",
			User:     "sharko",
			Action:   "delete",
			Resource: fmt.Sprintf("cluster:%s", name),
			Source:   "reconciler",
			Result:   "failure",
			Error:    err.Error(),
		})
		return
	}

	stats.Deleted++
	slog.Info("[clusterreconciler] orphan Secret deleted",
		"cluster", name, "namespace", r.namespace,
	)
	r.audit(audit.Entry{
		Level:    "info",
		Event:    "cluster_secret_delete",
		User:     "sharko",
		Action:   "delete",
		Resource: fmt.Sprintf("cluster:%s", name),
		Source:   "reconciler",
		Result:   "success",
	})
}

// emitSummaryAudit fires one audit entry per tick describing the net
// effect (counts of created / deleted / skipped / errors). Operationally
// critical per design doc §10 — the operator's only signal that the
// reconciler is alive AND making (or refusing to make) decisions.
func (r *Reconciler) emitSummaryAudit(stats reconcileStats) {
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
		Resource: fmt.Sprintf("created:%d deleted:%d skipped_unlabeled:%d errors:%d",
			stats.Created, stats.Deleted, stats.SkippedUnlabeled, stats.Errors),
		Source: "reconciler",
		Result: result,
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
// through Ensure's adoption path (which we deliberately avoid in
// V125-1-8 per the §9 ownership policy).
//
// The Secret shape (labels, stringData keys, execProviderConfig JSON)
// MUST stay byte-identical to argosecrets.Manager's output during the
// V125-1-8 transition window — both writers coexist until the legacy
// argosecrets.Reconciler is retired, and ArgoCD's auth code path
// resolves the same way for both.
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
