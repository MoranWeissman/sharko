package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/catalog"
)

// helper: build a test catalog from an inline YAML fixture.
func testCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	y := `
addons:
  - name: cert-manager
    description: TLS lifecycle.
    chart: cert-manager
    repo: https://charts.jetstack.io
    default_namespace: cert-manager
    maintainers: [jetstack]
    license: Apache-2.0
    category: security
    curated_by: [cncf-graduated, aws-eks-blueprints]
    security_score: 8.2
    security_score_updated: "2026-04-15"
  - name: grafana
    description: Visualisation.
    chart: grafana
    repo: https://grafana.github.io/helm-charts
    default_namespace: monitoring
    maintainers: [grafana]
    license: AGPL-3.0
    category: observability
    curated_by: [cncf-incubating, artifacthub-verified]
    security_score: unknown
`
	c, err := catalog.LoadBytes([]byte(y))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	return c
}

// serverWithCatalog returns a Server whose only dependency is a loaded
// catalog — enough to exercise the catalog handlers in isolation.
func serverWithCatalog(t *testing.T, c *catalog.Catalog) *Server {
	t.Helper()
	s := &Server{}
	s.SetCatalog(c)
	return s
}

func TestHandleListCatalogAddons_NoFilters(t *testing.T) {
	srv := serverWithCatalog(t, testCatalog(t))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/addons", nil)
	rw := httptest.NewRecorder()
	srv.handleListCatalogAddons(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rw.Code)
	}
	var body catalogListResponse
	if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Total != 2 {
		t.Fatalf("total = %d, want 2", body.Total)
	}
	// Sorted by name.
	if body.Addons[0].Name != "cert-manager" || body.Addons[1].Name != "grafana" {
		t.Errorf("unexpected order: %s, %s", body.Addons[0].Name, body.Addons[1].Name)
	}
	// cert-manager has a score, derived tier should be present.
	if body.Addons[0].SecurityTier != "Strong" {
		t.Errorf("cert-manager tier = %q, want Strong", body.Addons[0].SecurityTier)
	}
}

func TestHandleListCatalogAddons_FilterCategory(t *testing.T) {
	srv := serverWithCatalog(t, testCatalog(t))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/addons?category=security", nil)
	rw := httptest.NewRecorder()
	srv.handleListCatalogAddons(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d", rw.Code)
	}
	var body catalogListResponse
	_ = json.Unmarshal(rw.Body.Bytes(), &body)
	if body.Total != 1 || body.Addons[0].Name != "cert-manager" {
		t.Errorf("category=security should yield cert-manager only; got %+v", body)
	}
}

func TestHandleListCatalogAddons_FilterMinScore(t *testing.T) {
	srv := serverWithCatalog(t, testCatalog(t))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/addons?min_score=7.5", nil)
	rw := httptest.NewRecorder()
	srv.handleListCatalogAddons(rw, req)

	var body catalogListResponse
	_ = json.Unmarshal(rw.Body.Bytes(), &body)
	if body.Total != 1 || body.Addons[0].Name != "cert-manager" {
		t.Errorf("min_score=7.5 should yield cert-manager only; got %+v", body)
	}
}

func TestHandleListCatalogAddons_FilterCuratedBy_AllOf(t *testing.T) {
	srv := serverWithCatalog(t, testCatalog(t))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/addons?curated_by=cncf-graduated,aws-eks-blueprints", nil)
	rw := httptest.NewRecorder()
	srv.handleListCatalogAddons(rw, req)

	var body catalogListResponse
	_ = json.Unmarshal(rw.Body.Bytes(), &body)
	if body.Total != 1 || body.Addons[0].Name != "cert-manager" {
		t.Errorf("curated_by AND match should yield cert-manager only; got %+v", body)
	}
}

func TestHandleListCatalogAddons_FreeTextSearch(t *testing.T) {
	srv := serverWithCatalog(t, testCatalog(t))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/addons?q=VISUAL", nil)
	rw := httptest.NewRecorder()
	srv.handleListCatalogAddons(rw, req)

	var body catalogListResponse
	_ = json.Unmarshal(rw.Body.Bytes(), &body)
	if body.Total != 1 || body.Addons[0].Name != "grafana" {
		t.Errorf("text search should match grafana (case-insensitive description); got %+v", body)
	}
}

func TestHandleListCatalogAddons_BadMinScore(t *testing.T) {
	srv := serverWithCatalog(t, testCatalog(t))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/addons?min_score=abc", nil)
	rw := httptest.NewRecorder()
	srv.handleListCatalogAddons(rw, req)
	if rw.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on malformed min_score, got %d", rw.Code)
	}
}

func TestHandleListCatalogAddons_ServiceUnavailable(t *testing.T) {
	srv := &Server{} // no catalog set
	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/addons", nil)
	rw := httptest.NewRecorder()
	srv.handleListCatalogAddons(rw, req)
	if rw.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when catalog not loaded, got %d", rw.Code)
	}
}

func TestHandleGetCatalogAddon_Found(t *testing.T) {
	srv := serverWithCatalog(t, testCatalog(t))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/addons/cert-manager", nil)
	req.SetPathValue("name", "cert-manager")
	rw := httptest.NewRecorder()
	srv.handleGetCatalogAddon(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d", rw.Code)
	}
	var entry catalog.CatalogEntry
	if err := json.Unmarshal(rw.Body.Bytes(), &entry); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if entry.Name != "cert-manager" {
		t.Errorf("name = %q", entry.Name)
	}
	if entry.SecurityTier != "Strong" {
		t.Errorf("derived tier not set; got %q", entry.SecurityTier)
	}
	if entry.SecurityScoreUpdated != "2026-04-15" {
		t.Errorf("security_score_updated = %q", entry.SecurityScoreUpdated)
	}
}

func TestHandleGetCatalogAddon_NotFound(t *testing.T) {
	srv := serverWithCatalog(t, testCatalog(t))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/addons/ghost", nil)
	req.SetPathValue("name", "ghost")
	rw := httptest.NewRecorder()
	srv.handleGetCatalogAddon(rw, req)

	if rw.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rw.Code)
	}
	var errBody map[string]interface{}
	_ = json.Unmarshal(rw.Body.Bytes(), &errBody)
	if msg, _ := errBody["error"].(string); !strings.Contains(msg, "not found") {
		t.Errorf("error payload = %v", errBody)
	}
}

func TestHandleGetCatalogAddon_ServiceUnavailable(t *testing.T) {
	srv := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/addons/anything", nil)
	req.SetPathValue("name", "anything")
	rw := httptest.NewRecorder()
	srv.handleGetCatalogAddon(rw, req)
	if rw.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when catalog not loaded, got %d", rw.Code)
	}
}
