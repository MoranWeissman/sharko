//go:build e2e

package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// Client is a typed wrapper over the sharko HTTP API.
//
// It owns auth state (a session token + retry-on-401 logic) and exposes
// generic JSON helpers (Get/Post/Put/Patch/Delete) plus a small set of
// typed wrappers for the highest-traffic endpoints. Downstream domain
// stories (7-1.4..7-1.13) extend the typed surface as they need it; the
// generic helpers cover everything in the meantime.
//
// The typed wrappers IMPORT sharko's internal request/response types
// directly (`internal/models`, `internal/orchestrator`) — when those
// shapes evolve, the harness breaks at compile time. Zero codegen, zero
// drift.
//
// Auth model:
//   - NewClient logs in once at construction and stores the session token.
//   - Every JSON helper injects "Authorization: Bearer <token>" automatically.
//   - On 401 the helper invokes Refresh once (re-login) and retries the
//     request once. A second 401 is treated as a real auth failure and
//     fails the test via t.Fatalf.
//
// Concurrency: a Client is intended for sequential use by one test (or a
// strictly serial sub-test tree). Go's *http.Client itself is concurrency-
// safe, but the bearer-token mutate on Refresh is not synchronised — keep
// one Client per goroutine.
type Client struct {
	BaseURL string
	User    string
	Pass    string

	httpClient *http.Client
	token      string
}

// defaultRequestTimeout caps how long any single in-process API call may
// take. The default is generous enough for the slowest in-process flow
// (cluster register + dry-run) and short enough to fail fast if a handler
// deadlocks. Override via Client.Do or per-call options when a test needs
// a different bound.
const defaultRequestTimeout = 10 * time.Second

// requestOptions holds per-call overrides applied by the JSON helpers.
type requestOptions struct {
	headers      map[string]string
	expectStatus int // 0 means "any 2xx is fine"
	timeout      time.Duration
	skipAuth     bool // for the login call itself
	noRetry      bool // disable the 401-retry-once behaviour
}

// RequestOption tunes a single typed-helper invocation.
type RequestOption func(*requestOptions)

// WithHeader sets an extra header on the request. Repeatable across calls.
func WithHeader(key, value string) RequestOption {
	return func(o *requestOptions) {
		if o.headers == nil {
			o.headers = make(map[string]string, 2)
		}
		o.headers[key] = value
	}
}

// WithExpectStatus tells the helper to require a specific status code
// instead of "any 2xx". Useful for assertions like 201 Created or for
// happy-path negative tests that expect 4xx.
func WithExpectStatus(code int) RequestOption {
	return func(o *requestOptions) { o.expectStatus = code }
}

// WithTimeout overrides the per-request timeout (defaults to 10s).
func WithTimeout(d time.Duration) RequestOption {
	return func(o *requestOptions) { o.timeout = d }
}

// WithNoRetry disables the 401-retry-once behaviour for this call. Use in
// tests that intentionally drive a 401 path and want to assert it directly.
func WithNoRetry() RequestOption {
	return func(o *requestOptions) { o.noRetry = true }
}

// NewClient returns a Client pointed at sharko.URL using the bootstrap
// admin credentials seeded by StartSharko. The login call happens
// synchronously; t.Fatalf on failure.
func NewClient(t *testing.T, sharko *Sharko) *Client {
	t.Helper()
	if sharko == nil {
		t.Fatalf("NewClient: sharko is nil")
	}
	return NewClientAs(t, sharko, sharko.AdminUser, sharko.AdminPass)
}

// NewClientAs returns a Client logged in as a specific user. Used by RBAC
// tests to drive a non-admin session against the API.
func NewClientAs(t *testing.T, sharko *Sharko, user, pass string) *Client {
	t.Helper()
	if sharko == nil {
		t.Fatalf("NewClientAs: sharko is nil")
	}
	if user == "" || pass == "" {
		t.Fatalf("NewClientAs: empty creds (user=%q, pass-empty=%v)", user, pass == "")
	}
	c := &Client{
		BaseURL:    strings.TrimRight(sharko.URL, "/"),
		User:       user,
		Pass:       pass,
		httpClient: &http.Client{Timeout: defaultRequestTimeout},
	}
	c.Login(t)
	return c
}

// Login establishes a session by POSTing /api/v1/auth/login with the
// stored credentials. Returns the token (also stashed on the Client).
func (c *Client) Login(t *testing.T) string {
	t.Helper()
	body, err := json.Marshal(map[string]string{
		"username": c.User,
		"password": c.Pass,
	})
	if err != nil {
		t.Fatalf("Client.Login: marshal: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/v1/auth/login", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Client.Login: build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		t.Fatalf("Client.Login: http: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("Client.Login: status=%d (user=%q); body=%s", resp.StatusCode, c.User, raw)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("Client.Login: decode: %v", err)
	}
	if out.Token == "" {
		t.Fatalf("Client.Login: empty token in response")
	}
	c.token = out.Token
	return out.Token
}

// Refresh re-establishes the session after a 401. Identical to Login
// today; named separately so 401-retry call sites read clearly.
func (c *Client) Refresh(t *testing.T) {
	t.Helper()
	c.token = ""
	c.Login(t)
}

// Token returns the current session token (mainly for tests asserting
// auth state directly).
func (c *Client) Token() string { return c.token }

// SetToken installs a token directly. Used by tests that want to inject
// a deliberately invalid token to exercise 401 paths.
func (c *Client) SetToken(tok string) { c.token = tok }

// ---------------------------------------------------------------------------
// generic JSON helpers
// ---------------------------------------------------------------------------

// GetJSON issues a GET to path, decodes the response into out (may be nil
// to discard), and asserts a 2xx status (or the explicit WithExpectStatus
// override). 401 triggers one Refresh + retry.
func (c *Client) GetJSON(t *testing.T, path string, out any, opts ...RequestOption) {
	t.Helper()
	c.do(t, http.MethodGet, path, nil, out, opts)
}

// PostJSON issues a POST with a JSON-marshaled body, decodes the response
// into out (may be nil), and asserts a 2xx status (or the explicit
// WithExpectStatus override).
func (c *Client) PostJSON(t *testing.T, path string, body any, out any, opts ...RequestOption) {
	t.Helper()
	c.do(t, http.MethodPost, path, body, out, opts)
}

// PutJSON — same as PostJSON for PUT.
func (c *Client) PutJSON(t *testing.T, path string, body any, out any, opts ...RequestOption) {
	t.Helper()
	c.do(t, http.MethodPut, path, body, out, opts)
}

// PatchJSON — same as PostJSON for PATCH.
func (c *Client) PatchJSON(t *testing.T, path string, body any, out any, opts ...RequestOption) {
	t.Helper()
	c.do(t, http.MethodPatch, path, body, out, opts)
}

// Delete issues a DELETE and asserts 2xx (or explicit override). No body
// is decoded — most sharko DELETE endpoints return a tiny ack object that
// callers don't care about. Tests that need the body should use Do.
func (c *Client) Delete(t *testing.T, path string, opts ...RequestOption) {
	t.Helper()
	c.do(t, http.MethodDelete, path, nil, nil, opts)
}

// Do is the lower-level escape hatch. Returns the raw *http.Response for
// tests that need to assert specific status codes or read non-JSON bodies.
// 401-retry is NOT applied here — callers own the response lifecycle.
//
// Caller MUST close resp.Body.
func (c *Client) Do(t *testing.T, method, path string, body any, opts ...RequestOption) *http.Response {
	t.Helper()
	o := buildOpts(opts)
	resp, err := c.send(method, path, body, o)
	if err != nil {
		t.Fatalf("Client.Do %s %s: %v", method, path, err)
	}
	return resp
}

// ---------------------------------------------------------------------------
// internal request plumbing
// ---------------------------------------------------------------------------

func buildOpts(opts []RequestOption) *requestOptions {
	o := &requestOptions{timeout: defaultRequestTimeout}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

// do is the shared helper behind GetJSON / PostJSON / etc. Handles auth
// header injection, 401-retry-once, status-code assertion, and JSON
// decoding into out (nil-safe).
func (c *Client) do(t *testing.T, method, path string, body any, out any, opts []RequestOption) {
	t.Helper()
	o := buildOpts(opts)

	resp, err := c.send(method, path, body, o)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}

	// 401-retry-once: refresh, then re-issue the request a single time.
	if resp.StatusCode == http.StatusUnauthorized && !o.skipAuth && !o.noRetry {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		c.Refresh(t)
		resp, err = c.send(method, path, body, o)
		if err != nil {
			t.Fatalf("%s %s (after refresh): %v", method, path, err)
		}
	}
	defer resp.Body.Close()

	if !c.statusOK(resp.StatusCode, o) {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("%s %s: status=%d; body=%s", method, path, resp.StatusCode, raw)
	}

	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil && err != io.EOF {
		t.Fatalf("%s %s: decode response: %v", method, path, err)
	}
}

// send builds and executes a single HTTP request; no retries.
//
// Per-request timeout is enforced by an *http.Client cloned with the
// requested timeout — that bounds both the round-trip AND the body read,
// and avoids a leaked context.WithTimeout cancel funcs (which we cannot
// defer here because Do callers want the live body).
func (c *Client) send(method, path string, body any, o *requestOptions) (*http.Response, error) {
	var reqBody io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		reqBody = bytes.NewReader(raw)
	}
	url := c.BaseURL + path
	if !strings.HasPrefix(path, "/") {
		url = c.BaseURL + "/" + path
	}

	timeout := o.timeout
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}

	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if !o.skipAuth && c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for k, v := range o.headers {
		req.Header.Set(k, v)
	}

	httpClient := c.httpClient
	if timeout != httpClient.Timeout {
		// Clone once for this call so concurrent callers don't see a
		// drifting timeout on the shared client.
		clone := *c.httpClient
		clone.Timeout = timeout
		httpClient = &clone
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	return resp, nil
}

func (c *Client) statusOK(status int, o *requestOptions) bool {
	if o.expectStatus != 0 {
		return status == o.expectStatus
	}
	return status >= 200 && status < 300
}

// ---------------------------------------------------------------------------
// typed wrappers — the most common endpoints
// ---------------------------------------------------------------------------
//
// Downstream stories add more wrappers as they need them. The point of
// the initial set is to (a) prove the typed-import pattern works against
// real sharko types and (b) cover the smoke-test paths.

// HealthResponse mirrors the inline shape sharko returns from
// GET /api/v1/health (handler builds a map[string]string with status,
// version, mode). Defining a typed struct here keeps the test surface
// readable; the field names match what handleHealth writes today.
type HealthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
	Mode    string `json:"mode"`
}

// Health fetches GET /api/v1/health.
func (c *Client) Health(t *testing.T) HealthResponse {
	t.Helper()
	var out HealthResponse
	c.GetJSON(t, "/api/v1/health", &out)
	return out
}

// ListClusters fetches GET /api/v1/clusters and returns the typed
// ClustersResponse from internal/models. Pagination/filtering can be
// passed via path query parameters (caller-encoded).
func (c *Client) ListClusters(t *testing.T) *models.ClustersResponse {
	t.Helper()
	var out models.ClustersResponse
	c.GetJSON(t, "/api/v1/clusters", &out)
	return &out
}

// GetCluster fetches GET /api/v1/clusters/{name} and returns the typed
// ClusterDetailResponse from internal/models.
func (c *Client) GetCluster(t *testing.T, name string) *models.ClusterDetailResponse {
	t.Helper()
	var out models.ClusterDetailResponse
	c.GetJSON(t, "/api/v1/clusters/"+name, &out)
	return &out
}

// RegisterCluster POSTs an orchestrator.RegisterClusterRequest to
// /api/v1/clusters and returns the orchestrator.RegisterClusterResult.
// Downstream cluster-lifecycle tests (7-1.4) drive their happy path
// through this wrapper.
func (c *Client) RegisterCluster(t *testing.T, req orchestrator.RegisterClusterRequest) *orchestrator.RegisterClusterResult {
	t.Helper()
	var out orchestrator.RegisterClusterResult
	c.PostJSON(t, "/api/v1/clusters", req, &out)
	return &out
}

// DeregisterCluster issues DELETE /api/v1/clusters/{name}.
func (c *Client) DeregisterCluster(t *testing.T, name string) {
	t.Helper()
	c.Delete(t, "/api/v1/clusters/"+name)
}

// ListUsers fetches GET /api/v1/users (admin-only).
func (c *Client) ListUsers(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	c.GetJSON(t, "/api/v1/users", &out)
	return out
}

// CreateUserResponse mirrors the JSON returned by handleCreateUser.
type CreateUserResponse struct {
	Username     string `json:"username"`
	Role         string `json:"role"`
	TempPassword string `json:"temp_password"`
	Message      string `json:"message"`
}

// CreateUser POSTs to /api/v1/users (admin-only).
func (c *Client) CreateUser(t *testing.T, username, role string) CreateUserResponse {
	t.Helper()
	var out CreateUserResponse
	c.PostJSON(t, "/api/v1/users", map[string]string{
		"username": username,
		"role":     role,
	}, &out, WithExpectStatus(http.StatusCreated))
	return out
}
