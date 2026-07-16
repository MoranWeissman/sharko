package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/ai"
	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
	"github.com/MoranWeissman/sharko/internal/service"
)

// V2-cleanup-89.4 — POST /api/v1/clusters/{name}/reconcile ("sync now").
//
// Pinned contract:
//  1. 202 on a known cluster, and the reconciler's Trigger() fires exactly
//     once — this is a global-pass nudge (see the handler doc comment for
//     why), not a targeted single-cluster reconcile.
//  2. 404 when the cluster is not in managed-clusters.yaml.
//  3. 503 when no cluster reconciler is wired on this server instance.
//  4. 403 for a viewer — this is an operator+ action.

// reconcileFakeGP is a minimal gitprovider.GitProvider for
// handleReconcileCluster tests — only GetFileContent(managed-clusters.yaml)
// is exercised; every other method is a no-op stub.
type reconcileFakeGP struct {
	managedYAML []byte
}

func (f *reconcileFakeGP) GetFileContent(_ context.Context, path, _ string) ([]byte, error) {
	if path == "configuration/managed-clusters.yaml" && f.managedYAML != nil {
		return f.managedYAML, nil
	}
	return nil, gitprovider.ErrFileNotFound
}
func (f *reconcileFakeGP) ListDirectory(_ context.Context, _, _ string) ([]string, error) {
	return nil, nil
}
func (f *reconcileFakeGP) ListPullRequests(_ context.Context, _ string) ([]gitprovider.PullRequest, error) {
	return nil, nil
}
func (f *reconcileFakeGP) TestConnection(_ context.Context) error            { return nil }
func (f *reconcileFakeGP) CreateBranch(_ context.Context, _, _ string) error { return nil }
func (f *reconcileFakeGP) CreateOrUpdateFile(_ context.Context, _ string, _ []byte, _, _ string) error {
	return nil
}
func (f *reconcileFakeGP) BatchCreateFiles(_ context.Context, _ map[string][]byte, _, _ string) error {
	return nil
}
func (f *reconcileFakeGP) DeleteFile(_ context.Context, _, _, _ string) error { return nil }
func (f *reconcileFakeGP) CreatePullRequest(_ context.Context, _, _, _, _ string) (*gitprovider.PullRequest, error) {
	return nil, nil
}
func (f *reconcileFakeGP) MergePullRequest(_ context.Context, _ int) error { return nil }
func (f *reconcileFakeGP) GetPullRequestStatus(_ context.Context, _ int) (string, error) {
	return "", nil
}
func (f *reconcileFakeGP) DeleteBranch(_ context.Context, _ string) error { return nil }

// reconcileTestServer wires a real Server against a stub ArgoCD server and
// the supplied git provider — same shape as orphanTestServer in
// clusters_orphan_delete_test.go, kept separate so this file's fixtures
// don't couple to the orphan-delete suite's evolution.
func reconcileTestServer(t *testing.T, gp gitprovider.GitProvider, argoURL string) (*Server, http.Handler) {
	t.Helper()
	f, err := os.CreateTemp("", "sharko-reconcile-test-*.yaml")
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
	clusterSvc := service.NewClusterService("")
	observabilitySvc := service.NewObservabilityService(clusterSvc)
	upgradeSvc := service.NewUpgradeService(ai.NewClient(ai.Config{}), nil, "")
	srv := NewServer(connSvc, clusterSvc, addonSvc, dashboardSvc, observabilitySvc, upgradeSvc, ai.NewClient(ai.Config{}))

	if err := connSvc.Create(models.CreateConnectionRequest{
		Name: "reconcile-test",
		Git:  models.GitRepoConfig{Provider: models.GitProviderGitHub, Owner: "o", Repo: "r"},
		Argocd: models.ArgocdConfig{
			ServerURL: argoURL,
			Token:     "test-token",
			Insecure:  true,
		},
	}); err != nil {
		t.Fatalf("seed connection: %v", err)
	}
	if err := connSvc.SetActive("reconcile-test"); err != nil {
		t.Fatalf("activate connection: %v", err)
	}
	connSvc.SetGitProviderOverride(gp)

	return srv, NewRouter(srv, nil)
}

// reconcileOperatorReq builds an authenticated operator request for the
// reconcile route — the handler requires authz.Require("cluster.reconcile")
// which resolves to RoleOperator.
func reconcileOperatorReq(name string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/"+name+"/reconcile", nil)
	req.Header.Set("X-Sharko-User", "op")
	req.Header.Set("X-Sharko-Role", "operator")
	return req
}

func TestHandleReconcileCluster_202_TriggersReconciler(t *testing.T) {
	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": "prod-eu", "server": "https://prod-eu.example.com"},
	}, http.StatusOK)
	gp := &reconcileFakeGP{managedYAML: []byte("clusters:\n- name: prod-eu\n  labels: {}\n")}
	srv, router := reconcileTestServer(t, gp, argo.URL)

	triggered := 0
	srv.SetReconcilerTrigger(func() { triggered++ })

	w := httptest.NewRecorder()
	router.ServeHTTP(w, reconcileOperatorReq("prod-eu"))

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d (body=%s)", w.Code, w.Body.String())
	}
	if triggered != 1 {
		t.Fatalf("expected the reconciler trigger to fire exactly once, got %d", triggered)
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["status"] != "accepted" {
		t.Errorf(`status = %q, want "accepted"`, body["status"])
	}
	// V2-cleanup-90.3 / review finding L2 — the 202 message must not
	// overclaim per-cluster scoping: this is a fleet-wide pass that happens
	// to include the named cluster, not a targeted single-cluster reconcile.
	wantMsg := `reconcile pass triggered — the fleet-wide pass includes cluster "prod-eu"`
	if body["message"] != wantMsg {
		t.Errorf("message = %q, want %q", body["message"], wantMsg)
	}
}

// TestHandleReconcileCluster_503_NoReconcilerWired_SkipsGitAndArgoCDRoundTrips
// pins the L2 handler-order fix: the cheap "is a reconciler wired" 503
// check must run BEFORE the Git/ArgoCD round-trips, so a server with no
// reconciler wired never touches the Git provider or ArgoCD client at all
// — even for a cluster that doesn't exist.
func TestHandleReconcileCluster_503_NoReconcilerWired_SkipsGitAndArgoCDRoundTrips(t *testing.T) {
	gp := &reconcileFakeGP{} // no managedYAML set — GetFileContent always errors if called
	_, router := reconcileTestServer(t, gp, "http://127.0.0.1:1") // unreachable ArgoCD URL
	// Deliberately do NOT call SetReconcilerTrigger.

	w := httptest.NewRecorder()
	router.ServeHTTP(w, reconcileOperatorReq("does-not-exist"))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 (reconciler-not-wired check must short-circuit before any Git/ArgoCD round-trip), got %d (body=%s)", w.Code, w.Body.String())
	}
}

func TestHandleReconcileCluster_404_UnknownCluster(t *testing.T) {
	argo := newStubArgoSrv(t, nil, http.StatusOK)
	gp := &reconcileFakeGP{managedYAML: []byte("clusters:\n- name: prod-eu\n  labels: {}\n")}
	srv, router := reconcileTestServer(t, gp, argo.URL)
	srv.SetReconcilerTrigger(func() {})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, reconcileOperatorReq("does-not-exist"))

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d (body=%s)", w.Code, w.Body.String())
	}
}

func TestHandleReconcileCluster_503_NoReconcilerWired(t *testing.T) {
	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": "prod-eu", "server": "https://prod-eu.example.com"},
	}, http.StatusOK)
	gp := &reconcileFakeGP{managedYAML: []byte("clusters:\n- name: prod-eu\n  labels: {}\n")}
	_, router := reconcileTestServer(t, gp, argo.URL)
	// Deliberately do NOT call SetReconcilerTrigger — simulates a
	// deployment mode where the cluster reconciler never got wired
	// (out-of-cluster, no credentials provider).

	w := httptest.NewRecorder()
	router.ServeHTTP(w, reconcileOperatorReq("prod-eu"))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when no reconciler is wired, got %d (body=%s)", w.Code, w.Body.String())
	}
}

func TestHandleReconcileCluster_403_ViewerRole(t *testing.T) {
	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": "prod-eu", "server": "https://prod-eu.example.com"},
	}, http.StatusOK)
	gp := &reconcileFakeGP{managedYAML: []byte("clusters:\n- name: prod-eu\n  labels: {}\n")}
	srv, router := reconcileTestServer(t, gp, argo.URL)
	srv.SetReconcilerTrigger(func() {})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/prod-eu/reconcile", nil)
	req.Header.Set("X-Sharko-User", "bob")
	req.Header.Set("X-Sharko-Role", "viewer")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for viewer role, got %d (body=%s)", w.Code, w.Body.String())
	}
}

// reconcileFakeVault is a minimal providers.ClusterCredentialsProvider so a
// real clusterreconciler.Reconciler can be driven end-to-end in
// TestHandleGetCluster_LastReconcile_ProjectedOntoReadModel below.
type reconcileFakeVault struct{}

func (reconcileFakeVault) GetCredentials(name string) (*providers.Kubeconfig, error) {
	return &providers.Kubeconfig{Server: "https://" + name + ".example.com", CAData: []byte("ca"), Token: "tk"}, nil
}
func (reconcileFakeVault) ListClusters() ([]providers.ClusterInfo, error) { return nil, nil }
func (reconcileFakeVault) SearchSecrets(_ string) ([]string, error)       { return nil, nil }
func (reconcileFakeVault) HealthCheck(_ context.Context) error            { return nil }

// TestHandleGetCluster_LastReconcile_ProjectedOntoReadModel — end-to-end
// through a real clusterreconciler.Reconciler: after a tick that reconciles
// "prod-eu", GET /clusters/prod-eu must include last_reconcile with
// outcome "succeeded". Exercises applyLastReconcile (clusters_reconcile.go)
// wired via handleGetCluster in clusters.go.
func TestHandleGetCluster_LastReconcile_ProjectedOntoReadModel(t *testing.T) {
	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": "prod-eu", "server": "https://prod-eu.example.com"},
	}, http.StatusOK)
	gp := &reconcileFakeGP{managedYAML: []byte("clusters:\n- name: prod-eu\n  labels: {}\n")}
	srv, router := reconcileTestServer(t, gp, argo.URL)

	recon := clusterreconciler.New(clusterreconciler.Deps{
		GitProvider:  func() gitprovider.GitProvider { return gp },
		ArgoClient:   fake.NewSimpleClientset(),
		Vault:        reconcileFakeVault{},
		AuditFn:      func(audit.Entry) {},
		TickInterval: time.Hour, // never auto-fires; the test drives it via Trigger
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	recon.Start(ctx)
	defer recon.Stop()
	recon.Trigger()

	// Wait for the triggered tick to record an outcome for prod-eu.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := recon.LastReconcile("prod-eu"); ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, ok := recon.LastReconcile("prod-eu"); !ok {
		t.Fatal("timed out waiting for the reconciler to record an outcome for prod-eu")
	}

	srv.SetClusterReconciler(recon)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/prod-eu", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}

	var resp struct {
		Cluster models.Cluster `json:"cluster"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Cluster.LastReconcile == nil {
		t.Fatal("expected last_reconcile to be populated on the cluster read model")
	}
	if resp.Cluster.LastReconcile.Outcome != string(clusterreconciler.OutcomeSucceeded) {
		t.Errorf("last_reconcile.outcome = %q, want %q", resp.Cluster.LastReconcile.Outcome, clusterreconciler.OutcomeSucceeded)
	}
	if resp.Cluster.LastReconcile.Time == "" {
		t.Error("expected last_reconcile.time to be set")
	}
}
