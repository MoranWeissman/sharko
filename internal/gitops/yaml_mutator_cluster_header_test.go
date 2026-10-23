package gitops

import (
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

const testFileHeader = "# The clusters Sharko manages.\n# A second line of notes.\n"

func headedFile(t *testing.T, names ...string) []byte {
	t.Helper()
	data := []byte("clusters:\n")
	var err error
	for _, n := range names {
		data, err = AddClusterEntry(data, ClusterEntryInput{Name: n})
		if err != nil {
			t.Fatal(err)
		}
	}
	return append([]byte(testFileHeader), data...)
}

// The top comments survive every rewrite, once, with no blank line added
// (episode 3 dry run, finding 12).
func TestClusterMutators_KeepTheTopComments(t *testing.T) {
	start := headedFile(t, "a")
	writes := map[string]func([]byte) ([]byte, error){
		"add":       func(d []byte) ([]byte, error) { return AddClusterEntry(d, ClusterEntryInput{Name: "b"}) },
		"add again": func(d []byte) ([]byte, error) { return AddClusterEntry(d, ClusterEntryInput{Name: "a"}) },
		"remove":    func(d []byte) ([]byte, error) { return RemoveClusterEntry(headedFile(t, "a", "b"), "b") },
		"enable":    func(d []byte) ([]byte, error) { return EnableAddonLabel(d, "a", "datadog") },
	}
	for name, write := range writes {
		t.Run(name, func(t *testing.T) {
			out, err := write(start)
			if err != nil {
				t.Fatal(err)
			}
			text := string(out)
			if !strings.HasPrefix(text, testFileHeader+models.ManagedClustersSchemaHeader+"\n") {
				t.Fatalf("top of the file changed:\n%s", text)
			}
			if strings.Count(text, "# The clusters Sharko manages.") != 1 {
				t.Fatalf("header doubled:\n%s", text)
			}
			if strings.Contains(text, "\n\n") {
				t.Fatalf("a blank line was added:\n%s", text)
			}
		})
	}
}

// Adding a cluster that is already there writes the file back exactly as
// it was, so a take over with nothing to change can open no pull request.
func TestAddClusterEntry_DuplicateLeavesTheBytesAlone(t *testing.T) {
	start := headedFile(t, "a")
	out, err := AddClusterEntry(start, ClusterEntryInput{Name: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(start) {
		t.Fatalf("bytes changed:\nbefore:\n%s\nafter:\n%s", start, out)
	}
}
