//go:build e2e

package harness

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/models"
)

// Typed wrappers for the dashboard / observability / reads sweep
// (V2 Epic 7-1.12).
//
// These methods extend the base Client surface with the read-heavy
// endpoints exercised by tests/e2e/lifecycle/dashboard_test.go. They
// import the in-tree request/response types where shapes are exported
// (`internal/models`, `internal/audit`) so the harness breaks at
// compile time when those shapes evolve. Endpoints that build their
// JSON inline with `map[string]interface{}` use small purpose-built
// structs in this file so callers stay typed.
//
// Concurrency: same rules as Client — one wrapper per goroutine.

// ---------------------------------------------------------------------------
// dashboard
// ---------------------------------------------------------------------------

// DashboardAttentionItem mirrors the inline shape returned by
// GET /api/v1/dashboard/attention. The handler defines the struct
// inside its function body, so the harness redeclares it here with
// the same JSON tags.
type DashboardAttentionItem struct {
	AppName   string `json:"app_name"`
	AddonName string `json:"addon_name"`
	Cluster   string `json:"cluster"`
	Health    string `json:"health"`
	Sync      string `json:"sync"`
	Error     string `json:"error,omitempty"`
	ErrorType string `json:"error_type,omitempty"`
}

// DashboardAttention fetches GET /api/v1/dashboard/attention.
//
// In the in-process boot path with no active ArgoCD connection the
// handler returns 503 from the connection lookup; callers exercising
// that path should use Do/WithExpectStatus instead of this wrapper.
func (c *Client) DashboardAttention(t *testing.T) []DashboardAttentionItem {
	t.Helper()
	var out []DashboardAttentionItem
	c.GetJSON(t, "/api/v1/dashboard/attention", &out)
	return out
}

// DashboardPullRequests fetches GET /api/v1/dashboard/pull-requests.
//
// Only requires the active git provider — works against a MockGitProvider
// in the in-process boot path with no further wiring.
func (c *Client) DashboardPullRequests(t *testing.T) *models.DashboardPullRequestsResponse {
	t.Helper()
	var out models.DashboardPullRequestsResponse
	c.GetJSON(t, "/api/v1/dashboard/pull-requests", &out)
	return &out
}

// DashboardStats fetches GET /api/v1/dashboard/stats.
//
// Requires both git and argocd; in-process tests typically expect 503.
func (c *Client) DashboardStats(t *testing.T) map[string]interface{} {
	t.Helper()
	out := map[string]interface{}{}
	c.GetJSON(t, "/api/v1/dashboard/stats", &out)
	return out
}

// ---------------------------------------------------------------------------
// observability
// ---------------------------------------------------------------------------

// ObservabilityOverview fetches GET /api/v1/observability/overview.
//
// Requires argocd; degrades to 503 when no active argocd connection.
func (c *Client) ObservabilityOverview(t *testing.T) map[string]interface{} {
	t.Helper()
	out := map[string]interface{}{}
	c.GetJSON(t, "/api/v1/observability/overview", &out)
	return out
}

// ---------------------------------------------------------------------------
// fleet / repo
// ---------------------------------------------------------------------------

// FleetStatusResponse mirrors the struct returned by GET /api/v1/fleet/status.
// The handler keeps its struct unexported, so we redeclare it here with
// the same JSON tags. Field order matches the source for diffability.
type FleetStatusResponse struct {
	ServerVersion        string                `json:"server_version"`
	Uptime               string                `json:"uptime"`
	GitUnavailable       bool                  `json:"git_unavailable,omitempty"`
	ArgoUnavailable      bool                  `json:"argo_unavailable,omitempty"`
	TotalClusters        int                   `json:"total_clusters"`
	HealthyClusters      int                   `json:"healthy_clusters"`
	DegradedClusters     int                   `json:"degraded_clusters"`
	DisconnectedClusters int                   `json:"disconnected_clusters"`
	TotalAddons          int                   `json:"total_addons"`
	TotalDeployments     int                   `json:"total_deployments"`
	HealthyDeployments   int                   `json:"healthy_deployments"`
	DegradedDeployments  int                   `json:"degraded_deployments"`
	OutOfSyncDeployments int                   `json:"out_of_sync_deployments"`
	AddonDataUnavailable bool                  `json:"addon_data_unavailable,omitempty"`
	Clusters             []FleetClusterSummary `json:"clusters"`
}

// FleetClusterSummary mirrors fleetClusterSummary inside fleet.go.
type FleetClusterSummary struct {
	Name             string `json:"name"`
	ConnectionStatus string `json:"connection_status"`
	TotalAddons      int    `json:"total_addons"`
	HealthyAddons    int    `json:"healthy_addons"`
	DegradedAddons   int    `json:"degraded_addons"`
}

// FleetStatus fetches GET /api/v1/fleet/status. The handler is
// resilient: missing git or argocd is reported as a flag, never an
// error, so this wrapper always expects 200.
func (c *Client) FleetStatus(t *testing.T) *FleetStatusResponse {
	t.Helper()
	var out FleetStatusResponse
	c.GetJSON(t, "/api/v1/fleet/status", &out)
	return &out
}

// RepoStatusResponseDTO mirrors api.RepoStatusResponse so callers have
// a typed view without importing the api package.
type RepoStatusResponseDTO struct {
	Initialized     bool   `json:"initialized"`
	BootstrapSynced bool   `json:"bootstrap_synced"`
	Reason          string `json:"reason,omitempty"`
}

// RepoStatus fetches GET /api/v1/repo/status. Always returns 200 (the
// handler swallows lookup errors and reports them via Reason).
func (c *Client) RepoStatus(t *testing.T) *RepoStatusResponseDTO {
	t.Helper()
	var out RepoStatusResponseDTO
	c.GetJSON(t, "/api/v1/repo/status", &out)
	return &out
}

// ---------------------------------------------------------------------------
// argocd config
// ---------------------------------------------------------------------------

// ArgocdResourceExclusionsResponse mirrors the inline body returned by
// GET /api/v1/argocd/resource-exclusions.
type ArgocdResourceExclusionsResponse struct {
	Configured     bool   `json:"configured"`
	Detail         string `json:"detail"`
	Recommendation string `json:"recommendation,omitempty"`
}

// ArgocdResourceExclusions fetches GET /api/v1/argocd/resource-exclusions.
// The handler's K8s probe fails outside of a real cluster, but it returns
// 200 with configured=false + a detail explaining why.
func (c *Client) ArgocdResourceExclusions(t *testing.T) *ArgocdResourceExclusionsResponse {
	t.Helper()
	var out ArgocdResourceExclusionsResponse
	c.GetJSON(t, "/api/v1/argocd/resource-exclusions", &out)
	return &out
}

// ---------------------------------------------------------------------------
// audit log + stream
// ---------------------------------------------------------------------------

// AuditLogResponse mirrors the inline shape from GET /api/v1/audit.
type AuditLogResponse struct {
	Entries []audit.Entry `json:"entries"`
	Count   int           `json:"count"`
}

// AuditLog fetches GET /api/v1/audit. Optional query params (user,
// action, limit, etc.) can be appended to path by the caller.
func (c *Client) AuditLog(t *testing.T, query string) *AuditLogResponse {
	t.Helper()
	path := "/api/v1/audit"
	if query != "" {
		if !strings.HasPrefix(query, "?") {
			path += "?" + query
		} else {
			path += query
		}
	}
	var out AuditLogResponse
	c.GetJSON(t, path, &out)
	return &out
}

// AuditStream opens GET /api/v1/audit/stream as Server-Sent Events,
// reads up to maxEvents entries (or until ctx cancels), and returns
// the parsed entries. Closes the response body before returning.
//
// The middleware skips audit on GET, so the stream test must trigger
// a write from another goroutine to observe an event.
func (c *Client) AuditStream(t *testing.T, ctx context.Context, maxEvents int) []audit.Entry {
	t.Helper()
	if maxEvents <= 0 {
		maxEvents = 1
	}
	url := c.BaseURL + "/api/v1/audit/stream"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("AuditStream: build request: %v", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "text/event-stream")

	// Use a fresh client without the default short timeout so the SSE
	// stream isn't killed at the round-trip deadline. The supplied ctx
	// is the only stop signal.
	httpClient := &http.Client{}
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("AuditStream: http: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("AuditStream: status=%d body=%s", resp.StatusCode, raw)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("AuditStream: unexpected Content-Type %q", ct)
	}

	var entries []audit.Entry
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		var entry audit.Entry
		if err := json.Unmarshal([]byte(payload), &entry); err != nil {
			t.Fatalf("AuditStream: decode entry %q: %v", payload, err)
		}
		entries = append(entries, entry)
		if len(entries) >= maxEvents {
			return entries
		}
		if ctx.Err() != nil {
			return entries
		}
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		t.Fatalf("AuditStream: scan: %v", err)
	}
	return entries
}

// ---------------------------------------------------------------------------
// docs
// ---------------------------------------------------------------------------

// DocEntry mirrors the docEntry struct in docs.go.
type DocEntry struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
	Order int    `json:"order"`
}

// DocsList fetches GET /api/v1/docs/list.
func (c *Client) DocsList(t *testing.T) []DocEntry {
	t.Helper()
	var out []DocEntry
	c.GetJSON(t, "/api/v1/docs/list", &out)
	return out
}

// DocContent mirrors the inline body returned by GET /api/v1/docs/{slug}.
type DocContent struct {
	Slug    string `json:"slug"`
	Content string `json:"content"`
}

// DocsGet fetches GET /api/v1/docs/{slug}.
func (c *Client) DocsGet(t *testing.T, slug string) *DocContent {
	t.Helper()
	var out DocContent
	c.GetJSON(t, "/api/v1/docs/"+slug, &out)
	return &out
}

// ---------------------------------------------------------------------------
// embedded dashboards
// ---------------------------------------------------------------------------

// EmbeddedDashboard mirrors the embeddedDashboard struct in dashboards.go.
type EmbeddedDashboard struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	Provider string `json:"provider"`
}

// EmbeddedDashboardsList fetches GET /api/v1/embedded-dashboards.
//
// Outside a Kubernetes pod the loader can't read the ConfigMap and
// returns an empty slice rather than an error.
func (c *Client) EmbeddedDashboardsList(t *testing.T) []EmbeddedDashboard {
	t.Helper()
	var out []EmbeddedDashboard
	c.GetJSON(t, "/api/v1/embedded-dashboards", &out)
	return out
}

// ---------------------------------------------------------------------------
// secrets
// ---------------------------------------------------------------------------

// SecretsStatusResponse is a permissive container — the secrets reconciler's
// stats type is not exported through the API, and tests on the in-process
// boot path expect a 503 (no reconciler wired) rather than a populated body.
type SecretsStatusResponse map[string]interface{}

// ---------------------------------------------------------------------------
// config
// ---------------------------------------------------------------------------

// ServerConfigResponse mirrors the inline shape returned by
// GET /api/v1/config. The handler builds the body as a nested
// map[string]interface{}; this struct picks out the fields the
// dashboard sweep asserts on.
type ServerConfigResponse struct {
	RepoPaths struct {
		ClusterValues string `json:"cluster_values"`
		GlobalValues  string `json:"global_values"`
		Charts        string `json:"charts"`
		Bootstrap     string `json:"bootstrap"`
	} `json:"repo_paths"`
	Gitops struct {
		PRAutoMerge  bool   `json:"pr_auto_merge"`
		BranchPrefix string `json:"branch_prefix"`
		CommitPrefix string `json:"commit_prefix"`
		BaseBranch   string `json:"base_branch"`
	} `json:"gitops"`
	Argocd struct {
		Connected bool   `json:"connected"`
		Version   string `json:"version,omitempty"`
	} `json:"argocd"`
	Provider map[string]string `json:"provider,omitempty"`
}

// Config fetches GET /api/v1/config.
func (c *Client) Config(t *testing.T) *ServerConfigResponse {
	t.Helper()
	var out ServerConfigResponse
	c.GetJSON(t, "/api/v1/config", &out)
	return &out
}

