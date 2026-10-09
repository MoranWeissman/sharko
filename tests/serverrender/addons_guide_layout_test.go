package serverrender

// v4.0.4 U9d: the addons guide showed the old (v3) repo layout —
// addons/<name>/values.yaml with a clusters/ folder under it — which is
// not where a v4 repo keeps anything. Pin the v4 paths and ban the old
// picture so it cannot come back.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddonsGuideShowsTheV4RepoLayout(t *testing.T) {
	root := repoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "docs", "site", "user-guide", "addons.md"))
	if err != nil {
		t.Fatalf("read addons guide: %v", err)
	}
	text := string(body)

	for _, want := range []string{
		"catalog.yaml",
		"values/\n  global/\n    cert-manager.yaml",
		"  clusters/\n    my-cluster/\n      cert-manager.yaml",
		"cluster-addons/\n  my-cluster.yaml",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("addons guide no longer shows the v4 layout piece %q", want)
		}
	}

	for _, banned := range []string{
		"addons/\n  cert-manager/\n    values.yaml",
		"adds the addon's directory structure",
	} {
		if strings.Contains(text, banned) {
			t.Errorf("addons guide shows the old v3 layout again: %q", banned)
		}
	}
}
