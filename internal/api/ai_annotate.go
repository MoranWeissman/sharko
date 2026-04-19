// Package api — AI annotate endpoints (v1.21 Epic V121-7, Story 7.4).
//
// Two small endpoints sit on top of the existing values-editor pipeline:
//
//   POST /api/v1/addons/{name}/values/annotate     — Tier 2: regenerate
//         the global values file with a fresh AI annotation pass. Same
//         rules as the seed flow: secret-leak guard hard-blocks; on
//         block we return a 422 with the redacted match list (no PR
//         created). On success the orchestrator opens a Tier 2 PR with
//         the newly annotated content.
//
//   PUT  /api/v1/addons/{name}/values/ai-opt-out  — Tier 2: flip the
//         per-addon `# sharko: ai-annotate=off` directive in the values
//         file header without changing the body. Locked decision
//         (Moran): the directive is the only persistence — we don't add
//         a new K8s store for per-addon settings. The toggle is
//         idempotent.
//
// Both endpoints reuse the orchestrator.SetGlobalAddonValues commit
// helper so the audit, attribution, and PR shape match the manual edit
// path.

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

// annotateAddonValuesResponse is the success body of the manual annotate
// endpoint. When the AI guard fires the response is a 422 with the
// `aiAnnotateBlockedResponse` shape instead.
type annotateAddonValuesResponse struct {
	PRUrl     string `json:"pr_url,omitempty"`
	PRID      int    `json:"pr_id,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Merged    bool   `json:"merged"`
	CommitSHA string `json:"commit_sha,omitempty"`
	// AISkipReason is set when the LLM was skipped and the heuristic-only
	// output was committed instead. Possible values: "not_configured",
	// "opted_out", "oversize", "timeout", "llm_error", "parse_error".
	AISkipReason string `json:"ai_skip_reason,omitempty"`
}

// aiAnnotateBlockedResponse is the 422 body when the secret-leak guard
// hard-blocks the annotate call. The matches are redacted via
// orchestrator.SecretMatch.Field — no real secret values appear.
type aiAnnotateBlockedResponse struct {
	Code    string                       `json:"code"`
	Message string                       `json:"message"`
	Matches []orchestrator.SecretMatch   `json:"matches"`
}

// handleAnnotateAddonValues godoc
//
// @Summary Re-annotate an addon's global values via AI
// @Description Fetches the chart's upstream values.yaml at the catalog-pinned version, runs the V121-7 AI annotate pass (one-line `# description` comments + LLM-suggested cluster-specific paths unioned with the heuristic), and opens a Tier 2 PR with the result. Hard-blocked when the secret-leak guard matches; in that case returns 422 with redacted matches and no PR. Skipped when AI is not configured or the addon is opted out.
// @Tags addons
// @Produce json
// @Security BearerAuth
// @Param name path string true "Addon name"
// @Success 200 {object} annotateAddonValuesResponse "PR opened (or merged if auto-merge is on)"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 404 {object} map[string]interface{} "Addon not found"
// @Failure 422 {object} aiAnnotateBlockedResponse "Secret-leak guard blocked the LLM call"
// @Failure 502 {object} map[string]interface{} "Git or upstream fetch error"
// @Failure 503 {object} map[string]interface{} "AI not configured"
// @Router /addons/{name}/values/annotate [post]
func (s *Server) handleAnnotateAddonValues(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "addon.update-catalog") {
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "addon name is required")
		return
	}

	// 503 when AI is not configured at all — UI uses this to suggest
	// jumping to Settings → AI rather than failing silently.
	if s.aiClient == nil || !s.aiClient.IsEnabled() {
		writeError(w, http.StatusServiceUnavailable, "AI is not configured; configure a provider in Settings → AI")
		return
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

	addon, gerr := s.addonSvc.GetAddonDetail(ctx, name, git, ac)
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
		writeError(w, http.StatusBadRequest, "addon catalog entry is missing chart/repo/version metadata required to annotate")
		return
	}

	// Per-addon opt-out: read the existing file's header and abort if
	// the user previously opted out. Honoring the directive prevents
	// an accidental re-enable via this endpoint.
	dir := strings.TrimSuffix(s.repoPaths.GlobalValues, "/")
	if dir == "" {
		dir = "configuration/addons-global-values"
	}
	valuesFile := dir + "/" + name + ".yaml"
	if existing, eerr := git.GetFileContent(ctx, valuesFile, s.gitopsCfg.BaseBranch); eerr == nil && len(existing) > 0 {
		if h := orchestrator.ParseSmartValuesHeader(existing); h.AIOptOut {
			writeError(w, http.StatusConflict, "this addon is opted out of AI annotation; clear the opt-out via PUT /addons/"+name+"/values/ai-opt-out before retrying")
			return
		}
	}

	raw, ferr := helm.NewFetcher().FetchValues(ctx, repoURL, chart, version)
	if ferr != nil {
		writeError(w, http.StatusBadGateway, "fetching upstream values: "+ferr.Error())
		return
	}

	annRes, annErr := orchestrator.AnnotateValues(ctx, []byte(raw), chart, version, s.aiClient)
	if annErr != nil {
		var secretBlock *orchestrator.SecretLeakError
		if errors.As(annErr, &secretBlock) {
			audit.Enrich(ctx, audit.Fields{
				Event:    "ai_annotate_blocked",
				Resource: fmt.Sprintf("addon:%s", name),
				Detail:   fmt.Sprintf("chart=%s version=%s matches=%d", chart, version, len(secretBlock.Matches)),
			})
			// Story V121-8.5: emit a separate, grep-stable
			// `secret_leak_blocked` entry on the audit ring so security
			// review can find every block (across handlers) with one query.
			s.emitSecretLeakAuditBlock(ctx, "ai_annotate", name, chart, version, secretBlock.Matches)
			writeJSON(w, http.StatusUnprocessableEntity, aiAnnotateBlockedResponse{
				Code:    secretBlock.Code(),
				Message: "Secret-like content detected in upstream values; AI annotation is hard-blocked.",
				Matches: secretBlock.Matches,
			})
			return
		}
		// Other errors are caught inside AnnotateValues and recorded as
		// SkipReason — the err here is nil in that case. Fall through.
	}

	generated := orchestrator.GenerateGlobalValuesFile(
		name, chart, version, repoURL, annRes.AnnotatedYAML,
		annRes.SkipReason == "", false,
		annRes.AdditionalClusterPaths...,
	)

	orch := orchestrator.New(&s.gitMu, nil, ac, git, s.gitopsCfg, s.repoPaths, nil)
	result, err := orch.SetGlobalAddonValues(ctx, name, string(generated))
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
			PRTitle:    fmt.Sprintf("AI annotate values for %s@%s", chart, version),
			PRBase:     "main",
			Addon:      name,
			Operation:  "values-ai-annotate",
			User:       user,
			Source:     "api",
			CreatedAt:  time.Now(),
			LastStatus: "open",
		})
	}

	state := "ok"
	if annRes.SkipReason != "" {
		state = annRes.SkipReason
	}
	audit.Enrich(ctx, audit.Fields{
		Event:    "ai_annotate_run",
		Resource: fmt.Sprintf("addon:%s", name),
		Detail:   fmt.Sprintf("chart=%s version=%s state=%s", chart, version, state),
	})

	resp := annotateAddonValuesResponse{
		Merged:       result.Merged,
		PRUrl:        result.PRUrl,
		PRID:         result.PRID,
		Branch:       result.Branch,
		CommitSHA:    result.CommitSHA,
		AISkipReason: annRes.SkipReason,
	}
	writeJSON(w, http.StatusOK, withAttributionWarning(resp, tokRes))
	slog.Info("ai annotate run", "addon", name, "chart", chart, "version", version, "skip_reason", annRes.SkipReason)
}

// setAIOptOutRequest is the body of the per-addon opt-out toggle.
type setAIOptOutRequest struct {
	OptOut bool `json:"opt_out"`
}

// handleSetAddonAIOptOut godoc
//
// @Summary Toggle per-addon AI annotation opt-out
// @Description Sets or clears the `# sharko: ai-annotate=off` directive in the addon's global values file header. When opt-out is true, future addon-add and refresh-from-upstream runs skip the AI annotate pass for this addon. The directive is the only persistence (no Sharko-side store). Idempotent.
// @Tags addons
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Addon name"
// @Param body body setAIOptOutRequest true "opt_out flag"
// @Success 200 {object} map[string]interface{} "PR opened (or merged) with header-only change"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 404 {object} map[string]interface{} "Addon not found"
// @Failure 502 {object} map[string]interface{} "Git error"
// @Router /addons/{name}/values/ai-opt-out [put]
func (s *Server) handleSetAddonAIOptOut(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "addon.update-catalog") {
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "addon name is required")
		return
	}
	var req setAIOptOutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
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

	dir := strings.TrimSuffix(s.repoPaths.GlobalValues, "/")
	if dir == "" {
		dir = "configuration/addons-global-values"
	}
	valuesFile := dir + "/" + name + ".yaml"

	existing, eerr := git.GetFileContent(ctx, valuesFile, s.gitopsCfg.BaseBranch)
	if eerr != nil || len(existing) == 0 {
		writeError(w, http.StatusNotFound, "no global values file at "+valuesFile)
		return
	}

	header := orchestrator.ParseSmartValuesHeader(existing)
	if header.AIOptOut == req.OptOut {
		// Idempotent — same target state. Return 200 with a no-op marker.
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":   "noop",
			"opt_out":  req.OptOut,
			"addon":    name,
			"message":  fmt.Sprintf("addon already %s of AI annotation", optOutLabel(req.OptOut)),
		})
		return
	}

	updated := rewriteHeaderOptOut(existing, req.OptOut)

	orch := orchestrator.New(&s.gitMu, nil, ac, git, s.gitopsCfg, s.repoPaths, nil)
	result, err := orch.SetGlobalAddonValues(ctx, name, string(updated))
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
			PRTitle:    fmt.Sprintf("Toggle AI opt-out for %s (%s)", name, optOutLabel(req.OptOut)),
			PRBase:     "main",
			Addon:      name,
			Operation:  "values-ai-opt-out",
			User:       user,
			Source:     "api",
			CreatedAt:  time.Now(),
			LastStatus: "open",
		})
	}

	audit.Enrich(ctx, audit.Fields{
		Event:    "ai_opt_out_toggled",
		Resource: fmt.Sprintf("addon:%s", name),
		Detail:   fmt.Sprintf("opt_out=%v", req.OptOut),
	})
	writeJSON(w, http.StatusOK, withAttributionWarning(map[string]interface{}{
		"status":   "ok",
		"opt_out":  req.OptOut,
		"addon":    name,
		"pr_url":   safePRURL(result),
		"pr_id":    safePRID(result),
		"merged":   safeMerged(result),
	}, tokRes))
}

// optOutLabel returns the human-readable label for the opt-out state.
func optOutLabel(b bool) string {
	if b {
		return "opted-out"
	}
	return "opted-in"
}

// rewriteHeaderOptOut returns the file with the `# sharko: ai-annotate=off`
// header line added (or removed). The body is preserved verbatim. We
// re-render the header using WriteSmartValuesHeader so format drift can't
// creep in across versions.
//
// If the file has no Sharko header (legacy file pre-v1.21), we don't
// rewrite — opt-out is meaningless for files the smart-values pipeline
// doesn't manage. The handler treats that as a no-op (handled upstream
// by the idempotent check on header.AIOptOut).
func rewriteHeaderOptOut(content []byte, optOut bool) []byte {
	h := orchestrator.ParseSmartValuesHeader(content)
	if !h.Managed {
		return content
	}
	// Strip the existing header lines (every leading `#` line up to the
	// first non-comment line) and write a fresh header with the new flag.
	lines := strings.Split(string(content), "\n")
	bodyStart := 0
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		bodyStart = i
		break
	}
	body := strings.Join(lines[bodyStart:], "\n")

	newHeader := orchestrator.WriteSmartValuesHeader(orchestrator.SmartValuesHeader{
		Chart:       h.Chart,
		Version:     h.Version,
		RepoURL:     h.RepoURL,
		AIAnnotated: h.AIAnnotated,
		AIOptOut:    optOut,
	})
	return []byte(newHeader + body)
}

// safePRURL / safePRID / safeMerged are nil-safe accessors for the
// orchestrator's *GitResult so the response builder can stay tiny.
func safePRURL(r *orchestrator.GitResult) string {
	if r == nil {
		return ""
	}
	return r.PRUrl
}

func safePRID(r *orchestrator.GitResult) int {
	if r == nil {
		return 0
	}
	return r.PRID
}

func safeMerged(r *orchestrator.GitResult) bool {
	if r == nil {
		return false
	}
	return r.Merged
}
