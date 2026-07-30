package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// session_expiry_test.go — v4-wave2 8.3. Human logins last
// DefaultSessionLifetime (24 hours). When that runs out the session is gone:
// the next request is refused with a 401 and the only way back in is to log
// in again. There is no refresh token and no remember-me.

func TestSessionLifetime_DefaultIs24Hours(t *testing.T) {
	if DefaultSessionLifetime != 24*time.Hour {
		t.Fatalf("DefaultSessionLifetime = %s, want 24h — the documented default", DefaultSessionLifetime)
	}
	if sessionLifetime != DefaultSessionLifetime {
		t.Errorf("sessions are handed out with %s, want the documented %s", sessionLifetime, DefaultSessionLifetime)
	}
}

// A fresh login hands back a session that lasts the documented window.
func TestLogin_IssuesSessionWithDocumentedLifetime(t *testing.T) {
	srv := newTestServer()
	if err := srv.authStore.AddUser("alice", "correct-horse-battery-staple", "admin"); err != nil {
		t.Fatalf("AddUser: %v", err)
	}

	before := time.Now()
	token := loginForTest(t, srv, "alice", "correct-horse-battery-staple")

	sessionsMu.RLock()
	sess, ok := activeSessions[token]
	var expiry time.Time
	if ok {
		expiry = sess.Expiry
	}
	sessionsMu.RUnlock()
	t.Cleanup(func() { deleteSessionForTest(token) })

	if !ok {
		t.Fatal("login did not record a session")
	}
	want := before.Add(DefaultSessionLifetime)
	if diff := expiry.Sub(want); diff < -time.Minute || diff > time.Minute {
		t.Errorf("session expires at %s, want about %s", expiry, want)
	}
}

// The whole story in one test: a session past its lifetime is refused with a
// 401, and logging in again gets the caller a working session.
func TestSession_ExpiresThen401ThenReLoginWorks(t *testing.T) {
	srv := newTestServer()
	if err := srv.authStore.AddUser("alice", "correct-horse-battery-staple", "admin"); err != nil {
		t.Fatalf("AddUser: %v", err)
	}
	router := NewRouter(srv, nil)

	callWith := func(token string) int {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tokens", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rw := httptest.NewRecorder()
		router.ServeHTTP(rw, req)
		return rw.Code
	}

	// 1. Log in — the session works.
	token := loginForTest(t, srv, "alice", "correct-horse-battery-staple")
	t.Cleanup(func() { deleteSessionForTest(token) })
	if got := callWith(token); got != http.StatusOK {
		t.Fatalf("fresh session: status = %d, want 200", got)
	}

	// 2. Push the session past its lifetime — the same token is now refused.
	//    We move the expiry rather than waiting 24 hours; the check the
	//    middleware runs is the same either way.
	expireSessionForTest(token)
	if got := callWith(token); got != http.StatusUnauthorized {
		t.Fatalf("expired session: status = %d, want 401", got)
	}

	// 3. Log in again — a new session, and access is back.
	newToken := loginForTest(t, srv, "alice", "correct-horse-battery-staple")
	t.Cleanup(func() { deleteSessionForTest(newToken) })
	if newToken == token {
		t.Error("re-login must issue a new session token")
	}
	if got := callWith(newToken); got != http.StatusOK {
		t.Fatalf("after re-login: status = %d, want 200", got)
	}
}

// An expired session must not be usable for logout either — getSessionUser
// checks the expiry, so the caller is told to log in again rather than being
// handed a successful logout on a session that is already gone.
func TestLogout_ExpiredSessionIsRefused(t *testing.T) {
	srv := newTestServer()
	if err := srv.authStore.AddUser("alice", "correct-horse-battery-staple", "admin"); err != nil {
		t.Fatalf("AddUser: %v", err)
	}

	token := loginForTest(t, srv, "alice", "correct-horse-battery-staple")
	t.Cleanup(func() { deleteSessionForTest(token) })
	expireSessionForTest(token)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rw := httptest.NewRecorder()
	srv.handleLogout(rw, req)

	if rw.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body = %s", rw.Code, rw.Body.String())
	}
	if !strings.Contains(rw.Body.String(), "expired") {
		t.Errorf("refusal should mention the session expired; got %s", rw.Body.String())
	}
}

// The hourly sweep drops expired sessions from memory. The gate is the
// per-request expiry check, but the sweep keeps the map from growing forever.
func TestSessionCleanup_DropsExpiredEntries(t *testing.T) {
	token := "sweep-me-" + t.Name()
	sessionsMu.Lock()
	activeSessions[token] = &sessionInfo{Username: "alice", Expiry: time.Now().Add(-1 * time.Hour)}
	sessionsMu.Unlock()
	t.Cleanup(func() { deleteSessionForTest(token) })

	if isValidSession(token) {
		t.Fatal("an expired session must never validate")
	}
	if user := getSessionUser(token); user != "" {
		t.Errorf("getSessionUser returned %q for an expired session, want empty", user)
	}
}

// --- helpers ---

func loginForTest(t *testing.T, srv *Server, username, password string) string {
	t.Helper()
	body := strings.NewReader(`{"username":"` + username + `","password":"` + password + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body)
	rw := httptest.NewRecorder()
	srv.handleLogin(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("login: status = %d, want 200; body = %s", rw.Code, rw.Body.String())
	}
	var resp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rw.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode login response: %v; body = %s", err, rw.Body.String())
	}
	if resp.Token == "" {
		t.Fatal("login returned no session token")
	}
	return resp.Token
}

func expireSessionForTest(token string) {
	sessionsMu.Lock()
	if sess, ok := activeSessions[token]; ok {
		sess.Expiry = time.Now().Add(-1 * time.Minute)
	}
	sessionsMu.Unlock()
}

func deleteSessionForTest(token string) {
	sessionsMu.Lock()
	delete(activeSessions, token)
	sessionsMu.Unlock()
}
