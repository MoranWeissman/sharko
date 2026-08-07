package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/secrets"
)

// orphaned_secret_test.go — DELETE
// /clusters/{name}/orphaned-secrets/{namespace}/{secret} (leftover-secrets
// S1). Same pinned-contract shape as addon_secret_single_test.go's Refresh/
// Sync tests: 403 for a viewer, 503 with no engine wired, an honest status
// per refusal reason, and an audit entry on success that never carries a
// secret value.

func deleteOrphanReq(cluster, namespace, name string) *http.Request {
	req := httptest.NewRequest(http.MethodDelete,
		"/api/v1/clusters/"+cluster+"/orphaned-secrets/"+namespace+"/"+name, nil)
	return req
}

func TestDeleteOrphanedSecret_ViewerForbidden(t *testing.T) {
	srv := newTestServer()
	srv.SetSecretReconciler(&fakeReconciler{})
	router := NewRouter(srv, nil)

	req := withRole(deleteOrphanReq("prod-eu", "data", "old-redis-secret"), "viewer")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert403(t, w)
}

func TestDeleteOrphanedSecret_NoReconciler503(t *testing.T) {
	srv := newTestServer()
	router := NewRouter(srv, nil)

	req := withRole(deleteOrphanReq("prod-eu", "data", "old-redis-secret"), "operator")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body = %s", w.Code, w.Body.String())
	}
}

func TestDeleteOrphanedSecret_OperatorSuccess(t *testing.T) {
	srv := newTestServer()
	rec := &fakeReconciler{}
	srv.SetSecretReconciler(rec)
	router := NewRouter(srv, nil)

	req := withRole(deleteOrphanReq("prod-eu", "data", "old-redis-secret"), "operator")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	if len(rec.deleteOrphanCalls) != 1 || rec.deleteOrphanCalls[0] != (orphanDeleteCallArgs{"prod-eu", "data", "old-redis-secret"}) {
		t.Fatalf("DeleteOrphanedSecret called with %+v, want exactly one call for (prod-eu, data, old-redis-secret)", rec.deleteOrphanCalls)
	}

	var body orphanedSecretDeleteResult
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Status != "deleted" || body.Cluster != "prod-eu" || body.Namespace != "data" || body.Name != "old-redis-secret" {
		t.Errorf("body = %+v, want status=deleted cluster=prod-eu namespace=data name=old-redis-secret", body)
	}
	if body.Message == "" {
		t.Error("expected a plain-English message")
	}
}

func TestDeleteOrphanedSecret_AuditEntryWritten_NoSecretValue(t *testing.T) {
	srv := newTestServer()
	srv.SetSecretReconciler(&fakeReconciler{})
	router := NewRouter(srv, nil)

	req := withRole(deleteOrphanReq("prod-eu", "data", "old-redis-secret"), "operator")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	entries := srv.AuditLog().List(0)
	found := false
	for _, e := range entries {
		if e.Event != "orphaned_secret_deleted" {
			continue
		}
		found = true
		if e.Resource != "cluster:prod-eu" {
			t.Errorf("audit resource = %q, want cluster:prod-eu", e.Resource)
		}
		if e.Detail != "deleted leftover secret data/old-redis-secret" {
			t.Errorf("audit detail = %q, want %q", e.Detail, "deleted leftover secret data/old-redis-secret")
		}
		// S9/"Sharko may describe the delivery, never the secret" — the
		// whole entry, marshalled, must never contain anything that looks
		// like a secret's own content. There is no secret value anywhere
		// in this test's inputs, so this is really asserting the entry
		// only ever carries cluster+namespace+name+addon-shaped facts.
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("marshal audit entry: %v", err)
		}
		if strings.Contains(string(raw), "password") || strings.Contains(string(raw), "api-key") {
			t.Errorf("audit entry looks like it leaked secret-shaped content: %s", raw)
		}
	}
	if !found {
		t.Fatal("no orphaned_secret_deleted audit entry was written")
	}
}

func TestDeleteOrphanedSecret_UnknownOrphan404(t *testing.T) {
	srv := newTestServer()
	srv.SetSecretReconciler(&fakeReconciler{deleteOrphanErr: secrets.ErrOrphanUnknown})
	router := NewRouter(srv, nil)

	req := withRole(deleteOrphanReq("prod-eu", "data", "old-redis-secret"), "operator")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", w.Code, w.Body.String())
	}
}

func TestDeleteOrphanedSecret_Reclaimed409(t *testing.T) {
	srv := newTestServer()
	srv.SetSecretReconciler(&fakeReconciler{deleteOrphanErr: secrets.ErrOrphanReclaimed})
	router := NewRouter(srv, nil)

	req := withRole(deleteOrphanReq("prod-eu", "data", "old-redis-secret"), "operator")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body = %s", w.Code, w.Body.String())
	}
}

func TestDeleteOrphanedSecret_Refused409(t *testing.T) {
	srv := newTestServer()
	srv.SetSecretReconciler(&fakeReconciler{deleteOrphanErr: secrets.ErrOrphanRefused})
	router := NewRouter(srv, nil)

	req := withRole(deleteOrphanReq("prod-eu", "data", "old-redis-secret"), "operator")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body = %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// GET /system/managed-secrets — orphaned rows (leftover-secrets S1)
// ---------------------------------------------------------------------------

func TestHandleGetManagedSecrets_OrphanedSecrets_ArrayAndRowsPopulated(t *testing.T) {
	srv := newTestServer()
	checkedAt := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	srv.SetSecretReconciler(&fakeReconciler{
		orphanedSecrets: []models.OrphanedSecret{
			{Cluster: "prod-eu", Namespace: "data", Name: "old-redis-secret", Addon: "redis", LastChecked: checkedAt},
		},
	})
	router := NewRouter(srv, nil)

	req := withRole(httptest.NewRequest(http.MethodGet, "/api/v1/system/managed-secrets", nil), "viewer")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	var resp managedSecretsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(resp.OrphanedSecrets) != 1 {
		t.Fatalf("orphaned_secrets = %+v, want exactly 1 row", resp.OrphanedSecrets)
	}
	o := resp.OrphanedSecrets[0]
	if o.Cluster != "prod-eu" || o.SecretNamespace != "data" || o.SecretName != "old-redis-secret" || o.Addon != "redis" || o.State != "orphaned" {
		t.Errorf("orphaned row = %+v, want cluster=prod-eu namespace=data name=old-redis-secret addon=redis state=orphaned", o)
	}
	if o.LastChecked != "2026-08-01T12:00:00Z" {
		t.Errorf("LastChecked = %q, want 2026-08-01T12:00:00Z", o.LastChecked)
	}

	// Merged into Rows too, tagged Kind=values, State=orphaned.
	found := false
	for _, r := range resp.Rows {
		if r.Kind == managedSecretKindValues && r.State == "orphaned" && r.Cluster == "prod-eu" && r.Addon == "redis" {
			found = true
			if r.SelfHeals {
				t.Error("an orphaned row must never report self_heals=true — nothing claims it, so nothing converges it")
			}
		}
	}
	if !found {
		t.Errorf("no orphaned row found in the merged Rows array: %+v", resp.Rows)
	}
}

// TestBuildManagedSecretRows_OrphanedRankPosition pins locked decision #5's
// exact ordering — missing, out_of_sync, orphaned, foreign, unknown,
// in_sync — with a real orphaned row in the mix, at the buildManagedSecretRows
// level (no live connection needed, unlike the full handler path).
func TestBuildManagedSecretRows_OrphanedRankPosition(t *testing.T) {
	values := []addonValuesSecretRow{
		{Cluster: "c-out-of-sync", Addon: "a", State: "out_of_sync"},
		{Cluster: "c-foreign", Addon: "a", State: "foreign"},
		{Cluster: "c-in-sync", Addon: "a", State: "in_sync"},
	}
	orphaned := []orphanedSecretRow{
		{Cluster: "c-orphaned", Addon: "redis", State: "orphaned"},
	}

	rows := buildManagedSecretRows(nil, values, orphaned)

	states := make([]string, 0, len(rows))
	for _, r := range rows {
		states = append(states, r.State)
	}
	want := []string{"out_of_sync", "orphaned", "foreign", "in_sync"}
	if len(states) != len(want) {
		t.Fatalf("states = %v, want %v", states, want)
	}
	for i, w := range want {
		if states[i] != w {
			t.Errorf("states[%d] = %q, want %q (full order: %v)", i, states[i], w, states)
		}
	}
}

func TestHandleGetManagedSecrets_OrphanedSecrets_StateFilter(t *testing.T) {
	srv := newTestServer()
	srv.SetSecretReconciler(&fakeReconciler{
		orphanedSecrets: []models.OrphanedSecret{
			{Cluster: "prod-eu", Namespace: "data", Name: "old-redis-secret", Addon: "redis", LastChecked: time.Now()},
		},
		itemOutcome: map[[2]string]string{
			{"prod-eu", "datadog"}: "out_of_sync",
		},
	})
	router := NewRouter(srv, nil)

	req := withRole(httptest.NewRequest(http.MethodGet, "/api/v1/system/managed-secrets?state=orphaned", nil), "viewer")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	var resp managedSecretsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(resp.Rows) != 1 || resp.Rows[0].State != "orphaned" {
		t.Fatalf("?state=orphaned rows = %+v, want exactly one orphaned row", resp.Rows)
	}
	// The two per-kind/whole arrays stay full and unfiltered — E1's
	// backward-compatibility rule, unchanged by this addition.
	if len(resp.OrphanedSecrets) != 1 {
		t.Errorf("orphaned_secrets array = %+v, want unfiltered (still 1)", resp.OrphanedSecrets)
	}
}
