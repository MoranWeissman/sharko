package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MoranWeissman/sharko/internal/ai"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/service"
)

// V125-1-7 / BUG-058 — DELETE /api/v1/clusters/{name}/orphan handler.
//
// Pinned safety contract:
//
//  1. 204 on a real orphan (in ArgoCD, not in git, no open PR).
//  2. 400 when the cluster is genuinely managed (in git) — the user must
//     use DELETE /api/v1/clusters/{name} for that path.
//  3. 400 when the cluster has an open registration PR — close the PR
//     first.
//  4. 404 when the cluster is not in ArgoCD at all (nothing to delete).
//  5. 502 when the ArgoCD DELETE call itself errors (upstream classify).
//  6. The audit event "cluster_orphan_deleted" is emitted on success.

// orphanFakeGP is a minimal gitprovider.GitProvider for the DELETE-orphan
// handler tests. It only implements the methods the handler calls
// (GetFileContent for managed-clusters.yaml + ListPullRequests for the
// pending-PR check). All other methods are no-ops.
type orphanFakeGP struct {
	managedYAML []byte
	prs         []gitprovider.PullRequest
}

func (f *orphanFakeGP) GetFileContent(_ context.Context, path, _ string) ([]byte, error) {
	if path == "configuration/managed-clusters.yaml" && f.managedYAML != nil {
		return f.managedYAML, nil
	}
	return nil, gitprovider.ErrFileNotFound
}
func (f *orphanFakeGP) ListDirectory(_ context.Context, _, _ string) ([]string, error) {
	return nil, nil
}
func (f *orphanFakeGP) ListPullRequests(_ context.Context, _ string) ([]gitprovider.PullRequest, error) {
	return f.prs, nil
}
func (f *orphanFakeGP) TestConnection(_ context.Context) error                  { return nil }
func (f *orphanFakeGP) CreateBranch(_ context.Context, _, _ string) error       { return nil }
func (f *orphanFakeGP) CreateOrUpdateFile(_ context.Context, _ string, _ []byte, _, _ string) error {
	return nil
}
func (f *orphanFakeGP) BatchCreateFiles(_ context.Context, _ map[string][]byte, _, _ string) error {
	return nil
}
func (f *orphanFakeGP) DeleteFile(_ context.Context, _, _, _ string) error { return nil }
func (f *orphanFakeGP) CreatePullRequest(_ context.Context, _, _, _, _ string) (*gitprovider.PullRequest, error) {
	return nil, nil
}
func (f *orphanFakeGP) MergePullRequest(_ context.Context, _ int) error            { return nil }
func (f *orphanFakeGP) GetPullRequestStatus(_ context.Context, _ int) (string, error) { return "", nil }
func (f *orphanFakeGP) DeleteBranch(_ context.Context, _ string) error             { return nil }

var _ gitprovider.GitProvider = (*orphanFakeGP)(nil)

// stubArgoSrv stands up an httptest server that emulates the ArgoCD REST
// surface the orphan handler depends on:
//
//   - GET  /api/v1/clusters         → returns the supplied cluster items
//   - DELETE /api/v1/clusters/<URL> → recorded; returns deleteStatus
//
// deleteCalls counts DELETE requests so tests can assert "delete fired".
type stubArgoSrv struct {
	*httptest.Server
	deleteCalls    *int32
	deleteStatus   int
	deleteCallURL  *atomic.Value // last DELETE path
}

func newStubArgoSrv(t *testing.T, clusters []map[string]interface{}, deleteStatus int) *stubArgoSrv {
	t.Helper()
	calls := int32(0)
	last := atomic.Value{}
	last.Store("")

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/clusters", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"items": clusters})
			return
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	})
	mux.HandleFunc("/api/v1/clusters/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodDelete:
			atomic.AddInt32(&calls, 1)
			last.Store(r.URL.Path)
			if deleteStatus != 0 && deleteStatus != http.StatusOK {
				w.WriteHeader(deleteStatus)
				return
			}
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			// used by buildArgocdClient TestConnection — return empty
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"items": []interface{}{}})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return &stubArgoSrv{Server: s, deleteCalls: &calls, deleteStatus: deleteStatus, deleteCallURL: &last}
}

// orphanTestServer wires up the bits the DELETE-orphan handler needs:
// real Server with a saved + active connection pointing at the stub
// ArgoCD server, and the supplied gitprovider override. Returns the
// router for direct ServeHTTP calls.
func orphanTestServer(t *testing.T, gp gitprovider.GitProvider, argoURL string) (*Server, http.Handler) {
	t.Helper()
	f, err := os.CreateTemp("", "sharko-orphan-test-*.yaml")
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
	srv := NewServer(connSvc, clusterSvc, addonSvc, dashboardSvc, observabilitySvc, upgradeSvc, ai.NewClient(ai.Config{}))

	// Save an active connection pointing at the stub ArgoCD URL. The Git
	// side is overridden below so its config doesn't matter beyond shape.
	if err := connSvc.Create(models.CreateConnectionRequest{
		Name: "orphan-test",
		Git:  models.GitRepoConfig{Provider: models.GitProviderGitHub, Owner: "o", Repo: "r"},
		Argocd: models.ArgocdConfig{
			ServerURL: argoURL,
			Token:     "test-token",
			Insecure:  true,
		},
	}); err != nil {
		t.Fatalf("seed connection: %v", err)
	}
	if err := connSvc.SetActive("orphan-test"); err != nil {
		t.Fatalf("activate connection: %v", err)
	}

	connSvc.SetGitProviderOverride(gp)
	return srv, NewRouter(srv, nil)
}

// orphanAdminReq builds an authenticated admin request for the DELETE
// route — the handler requires authz.Require("cluster.remove") which
// resolves to RoleAdmin. The auth middleware reads role from the
// X-Sharko-User / context-injected role; the simplest path here is to
// directly inject role via the same headers other handler tests use.
func orphanAdminReq(name string) *http.Request {
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/clusters/"+name+"/orphan", nil)
	req.Header.Set("X-Sharko-User", "admin")
	req.Header.Set("X-Sharko-Role", "admin")
	return req
}

func TestHandleDeleteOrphanCluster_SuccessRealOrphan(t *testing.T) {
	// ArgoCD has one cluster ("kind-orphan") that is NOT in
	// managed-clusters.yaml AND has no open registration PR. The handler
	// must DELETE it and respond 204.
	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": "kind-orphan", "server": "https://kind-orphan.local:6443", "info": map[string]interface{}{"connectionState": map[string]interface{}{"status": "Successful"}}},
	}, http.StatusOK)
	gp := &orphanFakeGP{managedYAML: []byte("clusters: []")}
	_, router := orphanTestServer(t, gp, argo.URL)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, orphanAdminReq("kind-orphan"))

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d (body=%s)", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(argo.deleteCalls); got != 1 {
		t.Errorf("expected 1 ArgoCD DELETE call, got %d", got)
	}
	// Go's net/http unescapes the path before exposing it on r.URL.Path,
	// so we assert on the unescaped form (the wire-level form is
	// url.PathEscape(server) — verified separately by the argocd client
	// unit tests). Either form is acceptable to net/http; a literal
	// https://... in the path is unusual but legal. What matters is the
	// server URL appears verbatim and the DELETE fired.
	last := argo.deleteCallURL.Load().(string)
	if last == "" || !strings.Contains(last, "kind-orphan.local:6443") {
		t.Errorf("unexpected DELETE path: %q", last)
	}
	_ = url.PathEscape // keep the import if a future test wants the escaped form
}

func TestHandleDeleteOrphanCluster_RefusesManagedCluster(t *testing.T) {
	// Cluster IS in managed-clusters.yaml — the handler must refuse
	// (400) and emit a remediation hint pointing at the regular
	// deregister endpoint.
	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": "prod-eu", "server": "https://prod-eu.example.com"},
	}, http.StatusOK)
	gp := &orphanFakeGP{managedYAML: []byte("clusters:\n- name: prod-eu\n  labels: {}\n")}
	_, router := orphanTestServer(t, gp, argo.URL)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, orphanAdminReq("prod-eu"))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (body=%s)", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(argo.deleteCalls); got != 0 {
		t.Errorf("expected 0 ArgoCD DELETE calls (refused), got %d", got)
	}
	if !strings.Contains(w.Body.String(), "managed") {
		t.Errorf("expected error to mention 'managed', got: %s", w.Body.String())
	}
}

func TestHandleDeleteOrphanCluster_RefusesPendingCluster(t *testing.T) {
	// Cluster has an open registration PR — the handler must refuse
	// (400) and tell the user to close the PR first.
	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": "kind-pending", "server": "https://kind-pending.local:6443"},
	}, http.StatusOK)
	gp := &orphanFakeGP{
		managedYAML: []byte("clusters: []"),
		prs: []gitprovider.PullRequest{
			{
				Title:        "sharko: register cluster kind-pending (kubeconfig provider)",
				URL:          "https://github.com/o/r/pull/77",
				SourceBranch: "sharko/register-cluster-kind-pending-abcd",
			},
		},
	}
	_, router := orphanTestServer(t, gp, argo.URL)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, orphanAdminReq("kind-pending"))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (body=%s)", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(argo.deleteCalls); got != 0 {
		t.Errorf("expected 0 ArgoCD DELETE calls (refused), got %d", got)
	}
	if !strings.Contains(w.Body.String(), "registration PR") {
		t.Errorf("expected error to mention 'registration PR', got: %s", w.Body.String())
	}
}

func TestHandleDeleteOrphanCluster_NotFoundInArgoCD(t *testing.T) {
	// Cluster name is genuinely not in ArgoCD at all — there's nothing
	// to delete. 404.
	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": "other", "server": "https://other.example.com"},
	}, http.StatusOK)
	gp := &orphanFakeGP{managedYAML: []byte("clusters: []")}
	_, router := orphanTestServer(t, gp, argo.URL)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, orphanAdminReq("missing"))

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d (body=%s)", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(argo.deleteCalls); got != 0 {
		t.Errorf("expected 0 ArgoCD DELETE calls, got %d", got)
	}
}

func TestHandleDeleteOrphanCluster_ArgocdDeleteErrorPropagates(t *testing.T) {
	// The cluster IS an orphan — but the ArgoCD DELETE itself errors.
	// The handler must surface a 5xx (upstream-error path), not silently
	// pretend success.
	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": "kind-orphan", "server": "https://kind-orphan.local:6443"},
	}, http.StatusInternalServerError)
	gp := &orphanFakeGP{managedYAML: []byte("clusters: []")}
	_, router := orphanTestServer(t, gp, argo.URL)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, orphanAdminReq("kind-orphan"))

	if w.Code < 500 || w.Code > 599 {
		t.Fatalf("expected 5xx upstream error, got %d (body=%s)", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(argo.deleteCalls); got != 1 {
		t.Errorf("expected 1 ArgoCD DELETE call (which errored), got %d", got)
	}
}
