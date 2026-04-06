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
	catalogPath := path.Join(o.paths.GlobalValues, "..", "addons-catalog.yaml")
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
		SyncWave:  req.SyncWave,
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
	catalogPath := path.Join(o.paths.GlobalValues, "..", "addons-catalog.yaml")
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
