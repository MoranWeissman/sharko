package api

// cluster_secrets_refresh_test.go — task #152, story 152.A: the refresh
// endpoint reads Git, never a request body, and the definition-CRUD
// endpoints are gone. These tests pin the closed hole shut:
//
//   1. An API caller cannot introduce a secret definition — the POST/DELETE
//      /addon-secrets routes no longer exist, and hitting the old paths
//      changes nothing in the server.
//   2. A refresh carrying a hand-made body delivers nothing the body asked
//      for — the handler never reads the body; the only inputs that reach
//      the engine are the cluster name from the URL and the optional
//      ?addon= query value.
//   3. A refresh for an addon Git does not define is refused with a canned
//      sentence, and the refusal lands in the audit log.

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// postRefresh sends POST /clusters/{name}/secrets/refresh with the given
// extra query string and body, and returns the recorder.
func postRefresh(router http.Handler, cluster, query string, body []byte) *httptest.ResponseRecorder {
	path := "/api/v1/clusters/" + cluster + "/secrets/refresh" + query
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(http.MethodPost, path, nil)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// TestAddonSecretWriteEndpoints_Gone: the definition-CRUD surface is
// retired. Every old route 404s, and a POST that would once have planted a
// definition leaves the server's demo-only map untouched.
func TestAddonSecretWriteEndpoints_Gone(t *testing.T) {
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	def := []byte(`{"addon_name":"datadog","secret_name":"stolen-secret","namespace":"attacker-ns","keys":{"api-key":"secrets/attacker/path"}}`)

	cases := []struct {
		method, path string
		body         []byte
	}{
		{http.MethodPost, "/api/v1/addon-secrets", def},
		{http.MethodDelete, "/api/v1/addon-secrets/datadog", nil},
		{http.MethodGet, "/api/v1/addon-secrets", nil},
	}
	for _, tc := range cases {
		var req *http.Request
		if tc.body != nil {
			req = httptest.NewRequest(tc.method, tc.path, bytes.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
		} else {
			req = httptest.NewRequest(tc.method, tc.path, nil)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s %s: status = %d, want 404 — this endpoint must stay gone", tc.method, tc.path, w.Code)
		}
	}

	// The POST above must not have planted anything.
	srv.addonSecretDefsMu.RLock()
	defCount := len(srv.addonSecretDefs)
	srv.addonSecretDefsMu.RUnlock()
	if defCount != 0 {
		t.Fatalf("an API caller introduced %d definition(s) — the write path into addonSecretDefs must be gone", defCount)
	}
}

// TestRefreshClusterSecrets_IgnoresHandMadeBody: a body naming a backend
// path, a destination namespace, a Secret name, and a key list changes
// NOTHING — the engine is called with the cluster identity only, and the
// response echoes none of the body's content.
func TestRefreshClusterSecrets_IgnoresHandMadeBody(t *testing.T) {
	srv := newIsolatedTestServer(t)
	rec := &fakeReconciler{syncClusterRefreshed: []string{"git-defined-secret"}}
	srv.SetSecretReconciler(rec)
	router := NewRouter(srv, nil)

	body := []byte(`{
		"addon_name": "datadog",
		"secret_name": "attacker-chosen-secret",
		"namespace": "attacker-ns",
		"keys": {"api-key": "secrets/attacker/prod-master-key"}
	}`)

	w := postRefresh(router, "prod-eu", "", body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	// The engine saw the cluster name and an empty addon filter — nothing
	// else. There is no parameter through which the body COULD arrive.
	if len(rec.syncClusterCalls) != 1 {
		t.Fatalf("SyncCluster calls = %d, want 1", len(rec.syncClusterCalls))
	}
	if got := rec.syncClusterCalls[0]; got.cluster != "prod-eu" || got.addon != "" {
		t.Errorf("SyncCluster called with (%q, %q), want (prod-eu, \"\")", got.cluster, got.addon)
	}

	// Nothing the body asked for appears anywhere in the response.
	resp := w.Body.String()
	for _, smuggled := range []string{"attacker-chosen-secret", "attacker-ns", "secrets/attacker/prod-master-key"} {
		if strings.Contains(resp, smuggled) {
			t.Errorf("response echoes body content %q — the body must be ignored entirely", smuggled)
		}
	}
	if !strings.Contains(resp, "git-defined-secret") {
		t.Errorf("response should carry the git-backed result; body = %s", resp)
	}
}

// TestRefreshClusterSecrets_AddonNotInGit_RefusedAndAudited: naming an
// addon Git does not define refuses the call with a fixed sentence, and
// the refusal is audited.
func TestRefreshClusterSecrets_AddonNotInGit_RefusedAndAudited(t *testing.T) {
	srv := newIsolatedTestServer(t)
	rec := &fakeReconciler{
		syncClusterErr: errors.New("Git does not define an addon-values secret for this addon on this cluster — nothing to refresh"),
	}
	srv.SetSecretReconciler(rec)
	router := NewRouter(srv, nil)

	w := postRefresh(router, "prod-eu", "?addon=ghost-addon", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !strings.Contains(body["error"], "Git does not define an addon-values secret") {
		t.Errorf("error = %q, want the canned not-in-Git sentence", body["error"])
	}
	if !strings.Contains(body["error"], "ghost-addon") {
		t.Errorf("error = %q, should name the refused addon", body["error"])
	}

	// The refusal is on the audit trail.
	found := false
	for _, e := range srv.AuditLog().List(50) {
		if e.Event == "cluster_secret_refresh_refused" {
			found = true
			if e.Result == "success" {
				t.Errorf("refusal audit entry has result %q, want a failure result", e.Result)
			}
		}
	}
	if !found {
		t.Errorf("no cluster_secret_refresh_refused audit entry — the refusal must be audited")
	}
}

// TestRefreshClusterSecrets_UnknownCluster_Refused404: a cluster the Git
// managed-clusters list does not carry is refused, not silently emptied.
func TestRefreshClusterSecrets_UnknownCluster_Refused404(t *testing.T) {
	srv := newIsolatedTestServer(t)
	rec := &fakeReconciler{
		syncClusterErr: errors.New("this cluster is not in the managed clusters list in Git — nothing to refresh"),
	}
	srv.SetSecretReconciler(rec)
	router := NewRouter(srv, nil)

	w := postRefresh(router, "not-in-git", "", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "not in the managed clusters list in Git") {
		t.Errorf("body = %s, want the canned not-in-Git sentence", w.Body.String())
	}
}

// TestRefreshClusterSecrets_PartialFailure_207WithCannedNote: per-item
// failures surface as a 207 with secret NAMES and one fixed sentence —
// never raw provider/reconciler error text.
func TestRefreshClusterSecrets_PartialFailure_207WithCannedNote(t *testing.T) {
	srv := newIsolatedTestServer(t)
	rec := &fakeReconciler{
		syncClusterRefreshed: []string{"good-secret"},
		syncClusterFailed:    []string{"bad-secret"},
	}
	srv.SetSecretReconciler(rec)
	router := NewRouter(srv, nil)

	w := postRefresh(router, "prod-eu", "", nil)
	if w.Code != http.StatusMultiStatus {
		t.Fatalf("status = %d, want 207; body = %s", w.Code, w.Body.String())
	}
	var body map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["note"] == nil || body["note"] == "" {
		t.Errorf("207 body should carry the canned note; got %+v", body)
	}
}

// TestRefreshClusterSecrets_GitUnreadable_502Canned: a git read failure
// maps to a fixed sentence — the raw wrapped error never reaches the
// response.
func TestRefreshClusterSecrets_GitUnreadable_502Canned(t *testing.T) {
	srv := newIsolatedTestServer(t)
	rec := &fakeReconciler{
		syncClusterErr: errors.New("could not read the addon catalog or managed clusters list: dial tcp 10.0.0.1:443: connection refused"),
	}
	srv.SetSecretReconciler(rec)
	router := NewRouter(srv, nil)

	w := postRefresh(router, "prod-eu", "", nil)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body = %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "dial tcp") {
		t.Errorf("raw error text leaked into the response: %s", w.Body.String())
	}
}
