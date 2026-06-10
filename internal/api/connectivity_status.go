package api

import (
	"strings"
	"time"

	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/observations"
)

// checkPendingEscalation is the age after which a still-pending check app is
// escalated to check_failed. Without an ArgoCD webhook a new app can take ~3
// minutes to sync; 10 minutes is a clear signal something is wrong.
const checkPendingEscalation = 10 * time.Minute

// connectivityVerdict is the result of the priority connectivity computation
// for one cluster.
type connectivityVerdict struct {
	// Status values: "verified_argocd" | "verified_check" | "check_pending" | "check_failed" | ""
	Status string
	// Detail is populated when Status is "check_pending" or "check_failed".
	Detail string
}

// computeConnectivityVerdict applies the priority logic for connectivity.
// It is a thin wrapper around computeConnectivityVerdictAt using time.Now().
//
// apps is the pre-fetched ArgoCD application list for the cluster (the same
// slice the handler already holds — no extra API call). The check Application
// is identified by name "connectivity-check-<clusterName>".
func computeConnectivityVerdict(clusterName, connectionStatus string, apps []models.ArgocdApplication) connectivityVerdict {
	return computeConnectivityVerdictAt(clusterName, connectionStatus, apps, time.Now())
}

// computeConnectivityVerdictAt is the testable inner implementation; callers
// inject "now" to control time.
//
// Priority order:
//  1. ArgoCD ConnectionStatus == "Successful" → "verified_argocd"
//  2. Connectivity-check Application Synced+Healthy → "verified_check"
//  3. Connectivity-check Application has honest failure signals → "check_failed"
//  4. Connectivity-check Application exists but not yet healthy → "check_pending"
//     (escalated to "check_failed" when age > checkPendingEscalation)
//  5. No check app → "" (ArgoCD "Unknown" stands)
//
// Honest failure signals (Priority 3):
//   - HealthStatus == "Degraded"
//   - OperationPhase in {"Failed", "Error"} (ArgoCD client literal values)
//   - Any Condition whose Type contains "Error" (SyncError, ComparisonError,
//     InvalidSpecError, etc.) — plain warnings are NOT failures.
func computeConnectivityVerdictAt(clusterName, connectionStatus string, apps []models.ArgocdApplication, now time.Time) connectivityVerdict {
	// Priority 1: ArgoCD itself says the connection is fine.
	if connectionStatus == "Successful" {
		return connectivityVerdict{Status: "verified_argocd"}
	}

	// Find the connectivity-check Application in the pre-fetched app list.
	checkAppName := "connectivity-check-" + clusterName
	var checkApp *models.ArgocdApplication
	for i := range apps {
		if apps[i].Name == checkAppName {
			checkApp = &apps[i]
			break
		}
	}

	if checkApp == nil {
		// No check app → nothing known beyond what ArgoCD says.
		return connectivityVerdict{}
	}

	// Priority 2: check app Synced + Healthy.
	if checkApp.SyncStatus == "Synced" && checkApp.HealthStatus == "Healthy" {
		return connectivityVerdict{Status: "verified_check"}
	}

	// Priority 3: honest failure signals.
	if isHonestFailure(checkApp) {
		return connectivityVerdict{Status: "check_failed", Detail: buildCheckFailDetail(checkApp)}
	}

	// Priority 4: check app exists but is not yet healthy → pending.
	// Escalate to failed if the app has been around longer than the threshold.
	if checkApp.CreatedAt != "" {
		created, err := time.Parse(time.RFC3339, checkApp.CreatedAt)
		if err == nil && now.Sub(created) > checkPendingEscalation {
			return connectivityVerdict{
				Status: "check_failed",
				Detail: "connectivity check has not completed after 10 minutes — inspect the connectivity-check application in ArgoCD",
			}
		}
	}

	return connectivityVerdict{
		Status: "check_pending",
		Detail: "connectivity check is deploying — usually under a minute; without an ArgoCD webhook it can take ~3 minutes",
	}
}

// isHonestFailure returns true only on genuine failure signals from ArgoCD,
// not transient "app just created" states like OutOfSync or Missing health.
func isHonestFailure(app *models.ArgocdApplication) bool {
	// Degraded health is a concrete failure.
	if app.HealthStatus == "Degraded" {
		return true
	}
	// OperationPhase "Failed" or "Error" means ArgoCD tried and failed.
	// These are the literal phase strings the ArgoCD client maps from
	// operationState.phase in the API response.
	if app.OperationPhase == "Failed" || app.OperationPhase == "Error" {
		return true
	}
	// Conditions whose Type contains "Error" are real errors
	// (SyncError, ComparisonError, InvalidSpecError, etc.).
	// Conditions whose Type does NOT contain "Error" (e.g. SharedResourceWarning)
	// are mere warnings and do NOT constitute failure.
	for _, c := range app.Conditions {
		if strings.Contains(c.Type, "Error") {
			return true
		}
	}
	return false
}

// buildCheckFailDetail assembles a short human-readable reason from the
// ArgoCD application's operation message and conditions.
func buildCheckFailDetail(app *models.ArgocdApplication) string {
	// Prefer the operation message (sync error detail).
	if app.OperationMessage != "" {
		return app.OperationMessage
	}
	// Fall back to first error condition message.
	for _, c := range app.Conditions {
		if strings.Contains(c.Type, "Error") && c.Message != "" {
			return c.Message
		}
	}
	// Generic fallback.
	return "connectivity-check application sync or health error — inspect in ArgoCD"
}

// applyObsFields fills the Sharko observability fields on cluster from obs.
// obs may be nil (obsStore unavailable); in that case the fields are left
// at their zero values (all omitempty, so they are absent from JSON).
func applyObsFields(cluster *models.Cluster, obs *observations.Observation) {
	if obs == nil {
		return
	}
	result := observations.ComputeStatus(obs, false)
	cluster.SharkoStatus = string(result.Status)
	if !result.LastTestAt.IsZero() {
		cluster.LastTestAt = result.LastTestAt.UTC().Format(time.RFC3339)
	}
	cluster.TestFailing = result.TestFailing
	cluster.TestErrorCode = result.ErrorCode
}
