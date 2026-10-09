package auth

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// token_lifecycle_test.go — v4-wave2 8.2. Covers the expiry, renew, and
// refusal behaviour of API tokens.

func TestCreateToken_DefaultExpiryIs90Days(t *testing.T) {
	s := testStore()

	before := time.Now()
	if _, err := s.CreateToken("default-expiry", "viewer", 0); err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	tokens := s.ListTokens()
	if len(tokens) != 1 {
		t.Fatalf("expected 1 token, got %d", len(tokens))
	}
	tok := tokens[0]
	if tok.ExpiresAt == nil {
		t.Fatal("a new token must have an expiry date")
	}
	want := before.AddDate(0, 0, DefaultTokenExpiryDays)
	if diff := tok.ExpiresAt.Sub(want); diff < -time.Minute || diff > time.Minute {
		t.Errorf("expiry = %s, want about %s (default is %d days)",
			tok.ExpiresAt, want, DefaultTokenExpiryDays)
	}
	if tok.Status != TokenStatusActive {
		t.Errorf("status = %q, want %q", tok.Status, TokenStatusActive)
	}
	if tok.Expired {
		t.Error("a freshly created token must not be expired")
	}
}

func TestCreateToken_ExplicitExpiryWindow(t *testing.T) {
	s := testStore()

	before := time.Now()
	if _, err := s.CreateToken("short-lived", "viewer", 7); err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	tok := s.ListTokens()[0]
	if tok.ExpiresAt == nil {
		t.Fatal("expiry missing")
	}
	want := before.AddDate(0, 0, 7)
	if diff := tok.ExpiresAt.Sub(want); diff < -time.Minute || diff > time.Minute {
		t.Errorf("expiry = %s, want about %s", tok.ExpiresAt, want)
	}
	// 7 days out is inside the 14-day warning window.
	if !tok.ExpiringSoon {
		t.Error("a token expiring in 7 days should report expiring_soon")
	}
}

func TestCreateToken_ExpiryOutOfBoundsRejected(t *testing.T) {
	s := testStore()

	for _, days := range []int{-1, 366, 10000} {
		_, err := s.CreateToken("bad-window", "viewer", days)
		if err == nil {
			t.Fatalf("expires_in_days=%d should be rejected", days)
		}
		if !strings.Contains(err.Error(), "between 1 and 365") {
			t.Errorf("error for %d days should name the allowed range, got: %v", days, err)
		}
	}
}

func TestCreateToken_BoundaryValuesAccepted(t *testing.T) {
	s := testStore()

	if _, err := s.CreateToken("one-day", "viewer", MinTokenExpiryDays); err != nil {
		t.Errorf("expires_in_days=%d should be accepted: %v", MinTokenExpiryDays, err)
	}
	if _, err := s.CreateToken("one-year", "viewer", MaxTokenExpiryDays); err != nil {
		t.Errorf("expires_in_days=%d should be accepted: %v", MaxTokenExpiryDays, err)
	}
}

func TestAuthenticateToken_ExpiredTokenRefused(t *testing.T) {
	s := testStore()

	plaintext, err := s.CreateToken("ci-deploy", "operator", 0)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	// Wind the expiry back so the token is past its window.
	past := time.Now().Add(-1 * time.Hour)
	s.mu.Lock()
	s.tokens["ci-deploy"].ExpiresAt = &past
	s.mu.Unlock()

	_, _, err = s.AuthenticateToken(plaintext)
	if err == nil {
		t.Fatal("an expired token must be refused")
	}

	var expired *TokenExpiredError
	if !errors.As(err, &expired) {
		t.Fatalf("error should be *TokenExpiredError, got %T: %v", err, err)
	}
	if expired.Name != "ci-deploy" {
		t.Errorf("refusal should name the token, got %q", expired.Name)
	}
	if !strings.Contains(err.Error(), "ci-deploy") || !strings.Contains(err.Error(), "expired") {
		t.Errorf("refusal message should say which token expired, got: %v", err)
	}

	// SECURITY: the refusal must never carry the hash or the plaintext.
	s.mu.RLock()
	hash := s.tokens["ci-deploy"].Hash
	s.mu.RUnlock()
	if strings.Contains(err.Error(), hash) || strings.Contains(err.Error(), plaintext) {
		t.Error("refusal message leaked the token hash or value")
	}

	// The boolean wrapper agrees.
	if _, _, ok := s.ValidateToken(plaintext); ok {
		t.Error("ValidateToken must return false for an expired token")
	}
}

func TestAuthenticateToken_ExpiredTokenDoesNotUpdateLastUsed(t *testing.T) {
	s := testStore()

	plaintext, err := s.CreateToken("stale", "viewer", 0)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	past := time.Now().Add(-1 * time.Hour)
	s.mu.Lock()
	s.tokens["stale"].ExpiresAt = &past
	s.mu.Unlock()

	if _, _, err := s.AuthenticateToken(plaintext); err == nil {
		t.Fatal("expected refusal")
	}

	s.mu.RLock()
	lastUsed := s.tokens["stale"].LastUsed
	s.mu.RUnlock()
	if !lastUsed.IsZero() {
		t.Error("a refused token must not record a last-used time")
	}
}

func TestAuthenticateToken_UnknownTokenIsNotFound(t *testing.T) {
	s := testStore()

	_, _, err := s.AuthenticateToken("sharko_00000000000000000000000000000000")
	if !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("unknown token should give ErrTokenNotFound, got %v", err)
	}

	_, _, err = s.AuthenticateToken("not-a-token")
	if !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("malformed token should give ErrTokenNotFound, got %v", err)
	}
}

func TestAuthenticateToken_RevokedTokenIsNotFound(t *testing.T) {
	s := testStore()

	plaintext, err := s.CreateToken("gone", "viewer", 0)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	if err := s.RevokeToken("gone"); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}

	// A revoked token must look exactly like a token that never existed —
	// no hint that the name was ever real.
	_, _, err = s.AuthenticateToken(plaintext)
	if !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("revoked token should give ErrTokenNotFound, got %v", err)
	}
}

func TestRenewToken_ExtendsExpiryWithoutChangingSecret(t *testing.T) {
	s := testStore()

	plaintext, err := s.CreateToken("renew-me", "operator", 2)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	s.mu.RLock()
	originalHash := s.tokens["renew-me"].Hash
	originalExpiry := *s.tokens["renew-me"].ExpiresAt
	s.mu.RUnlock()

	renewed, err := s.RenewToken("renew-me", 0)
	if err != nil {
		t.Fatalf("RenewToken: %v", err)
	}
	if renewed.ExpiresAt == nil {
		t.Fatal("renewed token must have an expiry")
	}
	if !renewed.ExpiresAt.After(originalExpiry) {
		t.Errorf("renewed expiry %s should be later than %s", renewed.ExpiresAt, originalExpiry)
	}
	if renewed.Status != TokenStatusActive {
		t.Errorf("status = %q, want %q", renewed.Status, TokenStatusActive)
	}
	if renewed.Hash != "" {
		t.Error("renew response must not carry the hash")
	}

	// The secret is untouched — the same plaintext still authenticates.
	s.mu.RLock()
	newHash := s.tokens["renew-me"].Hash
	s.mu.RUnlock()
	if newHash != originalHash {
		t.Error("renew must not change the stored secret")
	}
	if _, _, ok := s.ValidateToken(plaintext); !ok {
		t.Error("the original token value must still work after renew")
	}
}

// v4-wave2 review H1: ownership is what the renew split keys off. A token
// belongs to whoever created it, or to itself — nothing else counts, and a
// token with no creator recorded belongs to nobody.
func TestTokenOwnedBy(t *testing.T) {
	s := testStore()

	if _, err := s.CreateTokenFor("alice", "alices-ci", "operator", 0); err != nil {
		t.Fatalf("CreateTokenFor: %v", err)
	}
	if _, err := s.CreateToken("orphan", "operator", 0); err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	cases := []struct {
		name, user string
		want       bool
	}{
		{"alices-ci", "alice", true}, // she created it
		{"alices-ci", "bob", false},  // he did not
		{"orphan", "orphan", true},   // a token authenticates as itself
		{"orphan", "alice", false},   // nobody is recorded against it
		{"orphan", "", false},        // an unauthenticated caller owns nothing
		{"no-such-token", "alice", false},
	}
	for _, c := range cases {
		if got := s.TokenOwnedBy(c.name, c.user); got != c.want {
			t.Errorf("TokenOwnedBy(%q, %q) = %v, want %v", c.name, c.user, got, c.want)
		}
	}

	// The creator is visible on the read shape, and the hash still is not.
	view, ok := s.GetToken("alices-ci")
	if !ok {
		t.Fatal("GetToken: token missing")
	}
	if view.CreatedBy != "alice" {
		t.Errorf("CreatedBy = %q, want alice", view.CreatedBy)
	}
	if view.Hash != "" {
		t.Error("the read shape must never carry the hash")
	}
}

func TestRenewToken_RevivesAnExpiredToken(t *testing.T) {
	s := testStore()

	plaintext, err := s.CreateToken("lapsed", "viewer", 0)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	past := time.Now().Add(-1 * time.Hour)
	s.mu.Lock()
	s.tokens["lapsed"].ExpiresAt = &past
	s.mu.Unlock()

	if _, _, ok := s.ValidateToken(plaintext); ok {
		t.Fatal("token should be refused before renew")
	}
	if _, err := s.RenewToken("lapsed", 30); err != nil {
		t.Fatalf("RenewToken: %v", err)
	}
	if _, _, ok := s.ValidateToken(plaintext); !ok {
		t.Error("token should work again after renew")
	}
}

func TestRenewToken_NotFoundAndBadWindow(t *testing.T) {
	s := testStore()

	_, err := s.RenewToken("nope", 0)
	if err == nil {
		t.Error("renewing an unknown token should fail")
	}
	// v4-wave2 review L1: the handler decides its 404 on this type, not on
	// the word "not found" appearing somewhere in the message.
	var notFound *TokenNotFoundError
	if !errors.As(err, &notFound) {
		t.Errorf("RenewToken should return *TokenNotFoundError, got %T (%v)", err, err)
	} else if notFound.Name != "nope" {
		t.Errorf("the error should name the token asked for, got %q", notFound.Name)
	}
	if err := s.RevokeToken("nope"); !errors.As(err, &notFound) {
		t.Errorf("RevokeToken should return *TokenNotFoundError, got %T (%v)", err, err)
	}

	if _, err := s.CreateToken("bounded", "viewer", 0); err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	if _, err := s.RenewToken("bounded", 400); err == nil {
		t.Error("renew should reject a window outside 1-365")
	}
}

// Tokens stored before expiry existed have no expiry date. We do NOT
// force-expire them — they keep working, and the list says plainly that they
// have no expiry so an operator knows to recreate them.
func TestLegacyTokenWithoutExpiry_KeepsWorkingAndSaysSo(t *testing.T) {
	s := testStore()

	plaintext, err := s.CreateToken("legacy", "operator", 0)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	s.mu.Lock()
	s.tokens["legacy"].ExpiresAt = nil // as if stored before expiry existed
	s.mu.Unlock()

	username, role, err := s.AuthenticateToken(plaintext)
	if err != nil {
		t.Fatalf("a legacy token must keep working, got: %v", err)
	}
	if username != "legacy" || role != "operator" {
		t.Errorf("got user=%q role=%q, want legacy/operator", username, role)
	}

	tok := s.ListTokens()[0]
	if tok.Status != TokenStatusLegacyNoExpiry {
		t.Errorf("status = %q, want %q", tok.Status, TokenStatusLegacyNoExpiry)
	}
	if tok.ExpiresAt != nil {
		t.Error("a legacy token must report no expiry date")
	}
	if tok.Expired {
		t.Error("a legacy token must not be marked expired")
	}
}

// ListTokens is the one place token metadata leaves the process. It must
// never carry the bcrypt hash.
func TestListTokens_NeverCarriesTheHash(t *testing.T) {
	s := testStore()

	if _, err := s.CreateToken("visible", "viewer", 0); err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	for _, tok := range s.ListTokens() {
		if tok.Hash != "" {
			t.Errorf("ListTokens leaked a hash for %q", tok.Name)
		}
	}
}
