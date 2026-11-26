package auth

import "testing"

func TestLowerRole(t *testing.T) {
	cases := []struct{ key, maker, want string }{
		{"admin", "admin", "admin"},
		{"admin", "operator", "operator"},
		{"admin", "viewer", "viewer"},
		{"operator", "admin", "operator"},
		{"operator", "viewer", "viewer"},
		{"viewer", "admin", "viewer"},
		{"viewer", "viewer", "viewer"},
		// A maker role Sharko does not know never hands the key its own role.
		{"admin", "", "viewer"},
		{"admin", "owner", "viewer"},
	}
	for _, c := range cases {
		if got := LowerRole(c.key, c.maker); got != c.want {
			t.Errorf("LowerRole(%q, %q) = %q, want %q", c.key, c.maker, got, c.want)
		}
	}
}

// A key with no maker on record (made before makers were recorded, or at
// start-up) keeps its own role: there is nobody to compare it with.
func TestAuthenticateToken_KeyWithNoMakerKeepsItsRole(t *testing.T) {
	s := NewStore()
	plain, err := s.CreateToken("old-key", "admin", 0)
	if err != nil {
		t.Fatal(err)
	}
	_, role, err := s.AuthenticateToken(plain)
	if err != nil || role != "admin" {
		t.Errorf("role = %q err = %v, want admin", role, err)
	}
}

func TestReservedNameSentenceIsPinned(t *testing.T) {
	const want = "The name %q is kept for Sharko's own work. Pick another name."
	if ReservedNameSentence != want {
		t.Errorf("ReservedNameSentence = %q, want %q", ReservedNameSentence, want)
	}
	for _, n := range []string{"sharko", "Sharko", "system", "anonymous", "sharko-bot", "unknown", "ai-agent", "webhook"} {
		if !IsReservedName(n) {
			t.Errorf("%q is not kept back", n)
		}
	}
	for _, n := range []string{"admin", "bob", "sharko-ci", "systems"} {
		if IsReservedName(n) {
			t.Errorf("%q is kept back and should not be", n)
		}
	}
}
