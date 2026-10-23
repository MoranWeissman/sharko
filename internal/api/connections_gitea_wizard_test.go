package api

// Finding 30: the first-run wizard's Git test said "Sharko can't reach your
// Git host" when the real reason was that Sharko did not know which Git
// product runs on the address. These tests pin the plain sentence, that a
// given provider works with any address, and that the Helm values reach the
// wizard.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

const wantGitHostNotRecognised = `Sharko does not recognise the Git host "gitea.example.test". Choose the Git provider (GitHub or Gitea) next to the address, then test again.`

func decodeTestBody(t *testing.T, rw *httptest.ResponseRecorder) map[string]map[string]interface{} {
	t.Helper()
	var body map[string]map[string]interface{}
	if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding body: %v; body = %s", err, rw.Body.String())
	}
	return body
}

func TestTestCredentials_UnknownHostWithoutProvider_SaysSo(t *testing.T) {
	argo := newRecordingHost(t)
	srv := newIsolatedTestServer(t)

	rw := postTestCredentials(t, srv, "operator", models.CreateConnectionRequest{
		Git:    models.GitRepoConfig{RepoURL: "https://gitea.example.test/acme/platform", Token: "git-token"},
		Argocd: models.ArgocdConfig{ServerURL: argo.URL, Token: "argo-token", Namespace: "argocd"},
	})
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rw.Code, rw.Body.String())
	}
	body := decodeTestBody(t, rw)
	if got := body["git"]["status"]; got != "error" {
		t.Errorf("git.status = %v, want error", got)
	}
	if got := body["git"]["message"]; got != wantGitHostNotRecognised {
		t.Errorf("git.message:\n got: %v\nwant: %q", got, wantGitHostNotRecognised)
	}
	if got := body["git"]["message"]; got == "Sharko can't reach your Git host. Check the connection settings and try again." {
		t.Errorf("the made-up 'can't reach' sentence came back")
	}
	if got := body["argocd"]["status"]; got != "not_tested" {
		t.Errorf("argocd.status = %v, want not_tested", got)
	}
	if argo.hitCount() != 0 {
		t.Errorf("nothing should be contacted, got %d requests", argo.hitCount())
	}
}

// With the provider given, a self-hosted address is fine: the test goes out.
func TestTestCredentials_GiteaProviderGivenOnAnyHost_IsTested(t *testing.T) {
	argo := newRecordingHost(t)
	// A plain 127.0.0.1 address: not a name Sharko knows. It answers like Gitea.
	var gitHits int32
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&gitHits, 1)
		switch r.URL.Path {
		case "/api/v1/version":
			_, _ = w.Write([]byte(`{"version":"1.22.0"}`))
		case "/api/v1/repos/acme/platform":
			_, _ = w.Write([]byte(`{"name":"platform","empty":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer fake.Close()
	srv := newIsolatedTestServer(t)

	rw := postTestCredentials(t, srv, "operator", models.CreateConnectionRequest{
		Git:    models.GitRepoConfig{Provider: models.GitProviderGitea, RepoURL: fake.URL + "/acme/platform", Token: "git-token"},
		Argocd: models.ArgocdConfig{ServerURL: argo.URL, Token: "argo-token", Namespace: "argocd"},
	})
	body := decodeTestBody(t, rw)
	if got := body["git"]["status"]; got != "ok" {
		t.Errorf("git.status = %v, want ok; body = %s", got, rw.Body.String())
	}
	if atomic.LoadInt32(&gitHits) == 0 {
		t.Errorf("the Git host was never contacted")
	}
}

func TestGitDefaults_ReturnsWhatTheChartDeclared(t *testing.T) {
	t.Setenv("SHARKO_CONN_GIT_PROVIDER", "gitea")
	t.Setenv("SHARKO_CONN_GIT_REPO_URL", "http://gitea.dev.svc:3000/acme/platform")
	srv := newIsolatedTestServer(t)

	rw := httptest.NewRecorder()
	srv.handleGitDefaults(rw, httptest.NewRequest(http.MethodGet, "/api/v1/connections/git-defaults", nil))
	var got map[string]string
	if err := json.Unmarshal(rw.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["provider"] != "gitea" || got["repo_url"] != "http://gitea.dev.svc:3000/acme/platform" {
		t.Errorf("got %v", got)
	}
}

func TestGitDefaults_EmptyWhenTheChartSaysNothing(t *testing.T) {
	t.Setenv("SHARKO_CONN_GIT_PROVIDER", "")
	t.Setenv("SHARKO_CONN_GIT_REPO_URL", "")
	srv := newIsolatedTestServer(t)

	rw := httptest.NewRecorder()
	srv.handleGitDefaults(rw, httptest.NewRequest(http.MethodGet, "/api/v1/connections/git-defaults", nil))
	var got map[string]string
	if err := json.Unmarshal(rw.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["provider"] != "" || got["repo_url"] != "" {
		t.Errorf("got %v, want both empty", got)
	}
}

// EP3-DRYRUN-FIXES join: GET /connections/git-defaults sits behind login.
// Without a session it answers 401 and says nothing about the chart.
func TestGitDefaults_NeedsLogin(t *testing.T) {
	t.Setenv("SHARKO_CONN_GIT_PROVIDER", "gitea")
	t.Setenv("SHARKO_CONN_GIT_REPO_URL", "http://gitea.dev.svc:3000/acme/platform")
	srv, token := newAuthedTestServer(t)
	router := NewRouter(srv, nil)

	rw := httptest.NewRecorder()
	router.ServeHTTP(rw, httptest.NewRequest(http.MethodGet, "/api/v1/connections/git-defaults", nil))
	if rw.Code != http.StatusUnauthorized {
		t.Fatalf("no session: want 401, got %d (body=%s)", rw.Code, rw.Body.String())
	}
	if strings.Contains(rw.Body.String(), "gitea") {
		t.Fatalf("the 401 body tells the chart's Git settings: %s", rw.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/connections/git-defaults", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rw = httptest.NewRecorder()
	router.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("with a session: want 200, got %d (body=%s)", rw.Code, rw.Body.String())
	}
}

// EP3-DRYRUN-FIXES join: a token the operator wrote into the chart's repo
// address never comes back. The answer has only provider and repo_url.
func TestGitDefaults_NeverReturnsATokenInTheAddress(t *testing.T) {
	const secret = "not-a-real-chart-token-123"
	cases := []struct{ name, raw, want string }{
		{"user and token", "https://x-access-token:" + secret + "@github.example.com/acme/platform.git", "https://github.example.com/acme/platform.git"},
		{"token as the user", "https://" + secret + "@github.example.com/acme/platform", "https://github.example.com/acme/platform"},
		{"token in the query", "https://github.example.com/acme/platform?access_token=" + secret, "https://github.example.com/acme/platform"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SHARKO_CONN_GIT_PROVIDER", "github")
			t.Setenv("SHARKO_CONN_GIT_REPO_URL", tc.raw)
			srv := newIsolatedTestServer(t)

			rw := httptest.NewRecorder()
			srv.handleGitDefaults(rw, httptest.NewRequest(http.MethodGet, "/api/v1/connections/git-defaults", nil))
			if strings.Contains(rw.Body.String(), secret) {
				t.Fatalf("the token came back: %s", rw.Body.String())
			}
			var got map[string]interface{}
			if err := json.Unmarshal(rw.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if len(got) != 2 || got["provider"] != "github" || got["repo_url"] != tc.want {
				t.Fatalf("got %v, want only provider=github and repo_url=%q", got, tc.want)
			}
		})
	}
}
