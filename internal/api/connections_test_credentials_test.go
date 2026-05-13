// V124-19 / BUG-044 — POST /api/v1/connections/test-credentials must honor
// `use_saved=true` so the wizard's "leave blank to keep, or enter new value
// to replace" placeholder (V124-17 / BUG-040) actually works end-to-end.
//
// Pre-fix: a blank token in the wizard's resume-mode test request reached
// TestCredentials → buildArgocdClient and surfaced "ArgoCD token not
// configured", which kept the wizard's Next gate disabled. The user could
// see the saved connection but couldn't proceed without re-typing a
// credential they didn't want to replace.
//
// Fix contract (assertions below):
//   - use_saved=true + saved connection present → 200, test runs against
//     the stored credentials (request body credentials are ignored)
//   - use_saved=true + no matching saved connection → 400 with a
//     descriptive error naming the missing connection
//   - use_saved=true with empty name → 400 (can't look up an unnamed conn)
//   - use_saved=false / omitted → existing behavior unchanged (regression
//     guard for the V124-19 split — Create/Update paths must keep ignoring
//     the field, and the "fresh body credentials" test path still runs)
package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

// helper — seed a connection with a known-bad token so TestCredentials will
// surface a non-empty error message (we don't have a real Git/ArgoCD remote
// in unit tests; the goal is to verify the *path* the handler takes, not the
// upstream call result).
func seedTestCredentialsConnection(t *testing.T, srv *Server, name, token string) {
	t.Helper()
	req := models.CreateConnectionRequest{
		Name: name,
		Git: models.GitRepoConfig{
			Provider: models.GitProviderGitHub,
			RepoURL:  "https://github.com/example/repo",
			Owner:    "example",
			Repo:     "repo",
			Token:    token,
		},
		Argocd: models.ArgocdConfig{
			ServerURL: "https://argocd.example.com",
			Token:     token,
			Namespace: "argocd",
		},
	}
	if err := srv.connSvc.Create(req); err != nil {
		t.Fatalf("seed connection %q: %v", name, err)
	}
}

func TestHandleTestCredentials_UseSaved_HappyPath(t *testing.T) {
	// use_saved=true + a saved connection exists → 200. The handler must
	// NOT short-circuit with a "no token in body" failure; it must fetch
	// the saved record and run TestCredentials against the stored creds.
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	const connName = "saved-conn"
	seedTestCredentialsConnection(t, srv, connName, "ghp_savedtoken")

	body, _ := json.Marshal(models.CreateConnectionRequest{
		Name:     connName,
		UseSaved: true,
		// Intentionally NO token in Git/Argocd — this is the wizard's
		// "blank-keep" submission. Without the use_saved flag the handler
		// would test with no credentials and fail.
		Git: models.GitRepoConfig{
			Provider: models.GitProviderGitHub,
			RepoURL:  "https://github.com/example/repo",
		},
		Argocd: models.ArgocdConfig{
			ServerURL: "https://argocd.example.com",
			Namespace: "argocd",
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/connections/test-credentials", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (use_saved should not 4xx when conn exists). Body: %s", w.Code, w.Body.String())
	}

	var resp map[string]map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	// Both keys present — handler ran the full test path. We can't assert
	// status: "ok" because there's no real GitHub/ArgoCD endpoint, but we
	// CAN assert the error (if any) is NOT the "X token not configured"
	// shape, since the saved token was injected.
	gitMsg := resp["git"]["message"]
	argocdMsg := resp["argocd"]["message"]
	if strings.Contains(gitMsg, "GitHub token not configured") {
		t.Errorf("git error mentions missing token despite use_saved=true: %q", gitMsg)
	}
	if strings.Contains(argocdMsg, "ArgoCD token not configured") {
		t.Errorf("argocd error mentions missing token despite use_saved=true: %q", argocdMsg)
	}
}

func TestHandleTestCredentials_UseSaved_NoMatchingConnection_Returns400(t *testing.T) {
	// use_saved=true but no saved connection by that name → 400 with a
	// descriptive error. The wizard surfaces this verbatim so the user
	// sees a clear "X is not saved" message rather than a generic 500.
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	body, _ := json.Marshal(models.CreateConnectionRequest{
		Name:     "does-not-exist",
		UseSaved: true,
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/connections/test-credentials", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (use_saved with missing conn must be a user-visible 400, not a 500). Body: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if !strings.Contains(resp["error"], "does-not-exist") {
		t.Errorf("error should name the missing connection, got: %q", resp["error"])
	}
	if !strings.Contains(resp["error"], "use_saved") {
		t.Errorf("error should mention the use_saved flag for grep-ability, got: %q", resp["error"])
	}
}

func TestHandleTestCredentials_UseSaved_EmptyName_Returns400(t *testing.T) {
	// use_saved=true with no name in the body is a programming error in
	// the caller — the handler can't know which connection to look up.
	// Surface as 400 so the wizard's catch path renders a clear message
	// rather than silently testing nothing.
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	body, _ := json.Marshal(models.CreateConnectionRequest{
		// Name intentionally empty
		UseSaved: true,
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/connections/test-credentials", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (use_saved without name must be 400). Body: %s", w.Code, w.Body.String())
	}
}

func TestHandleTestCredentials_UseSavedFalse_FreshBodyPath_Unchanged(t *testing.T) {
	// Regression guard: with use_saved omitted (or false), the original
	// handler behavior must be preserved — empty token + empty name → run
	// TestCredentials with whatever the caller submitted. The pre-V124-19
	// behavior (auto-fill empty tokens from saved conn when name is set)
	// is also preserved separately and exercised by the happy-path test
	// above's seed step.
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	body, _ := json.Marshal(models.CreateConnectionRequest{
		Git: models.GitRepoConfig{
			Provider: models.GitProviderGitHub,
			RepoURL:  "https://github.com/example/repo",
			Token:    "ghp_freshtoken",
		},
		Argocd: models.ArgocdConfig{
			ServerURL: "https://argocd.example.com",
			Token:     "argocd_freshtoken",
			Namespace: "argocd",
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/connections/test-credentials", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Will likely be 200 with error messages inside (no real upstream), but
	// MUST NOT be 400/500 — that'd mean the regular fresh-body path broke.
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for the regular fresh-body path. Body: %s", w.Code, w.Body.String())
	}
}
