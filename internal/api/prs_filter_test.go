// V125-1-6: handleListPRs filter + sort + limit tests.
//
// Verifies the new /api/v1/prs query semantics:
//   ?operation=<csv>   — filter to one or more canonical Operation codes
//   ?limit=N           — clamp result count (default 100, hard cap 500)
//   default sort       — created_at descending (newest first)
//
// We bypass the auth middleware by hitting the handler directly with a
// minimal Server. The prtracker is wired against an in-memory cmstore
// (k8s/client-go fake).

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/cmstore"
	"github.com/MoranWeissman/sharko/internal/prtracker"
)

// newPrsFilterTestServer builds a Server with a prtracker that holds the
// supplied PRs. Auth is left disabled (no users) so the request is
// allowed straight through.
func newPrsFilterTestServer(t *testing.T, prs []prtracker.PRInfo) *Server {
	t.Helper()
	client := fake.NewSimpleClientset()
	store := cmstore.NewStore(client, "default", "sharko-pending-prs")
	tracker := prtracker.NewTracker(store, func() prtracker.GitProvider { return nil }, func(audit.Entry) {})

	srv := &Server{}
	srv.SetPRTracker(tracker)

	for _, pr := range prs {
		if err := tracker.TrackPR(t.Context(), pr); err != nil {
			t.Fatalf("seed TrackPR(%d): %v", pr.PRID, err)
		}
	}
	return srv
}

func TestHandleListPRs_OperationCSVFilter(t *testing.T) {
	now := time.Now()
	srv := newPrsFilterTestServer(t, []prtracker.PRInfo{
		{PRID: 1, Operation: "register-cluster", Cluster: "prod-eu", LastStatus: "open", CreatedAt: now.Add(-3 * time.Minute)},
		{PRID: 2, Operation: "addon-add", Addon: "datadog", LastStatus: "open", CreatedAt: now.Add(-2 * time.Minute)},
		{PRID: 3, Operation: "addon-upgrade", Addon: "metrics-server", LastStatus: "open", CreatedAt: now.Add(-1 * time.Minute)},
		{PRID: 4, Operation: "init-repo", LastStatus: "open", CreatedAt: now},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/prs?operation=addon-add,addon-upgrade", nil)
	rr := httptest.NewRecorder()
	srv.handleListPRs(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200, body=%s", rr.Code, rr.Body.String())
	}
	var resp PRListResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.PRs) != 2 {
		t.Fatalf("expected 2 PRs, got %d (%v)", len(resp.PRs), resp.PRs)
	}

	// Default sort is newest-first → addon-upgrade (PR 3) leads addon-add (PR 2).
	if resp.PRs[0].PRID != 3 {
		t.Errorf("expected PR 3 first (newest), got %d", resp.PRs[0].PRID)
	}
	if resp.PRs[1].PRID != 2 {
		t.Errorf("expected PR 2 second, got %d", resp.PRs[1].PRID)
	}
}

func TestHandleListPRs_LimitClampsResults(t *testing.T) {
	now := time.Now()
	var seed []prtracker.PRInfo
	for i := 0; i < 5; i++ {
		seed = append(seed, prtracker.PRInfo{
			PRID:       100 + i,
			Operation:  "addon-upgrade",
			LastStatus: "open",
			CreatedAt:  now.Add(time.Duration(i) * time.Minute),
		})
	}
	srv := newPrsFilterTestServer(t, seed)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/prs?limit=2", nil)
	rr := httptest.NewRecorder()
	srv.handleListPRs(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	var resp PRListResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.PRs) != 2 {
		t.Fatalf("expected 2 PRs, got %d", len(resp.PRs))
	}
	// Limit field is surfaced so the FE can render a "View all on GitHub →"
	// escape hatch when row count == limit.
	if resp.Limit != 2 {
		t.Errorf("response Limit: got %d want 2", resp.Limit)
	}
	// Newest-first → highest IDs (104, 103).
	if resp.PRs[0].PRID != 104 || resp.PRs[1].PRID != 103 {
		t.Errorf("sort: got [%d, %d] want [104, 103]", resp.PRs[0].PRID, resp.PRs[1].PRID)
	}
}

func TestHandleListPRs_DefaultSortNewestFirst(t *testing.T) {
	now := time.Now()
	srv := newPrsFilterTestServer(t, []prtracker.PRInfo{
		{PRID: 11, Operation: "addon-add", LastStatus: "open", CreatedAt: now.Add(-2 * time.Hour)},
		{PRID: 22, Operation: "addon-add", LastStatus: "open", CreatedAt: now.Add(-1 * time.Hour)},
		{PRID: 33, Operation: "addon-add", LastStatus: "open", CreatedAt: now},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/prs", nil)
	rr := httptest.NewRecorder()
	srv.handleListPRs(rr, req)

	var resp PRListResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.PRs) != 3 {
		t.Fatalf("expected 3 PRs, got %d", len(resp.PRs))
	}
	want := []int{33, 22, 11}
	for i, w := range want {
		if resp.PRs[i].PRID != w {
			t.Errorf("position %d: got %d want %d", i, resp.PRs[i].PRID, w)
		}
	}
}
