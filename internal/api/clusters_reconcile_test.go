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
	observabilitySvc := service.NewObservabilityService(clusterSvc)
	upgradeSvc := service.NewUpgradeService(ai.NewClient(ai.Config{}), nil, "")
	srv := withLegacyOpenAuthForTests(NewServer(connSvc, clusterSvc, addonSvc, dashboardSvc, observabilitySvc, upgradeSvc, ai.NewClient(ai.Config{})))

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

// TestHandleReconcileCluster_202_TriggersCheckOnlyPass pins the P1-A A2
// contract: the endpoint every "Refresh" button in the UI reaches must fire
// the READ-ONLY check, and must NOT fire the write nudge. Wiring both and
// asserting one moved is the point — a regression that swaps them back is
// exactly the bug this lane fixed.
func TestHandleReconcileCluster_202_TriggersCheckOnlyPass(t *testing.T) {
	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": "prod-eu", "server": "https://prod-eu.example.com"},
	}, http.StatusOK)
	gp := &reconcileFakeGP{managedYAML: []byte("clusters:\n- name: prod-eu\n  labels: {}\n")}
	srv, router := reconcileTestServer(t, gp, argo.URL)

	wrote, checked := 0, 0
	srv.SetReconcilerTrigger(func() { wrote++ })
	srv.SetReconcilerCheckTrigger(func() { checked++ })

	w := httptest.NewRecorder()
	router.ServeHTTP(w, reconcileOperatorReq("prod-eu"))

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d (body=%s)", w.Code, w.Body.String())
	}
	if checked != 1 {
		t.Fatalf("expected the read-only check trigger to fire exactly once, got %d", checked)
	}
	if wrote != 0 {
		t.Fatalf("a button labelled Refresh must never fire the write pass — write trigger fired %d time(s)", wrote)
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["status"] != "accepted" {
		t.Errorf(`status = %q, want "accepted"`, body["status"])
	}
	// The message must not overclaim per-cluster scoping (the pass covers
	// every cluster), and must say plainly that nothing is written.
	wantMsg := `checking every cluster's connection secret now, "prod-eu" included — nothing is written`
	if body["message"] != wantMsg {
		t.Errorf("message = %q, want %q", body["message"], wantMsg)
	}
}

// TestHandleReconcileCluster_AuditEntryStatesTheBlastRadius pins P3-F1 on
// the connection side. This endpoint takes ONE cluster name in its path
// but starts a pass over every cluster, and the audit entry used to say
// "every cluster's connection secret" and stop there — true, and useless
// to anyone trying to work out afterwards what a click actually touched.
func TestHandleReconcileCluster_AuditEntryStatesTheBlastRadius(t *testing.T) {
	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": "prod-eu", "server": "https://prod-eu.example.com"},
	}, http.StatusOK)
	gp := &reconcileFakeGP{managedYAML: []byte("clusters:\n- name: prod-eu\n  labels: {}\n")}
	srv, router := reconcileTestServer(t, gp, argo.URL)
	srv.SetReconcilerCheckTrigger(func() {})

	// Give the reconciler three real per-cluster records — that map IS
	// what a check pass covers, so 3 is the honest number here.
	recon := clusterreconciler.New(clusterreconciler.Deps{
		ArgoClient: fake.NewSimpleClientset(),
		Namespace:  "argocd",
		AuditFn:    func(audit.Entry) {},
	})
	for _, name := range []string{"prod-eu", "staging-us", "spoke-asia"} {
		recon.SeedReconcileRecordForDemo(name, clusterreconciler.OutcomeSucceeded, "", time.Now(), nil)
	}
	srv.SetClusterReconciler(recon)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, reconcileOperatorReq("prod-eu"))
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d (body=%s)", w.Code, w.Body.String())
	}

	var entry audit.Entry
	for _, e := range srv.AuditLog().List(0) {
		if e.Event == "cluster_connection_secret_check_triggered" {
			entry = e
			break
		}
	}
	if entry.Event == "" {
		t.Fatalf("no check-triggered audit entry; got %+v", srv.AuditLog().List(0))
	}
	if entry.Resource != "cluster:prod-eu" {
		t.Errorf("resource = %q, want cluster:prod-eu (the cluster the click was on)", entry.Resource)
	}
	want := clusterCheckBlastRadius(3)
	if entry.Detail != want {
		t.Errorf("detail = %q, want %q — the entry must say how many clusters the check covered", entry.Detail, want)
	}
}

// TestHandleReconcileCluster_503_NoReconcilerWired_SkipsGitAndArgoCDRoundTrips
// pins the L2 handler-order fix: the cheap "is a reconciler wired" 503
// check must run BEFORE the Git/ArgoCD round-trips, so a server with no
// reconciler wired never touches the Git provider or ArgoCD client at all
// — even for a cluster that doesn't exist.
func TestHandleReconcileCluster_503_NoReconcilerWired_SkipsGitAndArgoCDRoundTrips(t *testing.T) {
	gp := &reconcileFakeGP{}                                      // no managedYAML set — GetFileContent always errors if called
	_, router := reconcileTestServer(t, gp, "http://127.0.0.1:1") // unreachable ArgoCD URL
	// Deliberately do NOT call SetReconcilerCheckTrigger.

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
	srv.SetReconcilerCheckTrigger(func() {})

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
	// Deliberately do NOT call SetReconcilerCheckTrigger — simulates a
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
	srv.SetReconcilerCheckTrigger(func() {})

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
		Vault:        staticVault(reconcileFakeVault{}),
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

// R2-2 — applyLastReconcile / lastReconcileMessage tests. Product owner's
// rule, verbatim (2026-08-14): "Failed → safe mapped failure sentence.
// Skipped → its fixed explanatory sentence. Succeeded → empty or the
// genuine success/fight message, never a failure sentence."
//
// Each test below pins the WHOLE sentence against a `want` constant, never
// a substring and never a non-empty check — the exact gap that let a wrong
// wording survive four review rounds earlier in this epic (see S4-3).

// TestLastReconcileMessage_Failed_MapsThroughFailureSentence pins the
// failed→mapped projection. Wording rule set by the product owner
// 2026-08-14.
func TestLastReconcileMessage_Failed_MapsThroughFailureSentence(t *testing.T) {
	rec := clusterreconciler.ClusterReconcileRecord{
		Outcome: clusterreconciler.OutcomeFailed,
		Message: "Sharko couldn't converge git-desired addon labels on this drifted managed-cluster Secret: Update \"...\": conflict",
	}
	want := "Sharko tried to fix this cluster's connection secret and the write failed. Click Refresh to try again."
	got := lastReconcileMessage(rec)
	if got != want {
		t.Errorf("lastReconcileMessage(failed) = %q, want %q", got, want)
	}
}

// TestLastReconcileMessage_Skipped_UntouchedFixedSentence pins the
// skipped→untouched projection using a real sentinel:
// ManagedSecretNotCreatedMessage. Wording rule set by the product owner
// 2026-08-14.
func TestLastReconcileMessage_Skipped_UntouchedFixedSentence(t *testing.T) {
	rec := clusterreconciler.ClusterReconcileRecord{
		Outcome: clusterreconciler.OutcomeSkipped,
		Message: clusterreconciler.ManagedSecretNotCreatedMessage,
	}
	want := "This cluster's ArgoCD secret has not been created yet, so there is nothing to sync onto."
	got := lastReconcileMessage(rec)
	if got != want {
		t.Errorf("lastReconcileMessage(skipped) = %q, want %q", got, want)
	}
	if got != clusterreconciler.ManagedSecretNotCreatedMessage {
		t.Errorf("lastReconcileMessage(skipped) = %q, want it to equal ManagedSecretNotCreatedMessage exactly (%q) — a skipped record's fixed sentence must arrive untouched", got, clusterreconciler.ManagedSecretNotCreatedMessage)
	}
}

// TestLastReconcileMessage_Succeeded_UntouchedGenuineSentence pins the
// succeeded→untouched projection using reconciler.go:1694's real drift-
// corrected sentence. Wording rule set by the product owner 2026-08-14.
func TestLastReconcileMessage_Succeeded_UntouchedGenuineSentence(t *testing.T) {
	const wantSentence = "drift corrected — git-desired addon labels converged"
	rec := clusterreconciler.ClusterReconcileRecord{
		Outcome: clusterreconciler.OutcomeSucceeded,
		Message: wantSentence,
	}
	got := lastReconcileMessage(rec)
	if got != wantSentence {
		t.Errorf("lastReconcileMessage(succeeded) = %q, want %q", got, wantSentence)
	}
}

// TestLastReconcileMessage_SucceededEmpty_StaysEmpty pins the fourth
// pinned projection: a succeeded record with an empty message (e.g.
// reconciler.go:2315's bare secret-create success) stays empty — it must
// NOT be upgraded to any sentence, mapped or otherwise. Wording rule set by
// the product owner 2026-08-14.
func TestLastReconcileMessage_SucceededEmpty_StaysEmpty(t *testing.T) {
	rec := clusterreconciler.ClusterReconcileRecord{
		Outcome: clusterreconciler.OutcomeSucceeded,
		Message: "",
	}
	got := lastReconcileMessage(rec)
	if got != "" {
		t.Errorf("lastReconcileMessage(succeeded, empty) = %q, want empty", got)
	}
}

// TestLastReconcileMessage_NeverPairsSucceededWithFailureDefault bans the
// contradiction by name (acceptance criterion 2): no projection may ever
// pair outcome succeeded with clusterreconciler.DefaultFailureSentence —
// asserted against the named constant, not a copied string. Before R2-2
// this exact pairing was the bug: applyLastReconcile mapped every outcome
// through FailureSentence unconditionally, so ANY succeeded record with a
// non-empty message that FailureSentence's switch didn't recognize (which
// is every genuine product sentence — none of them contain FailureSentence's
// fixed-prefix keywords) fell through to the generic default and got
// printed on a healthy cluster's page.
func TestLastReconcileMessage_NeverPairsSucceededWithFailureDefault(t *testing.T) {
	// A representative sample of genuine succeeded messages this package
	// actually records (see the R2-2 call-site audit) plus the drift-fight
	// warning, which carries interpolated content but is still a genuine
	// product sentence, never error text.
	succeededMessages := []string{
		"",
		"cluster Secret present; labels verified",
		"cluster Secret present",
		"drift corrected — git-desired addon labels converged",
		"orphaned Secret already removed",
		"orphaned Secret removed",
		"something else keeps overwriting Sharko's addon labels on this cluster's self-managed ArgoCD secret (reverted 3 checks in a row) — likely the ArgoCD application that renders this secret from Git fighting with Sharko over it. Sharko will keep re-applying its labels every tick; see https://sharko.readthedocs.io/en/latest/operator/self-managed-connections/.",
	}
	for _, msg := range succeededMessages {
		rec := clusterreconciler.ClusterReconcileRecord{
			Outcome: clusterreconciler.OutcomeSucceeded,
			Message: msg,
		}
		got := lastReconcileMessage(rec)
		if got == clusterreconciler.DefaultFailureSentence {
			t.Errorf("lastReconcileMessage(succeeded, message=%q) = the failure-default sentence %q — a succeeded record must never say the last check didn't finish", msg, clusterreconciler.DefaultFailureSentence)
		}
	}
}

// TestHandleGetCluster_ManagedSecretFields_ProjectedOntoReadModel — walk day
// 4 locks, S1 + S2. Same real-reconciler wiring as the LastReconcile test
// above: after a tick that creates prod-eu's ArgoCD cluster Secret,
// GET /clusters/prod-eu must report the real secret's "namespace/name" and
// that it is already Sharko-managed — applyManagedSecretFields
// (clusters_reconcile.go) wired via handleGetCluster in clusters.go.
func TestHandleGetCluster_ManagedSecretFields_ProjectedOntoReadModel(t *testing.T) {
	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": "prod-eu", "server": "https://prod-eu.example.com"},
	}, http.StatusOK)
	gp := &reconcileFakeGP{managedYAML: []byte("clusters:\n- name: prod-eu\n  labels: {}\n")}
	srv, router := reconcileTestServer(t, gp, argo.URL)

	recon := clusterreconciler.New(clusterreconciler.Deps{
		GitProvider:  func() gitprovider.GitProvider { return gp },
		ArgoClient:   fake.NewSimpleClientset(),
		Vault:        staticVault(reconcileFakeVault{}),
		AuditFn:      func(audit.Entry) {},
		TickInterval: time.Hour, // never auto-fires; the test drives it via Trigger
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	recon.Start(ctx)
	defer recon.Stop()
	recon.Trigger()

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
	if resp.Cluster.ManagedSecretName != "argocd/prod-eu" {
		t.Errorf("managed_secret_name = %q, want %q", resp.Cluster.ManagedSecretName, "argocd/prod-eu")
	}
	if !resp.Cluster.AlreadyManagedBySharko {
		t.Error("expected already_managed_by_sharko to be true once the reconciler has created and labelled the Secret")
	}
}
