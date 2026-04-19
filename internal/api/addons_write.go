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
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/prtracker"
)

// handleAddAddon godoc
//
// @Summary Add addon
// @Description Adds a new addon to the catalog by creating its ApplicationSet in the GitOps repo
// @Tags addons
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body orchestrator.AddAddonRequest true "Add addon request"
// @Success 201 {object} map[string]interface{} "Addon created"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 409 {object} map[string]interface{} "Addon already exists in catalog"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /addons [post]
// handleAddAddon handles POST /api/v1/addons — add a new addon to the catalog.
func (s *Server) handleAddAddon(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "addon.add-to-catalog") {
		return
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

	var req orchestrator.AddAddonRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "addon name is required")
		return
	}
	if req.Chart == "" {
		writeError(w, http.StatusBadRequest, "addon chart is required")
		return
	}
	if req.RepoURL == "" {
		writeError(w, http.StatusBadRequest, "addon repo_url is required")
		return
	}
	if req.Version == "" {
		writeError(w, http.StatusBadRequest, "addon version is required")
		return
	}

	// V121-6 smart-values seeding: best-effort pre-fetch of the chart's
	// upstream values.yaml so AddAddon can write an annotated global values
	// file with a per-cluster template. On any fetch failure (network,
	// chart unreachable, version not found) we silently fall back to the
	// pre-v1.21 minimal stub — the user can always Refresh from upstream
	// later via V121-6.4 once connectivity is restored. This decision
	// keeps Add Addon non-blocking on flaky upstream registries.
	if req.RepoURL != "" && req.Chart != "" && req.Version != "" {
		if upstream, ferr := helm.NewFetcher().FetchValues(ctx, req.RepoURL, req.Chart, req.Version); ferr == nil {
			req.UpstreamValues = []byte(upstream)
		} else {
			slog.Info("smart-values pre-fetch failed; falling back to minimal stub",
				"addon", req.Name, "chart", req.Chart, "version", req.Version, "error", ferr)
		}
	}

	// V121-7 AI annotate: when AI is configured AND the global Settings
	// toggle is on AND the per-addon opt-out is NOT set, run the LLM
	// pass to (a) inject inline `# description` comments and (b) augment
	// the heuristic's cluster-specific path set. Hard secret-leak guard
	// runs first; on a match the call is blocked and the seed continues
	// with heuristic-only output (the UI surfaces the block via the
	// returned `secret_detected_blocked` warning so the user knows their
	// chart values look secret-bearing).
	//
	// Failure modes are graceful — see ai_annotate.go. AI is best-effort
	// and never fails the addon-add. The only error we surface to the
	// caller is the SecretLeakError (rendered as a banner, not a toast).
	var secretBlock *orchestrator.SecretLeakError
	if len(req.UpstreamValues) > 0 && !req.AIOptOut && s.aiClient != nil && s.aiClient.AnnotateOnSeedEnabled() {
		annRes, annErr := orchestrator.AnnotateValues(ctx, req.UpstreamValues, req.Chart, req.Version, s.aiClient)
		if annErr != nil {
			// Only one error class is non-fatal-yet-surfaced: secret leak.
			// Everything else is logged inside AnnotateValues and a nil
			// error is returned.
			if errors.As(annErr, &secretBlock) {
				slog.Warn("addon-add: ai annotate hard-blocked by secret guard; proceeding with heuristic-only",
					"addon", req.Name, "chart", req.Chart, "version", req.Version,
					"matches", len(secretBlock.Matches),
				)
			}
		}
		if annRes.SkipReason == "" {
			req.UpstreamValues = annRes.AnnotatedYAML
			req.ExtraClusterSpecificPaths = annRes.AdditionalClusterPaths
			req.AIAnnotated = true
		}
	}

	orch := orchestrator.New(&s.gitMu, nil, ac, git, s.gitopsCfg, s.repoPaths, nil)
	result, err := orch.AddAddon(ctx, req)
	if err != nil {
		// Surface "already in catalog" as 409 with a structured body so the
		// Marketplace Configure modal can render a friendly inline error and
		// link to the existing addon. Locked decision (V121-5.1): the
		// duplicate path stays inside the existing handler — no separate
		// pre-flight endpoint.
		if strings.Contains(err.Error(), "already exists in catalog") {
			source := req.Source
			if source == "" {
				source = "manual"
			}
			audit.Enrich(ctx, audit.Fields{
				Event:    "addon_added",
				Resource: fmt.Sprintf("addon:%s", req.Name),
				Detail:   fmt.Sprintf("chart=%s version=%s source=%s result=duplicate", req.Chart, req.Version, source),
			})
			writeJSON(w, http.StatusConflict, map[string]interface{}{
				"error":        fmt.Sprintf("%s is already in your catalog", req.Name),
				"code":         "addon_already_exists",
				"addon":        req.Name,
				"existing_url": fmt.Sprintf("/addons/%s", req.Name),
			})
			return
		}
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
			PRTitle:    "Add addon " + req.Name,
			PRBase:     "main",
			Addon:      req.Name,
			Operation:  "addon-add",
			User:       user,
			Source:     "api",
			CreatedAt:  time.Now(),
			LastStatus: "open",
		})
	}

	// Audit detail uses key=value to stay grep-friendly. `source` defaults to
	// "manual" when the request body doesn't include it (raw Add Addon form
	// or older clients) and is "marketplace" when submitted via the V121-5
	// Configure modal. Locked decision 2026-04-19 (Moran): no new event name —
	// the source field on `addon_added` is the discriminator.
	source := req.Source
	if source == "" {
		source = "manual"
	}
	// Audit detail captures the AI annotation outcome so operators can
	// trace why a given file was/wasn't annotated. `ai=on` when the LLM
	// successfully annotated, `ai=secret_blocked` when the guard fired,
	// `ai=off` when AI was not configured / opted out / otherwise skipped.
	aiState := "off"
	if req.AIAnnotated {
		aiState = "on"
	} else if secretBlock != nil {
		aiState = "secret_blocked"
	}
	audit.Enrich(ctx, audit.Fields{
		Event:    "addon_added",
		Resource: fmt.Sprintf("addon:%s", req.Name),
		Detail:   fmt.Sprintf("chart=%s version=%s source=%s ai=%s", req.Chart, req.Version, source, aiState),
	})

	// Surface the UX nudge: when Tier 2 had no per-user PAT we fell back to
	// the service token + co-author trailer. The UI watches for
	// attribution_warning="no_per_user_pat" to render the banner.
	//
	// Secret-leak guard: when the AI annotate pass was hard-blocked by
	// a secret-like pattern in the chart values, surface the redacted
	// match list on the response so the UI can render the dedicated
	// banner. Locked decision (Moran): no override — the addon is still
	// added with heuristic-only annotation, but the operator sees the
	// reason explicitly.
	body := withAttributionWarning(result, tokRes)
	if secretBlock != nil {
		// Promote `body` to a map shape if it isn't already so the
		// secret-leak detail can ride alongside the result + any
		// attribution warning.
		bodyMap, ok := body.(map[string]interface{})
		if !ok {
			bodyMap = map[string]interface{}{"result": body}
		}
		bodyMap["ai_annotate_blocked"] = map[string]interface{}{
			"code":    secretBlock.Code(),
			"matches": secretBlock.Matches,
		}
		body = bodyMap
	}
	writeJSON(w, http.StatusCreated, body)
}

// handleRemoveAddon godoc
//
// @Summary Remove addon
// @Description Removes an addon from the catalog. Without ?confirm=true returns a dry-run impact report.
// @Tags addons
// @Produce json
// @Security BearerAuth
// @Param name path string true "Addon name"
// @Param confirm query string false "Set to 'true' to confirm destructive removal"
// @Success 200 {object} map[string]interface{} "Addon removed"
// @Failure 400 {object} map[string]interface{} "Confirmation required or bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 404 {object} map[string]interface{} "Addon not found"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /addons/{name} [delete]
// handleRemoveAddon handles DELETE /api/v1/addons/{name} — remove an addon.
// Requires ?confirm=true query parameter. Without it, returns a dry-run impact report.
func (s *Server) handleRemoveAddon(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "addon.remove-from-catalog") {
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "addon name is required")
		return
	}

	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active ArgoCD connection: "+err.Error())
		return
	}

	// Tier 2: configuration change.
	ctx, git, tokRes, err := s.GitProviderForTier(r.Context(), r, audit.Tier2)
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active Git connection: "+err.Error())
		return
	}

	// Without ?confirm=true, return a dry-run impact report.
	if r.URL.Query().Get("confirm") != "true" {
		catalog, err := s.addonSvc.GetCatalog(ctx, git, ac)
		if err != nil {
			writeError(w, http.StatusBadGateway, "failed to fetch addon catalog: "+err.Error())
			return
		}

		affectedClusters := []string{}
		found := false
		for _, addon := range catalog.Addons {
			if addon.AddonName != name {
				continue
			}
			found = true
			for _, app := range addon.Applications {
				affectedClusters = append(affectedClusters, app.ClusterName)
			}
		}
		if !found {
			writeError(w, http.StatusNotFound, "addon not found in catalog: "+name)
			return
		}

		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "destructive operation requires ?confirm=true",
			"impact": map[string]interface{}{
				"addon":                        name,
				"affected_clusters":            affectedClusters,
				"total_deployments_to_remove":  len(affectedClusters),
				"warning":                      "ArgoCD will cascade-delete " + name + " from all affected clusters when the ApplicationSet entry is removed.",
			},
		})
		return
	}

	orch := orchestrator.New(&s.gitMu, nil, ac, git, s.gitopsCfg, s.repoPaths, nil)
	result, err := orch.RemoveAddon(ctx, name)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	audit.Enrich(ctx, audit.Fields{
		Event:    "addon_removed",
		Resource: fmt.Sprintf("addon:%s", name),
	})
	writeJSON(w, http.StatusOK, withAttributionWarning(result, tokRes))
}

// handleConfigureAddon godoc
//
// @Summary Configure addon
// @Description Updates an addon's catalog configuration. Only provided fields are modified (merge semantics).
// @Tags addons
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Addon name"
// @Param body body orchestrator.ConfigureAddonRequest true "Configuration update"
// @Success 200 {object} map[string]interface{} "Addon configured"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 404 {object} map[string]interface{} "Addon not found"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /addons/{name} [patch]
func (s *Server) handleConfigureAddon(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "addon.update-catalog") {
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "addon name is required")
		return
	}

	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active ArgoCD connection: "+err.Error())
		return
	}

	// Tier 2: configuration change.
	ctx, git, tokRes, err := s.GitProviderForTier(r.Context(), r, audit.Tier2)
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active Git connection: "+err.Error())
		return
	}

	var req orchestrator.ConfigureAddonRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	req.Name = name

	orch := orchestrator.New(&s.gitMu, nil, ac, git, s.gitopsCfg, s.repoPaths, nil)
	result, err := orch.ConfigureAddon(ctx, req)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	audit.Enrich(ctx, audit.Fields{
		Event:    "addon_configured",
		Resource: fmt.Sprintf("addon:%s", name),
	})
	writeJSON(w, http.StatusOK, withAttributionWarning(result, tokRes))
}
