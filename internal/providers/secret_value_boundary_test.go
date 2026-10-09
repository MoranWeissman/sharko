package providers

// secret_value_boundary_test.go — S2. GetSecretValue is a TRUSTED BOUNDARY,
// and these tests are what stop the mark on it being deleted silently.
//
// # Why this file exists
//
// A break test proved it was needed. Removing credsafe.MarkSecretValue from
// AWSSecretsManagerProvider.GetSecretValue failed NOTHING in this package:
// every existing test either checks the boundary refusals (which are not
// marked, on purpose) or checks that the secret VALUE does not leak (which was
// never the gap). The gap was the backend's own ERROR TEXT, and nothing
// watched it.
//
// # The two halves, and why they must both hold
//
//  1. A backend-call failure must say the fixed safe sentence. That is the
//     mark doing its job — GetCredentials has had it for a long time and
//     GetSecretValue did not, which is what made it an oversight rather than a
//     decision.
//
//  2. A BOUNDARY REFUSAL must still say its own words, verbatim. Those
//     sentences are Sharko's own, they name only the refused path and the
//     configured prefix or namespace, and internal/providers/boundary.go's doc
//     comment says the UI, the API and the CLI all render them verbatim on
//     purpose ("three-door parity"). Marking those too would look like extra
//     safety and would in fact break a shipped, tested behaviour.
//
// Half 2 is the one worth being careful about: it is the reason the fix marks
// two specific error returns rather than wrapping the whole function.

import (
	"context"
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/MoranWeissman/sharko/internal/credsafe"
)

// boundarySentinel stands in for credential material a misbehaving backend SDK
// wrote into its own error text. Fake value, never a real credential.
const boundarySentinel = "CANARY-3f90ce41-sdk-error-carried-this-8ab27d05"

func TestAWSGetSecretValue_BackendErrorSaysTheSafeSentence(t *testing.T) {
	backendErr := errors.New("AccessDeniedException: presigned URL " + boundarySentinel + " was rejected")
	p := &AWSSecretsManagerProvider{client: &fakeSMErrClient{err: backendErr}, prefix: "sharko/addons/"}

	_, err := p.GetSecretValue(context.Background(), "sharko/addons/datadog/api-key")
	if err == nil {
		t.Fatal("expected an error from a failing AWS call")
	}

	// The mark must be on it, so every downstream boundary is safe by default.
	if !credsafe.Is(err) {
		t.Fatal("the AWS backend error is not marked as a credentials-backend failure — a caller that forgets to ask will leak its text")
	}
	if got := err.Error(); got != credsafe.SecretValueMessage {
		t.Fatalf("Error() = %q, want the fixed secret-value sentence", got)
	}
	// Every shape a forgetful caller would reach for.
	for _, got := range []string{err.Error(), credsafe.Sentence(err)} {
		if strings.Contains(got, boundarySentinel) {
			t.Errorf("the SDK's error text leaked: %q", got)
		}
		if strings.Contains(got, "AccessDeniedException") {
			t.Errorf("the SDK's own wording leaked: %q", got)
		}
	}
	// The real cause must still be reachable for classification.
	if !strings.Contains(credsafe.Cause(err).Error(), "AccessDeniedException") {
		t.Error("credsafe.Cause must still reach the real error — classification depends on it")
	}
}

func TestK8sGetSecretValue_BackendErrorIsMarked(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "datadog", Namespace: "sharko"},
		Data:       map[string][]byte{"api-key": []byte("v")},
	})
	client.PrependReactor("get", "secrets", func(k8stesting.Action) (bool, k8sruntime.Object, error) {
		return true, nil, errors.New(`secrets "datadog" is forbidden: User "system:serviceaccount:sharko:sa" cannot get, ` + boundarySentinel)
	})
	p := newKubernetesSecretProviderWithClient(client, "sharko")

	_, err := p.GetSecretValue(context.Background(), "datadog/api-key")
	if err == nil {
		t.Fatal("expected an error from a failing Kubernetes read")
	}
	if !credsafe.Is(err) {
		t.Fatal("the Kubernetes backend error is not marked — a caller that forgets to ask will leak its text")
	}
	if got := err.Error(); got != credsafe.SecretValueMessage {
		t.Fatalf("Error() = %q, want the fixed secret-value sentence", got)
	}
	for _, leaked := range []string{boundarySentinel, "system:serviceaccount", "is forbidden"} {
		if strings.Contains(err.Error(), leaked) {
			t.Errorf("the Kubernetes error's own words (%q) leaked: %q", leaked, err.Error())
		}
	}
}

// TestGetSecretValue_BoundaryRefusalsStillSayTheirOwnWords is the other half,
// and the one a careless "mark everything" fix would break.
//
// These refusals happen BEFORE any backend call, they are Sharko's own
// sentences, they name only the refused path and the configured boundary, and
// boundary.go pins them as the exact text an operator sees at every door. They
// must NOT be marked.
func TestGetSecretValue_BoundaryRefusalsStillSayTheirOwnWords(t *testing.T) {
	awsProv := &AWSSecretsManagerProvider{client: &fakeSMErrClient{err: errors.New("should never be called")}, prefix: "sharko/addons/"}
	_, awsErr := awsProv.GetSecretValue(context.Background(), "somewhere/else/key")
	if awsErr == nil {
		t.Fatal("expected the AWS prefix refusal")
	}
	if credsafe.Is(awsErr) {
		t.Error("the AWS boundary refusal must NOT be marked — its own words are the shipped, tested text at every door")
	}
	if !strings.Contains(awsErr.Error(), "somewhere/else/key") || !strings.Contains(awsErr.Error(), "sharko/addons/") {
		t.Errorf("the AWS refusal no longer names the refused path and the configured prefix: %q", awsErr.Error())
	}
	if !errors.Is(awsErr, ErrSecretPathRefused) {
		t.Error("the AWS refusal must still match ErrSecretPathRefused")
	}

	client := fake.NewSimpleClientset()
	k8sProv := newKubernetesSecretProviderWithClient(client, "sharko")
	_, k8sErr := k8sProv.GetSecretValue(context.Background(), "kube-system/victim/token")
	if k8sErr == nil {
		t.Fatal("expected the Kubernetes namespace refusal")
	}
	if credsafe.Is(k8sErr) {
		t.Error("the Kubernetes boundary refusal must NOT be marked")
	}
	if !strings.Contains(k8sErr.Error(), "kube-system") || !strings.Contains(k8sErr.Error(), "sharko") {
		t.Errorf("the Kubernetes refusal no longer names the requested and allowed namespaces: %q", k8sErr.Error())
	}
	if !errors.Is(k8sErr, ErrSecretPathRefused) {
		t.Error("the Kubernetes refusal must still match ErrSecretPathRefused")
	}
}
