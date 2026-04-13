package orchestrator

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

// ---------- mock ArgoCD client ----------

type mockArgocd struct {
	mu                 sync.Mutex
	registeredClusters map[string]string              // name -> server
	deletedServers     []string
	updatedLabels      map[string]map[string]string   // server -> labels
	syncedApps         []string
	existingClusters   []models.ArgocdCluster         // for ListClusters / duplicate check
	addedRepos         []string                        // repo URLs added via AddRepository
	applications       map[string]*models.ArgocdApplication // name -> app (for GetApplication)
	registerErr        error
	deleteErr          error
	updateLabelsErr    error
	addRepoErr         error
	getAppErr          error
}

func newMockArgocd() *mockArgocd {
	return &mockArgocd{
		registeredClusters: make(map[string]string),
		updatedLabels:      make(map[string]map[string]string),
	}
}

func (m *mockArgocd) ListClusters(_ context.Context) ([]models.ArgocdCluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.existingClusters, nil
}

func (m *mockArgocd) RegisterCluster(_ context.Context, name, server string, _ []byte, _ string, _ map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.registerErr != nil {
		return m.registerErr
	}
	m.registeredClusters[name] = server
	return nil
}

func (m *mockArgocd) DeleteCluster(_ context.Context, serverURL string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.deletedServers = append(m.deletedServers, serverURL)
	return nil
}

func (m *mockArgocd) UpdateClusterLabels(_ context.Context, serverURL string, labels map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.updateLabelsErr != nil {
		return m.updateLabelsErr
	}
	m.updatedLabels[serverURL] = labels
	return nil
}

func (m *mockArgocd) SyncApplication(_ context.Context, appName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.syncedApps = append(m.syncedApps, appName)
	return nil
}

func (m *mockArgocd) CreateProject(_ context.Context, _ []byte) error {
	return nil
}

func (m *mockArgocd) CreateApplication(_ context.Context, _ []byte) error {
	return nil
}

func (m *mockArgocd) AddRepository(_ context.Context, repoURL, _, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.addRepoErr != nil {
		return m.addRepoErr
	}
	m.addedRepos = append(m.addedRepos, repoURL)
	return nil
}

func (m *mockArgocd) GetApplication(_ context.Context, name string) (*models.ArgocdApplication, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.getAppErr != nil {
		return nil, m.getAppErr
	}
	if m.applications != nil {
		if app, ok := m.applications[name]; ok {
			return app, nil
		}
	}
	return nil, fmt.Errorf("application %q not found", name)
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
	mu           sync.Mutex
	files        map[string][]byte // path -> content (written files)
	deletedFiles []string
	branches     []string
	prs          []*gitprovider.PullRequest
	createErr    error
	deleteErr    error
	prErr        error
	mergeErr     error
}

func newMockGitProvider() *mockGitProvider {
	return &mockGitProvider{
		files: make(map[string][]byte),
	}
}

func (m *mockGitProvider) GetFileContent(_ context.Context, path, _ string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
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
	m.mu.Lock()
	defer m.mu.Unlock()
	m.branches = append(m.branches, branchName)
	return nil
}

func (m *mockGitProvider) CreateOrUpdateFile(_ context.Context, path string, content []byte, _, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.createErr != nil {
		return m.createErr
	}
	m.files[path] = content
	return nil
}

func (m *mockGitProvider) BatchCreateFiles(_ context.Context, files map[string][]byte, _, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.createErr != nil {
		return m.createErr
	}
	for path, content := range files {
		m.files[path] = content
	}
	return nil
}

func (m *mockGitProvider) DeleteFile(_ context.Context, path, _, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.deletedFiles = append(m.deletedFiles, path)
	delete(m.files, path)
	return nil
}

func (m *mockGitProvider) CreatePullRequest(_ context.Context, title, _, head, _ string) (*gitprovider.PullRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
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

func (m *mockGitProvider) MergePullRequest(_ context.Context, _ int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mergeErr
}
func (m *mockGitProvider) GetPullRequestStatus(_ context.Context, _ int) (string, error) {
	return "open", nil
}
func (m *mockGitProvider) DeleteBranch(_ context.Context, _ string) error { return nil }

// ---------- helpers ----------

func defaultGitOps() GitOpsConfig {
	return GitOpsConfig{
		PRAutoMerge:  false,
		BranchPrefix: "sharko/",
		CommitPrefix: "sharko:",
		BaseBranch:   "main",
	}
}

func autoMergeGitOps() GitOpsConfig {
	cfg := defaultGitOps()
	cfg.PRAutoMerge = true
	return cfg
}

func defaultPaths() RepoPathsConfig {
	return RepoPathsConfig{
		ClusterValues: "configuration/addons-clusters-values",
		GlobalValues:  "configuration/addons-global-values",
		Catalog:       "configuration/addons-catalog.yaml",
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

// ---------- test template FS for InitRepo ----------

func testTemplateFS() fs.FS {
	return fstest.MapFS{
		"bootstrap/root-app.yaml": &fstest.MapFile{
			Data: []byte(`---
apiVersion: argoproj.io/v1alpha1
kind: AppProject
metadata:
  name: addons
  namespace: argocd
spec:
  description: Addon management project
---
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: addons-bootstrap
  namespace: argocd
spec:
  project: addons
  sources:
    - repoURL: SHARKO_GIT_REPO_URL
      targetRevision: SHARKO_GIT_BRANCH
      path: bootstrap
`),
		},
		"bootstrap/addons-catalog.yaml": &fstest.MapFile{
			Data: []byte("# catalog\n"),
		},
	}
}

// ---------- InitRepo tests ----------

func TestInitRepo_CommitsViaPR(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	cfg := autoMergeGitOps()
	cfg.RepoURL = "https://github.com/example/addons"
	orch := New(nil, defaultCreds(), argocd, git, cfg, defaultPaths(), testTemplateFS())

	result, err := orch.InitRepo(context.Background(), InitRepoRequest{BootstrapArgoCD: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected status 'success', got %q", result.Status)
	}
	// Should have created a branch (PR mode, not direct commits).
	if len(git.branches) == 0 {
		t.Fatal("expected a branch to be created (PR mode)")
	}
	// Should have created a PR.
	if len(git.prs) == 0 {
		t.Fatal("expected a PR to be created")
	}
	if result.Repo == nil {
		t.Fatal("expected Repo in result")
	}
	if result.Repo.PRUrl == "" {
		t.Error("expected PR URL in result")
	}
	// Should have committed multiple files.
	if len(result.Repo.FilesCreated) < 2 {
		t.Errorf("expected at least 2 files created, got %d: %v", len(result.Repo.FilesCreated), result.Repo.FilesCreated)
	}
	// All files should be in git.
	for _, f := range result.Repo.FilesCreated {
		if _, ok := git.files[f]; !ok {
			t.Errorf("file %q not found in git", f)
		}
	}
}

func TestInitRepo_AlreadyInitialized(t *testing.T) {
	git := newMockGitProvider()
	// Pre-populate the bootstrap file so it looks initialized.
	git.files["bootstrap/Chart.yaml"] = []byte("existing")

	cfg := defaultGitOps()
	cfg.RepoURL = "https://github.com/example/addons"
	orch := New(nil, defaultCreds(), newMockArgocd(), git, cfg, defaultPaths(), testTemplateFS())

	_, err := orch.InitRepo(context.Background(), InitRepoRequest{BootstrapArgoCD: false})
	if err == nil {
		t.Fatal("expected error for already-initialized repo")
	}
	if !strings.Contains(err.Error(), "already initialized") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestInitRepo_SyncTimeout(t *testing.T) {
	argocd := newMockArgocd()
	// GetApplication always returns "not found" — triggers timeout.
	argocd.getAppErr = fmt.Errorf("not found")

	git := newMockGitProvider()
	cfg := autoMergeGitOps()
	cfg.RepoURL = "https://github.com/example/addons"
	orch := New(nil, defaultCreds(), argocd, git, cfg, defaultPaths(), testTemplateFS())

	// Use a context with a short deadline to avoid waiting 2 minutes.
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	result, err := orch.InitRepo(ctx, InitRepoRequest{
		BootstrapArgoCD: true,
		GitUsername:      "x-access-token",
		GitToken:         "test-token",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ArgoCD == nil {
		t.Fatal("expected ArgoCD info in result")
	}
	if result.ArgoCD.SyncStatus != "timeout" {
		t.Errorf("expected sync status 'timeout', got %q", result.ArgoCD.SyncStatus)
	}
	if result.Status != "syncing" {
		t.Errorf("expected status 'syncing', got %q", result.Status)
	}
}

func TestInitRepo_WithBootstrapAndSync(t *testing.T) {
	argocd := newMockArgocd()
	argocd.applications = map[string]*models.ArgocdApplication{
		"addons-bootstrap": {
			Name:         "addons-bootstrap",
			SyncStatus:   "Synced",
			HealthStatus: "Healthy",
		},
	}

	git := newMockGitProvider()
	cfg := autoMergeGitOps()
	cfg.RepoURL = "https://github.com/example/addons"
	orch := New(nil, defaultCreds(), argocd, git, cfg, defaultPaths(), testTemplateFS())

	result, err := orch.InitRepo(context.Background(), InitRepoRequest{
		BootstrapArgoCD: true,
		GitUsername:      "x-access-token",
		GitToken:         "test-token",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ArgoCD == nil {
		t.Fatal("expected ArgoCD info in result")
	}
	if !result.ArgoCD.Bootstrapped {
		t.Error("expected Bootstrapped=true")
	}
	if result.ArgoCD.SyncStatus != "synced" {
		t.Errorf("expected sync status 'synced', got %q", result.ArgoCD.SyncStatus)
	}
	if result.Status != "success" {
		t.Errorf("expected status 'success', got %q", result.Status)
	}
	// Should have added the repo to ArgoCD.
	if len(argocd.addedRepos) != 1 {
		t.Errorf("expected 1 repo added to ArgoCD, got %d", len(argocd.addedRepos))
	}
}

func TestInitRepo_AddRepositoryFails_PartialSuccess(t *testing.T) {
	argocd := newMockArgocd()
	argocd.addRepoErr = fmt.Errorf("ArgoCD returned 409")
	git := newMockGitProvider()
	cfg := autoMergeGitOps()
	cfg.RepoURL = "https://github.com/example/addons"
	orch := New(nil, defaultCreds(), argocd, git, cfg, defaultPaths(), testTemplateFS())

	result, err := orch.InitRepo(context.Background(), InitRepoRequest{
		BootstrapArgoCD: true,
		GitUsername:     "x-access-token",
		GitToken:        "test-token",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "partial" {
		t.Errorf("expected 'partial', got %q", result.Status)
	}
	if result.ArgoCD == nil || result.ArgoCD.Bootstrapped {
		t.Error("expected Bootstrapped=false on AddRepository failure")
	}
	// PR should still have been created.
	if result.Repo == nil || result.Repo.PRUrl == "" {
		t.Error("expected PR URL in partial result")
	}
}

func TestRegisterCluster_ManualPR(t *testing.T) {
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
	if result.Git.Merged {
		t.Error("expected Merged=false for manual PR mode")
	}
	if result.Git.PRUrl == "" {
		t.Error("expected PR URL")
	}

	valuesPath := "configuration/addons-clusters-values/prod-eu.yaml"
	if _, ok := git.files[valuesPath]; !ok {
		t.Errorf("values file not created at %s", valuesPath)
	}
}

func TestRegisterCluster_AutoMergePR(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	orch := New(nil, defaultCreds(), argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:   "prod-eu",
		Addons: map[string]bool{"monitoring": true},
		Region: "us-east-1",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Git == nil {
		t.Fatal("expected Git result")
	}
	if !result.Git.Merged {
		t.Error("expected Merged=true for auto-merge mode")
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

func TestRegisterCluster_AutoMergeFails(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	git.mergeErr = fmt.Errorf("merge conflict")
	orch := New(nil, defaultCreds(), argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:   "prod-eu",
		Addons: map[string]bool{"monitoring": true},
	})

	// RegisterCluster should return partial success when merge fails.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "partial" {
		t.Errorf("expected status 'partial', got %q", result.Status)
	}
	if result.FailedStep != "pr_merge" {
		t.Errorf("expected failed step 'pr_merge', got %q", result.FailedStep)
	}
	if result.Git == nil || result.Git.PRUrl == "" {
		t.Error("expected PR URL in partial result")
	}
	if result.Git.Merged {
		t.Error("expected Merged=false when merge fails")
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
	// ArgoCD registration is LAST — it should NOT have happened when Git fails.
	if _, ok := argocd.registeredClusters["prod-eu"]; ok {
		t.Error("cluster should NOT be registered in ArgoCD when Git commit fails (ArgoCD is last step)")
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
	orch.drainSleep = 0 // skip drain wait in tests

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
	if result.Git == nil {
		t.Fatal("expected Git result")
	}
	if result.Git.Merged {
		t.Error("expected Merged=false for manual PR mode")
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
	// Pre-populate an empty catalog so AddAddon can read it.
	catalogPath := "configuration/addons-catalog.yaml"
	git.files[catalogPath] = []byte("applicationsets:\n")

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
	if result.Merged {
		t.Error("expected Merged=false for manual PR mode")
	}

	// Catalog should now contain the new entry.
	catalogContent := string(git.files[catalogPath])
	if !strings.Contains(catalogContent, "name: prometheus") {
		t.Errorf("catalog does not contain new addon entry:\n%s", catalogContent)
	}

	globalPath := "configuration/addons-global-values/prometheus.yaml"
	if _, ok := git.files[globalPath]; !ok {
		t.Errorf("global values file not created at %s", globalPath)
	}
}

func TestRemoveAddon(t *testing.T) {
	git := newMockGitProvider()
	// Pre-populate catalog with a prometheus entry.
	catalogPath := "configuration/addons-catalog.yaml"
	git.files[catalogPath] = []byte("applicationsets:\n  - name: prometheus\n    chart: kube-prometheus-stack\n    repoURL: https://prometheus-community.github.io/helm-charts\n    version: 45.0.0\n")
	// Also put the global values file so it can be deleted.
	git.files["configuration/addons-global-values/prometheus.yaml"] = []byte("prometheus:\n  enabled: false\n")

	orch := New(nil, defaultCreds(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)

	result, err := orch.RemoveAddon(context.Background(), "prometheus")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only the global values file is deleted; the catalog is updated (not deleted).
	if len(git.deletedFiles) != 1 {
		t.Errorf("expected 1 file deleted (global values), got %d: %v", len(git.deletedFiles), git.deletedFiles)
	}
	// Catalog should no longer contain the prometheus entry.
	catalogContent := string(git.files[catalogPath])
	if strings.Contains(catalogContent, "name: prometheus") {
		t.Errorf("catalog still contains prometheus entry after removal:\n%s", catalogContent)
	}
	if result.Merged {
		t.Error("expected Merged=false for manual PR mode")
	}
}

func TestConfigureAddon(t *testing.T) {
	catalogPath := "configuration/addons-catalog.yaml"
	kedaCatalog := []byte("applicationsets:\n  - name: keda\n    chart: keda\n    repoURL: https://kedacore.github.io/charts\n    version: 2.10.0\n    namespace: keda\n")

	t.Run("update version succeeds and catalog is updated", func(t *testing.T) {
		git := newMockGitProvider()
		git.files[catalogPath] = kedaCatalog

		orch := New(nil, defaultCreds(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)

		result, err := orch.ConfigureAddon(context.Background(), ConfigureAddonRequest{
			Name:    "keda",
			Version: "2.11.0",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("expected non-nil git result")
		}

		// Catalog should contain the updated version.
		catalogContent := string(git.files[catalogPath])
		if !strings.Contains(catalogContent, "2.11.0") {
			t.Errorf("catalog does not contain updated version:\n%s", catalogContent)
		}
		if strings.Contains(catalogContent, "2.10.0") {
			t.Errorf("catalog still contains old version:\n%s", catalogContent)
		}
	})

	t.Run("SyncOptions are applied to catalog", func(t *testing.T) {
		git := newMockGitProvider()
		git.files[catalogPath] = kedaCatalog

		orch := New(nil, defaultCreds(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)

		result, err := orch.ConfigureAddon(context.Background(), ConfigureAddonRequest{
			Name:        "keda",
			SyncOptions: []string{"CreateNamespace=true"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("expected non-nil git result")
		}
		catalogContent := string(git.files[catalogPath])
		if !strings.Contains(catalogContent, "CreateNamespace=true") {
			t.Errorf("expected SyncOptions in catalog, got:\n%s", catalogContent)
		}
	})
}

func TestGenerateClusterValues(t *testing.T) {
	content := generateClusterValues("prod-eu", "eu-west-1", map[string]bool{
		"monitoring": true,
		"logging":    false,
	}, nil)

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
	content := generateClusterValues("test", "us-east-1", nil, nil)
	s := string(content)

	if !strings.Contains(s, "region: us-east-1") {
		t.Error("expected region in output")
	}
	// Should not contain addon sections.
	if strings.Contains(s, "enabled:") {
		t.Error("unexpected addon section in output with nil addons")
	}
}

func TestRegisterCluster_AdoptsExistingArgocdCluster(t *testing.T) {
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{
		{Name: "prod-eu", Server: "https://k8s.example.com:6443"},
	}
	git := newMockGitProvider()
	orch := New(nil, defaultCreds(), argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:   "prod-eu",
		Addons: map[string]bool{"monitoring": true},
	})

	if err != nil {
		t.Fatalf("expected adoption to succeed, got error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected status=success, got: %s", result.Status)
	}
	if !result.Adopted {
		t.Error("expected Adopted=true for cluster already in ArgoCD")
	}
	// Should NOT have called RegisterCluster in ArgoCD (already there).
	if _, ok := argocd.registeredClusters["prod-eu"]; ok {
		t.Error("adopted cluster should not be re-registered in ArgoCD")
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
	orchA := New(mu, creds, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	orchB := New(mu, creds, argocd, git, autoMergeGitOps(), defaultPaths(), nil)

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

func TestDeregisterCluster_AutoMerge(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	orch := New(nil, defaultCreds(), argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	orch.drainSleep = 0 // skip drain wait in tests

	result, err := orch.DeregisterCluster(context.Background(), "prod-eu", "https://k8s.example.com:6443")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected success, got %q", result.Status)
	}
	if result.Git == nil || !result.Git.Merged {
		t.Error("expected Merged=true with auto-merge")
	}
}

func TestDeregisterCluster_AutoMergeFails(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	git.mergeErr = fmt.Errorf("merge conflict")
	orch := New(nil, defaultCreds(), argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	orch.drainSleep = 0 // skip drain wait in tests

	result, err := orch.DeregisterCluster(context.Background(), "prod-eu", "https://k8s.example.com:6443")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "partial" {
		t.Errorf("expected partial, got %q", result.Status)
	}
	if result.FailedStep != "pr_merge" {
		t.Errorf("expected failed step pr_merge, got %q", result.FailedStep)
	}
	if result.Git == nil || result.Git.PRUrl == "" {
		t.Error("expected PR URL in partial result")
	}
	if result.Git.Merged {
		t.Error("expected Merged=false when merge fails")
	}
}

func TestUpdateClusterAddons_AutoMergeFails(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	git.mergeErr = fmt.Errorf("merge conflict")
	orch := New(nil, defaultCreds(), argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	result, err := orch.UpdateClusterAddons(context.Background(), "prod-eu", "https://k8s.example.com:6443", "eu-west-1",
		map[string]bool{"monitoring": true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "partial" {
		t.Errorf("expected partial, got %q", result.Status)
	}
	if result.FailedStep != "pr_merge" {
		t.Errorf("expected failed step pr_merge, got %q", result.FailedStep)
	}
	if result.Git == nil || result.Git.PRUrl == "" {
		t.Error("expected PR URL in partial result")
	}
}

// ---------- mock secret fetcher ----------

type mockSecretFetcher struct {
	secrets map[string][]byte // provider path → value
	err     error
}

func (m *mockSecretFetcher) GetSecretValue(_ context.Context, path string) ([]byte, error) {
	if m.err != nil {
		return nil, m.err
	}
	v, ok := m.secrets[path]
	if !ok {
		return nil, fmt.Errorf("secret not found: %s", path)
	}
	return v, nil
}

func fakeClientFactory() RemoteClientFactory {
	return func(_ []byte) (kubernetes.Interface, error) {
		return fake.NewSimpleClientset(), nil
	}
}

func defaultSecretDefs() map[string]AddonSecretDefinition {
	return map[string]AddonSecretDefinition{
		"datadog": {
			AddonName:  "datadog",
			SecretName: "datadog-keys",
			Namespace:  "datadog",
			Keys:       map[string]string{"api-key": "secrets/datadog/api-key"},
		},
	}
}

func defaultSecretFetcher() *mockSecretFetcher {
	return &mockSecretFetcher{
		secrets: map[string][]byte{
			"secrets/datadog/api-key": []byte("dd-api-key-value"),
		},
	}
}

func defaultCredsWithRaw() *mockCredProvider {
	return &mockCredProvider{
		creds: map[string]*providers.Kubeconfig{
			"prod-eu": {
				Server: "https://k8s.example.com:6443",
				CAData: []byte("fake-ca"),
				Token:  "fake-token",
				Raw:    []byte("fake-kubeconfig"),
			},
		},
	}
}

// ---------- secret integration tests ----------

func TestRegisterCluster_WithSecrets(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	orch := New(nil, defaultCredsWithRaw(), argocd, git, defaultGitOps(), defaultPaths(), nil)
	orch.SetSecretManagement(defaultSecretDefs(), defaultSecretFetcher(), fakeClientFactory())

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:   "prod-eu",
		Addons: map[string]bool{"datadog": true, "logging": false},
		Region: "eu-west-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected status 'success', got %q", result.Status)
	}
	if len(result.Secrets) != 1 || result.Secrets[0] != "datadog-keys" {
		t.Errorf("expected [datadog-keys], got %v", result.Secrets)
	}
	// Verify create_secrets step was recorded.
	found := false
	for _, s := range result.CompletedSteps {
		if s == "create_secrets" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'create_secrets' in completed steps, got %v", result.CompletedSteps)
	}
}

func TestRegisterCluster_SecretFailure_PartialSuccess(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	orch := New(nil, defaultCredsWithRaw(), argocd, git, defaultGitOps(), defaultPaths(), nil)
	failingFetcher := &mockSecretFetcher{err: fmt.Errorf("vault connection refused")}
	orch.SetSecretManagement(defaultSecretDefs(), failingFetcher, fakeClientFactory())

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:   "prod-eu",
		Addons: map[string]bool{"datadog": true},
		Region: "eu-west-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "partial" {
		t.Errorf("expected status 'partial', got %q", result.Status)
	}
	if result.FailedStep != "create_secrets" {
		t.Errorf("expected failed step 'create_secrets', got %q", result.FailedStep)
	}
	// With partial success, individual secret failures are recorded but Git/PR + ArgoCD proceed.
	if len(result.FailedSecrets) != 1 {
		t.Errorf("expected 1 failed secret, got %d", len(result.FailedSecrets))
	}
	// Git commit SHOULD have happened — partial success continues with remaining steps.
	if len(git.prs) == 0 {
		t.Error("expected a PR to be created even when some secrets failed")
	}
	// ArgoCD registration SHOULD have happened (it's the last step and proceeds after partial secret failure).
	if _, ok := argocd.registeredClusters["prod-eu"]; !ok {
		t.Error("expected cluster to be registered in ArgoCD even with partial secret failure")
	}
}

func TestRegisterCluster_NoSecretConfig_BackwardCompat(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	// No SetSecretManagement call — nil secret deps.
	orch := New(nil, defaultCreds(), argocd, git, defaultGitOps(), defaultPaths(), nil)

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:   "prod-eu",
		Addons: map[string]bool{"monitoring": true},
		Region: "eu-west-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected status 'success', got %q", result.Status)
	}
	// No secrets should be in the result.
	if len(result.Secrets) != 0 {
		t.Errorf("expected no secrets, got %v", result.Secrets)
	}
}
