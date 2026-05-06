package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/service"
)

// V124-4.2 / BUG-017 — POST /api/v1/connections/ and PUT /api/v1/connections/{name}
// must reject empty `{}` bodies with 400 + an actionable validation message,
// not silently persist a name="default" placeholder with no usable Git config.
//
// V124-3.3 added URL-parse validation but only fired when RepoURL was non-empty;
// an empty body skipped both the URL parse AND the required-field check, so the
// SaveConnection path persisted garbage. V124-4.2 adds the missing required-
// field gate before any other processing.

func TestHandleCreateConnection_EmptyBody_Returns400(t *testing.T) {
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/connections/", bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (V124-4.2: empty body must NOT persist garbage)", w.Code)
	}
	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if !strings.Contains(resp["error"], "git.provider") {
		t.Errorf("error body should mention git.provider, got: %q", resp["error"])
	}
	if !strings.Contains(resp["error"], "validation failed") {
		t.Errorf("error body should mention validation failed (ErrValidation sentinel), got: %q", resp["error"])
	}
}

func TestHandleCreateConnection_MissingProvider_Returns400(t *testing.T) {
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	body, _ := json.Marshal(models.CreateConnectionRequest{
		Name: "any",
		Git: models.GitRepoConfig{
			Owner: "owner", Repo: "repo", // identifier present but no Provider
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/connections/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	var resp map[string]string
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if !strings.Contains(resp["error"], "git.provider") {
		t.Errorf("error body should mention git.provider, got: %q", resp["error"])
	}
}

func TestHandleCreateConnection_MissingIdentifier_GitHub_Returns400(t *testing.T) {
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	body, _ := json.Marshal(models.CreateConnectionRequest{
		Git: models.GitRepoConfig{
			Provider: models.GitProviderGitHub,
			// no RepoURL, no Owner, no Repo
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/connections/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	var resp map[string]string
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if !strings.Contains(resp["error"], "owner") || !strings.Contains(resp["error"], "repo") {
		t.Errorf("error body should mention owner+repo, got: %q", resp["error"])
	}
}

func TestHandleCreateConnection_MissingIdentifier_AzureDevOps_Returns400(t *testing.T) {
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	body, _ := json.Marshal(models.CreateConnectionRequest{
		Git: models.GitRepoConfig{
			Provider: models.GitProviderAzureDevOps,
			// no RepoURL, no organization/project/repository
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/connections/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	var resp map[string]string
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if !strings.Contains(resp["error"], "organization") || !strings.Contains(resp["error"], "project") {
		t.Errorf("error body should mention organization+project+repository, got: %q", resp["error"])
	}
}

func TestHandleCreateConnection_UnsupportedProvider_Returns400(t *testing.T) {
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	body, _ := json.Marshal(models.CreateConnectionRequest{
		Git: models.GitRepoConfig{
			Provider: "gitlab", // not supported
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/connections/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	var resp map[string]string
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if !strings.Contains(resp["error"], "gitlab") {
		t.Errorf("error body should echo the unsupported provider value, got: %q", resp["error"])
	}
}

func TestHandleCreateConnection_ValidBody_Returns201(t *testing.T) {
	// Regression: existing happy path must still work after the new validator.
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	body, _ := json.Marshal(models.CreateConnectionRequest{
		Name: "good",
		Git: models.GitRepoConfig{
			Provider: models.GitProviderGitHub,
			Owner:    "owner",
			Repo:     "repo",
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/connections/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (regression: V124-4.2 must not break the happy path)", w.Code)
	}
}

func TestHandleUpdateConnection_EmptyBody_Returns400(t *testing.T) {
	// PUT goes through the same Create() path (handler overlays partial fields
	// from the saved connection then calls Create), so the validator must fire
	// the same way when the merged request still has no provider.
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/connections/nonexistent", bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// TestList_SkipsEmptyNameEntries_Defensive — V124-4.2 read-side guard.
//
// Pre-V124-4 the Create path persisted an empty-name "default" entry on
// `POST {}`. The maintainer's demo cluster currently has such an entry from
// the BUG-017 reproducer. The List handler must skip empty-name entries with
// a single warning rather than expose them via the API. We verify by writing
// a fixture YAML that contains an empty-name entry alongside a valid one and
// asserting List returns only the valid one.
func TestList_SkipsEmptyNameEntries_Defensive(t *testing.T) {
	// Hand-roll a fixture YAML with an empty-name entry. We can't reach the
	// store via Create() to seed this state because Create() now rejects it.
	fixture := []byte(`
connections:
  - name: ""
    git:
      provider: github
      owner: ""
      repo: ""
  - name: real-conn
    git:
      provider: github
      owner: owner
      repo: repo
`)
	f, err := os.CreateTemp("", "sharko-bug017-fixture-*.yaml")
	if err != nil {
		t.Fatalf("create temp fixture: %v", err)
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(fixture); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	f.Close()

	store := config.NewFileStore(f.Name())
	connSvc := service.NewConnectionService(store)

	resp, err := connSvc.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(resp.Connections) != 1 {
		t.Fatalf("expected 1 connection (empty-name skipped), got %d: %+v", len(resp.Connections), resp.Connections)
	}
	if resp.Connections[0].Name != "real-conn" {
		t.Errorf("expected real-conn to remain, got %q", resp.Connections[0].Name)
	}
}
