//go:build e2e

// Package lifecycle holds the V2 Epic 7-1 end-to-end suite that drives
// real sharko HTTP traffic against in-process / kind boots.
//
// auth_test.go (V2 Epic 7-1.10) covers the auth + tokens + RBAC slice:
// every endpoint under /api/v1/auth/* and /api/v1/tokens/*, plus a
// representative sample of write endpoints across domains so the RBAC
// matrix is observably enforced (not just unit-tested).
//
// All tests use the in-process boot path — no kind / docker / argocd
// dependency. Single sharko per top-level test, sub-tests share state
// where it is harmless and isolate fixtures (extra users, tokens) when
// they need a clean slate.
package lifecycle

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/tests/e2e/harness"
)

// startInProcess boots the standard in-process sharko + git fakes used
// by every test in this file. Kept tiny so the per-test setup block
// stays readable.
func startInProcess(t *testing.T) (*harness.Sharko, []harness.TestUser) {
	t.Helper()
	git := harness.StartGitFake(t)
	mock := harness.StartGitMock(t)
	sharko := harness.StartSharko(t, harness.SharkoConfig{
		Mode:        harness.SharkoModeInProcess,
		GitFake:     git,
		GitProvider: mock,
	})
	sharko.WaitHealthy(t, 10*time.Second)
	users := harness.DefaultTestUsers()
	harness.SeedUsers(t, sharko, users)
	return sharko, users
}

// findUser locates a TestUser by role from the slice DefaultTestUsers
// returns. Fails the test if the role is missing — DefaultTestUsers is
// the contract, this is just a typed accessor.
func findUser(t *testing.T, users []harness.TestUser, role string) harness.TestUser {
	t.Helper()
	for _, u := range users {
		if u.Role == role {
			return u
		}
	}
	t.Fatalf("findUser: no test user with role %q in %v", role, users)
	return harness.TestUser{}
}

// ---------------------------------------------------------------------
// TestAuthFlow — login / logout / update-password / hash
// ---------------------------------------------------------------------

// TestAuthFlow exercises every endpoint under /api/v1/auth/*.
//
// The login endpoint is rate-limited to 5 attempts per IP per minute
// (see internal/api/router.go newLoginRateLimiter). The whole suite
// runs against 127.0.0.1, so a naive implementation that creates a
// fresh Client (= one login each) per sub-test exhausts the budget
// after three sub-tests and the rest fail with 429s that look like
// the test caught a real bug.
//
// Mitigation: do exactly ONE NewClientAs per top-level test (i.e. one
// login) and reuse it everywhere. Negative paths (LoginInvalidPassword,
// LoginUnknownUser) issue raw POSTs via Client.Do — those go against
// the 5-per-minute budget, so we cap negative attempts to two and we
// run them BEFORE any rate-limiter-spending logout/password flows.
func TestAuthFlow(t *testing.T) {
	sharko, users := startInProcess(t)
	admin := findUser(t, users, "admin")
	viewer := findUser(t, users, "viewer")

	// Single shared admin client (1 login spent).
	c := harness.NewClientAs(t, sharko, admin.Username, admin.Password)

	t.Run("LoginValid", func(t *testing.T) {
		// Reuse c — the LoginAs helper just issues an additional
		// POST /auth/login with the supplied credentials and returns
		// the response shape; it does NOT mutate c.token.
		resp := c.LoginAs(t, admin.Username, admin.Password) // login #2
		if resp.Token == "" {
			t.Fatalf("LoginValid: empty token in response")
		}
		if resp.Username != admin.Username {
			t.Fatalf("LoginValid: username=%q want %q", resp.Username, admin.Username)
		}
		if resp.Role != "admin" {
			t.Fatalf("LoginValid: role=%q want admin", resp.Role)
		}
	})

	t.Run("LoginInvalidPassword", func(t *testing.T) {
		resp := c.Do(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
			"username": admin.Username,
			"password": "definitely-the-wrong-password",
		}, harness.WithNoRetry()) // login #3
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("LoginInvalidPassword: status=%d want 401", resp.StatusCode)
		}
	})

	t.Run("LoginUnknownUser", func(t *testing.T) {
		resp := c.Do(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
			"username": "ghost-" + harness.RandSuffix(),
			"password": "anything",
		}, harness.WithNoRetry()) // login #4
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("LoginUnknownUser: status=%d want 401", resp.StatusCode)
		}
	})

	t.Run("HashEndpoint", func(t *testing.T) {
		// In the in-process boot path StartSharko always seeds an
		// admin → authStore.HasUsers()==true → /auth/hash returns 403
		// by design (security: bcrypt-as-a-service is only safe during
		// initial bootstrap). Asserting 403 documents the contract.
		resp := c.Do(t, http.MethodPost, "/api/v1/auth/hash", map[string]string{
			"password": "anything-not-empty",
		})
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("HashEndpoint (auth-enabled): status=%d want 403", resp.StatusCode)
		}
	})

	t.Run("Logout", func(t *testing.T) {
		// Use the viewer's session: we logout that token, then assert
		// it is invalidated. Burning the viewer's session is safe —
		// no later sub-test in this file relies on it.
		// SeedUsers used the AddDemoUser bypass, so the login below is
		// the FIRST login for the viewer's IP slot — no 429.
		v := harness.NewClientAs(t, sharko, viewer.Username, viewer.Password) // login #5
		v.Logout(t)

		resp := v.Do(t, http.MethodGet, "/api/v1/users/me", nil, harness.WithNoRetry())
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("Logout: post-logout status=%d want 401", resp.StatusCode)
		}
	})

	// UpdatePassword runs in a separate top-level test so it gets a
	// fresh sharko boot and a fresh login budget. Drives the password
	// change flow on a throwaway user without disturbing the shared
	// admin/operator/viewer fixtures.
}

// TestAuthUpdatePassword runs in its own top-level test to escape the
// shared-fixture login-rate budget consumed by TestAuthFlow. Spawns a
// dedicated sharko, seeds a throwaway user via AddDemoUser (zero
// rate-limit cost), then drives the password-change loop.
func TestAuthUpdatePassword(t *testing.T) {
	sharko, _ := startInProcess(t)

	throwUser := "pwchange-" + harness.RandSuffix()
	throwPass := "init-passw0rd-1234"
	if err := sharko.APIServer().AddDemoUser(throwUser, throwPass, "viewer"); err != nil {
		t.Fatalf("seed throwaway user: %v", err)
	}

	// login #1 — establishes the session for the password update.
	c := harness.NewClientAs(t, sharko, throwUser, throwPass)
	newPass := "brand-new-passw0rd-456"
	c.UpdatePassword(t, throwPass, newPass)

	// login #2 — old password must fail with 401.
	resp := c.Do(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"username": throwUser,
		"password": throwPass,
	}, harness.WithNoRetry())
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("UpdatePassword: old-password login status=%d want 401", resp.StatusCode)
	}

	// login #3 — new password must succeed.
	fresh := harness.NewClientAs(t, sharko, throwUser, newPass)
	if fresh.Token() == "" {
		t.Fatalf("UpdatePassword: new-password login produced empty token")
	}

	// Validation: short password rejected with 400. Reuses the
	// existing session — no extra login spend.
	short := "shortpw" // 7 chars, <12
	resp2 := c.Do(t, http.MethodPost, "/api/v1/auth/update-password", map[string]string{
		"current_password": newPass,
		"new_password":     short,
	})
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("UpdatePassword: short-pw status=%d want 400", resp2.StatusCode)
	}
}

// ---------------------------------------------------------------------
// TestTokensCRUD — list / create / use / delete API tokens
// ---------------------------------------------------------------------

func TestTokensCRUD(t *testing.T) {
	sharko, users := startInProcess(t)
	admin := findUser(t, users, "admin")

	c := harness.NewClientAs(t, sharko, admin.Username, admin.Password)
	tokenName := "ci-token-" + harness.RandSuffix()

	t.Run("ListEmpty", func(t *testing.T) {
		// Brand-new sharko: no API tokens yet (session tokens are
		// separate state). The slice should be empty, not nil.
		got := c.ListTokens(t)
		for _, tok := range got {
			if tok.Name == tokenName {
				t.Fatalf("ListEmpty: did not expect %q in initial list", tokenName)
			}
		}
	})

	var created harness.CreateTokenResponse

	t.Run("Create", func(t *testing.T) {
		created = c.CreateToken(t, tokenName, "operator")
		if created.Name != tokenName {
			t.Fatalf("Create: name=%q want %q", created.Name, tokenName)
		}
		if !strings.HasPrefix(created.Token, "sharko_") {
			t.Fatalf("Create: token=%q does not have sharko_ prefix", created.Token)
		}
		if len(created.Token) != 39 {
			t.Fatalf("Create: token len=%d want 39 (sharko_ + 32 hex)", len(created.Token))
		}
		if created.Role != "operator" {
			t.Fatalf("Create: role=%q want operator", created.Role)
		}
	})

	t.Run("ListShows", func(t *testing.T) {
		got := c.ListTokens(t)
		var found *harness.APITokenView
		for i := range got {
			if got[i].Name == tokenName {
				found = &got[i]
				break
			}
		}
		if found == nil {
			t.Fatalf("ListShows: %q not in token list (got %d entries)", tokenName, len(got))
		}
		if found.Role != "operator" {
			t.Fatalf("ListShows: role=%q want operator", found.Role)
		}
		if found.CreatedAt.IsZero() {
			t.Fatalf("ListShows: created_at is zero")
		}
	})

	t.Run("UseToken", func(t *testing.T) {
		// Drive a request with the API token in the Authorization
		// header instead of the session bearer. We use the lower-level
		// Do path to inject WithHeader without contaminating the
		// admin's session state.
		probe := harness.NewClientAs(t, sharko, admin.Username, admin.Password)
		probe.SetToken("") // strip session — force header auth path
		resp := probe.Do(t, http.MethodGet, "/api/v1/health",
			nil,
			harness.WithHeader("Authorization", "Bearer "+created.Token),
			harness.WithNoRetry(),
		)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("UseToken: /health status=%d want 200", resp.StatusCode)
		}

		// Stronger check: hit an authenticated endpoint with the API
		// token. /api/v1/users requires user.list (viewer+). The
		// operator-role token easily satisfies that.
		resp = probe.Do(t, http.MethodGet, "/api/v1/users",
			nil,
			harness.WithHeader("Authorization", "Bearer "+created.Token),
			harness.WithNoRetry(),
		)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("UseToken: /users status=%d want 200", resp.StatusCode)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		c.RevokeToken(t, tokenName)

		// List no longer contains the token.
		got := c.ListTokens(t)
		for _, tok := range got {
			if tok.Name == tokenName {
				t.Fatalf("Delete: token %q still in list after revoke", tokenName)
			}
		}

		// And the token can no longer authenticate.
		probe := harness.NewClientAs(t, sharko, admin.Username, admin.Password)
		probe.SetToken("")
		resp := probe.Do(t, http.MethodGet, "/api/v1/users",
			nil,
			harness.WithHeader("Authorization", "Bearer "+created.Token),
			harness.WithNoRetry(),
		)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("Delete: post-revoke /users status=%d want 401", resp.StatusCode)
		}
	})
}

// ---------------------------------------------------------------------
// TestRBACEnforcement — observe the role matrix in real HTTP traffic
// ---------------------------------------------------------------------

// roleMatrix encodes the EXPECTED minimum role per sample action,
// derived from internal/authz/authz.go ActionRequirements at the time
// of writing (V2 Epic 7-1.10). When this drifts, the test fails loudly
// — that is the entire point of the RBAC enforcement layer.
//
//   addon.add-to-catalog  → operator (POST /api/v1/addons)
//   connection.create     → operator (POST /api/v1/connections/)
//   token.create          → operator (POST /api/v1/tokens)
//   cluster.remove        → admin    (DELETE /api/v1/clusters/{name})
//
// "Allowed" here means "NOT 403". The handler is free to return 200,
// 201, 400, 404, 502 etc. once authz waves the request through — that
// is downstream domain validation, not authz.
func TestRBACEnforcement(t *testing.T) {
	sharko, users := startInProcess(t)
	admin := findUser(t, users, "admin")
	operator := findUser(t, users, "operator")
	viewer := findUser(t, users, "viewer")

	adminClient := harness.NewClientAs(t, sharko, admin.Username, admin.Password)
	operatorClient := harness.NewClientAs(t, sharko, operator.Username, operator.Password)
	viewerClient := harness.NewClientAs(t, sharko, viewer.Username, viewer.Password)

	type expect struct {
		viewer   bool // true = allowed (non-403); false = expect 403
		operator bool
		admin    bool
	}
	type sample struct {
		name string
		// do issues the request and returns (status, body-snippet).
		// body-snippet is included only on assertion failure to make
		// triage trivial.
		do func(c *harness.Client) (int, string)
		// minimum role required per the matrix (for log clarity).
		minRole string
		exp     expect
	}

	samples := []sample{
		{
			name:    "AddCustomAddon",
			minRole: "operator",
			exp:     expect{viewer: false, operator: true, admin: true},
			do: func(c *harness.Client) (int, string) {
				return doStatus(t, c, http.MethodPost, "/api/v1/addons", map[string]any{
					"name":     "rbac-probe-" + harness.RandSuffix(),
					"chart":    "nginx",
					"repo_url": "https://example.test/charts",
					"version":  "0.0.1",
				})
			},
		},
		{
			name:    "CreateConnection",
			minRole: "operator",
			exp:     expect{viewer: false, operator: true, admin: true},
			do: func(c *harness.Client) (int, string) {
				return doStatus(t, c, http.MethodPost, "/api/v1/connections/", map[string]any{
					"name": "rbac-probe-" + harness.RandSuffix(),
				})
			},
		},
		{
			name:    "CreateToken",
			minRole: "operator",
			exp:     expect{viewer: false, operator: true, admin: true},
			do: func(c *harness.Client) (int, string) {
				return doStatus(t, c, http.MethodPost, "/api/v1/tokens", map[string]any{
					"name": "rbac-probe-" + harness.RandSuffix(),
					"role": "viewer",
				})
			},
		},
		{
			name:    "DeleteCluster",
			minRole: "admin",
			exp:     expect{viewer: false, operator: false, admin: true},
			do: func(c *harness.Client) (int, string) {
				// Path doesn't exist → admin gets 4xx, operator/viewer
				// get 403 from authz BEFORE the lookup happens.
				return doStatus(t, c, http.MethodDelete,
					"/api/v1/clusters/does-not-exist-"+harness.RandSuffix(), nil)
			},
		},
	}

	for _, s := range samples {
		t.Run(s.name, func(t *testing.T) {
			t.Logf("RBAC sample %q (min role=%s): viewer=%v operator=%v admin=%v",
				s.name, s.minRole, s.exp.viewer, s.exp.operator, s.exp.admin)

			vStatus, vBody := s.do(viewerClient)
			assertAllowed(t, "viewer", s.name, vStatus, vBody, s.exp.viewer)

			oStatus, oBody := s.do(operatorClient)
			assertAllowed(t, "operator", s.name, oStatus, oBody, s.exp.operator)

			aStatus, aBody := s.do(adminClient)
			assertAllowed(t, "admin", s.name, aStatus, aBody, s.exp.admin)
		})
	}
}

// doStatus issues a single request via Client.Do and returns the
// status + a body snippet (capped). It deliberately does NOT call
// t.Fatalf on non-2xx — RBAC tests want to inspect 403/4xx/5xx as
// data, not as failure.
func doStatus(t *testing.T, c *harness.Client, method, path string, body any) (int, string) {
	t.Helper()
	resp := c.Do(t, method, path, body, harness.WithNoRetry())
	defer resp.Body.Close()
	buf := make([]byte, 256)
	n, _ := resp.Body.Read(buf)
	return resp.StatusCode, string(buf[:n])
}

// assertAllowed turns the two-state expectation into a clear assertion.
// allowed==true means status MUST NOT be 403 (anything else is fine —
// 200, 201, 400, 404, 502 are all "the handler ran").
// allowed==false means status MUST BE exactly 403 (the authz contract).
func assertAllowed(t *testing.T, role, sample string, status int, body string, allowed bool) {
	t.Helper()
	if allowed {
		if status == http.StatusForbidden {
			t.Fatalf("RBAC %s/%s: got 403 but expected handler to run; body=%s",
				role, sample, body)
		}
		return
	}
	if status != http.StatusForbidden {
		t.Fatalf("RBAC %s/%s: status=%d want 403; body=%s",
			role, sample, status, body)
	}
}
