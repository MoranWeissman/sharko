package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// creds-reframe-1 — handler-level edge validation for the explicit
// creds_source axis on POST /api/v1/clusters.
//
// The orchestrator-level routing + derivation + per-source validation tests
// live in internal/orchestrator/creds_source_test.go. These tests pin the
// handler edge: that creds_source drives the inline-vs-backend decision (not
// Provider alone), that an unknown value is a 400, and that backward-compat
// requests (no creds_source) are unchanged.
//
// newIsolatedTestServer wires a nil credProvider, so a BACKEND source surfaces
// the 503 missing-provider hint while an INLINE source must NOT (it does not
// need a backend) — that status contrast is the assertion.

func postCluster(t *testing.T, router http.Handler, body map[string]interface{}) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// Explicit creds_source=inline-kubeconfig with NO provider must reach past the
// credProvider==nil guard (the inline path needs no backend). It must not 503.
func TestRegisterCluster_CredsSourceInline_NotGatedByProvider(t *testing.T) {
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	w := postCluster(t, router, map[string]interface{}{
		"name":         "kind-test",
		"creds_source": "inline-kubeconfig", // provider intentionally omitted
		"kubeconfig":   "apiVersion: v1\nkind: Config\n",
	})

	if w.Code == http.StatusServiceUnavailable {
		t.Fatalf("status = 503: an inline-kubeconfig creds_source must NOT be gated by the credProvider==nil check")
	}
}

// Explicit creds_source=secret-kubeconfig with NO provider must hit the
// backend path but, with credentials optional at registration
// (V2-cleanup-88.3 — lazy credentials), must NOT surface the old 503
// missing-provider rejection — it degrades to a connection-only
// registration instead.
func TestRegisterCluster_CredsSourceSecretBackend_ProviderOptional(t *testing.T) {
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	w := postCluster(t, router, map[string]interface{}{
		"name":         "prod-eu",
		"creds_source": "secret-kubeconfig",
	})

	if w.Code == http.StatusServiceUnavailable {
		t.Fatalf("status = 503: a backend creds_source with no provider configured must degrade to connection-only registration, not 503 (V2-cleanup-88.3)")
	}
}

// Explicit creds_source=eks-token also routes to the backend path — same
// relaxation, no 503 with a nil credProvider.
func TestRegisterCluster_CredsSourceEKSToken_ProviderOptional(t *testing.T) {
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	w := postCluster(t, router, map[string]interface{}{
		"name":         "prod-eu",
		"creds_source": "eks-token",
	})

	if w.Code == http.StatusServiceUnavailable {
		t.Fatalf("status = 503: eks-token routes through the backend path but must not require a provider (V2-cleanup-88.3)")
	}
}

// An unknown creds_source value is a 400 at the handler edge.
func TestRegisterCluster_UnknownCredsSource_Returns400(t *testing.T) {
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	w := postCluster(t, router, map[string]interface{}{
		"name":         "prod-eu",
		"creds_source": "vault",
	})

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for unknown creds_source", w.Code)
	}
	msg := decodeError(t, w)
	if !strings.Contains(msg, "creds_source") {
		t.Errorf("error %q should mention creds_source", msg)
	}
}

// creds_source=inline-kubeconfig with a backend-only field (secret_path) is a
// 400 — the inline path rejects backend field bleed regardless of provider.
func TestRegisterCluster_CredsSourceInline_RejectsSecretPath(t *testing.T) {
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	w := postCluster(t, router, map[string]interface{}{
		"name":         "kind-test",
		"creds_source": "inline-kubeconfig",
		"kubeconfig":   "apiVersion: v1\nkind: Config\n",
		"secret_path":  "k8s-something",
	})

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (secret_path not valid for inline source)", w.Code)
	}
	msg := decodeError(t, w)
	if !strings.Contains(msg, "secret_path") {
		t.Errorf("error %q should mention secret_path", msg)
	}
}

// Backward-compat: with no creds_source, an empty-provider request still
// hits the backend path but no longer surfaces 503 — credentials are
// optional at registration for every connection mode (V2-cleanup-88.3 —
// lazy credentials).
func TestRegisterCluster_BackwardCompat_NoCredsSource_ProviderOptional(t *testing.T) {
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	w := postCluster(t, router, map[string]interface{}{
		"name": "prod-eu",
	})

	if w.Code == http.StatusServiceUnavailable {
		t.Fatalf("status = 503: the legacy empty-provider backend path must now degrade to connection-only registration (V2-cleanup-88.3)")
	}
}
