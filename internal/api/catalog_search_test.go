package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MoranWeissman/sharko/internal/catalog"
)

// helper: build a Server + a stubbed ArtifactHub client + a counter so tests
// can assert "second call within TTL did not hit upstream."
func searchTestServer(t *testing.T, ahHandler http.HandlerFunc) (*Server, *int64, func()) {
	t.Helper()
	resetCatalogProxyStateForTest()

	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		ahHandler(w, r)
	}))

	client := catalog.NewArtifactHubClient(nil)
	client.BaseURL = srv.URL
	restore := setArtifactHubClientForTest(client)

	srvAPI := serverWithCatalog(t, testCatalog(t))
	cleanup := func() {
		restore()
		srv.Close()
		resetCatalogProxyStateForTest()
	}
	return srvAPI, &calls, cleanup
}

func TestHandleSearchCatalog_BlendsCuratedAndArtifactHub(t *testing.T) {
	srv, _, cleanup := searchTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"packages":[
			{"package_id":"x","name":"cert-manager-external","stars":42,
			 "repository":{"name":"some-third-party","kind":0,"verified_publisher":false}}
		]}`))
	})
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/search?q=cert", nil)
	rw := httptest.NewRecorder()
	srv.handleSearchCatalog(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rw.Code, rw.Body.String())
	}
	var resp catalogSearchResponse
	if err := json.Unmarshal(rw.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Curated) != 1 || resp.Curated[0].Name != "cert-manager" {
		t.Errorf("expected 1 curated cert-manager hit, got %+v", resp.Curated)
	}
	if len(resp.ArtifactHub) != 1 || resp.ArtifactHub[0].Name != "cert-manager-external" {
		t.Errorf("expected 1 artifacthub hit, got %+v", resp.ArtifactHub)
	}
	if resp.ArtifactHubError != "" {
		t.Errorf("expected no error, got %q", resp.ArtifactHubError)
	}
}

func TestHandleSearchCatalog_CacheHit(t *testing.T) {
	srv, calls, cleanup := searchTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"packages":[]}`))
	})
	defer cleanup()

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/search?q=prometheus", nil)
		rw := httptest.NewRecorder()
		srv.handleSearchCatalog(rw, req)
		if rw.Code != http.StatusOK {
			t.Fatalf("attempt %d: status = %d", i, rw.Code)
		}
	}
	if got := atomic.LoadInt64(calls); got != 1 {
		t.Errorf("upstream calls = %d, want 1 (cache should serve attempts 2+)", got)
	}
}

func TestHandleSearchCatalog_UpstreamErrorReturnsCuratedOnly(t *testing.T) {
	srv, _, cleanup := searchTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/search?q=cert", nil)
	rw := httptest.NewRecorder()
	srv.handleSearchCatalog(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d (want 200 — curated should still serve)", rw.Code)
	}
	var resp catalogSearchResponse
	_ = json.Unmarshal(rw.Body.Bytes(), &resp)
	if len(resp.Curated) == 0 {
		t.Errorf("expected curated hits even on upstream error")
	}
	if resp.ArtifactHubError != "server_error" {
		t.Errorf("artifacthub_error = %q, want server_error", resp.ArtifactHubError)
	}
	if resp.Stale {
		t.Errorf("stale should be false (no prior cached value)")
	}
}

func TestHandleSearchCatalog_StaleServeOnError(t *testing.T) {
	var failing atomic.Bool
	srv, _, cleanup := searchTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if failing.Load() {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"packages":[{"package_id":"x","name":"first-call","repository":{"name":"r","kind":0}}]}`))
	})
	defer cleanup()

	// First call populates the cache.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/search?q=prometheus", nil)
	srv.handleSearchCatalog(httptest.NewRecorder(), req)

	// Manually expire the entry so the next call goes upstream and fails.
	expireSearchCacheForTest()
	failing.Store(true)

	rw := httptest.NewRecorder()
	srv.handleSearchCatalog(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d", rw.Code)
	}
	if rw.Header().Get("X-Cache-Stale") != "true" {
		t.Errorf("missing X-Cache-Stale header")
	}
	var resp catalogSearchResponse
	_ = json.Unmarshal(rw.Body.Bytes(), &resp)
	if !resp.Stale {
		t.Errorf("stale flag not set")
	}
	if len(resp.ArtifactHub) != 1 || resp.ArtifactHub[0].Name != "first-call" {
		t.Errorf("expected stale value, got %+v", resp.ArtifactHub)
	}
}

func TestHandleSearchCatalog_EmptyQuery(t *testing.T) {
	srv := serverWithCatalog(t, testCatalog(t))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/search", nil)
	rw := httptest.NewRecorder()
	srv.handleSearchCatalog(rw, req)
	if rw.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rw.Code)
	}
	if !strings.Contains(rw.Body.String(), "q is required") {
		t.Errorf("body = %s", rw.Body.String())
	}
}
