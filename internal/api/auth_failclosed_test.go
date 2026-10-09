package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/auth"
)

// auth_failclosed_test.go — v4 fail-open removal (lane A).
//
// The old behavior: zero configured users meant basicAuthMiddleware returned
// the mux unwrapped (every endpoint open) and handleLogin minted an
// "anonymous" session for ANY credentials. Both paths are dead. These tests
// pin the new contract:
//
//   - zero users at request time = 401 on every protected route (fail CLOSED)
//   - login with zero users = 401, same as any bad credential
//   - a non-demo start with zero users gets a generated initial admin, and
//     auth is enforced from the first request
//   - demo users (admin/admin, qa/sharko) keep working, with auth enforced
//
// newFailClosedServer builds a server WITHOUT the legacy-open test seam and
// with the local auth env vars cleared, so the store genuinely has zero
// users — the production shape of a bare start.
func newFailClosedServer(t *testing.T) *Server {
	t.Helper()
	t.Setenv("SHARKO_AUTH_USER", "")
	t.Setenv("SHARKO_AUTH_PASSWORD", "")
	srv := newTestServer()
	srv.authDisabledForTests = false // production behavior: fail closed
	return srv
}

func TestAuth_FailClosed_ZeroUsers(t *testing.T) {
	srv := newFailClosedServer(t)
	router := NewRouter(srv, nil)

	// Any protected route refuses without credentials.
	for _, path := range []string{"/api/v1/clusters", "/api/v1/tokens", "/api/v1/addons"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("GET %s with zero users: status = %d, want 401 (fail closed)", path, w.Code)
		}
	}

	// Health stays open — it is the documented unauthenticated endpoint.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("GET /api/v1/health: status = %d, want 200", w.Code)
	}
}

func TestAuth_Login_ZeroUsers_AnonymousPathIsDead(t *testing.T) {
	srv := newFailClosedServer(t)
	router := NewRouter(srv, nil)

	// The old code returned 200 + an "anonymous" session for ANY creds.
	body := strings.NewReader(`{"username":"whoever","password":"whatever"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("login with zero users: status = %d, want 401; body = %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "token") && strings.Contains(w.Body.String(), `"token":"`) {
		t.Fatalf("login with zero users must not mint a session token; body = %s", w.Body.String())
	}
}

func TestAuth_InitialAdmin_GeneratedAndEnforced(t *testing.T) {
	// A non-demo start with zero users: EnsureInitialAdmin (called by
	// cmd/sharko/serve.go before the router is built) generates an admin,
	// and from then on auth is enforced — unauthenticated requests get a
	// 401, and the generated credential logs in and works.
	credFile := filepath.Join(t.TempDir(), "initial-admin.json")
	t.Setenv(auth.EnvInitialAdminFile, credFile)

	srv := newFailClosedServer(t)
	if err := srv.EnsureInitialAdmin(context.Background()); err != nil {
		t.Fatalf("EnsureInitialAdmin: %v", err)
	}
	router := NewRouter(srv, nil)

	// The credential landed in the local file (local mode in unit tests).
	raw, err := os.ReadFile(credFile)
	if err != nil {
		t.Fatalf("read generated credential file: %v", err)
	}
	var cred struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(raw, &cred); err != nil {
		t.Fatalf("parse generated credential file: %v", err)
	}

	// Unauthenticated request → 401.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tokens", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated request after generation: status = %d, want 401", w.Code)
	}

	// Login with the generated password → 200 + a working session.
	token := loginForTest(t, srv, cred.Username, cred.Password)
	t.Cleanup(func() { deleteSessionForTest(token) })

	authed := httptest.NewRequest(http.MethodGet, "/api/v1/tokens", nil)
	authed.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, authed)
	if w.Code != http.StatusOK {
		t.Fatalf("authenticated request with generated admin: status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
}

func TestAuth_DemoUsers_UnchangedAndEnforced(t *testing.T) {
	// Demo mode seeds admin/admin and qa/sharko BEFORE the router is
	// built (internal/demo/setup.go). Auth stays enforced: the demo
	// users work, junk does not, and unauthenticated requests get 401.
	srv := newFailClosedServer(t)
	if err := srv.AddDemoUser("admin", "admin", "admin"); err != nil {
		t.Fatalf("AddDemoUser: %v", err)
	}
	if err := srv.AddDemoUser("qa", "sharko", "viewer"); err != nil {
		t.Fatalf("AddDemoUser: %v", err)
	}
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tokens", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated request in demo shape: status = %d, want 401", w.Code)
	}

	adminToken := loginForTest(t, srv, "admin", "admin")
	t.Cleanup(func() { deleteSessionForTest(adminToken) })
	qaToken := loginForTest(t, srv, "qa", "sharko")
	t.Cleanup(func() { deleteSessionForTest(qaToken) })

	// Junk credentials are refused — the anonymous path is dead here too.
	body := strings.NewReader(`{"username":"nobody","password":"junk"}`)
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body)
	w = httptest.NewRecorder()
	srv.handleLogin(w, loginReq)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("junk login in demo shape: status = %d, want 401", w.Code)
	}
}

func TestAuth_HashEndpoint_RequiresAuth(t *testing.T) {
	// handleHashPassword used to be the "no users yet" setup helper. That
	// window no longer exists, so the endpoint is now a plain
	// authenticated utility — and it must NOT be a new unauthenticated
	// surface.
	srv := newFailClosedServer(t)
	router := NewRouter(srv, nil)

	// Zero users: 401 from the middleware, not a working hash service.
	body := strings.NewReader(`{"password":"whatever"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/hash", body)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated hash request: status = %d, want 401", w.Code)
	}

	// Authenticated: works.
	if err := srv.authStore.AddUser("alice", "correct-horse-battery-staple", "admin"); err != nil {
		t.Fatalf("AddUser: %v", err)
	}
	token := loginForTest(t, srv, "alice", "correct-horse-battery-staple")
	t.Cleanup(func() { deleteSessionForTest(token) })

	body = strings.NewReader(`{"password":"hash-me-please"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/hash", body)
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("authenticated hash request: status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	var resp struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode hash response: %v", err)
	}
	if !strings.HasPrefix(resp.Hash, "$2") {
		t.Fatalf("hash response is not bcrypt: %q", resp.Hash)
	}
}
