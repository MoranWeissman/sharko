package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/MoranWeissman/sharko/internal/ai"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/service"
)

// ---------------------------------------------------------------------------
// Fake GitProvider for handler tests
// ---------------------------------------------------------------------------

// handlerFakeGitProvider is a minimal gitprovider.GitProvider that returns a
// fixed set of file contents. Missing paths return a non-nil error.
// Tests that exercise recent-PRs endpoints can optionally set `prs` to stub
// the ListPullRequests response.
type handlerFakeGitProvider struct {
	files map[string][]byte
	prs   []gitprovider.PullRequest
}

func (f *handlerFakeGitProvider) GetFileContent(_ context.Context, path, _ string) ([]byte, error) {
	data, ok := f.files[path]
	if !ok {
		return nil, errors.New("not found: " + path)
	}
	return data, nil
}

func (f *handlerFakeGitProvider) ListDirectory(_ context.Context, _, _ string) ([]string, error) {
	return nil, nil
}

func (f *handlerFakeGitProvider) ListPullRequests(_ context.Context, _ string) ([]gitprovider.PullRequest, error) {
	return f.prs, nil
}

func (f *handlerFakeGitProvider) TestConnection(_ context.Context) error { return nil }

func (f *handlerFakeGitProvider) CreateBranch(_ context.Context, _, _ string) error { return nil }

func (f *handlerFakeGitProvider) CreateOrUpdateFile(_ context.Context, _ string, _ []byte, _, _ string) error {
	return nil
}

func (f *handlerFakeGitProvider) BatchCreateFiles(_ context.Context, _ map[string][]byte, _, _ string) error {
	return nil
}

func (f *handlerFakeGitProvider) DeleteFile(_ context.Context, _, _, _ string) error { return nil }

func (f *handlerFakeGitProvider) CreatePullRequest(_ context.Context, _, _, _, _ string) (*gitprovider.PullRequest, error) {
	return nil, nil
}

func (f *handlerFakeGitProvider) MergePullRequest(_ context.Context, _ int) error { return nil }

func (f *handlerFakeGitProvider) GetPullRequestStatus(_ context.Context, _ int) (string, error) {
	return "open", nil
}

func (f *handlerFakeGitProvider) DeleteBranch(_ context.Context, _ string) error { return nil }

// ---------------------------------------------------------------------------
// Fake SecretReconciler for handler tests
// ---------------------------------------------------------------------------

type fakeReconciler struct {
	triggered bool
	stats     interface{}
}

func (r *fakeReconciler) Trigger() { r.triggered = true }

func (r *fakeReconciler) GetStats() interface{} { return r.stats }

// ---------------------------------------------------------------------------
// handleRepoStatus
// ---------------------------------------------------------------------------

func TestHandleRepoStatus_NotInitialized_NoConnection(t *testing.T) {
	// No connection configured — connSvc returns error from GetActiveGitProvider.
	srv := newTestServer()
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/repo/status", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["initialized"] != false {
		t.Errorf("expected initialized=false, got %v", body["initialized"])
	}
	if body["reason"] != "no_connection" {
		t.Errorf("expected reason=no_connection, got %v", body["reason"])
	}
}

func TestHandleRepoStatus_NotInitialized_NotBootstrapped(t *testing.T) {
	// Connection present but bootstrap/Chart.yaml does not exist.
	srv := newTestServer()
	// Install a git provider override that returns nothing (all paths return error).
	gp := &handlerFakeGitProvider{files: map[string][]byte{}}
	srv.connSvc.SetGitProviderOverride(gp)

	router := NewRouter(srv, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/repo/status", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["initialized"] != false {
		t.Errorf("expected initialized=false, got %v", body["initialized"])
	}
	if body["reason"] != "not_bootstrapped" {
		t.Errorf("expected reason=not_bootstrapped, got %v", body["reason"])
	}
}

func TestHandleRepoStatus_Initialized(t *testing.T) {
	// Connection present and bootstrap/Chart.yaml exists.
	srv := newTestServer()
	srv.gitopsCfg = orchestrator.GitOpsConfig{BaseBranch: "main"}
	gp := &handlerFakeGitProvider{files: map[string][]byte{
		"bootstrap/Chart.yaml": []byte("apiVersion: v2\nname: bootstrap\n"),
	}}
	srv.connSvc.SetGitProviderOverride(gp)

	router := NewRouter(srv, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/repo/status", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["initialized"] != true {
		t.Errorf("expected initialized=true, got %v", body["initialized"])
	}
}

// ---------------------------------------------------------------------------
// handleTriggerReconcile
// ---------------------------------------------------------------------------

func TestHandleTriggerReconcile_NotConfigured(t *testing.T) {
	srv := newTestServer()
	// secretReconciler is nil (not configured).
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/secrets/reconcile", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestHandleTriggerReconcile_Configured(t *testing.T) {
	srv := newTestServer()
	rec := &fakeReconciler{}
	srv.SetSecretReconciler(rec)
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/secrets/reconcile", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", w.Code)
	}
	if !rec.triggered {
		t.Error("expected reconciler.Trigger() to have been called")
	}

	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "reconcile triggered" {
		t.Errorf("unexpected status: %v", body["status"])
	}
}

// ---------------------------------------------------------------------------
// handleReconcileStatus
// ---------------------------------------------------------------------------

func TestHandleReconcileStatus_NotConfigured(t *testing.T) {
	srv := newTestServer()
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/secrets/status", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestHandleReconcileStatus_ReturnsStats(t *testing.T) {
	type stats struct {
		Checked int `json:"checked"`
		Updated int `json:"updated"`
	}

	srv := newTestServer()
	rec := &fakeReconciler{stats: stats{Checked: 5, Updated: 2}}
	srv.SetSecretReconciler(rec)
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/secrets/status", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	// JSON numbers are float64 by default.
	if body["checked"] != float64(5) {
		t.Errorf("expected checked=5, got %v", body["checked"])
	}
	if body["updated"] != float64(2) {
		t.Errorf("expected updated=2, got %v", body["updated"])
	}
}

// ---------------------------------------------------------------------------
// handleGetFleetStatus — resilient when Git/ArgoCD unavailable
// ---------------------------------------------------------------------------

func TestHandleGetFleetStatus_NoConnections(t *testing.T) {
	// No connections configured — both git_unavailable and argo_unavailable should be true.
	srv := newTestServer()
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/fleet/status", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Must always return 200 even with no providers.
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body fleetStatusResponse
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.GitUnavailable {
		t.Error("expected git_unavailable=true when no connection configured")
	}
	if !body.ArgoUnavailable {
		t.Error("expected argo_unavailable=true when no connection configured")
	}
	if body.Clusters == nil {
		t.Error("expected clusters to be a non-nil slice")
	}
}

func TestHandleGetFleetStatus_HasServerVersion(t *testing.T) {
	srv := newTestServer()
	srv.SetVersion("1.2.3")
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/fleet/status", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	var body fleetStatusResponse
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.ServerVersion != "1.2.3" {
		t.Errorf("expected server_version=1.2.3, got %q", body.ServerVersion)
	}
}

func TestHandleGetFleetStatus_DefaultVersion(t *testing.T) {
	// When version is not set, should fall back to "dev".
	srv := newTestServer()
	// Do NOT call SetVersion — version field remains zero value.
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/fleet/status", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	var body fleetStatusResponse
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.ServerVersion != "dev" {
		t.Errorf("expected server_version=dev, got %q", body.ServerVersion)
	}
}

// ---------------------------------------------------------------------------
// ReinitializeFromConnection
// ---------------------------------------------------------------------------

// newIsolatedTestServer creates a Server backed by a unique temp file store so that
// ReinitializeFromConnection tests do not share state with newTestServer() or each other.
func newIsolatedTestServer(t *testing.T) *Server {
	t.Helper()
	f, err := os.CreateTemp("", "sharko-test-*.yaml")
	if err != nil {
		t.Fatalf("create temp config file: %v", err)
	}
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })

	store := config.NewFileStore(f.Name())
	connSvc := service.NewConnectionService(store)
	clusterSvc := service.NewClusterService("")
	addonSvc := service.NewAddonService("")
	dashboardSvc := service.NewDashboardService(connSvc, "")
	observabilitySvc := service.NewObservabilityService()
	upgradeSvc := service.NewUpgradeService(ai.NewClient(ai.Config{}), nil, "")
	aiClient := ai.NewClient(ai.Config{})
	return NewServer(connSvc, clusterSvc, addonSvc, dashboardSvc, observabilitySvc, upgradeSvc, aiClient)
}

// seedActiveConnection saves a Connection and marks it active on the server's connSvc.
func seedActiveConnection(t *testing.T, srv *Server, conn models.Connection) {
	t.Helper()
	if err := srv.connSvc.Create(models.CreateConnectionRequest{
		Name:     conn.Name,
		Git:      conn.Git,
		Argocd:   conn.Argocd,
		Provider: conn.Provider,
		GitOps:   conn.GitOps,
	}); err != nil {
		t.Fatalf("seed connection: %v", err)
	}
	if err := srv.connSvc.SetActive(conn.Name); err != nil {
		t.Fatalf("set active connection: %v", err)
	}
}

func TestReinitializeFromConnection_NoConnection(t *testing.T) {
	// No active connection — ReinitializeFromConnection must not panic
	// and credProvider must remain nil.
	srv := newIsolatedTestServer(t)
	srv.ReinitializeFromConnection()

	if srv.credProvider != nil {
		t.Error("expected credProvider to remain nil when no active connection")
	}
}

func TestReinitializeFromConnection_GitOpsConfig(t *testing.T) {
	// Connection with GitOps settings populated.
	// ReinitializeFromConnection must copy those values into srv.gitopsCfg.
	srv := newIsolatedTestServer(t)

	autoMerge := true
	seedActiveConnection(t, srv, models.Connection{
		Name: "gitops-conn",
		Git:  models.GitRepoConfig{Provider: models.GitProviderGitHub, Owner: "owner", Repo: "repo"},
		GitOps: &models.GitOpsSettings{
			BaseBranch:   "develop",
			BranchPrefix: "feature/",
			CommitPrefix: "feat:",
			PRAutoMerge:  &autoMerge,
		},
	})

	srv.ReinitializeFromConnection()

	if srv.gitopsCfg.BaseBranch != "develop" {
		t.Errorf("expected BaseBranch=develop, got %q", srv.gitopsCfg.BaseBranch)
	}
	if srv.gitopsCfg.BranchPrefix != "feature/" {
		t.Errorf("expected BranchPrefix=feature/, got %q", srv.gitopsCfg.BranchPrefix)
	}
	if srv.gitopsCfg.CommitPrefix != "feat:" {
		t.Errorf("expected CommitPrefix=feat:, got %q", srv.gitopsCfg.CommitPrefix)
	}
	if !srv.gitopsCfg.PRAutoMerge {
		t.Error("expected PRAutoMerge=true")
	}
}

func TestReinitializeFromConnection_SetsProvider(t *testing.T) {
	// Connection with an aws-sm provider config.
	// providers.New(aws-sm) succeeds without real credentials at construction time
	// (the AWS SDK defers credential resolution to the first API call).
	// After ReinitializeFromConnection, credProvider must be non-nil.
	srv := newIsolatedTestServer(t)

	seedActiveConnection(t, srv, models.Connection{
		Name: "aws-conn",
		Git:  models.GitRepoConfig{Provider: models.GitProviderGitHub, Owner: "owner", Repo: "repo"},
		Provider: &models.ProviderConfig{
			Type:   "aws-sm",
			Region: "us-east-1",
			Prefix: "clusters/",
		},
	})

	srv.ReinitializeFromConnection()

	if srv.credProvider == nil {
		t.Error("expected credProvider to be set after ReinitializeFromConnection with aws-sm provider")
	}
	if srv.providerCfg == nil {
		t.Error("expected providerCfg to be set after ReinitializeFromConnection")
	}
	if srv.providerCfg != nil && srv.providerCfg.Type != "aws-sm" {
		t.Errorf("expected providerCfg.Type=aws-sm, got %q", srv.providerCfg.Type)
	}
}

func TestReinitializeFromConnection_RepoURL(t *testing.T) {
	// Connection with a git RepoURL — gitopsCfg.RepoURL must be populated.
	srv := newIsolatedTestServer(t)

	seedActiveConnection(t, srv, models.Connection{
		Name: "repo-conn",
		Git: models.GitRepoConfig{
			Provider: models.GitProviderGitHub,
			Owner:    "owner",
			Repo:     "repo",
			RepoURL:  "https://github.com/owner/repo.git",
		},
	})

	srv.ReinitializeFromConnection()

	if srv.gitopsCfg.RepoURL != "https://github.com/owner/repo.git" {
		t.Errorf("expected RepoURL=https://github.com/owner/repo.git, got %q", srv.gitopsCfg.RepoURL)
	}
}
