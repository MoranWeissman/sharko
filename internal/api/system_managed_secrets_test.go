package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

// TestHandleGetManagedSecrets_AddonValuesSecretRow_TimestampsFromReconcilerAndAudit
// pins S3: addon_values_secrets rows now carry real last_checked (from the
// addon-values reconciler's per-item state) and last_repaired (+ a canned
// detail string, joined from an audit entry) — the same honest-unknown
// contract connection-secret rows already have. A row with neither source
// available still comes back with both fields empty ("unknown" stays
// unknown), never a guessed value.
func TestHandleGetManagedSecrets_AddonValuesSecretRow_TimestampsFromReconcilerAndAudit(t *testing.T) {
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

	checkedAt := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	srv.SetSecretReconciler(&fakeReconciler{
		itemChecked: map[[2]string]time.Time{{"prod-eu", "datadog"}: checkedAt},
	})

	srv.AuditLog().Add(audit.Entry{
		Event:    "addon_secret_created",
		Resource: "cluster:prod-eu/addon:datadog",
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
	if len(body.AddonValuesSecrets) != 1 {
		t.Fatalf("expected exactly 1 addon-values row, got %d (%+v)", len(body.AddonValuesSecrets), body.AddonValuesSecrets)
	}
	row := body.AddonValuesSecrets[0]
	wantChecked := checkedAt.Format(time.RFC3339)
	if row.LastChecked != wantChecked {
		t.Errorf("last_checked = %q, want %q", row.LastChecked, wantChecked)
	}
	if row.LastRepaired == "" {
		t.Error("expected last_repaired to be populated from the matching audit entry")
	}
	if row.LastRepairedDetail != "secret created" {
		t.Errorf("last_repaired_detail = %q, want %q", row.LastRepairedDetail, "secret created")
	}
}

// TestHandleGetManagedSecrets_AddonValuesSecretRow_UnknownStaysUnknown pins
// the honest-unknown half of S3: with no secretReconciler wired and no
// matching audit entry, last_checked/last_repaired/last_repaired_detail
// stay empty — never a fabricated timestamp.
func TestHandleGetManagedSecrets_AddonValuesSecretRow_UnknownStaysUnknown(t *testing.T) {
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
	if row.LastChecked != "" {
		t.Errorf("last_checked = %q, want empty (no secretReconciler wired)", row.LastChecked)
	}
	if row.LastRepaired != "" || row.LastRepairedDetail != "" {
		t.Errorf("last_repaired/detail = %q/%q, want empty (no matching audit entry)", row.LastRepaired, row.LastRepairedDetail)
	}
}

// TestHandleGetManagedSecrets_AddonValuesSecretRow_LastCheckErrorIsCanned
// pins S8: a per-item error recorded by the reconciler surfaces on the row
// as a safe, pre-written sentence — NEVER the reconciler's raw error text,
// which could in principle carry a fragment of a secret value (the
// provider-fetch error case is wrapped verbatim by
// internal/secrets.Reconciler).
func TestHandleGetManagedSecrets_AddonValuesSecretRow_LastCheckErrorIsCanned(t *testing.T) {
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

	rawErr := `fetching "secrets/datadog/api-key" from provider: dial tcp: connection refused`
	srv.SetSecretReconciler(&fakeReconciler{
		itemError: map[[2]string]string{{"prod-eu", "datadog"}: rawErr},
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
		t.Fatalf("expected exactly 1 addon-values row, got %d", len(body.AddonValuesSecrets))
	}
	row := body.AddonValuesSecrets[0]
	want := "Sharko couldn't fetch this secret's value from the vault."
	if row.LastCheckError != want {
		t.Errorf("last_check_error = %q, want %q", row.LastCheckError, want)
	}
	if strings.Contains(row.LastCheckError, "dial tcp") || strings.Contains(row.LastCheckError, "secrets/datadog/api-key") {
		t.Errorf("last_check_error leaked the raw provider error text: %q", row.LastCheckError)
	}
}

// TestAddonValuesSecretCheckFailureSentence_NeverEchoesRawText is a
// table-driven pin on the mapping function itself (S8): every known
// failure stage maps to its own fixed sentence, an unrecognized stage
// falls back to a generic sentence rather than the raw text, and the
// output NEVER contains the substrings a value-fetch error could plausibly
// carry (the provider path, or something that looks like leaked content).
func TestAddonValuesSecretCheckFailureSentence_NeverEchoesRawText(t *testing.T) {
	cases := []struct {
		name   string
		errMsg string
		want   string
	}{
		{"empty", "", ""},
		{
			"missing catalog field",
			`the secret definition in the catalog has no secret name — fill that in and Sharko can push it`,
			"The secret definition in the catalog is incomplete — fill in the missing fields.",
		},
		{
			"credentials",
			`getting credentials: assume role denied`,
			"Sharko couldn't get credentials for this cluster.",
		},
		{
			"connecting",
			`connecting to cluster: dial tcp 10.0.0.1:443: i/o timeout`,
			"Sharko couldn't connect to this cluster.",
		},
		{
			"provider fetch — the exact near-miss shape",
			`fetching "secrets/datadog/api-key" from provider: secret value was "sk_live_abc123..."`,
			"Sharko couldn't fetch this secret's value from the vault.",
		},
		{
			"existing secret read",
			`checking existing secret: etcdserver: request timed out`,
			"Sharko couldn't read the existing secret on this cluster.",
		},
		{
			"create",
			`creating secret: secrets "datadog-secrets" already exists`,
			"Sharko couldn't create this secret on the cluster.",
		},
		{
			"update",
			`updating secret: Operation cannot be fulfilled`,
			"Sharko couldn't update this secret on the cluster.",
		},
		{
			"unrecognized stage — safe fallback, not the raw string",
			`something entirely unexpected: value=hunter2`,
			"The last check didn't finish.",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := addonValuesSecretCheckFailureSentence(tc.errMsg)
			if got != tc.want {
				t.Errorf("addonValuesSecretCheckFailureSentence(%q) = %q, want %q", tc.errMsg, got, tc.want)
			}
			if tc.errMsg != "" && got == tc.errMsg {
				t.Errorf("mapped sentence equals the raw error text — S8 requires a canned sentence, never a passthrough")
			}
		})
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
