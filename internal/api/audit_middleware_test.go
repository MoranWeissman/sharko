package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MoranWeissman/sharko/internal/readcache"
)

// TestResultFromStatus — V2-cleanup-85.2 regression coverage.
//
// 207 is inside the [200,300) range, so a naive range-first switch swallows
// it under the 2xx case before the `code == 207` arm is ever reached — every
// partial-success response (PR created but not merged, ArgoCD registered
// but Git failed) was mislabeled "success" in the audit log. resultFromStatus
// must test 207 before the 2xx catch-all.
func TestResultFromStatus(t *testing.T) {
	cases := []struct {
		code int
		want string
	}{
		{200, "success"},
		{201, "success"},
		{204, "success"},
		{207, "partial"},
		{400, "rejected"},
		{404, "rejected"},
		{409, "rejected"},
		{499, "rejected"},
		{500, "failure"},
		{502, "failure"},
		{503, "failure"},
	}
	for _, tc := range cases {
		if got := resultFromStatus(tc.code); got != tc.want {
			t.Errorf("resultFromStatus(%d) = %q, want %q", tc.code, got, tc.want)
		}
	}
}

// TestAuditMiddleware_InvalidatesReadCacheOnSuccessfulMutation is the perf
// M1 regression test for the invalidation choke point: any mutating
// (POST/PUT/PATCH/DELETE) request to an /api/ path that completes without
// a 4xx/5xx must clear the shared read cache (internal/readcache), even
// though the middleware never knows which of the six cached endpoints (if
// any) the mutation actually affects — see auditMiddleware's doc comment.
func TestAuditMiddleware_InvalidatesReadCacheOnSuccessfulMutation(t *testing.T) {
	srv := newTestServer()
	readcache.Set(srv.readCache, "some:cached:key", "stale-value")
	if srv.readCache.Len() != 1 {
		t.Fatalf("setup: readCache.Len() = %d, want 1", srv.readCache.Len())
	}

	handler := srv.auditMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/some-mutation", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", w.Code)
	}
	if srv.readCache.Len() != 0 {
		t.Errorf("readCache.Len() after a successful mutation = %d, want 0 (InvalidateAll should have cleared it)", srv.readCache.Len())
	}
}

// TestAuditMiddleware_DoesNotInvalidateReadCacheOnFailedMutation asserts
// the flip side: a mutation that fails (4xx/5xx) changed nothing, so the
// cache must survive — otherwise every rejected/failed write would pay a
// full cache-miss recompute for no reason.
func TestAuditMiddleware_DoesNotInvalidateReadCacheOnFailedMutation(t *testing.T) {
	srv := newTestServer()
	readcache.Set(srv.readCache, "some:cached:key", "still-fresh")

	handler := srv.auditMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/some-mutation", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	if v, ok := readcache.Get[string](srv.readCache, "some:cached:key"); !ok || v != "still-fresh" {
		t.Errorf("expected the cache entry to survive a failed (400) mutation, got ok=%v v=%q", ok, v)
	}
}

// TestAuditMiddleware_DoesNotInvalidateReadCacheOnGet asserts read-only
// requests never touch the cache-invalidation path — GETs short-circuit
// before the invalidation defer is even registered.
func TestAuditMiddleware_DoesNotInvalidateReadCacheOnGet(t *testing.T) {
	srv := newTestServer()
	readcache.Set(srv.readCache, "some:cached:key", "still-fresh")

	handler := srv.auditMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/some-read", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if v, ok := readcache.Get[string](srv.readCache, "some:cached:key"); !ok || v != "still-fresh" {
		t.Errorf("expected a GET to never invalidate the cache, got ok=%v v=%q", ok, v)
	}
}
