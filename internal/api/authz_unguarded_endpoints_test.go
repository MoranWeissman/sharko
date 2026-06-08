package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/operations"
)

// V2-cleanup-21 (decision #7) — five write endpoints that previously skipped
// the role check now enforce the permission already defined for each action.
//
// These tests exercise the authz path the way the real auth middleware feeds
// it: the middleware authenticates a request and stamps X-Sharko-User +
// X-Sharko-Role, which authz.Require reads. Setting those headers here puts the
// server in "auth configured" mode for the request, so a viewer is denied and
// the permitted role is allowed — the path is genuinely exercised, not the
// no-users bypass. (A request with NO X-Sharko-User and NO X-Sharko-Role is
// the "auth not configured" mode that lets everything through; we never use
// that mode in these tests.)

// fakeSecretReconciler satisfies the SecretReconciler interface so the trigger
// handler can reach its 202 success path under an allowed role.
type fakeSecretReconciler struct{ triggered int }

func (f *fakeSecretReconciler) Trigger()            { f.triggered++ }
func (f *fakeSecretReconciler) GetStats() interface{} { return map[string]int{} }

// assert403 decodes the body and asserts a clean JSON 403.
func assert403(t *testing.T, rw *httptest.ResponseRecorder) {
	t.Helper()
	if rw.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", rw.Code, rw.Body.String())
	}
	var errBody map[string]interface{}
	if err := json.Unmarshal(rw.Body.Bytes(), &errBody); err != nil {
		t.Fatalf("decode 403 body: %v; body = %s", err, rw.Body.String())
	}
	if errBody["error"] == nil {
		t.Errorf("403 body missing \"error\" key; got %+v", errBody)
	}
}

func withRole(req *http.Request, role string) *http.Request {
	req.Header.Set("X-Sharko-User", role+"-user")
	req.Header.Set("X-Sharko-Role", role)
	return req
}

// --- handleTriggerReconcile (reconciler.trigger, operator+) ---

func TestTriggerReconcile_ViewerForbidden(t *testing.T) {
	s := &Server{secretReconciler: &fakeSecretReconciler{}}
	req := withRole(httptest.NewRequest(http.MethodPost, "/api/v1/secrets/reconcile", nil), "viewer")
	rw := httptest.NewRecorder()
	s.handleTriggerReconcile(rw, req)
	assert403(t, rw)
}

func TestTriggerReconcile_OperatorAccepted(t *testing.T) {
	rec := &fakeSecretReconciler{}
	s := &Server{secretReconciler: rec}
	req := withRole(httptest.NewRequest(http.MethodPost, "/api/v1/secrets/reconcile", nil), "operator")
	rw := httptest.NewRecorder()
	s.handleTriggerReconcile(rw, req)
	if rw.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %s", rw.Code, rw.Body.String())
	}
	if rec.triggered != 1 {
		t.Errorf("reconciler triggered %d times, want 1", rec.triggered)
	}
}

// --- handleOperationHeartbeat (init, operator+) ---

func TestOperationHeartbeat_ViewerForbidden(t *testing.T) {
	store := operations.NewStore()
	sess := store.Create("init", []string{"step"})
	s := &Server{opsStore: store}
	req := withRole(httptest.NewRequest(http.MethodPost, "/api/v1/operations/"+sess.ID+"/heartbeat", nil), "viewer")
	req.SetPathValue("id", sess.ID)
	rw := httptest.NewRecorder()
	s.handleOperationHeartbeat(rw, req)
	assert403(t, rw)
}

func TestOperationHeartbeat_OperatorOK(t *testing.T) {
	store := operations.NewStore()
	sess := store.Create("init", []string{"step"})
	s := &Server{opsStore: store}
	req := withRole(httptest.NewRequest(http.MethodPost, "/api/v1/operations/"+sess.ID+"/heartbeat", nil), "operator")
	req.SetPathValue("id", sess.ID)
	rw := httptest.NewRecorder()
	s.handleOperationHeartbeat(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rw.Code, rw.Body.String())
	}
}

// --- handleCancelOperation (init, operator+) ---

func TestCancelOperation_ViewerForbidden(t *testing.T) {
	store := operations.NewStore()
	sess := store.Create("init", []string{"step"})
	s := &Server{opsStore: store}
	req := withRole(httptest.NewRequest(http.MethodPost, "/api/v1/operations/"+sess.ID+"/cancel", nil), "viewer")
	req.SetPathValue("id", sess.ID)
	rw := httptest.NewRecorder()
	s.handleCancelOperation(rw, req)
	assert403(t, rw)
}

func TestCancelOperation_OperatorOK(t *testing.T) {
	store := operations.NewStore()
	sess := store.Create("init", []string{"step"})
	s := &Server{opsStore: store}
	req := withRole(httptest.NewRequest(http.MethodPost, "/api/v1/operations/"+sess.ID+"/cancel", nil), "operator")
	req.SetPathValue("id", sess.ID)
	rw := httptest.NewRecorder()
	s.handleCancelOperation(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rw.Code, rw.Body.String())
	}
}

// --- handleReprobeArtifactHub (catalog.sources.refresh, admin) ---

func TestReprobeArtifactHub_ViewerForbidden(t *testing.T) {
	s := &Server{}
	req := withRole(httptest.NewRequest(http.MethodPost, "/api/v1/catalog/reprobe", nil), "viewer")
	rw := httptest.NewRecorder()
	s.handleReprobeArtifactHub(rw, req)
	assert403(t, rw)
}

func TestReprobeArtifactHub_OperatorForbidden(t *testing.T) {
	// catalog.sources.refresh is admin-only; an operator must also be denied.
	s := &Server{}
	req := withRole(httptest.NewRequest(http.MethodPost, "/api/v1/catalog/reprobe", nil), "operator")
	rw := httptest.NewRecorder()
	s.handleReprobeArtifactHub(rw, req)
	assert403(t, rw)
}

func TestReprobeArtifactHub_AdminAllowed(t *testing.T) {
	resetCatalogProxyStateForTest()
	// Point the shared ArtifactHub client at a local stub so the probe is
	// deterministic and offline.
	ah := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ah.Close()
	client := catalog.NewArtifactHubClient(nil)
	client.BaseURL = ah.URL
	restore := setArtifactHubClientForTest(client)
	defer restore()
	defer resetCatalogProxyStateForTest()

	s := &Server{}
	req := withRole(httptest.NewRequest(http.MethodPost, "/api/v1/catalog/reprobe", nil), "admin")
	rw := httptest.NewRecorder()
	s.handleReprobeArtifactHub(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (admin passes the gate); body = %s", rw.Code, rw.Body.String())
	}
	var resp catalogReprobeResponse
	if err := json.Unmarshal(rw.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body = %s", err, rw.Body.String())
	}
	if !resp.Reachable {
		t.Errorf("Reachable = false, want true (stub returns 200)")
	}
}

// --- handleMarkAllNotificationsRead (reconciler.trigger, operator+) ---

func TestMarkAllNotificationsRead_ViewerForbidden(t *testing.T) {
	s := &Server{}
	req := withRole(httptest.NewRequest(http.MethodPost, "/api/v1/notifications/read-all", nil), "viewer")
	rw := httptest.NewRecorder()
	s.handleMarkAllNotificationsRead(rw, req)
	assert403(t, rw)
}

func TestMarkAllNotificationsRead_OperatorOK(t *testing.T) {
	// notificationStore nil is tolerated by the handler; the assertion under
	// test is that an operator passes the authz gate and reaches 200.
	s := &Server{}
	req := withRole(httptest.NewRequest(http.MethodPost, "/api/v1/notifications/read-all", nil), "operator")
	rw := httptest.NewRecorder()
	s.handleMarkAllNotificationsRead(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rw.Code, rw.Body.String())
	}
}
