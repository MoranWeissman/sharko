package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/orchestrator"
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

// TestRegisterCluster_HTTP_NoSettingsStore_RefusedWithNoEnablePath pins
// ruling (e) of the 2026-08-19 corrective round, which closed Story 1's
// reported deviation 6 by ACCEPTING it as designed:
//
//	"No settings store — ACCEPTED as designed. With no settings store,
//	 inline registration stays disabled with no enable path. That is the
//	 correct fail-closed behavior. Keep the limitation documented and test
//	 the explicit refusal."
//
// The whole loop is pinned here, because half of it would be a trap: a
// refusal that pointed at a Settings switch which cannot be flipped is
// worse than one that says nothing. So:
//
//  1. the registration is refused, 403, with the explicit refusal;
//  2. reading the setting answers false rather than erroring;
//  3. writing it answers 503 — there genuinely is no enable path.
//
// The refusal SENTENCE is taken from the type itself rather than retyped:
// the exact wording is already pinned by exact-literal equality in
// wantInlineCredentialsDisabledMsg above and in the orchestrator's own
// policy test. What this test owns is the no-store PATH.
func TestRegisterCluster_HTTP_NoSettingsStore_RefusedWithNoEnablePath(t *testing.T) {
	wantRefusal := (&orchestrator.InlineCredentialsDisabledError{}).Error()

	srv := newIsolatedTestServer(t)
	argoStub := startArgocdStub(t, nil)
	seedActiveConnectionWithArgo(t, srv, argoStub.URL)
	// NO settings store at all — bare local dev, out of cluster, nowhere to
	// persist an opt-in.
	srv.SetSettingsStore(nil)
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
		t.Fatalf("expected 403 with NO settings store wired, got %d (body=%s)", w.Code, w.Body.String())
	}
	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["error"] != wantRefusal {
		t.Errorf("error message = %q, want the explicit refusal %q", resp["error"], wantRefusal)
	}

	// Reading the setting: false, not an error. An operator asking "is this
	// on?" gets the honest answer.
	readReq := httptest.NewRequest(http.MethodGet, "/api/v1/settings/allow-inline-credentials", nil)
	readReq.Header.Set("X-Sharko-User", "admin")
	readReq.Header.Set("X-Sharko-Role", "admin")
	readW := httptest.NewRecorder()
	router.ServeHTTP(readW, readReq)
	if readW.Code != http.StatusOK {
		t.Fatalf("GET the setting with no store: expected 200, got %d (body=%s)", readW.Code, readW.Body.String())
	}
	var got allowInlineCredentialsResponse
	if err := json.NewDecoder(readW.Body).Decode(&got); err != nil {
		t.Fatalf("decode setting: %v", err)
	}
	if got.AllowInlineCredentials {
		t.Error("the setting reads true with no store to have persisted it — that would be failing OPEN")
	}

	// Writing it: 503. There is no enable path, and the server says so
	// plainly instead of pretending the write landed.
	writeBody, _ := json.Marshal(allowInlineCredentialsResponse{AllowInlineCredentials: true})
	writeReq := httptest.NewRequest(http.MethodPut, "/api/v1/settings/allow-inline-credentials", bytes.NewReader(writeBody))
	writeReq.Header.Set("Content-Type", "application/json")
	writeReq.Header.Set("X-Sharko-User", "admin")
	writeReq.Header.Set("X-Sharko-Role", "admin")
	writeW := httptest.NewRecorder()
	router.ServeHTTP(writeW, writeReq)
	if writeW.Code != http.StatusServiceUnavailable {
		t.Fatalf("PUT the setting with no store: expected 503 (no enable path), got %d (body=%s)", writeW.Code, writeW.Body.String())
	}

	// And the refusal still stands after the failed opt-in attempt.
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/clusters", bytes.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	if w2.Code != http.StatusForbidden {
		t.Fatalf("after a failed opt-in the registration should still be refused, got %d", w2.Code)
	}
}
