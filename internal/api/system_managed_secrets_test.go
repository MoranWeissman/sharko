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
				// M8 (code review): connectionSecretState now matches on
				// RawMessage, not Message — Message is the
				// FailureSentence-mapped, safe-for-a-browser text
				// (applyLastReconcile), which no longer carries this exact
				// sentinel string. RawMessage is what applyLastReconcile
				// actually populates from the reconciler's own unmapped
				// Message, so that's what the fixture sets here.
				RawMessage: clusterreconciler.SelfManagedSecretNotCreatedMessage,
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
	// M8 (code review): connectionSecretCheckError now maps RawMessage, not
	// Message — see that function's own comment.
	failed := &models.ClusterLastReconcile{Outcome: string(clusterreconciler.OutcomeFailed), RawMessage: raw}
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

	srv.SetDemoAddonSecretDefs(map[string]orchestrator.AddonSecretDefinition{
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

	srv.SetDemoAddonSecretDefs(map[string]orchestrator.AddonSecretDefinition{
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

	srv.SetDemoAddonSecretDefs(map[string]orchestrator.AddonSecretDefinition{
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

	srv.SetDemoAddonSecretDefs(map[string]orchestrator.AddonSecretDefinition{
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
	srv.SetDemoAddonSecretDefs(map[string]orchestrator.AddonSecretDefinition{
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

	srv.SetDemoAddonSecretDefs(map[string]orchestrator.AddonSecretDefinition{
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
		{
			// remoteclient.ErrUnverifiedDestination's exact text, as
			// checkWork/reconcileSecret return it unwrapped (task #152
			// lane C's row-level loose end — see the case's own comment
			// in system_managed_secrets.go for why this was missing).
			"unverified destination — TLS refusal reaches the row",
			`this cluster's connection is set up to skip certificate checks, so Sharko will not send a secret over it`,
			"This cluster's connection is set up to skip certificate checks, so Sharko refused to send it a secret.",
		},
		{
			// internal/providers.awsOutsidePrefixRefusal, wrapped the way
			// reconcileSecret/checkWork wrap every provider fetch error
			// ("fetching %q from provider: %w") — task #152 story B's
			// boundary refusal reaching the row.
			"AWS prefix boundary refusal reaches the row",
			`fetching "clusters/prod/x" from provider: secret path refused: "clusters/prod/x" is outside the prefix "clusters/" this AWS Secrets Manager connection is allowed to read. Sharko only reads addon secrets under the configured prefix`,
			`"clusters/prod/x" is outside the prefix "clusters/" this AWS Secrets Manager connection is allowed to read. Sharko only reads addon secrets under the configured prefix`,
		},
		{
			// internal/providers.k8sOutsideNamespaceRefusal, same
			// wrapping — pins that the extraction isn't AWS-specific.
			"Kubernetes namespace boundary refusal reaches the row",
			`fetching "other-ns/creds/api-key" from provider: secret path refused: "other-ns/creds/api-key" points at namespace "other-ns", but this Kubernetes secrets connection is only allowed to read secrets in namespace "sharko"`,
			`"other-ns/creds/api-key" points at namespace "other-ns", but this Kubernetes secrets connection is only allowed to read secrets in namespace "sharko"`,
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

// TestAddonValuesSecretSyncFailureSentence_NeverEchoesRawText is the
// sync-side twin of TestAddonValuesSecretCheckFailureSentence_NeverEchoesRawText
// (S8/H1) — pins that addonValuesSecretSyncFailureSentence recognizes the
// same TLS and boundary refusals the check-side mapper does, since SyncOne
// drives the identical checkWork/reconcileSecret refusal paths CheckOne
// does. Only the two refusal cases and the unrecognized-stage default are
// covered here (worded "sync" not "check") — the rest of the switch is
// identical to the check twin and already covered by that test plus the
// handler-level tests in addon_secret_single_test.go.
func TestAddonValuesSecretSyncFailureSentence_NeverEchoesRawText(t *testing.T) {
	cases := []struct {
		name   string
		errMsg string
		want   string
	}{
		{"empty", "", ""},
		{
			"unverified destination — TLS refusal reaches the row",
			`this cluster's connection is set up to skip certificate checks, so Sharko will not send a secret over it`,
			"This cluster's connection is set up to skip certificate checks, so Sharko refused to send it a secret.",
		},
		{
			"AWS prefix boundary refusal reaches the row",
			`fetching "clusters/prod/x" from provider: secret path refused: "clusters/prod/x" is outside the prefix "clusters/" this AWS Secrets Manager connection is allowed to read. Sharko only reads addon secrets under the configured prefix`,
			`"clusters/prod/x" is outside the prefix "clusters/" this AWS Secrets Manager connection is allowed to read. Sharko only reads addon secrets under the configured prefix`,
		},
		{
			"foreign secret refusal — passed through verbatim, unchanged",
			`Someone else created this one — Sharko will not touch it`,
			`Someone else created this one — Sharko will not touch it`,
		},
		{
			"unrecognized stage — safe fallback, not the raw string",
			`something entirely unexpected: value=hunter2`,
			"The last sync didn't finish.",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := addonValuesSecretSyncFailureSentence(tc.errMsg)
			if got != tc.want {
				t.Errorf("addonValuesSecretSyncFailureSentence(%q) = %q, want %q", tc.errMsg, got, tc.want)
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
		// G2 (gitops-proud P4-G): demo mode is a real, known backend — it
		// gets its own honest name now instead of the generic fallback, so
		// `make demo-big` stops showing "secrets store" on every row.
		{"demo backend — a real, known backend, gets its own name (G2)", &providers.AddonSecretProviderConfig{Type: "demo"}, "the demo secrets store"},
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
	srv.SetDemoAddonSecretDefs(map[string]orchestrator.AddonSecretDefinition{
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

// ---------------------------------------------------------------------------
// P3-E — GET /api/v1/system/managed-secrets learns to back a portal:
// query-param filters + paging (E1), a merged worst-first `rows` array
// (E2), and a Viewer-tier authz gate (E3).
// ---------------------------------------------------------------------------

// TestBuildManagedSecretRows_MergesAndTagsEachKind pins E2's field-mapping
// contract: a connection row and a values row both land in the merged
// array, each tagged with its Kind, carrying its own kind-specific fields,
// and leaving the OTHER kind's fields empty/zero rather than inventing a
// value (connection rows have no Addon/ConsecutiveFailures; values rows
// have no ComparedRevision/AppliedRevision/ComparedPath/DriftSource/
// FightCount).
func TestBuildManagedSecretRows_MergesAndTagsEachKind(t *testing.T) {
	conn := []connectionSecretRow{{
		Cluster:          "prod-eu",
		SecretNamespace:  "argocd",
		SecretName:       "prod-eu",
		State:            "out_of_sync",
		Source:           connectionSecretSource,
		LastChecked:      "2026-08-01T00:00:00Z",
		LastCheckError:   "",
		ComparedRevision: "abc1234",
		ComparedPath:     "configuration/managed-clusters.yaml",
		AppliedRevision:  "def5678",
		SelfHeals:        true,
		DriftSource:      "git",
		FightCount:       4,
	}}
	values := []addonValuesSecretRow{{
		Cluster:             "prod-eu",
		Addon:               "datadog",
		SecretName:          "datadog-secrets",
		SecretNamespace:     "datadog",
		State:               "missing",
		Source:              "AWS Secrets Manager",
		LastChecked:         "2026-08-01T00:05:00Z",
		SelfHeals:           true,
		ConsecutiveFailures: 2,
	}}

	rows := buildManagedSecretRows(conn, values, nil)
	if len(rows) != 2 {
		t.Fatalf("expected 2 merged rows, got %d", len(rows))
	}

	// G3 (gitops-proud P4-G): missing (rank 0) now sorts before out_of_sync
	// (rank 1) — the values row comes first.
	valuesRow, connRow := rows[0], rows[1]

	if connRow.Kind != managedSecretKindConnection {
		t.Fatalf("rows[1].Kind = %q, want %q", connRow.Kind, managedSecretKindConnection)
	}
	if connRow.Name != "prod-eu" || connRow.Namespace != "argocd" {
		t.Errorf("connection row name/namespace = %q/%q, want prod-eu/argocd", connRow.Name, connRow.Namespace)
	}
	if connRow.Cluster != "prod-eu" || connRow.Source != "git" || connRow.State != "out_of_sync" {
		t.Errorf("connection row cluster/source/state = %q/%q/%q, want prod-eu/git/out_of_sync", connRow.Cluster, connRow.Source, connRow.State)
	}
	if connRow.ComparedRevision != "abc1234" || connRow.AppliedRevision != "def5678" || connRow.ComparedPath != "configuration/managed-clusters.yaml" || connRow.DriftSource != "git" {
		t.Errorf("connection row lost a P2-C field: %+v", connRow)
	}
	if connRow.FightCount != 4 {
		t.Errorf("connection row fight_count = %d, want 4", connRow.FightCount)
	}
	// Fields that only apply to a values row must stay empty/zero on a
	// connection row — nothing invented.
	if connRow.Addon != "" {
		t.Errorf("connection row addon = %q, want empty (a connection secret has no addon)", connRow.Addon)
	}
	if connRow.ConsecutiveFailures != 0 {
		t.Errorf("connection row consecutive_failures = %d, want 0 (FightCount is this kind's counter)", connRow.ConsecutiveFailures)
	}

	if valuesRow.Kind != managedSecretKindValues {
		t.Fatalf("rows[0].Kind = %q, want %q", valuesRow.Kind, managedSecretKindValues)
	}
	if valuesRow.Name != "datadog-secrets" || valuesRow.Namespace != "datadog" {
		t.Errorf("values row name/namespace = %q/%q, want datadog-secrets/datadog", valuesRow.Name, valuesRow.Namespace)
	}
	if valuesRow.Cluster != "prod-eu" || valuesRow.Addon != "datadog" || valuesRow.State != "missing" {
		t.Errorf("values row cluster/addon/state = %q/%q/%q, want prod-eu/datadog/missing", valuesRow.Cluster, valuesRow.Addon, valuesRow.State)
	}
	if valuesRow.ConsecutiveFailures != 2 {
		t.Errorf("values row consecutive_failures = %d, want 2", valuesRow.ConsecutiveFailures)
	}
	// Fields that only apply to a connection row must stay empty/zero on a
	// values row — the values engine compares against the vault, not git,
	// so it has no commit-revision or drift-blame facts (S3(a)).
	if valuesRow.ComparedRevision != "" || valuesRow.AppliedRevision != "" || valuesRow.ComparedPath != "" || valuesRow.DriftSource != "" {
		t.Errorf("values row invented a git-only field: %+v", valuesRow)
	}
	if valuesRow.FightCount != 0 {
		t.Errorf("values row fight_count = %d, want 0 (ConsecutiveFailures is this kind's counter)", valuesRow.FightCount)
	}
}

// TestManagedSecretStateSortRank_MatchesTheUITable pins the exact rank
// table ui/src/components/resource/StatusMark.tsx's statusSortRank uses, so
// the merged Rows array reads in the same order the System page renders:
// missing, out_of_sync, orphaned, foreign, unknown, in_sync (leftover-
// secrets S1 inserted "orphaned" between out_of_sync and foreign). An
// unrecognized state string reads as "unknown", matching toResourceStatus's
// own fallback.
func TestManagedSecretStateSortRank_MatchesTheUITable(t *testing.T) {
	cases := []struct {
		state         string
		hasCheckError bool
		want          int
	}{
		{"missing", false, 0},
		{"out_of_sync", false, 1},
		{"orphaned", false, 2},
		{"foreign", false, 3},
		{"unknown", true, 4},                                   // a FAILED check outranks a never-checked row
		{"unknown", false, 5},                                  // genuinely never checked
		{"in_sync", false, 6},
		{"some-state-the-server-has-never-heard-of", false, 5}, // falls through to "unknown"'s (not-checked) rank
		{"some-state-the-server-has-never-heard-of", true, 4},  // ...but a check error still promotes it
		{"", false, 5},
	}
	for _, tc := range cases {
		if got := managedSecretStateSortRank(tc.state, tc.hasCheckError); got != tc.want {
			t.Errorf("managedSecretStateSortRank(%q, %v) = %d, want %d", tc.state, tc.hasCheckError, got, tc.want)
		}
	}
}

// TestBuildManagedSecretRows_WorstFirstOrder builds one row per rank
// position — including BOTH "unknown" flavors (a failed check and a
// genuinely never-checked row, distinguished only by LastCheckError) — in a
// scrambled input order, and pins that the merged array always comes back
// in the fixed worst-first order (G3, gitops-proud P4-G) regardless of
// input order or which kind each row is.
func TestBuildManagedSecretRows_WorstFirstOrder(t *testing.T) {
	conn := []connectionSecretRow{
		{Cluster: "c-in-sync", State: "in_sync"},
		{Cluster: "c-not-checked", State: "unknown"},
		{Cluster: "c-check-failed", State: "unknown", LastCheckError: "Sharko couldn't connect to this cluster."},
	}
	values := []addonValuesSecretRow{
		{Cluster: "c-foreign", Addon: "a", State: "foreign"},
		{Cluster: "c-missing", Addon: "a", State: "missing"},
		{Cluster: "c-out-of-sync", Addon: "a", State: "out_of_sync"},
	}

	rows := buildManagedSecretRows(conn, values, nil)
	if len(rows) != 6 {
		t.Fatalf("expected 6 merged rows, got %d", len(rows))
	}
	// c-check-failed and c-not-checked share the exact same State
	// ("unknown"), so the order is pinned by Cluster name, not State.
	wantClusters := []string{"c-missing", "c-out-of-sync", "c-foreign", "c-check-failed", "c-not-checked", "c-in-sync"}
	for i, want := range wantClusters {
		if rows[i].Cluster != want {
			t.Errorf("rows[%d].Cluster = %q, want %q (full order: %v)", i, rows[i].Cluster, want, clustersOf(rows))
		}
	}
}

func clustersOf(rows []managedSecretRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Cluster
	}
	return out
}

func statesOf(rows []managedSecretRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.State
	}
	return out
}

// TestFilterManagedSecretRows_EachParamAndCombinations pins E1's filter
// semantics: exact-match, case-honest, AND-joined across params. An
// unrecognized value for a param (a typo'd cluster, an addon that never
// runs on any row, a state/kind/source the server has never produced)
// matches nothing — a documented empty result, never an error — mirroring
// the house pattern filterClusters/filterAddons already use for a
// recognized filter field whose value simply doesn't occur.
func TestFilterManagedSecretRows_EachParamAndCombinations(t *testing.T) {
	rows := []managedSecretRow{
		{Kind: managedSecretKindConnection, Cluster: "prod-eu", Source: "git", State: "out_of_sync"},
		{Kind: managedSecretKindConnection, Cluster: "prod-us", Source: "git", State: "in_sync"},
		{Kind: managedSecretKindValues, Cluster: "prod-eu", Addon: "datadog", Source: "AWS Secrets Manager", State: "missing"},
		{Kind: managedSecretKindValues, Cluster: "prod-us", Addon: "eso", Source: "AWS Secrets Manager", State: "in_sync"},
	}

	cases := []struct {
		name    string
		filters managedSecretRowFilters
		want    int
	}{
		{"no filters returns everything", managedSecretRowFilters{}, 4},
		{"cluster filter", managedSecretRowFilters{Cluster: "prod-eu"}, 2},
		{"addon filter — only values rows can match", managedSecretRowFilters{Addon: "datadog"}, 1},
		{"state filter", managedSecretRowFilters{State: "in_sync"}, 2},
		{"kind filter — connection", managedSecretRowFilters{Kind: managedSecretKindConnection}, 2},
		{"kind filter — values", managedSecretRowFilters{Kind: managedSecretKindValues}, 2},
		{"source filter", managedSecretRowFilters{Source: "AWS Secrets Manager"}, 2},
		{"combined cluster+kind", managedSecretRowFilters{Cluster: "prod-eu", Kind: managedSecretKindValues}, 1},
		{"combined cluster+state — no match", managedSecretRowFilters{Cluster: "prod-eu", State: "in_sync"}, 0},
		{"unknown state value — documented empty, not an error", managedSecretRowFilters{State: "not-a-real-state"}, 0},
		{"unknown kind value — documented empty, not an error", managedSecretRowFilters{Kind: "not-a-real-kind"}, 0},
		{"unknown cluster value — documented empty", managedSecretRowFilters{Cluster: "does-not-exist"}, 0},
		{"case-honest — different case never matches", managedSecretRowFilters{Cluster: "PROD-EU"}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := filterManagedSecretRows(rows, tc.filters)
			if len(got) != tc.want {
				t.Errorf("filterManagedSecretRows(%+v) returned %d rows, want %d (got=%+v)", tc.filters, len(got), tc.want, got)
			}
		})
	}
}

// TestParseManagedSecretRowFilters_ReadsAllFiveParams pins the exact query
// param names E1 specifies.
func TestParseManagedSecretRowFilters_ReadsAllFiveParams(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/managed-secrets?cluster=prod-eu&addon=datadog&state=missing&kind=values&source=git", nil)
	got := parseManagedSecretRowFilters(req)
	want := managedSecretRowFilters{Cluster: "prod-eu", Addon: "datadog", State: "missing", Kind: "values", Source: "git"}
	if got != want {
		t.Errorf("parseManagedSecretRowFilters() = %+v, want %+v", got, want)
	}

	empty := parseManagedSecretRowFilters(httptest.NewRequest(http.MethodGet, "/api/v1/system/managed-secrets", nil))
	if empty != (managedSecretRowFilters{}) {
		t.Errorf("parseManagedSecretRowFilters() with no query = %+v, want zero value", empty)
	}
}

// managedSecretsFilterFixture stands up two clusters (prod-eu, prod-us),
// each with the datadog addon enabled and a registered secret definition,
// but deliberately does NOT start the cluster reconciler — SetClusterReconciler
// is called on a Reconciler that has never ticked, so ClientAndNamespace()
// resolves (giving connection rows a real secret name/namespace) while
// LastReconcile stays nil for every cluster, landing both connection rows
// on the honest "unknown" state without any goroutine/timing dependency.
// The values-row states are deterministic because fakeReconciler.itemOutcome
// is seeded directly, no reconcile pass needed either.
func managedSecretsFilterFixture(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": "prod-eu", "server": "https://prod-eu.example.com"},
		{"name": "prod-us", "server": "https://prod-us.example.com"},
	}, http.StatusOK)
	gp := &reconcileFakeGP{managedYAML: []byte(
		"clusters:\n" +
			"- name: prod-eu\n  labels:\n    datadog: enabled\n" +
			"- name: prod-us\n  labels:\n    datadog: enabled\n",
	)}
	srv, router := reconcileTestServer(t, gp, argo.URL)

	recon := clusterreconciler.New(clusterreconciler.Deps{
		GitProvider:  func() gitprovider.GitProvider { return gp },
		ArgoClient:   fake.NewSimpleClientset(),
		Vault:        reconcileFakeVault{},
		AuditFn:      func(audit.Entry) {},
		Namespace:    "argocd",
		TickInterval: time.Minute,
	})
	srv.SetClusterReconciler(recon)

	srv.SetDemoAddonSecretDefs(map[string]orchestrator.AddonSecretDefinition{
		"datadog": {AddonName: "datadog", SecretName: "datadog-secrets", Namespace: "datadog", Keys: map[string]string{"api-key": "secrets/datadog/api-key"}},
	})
	srv.SetSecretReconciler(&fakeReconciler{
		itemOutcome: map[[2]string]string{
			{"prod-eu", "datadog"}: "unchanged", // -> in_sync
			{"prod-us", "datadog"}: "missing",   // -> missing
		},
	})

	return srv, router
}

// TestHandleGetManagedSecrets_Rows_MergedShapeWorstFirstAndOldArraysUnchanged
// takes E1+E2+E3 through the real HTTP handler: two connection rows (both
// "unknown", no reconcile tick) and two values rows (one "in_sync", one
// "missing"), asserting the merged `rows` array has all four in worst-first
// order, and that the two pre-existing per-kind arrays are returned exactly
// as before this lane (the additive/backward-compatible contract).
func TestHandleGetManagedSecrets_Rows_MergedShapeWorstFirstAndOldArraysUnchanged(t *testing.T) {
	_, router := managedSecretsFilterFixture(t)

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

	// Pin: the old arrays STAY — full, unfiltered, exactly as this suite's
	// pre-P3-E tests already pin their shape.
	if len(body.ClusterConnectionSecrets) != 2 {
		t.Fatalf("expected 2 connection-secret rows (unchanged by this lane), got %d", len(body.ClusterConnectionSecrets))
	}
	if len(body.AddonValuesSecrets) != 2 {
		t.Fatalf("expected 2 addon-values rows (unchanged by this lane), got %d", len(body.AddonValuesSecrets))
	}

	if len(body.Rows) != 4 {
		t.Fatalf("expected 4 merged rows, got %d (%+v)", len(body.Rows), body.Rows)
	}
	wantStates := []string{"missing", "unknown", "unknown", "in_sync"}
	for i, want := range wantStates {
		if body.Rows[i].State != want {
			t.Errorf("rows[%d].State = %q, want %q (full: %v)", i, body.Rows[i].State, want, statesOf(body.Rows))
		}
	}
	// Tie-break within the "unknown" pair is by cluster name: prod-eu < prod-us.
	if body.Rows[1].Cluster != "prod-eu" || body.Rows[2].Cluster != "prod-us" {
		t.Errorf("unknown-state tie-break order = %q, %q, want prod-eu, prod-us", body.Rows[1].Cluster, body.Rows[2].Cluster)
	}
	// The missing row is the values row for prod-us; secret identity fields
	// must be the values definition's, not invented.
	if body.Rows[0].Kind != managedSecretKindValues || body.Rows[0].Cluster != "prod-us" || body.Rows[0].Addon != "datadog" {
		t.Errorf("rows[0] = %+v, want the prod-us/datadog values row", body.Rows[0])
	}
}

// TestHandleGetManagedSecrets_Rows_FilterByKind pins ?kind= restricting the
// merged array to one engine's rows, without touching the old per-kind
// arrays (which never carried a kind filter to begin with — they're
// arrays, not a mixed list).
func TestHandleGetManagedSecrets_Rows_FilterByKind(t *testing.T) {
	_, router := managedSecretsFilterFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/managed-secrets?kind=values", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	var body managedSecretsResponse
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Rows) != 2 {
		t.Fatalf("expected 2 values rows after ?kind=values, got %d (%+v)", len(body.Rows), body.Rows)
	}
	for _, row := range body.Rows {
		if row.Kind != managedSecretKindValues {
			t.Errorf("row kind = %q, want values (filter should have excluded it)", row.Kind)
		}
	}
	// Old arrays are untouched by the filter — still both present in full.
	if len(body.ClusterConnectionSecrets) != 2 || len(body.AddonValuesSecrets) != 2 {
		t.Errorf("old arrays changed shape under a rows-only filter: connection=%d values=%d", len(body.ClusterConnectionSecrets), len(body.AddonValuesSecrets))
	}
}

// TestHandleGetManagedSecrets_Rows_FilterByClusterAndState pins ?cluster=
// and ?state= combined (AND-joined), and that an unmatched combination
// returns a 200 with an empty rows array, never an error.
func TestHandleGetManagedSecrets_Rows_FilterByClusterAndState(t *testing.T) {
	_, router := managedSecretsFilterFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/managed-secrets?cluster=prod-us&state=missing", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	var body managedSecretsResponse
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Rows) != 1 || body.Rows[0].Cluster != "prod-us" || body.Rows[0].State != "missing" {
		t.Fatalf("expected exactly the prod-us/missing row, got %+v", body.Rows)
	}

	// An unmatched combination: 200 with empty rows, not an error.
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/system/managed-secrets?cluster=prod-us&state=in_sync", nil)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 for a filter combination with no matches, got %d", w2.Code)
	}
	var body2 managedSecretsResponse
	if err := json.NewDecoder(w2.Body).Decode(&body2); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body2.Rows) != 0 {
		t.Errorf("expected 0 rows for an unmatched filter combination, got %d", len(body2.Rows))
	}
}

// TestHandleGetManagedSecrets_Rows_Paging pins paging via the existing
// pagination helper (per_page/page query params, X-Total-Count/X-Page/
// X-Per-Page response headers — the same envelope /clusters and /addons
// already use), applied to the merged rows array. Total is 4 (2 connection
// + 2 values); per_page=1 slices to exactly the first (worst) row while
// X-Total-Count still reports the true unpaged total.
func TestHandleGetManagedSecrets_Rows_Paging(t *testing.T) {
	_, router := managedSecretsFilterFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/managed-secrets?page=1&per_page=1", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if got := w.Header().Get("X-Total-Count"); got != "4" {
		t.Errorf("X-Total-Count = %q, want 4", got)
	}
	if got := w.Header().Get("X-Page"); got != "1" {
		t.Errorf("X-Page = %q, want 1", got)
	}
	if got := w.Header().Get("X-Per-Page"); got != "1" {
		t.Errorf("X-Per-Page = %q, want 1", got)
	}
	var body managedSecretsResponse
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Rows) != 1 {
		t.Fatalf("expected exactly 1 row on page 1 with per_page=1, got %d", len(body.Rows))
	}
	// Worst row first — the prod-us/missing values row (see the worst-first test above).
	if body.Rows[0].State != "missing" {
		t.Errorf("rows[0].State = %q, want missing (the worst row)", body.Rows[0].State)
	}
	// Old arrays are NEVER paginated — still both full.
	if len(body.ClusterConnectionSecrets) != 2 || len(body.AddonValuesSecrets) != 2 {
		t.Errorf("old arrays got paginated: connection=%d values=%d, want 2/2", len(body.ClusterConnectionSecrets), len(body.AddonValuesSecrets))
	}

	// Page 2 gets the next row.
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/system/managed-secrets?page=2&per_page=1", nil)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	var body2 managedSecretsResponse
	if err := json.NewDecoder(w2.Body).Decode(&body2); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body2.Rows) != 1 {
		t.Fatalf("expected exactly 1 row on page 2, got %d", len(body2.Rows))
	}
	if body2.Rows[0].Cluster == body.Rows[0].Cluster && body2.Rows[0].Kind == body.Rows[0].Kind {
		t.Error("page 2 returned the same row as page 1")
	}
}

// TestHandleGetManagedSecrets_Rows_PagingHeadersOnDegradedResponse pins that
// the pagination header contract (X-Total-Count etc.) is set even on the
// "degrade, never 500" no-connection path — a caller paging through this
// endpoint sees a consistent header contract regardless of which internal
// path produced the response.
func TestHandleGetManagedSecrets_Rows_PagingHeadersOnDegradedResponse(t *testing.T) {
	srv := newTestServer()
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/managed-secrets", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if got := w.Header().Get("X-Total-Count"); got != "0" {
		t.Errorf("X-Total-Count = %q, want 0 on the degraded no-connection path", got)
	}
	var body managedSecretsResponse
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Rows) != 0 {
		t.Errorf("expected empty rows on the degraded path, got %d", len(body.Rows))
	}
}

// ---------------------------------------------------------------------------
// E3 — Viewer-tier authz gate
// ---------------------------------------------------------------------------

// TestHandleGetManagedSecrets_Authz_ViewerAndAboveAllowed pins that every
// role at Viewer or above can read the list — the same tier /clusters and
// /addons already use. The live per-row object read
// (secret.resource.read, internal/api/secret_resource.go) stays Operator+
// and is untouched by this lane.
func TestHandleGetManagedSecrets_Authz_ViewerAndAboveAllowed(t *testing.T) {
	for _, role := range []string{"viewer", "operator", "admin"} {
		t.Run(role, func(t *testing.T) {
			srv := newTestServer()
			router := NewRouter(srv, nil)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/system/managed-secrets", nil)
			req.Header.Set("X-Sharko-User", "someone")
			req.Header.Set("X-Sharko-Role", role)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("role %q: expected 200, got %d (body=%s)", role, w.Code, w.Body.String())
			}
		})
	}
}

// TestHandleGetManagedSecrets_Authz_NoAuthHeadersFailsOpen pins the
// existing, deployment-wide fail-open convention (internal/authz.Require:
// no X-Sharko-User/X-Sharko-Role headers at all means auth isn't configured
// for this deployment, not "reject") — the SAME convention every other
// Viewer-tier endpoint in this codebase already relies on. A request with
// NO headers is not a below-viewer role (there is no role below Viewer;
// RoleFromString's own zero value IS RoleViewer) — it is the
// auth-not-configured case, and this endpoint must behave exactly like
// every sibling Viewer-tier endpoint here, not invent a stricter rule of
// its own.
func TestHandleGetManagedSecrets_Authz_NoAuthHeadersFailsOpen(t *testing.T) {
	srv := newTestServer()
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/managed-secrets", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (no auth headers = auth not configured, fail-open, matches every other Viewer-tier endpoint), got %d (body=%s)", w.Code, w.Body.String())
	}
}
