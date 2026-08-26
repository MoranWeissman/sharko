package credsafe

// repourl_phrase_test.go — SafeRepoURLPhrase (B14).
//
// SafeRepoURL returning "" is right for a FIELD and wrong in the middle of a
// SENTENCE. These pin both halves: the credential still never travels, and the
// sentence never ends up with a hole in it.

import (
	"strings"
	"testing"
)

const phraseSentinel = "J3NB-repo-phrase-token-sentinel-9v6c1x-never-leaves-the-server-d8e2"

func TestSafeRepoURLPhrase(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		// want is typed out here, never read back off the code.
		want string
	}{
		{"token in the username position", "https://" + phraseSentinel + "@charts.example/org/repo", "https://charts.example/org/repo"},
		{"token in the password position", "https://x-access-token:" + phraseSentinel + "@charts.example/org/repo", "https://charts.example/org/repo"},
		{"token in the query string", "https://charts.example/org/repo?access_token=" + phraseSentinel, "https://charts.example/org/repo"},
		{"an ordinary address", "https://charts.example/org/repo", "https://charts.example/org/repo"},
		{"no scheme and nowhere to hide one", "charts.example/org/repo", "charts.example/org/repo"},
		{"an scp-style remote, which cannot be taken apart", "git@charts.example:org/repo.git", UnnamedRepoPhrase},
		{"empty", "", UnnamedRepoPhrase},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := SafeRepoURLPhrase(tc.in)
			if got != tc.want {
				t.Errorf("SafeRepoURLPhrase(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if strings.Contains(got, phraseSentinel) {
				t.Errorf("SafeRepoURLPhrase(%q) carried the token through: %q", tc.in, got)
			}
		})
	}
}

// TestSafeRepoURLPhrase_NeverEmpty is the whole reason this function exists.
// An empty string dropped into a sentence leaves the sentence hanging, and a
// reader cannot tell a redaction from a bug.
func TestSafeRepoURLPhrase_NeverEmpty(t *testing.T) {
	for _, in := range []string{
		"",
		"git@charts.example:org/repo.git",
		"://not a url at all",
		"@",
		"?",
		"#",
		"http://%zz",
	} {
		if got := SafeRepoURLPhrase(in); got == "" {
			t.Errorf("SafeRepoURLPhrase(%q) returned the empty string", in)
		}
	}
}

// TestUnnamedRepoPhrase_ReadsAsWords pins the fallback wording exactly. It
// goes on screen, so it is a sentence fragment a person reads, not a marker.
func TestUnnamedRepoPhrase_ReadsAsWords(t *testing.T) {
	const want = "the chart repository"
	if UnnamedRepoPhrase != want {
		t.Errorf("UnnamedRepoPhrase = %q, want exactly %q", UnnamedRepoPhrase, want)
	}
}
