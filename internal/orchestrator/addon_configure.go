package orchestrator

import (
	"context"
	"fmt"
	"path"

	"github.com/MoranWeissman/sharko/internal/models"
	"gopkg.in/yaml.v3"
)

func (o *Orchestrator) ConfigureAddon(ctx context.Context, req ConfigureAddonRequest) (*GitResult, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("addon name is required")
	}

	gp := o.git
	catalogPath := path.Join(o.paths.Charts, req.Name, "addon.yaml")

	data, err := gp.GetFileContent(ctx, o.gitops.BaseBranch, catalogPath)
	if err != nil {
		return nil, fmt.Errorf("addon %q not found in catalog: %w", req.Name, err)
	}

	var entry models.AddonCatalogEntry
	if err := yaml.Unmarshal(data, &entry); err != nil {
		return nil, fmt.Errorf("parsing addon %q catalog entry: %w", req.Name, err)
	}

	// Merge — only update fields that are provided
	if req.Version != "" {
		entry.Version = req.Version
	}
	if req.SyncWave != nil {
		entry.SyncWave = *req.SyncWave
	}
	if req.SelfHeal != nil {
		entry.SelfHeal = req.SelfHeal
	}
	if req.SyncOptions != nil {
		entry.SyncOptions = req.SyncOptions
	}
	if req.AdditionalSources != nil {
		entry.AdditionalSources = req.AdditionalSources
	}
	if req.IgnoreDifferences != nil {
		entry.IgnoreDifferences = req.IgnoreDifferences
	}
	if req.ExtraHelmValues != nil {
		entry.ExtraHelmValues = req.ExtraHelmValues
	}

	updatedData, err := yaml.Marshal(entry)
	if err != nil {
		return nil, fmt.Errorf("serializing addon %q: %w", req.Name, err)
	}

	header := fmt.Sprintf("# Addon catalog entry for %s\n", req.Name)
	files := map[string][]byte{
		catalogPath: append([]byte(header), updatedData...),
	}

	gitResult, err := o.commitChanges(ctx, files, nil, fmt.Sprintf("configure addon %s", req.Name))
	if err != nil {
		return nil, fmt.Errorf("committing addon %q configuration: %w", req.Name, err)
	}

	return gitResult, nil
}
