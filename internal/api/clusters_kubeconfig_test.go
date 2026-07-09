package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// V125-1.1 — handler-level cross-field validation for the kubeconfig
// registration path.
//
// The orchestrator-level happy-path + auth-rejection tests live in
// internal/orchestrator/cluster_kubeconfig_test.go. These tests pin the
// field-exclusion contract enforced by handleRegisterCluster BEFORE any
// upstream call (so the caller gets a precise 400 instead of a 502 from a
// downstream resolver that was handed an inconsistent request).

func decodeError(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if msg, ok := body["error"].(string); ok {
		return msg
	}
	return ""
}

func TestRegisterCluster_KubeconfigProvider_RejectsAWSFields(t *testing.T) {
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	cases := []struct {
		name        string
		body        map[string]interface{}
		wantInError string
	}{
		{
			name: "secret_path forbidden with kubeconfig",
			body: map[string]interface{}{
				"name":        "kind-test",
				"provider":    "kubeconfig",
				"kubeconfig":  "apiVersion: v1\nkind: Config\n",
				"secret_path": "k8s-something",
			},
			wantInError: "secret_path",
		},
		{
			name: "region forbidden with kubeconfig",
			body: map[string]interface{}{
				"name":       "kind-test",
				"provider":   "kubeconfig",
				"kubeconfig": "apiVersion: v1\nkind: Config\n",
				"region":     "us-east-1",
			},
			wantInError: "region",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(tc.body)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (kubeconfig field validation)", w.Code)
			}
			msg := decodeError(t, w)
			if !strings.Contains(strings.ToLower(msg), strings.ToLower(tc.wantInError)) {
				t.Errorf("error %q should mention %q", msg, tc.wantInError)
			}
		})
	}
}

// TestRegisterCluster_KubeconfigProvider_MissingKubeconfig_NotRejected pins
// V2-cleanup-88.3 (lazy credentials): provider=kubeconfig with no
// kubeconfig field is no longer a 400 up front — credentials are optional
// at registration for every connection mode, so this degrades to a
// connection-only registration instead of being rejected. The isolated test
// server has no active ArgoCD/Git connection wired, so the request still
// fails downstream (502) — the point of this test is that it is NOT the
// old 400 "kubeconfig is required" rejection.
func TestRegisterCluster_KubeconfigProvider_MissingKubeconfig_NotRejected(t *testing.T) {
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	w := postCluster(t, router, map[string]interface{}{
		"name":     "kind-test",
		"provider": "kubeconfig",
	})

	if w.Code == http.StatusBadRequest {
		msg := decodeError(t, w)
		if strings.Contains(msg, "kubeconfig is required") {
			t.Fatalf("status = 400 with the old rejection message: %q — credentials must be optional (V2-cleanup-88.3)", msg)
		}
	}
	if w.Code == http.StatusServiceUnavailable {
		t.Fatalf("status = 503: missing kubeconfig must not be gated by the credProvider==nil check either")
	}
}

func TestRegisterCluster_EKSProvider_RejectsKubeconfigField(t *testing.T) {
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	// An EXPLICIT non-kubeconfig provider still rejects a pasted kubeconfig
	// (cross-provider field bleed → 400): the caller said "backend", so a
	// kubeconfig on the request is almost certainly a bug.
	body, _ := json.Marshal(map[string]interface{}{
		"name":       "prod-eu",
		"provider":   "eks",
		"kubeconfig": "some yaml here",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (kubeconfig should not be valid for eks path)", w.Code)
	}
	msg := decodeError(t, w)
	if !strings.Contains(msg, "kubeconfig") {
		t.Errorf("error %q should mention kubeconfig", msg)
	}
}

func TestRegisterCluster_PastedKubeconfig_NoProvider_DerivesInlinePath(t *testing.T) {
	// V2-cleanup-60.4 derive-rule un-trap: a pasted kubeconfig with NOTHING
	// else said (no creds_source, no provider) is authoritative — the
	// request takes the inline path. Before this rule the same request was
	// derived onto the backend path and rejected with "kubeconfig is only
	// valid for an inline-kubeconfig registration", which trained callers
	// to drop the paste and fall into the silent backend/eks-token trap.
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	body, _ := json.Marshal(map[string]interface{}{
		"name":       "kind-local",
		"kubeconfig": "apiVersion: v1\nkind: Config\n",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// The inline path must be taken: NOT the backend-path 503 (no provider
	// configured on this bare test server) and NOT the old cross-field 400.
	if w.Code == http.StatusServiceUnavailable {
		t.Fatalf("status = 503: pasted-kubeconfig request was derived onto the backend path")
	}
	if w.Code == http.StatusBadRequest {
		msg := decodeError(t, w)
		if strings.Contains(msg, "only valid for an inline-kubeconfig registration") {
			t.Fatalf("old derive rule still active: %q", msg)
		}
	}
}

func TestRegisterCluster_KubeconfigProvider_DoesNotRequireCredProvider(t *testing.T) {
	// With provider=kubeconfig and a non-empty kubeconfig field, the handler
	// must NOT short-circuit on the credProvider==nil check (V124-4.1's 503
	// path). Instead it should attempt the full registration flow and fail
	// downstream when the test ArgoCD/Git connections aren't wired — which
	// surfaces as a 502 BadGateway, NOT a 503.
	//
	// The kubeconfig contents don't need to be valid for this assertion;
	// we're proving the handler reached past the provider-missing guard.
	// In practice the request will be rejected by the orchestrator (no
	// active ArgoCD connection) but with a different status code than the
	// 503 EKS-with-no-provider path returns — that's the contract under test.
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	body, _ := json.Marshal(map[string]interface{}{
		"name":       "kind-test",
		"provider":   "kubeconfig",
		"kubeconfig": "apiVersion: v1\nkind: Config\n",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code == http.StatusServiceUnavailable {
		t.Fatalf("status = 503: kubeconfig path must NOT be gated by the credProvider==nil check (V124-4.1 503 hint is reserved for the EKS path)")
	}
}
