package auth

// Pre-release fix round 2026-11-26, story C.

import (
	"errors"
	"testing"
)

func walkCStore(t *testing.T) *Store {
	t.Helper()
	s := &Store{
		mode:       ModeLocal,
		users:      make(map[string]*UserAccount),
		passHash:   make(map[string]string),
		userTokens: make(map[string]string),
		tokens:     make(map[string]*APIToken),
	}
	if err := s.AddUser("alice", "correct-horse-battery-staple", "operator"); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestUpdateUserFields_LeftOutPartsAreKept(t *testing.T) {
	s := walkCStore(t)
	off := false
	if err := s.UpdateUserFields("alice", &off, ""); err != nil {
		t.Fatal(err)
	}
	if u := s.GetUser("alice"); u.Enabled || u.Role != "operator" {
		t.Fatalf("after disable: %+v", u)
	}
	// Role only: the account stays off.
	if err := s.UpdateUserFields("alice", nil, "admin"); err != nil {
		t.Fatal(err)
	}
	if u := s.GetUser("alice"); u.Enabled {
		t.Fatal("changing the role turned the disabled account back on")
	} else if u.Role != "admin" {
		t.Fatalf("role = %q", u.Role)
	}
	// Nothing given: nothing changes.
	if err := s.UpdateUserFields("alice", nil, ""); err != nil {
		t.Fatal(err)
	}
	if u := s.GetUser("alice"); u.Enabled || u.Role != "admin" {
		t.Fatalf("an empty update changed the user: %+v", u)
	}
	if err := s.UpdateUserFields("nobody", nil, "admin"); !IsInputError(err) {
		t.Errorf("unknown user: %v", err)
	}
}

func TestTokenAndUserNamesNeverCollide(t *testing.T) {
	s := walkCStore(t)
	if _, err := s.CreateTokenFor("alice", "alice", "viewer", 0); err == nil ||
		err.Error() != `a user is already named "alice". Pick another name for the API key.` {
		t.Errorf("key named like a user: %v", err)
	}
	if _, err := s.CreateTokenFor("alice", "ci", "viewer", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser("ci", "viewer"); err == nil ||
		err.Error() != `an API key is already named "ci". Pick another user name, or revoke that key first.` {
		t.Errorf("user named like a key: %v", err)
	} else if !IsInputError(err) {
		t.Errorf("must be an input error (400), got %T", err)
	}
}

func TestAuthenticateToken_OwnerDisabledAndNameClash(t *testing.T) {
	s := walkCStore(t)
	plain, err := s.CreateTokenFor("alice", "alices-key", "viewer", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AuthenticateToken(plain); err != nil {
		t.Fatalf("fresh key: %v", err)
	}

	if err := s.UpdateUser("alice", false, ""); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.AuthenticateToken(plain)
	var off *TokenOwnerDisabledError
	if !errors.As(err, &off) || off.Name != "alices-key" {
		t.Fatalf("key of a disabled user: %v", err)
	}
	if err := s.UpdateUser("alice", true, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AuthenticateToken(plain); err != nil {
		t.Fatalf("after enable: %v", err)
	}

	// A key with no recorded creator, or one whose creator is not a user,
	// is not touched by the rule.
	legacy, err := s.CreateToken("legacy-key", "viewer", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AuthenticateToken(legacy); err != nil {
		t.Fatalf("key with no creator: %v", err)
	}

	// A user that gets the name of a stored key (added by hand) makes the
	// key unusable.
	if err := s.AddUser("legacy-key", "correct-horse-battery-staple", "admin"); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.AuthenticateToken(legacy)
	var clash *TokenNameIsUserError
	if !errors.As(err, &clash) {
		t.Fatalf("key that shares a user's name: %v", err)
	}
}

func TestRevokeTokensCreatedBy(t *testing.T) {
	s := walkCStore(t)
	for _, n := range []string{"b-key", "a-key"} {
		if _, err := s.CreateTokenFor("alice", n, "viewer", 0); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreateTokenFor("someone-else", "other-key", "viewer", 0); err != nil {
		t.Fatal(err)
	}
	got, err := s.RevokeTokensCreatedBy("alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "a-key" || got[1] != "b-key" {
		t.Errorf("revoked = %v, want [a-key b-key]", got)
	}
	if _, ok := s.GetToken("other-key"); !ok {
		t.Error("somebody else's key was revoked too")
	}
	if got, _ := s.RevokeTokensCreatedBy(""); got != nil {
		t.Error("an empty name must revoke nothing")
	}
}
