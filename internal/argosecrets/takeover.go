package argosecrets

// takeover.go — the read and the label-drop primitives brownfield takeover
// needs (v4 Wave 2, Epic 6, stories 6.1 / 6.3 / 6.4).
//
// There is deliberately no "swap" primitive here. The swap IS
// Manager.Ensure's adoption branch, which merges Sharko's labels onto the
// EXISTING Secret object, keeps every foreign label, stamps the adopted
// annotation, and never touches Data/StringData. One K8s Update on one
// object — the Secret is never absent for even an instant, and the cluster's
// name and server address come through byte-for-byte because they live in
// Data, which that branch does not write. Adding a second code path that
// created a new Secret would introduce exactly the gap Epic 6 forbids.

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/MoranWeissman/sharko/internal/models"
)

const (
	// AnnotationTakenOverAt records when Sharko took the connection over,
	// as an RFC3339 timestamp. Informational — nothing branches on it.
	AnnotationTakenOverAt = "sharko.dev/taken-over-at"

	// AnnotationTakeoverPreservedLabels is the comma-separated list of
	// label keys that belonged to the previous owner and were carried
	// over unchanged by the takeover.
	//
	// It exists so the "drop the old labels" action later on has an exact
	// list instead of a guess. Guessing would be dangerous in both
	// directions: drop too much and an unrelated ApplicationSet loses the
	// cluster; drop too little and the old owner's selector keeps
	// matching forever.
	AnnotationTakeoverPreservedLabels = "sharko.dev/takeover-preserved-labels"
)

// ClusterSecretDetail is the read-only view of a live ArgoCD cluster Secret
// that the takeover preflight works from. It is metadata plus the server
// address — never the credential material. `config` (which holds bearer
// tokens and client keys) is not read, not copied, and not returned.
type ClusterSecretDetail struct {
	// Found is false when no Secret with that name exists.
	Found bool
	// Name / Server are the cluster's identity as ArgoCD records it.
	Name   string
	Server string
	// Labels / Annotations are the live metadata, copied.
	Labels      map[string]string
	Annotations map[string]string
	// ManagedBy is the app.kubernetes.io/managed-by value ("" when unset).
	ManagedBy string
	// Adopted reports whether the Secret carries an adopted marker under
	// any of the three historical key spellings.
	Adopted bool
	// ForeignOwner* mirror DetectForeignOwner for this same object, so a
	// caller needs one API read, not two.
	ForeignOwnerAppName    string
	ForeignOwnerConfidence OwnerConfidence
	ForeignOwnerFound      bool
}

// PreservedLabelKeys returns the label keys a previous takeover recorded as
// carried over, in the order they were written. Returns nil when the
// annotation is absent or empty. nil-safe.
func PreservedLabelKeys(annotations map[string]string) []string {
	if annotations == nil {
		return nil
	}
	raw := strings.TrimSpace(annotations[AnnotationTakeoverPreservedLabels])
	if raw == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if k := strings.TrimSpace(part); k != "" {
			out = append(out, k)
		}
	}
	return out
}

// EncodePreservedLabelKeys renders keys for the annotation value. Keys are
// sorted so the annotation is stable across runs and a diff of the Secret
// does not churn.
func EncodePreservedLabelKeys(keys []string) string {
	cp := append([]string(nil), keys...)
	sort.Strings(cp)
	return strings.Join(cp, ",")
}

// GetClusterSecretDetail fetches the named cluster Secret and returns its
// metadata. A missing Secret is NOT an error — Found is false, err is nil —
// matching GetTrackingOwner / GetSecretOwnership's convention, because
// "there is nothing there yet" is an ordinary answer for a preflight.
func (m *Manager) GetClusterSecretDetail(ctx context.Context, name string) (ClusterSecretDetail, error) {
	existing, getErr := m.client.CoreV1().Secrets(m.namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(getErr) {
		return ClusterSecretDetail{Found: false}, nil
	}
	if getErr != nil {
		return ClusterSecretDetail{}, fmt.Errorf("getting secret %q in namespace %q: %w", name, m.namespace, getErr)
	}

	detail := ClusterSecretDetail{
		Found:       true,
		Name:        existing.Name,
		Labels:      copyStringMap(existing.Labels),
		Annotations: copyStringMap(existing.Annotations),
		ManagedBy:   existing.Labels[LabelManagedBy],
		Adopted:     IsAdopted(existing.Annotations),
	}
	// The server address lives in Data under "server" (K8s returns Data
	// even for values written through StringData). Only that one key is
	// read — "config" carries credentials and is left alone.
	if raw, ok := existing.Data["server"]; ok {
		detail.Server = string(raw)
	}
	detail.ForeignOwnerAppName, detail.ForeignOwnerConfidence, detail.ForeignOwnerFound = DetectForeignOwner(existing)
	return detail, nil
}

// TakeOverResult reports what the swap actually did.
type TakeOverResult struct {
	// Found is false when there is no Secret to take over.
	Found bool
	// AlreadyOwned is true when Sharko already owned the connection, so
	// the call did nothing. Re-running a takeover is safe.
	AlreadyOwned bool
	// Changed is true when a write was issued.
	Changed bool
	// PreservedLabels are the previous owner's labels that were carried
	// over verbatim (empty when the caller asked not to carry them).
	PreservedLabels map[string]string
	// DroppedLabels are the previous owner's labels that were removed
	// because the caller explicitly asked not to preserve them.
	DroppedLabels map[string]string
	// ProtectionRepaired is true when Sharko already owned the connection
	// but the record of which labels came from the previous owner was
	// missing, and this call wrote it back. See the AlreadyOwned branch of
	// TakeOverClusterSecret for why that record is worth repairing.
	ProtectionRepaired bool
	// Server is the cluster's API address, unchanged by the swap.
	Server string
}

// TakeOverClusterSecret makes Sharko the owner of an ArgoCD cluster Secret
// that somebody else set up.
//
// # Ordering — why there is no create-then-delete here
//
// The new Secret has to keep the SAME name as the old one, and two Secrets
// cannot share a name in a namespace. So "create the new one, then remove
// the old one" is not available: it would have to delete first, and between
// the delete and the create the cluster would have no connection at all —
// ArgoCD would drop it, and every Application pointed at it would go
// Unknown. Instead this is a single in-place update of the one Secret
// object: Sharko's ownership label goes on, the previous owner's labels
// stay, the credential material is not touched. The Secret is never absent
// for even an instant, and because it is one K8s write there is no
// half-finished state to recover from.
//
// # What it writes
//
//   - app.kubernetes.io/managed-by: sharko — Sharko now owns it.
//   - argocd.argoproj.io/secret-type: cluster — re-applied defensively; it
//     is already there on any working cluster Secret.
//   - the adopted marker — so every sweep in the codebase treats this as a
//     guest Secret: labels are managed, connection data is never rewritten,
//     and the automatic orphan cleanup will not delete it.
//   - a note of when the takeover happened, and the exact list of label
//     keys carried over from the previous owner.
//
// # What it never writes
//
// Data and StringData. That is where the cluster's name, its API address
// and its credentials live — all of it comes through the swap byte for
// byte, because this function has no code path that assigns to them.
//
// preserveLegacyLabels defaults the caller's intent: true carries every
// label the previous owner left behind; false removes them as part of the
// same single write. takenOverAt is the RFC3339 timestamp to record
// (passed in rather than read from the clock so the write is deterministic
// and testable).
func (m *Manager) TakeOverClusterSecret(ctx context.Context, name string, preserveLegacyLabels bool, takenOverAt string, legacyKeyFn func(key string) bool) (TakeOverResult, error) {
	if legacyKeyFn == nil {
		return TakeOverResult{}, fmt.Errorf("takeover of cluster %q: no legacy-label classifier supplied", name)
	}

	existing, getErr := m.client.CoreV1().Secrets(m.namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(getErr) {
		return TakeOverResult{Found: false}, nil
	}
	if getErr != nil {
		return TakeOverResult{}, fmt.Errorf("getting secret %q in namespace %q: %w", name, m.namespace, getErr)
	}

	res := TakeOverResult{Found: true}
	if raw, ok := existing.Data["server"]; ok {
		res.Server = string(raw)
	}

	if existing.Labels[LabelManagedBy] == ManagedByValue {
		res.AlreadyOwned = true
		// Sharko owns it already — but does it have the record of WHICH
		// labels came from whoever had it before?
		//
		// A cluster brought in by the older adopt path carries Sharko's
		// ownership label and the adopted marker, yet nothing ever wrote
		// the preserved-labels list. That list is what tells the rest of
		// Sharko "these keys are not mine, leave them alone". Without it,
		// the next label sync reads those keys as Sharko's own, sees they
		// are not in git, and reports them as drift — and a self-heal run
		// takes them off the connection entirely.
		//
		// So re-running a takeover repairs it. One metadata-only write:
		// the annotation goes on, and Data/StringData are not touched
		// (there is no code path below that assigns to them).
		repaired, repairErr := m.repairPreservedLabelsRecord(ctx, existing, legacyKeyFn)
		if repairErr != nil {
			return res, repairErr
		}
		if len(repaired) > 0 {
			res.ProtectionRepaired = true
			res.PreservedLabels = repaired
		}
		return res, nil
	}

	updated := existing.DeepCopy()
	if updated.Labels == nil {
		updated.Labels = map[string]string{}
	}

	preserved := map[string]string{}
	dropped := map[string]string{}
	for k, v := range existing.Labels {
		if !legacyKeyFn(k) {
			continue
		}
		if preserveLegacyLabels {
			preserved[k] = v
			continue
		}
		dropped[k] = v
		delete(updated.Labels, k)
	}

	updated.Labels[LabelSecretType] = "cluster"
	updated.Labels[LabelManagedBy] = ManagedByValue
	// Guest stance, same as Ensure's adopted branch: a cluster Sharko did
	// not set up never gets the connectivity-check label, not even for one
	// tick.
	models.RemoveConnectivityCheckLabels(updated.Labels)

	if updated.Annotations == nil {
		updated.Annotations = map[string]string{}
	}
	updated.Annotations[AnnotationAdopted] = "true"
	delete(updated.Annotations, AnnotationAdoptedDoubledPrefixLegacy)
	delete(updated.Annotations, AnnotationAdoptedLegacy)
	if takenOverAt != "" {
		updated.Annotations[AnnotationTakenOverAt] = takenOverAt
	}
	if len(preserved) > 0 {
		keys := make([]string, 0, len(preserved))
		for k := range preserved {
			keys = append(keys, k)
		}
		updated.Annotations[AnnotationTakeoverPreservedLabels] = EncodePreservedLabelKeys(keys)
	} else {
		delete(updated.Annotations, AnnotationTakeoverPreservedLabels)
	}

	// Deliberately NOT BuildClusterSecret: the takeover is a metadata-only
	// mutation of the LIVE object — the previous owner's Data/StringData
	// (name, server, credentials) come through byte-for-byte because no code
	// path here assigns to them. A from-scratch build would need credential
	// material Sharko does not have for a taken-over cluster.
	if _, updErr := m.client.CoreV1().Secrets(m.namespace).Update(ctx, updated, metav1.UpdateOptions{}); updErr != nil {
		return res, fmt.Errorf("taking over secret %q in namespace %q: %w", name, m.namespace, updErr)
	}

	res.Changed = true
	if len(preserved) > 0 {
		res.PreservedLabels = preserved
	}
	if len(dropped) > 0 {
		res.DroppedLabels = dropped
	}
	slog.Info("[argosecrets] cluster secret taken over — ownership stamped, connection data untouched",
		"cluster", name, "namespace", m.namespace,
		"preserved_labels", len(preserved), "dropped_labels", len(dropped),
	)
	return res, nil
}

// repairPreservedLabelsRecord stamps the preserved-labels annotation onto a
// connection Sharko already owns whose annotation is missing, and returns the
// labels it recorded. It returns nil (and writes nothing) when there is
// nothing to record or the record is already there — so it is safe to call on
// every re-run.
//
// Metadata only: the annotation map on a DeepCopy is the sole thing written.
// Labels, Data and StringData are never assigned to.
func (m *Manager) repairPreservedLabelsRecord(ctx context.Context, existing *corev1.Secret, legacyKeyFn func(key string) bool) (map[string]string, error) {
	// Already recorded — nothing to repair. An existing list is left exactly
	// as it is; this function only ever fills a hole.
	if strings.TrimSpace(existing.Annotations[AnnotationTakeoverPreservedLabels]) != "" {
		return nil, nil
	}

	legacy := map[string]string{}
	for k, v := range existing.Labels {
		if legacyKeyFn(k) {
			legacy[k] = v
		}
	}
	if len(legacy) == 0 {
		return nil, nil
	}

	updated := existing.DeepCopy()
	if updated.Annotations == nil {
		updated.Annotations = map[string]string{}
	}
	keys := make([]string, 0, len(legacy))
	for k := range legacy {
		keys = append(keys, k)
	}
	updated.Annotations[AnnotationTakeoverPreservedLabels] = EncodePreservedLabelKeys(keys)

	// Deliberately NOT BuildClusterSecret — annotation-only repair on a
	// DeepCopy of the live object; everything else must survive untouched.
	if _, updErr := m.client.CoreV1().Secrets(m.namespace).Update(ctx, updated, metav1.UpdateOptions{}); updErr != nil {
		return nil, fmt.Errorf("recording the carried-over labels on secret %q in namespace %q: %w", existing.Name, m.namespace, updErr)
	}
	slog.Info("[argosecrets] cluster already owned by Sharko — restored the record of which labels came from the previous owner",
		"cluster", existing.Name, "namespace", m.namespace, "preserved_labels", len(legacy),
	)
	return legacy, nil
}

// DropLabels removes the named label keys from a cluster Secret Sharko owns
// and returns the keys it actually removed.
//
// Three guards, in this order:
//
//  1. The Secret must exist. A missing one returns found=false, no error —
//     the caller reports it rather than erroring.
//  2. The Secret must be Sharko's (managed-by=sharko). Stripping labels off
//     somebody else's connection is not Sharko's to do, and the ownership
//     label is the same gate every other destructive path in the codebase
//     uses.
//  3. Keys Sharko must never remove are refused outright, not silently
//     skipped: ArgoCD's own secret-type marker (removing it makes ArgoCD
//     forget the cluster exists), Sharko's ownership label, and any v4
//     addon label (those are derived from git — removing one here would
//     just be undone on the next reconcile, and in the meantime the addon
//     would be pruned off the cluster).
//
// Keys that are simply not on the Secret are a no-op, so calling this twice
// is safe. When nothing would change, no write is issued at all.
func (m *Manager) DropLabels(ctx context.Context, name string, keys []string) (removed []string, found bool, err error) {
	for _, k := range keys {
		if reason := refuseDropReason(k); reason != "" {
			return nil, false, fmt.Errorf("refusing to remove label %q from cluster %q: %s", k, name, reason)
		}
	}

	existing, getErr := m.client.CoreV1().Secrets(m.namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(getErr) {
		return nil, false, nil
	}
	if getErr != nil {
		return nil, false, fmt.Errorf("getting secret %q in namespace %q: %w", name, m.namespace, getErr)
	}

	if existing.Labels[LabelManagedBy] != ManagedByValue {
		return nil, true, fmt.Errorf(
			"cluster %q is not managed by Sharko (its connection is marked %q) — take it over first",
			name, existing.Labels[LabelManagedBy])
	}

	updated := existing.DeepCopy()
	for _, k := range keys {
		if _, ok := updated.Labels[k]; ok {
			delete(updated.Labels, k)
			removed = append(removed, k)
		}
	}
	if len(removed) == 0 {
		return nil, true, nil
	}
	sort.Strings(removed)

	// Keep the preserved-labels annotation honest: whatever is gone comes
	// off the list, so a second drop does not offer keys that no longer
	// exist. The annotation disappears entirely once the list is empty.
	if updated.Annotations != nil {
		remaining := []string{}
		for _, k := range PreservedLabelKeys(updated.Annotations) {
			if !containsString(removed, k) {
				remaining = append(remaining, k)
			}
		}
		if len(remaining) == 0 {
			delete(updated.Annotations, AnnotationTakeoverPreservedLabels)
		} else {
			updated.Annotations[AnnotationTakeoverPreservedLabels] = EncodePreservedLabelKeys(remaining)
		}
	}

	// Data/StringData are never touched — this is a metadata-only update
	// on a DeepCopy, the same idiom Ensure's adopted branch uses.
	// Deliberately NOT BuildClusterSecret for the same reason: only label
	// removal happens here, everything else comes through verbatim.
	if _, updErr := m.client.CoreV1().Secrets(m.namespace).Update(ctx, updated, metav1.UpdateOptions{}); updErr != nil {
		return nil, true, fmt.Errorf("removing labels from secret %q in namespace %q: %w", name, m.namespace, updErr)
	}
	return removed, true, nil
}

// refuseDropReason returns a plain-English reason when a label key must
// never be removed by the drop action, or "" when the key is fair game.
func refuseDropReason(key string) string {
	switch key {
	case "":
		return "an empty label name is not a label"
	case LabelSecretType:
		return "this is the marker that makes ArgoCD treat the connection as a cluster — removing it makes ArgoCD forget the cluster exists"
	case LabelManagedBy:
		return "this is the marker that says Sharko owns the connection — removing it hands the connection back to nobody"
	}
	if IsAddonLabelKey(key) && strings.Contains(key, "/") {
		// Only the v4 namespaced addon keys reach this arm; an
		// unqualified key on a v4 cluster Secret is a legacy label and IS
		// droppable, which is the whole point of this feature.
		return "this label is how Sharko turns an addon on for this cluster — change it by turning the addon off instead"
	}
	return ""
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func copyStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
