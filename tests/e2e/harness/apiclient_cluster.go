//go:build e2e

package harness

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// decodeClusterJSON reads body and decodes into a fresh map. Empty body
// is treated as an empty map (some sharko handlers return 204 / 200 with
// no body). Used by the negative-status helpers in this file that want
// the raw error payload regardless of status code.
//
// Name-scoped to this file (not the generic decodeJSON) so a sibling
// agent's parallel apiclient_<domain>.go cannot collide on the symbol.
func decodeClusterJSON(t *testing.T, r io.Reader) map[string]any {
	t.Helper()
	raw, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("decodeClusterJSON: read: %v", err)
	}
	if len(raw) == 0 {
		return map[string]any{}
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		// Some endpoints (e.g. /clusters/{name}/test on success) return
		// JSON arrays or scalar shapes that don't fit a top-level
		// map. Stash the raw body so the test can still assert on it.
		return map[string]any{"_raw": string(raw)}
	}
	return out
}

// This file extends the typed *Client surface with cluster-lifecycle
// helpers used exclusively by the V2 Epic 7-1.4 lifecycle suite (see
// tests/e2e/lifecycle/cluster_test.go). Foundation contracts in
// apiclient.go remain stable; downstream domain stories (one per
// epic-7-1.x bundle) own one apiclient_<domain>.go file each so
// concurrent agents do not collide on a single file.
//
// Naming convention:
//   - Wrapper methods take (*testing.T, ...args) and t.Fatalf on
//     transport / decode errors. The underlying do() already classifies
//     non-2xx into t.Fatalf with body context (or honours
//     WithExpectStatus for negative assertions).
//   - Response types alias internal/models or internal/orchestrator
//     wherever a real production type exists. Inline anonymous response
//     types live here only for the handlers whose handler returns a
//     map[string]interface{} payload (history, secrets, batch result).
//
// Auth: all wrappers inherit Client's bearer-token + 401-retry-once.
// RBAC tests can drive a non-admin client via NewClientAs(t, sharko,
// "operator-test", pwd) and then call these wrappers as normal.

// ---------------------------------------------------------------------------
// Cluster-write wrappers
// ---------------------------------------------------------------------------

// PatchClusterAddons issues PATCH /api/v1/clusters/{name} with the given
// addon map (and optional secret_path). Returns the raw response payload
// because the handler's success body is a typed orchestrator result on
// the addons branch and a small ack object on the secret_path-only
// branch — the caller is more likely to want the lossless map than to
// commit to one shape.
func (c *Client) PatchClusterAddons(t *testing.T, name string, addons map[string]bool, secretPath *string) map[string]any {
	t.Helper()
	body := map[string]any{}
	if addons != nil {
		body["addons"] = addons
	}
	if secretPath != nil {
		body["secret_path"] = *secretPath
	}
	var out map[string]any
	c.PatchJSON(t, "/api/v1/clusters/"+name, body, &out)
	return out
}

// TestClusterConnectivity issues POST /api/v1/clusters/{name}/test. The
// handler returns 200 even on stage failure (the result body carries
// success/failure flags), so we always decode into the typed body shape
// the FE consumes.
//
// Pass deep=true to run the Stage 2 (ArgoCD round-trip) probe. The
// production handler will surface ServiceUnavailable (503) when no
// credentials provider is configured — the lifecycle test's "Test"
// subtest skips gracefully on 503 so a kubeconfig-only e2e run still
// proves the route is reachable.
func (c *Client) TestClusterConnectivity(t *testing.T, name string, deep bool) (status int, body map[string]any) {
	t.Helper()
	resp := c.Do(t, http.MethodPost, "/api/v1/clusters/"+name+"/test", map[string]bool{"deep": deep})
	defer resp.Body.Close()
	body = decodeClusterJSON(t, resp.Body)
	return resp.StatusCode, body
}

// RefreshClusterCredentials issues POST /api/v1/clusters/{name}/refresh.
// Returns (status, body) so the lifecycle test can skip-graceful on the
// 503 path (no credentials provider configured) without triggering a
// t.Fatalf inside the helper.
func (c *Client) RefreshClusterCredentials(t *testing.T, name string) (status int, body map[string]any) {
	t.Helper()
	resp := c.Do(t, http.MethodPost, "/api/v1/clusters/"+name+"/refresh", nil)
	defer resp.Body.Close()
	body = decodeClusterJSON(t, resp.Body)
	return resp.StatusCode, body
}

// DiagnoseCluster issues POST /api/v1/clusters/{name}/diagnose.
// 503 (no credentials provider) is the expected path for kubeconfig-only
// e2e runs; the helper returns (status, body) and lets the caller
// branch.
func (c *Client) DiagnoseCluster(t *testing.T, name string) (status int, body map[string]any) {
	t.Helper()
	resp := c.Do(t, http.MethodPost, "/api/v1/clusters/"+name+"/diagnose", nil)
	defer resp.Body.Close()
	body = decodeClusterJSON(t, resp.Body)
	return resp.StatusCode, body
}

// ---------------------------------------------------------------------------
// Cluster-read wrappers
// ---------------------------------------------------------------------------

// GetClusterValues issues GET /api/v1/clusters/{name}/values and returns
// the handler's raw map response.
func (c *Client) GetClusterValues(t *testing.T, name string) map[string]any {
	t.Helper()
	var out map[string]any
	c.GetJSON(t, "/api/v1/clusters/"+name+"/values", &out)
	return out
}

// GetClusterConfigDiff issues GET /api/v1/clusters/{name}/config-diff.
func (c *Client) GetClusterConfigDiff(t *testing.T, name string) map[string]any {
	t.Helper()
	var out map[string]any
	c.GetJSON(t, "/api/v1/clusters/"+name+"/config-diff", &out)
	return out
}

// GetClusterComparison issues GET /api/v1/clusters/{name}/comparison and
// returns the typed ClusterComparisonResponse from internal/models.
//
// The handler returns 404 when the cluster name is unknown to ArgoCD;
// the lifecycle test uses WithExpectStatus on that negative path via
// the lower-level Do helper, so this typed wrapper assumes happy path.
func (c *Client) GetClusterComparison(t *testing.T, name string) *models.ClusterComparisonResponse {
	t.Helper()
	var out models.ClusterComparisonResponse
	c.GetJSON(t, "/api/v1/clusters/"+name+"/comparison", &out)
	return &out
}

// GetClusterHistory issues GET /api/v1/clusters/{name}/history.
// Response shape is a small map (cluster_name + history slice); a typed
// model would be premature given the handler still emits inline maps.
func (c *Client) GetClusterHistory(t *testing.T, name string) map[string]any {
	t.Helper()
	var out map[string]any
	c.GetJSON(t, "/api/v1/clusters/"+name+"/history", &out)
	return out
}

// ListClusterSecrets issues GET /api/v1/clusters/{name}/secrets. 503 is
// expected when no credentials provider is configured.
func (c *Client) ListClusterSecrets(t *testing.T, name string) (status int, body map[string]any) {
	t.Helper()
	resp := c.Do(t, http.MethodGet, "/api/v1/clusters/"+name+"/secrets", nil)
	defer resp.Body.Close()
	body = decodeClusterJSON(t, resp.Body)
	return resp.StatusCode, body
}

// RefreshClusterSecrets issues POST /api/v1/clusters/{name}/secrets/refresh.
// 503 is expected when no credentials provider is configured.
func (c *Client) RefreshClusterSecrets(t *testing.T, name string) (status int, body map[string]any) {
	t.Helper()
	resp := c.Do(t, http.MethodPost, "/api/v1/clusters/"+name+"/secrets/refresh", nil)
	defer resp.Body.Close()
	body = decodeClusterJSON(t, resp.Body)
	return resp.StatusCode, body
}

// GetClusterNodes issues GET /api/v1/cluster/nodes. The handler always
// returns 200 — when sharko is not in-cluster, the body carries an
// explanatory "message" and an empty nodes slice.
func (c *Client) GetClusterNodes(t *testing.T) map[string]any {
	t.Helper()
	var out map[string]any
	c.GetJSON(t, "/api/v1/cluster/nodes", &out)
	return out
}

// ---------------------------------------------------------------------------
// Discovery / batch / orphan wrappers
// ---------------------------------------------------------------------------

// ListAvailableClusters issues GET /api/v1/clusters/available. 503 is
// returned when no credentials provider is configured (the EKS path).
func (c *Client) ListAvailableClusters(t *testing.T) (status int, body map[string]any) {
	t.Helper()
	resp := c.Do(t, http.MethodGet, "/api/v1/clusters/available", nil)
	defer resp.Body.Close()
	body = decodeClusterJSON(t, resp.Body)
	return resp.StatusCode, body
}

// DiscoverEKSClusters issues POST /api/v1/clusters/discover. 503 is
// expected when no credentials provider is configured.
func (c *Client) DiscoverEKSClusters(t *testing.T, region string) (status int, body map[string]any) {
	t.Helper()
	req := map[string]any{}
	if region != "" {
		req["region"] = region
	}
	resp := c.Do(t, http.MethodPost, "/api/v1/clusters/discover", req)
	defer resp.Body.Close()
	body = decodeClusterJSON(t, resp.Body)
	return resp.StatusCode, body
}

// AdoptClusters issues POST /api/v1/clusters/adopt. 503 is expected
// without a credentials provider.
func (c *Client) AdoptClusters(t *testing.T, req orchestrator.AdoptClustersRequest) (status int, body map[string]any) {
	t.Helper()
	resp := c.Do(t, http.MethodPost, "/api/v1/clusters/adopt", req)
	defer resp.Body.Close()
	body = decodeClusterJSON(t, resp.Body)
	return resp.StatusCode, body
}

// UnadoptCluster issues POST /api/v1/clusters/{name}/unadopt with the
// confirmation flag pre-filled.
func (c *Client) UnadoptCluster(t *testing.T, name string, dryRun bool) (status int, body map[string]any) {
	t.Helper()
	req := orchestrator.UnadoptClusterRequest{Yes: true, DryRun: dryRun}
	resp := c.Do(t, http.MethodPost, "/api/v1/clusters/"+name+"/unadopt", req)
	defer resp.Body.Close()
	body = decodeClusterJSON(t, resp.Body)
	return resp.StatusCode, body
}

// BatchRegisterClusters issues POST /api/v1/clusters/batch. 503 is the
// expected response on a kubeconfig-only run (handler requires
// credProvider regardless of per-cluster provider).
func (c *Client) BatchRegisterClusters(t *testing.T, reqs []orchestrator.RegisterClusterRequest) (status int, body map[string]any) {
	t.Helper()
	resp := c.Do(t, http.MethodPost, "/api/v1/clusters/batch", map[string]any{"clusters": reqs})
	defer resp.Body.Close()
	body = decodeClusterJSON(t, resp.Body)
	return resp.StatusCode, body
}

// DeleteOrphanCluster issues DELETE /api/v1/clusters/{name}/orphan and
// returns the raw status (the success path is 204 with no body).
func (c *Client) DeleteOrphanCluster(t *testing.T, name string) int {
	t.Helper()
	resp := c.Do(t, http.MethodDelete, "/api/v1/clusters/"+name+"/orphan", nil)
	defer resp.Body.Close()
	return resp.StatusCode
}

// RemoveCluster issues DELETE /api/v1/clusters/{name} with a body
// carrying cleanup + confirmation. The orchestrator returns
// orchestrator.RemoveClusterResult as JSON; we decode into a map so the
// helper does not couple the lifecycle test to internal field renames.
func (c *Client) RemoveCluster(t *testing.T, name string, cleanup string, dryRun bool) (status int, body map[string]any) {
	t.Helper()
	req := orchestrator.RemoveClusterRequest{
		Name:    name,
		Cleanup: cleanup,
		Yes:     true,
		DryRun:  dryRun,
	}
	resp := c.Do(t, http.MethodDelete, "/api/v1/clusters/"+name, req)
	defer resp.Body.Close()
	body = decodeClusterJSON(t, resp.Body)
	return resp.StatusCode, body
}
