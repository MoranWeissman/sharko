package api

// walks_c_sessions_test.go — pre-release fix round 2026-11-26, story C.
//
// Walk 5 found that a login went on working after log out, after the user was
// disabled, after the user was deleted and after an admin reset the password.
// It also found that changing the role of a disabled user turned the account
// back on. These tests pin the fixed behaviour.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const walkCPassword = "correct-horse-battery-staple"

// walkCServer is a server with an admin and one more user, with real auth on
// (the store has users, so the middleware enforces every call).
func walkCServer(t *testing.T, otherRole string) (*Server, http.Handler) {
	t.Helper()
	srv := newTestServer()
	if err := srv.authStore.AddUser("root-admin", walkCPassword, "admin"); err != nil {
		t.Fatalf("AddUser admin: %v", err)
	}
	if err := srv.authStore.AddUser("bob", walkCPassword, otherRole); err != nil {
		t.Fatalf("AddUser bob: %v", err)
	}
	return srv, NewRouter(srv, nil)
}

func walkCLogin(t *testing.T, srv *Server, user string) string {
	t.Helper()
	token := loginForTest(t, srv, user, walkCPassword)
	t.Cleanup(func() { deleteSessionForTest(token) })
	return token
}

func walkCCall(t *testing.T, h http.Handler, token, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		rdr = bytes.NewReader(data)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	return rw
}

func TestWalkC_LogOutEndsTheLoginOnTheServer(t *testing.T) {
	srv, h := walkCServer(t, "viewer")
	token := walkCLogin(t, srv, "bob")

	if rw := walkCCall(t, h, token, http.MethodGet, "/api/v1/users/me", nil); rw.Code != http.StatusOK {
		t.Fatalf("before log out: %d", rw.Code)
	}
	if rw := walkCCall(t, h, token, http.MethodPost, "/api/v1/auth/logout", nil); rw.Code != http.StatusOK {
		t.Fatalf("log out: %d %s", rw.Code, rw.Body.String())
	}
	if rw := walkCCall(t, h, token, http.MethodGet, "/api/v1/users/me", nil); rw.Code != http.StatusUnauthorized {
		t.Fatalf("after log out the same token must be refused, got %d", rw.Code)
	}
}

// The check is on every call, not only in the handler that disables: the user
// is switched off straight in the store here, behind the API's back.
func TestWalkC_DisabledUserIsRefusedOnEveryCall(t *testing.T) {
	srv, h := walkCServer(t, "operator")
	token := walkCLogin(t, srv, "bob")

	if err := srv.authStore.UpdateUser("bob", false, ""); err != nil {
		t.Fatalf("disable: %v", err)
	}

	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/users/me"},
		{http.MethodGet, "/api/v1/tokens"},
		{http.MethodPost, "/api/v1/tokens"},
		{http.MethodPost, "/api/v1/auth/update-password"},
		{http.MethodGet, "/api/v1/audit"},
	} {
		rw := walkCCall(t, h, token, c.method, c.path, map[string]string{"name": "x"})
		if rw.Code != http.StatusUnauthorized {
			t.Errorf("%s %s as a disabled user: got %d, want 401", c.method, c.path, rw.Code)
		}
	}
	if isValidSession(token) {
		t.Error("the login of a disabled user must be ended, it is still in the session list")
	}
}

func TestWalkC_DeletedUserIsRefused(t *testing.T) {
	srv, h := walkCServer(t, "operator")
	token := walkCLogin(t, srv, "bob")

	if err := srv.authStore.DeleteUser("bob"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if rw := walkCCall(t, h, token, http.MethodGet, "/api/v1/users/me", nil); rw.Code != http.StatusUnauthorized {
		t.Fatalf("a deleted user's login must be refused, got %d", rw.Code)
	}
}

// Each admin action ends the user's logins right when it happens. The session
// list is read directly, so this does not lean on the per-call check above.
func TestWalkC_AdminActionsEndTheUsersLogins(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"disable", http.MethodPut, "/api/v1/users/bob", map[string]any{"enabled": false}},
		{"role change", http.MethodPut, "/api/v1/users/bob", map[string]any{"role": "viewer"}},
		{"password reset", http.MethodPost, "/api/v1/users/bob/reset-password", nil},
		{"delete", http.MethodDelete, "/api/v1/users/bob", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, h := walkCServer(t, "operator")
			admin := walkCLogin(t, srv, "root-admin")
			one := walkCLogin(t, srv, "bob")
			two := walkCLogin(t, srv, "bob")

			rw := walkCCall(t, h, admin, tc.method, tc.path, tc.body)
			if rw.Code != http.StatusOK {
				t.Fatalf("%s: %d %s", tc.name, rw.Code, rw.Body.String())
			}
			if isValidSession(one) || isValidSession(two) {
				t.Errorf("after %s every login of the user must be ended", tc.name)
			}
			if !isValidSession(admin) {
				t.Errorf("after %s the admin's own login must still work", tc.name)
			}
		})
	}
}

// Saving a user with nothing changed ends nothing.
func TestWalkC_UpdateWithNoChangeKeepsLogins(t *testing.T) {
	srv, h := walkCServer(t, "operator")
	admin := walkCLogin(t, srv, "root-admin")
	bob := walkCLogin(t, srv, "bob")

	rw := walkCCall(t, h, admin, http.MethodPut, "/api/v1/users/bob", map[string]any{"role": "operator", "enabled": true})
	if rw.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rw.Code, rw.Body.String())
	}
	if !isValidSession(bob) {
		t.Error("nothing changed, so the login must stay")
	}
}

func TestWalkC_OwnPasswordChangeEndsOtherLoginsOnly(t *testing.T) {
	srv, h := walkCServer(t, "viewer")
	here := walkCLogin(t, srv, "bob")
	elsewhere := walkCLogin(t, srv, "bob")

	rw := walkCCall(t, h, here, http.MethodPost, "/api/v1/auth/update-password", map[string]string{
		"current_password": walkCPassword,
		"new_password":     "a-brand-new-long-password",
	})
	if rw.Code != http.StatusOK {
		t.Fatalf("change password: %d %s", rw.Code, rw.Body.String())
	}
	if !isValidSession(here) {
		t.Error("the login that changed the password must stay")
	}
	if isValidSession(elsewhere) {
		t.Error("every other login of that user must be ended")
	}
}

// Walk 5 S4: {"role":"operator"} on a disabled user answered enabled:true.
func TestWalkC_RoleChangeNeverTurnsADisabledUserBackOn(t *testing.T) {
	srv, h := walkCServer(t, "viewer")
	admin := walkCLogin(t, srv, "root-admin")

	if rw := walkCCall(t, h, admin, http.MethodPut, "/api/v1/users/bob", map[string]any{"enabled": false}); rw.Code != http.StatusOK {
		t.Fatalf("disable: %d %s", rw.Code, rw.Body.String())
	}
	rw := walkCCall(t, h, admin, http.MethodPut, "/api/v1/users/bob", map[string]any{"role": "operator"})
	if rw.Code != http.StatusOK {
		t.Fatalf("role change: %d %s", rw.Code, rw.Body.String())
	}
	var got struct {
		Enabled bool   `json:"enabled"`
		Role    string `json:"role"`
	}
	if err := json.Unmarshal(rw.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Enabled {
		t.Error("the answer says enabled:true — a role change turned a disabled account back on")
	}
	if got.Role != "operator" {
		t.Errorf("role = %q, want operator", got.Role)
	}
	if u := srv.authStore.GetUser("bob"); u == nil || u.Enabled {
		t.Error("the stored user is enabled again")
	}
	// And the other way round: switching on keeps the role.
	rw = walkCCall(t, h, admin, http.MethodPut, "/api/v1/users/bob", map[string]any{"enabled": true})
	if rw.Code != http.StatusOK {
		t.Fatalf("enable: %d", rw.Code)
	}
	if u := srv.authStore.GetUser("bob"); u == nil || !u.Enabled || u.Role != "operator" {
		t.Errorf("after enable: %+v, want enabled operator", u)
	}
}

func TestWalkC_CreateUserMessageSaysWhatReallyHappens(t *testing.T) {
	const want = "User created. Share the temporary password with them. They should change it after they log in, on the Account page."
	if CreateUserMessage != want {
		t.Fatalf("CreateUserMessage = %q, want %q", CreateUserMessage, want)
	}
	srv, h := walkCServer(t, "viewer")
	admin := walkCLogin(t, srv, "root-admin")
	rw := walkCCall(t, h, admin, http.MethodPost, "/api/v1/users", map[string]string{"username": "carol", "role": "viewer"})
	if rw.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rw.Code, rw.Body.String())
	}
	var got map[string]string
	_ = json.Unmarshal(rw.Body.Bytes(), &got)
	if got["message"] != want {
		t.Errorf("message = %q, want %q", got["message"], want)
	}
	// Sharko does not force the change, so it must not say it does.
	for _, banned := range []string{"must change", "first login"} {
		if strings.Contains(rw.Body.String(), banned) {
			t.Errorf("the answer still says %q", banned)
		}
	}
}

// Walk 6: a failed login with the name =1+1"<b>walk6x</b> was kept as Who.
func TestWalkC_FailedLoginNameIsCleanedForTheAuditLog(t *testing.T) {
	cases := map[string]string{
		`=1+1"<b>walk6x</b>`:     "_1_1__b_walk6x__b_",
		"":                       "unknown",
		"bob":                    "bob",
		"a.b_c-d9":               "a.b_c-d9",
		"line\nbreak,comma":      "line_break_comma",
		strings.Repeat("x", 500): strings.Repeat("x", 63),
		"@SUM(A1)":               "_SUM_A1_",
		"-2+3":                   "-2_3",
	}
	for in, want := range cases {
		if got := cleanLoginNameForAudit(in); got != want {
			t.Errorf("cleanLoginNameForAudit(%.30q) = %q, want %q", in, got, want)
		}
	}

	srv, h := walkCServer(t, "viewer")
	walkCCall(t, h, "", http.MethodPost, "/api/v1/auth/login", map[string]string{
		"username": `=1+1"<b>walk6x</b>`, "password": "nope",
	})
	entries := srv.auditLog.List(5)
	if len(entries) == 0 {
		t.Fatal("no audit entry for the failed login")
	}
	if got := entries[0].User; got != "_1_1__b_walk6x__b_" {
		t.Errorf("audit Who = %q, want the cleaned name", got)
	}
	if entries[0].Event != "login_failed" {
		t.Errorf("event = %q", entries[0].Event)
	}
}
