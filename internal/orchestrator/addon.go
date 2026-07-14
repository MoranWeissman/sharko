package orchestrator

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/MoranWeissman/sharko/internal/gitops"
)

// AddAddon adds a new addon to the catalog and generates its global values file.
func (o *Orchestrator) AddAddon(ctx context.Context, req AddAddonRequest) (*GitResult, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("addon name is required")
	}
	if req.Chart == "" {
		return nil, fmt.Errorf("addon chart is required")
	}
	if req.RepoURL == "" {
		return nil, fmt.Errorf("addon repo_url is required")
	}

	// Read the existing addons-catalog.yaml.
	catalogPath := o.paths.Catalog
	catalogData, err := o.git.GetFileContent(ctx, catalogPath, o.gitops.BaseBranch)
	if err != nil {
		return nil, fmt.Errorf("reading addons-catalog.yaml: %w", err)
	}

	// Append the new entry to the catalog.
	entry := gitops.CatalogEntryInput{
		Name:      req.Name,
		RepoURL:   req.RepoURL,
		Chart:     req.Chart,
		Version:   req.Version,
		Namespace: req.Namespace,
	}
	updatedCatalog, err := gitops.AddCatalogEntry(catalogData, entry)
	if err != nil {
		return nil, fmt.Errorf("adding addon %q to catalog: %w", req.Name, err)
	}

	// Generate the global values file. Two paths:
	//
	//  - Smart-values pipeline: when the caller supplied raw upstream
	//    chart values, run the heuristic splitter + per-cluster template
	//    generator + self-describing header. Used by the marketplace path
	//    and any caller that has the upstream bytes.
	//
	//  - Stub: when no upstream is supplied, fall back to the minimal
	//    `<name>:\n  enabled: false` payload.
	//
	// Skip-on-existing: if the file is already present in the user's
	// repo (re-add or partial-failure retry) we keep what's there. This
	// prevents the smart-values regen from clobbering hand edits the user
	// may have made between attempts.
	globalValuesPath := path.Join(o.paths.GlobalValues, req.Name+".yaml")
	files := map[string][]byte{
		catalogPath: updatedCatalog,
	}
	if existing, err := o.git.GetFileContent(ctx, globalValuesPath, o.gitops.BaseBranch); err != nil || len(existing) == 0 {
		if len(req.UpstreamValues) > 0 {
			files[globalValuesPath] = GenerateGlobalValuesFile(
				req.Name, req.Chart, req.Version, req.RepoURL, req.UpstreamValues,
				req.AIAnnotated, req.AIOptOut,
				req.ExtraClusterSpecificPaths...,
			)
		} else {
			files[globalValuesPath] = generateAddonGlobalValues(req)
		}
	}

	// Dry-run exit point: return a preview of the exact file set the real
	// write below would commit, with ZERO side effects (no branch, no commit,
	// no PR). The `files` map computed above is the single source of truth —
	// the preview iterates it so the dry-run set is guaranteed identical to
	// what the non-dry-run path writes. Mirrors register-cluster's dry-run
	// shape so the Marketplace UI reuses the same FilePreview render.
	if req.DryRun {
		filePreviews := make([]FilePreview, 0, len(files))
		// Catalog file first for a stable, readable preview order; the
		// remaining files (global values, when present) follow.
		filePreviews = append(filePreviews, FilePreview{Path: catalogPath, Action: o.fileAction(ctx, catalogPath)})
		for p := range files {
			if p == catalogPath {
				continue
			}
			filePreviews = append(filePreviews, FilePreview{Path: p, Action: o.fileAction(ctx, p)})
		}
		return &GitResult{
			DryRun: &DryRunResult{
				EffectiveAddons: []string{req.Name},
				FilesToWrite:    filePreviews,
				PRTitle:         fmt.Sprintf("%s add addon %s", o.gitops.CommitPrefix, req.Name),
				SecretsToCreate: []string{},
			},
		}, nil
	}

	gitResult, err := o.commitChangesWithMeta(ctx, files, nil, fmt.Sprintf("add addon %s", req.Name),
		o.prMeta(req.AutoMerge, "addon-add", fmt.Sprintf("Add addon %s", req.Name), "", req.Name))
	if err != nil {
		return nil, fmt.Errorf("committing addon %q to Git: %w", req.Name, err)
	}

	return gitResult, nil
}

// RemoveAddon removes an addon's catalog entry and global values file.
func (o *Orchestrator) RemoveAddon(ctx context.Context, req RemoveAddonRequest) (*GitResult, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("addon name is required")
	}

	// Read the existing addons-catalog.yaml.
	catalogPath := o.paths.Catalog
	catalogData, err := o.git.GetFileContent(ctx, catalogPath, o.gitops.BaseBranch)
	if err != nil {
		return nil, fmt.Errorf("reading addons-catalog.yaml: %w", err)
	}

	// Remove the entry from the catalog.
	updatedCatalog, err := gitops.RemoveCatalogEntry(catalogData, req.Name)
	if err != nil {
		return nil, fmt.Errorf("removing addon %q from catalog: %w", req.Name, err)
	}

	globalValuesPath := path.Join(o.paths.GlobalValues, req.Name+".yaml")

	// Dry-run exit point: return a preview of what would happen.
	if req.DryRun {
		filePreviews := []FilePreview{
			{Path: catalogPath, Action: "update"},
			{Path: globalValuesPath, Action: "delete"},
		}
		return &GitResult{
			DryRun: &DryRunResult{
				EffectiveAddons: []string{},
				FilesToWrite:    filePreviews,
				PRTitle:         fmt.Sprintf("%s remove addon %s", o.gitops.CommitPrefix, req.Name),
				SecretsToCreate: []string{},
			},
		}, nil
	}

	files := map[string][]byte{
		catalogPath: updatedCatalog,
	}
	deletePaths := []string{globalValuesPath}

	gitResult, err := o.commitChangesWithMeta(ctx, files, deletePaths, fmt.Sprintf("remove addon %s", req.Name),
		o.prMeta(req.AutoMerge, "addon-remove", fmt.Sprintf("Remove addon %s", req.Name), "", req.Name))
	if err != nil {
		return nil, fmt.Errorf("committing addon %q removal to Git: %w", req.Name, err)
	}

	return gitResult, nil
}

// generateAddonGlobalValues creates the default global values YAML for an addon.
//
// v1.21 Bundle 5: top-level keys are the chart's own values. The previous
// `<addonName>:` wrap was incorrect — Helm receives this file directly via
// `valueFiles:` in the ApplicationSet template and expects chart-level keys
// at the document root. We emit a placeholder comment instead of a real key
// so the file parses as an empty values document until the user populates it.
func generateAddonGlobalValues(req AddAddonRequest) []byte {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Helm values for %s — applied to all clusters\n", req.Name))
	b.WriteString("# Top-level keys are passed directly to the Helm chart.\n")
	if req.Version != "" {
		b.WriteString(fmt.Sprintf("# Catalog-pinned version: %s\n", req.Version))
	}
	b.WriteString("\n# (no defaults — populate with chart values as needed)\n")
	return []byte(b.String())
}
