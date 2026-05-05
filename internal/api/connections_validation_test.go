package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/service"
)

// V124-3.3 / M4 — POST /api/v1/connections must distinguish user-actionable
// validation errors from genuine internal failures.
//
// Pre-fix: every error from connSvc.Create flowed through writeServerError,
// so a malformed git URL (a 100% user-input issue) returned an opaque 500
// "Internal Server Error". Operators couldn't tell whether to fix their
// input or escalate.
//
// Fix: connSvc.Create wraps validation errors with service.ErrValidation;
// the handler errors.Is for that sentinel and surfaces 400 with the
// underlying message visible. Internal failures still return 500 sanitized.

func TestHandleCreateConnection_InvalidGitURL_Returns400(t *testing.T) {
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	// A URL that ParseRepoURL rejects: dev.azure.com without the required
	// /_git/ segment. ParseRepoURL returns a clear validation message.
	body, _ := json.Marshal(models.CreateConnectionRequest{
		Name: "bad-conn",
		Git: models.GitRepoConfig{
			RepoURL: "https://dev.azure.com/org/project/repo", // missing _git/
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/connections/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (validation error must NOT return 500)", w.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	// The user-actionable validation message MUST be visible. The whole
	// point of the 400 is to tell the operator what to fix.
	if !strings.Contains(resp["error"], "invalid git URL") {
		t.Errorf("error body should mention 'invalid git URL', got: %q", resp["error"])
	}
	if !strings.Contains(resp["error"], "_git") {
		t.Errorf("error body should preserve the underlying parser hint about /_git/, got: %q", resp["error"])
	}
}

func TestHandleCreateConnection_InvalidGitURL_DoesNotReturn500(t *testing.T) {
	// Defensive companion to the test above: explicitly assert the status
	// is NOT 500 even with a different malformed URL shape. This prevents
	// a future refactor from quietly downgrading the validation branch back
	// to writeServerError.
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	body, _ := json.Marshal(models.CreateConnectionRequest{
		Name: "bad-conn-2",
		Git: models.GitRepoConfig{
			// .visualstudio.com URL missing /_git/ segment.
			RepoURL: "https://example.visualstudio.com/project/just-repo",
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/connections/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code == http.StatusInternalServerError {
		t.Errorf("status = 500, want 400 (V124-3.3 regression: validation should not classify as internal failure)")
	}
}

// TestErrValidation_IsErrorsIs locks the contract that ErrValidation is the
// shared sentinel handlers can errors.Is against. Tests in other packages
// (or future handlers) rely on this behaviour, so we pin it here.
func TestErrValidation_IsErrorsIs(t *testing.T) {
	// A wrapped error chain that includes ErrValidation must be identifiable.
	wrapped := errors.New("invalid git URL: " + service.ErrValidation.Error())
	if errors.Is(wrapped, service.ErrValidation) {
		// String wrap doesn't propagate Is — by design. We need fmt.Errorf
		// with %w. The Create handler uses %w, so this branch never fires
		// via the production path; we're documenting the contract.
		t.Log("string concatenation accidentally satisfies errors.Is — note for future code")
	}

	// Production path: fmt.Errorf("...: %w: %w", ErrValidation, underlying)
	underlying := errors.New("missing _git/ segment")
	chain := joinErr(service.ErrValidation, underlying)
	if !errors.Is(chain, service.ErrValidation) {
		t.Error("errors.Is must match ErrValidation through the production wrap pattern")
	}
	if !errors.Is(chain, underlying) {
		t.Error("errors.Is must still reach the underlying parser error through the chain")
	}
}

// joinErr mirrors the wrapping pattern used in service.ConnectionService.Create
// so we can test the errors.Is contract without depending on connSvc.Create
// being called with a specific input.
func joinErr(a, b error) error {
	return errorJoin{a: a, b: b}
}

type errorJoin struct{ a, b error }

func (e errorJoin) Error() string { return e.a.Error() + ": " + e.b.Error() }
func (e errorJoin) Unwrap() []error { return []error{e.a, e.b} }
