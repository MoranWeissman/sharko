package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/MoranWeissman/sharko/internal/argocd"
	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/logging"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// parseJSONObject decodes data as a JSON object. Used by the values-editor
// schema lookup where we accept any well-formed JSON object as the schema —
// validation is the UI's job (monaco-yaml does it client-side).
func parseJSONObject(data []byte) (map[string]interface{}, error) {
	var out map[string]interface{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// extractEnvironment returns the environment segment from a cluster name by
// matching each dash-separated part against the configured environments list.
// e.g. "nms-core-dev-eks" with envs ["dev","prod"] → "dev"
func extractEnvironment(clusterName string, envs []string) string {
	if len(envs) == 0 {
		return ""
	}
	parts := strings.Split(clusterName, "-")
	for _, part := range parts {
		for _, env := range envs {
			if strings.EqualFold(part, env) {
				return env
			}
		}
	}
	return ""
}

// loadEnvironments reads the APP_ENVIRONMENTS env var and returns the list.
func loadEnvironments() []string {
	raw := os.Getenv("APP_ENVIRONMENTS")
	if raw == "" {
		return nil
	}
	var envs []string
	for _, e := range strings.Split(raw, ",") {
		if t := strings.TrimSpace(e); t != "" {
			envs = append(envs, t)
		}
	}
	return envs
}

// AddonService handles addon-related operations.
type AddonService struct {
	parser              *config.Parser
	managedClustersPath string // path in Git repo to managed-clusters.yaml

	// curated is the Marketplace's curated addon list (internal/catalog,
	// loaded from the embedded YAML at server startup), wired in via
	// SetCuratedCatalog. The v4 read branches pass it to
	// catalog.BuildCatalogView so an approved addon the Marketplace also
	// knows gets its description and docs link filled in. It NEVER adds an
	// addon: the org's catalog.yaml is the whole list. nil is safe —
	// every approved addon is then catalog.OriginInternal.
	curated *catalog.Catalog

	// baseBranchFn is the per-instance test seam (matches the nowFn /
	// tickInterval / gitProviderFn convention used across the codebase)
	// for the configured GitOps base branch. Wired via SetBaseBranchFn to
	// api.Server.GitopsBaseBranch in production so every read in this file
	// follows the connection's configured branch instead of a hardcoded
	// "main" (Wave 2 ride-along w2-q6 item 1). nil (the zero value, e.g.
	// in tests and the e2e harness that never call the setter) falls back
	// to "main" via the branch() helper below.
	baseBranchFn func() string
}

// SetCuratedCatalog wires in the Marketplace's curated list so the v4 read
// branches can fill in an approved addon's description and docs link. Pass
// nil (or skip the call) to leave every approved addon as
// catalog.OriginInternal — matches how internal/api/catalog_org.go's
// handlers treat a nil s.catalog (Server.catalog is itself optional, per
// router.go).
func (s *AddonService) SetCuratedCatalog(c *catalog.Catalog) {
	s.curated = c
}

// SetBaseBranchFn wires in a live accessor for the configured GitOps base
// branch (e.g. api.Server.GitopsBaseBranch). The function is called on
// every read rather than snapshotted once, so it stays correct across
// ReinitializeFromConnection hot-reloads.
func (s *AddonService) SetBaseBranchFn(fn func() string) {
	s.baseBranchFn = fn
}

// branch returns the configured GitOps base branch, defaulting to "main"
// when no seam is wired (tests, e2e harness) or it resolves to "".
func (s *AddonService) branch() string {
	if s.baseBranchFn == nil {
		return "main"
	}
	if b := s.baseBranchFn(); b != "" {
		return b
	}
	return "main"
}

// NewAddonService creates a new AddonService.
// managedClustersPath is the Git repo path to managed-clusters.yaml.
// An empty string defaults to "configuration/managed-clusters.yaml".
func NewAddonService(managedClustersPath string) *AddonService {
	if managedClustersPath == "" {
		managedClustersPath = "configuration/managed-clusters.yaml"
	}
	return &AddonService{
		parser:              config.NewParser(),
		managedClustersPath: managedClustersPath,
	}
}

// ListAddons returns the raw addon catalog from Git.
//
// v4-repo detection: identical probe to GetVersionMatrix/GetCatalog — the
// engine pin (orchestrator.EnginePinPath) resolving to non-empty content on
// the base branch. A v4 repo has no configuration/addons-catalog.yaml (the
// v3→v4 migration deletes it and a fresh v4 repo never had one), so the v4
// branch reads the org's catalog instead.
func (s *AddonService) ListAddons(ctx context.Context, gp gitprovider.GitProvider) ([]models.AddonCatalogEntry, error) {
	if pinContent, pinErr := gp.GetFileContent(ctx, orchestrator.EnginePinPath, s.branch()); pinErr == nil && len(pinContent) > 0 {
		return s.listAddonsV4(ctx, gp)
	}

	catalogData, err := gp.GetFileContent(ctx, "configuration/addons-catalog.yaml", s.branch())
	if err != nil {
		if isGitFileNotFound(err) {
			catalogData = []byte("applicationsets: []")
		} else {
			return nil, fmt.Errorf("reading addons-catalog.yaml: %w", err)
		}
	}

	return s.parser.ParseAddonsCatalog(catalogData)
}

// listAddonsV4 is ListAddons' v4-repo branch. It reads the org's approved
// addons out of catalog.yaml and flattens them into the same
// []models.AddonCatalogEntry shape ListAddons' v3 branch returns — callers
// (handleListAddons, notifications.ServiceProvider) only ever read
// Name/Chart/RepoURL/Version/Namespace off these entries, all of which a
// catalog.CatalogAddon carries directly.
func (s *AddonService) listAddonsV4(ctx context.Context, gp gitprovider.GitProvider) ([]models.AddonCatalogEntry, error) {
	deltaData, err := gp.GetFileContent(ctx, config.AddonCatalogPath, s.branch())
	var delta config.AddonCatalogSpec
	if err != nil {
		if !isGitFileNotFound(err) {
			return nil, fmt.Errorf("reading %s: %w", config.AddonCatalogPath, err)
		}
	} else {
		delta, err = config.LoadAddonCatalog(deltaData)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", config.AddonCatalogPath, err)
		}
	}

	merged := catalog.BuildCatalogView(s.curated, delta)

	names := make([]string, 0, len(merged))
	for name := range merged {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]models.AddonCatalogEntry, 0, len(names))
	for _, name := range names {
		out = append(out, mergedAddonToCatalogEntry(merged[name]))
	}
	return out, nil
}

// mergedAddonToCatalogEntry flattens a catalog.CatalogAddon into the
// []models.AddonCatalogEntry shape the v3 ListAddons/parser path returns.
// Secrets is intentionally left empty — catalog.CatalogAddon.Secrets carries
// catalog.SecretRequirement (knowledge-doc "what secrets this addon needs
// and why"), not models.AddonSecretRef (the deployment-time "which K8s
// Secret to create" spec parsed from the v3 catalog file) — the two are
// different concepts with no lossless conversion between them, and no v4
// caller of ListAddons reads AddonCatalogEntry.Secrets today.
func mergedAddonToCatalogEntry(m catalog.CatalogAddon) models.AddonCatalogEntry {
	entry := models.AddonCatalogEntry{
		Name:              m.Name,
		RepoURL:           m.RepoURL,
		Chart:             m.Chart,
		Version:           m.Version,
		Namespace:         m.Namespace,
		AdditionalSources: m.AdditionalSources,
		ExtraHelmValues:   m.ExtraHelmValues,
	}
	if m.Settings != nil {
		entry.SelfHeal = m.Settings.SelfHeal
		entry.SyncOptions = m.Settings.SyncOptions
		entry.IgnoreDifferences = m.Settings.IgnoreDifferences
	}
	return entry
}

// GetCatalog returns the full addon catalog with deployment stats across clusters.
//
// v4-repo detection: identical probe to GetVersionMatrix (the engine pin
// resolving to non-empty content on the base branch). See getCatalogV4 for
// the v4 branch. GetAddonDetail delegates to GetCatalog and needs no
// separate v4 branch of its own.
func (s *AddonService) GetCatalog(ctx context.Context, gp gitprovider.GitProvider, ac *argocd.Client) (*models.AddonCatalogResponse, error) {
	if pinContent, pinErr := gp.GetFileContent(ctx, orchestrator.EnginePinPath, s.branch()); pinErr == nil && len(pinContent) > 0 {
		return s.getCatalogV4(ctx, gp, ac)
	}

	log := logging.LoggerFromContext(ctx)
	clusterData, err := gp.GetFileContent(ctx, s.managedClustersPath, s.branch())
	if err != nil {
		if isGitFileNotFound(err) {
			clusterData = []byte("clusters: []")
		} else {
			return nil, fmt.Errorf("reading managed-clusters.yaml: %w", err)
		}
	}

	catalogData, err := gp.GetFileContent(ctx, "configuration/addons-catalog.yaml", s.branch())
	if err != nil {
		if isGitFileNotFound(err) {
			catalogData = []byte("applicationsets: []")
		} else {
			return nil, fmt.Errorf("reading addons-catalog.yaml: %w", err)
		}
	}

	repoCfg, err := s.parser.ParseAll(clusterData, catalogData)
	if err != nil {
		return nil, err
	}

	// Fetch ArgoCD applications
	argocdSvc := argocd.NewService(ac)
	allApps, err := ac.ListApplications(ctx)
	if err != nil {
		log.Warn("could not fetch argocd applications", "error", err)
	}

	appMap := make(map[string]models.ArgocdApplication)
	for _, app := range allApps {
		appMap[app.Name] = app
	}

	envList := loadEnvironments()
	items := make([]models.AddonCatalogItem, 0, len(repoCfg.Addons))
	totalClusters := len(repoCfg.Clusters)
	addonsOnlyInGit := 0

	for _, addon := range repoCfg.Addons {
		item := models.AddonCatalogItem{
			AddonName: addon.Name,
			Chart:     addon.Chart,
			RepoURL:   addon.RepoURL,
			Namespace: addon.Namespace,
			Version:   addon.Version,
		}

		// Check which clusters have this addon
		deployments := make([]models.AddonDeploymentInfo, 0)
		enabledCount, healthyCount, degradedCount, missingCount := 0, 0, 0, 0
		// N = clusters where the ArgoCD Application is BOTH Synced AND
		// Healthy. Stricter than the existing healthyCount (which only
		// checks HealthStatus) so the "Running on N/M clusters" tile
		// badge means "actually serving traffic", not "is healthy but
		// possibly out of sync".
		deployedCount := 0

		for _, cluster := range repoCfg.Clusters {
			labelVal, hasAddon := cluster.Labels[addon.Name]
			if !hasAddon {
				continue
			}

			// Only care about enabled addons — disabled is the same as not configured
			enabled := labelVal == "enabled"
			if !enabled {
				continue
			}
			enabledCount++

			// Version for this cluster
			version := addon.Version
			versionKey := addon.Name + "-version"
			if override, ok := cluster.Labels[versionKey]; ok {
				version = override
			}

			dep := models.AddonDeploymentInfo{
				ClusterName:        cluster.Name,
				ClusterEnvironment: extractEnvironment(cluster.Name, envList),
				Enabled:            true,
				ConfiguredVersion:  version,
				Namespace:          addon.Namespace,
			}

			{
				// Check ArgoCD status
				appName := addon.Name + "-" + cluster.Name
				if app, ok := appMap[appName]; ok {
					dep.SyncStatus = app.SyncStatus
					dep.HealthStatus = app.HealthStatus
					dep.DeployedVersion = app.SourceTargetRevision
					dep.ApplicationName = app.Name

					dep.Status, _ = classifyAddonApp(app)
					switch dep.Status {
					case "healthy":
						healthyCount++
					case "sync_failing", "unhealthy":
						// sync_failing counts as degraded for the tile counters so the
						// catalog health bar and unhealthy chip reflect the real situation.
						degradedCount++
					}
					if app.SyncStatus == "Synced" && app.HealthStatus == "Healthy" {
						deployedCount++
					}
				} else {
					dep.Status = "missing"
					missingCount++
				}
			}

			deployments = append(deployments, dep)
		}

		item.TotalClusters = len(deployments)
		item.EnabledClusters = enabledCount
		item.HealthyApplications = healthyCount
		item.DegradedApplications = degradedCount
		item.MissingApplications = missingCount
		// Tile badge sources:
		//   M = clusters where the addon is labelled enabled.
		//   N = clusters where the addon's ArgoCD Application is Synced + Healthy.
		item.TotalTargetClusterCount = enabledCount
		item.DeployedClusterCount = deployedCount
		item.Applications = deployments

		if enabledCount == 0 {
			addonsOnlyInGit++
		}

		items = append(items, item)
	}

	_ = argocdSvc // used for future enrichment

	return &models.AddonCatalogResponse{
		Addons:          items,
		TotalAddons:     len(items),
		TotalClusters:   totalClusters,
		AddonsOnlyInGit: addonsOnlyInGit,
	}, nil
}

// getCatalogV4 is GetCatalog's v4-repo branch. It builds the same
// models.AddonCatalogResponse shape as the v3 branch (Marketplace/browse
// surface — GET /addons/catalog), but sources per-cluster enablement from
// clusters/*.yaml (kind ClusterAddons) instead of managed-clusters.yaml
// labels, and the addon set from the org's catalog
// (catalog.MergeDelta(curated, catalog.yaml)) instead of
// addons-catalog.yaml — mirroring getVersionMatrixV4 exactly, including the
// "<addon>-<cluster>" ArgoCD Application naming convention. GetAddonDetail
// needs no v4 branch of its own: it delegates to GetCatalog and scans the
// result for the requested addon name, so this branch covers it too.
func (s *AddonService) getCatalogV4(ctx context.Context, gp gitprovider.GitProvider, ac *argocd.Client) (*models.AddonCatalogResponse, error) {
	log := logging.LoggerFromContext(ctx)

	clusterAddons, err := listClusterAddonsSpecs(ctx, gp, s.branch())
	if err != nil {
		return nil, fmt.Errorf("reading clusters/*.yaml: %w", err)
	}

	deltaData, err := gp.GetFileContent(ctx, config.AddonCatalogPath, s.branch())
	var delta config.AddonCatalogSpec
	if err != nil {
		if !isGitFileNotFound(err) {
			return nil, fmt.Errorf("reading %s: %w", config.AddonCatalogPath, err)
		}
		// Missing catalog.yaml means empty delta (design doc D16,
		// "missing means empty") — matches getVersionMatrixV4.
	} else {
		delta, err = config.LoadAddonCatalog(deltaData)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", config.AddonCatalogPath, err)
		}
	}

	merged := catalog.BuildCatalogView(s.curated, delta)

	allApps, err := ac.ListApplications(ctx)
	if err != nil {
		log.Warn("could not fetch argocd applications", "error", err)
	}
	appMap := make(map[string]models.ArgocdApplication, len(allApps))
	for _, app := range allApps {
		appMap[app.Name] = app
	}

	addonNames := make([]string, 0, len(merged))
	for name := range merged {
		addonNames = append(addonNames, name)
	}
	sort.Strings(addonNames)

	clusterNames := make([]string, 0, len(clusterAddons))
	for name := range clusterAddons {
		clusterNames = append(clusterNames, name)
	}
	sort.Strings(clusterNames)

	envList := loadEnvironments()
	items := make([]models.AddonCatalogItem, 0, len(addonNames))
	addonsOnlyInGit := 0

	for _, addonName := range addonNames {
		addon := merged[addonName]
		item := models.AddonCatalogItem{
			AddonName: addonName,
			Chart:     addon.Chart,
			RepoURL:   addon.RepoURL,
			Namespace: addon.Namespace,
			Version:   addon.Version,
		}

		deployments := make([]models.AddonDeploymentInfo, 0)
		enabledCount, healthyCount, degradedCount, missingCount := 0, 0, 0, 0
		deployedCount := 0

		for _, clusterName := range clusterNames {
			ca, hasAddon := clusterAddons[clusterName].Addons[addonName]
			if !hasAddon {
				continue
			}

			// Only care about enabled addons — disabled is the same as not
			// configured, matching the v3 branch's semantics.
			if !ca.Enabled {
				continue
			}
			enabledCount++

			// "No pin = follow the catalog default" (design doc §2.1) —
			// the ONLY per-cluster version pin location in the v4 format.
			version := ca.Version
			if version == "" {
				version = addon.Version
			}

			dep := models.AddonDeploymentInfo{
				ClusterName:        clusterName,
				ClusterEnvironment: extractEnvironment(clusterName, envList),
				Enabled:            true,
				ConfiguredVersion:  version,
				Namespace:          addon.Namespace,
			}

			appName := addonName + "-" + clusterName
			if app, ok := appMap[appName]; ok {
				dep.SyncStatus = app.SyncStatus
				dep.HealthStatus = app.HealthStatus
				dep.DeployedVersion = app.SourceTargetRevision
				dep.ApplicationName = app.Name

				dep.Status, _ = classifyAddonApp(app)
				switch dep.Status {
				case "healthy":
					healthyCount++
				case "sync_failing", "unhealthy":
					degradedCount++
				}
				if app.SyncStatus == "Synced" && app.HealthStatus == "Healthy" {
					deployedCount++
				}
			} else {
				dep.Status = "missing"
				missingCount++
			}

			deployments = append(deployments, dep)
		}

		item.TotalClusters = len(deployments)
		item.EnabledClusters = enabledCount
		item.HealthyApplications = healthyCount
		item.DegradedApplications = degradedCount
		item.MissingApplications = missingCount
		item.TotalTargetClusterCount = enabledCount
		item.DeployedClusterCount = deployedCount
		item.Applications = deployments

		if enabledCount == 0 {
			addonsOnlyInGit++
		}

		items = append(items, item)
	}

	return &models.AddonCatalogResponse{
		Addons:          items,
		TotalAddons:     len(items),
		TotalClusters:   len(clusterAddons),
		AddonsOnlyInGit: addonsOnlyInGit,
	}, nil
}

// GetAddonDetail returns detailed information about a specific addon.
func (s *AddonService) GetAddonDetail(ctx context.Context, addonName string, gp gitprovider.GitProvider, ac *argocd.Client) (*models.AddonDetailResponse, error) {
	log := logging.LoggerFromContext(ctx)
	catalog, err := s.GetCatalog(ctx, gp, ac)
	if err != nil {
		return nil, err
	}

	for _, item := range catalog.Addons {
		if item.AddonName == addonName {
			resp := &models.AddonDetailResponse{
				Addon: item,
			}

			// Enrich with ApplicationSet status from ArgoCD (best-effort).
			if appSetStatus, err := ac.GetApplicationSet(ctx, addonName); err == nil {
				info := &models.ApplicationSetStatusInfo{
					Name:          appSetStatus.Name,
					GeneratedApps: appSetStatus.GeneratedApps,
				}
				for _, c := range appSetStatus.Conditions {
					info.Conditions = append(info.Conditions, models.ApplicationSetCondition{
						Type:    c.Type,
						Status:  c.Status,
						Message: c.Message,
					})
				}
				resp.ApplicationSet = info
			} else {
				log.Warn("could not fetch applicationset status", "addon", addonName, "error", err)
			}

			return resp, nil
		}
	}

	return nil, nil
}

// GetVersionMatrix returns a version matrix showing addon versions and
// health across all clusters.
//
// v4-repo detection (v4 Wave 1 Story 4.2): the presence of the engine pin
// (orchestrator.EnginePinPath, "engine.yaml") on the base
// branch is the single signal Sharko already uses to distinguish a v4
// repo from a v3 one — CheckEnginePin (internal/orchestrator/enginepin.go)
// uses the identical probe for the same reason: "no pin found" is the
// ordinary, non-error "not a v4 repo yet" case, never a hard failure. When
// the pin is present, the matrix is built from clusters/*.yaml (kind
// ClusterAddons) and the org's catalog (catalog.yaml
// overlaid on s.curated, wired via SetCuratedCatalog) instead of
// managed-clusters.yaml labels and addons-catalog.yaml. s.curated may be
// nil (no embedded catalog loaded); every addon then merges as
// catalog.OriginInternal, matching catalog.MergeDelta's own contract.
//
// v3 path: when managed-clusters.yaml or addons-catalog.yaml is missing
// (e.g. fresh-install gitops repo with no clusters yet) the matrix
// degrades to an empty response rather than propagating a 500 with the
// raw filesystem error string. Same pattern as ClusterService.ListClusters:
// isGitFileNotFound uses errors.Is on gitprovider.ErrFileNotFound (and
// fs.ErrNotExist) so legitimate auth/branch/perm errors that happen to
// mention "404" still surface as real 5xx — only the genuinely
// missing-file path short-circuits.
func (s *AddonService) GetVersionMatrix(ctx context.Context, gp gitprovider.GitProvider, ac *argocd.Client) (*models.VersionMatrixResponse, error) {
	// Non-empty-content check (not just err == nil) because some
	// GitProvider fakes in this codebase's own test suites return
	// (nil, nil) rather than an error for an unknown path — matching the
	// stricter check keeps v4 detection correct against both real
	// providers (which error) and those doubles.
	if pinContent, pinErr := gp.GetFileContent(ctx, orchestrator.EnginePinPath, s.branch()); pinErr == nil && len(pinContent) > 0 {
		return s.getVersionMatrixV4(ctx, gp, ac)
	}

	log := logging.LoggerFromContext(ctx)
	clusterData, err := gp.GetFileContent(ctx, s.managedClustersPath, s.branch())
	if err != nil {
		if isGitFileNotFound(err) {
			clusterData = []byte("clusters: []")
		} else {
			return nil, fmt.Errorf("reading managed-clusters.yaml: %w", err)
		}
	}

	catalogData, err := gp.GetFileContent(ctx, "configuration/addons-catalog.yaml", s.branch())
	if err != nil {
		if isGitFileNotFound(err) {
			catalogData = []byte("applicationsets: []")
		} else {
			return nil, fmt.Errorf("reading addons-catalog.yaml: %w", err)
		}
	}

	repoCfg, err := s.parser.ParseAll(clusterData, catalogData)
	if err != nil {
		return nil, err
	}

	// Fetch ArgoCD applications once and build a lookup map
	allApps, err := ac.ListApplications(ctx)
	if err != nil {
		log.Warn("could not fetch argocd applications", "error", err)
	}

	appMap := make(map[string]models.ArgocdApplication)
	for _, app := range allApps {
		appMap[app.Name] = app
	}

	// Collect sorted cluster names
	clusterNames := make([]string, 0, len(repoCfg.Clusters))
	for _, cluster := range repoCfg.Clusters {
		clusterNames = append(clusterNames, cluster.Name)
	}
	sort.Strings(clusterNames)

	// Build a map for quick cluster lookup by name
	clusterMap := make(map[string]models.Cluster, len(repoCfg.Clusters))
	for _, cluster := range repoCfg.Clusters {
		clusterMap[cluster.Name] = cluster
	}

	// Build rows
	rows := make([]models.VersionMatrixRow, 0, len(repoCfg.Addons))
	for _, addon := range repoCfg.Addons {
		row := models.VersionMatrixRow{
			AddonName:      addon.Name,
			CatalogVersion: addon.Version,
			Chart:          addon.Chart,
			Cells:          make(map[string]models.VersionMatrixCell),
		}

		for _, clusterName := range clusterNames {
			cluster := clusterMap[clusterName]
			labelVal, hasLabel := cluster.Labels[addon.Name]
			if !hasLabel {
				continue
			}

			enabled := labelVal == "enabled"

			// Determine version
			version := addon.Version
			versionKey := addon.Name + "-version"
			if override, ok := cluster.Labels[versionKey]; ok {
				version = override
			}

			cell := models.VersionMatrixCell{
				Version:          version,
				DriftFromCatalog: version != addon.Version,
			}

			if !enabled {
				cell.Health = "not_enabled"
			} else {
				// Check ArgoCD status
				appName := addon.Name + "-" + clusterName
				if app, ok := appMap[appName]; ok {
					cell.Health = app.HealthStatus
					if cell.Health == "" {
						cell.Health = "Unknown"
					}
				} else {
					cell.Health = "missing"
				}
			}

			row.Cells[clusterName] = cell
		}

		rows = append(rows, row)
	}

	// Sort rows by addon name
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].AddonName < rows[j].AddonName
	})

	return &models.VersionMatrixResponse{
		Clusters: clusterNames,
		Addons:   rows,
	}, nil
}

// getVersionMatrixV4 is GetVersionMatrix's v4-repo branch (v4 Wave 1 Story
// 4.2). It reads clusters/*.yaml (kind ClusterAddons, one file per
// cluster — design doc §2.1) instead of managed-clusters.yaml labels, and
// the org's catalog (catalog.yaml)
// instead of addons-catalog.yaml. ArgoCD Application health is looked up by
// the SAME "<addon>-<cluster>" naming convention the v3 path uses — the
// engine chart's generated Applications are named identically
// (charts/sharko-engine/templates/appset.yaml: '{{ $name }}-{{ .name }}').
func (s *AddonService) getVersionMatrixV4(ctx context.Context, gp gitprovider.GitProvider, ac *argocd.Client) (*models.VersionMatrixResponse, error) {
	log := logging.LoggerFromContext(ctx)

	clusterAddons, err := listClusterAddonsSpecs(ctx, gp, s.branch())
	if err != nil {
		return nil, fmt.Errorf("reading clusters/*.yaml: %w", err)
	}

	deltaData, err := gp.GetFileContent(ctx, config.AddonCatalogPath, s.branch())
	var delta config.AddonCatalogSpec
	if err != nil {
		if !isGitFileNotFound(err) {
			return nil, fmt.Errorf("reading %s: %w", config.AddonCatalogPath, err)
		}
		// Missing catalog.yaml means empty delta (design doc D16,
		// "missing means empty") — not an error, mirrors
		// internal/api/catalog_delta.go's loadCatalogDelta.
	} else {
		delta, err = config.LoadAddonCatalog(deltaData)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", config.AddonCatalogPath, err)
		}
	}

	merged := catalog.BuildCatalogView(s.curated, delta)

	allApps, err := ac.ListApplications(ctx)
	if err != nil {
		log.Warn("could not fetch argocd applications", "error", err)
	}
	appMap := make(map[string]models.ArgocdApplication, len(allApps))
	for _, app := range allApps {
		appMap[app.Name] = app
	}

	clusterNames := make([]string, 0, len(clusterAddons))
	for name := range clusterAddons {
		clusterNames = append(clusterNames, name)
	}
	sort.Strings(clusterNames)

	addonNames := make([]string, 0, len(merged))
	for name := range merged {
		addonNames = append(addonNames, name)
	}
	sort.Strings(addonNames)

	rows := make([]models.VersionMatrixRow, 0, len(addonNames))
	for _, addonName := range addonNames {
		addon := merged[addonName]
		row := models.VersionMatrixRow{
			AddonName:      addonName,
			CatalogVersion: addon.Version,
			Chart:          addon.Chart,
			Cells:          make(map[string]models.VersionMatrixCell),
		}

		for _, clusterName := range clusterNames {
			ca, hasAddon := clusterAddons[clusterName].Addons[addonName]
			if !hasAddon {
				continue
			}

			// "No pin = follow the catalog default" (design doc §2.1) —
			// the ONLY per-cluster version pin location in the v4 format.
			version := ca.Version
			if version == "" {
				version = addon.Version
			}

			cell := models.VersionMatrixCell{
				Version:          version,
				DriftFromCatalog: version != addon.Version,
			}

			if !ca.Enabled {
				cell.Health = "not_enabled"
			} else {
				appName := addonName + "-" + clusterName
				if app, ok := appMap[appName]; ok {
					cell.Health = app.HealthStatus
					if cell.Health == "" {
						cell.Health = "Unknown"
					}
				} else {
					cell.Health = "missing"
				}
			}

			row.Cells[clusterName] = cell
		}

		rows = append(rows, row)
	}

	return &models.VersionMatrixResponse{
		Clusters: clusterNames,
		Addons:   rows,
	}, nil
}

// listClusterAddonsSpecs lists clusters/*.yaml and parses each into a
// ClusterAddonsSpec, keyed by cluster name. An empty (or absent —
// pre-first-cluster v4 repos have only clusters/.gitkeep) directory
// returns an empty, non-nil map rather than an error.
func listClusterAddonsSpecs(ctx context.Context, gp gitprovider.GitProvider, baseBranch string) (map[string]models.ClusterAddonsSpec, error) {
	entries, err := gp.ListDirectory(ctx, "clusters", baseBranch)
	if err != nil {
		if isGitFileNotFound(err) {
			return map[string]models.ClusterAddonsSpec{}, nil
		}
		return nil, err
	}

	out := make(map[string]models.ClusterAddonsSpec, len(entries))
	for _, name := range entries {
		if !strings.HasSuffix(name, ".yaml") {
			continue // .gitkeep and any non-YAML entry
		}
		data, readErr := gp.GetFileContent(ctx, path.Join("clusters", name), baseBranch)
		if readErr != nil {
			return nil, fmt.Errorf("reading clusters/%s: %w", name, readErr)
		}
		spec, parseErr := models.LoadClusterAddons(data)
		if parseErr != nil {
			return nil, fmt.Errorf("parsing clusters/%s: %w", name, parseErr)
		}
		out[spec.Cluster] = spec
	}
	return out, nil
}

// GetAddonValues returns the global default values YAML for a specific addon.
func (s *AddonService) GetAddonValues(ctx context.Context, addonName string, gp gitprovider.GitProvider) (*models.AddonValuesResponse, error) {
	path := fmt.Sprintf("configuration/addons-global-values/%s.yaml", addonName)
	data, err := gp.GetFileContent(ctx, path, s.branch())
	if err != nil {
		return nil, fmt.Errorf("failed to fetch global values for addon %s: %w", addonName, err)
	}

	return &models.AddonValuesResponse{
		AddonName:  addonName,
		ValuesYAML: string(data),
	}, nil
}

// GetAddonValuesAndSchema returns the current global values YAML for an
// addon plus an optional JSON Schema. The schema is read best-effort from
// `configuration/addons-global-values/<addonName>.schema.json`; if absent or
// unparseable, the returned schema is nil and the caller falls back to a
// plain YAML editor.
//
// Unlike GetAddonValues this returns an empty CurrentValues string (rather
// than an error) when the file does not exist yet, so the editor can still
// open with a blank document for first-time configuration.
func (s *AddonService) GetAddonValuesAndSchema(ctx context.Context, addonName string, gp gitprovider.GitProvider) (*models.AddonValuesSchemaResponse, error) {
	log := logging.LoggerFromContext(ctx)
	if addonName == "" {
		return nil, fmt.Errorf("addon name is required")
	}

	valuesPath := fmt.Sprintf("configuration/addons-global-values/%s.yaml", addonName)
	current := ""
	if data, err := gp.GetFileContent(ctx, valuesPath, s.branch()); err == nil {
		current = string(data)
	} else {
		log.Info("global values file missing — opening editor blank", "addon", addonName, "path", valuesPath)
	}

	resp := &models.AddonValuesSchemaResponse{
		AddonName:     addonName,
		CurrentValues: current,
	}

	schemaPath := fmt.Sprintf("configuration/addons-global-values/%s.schema.json", addonName)
	if schemaData, err := gp.GetFileContent(ctx, schemaPath, s.branch()); err == nil && len(schemaData) > 0 {
		schema, perr := parseJSONObject(schemaData)
		if perr != nil {
			log.Warn("ignoring unparseable values schema", "addon", addonName, "error", perr)
		} else {
			resp.Schema = schema
		}
	}

	return resp, nil
}
