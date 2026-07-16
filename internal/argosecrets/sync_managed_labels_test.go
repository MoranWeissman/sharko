package argosecrets

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestIsAddonLabelKey(t *testing.T) {
	cases := map[string]bool{
		"datadog":                       true,  // bare addon name
		"datadog-version":               true,  // version-override key
		"addon-foo":                     true,  // test-style bare key
		"":                              false, // empty is never an addon key
		LabelManagedBy:                  false, // app.kubernetes.io/managed-by
		LabelSecretType:                 false, // argocd.argoproj.io/secret-type
		LabelAppInstance:                false, // app.kubernetes.io/instance
		"sharko.dev/connectivity-check": false, // derived
		"example.com/team":              false, // foreign qualified
	}
	for k, want := range cases {
		if got := IsAddonLabelKey(k); got != want {
			t.Errorf("IsAddonLabelKey(%q) = %v, want %v", k, got, want)
		}
	}
}

// TestSyncManagedClusterLabels_Managed_Converges verifies the managed
// (non-adopted) path: add git-desired addon keys, DELETE removed-in-git addon
// keys, PRESERVE + defensively re-apply managed-by + secret-type, and never
// touch foreign labels / Data / annotations.
func TestSyncManagedClusterLabels_Managed_Converges(t *testing.T) {
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "c1",
			Namespace: testNamespace,
			Labels: map[string]string{
				LabelManagedBy:      ManagedByValue,
				LabelSecretType:     "cluster",
				"example.com/owner": "team-x",  // foreign — untouched
				"addon-old":         "enabled", // removed-in-git — deleted
			},
			Annotations: map[string]string{"note": "keep"},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{"config": []byte("secret-data")},
	}
	client := fake.NewSimpleClientset(existing)
	mgr := NewManager(client, testNamespace)

	res, err := mgr.SyncManagedClusterLabels(context.Background(), "c1", map[string]string{"addon-foo": "enabled"})
	if err != nil {
		t.Fatalf("SyncManagedClusterLabels: %v", err)
	}
	if !res.Found || !res.Changed || res.Adopted {
		t.Fatalf("unexpected result: %+v", res)
	}

	got, _ := client.CoreV1().Secrets(testNamespace).Get(context.Background(), "c1", metav1.GetOptions{})
	if got.Labels["addon-foo"] != "enabled" {
		t.Error("addon-foo not added")
	}
	if _, has := got.Labels["addon-old"]; has {
		t.Error("addon-old not deleted (convergence failed)")
	}
	if got.Labels[LabelManagedBy] != ManagedByValue {
		t.Error("managed-by not preserved")
	}
	if got.Labels[LabelSecretType] != "cluster" {
		t.Error("secret-type not preserved")
	}
	if got.Labels["example.com/owner"] != "team-x" {
		t.Error("foreign label mutated")
	}
	if got.Annotations["note"] != "keep" {
		t.Error("annotation mutated")
	}
	if string(got.Data["config"]) != "secret-data" {
		t.Error("Data mutated")
	}
}

// TestSyncManagedClusterLabels_Managed_ReAppliesLostOwnership verifies the
// recovery property: a managed Secret that previously LOST its managed-by
// label (the exact data-loss bug) gets it RE-APPLIED by the heal.
func TestSyncManagedClusterLabels_Managed_ReAppliesLostOwnership(t *testing.T) {
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "c1",
			Namespace: testNamespace,
			Labels: map[string]string{
				LabelSecretType: "cluster",
				// managed-by MISSING — simulate the prior strip bug.
				// No adopted annotation → treated as managed.
			},
		},
		Type: corev1.SecretTypeOpaque,
	}
	client := fake.NewSimpleClientset(existing)
	mgr := NewManager(client, testNamespace)

	res, err := mgr.SyncManagedClusterLabels(context.Background(), "c1", map[string]string{"addon-foo": "enabled"})
	if err != nil {
		t.Fatalf("SyncManagedClusterLabels: %v", err)
	}
	if res.Adopted {
		t.Fatal("secret without adopted annotation must be treated as managed")
	}
	got, _ := client.CoreV1().Secrets(testNamespace).Get(context.Background(), "c1", metav1.GetOptions{})
	if got.Labels[LabelManagedBy] != ManagedByValue {
		t.Error("managed-by should be RE-APPLIED on a managed secret that lost it")
	}
}

// TestSyncManagedClusterLabels_Adopted verifies the adopted path: converge
// only addon keys, NEVER stamp ownership beyond what already exists, and leave
// the other owner's labels/Data/annotations alone.
func TestSyncManagedClusterLabels_Adopted(t *testing.T) {
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "adopted",
			Namespace: testNamespace,
			Labels: map[string]string{
				LabelSecretType:  "cluster",
				LabelAppInstance: "other-app", // the other owner's tracking label
				"addon-stale":    "enabled",   // removed-in-git — deleted
				// intentionally NO managed-by to prove adopted path does not add it
			},
			Annotations: map[string]string{AnnotationAdopted: "true"},
		},
		Type: corev1.SecretTypeOpaque,
	}
	client := fake.NewSimpleClientset(existing)
	mgr := NewManager(client, testNamespace)

	res, err := mgr.SyncManagedClusterLabels(context.Background(), "adopted", map[string]string{"addon-foo": "enabled"})
	if err != nil {
		t.Fatalf("SyncManagedClusterLabels: %v", err)
	}
	if !res.Adopted {
		t.Fatal("secret with adopted annotation must be treated as adopted")
	}
	got, _ := client.CoreV1().Secrets(testNamespace).Get(context.Background(), "adopted", metav1.GetOptions{})
	if got.Labels["addon-foo"] != "enabled" {
		t.Error("addon-foo not added on adopted secret")
	}
	if _, has := got.Labels["addon-stale"]; has {
		t.Error("addon-stale not deleted on adopted secret")
	}
	if got.Labels[LabelAppInstance] != "other-app" {
		t.Error("other owner's app-instance label mutated")
	}
	if _, has := got.Labels[LabelManagedBy]; has {
		t.Error("adopted path must NOT stamp managed-by ownership label")
	}
}

// TestSyncManagedClusterLabels_NoChurn verifies no K8s write when already
// converged.
func TestSyncManagedClusterLabels_NoChurn(t *testing.T) {
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "c1",
			Namespace: testNamespace,
			Labels: map[string]string{
				LabelManagedBy:  ManagedByValue,
				LabelSecretType: "cluster",
				"addon-foo":     "enabled",
			},
		},
		Type: corev1.SecretTypeOpaque,
	}
	client := fake.NewSimpleClientset(existing)
	mgr := NewManager(client, testNamespace)

	res, err := mgr.SyncManagedClusterLabels(context.Background(), "c1", map[string]string{"addon-foo": "enabled"})
	if err != nil {
		t.Fatalf("SyncManagedClusterLabels: %v", err)
	}
	if !res.Found || res.Changed {
		t.Errorf("expected Found && !Changed (no churn), got %+v", res)
	}
}

// TestSyncManagedClusterLabels_NotFound verifies the not-found contract.
func TestSyncManagedClusterLabels_NotFound(t *testing.T) {
	client := fake.NewSimpleClientset()
	mgr := NewManager(client, testNamespace)
	res, err := mgr.SyncManagedClusterLabels(context.Background(), "missing", map[string]string{"addon-foo": "enabled"})
	if err != nil {
		t.Fatalf("not-found must not be an error: %v", err)
	}
	if res.Found {
		t.Error("expected Found=false for a missing secret")
	}
}

// TestSyncManagedClusterLabels_LegacyValueNormalized verifies "true"/"false"
// addon values converge to the canonical vocabulary.
func TestSyncManagedClusterLabels_LegacyValueNormalized(t *testing.T) {
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "c1",
			Namespace: testNamespace,
			Labels:    map[string]string{LabelManagedBy: ManagedByValue, LabelSecretType: "cluster"},
		},
		Type: corev1.SecretTypeOpaque,
	}
	client := fake.NewSimpleClientset(existing)
	mgr := NewManager(client, testNamespace)

	if _, err := mgr.SyncManagedClusterLabels(context.Background(), "c1", map[string]string{"addon-foo": "true"}); err != nil {
		t.Fatalf("SyncManagedClusterLabels: %v", err)
	}
	got, _ := client.CoreV1().Secrets(testNamespace).Get(context.Background(), "c1", metav1.GetOptions{})
	if got.Labels["addon-foo"] != "enabled" {
		t.Errorf("legacy 'true' should normalize to 'enabled', got %q", got.Labels["addon-foo"])
	}
}
