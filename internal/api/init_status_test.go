// init_status_test.go — V2-cleanup-9.1 coverage for the read-only repo-state
// probe (GET /api/v1/init/status) and the shared probeRepoState helper that
// both the probe handler AND runInitOperation classify with.
//
// The four cases mirror the wizard's Step-4 render branches:
//   - empty        — bootstrap root-app YAML absent on the base branch
//   - initialized  — file present AND ArgoCD bootstrap Synced + Healthy
//   - partial      — file present but ArgoCD bootstrap missing/unhealthy
//   - no-connection error — no active Git connection (wizard falls back)
//
// They reuse the existing handlerFakeGitProvider + initFakeArgocd fakes so the
// probe shares fixtures with the handleRepoStatus tests in handlers_test.go.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// initStatusBody runs GET /api/v1/init/status against a server wired with the
// given git provider override + (optional) ArgoCD override, asserts 200, and
// returns the decoded response. baseBranch is forced to "main".
func initStatusBody(t *testing.T, gp *handlerFakeGitProvider, ac orchestrator.ArgocdClient) InitStatusResponse {
	t.Helper()
	srv := newTestServer()
	srv.gitopsCfg = orchestrator.GitOpsConfig{BaseBranch: "main"}
	srv.connSvc.SetGitProviderOverride(gp)
	if ac != nil {
		srv.connSvc.SetArgocdClientOverride(ac)
	}

	router := NewRouter(srv, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/init/status", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body %s)", w.Code, w.Body.String())
	}
	var body InitStatusResponse
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return body
}

// healthyBootstrapApp is the ArgoCD app fixture for a Synced + Healthy
// bootstrap root application.
func healthyBootstrapApp() *initFakeArgocd {
	return &initFakeArgocd{
		app: &models.ArgocdApplication{
			Name:         orchestrator.BootstrapRootAppName,
			SyncStatus:   "Synced",
			HealthStatus: "Healthy",
		},
	}
}

// emptyRepoGit returns a git provider with no files — every GetFileContent
// errors, so the bootstrap root-app YAML is "missing".
func emptyRepoGit() *handlerFakeGitProvider {
	return &handlerFakeGitProvider{files: map[string][]byte{}}
}

// initializedRepoGit returns a git provider that serves the bootstrap
// root-app YAML at orchestrator.BootstrapRootAppPath on the base branch.
func initializedRepoGit() *handlerFakeGitProvider {
	return &handlerFakeGitProvider{files: map[string][]byte{
		orchestrator.BootstrapRootAppPath: []byte("kind: Application\n"),
	}}
}

// --- GET /api/v1/init/status — end-to-end handler tests ---------------------

func TestHandleInitStatus_Empty(t *testing.T) {
	body := initStatusBody(t, emptyRepoGit(), healthyBootstrapApp())
	if body.State != RepoStateEmpty {
		t.Errorf("expected state=%q, got %q", RepoStateEmpty, body.State)
	}
	if body.Detail != "" {
		t.Errorf("expected empty detail, got %q", body.Detail)
	}
}

func TestHandleInitStatus_Initialized(t *testing.T) {
	body := initStatusBody(t, initializedRepoGit(), healthyBootstrapApp())
	if body.State != RepoStateInitialized {
		t.Errorf("expected state=%q, got %q", RepoStateInitialized, body.State)
	}
	if body.Detail != "" {
		t.Errorf("expected empty detail, got %q", body.Detail)
	}
}

func TestHandleInitStatus_Partial_AppMissing(t *testing.T) {
	ac := &initFakeArgocd{getErr: errors.New("application not found: cluster-addons-bootstrap")}
	body := initStatusBody(t, initializedRepoGit(), ac)
	if body.State != RepoStatePartial {
		t.Errorf("expected state=%q, got %q", RepoStatePartial, body.State)
	}
	if body.Detail == "" {
		t.Error("expected a non-empty detail for partial state")
	}
}

func TestHandleInitStatus_Partial_AppDegraded(t *testing.T) {
	ac := &initFakeArgocd{app: &models.ArgocdApplication{
		Name:         orchestrator.BootstrapRootAppName,
		SyncStatus:   "OutOfSync",
		HealthStatus: "Degraded",
	}}
	body := initStatusBody(t, initializedRepoGit(), ac)
	if body.State != RepoStatePartial {
		t.Errorf("expected state=%q, got %q", RepoStatePartial, body.State)
	}
	if body.Detail == "" {
		t.Error("expected a non-empty detail for degraded bootstrap app")
	}
}

// No active Git connection — no override installed and the test config has no
// connection — must surface a clear error (502) the wizard can fall back on,
// not a panic or a misleading "empty".
func TestHandleInitStatus_NoConnection(t *testing.T) {
	srv := newTestServer()
	router := NewRouter(srv, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/init/status", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 with no Git connection, got %d (body %s)", w.Code, w.Body.String())
	}
}

// --- probeRepoState — shared helper unit tests ------------------------------
//
// These hit the helper directly so the classification logic is covered
// independent of the HTTP layer, proving POST /init and GET /init/status agree
// because both call this one function.

func TestProbeRepoState_Empty(t *testing.T) {
	state, detail := probeRepoState(context.Background(), emptyRepoGit(), healthyBootstrapApp(), "main")
	if state != RepoStateEmpty || detail != "" {
		t.Errorf("expected (empty, ''), got (%q, %q)", state, detail)
	}
}

func TestProbeRepoState_Initialized(t *testing.T) {
	state, detail := probeRepoState(context.Background(), initializedRepoGit(), healthyBootstrapApp(), "main")
	if state != RepoStateInitialized || detail != "" {
		t.Errorf("expected (initialized, ''), got (%q, %q)", state, detail)
	}
}

func TestProbeRepoState_Partial(t *testing.T) {
	ac := &initFakeArgocd{getErr: errors.New("application not found")}
	state, detail := probeRepoState(context.Background(), initializedRepoGit(), ac, "main")
	if state != RepoStatePartial {
		t.Errorf("expected state=partial, got %q", state)
	}
	if detail == "" {
		t.Error("expected non-empty detail for partial state")
	}
}
