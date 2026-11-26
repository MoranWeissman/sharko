package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/audit"
)

// A connection test is written in the audit log as a test of that cluster,
// with the cluster name, and as failed when it failed. Walk 6 found a test of a
// cluster that does not exist recorded as "clusters_created", "success", with
// no cluster name: every early return of the handler left the entry blank, so
// the log fell back to the name made from the route (POST on /clusters/...).

func lastAuditEntry(t *testing.T, srv *Server) audit.Entry {
	t.Helper()
	got := srv.auditLog.List(10)
	if len(got) == 0 {
		t.Fatal("no audit entry was written for the connection test")
	}
	return got[0]
}

func assertTestedAndFailed(t *testing.T, e audit.Entry, cluster string) {
	t.Helper()
	if e.Event != "cluster_tested" {
		t.Errorf("event = %q, want %q", e.Event, "cluster_tested")
	}
	if e.Event == "clusters_created" || strings.Contains(e.Event, "created") {
		t.Errorf("a connection test must never be recorded as a creation, got %q", e.Event)
	}
	if e.Resource != "cluster:"+cluster {
		t.Errorf("resource = %q, want %q", e.Resource, "cluster:"+cluster)
	}
	if e.Result != "failure" {
		t.Errorf("result = %q, want %q (the test failed)", e.Result, "failure")
	}
	if e.Detail != "result=fail" {
		t.Errorf("detail = %q, want %q", e.Detail, "result=fail")
	}
}

func TestClusterTest_Audit_CredentialsMissing_IsATestThatFailed(t *testing.T) {
	srv, _, _ := newCredsRoutingServer(t)
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/walk6-nosuch/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	assertTestedAndFailed(t, lastAuditEntry(t, srv), "walk6-nosuch")
}

func TestClusterTest_Audit_NoSecretsBackend_IsATestThatFailed(t *testing.T) {
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/any-cluster/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	assertTestedAndFailed(t, lastAuditEntry(t, srv), "any-cluster")
}

// A cluster whose credentials resolve but cannot be reached: HTTP 200 with
// success=false. The log must say failed, not "Succeeded".
func TestClusterTest_Audit_Unreachable_IsATestThatFailed(t *testing.T) {
	srv, _, _ := newCredsRoutingServer(t)
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/kind-inline/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	assertTestedAndFailed(t, lastAuditEntry(t, srv), "kind-inline")
}
