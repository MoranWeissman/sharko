package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// emptyGiteaServer answers the way Gitea 1.22 does for a repo with no
// commits: 200 with `[]` for any file path (instead of 404).
func emptyGiteaServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/version":
			w.Write([]byte(`{"version":"1.22.0"}`))
		case r.URL.Path == "/api/v1/repos/o/r":
			w.Write([]byte(`{"name":"r","empty":true}`))
		case strings.HasPrefix(r.URL.Path, "/api/v1/repos/o/r/raw/"):
			w.Write([]byte("[]"))
		case strings.HasPrefix(r.URL.Path, "/api/v1/repos/o/r/contents/"):
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"message":"The target couldn't be found."}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// Finding 31: an empty Gitea repo must read as "not set up yet" so the wizard
// opens, not as a connection error with a TLS or proxy hint.
func TestRepoStatus_EmptyGiteaRepo_IsNotBootstrapped_NotAConnectionError(t *testing.T) {
	fake := emptyGiteaServer()
	defer fake.Close()
	gp, err := gitprovider.NewGiteaProvider(fake.URL, "o", "r", "tok")
	if err != nil {
		t.Fatal(err)
	}
	srv := newTestServer()
	srv.publishGitopsCfg(orchestrator.GitOpsConfig{BaseBranch: "main"})
	srv.connSvc.SetGitProviderOverride(gp)
	router := NewRouter(srv, nil)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/repo/status", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}
	var body RepoStatusResponse
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Reason != "not_bootstrapped" || body.Initialized {
		t.Fatalf("empty Gitea repo: reason=%q initialized=%v, want not_bootstrapped / false", body.Reason, body.Initialized)
	}
	if body.Format != "" {
		t.Errorf("format = %q, an empty repo has none", body.Format)
	}

	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/init/status", nil))
	var init InitStatusResponse
	if err := json.NewDecoder(w.Body).Decode(&init); err != nil {
		t.Fatal(err)
	}
	if init.State != RepoStateEmpty {
		t.Errorf("init state = %q, want %q", init.State, RepoStateEmpty)
	}
}
