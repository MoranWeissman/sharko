package api

// The error contract PATCH/DELETE /api/v1/catalog/addons/{name} hand back
// — status code AND the machine-readable `code` beside the plain-English
// message, same discipline as POST /catalog/addons (catalog_org_errors_test.go).

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// TestWriteEditCatalogEntryError_Codes walks every error EditCatalogEntry
// can produce and pins the status + code pair.
func TestWriteEditCatalogEntryError_Codes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"request cannot be read", wrap(orchestrator.ErrCatalogRequestInvalid), http.StatusBadRequest, CodeInvalidRequest},
		{"not in catalog yet", wrap(orchestrator.ErrV4AddonNotInCatalog), http.StatusNotFound, CodeNotInCatalog},
		{"repo still v3", wrap(orchestrator.ErrV3RepoUnsupported), http.StatusConflict, CodeRepoLayout},
		{"repo half converted", wrap(orchestrator.ErrMixedRepoLayout), http.StatusConflict, CodeRepoLayout},
		{"blank catalog file", wrap(orchestrator.ErrCatalogFileEmpty), http.StatusUnprocessableEntity, CodeEmptyCatalogFile},
		{"upstream broke", &fakeUpstreamError{}, http.StatusBadGateway, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rw := httptest.NewRecorder()
			writeEditCatalogEntryError(rw, tc.err)
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

// fakeUpstreamError stands in for "the git host actually broke" — a plain
// error with no sentinel wrapping, so it falls through every case to the
// 502 default.
type fakeUpstreamError struct{}

func (e *fakeUpstreamError) Error() string { return "the git host returned 500" }

// TestWriteEditCatalogEntryError_IncompleteEntryCarriesProblems: an edit
// that leaves the entry half-written keeps the per-problem list.
func TestWriteEditCatalogEntryError_IncompleteEntryCarriesProblems(t *testing.T) {
	t.Parallel()
	rw := httptest.NewRecorder()
	// MissingRequiredFieldError is returned by catalog.ValidateCatalogEntry
	// directly (not wrapped by the orchestrator) — see EditCatalogEntry.
	err := &catalog.MissingRequiredFieldError{Addon: "cert-manager", Field: "chart"}
	writeEditCatalogEntryError(rw, err)
	if rw.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", rw.Code, rw.Body.String())
	}
	_, code, problems := decodeErrorBody(t, rw)
	if code != CodeIncompleteEntry {
		t.Errorf("code = %q, want %q", code, CodeIncompleteEntry)
	}
	if len(problems) != 1 {
		t.Errorf("problems = %+v, want the one problem", problems)
	}
}

// TestWriteDeleteFromCatalogError_Codes walks every error DeleteFromCatalog
// can produce and pins the status + code pair. The confirmation gate is
// deliberately 400 (not the 422 every other catalog-write confirmation
// uses) — see writeDeleteFromCatalogError's doc comment.
func TestWriteDeleteFromCatalogError_Codes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"request cannot be read", wrap(orchestrator.ErrCatalogRequestInvalid), http.StatusBadRequest, CodeInvalidRequest},
		{"not in catalog", wrap(orchestrator.ErrV4AddonNotInCatalog), http.StatusNotFound, CodeNotInCatalog},
		{"repo still v3", wrap(orchestrator.ErrV3RepoUnsupported), http.StatusConflict, CodeRepoLayout},
		{"repo half converted", wrap(orchestrator.ErrMixedRepoLayout), http.StatusConflict, CodeRepoLayout},
		{"blank catalog file", wrap(orchestrator.ErrCatalogFileEmpty), http.StatusUnprocessableEntity, CodeEmptyCatalogFile},
		{
			"still enabled on clusters",
			&orchestrator.CatalogDeleteBlockedError{Addon: "cert-manager", Clusters: []string{"prod-eu"}},
			http.StatusConflict, CodeAddonEnabledOnClusters,
		},
		{"upstream broke", &fakeUpstreamError{}, http.StatusBadGateway, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rw := httptest.NewRecorder()
			writeDeleteFromCatalogError(rw, tc.err)
			if rw.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", rw.Code, tc.wantStatus, rw.Body.String())
			}
			if tc.wantCode == "" {
				return
			}
			_, code, _ := decodeErrorBody(t, rw)
			if code != tc.wantCode {
				t.Errorf("code = %q, want %q", code, tc.wantCode)
			}
		})
	}
}

// TestWriteDeleteFromCatalogError_BlockedNamesClusters: the 409 body must
// name every cluster the addon is still enabled on, since that is the
// whole point of the refusal — the caller needs to know where to switch
// it off.
func TestWriteDeleteFromCatalogError_BlockedNamesClusters(t *testing.T) {
	t.Parallel()
	rw := httptest.NewRecorder()
	writeDeleteFromCatalogError(rw, &orchestrator.CatalogDeleteBlockedError{
		Addon:    "cert-manager",
		Clusters: []string{"prod-eu", "staging-eu"},
	})
	if rw.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body = %s", rw.Code, rw.Body.String())
	}
	var body struct {
		Code     string   `json:"code"`
		Addon    string   `json:"addon"`
		Clusters []string `json:"clusters"`
	}
	if err := json.NewDecoder(rw.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Code != CodeAddonEnabledOnClusters {
		t.Errorf("code = %q, want %q", body.Code, CodeAddonEnabledOnClusters)
	}
	if body.Addon != "cert-manager" {
		t.Errorf("addon = %q, want cert-manager", body.Addon)
	}
	if len(body.Clusters) != 2 || body.Clusters[0] != "prod-eu" || body.Clusters[1] != "staging-eu" {
		t.Errorf("clusters = %+v, want [prod-eu staging-eu]", body.Clusters)
	}
}

// TestWriteDeleteFromCatalogError_ConfirmationCarriesImpactReport: the 400
// confirmation gate must carry an `impact` object naming every file the
// real delete would touch — the same shape DELETE /addons/{name} (the v3
// door) returns, so a client written against that one confirms this one
// the same way.
func TestWriteDeleteFromCatalogError_ConfirmationCarriesImpactReport(t *testing.T) {
	t.Parallel()
	rw := httptest.NewRecorder()
	writeDeleteFromCatalogError(rw, &orchestrator.CatalogDeleteConfirmationError{
		Addon:        "cert-manager",
		FilesRemoved: []string{"catalog.yaml", "values/global/cert-manager.yaml"},
	})
	if rw.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (mirrors the v3 delete's impact-report status); body = %s", rw.Code, rw.Body.String())
	}
	var body struct {
		Error  string `json:"error"`
		Impact struct {
			Addon        string   `json:"addon"`
			FilesRemoved []string `json:"files_removed"`
		} `json:"impact"`
	}
	if err := json.NewDecoder(rw.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error == "" {
		t.Error("expected a plain-English error message")
	}
	if body.Impact.Addon != "cert-manager" {
		t.Errorf("impact.addon = %q, want cert-manager", body.Impact.Addon)
	}
	if len(body.Impact.FilesRemoved) != 2 {
		t.Errorf("impact.files_removed = %+v, want 2 entries", body.Impact.FilesRemoved)
	}
}
