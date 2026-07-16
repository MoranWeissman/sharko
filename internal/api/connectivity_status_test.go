package api

import (
	"strings"
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

// TestClusterHasHealthyAddon covers the V2-cleanup-85.4 addon-matching
// helper: destination server URL match, destination name match, name-suffix
// fallback, and the exclusion of Sharko system apps (bootstrap +
// connectivity-check) from counting as "an addon".
func TestClusterHasHealthyAddon(t *testing.T) {
	t.Parallel()

	const cluster = "prod-eu"
	const serverURL = "https://prod-eu.example.com"

	tests := []struct {
		name string
		apps []models.ArgocdApplication
		want bool
	}{
		{
			name: "matched by destination server, Synced+Healthy",
			apps: []models.ArgocdApplication{
				{Name: "datadog-prod-eu", DestinationServer: serverURL, SyncStatus: "Synced", HealthStatus: "Healthy"},
			},
			want: true,
		},
		{
			name: "matched by destination name, Synced+Healthy",
			apps: []models.ArgocdApplication{
				{Name: "karpenter", DestinationName: cluster, SyncStatus: "Synced", HealthStatus: "Healthy"},
			},
			want: true,
		},
		{
			name: "matched by name suffix, Synced+Healthy",
			apps: []models.ArgocdApplication{
				{Name: "keda-" + cluster, SyncStatus: "Synced", HealthStatus: "Healthy"},
			},
			want: true,
		},
		{
			name: "matched but OutOfSync — not healthy",
			apps: []models.ArgocdApplication{
				{Name: "datadog-prod-eu", DestinationServer: serverURL, SyncStatus: "OutOfSync", HealthStatus: "Healthy"},
			},
			want: false,
		},
		{
			name: "matched but Degraded — not healthy",
			apps: []models.ArgocdApplication{
				{Name: "datadog-prod-eu", DestinationServer: serverURL, SyncStatus: "Synced", HealthStatus: "Degraded"},
			},
			want: false,
		},
		{
			name: "connectivity-check app is not an addon, even if Synced+Healthy",
			apps: []models.ArgocdApplication{
				{Name: "connectivity-check-" + cluster, DestinationServer: serverURL, SyncStatus: "Synced", HealthStatus: "Healthy"},
			},
			want: false,
		},
		{
			name: "bootstrap root app is not an addon, even if Synced+Healthy",
			apps: []models.ArgocdApplication{
				{Name: "cluster-addons-bootstrap", DestinationServer: serverURL, SyncStatus: "Synced", HealthStatus: "Healthy"},
			},
			want: false,
		},
		{
			name: "app for a different cluster is not matched",
			apps: []models.ArgocdApplication{
				{Name: "datadog-other-cluster", DestinationServer: "https://other.example.com", SyncStatus: "Synced", HealthStatus: "Healthy"},
			},
			want: false,
		},
		{
			name: "no apps at all",
			apps: nil,
			want: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := clusterHasHealthyAddon(cluster, serverURL, tc.apps)
			if got != tc.want {
				t.Errorf("clusterHasHealthyAddon() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestComputeDerivedHealth covers the V2-cleanup-85.4 auto-derivation order:
// addon health first, then check-app verdict, then ArgoCD's own connection —
// with NO dependency on any manual "Test connection" result.
func TestComputeDerivedHealth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		hasHealthyAddon  bool
		verdict          connectivityVerdict
		connectionStatus string
		want             string
	}{
		{
			name:            "step 1: healthy addon wins outright, even with no other signal",
			hasHealthyAddon: true,
			verdict:         connectivityVerdict{},
			want:            derivedHealthHealthy,
		},
		{
			name:            "step 1: healthy addon wins even when ArgoCD connection looks bad",
			hasHealthyAddon: true,
			verdict:         connectivityVerdict{Status: "check_failed"},
			want:            derivedHealthHealthy,
		},
		{
			name:            "step 2: check app healthy, no addon yet",
			hasHealthyAddon: false,
			verdict:         connectivityVerdict{Status: "verified_check"},
			want:            derivedHealthReachable,
		},
		{
			name:             "step 3: ArgoCD connection verdict Successful (api-test mode — no check app ever exists)",
			hasHealthyAddon:  false,
			verdict:          connectivityVerdict{}, // no check app in ArgoCD at all
			connectionStatus: "Successful",
			want:             derivedHealthReachable,
		},
		{
			name:            "step 3: verified_argocd verdict also counts",
			hasHealthyAddon: false,
			verdict:         connectivityVerdict{Status: "verified_argocd"},
			want:            derivedHealthReachable,
		},
		{
			name:            "step 4: nothing known",
			hasHealthyAddon: false,
			verdict:         connectivityVerdict{},
			want:            derivedHealthUnknown,
		},
		{
			name:            "step 4: check_pending is not reachable",
			hasHealthyAddon: false,
			verdict:         connectivityVerdict{Status: "check_pending"},
			want:            derivedHealthUnknown,
		},
		{
			name:            "step 4: check_failed is not reachable",
			hasHealthyAddon: false,
			verdict:         connectivityVerdict{Status: "check_failed"},
			want:            derivedHealthUnknown,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := computeDerivedHealth(tc.hasHealthyAddon, tc.verdict, tc.connectionStatus)
			if got != tc.want {
				t.Errorf("computeDerivedHealth() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDetectConnectivityCheckDrift covers W4a (V3 RW1.8): detecting when a
// cluster is stuck at "check_pending" due to ApplicationSet label-selector
// drift (sharko.io → sharko.dev rename).
func TestDetectConnectivityCheckDrift(t *testing.T) {
	t.Parallel()

	const clusterName = "test-cluster"
	checkAppName := "connectivity-check-" + clusterName

	tests := []struct {
		name         string
		secretLabels map[string]string
		apps         []models.ArgocdApplication
		wantDrift    bool // true = drift detected, reason returned
	}{
		{
			name:         "no connectivity-check label → not applicable",
			secretLabels: map[string]string{},
			apps:         []models.ArgocdApplication{},
			wantDrift:    false,
		},
		{
			name: "has canonical label, check app exists → no drift",
			secretLabels: map[string]string{
				models.LabelConnectivityCheck: models.LabelEnabled,
			},
			apps: []models.ArgocdApplication{
				{Name: checkAppName},
			},
			wantDrift: false,
		},
		{
			name: "has legacy label, check app exists → no drift",
			secretLabels: map[string]string{
				models.LabelConnectivityCheckLegacy: models.LabelEnabled,
			},
			apps: []models.ArgocdApplication{
				{Name: checkAppName},
			},
			wantDrift: false,
		},
		{
			name: "has label, check app missing, real addon deployed → no drift (check app correctly yielded)",
			secretLabels: map[string]string{
				models.LabelConnectivityCheck: models.LabelEnabled,
			},
			apps: []models.ArgocdApplication{
				{Name: "velero-" + clusterName}, // real addon
			},
			wantDrift: false,
		},
		{
			name: "W4a: has label, check app missing, NO real addons → DRIFT DETECTED",
			secretLabels: map[string]string{
				models.LabelConnectivityCheck: models.LabelEnabled,
			},
			apps:      []models.ArgocdApplication{},
			wantDrift: true,
		},
		{
			name: "W4a: legacy label only, check app missing, no addons → DRIFT DETECTED",
			secretLabels: map[string]string{
				models.LabelConnectivityCheckLegacy: models.LabelEnabled,
			},
			apps:      []models.ArgocdApplication{},
			wantDrift: true,
		},
		{
			name: "system apps (bootstrap, other check apps) don't count as real addons",
			secretLabels: map[string]string{
				models.LabelConnectivityCheck: models.LabelEnabled,
			},
			apps: []models.ArgocdApplication{
				{Name: "sharko-bootstrap"},
				{Name: "connectivity-check-other-cluster"},
			},
			wantDrift: true, // system apps ignored → still drift
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reason := detectConnectivityCheckDrift(clusterName, tc.secretLabels, tc.apps)
			gotDrift := reason != ""

			if gotDrift != tc.wantDrift {
				t.Errorf("detectConnectivityCheckDrift() drift=%v (reason=%q), want drift=%v",
					gotDrift, reason, tc.wantDrift)
			}

			// When drift is detected, the reason must be non-empty and mention the fix.
			if tc.wantDrift && reason == "" {
				t.Error("expected a non-empty drift reason when wantDrift=true")
			}
			if tc.wantDrift && !strings.Contains(strings.ToLower(reason), "selector") {
				t.Errorf("expected drift reason to mention 'selector', got %q", reason)
			}
		})
	}
}
