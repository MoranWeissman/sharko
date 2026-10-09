package secrets

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// countingSecretProvider counts how many values were pulled out of the
// secrets store, so a test can prove Sharko does not fetch a value it has
// already decided it will never write.
type countingSecretProvider struct {
	values map[string][]byte
	calls  int
}

func (p *countingSecretProvider) GetSecretValue(_ context.Context, path string) ([]byte, error) {
	p.calls++
	val, ok := p.values[path]
	if !ok {
		return nil, errors.New("secret not found: " + path)
	}
	return val, nil
}

// ownership_gate_test.go — P1-A A1. Sharko reconciles only what it created.
//
// The bug these tests lock out: EnsureSecret used to Get the live Secret,
// replace its .Data, stamp app.kubernetes.io/managed-by=sharko on it and
// Update — no ownership question asked. A Secret created by a person, by
// External Secrets Operator, by Sealed Secrets or by a Helm chart was
// silently taken over every five minutes.
//
// Fixtures come from reconciler_test.go: the catalog defines addon
// "datadog" wanting Secret "datadog-secret" in namespace "monitoring", and
// cluster "prod-cluster" has that addon enabled.

// foreignSecret builds a Secret that exists on the cluster with the name
// Sharko wants, carrying somebody else's ownership label.
func foreignSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "datadog-secret",
			Namespace: "monitoring",
			Labels:    map[string]string{"app.kubernetes.io/managed-by": "external-secrets"},
		},
		Data: map[string][]byte{"api-key": []byte("not-sharkos-value")},
		Type: corev1.SecretTypeOpaque,
	}
}

// writeActions returns every mutating action recorded on a fake clientset.
// Get and List are reads and are deliberately not counted.
func writeActions(client *fake.Clientset) []string {
	var out []string
	for _, a := range client.Actions() {
		switch a.GetVerb() {
		case "create", "update", "patch", "delete", "delete-collection":
			out = append(out, a.GetVerb()+" "+a.GetResource().Resource)
		}
	}
	return out
}

func foreignGateReconciler(client *fake.Clientset) *Reconciler {
	return newReconciler(
		standardGitReader(catalogWithSecrets),
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		&mockSecretProvider{values: map[string][]byte{
			"secrets/datadog/api-key": []byte("the-api-key"),
			"secrets/datadog/app-key": []byte("the-app-key"),
		}},
		fakeRemoteClientFn(client),
	)
}

// TestPeriodicPass_NeverWritesAForeignSecret is the headline: the loop that
// runs every five minutes must leave somebody else's Secret exactly as it
// found it, and record it as foreign so the page can say so.
func TestPeriodicPass_NeverWritesAForeignSecret(t *testing.T) {
	existing := foreignSecret()
	client := fake.NewSimpleClientset(existing)
	r := foreignGateReconciler(client)

	r.reconcile()

	if got := writeActions(client); len(got) != 0 {
		t.Fatalf("the pass wrote to a secret Sharko did not create: %v", got)
	}

	live, err := client.CoreV1().Secrets("monitoring").Get(context.Background(), "datadog-secret", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading the live secret back: %v", err)
	}
	if string(live.Data["api-key"]) != "not-sharkos-value" {
		t.Errorf("the secret's content changed: %q", live.Data["api-key"])
	}
	if live.Labels["app.kubernetes.io/managed-by"] != "external-secrets" {
		t.Errorf("the ownership label was rewritten: %v", live.Labels)
	}

	outcome, ok := r.LastItemOutcome("prod-cluster", "datadog")
	if !ok || outcome != string(ItemOutcomeForeign) {
		t.Errorf("LastItemOutcome = (%q, %v), want (%q, true)", outcome, ok, ItemOutcomeForeign)
	}
	// Foreign is a boundary, not a failure: no error text, and the pass's
	// error counter must stay at zero.
	if msg, hasErr := r.LastItemError("prod-cluster", "datadog"); hasErr {
		t.Errorf("a foreign secret recorded an error: %q", msg)
	}
	if stats := r.GetStats(); stats.Errors != 0 || stats.Updated != 0 || stats.Created != 0 {
		t.Errorf("stats = %+v, want no writes and no errors", stats)
	}
}

// TestPeriodicPass_KeepsRecordingForeignOnEveryPass — repeated passes must
// keep reporting the same boundary and must never wear it down into a write.
func TestPeriodicPass_KeepsRecordingForeignOnEveryPass(t *testing.T) {
	client := fake.NewSimpleClientset(foreignSecret())
	r := foreignGateReconciler(client)

	for i := 0; i < 3; i++ {
		r.reconcile()
		outcome, ok := r.LastItemOutcome("prod-cluster", "datadog")
		if !ok || outcome != string(ItemOutcomeForeign) {
			t.Fatalf("pass %d: outcome = (%q, %v), want foreign", i+1, outcome, ok)
		}
	}
	if got := writeActions(client); len(got) != 0 {
		t.Fatalf("three passes wrote to a foreign secret: %v", got)
	}
}

// TestCheckOne_ReportsForeignWithoutReadingTheStore — a check may look, and
// it reports the boundary. It must also not pull a value out of the secrets
// store it has already decided it will never write.
func TestCheckOne_ReportsForeignWithoutReadingTheStore(t *testing.T) {
	client := fake.NewSimpleClientset(foreignSecret())
	secretProv := &countingSecretProvider{values: map[string][]byte{
		"secrets/datadog/api-key": []byte("the-api-key"),
		"secrets/datadog/app-key": []byte("the-app-key"),
	}}
	r := newReconciler(
		standardGitReader(catalogWithSecrets),
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		secretProv,
		fakeRemoteClientFn(client),
	)

	outcome, err := r.CheckOne(context.Background(), "prod-cluster", "datadog")
	if err != nil {
		t.Fatalf("CheckOne: unexpected error: %v", err)
	}
	if outcome != string(ItemOutcomeForeign) {
		t.Errorf("outcome = %q, want %q", outcome, ItemOutcomeForeign)
	}
	if got := writeActions(client); len(got) != 0 {
		t.Fatalf("CheckOne wrote something: %v", got)
	}
	if secretProv.calls != 0 {
		t.Errorf("CheckOne fetched %d value(s) for a secret it will never write", secretProv.calls)
	}
}

// TestSyncOne_RefusesAForeignSecret — the button is disabled in the UI, so
// this is the layer underneath: an API call straight past it is refused,
// with the sentence a person is meant to read.
func TestSyncOne_RefusesAForeignSecret(t *testing.T) {
	client := fake.NewSimpleClientset(foreignSecret())
	r := foreignGateReconciler(client)

	_, err := r.SyncOne(context.Background(), "prod-cluster", "datadog")
	if err == nil {
		t.Fatal("SyncOne accepted a secret Sharko did not create")
	}
	if !errors.Is(err, ErrForeignSecret) {
		t.Errorf("error = %v, want ErrForeignSecret", err)
	}
	if err.Error() != "Someone else created this one — Sharko will not touch it." {
		t.Errorf("refusal sentence = %q — it must match the one the page shows", err.Error())
	}
	if got := writeActions(client); len(got) != 0 {
		t.Fatalf("SyncOne wrote something: %v", got)
	}
	// The record says foreign, and carries no error text — nothing went
	// wrong, Sharko simply does not own this one.
	outcome, ok := r.LastItemOutcome("prod-cluster", "datadog")
	if !ok || outcome != string(ItemOutcomeForeign) {
		t.Errorf("LastItemOutcome = (%q, %v), want foreign", outcome, ok)
	}
	if msg, hasErr := r.LastItemError("prod-cluster", "datadog"); hasErr {
		t.Errorf("a refused sync recorded an error on the row: %q", msg)
	}
}

// TestPeriodicPass_StillUpdatesItsOwnSecret — the gate must not break the
// ordinary rotation it is wrapped around.
func TestPeriodicPass_StillUpdatesItsOwnSecret(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "datadog-secret",
			Namespace: "monitoring",
			Labels:    map[string]string{"app.kubernetes.io/managed-by": "sharko"},
		},
		Data: map[string][]byte{"api-key": []byte("stale")},
		Type: corev1.SecretTypeOpaque,
	})
	r := foreignGateReconciler(client)

	r.reconcile()

	outcome, ok := r.LastItemOutcome("prod-cluster", "datadog")
	if !ok || outcome != string(ItemOutcomeUpdated) {
		t.Fatalf("LastItemOutcome = (%q, %v), want updated", outcome, ok)
	}
	live, err := client.CoreV1().Secrets("monitoring").Get(context.Background(), "datadog-secret", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading the live secret back: %v", err)
	}
	if string(live.Data["api-key"]) != "the-api-key" {
		t.Errorf("Sharko's own secret was not rotated: %q", live.Data["api-key"])
	}
}

// TestPeriodicPass_StillCreatesAMissingSecret — a Secret that does not
// exist is still Sharko's to create; the label at birth is how ownership
// starts.
func TestPeriodicPass_StillCreatesAMissingSecret(t *testing.T) {
	client := fake.NewSimpleClientset()
	r := foreignGateReconciler(client)

	r.reconcile()

	live, err := client.CoreV1().Secrets("monitoring").Get(context.Background(), "datadog-secret", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("the missing secret was not created: %v", err)
	}
	if live.Labels["app.kubernetes.io/managed-by"] != "sharko" {
		t.Errorf("a newly created secret must carry Sharko's label, got %v", live.Labels)
	}
}

// TestCheckAll_ChecksEverythingAndWritesNothing — P1-A A3's fleet-wide
// check. It reports every row and never touches a cluster.
func TestCheckAll_ChecksEverythingAndWritesNothing(t *testing.T) {
	client := fake.NewSimpleClientset()
	r := foreignGateReconciler(client)

	if err := r.CheckAll(context.Background()); err != nil {
		t.Fatalf("CheckAll: unexpected error: %v", err)
	}
	if got := writeActions(client); len(got) != 0 {
		t.Fatalf("CheckAll wrote to the cluster: %v", got)
	}
	outcome, ok := r.LastItemOutcome("prod-cluster", "datadog")
	if !ok || outcome != string(ItemOutcomeMissing) {
		t.Errorf("LastItemOutcome = (%q, %v), want missing", outcome, ok)
	}
}

// TestCheckAll_NoGitConnection — nothing to check at all is a real error the
// caller hears about, not a silent success.
func TestCheckAll_NoGitConnection(t *testing.T) {
	r := newReconciler(
		nil,
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		&mockSecretProvider{},
		fakeRemoteClientFn(fake.NewSimpleClientset()),
	)
	r.gitReader = func() GitReader { return nil }

	if err := r.CheckAll(context.Background()); !errors.Is(err, ErrNoGitConnection) {
		t.Errorf("CheckAll error = %v, want ErrNoGitConnection", err)
	}
}

// TestCheckAll_IsolatesOneUnreachableCluster — one broken cluster must not
// stop the rest being checked.
//
// H2 (code review): CheckAll returning nil used to be the whole story —
// checkWork's failure paths never called recordItemCheck, so a vault-down
// pass left every row still saying whatever its LAST SUCCESSFUL check
// found (rows kept reading "Synced" while nothing was actually checkable),
// and CheckAll published no run record at all (the engine's own
// LastError/GetStats kept reporting a stale, unrelated pass). This test now
// asserts what the ROW says afterward, not just that the call didn't
// error: the item outcome is "error", LastItemError carries something,
// ConsecutiveFailures moved off zero, and GetStats' Errors count reflects
// the failed item.
func TestCheckAll_IsolatesOneUnreachableCluster(t *testing.T) {
	r := newReconciler(
		standardGitReader(catalogWithSecrets),
		&mockCredProvider{err: errors.New("credentials unavailable")},
		&mockSecretProvider{},
		fakeRemoteClientFn(fake.NewSimpleClientset()),
	)

	if err := r.CheckAll(context.Background()); err != nil {
		t.Fatalf("one unreachable cluster must not fail the whole check: %v", err)
	}

	outcome, ok := r.LastItemOutcome("prod-cluster", "datadog")
	if !ok || outcome != string(ItemOutcomeError) {
		t.Errorf("LastItemOutcome = (%q, %v), want (%q, true) — a failed check must be recorded on the row, not silently dropped",
			outcome, ok, ItemOutcomeError)
	}

	errMsg, hasErr := r.LastItemError("prod-cluster", "datadog")
	if !hasErr || errMsg == "" {
		t.Error("LastItemError has nothing recorded — a row that failed to check must say so")
	}

	if count, ok := r.LastItemConsecutiveFailures("prod-cluster", "datadog"); !ok || count != 1 {
		t.Errorf("LastItemConsecutiveFailures = (%d, %v), want (1, true) — L10's carry-forward must count this failure", count, ok)
	}

	stats := r.GetStats()
	if stats.Errors == 0 {
		t.Error("GetStats().Errors = 0 after CheckAll failed on every item — CheckAll must publish a real run record, same as the periodic pass")
	}
	if stats.Checked == 0 {
		t.Error("GetStats().Checked = 0 — CheckAll attempted at least one item and must say so")
	}
}

// TestCheckAll_AllFailures_EngineLevelErrorIsVisible pins the other half of
// H2: when CheckAll fails on every item, the engine-level LastError (what
// the Managed Secrets page's top strip reads) must say so too — not stay
// empty as if the pass had never run or had nothing to report.
func TestCheckAll_AllFailures_EngineLevelErrorIsVisible(t *testing.T) {
	r := newReconciler(
		standardGitReader(catalogWithSecrets),
		&mockCredProvider{err: errors.New("credentials unavailable")},
		&mockSecretProvider{},
		fakeRemoteClientFn(fake.NewSimpleClientset()),
	)

	if err := r.CheckAll(context.Background()); err != nil {
		t.Fatalf("one unreachable cluster must not fail the whole check: %v", err)
	}

	if got := r.LastError(); got == "" {
		t.Error("LastError() is empty after CheckAll failed on every item — the engine strip would show nothing was wrong")
	}
	if got := r.LastErrorCluster(); got != "prod-cluster" {
		t.Errorf("LastErrorCluster() = %q, want %q", got, "prod-cluster")
	}
}

// TestEnsureSecretGate_SurvivesALabelStrippedMidFlight — belt and braces:
// even if the ownership label vanishes between the reconciler's own Get and
// the write, the choke point inside remoteclient refuses, and the pass
// records foreign rather than an error.
func TestEnsureSecretGate_SurvivesALabelStrippedMidFlight(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "datadog-secret",
			Namespace: "monitoring",
			Labels:    map[string]string{"app.kubernetes.io/managed-by": "sharko"},
		},
		Data: map[string][]byte{"api-key": []byte("stale")},
		Type: corev1.SecretTypeOpaque,
	})

	// The reconciler's own Get sees Sharko's label; the choke point's Get,
	// one call later, sees somebody else's.
	gets := 0
	client.PrependReactor("get", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		gets++
		if gets == 1 {
			return false, nil, nil // let the tracker answer normally
		}
		s := foreignSecret()
		return true, s, nil
	})

	r := foreignGateReconciler(client)
	r.reconcile()

	outcome, ok := r.LastItemOutcome("prod-cluster", "datadog")
	if !ok || outcome != string(ItemOutcomeForeign) {
		t.Errorf("LastItemOutcome = (%q, %v), want foreign", outcome, ok)
	}
	if got := writeActions(client); len(got) != 0 {
		t.Fatalf("a write landed after the label was stripped: %v", got)
	}
}
