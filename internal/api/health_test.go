package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MoranWeissman/sharko/internal/ai"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/providers"
	"github.com/MoranWeissman/sharko/internal/service"
)

// healthTestStubCredProvider is a minimal ClusterCredentialsProvider
// implementation used only to flip srv.credProvider() to non-nil for the
// BUG-041 capability-flag assertion. The handler under test only checks
// `s.credProvider() != nil` — none of the interface methods are invoked.
type healthTestStubCredProvider struct{}

func (healthTestStubCredProvider) GetCredentials(string) (*providers.Kubeconfig, error) {
	return nil, nil
}
func (healthTestStubCredProvider) ListClusters() ([]providers.ClusterInfo, error) {
	return nil, nil
}
func (healthTestStubCredProvider) SearchSecrets(string) ([]string, error) { return nil, nil }
func (healthTestStubCredProvider) HealthCheck(context.Context) error      { return nil }

func newTestServer() *Server {
	store := config.NewFileStore("/tmp/sharko-test-config.yaml")
	connSvc := service.NewConnectionService(store)
	clusterSvc := service.NewClusterService("")
	addonSvc := service.NewAddonService("")
	dashboardSvc := service.NewDashboardService(connSvc, "")

	observabilitySvc := service.NewObservabilityService()
	upgradeSvc := service.NewUpgradeService(ai.NewClient(ai.Config{}), nil, "")

	aiClient := ai.NewClient(ai.Config{})
	return NewServer(connSvc, clusterSvc, addonSvc, dashboardSvc, observabilitySvc, upgradeSvc, aiClient)
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

	var body map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	if status, _ := body["status"].(string); status != "healthy" {
		t.Errorf("expected status=healthy, got %v", body["status"])
	}
}

// BUG-041: the /health response advertises `cluster_test_available` so
// the UI can disable the per-cluster Test button when there is no
// credentials provider configured on the active connection. The button
// otherwise renders enabled and the underlying POST /clusters/{name}/test
// returns 503 + error_code=no_secrets_backend, which is needlessly
// confusing in dev / `--demo` mode.
func TestHealthEndpoint_ClusterTestAvailable_False_NoCredProvider(t *testing.T) {
	srv := newTestServer() // no credProvider set
	router := NewRouter(srv, nil)

	req := httptest.NewRequest("GET", "/api/v1/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	got, ok := body["cluster_test_available"]
	if !ok {
		t.Fatalf("response missing cluster_test_available field; got keys: %v", body)
	}
	if available, _ := got.(bool); available {
		t.Errorf("expected cluster_test_available=false when no credProvider configured, got %v", got)
	}
}

// BUG-041 (paired with the false case above): when a credentials provider
// IS configured on the active connection, /health must report
// cluster_test_available=true so the UI renders the per-cluster Test
// button fully enabled.
func TestHealthEndpoint_ClusterTestAvailable_True_WithCredProvider(t *testing.T) {
	srv := newTestServer()
	installCredProvider(srv, &healthTestStubCredProvider{}, nil, nil)
	router := NewRouter(srv, nil)

	req := httptest.NewRequest("GET", "/api/v1/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	available, ok := body["cluster_test_available"].(bool)
	if !ok {
		t.Fatalf("cluster_test_available not a bool: %v", body["cluster_test_available"])
	}
	if !available {
		t.Errorf("expected cluster_test_available=true with credProvider configured, got false")
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
