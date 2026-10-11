package orchestrator

import (
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/gitops"
)

func newPreviewDiffTestOrch() *Orchestrator {
	return &Orchestrator{
		paths: RepoPathsConfig{
			ClusterValues: "configuration/addons-clusters-values",
			GlobalValues:  "configuration/addons-global-values",
		},
	}
}

// A values file with no values (empty, comments only, a bare document
// marker) must never preview as "null" — that is a value the file does
// not have. Found by hand on `serve --demo`: enabling datadog on dev-us
// previewed the two-line comment stub Sharko writes as "+null".
func TestBuildFileDiff_NoValuesFileNeverShowsNull(t *testing.T) {
	o := newPreviewDiffTestOrch()
	cases := []struct {
		name    string
		path    string
		content []byte
	}{
		{"v4 cluster stub", V4ClusterValuesDir + "/dev-us/datadog.yaml", clusterValuesStub("datadog", "dev-us")},
		{"v4 global stub", V4GlobalValuesDir + "/datadog.yaml", globalValuesStub("datadog")},
		{"v4 user comments only", V4ClusterValuesDir + "/dev-us/datadog.yaml", []byte("# nothing here yet\n# really\n")},
		{"v3 comments only", "configuration/addons-clusters-values/dev-us.yaml", []byte("# just a comment\n")},
		{"bare document marker", V4GlobalValuesDir + "/datadog.yaml", []byte("---\n")},
		{"whitespace and comments", V4GlobalValuesDir + "/datadog.yaml", []byte("\n\n# a\n\n")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, action := range []string{"create", "delete"} {
				var diff string
				if action == "create" {
					diff = o.buildFileDiff(tc.path, nil, tc.content, action)
				} else {
					diff = o.buildFileDiff(tc.path, tc.content, nil, action)
				}
				if strings.Contains(diff, "null") {
					t.Errorf("%s preview shows null for a file with no values:\n%s", action, diff)
				}
			}
		})
	}
}

// The comment lines Sharko writes itself are shown exactly, in order.
func TestBuildFileDiff_SharkoStubCommentsShown(t *testing.T) {
	o := newPreviewDiffTestOrch()
	path := V4ClusterValuesDir + "/dev-us/datadog.yaml"
	got := o.buildFileDiff(path, nil, clusterValuesStub("datadog", "dev-us"), "create")
	want := "--- " + path + "\n+++ " + path + "\n" +
		"@@ -0,0 +1,2 @@\n" +
		"+# Helm values for datadog on dev-us only. These override values/global/datadog.yaml.\n" +
		"+# An empty file means no cluster-specific overrides.\n"
	if got != want {
		t.Errorf("stub preview:\n got: %q\nwant: %q", got, want)
	}

	gpath := V4GlobalValuesDir + "/datadog.yaml"
	gdiff := o.buildFileDiff(gpath, nil, globalValuesStub("datadog"), "create")
	for _, l := range strings.Split(strings.TrimSpace(string(globalValuesStub("datadog"))), "\n") {
		if !strings.Contains(gdiff, "+"+l+"\n") {
			t.Errorf("global stub preview is missing Sharko's own line %q:\n%s", l, gdiff)
		}
	}

	// The seeded stub with override hints (the ones built from the global
	// file's template, including the one real fact — the cluster name).
	seeded := append(clusterValuesStub("datadog", "dev-us"), '\n')
	seeded = append(seeded, renderV4ClusterOverrideHints([]string{"region", "datadog.site", "global.clusterName"}, "dev-us")...)
	sdiff := o.buildFileDiff(path, nil, seeded, "create")
	for _, l := range strings.Split(strings.TrimSpace(string(seeded)), "\n") {
		if l == "" {
			continue
		}
		if !strings.Contains(sdiff, "+"+l+"\n") {
			t.Errorf("seeded stub preview is missing Sharko's own line %q:\n%s", l, sdiff)
		}
	}
}

// The dev-us.yaml case: a new addon block whose "enabled: true" line also
// appears under other addons. The old diff hid that line (it "existed"
// already), leaving a bare "+    datadog:" with nothing under it.
func TestBuildFileDiff_RepeatedLinesShownWhereTheyChange(t *testing.T) {
	o := newPreviewDiffTestOrch()
	path := "cluster-addons/dev-us.yaml"
	// Build the old file the way Sharko writes it: two addons already on,
	// each with its own "enabled: true" line.
	old, err := gitops.SetClusterAddonsAddon(nil, "dev-us", "cert-manager", true, nil, nil)
	if err != nil {
		t.Fatalf("seed cert-manager: %v", err)
	}
	old, err = gitops.SetClusterAddonsAddon(old, "dev-us", "metrics-server", true, nil, nil)
	if err != nil {
		t.Fatalf("seed metrics-server: %v", err)
	}
	updated, err := gitops.SetClusterAddonsAddon(old, "dev-us", "datadog", true, nil, nil)
	if err != nil {
		t.Fatalf("SetClusterAddonsAddon: %v", err)
	}
	diff := o.buildFileDiff(path, old, updated, "update")

	// Find the added datadog line and check the very next line is its
	// own added enabled: true.
	lines := strings.Split(diff, "\n")
	found := false
	for i, l := range lines {
		if strings.HasPrefix(l, "+") && strings.TrimSpace(l[1:]) == "datadog:" {
			found = true
			if i+1 >= len(lines) || !strings.HasPrefix(lines[i+1], "+") || strings.TrimSpace(lines[i+1][1:]) != "enabled: true" {
				t.Errorf("the new datadog block is shown without its enabled: true line:\n%s", diff)
			}
		}
	}
	if !found {
		t.Fatalf("diff does not show the new datadog block:\n%s", diff)
	}
	// Nothing that did not change is shown as removed.
	for _, l := range lines {
		if strings.HasPrefix(l, "-") && !strings.HasPrefix(l, "---") {
			t.Errorf("diff shows a removed line on a pure addition: %q\n%s", l, diff)
		}
	}
}

// Pins the unified format: in order, hunk header, three lines of context.
func TestBuildFileDiff_UnifiedFormat(t *testing.T) {
	o := newPreviewDiffTestOrch()
	path := "configuration/addons-catalog.yaml"
	old := "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\n"
	upd := "a\nb\nc\nd\nE\nf\ng\nh\ni\nj\nk\nl\nm\n"
	got := o.buildFileDiff(path, []byte(old), []byte(upd), "update")
	want := "--- " + path + "\n+++ " + path + "\n" +
		"@@ -2,7 +2,7 @@\n b\n c\n d\n-e\n+E\n f\n g\n h\n" +
		"@@ -10,3 +10,4 @@\n j\n k\n l\n+m\n"
	if got != want {
		t.Errorf("unified diff:\n got: %q\nwant: %q", got, want)
	}

	// Changes six unchanged lines apart share one hunk (GNU diff -u rule).
	old3 := "a\nb\nc\nd\ne\nf\ng\nh\n"
	upd3 := "A\nb\nc\nd\ne\nf\ng\nH\n"
	got3 := o.buildFileDiff(path, []byte(old3), []byte(upd3), "update")
	want3 := "--- " + path + "\n+++ " + path + "\n" +
		"@@ -1,8 +1,8 @@\n-a\n+A\n b\n c\n d\n e\n f\n g\n-h\n+H\n"
	if got3 != want3 {
		t.Errorf("one merged hunk:\n got: %q\nwant: %q", got3, want3)
	}

	// Two changes far apart are two hunks.
	old2 := "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n"
	upd2 := "x\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\ny\n"
	got2 := o.buildFileDiff(path, []byte(old2), []byte(upd2), "update")
	want2 := "--- " + path + "\n+++ " + path + "\n" +
		"@@ -1,4 +1,4 @@\n-1\n+x\n 2\n 3\n 4\n" +
		"@@ -9,4 +9,4 @@\n 9\n 10\n 11\n-12\n+y\n"
	if got2 != want2 {
		t.Errorf("two hunks:\n got: %q\nwant: %q", got2, want2)
	}

	// No change, no hunk.
	if got := o.buildFileDiff(path, []byte(old), []byte(old), "update"); got != "--- "+path+"\n+++ "+path+"\n" {
		t.Errorf("unchanged file printed hunks: %q", got)
	}
}

// No secret value ever reaches a preview in the new format — not from a
// value under a secret key, not from a value that looks like a secret,
// and not from a comment (head, inline or foot), in v3 and v4 values files.
func TestBuildFileDiff_SecretsNeverShownInNewFormat(t *testing.T) {
	o := newPreviewDiffTestOrch()
	jwt := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"
	old := []byte("# old admin password: hunter2\n" +
		"region: eu-west-1\n" +
		"password: oldpass-111\n" +
		"api:\n  key: oldkey-222 # rotate: oldkey-333\n")
	upd := []byte("# Helm values for datadog on dev-us only. These override values/global/datadog.yaml.\n" +
		"# clusterName: leaked-444\n" +
		"# password: newpass-555\n" +
		"region: eu-west-1\n" +
		"password: newpass-666\n" +
		"someField: " + jwt + "\n" +
		"api:\n  key: newkey-777 # was newkey-888\n" +
		"replicas: 3\n" +
		"# trailing note: s3cr3t-999\n")
	secrets := []string{"hunter2", "oldpass-111", "oldkey-222", "oldkey-333", "leaked-444",
		"newpass-555", "newpass-666", jwt[:20], "newkey-777", "newkey-888", "s3cr3t-999"}

	for _, path := range []string{
		V4ClusterValuesDir + "/dev-us/datadog.yaml",
		V4GlobalValuesDir + "/datadog.yaml",
		"configuration/addons-clusters-values/dev-us.yaml",
		"configuration/addons-global-values/datadog.yaml",
	} {
		for _, d := range []string{
			o.buildFileDiff(path, old, upd, "update"),
			o.buildFileDiff(path, nil, upd, "create"),
			o.buildFileDiff(path, old, nil, "delete"),
		} {
			for _, s := range secrets {
				if strings.Contains(d, s) {
					t.Errorf("%s: preview leaks %q:\n%s", path, s, d)
				}
			}
			if !strings.Contains(d, "@@ ") {
				t.Errorf("%s: preview has no hunk:\n%s", path, d)
			}
		}
		// The non-secret change is still visible.
		if d := o.buildFileDiff(path, old, upd, "update"); !strings.Contains(d, "+replicas: 3\n") {
			t.Errorf("%s: preview hides the real non-secret change:\n%s", path, d)
		}
	}
}
