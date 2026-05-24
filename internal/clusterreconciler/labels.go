// Package clusterreconciler — labels.go defines the ownership label that
// Sharko writes on every ArgoCD cluster Secret it creates. The label is
// the canonical signal for "this Secret is mine" and gates downstream
// behaviour:
//
//   - Reconciler core: apply the label on every Secret create/update.
//   - Orphan-delete tightening: only delete a Secret when
//     IsManagedBySharko returns true; never touch unlabeled /
//     externally-owned Secrets.
//   - Orchestrator-side cleanup: same predicate so behaviour is
//     consistent across code paths.
//
// See docs/design/2026-05-11-cluster-secret-reconciler-and-gitops-stance.md
// §5 (ownership model).
package clusterreconciler

import (
	corev1 "k8s.io/api/core/v1"
)

const (
	// LabelManagedBy is the standard Kubernetes recommended label key for
	// resource ownership / tooling identification.
	// See https://kubernetes.io/docs/concepts/overview/working-with-objects/common-labels/
	LabelManagedBy = "app.kubernetes.io/managed-by"

	// LabelValueSharko is the value Sharko writes on every Secret it creates.
	// Presence of LabelManagedBy = LabelValueSharko is the canonical "owned by
	// Sharko" signal; downstream stories key delete / adopt decisions off it.
	LabelValueSharko = "sharko"
)

// IsManagedBySharko reports whether secret carries the Sharko ownership label
// with the expected value.
//
// nil-safe: returns false for a nil Secret or a Secret with no labels.
// Used to gate orphan deletes and orchestrator cleanups.
func IsManagedBySharko(secret *corev1.Secret) bool {
	if secret == nil || secret.Labels == nil {
		return false
	}
	return secret.Labels[LabelManagedBy] == LabelValueSharko
}

// ApplyManagedBySharkoLabel sets the Sharko ownership label on secret in
// place. It is:
//   - nil-safe: a nil secret is a no-op
//   - idempotent: re-applying when the label is already correct does nothing
//   - overwriting: if the label key is present with a different value (rare —
//     would mean an external tool wrote our key with their value), we take it
//     over. This is intentional: ownership of the key belongs to Sharko.
//
// Initializes the Labels map if nil. Does NOT call the Kubernetes API — the
// caller is responsible for writing the mutated Secret back via their client.
func ApplyManagedBySharkoLabel(secret *corev1.Secret) {
	if secret == nil {
		return
	}
	if secret.Labels == nil {
		secret.Labels = make(map[string]string, 1)
	}
	secret.Labels[LabelManagedBy] = LabelValueSharko
}
