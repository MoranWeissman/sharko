package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// V124-4.3 / BUG-019 — POST /api/v1/addons must validate request-body required
// fields BEFORE any upstream call (ArgoCD round-trip, Git provider connection
// negotiation, Helm chart values fetch, AI annotate pass).
//
// Pre-V124-4 the handler dialled out to ArgoCD + Git first, so an empty `{}`
// POST returned a confusing 502 (`no active ArgoCD connection: …`) AND
// burned external API quota on every empty / invalid attempt. This is a
// resource-leak amplification pattern: a single misbehaving operator/script
// could drain Helm registry + Git provider quota with O(N) zero-payload
// POSTs, none of which would reach the validation gate.
//
// Fix: decode + required-field check run first; upstream resolution moves
// to AFTER the validation block.

func TestAddAddon_EmptyBody_Returns400_NoUpstreamDialled(t *testing.T) {
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	// Tripwire: count any attempt to resolve upstream connections.
	// We can't directly intercept GetActiveArgocdClient on the production
	// connection service, but the isolated test server has no active
	// connection wired, so a successful "fail-fast on bad body" path will
	// return 400 BEFORE the upstream resolver is invoked. Conversely, if
	// validation is skipped, the handler hits GetActiveArgocdClient first
	// and returns 502 / 503 with `no active ArgoCD connection` body —
	// exactly the BUG-019 symptom. We assert the status AND the body shape.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/addons", bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (V124-4.3: empty body must fail validation BEFORE upstream call)", w.Code)
	}
	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if !strings.Contains(resp["error"], "addon name is required") {
		t.Errorf("error body should be the field-specific validation message, got: %q", resp["error"])
	}
	// Negative regression guard: the OLD bug returned a 502 with the
	// upstream error string. A future refactor that re-orders these checks
	// would surface that string again.
	if strings.Contains(strings.ToLower(resp["error"]), "no active argocd") ||
		strings.Contains(strings.ToLower(resp["error"]), "no active git") {
		t.Errorf("V124-4.3 regression: empty body must NOT report upstream connection errors, got: %q", resp["error"])
	}
}

func TestAddAddon_PartialBody_Returns400_NoUpstreamDialled(t *testing.T) {
	// Name set but other required fields missing — the validator should
	// still fire before upstream resolution.
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	body, _ := json.Marshal(map[string]string{"name": "kube-prometheus-stack"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/addons", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	var resp map[string]string
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if !strings.Contains(resp["error"], "chart is required") {
		t.Errorf("error body should mention chart is required, got: %q", resp["error"])
	}
}

// TestAddAddon_ValidBody_ReachesUpstream proves the OPPOSITE direction of the
// fix: a fully-valid request DOES reach the upstream resolution path (which,
// in the isolated test server with no active connection, fails at the ArgoCD
// step with 502). This pins the contract that the fix is "validate first,
// then call upstream" — not "skip upstream entirely on a happy-path request".
func TestAddAddon_ValidBody_ReachesUpstream(t *testing.T) {
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	body, _ := json.Marshal(map[string]string{
		"name":     "kube-prometheus-stack",
		"chart":    "kube-prometheus-stack",
		"repo_url": "https://prometheus-community.github.io/helm-charts",
		"version":  "55.0.0",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/addons", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// With no active connection, the call must reach the upstream check
	// and fail there — NOT short-circuit at validation. We accept any
	// non-2xx, non-400 status as evidence that the validator passed.
	if w.Code == http.StatusBadRequest {
		t.Errorf("happy-path request was rejected at validation; status=%d body=%s", w.Code, w.Body.String())
	}
	if w.Code/100 == 2 {
		t.Errorf("happy-path request unexpectedly succeeded against an unconfigured test server; status=%d", w.Code)
	}
}

// Note on test approach: rather than instrumenting the connection service
// with a call-counting mock, we rely on the user-visible contract — the
// status code AND the absence of the upstream-error substring — which is
// exactly what the smoke runner tests against. A future V124-5 sweep can
// add a tracking-recorder mock if deeper guarantees are needed; status +
// body assertion was considered sufficient for V124-4.
