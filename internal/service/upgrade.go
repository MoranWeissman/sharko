package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/MoranWeissman/sharko/internal/ai"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/helm"
	"github.com/MoranWeissman/sharko/internal/models"
)

// UpgradeService handles upgrade impact checking operations.
type UpgradeService struct {
	parser              *config.Parser
	fetcher             *helm.Fetcher
	ai                  *ai.Client
	managedClustersPath string // path in Git repo to managed-clusters.yaml
}

// NewUpgradeService creates a new UpgradeService.
// managedClustersPath is the Git repo path to managed-clusters.yaml.
// An empty string defaults to "configuration/managed-clusters.yaml".
func NewUpgradeService(aiClient *ai.Client, managedClustersPath string) *UpgradeService {
	if managedClustersPath == "" {
		managedClustersPath = "configuration/managed-clusters.yaml"
	}
	return &UpgradeService{
		parser:              config.NewParser(),
		fetcher:             helm.NewFetcher(),
		ai:                  aiClient,
		managedClustersPath: managedClustersPath,
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
		if strings.Contains(err.Error(), "404") {
			catalogData = []byte("applicationsets: []")
		} else {
			return nil, fmt.Errorf("fetching addons catalog: %w", err)
		}
	}

	addons, err := s.parser.ParseAddonsCatalog(catalogData)
	if err != nil {
		return nil, fmt.Errorf("parsing addons catalog: %w", err)
	}

	var addon *models.AddonCatalogEntry
	for i := range addons {
		if addons[i].Name == addonName {
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
		AddonName:      addonName,
		Chart:          addon.Chart,
		RepoURL:        addon.RepoURL,
		CurrentVersion: addon.Version,
		Versions:       versions,
	}, nil
}

// CheckUpgrade performs an upgrade impact analysis comparing current and target chart versions.
func (s *UpgradeService) CheckUpgrade(ctx context.Context, addonName, targetVersion string, gp gitprovider.GitProvider) (*models.UpgradeCheckResponse, error) {
	// Get addon info from catalog
	catalogData, err := gp.GetFileContent(ctx, "configuration/addons-catalog.yaml", "main")
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			catalogData = []byte("applicationsets: []")
		} else {
			return nil, fmt.Errorf("fetching addons catalog: %w", err)
		}
	}

	addons, err := s.parser.ParseAddonsCatalog(catalogData)
	if err != nil {
		return nil, fmt.Errorf("parsing addons catalog: %w", err)
	}

	var addon *models.AddonCatalogEntry
	for i := range addons {
		if addons[i].Name == addonName {
			addon = &addons[i]
			break
		}
	}
	if addon == nil {
		return nil, fmt.Errorf("addon %q not found in catalog", addonName)
	}

	currentVersion := addon.Version
	var baselineNote string
	baselineUnavailable := false

	// Fetch values.yaml for current and target versions from Helm repo.
	// If the current version is missing from the repo, fall back to the nearest
	// available version or proceed without a baseline comparison.
	oldValues, err := s.fetcher.FetchValues(ctx, addon.RepoURL, addon.Chart, currentVersion)
	if err != nil {
		slog.Warn("current version not available in Helm repo, searching for fallback",
			"addon", addonName, "version", currentVersion, "error", err)

		// Try to find the nearest available version.
		nearestVersion, findErr := s.fetcher.FindNearestVersion(ctx, addon.RepoURL, addon.Chart, currentVersion)
		if findErr == nil && nearestVersion != "" {
			slog.Info("using fallback version for upgrade baseline",
				"addon", addonName, "original", currentVersion, "fallback", nearestVersion)
			oldValues, err = s.fetcher.FetchValues(ctx, addon.RepoURL, addon.Chart, nearestVersion)
			if err != nil {
				slog.Warn("fallback version fetch also failed", "version", nearestVersion, "error", err)
				oldValues = ""
				baselineUnavailable = true
				baselineNote = fmt.Sprintf("Current version %s is not available in the Helm repository. "+
					"Upgrade analysis shows target version details only.", currentVersion)
			} else {
				baselineNote = fmt.Sprintf("Note: %s not found in repo, using %s as baseline",
					currentVersion, nearestVersion)
			}
		} else {
			// No fallback found — proceed without comparison.
			oldValues = ""
			baselineUnavailable = true
			baselineNote = fmt.Sprintf("Current version %s is not available in the Helm repository. "+
				"Upgrade analysis shows target version details only.", currentVersion)
		}
	}

	newValues, err := s.fetcher.FetchValues(ctx, addon.RepoURL, addon.Chart, targetVersion)
	if err != nil {
		return nil, fmt.Errorf("fetching target version values: %w", err)
	}

	// Diff the two values.yaml files (if baseline is available).
	var addedEntries, removedEntries, changedEntries []models.ValueDiffEntry
	if oldValues != "" {
		added, removed, changed, diffErr := helm.DiffValues(oldValues, newValues)
		if diffErr != nil {
			return nil, fmt.Errorf("diffing values: %w", diffErr)
		}

		addedEntries = make([]models.ValueDiffEntry, 0, len(added))
		for _, d := range added {
			addedEntries = append(addedEntries, models.ValueDiffEntry{
				Path:     d.Path,
				Type:     string(d.Type),
				NewValue: d.NewValue,
			})
		}

		removedEntries = make([]models.ValueDiffEntry, 0, len(removed))
		for _, d := range removed {
			removedEntries = append(removedEntries, models.ValueDiffEntry{
				Path:     d.Path,
				Type:     string(d.Type),
				OldValue: d.OldValue,
			})
		}

		changedEntries = make([]models.ValueDiffEntry, 0, len(changed))
		for _, d := range changed {
			changedEntries = append(changedEntries, models.ValueDiffEntry{
				Path:     d.Path,
				Type:     string(d.Type),
				OldValue: d.OldValue,
				NewValue: d.NewValue,
			})
		}
	}

	// Check for conflicts with global values (skip if baseline unavailable).
	var allConflicts []models.ConflictCheckEntry

	if !baselineUnavailable {
		globalValuesPath := fmt.Sprintf("configuration/addons-global-values/%s.yaml", addonName)
		globalData, globalErr := gp.GetFileContent(ctx, globalValuesPath, "main")
		if globalErr != nil {
			slog.Warn("could not fetch global values", "addon", addonName, "error", globalErr)
		} else {
			conflicts, conflictErr := helm.FindConflicts(string(globalData), oldValues, newValues)
			if conflictErr != nil {
				slog.Warn("conflict check failed for global values", "error", conflictErr)
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
		clusterData, clusterErr := gp.GetFileContent(ctx, s.managedClustersPath, "main")
		if clusterErr != nil && strings.Contains(clusterErr.Error(), "404") {
			clusterData = []byte("clusters: []")
			clusterErr = nil
		}
		if clusterErr != nil {
			slog.Warn("could not fetch cluster addons config", "error", clusterErr)
		} else {
			clusters, parseErr := s.parser.ParseClusterAddons(clusterData)
			if parseErr != nil {
				slog.Warn("could not parse cluster addons", "error", parseErr)
			} else {
				for _, cluster := range clusters {
					labelVal, hasAddon := cluster.Labels[addonName]
					if !hasAddon || !strings.EqualFold(labelVal, "enabled") {
						continue
					}

					clusterValuesPath := fmt.Sprintf("configuration/addons-cluster-values/%s/%s.yaml", cluster.Name, addonName)
					clusterValuesData, cvErr := gp.GetFileContent(ctx, clusterValuesPath, "main")
					if cvErr != nil {
						continue
					}

					conflicts, conflictErr := helm.FindConflicts(string(clusterValuesData), oldValues, newValues)
					if conflictErr != nil {
						slog.Warn("conflict check failed for cluster", "cluster", cluster.Name, "error", conflictErr)
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
	}

	totalChanges := len(addedEntries) + len(removedEntries) + len(changedEntries)

	// Fetch release notes for the target version
	releaseNotes, _ := s.fetcher.FetchReleaseNotes(ctx, addon.RepoURL, addon.Chart, targetVersion)

	return &models.UpgradeCheckResponse{
		AddonName:           addonName,
		Chart:               addon.Chart,
		CurrentVersion:      currentVersion,
		TargetVersion:       targetVersion,
		TotalChanges:        totalChanges,
		Added:               addedEntries,
		Removed:             removedEntries,
		Changed:             changedEntries,
		Conflicts:           allConflicts,
		ReleaseNotes:        releaseNotes,
		BaselineUnavailable: baselineUnavailable,
		BaselineNote:        baselineNote,
	}, nil
}
