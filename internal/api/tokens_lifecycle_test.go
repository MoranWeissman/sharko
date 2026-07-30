package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/auth"
)

// tokens_lifecycle_test.go — v4-wave2 8.2. Exercises the HTTP surface of the
// token lifecycle: create with an expiry, list with a status, renew, revoke,
// and the refusal an expired token gets from the auth middleware.

func decodeTokenList(t *testing.T, body []byte) []auth.APIToken {
	t.Helper()
	var out []auth.APIToken
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode token list: %v; body = %s", err, body)
	}
	return out
}

func TestCreateTokenHandler_AppliesDefaultExpiry(t *testing.T) {
	srv := newTestServer()

	body := strings.NewReader(`{"name":"ci-default","role":"viewer"}`)
	req := withRole(httptest.NewRequest(http.MethodPost, "/api/v1/tokens", body), "admin")
	rw := httptest.NewRecorder()
	srv.handleCreateToken(rw, req)

	if rw.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", rw.Code, rw.Body.String())
	}
	var resp CreateTokenResponse
	if err := json.Unmarshal(rw.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body = %s", err, rw.Body.String())
	}
	if !strings.HasPrefix(resp.Token, "sharko_") {
		t.Errorf("plaintext token missing from the create response")
	}
	if resp.ExpiresAt == "" {
		t.Fatal("create response must carry the expiry date")
	}
	expiresAt, err := time.Parse(time.RFC3339, resp.ExpiresAt)
	if err != nil {
		t.Fatalf("expires_at is not a timestamp: %v", err)
	}
	want := time.Now().AddDate(0, 0, auth.DefaultTokenExpiryDays)
	if diff := expiresAt.Sub(want); diff < -time.Minute || diff > time.Minute {
		t.Errorf("expires_at = %s, want about %s", expiresAt, want)
	}
}

func TestCreateTokenHandler_RejectsOutOfRangeWindow(t *testing.T) {
	srv := newTestServer()

	body := strings.NewReader(`{"name":"too-long","role":"viewer","expires_in_days":9999}`)
	req := withRole(httptest.NewRequest(http.MethodPost, "/api/v1/tokens", body), "admin")
	rw := httptest.NewRecorder()
	srv.handleCreateToken(rw, req)

	if rw.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rw.Code, rw.Body.String())
	}
	if !strings.Contains(rw.Body.String(), "between 1 and 365") {
		t.Errorf("error should name the allowed range; got %s", rw.Body.String())
	}
}

func TestListTokensHandler_ShowsExpiryAndStatusButNeverTheSecret(t *testing.T) {
	srv := newTestServer()

	plaintext, err := srv.authStore.CreateToken("listed", "operator", 30)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	req := withRole(httptest.NewRequest(http.MethodGet, "/api/v1/tokens", nil), "viewer")
	rw := httptest.NewRecorder()
	srv.handleListTokens(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rw.Code, rw.Body.String())
	}
	raw := rw.Body.String()
	if strings.Contains(raw, plaintext) {
		t.Fatal("token list leaked the plaintext value")
	}
	if strings.Contains(raw, "$2a$") || strings.Contains(raw, "$2b$") {
		t.Fatal("token list leaked a bcrypt hash")
	}

	tokens := decodeTokenList(t, rw.Body.Bytes())
	if len(tokens) != 1 {
		t.Fatalf("expected 1 token, got %d", len(tokens))
	}
	if tokens[0].Status != auth.TokenStatusActive {
		t.Errorf("status = %q, want %q", tokens[0].Status, auth.TokenStatusActive)
	}
	if tokens[0].ExpiresAt == nil {
		t.Error("expires_at missing from the list response")
	}
}

func TestListTokensHandler_LegacyTokenReadsAsNoExpiry(t *testing.T) {
	srv := newTestServer()

	if _, err := srv.authStore.CreateToken("old-token", "viewer", 0); err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	// Simulate a token stored before expiry existed.
	stripTokenExpiry(t, srv, "old-token")

	req := withRole(httptest.NewRequest(http.MethodGet, "/api/v1/tokens", nil), "viewer")
	rw := httptest.NewRecorder()
	srv.handleListTokens(rw, req)

	tokens := decodeTokenList(t, rw.Body.Bytes())
	if len(tokens) != 1 {
		t.Fatalf("expected 1 token, got %d", len(tokens))
	}
	if tokens[0].Status != auth.TokenStatusLegacyNoExpiry {
		t.Errorf("status = %q, want %q", tokens[0].Status, auth.TokenStatusLegacyNoExpiry)
	}
	if tokens[0].ExpiresAt != nil {
		t.Error("a legacy token must report no expiry date")
	}
	if tokens[0].Expired {
		t.Error("a legacy token must not be reported as expired")
	}
}

func TestRenewTokenHandler(t *testing.T) {
	srv := newTestServer()

	// withRole stamps X-Sharko-User as "<role>-user", so this is a token the
	// caller below owns — the operator-tier branch of the renew split.
	if _, err := srv.authStore.CreateTokenFor("operator-user", "renewable", "operator", 5); err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	// No body at all — the default window applies.
	req := withRole(httptest.NewRequest(http.MethodPost, "/api/v1/tokens/renewable/renew", nil), "operator")
	req.SetPathValue("name", "renewable")
	rw := httptest.NewRecorder()
	srv.handleRenewToken(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rw.Code, rw.Body.String())
	}
	var tok auth.APIToken
	if err := json.Unmarshal(rw.Body.Bytes(), &tok); err != nil {
		t.Fatalf("decode: %v; body = %s", err, rw.Body.String())
	}
	if tok.ExpiresAt == nil {
		t.Fatal("renew response must carry the new expiry")
	}
	want := time.Now().AddDate(0, 0, auth.DefaultTokenExpiryDays)
	if diff := tok.ExpiresAt.Sub(want); diff < -time.Minute || diff > time.Minute {
		t.Errorf("expires_at = %s, want about %s", tok.ExpiresAt, want)
	}
	// The response must never carry a secret.
	if strings.Contains(rw.Body.String(), "sharko_") || strings.Contains(rw.Body.String(), "$2a$") {
		t.Error("renew response leaked a secret")
	}
}

func TestRenewTokenHandler_ViewerForbidden(t *testing.T) {
	srv := newTestServer()
	if _, err := srv.authStore.CreateToken("guarded", "viewer", 0); err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	req := withRole(httptest.NewRequest(http.MethodPost, "/api/v1/tokens/guarded/renew", nil), "viewer")
	req.SetPathValue("name", "guarded")
	rw := httptest.NewRecorder()
	srv.handleRenewToken(rw, req)

	assert403(t, rw)
}

func TestRenewTokenHandler_UnknownTokenIs404(t *testing.T) {
	srv := newTestServer()

	req := withRole(httptest.NewRequest(http.MethodPost, "/api/v1/tokens/ghost/renew", nil), "admin")
	req.SetPathValue("name", "ghost")
	rw := httptest.NewRecorder()
	srv.handleRenewToken(rw, req)

	if rw.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", rw.Code, rw.Body.String())
	}
}

// ─── v4-wave2 security review, finding B2 ────────────────────────────────
//
// POST /tokens gated on token.create (operator+) and then passed the
// requested role straight to the store. An operator could therefore ask for
// an admin token and hold, one request later, every permission their own
// account is refused. A token now carries at most the role of whoever asked
// for it.

func TestCreateTokenHandler_OperatorCannotMintAnAdminToken(t *testing.T) {
	srv := newTestServer()

	body := strings.NewReader(`{"name":"escalate","role":"admin"}`)
	req := withRole(httptest.NewRequest(http.MethodPost, "/api/v1/tokens", body), "operator")
	rw := httptest.NewRecorder()
	srv.handleCreateToken(rw, req)

	if rw.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", rw.Code, rw.Body.String())
	}
	if strings.Contains(rw.Body.String(), "sharko_") {
		t.Fatal("the refusal handed out a token anyway")
	}
	if _, ok := srv.authStore.GetToken("escalate"); ok {
		t.Fatal("the refused token was stored")
	}
}

func TestCreateTokenHandler_OperatorMayMintAtOrBelowTheirOwnRole(t *testing.T) {
	for _, role := range []string{"operator", "viewer"} {
		t.Run(role, func(t *testing.T) {
			srv := newTestServer()

			body := strings.NewReader(`{"name":"ci-` + role + `","role":"` + role + `"}`)
			req := withRole(httptest.NewRequest(http.MethodPost, "/api/v1/tokens", body), "operator")
			rw := httptest.NewRecorder()
			srv.handleCreateToken(rw, req)

			if rw.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201; body = %s", rw.Code, rw.Body.String())
			}
			var resp CreateTokenResponse
			if err := json.Unmarshal(rw.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode: %v; body = %s", err, rw.Body.String())
			}
			if resp.Role != role {
				t.Errorf("role = %q, want %q", resp.Role, role)
			}
		})
	}
}

func TestCreateTokenHandler_AdminMayStillMintAnAdminToken(t *testing.T) {
	srv := newTestServer()

	body := strings.NewReader(`{"name":"break-glass","role":"admin"}`)
	req := withRole(httptest.NewRequest(http.MethodPost, "/api/v1/tokens", body), "admin")
	rw := httptest.NewRecorder()
	srv.handleCreateToken(rw, req)

	if rw.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", rw.Code, rw.Body.String())
	}
	stored, ok := srv.authStore.GetToken("break-glass")
	if !ok {
		t.Fatal("the token was not stored")
	}
	if stored.Role != "admin" {
		t.Errorf("stored role = %q, want admin", stored.Role)
	}
	if stored.CreatedBy != "admin-user" {
		t.Errorf("created_by = %q, want the caller's username", stored.CreatedBy)
	}
}

// ─── v4-wave2 security review, finding H1 ────────────────────────────────
//
// Renewing kept ANY token alive for ANY operator. It now follows the same
// own/other split revoking does.

func TestRenewTokenHandler_OperatorCannotRenewSomebodyElsesToken(t *testing.T) {
	srv := newTestServer()
	if _, err := srv.authStore.CreateTokenFor("alice", "alices-token", "operator", 5); err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	req := withRole(httptest.NewRequest(http.MethodPost, "/api/v1/tokens/alices-token/renew", nil), "operator")
	req.SetPathValue("name", "alices-token")
	rw := httptest.NewRecorder()
	srv.handleRenewToken(rw, req)

	assert403(t, rw)
	if !strings.Contains(rw.Body.String(), "token.renew-other") {
		t.Errorf("the refusal should name the admin-tier action; got %s", rw.Body.String())
	}
}

// A token created before anyone was recorded against it is owned by nobody,
// so it falls to the admin branch rather than being open to every operator.
func TestRenewTokenHandler_UnownedTokenNeedsAnAdmin(t *testing.T) {
	srv := newTestServer()
	if _, err := srv.authStore.CreateToken("nobodys-token", "operator", 5); err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	req := withRole(httptest.NewRequest(http.MethodPost, "/api/v1/tokens/nobodys-token/renew", nil), "operator")
	req.SetPathValue("name", "nobodys-token")
	rw := httptest.NewRecorder()
	srv.handleRenewToken(rw, req)
	assert403(t, rw)

	admin := withRole(httptest.NewRequest(http.MethodPost, "/api/v1/tokens/nobodys-token/renew", nil), "admin")
	admin.SetPathValue("name", "nobodys-token")
	adminRW := httptest.NewRecorder()
	srv.handleRenewToken(adminRW, admin)
	if adminRW.Code != http.StatusOK {
		t.Fatalf("admin renew: status = %d, want 200; body = %s", adminRW.Code, adminRW.Body.String())
	}
}

// An API key authenticates as its own name, so a token renewing itself is
// the same "my own credential" case as one you created.
func TestRenewTokenHandler_ATokenMayRenewItself(t *testing.T) {
	srv := newTestServer()
	if _, err := srv.authStore.CreateToken("nightly-sync", "operator", 5); err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tokens/nightly-sync/renew", nil)
	req.Header.Set("X-Sharko-User", "nightly-sync")
	req.Header.Set("X-Sharko-Role", "operator")
	req.SetPathValue("name", "nightly-sync")
	rw := httptest.NewRecorder()
	srv.handleRenewToken(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rw.Code, rw.Body.String())
	}
}

// A token nobody can see must not become visible through the refusal: an
// operator asking about a name that does not exist gets the same 403 as one
// asking about a token they do not own, never a 404.
func TestRenewTokenHandler_UnknownTokenDoesNotLeakToAnOperator(t *testing.T) {
	srv := newTestServer()

	req := withRole(httptest.NewRequest(http.MethodPost, "/api/v1/tokens/ghost/renew", nil), "operator")
	req.SetPathValue("name", "ghost")
	rw := httptest.NewRecorder()
	srv.handleRenewToken(rw, req)

	assert403(t, rw)
}

// An expired API token gets a 401 that says, in plain words, which token ran
// out — the caller already holds that secret, so naming it costs nothing and
// tells them exactly what to fix.
func TestMiddleware_ExpiredTokenGetsAClearRefusal(t *testing.T) {
	srv := newTestServer()
	if err := srv.authStore.AddUser("alice", "correct-horse-battery-staple", "admin"); err != nil {
		t.Fatalf("AddUser: %v", err)
	}
	plaintext, err := srv.authStore.CreateToken("nightly-sync", "operator", 0)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	expireToken(t, srv, "nightly-sync")

	router := NewRouter(srv, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil)
	req.Header.Set("Authorization", "Bearer "+plaintext)
	rw := httptest.NewRecorder()
	router.ServeHTTP(rw, req)

	if rw.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body = %s", rw.Code, rw.Body.String())
	}
	body := rw.Body.String()
	if !strings.Contains(body, "nightly-sync") {
		t.Errorf("refusal should name the token; got %s", body)
	}
	if !strings.Contains(body, "expired") {
		t.Errorf("refusal should say the token expired; got %s", body)
	}
	if strings.Contains(body, plaintext) || strings.Contains(body, "$2a$") {
		t.Fatal("refusal leaked the token value or hash")
	}
}

// A token that never existed, or one that was revoked, must get the same flat
// 401 — nothing that lets an outsider probe which token names are real.
func TestMiddleware_UnknownAndRevokedTokensGetGeneric401(t *testing.T) {
	srv := newTestServer()
	if err := srv.authStore.AddUser("alice", "correct-horse-battery-staple", "admin"); err != nil {
		t.Fatalf("AddUser: %v", err)
	}
	revoked, err := srv.authStore.CreateToken("was-real", "operator", 0)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	if err := srv.authStore.RevokeToken("was-real"); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}

	router := NewRouter(srv, nil)
	cases := map[string]string{
		"never existed": "sharko_00000000000000000000000000000000",
		"revoked":       revoked,
	}
	for name, token := range cases {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rw := httptest.NewRecorder()
			router.ServeHTTP(rw, req)

			if rw.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401; body = %s", rw.Code, rw.Body.String())
			}
			if !strings.Contains(rw.Body.String(), "unauthorized") {
				t.Errorf("expected the generic refusal, got %s", rw.Body.String())
			}
			if strings.Contains(rw.Body.String(), "was-real") {
				t.Error("refusal revealed a token name to a caller who does not hold it")
			}
		})
	}
}

// A renewed token starts working again through the real middleware path.
func TestMiddleware_RenewedTokenWorksAgain(t *testing.T) {
	srv := newTestServer()
	if err := srv.authStore.AddUser("alice", "correct-horse-battery-staple", "admin"); err != nil {
		t.Fatalf("AddUser: %v", err)
	}
	plaintext, err := srv.authStore.CreateToken("comes-back", "admin", 0)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	expireToken(t, srv, "comes-back")

	router := NewRouter(srv, nil)
	call := func() int {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tokens", nil)
		req.Header.Set("Authorization", "Bearer "+plaintext)
		rw := httptest.NewRecorder()
		router.ServeHTTP(rw, req)
		return rw.Code
	}

	if got := call(); got != http.StatusUnauthorized {
		t.Fatalf("expired token: status = %d, want 401", got)
	}
	if _, err := srv.authStore.RenewToken("comes-back", 0); err != nil {
		t.Fatalf("RenewToken: %v", err)
	}
	if got := call(); got != http.StatusOK {
		t.Fatalf("renewed token: status = %d, want 200", got)
	}
}

// --- helpers ---

// expireToken winds a stored token's expiry into the past.
func expireToken(t *testing.T, srv *Server, name string) {
	t.Helper()
	past := time.Now().Add(-1 * time.Hour)
	setTokenExpiryForTest(t, srv, name, &past)
}

// stripTokenExpiry removes a stored token's expiry, as if it had been created
// before expiry dates existed.
func stripTokenExpiry(t *testing.T, srv *Server, name string) {
	t.Helper()
	setTokenExpiryForTest(t, srv, name, nil)
}

func setTokenExpiryForTest(t *testing.T, srv *Server, name string, expiry *time.Time) {
	t.Helper()
	if !srv.authStore.SetTokenExpiryForTest(name, expiry) {
		t.Fatalf("token %q not found", name)
	}
}
