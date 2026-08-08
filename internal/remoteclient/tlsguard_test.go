package remoteclient

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// tlsguard_test.go — the parts of the unverified-destination refusal that
// hold in EVERY build, bypass tag or not: the marker wrapper's behavior.
// The guard-on assertions (what a normal build refuses) live in
// tlsguard_guard_test.go behind !sharko_unverified_dest_ok; the bypass-on
// assertions live in tlsguard_bypass_test.go behind the tag.

// kubeconfig fixtures: valid YAML that round-trips through clientcmd, one
// per TLS mode. Fake server, fake token — nothing here ever connects.
const insecureKubeconfigYAML = `apiVersion: v1
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

const secureKubeconfigYAML = `apiVersion: v1
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

// TestEnsureSecret_RefusesUnverifiedDestinationBeforeAnyAPICall proves the
// choke point: a marked client is refused with ErrUnverifiedDestination
// and the refusal happens before EnsureSecret touches the API at all — the
// fake clientset records zero actions.
func TestEnsureSecret_RefusesUnverifiedDestinationBeforeAnyAPICall(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()
	marked := unverifiedDestinationClient{fakeClient}

	err := EnsureSecret(context.Background(), marked, "monitoring", "datadog-secret",
		map[string][]byte{"api-key": []byte("fake-value")}, nil)
	if !errors.Is(err, ErrUnverifiedDestination) {
		t.Fatalf("EnsureSecret on a marked client: err = %v, want ErrUnverifiedDestination", err)
	}
	if got := len(fakeClient.Actions()); got != 0 {
		t.Errorf("EnsureSecret made %d API calls before refusing — the refusal must come first, got actions: %v",
			got, fakeClient.Actions())
	}
}

// TestEnsureSecret_UnmarkedClientUnaffected pins that an ordinary client
// (any client not marked by NewClientFromKubeconfig) goes through the
// normal create path — the guard must not leak into the common case.
func TestEnsureSecret_UnmarkedClientUnaffected(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()

	if err := EnsureSecret(context.Background(), fakeClient, "monitoring", "datadog-secret",
		map[string][]byte{"api-key": []byte("fake-value")}, nil); err != nil {
		t.Fatalf("EnsureSecret on an unmarked client: unexpected error: %v", err)
	}
	secret, err := fakeClient.CoreV1().Secrets("monitoring").Get(context.Background(), "datadog-secret", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("secret was not created: %v", err)
	}
	if secret.Labels[managedByLabel] != managedByValue {
		t.Errorf("created secret is missing the ownership label")
	}
}

// TestDeleteSecretIfManaged_StillWorksOnUnverifiedDestination pins the
// deliberate scope of the refusal: a delete carries no secret value, so
// cleanup of an insecurely-registered cluster keeps working even on a
// marked client.
func TestDeleteSecretIfManaged_StillWorksOnUnverifiedDestination(t *testing.T) {
	fakeClient := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "datadog-secret",
			Namespace: "monitoring",
			Labels:    map[string]string{managedByLabel: managedByValue},
		},
	})
	marked := unverifiedDestinationClient{fakeClient}

	deleted, err := DeleteSecretIfManaged(context.Background(), marked, "monitoring", "datadog-secret")
	if err != nil {
		t.Fatalf("DeleteSecretIfManaged on a marked client: unexpected error: %v", err)
	}
	if !deleted {
		t.Error("DeleteSecretIfManaged on a marked client: deleted = false, want true — deletes carry no value and must keep working")
	}
}

// TestCheckDestinationTLS_UnparseableFailsOpenToClientBuilder pins the
// documented contract for garbage bytes: CheckDestinationTLS returns nil
// (in every build), because an unparseable kubeconfig cannot build a
// client through NewClientFromKubeconfig either — the parse error there
// is the one the caller should surface, and no client means no write.
func TestCheckDestinationTLS_UnparseableFailsOpenToClientBuilder(t *testing.T) {
	if err := CheckDestinationTLS([]byte("not a kubeconfig at all")); err != nil {
		t.Fatalf("CheckDestinationTLS(garbage) = %v, want nil (the client builder owns the parse error)", err)
	}
	if _, err := NewClientFromKubeconfig([]byte("not a kubeconfig at all")); err == nil {
		t.Fatal("NewClientFromKubeconfig(garbage) built a client — the fail-open above leans on this failing")
	}
}
