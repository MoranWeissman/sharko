package api

import (
	"strings"
	"time"

	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/observations"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
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
	checkAppName := orchestrator.ConnectivityCheckAppPrefix + clusterName
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

// Derived health status values (V2-cleanup-85.4). See models.Cluster.DerivedHealthStatus.
const (
	derivedHealthHealthy   = "healthy"
	derivedHealthReachable = "reachable"
	derivedHealthUnknown   = "unknown"
)

// computeDerivedHealth applies the V2-cleanup-85.4 auto-derivation order for
// cluster health — independent of any manual "Test connection" click
// (models.Cluster.SharkoStatus). First match wins:
//
//  1. hasHealthyAddon (any addon on the cluster is Synced+Healthy in ArgoCD)
//     -> "healthy"
//  2. verdict.Status == "verified_check" (the connectivity-check application
//     is itself Synced+Healthy) -> "reachable"
//  3. verdict.Status == "verified_argocd" OR connectionStatus == "Successful"
//     (ArgoCD's own connection to the cluster is fine) -> "reachable"
//  4. none of the above -> "unknown"
//
// probe_mode (check-app vs api-test, V2-cleanup-85.4) is not threaded
// through explicitly: in api-test mode no connectivity-check application is
// ever deployed to the cluster (see the register/reconcile path), so
// verdict.Status can never be "verified_check" and step 2 falls through to
// step 3 on its own — exactly the "step 2 is simply skipped" behavior the
// design calls for.
func computeDerivedHealth(hasHealthyAddon bool, verdict connectivityVerdict, connectionStatus string) string {
	if hasHealthyAddon {
		return derivedHealthHealthy
	}
	if verdict.Status == "verified_check" {
		return derivedHealthReachable
	}
	if verdict.Status == "verified_argocd" || connectionStatus == "Successful" {
		return derivedHealthReachable
	}
	return derivedHealthUnknown
}

// clusterHasHealthyAddon reports whether any non-system ArgoCD Application
// destined for this cluster is currently Synced+Healthy. This is step 1 of
// the V2-cleanup-85.4 derivation order — the signal that was previously
// invisible to the cluster LIST response (it lives only in the per-cluster
// comparison endpoint's TotalHealthy field).
//
// Matching mirrors internal/argocd.Service.GetClusterApplications' three
// strategies (destination server URL, destination name, "-<clusterName>"
// name suffix) so the same app is recognized as belonging to the same
// cluster everywhere in Sharko — but without GetClusterApplications' extra
// ArgoCD ListClusters call: serverURL is the cluster's already-registered
// ArgoCD server URL (models.Cluster.ServerURL), populated at read time by
// the caller.
//
// System apps (the bootstrap root app, any connectivity-check app) are
// excluded — they are not catalog addons and must never make a cluster
// look "healthy" via this path (that is step 2's job).
func clusterHasHealthyAddon(clusterName, serverURL string, apps []models.ArgocdApplication) bool {
	for i := range apps {
		app := &apps[i]
		if orchestrator.IsSharkoSystemApp(app.Name) {
			continue
		}
		matches := (serverURL != "" && app.DestinationServer == serverURL) ||
			(app.DestinationName != "" && app.DestinationName == clusterName) ||
			strings.HasSuffix(app.Name, "-"+clusterName)
		if !matches {
			continue
		}
		if app.SyncStatus == "Synced" && app.HealthStatus == "Healthy" {
			return true
		}
	}
	return false
}

// detectConnectivityCheckDrift checks whether a cluster stuck at "check_pending"
// is actually stranded due to connectivity-check ApplicationSet label-selector
// drift (W4a — V3 RW1.8). Returns a plain-English reason + next step if drift
// is detected, empty string otherwise.
//
// Logic (reuses the doctor check #6 signal):
//   - If the cluster's Secret has the connectivity-check label (either key)
//   - AND the expected connectivity-check-<cluster> Application does NOT exist
//   - AND the cluster has NO real addon apps deployed (which would correctly
//     suppress the check app)
//   - THEN drift is detected → return a reason.
//
// This is a lighter version of doctorCheckConnectivityApp, focused on the
// passive status enrichment case (no full doctor-check machinery).
func detectConnectivityCheckDrift(clusterName string, secretLabels map[string]string, apps []models.ArgocdApplication) string {
	// Only check if the cluster Secret has the connectivity-check label.
	if !models.HasConnectivityCheckLabel(secretLabels) {
		return ""
	}

	// Check if the expected connectivity-check app exists.
	checkAppName := "connectivity-check-" + clusterName
	checkAppFound := false
	for i := range apps {
		if apps[i].Name == checkAppName {
			checkAppFound = true
			break
		}
	}

	if checkAppFound {
		// App exists → no drift.
		return ""
	}

	// The cluster is labeled but the app is missing. Check if any real addon
	// app is deployed (which would correctly suppress the check app).
	for i := range apps {
		app := &apps[i]
		// Skip system apps (bootstrap root, connectivity-check apps).
		if orchestrator.IsSharkoSystemApp(app.Name) {
			continue
		}
		// Check if this app targets our cluster via name-suffix matching.
		if strings.HasSuffix(app.Name, "-"+clusterName) {
			// Real addon found → check app correctly yielded, no drift.
			return ""
		}
	}

	// Drift detected: cluster is labeled, no check app, no real addons.
	return "Connectivity-check ApplicationSet selector may be stale (sharko.io → sharko.dev label rename). Re-apply the current bootstrap templates to refresh the selector."
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
