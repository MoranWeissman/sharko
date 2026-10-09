package secrets

import (
	"context"
	"errors"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// single_item_test.go — CheckOne (S4 "Refresh") and SyncOne (S4 "Sync"),
// the reconciler's single-item path a row action drives instead of the
// whole periodic pass.
//
// Fixtures reused from reconciler_test.go: standardGitReader wires
// catalogWithSecrets (addon "datadog", secret "datadog-secret" in
// "monitoring", keys api-key/app-key) against clusterAddonsYAML (cluster
// "prod-cluster" with the "datadog" addon enabled).

func TestCheckOne_Missing(t *testing.T) {
	client := fake.NewSimpleClientset()
	r := newReconciler(
		standardGitReader(catalogWithSecrets),
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		&mockSecretProvider{values: map[string][]byte{
			"secrets/datadog/api-key": []byte("the-api-key"),
			"secrets/datadog/app-key": []byte("the-app-key"),
		}},
		fakeRemoteClientFn(client),
	)

	outcome, err := r.CheckOne(context.Background(), "prod-cluster", "datadog")
	if err != nil {
		t.Fatalf("CheckOne: unexpected error: %v", err)
	}
	if outcome != string(ItemOutcomeMissing) {
		t.Errorf("outcome = %q, want %q", outcome, ItemOutcomeMissing)
	}

	// Read-only: the check must never have created the secret.
	if _, getErr := client.CoreV1().Secrets("monitoring").Get(context.Background(), "datadog-secret", metav1.GetOptions{}); getErr == nil {
		t.Error("CheckOne wrote the secret — it must never write")
	}

	gotOutcome, ok := r.LastItemOutcome("prod-cluster", "datadog")
	if !ok || gotOutcome != string(ItemOutcomeMissing) {
		t.Errorf("LastItemOutcome = (%q, %v), want (%q, true)", gotOutcome, ok, ItemOutcomeMissing)
	}
}

func TestCheckOne_UnchangedAndOutOfSync(t *testing.T) {
	client := fake.NewSimpleClientset()
	secretProv := &mockSecretProvider{values: map[string][]byte{
		"secrets/datadog/api-key": []byte("the-api-key"),
		"secrets/datadog/app-key": []byte("the-app-key"),
	}}
	r := newReconciler(
		standardGitReader(catalogWithSecrets),
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		secretProv,
		fakeRemoteClientFn(client),
	)

	// Seed the live secret via a real periodic pass first.
	r.reconcile()
	if stats := r.GetStats(); stats.Created != 1 {
		t.Fatalf("setup: expected Created=1, got %d", stats.Created)
	}

	outcome, err := r.CheckOne(context.Background(), "prod-cluster", "datadog")
	if err != nil {
		t.Fatalf("CheckOne: unexpected error: %v", err)
	}
	if outcome != string(ItemOutcomeUnchanged) {
		t.Errorf("outcome = %q, want %q", outcome, ItemOutcomeUnchanged)
	}

	// Rotate the source value out from under the live secret — CheckOne
	// must report the mismatch WITHOUT writing it.
	r.secretProvider = &mockSecretProvider{values: map[string][]byte{
		"secrets/datadog/api-key": []byte("rotated-key"),
		"secrets/datadog/app-key": []byte("rotated-app"),
	}}
	outcome, err = r.CheckOne(context.Background(), "prod-cluster", "datadog")
	if err != nil {
		t.Fatalf("CheckOne: unexpected error: %v", err)
	}
	if outcome != string(ItemOutcomeOutOfSync) {
		t.Errorf("outcome = %q, want %q", outcome, ItemOutcomeOutOfSync)
	}

	secret, getErr := client.CoreV1().Secrets("monitoring").Get(context.Background(), "datadog-secret", metav1.GetOptions{})
	if getErr != nil {
		t.Fatalf("secret disappeared: %v", getErr)
	}
	if string(secret.Data["api-key"]) != "the-api-key" {
		t.Errorf("CheckOne wrote the rotated value — it must never write; api-key = %q", secret.Data["api-key"])
	}
}

func TestCheckOne_NoGitConnection(t *testing.T) {
	r := newReconciler(
		nil, // gitReaderFn will return nil below
		&mockCredProvider{},
		&mockSecretProvider{},
		fakeRemoteClientFn(fake.NewSimpleClientset()),
	)
	r.gitReader = func() GitReader { return nil }

	_, err := r.CheckOne(context.Background(), "prod-cluster", "datadog")
	if !errors.Is(err, ErrNoGitConnection) {
		t.Fatalf("err = %v, want ErrNoGitConnection", err)
	}
}

func TestCheckOne_ItemNotFound(t *testing.T) {
	client := fake.NewSimpleClientset()
	r := newReconciler(
		standardGitReader(catalogWithSecrets),
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		&mockSecretProvider{},
		fakeRemoteClientFn(client),
	)

	_, err := r.CheckOne(context.Background(), "prod-cluster", "no-such-addon")
	if err == nil {
		t.Fatal("expected an error for an addon with no push definition")
	}
	if !strings.Contains(err.Error(), "no addon-values secret is defined") {
		t.Errorf("error = %q, want it to name what's missing", err.Error())
	}

	_, err = r.CheckOne(context.Background(), "no-such-cluster", "datadog")
	if err == nil {
		t.Fatal("expected an error for a cluster not in the plan")
	}
}

func TestSyncOne_CreatesAndAudits(t *testing.T) {
	client := fake.NewSimpleClientset()
	r := newReconciler(
		standardGitReader(catalogWithSecrets),
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		&mockSecretProvider{values: map[string][]byte{
			"secrets/datadog/api-key": []byte("the-api-key"),
			"secrets/datadog/app-key": []byte("the-app-key"),
		}},
		fakeRemoteClientFn(client),
	)

	var audited []string
	r.SetItemAuditFunc(func(cluster, addon string, outcome ItemOutcome) {
		audited = append(audited, cluster+"/"+addon+"/"+string(outcome))
	})

	outcome, err := r.SyncOne(context.Background(), "prod-cluster", "datadog")
	if err != nil {
		t.Fatalf("SyncOne: unexpected error: %v", err)
	}
	if outcome != string(ItemOutcomeCreated) {
		t.Errorf("outcome = %q, want %q", outcome, ItemOutcomeCreated)
	}

	secret, getErr := client.CoreV1().Secrets("monitoring").Get(context.Background(), "datadog-secret", metav1.GetOptions{})
	if getErr != nil {
		t.Fatalf("expected secret to be created, got error: %v", getErr)
	}
	if string(secret.Data["api-key"]) != "the-api-key" {
		t.Errorf("api-key = %q, want the-api-key", secret.Data["api-key"])
	}

	if len(audited) != 1 || audited[0] != "prod-cluster/datadog/created" {
		t.Errorf("audited = %v, want exactly one prod-cluster/datadog/created entry", audited)
	}

	// A periodic pass never ran — SyncOne must never touch the periodic
	// pass's own aggregate stats.
	if stats := r.GetStats(); stats.Created != 0 {
		t.Errorf("r.lastStats.Created = %d, want 0 — SyncOne must not mutate the periodic pass's stats", stats.Created)
	}
}

func TestSyncOne_UnchangedDoesNotAudit(t *testing.T) {
	client := fake.NewSimpleClientset()
	r := newReconciler(
		standardGitReader(catalogWithSecrets),
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		&mockSecretProvider{values: map[string][]byte{
			"secrets/datadog/api-key": []byte("the-api-key"),
			"secrets/datadog/app-key": []byte("the-app-key"),
		}},
		fakeRemoteClientFn(client),
	)

	auditCalls := 0
	r.SetItemAuditFunc(func(_, _ string, _ ItemOutcome) { auditCalls++ })

	if _, err := r.SyncOne(context.Background(), "prod-cluster", "datadog"); err != nil {
		t.Fatalf("first SyncOne: %v", err)
	}
	if auditCalls != 1 {
		t.Fatalf("after create, auditCalls = %d, want 1", auditCalls)
	}

	outcome, err := r.SyncOne(context.Background(), "prod-cluster", "datadog")
	if err != nil {
		t.Fatalf("second SyncOne: %v", err)
	}
	if outcome != string(ItemOutcomeUnchanged) {
		t.Errorf("outcome = %q, want %q", outcome, ItemOutcomeUnchanged)
	}
	if auditCalls != 1 {
		t.Errorf("after unchanged sync, auditCalls = %d, want still 1 — an unchanged check must never audit", auditCalls)
	}

	if gotOutcome, ok := r.LastItemOutcome("prod-cluster", "datadog"); !ok || gotOutcome != string(ItemOutcomeUnchanged) {
		t.Errorf("LastItemOutcome = (%q, %v), want (%q, true)", gotOutcome, ok, ItemOutcomeUnchanged)
	}
}

func TestSyncOne_CredentialErrorSurfacesHonestly(t *testing.T) {
	r := newReconciler(
		standardGitReader(catalogWithSecrets),
		&mockCredProvider{err: errors.New("boom: no such secret in the vault")},
		&mockSecretProvider{},
		fakeRemoteClientFn(fake.NewSimpleClientset()),
	)

	_, err := r.SyncOne(context.Background(), "prod-cluster", "datadog")
	if err == nil {
		t.Fatal("expected an error when credentials cannot be fetched")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %q, want it to carry the underlying cause", err.Error())
	}
}
