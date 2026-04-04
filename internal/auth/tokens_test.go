package auth

import (
	"os"
	"strings"
	"testing"
	"time"
)

func testStore() *Store {
	os.Setenv("SHARKO_AUTH_USER", "admin")
	os.Setenv("SHARKO_AUTH_PASSWORD", "test123")
	return NewStore()
}

func TestCreateToken_Format(t *testing.T) {
	s := testStore()

	plaintext, err := s.CreateToken("test-token", "admin")
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	if !strings.HasPrefix(plaintext, "sharko_") {
		t.Errorf("token should start with sharko_, got %q", plaintext)
	}
	if len(plaintext) != 39 {
		t.Errorf("token should be 39 chars, got %d", len(plaintext))
	}
}

func TestCreateToken_DuplicateName(t *testing.T) {
	s := testStore()

	_, err := s.CreateToken("dup", "admin")
	if err != nil {
		t.Fatalf("first CreateToken failed: %v", err)
	}

	_, err = s.CreateToken("dup", "admin")
	if err == nil {
		t.Fatal("expected error for duplicate name, got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected 'already exists' error, got: %v", err)
	}
}

func TestValidateToken_Valid(t *testing.T) {
	s := testStore()

	plaintext, err := s.CreateToken("valid-token", "operator")
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	username, role, ok := s.ValidateToken(plaintext)
	if !ok {
		t.Fatal("ValidateToken returned false for valid token")
	}
	if username != "valid-token" {
		t.Errorf("expected username 'valid-token', got %q", username)
	}
	if role != "operator" {
		t.Errorf("expected role 'operator', got %q", role)
	}
}

func TestValidateToken_Invalid(t *testing.T) {
	s := testStore()

	_, _, ok := s.ValidateToken("sharko_00000000000000000000000000000000")
	if ok {
		t.Fatal("ValidateToken should return false for invalid token")
	}

	_, _, ok = s.ValidateToken("not-a-token")
	if ok {
		t.Fatal("ValidateToken should return false for non-sharko token")
	}

	_, _, ok = s.ValidateToken("")
	if ok {
		t.Fatal("ValidateToken should return false for empty string")
	}
}

func TestRevokeToken(t *testing.T) {
	s := testStore()

	plaintext, err := s.CreateToken("revoke-me", "admin")
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	// Token should validate before revocation
	_, _, ok := s.ValidateToken(plaintext)
	if !ok {
		t.Fatal("token should be valid before revocation")
	}

	err = s.RevokeToken("revoke-me")
	if err != nil {
		t.Fatalf("RevokeToken failed: %v", err)
	}

	// Token should not validate after revocation
	_, _, ok = s.ValidateToken(plaintext)
	if ok {
		t.Fatal("token should be invalid after revocation")
	}
}

func TestRevokeToken_NotFound(t *testing.T) {
	s := testStore()

	err := s.RevokeToken("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent token, got nil")
	}
}

func TestListTokens(t *testing.T) {
	s := testStore()

	_, err := s.CreateToken("tok-a", "admin")
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}
	_, err = s.CreateToken("tok-b", "viewer")
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	tokens := s.ListTokens()
	if len(tokens) != 2 {
		t.Fatalf("expected 2 tokens, got %d", len(tokens))
	}

	for _, tok := range tokens {
		if tok.Hash != "" {
			t.Errorf("ListTokens should not include hash, got %q for %s", tok.Hash, tok.Name)
		}
		if tok.Name == "" {
			t.Error("ListTokens should include name")
		}
		if tok.Role == "" {
			t.Error("ListTokens should include role")
		}
		if tok.CreatedAt.IsZero() {
			t.Error("ListTokens should include created_at")
		}
	}
}

func TestValidateToken_UpdatesLastUsed(t *testing.T) {
	s := testStore()

	plaintext, err := s.CreateToken("track-usage", "admin")
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	// Before validation, LastUsed should be zero
	s.mu.RLock()
	tok := s.tokens["track-usage"]
	initialLastUsed := tok.LastUsed
	s.mu.RUnlock()

	if !initialLastUsed.IsZero() {
		t.Fatal("LastUsed should be zero before any validation")
	}

	before := time.Now()
	_, _, ok := s.ValidateToken(plaintext)
	if !ok {
		t.Fatal("ValidateToken should succeed")
	}

	s.mu.RLock()
	tok = s.tokens["track-usage"]
	updatedLastUsed := tok.LastUsed
	s.mu.RUnlock()

	if updatedLastUsed.IsZero() {
		t.Fatal("LastUsed should be updated after validation")
	}
	if updatedLastUsed.Before(before) {
		t.Error("LastUsed should be after the validation call")
	}
}
