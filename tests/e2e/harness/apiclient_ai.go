//go:build e2e

package harness

import (
	"net/http"
	"testing"
)

// This file extends Client with typed wrappers for the AI / agent / providers
// endpoints exercised by tests/e2e/lifecycle/ai_test.go (V2 Epic 7-1.11).
//
// File-isolation: this file is the AI subset of the typed client. The
// generic Get/Post helpers from apiclient.go cover everything else; the
// wrappers here are sugar that make ai_test.go read more like the rest of
// the suite. They use the loosest shapes that will survive small handler
// shape drift (map[string]any) — the precise field names that ai_test.go
// asserts on are checked at the call site.

// AIProviderInfo mirrors a single entry in
// aiConfigResponse.AvailableProviders. Field names match the JSON shape
// emitted by handleGetAIConfig (see internal/api/ai_config.go).
type AIProviderInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Configured bool   `json:"configured"`
	Model      string `json:"model"`
}

// AIConfigResponse mirrors aiConfigResponse from internal/api/ai_config.go.
// Defined as a typed struct (not map[string]any) so test assertions can
// reference the field names directly without spelunking JSON.
type AIConfigResponse struct {
	CurrentProvider    string           `json:"current_provider"`
	AvailableProviders []AIProviderInfo `json:"available_providers"`
	AnnotateOnSeed     bool             `json:"annotate_on_seed"`
}

// SaveAIConfigRequest mirrors saveAIConfigRequest from
// internal/api/ai_config.go. Only the fields that ai_test.go drives are
// included — extra fields (annotate_on_seed) are appended via a generic
// map when needed.
type SaveAIConfigRequest struct {
	Provider  string `json:"provider"`
	APIKey    string `json:"api_key,omitempty"`
	Model     string `json:"model,omitempty"`
	BaseURL   string `json:"base_url,omitempty"`
	OllamaURL string `json:"ollama_url,omitempty"`
}

// GetAIConfig wraps GET /api/v1/ai/config.
func (c *Client) GetAIConfig(t *testing.T) AIConfigResponse {
	t.Helper()
	var out AIConfigResponse
	c.GetJSON(t, "/api/v1/ai/config", &out)
	return out
}

// SaveAIConfig wraps POST /api/v1/ai/config (admin only). Returns the
// raw response body decoded into a map — the handler ack shape is small
// and stable enough that we don't bother with a struct.
func (c *Client) SaveAIConfig(t *testing.T, req SaveAIConfigRequest) map[string]any {
	t.Helper()
	out := map[string]any{}
	c.PostJSON(t, "/api/v1/ai/config", req, &out)
	return out
}

// SetAIProvider wraps POST /api/v1/ai/provider (admin only). Switches the
// active provider without changing other config.
func (c *Client) SetAIProvider(t *testing.T, provider string) map[string]any {
	t.Helper()
	out := map[string]any{}
	c.PostJSON(t, "/api/v1/ai/provider", map[string]string{"provider": provider}, &out)
	return out
}

// TestAIConfig wraps POST /api/v1/ai/test-config. The handler always
// returns 200 OK with `status: ok` or `status: error` in the body — even
// for malformed input — so the typed return is map[string]any.
func (c *Client) TestAIConfig(t *testing.T, req SaveAIConfigRequest) map[string]any {
	t.Helper()
	out := map[string]any{}
	c.PostJSON(t, "/api/v1/ai/test-config", req, &out)
	return out
}

// TestAIConnection wraps POST /api/v1/ai/test. Returns 503 when AI is not
// configured (caller should pass WithExpectStatus(503) in that case);
// returns 200 + {status:ok, response:string} when invocation succeeds.
func (c *Client) TestAIConnection(t *testing.T, opts ...RequestOption) *http.Response {
	t.Helper()
	return c.Do(t, http.MethodPost, "/api/v1/ai/test", nil, opts...)
}

// AgentChatRequest mirrors the inline shape decoded by handleAgentChat.
type AgentChatRequest struct {
	SessionID   string `json:"session_id,omitempty"`
	Message     string `json:"message"`
	PageContext string `json:"page_context,omitempty"`
}

// AgentChatResponse mirrors the {session_id, response} JSON returned on success.
type AgentChatResponse struct {
	SessionID string `json:"session_id"`
	Response  string `json:"response"`
}

// AgentChat wraps POST /api/v1/agent/chat. Returns the raw *http.Response
// because the handler returns 503 when no active connection exists, and
// the test wants to inspect the status before decoding.
func (c *Client) AgentChat(t *testing.T, req AgentChatRequest, opts ...RequestOption) *http.Response {
	t.Helper()
	return c.Do(t, http.MethodPost, "/api/v1/agent/chat", req, opts...)
}

// AgentReset wraps POST /api/v1/agent/reset. Always returns 200 even when
// the session does not exist (handler is a silent no-op in that case).
func (c *Client) AgentReset(t *testing.T, sessionID string) map[string]any {
	t.Helper()
	out := map[string]any{}
	c.PostJSON(t, "/api/v1/agent/reset", map[string]string{"session_id": sessionID}, &out)
	return out
}

// UpgradeAIStatus wraps GET /api/v1/upgrade/ai-status. Returns the
// {enabled: bool} body decoded into a typed struct.
type UpgradeAIStatusResponse struct {
	Enabled bool `json:"enabled"`
}

func (c *Client) UpgradeAIStatus(t *testing.T) UpgradeAIStatusResponse {
	t.Helper()
	var out UpgradeAIStatusResponse
	c.GetJSON(t, "/api/v1/upgrade/ai-status", &out)
	return out
}

// UpgradeAISummaryRequest mirrors models.UpgradeCheckRequest as the
// handler decodes it. Only fields the e2e test drives are included.
type UpgradeAISummaryRequest struct {
	AddonName     string `json:"addon_name"`
	TargetVersion string `json:"target_version"`
}

// UpgradeAISummary wraps POST /api/v1/upgrade/ai-summary. Returns the
// raw response so the test can branch on status (this endpoint requires
// both an active connection AND a working AI provider).
func (c *Client) UpgradeAISummary(t *testing.T, req UpgradeAISummaryRequest, opts ...RequestOption) *http.Response {
	t.Helper()
	return c.Do(t, http.MethodPost, "/api/v1/upgrade/ai-summary", req, opts...)
}

// ProvidersResponse mirrors the body of GET /api/v1/providers.
//
// Shape (from internal/api/system.go handleGetProviders):
//
//	{
//	  "configured_provider": null | {type, region, status, error?},
//	  "available_types":     ["aws-sm", "k8s-secrets"]
//	}
type ProvidersResponse struct {
	ConfiguredProvider map[string]any `json:"configured_provider"`
	AvailableTypes     []string       `json:"available_types"`
}

// ListProviders wraps GET /api/v1/providers.
func (c *Client) ListProviders(t *testing.T) ProvidersResponse {
	t.Helper()
	var out ProvidersResponse
	c.GetJSON(t, "/api/v1/providers", &out)
	return out
}

// ProviderTestRequest is the body decoded by handleTestProvider /
// handleTestProviderConfig. All fields are optional for handleTestProvider;
// type is required for handleTestProviderConfig.
type ProviderTestRequest struct {
	Type      string `json:"type,omitempty"`
	Region    string `json:"region,omitempty"`
	Prefix    string `json:"prefix,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

// TestProvider wraps POST /api/v1/providers/test. Returns the raw
// response so the test can assert on the {status, message, ...} body.
func (c *Client) TestProvider(t *testing.T, req ProviderTestRequest, opts ...RequestOption) *http.Response {
	t.Helper()
	return c.Do(t, http.MethodPost, "/api/v1/providers/test", req, opts...)
}

// TestProviderConfig wraps POST /api/v1/providers/test-config. Always
// returns 200 with {status, message?, ...} in the body — even on bad
// configs (the handler treats unknown provider type as a soft error).
func (c *Client) TestProviderConfig(t *testing.T, req ProviderTestRequest) map[string]any {
	t.Helper()
	out := map[string]any{}
	c.PostJSON(t, "/api/v1/providers/test-config", req, &out)
	return out
}
