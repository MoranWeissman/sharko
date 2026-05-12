//go:build e2e

package harness

import (
	"crypto/rand"
	"encoding/hex"
	"testing"
)

// TestUser describes a user the harness should create at suite startup.
//
// Username + Password are passed verbatim to the auth store. Role must be
// one of the values sharko's auth layer recognises today: "admin",
// "operator", or "viewer". (The role list is enforced by
// authStore.CreateUser; see internal/auth/store.go.) Earlier versions of
// this harness used "editor" by analogy with other RBAC systems — that
// was wrong for sharko and is rejected with 400.
type TestUser struct {
	Username string
	Password string
	Role     string
}

// SeedUsers creates the requested users on a running Sharko instance.
//
// In the in-process boot path (the only path supported by 7-1.2/7-1.3),
// SeedUsers calls *Server.AddDemoUser directly — this is the lowest-cost
// path that the maintainer's existing demo mode also uses (see
// internal/demo/setup.go) and avoids needing an admin login flow before
// any user can exist.
//
// In the helm-install boot path (deferred to story 7-1.10), users are
// expected to come from chart values — SeedUsers will fall back to the
// API user-creation flow against a pre-seeded admin token at that point.
// For now the function fails fast in helm mode so the gap is obvious.
//
// Calls t.Fatalf on any seed failure.
func SeedUsers(t *testing.T, sharko *Sharko, users []TestUser) {
	t.Helper()
	if sharko == nil {
		t.Fatalf("SeedUsers: sharko is nil")
	}
	if len(users) == 0 {
		return
	}
	switch sharko.Mode {
	case SharkoModeInProcess:
		seedUsersInProcess(t, sharko, users)
	case SharkoModeHelm:
		t.Fatalf("SeedUsers: helm mode user seeding not yet implemented (deferred to story 7-1.10); " +
			"in helm mode users come from chart values, not this helper")
	default:
		t.Fatalf("SeedUsers: unknown sharko mode %d", sharko.Mode)
	}
}

// seedUsersInProcess uses the *Server.AddDemoUser path directly to seed
// users. AddDemoUser writes (username, plaintext-password, role) into the
// auth store; in local mode auth.Store stores the plaintext and
// ValidateCredentials does a direct equality check, so callers can log in
// immediately with the same password.
//
// Why the direct path? sharko's POST /api/v1/auth/login is rate-limited
// to 5 attempts per IP per minute. Driving the seed via the API
// (create-user → reset-password → login-as-user → update-password) costs
// ~4 logins per user — three users blow through the limiter. AddDemoUser
// has zero rate-limit cost. The harness's *Sharko.APIServer() accessor
// surfaces the *api.Server instance for this purpose.
//
// Helm mode (story 7-1.10) cannot use this path — there's no in-process
// *api.Server — and SeedUsers will need to fall back to the API path
// (paced under the limiter) or to chart-values seeding. SeedUsers
// already fails fast in helm mode so the gap is visible.
func seedUsersInProcess(t *testing.T, sharko *Sharko, users []TestUser) {
	t.Helper()
	srv := sharko.APIServer()
	if srv == nil {
		t.Fatalf("SeedUsers: in-process *api.Server is nil — harness wiring is broken")
	}
	for _, u := range users {
		if u.Username == "" {
			t.Fatalf("SeedUsers: empty Username in TestUser")
		}
		if u.Password == "" {
			t.Fatalf("SeedUsers: empty Password for user %q", u.Username)
		}
		role := u.Role
		if role == "" {
			role = "viewer"
		}
		if err := srv.AddDemoUser(u.Username, u.Password, role); err != nil {
			t.Fatalf("SeedUsers: AddDemoUser(%s, role=%s): %v", u.Username, role, err)
		}
		t.Logf("harness: seeded user %s [role=%s]", u.Username, role)
	}
}

// DefaultTestUsers returns admin/editor/viewer test users with random
// passwords. Use when you need standard RBAC fixtures and don't care about
// the exact passwords (typical RBAC test pattern: NewClientAs(t, sharko,
// users[i].Username, users[i].Password)).
//
// Note: the bootstrap admin from StartSharko is already present; this
// helper returns a SECOND admin (admin-test) plus an editor and viewer.
// Tests that need to act as the bootstrap admin should use NewClient.
func DefaultTestUsers() []TestUser {
	return []TestUser{
		{Username: "admin-test", Password: randPassword(), Role: "admin"},
		{Username: "operator-test", Password: randPassword(), Role: "operator"},
		{Username: "viewer-test", Password: randPassword(), Role: "viewer"},
	}
}

// randPassword returns a 32-hex-char password (16 bytes of entropy). Same
// shape as the bootstrap admin password StartSharko generates.
func randPassword() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// In a test harness this is always fatal — no test can proceed
		// without entropy.
		panic("randPassword: crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(buf)
}

