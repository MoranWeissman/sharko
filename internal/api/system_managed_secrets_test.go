package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// erroringVault is a providers.ClusterCredentialsProvider whose
// GetCredentials always fails — used to drive a real
// clusterreconciler.Reconciler tick into an OutcomeFailed record so the
// managed-secrets endpoint's cluster-connection engine has a genuine
// last_error to surface (TestHandleGetManagedSecrets_ClusterConnectionEngine_LastErrorNamesClusterAndTime).
type erroringVault struct{}

func (erroringVault) GetCredentials(name string) (*providers.Kubeconfig, error) {
	return nil, errors.New("dial tcp: connection refused")
}
func (erroringVault) ListClusters() ([]providers.ClusterInfo, error) { return nil, nil }
func (erroringVault) SearchSecrets(_ string) ([]string, error)       { return nil, nil }
func (erroringVault) HealthCheck(_ context.Context) error            { return nil }

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

// TestConnectionSecretState_FailedOutcomeIsUnknownNotOutOfSync pins P1-B B1
// / finding #120: a FAILED check must never wear "out_of_sync"'s badge —
// out_of_sync claims Sharko compared and found a real mismatch, and a
// failed check means Sharko does not know. Table-driven over every branch
// connectionSecretState's doc comment describes.
func TestConnectionSecretState_FailedOutcomeIsUnknownNotOutOfSync(t *testing.T) {
	cases := []struct {
		name string
		rec  *models.ClusterLastReconcile
		want string
	}{
		{"nil record", nil, "unknown"},
		{
			"self-managed secret not created yet -> missing",
			&models.ClusterLastReconcile{
				Outcome: string(clusterreconciler.OutcomeSkipped),
				Message: clusterreconciler.SelfManagedSecretNotCreatedMessage,
			},
			"missing",
		},
		{
			"succeeded, no drift -> in_sync",
			&models.ClusterLastReconcile{Outcome: string(clusterreconciler.OutcomeSucceeded)},
			"in_sync",
		},
		{
			"succeeded WITH drift -> out_of_sync (a real comparison found a mismatch)",
			&models.ClusterLastReconcile{
				Outcome:    string(clusterreconciler.OutcomeSucceeded),
				LabelDrift: &models.ClusterLastReconcileLabelDrift{Changed: []string{"datadog"}},
			},
			"out_of_sync",
		},
		{
			"failed -> unknown, NEVER out_of_sync (P1-B B1 / #120)",
			&models.ClusterLastReconcile{
				Outcome: string(clusterreconciler.OutcomeFailed),
				Message: "reconciler pass aborted: git read failed: dial tcp: i/o timeout",
			},
			"unknown",
		},
		{
			"other skipped reason -> unknown",
			&models.ClusterLastReconcile{
				Outcome: string(clusterreconciler.OutcomeSkipped),
				Message: clusterreconciler.UnlabeledSecretExistsMessage,
			},
			"unknown",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := connectionSecretState(tc.rec)
			if got != tc.want {
				t.Errorf("connectionSecretState(%+v) state = %q, want %q", tc.rec, got, tc.want)
			}
		})
	}
}

// TestConnectionSecretCheckError_OnlyFailedRecordsCarryASentence pins the
// second half of the two-facts shape (P1-B B1): last_check_error is set
// ONLY when Outcome is Failed, and it is always the FailureSentence
// mapping of the record's Message — never the raw text.
func TestConnectionSecretCheckError_OnlyFailedRecordsCarryASentence(t *testing.T) {
	if got := connectionSecretCheckError(nil); got != "" {
		t.Errorf("nil record: last_check_error = %q, want empty", got)
	}
	succeeded := &models.ClusterLastReconcile{Outcome: string(clusterreconciler.OutcomeSucceeded)}
	if got := connectionSecretCheckError(succeeded); got != "" {
		t.Errorf("succeeded record: last_check_error = %q, want empty", got)
	}
	skipped := &models.ClusterLastReconcile{
		Outcome: string(clusterreconciler.OutcomeSkipped),
		Message: clusterreconciler.SelfManagedSecretNotCreatedMessage,
	}
	if got := connectionSecretCheckError(skipped); got != "" {
		t.Errorf("skipped record: last_check_error = %q, want empty", got)
	}

	raw := "reconciler pass aborted: git read failed: dial tcp: i/o timeout"
	failed := &models.ClusterLastReconcile{Outcome: string(clusterreconciler.OutcomeFailed), Message: raw}
	got := connectionSecretCheckError(failed)
	if got == "" {
		t.Fatal("failed record: last_check_error is empty, want the mapped sentence")
	}
	if got == raw {
		t.Errorf("last_check_error is the raw record text: %q", got)
	}
	if want := clusterreconciler.FailureSentence(raw); got != want {
		t.Errorf("last_check_error = %q, want the FailureSentence mapping %q", got, want)
	}
	if strings.Contains(got, "dial tcp") {
		t.Errorf("last_check_error leaked the raw error text: %q", got)
	}
}

// TestHandleGetManagedSecrets_ConnectionSecretRow_FailedCheckIsUnknownWithSentence
// takes the same fact all the way through the endpoint, with a REAL
// clusterreconciler.Reconciler driven into a genuine Failed outcome — not
// just the pure function above.
func TestHandleGetManagedSecrets_ConnectionSecretRow_FailedCheckIsUnknownWithSentence(t *testing.T) {
	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": "prod-eu", "server": "https://prod-eu.example.com"},
	}, http.StatusOK)
	gp := &reconcileFakeGP{
		managedYAML: []byte("clusters:\n- name: prod-eu\n  labels: {}\n"),
	}
	srv, router := reconcileTestServer(t, gp, argo.URL)

	recon := clusterreconciler.New(clusterreconciler.Deps{
		GitProvider: func() gitprovider.GitProvider { return gp },
		ArgoClient:  fake.NewSimpleClientset(),
		Vault:       erroringVault{},
		AuditFn:     func(audit.Entry) {},
		Namespace:   "argocd",
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	recon.Start(ctx)
	defer recon.Stop()
	recon.Trigger()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if rec, ok := recon.LastReconcile("prod-eu"); ok && rec.Outcome == clusterreconciler.OutcomeFailed {
			break
		}
		time.Sleep(10 * time.Millisecond)
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
		t.Fatalf("expected exactly 1 connection-secret row, got %d", len(body.ClusterConnectionSecrets))
	}
	row := body.ClusterConnectionSecrets[0]
	if row.State != "unknown" {
		t.Errorf("state = %q, want unknown — a failed check must never wear out_of_sync's badge", row.State)
	}
	if row.LastCheckError == "" {
		t.Fatal("last_check_error is empty, want the canned failure sentence")
	}
	if strings.Contains(row.LastCheckError, "dial tcp") || strings.Contains(row.LastCheckError, "connection refused") {
		t.Errorf("last_check_error leaked the raw vault error text: %q", row.LastCheckError)
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

// TestHandleGetManagedSecrets_SourceIsOnEveryRow pins S1: the backend a
// secret's content comes from is a PER-ROW fact, not one label for the
// whole page. A reader grouping, filtering or sorting the list by backend
// is reading row.Source, so it has to be there — on both kinds of row,
// with the honest answer for each: a connection secret follows git, a
// values secret follows the real backend name the server resolved.
func TestHandleGetManagedSecrets_SourceIsOnEveryRow(t *testing.T) {
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

	var body managedSecretsResponse
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(body.ClusterConnectionSecrets) == 0 {
		t.Fatal("expected at least one connection-secret row")
	}
	for _, row := range body.ClusterConnectionSecrets {
		if row.Source != "git" {
			t.Errorf("connection row %q source = %q, want %q", row.Cluster, row.Source, "git")
		}
	}

	if len(body.AddonValuesSecrets) == 0 {
		t.Fatal("expected at least one addon-values row")
	}
	for _, row := range body.AddonValuesSecrets {
		if row.Source == "" {
			t.Errorf("addon-values row %s/%s has an empty source — every row must say what it follows", row.Cluster, row.Addon)
		}
		if row.Source != body.AddonValuesSecretSource {
			t.Errorf("addon-values row source = %q, want the resolved backend name %q", row.Source, body.AddonValuesSecretSource)
		}
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

// TestAddonValuesSecretRowState_Foreign pins P1-A: the reconciler's
// "foreign" outcome gets its own row state. Collapsing it into
// out_of_sync would tell a reader to press Sync on a secret Sharko must
// never write; collapsing it into unknown would claim Sharko never looked,
// when it looked and knows exactly what it found.
func TestAddonValuesSecretRowState_Foreign(t *testing.T) {
	rec := &fakeReconciler{
		itemOutcome: map[[2]string]string{{"prod-eu", "eso"}: "foreign"},
	}
	if got := addonValuesSecretRowState(rec, "prod-eu", "eso"); got != "foreign" {
		t.Errorf("state = %q, want %q", got, "foreign")
	}
}

// TestAddonValuesSecretRowState_ErrorOutcomeIsUnknownNotOutOfSync is the
// values-row symmetry fix for the SAME #120 bug this lane already fixed on
// the connection side (TestConnectionSecretState_FailedOutcomeIsUnknownNotOutOfSync):
// "error" (a check or push that itself failed) must never wear
// "out_of_sync"'s badge — out_of_sync claims a real comparison found a
// mismatch, and a failed check means Sharko does not know. Table-driven
// over every branch addonValuesSecretRowState's doc comment describes.
func TestAddonValuesSecretRowState_ErrorOutcomeIsUnknownNotOutOfSync(t *testing.T) {
	cases := []struct {
		name  string
		recon SecretReconciler
		want  string
	}{
		{"nil reconciler", nil, "unknown"},
		{"never checked", &fakeReconciler{}, "unknown"},
		{
			"unchanged -> in_sync",
			&fakeReconciler{itemOutcome: map[[2]string]string{{"prod-eu", "eso"}: "unchanged"}},
			"in_sync",
		},
		{
			"out_of_sync -> out_of_sync (a REAL comparison found a mismatch)",
			&fakeReconciler{itemOutcome: map[[2]string]string{{"prod-eu", "eso"}: "out_of_sync"}},
			"out_of_sync",
		},
		{
			"error -> unknown, NEVER out_of_sync (P1-B symmetry fix / #120)",
			&fakeReconciler{itemOutcome: map[[2]string]string{{"prod-eu", "eso"}: "error"}},
			"unknown",
		},
		{
			"missing -> missing",
			&fakeReconciler{itemOutcome: map[[2]string]string{{"prod-eu", "eso"}: "missing"}},
			"missing",
		},
		{
			"foreign -> foreign (not unknown, not out_of_sync)",
			&fakeReconciler{itemOutcome: map[[2]string]string{{"prod-eu", "eso"}: "foreign"}},
			"foreign",
		},
		{
			"skipped -> unknown",
			&fakeReconciler{itemOutcome: map[[2]string]string{{"prod-eu", "eso"}: "skipped"}},
			"unknown",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := addonValuesSecretRowState(tc.recon, "prod-eu", "eso")
			if got != tc.want {
				t.Errorf("addonValuesSecretRowState() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestHandleGetManagedSecrets_AddonValuesSecretRow_ForeignSurfacesOnTheRow
// takes the same fact all the way through the endpoint.
func TestHandleGetManagedSecrets_AddonValuesSecretRow_ForeignSurfacesOnTheRow(t *testing.T) {
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
	srv.SetSecretReconciler(&fakeReconciler{
		itemOutcome: map[[2]string]string{{"prod-eu", "datadog"}: "foreign"},
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
	if got := body.AddonValuesSecrets[0].State; got != "foreign" {
		t.Errorf("state = %q, want %q", got, "foreign")
	}
	// A boundary is not a failed check — the row must carry no error text.
	if got := body.AddonValuesSecrets[0].LastCheckError; got != "" {
		t.Errorf("last_check_error = %q, want empty", got)
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

// TestHandleGetManagedSecrets_ClusterConnectionEngine_LastErrorNamesClusterAndTime
// pins the fix behind the managed-secrets page's red engine error: the
// response now names WHICH cluster the error is about and WHEN it happened,
// not just a bare message with no subject.
func TestHandleGetManagedSecrets_ClusterConnectionEngine_LastErrorNamesClusterAndTime(t *testing.T) {
	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": "prod-eu", "server": "https://prod-eu.example.com"},
	}, http.StatusOK)
	gp := &reconcileFakeGP{
		managedYAML: []byte("clusters:\n- name: prod-eu\n  labels: {}\n"),
	}
	srv, router := reconcileTestServer(t, gp, argo.URL)

	recon := clusterreconciler.New(clusterreconciler.Deps{
		GitProvider: func() gitprovider.GitProvider { return gp },
		ArgoClient:  fake.NewSimpleClientset(),
		// A vault that always errors makes the reconciler record a Failed
		// outcome for the one managed cluster in managed-clusters.yaml.
		Vault:        erroringVault{},
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
		if rec, ok := recon.LastReconcile("prod-eu"); ok && rec.Outcome == clusterreconciler.OutcomeFailed {
			break
		}
		time.Sleep(10 * time.Millisecond)
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
	if body.Engines.ClusterConnection.LastError == "" {
		t.Fatal("expected cluster_connection engine last_error to be set")
	}
	if body.Engines.ClusterConnection.LastErrorCluster != "prod-eu" {
		t.Errorf("last_error_cluster = %q, want %q", body.Engines.ClusterConnection.LastErrorCluster, "prod-eu")
	}
	if body.Engines.ClusterConnection.LastErrorAt == "" {
		t.Error("expected last_error_at to be set — an error with no time isn't actionable")
	}
}

// TestAddonValuesSecretSourceLabel pins the fix for the "the vault" wording
// bug: the label is derived from the server's OWN addon-secret provider
// config, never a fixed guess, and unrecognized/unimplemented/demo backends
// fall back to the generic, lowercase, article-free "secrets store" rather
// than a product name Sharko isn't actually using.
func TestAddonValuesSecretSourceLabel(t *testing.T) {
	cases := []struct {
		name string
		cfg  *providers.AddonSecretProviderConfig
		want string
	}{
		{"nil config", nil, "secrets store"},
		{"aws-sm", &providers.AddonSecretProviderConfig{Type: "aws-sm"}, "AWS Secrets Manager"},
		{"aws-secrets-manager alias", &providers.AddonSecretProviderConfig{Type: "aws-secrets-manager"}, "AWS Secrets Manager"},
		{"k8s-secrets", &providers.AddonSecretProviderConfig{Type: "k8s-secrets"}, "a Kubernetes Secret"},
		{"kubernetes alias", &providers.AddonSecretProviderConfig{Type: "kubernetes"}, "a Kubernetes Secret"},
		{"gcp-sm", &providers.AddonSecretProviderConfig{Type: "gcp-sm"}, "Google Secret Manager"},
		{"azure-kv", &providers.AddonSecretProviderConfig{Type: "azure-kv"}, "Azure Key Vault"},
		// "vault" has no implemented factory (see AddonSecretProviderConfig.Type
		// doc comment) — never claim it's HashiCorp Vault when it isn't wired.
		{"vault type — no real factory, falls back honestly", &providers.AddonSecretProviderConfig{Type: "vault"}, "secrets store"},
		{"empty type", &providers.AddonSecretProviderConfig{Type: ""}, "secrets store"},
		{"demo backend — not a real product", &providers.AddonSecretProviderConfig{Type: "demo"}, "secrets store"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer()
			if tc.cfg != nil {
				srv.providerState.Store(&providerSet{addonSecretCfg: tc.cfg})
			}
			got := srv.addonValuesSecretSourceLabel()
			if got != tc.want {
				t.Errorf("addonValuesSecretSourceLabel() = %q, want %q", got, tc.want)
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

// TestHandleGetManagedSecrets_AddonValuesEngine_LastErrorNamesClusterAndTime
// pins P1-B B2 for the values engine: the same cluster+time fields #716
// added to the connection engine now exist on this one too, and last_error
// itself is already the fakeReconciler's pre-set (canned-shaped) sentence —
// the API layer does not double-map it.
func TestHandleGetManagedSecrets_AddonValuesEngine_LastErrorNamesClusterAndTime(t *testing.T) {
	srv := newTestServer()
	router := NewRouter(srv, nil)

	at := time.Date(2026, 8, 5, 9, 30, 0, 0, time.UTC)
	rec := &fakeReconciler{
		stats:            map[string]int{},
		lastError:        "Sharko couldn't connect to one of the clusters. Check that Sharko can reach that cluster, then click Refresh.",
		lastErrorCluster: "prod-eu",
		lastErrorAt:      at,
	}
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
	if body.Engines.AddonValues.LastError != rec.lastError {
		t.Errorf("last_error = %q, want %q", body.Engines.AddonValues.LastError, rec.lastError)
	}
	if body.Engines.AddonValues.LastErrorCluster != "prod-eu" {
		t.Errorf("last_error_cluster = %q, want %q", body.Engines.AddonValues.LastErrorCluster, "prod-eu")
	}
	if body.Engines.AddonValues.LastErrorAt == "" {
		t.Error("expected last_error_at to be set — an error with no time isn't actionable")
	}
}

// TestHandleGetManagedSecrets_AddonValuesEngine_NoErrorLeavesClusterAndTimeEmpty
// makes sure the new fields stay empty on a clean pass — no fabricated
// cluster or timestamp just because the fields now exist.
func TestHandleGetManagedSecrets_AddonValuesEngine_NoErrorLeavesClusterAndTimeEmpty(t *testing.T) {
	srv := newTestServer()
	router := NewRouter(srv, nil)
	srv.SetSecretReconciler(&fakeReconciler{stats: map[string]int{}})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/managed-secrets", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	var body managedSecretsResponse
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Engines.AddonValues.LastErrorCluster != "" {
		t.Errorf("last_error_cluster = %q, want empty on a clean pass", body.Engines.AddonValues.LastErrorCluster)
	}
	if body.Engines.AddonValues.LastErrorAt != "" {
		t.Errorf("last_error_at = %q, want empty on a clean pass", body.Engines.AddonValues.LastErrorAt)
	}
}

// TestConnectionSelfHeals_DerivationBothKinds pins P2-C3's real rule for a
// connection row's self_heals promise: self-managed and v4 always heal
// regardless of the setting; a v3, Sharko-managed cluster heals only when
// the live managed_cluster_self_heal setting says so.
func TestConnectionSelfHeals_DerivationBothKinds(t *testing.T) {
	cases := []struct {
		name         string
		cluster      models.Cluster
		comparedPath string
		v3SelfHealOn bool
		want         bool
	}{
		{
			name:         "self-managed connection always heals, setting off",
			cluster:      models.Cluster{ConnectionManagedBy: "user"},
			comparedPath: clusterreconciler.DefaultManagedClustersPath,
			v3SelfHealOn: false,
			want:         true,
		},
		{
			name:         "self-managed connection always heals, setting on",
			cluster:      models.Cluster{ConnectionManagedBy: "user"},
			comparedPath: clusterreconciler.DefaultManagedClustersPath,
			v3SelfHealOn: true,
			want:         true,
		},
		{
			name:         "v4 repo always heals, setting off",
			cluster:      models.Cluster{},
			comparedPath: clusterreconciler.V4ManagedClustersPath,
			v3SelfHealOn: false,
			want:         true,
		},
		{
			name:         "v3 repo heals only when the setting is on — ON",
			cluster:      models.Cluster{},
			comparedPath: clusterreconciler.DefaultManagedClustersPath,
			v3SelfHealOn: true,
			want:         true,
		},
		{
			name:         "v3 repo heals only when the setting is on — OFF",
			cluster:      models.Cluster{},
			comparedPath: clusterreconciler.DefaultManagedClustersPath,
			v3SelfHealOn: false,
			want:         false,
		},
		{
			name:         "never checked (comparedPath unknown) falls back to the real setting, not a guess",
			cluster:      models.Cluster{},
			comparedPath: "",
			v3SelfHealOn: false,
			want:         false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := connectionSelfHeals(tc.cluster, tc.comparedPath, tc.v3SelfHealOn); got != tc.want {
				t.Errorf("connectionSelfHeals() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestConnectionDriftSource_BothDirections pins P2-C6: which side moved,
// derived purely from comparing the two revisions on an out_of_sync row.
func TestConnectionDriftSource_BothDirections(t *testing.T) {
	cases := []struct {
		name             string
		state            string
		comparedRevision string
		appliedRevision  string
		want             string
	}{
		{
			name:             "git moved — compared and applied disagree",
			state:            "out_of_sync",
			comparedRevision: "newcommit1111111111111111111111111111aa",
			appliedRevision:  "oldcommit2222222222222222222222222222bb",
			want:             "git",
		},
		{
			name:             "the cluster moved — compared and applied agree, yet live differs",
			state:            "out_of_sync",
			comparedRevision: "samecommit111111111111111111111111111a",
			appliedRevision:  "samecommit111111111111111111111111111a",
			want:             "cluster",
		},
		{
			name:             "not out_of_sync — never guesses",
			state:            "in_sync",
			comparedRevision: "newcommit1111111111111111111111111111aa",
			appliedRevision:  "oldcommit2222222222222222222222222222bb",
			want:             "",
		},
		{
			name:             "compared revision unknown — says nothing",
			state:            "out_of_sync",
			comparedRevision: "",
			appliedRevision:  "oldcommit2222222222222222222222222222bb",
			want:             "",
		},
		{
			name:             "applied revision unknown (never successfully written) — says nothing",
			state:            "out_of_sync",
			comparedRevision: "newcommit1111111111111111111111111111aa",
			appliedRevision:  "",
			want:             "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := connectionDriftSource(tc.state, tc.comparedRevision, tc.appliedRevision); got != tc.want {
				t.Errorf("connectionDriftSource() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestHandleGetManagedSecrets_AddonValuesSecretRow_SelfHeals pins P2-C3 for
// the values side: every row self-heals except a foreign one, taken all
// the way through the endpoint (mirrors
// TestHandleGetManagedSecrets_AddonValuesSecretRow_ForeignSurfacesOnTheRow's
// shape).
func TestHandleGetManagedSecrets_AddonValuesSecretRow_SelfHeals(t *testing.T) {
	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": "prod-eu", "server": "https://prod-eu.example.com"},
	}, http.StatusOK)
	gp := &reconcileFakeGP{
		managedYAML: []byte("clusters:\n- name: prod-eu\n  labels:\n    datadog: enabled\n    eso: enabled\n"),
	}
	srv, router := reconcileTestServer(t, gp, argo.URL)
	srv.SetAddonSecretDefs(map[string]orchestrator.AddonSecretDefinition{
		"datadog": {AddonName: "datadog", SecretName: "datadog-secrets", Namespace: "datadog", Keys: map[string]string{"api-key": "secrets/datadog/api-key"}},
		"eso":     {AddonName: "eso", SecretName: "eso-secrets", Namespace: "eso", Keys: map[string]string{"api-key": "secrets/eso/api-key"}},
	})
	srv.SetSecretReconciler(&fakeReconciler{
		itemOutcome: map[[2]string]string{
			{"prod-eu", "datadog"}: "foreign",
			{"prod-eu", "eso"}:     "unchanged",
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
	if len(body.AddonValuesSecrets) != 2 {
		t.Fatalf("expected exactly 2 addon-values rows, got %d", len(body.AddonValuesSecrets))
	}
	for _, row := range body.AddonValuesSecrets {
		switch row.Addon {
		case "datadog":
			if row.SelfHeals {
				t.Error("a foreign row must never self-heal — Sharko doesn't touch what it didn't create")
			}
		case "eso":
			if !row.SelfHeals {
				t.Error("an owned row self-heals — the values engine always repairs what it owns")
			}
		}
	}
}
