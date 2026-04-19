// Package api — values editor endpoints (v1.20).
//
// Four endpoints power the in-app values editor:
//
//   PUT  /api/v1/addons/{name}/values                     — Tier 2: edit global values
//   PUT  /api/v1/clusters/{cluster}/addons/{name}/values  — Tier 2: edit per-cluster overrides
//   GET  /api/v1/addons/{name}/values-schema              — read: current values + optional JSON schema
//   GET  /api/v1/clusters/{cluster}/addons/{name}/values  — read: current overrides + optional schema
//
// The two PUTs both follow the same flow:
//   1. Resolve a Git provider via Server.GitProviderForTier(ctx, r, audit.Tier2),
//      which returns the right token (per-user PAT preferred, service token
//      fallback) and stamps audit attribution metadata.
//   2. Write the new YAML through the orchestrator's commit helpers, which
//      open a PR (and optionally auto-merge per the active connection's
//      gitops.PRAutoMerge setting).
//   3. Wrap the response with withAttributionWarning so the UI gets the
//      "no_per_user_pat" nudge signal when Tier 2 fell back to the service
//      token.
//
// The editor explicitly accepts the *full* file contents for global values
// (not a diff) so the resulting Git PR shows a clean before/after for human
// review. The per-cluster endpoint accepts only the addon's section — the
// orchestrator merges it into the existing per-cluster overrides file,
// preserving other addons and `clusterGlobalValues:`.

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/helm"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/prtracker"

	"gopkg.in/yaml.v3"
)

// setAddonValuesRequest is the body of PUT /api/v1/addons/{name}/values.
//
// v1.21 (V121-6.4) extension: when `RefreshFromUpstream` is true, the
// handler ignores any client-supplied `Values`, fetches the chart's
// upstream `values.yaml` for the version pinned in `addons-catalog.yaml`,
// runs the smart-values pipeline, and overwrites the global values file
// with the regenerated content. This replaces the dedicated
// `POST /api/v1/addons/{name}/values/pull-upstream` endpoint that v1.20.1
// shipped — see the locked decision in `epics-v1.21.md` Story V121-6.4.
type setAddonValuesRequest struct {
	// Values is the full YAML file content. Sharko commits this verbatim
	// as the new global default values for the addon. Ignored when
	// RefreshFromUpstream is true.
	Values string `json:"values"`

	// RefreshFromUpstream, when true, regenerates the global values file
	// from the chart's upstream values.yaml at the catalog-pinned version.
	// The audit event for this path is `values_refreshed_from_upstream`
	// (overriding the default `values_set`) so audit consumers can
	// distinguish manual edits from upstream refreshes.
	RefreshFromUpstream bool `json:"refresh_from_upstream,omitempty"`
}

// setClusterAddonValuesRequest is the body of
// PUT /api/v1/clusters/{cluster}/addons/{name}/values. Values here is the
// YAML for the addon's section in the cluster overrides file (NOT the
// whole file). Pass an empty string to remove the addon's overrides.
type setClusterAddonValuesRequest struct {
	Values string `json:"values"`
}

// handleSetAddonValues godoc
//
// @Summary Edit global addon values
// @Description Replaces the global default Helm values YAML for an addon and opens a PR. Tier 2 (configuration) — prefers the caller's personal GitHub PAT for proper Git authorship; otherwise falls back to the service token with a `Co-authored-by:` trailer and surfaces `attribution_warning: "no_per_user_pat"` in the response.
// @Tags addons
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Addon name"
// @Param body body api.setAddonValuesRequest true "Full YAML payload"
// @Success 200 {object} map[string]interface{} "PR created (or merged if auto-merge is on)"
// @Failure 400 {object} map[string]interface{} "Invalid YAML or missing field"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 404 {object} map[string]interface{} "Addon not found"
// @Failure 502 {object} map[string]interface{} "Git or ArgoCD failure"
// @Router /addons/{name}/values [put]
func (s *Server) handleSetAddonValues(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "addon.update-catalog") {
		return
	}

	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "addon name is required")
		return
	}

	var req setAddonValuesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	// Validate YAML only on the manual-edit path. The refresh path
	// generates its own content from the smart-values pipeline so any
	// user-supplied `values` field is ignored.
	if !req.RefreshFromUpstream {
		if err := yaml.Unmarshal([]byte(req.Values), new(interface{})); err != nil {
			writeError(w, http.StatusBadRequest, "invalid YAML: "+err.Error())
			return
		}
	}

	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active ArgoCD connection: "+err.Error())
		return
	}

	// Tier 2: configuration change — prefer per-user PAT, fall back to service token.
	ctx, git, tokRes, err := s.GitProviderForTier(r.Context(), r, audit.Tier2)
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active Git connection: "+err.Error())
		return
	}

	// Existence check: surface a 404 instead of a confusing PR-against-an-unknown-addon.
	addon, gerr := s.addonSvc.GetAddonDetail(ctx, name, git, ac)
	if gerr != nil {
		writeError(w, http.StatusBadGateway, "looking up addon: "+gerr.Error())
		return
	}
	if addon == nil {
		writeError(w, http.StatusNotFound, "addon not found in catalog: "+name)
		return
	}

	// Refresh path (V121-6.4): regenerate from the upstream chart's
	// values.yaml at the catalog-pinned version, then commit. This
	// replaces the dedicated pull-upstream endpoint deleted in V121-6.5.
	yamlPayload := req.Values
	auditEvent := "addon_values_edited"
	prTitle := "Update global values for " + name
	prOperation := "values-edit"
	auditDetail := fmt.Sprintf("file=configuration/addons-global-values/%s.yaml bytes=%d", name, len(req.Values))

	if req.RefreshFromUpstream {
		chart := addon.Addon.Chart
		repoURL := addon.Addon.RepoURL
		version := addon.Addon.Version
		if chart == "" || repoURL == "" || version == "" {
			writeError(w, http.StatusBadRequest, "addon catalog entry is missing chart/repo/version metadata required to refresh from upstream")
			return
		}
		raw, ferr := helm.NewFetcher().FetchValues(ctx, repoURL, chart, version)
		if ferr != nil {
			writeError(w, http.StatusBadGateway, "fetching upstream values: "+ferr.Error())
			return
		}

		// Per-addon AI opt-out: read the existing file's header to see if
		// the user previously set `# sharko: ai-annotate=off`. If so, the
		// refresh skips the AI pass and stamps the directive again so it
		// survives the regen.
		aiOptOut := false
		dir := strings.TrimSuffix(s.repoPaths.GlobalValues, "/")
		if dir == "" {
			dir = "configuration/addons-global-values"
		}
		valuesFile := dir + "/" + name + ".yaml"
		if existing, gerr := git.GetFileContent(ctx, valuesFile, s.gitopsCfg.BaseBranch); gerr == nil && len(existing) > 0 {
			if h := orchestrator.ParseSmartValuesHeader(existing); h.AIOptOut {
				aiOptOut = true
			}
		}

		// V121-7 AI annotate on refresh: same rules as the addon-add seed
		// flow. AI off / opted out / secret-blocked → heuristic-only.
		var (
			extraPaths  []string
			aiAnnotated bool
			secretBlock *orchestrator.SecretLeakError
		)
		valuesBytes := []byte(raw)
		if !aiOptOut && s.aiClient != nil && s.aiClient.AnnotateOnSeedEnabled() {
			annRes, annErr := orchestrator.AnnotateValues(ctx, valuesBytes, chart, version, s.aiClient)
			if annErr != nil {
				if errors.As(annErr, &secretBlock) {
					slog.Warn("values refresh: ai annotate hard-blocked by secret guard; proceeding with heuristic-only",
						"addon", name, "chart", chart, "version", version,
						"matches", len(secretBlock.Matches),
					)
					// Story V121-8.5: emit a dedicated
					// `secret_leak_blocked` audit entry so security review
					// can grep across handlers without parsing per-event
					// detail strings.
					s.emitSecretLeakAuditBlock(ctx, "values_refresh", name, chart, version, secretBlock.Matches)
				}
			}
			if annRes.SkipReason == "" {
				valuesBytes = annRes.AnnotatedYAML
				extraPaths = annRes.AdditionalClusterPaths
				aiAnnotated = true
			}
		}

		generated := orchestrator.GenerateGlobalValuesFile(name, chart, version, repoURL, valuesBytes, aiAnnotated, aiOptOut, extraPaths...)
		yamlPayload = string(generated)
		auditEvent = "values_refreshed_from_upstream"
		prTitle = fmt.Sprintf("Refresh upstream values for %s@%s", chart, version)
		prOperation = "values-refresh-upstream"
		aiState := "off"
		if aiAnnotated {
			aiState = "on"
		} else if secretBlock != nil {
			aiState = "secret_blocked"
		}
		auditDetail = fmt.Sprintf("chart=%s version=%s bytes=%d ai=%s", chart, version, len(generated), aiState)
	}

	orch := orchestrator.New(&s.gitMu, nil, ac, git, s.gitopsCfg, s.repoPaths, nil)
	result, err := orch.SetGlobalAddonValues(ctx, name, yamlPayload)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	if s.prTracker != nil && result != nil && result.PRID > 0 {
		user := r.Header.Get("X-Sharko-User")
		if user == "" {
			user = "system"
		}
		_ = s.prTracker.TrackPR(ctx, prtracker.PRInfo{
			PRID:       result.PRID,
			PRUrl:      result.PRUrl,
			PRBranch:   result.Branch,
			PRTitle:    prTitle,
			PRBase:     "main",
			Addon:      name,
			Operation:  prOperation,
			User:       user,
			Source:     "api",
			CreatedAt:  time.Now(),
			LastStatus: "open",
		})
	}

	audit.Enrich(ctx, audit.Fields{
		Event:    auditEvent,
		Resource: fmt.Sprintf("addon:%s", name),
		Detail:   auditDetail,
	})

	writeJSON(w, http.StatusOK, withAttributionWarning(result, tokRes))
}

// handleSetClusterAddonValues godoc
//
// @Summary Edit per-cluster addon overrides
// @Description Replaces the per-cluster overrides YAML for one addon on one cluster and opens a PR. Pass an empty `values` string to remove the addon's overrides entirely. Tier 2 (configuration) — same attribution semantics as the global values endpoint.
// @Tags clusters
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param cluster path string true "Cluster name"
// @Param name path string true "Addon name"
// @Param body body api.setClusterAddonValuesRequest true "Addon-section YAML payload"
// @Success 200 {object} map[string]interface{} "PR created (or merged if auto-merge is on)"
// @Failure 400 {object} map[string]interface{} "Invalid YAML or missing field"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 502 {object} map[string]interface{} "Git or ArgoCD failure"
// @Router /clusters/{cluster}/addons/{name}/values [put]
func (s *Server) handleSetClusterAddonValues(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "addon.update-catalog") {
		return
	}

	cluster := r.PathValue("cluster")
	if cluster == "" {
		writeError(w, http.StatusBadRequest, "cluster name is required")
		return
	}
	addonName := r.PathValue("name")
	if addonName == "" {
		writeError(w, http.StatusBadRequest, "addon name is required")
		return
	}

	var req setClusterAddonValuesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Values) != "" {
		if err := yaml.Unmarshal([]byte(req.Values), new(interface{})); err != nil {
			writeError(w, http.StatusBadRequest, "invalid YAML: "+err.Error())
			return
		}
	}

	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active ArgoCD connection: "+err.Error())
		return
	}

	ctx, git, tokRes, err := s.GitProviderForTier(r.Context(), r, audit.Tier2)
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active Git connection: "+err.Error())
		return
	}

	orch := orchestrator.New(&s.gitMu, nil, ac, git, s.gitopsCfg, s.repoPaths, nil)
	result, err := orch.SetClusterAddonValues(ctx, cluster, addonName, req.Values)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	if s.prTracker != nil && result != nil && result.PRID > 0 {
		user := r.Header.Get("X-Sharko-User")
		if user == "" {
			user = "system"
		}
		_ = s.prTracker.TrackPR(ctx, prtracker.PRInfo{
			PRID:       result.PRID,
			PRUrl:      result.PRUrl,
			PRBranch:   result.Branch,
			PRTitle:    fmt.Sprintf("Update %s overrides on cluster %s", addonName, cluster),
			PRBase:     "main",
			Cluster:    cluster,
			Addon:      addonName,
			Operation:  "values-edit",
			User:       user,
			Source:     "api",
			CreatedAt:  time.Now(),
			LastStatus: "open",
		})
	}

	audit.Enrich(ctx, audit.Fields{
		Event:    "cluster_addon_values_edited",
		Resource: fmt.Sprintf("cluster:%s addon:%s", cluster, addonName),
		Detail:   fmt.Sprintf("file=%s bytes=%d", result.ValuesFile, len(req.Values)),
	})

	writeJSON(w, http.StatusOK, withAttributionWarning(result, tokRes))
}

// handleGetAddonValuesSchema godoc
//
// @Summary Get addon global values + schema
// @Description Returns the addon's current global values YAML and an optional JSON Schema (read from `<addon>.schema.json` next to the values file). The schema is omitted when none has been published; the UI then falls back to plain YAML mode.
// @Tags addons
// @Produce json
// @Security BearerAuth
// @Param name path string true "Addon name"
// @Success 200 {object} map[string]interface{} "Current values + optional schema"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 502 {object} map[string]interface{} "Git failure"
// @Router /addons/{name}/values-schema [get]
func (s *Server) handleGetAddonValuesSchema(w http.ResponseWriter, r *http.Request) {
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

	resp, err := s.addonSvc.GetAddonValuesAndSchema(r.Context(), name, gp)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	// V121-6.5: version-mismatch detection. If the values file has a
	// smart-values header with `sharko: managed=true` and the chart
	// version it was generated against doesn't match the catalog's
	// current pin, we surface the mismatch so the UI can render the
	// refresh banner. This is best-effort — any failure to look up the
	// catalog (no ArgoCD, transient Git error) just suppresses the
	// banner; the values themselves still render.
	if resp != nil && resp.CurrentValues != "" {
		header := orchestrator.ParseSmartValuesHeader([]byte(resp.CurrentValues))
		// V121-7.4: surface the header-stored AI annotation state so the
		// UI can render the "AI not configured" banner and the per-addon
		// opt-out toggle without re-parsing the YAML on the client.
		resp.AIAnnotated = header.AIAnnotated
		resp.AIOptOut = header.AIOptOut

		if header.Managed {
			if ac, acErr := s.connSvc.GetActiveArgocdClient(); acErr == nil {
				if detail, derr := s.addonSvc.GetAddonDetail(r.Context(), name, gp, ac); derr == nil && detail != nil {
					if catalog, values := orchestrator.VersionMismatch(detail.Addon.Version, header); catalog != "" {
						resp.ValuesVersionMismatch = &models.ValuesVersionMismatch{
							CatalogVersion: catalog,
							ValuesVersion:  values,
						}
					}
				}
			}
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleGetClusterAddonValues godoc
//
// @Summary Get per-cluster addon overrides + schema
// @Description Returns the YAML for a single addon's section in a cluster's overrides file, plus an optional JSON Schema. `current_overrides` is empty when no overrides are configured for this addon yet.
// @Tags clusters
// @Produce json
// @Security BearerAuth
// @Param cluster path string true "Cluster name"
// @Param name path string true "Addon name"
// @Success 200 {object} map[string]interface{} "Current overrides + optional schema"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 502 {object} map[string]interface{} "Git failure"
// @Router /clusters/{cluster}/addons/{name}/values [get]
func (s *Server) handleGetClusterAddonValues(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "cluster.detail") {
		return
	}

	cluster := r.PathValue("cluster")
	if cluster == "" {
		writeError(w, http.StatusBadRequest, "cluster name is required")
		return
	}
	addonName := r.PathValue("name")
	if addonName == "" {
		writeError(w, http.StatusBadRequest, "addon name is required")
		return
	}

	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	resp, err := s.clusterSvc.GetClusterAddonValues(r.Context(), cluster, addonName, gp)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, resp)
}
