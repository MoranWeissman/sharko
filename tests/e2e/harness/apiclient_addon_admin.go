//go:build e2e

package harness

// Typed API-client extensions for V2 Epic 7-1.6 — custom addon admin +
// addon-secrets endpoints. Mirrors the apiclient.go pattern: each wrapper
// imports sharko's internal request/response type so that schema drift
// breaks the harness at compile time, with no codegen.
//
// Scope of this file (per the 7-1.6 dispatch):
//
//   POST   /api/v1/addons                       — add custom addon entry
//   GET    /api/v1/addons/{name}                — get details
//   PATCH  /api/v1/addons/{name}                — update metadata
//   DELETE /api/v1/addons/{name}                — remove
//   GET    /api/v1/addons/list                  — admin list
//   GET    /api/v1/addons/catalog               — catalog view
//   GET    /api/v1/addons/{name}/changelog      — version diff
//   GET    /api/v1/addons/version-matrix        — full matrix
//   POST   /api/v1/addons/unwrap-globals        — globals migration
//   POST   /api/v1/addons/upgrade-batch         — batch upgrade
//
// (The /addon-secrets definition-CRUD endpoints this file once wrapped
// were retired by task #152, story 152.A — see the note at the bottom.)
//
// Many of these endpoints require an active ArgoCD client. The in-process
// boot path used by the e2e harness today does NOT seed an ArgoCD
// connection (the harness only injects a *MockGitProvider via
// SetDemoGitProvider — there is no analogous Demo ArgoCD wiring yet).
// Wrappers in this file therefore split into two flavours:
//
//   - "happy-path" wrappers: return the typed result for endpoints that
//     work today against the in-process harness (addon-secrets CRUD,
//     /addons/list when a catalog file has been seeded into the mock
//     git provider).
//
//   - "raw" wrappers (suffix `Raw`): return *http.Response so tests can
//     assert the no-active-connection contract (502 / 503 + body shape)
//     without the JSON helper turning the failure into a t.Fatalf. These
//     are the tests that lock in the validate-before-dial contract:
//     every write endpoint validates its request body BEFORE dialling
//     an upstream connection, so an empty/invalid POST returns 400 and
//     a well-formed POST returns 502 with `no active <…> connection`.
//
// Once a downstream story (≥ 7-1.10) wires up an ArgoCD fake the raw
// wrappers can be promoted to typed-result variants without touching the
// test bodies — the test code already addresses the wrappers by the
// minimal API needed.

import (
	"net/http"
	"testing"

	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// ---------------------------------------------------------------------------
// /addons admin — write paths (require active ArgoCD; raw wrappers used by
// validation/contract assertions)
// ---------------------------------------------------------------------------

// AddAddonRaw POSTs req to /api/v1/addons and returns the raw response.
// Caller MUST close resp.Body.
//
// In the in-process harness this normally returns 502 ("no active ArgoCD
// connection") or 400 (validation) — the test is responsible for asserting
// the expected shape.
func (c *Client) AddAddonRaw(t *testing.T, req orchestrator.AddAddonRequest) *http.Response {
	t.Helper()
	return c.Do(t, http.MethodPost, "/api/v1/addons", req)
}

// PatchAddonRaw PATCHes /api/v1/addons/{name}.
func (c *Client) PatchAddonRaw(t *testing.T, name string, req orchestrator.ConfigureAddonRequest) *http.Response {
	t.Helper()
	return c.Do(t, http.MethodPatch, "/api/v1/addons/"+name, req)
}

// DeleteAddonRaw issues DELETE /api/v1/addons/{name}. Without
// `?confirm=true` the handler returns a 400 dry-run impact report; with
// confirm=true it falls through to the orchestrator (and 502 in the
// in-process harness because of the missing ArgoCD client).
func (c *Client) DeleteAddonRaw(t *testing.T, name string, confirm bool) *http.Response {
	t.Helper()
	path := "/api/v1/addons/" + name
	if confirm {
		path += "?confirm=true"
	}
	return c.Do(t, http.MethodDelete, path, nil)
}

// GetAddonDetailRaw returns the raw response from GET /api/v1/addons/{name}.
// Requires active ArgoCD — returns 503 in the in-process harness.
func (c *Client) GetAddonDetailRaw(t *testing.T, name string) *http.Response {
	t.Helper()
	return c.Do(t, http.MethodGet, "/api/v1/addons/"+name, nil)
}

// GetAddonCatalogRaw returns the raw response from GET /api/v1/addons/catalog.
func (c *Client) GetAddonCatalogRaw(t *testing.T) *http.Response {
	t.Helper()
	return c.Do(t, http.MethodGet, "/api/v1/addons/catalog", nil)
}

// GetVersionMatrixRaw returns the raw response from GET
// /api/v1/addons/version-matrix.
func (c *Client) GetVersionMatrixRaw(t *testing.T) *http.Response {
	t.Helper()
	return c.Do(t, http.MethodGet, "/api/v1/addons/version-matrix", nil)
}

// GetAddonChangelogRaw returns the raw response from GET
// /api/v1/addons/{name}/changelog. Does NOT require ArgoCD but does
// require helm chart registry reachability (so it 5xx's in the offline
// harness). Tests use this to assert validation paths (`?from=garbage`).
func (c *Client) GetAddonChangelogRaw(t *testing.T, name, from, to string) *http.Response {
	t.Helper()
	path := "/api/v1/addons/" + name + "/changelog"
	q := ""
	if from != "" {
		q = "?from=" + from
	}
	if to != "" {
		if q == "" {
			q = "?to=" + to
		} else {
			q += "&to=" + to
		}
	}
	return c.Do(t, http.MethodGet, path+q, nil)
}

// UnwrapGlobalsRaw POSTs /api/v1/addons/unwrap-globals. The handler takes
// no body in the wire schema (it scans the configured global-values
// directory). Requires ArgoCD — returns 502 in the harness.
func (c *Client) UnwrapGlobalsRaw(t *testing.T) *http.Response {
	t.Helper()
	return c.Do(t, http.MethodPost, "/api/v1/addons/unwrap-globals", nil)
}

// UpgradeBatchRaw POSTs /api/v1/addons/upgrade-batch with the upgrades
// map. Validation happens before the upstream dial.
func (c *Client) UpgradeBatchRaw(t *testing.T, upgrades map[string]string) *http.Response {
	t.Helper()
	body := map[string]any{"upgrades": upgrades}
	return c.Do(t, http.MethodPost, "/api/v1/addons/upgrade-batch", body)
}

// ---------------------------------------------------------------------------
// /addons admin — read paths that DON'T require ArgoCD
// ---------------------------------------------------------------------------

// AddonsListResponse mirrors the wire shape of GET /api/v1/addons/list
// today (`{"applicationsets": [...]}`) — handler builds it inline as a
// `map[string]interface{}`, so we use a typed wrapper here for ergonomics.
type AddonsListResponse struct {
	ApplicationSets []map[string]any `json:"applicationsets"`
}

// ListAdminAddons fetches GET /api/v1/addons/list. Only requires the
// active GitProvider — works in the in-process harness when the test
// has seeded `configuration/addons-catalog.yaml` into the mock.
func (c *Client) ListAdminAddons(t *testing.T) AddonsListResponse {
	t.Helper()
	var out AddonsListResponse
	c.GetJSON(t, "/api/v1/addons/list", &out)
	return out
}

// ListAdminAddonsRaw returns the raw response — useful for asserting the
// "no catalog file" → 502 path (sharko surfaces it as upstream error).
func (c *Client) ListAdminAddonsRaw(t *testing.T) *http.Response {
	t.Helper()
	return c.Do(t, http.MethodGet, "/api/v1/addons/list", nil)
}

// ---------------------------------------------------------------------------
// /addon-secrets — RETIRED (task #152, story 152.A)
// ---------------------------------------------------------------------------
//
// The GET/POST/DELETE /addon-secrets definition-CRUD endpoints are gone:
// they let an API caller plant a whole secret definition (backend path,
// destination namespace, Secret name, key list) into an in-memory map that
// the refresh endpoint then delivered, bypassing Git. The Git catalog is
// the only source of addon-secret definitions now, and
// POST /clusters/{name}/secrets/refresh runs the reconciler's git-backed
// plan. The typed CRUD wrappers that lived here are deleted with the
// endpoints; lifecycle's TestAddonSecretEndpointsRetired drives the old
// paths raw (via Client.Do) to pin the 404s in place.
