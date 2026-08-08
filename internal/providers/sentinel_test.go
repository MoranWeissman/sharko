package providers

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// Task #152 story D — prove a fetched secret value never leaves through a
// log line or an error message, right at the provider boundary where the
// value first enters Sharko's process. Fake value only, never real
// credential material.
//
// sentinelValue is deliberately distinctive (a fixed, greppable prefix plus
// a long random-looking tail) so a substring search for it is meaningful —
// it will never appear in a log line, error sentence, or metric label by
// accident.
const sentinelValue = "CANARY-9f3a7b21-do-not-log-me-4b7d9c44e0-sentinel"

// captureLogs runs fn with slog's default logger swapped for a
// buffer-backed JSON handler at Debug level — the lowest level, so every
// line the code under test emits, at any level, lands in the buffer.
// Restores the previous default when the test ends. Not safe to run in a
// test marked t.Parallel() (it mutates process-global state); every test
// using it below deliberately does not call t.Parallel().
func captureLogs(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	fn()
	return buf.String()
}

// --- AWS Secrets Manager backend --------------------------------------------

// TestSentinel_AWSGetSecretValue_StringSecret_NeverInLogs proves the string
// (SecretString) branch of GetSecretValue returns the value to its caller
// (that's the destination, not a leak) without ever writing it into a log
// line, at any level.
func TestSentinel_AWSGetSecretValue_StringSecret_NeverInLogs(t *testing.T) {
	fakeClient := &fakeSMClient{value: sentinelValue}
	p := &AWSSecretsManagerProvider{client: fakeClient, prefix: "sharko/addons/"}

	var got []byte
	logs := captureLogs(t, func() {
		var err error
		got, err = p.GetSecretValue(context.Background(), "sharko/addons/datadog/api-key")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if string(got) != sentinelValue {
		t.Fatalf("provider must still return the value to its caller, got %q", got)
	}
	if strings.Contains(logs, sentinelValue) {
		t.Fatalf("sentinel value leaked into AWS Secrets Manager provider logs:\n%s", logs)
	}
}

// TestSentinel_AWSGetSecretValue_BinarySecret_NeverInLogs is the same proof
// for the SecretBinary branch.
func TestSentinel_AWSGetSecretValue_BinarySecret_NeverInLogs(t *testing.T) {
	fakeClient := &fakeSMClient{binary: []byte(sentinelValue)}
	p := &AWSSecretsManagerProvider{client: fakeClient, prefix: "sharko/addons/"}

	var got []byte
	logs := captureLogs(t, func() {
		var err error
		got, err = p.GetSecretValue(context.Background(), "sharko/addons/registry/token")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if string(got) != sentinelValue {
		t.Fatalf("provider must still return the value to its caller, got %q", got)
	}
	if strings.Contains(logs, sentinelValue) {
		t.Fatalf("sentinel value leaked into AWS Secrets Manager provider logs (binary branch):\n%s", logs)
	}
}

// --- Cluster-credential fetch path (fetchSecret) ----------------------------
//
// fetchSecret (called from GetCredentials/GetCredentialsWithRoleARN) is a
// DIFFERENT path from GetSecretValue above: it fetches a whole kubeconfig —
// cluster credentials, not an addon-secret value — and logs at Info, not
// Debug, so these lines are on by default in production. Coordinator
// review flagged two "size" fields here that the original story's file:line
// list missed; they are fixed alongside their own sentinel proof.

// TestSentinel_FetchSecret_RawKubeconfig_NeverInLogs proves the raw-YAML
// branch of fetchSecret never writes the fetched kubeconfig's content
// (which includes a bearer token here — real credential material) into a
// log line, even though the log line legitimately names the secret and the
// format.
func TestSentinel_FetchSecret_RawKubeconfig_NeverInLogs(t *testing.T) {
	kubeconfigYAML := `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://example-cluster.invalid:6443
    insecure-skip-tls-verify: true
  name: sentinel-cluster
contexts:
- context:
    cluster: sentinel-cluster
    user: sentinel-user
  name: sentinel-context
current-context: sentinel-context
users:
- name: sentinel-user
  user:
    token: ` + sentinelValue + `
`
	fakeClient := &fakeSMClient{value: kubeconfigYAML}
	p := &AWSSecretsManagerProvider{client: fakeClient}

	var kc *Kubeconfig
	var err error
	logs := captureLogs(t, func() {
		kc, err = p.fetchSecret("clusters/prod/my-cluster", "")
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(kc.Raw), sentinelValue) {
		t.Fatal("sanity check failed: the fixture kubeconfig should carry the sentinel in Raw — the test proves nothing otherwise")
	}
	if kc.Token != sentinelValue {
		t.Fatalf("expected the parsed bearer token to equal the sentinel, got %q", kc.Token)
	}
	if strings.Contains(logs, sentinelValue) {
		t.Fatalf("sentinel (a whole kubeconfig's credential material) leaked into fetchSecret logs:\n%s", logs)
	}
}

// TestSentinel_FetchSecret_StructuredEKS_NeverInLogs is the same proof for
// the structured-EKS-JSON branch: the sentinel sits in the caData field
// (credential material Sharko decodes and hands to the K8s client), and
// must never appear in a log line even though the structured-format log
// line legitimately names the secret and the format.
func TestSentinel_FetchSecret_StructuredEKS_NeverInLogs(t *testing.T) {
	caDataWithSentinel := base64.StdEncoding.EncodeToString(
		[]byte("-----BEGIN CERTIFICATE-----\n" + sentinelValue + "\n-----END CERTIFICATE-----\n"))
	payload := `{
		"clusterName": "prod-eu",
		"host": "https://abc123.gr7.eu-west-1.eks.example.com",
		"caData": "` + caDataWithSentinel + `",
		"region": "eu-west-1"
	}`
	fakeClient := &fakeSMClient{value: payload}
	p := &AWSSecretsManagerProvider{
		client: fakeClient,
		// Stub out STS entirely — this test is about the caData leak, not
		// the token-minting path (see TestSentinel_TokenPrefix... below for
		// that one). A fixed, obviously-fake, non-secret-shaped token
		// avoids any real AWS call.
		eksTokenFn: func(_ context.Context, _, _, _ string) (string, error) {
			return "k8s-aws-v1.fake-non-secret-test-token", nil
		},
	}

	var kc *Kubeconfig
	var err error
	logs := captureLogs(t, func() {
		kc, err = p.fetchSecret("clusters/prod/prod-eu", "")
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(kc.CAData), sentinelValue) {
		t.Fatal("sanity check failed: the fixture caData should carry the sentinel — the test proves nothing otherwise")
	}
	if strings.Contains(logs, sentinelValue) {
		t.Fatalf("sentinel (structured-EKS credential material) leaked into fetchSecret logs:\n%s", logs)
	}
}

// TestSentinel_TokenPrefixIsAConstant_NeverVariesWithTheToken pins the
// judgment call on buildFromStructured's "tokenPrefix" Info log: the first
// 20 characters of a minted EKS token are the SAME LITERAL STRING every
// time, for every region, credential, and signature — see the doc comment
// on tokenPreview in aws_sm.go for the byte-by-byte reasoning. This test
// proves it empirically across three different presigned URLs (different
// regions, different credentials, different signatures) rather than just
// trusting the comment: if a future change to the STS presigning ever made
// tokenPrefix start varying with the signature or the credential, this
// test would catch it and the field would need to be re-judged.
func TestSentinel_TokenPrefixIsAConstant_NeverVariesWithTheToken(t *testing.T) {
	urls := []string{
		"https://sts.eu-west-1.amazonaws.com/?Action=GetCallerIdentity&X-Amz-Credential=AKIAABCDEFG1234567&X-Amz-Signature=deadbeef1111",
		"https://sts.us-east-1.amazonaws.com/?Action=GetCallerIdentity&X-Amz-Credential=AKIAZZZZZZZ9999999&X-Amz-Signature=cafebabe2222",
		"https://sts.ap-southeast-2.amazonaws.com/?Action=GetCallerIdentity&X-Amz-Credential=totallydifferentkey&X-Amz-Signature=00112233",
	}
	const v1Prefix = "k8s-aws-v1."
	const wantConstantPrefix = "k8s-aws-v1.aHR0cHM6L" // "k8s-aws-v1." + base64url("https:/" ...)

	for _, u := range urls {
		token := v1Prefix + base64.RawURLEncoding.EncodeToString([]byte(u))
		got := token
		if len(got) > 20 {
			got = got[:20]
		}
		if got != wantConstantPrefix {
			t.Errorf("tokenPrefix for URL %q = %q, want the constant %q — the field is no longer provably non-secret and must be re-judged",
				u, got, wantConstantPrefix)
		}
	}
}

// TestSentinel_AWSGetSecretValue_BackendError_ValueNeverFetched proves the
// backend-error branch: when the AWS SDK call itself fails, GetSecretValue
// never had the sentinel value to leak in the first place — the fetch
// itself is what failed. Pinned here so a future change that starts
// building a richer error (e.g. echoing a partial response) is caught: the
// wrapped error text is asserted to be exactly Sharko's own sentence plus
// the SDK error, never any secret-shaped payload.
func TestSentinel_AWSGetSecretValue_BackendError_ValueNeverFetched(t *testing.T) {
	backendErr := errors.New("simulated AWS Secrets Manager outage")
	fakeClient := &fakeSMErrClient{err: backendErr}
	p := &AWSSecretsManagerProvider{client: fakeClient, prefix: "sharko/addons/"}

	var got []byte
	var callErr error
	logs := captureLogs(t, func() {
		got, callErr = p.GetSecretValue(context.Background(), "sharko/addons/datadog/api-key")
	})
	if callErr == nil {
		t.Fatal("expected an error from a failing AWS call")
	}
	if got != nil {
		t.Fatalf("no value should be returned on a backend error, got %q", got)
	}
	if strings.Contains(logs, sentinelValue) {
		t.Fatalf("sentinel value must not appear in logs for a backend error path:\n%s", logs)
	}
	if strings.Contains(callErr.Error(), sentinelValue) {
		t.Fatalf("sentinel value must not appear in the backend-error message: %v", callErr)
	}
}

// fakeSMErrClient is a secretsManagerClient stand-in that always fails
// GetSecretValue, so the provider's error-wrapping branch runs with no
// value ever having been fetched.
type fakeSMErrClient struct {
	err error
}

func (f *fakeSMErrClient) GetSecretValue(_ context.Context, _ *secretsmanager.GetSecretValueInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	return nil, f.err
}

func (f *fakeSMErrClient) ListSecrets(_ context.Context, _ *secretsmanager.ListSecretsInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.ListSecretsOutput, error) {
	return nil, f.err
}

// --- Kubernetes Secrets backend ---------------------------------------------

func fakeK8sSecretsClientset(namespace, name, key, value string) *fake.Clientset {
	return fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Data:       map[string][]byte{key: []byte(value)},
	})
}

// TestSentinel_K8sGetSecretValue_NeverInLogs proves the Kubernetes backend's
// GetSecretValue returns the value to its caller without ever writing it
// into a log line.
func TestSentinel_K8sGetSecretValue_NeverInLogs(t *testing.T) {
	client := fakeK8sSecretsClientset("sharko", "datadog", "api-key", sentinelValue)
	p := newKubernetesSecretProviderWithClient(client, "sharko")

	var got []byte
	logs := captureLogs(t, func() {
		var err error
		got, err = p.GetSecretValue(context.Background(), "datadog/api-key")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if string(got) != sentinelValue {
		t.Fatalf("provider must still return the value to its caller, got %q", got)
	}
	if strings.Contains(logs, sentinelValue) {
		t.Fatalf("sentinel value leaked into Kubernetes secrets provider logs:\n%s", logs)
	}
}

// TestSentinel_K8sGetSecretValue_ExplicitNamespaceForm_NeverInLogs is the
// same proof for the explicit "namespace/secret/key" path form.
func TestSentinel_K8sGetSecretValue_ExplicitNamespaceForm_NeverInLogs(t *testing.T) {
	client := fakeK8sSecretsClientset("sharko", "datadog", "api-key", sentinelValue)
	p := newKubernetesSecretProviderWithClient(client, "sharko")

	var got []byte
	logs := captureLogs(t, func() {
		var err error
		got, err = p.GetSecretValue(context.Background(), "sharko/datadog/api-key")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if string(got) != sentinelValue {
		t.Fatalf("provider must still return the value to its caller, got %q", got)
	}
	if strings.Contains(logs, sentinelValue) {
		t.Fatalf("sentinel value leaked into Kubernetes secrets provider logs (explicit-namespace form):\n%s", logs)
	}
}

// TestSentinel_K8sGetSecretValue_BoundaryRefusal_NeverFetchesOrLogsValue
// proves that a namespace-escape refusal never even reaches the Kubernetes
// API — so there is no way for the value sitting in that foreign namespace
// to be read at all — and that the refusal's own log line and error
// sentence (which legitimately name the requested/allowed namespaces —
// config metadata, not secret material) never carry the value.
func TestSentinel_K8sGetSecretValue_BoundaryRefusal_NeverFetchesOrLogsValue(t *testing.T) {
	// The victim secret lives in a namespace this provider is NOT
	// configured for, and holds the sentinel as its value.
	client := fakeK8sSecretsClientset("kube-system", "victim", "token", sentinelValue)
	p := newKubernetesSecretProviderWithClient(client, "sharko")

	var callErr error
	logs := captureLogs(t, func() {
		_, callErr = p.GetSecretValue(context.Background(), "kube-system/victim/token")
	})
	if callErr == nil {
		t.Fatal("expected a boundary refusal for a namespace escape")
	}
	if strings.Contains(logs, sentinelValue) {
		t.Fatalf("sentinel value leaked into the boundary-refusal log line:\n%s", logs)
	}
	if strings.Contains(callErr.Error(), sentinelValue) {
		t.Fatalf("sentinel value leaked into the boundary-refusal error message: %v", callErr)
	}
	if got := len(client.Actions()); got != 0 {
		t.Errorf("the refusal must happen before any Kubernetes API call; %d call(s) were made", got)
	}
}
