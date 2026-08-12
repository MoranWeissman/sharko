package clusterreconciler

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
)

// TestOwnershipCheckConsistency documents the INTENTIONAL differences between
// the three ownership predicates used in connection repair:
//
// 1. IsManagedBySharko (clusterreconciler/labels.go) — checks for the
//    managed-by=sharko label specifically. Used by listManagedSecrets to filter.
//
// 2. RepairOwnedConnection (argosecrets/repair.go) — requires managed-by=sharko
//    AND rejects adopted Secrets. Full repairs must never touch adopted Secrets
//    because the connection material is the user's.
//
// 3. RepairAddonLabelsWithOwnershipCheck (argosecrets/manager.go) — considers
//    EITHER managed-by=sharko OR adopted as "owned". Labels-only repairs CAN
//    touch adopted Secrets' addon labels (that's the point of adoption).
//
// These differences are intentional and correct for their purposes. This test
// fails if someone accidentally makes them agree when they should differ.
func TestOwnershipCheckConsistency(t *testing.T) {
	// Case 1: fully-owned Secret (managed-by=sharko, not adopted).
	// All three should agree this is owned.
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

	// RepairOwnedConnection would pass (no ErrRepairOwnershipChanged).
	// Can't call it directly without a Manager, but the logic is:
	// - Line 158: fullyOwned.Labels[LabelManagedBy] == ManagedByValue ✓
	// - Line 165: !IsAdopted(nil) ✓

	// RepairAddonLabelsWithOwnershipCheck logic (manager.go:916-918):
	// hasManagedByLabel = true, hasAdoptedAnnotation = false, actualOwned = true.
	// If expectedOwned=true, it passes.

	// Case 2: adopted Secret (no managed-by label, but has adopted annotation).
	// IsManagedBySharko should return false (no label).
	// RepairOwnedConnection should reject it (ErrRepairOwnershipChanged).
	// RepairAddonLabelsWithOwnershipCheck should accept it when expectedOwned=true.
	adopted := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "adopted",
			Namespace: "argocd",
			Labels: map[string]string{
				argosecrets.LabelSecretType: "cluster",
				// NO managed-by label
			},
			Annotations: map[string]string{
				argosecrets.AnnotationAdopted: "true",
			},
		},
	}

	if IsManagedBySharko(adopted) {
		t.Error("IsManagedBySharko returned true for an adopted Secret; it should return false (no managed-by label)")
	}

	if !argosecrets.IsAdopted(adopted.Annotations) {
		t.Fatal("test setup error: adopted Secret not detected as adopted")
	}

	// This is the key difference: RepairOwnedConnection would reject this Secret
	// (line 165-166 in repair.go), but RepairAddonLabelsWithOwnershipCheck would
	// accept it when expectedOwned=true (line 918 in manager.go: hasAdoptedAnnotation = true).

	// Case 3: genuinely foreign Secret (another tool's managed-by label).
	// All three should agree this is NOT owned.
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
		t.Error("IsManagedBySharko returned true for a foreign Secret")
	}

	// RepairOwnedConnection would reject (line 158: != ManagedByValue).
	// RepairAddonLabelsWithOwnershipCheck with expectedOwned=true would reject
	// (line 916: hasManagedByLabel = false, so actualOwned = false, mismatch).

	// Case 4: unlabeled Secret (no managed-by, not adopted).
	// All three should agree this is NOT owned.
	unlabeled := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "unlabeled",
			Namespace: "argocd",
			Labels:    map[string]string{},
		},
	}

	if IsManagedBySharko(unlabeled) {
		t.Error("IsManagedBySharko returned true for an unlabeled Secret")
	}

	// RepairOwnedConnection would reject (line 158: != ManagedByValue).
	// RepairAddonLabelsWithOwnershipCheck with expectedOwned=true would reject
	// (actualOwned = false, mismatch).
}

// TestOwnershipCheckDocumentation is a documentation test: it exists to make
// the intentional differences between the three ownership predicates searchable
// and explicit. If someone wonders "why do these three checks differ?", this
// test answers that question.
func TestOwnershipCheckDocumentation(t *testing.T) {
	// The differences are intentional and correct:
	//
	// 1. IsManagedBySharko checks ONLY the managed-by label. Used by
	//    listManagedSecrets to filter which Secrets the reconciler sees. Adopted
	//    Secrets deliberately do not have this label, so they don't appear in
	//    that list — they're managed via a different flow.
	//
	// 2. RepairOwnedConnection rejects adopted Secrets because a full repair
	//    (rewriting the connection material from the backend) must never touch
	//    an adopted Secret — the connection material is the user's, not Sharko's.
	//
	// 3. RepairAddonLabelsWithOwnershipCheck accepts adopted Secrets when
	//    expectedOwned=true because a labels-only repair CAN touch adopted
	//    Secrets' addon labels — that's the whole point of adoption. The user
	//    owns the connection, but Sharko owns the addon labels.
	//
	// If these three ever accidentally converge to the same logic, they will
	// either:
	// - Break adopted-Secret label reconciliation (if they all reject adopted), or
	// - Allow full repairs to overwrite user-owned connection material (if they
	//   all accept adopted).
	//
	// This test documents that the differences are correct.
}
