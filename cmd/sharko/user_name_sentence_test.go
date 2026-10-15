package main

import (
	"testing"

	"github.com/MoranWeissman/sharko/internal/auth"
)

// TestUserCreateHelpCarriesServerSentence ties the user-name rule in the CLI
// help to the sentence the server sends back (auth.InvalidUsernameSentence).
// If either one is changed alone, this fails.
func TestUserCreateHelpCarriesServerSentence(t *testing.T) {
	want := "Create a new user. " + auth.InvalidUsernameSentence
	if userCreateCmd.Long != want {
		t.Errorf("sharko user create help drifted from the server sentence:\n got %q\nwant %q", userCreateCmd.Long, want)
	}
}
