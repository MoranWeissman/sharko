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
	"connection.delete":             RoleAdmin,
	"connection.enable-auto-merge":  RoleAdmin,
	"cluster.remove":                RoleAdmin,
	"cluster.unadopt":               RoleAdmin,
	"addon.remove-from-catalog":     RoleAdmin,
	"user.delete":                   RoleAdmin,
	"user.change-role":              RoleAdmin,
	"token.revoke-other":            RoleAdmin,
	"audit.clear":                   RoleAdmin,
	"ai.config":                     RoleAdmin,
	"ai.provider":                   RoleAdmin,
	"dashboard.save":                RoleAdmin,
	"argocd.resource-exclusions":    RoleAdmin,
	"addon-secret.create":           RoleAdmin,
	"addon-secret.delete":           RoleAdmin,
	"pr.delete":                     RoleAdmin,

	// Operator+ actions
	"addon.disable":                 RoleOperator,
	"cluster.register":              RoleOperator,
	"cluster.adopt":                 RoleOperator,
	"cluster.update-addons":         RoleOperator,
	"cluster.test":                  RoleOperator,
	"cluster.diagnose":              RoleOperator,
	"cluster.discover":              RoleOperator,
	"cluster.refresh-credentials":   RoleOperator,
	"cluster.secrets.list":          RoleOperator,
	"cluster.secrets.refresh":       RoleOperator,
	"connection.create":             RoleOperator,
	"connection.update":             RoleOperator,
	"connection.set-active":         RoleOperator,
	"connection.disable-auto-merge": RoleOperator,
	"addon.add-to-catalog":          RoleOperator,
	"addon.update-catalog":          RoleOperator,
	"reconciler.trigger":            RoleOperator,
	"token.create":                  RoleOperator,
	"token.revoke-own":              RoleOperator,
	"init":                          RoleOperator,

	// Viewer+ actions
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
	required, ok := ActionRequirements[action]
	if !ok {
		required = RoleAdmin // fail-closed
	}
	return userRole.AtLeast(required)
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
