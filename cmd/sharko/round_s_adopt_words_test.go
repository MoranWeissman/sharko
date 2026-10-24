package main

import (
	"net/http"
	"os"
	"strings"
	"testing"
)

// EP3 dry run round S, S24: the adopt command counts clusters with real
// words, "1 cluster" and "2 clusters", never "cluster(s)".

func adoptReplyServer(t *testing.T) {
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, 200, map[string]interface{}{
			"results": []map[string]interface{}{
				{"name": "a", "status": "success", "git": map[string]interface{}{"pr_url": waitPR, "merged": true}},
				{"name": "b", "status": "success", "git": map[string]interface{}{"pr_url": waitPR, "merged": true}},
			},
		})
	})
}

func runAdopt(t *testing.T, flags map[string]string, args []string) string {
	t.Helper()
	resetFlags(adoptCmd)
	setFlags(t, adoptCmd, flags)
	out, err := captureStdoutT(t, func() error { return adoptCmd.RunE(adoptCmd, args) })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "cluster(s)") {
		t.Errorf("old wording cluster(s) is back: %q", out)
	}
	return out
}

func TestAdopt_CountsClustersInWords(t *testing.T) {
	adoptReplyServer(t)
	if out := runAdopt(t, map[string]string{"yes": "true"}, []string{"a", "b"}); !strings.HasPrefix(out, "Adopting 2 clusters... ") {
		t.Errorf("two clusters: %q", out)
	}
	if out := runAdopt(t, map[string]string{"dry-run": "true"}, []string{"a"}); !strings.HasPrefix(out, "Dry-run: previewing adoption of 1 cluster...\n") {
		t.Errorf("dry run, one cluster: %q", out)
	}
	if out := runAdopt(t, map[string]string{"dry-run": "true"}, []string{"a", "b"}); !strings.HasPrefix(out, "Dry-run: previewing adoption of 2 clusters...\n") {
		t.Errorf("dry run, two clusters: %q", out)
	}
}

func TestAdopt_QuestionCountsClustersInWords(t *testing.T) {
	adoptReplyServer(t)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"a"}, "Adopt 1 cluster: a\nThis opens a pull request that adds it to Sharko. Continue? [y/N]: Aborted.\n"},
		{[]string{"a", "b"}, "Adopt 2 clusters: a, b\nThis opens a pull request that adds them to Sharko. Continue? [y/N]: Aborted.\n"},
	} {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.WriteString("n\n")
		w.Close()
		old := os.Stdin
		os.Stdin = r
		out := runAdopt(t, map[string]string{}, c.args)
		os.Stdin = old
		r.Close()
		if out != c.want {
			t.Errorf("question:\n got %q\nwant %q", out, c.want)
		}
	}
}
