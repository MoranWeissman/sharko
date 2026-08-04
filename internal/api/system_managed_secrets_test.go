package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// GET /api/v1/system/managed-secrets — pinned contract:
//
//  1. Never 500s the System page: no active connection, no reconcilers
//     wired -> 200 with empty tables and both engines reporting wired=false.
//  2. A cluster with a real, in-sync reconcile record shows up in
//     cluster_connection_secrets with state "in_sync" and a real
//     last_checked timestamp; the engine section reports wired=true with
//     the configured interval.
//  3. A cluster+addon with a registered secret definition and the addon
//     enabled shows up in addon_values_secrets with the definition's
//     secret name/namespace.
//  4. An audit entry recording a successful connection-secret write joins
//     onto the matching row as last_repaired + a plain-English detail.
//  5. The addon-values engine section reflects s.secretReconciler's own
//     stats (wired, interval, last run, last error) without a 500 even when
//     no addon secret definitions are registered.

func TestHandleGetManagedSecrets_NoConnectionNoReconcilers_Degrades200(t *testing.T) {
	srv := newTestServer()
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/managed-secrets", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (never 500 the System page), got %d (body=%s)", w.Code, w.Body.String())
	}

	var body managedSecretsResponse
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.ClusterConnectionSecrets) != 0 {
		t.Errorf("expected no connection-secret rows with no active connection, got %d", len(body.ClusterConnectionSecrets))
	}
	if len(body.AddonValuesSecrets) != 0 {
		t.Errorf("expected no addon-values rows with no active connection, got %d", len(body.AddonValuesSecrets))
	}
	if body.Engines.ClusterConnection.Wired {
		t.Error("expected cluster_connection engine wired=false when no clusterRecon is set")
	}
	if body.Engines.AddonValues.Wired {
		t.Error("expected addon_values engine wired=false when no secretReconciler is set")
	}
}

func TestHandleGetManagedSecrets_ConnectionSecretRow_InSync(t *testing.T) {
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
		Namespace:    "argocd",
		TickInterval: 45 * time.Second,
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

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/managed-secrets", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var body managedSecretsResponse
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.ClusterConnectionSecrets) != 1 {
		t.Fatalf("expected exactly 1 connection-secret row, got %d (%+v)", len(body.ClusterConnectionSecrets), body.ClusterConnectionSecrets)
	}
	row := body.ClusterConnectionSecrets[0]
	if row.Cluster != "prod-eu" {
		t.Errorf("cluster = %q, want prod-eu", row.Cluster)
	}
	if row.State != "in_sync" {
		t.Errorf("state = %q, want in_sync", row.State)
	}
	if row.LastChecked == "" {
		t.Error("expected last_checked to be set")
	}
	if row.SecretName != "prod-eu" || row.SecretNamespace != "argocd" {
		t.Errorf("secret_name/secret_namespace = %q/%q, want prod-eu/argocd", row.SecretName, row.SecretNamespace)
	}

	if !body.Engines.ClusterConnection.Wired {
		t.Error("expected cluster_connection engine wired=true")
	}
	if body.Engines.ClusterConnection.IntervalSeconds != 45 {
		t.Errorf("interval_seconds = %d, want 45", body.Engines.ClusterConnection.IntervalSeconds)
	}
	if body.Engines.ClusterConnection.LastRun == "" {
		t.Error("expected cluster_connection engine last_run to be set after a completed tick")
	}
}

func TestHandleGetManagedSecrets_ConnectionSecretRow_RepairJoinsFromAudit(t *testing.T) {
	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": "prod-eu", "server": "https://prod-eu.example.com"},
	}, http.StatusOK)
	gp := &reconcileFakeGP{managedYAML: []byte("clusters:\n- name: prod-eu\n  labels: {}\n")}
	srv, router := reconcileTestServer(t, gp, argo.URL)

	srv.AuditLog().Add(audit.Entry{
		Event:    "cluster_secret_create",
		Resource: "cluster:prod-eu",
		Source:   "reconciler",
		Result:   "success",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/managed-secrets", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var body managedSecretsResponse
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.ClusterConnectionSecrets) != 1 {
		t.Fatalf("expected exactly 1 connection-secret row, got %d", len(body.ClusterConnectionSecrets))
	}
	row := body.ClusterConnectionSecrets[0]
	if row.LastRepaired == "" {
		t.Error("expected last_repaired to be populated from the matching audit entry")
	}
	if row.LastRepairedDetail != "secret created" {
		t.Errorf("last_repaired_detail = %q, want %q", row.LastRepairedDetail, "secret created")
	}
	// No reconciler wired in this test, so the row's own state must stay
	// honest ("unknown"), not be inferred from the repair join.
	if row.State != "unknown" {
		t.Errorf("state = %q, want unknown (no clusterRecon wired)", row.State)
	}
}

func TestHandleGetManagedSecrets_AddonValuesSecretRow(t *testing.T) {
	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": "prod-eu", "server": "https://prod-eu.example.com"},
	}, http.StatusOK)
	gp := &reconcileFakeGP{
		managedYAML: []byte("clusters:\n- name: prod-eu\n  labels:\n    datadog: enabled\n"),
	}
	srv, router := reconcileTestServer(t, gp, argo.URL)

	srv.SetAddonSecretDefs(map[string]orchestrator.AddonSecretDefinition{
		"datadog": {
			AddonName:  "datadog",
			SecretName: "datadog-secrets",
			Namespace:  "datadog",
			Keys:       map[string]string{"api-key": "secrets/datadog/api-key"},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/managed-secrets", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var body managedSecretsResponse
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.AddonValuesSecrets) != 1 {
		t.Fatalf("expected exactly 1 addon-values row, got %d (%+v)", len(body.AddonValuesSecrets), body.AddonValuesSecrets)
	}
	row := body.AddonValuesSecrets[0]
	if row.Cluster != "prod-eu" || row.Addon != "datadog" {
		t.Errorf("cluster/addon = %q/%q, want prod-eu/datadog", row.Cluster, row.Addon)
	}
	if row.SecretName != "datadog-secrets" || row.SecretNamespace != "datadog" {
		t.Errorf("secret_name/secret_namespace = %q/%q, want datadog-secrets/datadog", row.SecretName, row.SecretNamespace)
	}
}

func TestHandleGetManagedSecrets_AddonValuesEngine_WiredReportsStats(t *testing.T) {
	srv := newTestServer()
	router := NewRouter(srv, nil)

	rec := &fakeReconciler{stats: map[string]int{}}
	srv.SetSecretReconciler(rec)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/managed-secrets", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var body managedSecretsResponse
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.Engines.AddonValues.Wired {
		t.Error("expected addon_values engine wired=true once a secretReconciler is set")
	}
	// fakeReconciler's zero-value LastRunTime/LastError -> the engine has
	// never actually completed a run; that must render as empty, not a
	// fabricated timestamp.
	if body.Engines.AddonValues.LastRun != "" {
		t.Errorf("last_run = %q, want empty (reconciler never ran)", body.Engines.AddonValues.LastRun)
	}
}
