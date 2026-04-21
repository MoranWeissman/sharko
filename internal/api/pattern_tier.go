package api

// pattern_tier.go — static pattern→tier table, the fallback surface that lets
// auditMiddleware stamp a sensible attribution mode on audit entries produced
// by handlers that use the legacy Git path (connSvc.GetActiveGitProvider)
// instead of GitProviderForTier.
//
// Keeping this in a hand-maintained table — rather than wrapping every
// mux.HandleFunc — avoids churn across a 50-route router for a purely
// auxiliary signal. The tier_coverage test already guarantees every mutating
// handler in HandlerTier has a classification; the audit Attribution column
// picks up that classification here via the matched route pattern.
//
// When a new route is added: if its handler stamps Tier/AttributionMode via
// GitProviderForTier, no entry here is needed. Otherwise add the pattern below
// so the audit log renders the right icon.

import "github.com/MoranWeissman/sharko/internal/audit"

// mutatingPatternTier is the table of (mux pattern → handler tier) used only
// as a fallback when the handler didn't enrich audit fields with Tier.
//
// Only MUTATING patterns matter — GET/HEAD/OPTIONS are never audited.
var mutatingPatternTier = map[string]audit.Tier{
	// Connections — Tier 1 (operational).
	"POST /api/v1/connections/":                    audit.Tier1,
	"PUT /api/v1/connections/{name}":               audit.Tier1,
	"DELETE /api/v1/connections/{name}":            audit.Tier1,
	"POST /api/v1/connections/active":              audit.Tier1,
	"POST /api/v1/connections/test":                audit.Tier1,
	"POST /api/v1/connections/test-credentials":    audit.Tier1,

	// Clusters — Tier 1.
	"POST /api/v1/clusters":                          audit.Tier1,
	"POST /api/v1/clusters/batch":                    audit.Tier1,
	"POST /api/v1/clusters/adopt":                    audit.Tier1,
	"POST /api/v1/clusters/discover":                 audit.Tier1,
	"DELETE /api/v1/clusters/{name}":                 audit.Tier1,
	"PATCH /api/v1/clusters/{name}":                  audit.Tier1,
	"POST /api/v1/clusters/{name}/refresh":           audit.Tier1,
	"POST /api/v1/clusters/{name}/test":              audit.Tier1,
	"POST /api/v1/clusters/{name}/diagnose":          audit.Tier1,
	"POST /api/v1/clusters/{name}/unadopt":           audit.Tier1,
	"POST /api/v1/clusters/{name}/addons/{addon}":    audit.Tier1,
	"DELETE /api/v1/clusters/{name}/addons/{addon}":  audit.Tier1,
	"POST /api/v1/clusters/{name}/secrets/refresh":   audit.Tier1,

	// Init — Tier 1.
	"POST /api/v1/init": audit.Tier1,

	// Operations — TierPersonal (system noise / UI state).
	"POST /api/v1/operations/{id}/heartbeat": audit.TierPersonal,
	"POST /api/v1/operations/{id}/cancel":    audit.Tier1,

	// Addons catalog — Tier 2 for catalog-editing, Tier 1 for ops.
	"POST /api/v1/addons":                  audit.Tier2,
	"DELETE /api/v1/addons/{name}":         audit.Tier2,
	"PATCH /api/v1/addons/{name}":          audit.Tier2,
	"POST /api/v1/addons/upgrade-batch":    audit.Tier1,
	"POST /api/v1/addons/{name}/upgrade":   audit.Tier1,

	// V123-1.6 — force-refresh third-party catalog sources. Tier 2
	// because it's an admin configuration-time action (verifying a
	// newly added SHARKO_CATALOG_URLS entry).
	"POST /api/v1/catalog/sources/refresh": audit.Tier2,

	// Values editor — Tier 2.
	"PUT /api/v1/addons/{name}/values":                    audit.Tier2,
	"PUT /api/v1/clusters/{cluster}/addons/{name}/values": audit.Tier2,

	// Catalog editor (v1.20.1) — Tier 2.
	"PUT /api/v1/addons/{name}/catalog": audit.Tier2,

	// Pull upstream defaults (v1.20.1) — Tier 2.
	"POST /api/v1/addons/{name}/values/pull-upstream": audit.Tier2,

	// Preview merge (v1.21 QA Bundle 4) — read-only, classified Personal.
	"POST /api/v1/addons/{name}/values/preview-merge": audit.TierPersonal,

	// Addon secrets — Tier 2.
	"POST /api/v1/addon-secrets":             audit.Tier2,
	"DELETE /api/v1/addon-secrets/{addon}":   audit.Tier2,

	// Secrets reconciler — Tier 1.
	"POST /api/v1/secrets/reconcile": audit.Tier1,

	// Providers tests — Tier 1.
	"POST /api/v1/providers/test":        audit.Tier1,
	"POST /api/v1/providers/test-config": audit.Tier1,

	// Embedded dashboards — Tier 1.
	"POST /api/v1/embedded-dashboards": audit.Tier1,

	// Upgrade analysis — TierPersonal (read-like).
	"POST /api/v1/upgrade/check":      audit.TierPersonal,
	"POST /api/v1/upgrade/ai-summary": audit.TierPersonal,

	// AI config — Tier 1 (operational platform setting).
	"POST /api/v1/ai/config":      audit.Tier1,
	"POST /api/v1/ai/provider":    audit.Tier1,
	"POST /api/v1/ai/test":        audit.Tier1,
	"POST /api/v1/ai/test-config": audit.TierPersonal,

	// Agent — TierPersonal.
	"POST /api/v1/agent/chat":  audit.TierPersonal,
	"POST /api/v1/agent/reset": audit.TierPersonal,

	// Notifications — TierPersonal.
	"POST /api/v1/notifications/read-all": audit.TierPersonal,

	// Users management — Tier 1.
	"POST /api/v1/users":                              audit.Tier1,
	"PUT /api/v1/users/{username}":                    audit.Tier1,
	"DELETE /api/v1/users/{username}":                 audit.Tier1,
	"POST /api/v1/users/{username}/reset-password":    audit.Tier1,

	// Tokens — Tier 1.
	"POST /api/v1/tokens":           audit.Tier1,
	"DELETE /api/v1/tokens/{token}": audit.Tier1,

	// My account — personal.
	"POST /api/v1/auth/update-password":       audit.TierPersonal,
	"PUT /api/v1/users/me/github-token":       audit.TierPersonal,
	"DELETE /api/v1/users/me/github-token":    audit.TierPersonal,
	"POST /api/v1/users/me/github-token/test": audit.TierPersonal,

	// PRs — Tier 1.
	"POST /api/v1/prs/{id}/refresh": audit.Tier1,
	"DELETE /api/v1/prs/{id}":       audit.Tier1,

	// Recent-PRs (v1.20.1) — Tier 1 (read-ish POSTs aren't used; these are GETs).
}

// init seeds the auditMiddleware lookup table from the declarative map above.
// Running at package init keeps the wiring self-contained: adding a new entry
// to mutatingPatternTier is the only code change needed on the audit side.
func init() {
	for pattern, tier := range mutatingPatternTier {
		rememberTierForPattern(pattern, tier)
	}
}
