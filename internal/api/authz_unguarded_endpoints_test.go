package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

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
// handler can reach its 202 success path under an allowed role. The
// itemCallArgs / checkOutcome / syncOutcome fields (S4) let tests configure
// and assert exactly what CheckOne/SyncOne were called with and what they
// returned, without pulling in the real internal/secrets.Reconciler (which
// would need a live Git connection).
type fakeSecretReconciler struct {
	mu        sync.Mutex
	triggered int
	// checkedAll counts CheckAll calls (P1-A A3) — the read-only fleet-wide
	// pass "Refresh all" drives on this engine. Guarded by mu because the
	// handler runs it in a goroutine.
	checkedAll  int
	checkAllErr error

	checkOutcome string
	checkErr     error
	checkCalls   []itemCallArgs

	syncOutcome string
	syncErr     error
	syncCalls   []itemCallArgs

	lastItemOutcome   string
	lastItemOutcomeOK bool

	// lastError/lastErrorCluster/lastErrorAt (P1-B B2) let tests configure
	// the engine-level error the LastError/LastErrorCluster/LastErrorAt
	// trio reports — zero values mean "no error", same as a clean pass.
	lastError        string
	lastErrorCluster string
	lastErrorAt      time.Time
}

type itemCallArgs struct{ cluster, addon string }

func (f *fakeSecretReconciler) Trigger()               { f.triggered++ }
func (f *fakeSecretReconciler) GetStats() interface{}  { return map[string]int{} }
func (f *fakeSecretReconciler) LastRunTime() time.Time { return time.Time{} }
func (f *fakeSecretReconciler) LastError() string      { return f.lastError }
func (f *fakeSecretReconciler) LastErrorCluster() string {
	return f.lastErrorCluster
}
func (f *fakeSecretReconciler) LastErrorAt() time.Time  { return f.lastErrorAt }
func (f *fakeSecretReconciler) Interval() time.Duration { return 0 }
func (f *fakeSecretReconciler) LastItemChecked(_, _ string) (time.Time, bool) {
	return time.Time{}, false
}

func (f *fakeSecretReconciler) LastItemOutcome(_, _ string) (string, bool) {
	return f.lastItemOutcome, f.lastItemOutcomeOK
}

func (f *fakeSecretReconciler) LastItemError(_, _ string) (string, bool) {
	return "", false
}

func (f *fakeSecretReconciler) LastItemConsecutiveFailures(_, _ string) (int, bool) {
	return 0, false
}

func (f *fakeSecretReconciler) KnownItemCount() int {
	return 0
}

func (f *fakeSecretReconciler) CheckOne(_ context.Context, cluster, addon string) (string, error) {
	f.checkCalls = append(f.checkCalls, itemCallArgs{cluster, addon})
	return f.checkOutcome, f.checkErr
}

func (f *fakeSecretReconciler) SyncOne(_ context.Context, cluster, addon string) (string, error) {
	f.syncCalls = append(f.syncCalls, itemCallArgs{cluster, addon})
	return f.syncOutcome, f.syncErr
}

func (f *fakeSecretReconciler) CheckAll(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checkedAll++
	return f.checkAllErr
}

// checkAllCount reads the CheckAll counter under the lock.
func (f *fakeSecretReconciler) checkAllCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.checkedAll
}

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

// --- handlePutDefaultAddons (default-addons.update, operator+) ---
//
// v4-8-4 role gate audit finding: this handler had NO authz gate at all
// before this fix — any authenticated caller, viewer included, could PUT a
// new global default-addon set (affecting every future cluster
// registration). Fixed alongside the mechanical completeness test in
// authz_coverage_test.go; this regression test locks in the fix.

func TestPutDefaultAddons_ViewerForbidden(t *testing.T) {
	s := &Server{}
	req := withRole(httptest.NewRequest(http.MethodPut, "/api/v1/default-addons", nil), "viewer")
	rw := httptest.NewRecorder()
	s.handlePutDefaultAddons(rw, req)
	assert403(t, rw)
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
