// Package api — legacy `<addon>:` wrap migration endpoint (v1.21 Bundle 5).
//
// Endpoint: POST /api/v1/addons/unwrap-globals
// Tier:     Tier 2 (configuration — opens a PR)
//
// Background: pre-Bundle-5 versions of Sharko wrote each addon's global
// values file under an `<addonName>:` (or `<chartName>:`) root key. The
// ApplicationSet template passes those files directly to Helm via
// `valueFiles:`, so Helm silently ignored every value in the file. Bundle 5
// fixes the writer; this endpoint migrates legacy files in place.
//
// Behaviour:
//   1. Walk every `*.yaml` file under `configuration/addons-global-values/`
//      in the user's repo (path is taken from `repoPaths.GlobalValues`).
//   2. For each file, derive the expected addon name from the filename
//      (`cert-manager.yaml` → `cert-manager`) and the chart name from the
//      catalog entry (when one exists). Run `UnwrapGlobalValuesFile`.
//   3. Files that are already unwrapped → skip.
//   4. Files that were unwrapped → stamp a one-line migration note into
//      the file header and stage them for the PR.
//   5. Open ONE PR with all the migrated files (one PR per call, NOT one
//      PR per addon) so the user reviews the full migration in one place.
//
// Idempotent: a second call after a successful migration returns 200 with
// `{migrated: 0, message: "all files already unwrapped"}` and DOES NOT
// open a PR.
//
// Optional `addon` query parameter scopes the migration to a single file —
// used by the per-file "Migrate this file" button on the AddonDetail Values
// tab. Without the parameter the endpoint is "migrate everything".

package api

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// unwrapGlobalsResult is the per-file outcome surfaced back to the caller.
type unwrapGlobalsFile struct {
	File    string `json:"file"`     // repo-relative path
	Addon   string `json:"addon"`    // derived addon name (filename without .yaml)
	Status  string `json:"status"`   // "migrated" | "skipped"
	Message string `json:"message,omitempty"`
}

// unwrapGlobalsResponse is the wire shape for POST /api/v1/addons/unwrap-globals.
//
// AttributionWarning (when set) is folded in via the shared
// `withAttributionWarning` wrapper at the call site, matching the
// values-editor response shape so the UI can reuse the AttributionNudge
// component.
type unwrapGlobalsResponse struct {
	Migrated int                  `json:"migrated"`
	Skipped  int                  `json:"skipped"`
	Files    []unwrapGlobalsFile  `json:"files"`
	Message  string               `json:"message,omitempty"`
	PRUrl    string               `json:"pr_url,omitempty"`
	PRID     int                  `json:"pr_id,omitempty"`
	Branch   string               `json:"branch,omitempty"`
	Merged   bool                 `json:"merged,omitempty"`
}

// handleUnwrapGlobalValues godoc
//
// @Summary Migrate legacy `<addon>:` wrapped global values files
// @Description Walks every file under `configuration/addons-global-values/` in the user's repo, detects files that are wrapped under an `<addonName>:` (or matching `<chartName>:`) root key, unwraps them so the chart's keys are at the document root, and opens ONE PR with the migrated files. Idempotent — running again when no files are wrapped returns 200 with `{migrated: 0}` and does not open a PR. Pass `?addon=<name>` to migrate a single file.
// @Tags addons
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param addon query string false "Migrate only this addon's file (defaults to all)"
// @Success 200 {object} api.unwrapGlobalsResponse "Migration result"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 502 {object} map[string]interface{} "Git or ArgoCD failure"
// @Router /addons/unwrap-globals [post]
func (s *Server) handleUnwrapGlobalValues(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "addon.update-catalog") {
		return
	}

	scopeAddon := strings.TrimSpace(r.URL.Query().Get("addon"))

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

	// Step 1: enumerate global values files.
	entries, lerr := git.ListDirectory(ctx, dir, s.gitopsCfg.BaseBranch)
	if lerr != nil {
		writeError(w, http.StatusBadGateway, "listing global values directory: "+lerr.Error())
		return
	}

	// Build the addon → chart map from the catalog. Best-effort — when the
	// catalog is unavailable, fall back to addon-name-only matching.
	addonChart := map[string]string{}
	if catalog, cerr := s.addonSvc.GetCatalog(ctx, git, ac); cerr == nil && catalog != nil {
		for _, a := range catalog.Addons {
			addonChart[a.AddonName] = a.Chart
		}
	}

	// Step 2: per-file detect + unwrap.
	files := map[string][]byte{}
	results := []unwrapGlobalsFile{}
	migratedCount := 0
	skippedCount := 0

	now := time.Now().UTC().Format("2006-01-02")
	headerStamp := fmt.Sprintf("# Migrated from legacy wrapped format on %s\n", now)

	sort.Strings(entries)
	for _, name := range entries {
		if !strings.HasSuffix(name, ".yaml") {
			continue
		}
		addon := strings.TrimSuffix(name, ".yaml")
		if scopeAddon != "" && !strings.EqualFold(addon, scopeAddon) {
			continue
		}
		fullPath := dir + "/" + name
		content, gerr := git.GetFileContent(ctx, fullPath, s.gitopsCfg.BaseBranch)
		if gerr != nil || len(content) == 0 {
			continue
		}
		chart := addonChart[addon]
		unwrapped, wasWrapped, uerr := orchestrator.UnwrapGlobalValuesFile(content, addon, chart)
		if uerr != nil {
			results = append(results, unwrapGlobalsFile{
				File: fullPath, Addon: addon, Status: "skipped", Message: uerr.Error(),
			})
			skippedCount++
			continue
		}
		if !wasWrapped {
			results = append(results, unwrapGlobalsFile{
				File: fullPath, Addon: addon, Status: "skipped", Message: "already unwrapped",
			})
			skippedCount++
			continue
		}
		stamped := stampMigrationNote(unwrapped, headerStamp)
		files[fullPath] = stamped
		results = append(results, unwrapGlobalsFile{
			File: fullPath, Addon: addon, Status: "migrated",
		})
		migratedCount++
	}

	// Audit always — even the no-op call shows up so security review can
	// see who attempted a migration.
	auditDetail := fmt.Sprintf("scope=%s migrated=%d skipped=%d",
		ifEmpty(scopeAddon, "all"), migratedCount, skippedCount)
	audit.Enrich(ctx, audit.Fields{
		Event:    "values_globals_unwrapped",
		Resource: "addons-global-values",
		Detail:   auditDetail,
	})

	resp := unwrapGlobalsResponse{
		Migrated: migratedCount,
		Skipped:  skippedCount,
		Files:    results,
	}

	if migratedCount == 0 {
		resp.Message = "all files already unwrapped"
		writeJSON(w, http.StatusOK, withAttributionWarning(resp, tokRes))
		return
	}

	// Step 3: open ONE PR with all migrations.
	// V125-1-6: route through CommitFilesAsPRWithMeta so the PR tracks
	// under the values-edit dashboard bucket (it's a global-values
	// rewrite, semantically the same family as a manual edit).
	orch := orchestrator.New(&s.gitMu, nil, ac, git, s.gitopsCfg, s.repoPaths, nil)
	s.attachPRTracker(orch)
	prResult, perr := orch.CommitFilesAsPRWithMeta(ctx, files,
		fmt.Sprintf("unwrap legacy global values (%d file(s))", migratedCount),
		orchestrator.PRMetadata{
			OperationCode: "values-edit",
			Title:         fmt.Sprintf("Unwrap legacy global values (%d file(s))", migratedCount),
		},
	)
	if perr != nil {
		writeError(w, http.StatusBadGateway, "opening migration PR: "+perr.Error())
		return
	}
	resp.PRUrl = prResult.PRUrl
	resp.PRID = prResult.PRID
	resp.Branch = prResult.Branch
	resp.Merged = prResult.Merged

	writeJSON(w, http.StatusOK, withAttributionWarning(resp, tokRes))
}

// stampMigrationNote prepends a single-line migration note ABOVE the
// existing file content. We don't try to splice it inside the smart-values
// header — putting it on its own line above keeps round-trip parsing
// (ParseSmartValuesHeader) untouched.
func stampMigrationNote(content []byte, note string) []byte {
	if len(content) == 0 {
		return []byte(note)
	}
	if strings.HasPrefix(string(content), note) {
		return content
	}
	return append([]byte(note), content...)
}

// ifEmpty is a tiny helper — returns alt when s is empty.
func ifEmpty(s, alt string) string {
	if s == "" {
		return alt
	}
	return s
}
