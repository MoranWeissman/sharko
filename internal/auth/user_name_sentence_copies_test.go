package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUserNameSentence_UICopyMatchesServer ties the helper text under the
// Username field in the UI to the sentence the server sends back. The UI
// shows it before the user types, so it is a typed copy; this test makes a
// change to one without the other fail.
func TestUserNameSentence_UICopyMatchesServer(t *testing.T) {
	const want = "A user name can use letters, numbers, dots, dashes and underscores, with no spaces."
	if InvalidUsernameSentence != want {
		t.Fatalf("InvalidUsernameSentence = %q, want %q", InvalidUsernameSentence, want)
	}

	path := filepath.Join("..", "..", "ui", "src", "views", "UserManagement.tsx")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	src := string(b)
	if n := strings.Count(src, InvalidUsernameSentence); n != 1 {
		t.Errorf("%s holds the exact user-name sentence %d time(s), want exactly 1:\n%q", path, n, InvalidUsernameSentence)
	}
}
