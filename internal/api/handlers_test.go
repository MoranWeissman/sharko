package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// ---------------------------------------------------------------------------
// Fake GitProvider for handler tests
// ---------------------------------------------------------------------------

// handlerFakeGitProvider is a minimal gitprovider.GitProvider that returns a
// fixed set of file contents. Missing paths return a non-nil error.
type handlerFakeGitProvider struct {
	files map[string][]byte
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
	return nil, nil
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
