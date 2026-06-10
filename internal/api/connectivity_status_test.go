package api

import (
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/models"
)

func TestComputeConnectivityVerdict(t *testing.T) {
	t.Parallel()

	const cluster = "my-cluster"
	checkApp := "connectivity-check-" + cluster

	// A fixed "now" used for time-sensitive tests.
	fixedNow := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	recentCreatedAt := fixedNow.Add(-30 * time.Second).UTC().Format(time.RFC3339)
	oldCreatedAt := fixedNow.Add(-11 * time.Minute).UTC().Format(time.RFC3339)

	tests := []struct {
		name             string
		connectionStatus string
		apps             []models.ArgocdApplication
		now              time.Time // zero → use time.Now() via thin wrapper
		wantStatus       string
		wantDetail       bool // true = Detail must be non-empty
	}{
		// --- Priority 1: ArgoCD ConnectionStatus ---
		{
			name:             "verified_argocd: ArgoCD connection successful",
			connectionStatus: "Successful",
			apps:             nil,
			wantStatus:       "verified_argocd",
		},
		{
			name:             "verified_argocd wins over check app",
			connectionStatus: "Successful",
			apps: []models.ArgocdApplication{
				{Name: checkApp, SyncStatus: "Synced", HealthStatus: "Healthy"},
			},
			wantStatus: "verified_argocd",
		},

		// --- Priority 2: Synced+Healthy ---
		{
			name:             "verified_check: check app Synced+Healthy",
			connectionStatus: "Unknown",
			apps: []models.ArgocdApplication{
				{Name: checkApp, SyncStatus: "Synced", HealthStatus: "Healthy"},
			},
			wantStatus: "verified_check",
		},

		// --- Priority 3: honest failure signals ---
		{
			name:             "check_failed: Degraded health",
			connectionStatus: "Unknown",
			apps: []models.ArgocdApplication{
				{Name: checkApp, SyncStatus: "Synced", HealthStatus: "Degraded",
					OperationMessage: "ConfigMap creation failed: namespace not found"},
			},
			wantStatus: "check_failed",
			wantDetail: true,
		},
		{
			name:             "check_failed: OperationPhase Failed",
			connectionStatus: "Unknown",
			apps: []models.ArgocdApplication{
				{Name: checkApp, SyncStatus: "OutOfSync", HealthStatus: "Missing",
					OperationPhase: "Failed", OperationMessage: "sync operation failed"},
			},
			wantStatus: "check_failed",
			wantDetail: true,
		},
		{
			name:             "check_failed: OperationPhase Error",
			connectionStatus: "Unknown",
			apps: []models.ArgocdApplication{
				{Name: checkApp, SyncStatus: "OutOfSync", HealthStatus: "Unknown",
					OperationPhase: "Error", OperationMessage: "hook error"},
			},
			wantStatus: "check_failed",
			wantDetail: true,
		},
		{
			name:             "check_failed: condition type SyncError",
			connectionStatus: "Unknown",
			apps: []models.ArgocdApplication{
				{Name: checkApp, SyncStatus: "Synced", HealthStatus: "Unknown",
					Conditions: []models.AppCondition{{Type: "SyncError", Message: "repo unreachable"}}},
			},
			wantStatus: "check_failed",
			wantDetail: true,
		},
		{
			name:             "check_failed: condition type ComparisonError",
			connectionStatus: "Unknown",
			apps: []models.ArgocdApplication{
				{Name: checkApp, SyncStatus: "OutOfSync", HealthStatus: "Unknown",
					Conditions: []models.AppCondition{{Type: "ComparisonError", Message: "manifest error"}}},
			},
			wantStatus: "check_failed",
			wantDetail: true,
		},

		// --- Priority 4: pending (transient / not-yet-started states) ---
		{
			name:             "check_pending: fresh OutOfSync",
			connectionStatus: "Unknown",
			now:              fixedNow,
			apps: []models.ArgocdApplication{
				{Name: checkApp, SyncStatus: "OutOfSync", HealthStatus: "Healthy",
					CreatedAt: recentCreatedAt},
			},
			wantStatus: "check_pending",
			wantDetail: true,
		},
		{
			name:             "check_pending: Missing health",
			connectionStatus: "Unknown",
			now:              fixedNow,
			apps: []models.ArgocdApplication{
				{Name: checkApp, SyncStatus: "OutOfSync", HealthStatus: "Missing",
					CreatedAt: recentCreatedAt},
			},
			wantStatus: "check_pending",
			wantDetail: true,
		},
		{
			name:             "check_pending: Progressing",
			connectionStatus: "Unknown",
			now:              fixedNow,
			apps: []models.ArgocdApplication{
				{Name: checkApp, SyncStatus: "Synced", HealthStatus: "Progressing",
					CreatedAt: recentCreatedAt},
			},
			wantStatus: "check_pending",
			wantDetail: true,
		},
		{
			name:             "check_pending: Unknown health",
			connectionStatus: "Unknown",
			now:              fixedNow,
			apps: []models.ArgocdApplication{
				{Name: checkApp, SyncStatus: "Unknown", HealthStatus: "Unknown",
					CreatedAt: recentCreatedAt},
			},
			wantStatus: "check_pending",
			wantDetail: true,
		},
		{
			name:             "check_pending: non-error condition is not a failure",
			connectionStatus: "Unknown",
			now:              fixedNow,
			apps: []models.ArgocdApplication{
				{Name: checkApp, SyncStatus: "OutOfSync", HealthStatus: "Unknown",
					CreatedAt:  recentCreatedAt,
					Conditions: []models.AppCondition{{Type: "SharedResourceWarning", Message: "resource conflict"}}},
			},
			wantStatus: "check_pending",
			wantDetail: true,
		},
		{
			name:             "check_pending: empty CreatedAt stays pending (never fail on missing metadata)",
			connectionStatus: "Unknown",
			now:              fixedNow,
			apps: []models.ArgocdApplication{
				{Name: checkApp, SyncStatus: "OutOfSync", HealthStatus: "Missing"},
			},
			wantStatus: "check_pending",
			wantDetail: true,
		},

		// --- Pending escalation ---
		{
			name:             "check_failed: pending escalated after 10 minutes",
			connectionStatus: "Unknown",
			now:              fixedNow,
			apps: []models.ArgocdApplication{
				{Name: checkApp, SyncStatus: "OutOfSync", HealthStatus: "Missing",
					CreatedAt: oldCreatedAt},
			},
			wantStatus: "check_failed",
			wantDetail: true,
		},

		// --- No check app ---
		{
			name:             "nothing known: no check app, ArgoCD Unknown",
			connectionStatus: "Unknown",
			apps:             nil,
			wantStatus:       "",
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
			var got connectivityVerdict
			if tc.now.IsZero() {
				got = computeConnectivityVerdict(cluster, tc.connectionStatus, tc.apps)
			} else {
				got = computeConnectivityVerdictAt(cluster, tc.connectionStatus, tc.apps, tc.now)
			}
			if got.Status != tc.wantStatus {
				t.Errorf("Status = %q, want %q", got.Status, tc.wantStatus)
			}
			if tc.wantDetail && got.Detail == "" {
				t.Errorf("Detail must be non-empty for status %q, got empty string", tc.wantStatus)
			}
		})
	}
}
