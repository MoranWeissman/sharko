package api

import (
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

func TestComputeConnectivityVerdict(t *testing.T) {
	t.Parallel()

	const cluster = "my-cluster"
	checkApp := "connectivity-check-" + cluster

	tests := []struct {
		name             string
		connectionStatus string
		apps             []models.ArgocdApplication
		wantStatus       string
		wantDetail       bool // true = Detail must be non-empty
	}{
		{
			name:             "verified_argocd: ArgoCD connection successful",
			connectionStatus: "Successful",
			apps:             nil,
			wantStatus:       "verified_argocd",
		},
		{
			name:             "verified_check: check app Synced+Healthy",
			connectionStatus: "Unknown",
			apps: []models.ArgocdApplication{
				{Name: checkApp, SyncStatus: "Synced", HealthStatus: "Healthy"},
			},
			wantStatus: "verified_check",
		},
		{
			name:             "check_failed: check app Degraded",
			connectionStatus: "Unknown",
			apps: []models.ArgocdApplication{
				{Name: checkApp, SyncStatus: "Synced", HealthStatus: "Degraded",
					OperationMessage: "ConfigMap creation failed: namespace not found"},
			},
			wantStatus: "check_failed",
			wantDetail: true,
		},
		{
			name:             "check_failed: check app OutOfSync",
			connectionStatus: "Unknown",
			apps: []models.ArgocdApplication{
				{Name: checkApp, SyncStatus: "OutOfSync", HealthStatus: "Healthy"},
			},
			wantStatus: "check_failed",
		},
		{
			name:             "check_failed: check app has conditions",
			connectionStatus: "Unknown",
			apps: []models.ArgocdApplication{
				{Name: checkApp, SyncStatus: "Synced", HealthStatus: "Unknown",
					Conditions: []models.AppCondition{{Type: "SyncError", Message: "repo unreachable"}}},
			},
			wantStatus: "check_failed",
			wantDetail: true,
		},
		{
			name:             "nothing known: no check app, ArgoCD Unknown",
			connectionStatus: "Unknown",
			apps:             nil,
			wantStatus:       "",
		},
		{
			name:             "nothing known: check app Progressing (not failed)",
			connectionStatus: "Unknown",
			apps: []models.ArgocdApplication{
				{Name: checkApp, SyncStatus: "Synced", HealthStatus: "Progressing"},
			},
			wantStatus: "",
		},
		{
			name:             "verified_argocd wins over check app",
			connectionStatus: "Successful",
			apps: []models.ArgocdApplication{
				{Name: checkApp, SyncStatus: "Synced", HealthStatus: "Healthy"},
			},
			wantStatus: "verified_argocd",
		},
		{
			name:             "check app for different cluster not matched",
			connectionStatus: "Unknown",
			apps: []models.ArgocdApplication{
				{Name: "connectivity-check-other-cluster", SyncStatus: "Synced", HealthStatus: "Healthy"},
			},
			wantStatus: "",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := computeConnectivityVerdict(cluster, tc.connectionStatus, tc.apps)
			if got.Status != tc.wantStatus {
				t.Errorf("Status = %q, want %q", got.Status, tc.wantStatus)
			}
			if tc.wantDetail && got.Detail == "" {
				t.Error("Detail must be non-empty for check_failed, got empty string")
			}
		})
	}
}
