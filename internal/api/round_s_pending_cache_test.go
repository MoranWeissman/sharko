package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
)

// EP3 dry run round S, S22: the Clusters page loads /clusters and
// /clusters/available together. Both need the open pull request list, so
// that list is read from Git once and shared for a short time. A write or a
// merge wipes it, and a failed read is never kept.

// countingPRGit counts how many times the open pull request list is read.
type countingPRGit struct {
	*handlerFakeGitProvider
	calls atomic.Int32
	err   error
}

func (g *countingPRGit) ListPullRequests(ctx context.Context, state string) ([]gitprovider.PullRequest, error) {
	g.calls.Add(1)
	if g.err != nil {
		return nil, g.err
	}
	return g.handlerFakeGitProvider.ListPullRequests(ctx, state)
}

func pendingCacheServer(t *testing.T) (*Server, *countingPRGit, string) {
	t.Helper()
	srv := newDoctorTestServer(t)
	prefix := strings.TrimSpace(srv.gitopsConfig().CommitPrefix)
	git := &countingPRGit{handlerFakeGitProvider: &handlerFakeGitProvider{
		files: map[string][]byte{
			srv.repoPaths.Catalog:         []byte(doctorCatalogYAML),
			srv.repoPaths.ManagedClusters: []byte(doctorManagedClustersYAML),
		},
		prs: []gitprovider.PullRequest{{ID: 12, Title: prefix + " register cluster dev", URL: "https://example.invalid/pull/12"}},
	}}
	srv.connSvc.SetGitProviderOverride(git)
	srv.connSvc.SetArgocdClientOverride(&discoverFakeArgocd{clusters: []models.ArgocdCluster{
		{Name: "dev", Server: "https://10.0.0.8:6443"},
		{Name: "legacy-1", Server: "https://10.0.0.5:6443"},
	}})

	// /clusters reads ArgoCD through a real client, so give it a tiny fake
	// ArgoCD server.
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/clusters", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"items": []map[string]interface{}{
			{"name": "dev", "server": "https://10.0.0.8:6443"},
			{"name": "legacy-1", "server": "https://10.0.0.5:6443"},
		}})
	})
	mux.HandleFunc("/api/v1/applications", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"items": []interface{}{}})
	})
	argoSrv := httptest.NewServer(mux)
	t.Cleanup(argoSrv.Close)
	if err := srv.connSvc.Create(models.CreateConnectionRequest{
		Name:   "pending-cache-test",
		Git:    models.GitRepoConfig{Provider: models.GitProviderGitHub, Owner: "o", Repo: "r"},
		Argocd: models.ArgocdConfig{ServerURL: argoSrv.URL, Token: "test-token", Insecure: true},
	}); err != nil {
		t.Fatalf("seed connection: %v", err)
	}
	if err := srv.connSvc.SetActive("pending-cache-test"); err != nil {
		t.Fatalf("activate connection: %v", err)
	}
	return srv, git, prefix
}

func getClustersPage(t *testing.T, srv *Server) (list, available string) {
	t.Helper()
	w := httptest.NewRecorder()
	srv.handleListClusters(w, withRole(httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil), "operator"))
	if w.Code != http.StatusOK {
		t.Fatalf("/clusters status %d: %s", w.Code, w.Body.String())
	}
	list = w.Body.String()
	w = httptest.NewRecorder()
	srv.handleDiscoverClusters(w, withRole(httptest.NewRequest(http.MethodGet, "/api/v1/clusters/available", nil), "operator"))
	if w.Code != http.StatusOK {
		t.Fatalf("/clusters/available status %d: %s", w.Code, w.Body.String())
	}
	return list, w.Body.String()
}

const onlyLegacyAvailable = "{\"clusters\":[{\"name\":\"legacy-1\",\"server_url\":\"https://10.0.0.5:6443\"}]}\n"

func TestClustersPage_ReadsTheOpenPullRequestsOnce(t *testing.T) {
	srv, git, _ := pendingCacheServer(t)
	before := git.calls.Load()

	list, available := getClustersPage(t, srv)
	if got := git.calls.Load() - before; got != 1 {
		t.Fatalf("open pull requests read %d times for one Clusters page, want 1", got)
	}
	if !strings.Contains(list, `"cluster_name":"dev"`) {
		t.Fatalf("/clusters does not show dev as pending: %s", list)
	}
	if available != onlyLegacyAvailable {
		t.Fatalf("/clusters/available = %s, want only legacy-1", available)
	}
}

func TestClustersPage_AWriteWipesTheSharedList(t *testing.T) {
	srv, git, prefix := pendingCacheServer(t)
	getClustersPage(t, srv)

	// A new register pull request opens, then a write wipes the cache (the
	// audit middleware does this after every successful write).
	git.prs = append(git.prs, gitprovider.PullRequest{ID: 13, Title: prefix + " register cluster legacy-1", URL: "https://example.invalid/pull/13"})
	srv.readCache.InvalidateAll()

	list, available := getClustersPage(t, srv)
	if !strings.Contains(list, `"cluster_name":"legacy-1"`) {
		t.Fatalf("/clusters still shows the old list: %s", list)
	}
	if available != "{\"clusters\":[]}\n" {
		t.Fatalf("/clusters/available still shows the old list: %s", available)
	}
}

func TestClustersPage_AFailedReadIsNotKept(t *testing.T) {
	srv, git, _ := pendingCacheServer(t)
	git.err = errors.New("git host timed out")
	getClustersPage(t, srv)
	git.err = nil
	before := git.calls.Load()

	_, available := getClustersPage(t, srv)
	if got := git.calls.Load() - before; got != 1 {
		t.Fatalf("after a failed read the list was read %d times, want 1 fresh read", got)
	}
	if available != onlyLegacyAvailable {
		t.Fatalf("/clusters/available = %s, want only legacy-1", available)
	}
}

// A caller that decides a write passes no cache and always reads fresh.
func TestResolvePendingRegistrations_NoCacheReadsFresh(t *testing.T) {
	git := &countingPRGit{handlerFakeGitProvider: &handlerFakeGitProvider{}}
	resolvePendingRegistrations(context.Background(), nil, git, "sharko:")
	resolvePendingRegistrations(context.Background(), nil, git, "sharko:")
	if got := git.calls.Load(); got != 2 {
		t.Fatalf("read %d times, want 2", got)
	}
}
