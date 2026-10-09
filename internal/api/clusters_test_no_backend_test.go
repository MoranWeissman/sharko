package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestTestCluster_NoSecretsBackend_BUG035 asserts that POST /clusters/{name}/test
// returns a structured 503 body with `error_code: "no_secrets_backend"` and an
// actionable error message when no credentials provider is configured.
//
// Background (BUG-035): the previous code returned a 503 with the bare
// message "no credentials provider configured". The UI surfaced that as the
// cluster being "Unreachable" — but the cluster wasn't unreachable, the
// *test feature* was unavailable because no secrets backend was wired up.
// The UI now keys off `error_code` to render a distinct "test unavailable"
// state with a path to Settings → Connections; this test pins the contract.
func TestTestCluster_NoSecretsBackend_BUG035(t *testing.T) {
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	body, _ := json.Marshal(map[string]bool{"deep": false})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/any-cluster/test", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if resp["error_code"] != "no_secrets_backend" {
		t.Errorf("error_code = %q, want %q (BUG-035: UI keys off this field)", resp["error_code"], "no_secrets_backend")
	}
	if resp["error"] == "" {
		t.Errorf("error message should be non-empty (BUG-035: must be actionable, not bare 'no credentials provider configured')")
	}
	// The new message must surface the actionable next step (Settings → Connections)
	// — pin the user-visible phrase so a future refactor can't silently
	// regress to a cryptic message.
	if !bytes.Contains([]byte(resp["error"]), []byte("Settings")) && !bytes.Contains([]byte(resp["error"]), []byte("secrets backend")) {
		t.Errorf("error message should mention 'Settings' or 'secrets backend', got %q", resp["error"])
	}
}
