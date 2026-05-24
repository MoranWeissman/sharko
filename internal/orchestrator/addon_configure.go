package orchestrator

import (
	"context"
	"fmt"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitops"
)

// configureCatalogParser is package-level — config.Parser is stateless and
// the ConfigureAddon hot path benefits from avoiding a per-call allocation.
// Mirrors the catalogParser pattern in internal/gitops/yaml_mutator_catalog.go.
var configureCatalogParser = config.NewParser()

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

	// If complex fields are provided, walk the typed catalog entries
	// directly. Simple-only updates fall through to the gitops shared
	// helper below.
	//
	// The reader/writer route preserves the envelope
	// (apiVersion/kind/metadata/spec) and upgrades legacy bare-YAML
	// inputs on the next emit — same contract as
	// gitops.UpdateCatalogEntry, which the simple-fields branch already
	// delegates to.
	hasComplexFields := req.SyncOptions != nil || req.AdditionalSources != nil ||
		req.IgnoreDifferences != nil || req.ExtraHelmValues != nil

	if hasComplexFields {
		entries, err := configureCatalogParser.ParseAddonsCatalog(data)
		if err != nil {
			return nil, fmt.Errorf("parsing catalog: %w", err)
		}

		found := false
		for i := range entries {
			if entries[i].Name != req.Name {
				continue
			}
			if req.SyncOptions != nil {
				entries[i].SyncOptions = req.SyncOptions
			}
			if req.AdditionalSources != nil {
				entries[i].AdditionalSources = req.AdditionalSources
			}
			if req.IgnoreDifferences != nil {
				entries[i].IgnoreDifferences = req.IgnoreDifferences
			}
			if req.ExtraHelmValues != nil {
				entries[i].ExtraHelmValues = req.ExtraHelmValues
			}
			// Also apply simple fields.
			if req.Version != "" {
				entries[i].Version = req.Version
			}
			if req.SyncWave != nil {
				entries[i].SyncWave = *req.SyncWave
			}
			if req.SelfHeal != nil {
				entries[i].SelfHeal = req.SelfHeal
			}
			found = true
			break
		}
		if !found {
			return nil, fmt.Errorf("addon %q not found in catalog", req.Name)
		}

		updatedData, err := config.MarshalAddonCatalog("", entries)
		if err != nil {
			return nil, fmt.Errorf("serializing catalog: %w", err)
		}

		files := map[string][]byte{catalogPath: updatedData}
		return o.commitChangesWithMeta(ctx, files, nil, fmt.Sprintf("configure addon %s", req.Name), PRMetadata{
			OperationCode: "addon-configure",
			Addon:         req.Name,
			Title:         fmt.Sprintf("Configure addon %s", req.Name),
		})
	}

	// Simple fields only — delegate to the gitops envelope-aware mutator.
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

	gitResult, err := o.commitChangesWithMeta(ctx, files, nil, fmt.Sprintf("configure addon %s", req.Name), PRMetadata{
		OperationCode: "addon-configure",
		Addon:         req.Name,
		Title:         fmt.Sprintf("Configure addon %s", req.Name),
	})
	if err != nil {
		return nil, fmt.Errorf("committing addon %q configuration: %w", req.Name, err)
	}

	return gitResult, nil
}
