package api

// tier_registry.go — classifies every mutating handler in this package as
// Tier 1 (operational) or Tier 2 (configuration), with a few exempt buckets
// for self-service and auth flows. See internal/audit/tier.go for the model
// and docs/design/2026-04-16-attribution-and-permissions-model.md.
//
// CI guard: tier_coverage_test.go fails if any handler registered for a
// mutating method is missing from this map (or the allowlist).

import "github.com/MoranWeissman/sharko/internal/audit"

// HandlerTier is the canonical tier for each mutating handler in this package.
// Keys are the unqualified handler function name (e.g. "handleRegisterCluster").
//
// When you add a new mutating endpoint, add an entry here. The tier_coverage_test
// will fail otherwise. If the endpoint is not a real Git-affecting mutation
// (auth, self-service, webhook), use the matching exempt tier and add it to
// the allowlist if appropriate.
var HandlerTier = map[string]audit.Tier{
	// ─── Tier 1: operational actions ────────────────────────────────────────
	// Cluster lifecycle and operations
	"handleRegisterCluster":            audit.Tier1,
	"handleDeregisterCluster":          audit.Tier1,
	"handleUpdateClusterAddons":        audit.Tier1,
	"handleRefreshClusterCredentials":  audit.Tier1,
	"handleTestCluster":                audit.Tier1,
	"handleDiagnoseCluster":            audit.Tier1,
	"handleUnadoptCluster":             audit.Tier1,
	"handleEnableAddon":                audit.Tier1,
	"handleDisableAddon":               audit.Tier1,
	"handleBatchRegisterClusters":      audit.Tier1,
	"handleAdoptClusters":              audit.Tier1,
	"handleDiscoverEKS":                audit.Tier1,

	// Addon ops on existing addons (upgrade is operational; the version is the catalog)
	"handleUpgradeAddon":               audit.Tier1,
	"handleUpgradeAddonsBatch":         audit.Tier1,

	// ArgoCD cluster secret sync, secret reconciliation
	"handleTriggerReconcile":           audit.Tier1,
	"handleRefreshClusterSecrets":      audit.Tier1,

	// Connection CRUD — Tier 1 because it manages Sharko-internal credentials, not values
	"handleCreateConnection":           audit.Tier1,
	"handleUpdateConnection":           audit.Tier1,
	"handleDeleteConnection":           audit.Tier1,
	"handleSetActiveConnection":        audit.Tier1,
	"handleTestConnection":             audit.Tier1,
	"handleTestCredentials":            audit.Tier1,

	// AI configuration — operational platform settings
	"handleSaveAIConfig":               audit.Tier1,
	"handleSetAIProvider":              audit.Tier1,
	"handleTestAI":                     audit.Tier1,

	// Dashboards (embedded)
	"handleSaveDashboards":             audit.Tier1,

	// Pull request management
	"handleRefreshPR":                  audit.Tier1,
	"handleDeletePR":                   audit.Tier1,

	// Operations (async tracking)
	"handleCancelOperation":            audit.Tier1,

	// Init bootstrap — operational platform setup (writes to repo but as platform action)
	"handleInit":                       audit.Tier1,

	// ArtifactHub proxy reprobe (v1.21) — operational: clears caches and probes
	// the upstream. Read-class but admin-tier so audit captures who reset state.
	"handleReprobeArtifactHub":         audit.Tier1,

	// Test/discover endpoints that POST credentials for validation
	"handleTestProvider":               audit.Tier1,
	"handleTestProviderConfig":         audit.Tier1,

	// ─── Tier 2: configuration changes (define future state) ────────────────
	// Catalog (addon catalog metadata: sync wave, sync options, ignore differences,
	// additional sources, version pin)
	"handleAddAddon":                   audit.Tier2,
	"handleRemoveAddon":                audit.Tier2,
	"handleConfigureAddon":             audit.Tier2,

	// Addon secret definitions — these define what secrets get reconciled where,
	// which changes future deployment behaviour, so config-tier.
	"handleCreateAddonSecret":          audit.Tier2,
	"handleDeleteAddonSecret":          audit.Tier2,

	// Values editor (v1.20) — Tier 2: changes WHAT gets deployed.
	// In v1.21 (Story V121-6.4) the same handler also services the
	// `refresh_from_upstream` flow that used to live on the now-removed
	// `pull-upstream` endpoint.
	"handleSetAddonValues":             audit.Tier2,
	"handleSetClusterAddonValues":      audit.Tier2,

	// V121-7.4: AI annotate + per-addon opt-out — both regenerate the
	// global values file (header + body), opening a Tier 2 PR.
	"handleAnnotateAddonValues":        audit.Tier2,
	"handleSetAddonAIOptOut":           audit.Tier2,

	// v1.21 Bundle 5: legacy `<addon>:` wrap migration. Tier 2 because
	// it rewrites every global values file in the repo and opens a PR.
	"handleUnwrapGlobalValues":         audit.Tier2,

	// V123-1.6 — force-refresh third-party catalog sources. Tier 2
	// because this is a configuration-time admin action (verifying a
	// newly added SHARKO_CATALOG_URLS entry) rather than a cluster
	// operation.
	"handleRefreshCatalogSources":      audit.Tier2,

	// v1.21 QA Bundle 4 (Fix #4): preview-merge is read-only — it returns
	// a candidate body but does not write Git. Classified TierPersonal so
	// it doesn't count as a real mutation in audit attribution. The actual
	// mutation happens through the existing PUT /addons/{name}/values
	// handler (already Tier 2) when the user clicks "Apply changes".
	"handlePreviewMergeAddonValues":    audit.TierPersonal,

	// ─── Personal: self-service on caller's own profile ─────────────────────
	"handleUpdatePassword":             audit.TierPersonal,

	// ─── Auth: handled by the auth allowlist already, also list here ────────
	"handleLogin":                      audit.TierAuth,
	"handleLogout":                     audit.TierAuth,
	"handleHashPassword":               audit.TierAuth,

	// ─── Webhook: inbound signed payload, no user identity ──────────────────
	"handleGitWebhook":                 audit.TierWebhook,

	// ─── User management (admin actions on other users) ─────────────────────
	// Tier 1: it's an operational action by an admin, not a config change
	// to the addon catalog or values.
	"handleCreateUser":                 audit.Tier1,
	"handleUpdateUser":                 audit.Tier1,
	"handleDeleteUser":                 audit.Tier1,
	"handleResetPassword":              audit.Tier1,

	// ─── API token management ───────────────────────────────────────────────
	"handleCreateToken":                audit.Tier1,
	"handleRevokeToken":                audit.Tier1,

	// ─── Operation heartbeat / system noise ─────────────────────────────────
	"handleOperationHeartbeat":         audit.TierPersonal, // client keep-alive ping

	// ─── Mark-all-read (UI state, not a real mutation) ──────────────────────
	"handleMarkAllNotificationsRead":   audit.TierPersonal,

	// ─── Agent chat (high-frequency, no Git effect) ─────────────────────────
	"handleAgentChat":                  audit.TierPersonal,
	"handleAgentReset":                 audit.TierPersonal,

	// ─── AI test / read-like POSTs (analysis endpoints) ─────────────────────
	"handleGetAISummary":               audit.TierPersonal,
	"handleTestAIConfig":               audit.TierPersonal,
	"handleCheckUpgrade":               audit.TierPersonal, // analysis only, no mutation

	// ─── Per-user PAT management (added in v1.20 for tiered attribution) ────
	"handleSetMyGitHubToken":           audit.TierPersonal,
	"handleClearMyGitHubToken":         audit.TierPersonal,
	"handleTestMyGitHubToken":          audit.TierPersonal,
}

// TierFor returns the tier for the given handler name. Defaults to Tier1 for
// unclassified handlers — the CI guard ensures this never happens in practice.
func TierFor(handler string) audit.Tier {
	if t, ok := HandlerTier[handler]; ok {
		return t
	}
	return audit.Tier1
}
