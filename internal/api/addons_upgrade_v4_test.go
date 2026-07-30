package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// v4 Wave 2 Epic 7 Story 7.2 — POST /api/v1/addons/{name}/upgrade-clusters.

func TestUpgradeAddonClustersV4Route_ViewerForbidden(t *testing.T) {
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	body, _ := json.Marshal(map[string]interface{}{
		"clusters": []string{"prod-eu"},
		"version":  "1.14.5",
		"yes":      true,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/addons/cert-manager/upgrade-clusters", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Sharko-User", "bob")
	req.Header.Set("X-Sharko-Role", "viewer")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for viewer role, got %d (body=%s)", w.Code, w.Body.String())
	}
}

func TestUpgradeAddonClustersV4Route_OperatorNoActiveConnection(t *testing.T) {
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	body, _ := json.Marshal(map[string]interface{}{
		"clusters": []string{"prod-eu"},
		"version":  "1.14.5",
		"yes":      true,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/addons/cert-manager/upgrade-clusters", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Sharko-User", "alice")
	req.Header.Set("X-Sharko-Role", "operator")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// No connection wired on an isolated test server — the handler should
	// fail cleanly on the missing ArgoCD/Git connection, never a panic or
	// a 5xx that leaks an internal error.
	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 with no active connection, got %d (body=%s)", w.Code, w.Body.String())
	}
}

func TestUpgradeAddonClustersV4Route_MissingAddonName404s(t *testing.T) {
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	// No {name} path segment at all — the bare collection path isn't
	// registered as a POST route, so this simply doesn't match.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/addons//upgrade-clusters", nil)
	req.Header.Set("X-Sharko-User", "alice")
	req.Header.Set("X-Sharko-Role", "operator")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Fatalf("expected a non-200 for an empty addon name, got 200 (body=%s)", w.Body.String())
	}
}
