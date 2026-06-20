package api

// dashboard_attention_test.go — V2-cleanup-52 regression coverage for
// handleGetAttentionItems: Sharko system apps (bootstrap root +
// connectivity-check probes) must be excluded from the Needs-Attention feed
// even when they are unhealthy, because the frontend renders each item as a
// clickable link to /addons/<name> which 404s for these names.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// attentionFakeArgocd is a minimal ArgocdClient whose ListApplications returns
// a fixed list. All other methods are no-ops.
type attentionFakeArgocd struct {
	apps []models.ArgocdApplication
}

func (a *attentionFakeArgocd) ListApplications(_ context.Context) ([]models.ArgocdApplication, error) {
	return a.apps, nil
}
func (a *attentionFakeArgocd) ListClusters(_ context.Context) ([]models.ArgocdCluster, error) {
	return nil, nil
}
func (a *attentionFakeArgocd) RegisterCluster(_ context.Context, _, _ string, _ []byte, _ string, _ map[string]string) error {
	return nil
}
func (a *attentionFakeArgocd) DeleteCluster(_ context.Context, _ string) error { return nil }
func (a *attentionFakeArgocd) UpdateClusterLabels(_ context.Context, _ string, _ map[string]string) error {
	return nil
}
func (a *attentionFakeArgocd) SyncApplication(_ context.Context, _ string) error  { return nil }
func (a *attentionFakeArgocd) CreateProject(_ context.Context, _ []byte) error    { return nil }
func (a *attentionFakeArgocd) CreateApplication(_ context.Context, _ []byte) error { return nil }
func (a *attentionFakeArgocd) AddRepository(_ context.Context, _, _, _ string) error { return nil }
func (a *attentionFakeArgocd) GetApplication(_ context.Context, _ string) (*models.ArgocdApplication, error) {
	return nil, nil
}

// TestHandleGetAttentionItems_SystemAppsExcluded verifies that the bootstrap
// root Application and connectivity-check probes are filtered out of the
// Needs-Attention response even when they are unhealthy. Real addon apps that
// are unhealthy ARE included. Healthy addon apps are excluded (existing
// behaviour). (V2-cleanup-52)
func TestHandleGetAttentionItems_SystemAppsExcluded(t *testing.T) {
	srv := newTestServer()
	srv.connSvc.SetArgocdClientOverride(&attentionFakeArgocd{
		apps: []models.ArgocdApplication{
			// Sharko system app — bootstrap root, unhealthy. Must be excluded.
			{
				Name:         orchestrator.BootstrapRootAppName,
				HealthStatus: "Degraded",
				SyncStatus:   "OutOfSync",
			},
			// Sharko system app — connectivity-check probe, unhealthy. Must be excluded.
			{
				Name:         "connectivity-check-test-1",
				HealthStatus: "Degraded",
				SyncStatus:   "OutOfSync",
			},
			// Real addon app, unhealthy — MUST appear in the response.
			{
				Name:         "keda-prod",
				HealthStatus: "Degraded",
				SyncStatus:   "OutOfSync",
			},
			// Real addon app, healthy — must NOT appear (existing behaviour).
			{
				Name:         "cert-manager-prod",
				HealthStatus: "Healthy",
				SyncStatus:   "Synced",
			},
		},
	})

	router := NewRouter(srv, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/attention", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var items []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&items); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Collect app names returned.
	returned := make(map[string]bool)
	for _, item := range items {
		if name, ok := item["app_name"].(string); ok {
			returned[name] = true
		}
	}

	// keda-prod must be present.
	if !returned["keda-prod"] {
		t.Errorf("expected keda-prod (unhealthy real addon) to appear in attention items, got %v", returned)
	}
	// System apps must not appear.
	for _, sysName := range []string{orchestrator.BootstrapRootAppName, "connectivity-check-test-1"} {
		if returned[sysName] {
			t.Errorf("system app %q must NOT appear in attention items (V2-cleanup-52), but it did", sysName)
		}
	}
	// Healthy addon must not appear.
	if returned["cert-manager-prod"] {
		t.Errorf("healthy addon cert-manager-prod must NOT appear in attention items")
	}
}
