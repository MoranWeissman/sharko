package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// TestHandleCheckEnginePin_NoConnectionNoFreshness_Returns502 pins the
// pre-Story-3.4 behavior for the case the freshness scheduler cannot help:
// no active Git connection AND no cached background result.
func TestHandleCheckEnginePin_NoConnectionNoFreshness_Returns502(t *testing.T) {
	srv := newServerWithConnSvc(newConnectionServiceForTest(t))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/engine/pin", nil)
	rw := httptest.NewRecorder()
	srv.handleCheckEnginePin(rw, req)

	if rw.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body = %s", rw.Code, rw.Body.String())
	}
}

// TestHandleCheckEnginePin_NoConnectionFallsBackToFreshnessSnapshot proves
// the v4 wave 1 Story 3.4 fallback: when the live check can't run (no
// active Git connection), the endpoint serves the freshness scheduler's
// last background result instead of failing — "every fetch failure →
// stale-but-dated data shown, never an error page."
func TestHandleCheckEnginePin_NoConnectionFallsBackToFreshnessSnapshot(t *testing.T) {
	srv := newServerWithConnSvc(newConnectionServiceForTest(t))

	c := testCatalog(t)
	checkFn := func(ctx context.Context) (*catalog.EnginePinStatus, error) {
		return &catalog.EnginePinStatus{
			V4Repo:           true,
			BundledVersion:   "4.3.0",
			PinnedVersion:    "4.2.0",
			UpgradeAvailable: true,
			Message:          "engine upgrade available: 4.2.0 -> 4.3.0",
		}, nil
	}
	sched := catalog.NewFreshnessScheduler(c, &fakeFreshnessLister{}, checkFn, time.Hour)
	sched.RefreshForTest()
	srv.SetFreshness(sched)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/engine/pin", nil)
	rw := httptest.NewRecorder()
	srv.handleCheckEnginePin(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (served from the freshness snapshot); body = %s", rw.Code, rw.Body.String())
	}
	var body orchestrator.EnginePinCheckResult
	if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.UpgradeAvailable || body.BundledVersion != "4.3.0" {
		t.Errorf("unexpected result: %+v", body)
	}
}

// TestHandleCheckEnginePin_NoConnectionFreshnessAlsoErrored_Returns502
// asserts that a freshness snapshot recording ITS OWN error (e.g. the
// scheduler ran with no connection either) is not treated as usable cached
// data — the handler must still return the live-check error rather than an
// empty/zero-value 200.
func TestHandleCheckEnginePin_NoConnectionFreshnessAlsoErrored_Returns502(t *testing.T) {
	srv := newServerWithConnSvc(newConnectionServiceForTest(t))

	c := testCatalog(t)
	checkFn := func(ctx context.Context) (*catalog.EnginePinStatus, error) {
		return nil, context.DeadlineExceeded
	}
	sched := catalog.NewFreshnessScheduler(c, &fakeFreshnessLister{}, checkFn, time.Hour)
	sched.RefreshForTest()
	srv.SetFreshness(sched)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/engine/pin", nil)
	rw := httptest.NewRecorder()
	srv.handleCheckEnginePin(rw, req)

	if rw.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (no usable cached result either); body = %s", rw.Code, rw.Body.String())
	}
}
