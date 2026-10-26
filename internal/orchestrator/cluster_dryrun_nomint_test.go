package orchestrator

import (
	"context"
	"slices"
	"sync"
	"testing"

	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/providers"
)

// ep4: `sharko add-clusters` checks every cluster with a dry run before it
// asks. A dry run must never make an EKS sign-in token. GetCredentials makes
// one every time it is called on stored EKS details, so these tests count
// GetCredentials calls.

// eksDetailsBackend holds EKS details (no login) for every cluster, like a
// structured EKS secret in AWS SM. Each GetCredentials call is one mint.
type eksDetailsBackend struct {
	mu    sync.Mutex
	mints int
}

func (b *eksDetailsBackend) GetCredentials(string) (*providers.Kubeconfig, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.mints++
	return &providers.Kubeconfig{Server: "https://eks.example.invalid", Token: "k8s-aws-v1.minted", Raw: []byte("kc")}, nil
}

func (b *eksDetailsBackend) StoredConnectionFacts(string) (*providers.StoredConnectionFacts, error) {
	return &providers.StoredConnectionFacts{Server: "https://eks.example.invalid", CredentialMintedPerFetch: true}, nil
}

func (b *eksDetailsBackend) ListClusters() ([]providers.ClusterInfo, error) { return nil, nil }
func (b *eksDetailsBackend) SearchSecrets(string) ([]string, error)         { return nil, nil }
func (b *eksDetailsBackend) HealthCheck(context.Context) error              { return nil }
func (b *eksDetailsBackend) count() int                                     { b.mu.Lock(); defer b.mu.Unlock(); return b.mints }

// mintOnlyBackend cannot be asked read-only at all. Calling it might mint.
type mintOnlyBackend struct{ eksDetailsBackend }

func (b *mintOnlyBackend) StoredConnectionFacts() {} // shadows the capability: not the interface's signature

func TestRegisterCluster_DryRun_EKSDetailsMakeNoToken(t *testing.T) {
	backend := &eksDetailsBackend{}
	orch := New(nil, backend, newMockArgocd(), newMockGitProvider(), defaultGitOps(), defaultPaths(), nil)
	orch.SetSecretManagement(nil, nil, fakeClientFactoryFor(fake.NewSimpleClientset()))

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name: "eks-a", Region: "us-east-1", CredsSource: "eks-token", DryRun: true,
	})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if got := backend.count(); got != 0 {
		t.Fatalf("a dry run made %d EKS sign-in tokens, want 0", got)
	}
	if !slices.Contains(result.CompletedSteps, "fetch_credentials") {
		t.Errorf("the dry run found the stored EKS details but did not say so: steps %v", result.CompletedSteps)
	}
	if slices.Contains(result.CompletedSteps, "skip_credentials") {
		t.Errorf("the dry run says there is no login, but the details are stored: steps %v", result.CompletedSteps)
	}
	if result.Cluster.Server != "https://eks.example.invalid" {
		t.Errorf("server = %q, want the stored one", result.Cluster.Server)
	}
	if result.DryRun == nil {
		t.Fatal("expected a preview")
	}
}

func TestRegisterCluster_DryRun_BackendWithoutReadOnlyAskMakesNoToken(t *testing.T) {
	backend := &mintOnlyBackend{}
	if _, ok := providers.ClusterCredentialsProvider(backend).(providers.StoredConnectionFactsProvider); ok {
		t.Fatal("setup: mintOnlyBackend must not have the read-only capability")
	}
	orch := New(nil, backend, newMockArgocd(), newMockGitProvider(), defaultGitOps(), defaultPaths(), nil)

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name: "eks-b", Region: "us-east-1", CredsSource: "eks-token", DryRun: true,
	})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if got := backend.count(); got != 0 {
		t.Fatalf("a dry run asked a backend that may mint (%d calls), want 0", got)
	}
	if !slices.Contains(result.CompletedSteps, "skip_credentials_preview") {
		t.Errorf("steps %v, want skip_credentials_preview", result.CompletedSteps)
	}
}

func TestRegisterCluster_RealRun_StillFetchesOnce(t *testing.T) {
	backend := &eksDetailsBackend{}
	orch := New(nil, backend, newMockArgocd(), newMockGitProvider(), defaultGitOps(), defaultPaths(), nil)

	if _, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name: "eks-c", Region: "us-east-1", CredsSource: "eks-token",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if got := backend.count(); got != 1 {
		t.Fatalf("a real registration fetched %d times, want 1 (unchanged)", got)
	}
}
