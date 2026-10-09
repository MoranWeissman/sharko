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
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// fakeCatalogGitProvider is a minimal gitprovider.GitProvider whose only
// meaningful method is GetFileContent, returning either a canned
// catalog.yaml body or a wrapped gitprovider.ErrFileNotFound. Every other
// method is a harmless no-op; the read handlers never call them.
type fakeCatalogGitProvider struct {
	catalogBody []byte // nil => file not found
}

func (f *fakeCatalogGitProvider) GetFileContent(_ context.Context, path, _ string) ([]byte, error) {
	if path == config.AddonCatalogPath && f.catalogBody != nil {
		return f.catalogBody, nil
	}
	return nil, gitprovider.ErrFileNotFound
}
func (f *fakeCatalogGitProvider) ListDirectory(context.Context, string, string) ([]string, error) {
	return nil, nil
}
func (f *fakeCatalogGitProvider) ListPullRequests(context.Context, string) ([]gitprovider.PullRequest, error) {
	return nil, nil
}
func (f *fakeCatalogGitProvider) TestConnection(context.Context) error               { return nil }
func (f *fakeCatalogGitProvider) CreateBranch(context.Context, string, string) error { return nil }
func (f *fakeCatalogGitProvider) CreateOrUpdateFile(context.Context, string, []byte, string, string) error {
	return nil
}
func (f *fakeCatalogGitProvider) BatchCreateFiles(context.Context, map[string][]byte, string, string) error {
	return nil
}
func (f *fakeCatalogGitProvider) DeleteFile(context.Context, string, string, string) error {
	return nil
}
func (f *fakeCatalogGitProvider) CreatePullRequest(context.Context, string, string, string, string) (*gitprovider.PullRequest, error) {
	return &gitprovider.PullRequest{ID: 1, URL: "https://example.com/pr/1"}, nil
}
func (f *fakeCatalogGitProvider) MergePullRequest(context.Context, int) error { return nil }
func (f *fakeCatalogGitProvider) GetPullRequestStatus(context.Context, int) (string, error) {
	return "open", nil
}
func (f *fakeCatalogGitProvider) DeleteBranch(context.Context, string) error { return nil }

// serverWithOrgCatalog wires a Server with the Marketplace's curated list
// and a Git provider standing in for the caller's active connection.
func serverWithOrgCatalog(t *testing.T, c *catalog.Catalog, gp gitprovider.GitProvider) *Server {
	t.Helper()
	connSvc := newConnectionServiceForTest(t)
	connSvc.SetGitProviderOverride(gp)
	s := &Server{connSvc: connSvc}
	s.SetCatalog(c)
	return s
}

// TestHandleListOrgCatalog_FreshRepoApprovesNothing is Moran's bug, as a
// test: a repo with no catalog.yaml must come back EMPTY, not carrying
// every addon Sharko happens to ship. This is the whole point of the
// rebuild — if this test ever goes green with a non-zero total, the
// curated auto-seeding is back.
func TestHandleListOrgCatalog_FreshRepoApprovesNothing(t *testing.T) {
	srv := serverWithOrgCatalog(t, testCatalog(t), &fakeCatalogGitProvider{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/addons", nil)
	rw := httptest.NewRecorder()
	srv.handleListOrgCatalog(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rw.Code, rw.Body.String())
	}
	var resp orgCatalogListResponse
	if err := json.Unmarshal(rw.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 0 || len(resp.Addons) != 0 {
		t.Fatalf("a repo with no catalog.yaml must approve nothing, got %d: %+v", resp.Total, resp.Addons)
	}
}

// TestHandleListOrgCatalog_ListsOnlyWhatTheFileSays: the curated list fills
// in the description for a name it knows, and adds nothing of its own.
func TestHandleListOrgCatalog_ListsOnlyWhatTheFileSays(t *testing.T) {
	body := `
apiVersion: sharko.dev/v1
kind: AddonCatalog
addons:
  cert-manager:
    repoURL: https://charts.jetstack.io
    chart: cert-manager
    version: "1.14.5"
  billing-api:
    repoURL: oci://registry.example.com/charts
    chart: billing-api
    version: "2.4.0"
`
	srv := serverWithOrgCatalog(t, testCatalog(t), &fakeCatalogGitProvider{catalogBody: []byte(body)})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/addons", nil)
	rw := httptest.NewRecorder()
	srv.handleListOrgCatalog(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rw.Code, rw.Body.String())
	}
	var resp orgCatalogListResponse
	if err := json.Unmarshal(rw.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// testCatalog carries cert-manager AND grafana. grafana is not in the
	// file, so it must not appear.
	if resp.Total != 2 {
		t.Fatalf("Total = %d, want 2 (only what the file says); got %+v", resp.Total, resp.Addons)
	}
	byName := make(map[string]catalog.CatalogAddon, len(resp.Addons))
	for _, a := range resp.Addons {
		byName[a.Name] = a
	}
	if _, seeded := byName["grafana"]; seeded {
		t.Error("grafana is curated but not approved — it must not appear in the catalog")
	}

	cm := byName["cert-manager"]
	if cm.Origin != catalog.OriginCurated {
		t.Errorf("cert-manager Origin = %q, want %q (the Marketplace knows it)", cm.Origin, catalog.OriginCurated)
	}
	if cm.Description == "" {
		t.Error("cert-manager should carry the Marketplace's description")
	}
	if cm.RepoURL != "https://charts.jetstack.io" || cm.Version != "1.14.5" {
		t.Errorf("deployment fields must come from the file: %+v", cm)
	}
	if !cm.Deployable {
		t.Errorf("cert-manager should be deployable: %+v", cm)
	}

	billing := byName["billing-api"]
	if billing.Origin != catalog.OriginInternal {
		t.Errorf("billing-api Origin = %q, want %q", billing.Origin, catalog.OriginInternal)
	}
	if billing.Description != "" {
		t.Errorf("an addon the Marketplace never heard of has no description: %+v", billing)
	}
}

// TestHandleListOrgCatalog_IncompleteEntryIsFlaggedNotFatal: one
// half-written entry must not take the whole page down.
func TestHandleListOrgCatalog_IncompleteEntryIsFlaggedNotFatal(t *testing.T) {
	body := `
apiVersion: sharko.dev/v1
kind: AddonCatalog
addons:
  billing-api:
    chart: billing-api
    version: "2.4.0"
`
	srv := serverWithOrgCatalog(t, testCatalog(t), &fakeCatalogGitProvider{catalogBody: []byte(body)})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/addons", nil)
	rw := httptest.NewRecorder()
	srv.handleListOrgCatalog(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rw.Code, rw.Body.String())
	}
	var resp orgCatalogListResponse
	if err := json.Unmarshal(rw.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Addons) != 1 {
		t.Fatalf("expected the entry to still be listed, got %+v", resp.Addons)
	}
	got := resp.Addons[0]
	if got.Deployable {
		t.Error("an entry with no repoURL is not deployable")
	}
	if len(got.MissingFields) == 0 || got.MissingFields[0] != "repoURL" {
		t.Errorf("MissingFields should name repoURL, got %+v", got.MissingFields)
	}
}

func TestHandleGetOrgCatalogAddon_NotApprovedIs404(t *testing.T) {
	srv := serverWithOrgCatalog(t, testCatalog(t), &fakeCatalogGitProvider{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/addons/cert-manager", nil)
	req.SetPathValue("name", "cert-manager")
	rw := httptest.NewRecorder()
	srv.handleGetOrgCatalogAddon(rw, req)
	// cert-manager IS curated — and still 404s, because the org has not
	// approved it. That is the gate.
	if rw.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", rw.Code, rw.Body.String())
	}
}

// TestLoadOrgCatalog_MissingFileIsEmpty.
func TestLoadOrgCatalog_MissingFileIsEmpty(t *testing.T) {
	srv := &Server{}
	spec, err := srv.loadOrgCatalog(context.Background(), &fakeCatalogGitProvider{})
	if err != nil {
		t.Fatalf("loadOrgCatalog: %v", err)
	}
	if len(spec.Addons) != 0 {
		t.Errorf("expected an empty spec, got %+v", spec.Addons)
	}
}

// realErrorCatalogGitProvider always returns a genuine (non-not-found)
// error, to confirm loadOrgCatalog does NOT swallow real upstream failures.
type realErrorCatalogGitProvider struct{ fakeCatalogGitProvider }

func (r *realErrorCatalogGitProvider) GetFileContent(context.Context, string, string) ([]byte, error) {
	return nil, errors.New("upstream is on fire")
}

func TestLoadOrgCatalog_RealErrorPropagates(t *testing.T) {
	srv := &Server{}
	if _, err := srv.loadOrgCatalog(context.Background(), &realErrorCatalogGitProvider{}); err == nil {
		t.Fatal("expected a real error to propagate")
	}
}

// refRecordingCatalogGitProvider records the git ref every GetFileContent
// call was made against.
type refRecordingCatalogGitProvider struct {
	fakeCatalogGitProvider
	lastRef string
}

func (f *refRecordingCatalogGitProvider) GetFileContent(ctx context.Context, path, ref string) ([]byte, error) {
	f.lastRef = ref
	return f.fakeCatalogGitProvider.GetFileContent(ctx, path, ref)
}

// TestLoadOrgCatalog_HonorsConfiguredBaseBranch — a connection with
// base_branch: "release" is a real Sharko configuration.
func TestLoadOrgCatalog_HonorsConfiguredBaseBranch(t *testing.T) {
	const configuredBranch = "release"
	srv := &Server{}
	srv.publishGitopsCfg(orchestrator.GitOpsConfig{BaseBranch: configuredBranch})

	gp := &refRecordingCatalogGitProvider{}
	if _, err := srv.loadOrgCatalog(context.Background(), gp); err != nil {
		t.Fatalf("loadOrgCatalog: %v", err)
	}
	if gp.lastRef != configuredBranch {
		t.Errorf("read ref %q, want the configured base branch %q", gp.lastRef, configuredBranch)
	}
}

// TestApprovedAddonsForFreshness lists what the background scheduler needs
// to watch — the org's own charts, not the Marketplace's.
func TestApprovedAddonsForFreshness(t *testing.T) {
	body := `
apiVersion: sharko.dev/v1
kind: AddonCatalog
addons:
  billing-api:
    repoURL: oci://registry.example.com/charts
    chart: billing-api
    version: "2.4.0"
`
	srv := serverWithOrgCatalog(t, testCatalog(t), &fakeCatalogGitProvider{catalogBody: []byte(body)})
	got, err := srv.ApprovedAddonsForFreshness(context.Background())
	if err != nil {
		t.Fatalf("ApprovedAddonsForFreshness: %v", err)
	}
	if len(got) != 1 || got[0].Name != "billing-api" || got[0].RepoURL != "oci://registry.example.com/charts" {
		t.Fatalf("unexpected approved list: %+v", got)
	}
}

// newConnectionServiceForTest already lives in tiered_git_test.go (same
// package) — reused here rather than duplicated.
