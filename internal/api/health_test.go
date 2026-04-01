package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/moran/argocd-addons-platform/internal/ai"
	"github.com/moran/argocd-addons-platform/internal/config"
	"github.com/moran/argocd-addons-platform/internal/datadog"
	"github.com/moran/argocd-addons-platform/internal/service"
)

func newTestServer() *Server {
	store := config.NewFileStore("/tmp/aap-test-config.yaml")
	connSvc := service.NewConnectionService(store)
	clusterSvc := service.NewClusterService()
	addonSvc := service.NewAddonService()
	dashboardSvc := service.NewDashboardService(connSvc)

	observabilitySvc := service.NewObservabilityService()
	upgradeSvc := service.NewUpgradeService(ai.NewClient(ai.Config{}))

	aiClient := ai.NewClient(ai.Config{})
	ddClient := datadog.NewClient(datadog.Config{})
	return NewServer(connSvc, clusterSvc, addonSvc, dashboardSvc, observabilitySvc, upgradeSvc, aiClient, ddClient, nil)
}

func TestHealthEndpoint(t *testing.T) {
	srv := newTestServer()
	router := NewRouter(srv, nil)

	req := httptest.NewRequest("GET", "/api/v1/health", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	if body["status"] != "healthy" {
		t.Errorf("expected status=healthy, got %s", body["status"])
	}
}

func TestCORSHeaders(t *testing.T) {
	srv := newTestServer()
	router := NewRouter(srv, nil)

	req := httptest.NewRequest("OPTIONS", "/api/v1/health", nil)
	req.Header.Set("Origin", "http://localhost")
	req.Host = "localhost"
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	// CORS Allow-Methods should always be present
	if w.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Error("missing CORS Allow-Methods header")
	}
	// Security headers should be present
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing X-Content-Type-Options security header")
	}
}

func TestConnectionsListEmpty(t *testing.T) {
	srv := newTestServer()
	router := NewRouter(srv, nil)

	req := httptest.NewRequest("GET", "/api/v1/connections/", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}
