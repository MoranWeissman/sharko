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
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MoranWeissman/sharko/internal/argocd"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// forbiddenBootstrapArgocd returns an ArgoCD fake whose GetApplication fails
// with a 403/permission-denied error — wrapping argocd.ErrPermissionDenied the
// same way the real client does (GetApplication wraps doGet, doGet wraps the
// sentinel). This drives the V2-cleanup-10 "forbidden" classification.
func forbiddenBootstrapArgocd() *initFakeArgocd {
	return &initFakeArgocd{
		getErr: fmt.Errorf("getting application %q: %w",
			orchestrator.BootstrapRootAppName, argocd.ErrPermissionDenied),
	}
}

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

// V2-cleanup-51.1: bootstrap app reports Sync=Unknown → state "unreachable".
// ArgoCD's repo-server can't reach the Git repo; the wizard must distinguish
// this from a repairable "partial" so it doesn't auto-trap the user (Story 2).
func TestHandleInitStatus_Unreachable_SyncUnknown(t *testing.T) {
	ac := &initFakeArgocd{app: &models.ArgocdApplication{
		Name:         orchestrator.BootstrapRootAppName,
		SyncStatus:   "Unknown",
		HealthStatus: "Error",
	}}
	body := initStatusBody(t, initializedRepoGit(), ac)
	if body.State != RepoStateUnreachable {
		t.Errorf("expected state=%q, got %q", RepoStateUnreachable, body.State)
	}
	if body.Detail == "" {
		t.Error("expected a non-empty detail for unreachable state")
	}
}

// V2-cleanup-10: a 403 from ArgoCD (token lacks RBAC) must classify as
// "forbidden" with an actionable permission message — NOT "partial"
// (missing/unhealthy), which would send the user chasing a phantom broken
// bootstrap instead of fixing their token's permissions.
func TestHandleInitStatus_Forbidden(t *testing.T) {
	body := initStatusBody(t, initializedRepoGit(), forbiddenBootstrapArgocd())
	if body.State != RepoStateForbidden {
		t.Errorf("expected state=%q, got %q", RepoStateForbidden, body.State)
	}
	if body.Detail != permissionDeniedDetail {
		t.Errorf("expected permission-denied detail, got %q", body.Detail)
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

// V2-cleanup-51.1: a bootstrap app with Sync=Unknown classifies as
// "unreachable" — distinct from "partial". ArgoCD can't reach the repo, so
// re-init won't help; the wizard must not auto-trap the user (Story 2).
func TestProbeRepoState_Unreachable(t *testing.T) {
	ac := &initFakeArgocd{app: &models.ArgocdApplication{
		Name:         orchestrator.BootstrapRootAppName,
		SyncStatus:   "Unknown",
		HealthStatus: "Error",
	}}
	state, detail := probeRepoState(context.Background(), initializedRepoGit(), ac, "main")
	if state != RepoStateUnreachable {
		t.Errorf("expected state=unreachable, got %q", state)
	}
	if detail == "" {
		t.Error("expected non-empty detail for unreachable state")
	}
}

// V2-cleanup-51.1: a genuinely degraded bootstrap (OutOfSync/Degraded) stays
// "partial", NOT "unreachable" — ArgoCD read the repo and found a fixable
// problem, so re-init/repair is the right move.
func TestProbeRepoState_Degraded_StaysPartial(t *testing.T) {
	ac := &initFakeArgocd{app: &models.ArgocdApplication{
		Name:         orchestrator.BootstrapRootAppName,
		SyncStatus:   "OutOfSync",
		HealthStatus: "Degraded",
	}}
	state, _ := probeRepoState(context.Background(), initializedRepoGit(), ac, "main")
	if state != RepoStatePartial {
		t.Errorf("expected degraded bootstrap to stay partial, got %q", state)
	}
}

// V2-cleanup-10: a permission-denied error classifies as "forbidden" with the
// actionable message — distinct from "partial".
func TestProbeRepoState_Forbidden(t *testing.T) {
	state, detail := probeRepoState(context.Background(), initializedRepoGit(), forbiddenBootstrapArgocd(), "main")
	if state != RepoStateForbidden {
		t.Errorf("expected state=forbidden, got %q", state)
	}
	if detail != permissionDeniedDetail {
		t.Errorf("expected permission-denied detail, got %q", detail)
	}
}

// --- ProbeBootstrapApp — error-classification unit tests --------------------

// A 403/permission-denied underlying error → "forbidden" + permission message.
func TestProbeBootstrapApp_Forbidden(t *testing.T) {
	status, detail := ProbeBootstrapApp(context.Background(), forbiddenBootstrapArgocd())
	if status != "forbidden" {
		t.Errorf("expected status=forbidden, got %q", status)
	}
	if detail != permissionDeniedDetail {
		t.Errorf("expected permission-denied detail, got %q", detail)
	}
}

// A genuine not-found error → "unhealthy" + the bootstrap-missing message,
// NOT the permission message. Proves the special-casing is conservative.
func TestProbeBootstrapApp_NotFound(t *testing.T) {
	ac := &initFakeArgocd{getErr: errors.New("application not found")}
	status, detail := ProbeBootstrapApp(context.Background(), ac)
	if status != "unhealthy" {
		t.Errorf("expected status=unhealthy, got %q", status)
	}
	if detail == permissionDeniedDetail {
		t.Errorf("not-found must NOT use the permission message, got %q", detail)
	}
	if detail == "" {
		t.Error("expected non-empty detail for missing app")
	}
}

// V2-cleanup-51.1: an app whose Sync=Unknown means ArgoCD's repo-server
// could not reach/evaluate the Git repo → "unreachable", a connection problem
// re-init cannot fix. Distinct from "unhealthy" (OutOfSync/Degraded).
func TestProbeBootstrapApp_Unreachable_SyncUnknown(t *testing.T) {
	ac := &initFakeArgocd{app: &models.ArgocdApplication{
		Name:         orchestrator.BootstrapRootAppName,
		SyncStatus:   "Unknown",
		HealthStatus: "Error",
	}}
	status, detail := ProbeBootstrapApp(context.Background(), ac)
	if status != "unreachable" {
		t.Errorf("expected status=unreachable for Sync=Unknown, got %q", status)
	}
	if detail == "" {
		t.Error("expected a non-empty detail for the unreachable bootstrap app")
	}
	if detail == permissionDeniedDetail {
		t.Errorf("unreachable app must NOT use the permission message, got %q", detail)
	}
}

// An unhealthy/degraded app → "unhealthy", never the permission message.
func TestProbeBootstrapApp_Degraded(t *testing.T) {
	ac := &initFakeArgocd{app: &models.ArgocdApplication{
		Name:         orchestrator.BootstrapRootAppName,
		SyncStatus:   "OutOfSync",
		HealthStatus: "Degraded",
	}}
	status, detail := ProbeBootstrapApp(context.Background(), ac)
	if status != "unhealthy" {
		t.Errorf("expected status=unhealthy, got %q", status)
	}
	if detail == permissionDeniedDetail {
		t.Errorf("degraded app must NOT use the permission message, got %q", detail)
	}
}

// --- V2-cleanup-11.2 — LIST-and-filter probe ---------------------------------
//
// The probe now LISTs applications and filters by name instead of GET-by-name,
// because ArgoCD answers GET-on-missing-app with 403 (not 404) for apiKey
// tokens. The critical new case is "app absent on a fresh ArgoCD with a
// populated repo": LIST returns 200 + empty, and that must classify as
// "absent" (offer init), NOT "forbidden" (the V2-cleanup-10.2 regression).

// listApps present + healthy → "healthy".
func TestProbeBootstrapApp_ListPresentHealthy(t *testing.T) {
	ac := &initFakeArgocd{listApps: []models.ArgocdApplication{
		{Name: orchestrator.BootstrapRootAppName, SyncStatus: "Synced", HealthStatus: "Healthy"},
	}}
	status, detail := ProbeBootstrapApp(context.Background(), ac)
	if status != "healthy" {
		t.Errorf("expected status=healthy, got %q (detail %q)", status, detail)
	}
}

// listApps present but degraded → "unhealthy".
func TestProbeBootstrapApp_ListPresentDegraded(t *testing.T) {
	ac := &initFakeArgocd{listApps: []models.ArgocdApplication{
		{Name: orchestrator.BootstrapRootAppName, SyncStatus: "OutOfSync", HealthStatus: "Degraded"},
	}}
	status, detail := ProbeBootstrapApp(context.Background(), ac)
	if status != "unhealthy" {
		t.Errorf("expected status=unhealthy, got %q", status)
	}
	if detail == permissionDeniedDetail {
		t.Errorf("degraded app must NOT use the permission message, got %q", detail)
	}
}

// LIST succeeds with an empty result (other apps but not the bootstrap one) →
// "absent", NOT "forbidden", NOT "unhealthy". This is the bug the story fixes.
func TestProbeBootstrapApp_ListAbsent_NotForbidden(t *testing.T) {
	ac := &initFakeArgocd{listApps: []models.ArgocdApplication{
		{Name: "some-other-app", SyncStatus: "Synced", HealthStatus: "Healthy"},
	}}
	status, detail := ProbeBootstrapApp(context.Background(), ac)
	if status != "absent" {
		t.Errorf("expected status=absent for a missing bootstrap app, got %q", status)
	}
	if detail == permissionDeniedDetail {
		t.Errorf("absent app must NOT use the permission-denied message, got %q", detail)
	}
	if detail == "" {
		t.Error("expected a non-empty detail explaining the app is not created yet")
	}
}

// LIST itself 403s → "forbidden" + permission message (genuine RBAC problem).
func TestProbeBootstrapApp_ListForbidden(t *testing.T) {
	ac := &initFakeArgocd{listErr: fmt.Errorf("listing applications: %w", argocd.ErrPermissionDenied)}
	status, detail := ProbeBootstrapApp(context.Background(), ac)
	if status != "forbidden" {
		t.Errorf("expected status=forbidden on a LIST 403, got %q", status)
	}
	if detail != permissionDeniedDetail {
		t.Errorf("expected permission-denied detail, got %q", detail)
	}
}

// Through probeRepoState: file present + app absent (LIST ok, empty) must map
// to RepoStatePartial — the wizard offers init/repair, NOT an RBAC message.
func TestProbeRepoState_AbsentApp_MapsToPartial_NotForbidden(t *testing.T) {
	ac := &initFakeArgocd{listApps: []models.ArgocdApplication{}} // LIST ok, empty
	state, detail := probeRepoState(context.Background(), initializedRepoGit(), ac, "main")
	if state != RepoStatePartial {
		t.Fatalf("absent bootstrap app must map to partial, got %q", state)
	}
	if state == RepoStateForbidden || detail == permissionDeniedDetail {
		t.Fatalf("absent app must NOT be classified as forbidden/permission-denied; state=%q detail=%q", state, detail)
	}
}

// Through probeRepoState: a genuine LIST 403 still maps to RepoStateForbidden
// with the permission message preserved.
func TestProbeRepoState_ListForbidden_MapsToForbidden(t *testing.T) {
	ac := &initFakeArgocd{listErr: fmt.Errorf("listing applications: %w", argocd.ErrPermissionDenied)}
	state, detail := probeRepoState(context.Background(), initializedRepoGit(), ac, "main")
	if state != RepoStateForbidden {
		t.Fatalf("a genuine LIST 403 must map to forbidden, got %q", state)
	}
	if detail != permissionDeniedDetail {
		t.Fatalf("expected the permission-denied message preserved, got %q", detail)
	}
}
