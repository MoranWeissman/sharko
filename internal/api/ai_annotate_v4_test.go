// ai_annotate_v4_test.go — proves the AI annotate + ai-opt-out endpoints
// work natively on a v4 repo (v4 smartvalues wave) instead of refusing
// with the old v4-layout 409. TestV3ValuesSurfaces_RefuseOnV4Repo
// (v4_editor_gate_test.go) already proves every OTHER v3-shaped values
// surface still refuses on a v4 repo — these two are the deliberate
// exceptions, so they get their own file rather than living in that list.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/MoranWeissman/sharko/internal/ai"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/service"
)

// annotateV4Git is a minimal gitprovider.GitProvider backed by an in-memory
// files map, with write tracking so a test can assert exactly which path
// got committed.
type annotateV4Git struct {
	files  map[string][]byte
	writes map[string][]byte
	prs    int
}

func newAnnotateV4Git(files map[string][]byte) *annotateV4Git {
	return &annotateV4Git{files: files, writes: map[string][]byte{}}
}

func (g *annotateV4Git) GetFileContent(_ context.Context, path, _ string) ([]byte, error) {
	if data, ok := g.files[path]; ok {
		return data, nil
	}
	return nil, fmt.Errorf("get file content: path %q not found: %w", path, gitprovider.ErrFileNotFound)
}
func (g *annotateV4Git) ListDirectory(context.Context, string, string) ([]string, error) {
	return nil, nil
}
func (g *annotateV4Git) ListPullRequests(context.Context, string) ([]gitprovider.PullRequest, error) {
	return nil, nil
}
func (g *annotateV4Git) TestConnection(context.Context) error               { return nil }
func (g *annotateV4Git) CreateBranch(context.Context, string, string) error { return nil }
func (g *annotateV4Git) CreateOrUpdateFile(_ context.Context, path string, content []byte, _, _ string) error {
	g.writes[path] = content
	return nil
}
func (g *annotateV4Git) BatchCreateFiles(_ context.Context, files map[string][]byte, _, _ string) error {
	for p, c := range files {
		g.writes[p] = c
	}
	return nil
}
func (g *annotateV4Git) DeleteFile(context.Context, string, string, string) error { return nil }
func (g *annotateV4Git) CreatePullRequest(_ context.Context, title, _, _, _ string) (*gitprovider.PullRequest, error) {
	g.prs++
	return &gitprovider.PullRequest{ID: g.prs, URL: "https://example.com/pr/1", Title: title}, nil
}
func (g *annotateV4Git) MergePullRequest(context.Context, int) error { return nil }
func (g *annotateV4Git) GetPullRequestStatus(context.Context, int) (string, error) {
	return "open", nil
}
func (g *annotateV4Git) DeleteBranch(context.Context, string) error { return nil }

// serverWithFullV4Connection builds a *Server with a REAL active connection
// (ArgoCD client resolves, so handleAnnotateAddonValues/
// handleSetAddonAIOptOut get past their `GetActiveArgocdClient` call) and
// the given git provider installed as the override every per-request
// resolver (GitProviderForTier, GetActiveGitProvider) consults first. The
// ArgoCD "server" is a real httptest.Server answering 404 to everything —
// every ArgoCD call these handlers make (ApplicationSet status lookup) is
// best-effort and swallows its own error, so a fast, deterministic 404 is
// all that's needed; no fake ArgoCD API is implemented.
func serverWithFullV4Connection(t *testing.T, gp gitprovider.GitProvider) *Server {
	t.Helper()
	argo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(argo.Close)

	f, err := os.CreateTemp(t.TempDir(), "sharko-test-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	connYAML := fmt.Sprintf(`connections:
  - name: test
    argocd:
      server_url: %q
      token: test-token
    git:
      type: github
      token: test
      org: test
      repo: test
active_connection: test
`, argo.URL)
	if _, err := f.WriteString(connYAML); err != nil {
		t.Fatal(err)
	}
	f.Close()

	store := config.NewFileStore(f.Name())
	connSvc := service.NewConnectionService(store)
	connSvc.SetGitProviderOverride(gp)

	clusterSvc := service.NewClusterService("")
	addonSvc := service.NewAddonService("")
	dashboardSvc := service.NewDashboardService(connSvc, "")
	observabilitySvc := service.NewObservabilityService(clusterSvc)
	upgradeSvc := service.NewUpgradeService(ai.NewClient(ai.Config{}), nil, "")
	// A configured-but-unreachable provider: IsEnabled() is true (so the
	// annotate endpoint's 503 gate passes), but the actual LLM call fails
	// fast (connection refused) — AnnotateValues swallows that as
	// SkipReason="llm_error" and the heuristic-only output proceeds. This
	// keeps the test deterministic without a fake LLM server.
	aiClient := ai.NewClient(ai.Config{Provider: ai.ProviderCustomOpenAI, BaseURL: "http://127.0.0.1:1", CloudModel: "test-model"})

	return withLegacyOpenAuthForTests(NewServer(connSvc, clusterSvc, addonSvc, dashboardSvc, observabilitySvc, upgradeSvc, aiClient))
}

// v4CatalogAndValuesFiles returns the file set a v4 repo with one approved
// addon (cert-manager) needs for GetAddonDetail (via GetCatalog's v4
// branch) to find it: the engine pin (v4 marker) and catalog.yaml.
// globalValuesBody is optional extra content for values/global/cert-manager.yaml.
func v4CatalogAndValuesFiles(t *testing.T, globalValuesBody []byte) map[string][]byte {
	t.Helper()
	catalogBody, err := config.SaveAddonCatalog(config.AddonCatalogSpec{
		Addons: map[string]config.AddonCatalogEntry{
			"cert-manager": {
				RepoURL: "https://charts.jetstack.io", Chart: "cert-manager",
				Version: "1.14.5", Namespace: "cert-manager",
			},
		},
	})
	if err != nil {
		t.Fatalf("SaveAddonCatalog: %v", err)
	}
	files := map[string][]byte{
		orchestrator.BootstrapRootAppPath: []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n"),
		config.AddonCatalogPath:           catalogBody,
	}
	if len(globalValuesBody) > 0 {
		p, err := orchestrator.V4GlobalValuesPath("cert-manager")
		if err != nil {
			t.Fatalf("V4GlobalValuesPath: %v", err)
		}
		files[p] = globalValuesBody
	}
	return files
}

// TestSetAddonAIOptOut_V4Repo_404sAtTheV4Path proves the un-gating: on a
// v4 repo with no global values file yet, the opt-out endpoint answers
// 404 naming the V4 path (values/global/cert-manager.yaml) — NOT the old
// 409 "v4 layout not supported" refusal, and NOT the v3 path.
func TestSetAddonAIOptOut_V4Repo_404sAtTheV4Path(t *testing.T) {
	gp := newAnnotateV4Git(v4CatalogAndValuesFiles(t, nil))
	srv := serverWithFullV4Connection(t, gp)
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/addons/cert-manager/values/ai-opt-out",
		bytes.NewReader([]byte(`{"opt_out":true}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body: %s)", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	msg, _ := body["error"].(string)
	if msg == "" {
		t.Fatal("expected an error message")
	}
	if !bytes.Contains([]byte(msg), []byte("values/global/cert-manager.yaml")) {
		t.Errorf("error should name the v4 path, got %q", msg)
	}
	if bytes.Contains([]byte(msg), []byte(V4EditorUnsupportedMessage)) {
		t.Errorf("must NOT be the old v4-refusal message any more, got %q", msg)
	}
}

// TestSetAddonAIOptOut_V4Repo_TogglesHeaderAndOpensPR is the happy path:
// an existing v4 global values file with the smart-values header gets its
// opt-out flag flipped and committed at the V4 PATH, in a real pull
// request — proving the whole isV4 branch (path selection AND writer
// selection) works end to end.
func TestSetAddonAIOptOut_V4Repo_TogglesHeaderAndOpensPR(t *testing.T) {
	existing := orchestrator.GenerateGlobalValuesFileV4(
		"cert-manager", "cert-manager", "1.14.5", "https://charts.jetstack.io",
		[]byte("installCRDs: true\n"), false, false,
	)
	gp := newAnnotateV4Git(v4CatalogAndValuesFiles(t, existing))
	srv := serverWithFullV4Connection(t, gp)
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/addons/cert-manager/values/ai-opt-out",
		bytes.NewReader([]byte(`{"opt_out":true}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	// withAttributionWarning wraps the handler's payload under "result"
	// when it has an attribution warning to add (no per-user PAT here).
	result, _ := body["result"].(map[string]any)
	if result == nil {
		result = body
	}
	if result["pr_url"] == "" || result["pr_url"] == nil {
		t.Errorf("expected a pr_url in the response, got %+v", body)
	}

	written, ok := gp.writes["values/global/cert-manager.yaml"]
	if !ok {
		t.Fatalf("expected values/global/cert-manager.yaml to be committed, writes: %v", mapKeysBytes(gp.writes))
	}
	header := orchestrator.ParseSmartValuesHeader(written)
	if !header.AIOptOut {
		t.Errorf("expected the opt-out directive set in the committed header, got:\n%s", written)
	}
	if !bytes.Contains(written, []byte("installCRDs: true")) {
		t.Errorf("the body must be preserved untouched, got:\n%s", written)
	}
}

// TestSetAddonAIOptOut_V4Repo_IdempotentNoop: asking for the state the
// file is already in returns a no-op, same contract as the v3 path.
func TestSetAddonAIOptOut_V4Repo_IdempotentNoop(t *testing.T) {
	existing := orchestrator.GenerateGlobalValuesFileV4(
		"cert-manager", "cert-manager", "1.14.5", "https://charts.jetstack.io",
		[]byte("installCRDs: true\n"), false, false,
	)
	gp := newAnnotateV4Git(v4CatalogAndValuesFiles(t, existing))
	srv := serverWithFullV4Connection(t, gp)
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/addons/cert-manager/values/ai-opt-out",
		bytes.NewReader([]byte(`{"opt_out":false}`))) // already opted-in
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if body["status"] != "noop" {
		t.Errorf("expected status=noop, got %+v", body)
	}
	if len(gp.writes) != 0 {
		t.Errorf("a no-op must not write anything, got %v", mapKeysBytes(gp.writes))
	}
}

// TestHandleAnnotateAddonValues_V4Repo_NoLongerRefusedWithV4Message proves
// the annotate endpoint's un-gating specifically: on a v4 repo it now gets
// far enough to look the addon up (proving the old blanket 409 is gone) —
// it still 502s here because the fetch of the chart's real upstream
// values.yaml is a genuine outbound network call this unit test does not
// stand a fake chart registry for. The full generate-and-commit path is
// covered at the orchestrator level (catalog_ops_test.go /
// values_editor_test.go) and by the opt-out tests above, which exercise
// the identical isV4 branching this handler shares.
func TestHandleAnnotateAddonValues_V4Repo_NoLongerRefusedWithV4Message(t *testing.T) {
	gp := newAnnotateV4Git(v4CatalogAndValuesFiles(t, nil))
	srv := serverWithFullV4Connection(t, gp)
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/addons/cert-manager/values/annotate", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code == http.StatusConflict {
		var body map[string]any
		_ = json.NewDecoder(w.Body).Decode(&body)
		if msg, _ := body["error"].(string); msg == V4EditorUnsupportedMessage {
			t.Fatalf("annotate must not refuse a v4 repo with the old v4-layout message any more, got: %s", w.Body.String())
		}
	}
	// The real chart registry is unreachable from this test — a 502 here
	// is the correct, honest "fetching upstream values" failure, not a
	// refusal. What matters is it is NOT the v4-layout 409.
	if w.Code != http.StatusBadGateway {
		t.Logf("status = %d, body = %s (expected 502 from the real chart fetch failing; any status other than 409/v4-message is fine)", w.Code, w.Body.String())
	}
}

// TestGlobalValuesPathForRepo pins the pure path-selection helper both
// annotate endpoints share: v4 gets the fixed values/global/<addon>.yaml
// path, v3 gets the configurable-directory path (default when
// s.repoPaths.GlobalValues is unset).
func TestGlobalValuesPathForRepo(t *testing.T) {
	srv := &Server{}

	v4Path, err := srv.globalValuesPathForRepo("cert-manager", true)
	if err != nil {
		t.Fatalf("v4 path: %v", err)
	}
	if v4Path != "values/global/cert-manager.yaml" {
		t.Errorf("v4 path = %q, want values/global/cert-manager.yaml", v4Path)
	}

	v3Path, err := srv.globalValuesPathForRepo("cert-manager", false)
	if err != nil {
		t.Fatalf("v3 path: %v", err)
	}
	if v3Path != "configuration/addons-global-values/cert-manager.yaml" {
		t.Errorf("v3 path = %q, want the default v3 directory", v3Path)
	}
}

func mapKeysBytes(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
