package service

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/moran/argocd-addons-platform/internal/ai"
	"github.com/moran/argocd-addons-platform/internal/config"
	"github.com/moran/argocd-addons-platform/internal/gitprovider"
	"github.com/moran/argocd-addons-platform/internal/helm"
	"github.com/moran/argocd-addons-platform/internal/models"
)

// UpgradeService handles upgrade impact checking operations.
type UpgradeService struct {
	parser  *config.Parser
	fetcher *helm.Fetcher
	ai      *ai.Client
}

// NewUpgradeService creates a new UpgradeService.
func NewUpgradeService(aiClient *ai.Client) *UpgradeService {
	return &UpgradeService{
		parser:  config.NewParser(),
		fetcher: helm.NewFetcher(),
		ai:      aiClient,
	}
}

// IsAIEnabled returns true when the AI client is configured and available.
func (s *UpgradeService) IsAIEnabled() bool {
	return s.ai != nil && s.ai.IsEnabled()
}

// GetAISummary generates an AI-powered summary of an upgrade impact analysis.
func (s *UpgradeService) GetAISummary(ctx context.Context, result *models.UpgradeCheckResponse) (string, error) {
	if s.ai == nil || !s.ai.IsEnabled() {
		return "", fmt.Errorf("AI not configured")
	}

	// Build changed details string
	var changedDetails strings.Builder
	for _, c := range result.Changed {
		fmt.Fprintf(&changedDetails, "- %s: %s -> %s\n", c.Path, c.OldValue, c.NewValue)
	}

	// Build conflicts string
	var conflictsStr strings.Builder
	for _, c := range result.Conflicts {
		fmt.Fprintf(&conflictsStr, "- %s: configured=%s, old_default=%s, new_default=%s (source: %s)\n",
			c.Path, c.ConfiguredValue, c.OldDefault, c.NewDefault, c.Source)
	}

	prompt := ai.BuildUpgradePrompt(
		result.AddonName, result.CurrentVersion, result.TargetVersion,
		len(result.Added), len(result.Removed), len(result.Changed),
		changedDetails.String(), conflictsStr.String(), result.ReleaseNotes,
	)

	return s.ai.Summarize(ctx, prompt)
}

// ListVersions returns available versions for an addon's Helm chart.
func (s *UpgradeService) ListVersions(ctx context.Context, addonName string, gp gitprovider.GitProvider) (*models.AvailableVersionsResponse, error) {
	// Get addon info from catalog
	catalogData, err := gp.GetFileContent(ctx, "configuration/addons-catalog.yaml", "main")
	if err != nil {
		return nil, fmt.Errorf("fetching addons catalog: %w", err)
	}

	addons, err := s.parser.ParseAddonsCatalog(catalogData)
	if err != nil {
		return nil, fmt.Errorf("parsing addons catalog: %w", err)
	}

	var addon *models.AddonCatalogEntry
	for i := range addons {
		if addons[i].AppName == addonName {
			addon = &addons[i]
			break
		}
	}
	if addon == nil {
		return nil, fmt.Errorf("addon %q not found in catalog", addonName)
	}

	// Fetch versions from Helm repo index
	chartVersions, err := s.fetcher.ListVersions(ctx, addon.RepoURL, addon.Chart)
	if err != nil {
		return nil, fmt.Errorf("listing chart versions: %w", err)
	}

	// Return top 20 versions
	limit := 20
	if len(chartVersions) < limit {
		limit = len(chartVersions)
	}

	versions := make([]models.AvailableVersion, 0, limit)
	for i := 0; i < limit; i++ {
		versions = append(versions, models.AvailableVersion{
			Version:    chartVersions[i].Version,
			AppVersion: chartVersions[i].AppVersion,
		})
	}

	return &models.AvailableVersionsResponse{
		AddonName: addonName,
		Chart:     addon.Chart,
		RepoURL:   addon.RepoURL,
		Versions:  versions,
	}, nil
}

// CheckUpgrade performs an upgrade impact analysis comparing current and target chart versions.
func (s *UpgradeService) CheckUpgrade(ctx context.Context, addonName, targetVersion string, gp gitprovider.GitProvider) (*models.UpgradeCheckResponse, error) {
	// Get addon info from catalog
	catalogData, err := gp.GetFileContent(ctx, "configuration/addons-catalog.yaml", "main")
	if err != nil {
		return nil, fmt.Errorf("fetching addons catalog: %w", err)
	}

	addons, err := s.parser.ParseAddonsCatalog(catalogData)
	if err != nil {
		return nil, fmt.Errorf("parsing addons catalog: %w", err)
	}

	var addon *models.AddonCatalogEntry
	for i := range addons {
		if addons[i].AppName == addonName {
			addon = &addons[i]
			break
		}
	}
	if addon == nil {
		return nil, fmt.Errorf("addon %q not found in catalog", addonName)
	}

	currentVersion := addon.Version

	// Fetch values.yaml for current and target versions from Helm repo
	oldValues, err := s.fetcher.FetchValues(ctx, addon.RepoURL, addon.Chart, currentVersion)
	if err != nil {
		return nil, fmt.Errorf("fetching current version values: %w", err)
	}

	newValues, err := s.fetcher.FetchValues(ctx, addon.RepoURL, addon.Chart, targetVersion)
	if err != nil {
		return nil, fmt.Errorf("fetching target version values: %w", err)
	}

	// Diff the two values.yaml files
	added, removed, changed, err := helm.DiffValues(oldValues, newValues)
	if err != nil {
		return nil, fmt.Errorf("diffing values: %w", err)
	}

	// Convert helm diffs to model diffs
	addedEntries := make([]models.ValueDiffEntry, 0, len(added))
	for _, d := range added {
		addedEntries = append(addedEntries, models.ValueDiffEntry{
			Path:     d.Path,
			Type:     string(d.Type),
			NewValue: d.NewValue,
		})
	}

	removedEntries := make([]models.ValueDiffEntry, 0, len(removed))
	for _, d := range removed {
		removedEntries = append(removedEntries, models.ValueDiffEntry{
			Path:     d.Path,
			Type:     string(d.Type),
			OldValue: d.OldValue,
		})
	}

	changedEntries := make([]models.ValueDiffEntry, 0, len(changed))
	for _, d := range changed {
		changedEntries = append(changedEntries, models.ValueDiffEntry{
			Path:     d.Path,
			Type:     string(d.Type),
			OldValue: d.OldValue,
			NewValue: d.NewValue,
		})
	}

	// Check for conflicts with global values
	var allConflicts []models.ConflictCheckEntry

	globalValuesPath := fmt.Sprintf("configuration/addons-global-values/%s.yaml", addonName)
	globalData, err := gp.GetFileContent(ctx, globalValuesPath, "main")
	if err != nil {
		log.Printf("Warning: could not fetch global values for %s: %v", addonName, err)
	} else {
		conflicts, err := helm.FindConflicts(string(globalData), oldValues, newValues)
		if err != nil {
			log.Printf("Warning: conflict check failed for global values: %v", err)
		} else {
			for _, c := range conflicts {
				allConflicts = append(allConflicts, models.ConflictCheckEntry{
					Path:            c.Path,
					ConfiguredValue: c.ConfiguredValue,
					OldDefault:      c.OldDefault,
					NewDefault:      c.NewDefault,
					Source:          "global",
				})
			}
		}
	}

	// Check for conflicts with per-cluster values
	clusterData, err := gp.GetFileContent(ctx, "configuration/cluster-addons.yaml", "main")
	if err != nil {
		log.Printf("Warning: could not fetch cluster addons config: %v", err)
	} else {
		clusters, err := s.parser.ParseClusterAddons(clusterData)
		if err != nil {
			log.Printf("Warning: could not parse cluster addons: %v", err)
		} else {
			for _, cluster := range clusters {
				// Check if this cluster has the addon enabled
				labelVal, hasAddon := cluster.Labels[addonName]
				if !hasAddon || !strings.EqualFold(labelVal, "enabled") {
					continue
				}

				// Try to fetch per-cluster addon values
				clusterValuesPath := fmt.Sprintf("configuration/addons-cluster-values/%s/%s.yaml", cluster.Name, addonName)
				clusterValuesData, err := gp.GetFileContent(ctx, clusterValuesPath, "main")
				if err != nil {
					// Cluster may not have per-addon overrides, that's fine
					continue
				}

				conflicts, err := helm.FindConflicts(string(clusterValuesData), oldValues, newValues)
				if err != nil {
					log.Printf("Warning: conflict check failed for cluster %s: %v", cluster.Name, err)
					continue
				}

				for _, c := range conflicts {
					allConflicts = append(allConflicts, models.ConflictCheckEntry{
						Path:            c.Path,
						ConfiguredValue: c.ConfiguredValue,
						OldDefault:      c.OldDefault,
						NewDefault:      c.NewDefault,
						Source:          cluster.Name,
					})
				}
			}
		}
	}

	totalChanges := len(addedEntries) + len(removedEntries) + len(changedEntries)

	// Fetch release notes for the target version
	releaseNotes, _ := s.fetcher.FetchReleaseNotes(ctx, addon.RepoURL, addon.Chart, targetVersion)

	return &models.UpgradeCheckResponse{
		AddonName:      addonName,
		Chart:          addon.Chart,
		CurrentVersion: currentVersion,
		TargetVersion:  targetVersion,
		TotalChanges:   totalChanges,
		Added:          addedEntries,
		Removed:        removedEntries,
		Changed:        changedEntries,
		Conflicts:      allConflicts,
		ReleaseNotes:   releaseNotes,
	}, nil
}
