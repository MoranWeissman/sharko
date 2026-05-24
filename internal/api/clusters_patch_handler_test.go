package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

// ZG2-1 — regression test for PATCH /api/v1/clusters/{name}.
//
// Background: nightly E2E on main (run 26354008904, sha abac48db, 2026-05-24)
// flagged TestClusterLifecycle/PatchClusterLabels failing with a 404 on a
// cluster that GET /api/v1/clusters/{name} had just successfully resolved.
//
// Root cause: handleUpdateClusterAddons resolves the cluster via
// argocd.ListClusters (resolveClusterServer in clusters_write.go). When the
// cluster is registered in Sharko's Git state (managed-clusters.yaml) but the
// V125-1-8 clusterreconciler has not yet projected it into the argocd
// namespace, this is the correct response — but the e2e test's compensation
// (registerClusterInArgoCDDirect, gated on the now-retired
// "argocd_register"/partial signal) was no longer firing, leaving the
// in-process harness with no path to populate ArgoCD's view of the cluster.
//
// The product behaviour is correct. These tests pin the contract so future
// drift (e.g., accidentally swapping the route to method=PUT, accidentally
// resolving by uid/id, dropping the route, or leaking the underlying error)
// fails at the unit boundary rather than 14 minutes deep in the e2e suite.
//
// Test seam: we stand up an httptest.Server that mimics ArgoCD's
// GET /api/v1/clusters and seed an active Connection pointing at it. The
// PATCH handler then resolves the cluster via the real argocd.Client against
// the stub. Body validation runs *after* the connection lookups in the
// handler (orchestrator construction + ArgoCD ListClusters happen first), so
// these tests use an empty-but-valid body (`{}`) and assert on the routing +
// lookup outcome only.

// argocdStubResponse models the subset of ArgoCD's /api/v1/clusters response
// that resolveClusterServer reads (item.name + item.server).
type argocdStubResponse struct {
	Items []argocdStubCluster `json:"items"`
}

type argocdStubCluster struct {
	Name   string `json:"name"`
	Server string `json:"server"`
}

// startArgocdStub returns an httptest.Server that responds to
// GET /api/v1/clusters with the supplied cluster list. Other methods/paths
// return 404 so accidental calls are loud.
func startArgocdStub(t *testing.T, clusters []argocdStubCluster) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/clusters", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(argocdStubResponse{Items: clusters})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// seedActiveConnectionWithArgo seeds an active connection that points at the
// supplied ArgoCD URL and carries a non-empty token (required by
// buildArgocdClient). Git config is the minimum that passes
// validateConnectionRequest; tests inject a GitProvider override on the
// connection service so the real GitHub client is never instantiated.
func seedActiveConnectionWithArgo(t *testing.T, srv *Server, argoURL string) {
	t.Helper()
	if err := srv.connSvc.Create(models.CreateConnectionRequest{
		Name: "patch-handler-test",
		Git: models.GitRepoConfig{
			Provider: models.GitProviderGitHub,
			Owner:    "sharko-test",
			Repo:     "sharko-addons",
		},
		Argocd: models.ArgocdConfig{
			ServerURL: argoURL,
			Token:     "stub-token-not-validated-by-httptest",
			Insecure:  true,
		},
	}); err != nil {
		t.Fatalf("seed connection: %v", err)
	}
	if err := srv.connSvc.SetActive("patch-handler-test"); err != nil {
		t.Fatalf("activate connection: %v", err)
	}
	// Install a fake GitProvider so handleUpdateClusterAddons' Tier1 git
	// lookup doesn't try to reach api.github.com — the failure paths under
	// test happen *before* we hit Git, but GitProviderForTier is invoked
	// up-front (see clusters_write.go line 313).
	srv.connSvc.SetGitProviderOverride(&handlerFakeGitProvider{files: map[string][]byte{}})
}

// patchCluster issues PATCH /api/v1/clusters/{name} against srv and returns
// (status, decoded JSON body).
func patchCluster(t *testing.T, srv *Server, name string, body map[string]any) (int, map[string]any) {
	t.Helper()
	router := NewRouter(srv, nil)
	payload, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/clusters/"+name, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	var decoded map[string]any
	if w.Body.Len() > 0 {
		if err := json.NewDecoder(w.Body).Decode(&decoded); err != nil {
			t.Fatalf("response is not valid JSON: %v (raw=%q)", err, w.Body.String())
		}
	}
	return w.Code, decoded
}

// TestPatchCluster_RouteRegistered_ReachesHandler proves the
// PATCH /api/v1/clusters/{name} route exists and dispatches to
// handleUpdateClusterAddons (not handleGetCluster, not 405). Without an
// active connection the handler short-circuits at the ArgoCD lookup with a
// 502 — that is the route-reached signal we pin here, separate from the
// not-found-in-argocd contract below.
func TestPatchCluster_RouteRegistered_ReachesHandler(t *testing.T) {
	srv := newIsolatedTestServer(t)
	// No connection seeded — GetActiveArgocdClient returns an error, and
	// handleUpdateClusterAddons converts it to a 502 (clusters_write.go
	// line 307). A 404 here would mean the PATCH route is missing.
	code, body := patchCluster(t, srv, "anything", map[string]any{})

	if code == http.StatusNotFound {
		t.Fatalf("PATCH /api/v1/clusters/{name} returned 404 — route NOT registered (ZG2-1 regression). body=%v", body)
	}
	if code == http.StatusMethodNotAllowed {
		t.Fatalf("PATCH /api/v1/clusters/{name} returned 405 — route registered for a different method (ZG2-1 regression). body=%v", body)
	}
	// Acceptable outcomes: 502 (no connection) or 403 (auth) — anything that
	// proves the handler ran. We assert "not 404, not 405" rather than a
	// specific positive status so this test stays stable across refactors of
	// the no-connection error path.
	if code/100 == 2 {
		t.Fatalf("PATCH with no connection returned %d — must be a non-2xx error. body=%v", code, body)
	}
}

// TestPatchCluster_NotInArgoCD_Returns404 pins the contract that PATCH on a
// cluster name that ArgoCD does not know about returns a structured 404 JSON
// response. This is exactly the case the nightly e2e hit (ZG2-1): the cluster
// is in Sharko's Git but the V125-1-8 cluster reconciler has not yet
// projected it into the argocd namespace.
func TestPatchCluster_NotInArgoCD_Returns404(t *testing.T) {
	argo := startArgocdStub(t, []argocdStubCluster{
		// ArgoCD has some other cluster, but NOT the one we PATCH.
		{Name: "some-other-cluster", Server: "https://other.example"},
	})
	srv := newIsolatedTestServer(t)
	seedActiveConnectionWithArgo(t, srv, argo.URL)
	srv.ReinitializeFromConnection()

	code, body := patchCluster(t, srv, "missing-from-argocd", map[string]any{})

	if code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404. body=%v", code, body)
	}
	errMsg, _ := body["error"].(string)
	if errMsg == "" {
		t.Errorf("response missing 'error' field; body=%v", body)
	}
	// The handler must NOT leak internal paths in the error message
	// (writeServerError invariant). The "cluster not found in ArgoCD: name"
	// message is intentional and operator-actionable.
	for _, leak := range []string{"/var/", "/etc/", ".sharko", "managed-clusters.yaml"} {
		if bytes.Contains([]byte(errMsg), []byte(leak)) {
			t.Errorf("response leaked internal path %q: %s", leak, errMsg)
		}
	}
}

// TestPatchCluster_EmptyName_Returns404 confirms the route's path matcher
// rejects an empty {name} segment — `PATCH /api/v1/clusters/` does not
// match `PATCH /api/v1/clusters/{name}` and must NOT reach the handler. The
// http.ServeMux response for an unmatched path is 404 (or, with a trailing
// slash, redirected to the canonical match). This test exists as a smoke
// guard against an accidental route change to `{name...}` (which would
// match empty) or to a wildcard mount.
func TestPatchCluster_EmptyName_Returns404(t *testing.T) {
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/clusters/", nil)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
		t.Errorf("PATCH /api/v1/clusters/ (no name) returned %d — expected 404 or 405", w.Code)
	}
}
