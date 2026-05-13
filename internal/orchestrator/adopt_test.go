package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

// ---------- mock ArgoSecretManager ----------

type mockArgoSecretManager struct {
	ensureCalls    []ArgoSecretSpec
	annotations    map[string]map[string]string // cluster -> key -> value
	managedByLabel map[string]string            // cluster -> label value
	unadopted      []string
	ensureErr      error
	annotationErr  error
	labelErr       error
	unadoptErr     error
}

func newMockArgoSecretManager() *mockArgoSecretManager {
	return &mockArgoSecretManager{
		annotations:    make(map[string]map[string]string),
		managedByLabel: make(map[string]string),
	}
}

func (m *mockArgoSecretManager) Ensure(_ context.Context, spec ArgoSecretSpec) error {
	m.ensureCalls = append(m.ensureCalls, spec)
	return m.ensureErr
}

func (m *mockArgoSecretManager) SetAnnotation(_ context.Context, name, key, value string) error {
	if m.annotationErr != nil {
		return m.annotationErr
	}
	if m.annotations[name] == nil {
		m.annotations[name] = make(map[string]string)
	}
	m.annotations[name][key] = value
	return nil
}

func (m *mockArgoSecretManager) GetAnnotation(_ context.Context, name, key string) (string, error) {
	if m.annotationErr != nil {
		return "", m.annotationErr
	}
	if m.annotations[name] == nil {
		return "", nil
	}
	return m.annotations[name][key], nil
}

func (m *mockArgoSecretManager) GetManagedByLabel(_ context.Context, name string) (string, error) {
	if m.labelErr != nil {
		return "", m.labelErr
	}
	return m.managedByLabel[name], nil
}

func (m *mockArgoSecretManager) Unadopt(_ context.Context, name string) error {
	if m.unadoptErr != nil {
		return m.unadoptErr
	}
	m.unadopted = append(m.unadopted, name)
	return nil
}

// ---------- adopt tests ----------

func TestAdoptClusters_Success(t *testing.T) {
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{
		{Name: "cluster-a", Server: "https://a.example.com"},
		{Name: "cluster-b", Server: "https://b.example.com"},
	}

	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters:\n")

	asm := newMockArgoSecretManager()

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(asm, "")

	autoMerge := true
	result, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		Clusters:  []string{"cluster-a", "cluster-b"},
		AutoMerge: &autoMerge,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result.Results))
	}
	for _, cr := range result.Results {
		if cr.Status != "success" {
			t.Errorf("cluster %s: expected success, got %s (error: %s)", cr.Name, cr.Status, cr.Error)
		}
		if cr.Git == nil {
			t.Errorf("cluster %s: expected git result", cr.Name)
		}
	}

	// Check that adopted annotation was set.
	for _, name := range []string{"cluster-a", "cluster-b"} {
		if asm.annotations[name][AnnotationAdopted] != "true" {
			t.Errorf("expected adopted annotation on %s", name)
		}
	}
}

func TestAdoptClusters_ClusterNotInArgoCD(t *testing.T) {
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{
		{Name: "cluster-a", Server: "https://a.example.com"},
	}

	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters:\n")

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	result, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		Clusters: []string{"cluster-a", "not-in-argocd"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result.Results))
	}

	// cluster-a should succeed, not-in-argocd should fail.
	if result.Results[0].Status == "failed" && result.Results[0].Name == "cluster-a" {
		t.Error("cluster-a should have succeeded")
	}
	var failedResult *AdoptClusterResult
	for i := range result.Results {
		if result.Results[i].Name == "not-in-argocd" {
			failedResult = &result.Results[i]
		}
	}
	if failedResult == nil || failedResult.Status != "failed" {
		t.Error("expected not-in-argocd to fail")
	}
	if failedResult != nil && !strings.Contains(failedResult.Error, "not found in ArgoCD") {
		t.Errorf("unexpected error: %s", failedResult.Error)
	}
}

func TestAdoptClusters_RejectManagedByOther(t *testing.T) {
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{
		{Name: "cluster-a", Server: "https://a.example.com"},
	}

	git := newMockGitProvider()

	asm := newMockArgoSecretManager()
	asm.managedByLabel["cluster-a"] = "other-tool"

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(asm, "")

	result, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		Clusters: []string{"cluster-a"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Results[0].Status != "failed" {
		t.Errorf("expected failed, got %s", result.Results[0].Status)
	}
	if !strings.Contains(result.Results[0].Error, "managed by") {
		t.Errorf("expected managed-by error, got: %s", result.Results[0].Error)
	}
}

func TestAdoptClusters_DryRun(t *testing.T) {
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{
		{Name: "cluster-a", Server: "https://a.example.com"},
	}

	git := newMockGitProvider()

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	result, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		Clusters: []string{"cluster-a"},
		DryRun:   true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Results[0].DryRun == nil {
		t.Fatal("expected dry_run result")
	}
	if result.Results[0].DryRun.PRTitle == "" {
		t.Error("expected PR title in dry run")
	}
	// No PRs should have been created.
	if len(git.prs) > 0 {
		t.Error("expected no PRs in dry-run mode")
	}
}

func TestAdoptClusters_WithVerification(t *testing.T) {
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{
		{Name: "cluster-a", Server: "https://a.example.com"},
	}

	creds := &mockCredProvider{
		creds: map[string]*providers.Kubeconfig{
			"cluster-a": {
				Server: "https://a.example.com",
				CAData: []byte("fake-ca"),
				Token:  "fake-token",
				Raw:    []byte("fake-kubeconfig"),
			},
		},
	}

	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters:\n")

	fakeClientFn := func(_ []byte) (kubernetes.Interface, error) {
		return fake.NewSimpleClientset(), nil
	}

	orch := New(nil, creds, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetSecretManagement(nil, nil, fakeClientFn)

	result, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		Clusters: []string{"cluster-a"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cr := result.Results[0]
	if cr.Verification == nil {
		t.Fatal("expected verification result")
	}
	if !cr.Verification.Success {
		t.Errorf("expected verification to pass, got error: %s", cr.Verification.ErrorMessage)
	}
}

func TestAdoptClusters_EmptyRequest(t *testing.T) {
	orch := New(nil, nil, newMockArgocd(), newMockGitProvider(), autoMergeGitOps(), defaultPaths(), nil)
	_, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{})
	if err == nil {
		t.Fatal("expected error for empty clusters")
	}
}
