package providers

// testsupport.go — constructors other packages' tests use to drive the REAL
// providers without real AWS credentials.
//
// WHY THESE ARE HERE AND NOT IN A _test.go FILE. A Go _test.go file is only
// visible to its own package's tests. The security sentinel for the connection
// comparison lives in internal/api, and its whole point is to push a fake
// credential value through the ACTUAL AWS Secrets Manager provider — the real
// payload sniff, the real structured-EKS branch, the real logging — instead of
// through a hand-written double that could quietly behave differently from the
// thing being shipped. That test cannot reach an unexported field from another
// package, so the seam has to be exported.
//
// Nothing in here is wired into any production code path: no constructor in
// serve.go, no factory in provider.go, and no handler calls any of it. They only
// exist for a caller that already has a fake AWS client and a stubbed token
// function in hand.

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// fixedPayloadSMClient answers every AWS Secrets Manager read with one payload.
type fixedPayloadSMClient struct {
	payload string
}

func (c *fixedPayloadSMClient) GetSecretValue(_ context.Context, _ *secretsmanager.GetSecretValueInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	return &secretsmanager.GetSecretValueOutput{SecretString: aws.String(c.payload)}, nil
}

func (c *fixedPayloadSMClient) ListSecrets(_ context.Context, _ *secretsmanager.ListSecretsInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.ListSecretsOutput, error) {
	return &secretsmanager.ListSecretsOutput{}, nil
}

// NewAWSSecretsManagerProviderForTest builds a real AWSSecretsManagerProvider
// whose AWS Secrets Manager calls are served by a fixed payload and whose token
// mint is the supplied function.
//
// payload is what the backend returns for every secret lookup — a structured-EKS
// JSON descriptor or a raw kubeconfig, exactly as a real secret would hold it,
// so the provider's own format sniff decides the branch.
//
// eksTokenFn stands in for the STS mint. Pass a counter to prove a code path
// never mints: that is what the connection comparison's EKS test does, and the
// expected count there is zero.
func NewAWSSecretsManagerProviderForTest(payload string, eksTokenFn func(ctx context.Context, clusterName, region, roleARN string) (string, error)) *AWSSecretsManagerProvider {
	return &AWSSecretsManagerProvider{
		client:     &fixedPayloadSMClient{payload: payload},
		eksTokenFn: eksTokenFn,
	}
}

// NewArgoCDProviderWithFailingMintForTest builds a REAL ArgoCDProvider whose
// cluster Secret uses the AWS-IAM auth shape, backed by a fake Kubernetes
// client, with the STS token mint wired to the supplied function.
//
// This is the seam the credential-error sentinel test in internal/api needs, and
// it exists because the AWS Secrets Manager provider deliberately does NOT
// surface a mint failure: GetCredentials tries the prefixed name, then the exact
// name, and when both attempts fail — for ANY reason, including a failed mint —
// it returns its own "secret not found ... set secret_path" sentence. So on that
// backend the mint error only ever reaches a log line.
//
// The ArgoCD provider is where a mint failure genuinely travels OUTWARD: it
// wraps the mint error into ArgoCDProviderError.Detail, and internal/api hands
// Detail straight to the API response through writeStructuredError. That is the
// real path the fix has to cover, so that is the path the test drives.
//
// eksTokenFn stands in for the STS mint. Pass one that fails carrying a sentinel
// to prove the failure's text does not get out.
func NewArgoCDProviderWithFailingMintForTest(clusterName string, eksTokenFn func(ctx context.Context, clusterName, region, roleARN string) (string, error)) *ArgoCDProvider {
	config := fmt.Sprintf(`{"awsAuthConfig":{"clusterName":%q,"roleARN":"arn:aws:iam::000000000000:role/test-role"},"tlsClientConfig":{"insecure":true}}`, clusterName)
	client := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName,
			Namespace: "argocd",
			Labels:    map[string]string{"argocd.argoproj.io/secret-type": "cluster", "region": "eu-west-1"},
		},
		Data: map[string][]byte{
			"name":   []byte(clusterName),
			"server": []byte("https://abc123.gr7.eu-west-1.eks.example.com"),
			"config": []byte(config),
		},
	})
	p := newArgoCDProviderWithClient(client, "argocd")
	p.eksTokenFn = eksTokenFn
	return p
}

// NewArgoCDProviderWithFailingBackendForTest builds a REAL ArgoCDProvider whose
// backing Kubernetes reads all fail with backendErr.
//
// This is the shape that matters for the "raw provider error reaches a public
// boundary" proof, and it is a genuinely realistic one: the backend a credential
// is read FROM fails, and its own error text is what the provider wraps and
// returns. Pass a backendErr whose message carries a sentinel to prove that text
// never gets out.
//
// Unlike the mint-failure seam below, this error's text really does reach
// Error() — which is exactly the point. credsafe.Mark leaves Error() alone on
// purpose so internal callers keep working, and it is the public boundaries that
// must swap in the fixed sentence. If any of them forgets, the sentinel shows up
// and the test says where.
func NewArgoCDProviderWithFailingBackendForTest(backendErr error) *ArgoCDProvider {
	client := fake.NewSimpleClientset()
	client.PrependReactor("*", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, backendErr
	})
	return newArgoCDProviderWithClient(client, "argocd")
}

// NewFailingStoredFactsBackendForTest builds a cluster-credentials backend that
// fails every read with the given error.
//
// It exists for one specific proof: a secrets backend whose error message
// carries credential material in its own text — which real provider SDKs do —
// must not have that text passed through to a caller or into a log line.
func NewFailingStoredFactsBackendForTest(err error) ClusterCredentialsProvider {
	return &failingStoredFactsBackend{err: err}
}

type failingStoredFactsBackend struct {
	err error
}

func (b *failingStoredFactsBackend) StoredConnectionFacts(_ string) (*StoredConnectionFacts, error) {
	return nil, b.err
}
func (b *failingStoredFactsBackend) GetCredentials(_ string) (*Kubeconfig, error) {
	return nil, b.err
}
func (b *failingStoredFactsBackend) ListClusters() ([]ClusterInfo, error)     { return nil, b.err }
func (b *failingStoredFactsBackend) SearchSecrets(_ string) ([]string, error) { return nil, nil }
func (b *failingStoredFactsBackend) HealthCheck(_ context.Context) error      { return b.err }
