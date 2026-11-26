package gitops

import (
	"strings"
	"testing"
)

// Walk 1 L13, walk 2 finding 9: the first turn-off dropped the two header
// comment lines of cluster-addons/<cluster>.yaml.
func TestClusterAddonsMutators_KeepTheHeaderComments(t *testing.T) {
	header := "# Which approved addons run on spoke-1.\n# Turning an addon on or off for this cluster is done by editing this file.\n"
	start, err := SetClusterAddonsAddon(nil, "spoke-1", "reloader", true, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	file := append([]byte(header), start...)

	off, err := SetClusterAddonsAddon(file, "spoke-1", "reloader", false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(off), header) {
		t.Fatalf("turn-off dropped the header:\n%s", off)
	}
	if strings.Count(string(off), "yaml-language-server") != 1 {
		t.Fatalf("schema line doubled or lost:\n%s", off)
	}

	gone, err := RemoveClusterAddonsAddon(file, "spoke-1", "reloader")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(gone), header) {
		t.Fatalf("remove dropped the header:\n%s", gone)
	}
}
