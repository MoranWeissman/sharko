package api

// The error contract POST/GET /api/v1/catalog/addons hands back — the
// status code AND the machine-readable `code` beside the plain-English
// message. The UI branches on the code, so these are the shapes it builds
// on (review findings B-1, B-2, L).

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// wrapped is how these errors actually reach the handler: the orchestrator
// adds context around the sentinel on the way out, so the mapping has to
// work through errors.Is, not on the top-level value.
type wrapped struct{ err error }

func (e wrapped) Error() string { return "adding to the catalog failed: " + e.err.Error() }
func (e wrapped) Unwrap() error { return e.err }

func wrap(err error) error { return wrapped{err} }

// decodeErrorBody pulls the fields every coded refusal carries.
func decodeErrorBody(t *testing.T, rw *httptest.ResponseRecorder) (message, code string, problems []string) {
	t.Helper()
	var body struct {
		Error    string   `json:"error"`
		Code     string   `json:"code"`
		Problems []string `json:"problems"`
	}
	if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding the error body: %v — raw: %s", err, rw.Body.String())
	}
	return body.Error, body.Code, body.Problems
}

// TestWriteAddToCatalogError_Codes walks every error the add path can
// produce and pins the status + code pair. A 502 stays reserved for a git
// host that actually broke — everything a caller can fix is a 4xx, which
// is the fix for the wizard dead-ending on "gateway error" (review B-1).
func TestWriteAddToCatalogError_Codes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"request cannot be read", wrap(orchestrator.ErrCatalogRequestInvalid), http.StatusBadRequest, CodeInvalidRequest},
		{"unknown cluster", wrap(orchestrator.ErrV4ClusterNotFound), http.StatusNotFound, CodeClusterNotFound},
		{"repo still v3", wrap(orchestrator.ErrV4RepoUnsupported), http.StatusConflict, CodeRepoLayout},
		{"repo half converted", wrap(orchestrator.ErrMixedRepoLayout), http.StatusConflict, CodeRepoLayout},
		{"no confirmation", wrap(orchestrator.ErrCatalogConfirmationRequired), http.StatusUnprocessableEntity, CodeConfirmationRequired},
		{"blank catalog file", wrap(orchestrator.ErrCatalogFileEmpty), http.StatusUnprocessableEntity, CodeEmptyCatalogFile},
		{"not in the Marketplace", wrap(orchestrator.ErrAddonNotInMarketplace), http.StatusUnprocessableEntity, CodeNotInMarketplace},
		{"no version data", wrap(orchestrator.ErrCatalogVersionUnknown), http.StatusUnprocessableEntity, CodeVersionRequired},
		{"entry incomplete", wrap(orchestrator.ErrCatalogEntryIncomplete), http.StatusUnprocessableEntity, CodeIncompleteEntry},
		{"upstream broke", errors.New("the git host returned 500"), http.StatusBadGateway, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rw := httptest.NewRecorder()
			writeAddToCatalogError(rw, tc.err)
			if rw.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", rw.Code, tc.wantStatus, rw.Body.String())
			}
			msg, code, _ := decodeErrorBody(t, rw)
			if code != tc.wantCode {
				t.Errorf("code = %q, want %q", code, tc.wantCode)
			}
			if msg == "" {
				t.Error("every refusal must carry a message a person can read")
			}
		})
	}
}

// TestWriteAddToCatalogError_IncompleteEntryCarriesProblems: the
// half-written-entry refusal keeps its per-problem list, because that list
// is what tells the person which field to fill in.
func TestWriteAddToCatalogError_IncompleteEntryCarriesProblems(t *testing.T) {
	t.Parallel()
	rw := httptest.NewRecorder()
	writeAddToCatalogError(rw, &orchestrator.V4SemanticValidationError{
		Cluster:  "prod-eu",
		Addon:    "cert-manager",
		Problems: []string{"cert-manager has no chart repository URL"},
		Code:     orchestrator.CodeIncompleteEntry,
	})
	if rw.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rw.Code)
	}
	_, code, problems := decodeErrorBody(t, rw)
	if code != CodeIncompleteEntry {
		t.Errorf("code = %q, want %q", code, CodeIncompleteEntry)
	}
	if len(problems) != 1 {
		t.Errorf("problems = %+v, want the one problem", problems)
	}
}

// TestWriteAddToCatalogError_ValidationFailedIsADifferentCode: an entry
// that IS complete but whose cluster is missing a value is a different
// problem with a different fix, so it gets a different code. The combined
// add-and-enable flow used to tell these apart by reading the message text,
// which is exactly what review B-2 found broken.
func TestWriteAddToCatalogError_ValidationFailedIsADifferentCode(t *testing.T) {
	t.Parallel()
	rw := httptest.NewRecorder()
	writeAddToCatalogError(rw, &orchestrator.V4SemanticValidationError{
		Cluster:  "prod-eu",
		Addon:    "cert-manager",
		Problems: []string{"cert-manager needs a value for installCRDs"},
	})
	_, code, _ := decodeErrorBody(t, rw)
	if code != CodeValidationFailed {
		t.Errorf("code = %q, want %q", code, CodeValidationFailed)
	}
}

// TestWriteV4OrchestratorError_NotInCatalogCode is review B-2 proper: the
// enable endpoint's "your org has not approved this addon" refusal is the
// one the add-then-enable flow watches for, and it now says so in a field
// instead of in a sentence.
func TestWriteV4OrchestratorError_NotInCatalogCode(t *testing.T) {
	t.Parallel()
	rw := httptest.NewRecorder()
	writeV4OrchestratorError(rw, wrap(orchestrator.ErrV4AddonNotInCatalog))
	if rw.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", rw.Code, rw.Body.String())
	}
	_, code, _ := decodeErrorBody(t, rw)
	if code != CodeNotInCatalog {
		t.Errorf("code = %q, want %q", code, CodeNotInCatalog)
	}
}

// TestHandleListOrgCatalog_BlankFileSaysWhatToPutInIt: a catalog.yaml
// somebody created and left empty used to come back as a gateway error,
// which points at the git host for a problem that is in the repo.
func TestHandleListOrgCatalog_BlankFileSaysWhatToPutInIt(t *testing.T) {
	srv := serverWithOrgCatalog(t, testCatalog(t), &fakeCatalogGitProvider{catalogBody: []byte("\n  \n")})
	rw := httptest.NewRecorder()
	srv.handleListOrgCatalog(rw, httptest.NewRequest(http.MethodGet, "/api/v1/catalog/addons", nil))

	if rw.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", rw.Code, rw.Body.String())
	}
	msg, code, _ := decodeErrorBody(t, rw)
	if code != CodeEmptyCatalogFile {
		t.Errorf("code = %q, want %q", code, CodeEmptyCatalogFile)
	}
	for _, want := range []string{"apiVersion", "kind"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the message must name the %s header line, got %q", want, msg)
		}
	}
}
