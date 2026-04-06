package orchestrator

import (
	"context"
	"fmt"

	"github.com/MoranWeissman/sharko/internal/gitops"
)

func (o *Orchestrator) ConfigureAddon(ctx context.Context, req ConfigureAddonRequest) (*GitResult, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("addon name is required")
	}

	// Reject complex fields that line-level mutation doesn't yet support.
	if req.SyncOptions != nil {
		return nil, fmt.Errorf("sync_options configuration is not yet supported via this endpoint")
	}
	if req.AdditionalSources != nil {
		return nil, fmt.Errorf("additional_sources configuration is not yet supported via this endpoint")
	}
	if req.IgnoreDifferences != nil {
		return nil, fmt.Errorf("ignore_differences configuration is not yet supported via this endpoint")
	}
	if req.ExtraHelmValues != nil {
		return nil, fmt.Errorf("extra_helm_values configuration is not yet supported via this endpoint")
	}

	// Read the existing addons-catalog.yaml.
	catalogPath := o.paths.Catalog
	data, err := o.git.GetFileContent(ctx, catalogPath, o.gitops.BaseBranch)
	if err != nil {
		return nil, fmt.Errorf("addon %q not found in catalog: %w", req.Name, err)
	}

	// Build updates map from non-zero/non-nil fields.
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
