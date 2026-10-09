package auth

import (
	"testing"
)

// TestUserGitHubTokenLocalRoundTrip verifies set/get/has/clear in local mode
// (in-memory) — no K8s client required.
func TestUserGitHubTokenLocalRoundTrip(t *testing.T) {
	const (
		username = "alice"
		token    = "ghp_test_token_value_xyz"
		key      = "test-encryption-key"
	)

	s := &Store{
		mode:       ModeLocal,
		users:      map[string]*UserAccount{username: {Username: username, Enabled: true, Role: "viewer"}},
		passHash:   make(map[string]string),
		userTokens: make(map[string]string),
	}

	if s.HasUserGitHubToken(username) {
		t.Fatalf("fresh store should report no token")
	}

	if err := s.SetUserGitHubToken(username, token, key); err != nil {
		t.Fatalf("SetUserGitHubToken: %v", err)
	}

	if !s.HasUserGitHubToken(username) {
		t.Fatalf("expected HasUserGitHubToken=true after Set")
	}

	got, err := s.GetUserGitHubToken(username, key)
	if err != nil {
		t.Fatalf("GetUserGitHubToken: %v", err)
	}
	if got != token {
		t.Fatalf("round-trip mismatch: want %q got %q", token, got)
	}

	// Stored value must NOT be plaintext (encryption-at-rest verification).
	if s.userTokens[username] == token {
		t.Fatalf("stored token is plaintext — encryption did not run")
	}

	// Clear is idempotent.
	if err := s.ClearUserGitHubToken(username); err != nil {
		t.Fatalf("ClearUserGitHubToken: %v", err)
	}
	if s.HasUserGitHubToken(username) {
		t.Fatalf("expected HasUserGitHubToken=false after Clear")
	}
	if err := s.ClearUserGitHubToken(username); err != nil {
		t.Fatalf("second Clear should be a no-op: %v", err)
	}
}

// TestUserGitHubTokenRequiresUser verifies the lookup fails for unknown users
// rather than silently caching ghost entries.
func TestUserGitHubTokenRequiresUser(t *testing.T) {
	s := &Store{
		mode:       ModeLocal,
		users:      map[string]*UserAccount{},
		passHash:   make(map[string]string),
		userTokens: make(map[string]string),
	}

	if err := s.SetUserGitHubToken("ghost", "tok", "key"); err == nil {
		t.Fatal("expected error setting token for unknown user")
	}
}

// TestUserGitHubTokenWrongKeyFailsClean verifies decryption with the wrong
// key returns an error (does not leak garbage).
func TestUserGitHubTokenWrongKeyFailsClean(t *testing.T) {
	const username = "alice"
	s := &Store{
		mode:       ModeLocal,
		users:      map[string]*UserAccount{username: {Username: username, Enabled: true, Role: "viewer"}},
		passHash:   make(map[string]string),
		userTokens: make(map[string]string),
	}

	if err := s.SetUserGitHubToken(username, "ghp_xxx", "right-key"); err != nil {
		t.Fatalf("set: %v", err)
	}

	if _, err := s.GetUserGitHubToken(username, "wrong-key"); err == nil {
		t.Fatal("expected decryption to fail with wrong key")
	}
}

// TestUserGitHubTokenEmptyInputs verifies empty username/token/key produce
// useful errors rather than silently corrupting state.
func TestUserGitHubTokenEmptyInputs(t *testing.T) {
	s := &Store{
		mode:       ModeLocal,
		users:      map[string]*UserAccount{"u": {Username: "u", Enabled: true, Role: "viewer"}},
		passHash:   make(map[string]string),
		userTokens: make(map[string]string),
	}

	if err := s.SetUserGitHubToken("", "tok", "key"); err == nil {
		t.Error("empty username should error")
	}
	if err := s.SetUserGitHubToken("u", "", "key"); err == nil {
		t.Error("empty token should error")
	}
	if err := s.SetUserGitHubToken("u", "tok", ""); err == nil {
		t.Error("empty encryption key should error")
	}
	if _, err := s.GetUserGitHubToken("u", ""); err == nil {
		t.Error("empty encryption key on Get should error")
	}
	if got, err := s.GetUserGitHubToken("", "key"); err != nil || got != "" {
		t.Errorf("empty username on Get: want empty string + nil err, got %q / %v", got, err)
	}
}
