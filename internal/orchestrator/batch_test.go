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

	// V125-1-8.3: batch register goes through RegisterCluster → reconciler.
	// No direct ArgoCD API calls happen pre-merge.
	if len(argocd.registeredClusters) != 0 {
		t.Errorf("V125-1-8.3: expected 0 direct ArgoCD registrations (reconciler owns them), got %d", len(argocd.registeredClusters))
	}
}

func TestRegisterClusterBatch_OneFailure(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	// Credentials are OPTIONAL at registration for every cluster
	// (V2-cleanup-88.3 — lazy credentials), so a missing credProvider entry
	// no longer fails a batch member. Trigger the one genuine failure via a
	// referential-integrity rejection instead — prod-us asks for an addon
	// that is not in the seeded catalog (defaultTestCatalogYAML).
	creds := &mockCredProvider{
		creds: map[string]*providers.Kubeconfig{
			"prod-eu": {Server: "https://eu.example.com:6443", CAData: []byte("ca"), Token: "tok"},
			"staging": {Server: "https://stg.example.com:6443", CAData: []byte("ca"), Token: "tok"},
		},
	}

	orch := New(nil, creds, argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	requests := []RegisterClusterRequest{
		{Name: "prod-eu", Addons: map[string]bool{"monitoring": true}, Region: "eu-west-1"},
		{Name: "prod-us", Addons: map[string]bool{"nonexistent-addon": true}, Region: "us-east-1"},
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

	// The failed cluster should be prod-us (addon not in catalog).
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

	// V125-1-8.3: the two successful clusters do NOT direct-register in
	// ArgoCD (reconciler owns Secret writes). The orchestrator returns
	// success because the PR landed and the reconciler trigger fired.
	if _, ok := argocd.registeredClusters["prod-eu"]; ok {
		t.Error("V125-1-8.3: prod-eu must NOT be direct-registered in ArgoCD (reconciler owns it)")
	}
	if _, ok := argocd.registeredClusters["staging"]; ok {
		t.Error("V125-1-8.3: staging must NOT be direct-registered in ArgoCD (reconciler owns it)")
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

// TestRegisterClusterBatch_EveryClusterPartial_IsNotCountedAsAllFailed is the
// R2-7 regression pin.
//
// A "partial" cluster is not a failure. Its pull request was opened (and here
// the merge is what broke), its Secrets were written, and nothing was rolled
// back — real things changed in Git and in the operator's cluster. The batch
// counters used to have nowhere to put that, so every partial was added to
// Failed and nothing else. A batch where EVERY cluster came back partial then
// looked identical to a batch where nothing at all had happened, which is what
// the audit trail went on to report.
//
// The merge failure is the real production path: RegisterCluster opens the PR,
// the merge throws, and it returns status "partial" with FailedStep "pr_merge"
// (see TestRegisterCluster_AutoMergeFails).
func TestRegisterClusterBatch_EveryClusterPartial_IsNotCountedAsAllFailed(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	git.mergeErr = fmt.Errorf("merge conflict")

	creds := &mockCredProvider{
		creds: map[string]*providers.Kubeconfig{
			"prod-eu": {Server: "https://eu.example.com:6443", CAData: []byte("ca"), Token: "tok"},
			"prod-us": {Server: "https://us.example.com:6443", CAData: []byte("ca"), Token: "tok"},
		},
	}
	orch := New(nil, creds, argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	result := orch.RegisterClusterBatch(context.Background(), []RegisterClusterRequest{
		{Name: "prod-eu", Addons: map[string]bool{"monitoring": true}, Region: "eu-west-1"},
		{Name: "prod-us", Addons: map[string]bool{"monitoring": true}, Region: "us-east-1"},
	})

	// The fixture has to actually produce partials, or this test proves
	// nothing at all.
	for i, r := range result.Results {
		if r.Status != "partial" {
			t.Fatalf("result[%d] is %q, not \"partial\" — the fixture no longer produces the shape this test exists for (error: %s)",
				i, r.Status, r.Error)
		}
	}

	if result.Total != 2 {
		t.Errorf("total = %d, want 2", result.Total)
	}
	if result.Partial != 2 {
		t.Errorf("partial = %d, want 2 — both clusters opened a pull request and changed real things", result.Partial)
	}
	if result.HardFailed() != 0 {
		t.Errorf("hard failures = %d, want 0 — nothing failed before it had done any work", result.HardFailed())
	}
	if !result.AnythingApplied() {
		t.Error("AnythingApplied() = false, but two pull requests were opened — this is the false \"nothing changed\" the audit trail reported")
	}

	// The wire meaning of `failed` is unchanged: it has always counted
	// hard failures AND partials, the 207 status is derived from it, and
	// POST /api/v1/clusters/batch is a stable endpoint.
	if result.Failed != 2 {
		t.Errorf("failed = %d, want 2 — the wire field must keep counting failures plus partials", result.Failed)
	}
	if result.Succeeded+result.Failed != result.Total {
		t.Errorf("succeeded(%d) + failed(%d) != total(%d) — partial must stay a subset of failed, not a third bucket",
			result.Succeeded, result.Failed, result.Total)
	}
}

// TestRegisterClusterBatch_MixOfPartialAndHardFailure separates the two things
// Failed lumps together: one cluster that changed something before stopping,
// and one that never got off the ground.
func TestRegisterClusterBatch_MixOfPartialAndHardFailure(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	git.mergeErr = fmt.Errorf("merge conflict")

	creds := &mockCredProvider{
		creds: map[string]*providers.Kubeconfig{
			"prod-eu": {Server: "https://eu.example.com:6443", CAData: []byte("ca"), Token: "tok"},
		},
	}
	orch := New(nil, creds, argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	// prod-us asks for an addon that is not in the seeded catalog, which is
	// a referential-integrity rejection before any Git work — the same lever
	// TestRegisterClusterBatch_OneFailure uses.
	result := orch.RegisterClusterBatch(context.Background(), []RegisterClusterRequest{
		{Name: "prod-eu", Addons: map[string]bool{"monitoring": true}, Region: "eu-west-1"},
		{Name: "prod-us", Addons: map[string]bool{"nonexistent-addon": true}, Region: "us-east-1"},
	})

	statuses := map[string]int{}
	for _, r := range result.Results {
		statuses[r.Status]++
	}
	if statuses["partial"] != 1 || statuses["failed"] != 1 {
		t.Fatalf("fixture produced %v, want one partial and one failed", statuses)
	}

	if result.Partial != 1 {
		t.Errorf("partial = %d, want 1", result.Partial)
	}
	if result.HardFailed() != 1 {
		t.Errorf("hard failures = %d, want 1", result.HardFailed())
	}
	if result.Failed != 2 {
		t.Errorf("failed = %d, want 2 — unchanged wire meaning: failures plus partials", result.Failed)
	}
	if !result.AnythingApplied() {
		t.Error("AnythingApplied() = false, but prod-eu opened a pull request")
	}
}

// TestRegisterClusterBatch_AllHardFailed_AppliesNothing pins the other
// direction, so the fix cannot be "call everything partial".
func TestRegisterClusterBatch_AllHardFailed_AppliesNothing(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	orch := New(nil, &mockCredProvider{creds: map[string]*providers.Kubeconfig{}}, argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	result := orch.RegisterClusterBatch(context.Background(), []RegisterClusterRequest{
		{Name: "prod-eu", Addons: map[string]bool{"nonexistent-addon": true}},
		{Name: "prod-us", Addons: map[string]bool{"nonexistent-addon": true}},
	})

	for i, r := range result.Results {
		if r.Status != "failed" {
			t.Fatalf("result[%d] is %q, not \"failed\" — fixture no longer produces hard failures", i, r.Status)
		}
	}
	if result.Partial != 0 {
		t.Errorf("partial = %d, want 0", result.Partial)
	}
	if result.HardFailed() != 2 {
		t.Errorf("hard failures = %d, want 2", result.HardFailed())
	}
	if result.AnythingApplied() {
		t.Error("AnythingApplied() = true, but nothing was written anywhere")
	}
}
