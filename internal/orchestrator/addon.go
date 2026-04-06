package orchestrator

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/MoranWeissman/sharko/internal/models"
	"gopkg.in/yaml.v3"
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

	// Generate addon catalog entry YAML.
	catalogContent := generateAddonCatalogEntry(req)
	catalogPath := path.Join(o.paths.Charts, req.Name, "addon.yaml")

	// Generate global values file.
	globalValuesContent := generateAddonGlobalValues(req)
	globalValuesPath := path.Join(o.paths.GlobalValues, req.Name+".yaml")

	files := map[string][]byte{
		catalogPath:     catalogContent,
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

	catalogPath := path.Join(o.paths.Charts, name, "addon.yaml")
	globalValuesPath := path.Join(o.paths.GlobalValues, name+".yaml")

	deletePaths := []string{catalogPath, globalValuesPath}

	gitResult, err := o.commitChanges(ctx, nil, deletePaths, fmt.Sprintf("remove addon %s", name))
	if err != nil {
		return nil, fmt.Errorf("committing addon %q removal to Git: %w", name, err)
	}

	return gitResult, nil
}

// generateAddonCatalogEntry creates the YAML catalog entry for an addon.
func generateAddonCatalogEntry(req AddAddonRequest) []byte {
	entry := models.AddonCatalogEntry{
		Name:              req.Name,
		RepoURL:           req.RepoURL,
		Chart:             req.Chart,
		Version:           req.Version,
		Namespace:         req.Namespace,
		SyncWave:          req.SyncWave,
		SelfHeal:          req.SelfHeal,
		SyncOptions:       req.SyncOptions,
		AdditionalSources: req.AdditionalSources,
		IgnoreDifferences: req.IgnoreDifferences,
		ExtraHelmValues:   req.ExtraHelmValues,
	}

	data, err := yaml.Marshal(entry)
	if err != nil {
		// Fallback
		var b strings.Builder
		b.WriteString(fmt.Sprintf("name: %s\n", req.Name))
		b.WriteString(fmt.Sprintf("chart: %s\n", req.Chart))
		b.WriteString(fmt.Sprintf("repoURL: %s\n", req.RepoURL))
		b.WriteString(fmt.Sprintf("version: %s\n", req.Version))
		return []byte(b.String())
	}

	header := fmt.Sprintf("# Addon catalog entry for %s\n", req.Name)
	return append([]byte(header), data...)
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
