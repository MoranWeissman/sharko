package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"strings"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitops"
	"github.com/MoranWeissman/sharko/internal/models"
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

	// Parse catalog to validate dependencies and detect cycles.
	if len(req.DependsOn) > 0 {
		parser := config.NewParser()
		catalog, err := parser.ParseAddonsCatalog(catalogData)
		if err != nil {
			return nil, fmt.Errorf("parsing addons catalog for dependency validation: %w", err)
		}

		// Validate each declared dependency exists in the catalog.
		for _, dep := range req.DependsOn {
			found := false
			for _, entry := range catalog {
				if entry.Name == dep {
					found = true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("dependency %q not found in catalog", dep)
			}
		}

		// Build a temporary catalog that includes the new entry for cycle detection.
		prospective := append(catalog, models.AddonCatalogEntry{
			Name:      req.Name,
			DependsOn: req.DependsOn,
		})
		if err := detectCycles(prospective); err != nil {
			return nil, fmt.Errorf("dependency cycle detected: %w", err)
		}

		// Warn if sync waves may conflict with declared dependencies.
		warnSyncWaveConflicts(catalog, req)
	}

	// Append the new entry to the catalog.
	entry := gitops.CatalogEntryInput{
		Name:      req.Name,
		RepoURL:   req.RepoURL,
		Chart:     req.Chart,
		Version:   req.Version,
		Namespace: req.Namespace,
		SyncWave:  req.SyncWave,
		DependsOn: req.DependsOn,
	}
	updatedCatalog, err := gitops.AddCatalogEntry(catalogData, entry)
	if err != nil {
		return nil, fmt.Errorf("adding addon %q to catalog: %w", req.Name, err)
	}

	// Generate the global values file. Two paths:
	//
	//  - Smart-values pipeline (V121-6): when the caller supplied raw
	//    upstream chart values, run the heuristic splitter + per-cluster
	//    template generator + self-describing header. Used by the
	//    marketplace path and any caller that has the upstream bytes.
	//
	//  - Legacy stub: when no upstream is supplied, fall back to the
	//    pre-v1.21 minimal `<name>:\n  enabled: false` payload. Keeps
	//    older clients (raw API callers, legacy CLIs) working.
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

	gitResult, err := o.commitChangesWithMeta(ctx, files, nil, fmt.Sprintf("add addon %s", req.Name), PRMetadata{
		OperationCode: "addon-add",
		Addon:         req.Name,
		Title:         fmt.Sprintf("Add addon %s", req.Name),
	})
	if err != nil {
		return nil, fmt.Errorf("committing addon %q to Git: %w", req.Name, err)
	}

	return gitResult, nil
}

// RemoveAddon removes an addon's catalog entry and global values file.
func (o *Orchestrator) RemoveAddon(ctx context.Context, name string) (*GitResult, error) {
	if name == "" {
		return nil, fmt.Errorf("addon name is required")
	}

	// Read the existing addons-catalog.yaml.
	catalogPath := o.paths.Catalog
	catalogData, err := o.git.GetFileContent(ctx, catalogPath, o.gitops.BaseBranch)
	if err != nil {
		return nil, fmt.Errorf("reading addons-catalog.yaml: %w", err)
	}

	// Remove the entry from the catalog.
	updatedCatalog, err := gitops.RemoveCatalogEntry(catalogData, name)
	if err != nil {
		return nil, fmt.Errorf("removing addon %q from catalog: %w", name, err)
	}

	globalValuesPath := path.Join(o.paths.GlobalValues, name+".yaml")

	files := map[string][]byte{
		catalogPath: updatedCatalog,
	}
	deletePaths := []string{globalValuesPath}

	gitResult, err := o.commitChangesWithMeta(ctx, files, deletePaths, fmt.Sprintf("remove addon %s", name), PRMetadata{
		OperationCode: "addon-remove",
		Addon:         name,
		Title:         fmt.Sprintf("Remove addon %s", name),
	})
	if err != nil {
		return nil, fmt.Errorf("committing addon %q removal to Git: %w", name, err)
	}

	return gitResult, nil
}

// detectCycles performs a DFS on the addon dependency graph and returns an
// error if a cycle is found.  The catalog slice may include a prospective new
// entry that has not been committed yet.
func detectCycles(catalog []models.AddonCatalogEntry) error {
	// Build adjacency: name -> dependsOn names.
	adj := make(map[string][]string, len(catalog))
	for _, e := range catalog {
		adj[e.Name] = e.DependsOn
	}

	const (
		unvisited = 0
		inStack   = 1
		done      = 2
	)
	state := make(map[string]int, len(adj))

	var dfs func(node string) error
	dfs = func(node string) error {
		if state[node] == inStack {
			return fmt.Errorf("cycle involving %q", node)
		}
		if state[node] == done {
			return nil
		}
		state[node] = inStack
		for _, dep := range adj[node] {
			if err := dfs(dep); err != nil {
				return fmt.Errorf("%s -> %w", node, err)
			}
		}
		state[node] = done
		return nil
	}

	for name := range adj {
		if err := dfs(name); err != nil {
			return err
		}
	}
	return nil
}

// warnSyncWaveConflicts logs a warning when the new addon's sync wave may be
// lower than one of its declared dependencies, which would cause ArgoCD to
// deploy the new addon before the dependency is ready.
func warnSyncWaveConflicts(catalog []models.AddonCatalogEntry, req AddAddonRequest) {
	waveByName := make(map[string]int, len(catalog))
	for _, e := range catalog {
		waveByName[e.Name] = e.SyncWave
	}

	for _, dep := range req.DependsOn {
		depWave, ok := waveByName[dep]
		if !ok {
			continue
		}
		if req.SyncWave != 0 && req.SyncWave <= depWave {
			slog.Warn("addon sync wave may conflict with dependency",
				"addon", req.Name, "syncWave", req.SyncWave,
				"dependency", dep, "depSyncWave", depWave)
		}
	}
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
