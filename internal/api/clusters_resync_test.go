package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/settings"
)

// clusters_resync_test.go — POST /api/v1/clusters/{name}/resync (v4-8-5).
//
// Pinned contract:
//  1. 200 on a known, drifted, git-managed cluster; labels converge to
//     match git and the response reports the diff, regardless of
//     managed_cluster_self_heal.
//  2. The managed_cluster_self_heal setting is read BEFORE and AFTER the
//     call and must be byte-identical — resync never touches it.
//  3. 404 when the cluster is unknown.
//  4. 403 for a viewer — this is an operator+ action.
//  5. 503 when no cluster reconciler is wired.

// resyncOperatorReq builds an authenticated operator request for the
// resync route — the handler requires authz.Require("cluster.resync"),
// which resolves to RoleOperator.
func resyncOperatorReq(name string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/"+name+"/resync", nil)
	req.Header.Set("X-Sharko-User", "op")
	req.Header.Set("X-Sharko-Role", "operator")
	return req
}

func TestHandleResyncCluster_200_CorrectsDriftRegardlessOfSelfHeal(t *testing.T) {
	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": "prod-eu", "server": "https://prod-eu.example.com"},
	}, http.StatusOK)
	gp := &reconcileFakeGP{managedYAML: []byte("clusters:\n- name: prod-eu\n  labels:\n    addon-foo: enabled\n")}
	srv, router := reconcileTestServer(t, gp, argo.URL)

	// Seed the drifted ArgoCD cluster Secret directly — this is the live
	// state the reconciler's ArgoClient reads/writes; the addon-foo label
	// git wants is missing (drift), and the setting starts OFF.
	argoClient := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "prod-eu",
			Namespace: "argocd",
			Labels: map[string]string{
				clusterreconciler.LabelManagedBy: clusterreconciler.LabelValueSharko,
				"argocd.argoproj.io/secret-type": "cluster",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"name":   []byte("prod-eu"),
			"server": []byte("https://prod-eu.example.com"),
			"config": []byte(`{"execProviderConfig":{}}`),
		},
	})

	// Real settings.Store backed by a fake k8s client — the same object
	// production wires SelfHealFn from (cmd/sharko/serve.go). Self-heal
	// starts OFF (the default) — the whole point of this story is that
	// resync works anyway.
	settingsClient := fake.NewSimpleClientset()
	settingsStore := settings.NewStore(settingsClient, "sharko")

	recon := clusterreconciler.New(clusterreconciler.Deps{
		GitProvider:  func() gitprovider.GitProvider { return gp },
		ArgoClient:   argoClient,
		Vault:        reconcileFakeVault{},
		AuditFn:      func(audit.Entry) {},
		Namespace:    "argocd",
		TickInterval: time.Hour, // never auto-fires
		SelfHealFn:   settingsStore.IsManagedClusterSelfHealEnabled,
	})
	srv.SetClusterReconciler(recon)

	before, err := settingsStore.GetManagedClusterSelfHeal(context.Background())
	if err != nil {
		t.Fatalf("read self-heal setting before resync: %v", err)
	}
	if before != false {
		t.Fatalf("expected self-heal to default to OFF, got %v", before)
	}

	w := httptest.NewRecorder()
	router.ServeHTTP(w, resyncOperatorReq("prod-eu"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}

	var resp models.ClusterResyncResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Outcome != string(clusterreconciler.OutcomeSucceeded) {
		t.Errorf("outcome = %q, want %q (message=%q)", resp.Outcome, clusterreconciler.OutcomeSucceeded, resp.Message)
	}
	if len(resp.LabelDiff.Added) != 1 || resp.LabelDiff.Added[0] != "addon-foo" {
		t.Errorf("expected label_diff.added=['addon-foo'], got %v", resp.LabelDiff.Added)
	}

	// The cluster Secret's labels now match git.
	secret, err := argoClient.CoreV1().Secrets("argocd").Get(context.Background(), "prod-eu", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get cluster secret: %v", err)
	}
	if secret.Labels["addon-foo"] != "enabled" {
		t.Errorf("addon-foo should be 'enabled' on the live secret after resync, got %q", secret.Labels["addon-foo"])
	}

	// The self-heal setting is unchanged.
	after, err := settingsStore.GetManagedClusterSelfHeal(context.Background())
	if err != nil {
		t.Fatalf("read self-heal setting after resync: %v", err)
	}
	if after != before {
		t.Fatalf("managed_cluster_self_heal changed from %v to %v — resync must never touch it", before, after)
	}
}

func TestHandleResyncCluster_404_UnknownCluster(t *testing.T) {
	argo := newStubArgoSrv(t, nil, http.StatusOK)
	gp := &reconcileFakeGP{managedYAML: []byte("clusters:\n- name: prod-eu\n  labels: {}\n")}
	srv, router := reconcileTestServer(t, gp, argo.URL)

	recon := clusterreconciler.New(clusterreconciler.Deps{
		GitProvider: func() gitprovider.GitProvider { return gp },
		ArgoClient:  fake.NewSimpleClientset(),
		Vault:       reconcileFakeVault{},
		AuditFn:     func(audit.Entry) {},
	})
	srv.SetClusterReconciler(recon)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, resyncOperatorReq("does-not-exist"))

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d (body=%s)", w.Code, w.Body.String())
	}
}

func TestHandleResyncCluster_403_ViewerRole(t *testing.T) {
	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": "prod-eu", "server": "https://prod-eu.example.com"},
	}, http.StatusOK)
	gp := &reconcileFakeGP{managedYAML: []byte("clusters:\n- name: prod-eu\n  labels: {}\n")}
	srv, router := reconcileTestServer(t, gp, argo.URL)

	recon := clusterreconciler.New(clusterreconciler.Deps{
		GitProvider: func() gitprovider.GitProvider { return gp },
		ArgoClient:  fake.NewSimpleClientset(),
		Vault:       reconcileFakeVault{},
		AuditFn:     func(audit.Entry) {},
	})
	srv.SetClusterReconciler(recon)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/prod-eu/resync", nil)
	req.Header.Set("X-Sharko-User", "bob")
	req.Header.Set("X-Sharko-Role", "viewer")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for viewer role, got %d (body=%s)", w.Code, w.Body.String())
	}
}

func TestHandleResyncCluster_503_NoReconcilerWired(t *testing.T) {
	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": "prod-eu", "server": "https://prod-eu.example.com"},
	}, http.StatusOK)
	gp := &reconcileFakeGP{managedYAML: []byte("clusters:\n- name: prod-eu\n  labels: {}\n")}
	_, router := reconcileTestServer(t, gp, argo.URL)
	// Deliberately do NOT call SetClusterReconciler.

	w := httptest.NewRecorder()
	router.ServeHTTP(w, resyncOperatorReq("prod-eu"))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when no reconciler is wired, got %d (body=%s)", w.Code, w.Body.String())
	}
}

// Note: the "cluster known to Sharko but has no entry in the git-managed
// list" 400 branch (ErrClusterNotManaged) is exercised at the
// clusterreconciler unit level — see
// TestResyncClusterLabels_UnknownCluster_ReturnsErrClusterNotManaged in
// internal/clusterreconciler/resync_test.go. It is not reachable through
// this HTTP handler in practice: handleResyncCluster's own 404 existence
// gate (GetClusterDetail) reads the SAME git-managed-clusters source the
// reconciler does, so a cluster absent from git already 404s before
// ResyncClusterLabels is ever called.
