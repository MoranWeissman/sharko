package clusterreconciler

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
)

// TestOwnershipCheckConsistency CALLS all three ownership predicates used in
// connection repair, for every case it describes, and asserts the real answer
// from each.
//
// The three, and how they differ on purpose:
//
//  1. IsManagedBySharko (clusterreconciler/labels.go) — the managed-by=sharko
//     label and nothing else. It is a FILTER: listManagedSecrets uses it to
//     decide which Secrets the reconciler even sees. Adopted Secrets do not
//     carry that label, so they are deliberately invisible to it.
//
//  2. RepairOwnedConnection (argosecrets/repair.go) — needs managed-by=sharko
//     AND refuses an adopted Secret. A full repair rewrites the connection
//     material from the secrets backend, and on an adopted Secret that material
//     is the user's.
//
//  3. RepairAddonLabelsWithOwnershipCheck (argosecrets/manager.go) — counts
//     EITHER managed-by=sharko OR adopted as owned, because a labels-only repair
//     may converge an adopted Secret's addon labels. That is the whole point of
//     adoption: the user keeps the connection, Sharko keeps the addon labels.
//
// The differences are correct. This test's job is to FAIL if somebody makes the
// three agree — and it can only do that job by calling all three, which is why
// the prose that used to stand in for the third call is gone. A comment saying
// what a function would do proves nothing about what it does.
func TestOwnershipCheckConsistency(t *testing.T) {
	pinnedLabels := map[string]string{"datadog": "enabled"}

	// A desired Secret for the full-repair calls. It has to be built the way
	// production builds it, because RepairOwnedConnection takes the already-built
	// object; the values in it are made up and are not shaped like real ones.
	desiredFor := func(t *testing.T, name string) *corev1.Secret {
		t.Helper()
		built, err := argosecrets.BuildClusterSecret(argosecrets.ClusterSecretSpec{
			Name:   name,
			Server: "https://" + name + ".test.invalid",
			Token:  "made-up-not-a-real-token",
			Labels: map[string]string{"datadog": "enabled"},
		}, "argocd")
		if err != nil {
			t.Fatalf("building the desired Secret for %q: %v", name, err)
		}
		return built
	}

	// Case 1: fully owned — managed-by=sharko, not adopted.
	// All three agree this is Sharko's.
	fullyOwned := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "fully-owned",
			Namespace: "argocd",
			Labels: map[string]string{
				argosecrets.LabelManagedBy: argosecrets.ManagedByValue,
			},
		},
	}

	if !IsManagedBySharko(fullyOwned) {
		t.Error("IsManagedBySharko returned false for a fully-owned Secret")
	}

	client1 := fake.NewSimpleClientset(fullyOwned)
	mgr1 := argosecrets.NewManager(client1, "argocd")
	if _, err := mgr1.RepairOwnedConnection(context.Background(), desiredFor(t, "fully-owned"), nil); err != nil {
		t.Errorf("RepairOwnedConnection refused a fully-owned Secret: %v — a full repair is exactly what this state is for", err)
	}
	if _, _, err := mgr1.RepairAddonLabelsWithOwnershipCheck(context.Background(), "fully-owned", pinnedLabels, true); err != nil {
		t.Errorf("RepairAddonLabelsWithOwnershipCheck refused a fully-owned Secret with expectedOwned=true: %v", err)
	}

	// Case 2: adopted. THIS IS THE CASE THE PRIMITIVES DELIBERATELY DISAGREE ON,
	// so it is the case that has to be right, and it comes in two shapes.
	//
	// THE SHAPE SHARKO ACTUALLY WRITES carries BOTH markers: managed-by=sharko
	// AND the adopted annotation. Ensure's adoption branch stamps both (via
	// buildLabels' system labels and the annotation right after), TakeOverCluster
	// Secret stamps both, and the reconciler's orphan sweep depends on it —
	// listManagedSecrets filters on managed-by=sharko, and the adopted-skip check
	// runs over the Secrets that filter returned, so an adopted Secret with no
	// managed-by label would never reach that skip and would be swept away. The
	// package doc says the same: "Sharko-ADOPTED secrets (managed-by label +
	// adopted annotation)".
	//
	// So on the real shape IsManagedBySharko answers TRUE. The version of this
	// test before R3-19 asserted it answers FALSE, on a fixture with no
	// managed-by label — a shape Sharko does not write. It was pinning a claim
	// about production that is not true of production, and it went unnoticed
	// because the third predicate was described in a comment instead of called.
	adoptedRealShape := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "adopted",
			Namespace: "argocd",
			Labels: map[string]string{
				argosecrets.LabelManagedBy:  argosecrets.ManagedByValue,
				argosecrets.LabelSecretType: "cluster",
			},
			Annotations: map[string]string{
				argosecrets.AnnotationAdopted: "true",
			},
		},
	}

	if !IsManagedBySharko(adoptedRealShape) {
		t.Error(`IsManagedBySharko returned false for an adopted Secret in the shape Sharko writes (managed-by=sharko PLUS the adopted annotation).

It must answer true. The reconciler's orphan sweep only sees Secrets that filter returns, and its adopted-skip check runs over that result — if this answered false, an adopted cluster would never reach the skip and would be deleted.`)
	}
	if !argosecrets.IsAdopted(adoptedRealShape.Annotations) {
		t.Fatal("test setup error: the adopted fixture is not detected as adopted")
	}

	client2 := fake.NewSimpleClientset(adoptedRealShape)
	mgr2 := argosecrets.NewManager(client2, "argocd")
	if _, err := mgr2.RepairOwnedConnection(context.Background(), desiredFor(t, "adopted"), nil); !errors.Is(err, argosecrets.ErrRepairOwnershipChanged) {
		t.Errorf(`RepairOwnedConnection on an adopted Secret returned err = %v, want ErrRepairOwnershipChanged.

A full repair rewrites the connection material from the secrets backend, and on an adopted Secret that material is the USER'S. This refusal is the difference from the labels-only primitive, and it is correct. Note the managed-by label is present and Sharko's — the adopted annotation is what refuses this, and only that.`, err)
	}
	if _, _, err := mgr2.RepairAddonLabelsWithOwnershipCheck(context.Background(), "adopted", pinnedLabels, true); err != nil {
		t.Errorf(`RepairAddonLabelsWithOwnershipCheck refused an adopted Secret with expectedOwned=true: %v.

Adopted Secrets ARE owned for labels-only purposes — that is what adoption means. If this and RepairOwnedConnection ever give the same answer here, one of them is wrong.`, err)
	}

	// The second adopted shape: the annotation with NO managed-by label. Sharko
	// does not write this, but it can exist — somebody strips the label by hand,
	// or a Secret adopted long ago lost it. Both repair primitives must still
	// answer, and they still differ: the labels-only one treats the annotation
	// alone as owned (that is its rule), the full one still refuses.
	adoptedLabelStripped := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "adopted-label-stripped",
			Namespace: "argocd",
			Labels: map[string]string{
				argosecrets.LabelSecretType: "cluster",
			},
			Annotations: map[string]string{
				argosecrets.AnnotationAdopted: "true",
			},
		},
	}

	if IsManagedBySharko(adoptedLabelStripped) {
		t.Error("IsManagedBySharko returned true for a Secret with no managed-by label; that predicate is the label and only the label")
	}

	client2b := fake.NewSimpleClientset(adoptedLabelStripped)
	mgr2b := argosecrets.NewManager(client2b, "argocd")
	if _, err := mgr2b.RepairOwnedConnection(context.Background(), desiredFor(t, "adopted-label-stripped"), nil); !errors.Is(err, argosecrets.ErrRepairOwnershipChanged) {
		t.Errorf("RepairOwnedConnection on an adopted Secret whose managed-by label is gone returned err = %v, want ErrRepairOwnershipChanged", err)
	}
	if _, _, err := mgr2b.RepairAddonLabelsWithOwnershipCheck(context.Background(), "adopted-label-stripped", pinnedLabels, true); err != nil {
		t.Errorf("RepairAddonLabelsWithOwnershipCheck refused an adopted Secret whose managed-by label is gone: %v — the adopted annotation alone counts as owned on this path", err)
	}

	// Case 3: genuinely foreign — another tool's managed-by label.
	// All three agree this is not Sharko's.
	foreign := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "foreign",
			Namespace: "argocd",
			Labels: map[string]string{
				argosecrets.LabelManagedBy: "external-tool",
			},
		},
	}

	if IsManagedBySharko(foreign) {
		t.Error("IsManagedBySharko returned true for a Secret another tool owns")
	}

	client3 := fake.NewSimpleClientset(foreign)
	mgr3 := argosecrets.NewManager(client3, "argocd")
	if _, err := mgr3.RepairOwnedConnection(context.Background(), desiredFor(t, "foreign"), nil); !errors.Is(err, argosecrets.ErrRepairOwnershipChanged) {
		t.Errorf("RepairOwnedConnection on another tool's Secret returned err = %v, want ErrRepairOwnershipChanged", err)
	}
	// Both expected modes, because a foreign owner is refused whatever the
	// caller classified — expectedOwned gets no say (R3-15).
	for _, expectedOwned := range []bool{true, false} {
		if _, _, err := mgr3.RepairAddonLabelsWithOwnershipCheck(context.Background(), "foreign", pinnedLabels, expectedOwned); !errors.Is(err, argosecrets.ErrRepairOwnershipChanged) {
			t.Errorf("RepairAddonLabelsWithOwnershipCheck with expectedOwned=%v accepted another tool's Secret: err = %v, want ErrRepairOwnershipChanged", expectedOwned, err)
		}
	}

	// Case 4: unlabelled — no managed-by, not adopted. Adopt territory.
	// The filter and the full repair both say "not Sharko's". The labels-only
	// primitive says the same at expectedOwned=true, and this is the one state
	// where it legitimately WRITES: at expectedOwned=false it is the guest
	// arrangement, where the connection is the user's and the addon labels are
	// Sharko's.
	unlabeled := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "unlabeled",
			Namespace: "argocd",
			Labels:    map[string]string{},
		},
	}

	if IsManagedBySharko(unlabeled) {
		t.Error("IsManagedBySharko returned true for an unlabelled Secret")
	}

	client4 := fake.NewSimpleClientset(unlabeled)
	mgr4 := argosecrets.NewManager(client4, "argocd")
	if _, err := mgr4.RepairOwnedConnection(context.Background(), desiredFor(t, "unlabeled"), nil); !errors.Is(err, argosecrets.ErrRepairOwnershipChanged) {
		t.Errorf(`RepairOwnedConnection on an unlabelled Secret returned err = %v, want ErrRepairOwnershipChanged.

An unlabelled Secret is Adopt territory. Overwriting one would take over something Sharko was never given.`, err)
	}
	if _, _, err := mgr4.RepairAddonLabelsWithOwnershipCheck(context.Background(), "unlabeled", pinnedLabels, true); !errors.Is(err, argosecrets.ErrRepairOwnershipChanged) {
		t.Errorf("RepairAddonLabelsWithOwnershipCheck with expectedOwned=true accepted an unlabelled Secret: err = %v, want ErrRepairOwnershipChanged", err)
	}

	client5 := fake.NewSimpleClientset(unlabeled.DeepCopy())
	mgr5 := argosecrets.NewManager(client5, "argocd")
	if _, _, err := mgr5.RepairAddonLabelsWithOwnershipCheck(context.Background(), "unlabeled", pinnedLabels, false); err != nil {
		t.Errorf(`RepairAddonLabelsWithOwnershipCheck with expectedOwned=false refused an unlabelled Secret: %v.

An unlabelled connection with a guest-scope request is the legitimate guest arrangement, and it is the one state where a labels-only repair writes while a full repair refuses. Refusing it here would mean the two primitives agree on a case where they must differ.`, err)
	}
}

// TestOwnershipPredicatesDoNotAgreeOnAdoptedSecrets is the difference itself,
// stated as one assertion rather than as prose.
//
// It replaces a "documentation test" whose entire body was a comment block and
// which asserted nothing at all — it passed for ever, whatever the code did. The
// explanation that used to live in it is on TestOwnershipCheckConsistency above,
// where it belongs: on a test that can fail.
//
// What this pins: on an ADOPTED Secret the full-connection repair refuses and
// the labels-only repair accepts. If somebody makes the two agree here, one of
// two real things has broken — either adopted clusters stop getting their addon
// labels converged, or a full repair starts overwriting connection material that
// belongs to the user.
//
// The fixture is the shape Sharko really writes: managed-by=sharko AND the
// adopted annotation. That matters for what this test can catch. With no
// managed-by label, the full repair refuses on the MISSING LABEL and never
// reaches its adopted check, so deleting that check leaves this test passing —
// the refusal it saw came from a different line. With the label present, the
// adopted check is the only thing refusing, so removing it makes the two agree
// and this test fails. That is the difference between a test that watches the
// rule and a test that happens to watch a different rule.
func TestOwnershipPredicatesDoNotAgreeOnAdoptedSecrets(t *testing.T) {
	adopted := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "adopted-difference",
			Namespace: "argocd",
			Labels: map[string]string{
				argosecrets.LabelManagedBy:  argosecrets.ManagedByValue,
				argosecrets.LabelSecretType: "cluster",
			},
			Annotations: map[string]string{argosecrets.AnnotationAdopted: "true"},
		},
	}
	desired, err := argosecrets.BuildClusterSecret(argosecrets.ClusterSecretSpec{
		Name:   "adopted-difference",
		Server: "https://adopted-difference.test.invalid",
		Token:  "made-up-not-a-real-token",
		Labels: map[string]string{"datadog": "enabled"},
	}, "argocd")
	if err != nil {
		t.Fatalf("building the desired Secret: %v", err)
	}

	clientFull := fake.NewSimpleClientset(adopted)
	fullRefused := errors.Is(
		mustErr(argosecrets.NewManager(clientFull, "argocd").RepairOwnedConnection(context.Background(), desired, nil)),
		argosecrets.ErrRepairOwnershipChanged)

	clientLabels := fake.NewSimpleClientset(adopted.DeepCopy())
	_, _, labelsErr := argosecrets.NewManager(clientLabels, "argocd").
		RepairAddonLabelsWithOwnershipCheck(context.Background(), "adopted-difference", map[string]string{"datadog": "enabled"}, true)
	labelsAccepted := labelsErr == nil

	if !fullRefused || !labelsAccepted {
		t.Errorf(`on an adopted Secret the two repair primitives must DISAGREE: the full repair refuses, the labels-only repair accepts.
Got: full repair refused = %v, labels-only repair accepted = %v (its error: %v).

If they now agree, either adopted clusters have stopped getting their addon labels converged, or a full repair has started overwriting connection material that belongs to the user.`,
			fullRefused, labelsAccepted, labelsErr)
	}
}

// mustErr drops a RepairOutcome and keeps the error, so a one-line errors.Is
// stays readable.
func mustErr(_ argosecrets.RepairOutcome, err error) error { return err }
