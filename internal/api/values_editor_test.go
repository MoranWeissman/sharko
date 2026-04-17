package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSetAddonValues_MalformedYAML — YAML that fails to parse should be
// rejected with a 400 before any Git side effects. This is a fast contract
// check that does NOT require a live connection or fake provider; we hit
// the endpoint with no connection configured (the validation runs first).
func TestSetAddonValues_MalformedYAML(t *testing.T) {
	srv := newTestServer()
	router := NewRouter(srv, nil)

	body := []byte(`{"values":"foo: [bar"}`) // unbalanced bracket
	req := httptest.NewRequest(http.MethodPut, "/api/v1/addons/cert-manager/values", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d. body=%s", w.Code, w.Body.String())
	}
	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if got := resp["error"]; got == "" || !bytes.Contains([]byte(got), []byte("invalid YAML")) {
		t.Errorf("expected error mentioning invalid YAML, got %q", got)
	}
}

// TestSetClusterAddonValues_MalformedYAML — same contract for the cluster
// override endpoint. Empty values is allowed (it means "remove overrides")
// so we only validate non-empty payloads.
func TestSetClusterAddonValues_MalformedYAML(t *testing.T) {
	srv := newTestServer()
	router := NewRouter(srv, nil)

	body := []byte(`{"values":"replicaCount: : 3"}`) // double colon
	req := httptest.NewRequest(http.MethodPut, "/api/v1/clusters/dev-eu/addons/cert-manager/values", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d. body=%s", w.Code, w.Body.String())
	}
}

// TestGetAddonValuesSchema_NoConnection — without an active connection the
// endpoint returns 503. This guards against accidentally reading values
// from a broken connection state.
func TestGetAddonValuesSchema_NoConnection(t *testing.T) {
	srv := newTestServer()
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/addons/cert-manager/values-schema", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 (no connection), got %d. body=%s", w.Code, w.Body.String())
	}
}

// TestGetClusterAddonValues_NoConnection — same for the per-cluster read.
func TestGetClusterAddonValues_NoConnection(t *testing.T) {
	srv := newTestServer()
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/dev/addons/cert-manager/values", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 (no connection), got %d. body=%s", w.Code, w.Body.String())
	}
}

// TestValuesEditor_TierClassification — pin the tier classification of the
// new endpoints in HandlerTier. Tier 2 because they change WHAT will be
// deployed (the design doc tier model). The general TestTierCoverage test
// only checks presence, not the right tier — this fixes the value.
func TestValuesEditor_TierClassification(t *testing.T) {
	cases := []string{"handleSetAddonValues", "handleSetClusterAddonValues"}
	for _, h := range cases {
		got, ok := HandlerTier[h]
		if !ok {
			t.Errorf("%s missing from HandlerTier", h)
			continue
		}
		if string(got) != "tier2" {
			t.Errorf("%s should be tier2 (configuration), got %s", h, got)
		}
	}
}

