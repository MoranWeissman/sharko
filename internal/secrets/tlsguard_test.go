package secrets

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/remoteclient"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// tlsguard_test.go — task #152 lane C at the reconciler call site: a
// destination whose kubeconfig skips TLS certificate checks is refused by
// the scheduled pass, by SyncOne (the "Sync" row action) and by CheckOne
// (the "Refresh" row action), and a certificate-verifying destination is
// untouched. Fixtures reused from reconciler_test.go (standardGitReader /
// catalogWithSecrets: addon "datadog", secret "datadog-secret" in
// "monitoring", cluster "prod-cluster").
//
// These run in the default hermetic `go test` — the kubeconfigs below are
// valid YAML pointing at servers that do not exist, and the remote-client
// factory is the usual fake; the refusal fires before any client is built,
// so nothing ever connects.

const insecureDestKubeconfig = `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://unverified.example.invalid:6443
    insecure-skip-tls-verify: true
  name: unverified
contexts:
- context:
    cluster: unverified
    user: unverified
  name: unverified
current-context: unverified
users:
- name: unverified
  user:
    token: fake-token-for-tests
`

const verifiedDestKubeconfig = `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://verified.example.invalid:6443
  name: verified
contexts:
- context:
    cluster: verified
    user: verified
  name: verified
current-context: verified
users:
- name: verified
  user:
    token: fake-token-for-tests
`

func standardSecretValues() *mockSecretProvider {
	return &mockSecretProvider{values: map[string][]byte{
		"secrets/datadog/api-key": []byte("the-api-key"),
		"secrets/datadog/app-key": []byte("the-app-key"),
	}}
}

// TestReconcile_RefusesUnverifiedDestination: the scheduled pass refuses
// to deliver to a skip-verify destination — nothing is written, the pass
// counts an error, the row records the refusal, and the engine's LastError
// is the plain canned sentence, never raw error text.
func TestReconcile_RefusesUnverifiedDestination(t *testing.T) {
	client := fake.NewSimpleClientset()
	r := newReconciler(
		standardGitReader(catalogWithSecrets),
		&mockCredProvider{kubeconfig: []byte(insecureDestKubeconfig)},
		standardSecretValues(),
		fakeRemoteClientFn(client),
	)
	r.reconcile()

	// Nothing was written to the destination.
	if _, err := client.CoreV1().Secrets("monitoring").Get(context.Background(), "datadog-secret", metav1.GetOptions{}); err == nil {
		t.Fatal("a secret was delivered to a destination whose connection skips certificate checks")
	}

	stats := r.GetStats()
	if stats.Created != 0 || stats.Updated != 0 {
		t.Errorf("expected no writes, got Created=%d Updated=%d", stats.Created, stats.Updated)
	}
	if stats.Errors != 1 {
		t.Errorf("expected Errors=1, got %d", stats.Errors)
	}

	// The row carries the refusal with Sharko's own fixed sentence.
	rowErr, ok := r.LastItemError("prod-cluster", "datadog")
	if !ok {
		t.Fatal("the row has no error recorded for the refusal")
	}
	if !strings.Contains(rowErr, "skip certificate checks") {
		t.Errorf("row error = %q, want the fixed refusal sentence about skipping certificate checks", rowErr)
	}

	// The engine strip shows the canned sentence, mapped by
	// FailureSentence — not raw error text.
	if got := r.LastError(); !strings.Contains(got, "skip certificate checks") {
		t.Errorf("LastError() = %q, want the canned unverified-connection sentence", got)
	}
}

// TestReconcile_VerifiedDestinationUnaffected: an ordinary,
// certificate-verifying destination delivers exactly as before.
func TestReconcile_VerifiedDestinationUnaffected(t *testing.T) {
	client := fake.NewSimpleClientset()
	r := newReconciler(
		standardGitReader(catalogWithSecrets),
		&mockCredProvider{kubeconfig: []byte(verifiedDestKubeconfig)},
		standardSecretValues(),
		fakeRemoteClientFn(client),
	)
	r.reconcile()

	if _, err := client.CoreV1().Secrets("monitoring").Get(context.Background(), "datadog-secret", metav1.GetOptions{}); err != nil {
		t.Fatalf("secret was not delivered to a certificate-verifying destination: %v", err)
	}
	stats := r.GetStats()
	if stats.Created != 1 || stats.Errors != 0 {
		t.Errorf("expected Created=1 Errors=0, got Created=%d Errors=%d", stats.Created, stats.Errors)
	}
}

// TestSyncOne_RefusesUnverifiedDestination: the "Sync" row action drives
// reconcileSecret, so it inherits the identical refusal — and hands the
// caller the typed error, so the API layer can tell a refusal from a
// failure.
func TestSyncOne_RefusesUnverifiedDestination(t *testing.T) {
	client := fake.NewSimpleClientset()
	r := newReconciler(
		standardGitReader(catalogWithSecrets),
		&mockCredProvider{kubeconfig: []byte(insecureDestKubeconfig)},
		standardSecretValues(),
		fakeRemoteClientFn(client),
	)

	_, err := r.SyncOne(context.Background(), "prod-cluster", "datadog")
	if !errors.Is(err, remoteclient.ErrUnverifiedDestination) {
		t.Fatalf("SyncOne over a skip-verify connection: err = %v, want ErrUnverifiedDestination", err)
	}
	if _, getErr := client.CoreV1().Secrets("monitoring").Get(context.Background(), "datadog-secret", metav1.GetOptions{}); getErr == nil {
		t.Fatal("SyncOne wrote the secret despite the refusal")
	}
}

// TestCheckOne_RefusesUnverifiedDestination: the "Refresh" row action
// refuses too, so the row says the same thing after a manual check as it
// does after the periodic pass — no flip-flopping between a drift verdict
// and a refusal.
func TestCheckOne_RefusesUnverifiedDestination(t *testing.T) {
	client := fake.NewSimpleClientset()
	r := newReconciler(
		standardGitReader(catalogWithSecrets),
		&mockCredProvider{kubeconfig: []byte(insecureDestKubeconfig)},
		standardSecretValues(),
		fakeRemoteClientFn(client),
	)

	_, err := r.CheckOne(context.Background(), "prod-cluster", "datadog")
	if !errors.Is(err, remoteclient.ErrUnverifiedDestination) {
		t.Fatalf("CheckOne over a skip-verify connection: err = %v, want ErrUnverifiedDestination", err)
	}
	rowErr, ok := r.LastItemError("prod-cluster", "datadog")
	if !ok || !strings.Contains(rowErr, "skip certificate checks") {
		t.Errorf("LastItemError = (%q, %v), want the fixed refusal sentence", rowErr, ok)
	}
}

// TestFailureSentence_UnverifiedConnection pins the engine-strip mapping:
// both the call-site refusal shape and a defense-in-depth refusal that
// surfaced through EnsureSecret's write-stage wrapping map to the same
// honest sentence, never to the generic write-failed one.
func TestFailureSentence_UnverifiedConnection(t *testing.T) {
	want := "One of the clusters is set up to skip certificate checks, so Sharko refused to send it a secret. Fix that cluster's connection so its certificate can be verified, then click Refresh."
	raws := []string{
		"cluster=prod-cluster addon=datadog secret=datadog-secret: " + remoteclient.ErrUnverifiedDestination.Error(),
		"cluster=prod-cluster addon=datadog secret=datadog-secret: creating secret: secret monitoring/datadog-secret: " + remoteclient.ErrUnverifiedDestination.Error(),
	}
	for _, raw := range raws {
		if got := FailureSentence(raw); got != want {
			t.Errorf("FailureSentence(%q) = %q, want %q", raw, got, want)
		}
	}
}

// TestItemFailureReason_UnverifiedConnection pins the metric-label twin of
// the sentence mapping, in the same before-the-write-stages position.
func TestItemFailureReason_UnverifiedConnection(t *testing.T) {
	if got := itemFailureReason(remoteclient.ErrUnverifiedDestination); got != "unverified_connection" {
		t.Errorf("itemFailureReason(ErrUnverifiedDestination) = %q, want %q", got, "unverified_connection")
	}
}
