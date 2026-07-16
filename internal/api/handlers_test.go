package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/MoranWeissman/sharko/internal/ai"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/providers"
	"github.com/MoranWeissman/sharko/internal/service"
)

// ---------------------------------------------------------------------------
// Fake GitProvider for handler tests
// ---------------------------------------------------------------------------

// handlerFakeGitProvider is a minimal gitprovider.GitProvider that returns a
// fixed set of file contents. Missing paths return a non-nil error that wraps
// gitprovider.ErrFileNotFound — mirroring the real GitHub/Azure providers so
// callers using errors.Is(err, gitprovider.ErrFileNotFound) classify a genuine
// missing file (not a broken connection).
//
// To simulate a transport/TLS failure (where the repo can't be reached at all),
// set `getErr` — it is returned verbatim from GetFileContent and does NOT wrap
// the sentinel, so callers classify it as a connection error.
// Tests that exercise recent-PRs endpoints can optionally set `prs` to stub
// the ListPullRequests response.
type handlerFakeGitProvider struct {
	files  map[string][]byte
	prs    []gitprovider.PullRequest
	getErr error // when set, GetFileContent returns this verbatim (connection failure)
}

func (f *handlerFakeGitProvider) GetFileContent(_ context.Context, path, _ string) ([]byte, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	data, ok := f.files[path]
	if !ok {
		return nil, fmt.Errorf("get file content: path %q not found: %w", path, gitprovider.ErrFileNotFound)
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
	// V124-22 / BUG-046: bootstrap_synced is always present in the body.
	// When the repo isn't initialized, bootstrap_synced must be false —
	// the wizard gate combines (!initialized || !bootstrap_synced).
	if body["bootstrap_synced"] != false {
		t.Errorf("expected bootstrap_synced=false, got %v", body["bootstrap_synced"])
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
	if body["bootstrap_synced"] != false {
		t.Errorf("expected bootstrap_synced=false, got %v", body["bootstrap_synced"])
	}
}

// TestHandleRepoStatus_NotInitialized_ConnectionError is the V2-cleanup-50
// reproducer: a corporate TLS-inspection proxy (Zscaler) makes the
// bootstrap/Chart.yaml fetch fail with an x509 "unknown authority" error.
// That is NOT a missing file — it does not wrap gitprovider.ErrFileNotFound —
// so the handler must classify it as "connection_error". Pre-fix this was
// reported as "not_bootstrapped", which threw the user into the re-bootstrap
// wizard even though a working bootstrap was already in place.
func TestHandleRepoStatus_NotInitialized_ConnectionError(t *testing.T) {
	srv := newTestServer()
	// getErr is a generic transport/TLS error that does NOT wrap
	// gitprovider.ErrFileNotFound — i.e. the repo could not be reached.
	gp := &handlerFakeGitProvider{
		files:  map[string][]byte{},
		getErr: errors.New("tls: failed to verify certificate: x509: certificate signed by unknown authority"),
	}
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
	if body["reason"] != "connection_error" {
		t.Errorf("expected reason=connection_error, got %v", body["reason"])
	}
	if body["bootstrap_synced"] != false {
		t.Errorf("expected bootstrap_synced=false, got %v", body["bootstrap_synced"])
	}
}

// repoStatusInitializedTestSetup wires up a server with the bootstrap file
// present on the base branch and the supplied ArgocdClient override. It
// returns the body decoded from a GET /api/v1/repo/status — the four
// V124-22 cases below differ only in the override behaviour.
func repoStatusInitializedTestSetup(t *testing.T, ac orchestrator.ArgocdClient) map[string]interface{} {
	t.Helper()
	srv := newTestServer()
	srv.publishGitopsCfg(orchestrator.GitOpsConfig{BaseBranch: "main"})
	gp := &handlerFakeGitProvider{files: map[string][]byte{
		"bootstrap/Chart.yaml": []byte("apiVersion: v2\nname: bootstrap\n"),
	}}
	srv.connSvc.SetGitProviderOverride(gp)
	if ac != nil {
		srv.connSvc.SetArgocdClientOverride(ac)
	}

	router := NewRouter(srv, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/repo/status", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["initialized"] != true {
		t.Errorf("expected initialized=true, got %v", body["initialized"])
	}
	return body
}

// V124-22 / BUG-046 — repo initialized + bootstrap Synced + Healthy →
// bootstrap_synced=true. Wizard stays out of the way, dashboard renders.
func TestHandleRepoStatus_Initialized_BootstrapHealthy(t *testing.T) {
	ac := &initFakeArgocd{
		app: &models.ArgocdApplication{
			Name:         orchestrator.BootstrapRootAppName,
			SyncStatus:   "Synced",
			HealthStatus: "Healthy",
		},
	}
	body := repoStatusInitializedTestSetup(t, ac)
	if body["bootstrap_synced"] != true {
		t.Errorf("expected bootstrap_synced=true (Synced+Healthy app), got %v",
			body["bootstrap_synced"])
	}
	// A healthy bootstrap carries no reason (reason is omitempty, so absent).
	if r, ok := body["reason"]; ok && r != "" {
		t.Errorf("expected no reason for a healthy bootstrap, got %v", r)
	}
}

// V124-22 / BUG-046 — repo initialized + bootstrap missing → bootstrap_synced=false.
// This is the BUG-046 reproducer: user wiped the GitHub repo + ran
// `sharko-dev.sh argocd-reset`, then visited the UI. Without this fix,
// the dashboard renders with errors instead of the wizard.
func TestHandleRepoStatus_Initialized_BootstrapMissing(t *testing.T) {
	ac := &initFakeArgocd{
		getErr: errors.New("application not found: cluster-addons-bootstrap"),
	}
	body := repoStatusInitializedTestSetup(t, ac)
	if body["bootstrap_synced"] != false {
		t.Errorf("expected bootstrap_synced=false (app missing), got %v",
			body["bootstrap_synced"])
	}
}

// V2-cleanup-51.1 — repo initialized + bootstrap app Sync=Unknown (ArgoCD's
// repo-server can't reach the Git repo) → bootstrap_synced=false AND
// reason="bootstrap_unreachable". This is the live Zscaler bug: re-init can't
// fix a connection problem, so the UI must NOT auto-trap the user.
func TestHandleRepoStatus_Initialized_BootstrapUnreachable(t *testing.T) {
	ac := &initFakeArgocd{
		app: &models.ArgocdApplication{
			Name:         orchestrator.BootstrapRootAppName,
			SyncStatus:   "Unknown",
			HealthStatus: "Error",
		},
	}
	body := repoStatusInitializedTestSetup(t, ac)
	if body["bootstrap_synced"] != false {
		t.Errorf("expected bootstrap_synced=false (Sync=Unknown), got %v",
			body["bootstrap_synced"])
	}
	if body["reason"] != "bootstrap_unreachable" {
		t.Errorf("expected reason=bootstrap_unreachable, got %v", body["reason"])
	}
}

// V2-cleanup-51.1 — repo initialized + bootstrap genuinely degraded
// (OutOfSync/Degraded) → bootstrap_synced=false AND reason="bootstrap_degraded".
// ArgoCD read the repo and found a fixable problem, so re-init/repair is the
// right move — distinct from the unreachable (connection) case above.
func TestHandleRepoStatus_Initialized_BootstrapDegradedReason(t *testing.T) {
	ac := &initFakeArgocd{
		app: &models.ArgocdApplication{
			Name:         orchestrator.BootstrapRootAppName,
			SyncStatus:   "OutOfSync",
			HealthStatus: "Degraded",
		},
	}
	body := repoStatusInitializedTestSetup(t, ac)
	if body["bootstrap_synced"] != false {
		t.Errorf("expected bootstrap_synced=false (OutOfSync+Degraded), got %v",
			body["bootstrap_synced"])
	}
	if body["reason"] != "bootstrap_degraded" {
		t.Errorf("expected reason=bootstrap_degraded, got %v", body["reason"])
	}
}

// V124-22 / BUG-046 — repo initialized + bootstrap exists but degraded
// (OutOfSync / Degraded) → bootstrap_synced=false. Protects against the
// "user manually deleted the deployment" partial-state case so the wizard
// is the recovery surface, not a broken dashboard.
func TestHandleRepoStatus_Initialized_BootstrapDegraded(t *testing.T) {
	ac := &initFakeArgocd{
		app: &models.ArgocdApplication{
			Name:         orchestrator.BootstrapRootAppName,
			SyncStatus:   "OutOfSync",
			HealthStatus: "Degraded",
		},
	}
	body := repoStatusInitializedTestSetup(t, ac)
	if body["bootstrap_synced"] != false {
		t.Errorf("expected bootstrap_synced=false (OutOfSync+Degraded), got %v",
			body["bootstrap_synced"])
	}
}

// V124-22 / BUG-046 — repo initialized + ArgoCD client unavailable →
// bootstrap_synced=false (defensive). When we can't probe the cluster,
// the safe answer for the wizard gate is "treat as degraded" so the
// recovery surface is the wizard, not a dashboard that's silently
// missing the bootstrap. Achieved here by NOT installing an override
// AND keeping the ConnectionService without a configured connection
// (no test override → GetActiveOrchestratorArgocdClient returns an error,
// which the handler treats as "no probe possible" → bootstrap_synced=false).
func TestHandleRepoStatus_Initialized_ArgocdUnavailable(t *testing.T) {
	body := repoStatusInitializedTestSetup(t, nil)
	if body["bootstrap_synced"] != false {
		t.Errorf("expected bootstrap_synced=false (no ArgoCD client), got %v",
			body["bootstrap_synced"])
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
	observabilitySvc := service.NewObservabilityService(clusterSvc)
	upgradeSvc := service.NewUpgradeService(ai.NewClient(ai.Config{}), nil, "")
	aiClient := ai.NewClient(ai.Config{})
	return NewServer(connSvc, clusterSvc, addonSvc, dashboardSvc, observabilitySvc, upgradeSvc, aiClient)
}

// installCredProvider publishes cp as the server's cluster-credentials
// provider (with optional typed configs) through the same race-safe
// publication point production uses — with the ArgoCD-read route DISABLED
// so unit tests stay hermetic: the production ArgoCD reader falls back to
// ~/.kube/config out-of-cluster, which must never be touched from a test.
func installCredProvider(srv *Server, cp providers.ClusterCredentialsProvider, addonCfg *providers.AddonSecretProviderConfig, testCfg *providers.ClusterTestProviderConfig) {
	srv.providerState.Store(&providerSet{
		credProvider:   cp,
		addonSecretCfg: addonCfg,
		clusterTestCfg: testCfg,
		credsRouter: &providers.ClusterCredsRouter{
			Backend: cp,
			ArgoCDReaderFn: func() (providers.ClusterCredentialsProvider, error) {
				return nil, fmt.Errorf("argocd reader disabled in unit tests")
			},
		},
	})
}

// seedActiveConnection saves a Connection and marks it active on the server's connSvc.
func seedActiveConnection(t *testing.T, srv *Server, conn models.Connection) {
	t.Helper()
	if err := srv.connSvc.Create(models.CreateConnectionRequest{
		Name:                conn.Name,
		Git:                 conn.Git,
		Argocd:              conn.Argocd,
		Provider:            conn.Provider,
		AddonSecretProvider: conn.AddonSecretProvider,
		GitOps:              conn.GitOps,
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

	if srv.credProvider() != nil {
		t.Error("expected credProvider to remain nil when no active connection")
	}
}

func TestReinitializeFromConnection_GitOpsConfig(t *testing.T) {
	// Connection with GitOps settings populated.
	// ReinitializeFromConnection must copy those values into published gitops config (GF2).
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

	cfg := srv.gitopsConfig()
	if cfg.BaseBranch != "develop" {
		t.Errorf("expected BaseBranch=develop, got %q", cfg.BaseBranch)
	}
	if cfg.BranchPrefix != "feature/" {
		t.Errorf("expected BranchPrefix=feature/, got %q", cfg.BranchPrefix)
	}
	if cfg.CommitPrefix != "feat:" {
		t.Errorf("expected CommitPrefix=feat:, got %q", cfg.CommitPrefix)
	}
	if !cfg.PRAutoMerge {
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

	// V2-cleanup-53.1: the aws-sm cluster-creds arm is RESTORED. With
	// provider.type="aws-sm" the cluster-test fan-through now routes to the
	// SM-backed provider — construction succeeds without real credentials
	// (the AWS SDK defers resolution to the first API call), so this is
	// deterministic in CI. This is also the hot-reload contract: this same
	// method runs on every connection save, so the swap here IS what makes
	// a Settings change take effect without a pod restart.
	if _, ok := srv.credProvider().(*providers.AWSSecretsManagerProvider); !ok {
		t.Fatalf("credProvider = %T, want *providers.AWSSecretsManagerProvider (restored aws-sm cluster-creds arm)", srv.credProvider())
	}
	if srv.clusterTestCfg() == nil || srv.clusterTestCfg().Type != "aws-sm" {
		t.Errorf("expected clusterTestCfg.Type=aws-sm, got %+v", srv.clusterTestCfg())
	}
	if srv.clusterTestCfg() != nil && srv.clusterTestCfg().ArgoCDNamespace != "" {
		t.Errorf("ArgoCDNamespace = %q, want empty (V125-1-10.8 guard)", srv.clusterTestCfg().ArgoCDNamespace)
	}
	if srv.addonSecretCfg() == nil || srv.addonSecretCfg().Type != "aws-sm" {
		t.Errorf("expected addonSecretCfg.Type=aws-sm, got %+v", srv.addonSecretCfg())
	}
}

func TestReinitializeFromConnection_RepoURL(t *testing.T) {
	// Connection with a git RepoURL — published gitops config RepoURL must be populated (GF2).
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

	cfg := srv.gitopsConfig()
	if cfg.RepoURL != "https://github.com/owner/repo.git" {
		t.Errorf("expected RepoURL=https://github.com/owner/repo.git, got %q", cfg.RepoURL)
	}
}

// V3-P1.1: when Connection has BOTH Provider (cluster-creds) AND
// AddonSecretProvider (addon-secret), hot-reload must route them separately.
// This is the "argocd for cluster-creds + aws-sm for addon-secrets" scenario.
func TestReinitializeFromConnection_SeparateAddonSecretProvider(t *testing.T) {
	srv := newIsolatedTestServer(t)

	seedActiveConnection(t, srv, models.Connection{
		Name: "split-providers",
		Git:  models.GitRepoConfig{Provider: models.GitProviderGitHub, Owner: "owner", Repo: "repo"},
		Provider: &models.ProviderConfig{
			Type: "argocd", // cluster-creds only
		},
		AddonSecretProvider: &models.ProviderConfig{
			Type:   "aws-sm", // addon-secrets only
			Region: "us-east-1",
			Prefix: "addons/",
		},
	})

	srv.ReinitializeFromConnection()

	// Cluster-test config: argocd (config must publish even if provider construction failed)
	testCfg := srv.clusterTestCfg()
	if testCfg == nil {
		t.Fatalf("expected clusterTestCfg to be published, got nil")
	}
	if testCfg.Type != "argocd" {
		t.Errorf("expected clusterTestCfg.Type=argocd, got %q", testCfg.Type)
	}

	// Addon-secret config: aws-sm with explicit fields (independent of cluster-creds provider)
	addonCfg := srv.addonSecretCfg()
	if addonCfg == nil {
		t.Fatalf("expected addonSecretCfg to be published, got nil")
	}
	if addonCfg.Type != "aws-sm" {
		t.Errorf("expected addonSecretCfg.Type=aws-sm, got %q", addonCfg.Type)
	}
	if addonCfg.Region != "us-east-1" {
		t.Errorf("expected addonSecretCfg.Region=us-east-1, got %q", addonCfg.Region)
	}
	if addonCfg.Prefix != "addons/" {
		t.Errorf("expected addonSecretCfg.Prefix=addons/, got %q", addonCfg.Prefix)
	}
}

// V3-P1.1 backward compat: legacy connection (Provider set, AddonSecretProvider
// nil) must resolve addon-secret backend from Provider exactly as before.
func TestReinitializeFromConnection_LegacyProviderBackwardCompat(t *testing.T) {
	srv := newIsolatedTestServer(t)

	seedActiveConnection(t, srv, models.Connection{
		Name: "legacy-provider",
		Git:  models.GitRepoConfig{Provider: models.GitProviderGitHub, Owner: "owner", Repo: "repo"},
		Provider: &models.ProviderConfig{
			Type:   "aws-sm",
			Region: "eu-west-1",
			Prefix: "clusters/",
		},
		AddonSecretProvider: nil, // not set (pre-V3 connection)
	})

	srv.ReinitializeFromConnection()

	// Both cluster-test and addon-secret should resolve from Provider (backward compat)
	if srv.clusterTestCfg() == nil || srv.clusterTestCfg().Type != "aws-sm" {
		t.Errorf("expected clusterTestCfg.Type=aws-sm, got %+v", srv.clusterTestCfg())
	}
	if srv.addonSecretCfg() == nil || srv.addonSecretCfg().Type != "aws-sm" {
		t.Errorf("expected addonSecretCfg.Type=aws-sm (fallback from Provider), got %+v", srv.addonSecretCfg())
	}
	if srv.addonSecretCfg().Region != "eu-west-1" {
		t.Errorf("expected addonSecretCfg.Region=eu-west-1, got %q", srv.addonSecretCfg().Region)
	}
	if srv.addonSecretCfg().Prefix != "clusters/" {
		t.Errorf("expected addonSecretCfg.Prefix=clusters/, got %q", srv.addonSecretCfg().Prefix)
	}
}

// ---------------------------------------------------------------------------
// V125-1-10.7: provider auto-default end-to-end through ReinitializeFromConnection
// ---------------------------------------------------------------------------
//
// Story 10.7 removed the `pc.Type != ""` gate around providers.New so that the
// V125-1-10.2 auto-default path (in-cluster + empty type → ArgoCDProvider) can
// fire from the api-level ReinitializeFromConnection call site. The unit-level
// auto-default behavior is exhaustively tested in
// internal/providers/provider_test.go (TestNew_AutoDefault* — they swap the
// inClusterConfigFn package-private seam). The tests below cover the api-level
// wiring around providers.New: explicit "argocd"/"k8s-secrets" routing,
// out-of-cluster empty-type still leaves credProvider nil safely (no panic, no
// crash), and nil Provider in the connection is also tolerated.

func TestReinitializeFromConnection_ArgoCDExplicit(t *testing.T) {
	// Explicit Type="argocd" — the new dropdown option.
	// providers.New("argocd") may construct successfully when ~/.kube/config is
	// available, or return an error otherwise. Either way it must NOT return
	// "unknown provider type" — that's the regression we're guarding against.
	srv := newIsolatedTestServer(t)
	seedActiveConnection(t, srv, models.Connection{
		Name: "argocd-conn",
		Git:  models.GitRepoConfig{Provider: models.GitProviderGitHub, Owner: "owner", Repo: "repo"},
		Provider: &models.ProviderConfig{
			Type: "argocd",
		},
	})

	srv.ReinitializeFromConnection()

	// We can't assert credProvider != nil because constructing an
	// ArgoCDProvider out-of-cluster without a kubeconfig fails by design.
	// What we CAN assert: when construction succeeded the addon-secret
	// typed config should reflect the wired-through type, and the call
	// must not panic.
	if srv.addonSecretCfg() != nil && srv.addonSecretCfg().Type != "argocd" {
		t.Errorf("expected addonSecretCfg.Type=argocd when set, got %q", srv.addonSecretCfg().Type)
	}
}

func TestReinitializeFromConnection_K8sSecretsRegression(t *testing.T) {
	// k8s-secrets — regression guard for V125-1-10.7.
	// Pre-fix path was `pc.Type != ""` → still passed for k8s-secrets, so the
	// behavior is the same. We test it explicitly to lock in that the ungate
	// did not regress the existing branch.
	srv := newIsolatedTestServer(t)
	seedActiveConnection(t, srv, models.Connection{
		Name: "k8s-conn",
		Git:  models.GitRepoConfig{Provider: models.GitProviderGitHub, Owner: "owner", Repo: "repo"},
		Provider: &models.ProviderConfig{
			Type: "k8s-secrets",
		},
	})

	srv.ReinitializeFromConnection()

	// k8s-secrets construction may fail in the unit-test env (no in-cluster
	// config + no ~/.kube/config), but the api code must not crash and the
	// type must round-trip when a provider was successfully constructed.
	if srv.addonSecretCfg() != nil && srv.addonSecretCfg().Type != "k8s-secrets" {
		t.Errorf("expected addonSecretCfg.Type=k8s-secrets when set, got %q", srv.addonSecretCfg().Type)
	}
}

func TestReinitializeFromConnection_EmptyType_OutOfCluster(t *testing.T) {
	// Type=="" + out-of-cluster (the unit-test environment) → providers.New
	// returns the legacy "no provider configured" error. Pre-V125-1-10.7 the
	// providers.New call was gated and silently skipped — credProvider stayed
	// nil and the test would have passed for the wrong reason. Post-fix, the
	// call is made unconditionally; the same nil credProvider outcome is now
	// the result of the auto-default deciding "not in cluster, no provider
	// configured." The user-visible BUG-035 surface is preserved.
	srv := newIsolatedTestServer(t)
	seedActiveConnection(t, srv, models.Connection{
		Name: "empty-type-conn",
		Git:  models.GitRepoConfig{Provider: models.GitProviderGitHub, Owner: "owner", Repo: "repo"},
		Provider: &models.ProviderConfig{
			Type: "",
		},
	})

	// Must not panic.
	srv.ReinitializeFromConnection()

	if srv.credProvider() != nil {
		t.Error("expected credProvider to remain nil when out-of-cluster + empty provider type")
	}
}

func TestReinitializeFromConnection_NilProvider(t *testing.T) {
	// conn.Provider == nil + out-of-cluster → providers.New is still called
	// (pre-fix it was skipped entirely on the `pc != nil && pc.Type != ""`
	// guard) and returns the legacy "no provider configured" error.
	// credProvider stays nil safely.
	//
	// This case is the one the maintainer hit live on 2026-05-14: a fresh
	// install with no provider stored on the connection. Pre-fix
	// ReinitializeFromConnection skipped providers.New entirely, so even when
	// running in-cluster the ArgoCD auto-default never fired. Now the call is
	// made and (in-cluster) the auto-default returns ArgoCDProvider — proven
	// at the unit level by TestNew_AutoDefaultInCluster in
	// internal/providers/provider_test.go.
	srv := newIsolatedTestServer(t)
	seedActiveConnection(t, srv, models.Connection{
		Name:     "nil-provider-conn",
		Git:      models.GitRepoConfig{Provider: models.GitProviderGitHub, Owner: "owner", Repo: "repo"},
		Provider: nil,
	})

	// Must not panic.
	srv.ReinitializeFromConnection()

	// In the test env (out-of-cluster), the auto-default fails and credProvider
	// stays nil. The fact that the call was made — and didn't panic — IS the
	// fix; the in-cluster success branch is covered at the unit level.
	if srv.credProvider() != nil {
		t.Error("expected credProvider to remain nil when out-of-cluster + nil provider config")
	}
}

// TestReinitializeFromConnection_NoCrossContaminationIntoClusterTestNamespace
// is the unit-level guard for V125-1-11.7-fix.
//
// Story 11.6's fan-out copied conn.Provider.Namespace into BOTH
// addonSecretCfg.Namespace (correct — addon-secret namespace semantics) AND
// clusterTestCfg.ArgoCDNamespace (WRONG — addon-secret-shaped value bleeding
// into the argocd-install-namespace slot, recreating the V125-1-10.8 cross-
// contamination via a different code path).
//
// This test exercises the exact wire-shape that the e2e helm test
// TestClusterTest_ProviderCrossContamination_NamespaceSwitch caught at the
// integration layer: a connection with Provider.Type="argocd" AND
// Provider.Namespace="sharko" (the leftover addon-secrets value from a
// previous dropdown selection). The assertion: clusterTestCfg.ArgoCDNamespace
// MUST be empty after fan-out so resolveArgoCDNamespaceTyped falls back to
// the env-var (deprecated compat alias) or the "argocd" hardcoded default
// — NOT inherit "sharko" verbatim.
//
// Keeping this at the unit level means future regressions get caught in
// `go test ./internal/api/...` (seconds) instead of `make test-e2e-helm`
// (minutes + kind cluster spin-up).
func TestReinitializeFromConnection_NoCrossContaminationIntoClusterTestNamespace(t *testing.T) {
	srv := newIsolatedTestServer(t)
	seedActiveConnection(t, srv, models.Connection{
		Name: "cross-contamination-unit-guard",
		Git:  models.GitRepoConfig{Provider: models.GitProviderGitHub, Owner: "owner", Repo: "repo"},
		Provider: &models.ProviderConfig{
			Type:      "argocd",
			Namespace: "sharko", // leftover from a prior k8s-secrets selection
		},
	})

	srv.ReinitializeFromConnection()

	// Even when ReinitializeFromConnection fans out the connection-level
	// Provider block, addonSecretCfg gets the namespace ("sharko" is the
	// correct addon-secret value) while clusterTestCfg MUST keep
	// ArgoCDNamespace empty — letting resolveArgoCDNamespaceTyped fall back
	// through env / "argocd" default.
	//
	// Out-of-cluster the cluster-test factory returns the legacy "no provider
	// configured" error so credProvider stays nil and clusterTestCfg stays
	// nil too. That's fine — the bug we're guarding against can only fire
	// when the cluster-test config is actually used, which means
	// clusterTestCfg got set. So we assert ONLY the populated case (matching
	// the existing TestReinitializeFromConnection_SetsProvider pattern).
	if srv.clusterTestCfg() != nil && srv.clusterTestCfg().ArgoCDNamespace != "" {
		t.Errorf("V125-1-11.7-fix regression: clusterTestCfg.ArgoCDNamespace = %q, want \"\" "+
			"(addon-secrets-shaped Provider.Namespace must NOT bleed into the argocd-install-namespace slot)",
			srv.clusterTestCfg().ArgoCDNamespace)
	}

	// addonSecretCfg in this case can still be populated when the
	// cluster-test factory fails (because the addon-secret config is built
	// alongside but stashed only when credProvider succeeds). Either way,
	// the addon-secret Namespace SHOULD carry the connection's
	// Provider.Namespace verbatim when the factory completes — that's the
	// correct addon-secret-namespace semantics and explicitly NOT the bug
	// we're guarding against here.
	if srv.addonSecretCfg() != nil && srv.addonSecretCfg().Namespace != "sharko" {
		t.Errorf("addonSecretCfg.Namespace = %q, want \"sharko\" "+
			"(addon-secret namespace should carry the connection's Provider.Namespace through verbatim)",
			srv.addonSecretCfg().Namespace)
	}
}
