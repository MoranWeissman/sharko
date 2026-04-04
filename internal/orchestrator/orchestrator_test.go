package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// ---------- mock ArgoCD client ----------

type mockArgocd struct {
	registeredClusters map[string]string              // name -> server
	deletedServers     []string
	updatedLabels      map[string]map[string]string   // server -> labels
	syncedApps         []string
	existingClusters   []models.ArgocdCluster         // for ListClusters / duplicate check
	registerErr        error
	deleteErr          error
	updateLabelsErr    error
}

func newMockArgocd() *mockArgocd {
	return &mockArgocd{
		registeredClusters: make(map[string]string),
		updatedLabels:      make(map[string]map[string]string),
	}
}

func (m *mockArgocd) ListClusters(_ context.Context) ([]models.ArgocdCluster, error) {
	return m.existingClusters, nil
}

func (m *mockArgocd) RegisterCluster(_ context.Context, name, server string, _ []byte, _ string, _ map[string]string) error {
	if m.registerErr != nil {
		return m.registerErr
	}
	m.registeredClusters[name] = server
	return nil
}

func (m *mockArgocd) DeleteCluster(_ context.Context, serverURL string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.deletedServers = append(m.deletedServers, serverURL)
	return nil
}

func (m *mockArgocd) UpdateClusterLabels(_ context.Context, serverURL string, labels map[string]string) error {
	if m.updateLabelsErr != nil {
		return m.updateLabelsErr
	}
	m.updatedLabels[serverURL] = labels
	return nil
}

func (m *mockArgocd) SyncApplication(_ context.Context, appName string) error {
	m.syncedApps = append(m.syncedApps, appName)
	return nil
}

func (m *mockArgocd) CreateProject(_ context.Context, _ []byte) error {
	return nil
}

func (m *mockArgocd) CreateApplication(_ context.Context, _ []byte) error {
	return nil
}

// ---------- mock credentials provider ----------

type mockCredProvider struct {
	creds map[string]*providers.Kubeconfig
	err   error
}

func (m *mockCredProvider) GetCredentials(clusterName string) (*providers.Kubeconfig, error) {
	if m.err != nil {
		return nil, m.err
	}
	if c, ok := m.creds[clusterName]; ok {
		return c, nil
	}
	return nil, fmt.Errorf("cluster %q not found", clusterName)
}

func (m *mockCredProvider) ListClusters() ([]providers.ClusterInfo, error) {
	return nil, nil
}

// ---------- mock Git provider ----------

type mockGitProvider struct {
	files        map[string][]byte // path -> content (written files)
	deletedFiles []string
	branches     []string
	prs          []*gitprovider.PullRequest
	createErr    error
	deleteErr    error
	prErr        error
}

func newMockGitProvider() *mockGitProvider {
	return &mockGitProvider{
		files: make(map[string][]byte),
	}
}

func (m *mockGitProvider) GetFileContent(_ context.Context, path, _ string) ([]byte, error) {
	if c, ok := m.files[path]; ok {
		return c, nil
	}
	return nil, fmt.Errorf("file not found: %s", path)
}

func (m *mockGitProvider) ListDirectory(_ context.Context, _, _ string) ([]string, error) {
	return nil, nil
}

func (m *mockGitProvider) ListPullRequests(_ context.Context, _ string) ([]gitprovider.PullRequest, error) {
	return nil, nil
}

func (m *mockGitProvider) TestConnection(_ context.Context) error { return nil }

func (m *mockGitProvider) CreateBranch(_ context.Context, branchName, _ string) error {
	m.branches = append(m.branches, branchName)
	return nil
}

func (m *mockGitProvider) CreateOrUpdateFile(_ context.Context, path string, content []byte, _, _ string) error {
	if m.createErr != nil {
		return m.createErr
	}
	m.files[path] = content
	return nil
}

func (m *mockGitProvider) DeleteFile(_ context.Context, path, _, _ string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.deletedFiles = append(m.deletedFiles, path)
	delete(m.files, path)
	return nil
}

func (m *mockGitProvider) CreatePullRequest(_ context.Context, title, _, head, _ string) (*gitprovider.PullRequest, error) {
	if m.prErr != nil {
		return nil, m.prErr
	}
	pr := &gitprovider.PullRequest{
		ID:           len(m.prs) + 1,
		Title:        title,
		SourceBranch: head,
		URL:          fmt.Sprintf("https://github.com/example/repo/pull/%d", len(m.prs)+1),
	}
	m.prs = append(m.prs, pr)
	return pr, nil
}

func (m *mockGitProvider) MergePullRequest(_ context.Context, _ int) error { return nil }
func (m *mockGitProvider) DeleteBranch(_ context.Context, _ string) error  { return nil }

// ---------- helpers ----------

func defaultGitOps() GitOpsConfig {
	return GitOpsConfig{
		DefaultMode:  "direct",
		BranchPrefix: "sharko/",
		CommitPrefix: "sharko:",
		BaseBranch:   "main",
	}
}

func prGitOps() GitOpsConfig {
	cfg := defaultGitOps()
	cfg.DefaultMode = "pr"
	return cfg
}

func defaultPaths() RepoPathsConfig {
	return RepoPathsConfig{
		ClusterValues: "configuration/addons-clusters-values",
		GlobalValues:  "configuration/addons-global-values",
		Charts:        "charts",
		Bootstrap:     "bootstrap",
	}
}

func defaultCreds() *mockCredProvider {
	return &mockCredProvider{
		creds: map[string]*providers.Kubeconfig{
			"prod-eu": {
				Server: "https://k8s.example.com:6443",
				CAData: []byte("fake-ca"),
				Token:  "fake-token",
			},
		},
	}
}

// ---------- tests ----------

func TestRegisterCluster_DirectMode(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	orch := New(nil, defaultCreds(), argocd, git, defaultGitOps(), defaultPaths(), nil)

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:   "prod-eu",
		Addons: map[string]bool{"monitoring": true, "logging": false},
		Region: "eu-west-1",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected status 'success', got %q", result.Status)
	}
	if result.Cluster.Server != "https://k8s.example.com:6443" {
		t.Errorf("unexpected server: %s", result.Cluster.Server)
	}
	if _, ok := argocd.registeredClusters["prod-eu"]; !ok {
		t.Error("cluster not registered in ArgoCD")
	}
	if result.Git == nil {
		t.Fatal("expected Git result")
	}
	if result.Git.Mode != "direct" {
		t.Errorf("expected mode 'direct', got %q", result.Git.Mode)
	}

	valuesPath := "configuration/addons-clusters-values/prod-eu.yaml"
	if _, ok := git.files[valuesPath]; !ok {
		t.Errorf("values file not created at %s", valuesPath)
	}
}

func TestRegisterCluster_PRMode(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	orch := New(nil, defaultCreds(), argocd, git, prGitOps(), defaultPaths(), nil)

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:   "prod-eu",
		Addons: map[string]bool{"monitoring": true},
		Region: "us-east-1",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Git == nil || result.Git.Mode != "pr" {
		t.Fatal("expected PR mode result")
	}
	if result.Git.PRUrl == "" {
		t.Error("expected PR URL")
	}
	if len(git.branches) == 0 {
		t.Error("expected branch to be created")
	}
	if len(git.prs) == 0 {
		t.Error("expected PR to be created")
	}
}

func TestRegisterCluster_PartialSuccess_GitFails(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	git.createErr = fmt.Errorf("git API error")
	orch := New(nil, defaultCreds(), argocd, git, defaultGitOps(), defaultPaths(), nil)

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:   "prod-eu",
		Addons: map[string]bool{"monitoring": true},
	})

	if err != nil {
		t.Fatalf("expected partial success, not error: %v", err)
	}
	if result.Status != "partial" {
		t.Errorf("expected status 'partial', got %q", result.Status)
	}
	if result.FailedStep != "git_commit" {
		t.Errorf("expected failed step 'git_commit', got %q", result.FailedStep)
	}
	// Cluster should still be registered in ArgoCD.
	if _, ok := argocd.registeredClusters["prod-eu"]; !ok {
		t.Error("cluster should remain registered in ArgoCD after Git failure")
	}
}

func TestRegisterCluster_ProviderFails(t *testing.T) {
	creds := &mockCredProvider{err: fmt.Errorf("vault unavailable")}
	orch := New(nil, creds, newMockArgocd(), newMockGitProvider(), defaultGitOps(), defaultPaths(), nil)

	_, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name: "prod-eu",
	})

	if err == nil {
		t.Fatal("expected error when provider fails")
	}
	if !strings.Contains(err.Error(), "vault unavailable") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestRegisterCluster_InvalidName(t *testing.T) {
	orch := New(nil, defaultCreds(), newMockArgocd(), newMockGitProvider(), defaultGitOps(), defaultPaths(), nil)

	_, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name: "",
	})
	if err == nil {
		t.Error("expected error for empty name")
	}

	_, err = orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name: "-invalid",
	})
	if err == nil {
		t.Error("expected error for name starting with hyphen")
	}
}

func TestDeregisterCluster(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	orch := New(nil, defaultCreds(), argocd, git, defaultGitOps(), defaultPaths(), nil)

	result, err := orch.DeregisterCluster(context.Background(), "prod-eu", "https://k8s.example.com:6443")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(argocd.deletedServers) != 1 {
		t.Error("expected cluster to be deleted from ArgoCD")
	}
	if len(git.deletedFiles) != 1 {
		t.Error("expected values file to be deleted")
	}
	if result.Status != "success" {
		t.Errorf("expected success status, got %q", result.Status)
	}
	if result.Git == nil || result.Git.Mode != "direct" {
		t.Error("expected direct mode git result")
	}
}

func TestUpdateClusterAddons(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	orch := New(nil, defaultCreds(), argocd, git, defaultGitOps(), defaultPaths(), nil)

	result, err := orch.UpdateClusterAddons(context.Background(), "prod-eu", "https://k8s.example.com:6443", "eu-west-1",
		map[string]bool{"monitoring": true, "logging": true})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	labels := argocd.updatedLabels["https://k8s.example.com:6443"]
	if labels["monitoring"] != "enabled" {
		t.Error("expected monitoring label to be 'enabled'")
	}

	valuesPath := "configuration/addons-clusters-values/prod-eu.yaml"
	if _, ok := git.files[valuesPath]; !ok {
		t.Error("expected values file to be updated")
	}
	if result.Status != "success" {
		t.Errorf("expected success status, got %q", result.Status)
	}
	if result.Git == nil || result.Git.ValuesFile != valuesPath {
		t.Error("expected values file path in git result")
	}
}

func TestAddAddon(t *testing.T) {
	git := newMockGitProvider()
	orch := New(nil, defaultCreds(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)

	result, err := orch.AddAddon(context.Background(), AddAddonRequest{
		Name:      "prometheus",
		Chart:     "kube-prometheus-stack",
		RepoURL:   "https://prometheus-community.github.io/helm-charts",
		Version:   "45.0.0",
		Namespace: "monitoring",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Mode != "direct" {
		t.Errorf("expected direct mode, got %q", result.Mode)
	}

	catalogPath := "charts/prometheus/addon.yaml"
	if _, ok := git.files[catalogPath]; !ok {
		t.Errorf("catalog file not created at %s", catalogPath)
	}

	globalPath := "configuration/addons-global-values/prometheus.yaml"
	if _, ok := git.files[globalPath]; !ok {
		t.Errorf("global values file not created at %s", globalPath)
	}
}

func TestRemoveAddon(t *testing.T) {
	git := newMockGitProvider()
	orch := New(nil, defaultCreds(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)

	result, err := orch.RemoveAddon(context.Background(), "prometheus")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(git.deletedFiles) != 2 {
		t.Errorf("expected 2 files deleted, got %d", len(git.deletedFiles))
	}
	if result.Mode != "direct" {
		t.Errorf("expected direct mode, got %q", result.Mode)
	}
}

func TestGenerateClusterValues(t *testing.T) {
	content := generateClusterValues("prod-eu", "eu-west-1", map[string]bool{
		"monitoring": true,
		"logging":    false,
	})

	s := string(content)

	if !strings.Contains(s, "region: eu-west-1") {
		t.Error("expected region in output")
	}
	if !strings.Contains(s, "monitoring:\n  enabled: true") {
		t.Error("expected monitoring enabled")
	}
	if !strings.Contains(s, "logging:\n  enabled: false") {
		t.Error("expected logging disabled")
	}
}

func TestGenerateClusterValues_NoAddons(t *testing.T) {
	content := generateClusterValues("test", "us-east-1", nil)
	s := string(content)

	if !strings.Contains(s, "region: us-east-1") {
		t.Error("expected region in output")
	}
	// Should not contain addon sections.
	if strings.Contains(s, "enabled:") {
		t.Error("unexpected addon section in output with nil addons")
	}
}

func TestRegisterCluster_DuplicateReturnsError(t *testing.T) {
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{
		{Name: "prod-eu", Server: "https://k8s.example.com:6443"},
	}
	git := newMockGitProvider()
	orch := New(nil, defaultCreds(), argocd, git, defaultGitOps(), defaultPaths(), nil)

	_, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:   "prod-eu",
		Addons: map[string]bool{"monitoring": true},
	})

	if err == nil {
		t.Fatal("expected error for duplicate cluster")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected 'already exists' error, got: %v", err)
	}
	// Should NOT have registered in ArgoCD.
	if _, ok := argocd.registeredClusters["prod-eu"]; ok {
		t.Error("duplicate cluster should not be registered in ArgoCD")
	}
}

func TestRegisterCluster_ConcurrentDifferentClusters(t *testing.T) {
	mu := &sync.Mutex{}
	creds := &mockCredProvider{
		creds: map[string]*providers.Kubeconfig{
			"cluster-a": {Server: "https://a.example.com:6443", CAData: []byte("ca-a"), Token: "tok-a"},
			"cluster-b": {Server: "https://b.example.com:6443", CAData: []byte("ca-b"), Token: "tok-b"},
		},
	}

	argocd := newMockArgocd()
	git := newMockGitProvider()
	// Two orchestrators sharing the same mutex (simulates two concurrent requests).
	orchA := New(mu, creds, argocd, git, prGitOps(), defaultPaths(), nil)
	orchB := New(mu, creds, argocd, git, prGitOps(), defaultPaths(), nil)

	var errA, errB error
	var resultA, resultB *RegisterClusterResult
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		resultA, errA = orchA.RegisterCluster(context.Background(), RegisterClusterRequest{
			Name: "cluster-a", Addons: map[string]bool{"monitoring": true},
		})
	}()
	go func() {
		defer wg.Done()
		resultB, errB = orchB.RegisterCluster(context.Background(), RegisterClusterRequest{
			Name: "cluster-b", Addons: map[string]bool{"logging": true},
		})
	}()
	wg.Wait()

	if errA != nil {
		t.Fatalf("cluster-a failed: %v", errA)
	}
	if errB != nil {
		t.Fatalf("cluster-b failed: %v", errB)
	}
	if resultA.Status != "success" {
		t.Errorf("cluster-a: expected success, got %q", resultA.Status)
	}
	if resultB.Status != "success" {
		t.Errorf("cluster-b: expected success, got %q", resultB.Status)
	}
	// Both should have created branches (PR mode).
	if len(git.branches) != 2 {
		t.Errorf("expected 2 branches, got %d", len(git.branches))
	}
	// Both should be registered in ArgoCD.
	if _, ok := argocd.registeredClusters["cluster-a"]; !ok {
		t.Error("cluster-a not registered in ArgoCD")
	}
	if _, ok := argocd.registeredClusters["cluster-b"]; !ok {
		t.Error("cluster-b not registered in ArgoCD")
	}
}
