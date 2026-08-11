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

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
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
