package connectioncompare

// fields.go — CC2-2, the field-ownership policy. One shared place that says
// which parts of an ArgoCD cluster connection Secret Sharko puts there and
// therefore compares, which parts belong to somebody else and are invisible
// to the comparison, and which of the compared parts hold credential material
// and may never be shown.
//
// It is one policy on purpose. The same question used to be answered by
// scattered checks in the writers (Ensure's adopted branch, SyncLabelsOnly's
// guest stance, DropLabels' refusal list), and a reader that re-guessed the
// boundary would report drift on things Sharko never wrote — or worse, stay
// quiet about something it did.
//
// Every entry below was checked against the writers before it was written
// down, and where the plan and the code disagreed the code won. The three
// places that happened are called out in the comments: the connectivity-check
// label, the provenance annotations, and the credential blob for a cluster
// whose configured credentials source stores no credential at all.

import (
	"encoding/json"
	"sort"

	corev1 "k8s.io/api/core/v1"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
	"github.com/MoranWeissman/sharko/internal/models"
)

// Field paths. Stable strings — the UI in step 4 and the repair in step 3 both
// key off them, so they are literals here rather than assembled at each use.
const (
	FieldPathSecretType = "type"
	FieldPathName       = "metadata.name"
	FieldPathNamespace  = "metadata.namespace"
	FieldPathDataName   = "data.name"
	FieldPathDataServer = "data.server"
	FieldPathDataConfig = "data.config"
)

// labelFieldPath renders the field path for one label key.
func labelFieldPath(key string) string { return "metadata.labels[" + key + "]" }

// FieldStatus is what happened to one compared field.
type FieldStatus string

const (
	// FieldSame — Sharko's expected value and the live value agree.
	FieldSame FieldStatus = "same"
	// FieldDifferent — both sides have a value and they disagree.
	FieldDifferent FieldStatus = "different"
	// FieldMissing — Sharko expects this field and the live Secret has no
	// value for it.
	FieldMissing FieldStatus = "missing"
	// FieldUnexpected — the live Secret carries a value for a field Sharko
	// owns but no longer declares. A stale addon label is the usual case:
	// turning an addon off in Git removes the key, so the leftover live key
	// keeps the addon deployed until it is taken off.
	FieldUnexpected FieldStatus = "unexpected"
)

// Difference is one field that did not match.
//
// # Why Expected and Live are pointers
//
// A sensitive field must come back with NO expected property and NO live
// property — not empty strings, the properties must be absent. Pointers plus
// omitempty do that, and MarshalJSON below makes it structural rather than a
// habit: a sensitive Difference cannot serialise either side even if some
// future code path fills the pointers in by mistake.
type Difference struct {
	// Path is the field, e.g. "data.server" or
	// `metadata.labels[addons.sharko.dev/datadog]`.
	Path string `json:"path"`

	// Status is same / different / missing / unexpected.
	Status FieldStatus `json:"status"`

	// Sensitive marks a field whose value is credential material. When true,
	// neither side is ever serialised — see MarshalJSON.
	Sensitive bool `json:"sensitive,omitempty"`

	// Expected is what Sharko means this field to be. Absent for a sensitive
	// field, and absent for an unexpected field (Sharko declares nothing).
	Expected *string `json:"expected,omitempty"`

	// Live is what the connection actually carries. Absent for a sensitive
	// field, and absent for a missing field (there is nothing live).
	Live *string `json:"live,omitempty"`
}

// MarshalJSON drops both sides of a sensitive field unconditionally.
//
// This is the last line, not the only line: safeDifference and
// sensitiveDifference are the only constructors, and sensitiveDifference never
// fills the pointers. But the rule is "a credential value must never leave the
// server", and a rule that important should hold even if somebody later builds
// a Difference by hand with a struct literal.
func (d Difference) MarshalJSON() ([]byte, error) {
	type wire struct {
		Path      string      `json:"path"`
		Status    FieldStatus `json:"status"`
		Sensitive bool        `json:"sensitive,omitempty"`
		Expected  *string     `json:"expected,omitempty"`
		Live      *string     `json:"live,omitempty"`
	}
	out := wire{Path: d.Path, Status: d.Status, Sensitive: d.Sensitive}
	if !d.Sensitive {
		out.Expected = d.Expected
		out.Live = d.Live
	}
	return json.Marshal(out)
}

// safeDifference builds a Difference for a field whose value is not credential
// material, so both sides may be shown. Pass "" with ok=false for a side that
// does not exist.
func safeDifference(path string, status FieldStatus, expected *string, live *string) Difference {
	return Difference{Path: path, Status: status, Expected: expected, Live: live}
}

// sensitiveDifference builds a Difference for a field holding credential
// material. It takes no values at all — there is no parameter a value could
// arrive through, so there is no code path here that could return one.
func sensitiveDifference(path string, status FieldStatus) Difference {
	return Difference{Path: path, Status: status, Sensitive: true}
}

func strPtr(s string) *string { return &s }

// sortDifferences puts differences in a fixed order so the same connection
// produces the same answer every time. Path alone is enough — no two
// differences share a path.
func sortDifferences(diffs []Difference) {
	sort.Slice(diffs, func(i, j int) bool { return diffs[i].Path < diffs[j].Path })
}

// ---------------------------------------------------------------------------
// What Sharko owns
// ---------------------------------------------------------------------------

// ownedLabelKey reports whether one label key on a cluster connection Secret
// is Sharko's to compare, given the scope this connection is being checked at.
//
// The addon-key half is argosecrets.IsAddonLabelKey, reused rather than
// re-listed. That predicate is structural, not a prefix list: an unqualified
// key (no "/") is one of Sharko's own addon-enablement keys, every
// DNS-qualified key is somebody's system or foreign label and is preserved
// untouched, and the one qualified exception is Sharko's own v4 vocabulary
// addons.sharko.dev/<addon>. Writing a second list here would be a second
// thing to keep in step, and the first one to drift.
//
// On top of that:
//
//   - The two system labels Sharko stamps — argocd.argoproj.io/secret-type
//     and app.kubernetes.io/managed-by — are Sharko's on a connection Sharko
//     owns. On a guest connection they are NOT: SyncLabelsOnly deliberately
//     does not write them, and on a plain self-managed Secret it actively
//     STRIPS the ownership label so the orphan sweeps stop treating the
//     person's connection as Sharko's. Comparing them at guest scope would
//     report the strip as drift.
//   - The connectivity-check label is Sharko's on a connection Sharko owns
//     and is never Sharko's on a guest connection — see
//     connectivityCheckOwnedAt.
func ownedLabelKey(key string, scope Scope) bool {
	if key == "" {
		return false
	}
	if models.HasConnectivityCheckLabel(map[string]string{key: ""}) {
		return connectivityCheckOwnedAt(scope)
	}
	switch key {
	case argosecrets.LabelSecretType, argosecrets.LabelManagedBy:
		return systemLabelsOwnedAt(scope)
	}
	return argosecrets.IsAddonLabelKey(key)
}

// systemLabelsOwnedAt reports whether the secret-type and managed-by labels
// are Sharko's to compare at this scope. Only on a connection Sharko owns.
func systemLabelsOwnedAt(scope Scope) bool {
	return scope == ScopeFull || scope == ScopeLimited
}

// connectivityCheckOwnedAt reports whether the connectivity-check label is
// Sharko's to compare at this scope.
//
// THE PLAN AND THE CODE DISAGREED HERE, AND THE CODE WON. The plan listed
// "Sharko-derived connection labels" as compared, full stop. In the code the
// connectivity-check label is derived per connection AND per ownership stance
// AND from a live server setting:
//
//   - createOne calls models.ApplyConnectivityCheckLabel, which stamps BOTH
//     the canonical sharko.dev/connectivity-check and the legacy
//     sharko.io/connectivity-check when the cluster has zero addons enabled,
//     and removes both when it has any.
//   - Whether the feature is on at all comes from
//     effectiveDisableConnectivityCheck: a static env flag OR the live
//     probe_mode server setting, either one turning it off.
//   - syncSelfManaged deliberately does not call it, with the comment "the
//     check label is never stamped on a connection Sharko does not own (guest
//     stance, same as adopted clusters)", and SyncLabelsOnly strips it
//     defensively. Ensure's adopted branch and TakeOverClusterSecret strip it
//     too.
//
// So the expected value is real and knowable — the caller reads the same live
// setting the writer would (Reconciler.ConnectivityCheckEnabled) and builds it
// through the same models.ApplyConnectivityCheckLabel — but only on a
// connection Sharko owns. At guest scope Sharko expects the label to be gone,
// and it is not Sharko's to compare.
func connectivityCheckOwnedAt(scope Scope) bool {
	return scope == ScopeFull || scope == ScopeLimited
}

// comparedAnnotationKeys is EMPTY, on purpose, and this comment is why.
//
// THE PLAN AND THE CODE DISAGREED HERE TOO, AND THE CODE WON. The plan said
// to compare "Sharko-owned provenance annotations that have a stable expected
// value". Reading every annotation Sharko actually writes onto a connection
// Secret, not one of them has a stable expected value:
//
//   - sharko.dev/written-at is the timestamp of the write. It changes on
//     every write by definition. A timestamp is not drift.
//   - sharko.dev/revision is the commit the write was built from. It changes
//     on every commit to the repo, whether or not anything about this cluster
//     changed.
//   - sharko.dev/source-file is the path the write was read from. It changes
//     when a repo moves between the v3 and v4 layouts, and it is omitted
//     entirely when a pass did not know the path — so an absent value is a
//     normal state, not a missing field.
//   - sharko.dev/adopted, sharko.dev/taken-over-at and
//     sharko.dev/takeover-preserved-labels are one-time records of something
//     that happened. Sharko has no ongoing expectation about them; the
//     comparison reads the adopted marker as a signal for classifying the
//     mode and never as a field to converge.
//
// So the honest compared-annotation set is empty. It is written as an explicit
// empty set with this reasoning, rather than left as an unstated absence, so
// that adding an annotation later is a decision somebody makes on purpose.
// TestComparedAnnotationsIsDeliberatelyEmpty pins it.
var comparedAnnotationKeys = []string{}

// ignoredAnnotationKeys are the provenance annotations that change on every
// write. Listed by name so the reasoning above is anchored to the real
// constants and a rename breaks the build rather than quietly widening the
// comparison.
var ignoredAnnotationKeys = []string{
	clusterreconciler.AnnotationWrittenAt,
	clusterreconciler.AnnotationRevision,
	clusterreconciler.AnnotationSourceFile,
	argosecrets.AnnotationAdopted,
	argosecrets.AnnotationAdoptedLegacy,
	argosecrets.AnnotationAdoptedDoubledPrefixLegacy,
	argosecrets.AnnotationTakenOverAt,
	argosecrets.AnnotationTakeoverPreservedLabels,
}

// Data keys Sharko writes. Everything else in a connection Secret's data
// belongs to whoever put it there and is never read, compared, or reported.
const (
	dataKeyName   = "name"
	dataKeyServer = "server"
	dataKeyConfig = "config"
)

// sensitiveDataKeys are the data keys that hold credential material. Their
// values are compared in memory and NEITHER SIDE is ever returned, logged, or
// counted: no value, no base64 of it, no hash of it, no length of it, no
// prefix of it, and no mask whose length tracks it.
//
// data.config is the whole credential blob — a bearer token, a client key, or
// the exec block that mints one. data.name and data.server are the cluster's
// own name and API address, which Sharko already returns from the cluster list
// and the connection detail endpoints, so they are not treated as secret here
// either.
var sensitiveDataKeys = map[string]bool{
	dataKeyConfig: true,
}

// ---------------------------------------------------------------------------
// Never compared
// ---------------------------------------------------------------------------

// volatileMetadataFields are the Kubernetes-owned fields that change without
// anybody touching the connection. They are named here for the record and for
// the guard test; the comparison never reaches for them at all, because it
// only ever reads the specific fields the policy above lists.
var volatileMetadataFields = []string{
	"metadata.resourceVersion",
	"metadata.uid",
	"metadata.generation",
	"metadata.creationTimestamp",
	"metadata.managedFields",
}

// labelSkipSet builds the set of label keys the comparison must leave alone on
// this particular connection, on top of the ownership rules.
//
// Right now that is the takeover-preserved labels. A takeover records the
// previous owner's label keys in sharko.dev/takeover-preserved-labels exactly
// so later actions have a list instead of a guess, and those keys are outside
// Sharko's scope by design. This matters more than it looks: a preserved key
// can be UNQUALIFIED (DropLabels' own refusal list shows bare legacy keys are
// the droppable ones), and an unqualified key is precisely what
// IsAddonLabelKey calls Sharko's own. Without this skip, every taken-over
// connection would report its previous owner's labels as unexpected drift.
func labelSkipSet(live *corev1.Secret) map[string]bool {
	if live == nil {
		return nil
	}
	keys := argosecrets.PreservedLabelKeys(live.Annotations)
	if len(keys) == 0 {
		return nil
	}
	skip := make(map[string]bool, len(keys))
	for _, k := range keys {
		skip[k] = true
	}
	return skip
}
