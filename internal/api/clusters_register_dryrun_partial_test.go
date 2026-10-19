package api

// clusters_register_dryrun_partial_test.go — LO-7 R5 (epic video-leftovers,
// 2026-10-18).
//
// A register dry run whose Git read fails (not "file not found") used to
// answer 502 with a Go error, while the real registration answers 207 with a
// "partial" result and failed step read_cluster_registry. The dry run now
// answers the same way.

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/providers"
)

func TestRegisterCluster_DryRunGitReadErrorAnswers207Partial(t *testing.T) {
	fake := &recordingCredProvider{kc: &providers.Kubeconfig{Server: "https://fetched.example.com"}}
	srv := newRegisterBackendTestServer(t, fake)
	srv.connSvc.SetGitProviderOverride(&handlerFakeGitProvider{getErr: errors.New("the git host did not answer")})
	router := NewRouter(srv, nil)

	body, _ := json.Marshal(map[string]interface{}{
		"name":         "prod-eu",
		"creds_source": "secret-kubeconfig",
		"dry_run":      true,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusMultiStatus {
		t.Fatalf("status = %d, want 207 (body=%s)", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["status"] != "partial" || resp["failed_step"] != "read_cluster_registry" {
		t.Errorf("status %v failed_step %v, want partial / read_cluster_registry", resp["status"], resp["failed_step"])
	}
	const want = "Sharko could not read the cluster registry from Git, so it could not show what registering this cluster would change. Nothing was changed. Try again once Git is reachable."
	if resp["message"] != want {
		t.Errorf("message:\n got: %v\nwant: %q", resp["message"], want)
	}
	if _, ok := resp["dry_run"]; ok {
		t.Errorf("a preview came back for a registry Sharko could not read: %v", resp["dry_run"])
	}
	if e, _ := resp["error"].(string); !strings.HasSuffix(e, ": the git host did not answer") {
		t.Errorf("error = %q, want it to end with the Git error", e)
	}
}
