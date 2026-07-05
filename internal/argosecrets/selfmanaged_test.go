package argosecrets

import (
	"context"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/models"
)

// V2-cleanup-57.2 — self-managed connections (connectionManagedBy: user).
//
// Manager.SyncLabelsOnly is the shared label-only write primitive both
// reconcilers use for user-owned ArgoCD cluster Secrets. These tests pin:
//
//   - Missing Secret → (changed=false, found=false, nil) — pending, never
//     an error.
//   - Label merge: desired addon labels win, foreign labels kept,
//     Data/StringData/annotations byte-untouched.
//   - Guest stance: no managed-by, no secret-type, no connectivity-check
//     label is ever stamped; a lingering check label is stripped.
//   - Mode-switch handover: managed-by=sharko stripped on NON-adopted
//     Secrets; kept (with annotation) on adopted ones.
//   - Idempotence: converged labels → no write.
//
// And the legacy Reconciler's routing:
//
//   - connectionManagedBy: user → NO credentials fetch, NO Ensure — only
//     SyncLabelsOnly; a missing user Secret is not an error.

func newUserOwnedSecret(name string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
			Labels: map[string]string{
				LabelSecretType: "cluster",
				"team":          "payments",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"name":   []byte(name),
			"server": []byte("https://byo.example.com:6443"),
			"config": []byte(`{"bearerToken":"user-token","tlsClientConfig":{"insecure":false}}`),
		},
	}
}

func TestSyncLabelsOnly_MissingSecret_PendingNotError(t *testing.T) {
	client := fake.NewClientset()
	mgr := NewManager(client, testNamespace)

	changed, found, err := mgr.SyncLabelsOnly(context.Background(), "nope", map[string]string{"monitoring": "enabled"})
	if err != nil {
		t.Fatalf("missing Secret must not be an error: %v", err)
	}
	if found || changed {
		t.Fatalf("want (changed=false, found=false), got (%v, %v)", changed, found)
	}
}

func TestSyncLabelsOnly_MergesLabels_TouchesNothingElse(t *testing.T) {
	orig := newUserOwnedSecret("byo")
	client := fake.NewClientset(orig)
	mgr := NewManager(client, testNamespace)

	changed, found, err := mgr.SyncLabelsOnly(context.Background(), "byo", map[string]string{
		"monitoring": "enabled",
		"logging":    "disabled",
		// A caller that accidentally includes the check label must not get
		// it stamped — guest stance.
		models.LabelConnectivityCheck: "enabled",
	})
	if err != nil || !found || !changed {
		t.Fatalf("want (true, true, nil), got (%v, %v, %v)", changed, found, err)
	}

	got, _ := client.CoreV1().Secrets(testNamespace).Get(context.Background(), "byo", metav1.GetOptions{})
	if got.Labels["monitoring"] != "enabled" || got.Labels["logging"] != "disabled" {
		t.Fatalf("addon labels not merged: %v", got.Labels)
	}
	if got.Labels["team"] != "payments" || got.Labels[LabelSecretType] != "cluster" {
		t.Fatalf("foreign/user labels must be kept: %v", got.Labels)
	}
	if _, has := got.Labels[LabelManagedBy]; has {
		t.Fatalf("managed-by must NOT be stamped: %v", got.Labels)
	}
	if _, has := got.Labels[models.LabelConnectivityCheck]; has {
		t.Fatalf("connectivity-check must NOT be stamped: %v", got.Labels)
	}
	for k, want := range orig.Data {
		if string(got.Data[k]) != string(want) {
			t.Fatalf("Data[%q] mutated: %q", k, got.Data[k])
		}
	}
	if len(got.StringData) != 0 {
		t.Fatalf("StringData must stay empty: %v", got.StringData)
	}
	if len(got.Annotations) != 0 {
		t.Fatalf("annotations must stay untouched: %v", got.Annotations)
	}
}

func TestSyncLabelsOnly_Idempotent_NoWriteWhenConverged(t *testing.T) {
	client := fake.NewClientset(newUserOwnedSecret("byo"))
	mgr := NewManager(client, testNamespace)
	labels := map[string]string{"monitoring": "enabled"}

	if changed, _, err := mgr.SyncLabelsOnly(context.Background(), "byo", labels); err != nil || !changed {
		t.Fatalf("first sync should write: changed=%v err=%v", changed, err)
	}
	changed, found, err := mgr.SyncLabelsOnly(context.Background(), "byo", labels)
	if err != nil || !found {
		t.Fatalf("second sync: found=%v err=%v", found, err)
	}
	if changed {
		t.Fatal("second sync with converged labels must not write")
	}
}

func TestSyncLabelsOnly_Handover_StripsManagedBy_NonAdopted(t *testing.T) {
	s := newUserOwnedSecret("switched")
	s.Labels[LabelManagedBy] = ManagedByValue
	s.Labels[models.LabelConnectivityCheck] = "enabled" // lingering from the sharko-managed past
	client := fake.NewClientset(s)
	mgr := NewManager(client, testNamespace)

	changed, found, err := mgr.SyncLabelsOnly(context.Background(), "switched", map[string]string{"monitoring": "enabled"})
	if err != nil || !found || !changed {
		t.Fatalf("want (true, true, nil), got (%v, %v, %v)", changed, found, err)
	}
	got, _ := client.CoreV1().Secrets(testNamespace).Get(context.Background(), "switched", metav1.GetOptions{})
	if _, has := got.Labels[LabelManagedBy]; has {
		t.Fatalf("handover must strip managed-by: %v", got.Labels)
	}
	if _, has := got.Labels[models.LabelConnectivityCheck]; has {
		t.Fatalf("handover must strip the connectivity-check label: %v", got.Labels)
	}
}

func TestSyncLabelsOnly_AdoptedSecret_KeepsManagedByAndAnnotation(t *testing.T) {
	s := newUserOwnedSecret("adopted")
	s.Labels[LabelManagedBy] = ManagedByValue
	s.Annotations = map[string]string{AnnotationAdopted: "true"}
	client := fake.NewClientset(s)
	mgr := NewManager(client, testNamespace)

	if _, _, err := mgr.SyncLabelsOnly(context.Background(), "adopted", map[string]string{"monitoring": "enabled"}); err != nil {
		t.Fatalf("sync: %v", err)
	}
	got, _ := client.CoreV1().Secrets(testNamespace).Get(context.Background(), "adopted", metav1.GetOptions{})
	if got.Labels[LabelManagedBy] != ManagedByValue {
		t.Fatalf("adopted Secret must keep managed-by: %v", got.Labels)
	}
	if got.Annotations[AnnotationAdopted] != "true" {
		t.Fatalf("adopted annotation must survive: %v", got.Annotations)
	}
	if got.Labels["monitoring"] != "enabled" {
		t.Fatalf("addon labels must still sync: %v", got.Labels)
	}
}

func TestLegacyReconciler_SelfManaged_NoCredFetch_NoEnsure_LabelSyncOnly(t *testing.T) {
	const yaml = `
clusters:
  - name: byo
    connectionManagedBy: user
    labels:
      monitoring: "enabled"
`
	reader := &mockGitReader{files: map[string][]byte{clusterAddonsPath: []byte(yaml)}}
	// Global error: ANY credentials lookup explodes the reconcile with a
	// counted error. A clean run therefore proves no lookup happened.
	creds := &mockCredProvider{err: fmt.Errorf("secrets backend must not be consulted for self-managed clusters")}
	rec, _, client := newTestReconciler(func() GitReader { return reader }, creds)

	// User-created Secret already in place.
	if _, err := client.CoreV1().Secrets(testNamespace).Create(
		context.Background(), newUserOwnedSecret("byo"), metav1.CreateOptions{}); err != nil {
		t.Fatalf("seeding user Secret: %v", err)
	}

	rec.ReconcileOnce(context.Background())

	stats := rec.GetStats()
	if stats.Errors != 0 {
		t.Fatalf("self-managed reconcile must not error (creds lookup attempted?): %+v, errors=%v", stats, rec.GetErrors())
	}
	got, err := client.CoreV1().Secrets(testNamespace).Get(context.Background(), "byo", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Labels["monitoring"] != "enabled" {
		t.Fatalf("labels not synced: %v", got.Labels)
	}
	// Ensure was NOT taken: the config payload the user wrote is untouched
	// and no managed-by ownership label appeared.
	if string(got.Data["config"]) != `{"bearerToken":"user-token","tlsClientConfig":{"insecure":false}}` {
		t.Fatalf("connection config mutated: %s", got.Data["config"])
	}
	if _, has := got.Labels[LabelManagedBy]; has {
		t.Fatalf("managed-by stamped on user Secret: %v", got.Labels)
	}
}

func TestLegacyReconciler_SelfManaged_MissingSecret_NotAnError(t *testing.T) {
	const yaml = `
clusters:
  - name: byo-waiting
    connectionManagedBy: user
    labels:
      monitoring: "enabled"
`
	reader := &mockGitReader{files: map[string][]byte{clusterAddonsPath: []byte(yaml)}}
	creds := &mockCredProvider{err: fmt.Errorf("must not be consulted")}
	rec, _, client := newTestReconciler(func() GitReader { return reader }, creds)

	rec.ReconcileOnce(context.Background())

	stats := rec.GetStats()
	if stats.Errors != 0 {
		t.Fatalf("missing user Secret must be a wait, not an error: %+v errors=%v", stats, rec.GetErrors())
	}
	// Nothing was created on the user's behalf.
	list, _ := client.CoreV1().Secrets(testNamespace).List(context.Background(), metav1.ListOptions{})
	if len(list.Items) != 0 {
		t.Fatalf("no Secret may be created for a self-managed cluster: %d found", len(list.Items))
	}
}
