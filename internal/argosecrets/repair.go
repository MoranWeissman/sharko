package argosecrets

// repair.go — the write primitive for repairing a connection Sharko OWNS.
//
// # Why this is a new primitive and not one of the three that already exist
//
// The three existing writers were each read before this was added, and none of
// them can do this job:
//
//   - Ensure (manager.go) replaces the label map outright (updated.Labels =
//     desiredLabels) and nils Data before writing StringData. So a foreign
//     label and a data key Sharko did not write are both LOST. A repair must
//     preserve them — they belong to whoever put them there.
//   - SyncLabelsOnly is the guest primitive for user-owned Secrets. It never
//     touches Data at all and it STRIPS Sharko's ownership label from a
//     non-adopted Secret. Both are wrong for repairing a connection Sharko owns.
//   - SyncManagedClusterLabels converges labels only and never touches Data.
//     It is exactly right for the addon-labels-only repair scope, and it is
//     what that scope uses — but it cannot rewrite the connection itself.
//
// So the full-connection repair needed a primitive that writes the owned
// connection fields IN PLACE while leaving everything else alone. That is this
// file, and it lives next to the others rather than in a new package so there is
// still one place to look for "what writes a cluster connection Secret".
//
// # The ownership recheck is inside this function, right against the Update
//
// The gap between "Sharko checked who owns this" and "Sharko wrote it" is the
// whole risk of this feature. So the check does not happen in the handler, or in
// the reconciler, or at the top of a long function: it happens HERE, and between
// it and the Update call there is nothing but pure in-memory map work — no HTTP
// call, no git read, no credential fetch, no lock, nothing that can block.
//
// On top of that, the Update carries the resourceVersion the Get returned
// (DeepCopy preserves it), so Kubernetes itself performs the final
// compare-and-swap. If anything at all changed the Secret between the Get and
// the Update — including something that changed the ownership label in that
// window — the API server rejects the write with a conflict and nothing is
// written. The recheck catches the case we can name; optimistic concurrency
// catches the case we cannot.
//
// # It never deletes and never recreates
//
// One Update against one live object, always. A delete-and-recreate would lose
// every field Sharko does not model, break anything watching the object, and
// briefly leave ArgoCD with no connection at all.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/MoranWeissman/sharko/internal/models"
)

// The three data keys Sharko writes into a connection Secret. BuildClusterSecret
// (builder.go) writes exactly these three into StringData; they are named here as
// constants so the repair's "which keys are mine" boundary is a shared fact
// rather than three string literals repeated at each use.
const (
	dataKeyName   = "name"
	dataKeyServer = "server"
	dataKeyConfig = "config"
)

// ErrRepairSecretMissing means there is no connection Secret to repair.
//
// Repair is an in-place correction of something that exists. Creating a missing
// connection is the reconciler's own job (createOne, on its normal pass), and
// doing it here would make "repair" mean two different things depending on what
// it found.
var ErrRepairSecretMissing = errors.New("there is no ArgoCD connection Secret for this cluster to repair")

// ErrRepairOwnershipChanged means the live Secret is not Sharko's to write.
//
// Either its ownership marker names another tool, or it carries no marker at
// all (an unlabeled Secret is Adopt territory, never something to overwrite).
// Both are refusals, and the refusal is the same either way: Sharko does not
// write a connection it does not own.
var ErrRepairOwnershipChanged = errors.New("this cluster's ArgoCD connection is not owned by Sharko any more, so Sharko will not change it")

// ErrRepairSecretChangedUnderneath means something else wrote the Secret
// between Sharko reading it and Sharko writing it, so the write was rejected
// and nothing changed.
var ErrRepairSecretChangedUnderneath = errors.New("something else changed this cluster's ArgoCD connection while Sharko was repairing it, so Sharko wrote nothing")

// RepairOutcome reports what a repair actually did.
type RepairOutcome struct {
	// Changed is true when a real Update was issued. False means the
	// connection already matched the Git-defined connection and no write was
	// needed.
	Changed bool

	// FieldsWritten are the owned field paths the repair changed, sorted. It
	// names FIELDS, never values — data.config appears when the credential
	// blob was rewritten, and its contents are never reported anywhere.
	FieldsWritten []string

	// PreservedForeignLabels and PreservedForeignDataKeys count what the
	// repair deliberately left alone. Counts and key NAMES only for labels
	// Sharko owns; for foreign material only the count, because naming
	// somebody else's keys is reporting on their data.
	PreservedForeignLabels   int
	PreservedForeignDataKeys int
}

// changedLabelPaths reports which label keys differ between two label maps, as
// metadata.labels[<key>] paths, sorted.
//
// It is a DIFF, not a running tally, and that is the point: a tally has to be
// updated at every mutation site and silently drifts the day somebody adds
// another one. Diffing the label map Sharko is about to write against the one
// it read cannot drift — additions, value changes and removals all fall out of
// the same comparison, and "nothing changed" is exactly an empty result, so the
// no-write decision and the reported field list can never disagree.
//
// It names KEYS, never values. A label key is Sharko's own vocabulary or a
// foreign key that is already visible to anyone who can read the object's
// metadata; a label VALUE never appears here, and neither does anything from
// Data.
func changedLabelPaths(before, after map[string]string) []string {
	var paths []string
	for k, afterVal := range after {
		beforeVal, wasThere := before[k]
		if !wasThere || beforeVal != afterVal {
			paths = append(paths, "metadata.labels["+k+"]")
		}
	}
	for k := range before {
		if _, stillThere := after[k]; !stillThere {
			paths = append(paths, "metadata.labels["+k+"]")
		}
	}
	sort.Strings(paths)
	return paths
}

// foreignPreservationCounts counts the material a repair deliberately left
// alone: labels that are not Sharko's to converge, and data keys Sharko never
// writes.
//
// COUNTS ONLY, never names. A foreign label key or data key belongs to whoever
// put it there, and listing them would be Sharko reporting on somebody else's
// material. The count is enough to say "your things are still there".
//
// Both repair paths call this so a repair reports preservation the same way
// whichever path ran. The definition of "foreign" is the same on both, too: the
// three connection data keys and Sharko's own label vocabulary are Sharko's,
// everything else is not. The labels-only path never writes Data at all, so on
// that path this number says what a full repair WOULD have left alone — the
// same sentence, the same meaning, one place that decides it.
//
// preserved is the set of label keys a takeover recorded as the previous
// owner's. Those are foreign by record even though their bare keys look like
// Sharko's vocabulary.
func foreignPreservationCounts(labels map[string]string, data map[string][]byte, preserved map[string]struct{}) (foreignLabels, foreignDataKeys int) {
	for k := range labels {
		if _, isPreserved := preserved[k]; isPreserved {
			foreignLabels++
			continue
		}
		if !IsAddonLabelKey(k) && k != LabelManagedBy && k != LabelSecretType &&
			!models.HasConnectivityCheckLabel(map[string]string{k: ""}) {
			foreignLabels++
		}
	}
	for k := range data {
		if k != dataKeyName && k != dataKeyServer && k != dataKeyConfig {
			foreignDataKeys++
		}
	}
	return foreignLabels, foreignDataKeys
}

// RepairOwnedConnection rewrites the connection fields Sharko owns on the named
// cluster's ArgoCD connection Secret, in place, and leaves everything else
// exactly as it was.
//
// What it writes:
//   - the three data keys Sharko writes: name, server, config
//   - the labels the canonical builder produces for this spec, merged onto the
//     live label set
//   - the Sharko addon-label keys git no longer declares are removed, the same
//     full convergence SyncManagedClusterLabels performs, and for the same
//     reason: a leftover enabled label keeps an addon deployed after it was
//     turned off in git
//   - the provenance annotations the caller supplies, merged onto the existing
//     annotations
//
// What it never touches: any other data key, any label that is not Sharko's,
// any label a takeover recorded as preserved, any other annotation, and the
// Secret's identity.
//
// desired must come from BuildClusterSecret — it is built by the caller and
// passed in, so the build happens BEFORE the live read and the window between
// the ownership recheck and the Update stays as small as possible.
func (m *Manager) RepairOwnedConnection(ctx context.Context, desired *corev1.Secret, provenance map[string]string) (RepairOutcome, error) {
	if desired == nil {
		return RepairOutcome{}, fmt.Errorf("no desired connection Secret was built")
	}
	name := desired.Name

	// The live read. Everything from here to the Update is deliberately
	// I/O-free.
	existing, getErr := m.client.CoreV1().Secrets(m.namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(getErr) {
		return RepairOutcome{}, ErrRepairSecretMissing
	}
	if getErr != nil {
		return RepairOutcome{}, fmt.Errorf("re-reading the cluster Secret %q in namespace %q immediately before the repair: %w", name, m.namespace, getErr)
	}

	// ── THE OWNERSHIP RECHECK ─────────────────────────────────────────────
	// This is rule 3 and rule 4. It runs against the object that was just
	// read, not against anything gathered earlier in the request, and there is
	// no I/O between here and the Update below.
	//
	// Sharko's own marker, exactly. An empty marker is an unlabeled Secret
	// (Adopt territory) and a different marker is another tool's connection;
	// neither is Sharko's to rewrite.
	if existing.Labels[LabelManagedBy] != ManagedByValue {
		return RepairOutcome{}, ErrRepairOwnershipChanged
	}
	// An adopted Secret is a guest arrangement: the connection material is the
	// user's and only the addon labels are Sharko's. A full repair must never
	// reach one. The caller's policy already routes adopted clusters to the
	// labels-only scope; this is the last line, not the only one.
	if IsAdopted(existing.Annotations) {
		return RepairOutcome{}, ErrRepairOwnershipChanged
	}

	updated := existing.DeepCopy()
	if updated.Labels == nil {
		updated.Labels = make(map[string]string, len(desired.Labels))
	}
	if updated.Data == nil {
		updated.Data = make(map[string][]byte, len(desired.StringData))
	}

	// Labels a takeover carried over from the previous owner are never
	// convergence candidates — same rule SyncManagedClusterLabels follows, and
	// it matters for the same reason: a legacy key like "env: prod" has no "/"
	// in it, so IsAddonLabelKey calls it Sharko's, and the delete loop below
	// would quietly undo the takeover's "every legacy label preserved" promise.
	preserved := make(map[string]struct{})
	for _, k := range PreservedLabelKeys(existing.Annotations) {
		preserved[k] = struct{}{}
	}

	var written []string

	// Remove the Sharko addon keys git no longer declares.
	for k := range updated.Labels {
		if _, isPreserved := preserved[k]; isPreserved {
			continue
		}
		if !IsAddonLabelKey(k) {
			continue
		}
		if _, stillWanted := desired.Labels[k]; !stillWanted {
			delete(updated.Labels, k)
			written = append(written, "metadata.labels["+k+"]")
		}
	}

	// Apply every label the builder produced. A preserved takeover key is
	// skipped even here: the builder cannot know about it, so a collision means
	// the takeover's copy wins, which is the promise that was made.
	for k, v := range desired.Labels {
		if _, isPreserved := preserved[k]; isPreserved {
			continue
		}
		if updated.Labels[k] != v {
			updated.Labels[k] = v
			written = append(written, "metadata.labels["+k+"]")
		}
	}

	// The connection data. Only the three keys Sharko writes, straight from the
	// canonical builder's output, into Data as bytes.
	//
	// Writing Data rather than StringData is deliberate: StringData is a
	// write-only convenience the API server merges into Data, so setting it
	// here would leave the merge semantics for foreign keys up to the server
	// and up to whatever a fake client does in a test. Writing Data directly
	// means the preserved foreign keys are visible in the object being sent,
	// and what the tests assert is exactly what production sends.
	for _, key := range []string{dataKeyName, dataKeyServer, dataKeyConfig} {
		want, ok := desired.StringData[key]
		if !ok {
			continue
		}
		if string(updated.Data[key]) == want {
			continue
		}
		updated.Data[key] = []byte(want)
		written = append(written, "data."+key)
	}
	// StringData is left nil on purpose — see above. DeepCopy carried whatever
	// the live object had, so clear it rather than letting a stale
	// server-side merge re-apply old values.
	updated.StringData = nil

	// Count what was deliberately left alone, for an honest report. The count
	// runs through the shared helper both repair paths use, so "foreign" means
	// one thing across the whole feature.
	outcome := RepairOutcome{}
	outcome.PreservedForeignLabels, outcome.PreservedForeignDataKeys =
		foreignPreservationCounts(updated.Labels, updated.Data, preserved)

	if len(written) == 0 {
		// Already the Git-defined connection. No write, no churn, and no provenance
		// stamp — an untouched Secret has nothing to be provenance for.
		slog.Debug("[argosecrets] connection repair found nothing to change",
			"cluster", name, "namespace", m.namespace)
		outcome.Changed = false
		return outcome, nil
	}

	// Provenance, merged on top of whatever is already there. Every other
	// annotation on the Secret — an adopted marker, a takeover's preserved-label
	// record — survives untouched.
	if len(provenance) > 0 {
		if updated.Annotations == nil {
			updated.Annotations = make(map[string]string, len(provenance))
		}
		for k, v := range provenance {
			updated.Annotations[k] = v
		}
	}

	// ── THE WRITE ─────────────────────────────────────────────────────────
	// One Update, in place, against the object read above — never a delete and
	// never a create. updated carries the resourceVersion from the Get, so the
	// API server does a compare-and-swap: any change made by anyone else since
	// the Get makes this fail rather than overwrite.
	if _, updateErr := m.client.CoreV1().Secrets(m.namespace).Update(ctx, updated, metav1.UpdateOptions{}); updateErr != nil {
		if apierrors.IsConflict(updateErr) {
			return RepairOutcome{}, ErrRepairSecretChangedUnderneath
		}
		return RepairOutcome{}, fmt.Errorf("repairing the cluster Secret %q in namespace %q: %w", name, m.namespace, updateErr)
	}

	sort.Strings(written)
	outcome.Changed = true
	outcome.FieldsWritten = written
	slog.Info("[argosecrets] cluster connection repaired in place — foreign labels, foreign data keys and annotations preserved",
		"cluster", name, "namespace", m.namespace,
		"fields_written", len(written),
		"foreign_labels_preserved", outcome.PreservedForeignLabels,
		"foreign_data_keys_preserved", outcome.PreservedForeignDataKeys,
	)
	return outcome, nil
}
