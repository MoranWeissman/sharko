package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/service"
)

// secretPathFakeGP is a minimal git provider fake for the secret_path v4
// fix (clusters_write.go): it records every path CreateOrUpdateFile /
// BatchCreateFiles ever wrote to, so a test can assert "this exact path was
// written" as precisely as "that other path never was".
type secretPathFakeGP struct {
	mu      sync.Mutex
	files   map[string][]byte
	written map[string][]byte
	// readErr, when set for a path, is returned instead of that path's
	// content — the transient-git-failure case the fail-closed probe must
	// abort on rather than guessing.
	readErr map[string]error
}

func newSecretPathFakeGP(seed map[string][]byte) *secretPathFakeGP {
	f := &secretPathFakeGP{files: map[string][]byte{}, written: map[string][]byte{}, readErr: map[string]error{}}
	for k, v := range seed {
		f.files[k] = v
	}
	return f
}

func (f *secretPathFakeGP) GetFileContent(_ context.Context, path, _ string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.readErr[path]; ok {
		return nil, err
	}
	if body, ok := f.files[path]; ok {
		return body, nil
	}
	return nil, fmt.Errorf("secretPathFakeGP: %s: %w", path, gitprovider.ErrFileNotFound)
}
func (f *secretPathFakeGP) ListDirectory(_ context.Context, _, _ string) ([]string, error) {
	return nil, nil
}
func (f *secretPathFakeGP) ListPullRequests(_ context.Context, _ string) ([]gitprovider.PullRequest, error) {
	return nil, nil
}
func (f *secretPathFakeGP) TestConnection(_ context.Context) error { return nil }
func (f *secretPathFakeGP) CreateBranch(_ context.Context, _, _ string) error {
	return nil
}
func (f *secretPathFakeGP) CreateOrUpdateFile(_ context.Context, path string, body []byte, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.written[path] = body
	return nil
}
func (f *secretPathFakeGP) BatchCreateFiles(_ context.Context, files map[string][]byte, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for p, b := range files {
		f.written[p] = b
	}
	return nil
}
func (f *secretPathFakeGP) DeleteFile(_ context.Context, _, _, _ string) error { return nil }
func (f *secretPathFakeGP) CreatePullRequest(_ context.Context, _, _, _, _ string) (*gitprovider.PullRequest, error) {
	return &gitprovider.PullRequest{ID: 1, URL: "https://example.invalid/pr/1"}, nil
}
func (f *secretPathFakeGP) MergePullRequest(_ context.Context, _ int) error { return nil }
func (f *secretPathFakeGP) GetPullRequestStatus(_ context.Context, _ int) (string, error) {
	return "open", nil
}
func (f *secretPathFakeGP) DeleteBranch(_ context.Context, _ string) error { return nil }

func (f *secretPathFakeGP) wrote(path string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.written[path]
	return ok
}

// argocdListClustersServer answers GET /api/v1/clusters with one cluster
// named "prod-eu" — enough for resolveClusterServer to find it.
func argocdListClustersServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/clusters" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"items":[{"name":"prod-eu","server":"https://prod-eu.example.com"}]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
}

// secretPathTestServer wires a real Server + router against the supplied
// git provider and a fake ArgoCD that knows about cluster "prod-eu".
func secretPathTestServer(t *testing.T, gp gitprovider.GitProvider) (*Server, http.Handler, *secretPathFakeGP) {
	t.Helper()
	argo := argocdListClustersServer(t)
	t.Cleanup(argo.Close)

	f, err := os.CreateTemp("", "sharko-secretpath-test-*.yaml")
	if err != nil {
		t.Fatalf("create temp config file: %v", err)
	}
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })

	store := config.NewFileStore(f.Name())
	connSvc := service.NewConnectionService(store)
	srv := newTestServer()
	srv.connSvc = connSvc

	if err := connSvc.Create(models.CreateConnectionRequest{
		Name:   "secretpath-test",
		Git:    models.GitRepoConfig{Provider: models.GitProviderGitHub, Owner: "o", Repo: "r"},
		Argocd: models.ArgocdConfig{ServerURL: argo.URL, Token: "t", Insecure: true},
	}); err != nil {
		t.Fatalf("seed connection: %v", err)
	}
	if err := connSvc.SetActive("secretpath-test"); err != nil {
		t.Fatalf("activate connection: %v", err)
	}
	connSvc.SetGitProviderOverride(gp)

	fake, ok := gp.(*secretPathFakeGP)
	if !ok {
		t.Fatal("secretPathTestServer requires a *secretPathFakeGP")
	}
	return srv, NewRouter(srv, nil), fake
}

func secretPathPatchReq(t *testing.T, secretPath string) *http.Request {
	t.Helper()
	body, err := json.Marshal(map[string]interface{}{"secret_path": secretPath})
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/clusters/prod-eu", strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	r.ContentLength = int64(len(body))
	r.Header.Set("X-Sharko-User", "admin-user")
	r.Header.Set("X-Sharko-Role", "admin")
	return r
}

// TestUpdateClusterAddons_SecretPath_V4_WritesRootFile is the bug-fix
// regression: on a v4 repo, the secret_path half of PATCH /clusters/{name}
// must write the v4 root managed-clusters.yaml, never
// configuration/managed-clusters.yaml (which would open a pull request
// creating a rival v3 registry file on a v4 repo).
func TestUpdateClusterAddons_SecretPath_V4_WritesRootFile(t *testing.T) {
	seed := map[string][]byte{
		orchestrator.BootstrapRootAppPath:  []byte("kind: Application\n"), // makes this a v4 repo
		orchestrator.V4ManagedClustersPath: []byte("clusters:\n  - name: prod-eu\n"),
	}
	gp := newSecretPathFakeGP(seed)
	_, router, fake := secretPathTestServer(t, gp)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, secretPathPatchReq(t, "clusters/prod/prod-eu"))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	if !fake.wrote(orchestrator.V4ManagedClustersPath) {
		t.Errorf("expected %s to be written; wrote: %v", orchestrator.V4ManagedClustersPath, fake.written)
	}
	if fake.wrote("configuration/managed-clusters.yaml") {
		t.Error("the v3 registry must NOT be written on a v4 repo — this is exactly the rival-file bug")
	}
}

// TestUpdateClusterAddons_SecretPath_ProbeFailure_FailsClosed pins the
// other half of the bug fix: when the v4-layout probe itself cannot get an
// answer (a transient git-host failure, not "the engine pin is absent"),
// the write must abort rather than guess "not v4" and go on to create the
// v3 registry file — the exact hijack this fix exists to prevent.
func TestUpdateClusterAddons_SecretPath_ProbeFailure_FailsClosed(t *testing.T) {
	seed := map[string][]byte{
		orchestrator.V4ManagedClustersPath: []byte("clusters:\n  - name: prod-eu\n"),
	}
	gp := newSecretPathFakeGP(seed)
	gp.readErr[orchestrator.BootstrapRootAppPath] = fmt.Errorf("git host rate limited this request")
	_, router, fake := secretPathTestServer(t, gp)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, secretPathPatchReq(t, "clusters/prod/prod-eu"))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 when the v4 probe fails, got %d (body=%s)", w.Code, w.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["code"] != "repo_layout" {
		t.Errorf(`expected code "repo_layout", got %v (body=%s)`, body["code"], w.Body.String())
	}
	if fake.wrote("configuration/managed-clusters.yaml") || fake.wrote(orchestrator.V4ManagedClustersPath) {
		t.Errorf("nothing may be written when the layout probe failed; wrote: %v", fake.written)
	}
}
