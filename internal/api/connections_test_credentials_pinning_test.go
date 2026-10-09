package api

// connections_test_credentials_pinning_test.go — v4-wave2 security review,
// finding B1.
//
// POST /connections/test-credentials used to be open to any authenticated
// caller AND used to lend a saved connection's stored Git/ArgoCD tokens to
// whatever address the request body happened to name. Together that was a
// way to read secrets out of the server: name a saved connection, leave the
// token fields blank, point repo_url or server_url at a host you control,
// and the stored credential arrives in your log.
//
// These tests pin both halves of the fix — the role gate, and the rule that a
// stored secret is only ever sent to the address it belongs to.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

// recordingHost stands in for a Git host or an ArgoCD server: it counts the
// requests it received and remembers every Authorization header presented to
// it, so a test can prove a secret did — or did not — arrive.
type recordingHost struct {
	*httptest.Server
	hits  int32
	auths []string
}

func newRecordingHost(t *testing.T) *recordingHost {
	t.Helper()
	h := &recordingHost{}
	h.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&h.hits, 1)
		h.auths = append(h.auths, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	t.Cleanup(h.Close)
	return h
}

func (h *recordingHost) hitCount() int { return int(atomic.LoadInt32(&h.hits)) }

func (h *recordingHost) sawSecret(secret string) bool {
	for _, a := range h.auths {
		if strings.Contains(a, secret) {
			return true
		}
	}
	return false
}

const (
	savedGitSecret    = "saved-git-secret-value"
	savedArgocdSecret = "saved-argocd-secret-value"
)

// serverWithSavedConnection returns a server holding one saved connection
// whose Git repo and ArgoCD server both live on `own`, with real-looking
// secrets stored against both.
func serverWithSavedConnection(t *testing.T, own *recordingHost) *Server {
	t.Helper()
	srv := newIsolatedTestServer(t)
	err := srv.connSvc.Create(models.CreateConnectionRequest{
		Name: "platform",
		Git: models.GitRepoConfig{
			Provider: models.GitProviderGitea,
			RepoURL:  own.URL + "/acme/platform",
			Token:    savedGitSecret,
		},
		Argocd: models.ArgocdConfig{
			ServerURL: own.URL,
			Token:     savedArgocdSecret,
			Namespace: "argocd",
		},
	})
	if err != nil {
		t.Fatalf("saving the connection under test: %v", err)
	}
	return srv
}

func postTestCredentials(t *testing.T, srv *Server, role string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := withRole(httptest.NewRequest(http.MethodPost, "/api/v1/connections/test-credentials", bytes.NewReader(raw)), role)
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	srv.handleTestCredentials(rw, req)
	return rw
}

// --- the role gate ---

func TestTestCredentials_ViewerForbidden(t *testing.T) {
	own := newRecordingHost(t)
	srv := serverWithSavedConnection(t, own)

	rw := postTestCredentials(t, srv, "viewer", models.CreateConnectionRequest{Name: "platform"})

	assert403(t, rw)
	if own.hitCount() != 0 {
		t.Errorf("a refused request still reached out %d times", own.hitCount())
	}
}

func TestTestConnection_ViewerForbidden(t *testing.T) {
	srv := newIsolatedTestServer(t)
	req := withRole(httptest.NewRequest(http.MethodPost, "/api/v1/connections/test", nil), "viewer")
	rw := httptest.NewRecorder()
	srv.handleTestConnection(rw, req)
	assert403(t, rw)
}

func TestTestProvider_ViewerForbidden(t *testing.T) {
	srv := newIsolatedTestServer(t)
	req := withRole(httptest.NewRequest(http.MethodPost, "/api/v1/providers/test", nil), "viewer")
	rw := httptest.NewRecorder()
	srv.handleTestProvider(rw, req)
	assert403(t, rw)
}

func TestTestProviderConfig_ViewerForbidden(t *testing.T) {
	srv := newIsolatedTestServer(t)
	req := withRole(httptest.NewRequest(http.MethodPost, "/api/v1/providers/test-config", nil), "viewer")
	rw := httptest.NewRecorder()
	srv.handleTestProviderConfig(rw, req)
	assert403(t, rw)
}

// --- the address pin ---

// The exfiltration attempt the review found: an operator names the saved
// connection, leaves the Git token blank, and points repo_url at a host they
// control. The saved Gitea token must not travel to that host.
func TestTestCredentials_SavedGitSecretNeverGoesToAnotherAddress(t *testing.T) {
	own := newRecordingHost(t)
	attacker := newRecordingHost(t)
	srv := serverWithSavedConnection(t, own)

	rw := postTestCredentials(t, srv, "operator", models.CreateConnectionRequest{
		Name: "platform",
		Git: models.GitRepoConfig{
			Provider: models.GitProviderGitea,
			RepoURL:  attacker.URL + "/acme/platform",
			// Token deliberately blank — this is the back-fill request.
		},
		Argocd: models.ArgocdConfig{ServerURL: own.URL, Namespace: "argocd"},
	})

	if rw.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", rw.Code, rw.Body.String())
	}
	if attacker.hitCount() != 0 {
		t.Fatalf("the saved credential was sent to the address in the request body (%d requests)", attacker.hitCount())
	}
	if attacker.sawSecret(savedGitSecret) {
		t.Fatal("the stored Git token reached an address the caller chose")
	}
	body := rw.Body.String()
	if strings.Contains(body, savedGitSecret) {
		t.Fatal("the refusal body echoed the stored secret")
	}
	if !strings.Contains(body, "submit its credentials explicitly") {
		t.Errorf("the refusal should say what to do instead; got %s", body)
	}
}

// Same attempt through the ArgoCD half of the same body.
func TestTestCredentials_SavedArgocdSecretNeverGoesToAnotherAddress(t *testing.T) {
	own := newRecordingHost(t)
	attacker := newRecordingHost(t)
	srv := serverWithSavedConnection(t, own)

	rw := postTestCredentials(t, srv, "operator", models.CreateConnectionRequest{
		Name:   "platform",
		Git:    models.GitRepoConfig{Provider: models.GitProviderGitea, RepoURL: own.URL + "/acme/platform"},
		Argocd: models.ArgocdConfig{ServerURL: attacker.URL, Namespace: "argocd"},
	})

	if rw.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", rw.Code, rw.Body.String())
	}
	if attacker.hitCount() != 0 {
		t.Fatalf("the saved ArgoCD token was sent to the address in the request body (%d requests)", attacker.hitCount())
	}
	if attacker.sawSecret(savedArgocdSecret) {
		t.Fatal("the stored ArgoCD token reached an address the caller chose")
	}
}

// Changing only the provider is the same attack with a different lever: a
// self-hosted Gitea address plus provider "gitea" is what turns repo_url into
// the host the token is sent to.
func TestTestCredentials_SwitchingProviderAloneIsRefused(t *testing.T) {
	own := newRecordingHost(t)
	srv := serverWithSavedConnection(t, own)

	rw := postTestCredentials(t, srv, "operator", models.CreateConnectionRequest{
		Name: "platform",
		Git: models.GitRepoConfig{
			Provider: models.GitProviderGitHub,
			RepoURL:  own.URL + "/acme/platform",
		},
		Argocd: models.ArgocdConfig{ServerURL: own.URL, Namespace: "argocd"},
	})

	if rw.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", rw.Code, rw.Body.String())
	}
}

// The flow the pin must not break: the operator edits a connection, leaves
// both token fields blank because they are not changing them, and tests the
// same addresses that are already saved. The stored secrets are used, and
// they arrive at the connection's own address.
func TestTestCredentials_SavedSecretStillUsedForItsOwnAddress(t *testing.T) {
	own := newRecordingHost(t)
	srv := serverWithSavedConnection(t, own)

	rw := postTestCredentials(t, srv, "operator", models.CreateConnectionRequest{
		Name: "platform",
		Git: models.GitRepoConfig{
			Provider: models.GitProviderGitea,
			RepoURL:  own.URL + "/acme/platform",
		},
		Argocd: models.ArgocdConfig{ServerURL: own.URL, Namespace: "argocd"},
	})

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rw.Code, rw.Body.String())
	}
	if own.hitCount() == 0 {
		t.Fatal("the test never reached the connection's own address, so the back-fill did not happen")
	}
	if !own.sawSecret(savedArgocdSecret) {
		t.Errorf("the stored ArgoCD token was not used for its own address; headers seen: %v", own.auths)
	}
	// And the response must never hand the secret back to the caller.
	if strings.Contains(rw.Body.String(), savedArgocdSecret) || strings.Contains(rw.Body.String(), savedGitSecret) {
		t.Fatal("the test result echoed a stored secret")
	}
}

// A trailing slash or a .git suffix is the same address, not a different one
// — the pin must not turn a cosmetic difference into a refusal.
func TestSameEndpoint_IgnoresCosmeticDifferences(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"https://git.example.com/acme/platform", "https://git.example.com/acme/platform/", true},
		{"https://git.example.com/acme/platform.git", "https://git.example.com/acme/platform", true},
		{"https://GIT.example.com/acme/platform", "https://git.example.com/acme/platform", true},
		{"https://git.example.com/acme/platform", "https://elsewhere.example.com/acme/platform", false},
		{"", "https://git.example.com/acme/platform", false},
	}
	for _, c := range cases {
		if got := sameEndpoint(c.a, c.b); got != c.want {
			t.Errorf("sameEndpoint(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
