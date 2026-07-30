package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
)

// fakeDeltaGitProvider is a minimal gitprovider.GitProvider whose only
// meaningful method is GetFileContent, returning either a canned body for
// catalog/addons.yaml or a wrapped gitprovider.ErrFileNotFound ("missing
// means empty" — design doc D16). Every other method is a harmless no-op;
// the handlers under test never call them.
type fakeDeltaGitProvider struct {
	deltaBody []byte // nil => file not found
}

func (f *fakeDeltaGitProvider) GetFileContent(_ context.Context, path, _ string) ([]byte, error) {
	if path == config.AddonCatalogDeltaPath && f.deltaBody != nil {
		return f.deltaBody, nil
	}
	return nil, gitprovider.ErrFileNotFound
}
func (f *fakeDeltaGitProvider) ListDirectory(context.Context, string, string) ([]string, error) {
	return nil, nil
}
func (f *fakeDeltaGitProvider) ListPullRequests(context.Context, string) ([]gitprovider.PullRequest, error) {
	return nil, nil
}
func (f *fakeDeltaGitProvider) TestConnection(context.Context) error               { return nil }
func (f *fakeDeltaGitProvider) CreateBranch(context.Context, string, string) error { return nil }
func (f *fakeDeltaGitProvider) CreateOrUpdateFile(context.Context, string, []byte, string, string) error {
	return nil
}
func (f *fakeDeltaGitProvider) BatchCreateFiles(context.Context, map[string][]byte, string, string) error {
	return nil
}
func (f *fakeDeltaGitProvider) DeleteFile(context.Context, string, string, string) error { return nil }
func (f *fakeDeltaGitProvider) CreatePullRequest(context.Context, string, string, string, string) (*gitprovider.PullRequest, error) {
	return &gitprovider.PullRequest{ID: 1, URL: "https://example.com/pr/1"}, nil
}
func (f *fakeDeltaGitProvider) MergePullRequest(context.Context, int) error { return nil }
func (f *fakeDeltaGitProvider) GetPullRequestStatus(context.Context, int) (string, error) {
	return "open", nil
}
func (f *fakeDeltaGitProvider) DeleteBranch(context.Context, string) error { return nil }

// serverWithCatalogAndDelta wires a Server with a curated catalog and a Git
// provider override standing in for the caller's active connection — enough
// to exercise the merged-view handlers without a real Git backend.
func serverWithCatalogAndDelta(t *testing.T, c *catalog.Catalog, gp gitprovider.GitProvider) *Server {
	t.Helper()
	connSvc := newConnectionServiceForTest(t)
	connSvc.SetGitProviderOverride(gp)
	s := &Server{connSvc: connSvc}
	s.SetCatalog(c)
	return s
}

func TestHandleListMergedCatalogDelta_NoDeltaFileReturnsCuratedUntouched(t *testing.T) {
	srv := serverWithCatalogAndDelta(t, testCatalog(t), &fakeDeltaGitProvider{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/delta/addons", nil)
	rw := httptest.NewRecorder()
	srv.handleListMergedCatalogDelta(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rw.Code, rw.Body.String())
	}
	var resp mergedCatalogListResponse
	if err := json.Unmarshal(rw.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 2 { // testCatalog fixture has cert-manager + grafana
		t.Errorf("Total = %d, want 2", resp.Total)
	}
	for _, a := range resp.Addons {
		if a.Origin != catalog.OriginCurated {
			t.Errorf("%s: Origin = %q, want %q", a.Name, a.Origin, catalog.OriginCurated)
		}
		if a.Customized {
			t.Errorf("%s: Customized = true with no delta file", a.Name)
		}
	}
}

func TestHandleListMergedCatalogDelta_DeltaOverridesVersionAndAddsInternalAddon(t *testing.T) {
	delta := `
apiVersion: sharko.dev/v1
kind: AddonCatalogDelta
metadata:
  name: addon-catalog-delta
spec:
  addons:
    cert-manager:
      version: "1.14.5"
    billing-api:
      repoURL: oci://registry.example.com/charts
      chart: billing-api
      version: "2.4.0"
`
	srv := serverWithCatalogAndDelta(t, testCatalog(t), &fakeDeltaGitProvider{deltaBody: []byte(delta)})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/delta/addons", nil)
	rw := httptest.NewRecorder()
	srv.handleListMergedCatalogDelta(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rw.Code, rw.Body.String())
	}
	var resp mergedCatalogListResponse
	if err := json.Unmarshal(rw.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 3 { // cert-manager, grafana, billing-api
		t.Errorf("Total = %d, want 3", resp.Total)
	}
	byName := make(map[string]catalog.MergedAddon, len(resp.Addons))
	for _, a := range resp.Addons {
		byName[a.Name] = a
	}
	cm, ok := byName["cert-manager"]
	if !ok {
		t.Fatalf("expected cert-manager in response")
	}
	if cm.Version != "1.14.5" || cm.Origin != catalog.OriginCurated {
		t.Errorf("cert-manager not merged correctly: %+v", cm)
	}
	billing, ok := byName["billing-api"]
	if !ok {
		t.Fatalf("expected billing-api (internal addon) in response")
	}
	if billing.Origin != catalog.OriginInternal {
		t.Errorf("billing-api Origin = %q, want %q", billing.Origin, catalog.OriginInternal)
	}
	if billing.RepoURL == "" || billing.Chart == "" || billing.Version == "" {
		t.Errorf("billing-api missing deployment fields: %+v", billing)
	}
}

func TestHandleGetMergedCatalogDeltaAddon_NotFound(t *testing.T) {
	srv := serverWithCatalogAndDelta(t, testCatalog(t), &fakeDeltaGitProvider{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/delta/addons/does-not-exist", nil)
	req.SetPathValue("name", "does-not-exist")
	rw := httptest.NewRecorder()
	srv.handleGetMergedCatalogDeltaAddon(rw, req)
	if rw.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", rw.Code, rw.Body.String())
	}
}

func TestHandleGetMergedCatalogDeltaAddon_CatalogNotLoaded(t *testing.T) {
	srv := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/delta/addons/cert-manager", nil)
	req.SetPathValue("name", "cert-manager")
	rw := httptest.NewRecorder()
	srv.handleGetMergedCatalogDeltaAddon(rw, req)
	if rw.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body = %s", rw.Code, rw.Body.String())
	}
}

func TestHandleListMergedCatalogDelta_MissingRequiredFieldSurfacesAs422(t *testing.T) {
	delta := `
apiVersion: sharko.dev/v1
kind: AddonCatalogDelta
metadata:
  name: addon-catalog-delta
spec:
  addons:
    billing-api:
      chart: billing-api
      version: "2.4.0"
`
	srv := serverWithCatalogAndDelta(t, testCatalog(t), &fakeDeltaGitProvider{deltaBody: []byte(delta)})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/delta/addons", nil)
	rw := httptest.NewRecorder()
	srv.handleListMergedCatalogDelta(rw, req)
	if rw.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", rw.Code, rw.Body.String())
	}
}

// TestLoadCatalogDelta_MissingFileIsEmpty is a focused unit test of the
// helper both merged-view handlers share.
func TestLoadCatalogDelta_MissingFileIsEmpty(t *testing.T) {
	srv := &Server{}
	spec, err := srv.loadCatalogDelta(context.Background(), &fakeDeltaGitProvider{})
	if err != nil {
		t.Fatalf("loadCatalogDelta: %v", err)
	}
	if len(spec.Addons) != 0 {
		t.Errorf("expected empty spec, got %+v", spec.Addons)
	}
}

// realErrorGitProvider always returns a genuine (non-not-found) error, to
// confirm loadCatalogDelta does NOT swallow real upstream failures.
type realErrorGitProvider struct{ fakeDeltaGitProvider }

func (r *realErrorGitProvider) GetFileContent(context.Context, string, string) ([]byte, error) {
	return nil, errors.New("upstream is on fire")
}

func TestLoadCatalogDelta_RealErrorPropagates(t *testing.T) {
	srv := &Server{}
	_, err := srv.loadCatalogDelta(context.Background(), &realErrorGitProvider{})
	if err == nil {
		t.Fatalf("expected a real error to propagate")
	}
}

// newConnectionServiceForTest already lives in tiered_git_test.go (same
// package) — reused here rather than duplicated.
