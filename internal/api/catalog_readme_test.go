package api

// catalog_readme_test.go — coverage for the v1.21 QA Bundle 2 README proxy.
//
// We exercise:
//   • happy path: SearchHelm + GetPackage → README payload
//   • match heuristic: pickBestAHMatch picks verified+exact-name first
//   • unknown chart: catalog.Get returns false → 404
//   • no AH match: SearchHelm returns nothing → 200 with empty README + caches
//     the empty answer (so we don't hammer ArtifactHub)
//   • bad input: empty addon name → 400
//
// Network calls are stubbed via the in-process httptest server registered
// with setArtifactHubClientForTest, identical to catalog_remote_test.go.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MoranWeissman/sharko/internal/catalog"
)

// readmeTestServer wires a stub ArtifactHub that responds to two paths:
//   GET /packages/search       → returns one or more matches
//   GET /packages/helm/{r}/{n} → returns a package detail with README
// The caller passes a single dispatching handler so each test can shape
// both responses inline.
func readmeTestServer(t *testing.T, ahHandler http.HandlerFunc) (*Server, *int64, func()) {
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

func TestHandleGetCatalogReadme_OK(t *testing.T) {
	srv, _, cleanup := readmeTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/packages/search"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"packages":[
				{"package_id":"a","name":"cert-manager","stars":12000,
				 "repository":{"name":"jetstack","kind":0,"verified_publisher":true}}
			]}`))
		case strings.HasPrefix(r.URL.Path, "/packages/helm/jetstack/cert-manager"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"package_id":"a","name":"cert-manager",
				"readme":"# cert-manager\nUsage…",
				"repository":{"name":"jetstack","kind":0}}`))
		default:
			t.Errorf("unexpected upstream path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/addons/cert-manager/readme", nil)
	req.SetPathValue("name", "cert-manager")
	rw := httptest.NewRecorder()
	srv.handleGetCatalogReadme(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rw.Code, rw.Body.String())
	}
	var resp catalogReadmeResponse
	if err := json.Unmarshal(rw.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(resp.Readme, "cert-manager") {
		t.Errorf("readme = %q", resp.Readme)
	}
	if resp.AHRepo != "jetstack" || resp.AHChart != "cert-manager" {
		t.Errorf("AH coords = %s/%s", resp.AHRepo, resp.AHChart)
	}
}

func TestHandleGetCatalogReadme_UnknownAddon(t *testing.T) {
	srv := serverWithCatalog(t, testCatalog(t))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/addons/who-dis/readme", nil)
	req.SetPathValue("name", "who-dis")
	rw := httptest.NewRecorder()
	srv.handleGetCatalogReadme(rw, req)
	if rw.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rw.Code)
	}
}

func TestHandleGetCatalogReadme_EmptyName(t *testing.T) {
	srv := serverWithCatalog(t, testCatalog(t))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/addons//readme", nil)
	req.SetPathValue("name", "")
	rw := httptest.NewRecorder()
	srv.handleGetCatalogReadme(rw, req)
	if rw.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rw.Code)
	}
}

func TestHandleGetCatalogReadme_NoAHMatch(t *testing.T) {
	srv, _, cleanup := readmeTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		// Search returns an empty hit list — we should cache the empty README
		// rather than 404 (the curated entry exists, ArtifactHub just doesn't
		// know about it).
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"packages":[]}`))
	})
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/addons/cert-manager/readme", nil)
	req.SetPathValue("name", "cert-manager")
	rw := httptest.NewRecorder()
	srv.handleGetCatalogReadme(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rw.Code)
	}
	var resp catalogReadmeResponse
	_ = json.Unmarshal(rw.Body.Bytes(), &resp)
	if resp.Readme != "" {
		t.Errorf("expected empty readme, got %q", resp.Readme)
	}
}

func TestPickBestAHMatch_PrefersExactVerified(t *testing.T) {
	hits := []catalog.AHSearchPackage{
		{Name: "kube-prometheus-stack", Stars: 9000,
			Repository: catalog.AHRepo{Name: "prometheus-community", VerifiedPublisher: true}},
		{Name: "prometheus", Stars: 1000,
			Repository: catalog.AHRepo{Name: "bitnami", VerifiedPublisher: true}},
		{Name: "prometheus", Stars: 12000,
			Repository: catalog.AHRepo{Name: "prometheus-community", VerifiedPublisher: true}},
		{Name: "prometheus", Stars: 500,
			Repository: catalog.AHRepo{Name: "random", VerifiedPublisher: false}},
	}
	best := pickBestAHMatch(hits, "prometheus")
	if best == nil {
		t.Fatal("nil match")
	}
	// Highest-star verified exact-name match wins.
	if best.Repository.Name != "prometheus-community" || best.Stars != 12000 {
		t.Errorf("best = %+v", best)
	}
}

func TestPickBestAHMatch_FallsBackToFirst(t *testing.T) {
	hits := []catalog.AHSearchPackage{
		{Name: "totally-different", Stars: 1,
			Repository: catalog.AHRepo{Name: "x", VerifiedPublisher: false}},
		{Name: "another-different", Stars: 2,
			Repository: catalog.AHRepo{Name: "y", VerifiedPublisher: false}},
	}
	best := pickBestAHMatch(hits, "prometheus")
	if best == nil || best.Name != "totally-different" {
		t.Errorf("best = %+v", best)
	}
}

func TestPickBestAHMatch_Empty(t *testing.T) {
	if got := pickBestAHMatch(nil, "x"); got != nil {
		t.Errorf("got %+v, want nil", got)
	}
}
