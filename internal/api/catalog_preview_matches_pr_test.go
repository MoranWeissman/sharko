package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// Walk 1, serious finding: Preview, before adding an add-on to the catalog,
// showed values/global/<addon>.yaml as three comment lines, while the pull
// request it led to wrote the chart's whole values file (421 lines in the
// walk). A reviewer approved one thing and merged another. The preview now
// goes through the same chart download as the real request, so both write
// the same file.

const previewChartValues = "replicaCount: 1\nimage:\n  repository: example/reloader\n  tag: v2.2.21\nwatchGlobally: true\n"

func addToCatalogBody(t *testing.T, repoURL string, dry bool) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"dry_run": dry,
		"addons": []map[string]any{{
			"name": "reloader", "repo_url": repoURL, "chart": "reloader",
			"version": "2.2.21", "namespace": "reloader",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func postAddToCatalog(t *testing.T, srvFiles map[string][]byte, body []byte) (*httptest.ResponseRecorder, *annotateV4Git) {
	t.Helper()
	gp := newAnnotateV4Git(srvFiles)
	srv := serverWithFullV4Connection(t, gp)
	router := NewRouter(srv, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/catalog/addons", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w, gp
}

type previewBody struct {
	Result struct {
		DryRun *struct {
			FilesToWrite []struct {
				Path   string `json:"path"`
				Action string `json:"action"`
				Diff   string `json:"diff"`
			} `json:"files_to_write"`
		} `json:"dry_run"`
		Warnings []string `json:"warnings"`
	} `json:"result"`
}

// addedLines returns the "+" lines of a create-diff, header lines left out,
// joined back into the file content the diff stands for.
func addedLines(diff string) string {
	var b strings.Builder
	for _, l := range strings.Split(diff, "\n") {
		if strings.HasPrefix(l, "+++") || !strings.HasPrefix(l, "+") {
			continue
		}
		b.WriteString(strings.TrimPrefix(l, "+"))
		b.WriteString("\n")
	}
	return b.String()
}

func TestAddToCatalogPreview_ShowsTheSameValuesFileThePullRequestWrites(t *testing.T) {
	repoURL := localChartRepo(t, "reloader", "2.2.21", previewChartValues)
	files := map[string][]byte{
		orchestrator.BootstrapRootAppPath: []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n"),
	}

	// The preview.
	w, gp := postAddToCatalog(t, files, addToCatalogBody(t, repoURL, true))
	if w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Fatalf("preview status = %d, body: %s", w.Code, w.Body.String())
	}
	var pv previewBody
	if err := json.Unmarshal(w.Body.Bytes(), &pv); err != nil || pv.Result.DryRun == nil {
		t.Fatalf("preview body unreadable: %v (%s)", err, w.Body.String())
	}
	var previewDiff string
	for _, f := range pv.Result.DryRun.FilesToWrite {
		if f.Path == "values/global/reloader.yaml" {
			previewDiff = f.Diff
		}
	}
	if previewDiff == "" {
		t.Fatalf("preview does not list values/global/reloader.yaml: %s", w.Body.String())
	}
	if len(gp.writes) != 0 {
		t.Errorf("a preview wrote %d files, want 0", len(gp.writes))
	}

	// The real request.
	w2, gp2 := postAddToCatalog(t, files, addToCatalogBody(t, repoURL, false))
	if w2.Code != http.StatusCreated {
		t.Fatalf("real status = %d, body: %s", w2.Code, w2.Body.String())
	}
	written := string(gp2.writes["values/global/reloader.yaml"])
	if !strings.Contains(written, "tag: v2.2.21") || strings.Contains(written, "An empty file means") {
		t.Fatalf("the real pull request does not carry the chart values: %q", written)
	}

	// The preview shows the same settings the pull request writes. It
	// leaves the comment lines out on purpose (see PreviewValuesFileNote),
	// so compare with the comments taken off the real file.
	if got, want := addedLines(previewDiff), withoutComments(written); got != want {
		t.Errorf("the preview of values/global/reloader.yaml is not what the pull request writes.\npreview:\n%s\nwritten (comments removed):\n%s", got, want)
	}
	// The note that says what the preview leaves out is pinned word for word.
	const wantNote = "Values files are shown here without their comment lines, and values that look like passwords are hidden. The pull request writes the whole file."
	found := false
	for _, wn := range pv.Result.Warnings {
		if wn == wantNote {
			found = true
		}
	}
	if !found {
		t.Errorf("preview warnings %q do not carry the exact note %q", pv.Result.Warnings, wantNote)
	}
	// And the old comment-only stub is gone from the preview.
	if strings.Contains(previewDiff, "An empty file means the chart's own defaults.") {
		t.Errorf("the preview still shows the empty-file stub: %s", previewDiff)
	}
}

func withoutComments(file string) string {
	var b strings.Builder
	for _, l := range strings.Split(file, "\n") {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		b.WriteString(l)
		b.WriteString("\n")
	}
	return b.String()
}
