package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The docs tell a script to wait with `sharko pr wait <PR number>`, and say
// the number is the last part of the link this command prints. That is only
// true while the waiting line prints the link and not the number. If the line
// changes, this fails, and the docs sentence has to change with it
// (internal/api TestDocsEp3RoundRJoinPins pins the docs side).
func TestWaitingForMergeLine_PrintsTheLinkNotTheNumber(t *testing.T) {
	const link = "https://github.com/example/fleet/pull/12"
	if got, want := waitingForMergeLine(link), "PR opened, waiting for merge: "+link; got != want {
		t.Fatalf("waiting line is %q, want %q", got, want)
	}

	const docsSentence = "The command prints the pull request link, not its number. The number is the last part of the link: for a link that ends in `/pull/12`, run `sharko pr wait 12`."
	for _, rel := range []string{"docs/site/cli/commands.md", "docs/site/user-guide/clusters.md"} {
		b, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("cannot read %s: %v", rel, err)
		}
		flat := strings.Join(strings.Fields(string(b)), " ")
		if !strings.Contains(flat, docsSentence) {
			t.Errorf("%s no longer says where the pr wait number comes from: %q", rel, docsSentence)
		}
	}
}
