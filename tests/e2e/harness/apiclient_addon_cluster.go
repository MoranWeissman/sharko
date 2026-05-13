//go:build e2e

package harness

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// ---------------------------------------------------------------------------
// Per-cluster addon lifecycle wrappers (V2 Epic 7-1.7)
// ---------------------------------------------------------------------------
//
// These wrappers cover the three argocd-touching endpoints in the
// per-cluster addon orchestration surface:
//
//   POST   /api/v1/clusters/{name}/addons/{addon}   — enable an addon on a cluster
//   DELETE /api/v1/clusters/{name}/addons/{addon}   — disable an addon on a cluster
//   POST   /api/v1/addons/{name}/upgrade            — upgrade an addon (global or per-cluster)
//
// Per-cluster values overrides (PUT /clusters/{cluster}/addons/{name}/values)
// belong to story 7-1.8 and are intentionally NOT covered here.
//
// All wrappers IMPORT the production request/response types directly from
// internal/orchestrator so any shape drift breaks the harness at compile
// time. There is no JSON-string duplication.

// EnableAddonOnCluster issues
// POST /api/v1/clusters/{name}/addons/{addon} with the supplied request
// body and returns the typed orchestrator.EnableAddonResult. Use
// req.DryRun=true to exercise the preview path; the live (non-dry-run)
// path requires Yes=true on the request.
//
// The handler returns 200 on success, 207 on partial success — both are
// 2xx so the typed wrapper does not need to special-case 207. Tests that
// want to assert on partial-success specifically can still do so via
// result.Status == "partial".
func (c *Client) EnableAddonOnCluster(t *testing.T, cluster, addon string, req orchestrator.EnableAddonRequest) *orchestrator.EnableAddonResult {
	t.Helper()
	var out orchestrator.EnableAddonResult
	c.PostJSON(t,
		"/api/v1/clusters/"+cluster+"/addons/"+addon,
		req,
		&out,
	)
	return &out
}

// EnableAddonOnClusterRaw is the lower-level escape hatch for negative
// tests that need to assert on a specific status code or read a non-2xx
// body. The caller MUST close resp.Body.
func (c *Client) EnableAddonOnClusterRaw(t *testing.T, cluster, addon string, req orchestrator.EnableAddonRequest, opts ...RequestOption) *http.Response {
	t.Helper()
	return c.Do(t, http.MethodPost,
		"/api/v1/clusters/"+cluster+"/addons/"+addon,
		req, opts...)
}

// DisableAddonOnCluster issues
// DELETE /api/v1/clusters/{name}/addons/{addon} with the supplied
// request body. The handler accepts the body even on DELETE — the
// request shape carries dry_run / cleanup / yes. Returns the typed
// orchestrator.DisableAddonResult.
//
// Sharko's generic Delete helper does not send a body, so this wrapper
// uses the lower-level Do to preserve the JSON request body. The
// 401-retry-once behaviour is intentionally NOT applied here — the
// addon-disable contract is single-shot; if a test needs a refresh it
// should re-construct the client.
func (c *Client) DisableAddonOnCluster(t *testing.T, cluster, addon string, req orchestrator.DisableAddonRequest) *orchestrator.DisableAddonResult {
	t.Helper()
	resp := c.Do(t, http.MethodDelete,
		"/api/v1/clusters/"+cluster+"/addons/"+addon,
		req,
	)
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("DisableAddonOnCluster %s/%s: status=%d", cluster, addon, resp.StatusCode)
	}
	var out orchestrator.DisableAddonResult
	decodeJSON(t, resp, &out, "DisableAddonOnCluster "+cluster+"/"+addon)
	return &out
}

// DisableAddonOnClusterRaw is the negative-test escape hatch.
// Caller MUST close resp.Body.
func (c *Client) DisableAddonOnClusterRaw(t *testing.T, cluster, addon string, req orchestrator.DisableAddonRequest, opts ...RequestOption) *http.Response {
	t.Helper()
	return c.Do(t, http.MethodDelete,
		"/api/v1/clusters/"+cluster+"/addons/"+addon,
		req, opts...)
}

// UpgradeAddonRequest is the request shape for POST /api/v1/addons/{name}/upgrade.
//
// Mirrors the inline anonymous struct in handleUpgradeAddon — see
// internal/api/addons_upgrade.go. Defined here as a named type so test
// bodies are self-documenting; the field names match the JSON keys the
// handler decodes.
type UpgradeAddonRequest struct {
	// Version is the new addon version string (e.g. "1.15.0"). Required —
	// the handler returns 400 when empty.
	Version string `json:"version"`
	// Cluster, when non-empty, scopes the upgrade to a single cluster's
	// per-cluster values file (UpgradeAddonCluster). When empty, the
	// upgrade rewrites the global addons-catalog.yaml entry
	// (UpgradeAddonGlobal) and propagates to every cluster pinned to the
	// global version on its next ArgoCD sync.
	Cluster string `json:"cluster,omitempty"`
}

// UpgradeAddon issues POST /api/v1/addons/{name}/upgrade and returns the
// typed orchestrator.GitResult. Use req.Cluster="" for a global upgrade
// (rewrites configuration/addons-catalog.yaml) or req.Cluster="<name>"
// for a per-cluster pin (rewrites the cluster's values file).
//
// The handler always commits to git on success — there is no dry_run
// mode for the upgrade endpoint. Tests that want to drive this against
// the in-memory git mock must pre-seed the catalog (or the per-cluster
// values file) with a parseable structure.
func (c *Client) UpgradeAddon(t *testing.T, addon string, req UpgradeAddonRequest) *orchestrator.GitResult {
	t.Helper()
	var out orchestrator.GitResult
	c.PostJSON(t,
		"/api/v1/addons/"+addon+"/upgrade",
		req,
		&out,
	)
	return &out
}

// UpgradeAddonRaw is the negative-test escape hatch.
// Caller MUST close resp.Body.
func (c *Client) UpgradeAddonRaw(t *testing.T, addon string, req UpgradeAddonRequest, opts ...RequestOption) *http.Response {
	t.Helper()
	return c.Do(t, http.MethodPost,
		"/api/v1/addons/"+addon+"/upgrade",
		req, opts...)
}

// decodeJSON is a tiny helper used by DisableAddonOnCluster (which goes
// through the lower-level Do path so it can carry a request body on
// DELETE). Lives here rather than in apiclient.go so the typed wrapper
// is self-contained.
func decodeJSON(t *testing.T, resp *http.Response, out any, label string) {
	t.Helper()
	if out == nil {
		return
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil && err != io.EOF {
		t.Fatalf("%s: decode response: %v", label, err)
	}
}
