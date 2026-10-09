package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// addon_secret_single_test.go — POST /clusters/{name}/addons/{addon}/secret/refresh
// and .../secret/sync (S4, Managed Secrets page row actions).
//
// Pinned contract:
//  1. 200 on success, driving the reconciler's single-item path
//     (CheckOne/SyncOne) with exactly the cluster+addon from the URL — never
//     a whole pass.
//  2. 403 for a viewer — both are operator+ actions.
//  3. 503 when no addon-values reconciler is wired on this server instance.
//  4. 422 with the reconciler's own honest error text when the check/sync
//     itself cannot run (no Git connection, item not found, credentials,
//     etc.) — never a silent success.
//  5. An audit entry is written on success.

func refreshReq(cluster, addon string) *http.Request {
	return httptest.NewRequest(http.MethodPost,
		"/api/v1/clusters/"+cluster+"/addons/"+addon+"/secret/refresh", nil)
}

func syncReq(cluster, addon string) *http.Request {
	return httptest.NewRequest(http.MethodPost,
		"/api/v1/clusters/"+cluster+"/addons/"+addon+"/secret/sync", nil)
}

// --- Refresh (CheckOne) ---

func TestRefreshAddonValuesSecret_ViewerForbidden(t *testing.T) {
	srv := newTestServer()
	srv.SetSecretReconciler(&fakeSecretReconciler{})
	router := NewRouter(srv, nil)

	req := withRole(refreshReq("prod-eu", "datadog"), "viewer")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert403(t, w)
}

func TestRefreshAddonValuesSecret_NoReconciler503(t *testing.T) {
	srv := newTestServer()
	router := NewRouter(srv, nil)

	req := withRole(refreshReq("prod-eu", "datadog"), "operator")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body = %s", w.Code, w.Body.String())
	}
}

func TestRefreshAddonValuesSecret_Success(t *testing.T) {
	srv := newTestServer()
	rec := &fakeSecretReconciler{checkOutcome: "out_of_sync"}
	srv.SetSecretReconciler(rec)
	router := NewRouter(srv, nil)

	req := withRole(refreshReq("prod-eu", "datadog"), "operator")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	if len(rec.checkCalls) != 1 || rec.checkCalls[0] != (itemCallArgs{"prod-eu", "datadog"}) {
		t.Fatalf("CheckOne called with %+v, want exactly one call for (prod-eu, datadog)", rec.checkCalls)
	}
	// Never a fleet-wide trigger for a single-row action.
	if rec.triggered != 0 {
		t.Errorf("Trigger() called %d times, want 0 — this is a single-item action", rec.triggered)
	}

	var body addonValuesSecretActionResult
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Outcome != "out_of_sync" {
		t.Errorf("outcome = %q, want out_of_sync", body.Outcome)
	}
	if body.Cluster != "prod-eu" || body.Addon != "datadog" {
		t.Errorf("cluster/addon = %q/%q, want prod-eu/datadog", body.Cluster, body.Addon)
	}
	if body.Message == "" {
		t.Error("expected a plain-English message")
	}
}

func TestRefreshAddonValuesSecret_HonestFailure(t *testing.T) {
	srv := newTestServer()
	rec := &fakeSecretReconciler{checkErr: errors.New("no addon-values secret is defined for cluster \"prod-eu\", addon \"datadog\"")}
	srv.SetSecretReconciler(rec)
	router := NewRouter(srv, nil)

	req := withRole(refreshReq("prod-eu", "datadog"), "operator")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["error"] == "" {
		t.Error("expected a non-empty, plain-words error message — never a silent success")
	}
}

// TestRefreshAddonValuesSecret_NeverEchoesRawProviderError is H1's pin
// (code review): before this fix, the handler passed CheckOne's error
// straight through via err.Error() — a provider-style error (the "near
// miss" shape system_managed_secrets_test.go already pins for the row
// path) would have reached the response body verbatim. Refresh must go
// through the same canned-sentence mapping the row path uses.
func TestRefreshAddonValuesSecret_NeverEchoesRawProviderError(t *testing.T) {
	srv := newTestServer()
	rawErr := `fetching "secrets/datadog/api-key" from provider: secret value was "sk_live_abc123..."`
	rec := &fakeSecretReconciler{checkErr: errors.New(rawErr)}
	srv.SetSecretReconciler(rec)
	router := NewRouter(srv, nil)

	req := withRole(refreshReq("prod-eu", "datadog"), "operator")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, "sk_live_abc123") || strings.Contains(body, "secrets/datadog/api-key") {
		t.Fatalf("response body leaked the raw provider error text: %s", body)
	}
	var decoded map[string]string
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	want := "Sharko couldn't fetch this secret's value from the vault."
	if decoded["error"] != want {
		t.Errorf("error = %q, want the canned sentence %q", decoded["error"], want)
	}
}

func TestRefreshAddonValuesSecret_AuditEntryWritten(t *testing.T) {
	srv := newTestServer()
	srv.SetSecretReconciler(&fakeSecretReconciler{checkOutcome: "unchanged"})
	router := NewRouter(srv, nil)

	req := withRole(refreshReq("prod-eu", "datadog"), "operator")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	entries := srv.AuditLog().List(0)
	found := false
	for _, e := range entries {
		if e.Event == "addon_values_secret_refresh_triggered" && e.Resource == "cluster:prod-eu/addon:datadog" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected an audit entry for addon_values_secret_refresh_triggered on cluster:prod-eu/addon:datadog, got %+v", entries)
	}
}

// --- Sync (SyncOne) ---

func TestSyncAddonValuesSecret_ViewerForbidden(t *testing.T) {
	srv := newTestServer()
	srv.SetSecretReconciler(&fakeSecretReconciler{})
	router := NewRouter(srv, nil)

	req := withRole(syncReq("prod-eu", "datadog"), "viewer")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert403(t, w)
}

func TestSyncAddonValuesSecret_NoReconciler503(t *testing.T) {
	srv := newTestServer()
	router := NewRouter(srv, nil)

	req := withRole(syncReq("prod-eu", "datadog"), "operator")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body = %s", w.Code, w.Body.String())
	}
}

func TestSyncAddonValuesSecret_Success(t *testing.T) {
	srv := newTestServer()
	rec := &fakeSecretReconciler{syncOutcome: "updated"}
	srv.SetSecretReconciler(rec)
	router := NewRouter(srv, nil)

	req := withRole(syncReq("prod-eu", "datadog"), "operator")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	if len(rec.syncCalls) != 1 || rec.syncCalls[0] != (itemCallArgs{"prod-eu", "datadog"}) {
		t.Fatalf("SyncOne called with %+v, want exactly one call for (prod-eu, datadog)", rec.syncCalls)
	}
	if rec.triggered != 0 {
		t.Errorf("Trigger() called %d times, want 0 — this is a single-item action", rec.triggered)
	}

	var body addonValuesSecretActionResult
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Outcome != "updated" {
		t.Errorf("outcome = %q, want updated", body.Outcome)
	}
}

func TestSyncAddonValuesSecret_HonestFailure(t *testing.T) {
	srv := newTestServer()
	rec := &fakeSecretReconciler{syncErr: errors.New("no Git connection is configured — nothing to check or push")}
	srv.SetSecretReconciler(rec)
	router := NewRouter(srv, nil)

	req := withRole(syncReq("prod-eu", "datadog"), "operator")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["error"] == "" {
		t.Error("expected a non-empty, plain-words error message — never a silent success")
	}
}

// TestSyncAddonValuesSecret_NeverEchoesRawProviderError is Sync's half of
// H1's pin — same fix, sync-side sentence mapper.
func TestSyncAddonValuesSecret_NeverEchoesRawProviderError(t *testing.T) {
	srv := newTestServer()
	rawErr := `updating secret: Operation cannot be fulfilled on secrets "datadog-secret": the object has been modified`
	rec := &fakeSecretReconciler{syncErr: errors.New(rawErr)}
	srv.SetSecretReconciler(rec)
	router := NewRouter(srv, nil)

	req := withRole(syncReq("prod-eu", "datadog"), "operator")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, "Operation cannot be fulfilled") || strings.Contains(body, "the object has been modified") {
		t.Fatalf("response body leaked the raw Kubernetes error text: %s", body)
	}
	var decoded map[string]string
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	want := "Sharko couldn't update this secret on the cluster."
	if decoded["error"] != want {
		t.Errorf("error = %q, want the canned sentence %q", decoded["error"], want)
	}
}

func TestSyncAddonValuesSecret_AuditEntryWritten(t *testing.T) {
	srv := newTestServer()
	srv.SetSecretReconciler(&fakeSecretReconciler{syncOutcome: "created"})
	router := NewRouter(srv, nil)

	req := withRole(syncReq("prod-eu", "datadog"), "operator")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	entries := srv.AuditLog().List(0)
	found := false
	for _, e := range entries {
		if e.Event == "addon_values_secret_sync_triggered" && e.Resource == "cluster:prod-eu/addon:datadog" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected an audit entry for addon_values_secret_sync_triggered on cluster:prod-eu/addon:datadog, got %+v", entries)
	}
}
