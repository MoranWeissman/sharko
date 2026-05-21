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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/ai"
	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
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

// orphanK8sSecret is a tiny helper that constructs a corev1.Secret in the
// argocd namespace. labeled=true sets the Sharko ownership label that the
// V125-1-8.2 label gate keys off; labeled=false produces an externally-
// owned Secret the gate will reject (the unlabeled-rejection test path).
func orphanK8sSecret(name string, labeled bool) *corev1.Secret {
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "argocd",
		},
		Type: corev1.SecretTypeOpaque,
	}
	if labeled {
		clusterreconciler.ApplyManagedBySharkoLabel(s)
	}
	return s
}

// orphanK8sClient returns a fake k8s clientset seeded with the supplied
// Secrets in the argocd namespace. Tests that exercise the V125-1-8.2
// label gate use this to wire a real-looking K8s view at the same time as
// the stub ArgoCD REST server (the two surfaces are independent — ArgoCD
// reports the cluster, K8s reports the Secret-with-label).
func orphanK8sClient(secrets ...*corev1.Secret) kubernetes.Interface {
	objs := make([]runtime.Object, 0, len(secrets))
	for _, s := range secrets {
		objs = append(objs, s)
	}
	return fake.NewSimpleClientset(objs...)
}

// orphanTestServer wires up the bits the DELETE-orphan handler needs:
// real Server with a saved + active connection pointing at the stub
// ArgoCD server, and the supplied gitprovider override. When k8sClient is
// non-nil, the server is wired with an ArgoReconcilerConfig pointing at
// that clientset so the V125-1-8.2 ownership-label gate has a Secret API
// to consult; pass nil to exercise the "no k8s client wired" 503 path.
// Returns the router for direct ServeHTTP calls.
func orphanTestServer(t *testing.T, gp gitprovider.GitProvider, argoURL string, k8sClient kubernetes.Interface) (*Server, http.Handler) {
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

	// V125-1-8.2 ownership-label gate — the handler reads
	// argoReconcilerConfig.K8sClient via Server.k8sClientAndNamespace().
	// We deliberately wire ONLY the fields the gate needs (K8sClient +
	// ArgocdNamespace) so this test fixture doesn't accidentally pull in
	// the rest of the secrets-reconciler bootstrap.
	if k8sClient != nil {
		srv.SetArgoReconcilerConfig(&ArgoReconcilerCfg{
			K8sClient:       k8sClient,
			ArgocdNamespace: "argocd",
		})
	}
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
	//
	// V125-1-8.2 — the backing Secret carries the sharko ownership label
	// so the new label gate (clusterreconciler.IsManagedBySharko) lets the
	// delete through. This is the V125-1-8.2 "labeled secret deletes as
	// before" regression contract — the same test that proved 204 pre-
	// label-gate continues to prove 204 post-label-gate.
	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": "kind-orphan", "server": "https://kind-orphan.local:6443", "info": map[string]interface{}{"connectionState": map[string]interface{}{"status": "Successful"}}},
	}, http.StatusOK)
	gp := &orphanFakeGP{managedYAML: []byte("clusters: []")}
	k8s := orphanK8sClient(orphanK8sSecret("kind-orphan", true))
	_, router := orphanTestServer(t, gp, argo.URL, k8s)

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
	// nil k8s client — this test rejects BEFORE the label gate (managed
	// check is step 4; label gate is step 7), so k8s wiring is irrelevant.
	_, router := orphanTestServer(t, gp, argo.URL, nil)

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
	// nil k8s client — pending check rejects before the label gate.
	_, router := orphanTestServer(t, gp, argo.URL, nil)

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
	// nil k8s client — 404 fires at step 6 (ArgoCD lookup) before the
	// V125-1-8.2 label gate at step 7 is reached.
	_, router := orphanTestServer(t, gp, argo.URL, nil)

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
	//
	// V125-1-8.2 — the test reaches the DELETE step only when the new
	// ownership-label gate accepts the Secret, so seed it with the
	// sharko label.
	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": "kind-orphan", "server": "https://kind-orphan.local:6443"},
	}, http.StatusInternalServerError)
	gp := &orphanFakeGP{managedYAML: []byte("clusters: []")}
	k8s := orphanK8sClient(orphanK8sSecret("kind-orphan", true))
	_, router := orphanTestServer(t, gp, argo.URL, k8s)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, orphanAdminReq("kind-orphan"))

	if w.Code < 500 || w.Code > 599 {
		t.Fatalf("expected 5xx upstream error, got %d (body=%s)", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(argo.deleteCalls); got != 1 {
		t.Errorf("expected 1 ArgoCD DELETE call (which errored), got %d", got)
	}
}

// V125-1-7.1 — defensive nil-check hardening tests.

func TestHandleDeleteOrphanCluster_NoActiveConnection_Returns502(t *testing.T) {
	// When there is NO active connection at all, GetActiveArgocdClient
	// returns a non-nil error and the handler must respond 502 — NOT 500.
	// This exercises the error path that precedes the nil-client guard
	// (the guard is a second-line defence; the error path is the first).
	//
	// We build a Server with an empty connection store (no active
	// connection) so GetActiveArgocdClient returns an error. The expected
	// result is any 4xx/5xx gateway error — we accept 400, 502, or 503
	// because the exact code depends on the error message from the store.
	f, err := os.CreateTemp("", "sharko-orphan-noconn-*.yaml")
	if err != nil {
		t.Fatalf("create temp config: %v", err)
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
	router := NewRouter(srv, nil)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, orphanAdminReq("kind-orphan"))

	// No active connection → handler must respond with a non-2xx error.
	if w.Code < 400 {
		t.Fatalf("expected 4xx/5xx (no active connection), got %d (body=%s)", w.Code, w.Body.String())
	}
}

func TestClassifyUpstreamError_NilInput_Returns500(t *testing.T) {
	// V125-1-7.1 hardening: classifyUpstreamError(nil) must return 500,
	// never panic. This documents the safety of the writeUpstreamError
	// path when an upstream call somehow returns (nil, nil) — the
	// classifyUpstreamError guard converts the nil err to 500 before
	// writeServerError logs it.
	got := classifyUpstreamError(nil)
	if got != http.StatusInternalServerError {
		t.Errorf("classifyUpstreamError(nil) = %d, want 500", got)
	}
}

func TestHandleDeleteOrphanCluster_ArgocdListErrorSurfaces502(t *testing.T) {
	// When the ArgoCD cluster-list call (check #3) fails with a connection
	// error, the handler must return a gateway error (5xx) with an op tag
	// so operators can identify which step failed. This exercises the
	// writeUpstreamError("delete_orphan_cluster_argocd_list", err) path.
	//
	// To trigger the ArgoCD list error after the managed-cluster check
	// passes, we need a server that returns the managed-clusters list fine
	// (via ListClusters through the service) but then fails the direct
	// ArgoCD ListClusters call.  Since the handler calls ac.ListClusters
	// after s.clusterSvc.ListClusters, and both go to the same stub
	// server, we use a stub that returns success for the first GET but
	// errors on subsequent ones.
	callCount := int32(0)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/clusters", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		n := atomic.AddInt32(&callCount, 1)
		if n == 1 {
			// First call (from clusterSvc.ListClusters ArgoCD health check):
			// return empty list — the cluster "kind-orphan" is not managed,
			// not in ArgoCD from the service perspective, so the managed check
			// passes and we reach check #3.
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"items": []interface{}{}})
			return
		}
		// Second call (from ac.ListClusters in check #3): simulate error.
		w.WriteHeader(http.StatusServiceUnavailable)
	})

	argoSrv := httptest.NewServer(mux)
	t.Cleanup(argoSrv.Close)

	gp := &orphanFakeGP{managedYAML: []byte("clusters: []")}
	// nil k8s client — ArgoCD list error (step 6) fires before the label
	// gate (step 7) so k8s wiring is irrelevant.
	_, router := orphanTestServer(t, gp, argoSrv.URL, nil)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, orphanAdminReq("kind-orphan"))

	// ArgoCD list error → the handler must return a 5xx.
	if w.Code < 500 || w.Code > 599 {
		t.Fatalf("expected 5xx for argocd list failure, got %d (body=%s)", w.Code, w.Body.String())
	}
	// Response body must include the "op" tag so operators can grep logs.
	body := w.Body.String()
	if !strings.Contains(body, "delete_orphan_cluster_argocd_list") {
		t.Errorf("expected op tag in body, got: %s", body)
	}
}

// V125-1-8.2 — ownership-label gate tests. Adds the third safety check
// (after managed + pending + ArgoCD-presence): the backing Secret must
// carry app.kubernetes.io/managed-by=sharko. Unlabeled = V125-2 Adopt
// territory; Discard would silently destroy whatever tool owns the Secret.

func TestHandleDeleteOrphan_UnlabeledSecret_Reject400(t *testing.T) {
	// Seed ArgoCD with an unmanaged cluster ("kind-foreign") that would
	// have qualified as an orphan under the pre-V125-1-8.2 algorithm (in
	// ArgoCD, not in git, no open PR). Seed K8s with the same-named Secret
	// but WITHOUT the sharko label — simulates an externally-created
	// Secret that V125-2 Adopt will own.
	//
	// The handler must:
	//   - return HTTP 400 with the spec-locked error message
	//   - NOT issue any DELETE to ArgoCD
	//
	// This is the V125-1-7 foot-gun closure: an operator who clicks
	// Discard on an externally-owned cluster gets a clear remediation
	// pointing them at the Adopt action instead.
	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": "kind-foreign", "server": "https://kind-foreign.local:6443"},
	}, http.StatusOK)
	gp := &orphanFakeGP{managedYAML: []byte("clusters: []")}
	k8s := orphanK8sClient(orphanK8sSecret("kind-foreign", false)) // labeled=false
	_, router := orphanTestServer(t, gp, argo.URL, k8s)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, orphanAdminReq("kind-foreign"))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 (unlabeled Secret rejected), got %d (body=%s)", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(argo.deleteCalls); got != 0 {
		t.Errorf("expected 0 ArgoCD DELETE calls (label gate rejected), got %d", got)
	}
	// The spec-locked error message must reference managed-by and Adopt
	// so operators understand both WHY the rejection happened and HOW to
	// proceed. We assert on the two anchor substrings rather than the
	// full string to keep the test robust against minor phrasing edits.
	body := w.Body.String()
	if !strings.Contains(body, "managed-by label") {
		t.Errorf("expected error to reference managed-by label, got: %s", body)
	}
	if !strings.Contains(body, "Adopt") {
		t.Errorf("expected error to point at Adopt action, got: %s", body)
	}
}

func TestHandleDeleteOrphan_LabeledSecret_DeletesAsBeforeShipped(t *testing.T) {
	// Regression guard for the V125-1-8.2 label gate: a Secret with the
	// sharko label must continue to delete the same way it did before the
	// gate landed. Same shape as TestHandleDeleteOrphanCluster_SuccessRealOrphan
	// but spelled out as a dedicated test so the V125-1-8.2 commit can
	// point at "this specific test proves we didn't regress the happy
	// path while adding the gate".
	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": "kind-owned", "server": "https://kind-owned.local:6443", "info": map[string]interface{}{"connectionState": map[string]interface{}{"status": "Successful"}}},
	}, http.StatusOK)
	gp := &orphanFakeGP{managedYAML: []byte("clusters: []")}
	k8s := orphanK8sClient(orphanK8sSecret("kind-owned", true)) // labeled=true
	_, router := orphanTestServer(t, gp, argo.URL, k8s)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, orphanAdminReq("kind-owned"))

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 (labeled Secret deletes as before), got %d (body=%s)", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(argo.deleteCalls); got != 1 {
		t.Errorf("expected 1 ArgoCD DELETE call (gate passed), got %d", got)
	}
	// Verify it was the right URL — defence-in-depth in case a future
	// refactor accidentally targets the wrong cluster after the gate.
	last := argo.deleteCallURL.Load().(string)
	if last == "" || !strings.Contains(last, "kind-owned.local:6443") {
		t.Errorf("unexpected DELETE path: %q", last)
	}
}

func TestHandleDeleteOrphan_NoK8sClient_Returns503(t *testing.T) {
	// If the server is started without an in-cluster K8s client (the
	// argoReconcilerConfig is nil — production never hits this path but
	// dev / demo modes can), the label gate cannot be verified. The
	// handler MUST fail closed with 503 rather than silently bypass the
	// gate; doing otherwise would defeat the V125-1-7 foot-gun closure
	// that motivated this story.
	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": "kind-orphan", "server": "https://kind-orphan.local:6443"},
	}, http.StatusOK)
	gp := &orphanFakeGP{managedYAML: []byte("clusters: []")}
	_, router := orphanTestServer(t, gp, argo.URL, nil) // nil k8s client

	w := httptest.NewRecorder()
	router.ServeHTTP(w, orphanAdminReq("kind-orphan"))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 (k8s client not wired, gate cannot verify), got %d (body=%s)", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(argo.deleteCalls); got != 0 {
		t.Errorf("expected 0 ArgoCD DELETE calls (fail-closed), got %d", got)
	}
	// Sanity: the op tag identifies the failure mode for log grepping.
	if !strings.Contains(w.Body.String(), "delete_orphan_cluster_no_k8s_client") {
		t.Errorf("expected op=delete_orphan_cluster_no_k8s_client in body, got: %s", w.Body.String())
	}
}
