// Package api — values diff-and-merge preview (v1.21 QA Bundle 4 Fix #4).
//
// Endpoint: POST /api/v1/addons/{name}/values/preview-merge
// Tier:     Tier 1 (read-only — does NOT open a PR; the caller submits the
//           merged YAML through the existing PUT values endpoint to commit).
//
// Why this exists (separate from "Refresh from upstream"):
//
//   - The existing PUT /api/v1/addons/{name}/values?refresh_from_upstream=true
//     REPLACES the file with a freshly-generated smart-values output. That's
//     the right tool when the chart got upgraded and the file is stale, but
//     it loses the user's customizations.
//
//   - This new "preview merge" returns a candidate body that takes upstream's
//     STRUCTURE and DEFAULTS for any keys the user hasn't overridden, but
//     PRESERVES every key the user already set. The UI shows the diff, the
//     user approves, and a separate PUT call (no special flag) opens the
//     PR with the merged body.
//
// The merge is purely additive: the only changes vs current are NEW keys
// from upstream. Existing keys keep their current values regardless of how
// upstream's default has changed — the human reviews on the PR page.
//
// Locked decision (Moran): keep this on the same handler surface as values
// editing (Tier 1 read for the preview, Tier 2 write for the eventual PUT).
// No new write endpoint, no fragmented audit surface.

package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/helm"
	"github.com/MoranWeissman/sharko/internal/orchestrator"

	"gopkg.in/yaml.v3"
)

// previewMergeResponse is the wire shape for POST /api/v1/addons/{name}/values/preview-merge.
type previewMergeResponse struct {
	// Current is the existing global values file body, exactly as returned
	// by the GET values-schema endpoint. Returned so the UI can render a
	// before/after diff without a second request.
	Current string `json:"current"`

	// Merged is the candidate body the user can submit through the existing
	// PUT /api/v1/addons/{name}/values endpoint. Same shape as Current
	// (full YAML with smart-values header) plus any new upstream keys the
	// user did not previously set.
	Merged string `json:"merged"`

	// DiffSummary is an at-a-glance view of what changed. Used by the modal
	// header so the user can decide whether to open the diff or skip.
	DiffSummary previewMergeSummary `json:"diff_summary"`

	// UpstreamVersion is the chart version Sharko fetched the upstream
	// values.yaml for. Echo of the catalog pin — surfaced so the UI can
	// show "Merging in defaults from cert-manager@1.20.2".
	UpstreamVersion string `json:"upstream_version"`
}

// previewMergeSummary surfaces the headline counts.
type previewMergeSummary struct {
	// NewKeys are dotted paths present in upstream but NOT in the user's
	// current file. These are the only paths that the merge actually adds.
	NewKeys []string `json:"new_keys"`

	// PreservedUserKeys are dotted paths the user has set that already
	// exist in upstream — the merge keeps the user's value and the user's
	// inline comments are honored on the PR side. Surfaced so the modal
	// can reassure the user nothing of theirs is being overwritten.
	PreservedUserKeys []string `json:"preserved_user_keys"`

	// NoOp is true when the merge produced the same body as Current — the
	// user already has every upstream key. The UI uses this to render a
	// "Already up to date" empty state instead of an empty diff.
	NoOp bool `json:"no_op"`
}

// handlePreviewMergeAddonValues godoc
//
// @Summary Preview a safe merge of upstream values into an addon's values file
// @Description Returns a candidate values YAML that adds NEW upstream keys (and their default values) on top of the user's current file, preserving every key the user has already set. The endpoint is Tier 1 read-only — it does NOT open a PR. Submit the returned `merged` body through PUT /api/v1/addons/{name}/values to commit.
// @Tags addons
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Addon name"
// @Success 200 {object} api.previewMergeResponse
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 404 {object} map[string]interface{} "Addon not found"
// @Failure 502 {object} map[string]interface{} "Git or Helm-repo failure"
// @Router /addons/{name}/values/preview-merge [post]
func (s *Server) handlePreviewMergeAddonValues(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "addon.list") {
		return
	}

	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "addon name is required")
		return
	}

	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active ArgoCD connection: "+err.Error())
		return
	}

	addon, gerr := s.addonSvc.GetAddonDetail(r.Context(), name, gp, ac)
	if gerr != nil {
		writeError(w, http.StatusBadGateway, "looking up addon: "+gerr.Error())
		return
	}
	if addon == nil {
		writeError(w, http.StatusNotFound, "addon not found in catalog: "+name)
		return
	}

	chart := addon.Addon.Chart
	repoURL := addon.Addon.RepoURL
	version := addon.Addon.Version
	if chart == "" || repoURL == "" || version == "" {
		writeError(w, http.StatusBadRequest, "addon catalog entry is missing chart/repo/version metadata required to preview a merge")
		return
	}

	// Read the user's current global values file. Empty body is OK —
	// the merge will then look like a full upstream import.
	dir := strings.TrimSuffix(s.repoPaths.GlobalValues, "/")
	if dir == "" {
		dir = "configuration/addons-global-values"
	}
	valuesFile := dir + "/" + name + ".yaml"
	current, _ := gp.GetFileContent(r.Context(), valuesFile, s.gitopsCfg.BaseBranch)

	// Pull the chart's upstream values.yaml at the catalog-pinned version.
	upstreamRaw, ferr := helm.NewFetcher().FetchValues(r.Context(), repoURL, chart, version)
	if ferr != nil {
		writeError(w, http.StatusBadGateway, "fetching upstream values: "+ferr.Error())
		return
	}
	upstream := []byte(upstreamRaw)
	// Apply the same chart-name unwrap as the smart-values writer so a
	// `velero:`-rooted upstream doesn't get double-wrapped at merge time.
	// This unwrap is internal to the merge engine — the response body
	// re-wraps the addon-name root the same way SetGlobalAddonValues does.
	upstream = orchestrator.UnwrapChartNameRoot(upstream, name, chart)

	// Run the additive merge.
	mergedBody, summary, mergeErr := previewMergeBodies(name, current, upstream)
	if mergeErr != nil {
		writeError(w, http.StatusBadGateway, "merging values: "+mergeErr.Error())
		return
	}

	// NOTE: this endpoint does NOT call audit.Enrich — it's a read-only
	// preview registered as Tier 1 read in the tier registry / pattern
	// table. POST is used purely because the response body benefits from
	// caching avoidance and the future evolution might accept a body
	// (e.g. "preview merging in this version instead of the catalog pin").

	resp := previewMergeResponse{
		Current:         string(current),
		Merged:          mergedBody,
		DiffSummary:     summary,
		UpstreamVersion: version,
	}
	writeJSON(w, http.StatusOK, resp)
}

// previewMergeBodies takes the user's current global values file (as written
// by the smart-values pipeline — `<addonName>: { … }` with optional header
// and per-cluster template) and the chart's upstream values.yaml (already
// chart-name-unwrapped if applicable). Returns the merged body to commit
// plus a summary of what changed.
//
// Merge rules:
//   - If a key path exists in user, keep user's value.
//   - If a key path exists in upstream but not in user, add upstream's
//     default value at that path.
//   - Top-level keys outside the `<addonName>:` wrapper (e.g. user-added
//     `clusterGlobalValues:`) are preserved as-is.
//
// The merge round-trips through map[string]interface{} so YAML comments
// from the user's file are LOST in the output. That's intentional: the
// merge surface is a preview the user reviews on the PR page, and the
// PR diff makes the change set obvious. We do preserve the smart-values
// header by detecting it textually and re-prepending it to the output.
func previewMergeBodies(addonName string, currentRaw, upstreamRaw []byte) (string, previewMergeSummary, error) {
	header, currentBody := splitSmartValuesHeader(currentRaw)

	// Decode the user's current values file.
	currentRoot := map[string]interface{}{}
	if len(strings.TrimSpace(string(currentBody))) > 0 {
		if err := yaml.Unmarshal(currentBody, &currentRoot); err != nil {
			return "", previewMergeSummary{}, fmt.Errorf("parsing current values: %w", err)
		}
		if currentRoot == nil {
			currentRoot = map[string]interface{}{}
		}
	}

	// The user's file is wrapped under `<addonName>:`. Pull the inner map
	// out so the merge operates on the chart's values shape.
	var currentInner map[string]interface{}
	if v, ok := currentRoot[addonName]; ok {
		if m, ok := v.(map[string]interface{}); ok {
			currentInner = m
		}
	}
	if currentInner == nil {
		currentInner = map[string]interface{}{}
	}

	// Decode upstream — already unwrapped of any chart-name root by the
	// caller.
	var upstreamInner interface{}
	if err := yaml.Unmarshal(upstreamRaw, &upstreamInner); err != nil {
		return "", previewMergeSummary{}, fmt.Errorf("parsing upstream values: %w", err)
	}
	upstreamMap, _ := upstreamInner.(map[string]interface{})
	if upstreamMap == nil {
		upstreamMap = map[string]interface{}{}
	}

	// Run the recursive additive merge.
	var (
		newKeys      []string
		preserved    []string
	)
	merged := mergeAdditive(currentInner, upstreamMap, "", &newKeys, &preserved)

	// Re-wrap under the addon name so the file shape on disk stays the
	// same as the writer convention.
	wrapped := map[string]interface{}{addonName: merged}
	// Preserve any user-added top-level keys outside the addon wrapper
	// (e.g. clusterGlobalValues: from a misconfigured copy/paste).
	for k, v := range currentRoot {
		if k == addonName {
			continue
		}
		wrapped[k] = v
	}

	out, err := yaml.Marshal(wrapped)
	if err != nil {
		return "", previewMergeSummary{}, fmt.Errorf("serializing merged values: %w", err)
	}

	// Re-prepend the header (if there was one). The header is purely
	// metadata for the smart-values machinery — keep the existing
	// `# Generated by Sharko from <chart>@<version>` line so the
	// version-mismatch banner stays in sync.
	body := string(out)
	if len(header) > 0 {
		body = header + body
	}

	sort.Strings(newKeys)
	sort.Strings(preserved)
	summary := previewMergeSummary{
		NewKeys:           newKeys,
		PreservedUserKeys: preserved,
		NoOp:              len(newKeys) == 0,
	}
	return body, summary, nil
}

// splitSmartValuesHeader returns the leading comment header (a contiguous
// block of comment-only lines starting with `# Generated by Sharko`) and
// the rest of the body. When the file has no smart-values header, returns
// ("", input).
func splitSmartValuesHeader(content []byte) (string, []byte) {
	if len(content) == 0 {
		return "", content
	}
	lines := strings.Split(string(content), "\n")
	headerEnd := 0
	for i, raw := range lines {
		trim := strings.TrimSpace(raw)
		if trim == "" {
			headerEnd = i + 1
			continue
		}
		if !strings.HasPrefix(trim, "#") {
			break
		}
		// Conservative: the smart-values writer's last header line is
		// `# sharko: managed=true` (or the optional `# sharko: ai-annotate=off`).
		// Once we pass it AND see a blank line OR a non-comment line, stop.
		headerEnd = i + 1
	}
	if headerEnd == 0 {
		return "", content
	}
	header := strings.Join(lines[:headerEnd], "\n")
	if !strings.HasSuffix(header, "\n") {
		header += "\n"
	}
	rest := strings.Join(lines[headerEnd:], "\n")
	// Re-anchor blank line between header and body so re-marshaling
	// produces the same shape as smart-values writes natively.
	if !strings.HasPrefix(rest, "\n") {
		header = strings.TrimRight(header, "\n") + "\n"
	}
	return header, []byte(rest)
}

// mergeAdditive recursively merges upstream into current. Only keys NOT
// present in current are added; existing keys are kept verbatim. Maps are
// recursed into; non-map leaves are atomic (we never re-shape a user value).
//
// Tracks the dotted path of each addition into `newKeys` and each preserved
// override into `preserved` so the response can summarise the diff for the UI.
func mergeAdditive(
	current map[string]interface{},
	upstream map[string]interface{},
	prefix string,
	newKeys *[]string,
	preserved *[]string,
) map[string]interface{} {
	out := map[string]interface{}{}
	// Start with everything the user already has.
	for k, v := range current {
		out[k] = v
	}
	for k, v := range upstream {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		existing, hadIt := current[k]
		if !hadIt {
			out[k] = v
			collectLeafPaths(v, path, newKeys)
			continue
		}
		// Both sides have the key. If both are maps, recurse — otherwise
		// keep the user's value verbatim.
		curMap, curIsMap := existing.(map[string]interface{})
		upMap, upIsMap := v.(map[string]interface{})
		if curIsMap && upIsMap {
			out[k] = mergeAdditive(curMap, upMap, path, newKeys, preserved)
		} else {
			*preserved = append(*preserved, path)
		}
	}
	return out
}

// collectLeafPaths walks a yaml.Unmarshal-shaped value and appends every
// scalar leaf's dotted path to `out`. Used so the summary lists *every*
// new path the merge introduced, not just the top-level new key — the user
// wants to know e.g. that `controller.metrics.serviceMonitor.enabled` is
// new, not just that `controller:` got a child.
func collectLeafPaths(v interface{}, prefix string, out *[]string) {
	switch t := v.(type) {
	case map[string]interface{}:
		for k, child := range t {
			child := child
			path := prefix + "." + k
			collectLeafPaths(child, path, out)
		}
	case []interface{}:
		// Lists are atomic from the merge's perspective — record the
		// list's path itself so the user can see "this key got added"
		// without flooding the summary with `[0]`, `[1]`, … noise.
		*out = append(*out, prefix)
	default:
		*out = append(*out, prefix)
	}
}

// allow the JSON encoder to round-trip the existing summary type without an
// extra typed alias.
var _ = json.Marshal
