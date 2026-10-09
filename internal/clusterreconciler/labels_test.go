package clusterreconciler

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestIsManagedBySharko_NilSecret_False(t *testing.T) {
	t.Parallel()
	if IsManagedBySharko(nil) {
		t.Fatal("IsManagedBySharko(nil) = true, want false")
	}
}

func TestIsManagedBySharko_NoLabels_False(t *testing.T) {
	t.Parallel()
	s := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "x"}}
	if IsManagedBySharko(s) {
		t.Fatal("IsManagedBySharko(no-labels) = true, want false")
	}
}

func TestIsManagedBySharko_WrongValue_False(t *testing.T) {
	t.Parallel()
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "x",
			Labels: map[string]string{
				LabelManagedBy: "argocd",
			},
		},
	}
	if IsManagedBySharko(s) {
		t.Fatal("IsManagedBySharko(label=argocd) = true, want false")
	}
}

func TestIsManagedBySharko_WrongKey_False(t *testing.T) {
	t.Parallel()
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "x",
			Labels: map[string]string{
				"other.example.com/managed-by": LabelValueSharko,
			},
		},
	}
	if IsManagedBySharko(s) {
		t.Fatal("IsManagedBySharko(wrong-key) = true, want false")
	}
}

func TestIsManagedBySharko_LabeledCorrectly_True(t *testing.T) {
	t.Parallel()
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "x",
			Labels: map[string]string{
				LabelManagedBy: LabelValueSharko,
			},
		},
	}
	if !IsManagedBySharko(s) {
		t.Fatal("IsManagedBySharko(labeled-sharko) = false, want true")
	}
}

func TestApplyManagedBySharkoLabel_NilSecret_NoPanic(t *testing.T) {
	t.Parallel()
	// Should not panic.
	ApplyManagedBySharkoLabel(nil)
}

func TestApplyManagedBySharkoLabel_NoLabelsMap_InitializesAndSets(t *testing.T) {
	t.Parallel()
	s := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "x"}}
	ApplyManagedBySharkoLabel(s)
	if s.Labels == nil {
		t.Fatal("Labels map not initialized")
	}
	if got := s.Labels[LabelManagedBy]; got != LabelValueSharko {
		t.Fatalf("Labels[%s] = %q, want %q", LabelManagedBy, got, LabelValueSharko)
	}
	if !IsManagedBySharko(s) {
		t.Fatal("IsManagedBySharko after ApplyManagedBySharkoLabel = false")
	}
}

func TestApplyManagedBySharkoLabel_ExistingLabelsMap_AddsLabel(t *testing.T) {
	t.Parallel()
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "x",
			Labels: map[string]string{
				"keep.example.com/this": "yes",
			},
		},
	}
	ApplyManagedBySharkoLabel(s)
	if got := s.Labels["keep.example.com/this"]; got != "yes" {
		t.Fatalf("pre-existing label clobbered: got %q, want %q", got, "yes")
	}
	if got := s.Labels[LabelManagedBy]; got != LabelValueSharko {
		t.Fatalf("Labels[%s] = %q, want %q", LabelManagedBy, got, LabelValueSharko)
	}
}

func TestApplyManagedBySharkoLabel_AlreadyLabeled_Idempotent(t *testing.T) {
	t.Parallel()
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "x",
			Labels: map[string]string{
				LabelManagedBy: LabelValueSharko,
			},
		},
	}
	ApplyManagedBySharkoLabel(s)
	ApplyManagedBySharkoLabel(s)
	if got := s.Labels[LabelManagedBy]; got != LabelValueSharko {
		t.Fatalf("Labels[%s] = %q, want %q", LabelManagedBy, got, LabelValueSharko)
	}
	if len(s.Labels) != 1 {
		t.Fatalf("expected 1 label, got %d (%v)", len(s.Labels), s.Labels)
	}
}

func TestApplyManagedBySharkoLabel_DifferentValue_Overwrites(t *testing.T) {
	t.Parallel()
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "x",
			Labels: map[string]string{
				LabelManagedBy: "argocd",
			},
		},
	}
	ApplyManagedBySharkoLabel(s)
	if got := s.Labels[LabelManagedBy]; got != LabelValueSharko {
		t.Fatalf("Labels[%s] = %q, want %q (Sharko must take over its own key)", LabelManagedBy, got, LabelValueSharko)
	}
}
