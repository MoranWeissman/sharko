package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/schema"
	"gopkg.in/yaml.v3"
)

const (
	// DefaultAddonsFilename is the canonical git filename for default addons.
	DefaultAddonsFilename = "default-addons.yaml"
	// DefaultAddonsPath is the full git path under the configuration directory.
	DefaultAddonsPath = "configuration/default-addons.yaml"
	// DefaultAddonsSchemaHeader is the yaml-language-server directive.
	DefaultAddonsSchemaHeader = "# yaml-language-server: $schema=https://raw.githubusercontent.com/MoranWeissman/sharko/main/docs/schemas/default-addons.v1.json"
)

// DefaultAddonsResponse is the JSON shape returned by GET /default-addons.
type DefaultAddonsResponse struct {
	Addons []string `json:"addons"`
}

// DefaultAddonsPutRequest is the JSON shape accepted by PUT /default-addons.
type DefaultAddonsPutRequest struct {
	Addons []string `json:"addons"`
	DryRun bool     `json:"dry_run,omitempty"`
}

// handleGetDefaultAddons godoc
//
// @Summary Get default addons
// @Description Returns the current set of default addon names (auto-enabled on cluster registration without explicit addons). Reads from default-addons.yaml if present, falls back to the connection's gitops.default_addons string for backward compatibility.
// @Tags default-addons
// @Produce json
// @Security BearerAuth
// @Success 200 {object} DefaultAddonsResponse "Current default addons"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Router /default-addons [get]
func (s *Server) handleGetDefaultAddons(w http.ResponseWriter, r *http.Request) {
	addons, err := s.ReadDefaultAddons(r.Context())
	if err != nil {
		writeServerError(w, http.StatusInternalServerError, "read_default_addons", err)
		return
	}

	writeJSON(w, http.StatusOK, DefaultAddonsResponse{Addons: addons})
}

// handlePutDefaultAddons godoc
//
// @Summary Update default addons
// @Description Replaces the current default addon set with the supplied list. Opens a PR with the new default-addons.yaml (or updates an existing open PR for idempotency). Does NOT mutate the connection.
// @Tags default-addons
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body DefaultAddonsPutRequest true "New default addon list"
// @Success 200 {object} map[string]interface{} "PR created/updated"
// @Failure 400 {object} map[string]interface{} "Invalid request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Router /default-addons [put]
func (s *Server) handlePutDefaultAddons(w http.ResponseWriter, r *http.Request) {
	// Audit enrichment (mutating handler).
	audit.Enrich(r.Context(), audit.Fields{
		Event:    "default_addons_updated",
		Resource: "default-addons",
	})

	var req DefaultAddonsPutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	// Normalize the input: strip whitespace, deduplicate, filter empty.
	normalized := normalizeAddonNames(req.Addons)

	// Marshal to enveloped YAML.
	fileBody, err := marshalDefaultAddons(normalized)
	if err != nil {
		writeServerError(w, http.StatusInternalServerError, "marshal_default_addons", err)
		return
	}

	// Build orchestrator (same pattern as clusters_batch.go).
	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active ArgoCD connection: "+err.Error())
		return
	}
	git, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active Git connection: "+err.Error())
		return
	}

	orch := orchestrator.New(&s.gitMu, s.credProvider(), ac, git, s.gitopsCfg, s.repoPaths, nil)
	s.attachPRTracker(orch)

	files := map[string][]byte{DefaultAddonsPath: fileBody}
	operation := "update default addons"
	meta := orchestrator.PRMetadata{
		OperationCode: "default_addons_update",
		Title:         "Update default addons",
		// Cluster and Addon fields are empty — this is a global operation.
	}

	// Dry-run: return preview without side effects.
	if req.DryRun {
		filePreviews := []orchestrator.FilePreview{
			{Path: DefaultAddonsPath, Action: "update"},
		}
		dryRunResult := &orchestrator.GitResult{
			DryRun: &orchestrator.DryRunResult{
				EffectiveAddons: normalized,
				FilesToWrite:    filePreviews,
				PRTitle:         meta.Title,
				SecretsToCreate: []string{},
			},
		}
		writeJSON(w, http.StatusOK, dryRunResult)
		return
	}

	result, err := orch.CommitFilesAsPRWithMeta(r.Context(), files, operation, meta)
	if err != nil {
		writeServerError(w, http.StatusInternalServerError, "commit_default_addons", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Default addons PR created/updated",
		"pr_url":  result.PRUrl,
		"pr_id":   result.PRID,
	})
}

// ReadDefaultAddons reads the current default addon set from git (default-addons.yaml)
// or falls back to the connection's gitops.default_addons string. Exported so boot
// and hot-reload can reuse the same read+fallback logic.
func (s *Server) ReadDefaultAddons(ctx context.Context) ([]string, error) {
	git, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		return nil, fmt.Errorf("no active git provider: %w", err)
	}

	// Attempt to read default-addons.yaml from git.
	body, err := git.GetFileContent(ctx, DefaultAddonsPath, s.gitopsCfg.BaseBranch)
	if err == nil && len(body) > 0 {
		// File exists — parse it.
		addons, parseErr := parseDefaultAddons(body)
		if parseErr != nil {
			return nil, fmt.Errorf("parsing default-addons.yaml: %w", parseErr)
		}
		return addons, nil
	}

	// File absent or empty — fall back to connection string.
	// Fetch connection for the fallback path.
	conn, connErr := s.connSvc.GetActiveConnection()
	if connErr != nil {
		// If we can't fetch the connection and the file doesn't exist, return empty.
		return []string{}, nil
	}

	if conn == nil || conn.GitOps == nil || conn.GitOps.DefaultAddons == "" {
		return []string{}, nil
	}

	// Parse the comma-separated string.
	parts := strings.Split(conn.GitOps.DefaultAddons, ",")
	return normalizeAddonNames(parts), nil
}

// parseDefaultAddons parses a default-addons.yaml body (enveloped) and returns the addon names.
func parseDefaultAddons(body []byte) ([]string, error) {
	enveloped, err := schema.IsEnveloped(body)
	if err != nil {
		return nil, fmt.Errorf("checking envelope: %w", err)
	}
	if !enveloped {
		return nil, fmt.Errorf("default-addons.yaml must be enveloped (apiVersion: sharko.dev/v1, kind: DefaultAddons)")
	}

	// Validate against schema.
	if validator, vErr := schema.DefaultValidator(); vErr == nil && validator != nil {
		if err := validator.Validate(schema.KindDefaultAddons, body); err != nil {
			return nil, fmt.Errorf("validating default-addons.yaml: %w", err)
		}
	}

	// Parse the envelope.
	var doc schema.Envelope[config.DefaultAddonsSpec]
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("unmarshalling default-addons.yaml: %w", err)
	}
	if doc.Kind != schema.KindDefaultAddons {
		return nil, fmt.Errorf("wrong kind %q, expected %q", doc.Kind, schema.KindDefaultAddons)
	}

	return normalizeAddonNames(doc.Spec.Addons), nil
}

// marshalDefaultAddons serializes addon names to the enveloped default-addons.yaml format.
func marshalDefaultAddons(addons []string) ([]byte, error) {
	if addons == nil {
		addons = []string{}
	}

	doc := schema.Envelope[config.DefaultAddonsSpec]{
		APIVersion: schema.APIVersion,
		Kind:       schema.KindDefaultAddons,
		Metadata:   schema.Metadata{Name: "default-addons"},
		Spec:       config.DefaultAddonsSpec{Addons: addons},
	}

	body, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, fmt.Errorf("marshalling envelope: %w", err)
	}

	// Validate before returning (safety net).
	if validator, vErr := schema.DefaultValidator(); vErr == nil && validator != nil {
		if err := validator.Validate(schema.KindDefaultAddons, body); err != nil {
			return nil, fmt.Errorf("validating before write: %w", err)
		}
	}

	// Prepend schema header.
	var buf strings.Builder
	buf.WriteString(DefaultAddonsSchemaHeader)
	buf.WriteByte('\n')
	buf.Write(body)
	return []byte(buf.String()), nil
}

// normalizeAddonNames trims whitespace, filters empties, and deduplicates addon names.
func normalizeAddonNames(names []string) []string {
	seen := make(map[string]bool, len(names))
	var result []string
	for _, name := range names {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		if seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		result = append(result, trimmed)
	}
	return result
}
