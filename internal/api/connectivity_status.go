package api

import (
	"time"

	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/observations"
)

// connectivityVerdict is the result of the priority connectivity computation
// for one cluster.
type connectivityVerdict struct {
	// Status values: "verified_argocd" | "verified_check" | "check_failed" | ""
	Status string
	// Detail is populated only when Status == "check_failed".
	Detail string
}

// computeConnectivityVerdict applies the four-priority logic described in
// V2-cleanup-29:
//
//  1. ArgoCD ConnectionStatus == "Successful" → "verified_argocd"
//  2. Connectivity-check Application Synced+Healthy  → "verified_check"
//  3. Connectivity-check Application degraded/error  → "check_failed" + detail
//  4. Otherwise                                       → "" (ArgoCD "Unknown" stands)
//
// apps is the pre-fetched ArgoCD application list for the cluster (the same
// slice the handler already holds — no extra API call). The check Application
// is identified by name "connectivity-check-<clusterName>".
func computeConnectivityVerdict(clusterName, connectionStatus string, apps []models.ArgocdApplication) connectivityVerdict {
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

	// Priority 3: check app is in a bad state → surface the reason.
	isDegraded := checkApp.HealthStatus == "Degraded" || checkApp.HealthStatus == "Missing"
	isSyncError := checkApp.SyncStatus == "OutOfSync" || checkApp.SyncStatus == "Unknown"
	hasConditions := len(checkApp.Conditions) > 0
	if isDegraded || isSyncError || hasConditions {
		detail := buildCheckFailDetail(checkApp)
		return connectivityVerdict{Status: "check_failed", Detail: detail}
	}

	// Not yet Synced+Healthy but not obviously failed (e.g. Progressing) → empty.
	return connectivityVerdict{}
}

// buildCheckFailDetail assembles a short human-readable reason from the
// ArgoCD application's operation message and conditions.
func buildCheckFailDetail(app *models.ArgocdApplication) string {
	// Prefer the operation message (sync error detail).
	if app.OperationMessage != "" {
		return app.OperationMessage
	}
	// Fall back to first condition message.
	for _, c := range app.Conditions {
		if c.Message != "" {
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
