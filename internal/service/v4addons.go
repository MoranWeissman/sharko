package service

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// This file is the ONE shared place ClusterService and AddonService both
// read a v4 repo's addon layout from (catalog.yaml + cluster-addons/*.yaml)
// instead of the v3 layout (addons-catalog.yaml + managed-clusters.yaml
// labels). Before this file existed, AddonService had its own v4 dispatch
// (getCatalogV4/getVersionMatrixV4) and ClusterService had NONE — every
// cluster page (detail, comparison, and the list's addon count) silently
// read the v3 layout on a v4 repo and reported zero addons forever (walk
// day 5 root cause). Keeping the probe and the readers here means the two
// services can never drift apart on what "this is a v4 repo" means.

// isV4Repo probes for the v4 engine pin (orchestrator.EnginePinPath) on the
// given branch — the single signal Sharko uses everywhere else
// (CheckEnginePin, AddonService's existing v3/v4 dispatch) to tell a v4 repo
// from a v3 one. A pin-read error (including "file not found") means "not a
// v4 repo yet", never a hard failure — the ordinary state for every v3 repo
// and for a v4 repo before its first bootstrap.
func isV4Repo(ctx context.Context, gp gitprovider.GitProvider, branch string) bool {
	pinContent, pinErr := gp.GetFileContent(ctx, orchestrator.EnginePinPath, branch)
	return pinErr == nil && len(pinContent) > 0
}

// readV4ApprovedCatalog reads catalog.yaml (config.AddonCatalogPath) and
// returns the org's approved addons. A missing file means "nothing approved
// yet" (design doc D16, "missing means empty") — not an error — matching
// every existing v4 read branch (getCatalogV4, getVersionMatrixV4).
func readV4ApprovedCatalog(ctx context.Context, gp gitprovider.GitProvider, branch string) (config.AddonCatalogSpec, error) {
	approvedData, err := gp.GetFileContent(ctx, config.AddonCatalogPath, branch)
	if err != nil {
		if isGitFileNotFound(err) {
			return config.AddonCatalogSpec{}, nil
		}
		return config.AddonCatalogSpec{}, fmt.Errorf("reading %s: %w", config.AddonCatalogPath, err)
	}
	approved, err := config.LoadAddonCatalog(approvedData)
	if err != nil {
		return config.AddonCatalogSpec{}, fmt.Errorf("parsing %s: %w", config.AddonCatalogPath, err)
	}
	return approved, nil
}

// v4CatalogEntries flattens a merged v4 catalog view (catalog.BuildCatalogView)
// into the same []models.AddonCatalogEntry shape the v3 parser's
// ParseAddonsCatalog returns, sorted by name for deterministic output. This
// is what lets ClusterService reuse config.Parser.GetEnabledAddons unchanged
// for both repo layouts — the enablement-resolution logic (version overrides,
// EnvironmentVersion, CustomVersion, HasVersionOverride) stays in ONE place
// instead of being re-implemented per layout.
func v4CatalogEntries(merged map[string]catalog.CatalogAddon) []models.AddonCatalogEntry {
	names := make([]string, 0, len(merged))
	for name := range merged {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]models.AddonCatalogEntry, 0, len(names))
	for _, name := range names {
		addon := merged[name]
		out = append(out, models.AddonCatalogEntry{
			Name:      name,
			RepoURL:   addon.RepoURL,
			Chart:     addon.Chart,
			Version:   addon.Version,
			Namespace: addon.Namespace,
		})
	}
	return out
}

// buildV4RepoConfig builds a config.RepoConfig from a v4 repo's files:
// clusters from the already-parsed managed-clusters.yaml (clusterData —
// v4's cluster list, name-only; per-cluster addon enablement lives
// elsewhere, in cluster-addons/<name>.yaml) and addons from catalog.yaml
// via readV4ApprovedCatalog + catalog.BuildCatalogView. curated is passed
// through so a caller with a Marketplace catalog wired in (AddonService)
// can supply it; ClusterService has none, so it passes nil — safe, per
// catalog.BuildCatalogView's doc, and irrelevant here since
// v4CatalogEntries only reads the deployment fields, never the curated
// knowledge fields.
func buildV4RepoConfig(ctx context.Context, gp gitprovider.GitProvider, branch string, parser *config.Parser, curated *catalog.Catalog, clusterData []byte) (*config.RepoConfig, error) {
	clusters, err := parser.ParseClusterAddons(clusterData)
	if err != nil {
		return nil, err
	}

	approved, err := readV4ApprovedCatalog(ctx, gp, branch)
	if err != nil {
		return nil, err
	}

	merged := catalog.BuildCatalogView(curated, approved)

	return &config.RepoConfig{
		Clusters: clusters,
		Addons:   v4CatalogEntries(merged),
	}, nil
}

// readV4ClusterAddons reads a single cluster's assignment file
// (config.V4ClusterAddonsPath, "cluster-addons/<name>.yaml") — the ONLY
// per-cluster addon-enablement source in a v4 repo. found is false when the
// file does not exist yet (a cluster registered but not yet assigned any
// addons); callers treat that the same as an empty Addons map, never an
// error.
func readV4ClusterAddons(ctx context.Context, gp gitprovider.GitProvider, branch, clusterName string) (spec models.ClusterAddonsSpec, found bool, err error) {
	filePath, pathErr := config.V4ClusterAddonsPath(clusterName)
	if pathErr != nil {
		return models.ClusterAddonsSpec{}, false, pathErr
	}
	data, readErr := gp.GetFileContent(ctx, filePath, branch)
	if readErr != nil {
		if isGitFileNotFound(readErr) {
			return models.ClusterAddonsSpec{}, false, nil
		}
		return models.ClusterAddonsSpec{}, false, fmt.Errorf("reading %s: %w", filePath, readErr)
	}
	spec, parseErr := models.LoadClusterAddons(data)
	if parseErr != nil {
		return models.ClusterAddonsSpec{}, false, fmt.Errorf("parsing %s: %w", filePath, parseErr)
	}
	return spec, true, nil
}

// v4ClusterLabels synthesizes the same label shape a v3
// managed-clusters.yaml entry carries (`<addon>: enabled|disabled` and, on
// an override, `<addon>-version: <version>`) from a v4
// cluster-addons/<name>.yaml spec. This is what lets every existing
// label-reading code path — config.Parser.GetEnabledAddons, and the UI's
// own `Object.values(cluster.labels).filter(v => v === 'enabled').length`
// addon-count logic in ClustersOverview.tsx — work unchanged for a v4
// cluster instead of seeing an empty label map and reporting zero addons.
func v4ClusterLabels(spec models.ClusterAddonsSpec) map[string]string {
	labels := make(map[string]string, len(spec.Addons)*2)
	for name, ca := range spec.Addons {
		if ca.Enabled {
			labels[name] = models.LabelEnabled
		} else {
			labels[name] = "disabled"
		}
		if ca.Version != "" {
			labels[name+"-version"] = ca.Version
		}
	}
	return labels
}

// listClusterAddonsSpecsConcurrency bounds the fan-out in
// listClusterAddonsSpecs (perf M2 N+1 fix) so a very large fleet doesn't
// open one goroutine (and one Git provider round trip) per cluster file
// all at once.
const listClusterAddonsSpecsConcurrency = 8

// listClusterAddonsSpecs lists cluster-addons/*.yaml and parses each into a
// ClusterAddonsSpec, keyed by cluster name. An empty (or absent —
// pre-first-cluster v4 repos have only cluster-addons/.gitkeep) directory
// returns an empty, non-nil map rather than an error.
//
// The per-file reads are independent — nothing about parsing one cluster's
// spec depends on any other — so on a fleet with many clusters this used to
// be a classic N+1: one sequential Git provider round trip per cluster
// (perf M2). GitProvider has no batch/multi-file read, so the fix is a
// bounded concurrent fan-out over the single ListDirectory result instead of
// a second network primitive: still exactly one call per file, but they
// happen in parallel (capped by listClusterAddonsSpecsConcurrency) rather
// than one after another. Output and error behavior are unchanged — the
// first error from any file still fails the whole call, with the same
// wrapped message.
//
// Shared by AddonService (GetCatalog/GetVersionMatrix's v4 branches, which
// need every cluster's addons) and ClusterService (ListClusters' v4 branch,
// same reason). A single-cluster read (GetClusterDetail, GetClusterComparison)
// uses readV4ClusterAddons instead — no reason to list-and-fan-out the whole
// directory for one file.
func listClusterAddonsSpecs(ctx context.Context, gp gitprovider.GitProvider, baseBranch string) (map[string]models.ClusterAddonsSpec, error) {
	entries, err := gp.ListDirectory(ctx, orchestrator.V4ClustersDir, baseBranch)
	if err != nil {
		if isGitFileNotFound(err) {
			return map[string]models.ClusterAddonsSpec{}, nil
		}
		return nil, err
	}

	var yamlNames []string
	for _, name := range entries {
		if strings.HasSuffix(name, ".yaml") {
			yamlNames = append(yamlNames, name)
		}
	}

	// Each goroutine writes to its own index — no lock needed, and no
	// races even though the slice itself is shared.
	specs := make([]models.ClusterAddonsSpec, len(yamlNames))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(listClusterAddonsSpecsConcurrency)
	for i, name := range yamlNames {
		g.Go(func() error {
			data, readErr := gp.GetFileContent(gctx, path.Join(orchestrator.V4ClustersDir, name), baseBranch)
			if readErr != nil {
				return fmt.Errorf("reading %s/%s: %w", orchestrator.V4ClustersDir, name, readErr)
			}
			spec, parseErr := models.LoadClusterAddons(data)
			if parseErr != nil {
				return fmt.Errorf("parsing %s/%s: %w", orchestrator.V4ClustersDir, name, parseErr)
			}
			specs[i] = spec
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	out := make(map[string]models.ClusterAddonsSpec, len(specs))
	for _, spec := range specs {
		out[spec.Cluster] = spec
	}
	return out, nil
}
