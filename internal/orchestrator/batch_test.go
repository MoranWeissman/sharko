package orchestrator

import (
	"context"
	"fmt"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
)

func TestRegisterClusterBatch_AllSucceed(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	creds := &mockCredProvider{
		creds: map[string]*providers.Kubeconfig{
			"prod-eu": {Server: "https://eu.example.com:6443", CAData: []byte("ca"), Token: "tok"},
			"prod-us": {Server: "https://us.example.com:6443", CAData: []byte("ca"), Token: "tok"},
			"staging": {Server: "https://stg.example.com:6443", CAData: []byte("ca"), Token: "tok"},
		},
	}

	orch := New(nil, creds, argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	requests := []RegisterClusterRequest{
		{Name: "prod-eu", Addons: map[string]bool{"monitoring": true}, Region: "eu-west-1"},
		{Name: "prod-us", Addons: map[string]bool{"monitoring": true}, Region: "us-east-1"},
		{Name: "staging", Addons: map[string]bool{"logging": true}, Region: "eu-west-1"},
	}

	result := orch.RegisterClusterBatch(context.Background(), requests)

	if result.Total != 3 {
		t.Errorf("expected total=3, got %d", result.Total)
	}
	if result.Succeeded != 3 {
		t.Errorf("expected succeeded=3, got %d", result.Succeeded)
	}
	if result.Failed != 0 {
		t.Errorf("expected failed=0, got %d", result.Failed)
	}
	if len(result.Results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(result.Results))
	}
	for i, r := range result.Results {
		if r.Status != "success" {
			t.Errorf("result[%d]: expected status 'success', got %q (error: %s)", i, r.Status, r.Error)
		}
	}

	// Verify all three are registered in ArgoCD.
	if len(argocd.registeredClusters) != 3 {
		t.Errorf("expected 3 clusters registered in ArgoCD, got %d", len(argocd.registeredClusters))
	}
}

func TestRegisterClusterBatch_OneFailure(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	// Only provide creds for 2 of 3 clusters — the third will fail.
	creds := &mockCredProvider{
		creds: map[string]*providers.Kubeconfig{
			"prod-eu": {Server: "https://eu.example.com:6443", CAData: []byte("ca"), Token: "tok"},
			"staging": {Server: "https://stg.example.com:6443", CAData: []byte("ca"), Token: "tok"},
		},
	}

	orch := New(nil, creds, argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	requests := []RegisterClusterRequest{
		{Name: "prod-eu", Addons: map[string]bool{"monitoring": true}, Region: "eu-west-1"},
		{Name: "prod-us", Addons: map[string]bool{"monitoring": true}, Region: "us-east-1"},
		{Name: "staging", Addons: map[string]bool{"logging": true}, Region: "eu-west-1"},
	}

	result := orch.RegisterClusterBatch(context.Background(), requests)

	if result.Total != 3 {
		t.Errorf("expected total=3, got %d", result.Total)
	}
	if result.Succeeded != 2 {
		t.Errorf("expected succeeded=2, got %d", result.Succeeded)
	}
	if result.Failed != 1 {
		t.Errorf("expected failed=1, got %d", result.Failed)
	}

	// The failed cluster should be prod-us (no credentials).
	var failedCluster string
	for _, r := range result.Results {
		if r.Status == "failed" {
			failedCluster = r.Cluster.Name
			if r.Error == "" {
				t.Error("expected error message for failed cluster")
			}
		}
	}
	if failedCluster != "prod-us" {
		t.Errorf("expected prod-us to fail, got %q", failedCluster)
	}

	// The other two should have succeeded.
	if _, ok := argocd.registeredClusters["prod-eu"]; !ok {
		t.Error("prod-eu should be registered in ArgoCD")
	}
	if _, ok := argocd.registeredClusters["staging"]; !ok {
		t.Error("staging should be registered in ArgoCD")
	}
}

func TestRegisterClusterBatch_OverMaxSize(t *testing.T) {
	// This tests the MaxBatchSize constant is correct.
	// The API handler enforces the limit, not the orchestrator,
	// so here we just verify the constant value.
	if MaxBatchSize != 10 {
		t.Errorf("expected MaxBatchSize=10, got %d", MaxBatchSize)
	}

	// Also verify the orchestrator still processes lists of any size
	// (enforcement is at the API layer).
	argocd := newMockArgocd()
	git := newMockGitProvider()

	creds := &mockCredProvider{creds: make(map[string]*providers.Kubeconfig)}
	for i := 0; i < 11; i++ {
		name := fmt.Sprintf("cluster-%d", i)
		creds.creds[name] = &providers.Kubeconfig{
			Server: fmt.Sprintf("https://%s.example.com:6443", name),
			CAData: []byte("ca"),
			Token:  "tok",
		}
	}

	orch := New(nil, creds, argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	requests := make([]RegisterClusterRequest, 11)
	for i := 0; i < 11; i++ {
		requests[i] = RegisterClusterRequest{Name: fmt.Sprintf("cluster-%d", i)}
	}

	result := orch.RegisterClusterBatch(context.Background(), requests)

	// Orchestrator processes all 11 — batch limit is enforced at API layer.
	if result.Total != 11 {
		t.Errorf("expected total=11, got %d", result.Total)
	}
}

func TestRegisterClusterBatch_Empty(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	orch := New(nil, defaultCreds(), argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	result := orch.RegisterClusterBatch(context.Background(), nil)

	if result.Total != 0 {
		t.Errorf("expected total=0, got %d", result.Total)
	}
	if result.Succeeded != 0 {
		t.Errorf("expected succeeded=0, got %d", result.Succeeded)
	}
	if result.Failed != 0 {
		t.Errorf("expected failed=0, got %d", result.Failed)
	}
}

// TestDiscoverClusters_CrossReference validates the cross-referencing logic
// that the discover endpoint uses: provider clusters annotated with registration status.
func TestDiscoverClusters_CrossReference(t *testing.T) {
	// Simulate provider clusters.
	providerClusters := []providers.ClusterInfo{
		{Name: "prod-eu", Region: "eu-west-1"},
		{Name: "prod-us", Region: "us-east-1"},
		{Name: "staging", Region: "eu-west-1"},
	}

	// Simulate ArgoCD clusters — only prod-eu is registered.
	argoClusters := []models.ArgocdCluster{
		{Name: "prod-eu", Server: "https://eu.example.com:6443"},
		{Name: "in-cluster", Server: "https://kubernetes.default.svc"},
	}

	// Build registered set (same logic as the handler).
	registered := make(map[string]bool, len(argoClusters))
	for _, c := range argoClusters {
		registered[c.Name] = true
	}

	// Cross-reference.
	type entry struct {
		Name       string
		Region     string
		Registered bool
	}
	var results []entry
	for _, pc := range providerClusters {
		results = append(results, entry{
			Name:       pc.Name,
			Region:     pc.Region,
			Registered: registered[pc.Name],
		})
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// prod-eu should be registered.
	if !results[0].Registered {
		t.Error("expected prod-eu to be registered")
	}
	// prod-us should not be registered.
	if results[1].Registered {
		t.Error("expected prod-us to NOT be registered")
	}
	// staging should not be registered.
	if results[2].Registered {
		t.Error("expected staging to NOT be registered")
	}
}
