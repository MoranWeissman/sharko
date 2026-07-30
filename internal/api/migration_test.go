package api

// migration_test.go — HTTP contract for the v3 -> v4 migration endpoints,
// and the half of Story 5.1 that only shows up at this layer: writes are
// blocked on a v3 repo while READS keep working.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// migrationV3Files is the smallest thing that counts as a v3 repo: the
// cluster registry (which is also V3SecondaryMarkerPath) plus a catalog.
func migrationV3Files() map[string][]byte {
	return map[string][]byte{
		"configuration/managed-clusters.yaml": []byte(
			"clusters:\n  - name: prod-eu\n    region: eu-central-1\n    labels:\n      cert-manager: enabled\n"),
		"configuration/addons-catalog.yaml": []byte(
			"applicationsets:\n  - name: cert-manager\n    repoURL: https://charts.jetstack.io\n    chart: cert-manager\n    version: 1.14.5\n"),
	}
}

// newMigrationTestServer wires a server with a real active connection (the
// migrate handler resolves its Git provider through the tiered-attribution
// path, which needs one) and an ArgoCD stub (the cluster/addon READ views
// need one — and proving those reads still work is half of Story 5.1).
func newMigrationTestServer(t *testing.T, files map[string][]byte) *Server {
	t.Helper()
	srv := newIsolatedTestServer(t)
	argoStub := startArgocdStub(t, nil)
	seedActiveConnectionWithArgo(t, srv, argoStub.URL)
	srv.connSvc.SetGitProviderOverride(&handlerFakeGitProvider{files: files})
	return srv
}

func getJSON(t *testing.T, srv *Server, method, path, role string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if role != "" {
		withRole(req, role)
	}
	w := httptest.NewRecorder()
	NewRouter(srv, nil).ServeHTTP(w, req)
	return w
}

// ─── status ──────────────────────────────────────────────────────────────

func TestMigrationStatus_V3Repo_ReportsAvailable(t *testing.T) {
	srv := newMigrationTestServer(t, migrationV3Files())

	w := getJSON(t, srv, http.MethodGet, "/api/v1/migration/status", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}

	var got orchestrator.MigrationStatusResult
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v; body=%s", err, w.Body.String())
	}
	if got.Format != orchestrator.RepoFormatV3 {
		t.Errorf("format = %q, want %q", got.Format, orchestrator.RepoFormatV3)
	}
	if !got.MigrationAvailable {
		t.Error("migration_available = false, want true on a v3 repo")
	}
}

func TestMigrationStatus_V4Repo_ReportsNothingToDo(t *testing.T) {
	srv := newMigrationTestServer(t, map[string][]byte{
		orchestrator.BootstrapRootAppPath: []byte("kind: Application\n"),
	})

	w := getJSON(t, srv, http.MethodGet, "/api/v1/migration/status", "")

	var got orchestrator.MigrationStatusResult
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v; body=%s", err, w.Body.String())
	}
	if got.Format != orchestrator.RepoFormatV4 {
		t.Errorf("format = %q, want %q", got.Format, orchestrator.RepoFormatV4)
	}
	if got.MigrationAvailable {
		t.Error("migration_available = true on a v4 repo; want false")
	}
}

// TestMigrationStatus_ViewerAllowed — seeing WHETHER a migration is
// available is a read; running one is not.
func TestMigrationStatus_ViewerAllowed(t *testing.T) {
	srv := newMigrationTestServer(t, migrationV3Files())

	if w := getJSON(t, srv, http.MethodGet, "/api/v1/migration/status", "viewer"); w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a viewer (body=%s)", w.Code, w.Body.String())
	}
}

// ─── preview + migrate are admin ─────────────────────────────────────────

func TestMigrationPreview_RequiresAdmin(t *testing.T) {
	srv := newMigrationTestServer(t, migrationV3Files())

	for _, role := range []string{"viewer", "operator"} {
		w := getJSON(t, srv, http.MethodPost, "/api/v1/migration/preview", role)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403 (body=%s)", role, w.Code, w.Body.String())
		}
	}

	w := getJSON(t, srv, http.MethodPost, "/api/v1/migration/preview", "admin")
	if w.Code != http.StatusOK {
		t.Fatalf("admin: status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var plan orchestrator.MigrationPlan
	if err := json.Unmarshal(w.Body.Bytes(), &plan); err != nil {
		t.Fatalf("decode: %v; body=%s", err, w.Body.String())
	}
	if len(plan.Convert) == 0 || len(plan.Remove) == 0 {
		t.Errorf("the plan should list conversions and removals; got %d and %d",
			len(plan.Convert), len(plan.Remove))
	}
}

func TestMigrate_RequiresAdmin(t *testing.T) {
	srv := newMigrationTestServer(t, migrationV3Files())

	for _, role := range []string{"viewer", "operator"} {
		w := getJSON(t, srv, http.MethodPost, "/api/v1/migration/migrate", role)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403 (body=%s)", role, w.Code, w.Body.String())
		}
	}
}

// TestMigrationStatus_ReportsOpenMigrationPR — GET /migration/status must
// surface a previous attempt's still-open PR as migration_pr_url /
// migration_pr_number, so the UI banner can render "migration PR open"
// from server truth on every load instead of trusting its own component
// state (which a remount wipes).
func TestMigrationStatus_ReportsOpenMigrationPR(t *testing.T) {
	srv := newMigrationTestServer(t, migrationV3Files())
	prefix := srv.gitopsConfig().BranchPrefix
	fakeGit := &handlerFakeGitProvider{
		files: migrationV3Files(),
		prs: []gitprovider.PullRequest{
			{ID: 9, URL: "https://example.com/pull/9", SourceBranch: prefix + "migrate-to-v4-cafebabe", TargetBranch: "main"},
		},
	}
	srv.connSvc.SetGitProviderOverride(fakeGit)

	w := getJSON(t, srv, http.MethodGet, "/api/v1/migration/status", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}

	var got orchestrator.MigrationStatusResult
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v; body=%s", err, w.Body.String())
	}
	if got.MigrationPRURL != "https://example.com/pull/9" {
		t.Errorf("MigrationPRURL = %q, want the open PR's URL", got.MigrationPRURL)
	}
	if got.MigrationPRNumber != 9 {
		t.Errorf("MigrationPRNumber = %d, want 9", got.MigrationPRNumber)
	}
}

// TestMigrate_RefusesWithOpenMigrationPR is the pinning test for the
// migration-banner double-PR bug: POST /migration/migrate must refuse with
// a 409 (not open a second PR) when a previous attempt's PR is still
// open, and the body must carry that PR's link back to the caller.
func TestMigrate_RefusesWithOpenMigrationPR(t *testing.T) {
	srv := newMigrationTestServer(t, migrationV3Files())
	prefix := srv.gitopsConfig().BranchPrefix
	fakeGit := &handlerFakeGitProvider{
		files: migrationV3Files(),
		prs: []gitprovider.PullRequest{
			{ID: 9, URL: "https://example.com/pull/9", SourceBranch: prefix + "migrate-to-v4-cafebabe", TargetBranch: "main"},
		},
	}
	srv.connSvc.SetGitProviderOverride(fakeGit)

	body, err := json.Marshal(map[string]interface{}{"yes": true})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/migration/migrate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withRole(req, "admin")
	w := httptest.NewRecorder()
	NewRouter(srv, nil).ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", w.Code, w.Body.String())
	}

	var got map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v; body=%s", err, w.Body.String())
	}
	if got["code"] != "migration_pr_already_open" {
		t.Errorf("code = %v, want %q", got["code"], "migration_pr_already_open")
	}
	if got["migration_pr_url"] != "https://example.com/pull/9" {
		t.Errorf("migration_pr_url = %v, want the existing PR's URL", got["migration_pr_url"])
	}
	if got["migration_pr_number"] != float64(9) {
		t.Errorf("migration_pr_number = %v, want 9", got["migration_pr_number"])
	}
}

// TestMigrate_WithoutConfirmation_Refused — a call that would rewrite the
// whole repository must not do so on an empty body.
func TestMigrate_WithoutConfirmation_Refused(t *testing.T) {
	srv := newMigrationTestServer(t, migrationV3Files())

	w := getJSON(t, srv, http.MethodPost, "/api/v1/migration/migrate", "admin")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", w.Code, w.Body.String())
	}
	if msg := decodeError(t, w); msg == "" {
		t.Error("the refusal should say what is missing")
	}
}

// ─── the point of Story 5.1: writes blocked, reads fine ──────────────────

// TestV3Repo_WritesBlocked_ReadsKeepWorking is the acceptance criterion in
// one test. Every one of these write doors answers the SAME plain sentence,
// and the read views are completely unaffected.
func TestV3Repo_WritesBlocked_ReadsKeepWorking(t *testing.T) {
	srv := newMigrationTestServer(t, migrationV3Files())
	router := NewRouter(srv, nil)

	writes := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/clusters"},
		{http.MethodPost, "/api/v1/clusters/batch"},
		{http.MethodDelete, "/api/v1/clusters/prod-eu"},
		{http.MethodPatch, "/api/v1/clusters/prod-eu"},
		{http.MethodPost, "/api/v1/clusters/adopt"},
		{http.MethodPost, "/api/v1/clusters/prod-eu/unadopt"},
		{http.MethodPost, "/api/v1/clusters/prod-eu/addons/cert-manager"},
		{http.MethodDelete, "/api/v1/clusters/prod-eu/addons/cert-manager"},
		{http.MethodPost, "/api/v1/addons"},
		{http.MethodDelete, "/api/v1/addons/cert-manager"},
		{http.MethodPatch, "/api/v1/addons/cert-manager"},
		{http.MethodPost, "/api/v1/addons/cert-manager/upgrade"},
		{http.MethodPost, "/api/v1/addons/upgrade-batch"},
	}
	for _, tc := range writes {
		req := httptest.NewRequest(tc.method, tc.path, http.NoBody)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusConflict {
			t.Errorf("%s %s: status = %d, want 409 (body=%s)", tc.method, tc.path, w.Code, w.Body.String())
			continue
		}
		if msg := decodeError(t, w); msg != orchestrator.V3MigrationRequiredMessage {
			t.Errorf("%s %s: message = %q, want the one shared sentence %q",
				tc.method, tc.path, msg, orchestrator.V3MigrationRequiredMessage)
		}
	}

	// Reads: completely unaffected.
	for _, path := range []string{
		"/api/v1/clusters",
		"/api/v1/addons/catalog",
		"/api/v1/repo/status",
		"/api/v1/migration/status",
	} {
		w := getJSON(t, srv, http.MethodGet, path, "")
		if w.Code != http.StatusOK {
			t.Errorf("GET %s: status = %d, want 200 — reads must keep working (body=%s)",
				path, w.Code, w.Body.String())
		}
	}

	// And the read actually still answers with the fleet, not an empty
	// shell — "reads keep working" means the data, not just the status code.
	w := getJSON(t, srv, http.MethodGet, "/api/v1/clusters", "")
	var resp models.ClustersResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode clusters: %v; body=%s", err, w.Body.String())
	}
	if len(resp.Clusters) != 1 || resp.Clusters[0].Name != "prod-eu" {
		t.Errorf("GET /clusters returned %+v, want the one v3 cluster", resp.Clusters)
	}
}

// TestV4Repo_WritesNotBlockedByTheMigrationGate proves the gate is scoped
// to v3: on a v4 repo it must never fire, or it would block the very
// operations the migration exists to unlock.
func TestV4Repo_WritesNotBlockedByTheMigrationGate(t *testing.T) {
	srv := newMigrationTestServer(t, map[string][]byte{
		orchestrator.BootstrapRootAppPath: []byte("kind: Application\n"),
	})
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters", http.NoBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code == http.StatusConflict {
		if msg := decodeError(t, w); msg == orchestrator.V3MigrationRequiredMessage {
			t.Error("the v3 migration gate fired on a v4 repo")
		}
	}
}
