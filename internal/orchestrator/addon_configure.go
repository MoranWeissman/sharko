package orchestrator

import (
	"context"
	"fmt"

	"github.com/MoranWeissman/sharko/internal/gitops"
	"github.com/MoranWeissman/sharko/internal/models"
	"gopkg.in/yaml.v3"
)

func (o *Orchestrator) ConfigureAddon(ctx context.Context, req ConfigureAddonRequest) (*GitResult, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("addon name is required")
	}

	// Read the existing addons-catalog.yaml.
	catalogPath := o.paths.Catalog
	data, err := o.git.GetFileContent(ctx, catalogPath, o.gitops.BaseBranch)
	if err != nil {
		return nil, fmt.Errorf("addon %q not found in catalog: %w", req.Name, err)
	}

	// If complex fields are provided, use full unmarshal/marshal for the entry.
	hasComplexFields := req.SyncOptions != nil || req.AdditionalSources != nil ||
		req.IgnoreDifferences != nil || req.ExtraHelmValues != nil

	if hasComplexFields {
		var file struct {
			ApplicationSets []models.AddonCatalogEntry `yaml:"applicationsets"`
		}
		if err := yaml.Unmarshal(data, &file); err != nil {
			return nil, fmt.Errorf("parsing catalog: %w", err)
		}

		found := false
		for i, entry := range file.ApplicationSets {
			if entry.Name == req.Name {
				if req.SyncOptions != nil {
					file.ApplicationSets[i].SyncOptions = req.SyncOptions
				}
				if req.AdditionalSources != nil {
					file.ApplicationSets[i].AdditionalSources = req.AdditionalSources
				}
				if req.IgnoreDifferences != nil {
					file.ApplicationSets[i].IgnoreDifferences = req.IgnoreDifferences
				}
				if req.ExtraHelmValues != nil {
					file.ApplicationSets[i].ExtraHelmValues = req.ExtraHelmValues
				}
				// Also apply simple fields.
				if req.Version != "" {
					file.ApplicationSets[i].Version = req.Version
				}
				if req.SyncWave != nil {
					file.ApplicationSets[i].SyncWave = *req.SyncWave
				}
				if req.SelfHeal != nil {
					file.ApplicationSets[i].SelfHeal = req.SelfHeal
				}
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("addon %q not found in catalog", req.Name)
		}

		updatedData, err := yaml.Marshal(file)
		if err != nil {
			return nil, fmt.Errorf("serializing catalog: %w", err)
		}

		files := map[string][]byte{catalogPath: updatedData}
		return o.commitChanges(ctx, files, nil, fmt.Sprintf("configure addon %s", req.Name))
	}

	// Simple fields only — use line-level mutation.
	updates := make(map[string]string)
	if req.Version != "" {
		updates["version"] = req.Version
	}
	if req.SyncWave != nil {
		updates["syncWave"] = fmt.Sprintf("%d", *req.SyncWave)
	}
	if req.SelfHeal != nil {
		updates["selfHeal"] = fmt.Sprintf("%v", *req.SelfHeal)
	}

	if len(updates) == 0 {
		return nil, fmt.Errorf("no updatable fields provided for addon %q", req.Name)
	}

	updatedData, err := gitops.UpdateCatalogEntry(data, req.Name, updates)
	if err != nil {
		return nil, fmt.Errorf("updating addon %q in catalog: %w", req.Name, err)
	}

	files := map[string][]byte{
		catalogPath: updatedData,
	}

	gitResult, err := o.commitChanges(ctx, files, nil, fmt.Sprintf("configure addon %s", req.Name))
	if err != nil {
		return nil, fmt.Errorf("committing addon %q configuration: %w", req.Name, err)
	}

	return gitResult, nil
}
