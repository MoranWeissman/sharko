//go:build e2e

package harness

import (
	"net/http"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/operations"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// This file extends Client with typed wrappers for the connections / init /
// operations API surfaces driven by V2 Epic 7-1.9.
//
// Lives in apiclient_init.go so the original apiclient.go (story 7-1.3) keeps
// its narrow scope and downstream story files own their own typed extensions.
// Every wrapper imports sharko's real request/response types directly — no
// codegen, no drift.

// ---------------------------------------------------------------------------
// Connections
// ---------------------------------------------------------------------------

// ListConnections fetches GET /api/v1/connections/ and returns the typed
// ConnectionsListResponse from internal/models.
func (c *Client) ListConnections(t *testing.T) *models.ConnectionsListResponse {
	t.Helper()
	var out models.ConnectionsListResponse
	c.GetJSON(t, "/api/v1/connections/", &out)
	return &out
}

// CreateConnectionAck mirrors the inline JSON shape returned by
// handleCreateConnection on success — `{"status":"created","name":"<name>"}`.
type CreateConnectionAck struct {
	Status string `json:"status"`
	Name   string `json:"name"`
}

// CreateConnection POSTs a CreateConnectionRequest to /api/v1/connections/.
// Asserts 201 Created (the handler's documented success code) and returns
// the typed ack.
func (c *Client) CreateConnection(t *testing.T, req models.CreateConnectionRequest) CreateConnectionAck {
	t.Helper()
	var out CreateConnectionAck
	c.PostJSON(t, "/api/v1/connections/", req, &out, WithExpectStatus(http.StatusCreated))
	return out
}

// UpdateConnection PUTs a CreateConnectionRequest to
// /api/v1/connections/{name}. Returns the inline ack
// (`{"status":"updated","name":"<name>"}`).
func (c *Client) UpdateConnection(t *testing.T, name string, req models.CreateConnectionRequest) CreateConnectionAck {
	t.Helper()
	var out CreateConnectionAck
	c.PutJSON(t, "/api/v1/connections/"+name, req, &out)
	return out
}

// DeleteConnection issues DELETE /api/v1/connections/{name}.
func (c *Client) DeleteConnection(t *testing.T, name string) {
	t.Helper()
	c.Delete(t, "/api/v1/connections/"+name)
}

// SetActiveConnection POSTs to /api/v1/connections/active.
func (c *Client) SetActiveConnection(t *testing.T, name string) {
	t.Helper()
	c.PostJSON(t, "/api/v1/connections/active",
		models.SetActiveConnectionRequest{ConnectionName: name}, nil)
}

// CredentialTestResult captures the per-service status returned by the
// /connections/test and /connections/test-credentials endpoints. Both
// handlers return the same shape — a top-level map with "git" and "argocd"
// keys, each holding a {status, message?, auth?} object.
//
// We model it as a flat struct (rather than nested maps) so test assertions
// read cleanly: `got.Git.Status == "ok"`. JSON decoder happily ignores any
// extra fields, so additions to the handler payload don't break old tests.
type CredentialTestResult struct {
	Git    CredentialServiceResult `json:"git"`
	Argocd CredentialServiceResult `json:"argocd"`
}

// CredentialServiceResult mirrors the per-service object shape returned by
// the handlers under each service key.
type CredentialServiceResult struct {
	Status  string `json:"status"`            // "ok" | "error"
	Message string `json:"message,omitempty"` // present when status=="error"
	Auth    string `json:"auth,omitempty"`    // e.g. "provided", "env:GITHUB_TOKEN"
}

// TestCredentials POSTs a CreateConnectionRequest to
// /api/v1/connections/test-credentials and returns the typed result.
//
// The handler always responds 200 — even when the credentials don't
// authenticate (the error is signalled in the per-service result body).
func (c *Client) TestCredentials(t *testing.T, req models.CreateConnectionRequest) CredentialTestResult {
	t.Helper()
	var out CredentialTestResult
	c.PostJSON(t, "/api/v1/connections/test-credentials", req, &out)
	return out
}

// TestActiveConnection POSTs to /api/v1/connections/test (no body) and
// returns the per-service connectivity result for the currently active
// connection.
func (c *Client) TestActiveConnection(t *testing.T) CredentialTestResult {
	t.Helper()
	var out CredentialTestResult
	c.PostJSON(t, "/api/v1/connections/test", nil, &out)
	return out
}

// DiscoverArgocdResponse mirrors the inline JSON returned by
// handleDiscoverArgocd — `{server_url, has_env_token, namespace}`.
type DiscoverArgocdResponse struct {
	ServerURL   string `json:"server_url"`
	HasEnvToken bool   `json:"has_env_token"`
	Namespace   string `json:"namespace"`
}

// DiscoverArgocd fetches GET /api/v1/connections/discover-argocd. The
// optional namespace query parameter defaults to "argocd" server-side
// when empty; pass "" to use the default.
func (c *Client) DiscoverArgocd(t *testing.T, namespace string) DiscoverArgocdResponse {
	t.Helper()
	var out DiscoverArgocdResponse
	path := "/api/v1/connections/discover-argocd"
	if namespace != "" {
		path += "?namespace=" + namespace
	}
	c.GetJSON(t, path, &out)
	return out
}

// ---------------------------------------------------------------------------
// Init
// ---------------------------------------------------------------------------

// InitResponse mirrors the inline JSON returned by handleInit. The handler
// returns 202 for a freshly-created session and 200 for a resumed/in-progress
// session — both share the same fields. WaitDetail / WaitPayload are only
// populated when the resumed session is in StatusWaiting.
type InitResponse struct {
	OperationID string `json:"operation_id"`
	Status      string `json:"status"`
	WaitDetail  string `json:"wait_detail,omitempty"`
	WaitPayload string `json:"wait_payload,omitempty"`
	Resumed     bool   `json:"resumed,omitempty"`
}

// Init POSTs an InitRepoRequest to /api/v1/init. The handler may respond
// with either 202 Accepted (new session) or 200 OK (resumed session); we
// accept any 2xx so callers don't have to branch on status code.
func (c *Client) Init(t *testing.T, req orchestrator.InitRepoRequest) InitResponse {
	t.Helper()
	var out InitResponse
	c.PostJSON(t, "/api/v1/init", req, &out)
	return out
}

// ---------------------------------------------------------------------------
// Operations
// ---------------------------------------------------------------------------

// GetOperation fetches GET /api/v1/operations/{id} and returns the typed
// operations.Session from internal/operations.
func (c *Client) GetOperation(t *testing.T, id string) *operations.Session {
	t.Helper()
	var out operations.Session
	c.GetJSON(t, "/api/v1/operations/"+id, &out)
	return &out
}

// CancelOperation POSTs to /api/v1/operations/{id}/cancel.
func (c *Client) CancelOperation(t *testing.T, id string) {
	t.Helper()
	c.PostJSON(t, "/api/v1/operations/"+id+"/cancel", nil, nil)
}

// HeartbeatOperation POSTs to /api/v1/operations/{id}/heartbeat.
func (c *Client) HeartbeatOperation(t *testing.T, id string) {
	t.Helper()
	c.PostJSON(t, "/api/v1/operations/"+id+"/heartbeat", nil, nil)
}

// WaitForOperationStatus polls GET /api/v1/operations/{id} until the session's
// Status equals one of the wantStatuses values, or timeout elapses. Calls
// t.Fatalf with the most-recent observed status on timeout.
//
// Returns the final session for further assertions.
func (c *Client) WaitForOperationStatus(t *testing.T, id string, timeout time.Duration, wantStatuses ...operations.Status) *operations.Session {
	t.Helper()
	if len(wantStatuses) == 0 {
		t.Fatalf("WaitForOperationStatus: at least one wantStatus required")
	}
	want := make(map[operations.Status]bool, len(wantStatuses))
	for _, s := range wantStatuses {
		want[s] = true
	}
	var last *operations.Session
	Eventually(t, timeout, func() bool {
		last = c.GetOperation(t, id)
		return want[last.Status]
	}, "operation %s never reached %v", id, wantStatuses)
	return last
}
