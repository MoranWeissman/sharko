package service

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/MoranWeissman/sharko/internal/advisories"
	"github.com/MoranWeissman/sharko/internal/ai"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/helm"
	"github.com/MoranWeissman/sharko/internal/models"
)

// semverParts holds the parsed major.minor.patch components of a version string.
type semverParts struct {
	major int
	minor int
	patch int
	pre   string // non-empty if prerelease (contains '-')
}

// parseSemver parses a version string like "1.2.3" or "1.2.3-alpha.1".
// Returns ok=false if the string does not look like a valid semver.
func parseSemver(v string) (p semverParts, ok bool) {
	// Strip optional leading 'v'
	v = strings.TrimPrefix(v, "v")

	// Split off prerelease
	if idx := strings.IndexByte(v, '-'); idx >= 0 {
		p.pre = v[idx+1:]
		v = v[:idx]
	}

	parts := strings.SplitN(v, ".", 3)
	if len(parts) != 3 {
		return p, false
	}

	var err error
	if p.major, err = strconv.Atoi(parts[0]); err != nil {
		return p, false
	}
	if p.minor, err = strconv.Atoi(parts[1]); err != nil {
		return p, false
	}
	if p.patch, err = strconv.Atoi(parts[2]); err != nil {
		return p, false
	}
	return p, true
}

// UpgradeService handles upgrade impact checking operations.
type UpgradeService struct {
	parser              *config.Parser
	fetcher             *helm.Fetcher
	ai                  *ai.Client
	advisories          advisorySource
	managedClustersPath string // path in Git repo to managed-clusters.yaml
}

// advisorySource is the subset of advisories.Service used by UpgradeService.
// Defined here (consumed-side) so tests can inject a mock without importing the package.
type advisorySource interface {
	Get(ctx context.Context, repoURL, chart string) ([]advisories.Advisory, error)
}

// NewUpgradeService creates a new UpgradeService.
// managedClustersPath is the Git repo path to managed-clusters.yaml.
// An empty string defaults to "configuration/managed-clusters.yaml".
func NewUpgradeService(aiClient *ai.Client, advSvc *advisories.Service, managedClustersPath string) *UpgradeService {
	if managedClustersPath == "" {
		managedClustersPath = "configuration/managed-clusters.yaml"
	}
	return &UpgradeService{
		parser:              config.NewParser(),
		fetcher:             helm.NewFetcher(),
		ai:                  aiClient,
		advisories:          advSvc,
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

// GetRecommendations returns smart upgrade recommendations for an addon:
// next patch (safe bugfix), next minor (feature update), and latest stable.
// It also builds security-aware RecommendationCards using advisory data from ArtifactHub.
func (s *UpgradeService) GetRecommendations(ctx context.Context, addonName string, gp gitprovider.GitProvider) (*models.UpgradeRecommendations, error) {
	// Fetch catalog to get current version and chart info
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

	current := addon.Version
	rec := &models.UpgradeRecommendations{CurrentVersion: current}

	cur, ok := parseSemver(current)
	if !ok {
		// Current version is not valid semver — return empty recommendations
		return rec, nil
	}

	// Fetch all versions from Helm repo
	chartVersions, err := s.fetcher.ListVersions(ctx, addon.RepoURL, addon.Chart)
	if err != nil {
		return nil, fmt.Errorf("listing chart versions: %w", err)
	}

	// Fetch advisories (best-effort — failures are logged, not returned as errors).
	advisoryMap := s.fetchAdvisoryMap(ctx, addon.RepoURL, addon.Chart)

	// Iterate over all versions and compute recommendations.
	// chartVersions is sorted latest-first by the Helm repo index.
	//
	// Root-cause fix (v1.21 QA Bundle 4, Fix #7 — downgrade as "latest"):
	// every candidate slot must be STRICTLY GREATER than the current
	// version. Without this guard the maintainer reported seeing
	// `velero@12.0.0` recommend "latest stable 11.4.0" — a downgrade
	// dressed up as a recommendation, because `latestStable` used to be
	// "first stable seen" regardless of how it compared to current.
	// `latestStable` is now "highest stable that is also newer than
	// current"; if no such version exists the slot stays empty.
	var (
		nextPatch    string
		nextPatchP   semverParts
		nextMinor    string
		nextMinorP   semverParts
		latestStable string
		latestStableP semverParts
	)

	for _, cv := range chartVersions {
		v := cv.Version
		p, valid := parseSemver(v)
		if !valid {
			continue
		}
		// Skip prereleases
		if p.pre != "" {
			continue
		}
		// Skip the current version itself.
		if p.major == cur.major && p.minor == cur.minor && p.patch == cur.patch {
			continue
		}

		// Skip anything that's not strictly newer than current — never
		// recommend a downgrade.
		if !semverGreater(p, cur) {
			continue
		}

		// Latest stable: highest version overall (we take the first one
		// because the list is sorted latest-first AND we already enforced
		// "strictly newer than current").
		if latestStable == "" || semverGreater(p, latestStableP) {
			latestStable = v
			latestStableP = p
		}

		// Next patch: same major.minor, higher patch
		if p.major == cur.major && p.minor == cur.minor && p.patch > cur.patch {
			if nextPatch == "" || p.patch > nextPatchP.patch {
				nextPatch = v
				nextPatchP = p
			}
		}

		// Next minor: same major, higher minor, latest patch of that minor
		if p.major == cur.major && p.minor > cur.minor {
			if nextMinor == "" || p.minor > nextMinorP.minor ||
				(p.minor == nextMinorP.minor && p.patch > nextMinorP.patch) {
				nextMinor = v
				nextMinorP = p
			}
		}
	}

	// Compute nextMajorVer: latest stable version in the smallest major > cur.major.
	// We scan all versions to find the smallest such major, then pick the highest
	// stable patch within it. By construction this is always strictly newer than
	// current, so no extra downgrade filter is needed here.
	var (
		nextMajorVer  string
		nextMajorVerP semverParts
		nextMajorNum  = -1 // smallest major > cur.major found so far
	)
	for _, cv := range chartVersions {
		p, valid := parseSemver(cv.Version)
		if !valid || p.pre != "" {
			continue
		}
		if p.major <= cur.major {
			continue
		}
		// Find the smallest major > cur.major
		if nextMajorNum == -1 || p.major < nextMajorNum {
			nextMajorNum = p.major
			nextMajorVer = cv.Version
			nextMajorVerP = p
		} else if p.major == nextMajorNum {
			// Within that major, keep the highest minor.patch
			if p.minor > nextMajorVerP.minor ||
				(p.minor == nextMajorVerP.minor && p.patch > nextMajorVerP.patch) {
				nextMajorVer = cv.Version
				nextMajorVerP = p
			}
		}
	}
	_ = nextMajorVerP // used only for comparisons above

	// Only set a recommendation if it differs from the current version
	if nextPatch != "" && nextPatch != current {
		rec.NextPatch = nextPatch
	}
	if nextMinor != "" && nextMinor != current {
		rec.NextMinor = nextMinor
	}
	if latestStable != "" && latestStable != current {
		rec.LatestStable = latestStable
	}

	// Build security-aware cards.
	rec.Cards, rec.Recommended = buildCards(cur, nextPatch, nextMinor, nextMajorVer, latestStable, advisoryMap)

	// Fix #7 (v1.21 QA Bundle 4): when none of the candidate slots
	// produced a card and we've seen at least one stable version that is
	// not newer than current, mark the addon as "on latest". The UI uses
	// this to render an explicit reassurance banner instead of silently
	// hiding an empty panel (which previously surfaced a downgrade).
	if len(rec.Cards) == 0 {
		hasStable := false
		for _, cv := range chartVersions {
			if p, ok := parseSemver(cv.Version); ok && p.pre == "" {
				hasStable = true
				break
			}
		}
		if hasStable {
			rec.OnLatest = true
		}
	}

	return rec, nil
}

// semverGreater returns true when a > b in semver order. Prerelease tags
// are not compared here — callers strip pre-release versions out before
// they get to this function.
func semverGreater(a, b semverParts) bool {
	if a.major != b.major {
		return a.major > b.major
	}
	if a.minor != b.minor {
		return a.minor > b.minor
	}
	return a.patch > b.patch
}

// fetchAdvisoryMap retrieves advisory data and returns a map of version → Advisory.
// Errors are logged and an empty map is returned so recommendations still work offline.
func (s *UpgradeService) fetchAdvisoryMap(ctx context.Context, repoURL, chart string) map[string]advisories.Advisory {
	result := make(map[string]advisories.Advisory)
	if s.advisories == nil {
		return result
	}
	advList, err := s.advisories.Get(ctx, repoURL, chart)
	if err != nil {
		slog.Warn("advisories fetch failed, proceeding without security data",
			"repo_url", repoURL, "chart", chart, "err", err)
		return result
	}
	for _, a := range advList {
		result[a.Version] = a
	}
	return result
}

// buildCards constructs RecommendationCards from the semver candidates and advisory map,
// then selects the recommended card. Returns (cards, recommendedVersion).
//
// Defensive guard (v1.21 QA Bundle 4, Fix #7): each candidate version is
// double-checked against `cur` and dropped if it isn't strictly greater.
// The upstream selection logic in GetRecommendations already filters
// downgrades out, but this layer is the final emit point for the UI cards
// — keeping the filter here prevents future regressions from a caller
// supplying a stale-cached `latestVer` string that points behind `cur`.
func buildCards(cur semverParts, patchVer, minorVer, nextMajorVer, latestVer string, advMap map[string]advisories.Advisory) ([]models.RecommendationCard, string) {
	type candidate struct {
		label   string
		version string
	}

	addIfNewer := func(c candidate) (candidate, bool) {
		if c.version == "" {
			return c, false
		}
		p, ok := parseSemver(c.version)
		if !ok {
			return c, false
		}
		if !semverGreater(p, cur) {
			return c, false
		}
		return c, true
	}

	// Deduplicate: skip in-major if it equals patch, skip latest if same as in-major or same major as current.
	var candidates []candidate

	if c, ok := addIfNewer(candidate{"Patch", patchVer}); ok {
		candidates = append(candidates, c)
	}
	if minorVer != "" && minorVer != patchVer {
		if c, ok := addIfNewer(candidate{fmt.Sprintf("Latest in %d.x", cur.major), minorVer}); ok {
			candidates = append(candidates, c)
		}
	}
	if nextMajorVer != "" {
		p, ok := parseSemver(nextMajorVer)
		// Only add if it's not the same as what we'd already show
		if ok && nextMajorVer != minorVer && nextMajorVer != patchVer && nextMajorVer != latestVer {
			if c, okNew := addIfNewer(candidate{fmt.Sprintf("Latest in %d.x", p.major), nextMajorVer}); okNew {
				candidates = append(candidates, c)
			}
		}
	}
	if latestVer != "" {
		p, ok := parseSemver(latestVer)
		if ok && p.major != cur.major && latestVer != minorVer && latestVer != patchVer {
			if c, okNew := addIfNewer(candidate{"Latest Stable", latestVer}); okNew {
				candidates = append(candidates, c)
			}
		}
	}

	if len(candidates) == 0 {
		return nil, ""
	}

	cards := make([]models.RecommendationCard, 0, len(candidates))
	for _, c := range candidates {
		adv, hasAdv := advMap[c.version]
		p, _ := parseSemver(c.version)
		crossMajor := p.major != cur.major

		card := models.RecommendationCard{
			Label:      c.label,
			Version:    c.version,
			CrossMajor: crossMajor,
			HasBreaking: crossMajor, // cross-major is always flagged as potentially breaking
		}
		if hasAdv {
			card.HasSecurity = adv.ContainsSecurityFix
			if adv.ContainsBreaking {
				card.HasBreaking = true
			}
			if adv.Summary != "" {
				card.AdvisorySummary = adv.Summary
			}
		}
		cards = append(cards, card)
	}

	// Pick the recommended card.
	recommendedIdx, reason := pickRecommended(cards)
	if recommendedIdx >= 0 {
		cards[recommendedIdx].IsRecommended = true
		cards[recommendedIdx].Reason = reason
		return cards, cards[recommendedIdx].Version
	}
	return cards, ""
}

// pickRecommended returns the index of the card Sharko recommends and a human-readable
// reason string explaining the selection.
//
// Priority order:
//  1. Patch card with security fix → safest upgrade path
//  2. In-major card with security fix
//  3. Any card with security fix
//  4. First card overall (patch if available, otherwise in-major, then latest)
func pickRecommended(cards []models.RecommendationCard) (int, string) {
	if len(cards) == 0 {
		return -1, ""
	}

	// Pass 1: patch card with security fix
	for i, c := range cards {
		if c.Label == "Patch" && c.HasSecurity {
			return i, "Lowest-risk path — applies a patch that contains security fixes"
		}
	}
	// Pass 2: in-major card with security fix
	for i, c := range cards {
		if !c.CrossMajor && c.HasSecurity {
			return i, "Stays in your current major while including security fixes"
		}
	}
	// Pass 3: any card with security fix (e.g., only latest stable has it)
	for i, c := range cards {
		if c.HasSecurity {
			return i, "Smallest version jump that includes security fixes"
		}
	}
	// Pass 4: no security fixes — pick the safest card based on version distance
	if len(cards) > 0 {
		c := cards[0]
		switch {
		case c.Label == "Patch":
			return 0, "Lowest-risk path — only patch-level changes"
		case !c.CrossMajor:
			return 0, "Latest stable in your current major — minimizes breaking changes"
		default:
			// Determine if there are multiple majors to step through
			for i, card := range cards {
				if card.Label == "Latest Stable" && i > 0 {
					// There's a stepping-stone card before Latest Stable
					return 0, "Stepping stone — moves you forward one major at a time"
				}
			}
			return 0, "Most up-to-date version — only choice that keeps you current"
		}
	}
	return 0, ""
}
