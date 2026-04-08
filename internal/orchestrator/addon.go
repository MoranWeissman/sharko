package orchestrator

import (
	"context"
	"fmt"
	"log"
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

	// Generate global values file.
	globalValuesContent := generateAddonGlobalValues(req)
	globalValuesPath := path.Join(o.paths.GlobalValues, req.Name+".yaml")

	files := map[string][]byte{
		catalogPath:      updatedCatalog,
		globalValuesPath: globalValuesContent,
	}

	gitResult, err := o.commitChanges(ctx, files, nil, fmt.Sprintf("add addon %s", req.Name))
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

	gitResult, err := o.commitChanges(ctx, files, deletePaths, fmt.Sprintf("remove addon %s", name))
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
			log.Printf("WARNING: addon %q has syncWave=%d but depends on %q (syncWave=%d); "+
				"dependency may not be ready before %q is deployed",
				req.Name, req.SyncWave, dep, depWave, req.Name)
		}
	}
}

// generateAddonGlobalValues creates the default global values YAML for an addon.
func generateAddonGlobalValues(req AddAddonRequest) []byte {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Global values for %s addon\n", req.Name))
	b.WriteString(fmt.Sprintf("%s:\n", req.Name))
	b.WriteString("  enabled: false\n")
	if req.Version != "" {
		b.WriteString(fmt.Sprintf("  version: %s\n", req.Version))
	}
	return []byte(b.String())
}
