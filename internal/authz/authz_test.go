package authz

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRoleFromString(t *testing.T) {
	tests := []struct {
		input string
		want  Role
	}{
		{"admin", RoleAdmin},
		{"operator", RoleOperator},
		{"viewer", RoleViewer},
		{"", RoleViewer},
		{"unknown", RoleViewer},
		{"ADMIN", RoleViewer}, // case-sensitive
	}
	for _, tt := range tests {
		got := RoleFromString(tt.input)
		if got != tt.want {
			t.Errorf("RoleFromString(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestRoleString(t *testing.T) {
	tests := []struct {
		role Role
		want string
	}{
		{RoleAdmin, "admin"},
		{RoleOperator, "operator"},
		{RoleViewer, "viewer"},
		{Role(-1), "viewer"}, // unknown numeric
	}
	for _, tt := range tests {
		got := tt.role.String()
		if got != tt.want {
			t.Errorf("Role(%d).String() = %q, want %q", tt.role, got, tt.want)
		}
	}
}

func TestAtLeast(t *testing.T) {
	tests := []struct {
		role     Role
		required Role
		want     bool
	}{
		{RoleAdmin, RoleAdmin, true},
		{RoleAdmin, RoleOperator, true},
		{RoleAdmin, RoleViewer, true},
		{RoleOperator, RoleAdmin, false},
		{RoleOperator, RoleOperator, true},
		{RoleOperator, RoleViewer, true},
		{RoleViewer, RoleAdmin, false},
		{RoleViewer, RoleOperator, false},
		{RoleViewer, RoleViewer, true},
	}
	for _, tt := range tests {
		got := tt.role.AtLeast(tt.required)
		if got != tt.want {
			t.Errorf("%v.AtLeast(%v) = %v, want %v", tt.role, tt.required, got, tt.want)
		}
	}
}

func makeRequest(user, role string) *http.Request {
	r := httptest.NewRequest("GET", "/test", nil)
	if user != "" {
		r.Header.Set("X-Sharko-User", user)
	}
	if role != "" {
		r.Header.Set("X-Sharko-Role", role)
	}
	return r
}

func TestRequire_AdminAction(t *testing.T) {
	action := "cluster.remove" // admin-only

	if !Require(makeRequest("admin-user", "admin"), action) {
		t.Error("admin should be allowed for admin action")
	}
	if Require(makeRequest("op-user", "operator"), action) {
		t.Error("operator should be denied for admin action")
	}
	if Require(makeRequest("view-user", "viewer"), action) {
		t.Error("viewer should be denied for admin action")
	}
}

func TestRequire_OperatorAction(t *testing.T) {
	action := "cluster.register" // operator+

	if !Require(makeRequest("admin-user", "admin"), action) {
		t.Error("admin should be allowed for operator action")
	}
	if !Require(makeRequest("op-user", "operator"), action) {
		t.Error("operator should be allowed for operator action")
	}
	if Require(makeRequest("view-user", "viewer"), action) {
		t.Error("viewer should be denied for operator action")
	}
}

func TestRequire_ViewerAction(t *testing.T) {
	action := "cluster.list" // viewer+

	if !Require(makeRequest("admin-user", "admin"), action) {
		t.Error("admin should be allowed for viewer action")
	}
	if !Require(makeRequest("op-user", "operator"), action) {
		t.Error("operator should be allowed for viewer action")
	}
	if !Require(makeRequest("view-user", "viewer"), action) {
		t.Error("viewer should be allowed for viewer action")
	}
}

func TestRequire_FailClosed(t *testing.T) {
	// Unknown action should require admin (fail-closed).
	action := "nonexistent.action"

	if !Require(makeRequest("admin-user", "admin"), action) {
		t.Error("admin should be allowed for unknown action (fail-closed = admin)")
	}
	if Require(makeRequest("op-user", "operator"), action) {
		t.Error("operator should be denied for unknown action (fail-closed)")
	}
	if Require(makeRequest("view-user", "viewer"), action) {
		t.Error("viewer should be denied for unknown action (fail-closed)")
	}
}

func TestRequire_NoAuthHeaders(t *testing.T) {
	// No X-Sharko-User and no X-Sharko-Role means auth is not configured.
	// All actions should be allowed (backward compat).
	r := makeRequest("", "")
	if !Require(r, "cluster.remove") {
		t.Error("no-auth mode should allow admin actions")
	}
	if !Require(r, "cluster.register") {
		t.Error("no-auth mode should allow operator actions")
	}
}

func TestRequire_AuthenticatedButNoRole(t *testing.T) {
	// X-Sharko-User present but no X-Sharko-Role -> treated as viewer.
	r := makeRequest("some-user", "")
	if Require(r, "cluster.remove") {
		t.Error("authenticated user with no role should be denied admin actions")
	}
	if !Require(r, "cluster.list") {
		t.Error("authenticated user with no role should be allowed viewer actions")
	}
}

func TestRequireWithResponse_WritesJSON403(t *testing.T) {
	w := httptest.NewRecorder()
	r := makeRequest("user", "viewer")

	allowed := RequireWithResponse(w, r, "cluster.remove")
	if allowed {
		t.Fatal("expected denied, got allowed")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

func TestRequireWithResponse_Allows(t *testing.T) {
	w := httptest.NewRecorder()
	r := makeRequest("admin-user", "admin")

	allowed := RequireWithResponse(w, r, "cluster.remove")
	if !allowed {
		t.Fatal("expected allowed, got denied")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (no write)", w.Code)
	}
}

func TestAllActionsPopulated(t *testing.T) {
	if len(ActionRequirements) == 0 {
		t.Fatal("ActionRequirements is empty")
	}

	// Verify we have at least one action for each role level.
	hasAdmin, hasOperator, hasViewer := false, false, false
	for _, role := range ActionRequirements {
		switch role {
		case RoleAdmin:
			hasAdmin = true
		case RoleOperator:
			hasOperator = true
		case RoleViewer:
			hasViewer = true
		}
	}
	if !hasAdmin {
		t.Error("no admin-only actions in ActionRequirements")
	}
	if !hasOperator {
		t.Error("no operator+ actions in ActionRequirements")
	}
	if !hasViewer {
		t.Error("no viewer+ actions in ActionRequirements")
	}
}
