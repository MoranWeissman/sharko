package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// v3RepoGit serves the v3 bootstrap marker (bootstrap/Chart.yaml) and NO
// engine pin — exactly what a repo Sharko set up before the v4 rename looks
// like today.
func v3RepoGit() *handlerFakeGitProvider {
	return &handlerFakeGitProvider{files: map[string][]byte{
		orchestrator.V3BootstrapMarkerPath: []byte("apiVersion: v2\nname: cluster-addons\n"),
	}}
}

// v3RegistryOnlyGit has no bootstrap chart but does have the v3 cluster
// registry — the shape of a repo Sharko adopted rather than bootstrapped.
func v3RegistryOnlyGit() *handlerFakeGitProvider {
	return &handlerFakeGitProvider{files: map[string][]byte{
		orchestrator.V3SecondaryMarkerPath: []byte("clusters:\n  - name: prod-eu\n"),
	}}
}

// legacyBootstrapApp is the v3 root Application — named
// "cluster-addons-bootstrap", not "sharko-engine".
func legacyBootstrapApp() *initFakeArgocd {
	return &initFakeArgocd{
		app: &models.ArgocdApplication{
			Name:         orchestrator.BootstrapRootAppNameLegacy,
			SyncStatus:   "Synced",
			HealthStatus: "Healthy",
		},
	}
}

// --- FIX-4: a v3 repo must not read as "never bootstrapped" ----------------

// TestInitStatus_V3Repo_ReportsInitialized — a live v3 repo has no engine
// pin. Probing only the pin classified it "empty", and the wizard then
// offered Initialize, which would have seeded the v4 folder tree on top of
// a working repo.
func TestInitStatus_V3Repo_ReportsInitialized(t *testing.T) {
	for name, gp := range map[string]*handlerFakeGitProvider{
		"bootstrap chart marker":  v3RepoGit(),
		"cluster registry marker": v3RegistryOnlyGit(),
	} {
		t.Run(name, func(t *testing.T) {
			body := initStatusBody(t, gp, legacyBootstrapApp())
			if body.State != RepoStateInitialized {
				t.Errorf("state = %q, want %q", body.State, RepoStateInitialized)
			}
			if body.Format != orchestrator.RepoFormatV3 {
				t.Errorf("format = %q, want %q", body.Format, orchestrator.RepoFormatV3)
			}
		})
	}
}

func TestInitStatus_EmptyRepo_StillOffersBootstrap(t *testing.T) {
	body := initStatusBody(t, emptyRepoGit(), healthyBootstrapApp())
	if body.State != RepoStateEmpty {
		t.Errorf("state = %q, want %q", body.State, RepoStateEmpty)
	}
	if body.Format != "" {
		t.Errorf("format = %q, want empty for a repo that was never set up", body.Format)
	}
}

func TestInitStatus_V4Repo_Unchanged(t *testing.T) {
	body := initStatusBody(t, initializedRepoGit(), healthyBootstrapApp())
	if body.State != RepoStateInitialized {
		t.Errorf("state = %q, want %q", body.State, RepoStateInitialized)
	}
	if body.Format != orchestrator.RepoFormatV4 {
		t.Errorf("format = %q, want %q", body.Format, orchestrator.RepoFormatV4)
	}
}

// repoStatusBody runs GET /api/v1/repo/status and decodes the response.
func repoStatusBody(t *testing.T, gp *handlerFakeGitProvider, ac orchestrator.ArgocdClient) RepoStatusResponse {
	t.Helper()
	srv := newTestServer()
	srv.publishGitopsCfg(orchestrator.GitOpsConfig{BaseBranch: "main"})
	srv.connSvc.SetGitProviderOverride(gp)
	if ac != nil {
		srv.connSvc.SetArgocdClientOverride(ac)
	}
	router := NewRouter(srv, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/repo/status", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	var body RepoStatusResponse
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body
}

func TestRepoStatus_V3Repo_ReportsInitialized(t *testing.T) {
	body := repoStatusBody(t, v3RepoGit(), legacyBootstrapApp())
	if !body.Initialized {
		t.Error("initialized = false; a live v3 repo must not read as never-bootstrapped")
	}
	if body.Format != orchestrator.RepoFormatV3 {
		t.Errorf("format = %q, want %q", body.Format, orchestrator.RepoFormatV3)
	}
	if body.Reason == "not_bootstrapped" {
		t.Error("reason = not_bootstrapped; the wizard would offer a destructive re-init")
	}
	// FIX-5 rides along here: the v3 bootstrap app is named
	// "cluster-addons-bootstrap", and the probe must find it under that
	// name rather than reporting the healthy cluster as degraded.
	if !body.BootstrapSynced {
		t.Error("bootstrap_synced = false; the legacy-named bootstrap app was not recognised")
	}
}

func TestRepoStatus_EmptyRepo_StillNotBootstrapped(t *testing.T) {
	body := repoStatusBody(t, emptyRepoGit(), healthyBootstrapApp())
	if body.Initialized {
		t.Error("initialized = true for a repo with neither an engine pin nor a v3 marker")
	}
	if body.Reason != "not_bootstrapped" {
		t.Errorf("reason = %q, want not_bootstrapped", body.Reason)
	}
}

func TestRepoStatus_V4Repo_Unchanged(t *testing.T) {
	body := repoStatusBody(t, initializedRepoGit(), healthyBootstrapApp())
	if !body.Initialized || !body.BootstrapSynced {
		t.Errorf("v4 repo status regressed: %+v", body)
	}
	if body.Format != orchestrator.RepoFormatV4 {
		t.Errorf("format = %q, want %q", body.Format, orchestrator.RepoFormatV4)
	}
}

// --- FIX-5: the bootstrap-app probe must accept both names -----------------

func TestProbeBootstrapApp_FindsBothNames(t *testing.T) {
	for name, ac := range map[string]*initFakeArgocd{
		"current name (sharko-engine)":       healthyBootstrapApp(),
		"v3 name (cluster-addons-bootstrap)": legacyBootstrapApp(),
	} {
		t.Run(name, func(t *testing.T) {
			status, detail := ProbeBootstrapApp(context.Background(), ac)
			if status != bootstrapHealthy {
				t.Errorf("status = %q (detail %q), want healthy", status, detail)
			}
		})
	}
}

func TestProbeBootstrapApp_NeitherName_ReportsAbsent(t *testing.T) {
	ac := &initFakeArgocd{listApps: []models.ArgocdApplication{{
		Name: "something-else", SyncStatus: "Synced", HealthStatus: "Healthy",
	}}}
	status, _ := ProbeBootstrapApp(context.Background(), ac)
	if status == bootstrapHealthy {
		t.Error("an unrelated app was mistaken for the bootstrap app")
	}
}
