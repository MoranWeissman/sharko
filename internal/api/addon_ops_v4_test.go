package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// TestFriendlyBodyDecodeError_ValuesTypeMismatch proves a wrong-shaped
// `values` field (a JSON string or array instead of an object) gets the
// specific, actionable plain-English message instead of encoding/json's
// raw internals — Moran hit the raw Go error live via the enable-addon
// dialog (Wave 2 ride-along w2-q6 item 2).
func TestFriendlyBodyDecodeError_ValuesTypeMismatch(t *testing.T) {
	var req orchestrator.EnableAddonV4Request
	err := json.Unmarshal([]byte(`{"values":"installCRDs: true"}`), &req)
	if err == nil {
		t.Fatal("expected a decode error for a string values field")
	}

	got := friendlyBodyDecodeError(err)
	want := `values must be a set of key: value pairs, e.g. {"installCRDs": true}`
	if got != want {
		t.Errorf("friendlyBodyDecodeError = %q, want %q", got, want)
	}
	if got == err.Error() {
		t.Error("friendlyBodyDecodeError must never echo the raw encoding/json error text")
	}
}

// TestFriendlyBodyDecodeError_ValuesArrayMismatch covers the array-instead-
// of-object shape explicitly called out in the ride-along item.
func TestFriendlyBodyDecodeError_ValuesArrayMismatch(t *testing.T) {
	var req orchestrator.EnableAddonV4Request
	err := json.Unmarshal([]byte(`{"values":["installCRDs", true]}`), &req)
	if err == nil {
		t.Fatal("expected a decode error for an array values field")
	}
	got := friendlyBodyDecodeError(err)
	if got != `values must be a set of key: value pairs, e.g. {"installCRDs": true}` {
		t.Errorf("friendlyBodyDecodeError = %q, want the values-specific message", got)
	}
}

// TestFriendlyBodyDecodeError_MalformedJSON proves plain syntax errors
// (unrelated to the values field) get a generic, still-plain-English
// message rather than the raw Go error text.
func TestFriendlyBodyDecodeError_MalformedJSON(t *testing.T) {
	var req orchestrator.EnableAddonV4Request
	err := json.Unmarshal([]byte(`{not json`), &req)
	if err == nil {
		t.Fatal("expected a syntax error")
	}
	got := friendlyBodyDecodeError(err)
	if got != "request body is not valid JSON" {
		t.Errorf("friendlyBodyDecodeError = %q, want the generic message", got)
	}
	if got == err.Error() {
		t.Error("friendlyBodyDecodeError must never echo the raw encoding/json error text")
	}
}

// TestWriteV4OrchestratorError proves the honest-status-code mapping (Wave
// 2 ride-along w2-q6 item 2): confirmation-required -> 400, unknown cluster
// -> 404, unknown addon -> 422, anything else -> 502 (never a blanket 502).
func TestWriteV4OrchestratorError(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{
			name:       "confirmation required",
			err:        errors.New("confirmation required: set yes: true in request body"),
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "unknown cluster",
			err:        fmt.Errorf("%w: %q", orchestrator.ErrV4ClusterNotFound, "no-such-cluster"),
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "unknown addon",
			err:        fmt.Errorf("%w: addon %q is not in the catalog", orchestrator.ErrV4AddonNotInCatalog, "does-not-exist"),
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name:       "real upstream failure",
			err:        errors.New("git provider: 503 service unavailable"),
			wantStatus: http.StatusBadGateway,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			writeV4OrchestratorError(w, tc.err)
			if w.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d (body: %s)", w.Code, tc.wantStatus, w.Body.String())
			}
		})
	}
}

// TestWriteV4OrchestratorError_UnknownAddonWording proves the 422 response
// body still carries the plain "not in the catalog" wording verbatim (not
// re-summarized), since that is the exact phrase the ride-along item
// requires.
func TestWriteV4OrchestratorError_UnknownAddonWording(t *testing.T) {
	w := httptest.NewRecorder()
	err := fmt.Errorf("%w: addon %q is not in the catalog — check the spelling", orchestrator.ErrV4AddonNotInCatalog, "does-not-exist")
	writeV4OrchestratorError(w, err)

	var body map[string]interface{}
	if decodeErr := json.NewDecoder(w.Body).Decode(&body); decodeErr != nil {
		t.Fatalf("response is not JSON: %v", decodeErr)
	}
	msg, _ := body["error"].(string)
	if msg == "" || !strings.Contains(msg, "not in the catalog") {
		t.Errorf("error = %q, want it to mention %q", msg, "not in the catalog")
	}
}
