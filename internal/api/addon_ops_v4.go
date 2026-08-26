// Package api — the v4-format addon enable/disable endpoints (v4 Wave 1
// Story 4.3, "the sharpened pipeline"). Distinct routes from
// handleEnableAddon / handleDisableAddon (addon_ops.go) rather than
// branching those handlers by repo format: v3 clusters keep working
// through the exact code path they use today, byte for byte, and a v4
// caller opts in explicitly by using the /v4/ route — no format-sniffing
// footgun where the wrong handler runs against the wrong repo shape.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/remoteclient"
)

// validateV4PathNames rejects a cluster or addon name that is empty or does
// not match the one shared name pattern (models.ResourceNamePattern), and
// writes the 400 itself. Returns a non-nil error when it wrote one, so the
// caller's line stays `if err := validateV4PathNames(...); err != nil { return }`.
//
// This is the request-edge half of the path-traversal fix. Both names land
// verbatim in a git commit path (cluster-addons/<cluster>.yaml,
// values/clusters/<cluster>/<addon>.yaml) and the addon name also becomes a
// Kubernetes label key, so an unchecked "../.." would let a caller rewrite
// any file in the repo — including the engine pin. Go 1.22's ServeMux
// URL-decodes path segments before PathValue sees them, so "..%2F" arrives
// as a plain "../" and this check is what stops it.
func validateV4PathNames(w http.ResponseWriter, clusterName, addonName string) error {
	for _, pair := range []struct{ kind, value string }{
		{"cluster", clusterName},
		{"addon", addonName},
	} {
		if pair.value == "" {
			err := fmt.Errorf("%s name is required", pair.kind)
			writeError(w, http.StatusBadRequest, err.Error())
			return err
		}
		if !models.IsValidResourceName(pair.value) {
			err := fmt.Errorf("invalid %s name %q: %s", pair.kind, pair.value, models.InvalidResourceNameMessage)
			writeError(w, http.StatusBadRequest, err.Error())
			return err
		}
	}
	return nil
}

// friendlyBodyDecodeError turns a JSON request-body decode error into a
// plain-English message safe to hand back to a caller. encoding/json's own
// error text is a Go implementation detail — "json: cannot unmarshal
// string into Go struct field EnableAddonV4Request.values of type
// map[string]interface {}" — that a Helm user has no way to act on; Moran
// hit exactly this raw message live via the enable-addon dialog's values
// box (Wave 2 ride-along w2-q6 item 2). A *json.UnmarshalTypeError naming
// the "values" field gets the specific, actionable example; every other
// decode failure (bad syntax, wrong type elsewhere) gets a generic
// "not valid JSON" message — never the raw encoding/json string.
func friendlyBodyDecodeError(err error) string {
	var terr *json.UnmarshalTypeError
	if errors.As(err, &terr) && terr.Field == "values" {
		return `values must be a set of key: value pairs, e.g. {"installCRDs": true}`
	}
	return "request body is not valid JSON"
}

// writeV4OrchestratorError maps an error returned by EnableAddonV4 /
// DisableAddonV4 to the right HTTP status — honest codes instead of a
// blanket 502 (Wave 2 ride-along w2-q6 item 2):
//
//   - confirmation-required            -> 400 (already-well-formed request, missing yes:true)
//   - *orchestrator.V4SemanticValidationError -> 422, structured (handled by the caller BEFORE this helper)
//   - orchestrator.ErrV4AddonNotInCatalog     -> 422, code not_in_catalog
//   - orchestrator.ErrCatalogFileEmpty        -> 422, code empty_catalog_file
//   - orchestrator.ErrMixedRepoLayout         -> 409, code repo_layout
//   - orchestrator.ErrV4ClusterNotFound       -> 404
//   - anything else                           -> 502 (a real upstream/git failure)
//
// The 422s carry a `code` as well as the message. The add-to-catalog-then-
// enable flow has to tell "this addon is not approved yet" apart from
// "this entry is half-written" apart from "this cluster is missing a
// value", and it was doing that by matching English in the message — which
// fired on the wrong ones and missed the right one (review finding B-2).
func writeV4OrchestratorError(w http.ResponseWriter, err error) {
	if err.Error() == "confirmation required: set yes: true in request body" {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if errors.Is(err, orchestrator.ErrV4ClusterNotFound) {
		writeCodedError(w, http.StatusNotFound, CodeClusterNotFound, err.Error(), nil)
		return
	}
	if orchestrator.IsV4RepoUnsupported(err) || errors.Is(err, orchestrator.ErrMixedRepoLayout) {
		writeCodedError(w, http.StatusConflict, CodeRepoLayout, err.Error(), nil)
		return
	}
	if errors.Is(err, orchestrator.ErrV4AddonNotInCatalog) {
		writeCodedError(w, http.StatusUnprocessableEntity, CodeNotInCatalog, err.Error(), nil)
		return
	}
	if errors.Is(err, orchestrator.ErrCatalogFileEmpty) {
		writeCodedError(w, http.StatusUnprocessableEntity, CodeEmptyCatalogFile, err.Error(), nil)
		return
	}
	writeError(w, http.StatusBadGateway, err.Error())
}

// handleEnableAddonV4 godoc
//
// @Summary Enable addon on cluster (v4 format)
// @Description Enables an addon on a cluster by writing cluster-addons/{name}.yaml (kind ClusterAddons) and, when values are supplied, values/clusters/{name}/{addon}.yaml — the v4 data-file format (design doc 2026-07-30-v4-data-file-format.md). Runs semantic validation BEFORE any branch or pull request exists: every required value the catalog entry declares must be present (in the supplied values or already on disk), and every secret the addon declares as needed to INSTALL must have a Sharko secret definition wired up. A validation failure returns 422 naming exactly what is missing, in plain English — nothing is written, not even a branch. Secrets the addon only needs at RUNTIME (required_for: runtime on the catalog entry) never block the install; a missing one is instead listed in the response's warnings field, both on a dry-run preview and on the real enable. Requires yes=true for confirmation (or dry_run=true to preview, which also runs validation first).
// @Tags addons
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Param addon path string true "Addon name"
// @Param body body orchestrator.EnableAddonV4Request true "Enable addon request"
// @Success 200 {object} orchestrator.GitResult "Addon enabled (or dry-run preview)"
// @Failure 400 {object} map[string]interface{} "Bad request, invalid JSON body, or missing confirmation"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 404 {object} map[string]interface{} "Cluster is not registered (code cluster_not_found)"
// @Failure 409 {object} map[string]interface{} "The repo carries both the old and the new layout (code repo_layout)"
// @Failure 422 {object} map[string]interface{} "Semantic validation failed (missing required values or undeclared secrets, listed by name) or the addon is not in the catalog. The body carries a machine-readable `code` next to `error`: `not_in_catalog` when the org has not approved this addon (add it first — POST /catalog/addons); `incomplete_entry` when its catalog entry is half-written, with `problems` naming each missing piece; `validation_failed` when the entry is fine but this cluster is missing a required value or a secret definition, also with `problems`; `empty_catalog_file` when catalog.yaml exists but is blank. Branch on the code, never on the message text."
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /v4/clusters/{name}/addons/{addon} [post]
func (s *Server) handleEnableAddonV4(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "addon.enable") {
		return
	}

	clusterName := r.PathValue("name")
	addonName := r.PathValue("addon")
	if err := validateV4PathNames(w, clusterName, addonName); err != nil {
		return
	}

	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeNoActiveArgocdConnection(w, r)
		return
	}
	git, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeNoActiveGitConnection(w, r)
		return
	}

	var req orchestrator.EnableAddonV4Request
	if r.Body != nil && r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, friendlyBodyDecodeError(err))
			return
		}
	}
	req.Cluster = clusterName
	req.Addon = addonName

	orch := orchestrator.New(&s.gitMu, s.credProvider(), ac, git, s.gitopsConfig(), s.repoPaths, nil)
	s.attachPRTracker(orch)
	orch.SetSecretManagement(s.addonSecretDefs, s.secretFetcher, remoteclient.NewClientFromKubeconfig)

	result, orchErr := orch.EnableAddonV4(r.Context(), req)
	if orchErr != nil {
		var verr *orchestrator.V4SemanticValidationError
		if errors.As(orchErr, &verr) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
				"error":    verr.Error(),
				"code":     semanticErrorCode(verr),
				"cluster":  verr.Cluster,
				"addon":    verr.Addon,
				"problems": verr.Problems,
			})
			return
		}
		writeV4OrchestratorError(w, orchErr)
		return
	}

	if req.DryRun {
		writeJSON(w, http.StatusOK, result)
		return
	}

	if s.clusterRecon != nil {
		s.clusterRecon.Trigger()
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "addon_enabled_on_cluster_v4",
		Resource: fmt.Sprintf("cluster:%s addon:%s", clusterName, addonName),
	})
	writeJSON(w, http.StatusOK, result)
}

// handleDisableAddonV4 godoc
//
// @Summary Disable addon on cluster (v4 format)
// @Description Disables an addon on a cluster by setting enabled=false in cluster-addons/{name}.yaml (kind ClusterAddons) — the entry (and its version pin and settings) is KEPT by default so re-enabling is a one-word change; pass remove=true to delete the entry entirely instead. No semantic validation runs (disabling never needs required values or secrets). Requires yes=true for confirmation (or dry_run=true to preview).
// @Tags addons
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Param addon path string true "Addon name"
// @Param body body orchestrator.DisableAddonV4Request true "Disable addon request"
// @Success 200 {object} orchestrator.GitResult "Addon disabled (or dry-run preview)"
// @Failure 400 {object} map[string]interface{} "Bad request, invalid JSON body, or missing confirmation"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 404 {object} map[string]interface{} "Cluster is not registered, or has nothing to disable (code cluster_not_found)"
// @Failure 409 {object} map[string]interface{} "The repo carries both the old and the new layout (code repo_layout)"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /v4/clusters/{name}/addons/{addon} [delete]
func (s *Server) handleDisableAddonV4(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "addon.disable") {
		return
	}

	clusterName := r.PathValue("name")
	addonName := r.PathValue("addon")
	if err := validateV4PathNames(w, clusterName, addonName); err != nil {
		return
	}

	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeNoActiveArgocdConnection(w, r)
		return
	}
	git, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeNoActiveGitConnection(w, r)
		return
	}

	var req orchestrator.DisableAddonV4Request
	if r.Body != nil && r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, friendlyBodyDecodeError(err))
			return
		}
	}
	req.Cluster = clusterName
	req.Addon = addonName

	orch := orchestrator.New(&s.gitMu, s.credProvider(), ac, git, s.gitopsConfig(), s.repoPaths, nil)
	s.attachPRTracker(orch)

	result, orchErr := orch.DisableAddonV4(r.Context(), req)
	if orchErr != nil {
		writeV4OrchestratorError(w, orchErr)
		return
	}

	if req.DryRun {
		writeJSON(w, http.StatusOK, result)
		return
	}

	if s.clusterRecon != nil {
		s.clusterRecon.Trigger()
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "addon_disabled_on_cluster_v4",
		Resource: fmt.Sprintf("cluster:%s addon:%s", clusterName, addonName),
	})
	writeJSON(w, http.StatusOK, result)
}
