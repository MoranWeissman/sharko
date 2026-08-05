package authz

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// Role represents a user's authorization level.
type Role int

const (
	RoleViewer   Role = 0
	RoleOperator Role = 1
	RoleAdmin    Role = 2
)

// RoleFromString parses a role string. Unknown values default to RoleViewer.
func RoleFromString(s string) Role {
	switch s {
	case "admin":
		return RoleAdmin
	case "operator":
		return RoleOperator
	default:
		return RoleViewer
	}
}

// AtLeast returns true if the role meets or exceeds the required level.
func (r Role) AtLeast(required Role) bool {
	return r >= required
}

// String returns the human-readable name of the role.
func (r Role) String() string {
	switch r {
	case RoleAdmin:
		return "admin"
	case RoleOperator:
		return "operator"
	default:
		return "viewer"
	}
}

// ActionRequirements maps each action to the minimum role required.
// Actions not in the map are treated as admin-only (fail-closed).
var ActionRequirements = map[string]Role{
	// Admin-only actions
	"connection.delete":            RoleAdmin,
	"connection.enable-auto-merge": RoleAdmin,
	"cluster.remove":               RoleAdmin,
	"cluster.unadopt":              RoleAdmin,
	"addon.remove-from-catalog":    RoleAdmin,
	"user.create":                  RoleAdmin,
	"user.delete":                  RoleAdmin,
	"user.change-role":             RoleAdmin,
	"token.revoke-other":           RoleAdmin,
	// Renewing somebody ELSE's token. The own/other split mirrors revoke:
	// pushing out the expiry of a token you do not own keeps a credential
	// alive that only its owner should be able to keep alive.
	"token.renew-other":                  RoleAdmin,
	"audit.clear":                        RoleAdmin,
	"ai.config":                          RoleAdmin,
	"ai.provider":                        RoleAdmin,
	"dashboard.save":                     RoleAdmin,
	"argocd.resource-exclusions":         RoleAdmin,
	"addon-secret.create":                RoleAdmin,
	"addon-secret.delete":                RoleAdmin,
	"pr.delete":                          RoleAdmin,
	"catalog.sources.refresh":            RoleAdmin,
	"settings.probe-mode":                RoleAdmin,
	"settings.allow-inline-credentials":  RoleAdmin,
	"settings.managed-cluster-self-heal": RoleAdmin,

	// v3 -> v4 migration (v4 Wave 2, Story 5.2). One call rewrites every
	// data file in the connected repo, so both the preview (which renders
	// the content of every file, values included) and the migration itself
	// are admin-only. The read-only status probe below is Viewer+.
	"migration.preview": RoleAdmin,
	"migration.migrate": RoleAdmin,

	// Brownfield takeover (v4 Wave 2, Epic 6). Both of these change who
	// owns a live ArgoCD cluster connection on a fleet that is already
	// running, so they sit with the other destructive-adjacent actions.
	// The read-only preflight below is Operator+.
	"cluster.takeover":             RoleAdmin,
	"cluster.takeover.drop-labels": RoleAdmin,

	// Operator+ actions
	"addon.enable":                RoleOperator,
	"addon.disable":               RoleOperator,
	"addon.restart-sync":          RoleOperator,
	"cluster.register":            RoleOperator,
	"cluster.adopt":               RoleOperator,
	"cluster.update-addons":       RoleOperator,
	"cluster.test":                RoleOperator,
	"cluster.diagnose":            RoleOperator,
	"cluster.doctor":              RoleOperator,
	"cluster.discover":            RoleOperator,
	"cluster.refresh-credentials": RoleOperator,
	"cluster.reconcile":           RoleOperator,
	// The takeover checks and the unregister consequences read state and
	// change nothing at all — same tier as the doctor and diagnose reads
	// they sit next to. Running one is how an operator finds out whether
	// the destructive step above is safe, so gating it at admin would
	// mean only admins could ever look before leaping.
	"cluster.takeover.preflight":      RoleOperator,
	"cluster.unregister.consequences": RoleOperator,
	"cluster.resync":                  RoleOperator,
	"cluster.secrets.list":            RoleOperator,
	"cluster.secrets.refresh":         RoleOperator,
	"connection.create":               RoleOperator,
	"connection.update":               RoleOperator,
	"connection.set-active":           RoleOperator,
	"connection.disable-auto-merge":   RoleOperator,
	"addon.add-to-catalog":            RoleOperator,
	"addon.update-catalog":            RoleOperator,
	// v4 Wave 2 Epic 7 Story 7.2 — subset upgrade: bumps the version pin
	// on a chosen set of clusters in one PR. Same tier as the existing
	// global/per-cluster addon.update-catalog upgrade write.
	"addon.upgrade-clusters":    RoleOperator,
	"default-addons.update":     RoleOperator,
	"engine.pin-upgrade":        RoleOperator,
	"catalog.add":               RoleOperator,
	"reconciler.trigger":        RoleOperator,
	"catalog.freshness.refresh": RoleOperator,
	// Single-item addon-values-secret row actions (S4 — Managed Secrets
	// page). Same tier as reconciler.trigger and cluster.reconcile: both
	// reach a live remote cluster with real credentials, just scoped to one
	// secret instead of a fleet-wide pass.
	"addon-secret.refresh": RoleOperator,
	"addon-secret.sync":    RoleOperator,
	// Reading the LIVE Secret object behind one Managed Secrets row, with
	// every value blanked server-side before the response is built (see
	// internal/api/secret_resource.go). Operator, not viewer, on purpose:
	// this is the same class of read as cluster.secrets.list above — it
	// reaches a live cluster with real credentials and returns that
	// Secret's own metadata and data KEY NAMES. The per-cluster comparison
	// read (GET /clusters/{name}/comparison) is open to any authenticated
	// caller, but it only ever exposes addon labels derived from git, never
	// a live Secret, so it is the wrong precedent to copy here.
	"secret.resource.read": RoleOperator,
	"token.create":              RoleOperator,
	"token.renew-own":           RoleOperator,
	"token.revoke-own":          RoleOperator,
	"init":                      RoleOperator,

	// Connectivity tests (v4-wave2 review B1). These endpoints reach out to
	// a Git host, an ArgoCD server or a secret store with real credentials —
	// including, on the saved-connection path, credentials the caller never
	// typed. They persist nothing, but "sends a stored secret somewhere" is
	// not a read, so they sit with the other operator actions rather than
	// being open to every authenticated viewer.
	"connection.test": RoleOperator,
	"provider.test":   RoleOperator,

	// Viewer+ actions
	// Self-service on the caller's own profile — any authenticated user.
	"user.me":             RoleViewer,
	"user.me.set-token":   RoleViewer,
	"user.me.clear-token": RoleViewer,

	"cluster.list":            RoleViewer,
	"cluster.detail":          RoleViewer,
	"cluster.list-discovered": RoleViewer,
	"addon.list":              RoleViewer,
	"addon.detail":            RoleViewer,
	"connection.list":         RoleViewer,
	"connection.detail":       RoleViewer,
	"pr.list":                 RoleViewer,
	"pr.detail":               RoleViewer,
	"pr.refresh":              RoleViewer,
	"user.list":               RoleViewer,
	"user.detail":             RoleViewer,
	"token.list":              RoleViewer,
	"audit.list":              RoleViewer,
	"audit.stream":            RoleViewer,
	"metrics.read":            RoleViewer,
	"addon-secret.list":       RoleViewer,
	"engine.pin-check":        RoleViewer,
	"catalog.freshness.read":  RoleViewer,

	// Read-only first-run repo-state probe (GET /api/v1/init/status). The
	// matching write action "init" is Operator+; the probe is read-only so
	// any authenticated viewer (and the unauthenticated first-run flow)
	// can check repo state before the wizard offers to initialize.
	"init.status": RoleViewer,

	// Read-only repo-format probe (GET /api/v1/migration/status). Same
	// stance as init.status: seeing WHETHER a migration is available is a
	// read; running one is admin (see migration.migrate above).
	"migration.status": RoleViewer,
}

// RoleAllows reports whether the given role is sufficient for the action.
// This is the underlying role check used by both the HTTP request path
// (Require / RequireWithResponse) and non-HTTP callers such as the AI agent's
// write-tool gate, which must enforce the SAME decision as the equivalent
// REST endpoint. Actions not in the requirements table are admin-only
// (fail-closed), matching Require's behavior.
func RoleAllows(role Role, action string) bool {
	required, ok := ActionRequirements[action]
	if !ok {
		required = RoleAdmin // fail-closed
	}
	return role.AtLeast(required)
}

// Require checks whether the request has a role sufficient for the given action.
// It returns true if allowed, false if denied.
func Require(r *http.Request, action string) bool {
	roleStr := r.Header.Get("X-Sharko-Role")
	if roleStr == "" {
		// If no auth headers at all, auth is not configured — allow through.
		if r.Header.Get("X-Sharko-User") == "" {
			return true
		}
		// Authenticated but no role header — treat as minimum.
		roleStr = "viewer"
	}

	userRole := RoleFromString(roleStr)
	return RoleAllows(userRole, action)
}

// RequireWithResponse checks authorization and writes a 403 JSON error if denied.
// Returns true if the request is allowed to proceed, false if a 403 was written.
func RequireWithResponse(w http.ResponseWriter, r *http.Request, action string) bool {
	if Require(r, action) {
		return true
	}

	roleStr := r.Header.Get("X-Sharko-Role")
	if roleStr == "" {
		roleStr = "viewer"
	}
	userRole := RoleFromString(roleStr)

	required, ok := ActionRequirements[action]
	if !ok {
		required = RoleAdmin
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error": fmt.Sprintf("action '%s' requires role '%s', you have '%s'", action, required, userRole),
	})
	return false
}
