package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/settings"
)

// V2-cleanup-90.3 / review finding M7a — HTTP-layer pin for the
// allow_inline_credentials kill switch (V2-cleanup-89.6). Before this file,
// the gate itself was only tested at the orchestrator level
// (inline_creds_policy_test.go) — never through the real HTTP handler, so a
// wiring regression (e.g. the handler forgetting to call
// SetAllowInlineCredentialsFn) would not have been caught.
//
// Exact expected error string (must match orchestrator.InlineCredentialsDisabledError.Error()).
// The refusal names the supported credential providers explicitly (product
// correction 5) — pinned by exact equality so a paraphrase fails here.
const wantInlineCredentialsDisabledMsg = "This server does not accept pasted credentials. A pasted credential would exist only inside the live connection and could never be restored from Git. Store the cluster's kubeconfig in a supported credentials provider instead — a Kubernetes Secret or AWS Secrets Manager — and register the cluster pointing at it. An admin can allow legacy inline credentials in Settings."

// killSwitchInlineKubeconfig is a syntactically valid kubeconfig pointing at
// a dead local port — same fixture shape as
// TestRegisterCluster_InlineKubeconfig_NeverTouchesBackendProvider. The
// kill-switch gate (Step 1a in orchestrator/cluster.go) fires before Stage1
// verification would ever dial this address, so its unreachability is
// irrelevant to this test.
const killSwitchInlineKubeconfig = `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://127.0.0.1:1
    insecure-skip-tls-verify: true
  name: inline
contexts:
- context:
    cluster: inline
    user: inline
  name: inline
current-context: inline
users:
- name: inline
  user:
    token: inline-token
`

// enableLegacyInlineForTest wires a settings store whose
// allow_inline_credentials is explicitly TRUE — the legacy escape hatch an
// admin can still open (product correction 5). Tests that exercise the
// inline registration path ITSELF (not the refusal) call this, so the
// default-off gate cannot swallow the behavior they pin: without it, a 403
// at the gate would satisfy their "not 503 / backend untouched" assertions
// while the code they exist to test never runs.
func enableLegacyInlineForTest(t *testing.T, srv *Server) {
	t.Helper()
	store := settings.NewStore(fake.NewSimpleClientset(), "sharko")
	if err := store.SetAllowInlineCredentials(t.Context(), true); err != nil {
		t.Fatalf("SetAllowInlineCredentials(true): %v", err)
	}
	srv.SetSettingsStore(store)
}

// newKillSwitchTestServer wires a Server with an active ArgoCD + Git
// connection (same shape as newRegisterBackendTestServer) plus a settings
// store whose allow_inline_credentials value is false.
func newKillSwitchTestServer(t *testing.T) *Server {
	t.Helper()
	srv := newIsolatedTestServer(t)
	argoStub := startArgocdStub(t, nil)
	seedActiveConnectionWithArgo(t, srv, argoStub.URL)

	client := fake.NewSimpleClientset()
	store := settings.NewStore(client, "sharko")
	if err := store.SetAllowInlineCredentials(t.Context(), false); err != nil {
		t.Fatalf("SetAllowInlineCredentials(false): %v", err)
	}
	srv.SetSettingsStore(store)
	return srv
}

// TestRegisterCluster_HTTP_InlineRefusedByDefault pins the flipped default
// (product correction 5): a settings store NOBODY ever touched refuses a new
// inline registration — no admin action required for the refusal, only for
// the opt-in.
func TestRegisterCluster_HTTP_InlineRefusedByDefault(t *testing.T) {
	srv := newIsolatedTestServer(t)
	argoStub := startArgocdStub(t, nil)
	seedActiveConnectionWithArgo(t, srv, argoStub.URL)
	// An untouched settings store — the DEFAULT is what refuses.
	srv.SetSettingsStore(settings.NewStore(fake.NewSimpleClientset(), "sharko"))
	router := NewRouter(srv, nil)

	body, _ := json.Marshal(map[string]interface{}{
		"name":         "kind-local",
		"creds_source": "inline-kubeconfig",
		"kubeconfig":   killSwitchInlineKubeconfig,
		"dry_run":      true,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 by DEFAULT for a pasted kubeconfig, got %d (body=%s)", w.Code, w.Body.String())
	}
	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["error"] != wantInlineCredentialsDisabledMsg {
		t.Errorf("error message = %q, want %q", resp["error"], wantInlineCredentialsDisabledMsg)
	}
}

func TestRegisterCluster_HTTP_InlineCredentialsDisabled_403PlainMessage(t *testing.T) {
	srv := newKillSwitchTestServer(t)
	router := NewRouter(srv, nil)

	body, _ := json.Marshal(map[string]interface{}{
		"name":         "kind-local",
		"creds_source": "inline-kubeconfig",
		"kubeconfig":   killSwitchInlineKubeconfig,
		"dry_run":      true,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when allow_inline_credentials is false and a kubeconfig is pasted, got %d (body=%s)", w.Code, w.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["error"] != wantInlineCredentialsDisabledMsg {
		t.Errorf("error message = %q, want %q", resp["error"], wantInlineCredentialsDisabledMsg)
	}
}

func TestRegisterClusterBatch_HTTP_InlineCredentialsDisabled_MemberRejected(t *testing.T) {
	srv := newKillSwitchTestServer(t)
	router := NewRouter(srv, nil)

	body, _ := json.Marshal(map[string]interface{}{
		"clusters": []map[string]interface{}{
			{
				"name":         "pasted-batch-member",
				"creds_source": "inline-kubeconfig",
				"kubeconfig":   killSwitchInlineKubeconfig,
				"dry_run":      true,
			},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/batch", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusMultiStatus {
		t.Fatalf("expected 207 (all members failed), got %d (body=%s)", w.Code, w.Body.String())
	}

	var resp struct {
		Failed  int `json:"failed"`
		Results []struct {
			Status  string `json:"status"`
			Cluster struct {
				Name string `json:"name"`
			} `json:"cluster"`
			Error string `json:"error"`
		} `json:"results"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Failed != 1 {
		t.Fatalf("expected failed=1, got %d", resp.Failed)
	}
	if len(resp.Results) != 1 || resp.Results[0].Status != "failed" {
		t.Fatalf("expected exactly one failed result, got %+v", resp.Results)
	}
	if resp.Results[0].Error != wantInlineCredentialsDisabledMsg {
		t.Errorf("batch member error = %q, want %q", resp.Results[0].Error, wantInlineCredentialsDisabledMsg)
	}
}
