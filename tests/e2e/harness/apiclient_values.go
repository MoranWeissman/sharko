//go:build e2e

// Package harness — typed-client helpers for the values editor + AI flows
// (V2 Epic 7-1 Story 7-1.8).
//
// This file extends the foundation Client with typed wrappers for the 10
// values-editor endpoints and a small SeedActiveConnection helper that wires
// up an active connection so handlers requiring a live ArgoCD client (PUT
// values, AI annotate, AI opt-out, preview-merge) can construct one and
// reach the orchestrator commit path.
//
// Why a saved connection at all when StartSharko already injects a
// MockGitProvider?
//
//   - SetDemoGitProvider only overrides connSvc.GetActiveGitProvider — the
//     PUT-values handlers ALSO call connSvc.GetActiveArgocdClient(), which
//     has no override. Without a saved connection the handler 502s before
//     it ever touches the mock.
//   - The ArgoCD client we install is a tokenised HTTP wrapper that never
//     dials anything during the values flow (the orchestrator's commit
//     helpers don't use it; AddonService.GetAddonDetail's
//     ac.GetApplicationSet call is best-effort and tolerates errors).
//     Synthetic URL + token are sufficient.
//   - We also need the addons-catalog.yaml on the mock so GetAddonDetail
//     resolves the addon (without it the PUT 404s with "addon not found").
//
// The contract is intentionally narrow: SeedActiveConnection only seeds
// what the values-editor flows need. Other story authors can extend the
// pattern as their endpoints demand.
package harness

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

// doJSONRaw issues a method+path+body request via the Client and returns
// the raw response body for callers that need to peek at the wire shape
// before decoding (chiefly: unwrapping the Tier 2 attribution envelope).
//
// Mirrors the GetJSON/PostJSON/PutJSON contract:
//   - 401 retries once via Client.Refresh.
//   - Non-2xx fails the test through t.Fatalf.
//   - The bearer token + Content-Type headers are injected automatically.
//
// Lives next to its primary consumer (unwrapAttribution) so the dependency
// is obvious. Future callers in other typed-wrapper files can reuse it.
func (c *Client) doJSONRaw(t *testing.T, method, path string, body any) json.RawMessage {
	t.Helper()
	resp := c.Do(t, method, path, body)
	// 401-retry-once parity with the do() helper.
	if resp.StatusCode == http.StatusUnauthorized {
		_ = resp.Body.Close()
		c.Refresh(t)
		resp = c.Do(t, method, path, body)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("%s %s: read body: %v", method, path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("%s %s: status=%d; body=%s", method, path, resp.StatusCode, raw)
	}
	return json.RawMessage(raw)
}

// attributionEnvelope mirrors the wrapper sharko's withAttributionWarning
// helper applies when a Tier 2 write falls back to the service token (no
// per-user PAT available). Wire shape:
//
//	{"result": <original-payload>, "attribution_warning": "no_per_user_pat"}
//
// The harness clients never auth as per-user PAT holders (no encryption
// key + no per-user token store), so EVERY Tier 2 PUT we drive ends up
// in this wrapped shape. unwrapAttribution handles both shapes uniformly:
// payload-direct (Tier 1, or future tests with a real PAT seeded) and
// the result-wrapped envelope (current Tier 2 fallback path).
//
// Calls t.Fatalf on a malformed body — the wrapper is the API contract
// and a missing-result-key is a regression we want loudly visible.
func unwrapAttribution(t *testing.T, raw json.RawMessage, out any) string {
	t.Helper()
	if len(raw) == 0 {
		return ""
	}
	// Probe for the envelope keys without committing to a particular
	// shape for the inner payload — keeps this helper polymorphic.
	var probe struct {
		Result             json.RawMessage `json:"result,omitempty"`
		AttributionWarning string          `json:"attribution_warning,omitempty"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("unwrapAttribution: probe decode: %v (raw=%s)", err, raw)
	}
	if probe.AttributionWarning != "" && len(probe.Result) > 0 {
		// Wrapped shape — decode the inner payload into out.
		if err := json.Unmarshal(probe.Result, out); err != nil {
			t.Fatalf("unwrapAttribution: wrapped payload decode: %v (raw=%s)", err, raw)
		}
		return probe.AttributionWarning
	}
	// Direct shape — decode the entire body into out.
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("unwrapAttribution: direct payload decode: %v (raw=%s)", err, raw)
	}
	return probe.AttributionWarning
}

// ─── Values editor (global) ──────────────────────────────────────────────

// AddonValuesSchema is the typed result of GET /api/v1/addons/{name}/values-schema.
// We re-export sharko's models.AddonValuesSchemaResponse so callers can read
// every field the handler emits (CurrentValues, Schema, AIAnnotated, AIOptOut,
// LegacyWrapDetected, ValuesVersionMismatch).
type AddonValuesSchema = models.AddonValuesSchemaResponse

// GetAddonValuesSchema fetches the current global values + optional schema +
// header-derived AI flags for an addon.
func (c *Client) GetAddonValuesSchema(t *testing.T, addonName string) *AddonValuesSchema {
	t.Helper()
	var out AddonValuesSchema
	c.GetJSON(t, "/api/v1/addons/"+addonName+"/values-schema", &out)
	return &out
}

// SetAddonValuesRequest mirrors the api.setAddonValuesRequest shape (the
// handler's request body). Re-declared locally to avoid pulling internal/api
// into the test surface — the field names match the wire contract verbatim.
type SetAddonValuesRequest struct {
	Values              string `json:"values"`
	RefreshFromUpstream bool   `json:"refresh_from_upstream,omitempty"`
}

// SetAddonValuesResult is the typed shape of a successful PUT response.
// Sharko's handler wraps a *orchestrator.GitResult through
// withAttributionWarning, so the canonical fields (PRUrl/PRID/Branch/Merged)
// land on a flat object — we capture them with explicit json tags.
type SetAddonValuesResult struct {
	PRUrl               string `json:"pr_url"`
	PRID                int    `json:"pr_id"`
	Branch              string `json:"branch"`
	Merged              bool   `json:"merged"`
	CommitSHA           string `json:"commit_sha"`
	ValuesFile          string `json:"values_file"`
	AttributionWarning  string `json:"attribution_warning,omitempty"`
}

// SetAddonValues PUTs the full YAML for an addon's global values. The
// handler validates parseability before any Git activity, so a malformed
// payload surfaces as a 400 (use Do + WithExpectStatus to assert that path).
//
// The response is automatically unwrapped from the
// {"result": ..., "attribution_warning": "..."} envelope sharko applies on
// Tier 2 writes that fell back to the service token. The
// AttributionWarning field on the returned struct carries the warning
// signal when it fired.
func (c *Client) SetAddonValues(t *testing.T, addonName string, req SetAddonValuesRequest) *SetAddonValuesResult {
	t.Helper()
	raw := c.doJSONRaw(t, http.MethodPut, "/api/v1/addons/"+addonName+"/values", req)
	var out SetAddonValuesResult
	out.AttributionWarning = unwrapAttribution(t, raw, &out)
	return &out
}

// SetAddonAIOptOutRequest is the body of PUT /addons/{name}/values/ai-opt-out.
type SetAddonAIOptOutRequest struct {
	OptOut bool `json:"opt_out"`
}

// SetAddonAIOptOutResult mirrors the handler's success body (idempotent
// no-op + post-mutation shapes share a flat structure here).
type SetAddonAIOptOutResult struct {
	Status  string `json:"status"`
	OptOut  bool   `json:"opt_out"`
	Addon   string `json:"addon"`
	Message string `json:"message,omitempty"`
	PRUrl   string `json:"pr_url,omitempty"`
	PRID    int    `json:"pr_id,omitempty"`
	Merged  bool   `json:"merged,omitempty"`
}

// SetAddonAIOptOut toggles the per-addon AI annotation opt-out directive.
// Returns the handler's response so callers can distinguish noop (status=
// "noop") from a fresh PR (status="ok" + pr_id != 0).
//
// Like SetAddonValues, this transparently unwraps the attribution
// envelope; the noop path returns a flat shape (no envelope) and is
// handled identically.
func (c *Client) SetAddonAIOptOut(t *testing.T, addonName string, optOut bool) *SetAddonAIOptOutResult {
	t.Helper()
	raw := c.doJSONRaw(t, http.MethodPut, "/api/v1/addons/"+addonName+"/values/ai-opt-out",
		SetAddonAIOptOutRequest{OptOut: optOut})
	var out SetAddonAIOptOutResult
	_ = unwrapAttribution(t, raw, &out)
	return &out
}

// AnnotateAddonValuesResult mirrors the handler's success body.
type AnnotateAddonValuesResult struct {
	PRUrl        string `json:"pr_url,omitempty"`
	PRID         int    `json:"pr_id,omitempty"`
	Branch       string `json:"branch,omitempty"`
	Merged       bool   `json:"merged"`
	CommitSHA    string `json:"commit_sha,omitempty"`
	AISkipReason string `json:"ai_skip_reason,omitempty"`
}

// AnnotateAddonValues triggers the AI annotate pass. Returns the typed
// success body. Failures surface via t.Fatalf inside the underlying
// PostJSON helper — tests that expect 503 (AI not configured) or 422
// (secret-leak block) should use Do directly.
//
// Response is unwrapped from the attribution envelope (Tier 2 write).
func (c *Client) AnnotateAddonValues(t *testing.T, addonName string) *AnnotateAddonValuesResult {
	t.Helper()
	raw := c.doJSONRaw(t, http.MethodPost, "/api/v1/addons/"+addonName+"/values/annotate", nil)
	var out AnnotateAddonValuesResult
	_ = unwrapAttribution(t, raw, &out)
	return &out
}

// PreviewMergeAddonValuesResult mirrors api.previewMergeResponse. Re-declared
// here so the test layer doesn't depend on a non-exported type.
type PreviewMergeAddonValuesResult struct {
	Current         string                       `json:"current"`
	Merged          string                       `json:"merged"`
	UpstreamVersion string                       `json:"upstream_version"`
	DiffSummary     PreviewMergeDiffSummary      `json:"diff_summary"`
}

// PreviewMergeDiffSummary mirrors api.previewMergeSummary.
type PreviewMergeDiffSummary struct {
	NewKeys           []string `json:"new_keys"`
	PreservedUserKeys []string `json:"preserved_user_keys"`
	NoOp              bool     `json:"no_op"`
}

// PreviewMergeAddonValues asks sharko to preview an additive merge of the
// chart's upstream values onto the user's current global file.
//
// Note: this endpoint reaches out to the chart's Helm repository for the
// upstream values.yaml (no test seam in internal/helm). Tests that may run
// without internet should call this through Do and skip on a non-2xx body
// containing "fetching upstream values".
func (c *Client) PreviewMergeAddonValues(t *testing.T, addonName string) *PreviewMergeAddonValuesResult {
	t.Helper()
	var out PreviewMergeAddonValuesResult
	c.PostJSON(t, "/api/v1/addons/"+addonName+"/values/preview-merge", nil, &out)
	return &out
}

// RecentValuesPRsEntry mirrors api.recentPRsEntry — one row in the recent-
// changes panel.
type RecentValuesPRsEntry struct {
	PRID     int    `json:"pr_id"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Author   string `json:"author"`
	MergedAt string `json:"merged_at"`
}

// RecentValuesPRsResponse mirrors api.recentPRsResponse.
type RecentValuesPRsResponse struct {
	Entries    []RecentValuesPRsEntry `json:"entries"`
	ViewAllURL string                 `json:"view_all_url,omitempty"`
	ValuesFile string                 `json:"values_file"`
}

// RecentAddonValuesPRs fetches the recent merged values PRs for an addon's
// global values file. The handler memoizes via a 5-minute TTL cache; tests
// that toggle PR state mid-run must accept that timing.
func (c *Client) RecentAddonValuesPRs(t *testing.T, addonName string) *RecentValuesPRsResponse {
	t.Helper()
	var out RecentValuesPRsResponse
	c.GetJSON(t, "/api/v1/addons/"+addonName+"/values/recent-prs", &out)
	return &out
}

// ─── Values editor (per-cluster) ─────────────────────────────────────────

// ClusterAddonValues mirrors models.ClusterAddonValuesResponse.
type ClusterAddonValues = models.ClusterAddonValuesResponse

// GetClusterAddonValues fetches the per-cluster overrides + optional schema
// for one addon on one cluster. CurrentOverrides is empty when the cluster
// has not overridden the addon yet.
func (c *Client) GetClusterAddonValues(t *testing.T, cluster, addonName string) *ClusterAddonValues {
	t.Helper()
	var out ClusterAddonValues
	c.GetJSON(t, "/api/v1/clusters/"+cluster+"/addons/"+addonName+"/values", &out)
	return &out
}

// SetClusterAddonValuesRequest mirrors api.setClusterAddonValuesRequest.
type SetClusterAddonValuesRequest struct {
	Values string `json:"values"`
}

// SetClusterAddonValuesResult mirrors the orchestrator.GitResult shape that
// the handler emits (with attribution warning).
type SetClusterAddonValuesResult struct {
	PRUrl              string `json:"pr_url"`
	PRID               int    `json:"pr_id"`
	Branch             string `json:"branch"`
	Merged             bool   `json:"merged"`
	CommitSHA          string `json:"commit_sha"`
	ValuesFile         string `json:"values_file"`
	AttributionWarning string `json:"attribution_warning,omitempty"`
}

// SetClusterAddonValues PUTs an addon's per-cluster overrides. Pass an
// empty Values string to delete the addon's section from the cluster file.
//
// Response is unwrapped from the attribution envelope (Tier 2 write).
func (c *Client) SetClusterAddonValues(t *testing.T, cluster, addonName string, req SetClusterAddonValuesRequest) *SetClusterAddonValuesResult {
	t.Helper()
	raw := c.doJSONRaw(t, http.MethodPut, "/api/v1/clusters/"+cluster+"/addons/"+addonName+"/values", req)
	var out SetClusterAddonValuesResult
	out.AttributionWarning = unwrapAttribution(t, raw, &out)
	return &out
}

// RecentClusterAddonValuesPRs fetches the recent PRs for a per-cluster
// overrides file scoped to one addon.
func (c *Client) RecentClusterAddonValuesPRs(t *testing.T, cluster, addonName string) *RecentValuesPRsResponse {
	t.Helper()
	var out RecentValuesPRsResponse
	c.GetJSON(t, "/api/v1/clusters/"+cluster+"/addons/"+addonName+"/values/recent-prs", &out)
	return &out
}

// ─── Connection bootstrap (values editor needs an active connection) ────

// SeedActiveConnectionConfig describes the connection the values-editor
// tests need. Most fields default to safe synthetic values; callers
// almost never need to override them.
type SeedActiveConnectionConfig struct {
	// Name is the connection identifier. Defaults to "e2e-values".
	Name string
	// ArgocdServerURL is stored on the connection so buildArgocdClient
	// returns a non-nil *argocd.Client. The client is constructed but
	// never dialled during the values flow. Defaults to a synthetic
	// loopback URL.
	ArgocdServerURL string
	// ArgocdToken is a synthetic non-empty token so buildArgocdClient's
	// "ArgoCD token not configured" gate doesn't fire. Defaults to a
	// synthetic value.
	ArgocdToken string
	// GitToken is a synthetic non-empty token so the connection passes
	// any future required-field tightening. Defaults to a synthetic value.
	GitToken string
	// GitOwner / GitRepo identify the synthetic GitHub repo. Defaults
	// match the MockGitProvider's owner/repo.
	GitOwner string
	GitRepo  string
}

// SeedActiveConnection registers + activates a synthetic GitHub connection
// against sharko so handlers requiring a live ArgoCD client (PUT values,
// AI annotate, AI opt-out) can construct one. Idempotent — a second call
// with the same Name updates rather than duplicates.
//
// Calls t.Fatalf on any failure. Returns the connection name (handy for
// downstream assertions).
func SeedActiveConnection(t *testing.T, sharko *Sharko, admin *Client, cfg SeedActiveConnectionConfig) string {
	t.Helper()
	if sharko == nil {
		t.Fatalf("SeedActiveConnection: sharko is nil")
	}
	if admin == nil {
		t.Fatalf("SeedActiveConnection: admin client is nil")
	}
	if cfg.Name == "" {
		cfg.Name = "e2e-values"
	}
	if cfg.ArgocdServerURL == "" {
		// Loopback URL with a port that is virtually guaranteed to be
		// closed in a test sandbox. The values flow constructs the client
		// but never dials, so this is fine; the only call AddonService
		// makes against it (ac.GetApplicationSet) is best-effort and the
		// resulting connection error is logged + ignored.
		cfg.ArgocdServerURL = "https://argocd-e2e.invalid:8080"
	}
	if cfg.ArgocdToken == "" {
		cfg.ArgocdToken = "e2e-synthetic-argocd-token"
	}
	// Intentionally NOT defaulting GitToken. The api.Server's
	// providerFromConnectionWithToken (tiered_git.go) constructs a real
	// GitHub HTTP client whenever the connection carries a non-empty token
	// — that bypasses connSvc.GetActiveGitProvider's MockGitProvider
	// override and the writes hit the real GitHub API (401 against
	// `sharko-e2e/sharko-addons` since that repo doesn't exist).
	//
	// Leaving Git.Token EMPTY routes the tiered path through the
	// `if token == "" { return s.connSvc.GetActiveGitProvider() }` branch,
	// which returns the mock. Token fields are not required at create
	// time per validateConnectionRequest, so this is a supported config.
	if cfg.GitOwner == "" {
		cfg.GitOwner = "sharko-e2e"
	}
	if cfg.GitRepo == "" {
		cfg.GitRepo = "sharko-addons"
	}

	body := models.CreateConnectionRequest{
		Name: cfg.Name,
		Git: models.GitRepoConfig{
			Provider: models.GitProviderGitHub,
			Owner:    cfg.GitOwner,
			Repo:     cfg.GitRepo,
			// Token deliberately omitted — see field-level comment on
			// SeedActiveConnectionConfig.GitToken. Honoring an explicit
			// non-empty caller value is still allowed (the field stays
			// supported on the config struct), but the default path
			// keeps the mock plumbed in.
			Token: cfg.GitToken,
		},
		Argocd: models.ArgocdConfig{
			ServerURL: cfg.ArgocdServerURL,
			Token:     cfg.ArgocdToken,
			Namespace: "argocd",
		},
		GitOps: &models.GitOpsSettings{
			BaseBranch:   "main",
			BranchPrefix: "sharko/",
			CommitPrefix: "sharko:",
		},
		SetAsDefault: true,
	}

	// POST /api/v1/connections/ — create. 201 on success; if it already
	// exists the file store will overwrite without erroring (SaveConnection
	// is upsert), so we don't bother branching for "already exists".
	resp := admin.Do(t, http.MethodPost, "/api/v1/connections/", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("SeedActiveConnection: POST /connections/ status=%d", resp.StatusCode)
	}

	// POST /api/v1/connections/active — set active. Required so
	// GetActiveConnection / GetActiveArgocdClient resolve.
	admin.PostJSON(t, "/api/v1/connections/active",
		models.SetActiveConnectionRequest{ConnectionName: cfg.Name}, nil)

	t.Logf("harness: seeded + activated connection %q (argocd=%s)", cfg.Name, cfg.ArgocdServerURL)
	return cfg.Name
}

// SeedAddonsCatalog writes a minimal addons-catalog.yaml to the mock so
// AddonService.GetCatalog / GetAddonDetail resolves the addons the values
// tests act on. The catalog uses real public Helm charts so flows that
// need to hit the upstream values.yaml (preview-merge, AI annotate) work
// against actual charts — the mock only serves the catalog itself.
//
// Defaults to a single cert-manager entry pinned at v1.16.3 (matches the
// Sharko bootstrap template). Callers can append entries via the optional
// extras parameter — each entry is a fully-formed YAML block under
// `applicationsets:`.
func SeedAddonsCatalog(t *testing.T, mock *MockGitProvider, extras ...string) {
	t.Helper()
	if mock == nil {
		t.Fatalf("SeedAddonsCatalog: mock is nil")
	}
	const certManagerEntry = `  - name: cert-manager
    repoURL: https://charts.jetstack.io
    chart: cert-manager
    version: "1.16.3"
    namespace: cert-manager
    syncWave: -1
`
	yaml := "applicationsets:\n" + certManagerEntry
	for _, e := range extras {
		yaml += e
	}

	if err := mock.CreateOrUpdateFile(context.Background(),
		"configuration/addons-catalog.yaml", []byte(yaml), "main", "seed catalog"); err != nil {
		t.Fatalf("SeedAddonsCatalog: write addons-catalog.yaml: %v", err)
	}

	// Also seed an empty managed-clusters.yaml so AddonService.GetCatalog
	// doesn't trip on the cluster-list parse path (it tolerates missing
	// files via 404-substring detection, but we'd rather have a clean,
	// predictable artefact on the mock).
	if !mock.FileExists("main", "configuration/managed-clusters.yaml") {
		if err := mock.CreateOrUpdateFile(context.Background(),
			"configuration/managed-clusters.yaml",
			[]byte("clusters: []\n"), "main", "seed clusters"); err != nil {
			t.Fatalf("SeedAddonsCatalog: write managed-clusters.yaml: %v", err)
		}
	}
}

// SeedManagedCluster appends a cluster entry to managed-clusters.yaml so
// per-cluster values tests have a registered target. The labels map enables
// the named addon on the cluster — same shape sharko's catalog parser
// expects.
//
// Idempotent: when the cluster name already exists in the file, this
// overwrites its entry rather than appending a duplicate.
func SeedManagedCluster(t *testing.T, mock *MockGitProvider, clusterName string, addons map[string]string) {
	t.Helper()
	if mock == nil {
		t.Fatalf("SeedManagedCluster: mock is nil")
	}
	if clusterName == "" {
		t.Fatalf("SeedManagedCluster: empty cluster name")
	}

	// Build a minimal cluster entry. We don't try to merge with an
	// existing file because per-cluster values tests run with a clean
	// catalog seed — the simplest contract is "this is the cluster list".
	entry := "clusters:\n  - name: " + clusterName + "\n"
	if len(addons) > 0 {
		entry += "    labels:\n"
		for k, v := range addons {
			entry += "      " + k + ": " + v + "\n"
		}
	}

	if err := mock.CreateOrUpdateFile(context.Background(),
		"configuration/managed-clusters.yaml",
		[]byte(entry), "main", "seed cluster "+clusterName); err != nil {
		t.Fatalf("SeedManagedCluster: %v", err)
	}
}
