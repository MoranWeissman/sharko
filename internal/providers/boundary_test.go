package providers

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// Task #152 story B — the addon-secret read boundary, pinned.
//
// Every value in these tests is fake. No real credential material, no
// real AWS account, no real cluster.

// fakeSMClient is a hermetic stand-in for the AWS Secrets Manager client.
// It records what it was asked for and serves canned fake values.
type fakeSMClient struct {
	gotSecretID string
	calls       int
	value       string
	binary      []byte
}

func (f *fakeSMClient) GetSecretValue(_ context.Context, params *secretsmanager.GetSecretValueInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	f.calls++
	f.gotSecretID = aws.ToString(params.SecretId)
	out := &secretsmanager.GetSecretValueOutput{}
	if f.binary != nil {
		out.SecretBinary = f.binary
		return out, nil
	}
	out.SecretString = aws.String(f.value)
	return out, nil
}

func (f *fakeSMClient) ListSecrets(_ context.Context, _ *secretsmanager.ListSecretsInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.ListSecretsOutput, error) {
	return &secretsmanager.ListSecretsOutput{}, nil
}

// --- AWS: refuse before the AWS call ----------------------------------------

// A path outside the configured prefix is refused, and the refusal comes
// BEFORE the AWS call: the client here is nil, so any attempt to reach
// AWS would panic the test.
func TestAWSGetSecretValue_RefusesPathOutsidePrefix(t *testing.T) {
	p := &AWSSecretsManagerProvider{prefix: "sharko/addons/"} // client deliberately nil

	_, err := p.GetSecretValue(context.Background(), "prod/payments/db-password")
	if err == nil {
		t.Fatal("expected a refusal for a path outside the prefix, got nil")
	}
	if !errors.Is(err, ErrSecretPathRefused) {
		t.Errorf("refusal must be marked with ErrSecretPathRefused, got: %v", err)
	}
	want := awsOutsidePrefixRefusal("prod/payments/db-password", "sharko/addons/").Error()
	if err.Error() != want {
		t.Errorf("refusal sentence drifted from the canned one.\n got: %s\nwant: %s", err, want)
	}
}

// An empty prefix never means "the whole AWS account". It is treated as a
// configuration error and every read is refused — again before any AWS
// call (nil client).
func TestAWSGetSecretValue_EmptyPrefixRefusesEverything(t *testing.T) {
	p := &AWSSecretsManagerProvider{prefix: ""} // client deliberately nil

	_, err := p.GetSecretValue(context.Background(), "any/path/at-all")
	if err == nil {
		t.Fatal("expected a refusal when no prefix is configured, got nil")
	}
	if !errors.Is(err, ErrSecretPathRefused) {
		t.Errorf("refusal must be marked with ErrSecretPathRefused, got: %v", err)
	}
	want := awsNoPrefixRefusal("any/path/at-all").Error()
	if err.Error() != want {
		t.Errorf("refusal sentence drifted from the canned one.\n got: %s\nwant: %s", err, want)
	}
}

// A path under the configured prefix still works: the value comes back
// and the AWS client is asked for exactly the path that was requested.
func TestAWSGetSecretValue_AllowedPathStillWorks(t *testing.T) {
	fakeClient := &fakeSMClient{value: "fake-addon-secret-value"}
	p := &AWSSecretsManagerProvider{client: fakeClient, prefix: "sharko/addons/"}

	val, err := p.GetSecretValue(context.Background(), "sharko/addons/datadog/api-key")
	if err != nil {
		t.Fatalf("allowed path must not be refused: %v", err)
	}
	if string(val) != "fake-addon-secret-value" {
		t.Errorf("value = %q, want the fake value", val)
	}
	if fakeClient.gotSecretID != "sharko/addons/datadog/api-key" {
		t.Errorf("AWS was asked for %q, want the requested path", fakeClient.gotSecretID)
	}
}

// The binary-payload branch works under the boundary too.
func TestAWSGetSecretValue_AllowedBinaryPathStillWorks(t *testing.T) {
	fakeClient := &fakeSMClient{binary: []byte{0x01, 0x02, 0x03}}
	p := &AWSSecretsManagerProvider{client: fakeClient, prefix: "sharko/addons/"}

	val, err := p.GetSecretValue(context.Background(), "sharko/addons/registry/dockerconfig")
	if err != nil {
		t.Fatalf("allowed path must not be refused: %v", err)
	}
	if len(val) != 3 || val[0] != 0x01 {
		t.Errorf("binary value not returned verbatim: %v", val)
	}
}

// The prefix is a literal string boundary. A path that merely shares
// characters with the prefix but does not start with it is refused.
func TestAWSGetSecretValue_SiblingPathDoesNotSlipThrough(t *testing.T) {
	p := &AWSSecretsManagerProvider{prefix: "sharko/addons/"} // nil client: refusal must come first

	if _, err := p.GetSecretValue(context.Background(), "sharko/addons-evil/x"); err == nil {
		t.Fatal("a sibling path outside the prefix must be refused")
	} else if !errors.Is(err, ErrSecretPathRefused) {
		t.Errorf("expected the boundary refusal, got: %v", err)
	}
}

// --- Kubernetes: the explicit-namespace form cannot escape -------------------

// An explicit-namespace path pointing outside the configured namespace is
// refused, and no Kubernetes API call is made — even when the target
// Secret really exists over there.
func TestK8sGetSecretValue_RefusesNamespaceEscape(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "victim", Namespace: "kube-system"},
		Data:       map[string][]byte{"token": []byte("fake-victim-value")},
	})
	p := newKubernetesSecretProviderWithClient(client, "sharko")

	_, err := p.GetSecretValue(context.Background(), "kube-system/victim/token")
	if err == nil {
		t.Fatal("expected a refusal for a path outside the configured namespace, got nil")
	}
	if !errors.Is(err, ErrSecretPathRefused) {
		t.Errorf("refusal must be marked with ErrSecretPathRefused, got: %v", err)
	}
	want := k8sOutsideNamespaceRefusal("kube-system/victim/token", "kube-system", "sharko").Error()
	if err.Error() != want {
		t.Errorf("refusal sentence drifted from the canned one.\n got: %s\nwant: %s", err, want)
	}
	if got := len(client.Actions()); got != 0 {
		t.Errorf("the refusal must happen before any Kubernetes API call; %d call(s) were made", got)
	}
}

// The explicit-namespace form is still fine when it names the SAME
// namespace the provider is configured for — spelled-out, not escaping.
func TestK8sGetSecretValue_ExplicitSameNamespaceAllowed(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "datadog", Namespace: "sharko"},
		Data:       map[string][]byte{"api-key": []byte("fake-api-key-value")},
	})
	p := newKubernetesSecretProviderWithClient(client, "sharko")

	val, err := p.GetSecretValue(context.Background(), "sharko/datadog/api-key")
	if err != nil {
		t.Fatalf("same-namespace explicit path must not be refused: %v", err)
	}
	if string(val) != "fake-api-key-value" {
		t.Errorf("value = %q, want the fake value", val)
	}
}

// The short "secret/key" form (provider namespace implied) keeps working.
func TestK8sGetSecretValue_DefaultNamespaceFormStillWorks(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "datadog", Namespace: "sharko"},
		Data:       map[string][]byte{"api-key": []byte("fake-api-key-value")},
	})
	p := newKubernetesSecretProviderWithClient(client, "sharko")

	val, err := p.GetSecretValue(context.Background(), "datadog/api-key")
	if err != nil {
		t.Fatalf("short-form path must not be refused: %v", err)
	}
	if string(val) != "fake-api-key-value" {
		t.Errorf("value = %q, want the fake value", val)
	}
}

// A provider constructed with no namespace at all fails closed: nothing
// is read, no matter which path form is used.
func TestK8sGetSecretValue_NoConfiguredNamespaceRefusesEverything(t *testing.T) {
	client := fake.NewSimpleClientset()
	p := newKubernetesSecretProviderWithClient(client, "")

	for _, path := range []string{"datadog/api-key", "sharko/datadog/api-key"} {
		_, err := p.GetSecretValue(context.Background(), path)
		if err == nil {
			t.Fatalf("path %q: expected a refusal when the provider has no namespace, got nil", path)
		}
		if !errors.Is(err, ErrSecretPathRefused) {
			t.Errorf("path %q: refusal must be marked with ErrSecretPathRefused, got: %v", path, err)
		}
	}
	if got := len(client.Actions()); got != 0 {
		t.Errorf("the refusal must happen before any Kubernetes API call; %d call(s) were made", got)
	}
}

// The malformed-path error is unchanged by the boundary work.
func TestK8sGetSecretValue_MalformedPathStillErrors(t *testing.T) {
	p := newKubernetesSecretProviderWithClient(fake.NewSimpleClientset(), "sharko")

	for _, path := range []string{"just-a-name", "a/b/c/d"} {
		if _, err := p.GetSecretValue(context.Background(), path); err == nil {
			t.Errorf("path %q: expected the invalid-path error, got nil", path)
		}
	}
}

// --- Three-door parity pin ---------------------------------------------------

// The boundary refusal is produced inside the provider, server-side.
// The API hands the provider's error string to its response body, the
// CLI prints that body's error field verbatim (printAPIError in
// cmd/sharko/cluster.go), and the UI shows the same field — so whichever
// door the operator came through, the sentence is this one. This test
// pins that the provider returns EXACTLY the canned sentence, with no
// caller-specific wording mixed in, and that no refusal ever carries an
// SDK error (the refusal fires before any SDK call — the AWS cases below
// run with a nil client, and the k8s case is action-checked above).
func TestBoundaryRefusal_CannedSentencesAreTheSameAtEveryDoor(t *testing.T) {
	awsProv := &AWSSecretsManagerProvider{prefix: "sharko/addons/"}
	_, awsErr := awsProv.GetSecretValue(context.Background(), "outside/path")
	if awsErr == nil || awsErr.Error() != awsOutsidePrefixRefusal("outside/path", "sharko/addons/").Error() {
		t.Errorf("AWS refusal is not the canned sentence verbatim: %v", awsErr)
	}

	awsEmpty := &AWSSecretsManagerProvider{}
	_, awsEmptyErr := awsEmpty.GetSecretValue(context.Background(), "outside/path")
	if awsEmptyErr == nil || awsEmptyErr.Error() != awsNoPrefixRefusal("outside/path").Error() {
		t.Errorf("AWS empty-prefix refusal is not the canned sentence verbatim: %v", awsEmptyErr)
	}

	k8sProv := newKubernetesSecretProviderWithClient(fake.NewSimpleClientset(), "sharko")
	_, k8sErr := k8sProv.GetSecretValue(context.Background(), "other-ns/name/key")
	if k8sErr == nil || k8sErr.Error() != k8sOutsideNamespaceRefusal("other-ns/name/key", "other-ns", "sharko").Error() {
		t.Errorf("k8s refusal is not the canned sentence verbatim: %v", k8sErr)
	}

	// All refusals share one sentinel, so every caller can detect a
	// boundary refusal the same way.
	for name, err := range map[string]error{"aws": awsErr, "aws-empty-prefix": awsEmptyErr, "k8s": k8sErr} {
		if !errors.Is(err, ErrSecretPathRefused) {
			t.Errorf("%s refusal is not marked with ErrSecretPathRefused", name)
		}
	}
}
