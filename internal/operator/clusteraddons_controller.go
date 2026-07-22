package operator

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	v1alpha1 "github.com/MoranWeissman/sharko/api/v1alpha1"
	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
)

// ReconcileStatusReader is the minimal interface this controller needs from
// the canonical cluster reconciler. The controller ONLY reads status — it does
// NOT call any write methods. This thin adapter avoids importing the full
// reconciler (tight coupling) while preserving the contract that this controller
// is READ-ONLY for addon labels and ArgoCD cluster Secrets.
//
// The single method is the same signature as clusterreconciler.Reconciler.LastReconcile.
type ReconcileStatusReader interface {
	LastReconcile(name string) (clusterreconciler.ClusterReconcileRecord, bool)
}

// ManagedClusterLabelWriter is the minimal interface for writing addon labels
// to ArgoCD cluster Secrets. This is Story 2.1 — the controller gains the
// (gated) ability to DRIVE addon labels from the CR spec. The interface mirrors
// the Phase-1 ReconcileStatusReader pattern: a thin seam so the controller stays
// testable and does NOT import the full argosecrets.Manager tightly.
//
// The single method matches argosecrets.Manager.SyncManagedClusterLabels exactly
// (the G3-safe primitive that PRESERVES managed-by + foreign labels + Secret Data).
type ManagedClusterLabelWriter interface {
	SyncManagedClusterLabels(ctx context.Context, name string, desiredAddonLabels map[string]string) (argosecrets.ManagedLabelSyncResult, error)
}

// ClusterAddonsReconciler reconciles ClusterAddons objects. In Phase 1 (flag OFF,
// default), it projects the canonical cluster reconciler's last-known outcome into
// the CR's .status subresource — read-only status projection. In Phase 2 (flag ON),
// it DRIVES addon labels from the CR spec: compute desired labels → write them to
// the ArgoCD cluster Secret → report the write outcome in .status.
//
// PHASE 1 INVARIANT (flag OFF, default): This controller MUST NOT write addon
// labels or ArgoCD cluster Secrets. It ONLY reads via ReconcileStatusReader and
// ONLY writes the .status subresource. The canonical reconciler
// (internal/clusterreconciler) remains the sole writer.
//
// PHASE 2 CONTRACT (flag ON, gated via SHARKO_OPERATOR_DRIVES_LABELS):
//   - The controller computes desired addon labels from cr.Spec.Addons via
//     AddonAssignmentsToLabels (the inverse of the generator's mapper).
//   - It calls labelWriter.SyncManagedClusterLabels (the G3-safe primitive
//     that PRESERVES managed-by + foreign labels + Secret Data).
//   - Ownership gate: only writes Secrets already `managed-by: sharko`
//     (SyncManagedClusterLabels gates internally; a missing/foreign Secret is
//     a clean no-op, NOT a create). The controller NEVER creates a cluster
//     Secret and NEVER writes Secret Data — labels only.
//   - Status source flips: .status.conditions[Ready] = LabelsApplied/True on
//     success, or False + error reason on failure; syncedAddons = count actually
//     applied; observedGeneration + lastReconcileTime from the controller's own run.
//
// The controller:
//   1. Gets the ClusterAddons CR (if NotFound, returns clean no-op — deletion case).
//   2. Flag OFF: looks up the last reconcile record from the status reader and
//      projects it into .status (Phase 1 behavior, byte-for-byte unchanged).
//   3. Flag ON: computes desired addon labels from cr.Spec.Addons → calls
//      labelWriter.SyncManagedClusterLabels → writes .status from the write outcome.
//   4. Requeues on a 60s safety interval (flag OFF) or immediate on success (flag ON).
//
// +kubebuilder:rbac:groups=sharko.dev,resources=clusteraddons,verbs=get;list;watch
// +kubebuilder:rbac:groups=sharko.dev,resources=clusteraddons/status,verbs=get;update;patch
type ClusterAddonsReconciler struct {
	client.Client
	statusReader ReconcileStatusReader
	labelWriter  ManagedClusterLabelWriter
	DrivesLabels bool // Set from SHARKO_OPERATOR_DRIVES_LABELS at wiring time (default false)
}

// Reconcile implements the controller-runtime reconcile.Reconciler interface.
// Flag OFF (default): projects the canonical reconciler's last-known outcome into .status.
// Flag ON: drives addon labels from CR spec → writes them to the Secret → reports outcome.
func (r *ClusterAddonsReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Step 1: Fetch the ClusterAddons CR. If not found, return clean no-op (deletion case).
	var cr v1alpha1.ClusterAddons
	if err := r.Get(ctx, req.NamespacedName, &cr); err != nil {
		if apierrors.IsNotFound(err) {
			// CR was deleted — no-op (status is gone with the CR).
			return ctrl.Result{}, nil
		}
		logger.Error(err, "failed to get ClusterAddons")
		return ctrl.Result{}, err
	}

	// Step 2: Branch on the flag — flag OFF = Phase 1 (read-only status projection),
	// flag ON = Phase 2 (drive labels from spec).
	if !r.DrivesLabels {
		// Phase 1 path (flag OFF, default): project reconciler status into CR status.
		return r.reconcilePhase1(ctx, &cr, logger)
	}

	// Phase 2 path (flag ON): drive labels from CR spec → write to Secret → report outcome.
	return r.reconcilePhase2(ctx, &cr, logger)
}

// reconcilePhase1 is the Phase-1 (flag OFF, default) reconcile path: read-only
// status projection from the canonical cluster reconciler. Byte-for-byte unchanged
// from the original Phase-1 controller (Story 1.3).
func (r *ClusterAddonsReconciler) reconcilePhase1(ctx context.Context, cr *v1alpha1.ClusterAddons, logger logr.Logger) (ctrl.Result, error) {
	// Step 2: Look up the last reconcile record for this cluster from the status reader.
	clusterName := cr.Spec.Cluster
	rec, ok := r.statusReader.LastReconcile(clusterName)

	// Step 3: Build .status fields.
	cr.Status.ObservedGeneration = cr.Generation

	// LastReconcileTime: set from rec.Time if a record exists.
	if ok {
		cr.Status.LastReconcileTime = &metav1.Time{Time: rec.Time}
	} else {
		cr.Status.LastReconcileTime = nil
	}

	// SyncedAddons: count of enabled addons in spec that reconciled successfully.
	// Derivation: if the last reconcile outcome is "succeeded", count the enabled addons
	// in the spec (those with Enabled == nil or Enabled == true). If the outcome is
	// failed/skipped or no record exists, set to 0 (nothing confirmed synced).
	//
	// This is a reasonable heuristic for Phase 1 — a more precise per-addon status
	// (which addons are actually deployed) is deferred to Phase 2+ when the controller
	// can read ArgoCD Application health directly.
	syncedCount := 0
	if ok && rec.Outcome == clusterreconciler.OutcomeSucceeded {
		for _, addon := range cr.Spec.Addons {
			if addon.Enabled == nil || *addon.Enabled {
				syncedCount++
			}
		}
	}
	cr.Status.SyncedAddons = syncedCount

	// Conditions: set a "Ready" condition based on the reconcile outcome.
	// - OutcomeSucceeded → Ready=True
	// - OutcomeFailed → Ready=False with reason/message from rec.Message
	// - OutcomeSkipped or no record → Ready=Unknown
	var readyCond metav1.Condition
	if !ok {
		// No record yet — cluster hasn't been reconciled by the canonical reconciler.
		readyCond = metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionUnknown,
			ObservedGeneration: cr.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             "NoReconcileRecord",
			Message:            "cluster has not been reconciled yet (waiting for canonical reconciler)",
		}
	} else {
		switch rec.Outcome {
		case clusterreconciler.OutcomeSucceeded:
			readyCond = metav1.Condition{
				Type:               "Ready",
				Status:             metav1.ConditionTrue,
				ObservedGeneration: cr.Generation,
				LastTransitionTime: metav1.Now(),
				Reason:             "ReconcileSucceeded",
				Message:            "cluster addons are in sync with managed-clusters.yaml",
			}
		case clusterreconciler.OutcomeFailed:
			readyCond = metav1.Condition{
				Type:               "Ready",
				Status:             metav1.ConditionFalse,
				ObservedGeneration: cr.Generation,
				LastTransitionTime: metav1.Now(),
				Reason:             "ReconcileFailed",
				Message:            rec.Message,
			}
		case clusterreconciler.OutcomeSkipped:
			readyCond = metav1.Condition{
				Type:               "Ready",
				Status:             metav1.ConditionUnknown,
				ObservedGeneration: cr.Generation,
				LastTransitionTime: metav1.Now(),
				Reason:             "ReconcileSkipped",
				Message:            rec.Message,
			}
		default:
			// Unknown outcome — treat as Unknown.
			readyCond = metav1.Condition{
				Type:               "Ready",
				Status:             metav1.ConditionUnknown,
				ObservedGeneration: cr.Generation,
				LastTransitionTime: metav1.Now(),
				Reason:             "UnknownOutcome",
				Message:            "reconcile outcome is unknown",
			}
		}
	}

	// Use meta.SetStatusCondition to merge the new condition into the existing list.
	// This helper handles the LastTransitionTime logic (only updates if status changed).
	meta.SetStatusCondition(&cr.Status.Conditions, readyCond)

	// Step 4: Write the status subresource ONLY. This is the only write operation
	// this controller performs in Phase 1 — no spec/label/secret writes.
	if err := r.Status().Update(ctx, cr); err != nil {
		logger.Error(err, "failed to update ClusterAddons status")
		return ctrl.Result{}, err
	}

	logger.V(1).Info("status updated (phase 1 read-only)", "cluster", clusterName, "outcome", rec.Outcome, "syncedAddons", syncedCount)

	// Step 5: Requeue on a 60s safety interval so status stays fresh even without
	// a CR event (e.g., the canonical reconciler runs and updates its internal state,
	// but no CR field changes to trigger a watch event).
	return ctrl.Result{RequeueAfter: 60 * time.Second}, nil
}

// reconcilePhase2 is the Phase-2 (flag ON, gated) reconcile path: drive addon labels
// from CR spec → write them to the ArgoCD cluster Secret → report the write outcome
// in .status. This is Story 2.1 — the controller becomes the writer when the flag is ON.
func (r *ClusterAddonsReconciler) reconcilePhase2(ctx context.Context, cr *v1alpha1.ClusterAddons, logger logr.Logger) (ctrl.Result, error) {
	clusterName := cr.Spec.Cluster

	// Precondition: labelWriter must be wired (non-nil). If it's nil, we can't write.
	// This should never happen (wiring guarantees it when DrivesLabels=true), but
	// defensive check.
	if r.labelWriter == nil {
		logger.Error(nil, "SHARKO_OPERATOR_DRIVES_LABELS=true but labelWriter is nil — cannot drive labels")
		// Set status to NotReady with a config error reason.
		cr.Status.ObservedGeneration = cr.Generation
		cr.Status.LastReconcileTime = &metav1.Time{Time: time.Now()}
		cr.Status.SyncedAddons = 0
		readyCond := metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			ObservedGeneration: cr.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             "ConfigError",
			Message:            "label writer not configured (internal error)",
		}
		meta.SetStatusCondition(&cr.Status.Conditions, readyCond)
		_ = r.Status().Update(ctx, cr) // Best-effort status write
		return ctrl.Result{}, nil       // Don't requeue — config error won't self-heal
	}

	// Step 1: Compute desired addon labels from CR spec via AddonAssignmentsToLabels.
	desiredLabels := AddonAssignmentsToLabels(cr.Spec.Addons)

	// Step 2: Write the labels to the ArgoCD cluster Secret via SyncManagedClusterLabels.
	// This is the G3-safe primitive: PRESERVES managed-by + foreign labels + Secret Data,
	// converges ONLY addon keys. Ownership gate: only writes Secrets already
	// `managed-by: sharko` (missing/foreign Secret → Found=false, no write, no create).
	result, err := r.labelWriter.SyncManagedClusterLabels(ctx, clusterName, desiredLabels)
	if err != nil {
		// Write error (e.g., Secret get/update failed, not a not-found).
		logger.Error(err, "failed to sync addon labels to cluster Secret", "cluster", clusterName)
		// Set status to NotReady with the error.
		cr.Status.ObservedGeneration = cr.Generation
		cr.Status.LastReconcileTime = &metav1.Time{Time: time.Now()}
		cr.Status.SyncedAddons = 0
		readyCond := metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			ObservedGeneration: cr.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             "LabelSyncFailed",
			Message:            err.Error(),
		}
		meta.SetStatusCondition(&cr.Status.Conditions, readyCond)
		if statusErr := r.Status().Update(ctx, cr); statusErr != nil {
			logger.Error(statusErr, "failed to update status after label sync error")
		}
		// Requeue on error (standard controller-runtime error-return behavior).
		return ctrl.Result{}, err
	}

	// Step 3: Interpret the SyncManagedClusterLabels result and write .status.
	// Status reflects the controller's own write outcome — NOT the canonical
	// reconciler's LastReconcile (that's reconcilePhase1 only).
	cr.Status.ObservedGeneration = cr.Generation
	cr.Status.LastReconcileTime = &metav1.Time{Time: time.Now()}

	var readyCond metav1.Condition
	if !result.Found {
		// Secret not found or not managed-by=sharko — no write occurred.
		// This is NOT an error (per the design: bootstrap creates the Secret at
		// register time; a CR for a non-existent cluster is a clean no-op + NotReady status).
		cr.Status.SyncedAddons = 0
		readyCond = metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			ObservedGeneration: cr.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             "SecretNotFound",
			Message:            "ArgoCD cluster Secret not found or not managed by Sharko",
		}
		logger.Info("cluster Secret not found or not managed — skipped label write", "cluster", clusterName)
	} else {
		// Secret found and write succeeded or was already converged.
		// SyncedAddons = count of enabled addons in spec (the labels we applied or verified).
		// The Converged field from result tells us which addon keys are live after the
		// write — use len(result.Converged) if we want the actual count of keys present,
		// but the spec's enabled count is clearer (shows intent vs drift).
		syncedCount := 0
		for _, addon := range cr.Spec.Addons {
			if addon.Enabled == nil || *addon.Enabled {
				syncedCount++
			}
		}
		cr.Status.SyncedAddons = syncedCount

		// Ready=True, reason LabelsApplied. Message reflects Changed state and count.
		readyCond = metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionTrue,
			ObservedGeneration: cr.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             "LabelsApplied",
			Message:            formatLabelSyncMessage(syncedCount, result.Changed),
		}
		logger.Info("addon labels synced to cluster Secret", "cluster", clusterName, "changed", result.Changed, "syncedAddons", syncedCount, "convergedKeys", len(result.Converged))
	}

	meta.SetStatusCondition(&cr.Status.Conditions, readyCond)

	// Write the status subresource.
	if err := r.Status().Update(ctx, cr); err != nil {
		logger.Error(err, "failed to update ClusterAddons status")
		return ctrl.Result{}, err
	}

	// Requeue immediately on success (no RequeueAfter) — standard controller-runtime
	// pattern for a writer controller (the next event will re-trigger if the CR changes).
	return ctrl.Result{}, nil
}

// formatLabelSyncMessage builds an honest message for the Ready condition
// after a successful label write. Distinguishes between "applied N labels"
// (a write occurred) and "already in sync" (converged, no write needed).
func formatLabelSyncMessage(enabledCount int, changed bool) string {
	if changed {
		// A write occurred — labels were applied.
		if enabledCount == 1 {
			return "1 addon label applied to ArgoCD cluster Secret"
		}
		return fmt.Sprintf("%d addon labels applied to ArgoCD cluster Secret", enabledCount)
	}
	// No write needed — labels already matched desired state.
	return "addon labels already in sync with cluster Secret"
}

// SetupWithManager registers this controller with the manager and configures
// it to watch ClusterAddons resources.
//
// statusReader is the thin adapter to the canonical cluster reconciler's
// last-reconcile read side (required for Phase 1 path, even when flag ON).
// labelWriter is the thin adapter to argosecrets.Manager.SyncManagedClusterLabels
// (required for Phase 2 path, may be nil when flag OFF).
func (r *ClusterAddonsReconciler) SetupWithManager(mgr ctrl.Manager, statusReader ReconcileStatusReader, labelWriter ManagedClusterLabelWriter) error {
	r.statusReader = statusReader
	r.labelWriter = labelWriter
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.ClusterAddons{}).
		Named("clusteraddons").
		Complete(r)
}
