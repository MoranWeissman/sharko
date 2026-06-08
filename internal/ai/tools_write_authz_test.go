package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
)

// failingProvider implements gitprovider.GitProvider with every method erroring.
// It lets a read-tool test prove the authz gate let the call through (the call
// reaches the body and fails on the provider, not on authz).
type failingProvider struct{}

var errFakeProvider = fmt.Errorf("fake provider: not wired")

func (failingProvider) GetFileContent(context.Context, string, string) ([]byte, error) {
	return nil, errFakeProvider
}
func (failingProvider) ListDirectory(context.Context, string, string) ([]string, error) {
	return nil, errFakeProvider
}
func (failingProvider) ListPullRequests(context.Context, string) ([]gitprovider.PullRequest, error) {
	return nil, errFakeProvider
}
func (failingProvider) TestConnection(context.Context) error { return errFakeProvider }
func (failingProvider) CreateBranch(context.Context, string, string) error {
	return errFakeProvider
}
func (failingProvider) CreateOrUpdateFile(context.Context, string, []byte, string, string) error {
	return errFakeProvider
}
func (failingProvider) BatchCreateFiles(context.Context, map[string][]byte, string, string) error {
	return errFakeProvider
}
func (failingProvider) DeleteFile(context.Context, string, string, string) error {
	return errFakeProvider
}
func (failingProvider) CreatePullRequest(context.Context, string, string, string, string) (*gitprovider.PullRequest, error) {
	return nil, errFakeProvider
}
func (failingProvider) MergePullRequest(context.Context, int) error          { return errFakeProvider }
func (failingProvider) GetPullRequestStatus(context.Context, int) (string, error) {
	return "", errFakeProvider
}
func (failingProvider) DeleteBranch(context.Context, string) error { return errFakeProvider }

// V2-cleanup-21 (decision #6) — the AI agent's WRITE tools are gated on the
// caller's role. A write tool invoked on behalf of a user who lacks permission
// is refused with the SAME authz decision the equivalent direct REST endpoint
// uses; read-only tools stay open to any authenticated user.

// TestAuthorizeWriteTool_PerToolPerRole walks every write tool against every
// role and asserts the gate matches the action's requirement in the authz
// table (enable/disable → operator+, update_addon_version → operator+ via
// addon.update-catalog, sync/refresh → operator+ via reconciler.trigger).
func TestAuthorizeWriteTool_PerToolPerRole(t *testing.T) {
	cases := []struct {
		tool       string
		minAllowed authz.Role // lowest role that may invoke the tool
	}{
		{"enable_addon", authz.RoleOperator},
		{"disable_addon", authz.RoleOperator},
		{"update_addon_version", authz.RoleOperator},
		{"sync_argocd_app", authz.RoleOperator},
		{"refresh_argocd_app", authz.RoleOperator},
	}
	roles := []authz.Role{authz.RoleViewer, authz.RoleOperator, authz.RoleAdmin}

	for _, c := range cases {
		for _, role := range roles {
			err := authorizeWriteTool(role, c.tool)
			wantAllowed := role.AtLeast(c.minAllowed)
			if wantAllowed && err != nil {
				t.Errorf("%s as %s: got refusal %q, want allowed", c.tool, role, err)
			}
			if !wantAllowed && err == nil {
				t.Errorf("%s as %s: got allowed, want refusal", c.tool, role)
			}
		}
	}
}

// TestAuthorizeWriteTool_ReadToolsNeverGated confirms read tools pass the gate
// for any role, including a viewer — chat/read access stays open.
func TestAuthorizeWriteTool_ReadToolsNeverGated(t *testing.T) {
	readTools := []string{
		"list_clusters", "list_addons", "get_cluster_addons",
		"find_addon_deployments", "get_argocd_app_health", "web_search",
		"get_platform_info",
	}
	for _, tool := range readTools {
		for _, role := range []authz.Role{authz.RoleViewer, authz.RoleOperator, authz.RoleAdmin} {
			if err := authorizeWriteTool(role, tool); err != nil {
				t.Errorf("read tool %s as %s: got refusal %q, want allowed", tool, role, err)
			}
		}
	}
}

// TestExecuteTool_ViewerRefusedWriteTool drives the refusal through the real
// ExecuteTool entrypoint. The gate returns BEFORE the tool body, so no git
// provider is touched — a viewer's enable_addon never reaches CreatePullRequest.
func TestExecuteTool_ViewerRefusedWriteTool(t *testing.T) {
	// A bare executor is safe: the viewer gate short-circuits before any
	// provider access.
	e := &ToolExecutor{managedClustersPath: "configuration/managed-clusters.yaml"}
	args := json.RawMessage(`{"cluster_name":"prod","addon_name":"datadog"}`)

	result, err := e.ExecuteTool(context.Background(), "enable_addon", args, authz.RoleViewer)
	if err != nil {
		t.Fatalf("ExecuteTool returned hard error %v, want soft refusal result", err)
	}
	if !strings.Contains(result, "permission denied") {
		t.Errorf("viewer result = %q, want a permission-denied refusal", result)
	}
	if !strings.Contains(result, "enable_addon") {
		t.Errorf("refusal should name the tool; got %q", result)
	}
}

// TestExecuteTool_ReadToolPassesGateForViewer confirms a viewer can invoke a
// read tool through ExecuteTool — the gate lets it through to the body (which
// then fails on the absent provider, NOT on authz). The point under test is
// that the failure is NOT a permission denial.
func TestExecuteTool_ReadToolPassesGateForViewer(t *testing.T) {
	e := newExecutorWithFailingProvider()
	result, err := e.ExecuteTool(context.Background(), "list_clusters", json.RawMessage(`{}`), authz.RoleViewer)
	// list_clusters reaches its body and surfaces the provider error; the
	// load-bearing assertion is simply that authz did not block it.
	if err == nil && strings.Contains(result, "permission denied") {
		t.Errorf("read tool was blocked by authz for a viewer: %q", result)
	}
	if err != nil && strings.Contains(err.Error(), "permission denied") {
		t.Errorf("read tool was blocked by authz for a viewer: %v", err)
	}
}

// failingProvider is a GitProvider whose every method errors — enough to prove
// a read tool passed the authz gate (it reaches the body and fails on the
// provider, not on authz) without standing up a real Git backend.
func newExecutorWithFailingProvider() *ToolExecutor {
	return &ToolExecutor{
		gp:                  failingProvider{},
		managedClustersPath: "configuration/managed-clusters.yaml",
	}
}
