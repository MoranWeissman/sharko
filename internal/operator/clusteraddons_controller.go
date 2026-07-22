package operator

import (
	"context"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	v1alpha1 "github.com/MoranWeissman/sharko/api/v1alpha1"
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

// ClusterAddonsReconciler reconciles ClusterAddons objects by reading the
// canonical cluster reconciler's last-known outcome and projecting it into
// the CR's .status subresource. This is Story 1.3 — the READ-ONLY status
// projection controller.
//
// CRITICAL INVARIANT (the safety contract for Operator Phase 1):
//   This controller MUST NOT write addon labels, ArgoCD cluster Secrets, or
//   call any methods on the reconciler/argosecrets.Manager that mutate state.
//   It ONLY reads via the ReconcileStatusReader interface and ONLY writes the
//   .status subresource via client.Status().Update. The canonical reconciler
//   (internal/clusterreconciler) remains the sole writer of addon labels and
//   cluster Secrets during Phase 1. Any code path that violates this invariant
//   breaks the Phase 1 contract and risks dual-writer conflicts.
//
// The controller:
//   1. Gets the ClusterAddons CR (if NotFound, returns clean no-op — deletion case).
//   2. Looks up the last reconcile record for cr.Spec.Cluster from the status reader.
//   3. Builds .status fields: ObservedGeneration, LastReconcileTime, SyncedAddons, Conditions.
//   4. Writes ONLY the status subresource via r.Status().Update(ctx, cr).
//   5. Requeues on a 60s safety interval so status stays fresh even without CR events.
//
// +kubebuilder:rbac:groups=sharko.dev,resources=clusteraddons,verbs=get;list;watch
// +kubebuilder:rbac:groups=sharko.dev,resources=clusteraddons/status,verbs=get;update;patch
type ClusterAddonsReconciler struct {
	client.Client
	statusReader ReconcileStatusReader
}

// Reconcile implements the controller-runtime reconcile.Reconciler interface.
// It projects the canonical reconciler's last-known outcome into the CR's .status.
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
	// this controller performs — no spec/label/secret writes.
	if err := r.Status().Update(ctx, &cr); err != nil {
		logger.Error(err, "failed to update ClusterAddons status")
		return ctrl.Result{}, err
	}

	logger.V(1).Info("status updated", "cluster", clusterName, "outcome", rec.Outcome, "syncedAddons", syncedCount)

	// Step 5: Requeue on a 60s safety interval so status stays fresh even without
	// a CR event (e.g., the canonical reconciler runs and updates its internal state,
	// but no CR field changes to trigger a watch event).
	return ctrl.Result{RequeueAfter: 60 * time.Second}, nil
}

// SetupWithManager registers this controller with the manager and configures
// it to watch ClusterAddons resources.
//
// statusReader is the thin adapter to the canonical cluster reconciler's
// last-reconcile read side. Pass the reconciler instance (or a thin adapter
// implementing ReconcileStatusReader) from the serve.go operator wiring block.
func (r *ClusterAddonsReconciler) SetupWithManager(mgr ctrl.Manager, statusReader ReconcileStatusReader) error {
	r.statusReader = statusReader
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.ClusterAddons{}).
		Named("clusteraddons").
		Complete(r)
}
