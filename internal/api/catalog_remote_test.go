package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/MoranWeissman/sharko/internal/catalog"
)

func remoteTestServer(t *testing.T, ahHandler http.HandlerFunc) (*Server, *int64, func()) {
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

func TestHandleGetRemotePackage_OK(t *testing.T) {
	srv, _, cleanup := remoteTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"package_id":"a","name":"cert-manager",
			"version":"1.20.2","license":"Apache-2.0",
			"repository":{"name":"jetstack","kind":0,"verified_publisher":true}}`))
	})
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/remote/jetstack/cert-manager", nil)
	req.SetPathValue("repo", "jetstack")
	req.SetPathValue("name", "cert-manager")
	rw := httptest.NewRecorder()
	srv.handleGetRemotePackage(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rw.Code, rw.Body.String())
	}
	var resp catalogRemotePackageResponse
	if err := json.Unmarshal(rw.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Package == nil || resp.Package.Name != "cert-manager" {
		t.Errorf("package = %+v", resp.Package)
	}
	if resp.Stale {
		t.Errorf("stale should be false")
	}
}

func TestHandleGetRemotePackage_404Passthrough(t *testing.T) {
	srv, _, cleanup := remoteTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/remote/x/y", nil)
	req.SetPathValue("repo", "x")
	req.SetPathValue("name", "y")
	rw := httptest.NewRecorder()
	srv.handleGetRemotePackage(rw, req)
	if rw.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rw.Code)
	}
}

func TestHandleGetRemotePackage_StaleServeOn5xx(t *testing.T) {
	var failing atomic.Bool
	srv, _, cleanup := remoteTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if failing.Load() {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"package_id":"a","name":"cached-name","repository":{"name":"r","kind":0}}`))
	})
	defer cleanup()

	makeReq := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/remote/jetstack/cert-manager", nil)
		r.SetPathValue("repo", "jetstack")
		r.SetPathValue("name", "cert-manager")
		return r
	}

	// Prime cache.
	srv.handleGetRemotePackage(httptest.NewRecorder(), makeReq())
	expirePackageCacheForTest()
	failing.Store(true)

	rw := httptest.NewRecorder()
	srv.handleGetRemotePackage(rw, makeReq())
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d", rw.Code)
	}
	if rw.Header().Get("X-Cache-Stale") != "true" {
		t.Errorf("missing X-Cache-Stale header")
	}
	var resp catalogRemotePackageResponse
	_ = json.Unmarshal(rw.Body.Bytes(), &resp)
	if !resp.Stale || resp.Package == nil || resp.Package.Name != "cached-name" {
		t.Errorf("expected stale cached value, got %+v", resp)
	}
}

func TestHandleGetRemotePackage_502WhenNoCacheAndUpstream5xx(t *testing.T) {
	srv, _, cleanup := remoteTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/remote/jetstack/cert-manager", nil)
	req.SetPathValue("repo", "jetstack")
	req.SetPathValue("name", "cert-manager")
	rw := httptest.NewRecorder()
	srv.handleGetRemotePackage(rw, req)

	if rw.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rw.Code)
	}
}

func TestHandleGetRemotePackage_BadInput(t *testing.T) {
	srv := serverWithCatalog(t, testCatalog(t))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/remote/", nil)
	req.SetPathValue("repo", "")
	req.SetPathValue("name", "")
	rw := httptest.NewRecorder()
	srv.handleGetRemotePackage(rw, req)
	if rw.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rw.Code)
	}
}

func TestHandleReprobeArtifactHub_Reachable(t *testing.T) {
	srv, _, cleanup := remoteTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/catalog/reprobe", nil)
	rw := httptest.NewRecorder()
	srv.handleReprobeArtifactHub(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d", rw.Code)
	}
	var resp catalogReprobeResponse
	if err := json.Unmarshal(rw.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Reachable {
		t.Errorf("expected reachable=true, got %+v", resp)
	}
}

func TestHandleReprobeArtifactHub_Unreachable(t *testing.T) {
	srv, _, cleanup := remoteTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/catalog/reprobe", nil)
	rw := httptest.NewRecorder()
	srv.handleReprobeArtifactHub(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d", rw.Code)
	}
	var resp catalogReprobeResponse
	_ = json.Unmarshal(rw.Body.Bytes(), &resp)
	if resp.Reachable {
		t.Errorf("expected reachable=false")
	}
	if resp.LastError == "" {
		t.Errorf("expected last_error to be set")
	}
}
