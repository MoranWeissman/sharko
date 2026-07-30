package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// v4GateGit answers with the engine pin (making the repo look v4) and
// records every write attempt, so a test can prove the gate fired BEFORE
// anything was committed.
type v4GateGit struct {
	v4     bool
	writes []string
}

func (g *v4GateGit) GetFileContent(_ context.Context, path, _ string) ([]byte, error) {
	if path == orchestrator.BootstrapRootAppPath && g.v4 {
		return []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n"), nil
	}
	return nil, gitprovider.ErrFileNotFound
}
func (g *v4GateGit) ListDirectory(context.Context, string, string) ([]string, error) { return nil, nil }
func (g *v4GateGit) ListPullRequests(context.Context, string) ([]gitprovider.PullRequest, error) {
	return nil, nil
}
func (g *v4GateGit) TestConnection(context.Context) error { return nil }
func (g *v4GateGit) CreateBranch(_ context.Context, name, _ string) error {
	g.writes = append(g.writes, "branch:"+name)
	return nil
}
func (g *v4GateGit) CreateOrUpdateFile(_ context.Context, path string, _ []byte, _, _ string) error {
	g.writes = append(g.writes, "file:"+path)
	return nil
}
func (g *v4GateGit) BatchCreateFiles(_ context.Context, files map[string][]byte, _, _ string) error {
	for p := range files {
		g.writes = append(g.writes, "file:"+p)
	}
	return nil
}
func (g *v4GateGit) DeleteFile(_ context.Context, path, _, _ string) error {
	g.writes = append(g.writes, "delete:"+path)
	return nil
}
func (g *v4GateGit) CreatePullRequest(_ context.Context, title, _, _, _ string) (*gitprovider.PullRequest, error) {
	g.writes = append(g.writes, "pr:"+title)
	return &gitprovider.PullRequest{ID: 1, URL: "https://example.com/pr/1"}, nil
}
func (g *v4GateGit) MergePullRequest(context.Context, int) error { return nil }
func (g *v4GateGit) GetPullRequestStatus(context.Context, int) (string, error) {
	return "open", nil
}
func (g *v4GateGit) DeleteBranch(context.Context, string) error { return nil }

func serverWithGitOverride(t *testing.T, gp gitprovider.GitProvider) *Server {
	t.Helper()
	srv := newIsolatedTestServer(t)
	srv.connSvc.SetGitProviderOverride(gp)
	return srv
}

// v3ValuesSurfaces enumerates every endpoint that reads or writes a
// hard-coded v3 values path. On a v4 repo each must answer with the single
// plain-words 409 rather than silently reading (and reporting empty) or
// writing a file the engine will never load.
func v3ValuesSurfaces() []struct {
	name   string
	method string
	path   string
	body   string
} {
	return []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"SetAddonValues", http.MethodPut, "/api/v1/addons/cert-manager/values", `{"values":"replicas: 1\n"}`},
		{"SetClusterAddonValues", http.MethodPut, "/api/v1/clusters/prod-eu/addons/cert-manager/values", `{"values":"replicas: 1\n"}`},
		{"GetAddonValuesSchema", http.MethodGet, "/api/v1/addons/cert-manager/values-schema", ""},
		{"GetClusterAddonValues", http.MethodGet, "/api/v1/clusters/prod-eu/addons/cert-manager/values", ""},
		{"GetAddonValuesRecentPRs", http.MethodGet, "/api/v1/addons/cert-manager/values/recent-prs", ""},
		{"GetClusterAddonValuesRecentPRs", http.MethodGet, "/api/v1/clusters/prod-eu/addons/cert-manager/values/recent-prs", ""},
		{"PreviewMergeAddonValues", http.MethodPost, "/api/v1/addons/cert-manager/values/preview-merge", `{}`},
		{"SetAddonAIOptOut", http.MethodPut, "/api/v1/addons/cert-manager/values/ai-opt-out", `{"opt_out":true}`},
	}
}

func TestV3ValuesSurfaces_RefuseOnV4Repo(t *testing.T) {
	for _, tc := range v3ValuesSurfaces() {
		t.Run(tc.name, func(t *testing.T) {
			gp := &v4GateGit{v4: true}
			srv := serverWithGitOverride(t, gp)
			router := NewRouter(srv, nil)

			var reader *bytes.Reader
			if tc.body != "" {
				reader = bytes.NewReader([]byte(tc.body))
			} else {
				reader = bytes.NewReader(nil)
			}
			req := httptest.NewRequest(tc.method, tc.path, reader)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409 (body: %s)", w.Code, w.Body.String())
			}
			var body map[string]any
			if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
				t.Fatalf("response is not JSON: %v", err)
			}
			msg, _ := body["error"].(string)
			if msg != V4EditorUnsupportedMessage {
				t.Errorf("error = %q, want the shared plain-words message", msg)
			}
			if len(gp.writes) != 0 {
				t.Errorf("the refusal still wrote to git: %v", gp.writes)
			}
		})
	}
}

// TestV3ValuesSurfaces_UnchangedOnV3Repo — with no engine pin the gate is
// inert, so none of these endpoints answers 409 with the v4 message. (They
// may 404/502 for their own ordinary reasons; the point is the gate does
// not fire.)
func TestV3ValuesSurfaces_UnchangedOnV3Repo(t *testing.T) {
	for _, tc := range v3ValuesSurfaces() {
		t.Run(tc.name, func(t *testing.T) {
			gp := &v4GateGit{v4: false}
			srv := serverWithGitOverride(t, gp)
			router := NewRouter(srv, nil)

			req := httptest.NewRequest(tc.method, tc.path, bytes.NewReader([]byte(tc.body)))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if strings.Contains(w.Body.String(), "v4 layout") {
				t.Fatalf("the v4 gate fired on a v3 repo: %d %s", w.Code, w.Body.String())
			}
		})
	}
}
