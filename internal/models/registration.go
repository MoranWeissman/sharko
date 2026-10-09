package models

import "time"

// Registration-window contract shared by the kubeconfig-registration
// direct-write (internal/orchestrator/cluster.go) and the cluster-Secret
// orphan sweep (internal/clusterreconciler/reconciler.go).
//
// The race this guards against (V2-cleanup-11.1):
//
// The kubeconfig-registration path direct-writes the ArgoCD cluster Secret
// during registration Stage 1 — BEFORE the registration PR that adds the
// cluster to configuration/managed-clusters.yaml has merged. Until that PR
// merges the cluster is not "in git", so the reconciler's orphan sweep sees
// the freshly-written Secret as in-argocd ∖ in-git and deletes it (observed
// live ~200ms after the direct-write). After the PR eventually merges the
// reconciler can NOT recreate the Secret: kubeconfig bearer-token credentials
// live in no secrets backend, which is the exact reason the direct-write
// exists in the first place.
//
// The fix: the direct-write stamps AnnotationRegistrationPending with an
// RFC3339 timestamp. The orphan sweep skips any Secret carrying that
// annotation while the timestamp is within RegistrationPendingGraceWindow,
// giving the PR time to merge. Once the cluster appears in
// managed-clusters.yaml the reconciler strips the annotation (converting the
// Secret to a normal managed Secret). If the PR never merges, the annotation
// expires and the Secret is reaped on a later sweep — no permanent leak.
//
// These two values are defined here, in the shared leaf package both writers
// already import, so the writer and the sweep can never disagree about the
// key or the window. internal/models imports neither orchestrator nor
// clusterreconciler, so this is cycle-free.
const (
	// AnnotationRegistrationPending marks an ArgoCD cluster Secret that was
	// direct-written during registration Stage 1, before the registration PR
	// added the cluster to managed-clusters.yaml. Its value is an RFC3339
	// (UTC) timestamp recording the moment of the direct-write.
	//
	// The "sharko.dev/" prefix matches Sharko's annotation/label domain
	// convention (the maintainer-owned domain — V2-cleanup-59).
	AnnotationRegistrationPending = "sharko.dev/registration-pending"

	// AnnotationRegistrationPendingLegacy is the pre-V2-cleanup-59 key
	// (sharko.io — a domain the project never owned). Only ever READ:
	// a registration that was in flight on a live cluster at upgrade time
	// carries the old key, and the orphan sweep must keep honouring its
	// grace window (deleting the Secret mid-registration is exactly the
	// race this annotation exists to prevent). Writers stamp only
	// AnnotationRegistrationPending; the sweep's clear pass removes both.
	AnnotationRegistrationPendingLegacy = "sharko.io/registration-pending"

	// RegistrationPendingTimeFormat is the layout the annotation value is
	// written and parsed with. RFC3339 so the expiry can be computed from the
	// annotation itself (restart-safe) rather than from process-start or a
	// wall-clock-only assumption.
	RegistrationPendingTimeFormat = time.RFC3339
)

// RegistrationPendingGraceWindow is how long after the direct-write the
// orphan sweep tolerates a registration-pending Secret that is not yet in
// managed-clusters.yaml. 10 minutes covers a slow PR review/merge. Computed
// from the annotation timestamp, so a reconciler restart mid-window still
// honours the remaining grace.
const RegistrationPendingGraceWindow = 10 * time.Minute

// RegistrationPendingTimestamp returns the RFC3339 (UTC) timestamp string to
// stamp on a freshly direct-written cluster Secret. Centralised so the writer
// and any test fixtures format the value identically.
func RegistrationPendingTimestamp(now time.Time) string {
	return now.UTC().Format(RegistrationPendingTimeFormat)
}

// IsRegistrationPending reports whether the given annotations carry an
// unexpired registration-pending marker, evaluated against now.
//
// Return values:
//   - pending == true  → the Secret was direct-written and is still within
//     the grace window; the orphan sweep must NOT delete it.
//   - pending == false, malformed == false → no annotation present, or the
//     annotation timestamp is older than the grace window (expired). Eligible
//     for the normal orphan sweep.
//   - pending == false, malformed == true → the annotation is present but its
//     value is not a parseable RFC3339 timestamp. Treated as NOT pending
//     (fail-safe: an unparseable marker must never make a Secret immune to
//     the sweep forever). The caller logs a warning.
func IsRegistrationPending(annotations map[string]string, now time.Time) (pending, malformed bool) {
	raw, ok := RegistrationPendingValue(annotations)
	if !ok || raw == "" {
		return false, false
	}
	stamped, err := time.Parse(RegistrationPendingTimeFormat, raw)
	if err != nil {
		// Fail-safe: unparseable timestamp → not pending, flag malformed.
		return false, true
	}
	expiry := stamped.Add(RegistrationPendingGraceWindow)
	return now.Before(expiry), false
}

// RegistrationPendingValue returns the registration-pending annotation value
// under EITHER the canonical or the legacy key, preferring the canonical one.
// ok reports whether either key is present. nil-safe. Readers (the orphan
// sweep's presence checks and logs) must use this accessor rather than a
// direct map lookup so an in-flight registration stamped before the group
// rename keeps its grace window (V2-cleanup-59).
func RegistrationPendingValue(annotations map[string]string) (value string, ok bool) {
	if annotations == nil {
		return "", false
	}
	if v, has := annotations[AnnotationRegistrationPending]; has {
		return v, true
	}
	if v, has := annotations[AnnotationRegistrationPendingLegacy]; has {
		return v, true
	}
	return "", false
}
