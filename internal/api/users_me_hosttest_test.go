package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/service"
)

// v4.0.4 U9c: the personal token Test only ever asked api.github.com, so
// on a Gitea connection it could never pass. On a non-GitHub host it now
// reads the GitOps repo with the token through the same provider Sharko
// writes with.

type tokenTestGP struct {
	*fakeGP
	err   error
	calls int
}

func (g *tokenTestGP) TestConnection(context.Context) error {
	g.calls++
	return g.err
}

func giteaConn() *models.Connection {
	return &models.Connection{
		Name: "play",
		Git: models.GitRepoConfig{
			Provider: models.GitProviderGitea,
			RepoURL:  "http://gitea.example.test:3000/acme/gitops.git",
			Owner:    "acme",
			Repo:     "gitops",
		},
	}
}

func TestPersonalTokenOnHost_GiteaTokenWorks(t *testing.T) {
	srv := newTestServer()
	gp := &tokenTestGP{fakeGP: &fakeGP{}}
	srv.connSvc.SetGitProviderOverride(gp)

	status, body := srv.testPersonalTokenOnHost(context.Background(), giteaConn(), "tok")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %v", status, body)
	}
	if body["git_provider"] != "gitea" || body["status"] != "ok" {
		t.Errorf("unexpected body %v", body)
	}
	if gp.calls != 1 {
		t.Errorf("expected one repo read, got %d", gp.calls)
	}
}

func TestPersonalTokenOnHost_GiteaTokenRejected_PlainMessage(t *testing.T) {
	srv := newTestServer()
	gp := &tokenTestGP{fakeGP: &fakeGP{}, err: errors.New("GET http://gitea.example.test:3000/api/v1/repos/acme/gitops: 401 token=abc")}
	srv.connSvc.SetGitProviderOverride(gp)

	status, body := srv.testPersonalTokenOnHost(context.Background(), giteaConn(), "tok")
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d", status)
	}
	msg, _ := body["error"].(string)
	want := "token validation failed: this token could not read the GitOps repo on your Git host. Check that it is still valid and can read and write that repo."
	if msg != want {
		t.Fatalf("error = %q, want %q", msg, want)
	}
	for _, leak := range []string{"gitea.example.test", "token=abc", "api.github.com"} {
		if strings.Contains(msg, leak) {
			t.Errorf("message leaks %q: %q", leak, msg)
		}
	}
}

// The handler must take the host path on a Gitea connection — never ask
// api.github.com (which in this test would fail the run, since the fake
// provider is the only thing that can answer).
func TestHandleTestMyToken_GiteaConnectionUsesHostPath(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "sharko-test-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`connections:
  - name: play
    argocd:
      server_url: "http://127.0.0.1:1"
      token: test-token
    git:
      provider: gitea
      repo_url: "http://gitea.example.test:3000/acme/gitops.git"
      owner: acme
      repo: gitops
      token: svc
active_connection: play
`)
	f.Close()

	srv := newTestServer()
	srv.connSvc = service.NewConnectionService(config.NewFileStore(f.Name()))
	gp := &tokenTestGP{fakeGP: &fakeGP{}}
	srv.connSvc.SetGitProviderOverride(gp)

	key := "0123456789abcdef0123456789abcdef"
	t.Setenv("SHARKO_ENCRYPTION_KEY", key)
	if err := srv.authStore.AddUser("alice", "correct-horse-battery-staple", "admin"); err != nil {
		t.Fatal(err)
	}
	if err := srv.authStore.SetUserGitHubToken("alice", "personal-tok", key); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/me/github-token/test", nil)
	req.Header.Set("X-Sharko-User", "alice")
	req.Header.Set("X-Sharko-Role", "admin")
	w := httptest.NewRecorder()
	srv.handleTestMyGitHubToken(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}
	var body map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["git_provider"] != "gitea" {
		t.Errorf("expected the Gitea host path, got %v", body)
	}
	if gp.calls != 1 {
		t.Errorf("expected one repo read through the Git host, got %d", gp.calls)
	}
}

// Review fix: the tests above install a provider override, and the
// override is returned before the token is even looked at — so they would
// stay green if the host test quietly used the service token. This one
// uses no override: a fake Gitea answers only to the USER's token.
func TestPersonalTokenOnHost_SendsTheUsersOwnToken(t *testing.T) {
	const personal = "personal-tok-123"
	const service = "service-tok-456"
	var seen []string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/version":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"version":"1.22.0"}`))
		case "/api/v1/repos/acme/gitops":
			seen = append(seen, r.Header.Get("Authorization"))
			if r.Header.Get("Authorization") != "token "+personal {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":1,"name":"gitops","full_name":"acme/gitops"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer fake.Close()

	srv := newTestServer()
	if srv.connSvc.GitProviderOverride() != nil {
		t.Fatal("this test must run without a provider override")
	}
	conn := giteaConn()
	conn.Git.RepoURL = fake.URL + "/acme/gitops.git"
	conn.Git.Token = service

	status, body := srv.testPersonalTokenOnHost(context.Background(), conn, personal)
	if status != http.StatusOK {
		t.Fatalf("user's own token: status = %d, body %v", status, body)
	}
	if len(seen) == 0 {
		t.Fatal("the fake Git host was never asked for the repo")
	}
	for _, h := range seen {
		if h != "token "+personal {
			t.Errorf("the Git host got Authorization %q, want the user's own token", h)
		}
		if strings.Contains(h, service) {
			t.Errorf("the service token was sent instead of the user's: %q", h)
		}
	}

	seen = nil
	status, body = srv.testPersonalTokenOnHost(context.Background(), conn, "wrong-tok")
	if status != http.StatusBadRequest || body["error"] != PersonalTokenHostTestFailed {
		t.Fatalf("wrong token: status = %d, body %v", status, body)
	}
}

// Review fix: a Git provider value Sharko does not know must not fall
// through to the service provider and report "works".
func TestPersonalTokenOnHost_UnknownHostFailsClosed(t *testing.T) {
	srv := newTestServer()
	gp := &tokenTestGP{fakeGP: &fakeGP{}}
	srv.connSvc.SetGitProviderOverride(gp)
	conn := giteaConn()
	conn.Git.Provider = models.GitProviderType("somethingelse")

	status, body := srv.testPersonalTokenOnHost(context.Background(), conn, "tok")
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, body %v", status, body)
	}
	want := "token test is not available for this Git host, so the token was not checked."
	if body["error"] != want {
		t.Fatalf("error = %v, want %q", body["error"], want)
	}
	if gp.calls != 0 {
		t.Errorf("nothing should be tested on an unknown host, got %d calls", gp.calls)
	}
}
