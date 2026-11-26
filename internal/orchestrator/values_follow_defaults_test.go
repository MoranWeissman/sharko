package orchestrator

import (
	"strings"
	"testing"
)

func TestFollowChartDefaults_MovesOnlyWhatNobodyChanged(t *testing.T) {
	oldDefaults := []byte(`image:
  repository: ghcr.io/stefanprodan/podinfo
  tag: 6.7.1
replicas: 1
color: "#34577c"
probe:
  timeout: 5
args:
  - --level=info
note: |
  old text
empty:
`)
	newDefaults := []byte(`image:
  repository: ghcr.io/stefanprodan/podinfo
  tag: 6.8.0
replicas: 2
color: "#112233"
probe:
  timeout: 10
args:
  - --level=info
  - --new
note: |
  new text
empty:
added: true
`)
	file := []byte(`# my comment
image:
  repository: ghcr.io/stefanprodan/podinfo
  tag: 6.7.1   # the chart's own tag

replicas: 5
color: "#34577c"
probe:
  timeout: 5
args:
  - --level=info
note: |
  old text
empty:
`)
	res := followChartDefaults(file, oldDefaults, newDefaults)
	want := `# my comment
image:
  repository: ghcr.io/stefanprodan/podinfo
  tag: 6.8.0   # the chart's own tag

replicas: 5
color: "#112233"
probe:
  timeout: 10
args:
  - --level=info
note: |
  old text
empty:
`
	if string(res.File) != want {
		t.Fatalf("file after =\n%s\nwant\n%s", res.File, want)
	}
	var moved, kept []string
	for _, m := range res.Moved {
		moved = append(moved, m.Path+"="+m.Old+">"+m.New)
	}
	for _, k := range res.Kept {
		kept = append(kept, k.Path)
	}
	if got, w := strings.Join(moved, " "), "color=#34577c>#112233 image.tag=6.7.1>6.8.0 probe.timeout=5>10"; got != w {
		t.Errorf("moved = %q, want %q", got, w)
	}
	// A list and a multi-line text that still equal the old default cannot
	// be edited one line at a time. They are reported, never silently left.
	if got, w := strings.Join(kept, " "), "args note"; got != w {
		t.Errorf("kept = %q, want %q", got, w)
	}
}

func TestFollowChartDefaults_NothingToDo_ReturnsTheSameBytes(t *testing.T) {
	defaults := []byte("image:\n  tag: 6.7.1\n")
	file := []byte("# only comments\n")
	if res := followChartDefaults(file, defaults, []byte("image:\n  tag: 6.8.0\n")); string(res.File) != string(file) || len(res.Moved)+len(res.Kept) != 0 {
		t.Errorf("a comments-only file must be left exactly as it is: %+v", res)
	}
	userSet := []byte("image:\n  tag: 6.5.0\n")
	if res := followChartDefaults(userSet, defaults, []byte("image:\n  tag: 6.8.0\n")); string(res.File) != string(userSet) || len(res.Moved)+len(res.Kept) != 0 {
		t.Errorf("a value a person chose must never move: %+v", res)
	}
}

func TestFollowChartDefaults_QuotedTextStaysText(t *testing.T) {
	// "1.10" must not come back as the number 1.1.
	res := followChartDefaults(
		[]byte("version: \"1.9\"\nflag: \"true\"\n"),
		[]byte("version: \"1.9\"\nflag: \"true\"\n"),
		[]byte("version: \"1.10\"\nflag: \"false\"\n"))
	want := "version: \"1.10\"\nflag: \"false\"\n"
	if string(res.File) != want {
		t.Errorf("got %q, want %q", res.File, want)
	}
}

func TestFollowChartDefaults_FlowStyleIsReportedNotEdited(t *testing.T) {
	file := []byte("image: {tag: 6.7.1}\n")
	res := followChartDefaults(file, []byte("image:\n  tag: 6.7.1\n"), []byte("image:\n  tag: 6.8.0\n"))
	if string(res.File) != string(file) {
		t.Errorf("a one-line block must not be edited in place: %q", res.File)
	}
	if len(res.Kept) != 1 || res.Kept[0].Path != "image.tag" {
		t.Errorf("it must be reported as kept: %+v", res.Kept)
	}
}

func TestValuesKeptOldDefaultsWarning_ExactSentence(t *testing.T) {
	got := ValuesKeptOldDefaultsWarning("values/global/podinfo.yaml", []KeptDefault{{Path: "args", Old: "- --level=info", New: "- --level=info; - --new"}})
	want := "values/global/podinfo.yaml still sets the old chart's value for: args (- --level=info, new chart default - --level=info; - --new). Sharko did not change these. They stay as they are after the upgrade until you edit that file."
	if got != want {
		t.Errorf("got\n  %q\nwant\n  %q", got, want)
	}
	gotCmp := ValuesNotComparedWarning("podinfo", "values/global/podinfo.yaml")
	wantCmp := "Sharko could not compare the old and new chart of podinfo, so it left values/global/podinfo.yaml as it is. If that file still sets the old chart's image tag, the old image keeps running after this upgrade. Check the file."
	if gotCmp != wantCmp {
		t.Errorf("got\n  %q\nwant\n  %q", gotCmp, wantCmp)
	}
}

func TestCompareChartVersions(t *testing.T) {
	cases := []struct {
		a, b string
		cmp  int
		ok   bool
	}{
		{"6.8.0", "6.7.1", 1, true},
		{"6.7.1", "6.8.0", -1, true},
		{"v1.16.3", "1.16.3", 0, true},
		{"3.0.0-beta.6", "3.0.0", -1, true},
		{"3.0.0", "3.0.0-beta.6", 1, true},
		{"abc", "1.0.0", 0, false},
	}
	for _, tc := range cases {
		cmp, ok := compareChartVersions(tc.a, tc.b)
		if cmp != tc.cmp || ok != tc.ok {
			t.Errorf("compareChartVersions(%q,%q) = %d,%v want %d,%v", tc.a, tc.b, cmp, ok, tc.cmp, tc.ok)
		}
	}
}
