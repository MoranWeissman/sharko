// Package audit — tier classification of mutating endpoints.
//
// Tiered attribution model (see docs/design/2026-04-16-attribution-and-permissions-model.md):
//
//   - Tier 1 (operational): cluster register/deregister/test/diagnose/refresh/unadopt,
//     addon enable/disable/upgrade on a cluster, ArgoCD cluster secret sync, Connection
//     CRUD, AI config, dashboards, PR refresh/delete, reconcile triggers.
//     → Service token is acceptable; commit gets a Co-authored-by trailer for the user.
//
//   - Tier 2 (configuration): edit global Helm values, edit per-cluster value
//     overrides, edit catalog metadata (sync wave, sync options, ignore differences,
//     additional sources). Anything that defines WHAT will be deployed.
//     → Per-user PAT preferred; falls back to service token with a UX nudge.
//
//   - Tier "personal": self-service actions on the caller's own resources (set/clear
//     personal PAT, change own password). No Git write happens.
//
//   - Tier "auth": authentication endpoints (login/logout/hash). No Git write happens.
//
//   - Tier "webhook": inbound webhooks. No user identity available.
package audit

// Tier is an attribution tier for a mutating endpoint.
type Tier string

const (
	Tier1        Tier = "tier1"        // operational
	Tier2        Tier = "tier2"        // configuration
	TierPersonal Tier = "personal"     // self-service on own profile (no git write)
	TierAuth     Tier = "auth"         // login/logout/hash (no git write)
	TierWebhook  Tier = "webhook"      // inbound webhook (no user identity)
)

// AttributionMode describes how a mutating action was attributed in the resulting
// Git commit. Set by the git-writing layer based on the resolved token + tier.
type AttributionMode string

const (
	// AttributionService — service token used; user appears nowhere on the commit.
	// Used when there is no user identity available (e.g. webhook, reconciler).
	AttributionService AttributionMode = "service"

	// AttributionPerUser — per-user PAT used; commit author IS the user.
	// Tier 2 with a per-user PAT configured.
	AttributionPerUser AttributionMode = "per_user"

	// AttributionCoAuthor — service token used, but commit message includes a
	// `Co-authored-by:` trailer for the user. Tier 1 default; Tier 2 fallback
	// when user has no per-user PAT.
	AttributionCoAuthor AttributionMode = "co_author"
)
