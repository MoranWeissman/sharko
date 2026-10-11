// generator_files.go — renders a GeneratedEstate into the same on-disk
// layout the hand-written demo fixture ships: the v4 layout only, the one
// a real v4 Sharko repo has. The demo repo never carries the old v3 files
// next to it — Sharko refuses every write on a repo that has both
// (orchestrator.ErrMixedRepoLayout).
package demo

import (
	"fmt"
	"sort"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// buildV4DemoFilesFromEstate renders estate as the v4 flat layout —
// catalog.yaml, cluster-addons/<cluster>.yaml per cluster, the flat
// managed-clusters.yaml, and a couple of plain (unenveloped) values stubs —
// mirroring buildV4DemoFiles's hand-written default-estate output but
// driven entirely by the generated estate.
func buildV4DemoFilesFromEstate(estate *GeneratedEstate) (map[string][]byte, error) {
	files := make(map[string][]byte)

	catalogEntries := make(map[string]config.AddonCatalogEntry, len(estate.Addons))
	for _, a := range estate.Addons {
		catalogEntries[a.Name] = config.AddonCatalogEntry{
			RepoURL:   a.RepoURL,
			Chart:     a.Chart,
			Version:   a.CatalogVersion,
			Namespace: a.Namespace,
		}
	}
	catalogBytes, err := config.SaveAddonCatalog(config.AddonCatalogSpec{Addons: catalogEntries})
	if err != nil {
		return nil, fmt.Errorf("rendering generated %s: %w", config.AddonCatalogPath, err)
	}
	files[config.AddonCatalogPath] = catalogBytes

	catalogVersion := make(map[string]string, len(estate.Addons))
	for _, a := range estate.Addons {
		catalogVersion[a.Name] = a.CatalogVersion
	}

	selfManagedV4 := make(map[string]bool, len(estate.SelfManagedClusterNames))
	for _, name := range estate.SelfManagedClusterNames {
		selfManagedV4[name] = true
	}

	managedEntries := make([]models.ManagedClusterEntry, 0, len(estate.Clusters))
	for _, c := range estate.Clusters {
		connectionManagedBy := ""
		if selfManagedV4[c.Name] {
			connectionManagedBy = "user"
		}
		managedEntries = append(managedEntries, models.ManagedClusterEntry{
			Name:                c.Name,
			Region:              c.Region,
			SecretPath:          "k8s-" + c.Name,
			CredsSource:         "secret-kubeconfig",
			ConnectionManagedBy: connectionManagedBy,
		})

		addonNames := make([]string, 0, len(c.Addons))
		for name := range c.Addons {
			addonNames = append(addonNames, name)
		}
		sort.Strings(addonNames)

		addons := make(map[string]models.ClusterAddonsAddon, len(addonNames))
		for _, name := range addonNames {
			version := c.Addons[name]
			// A version equal to the catalog default means "follow the
			// catalog" (empty Version), matching the default estate's
			// v4 fixture convention (v4_fixtures.go's metrics-server case).
			if version == catalogVersion[name] {
				version = ""
			}
			addons[name] = models.ClusterAddonsAddon{Enabled: true, Version: version}
		}
		body, err := models.SaveClusterAddons(models.ClusterAddonsSpec{Cluster: c.Name, Addons: addons})
		if err != nil {
			return nil, fmt.Errorf("rendering generated cluster-addons/%s.yaml: %w", c.Name, err)
		}
		files[orchestrator.V4ClustersDir+"/"+c.Name+".yaml"] = body
	}

	connBytes, err := models.SaveManagedClusters(models.ManagedClustersSpec{Clusters: managedEntries})
	if err != nil {
		return nil, fmt.Errorf("rendering generated %s: %w", orchestrator.V4ManagedClustersPath, err)
	}
	files[orchestrator.V4ManagedClustersPath] = connBytes

	// A couple of plain (unenveloped) values stubs, same as the default
	// estate's fixture, so the v4 values/ directory isn't empty.
	if len(estate.Addons) > 0 {
		files[orchestrator.V4GlobalValuesDir+"/"+estate.Addons[0].Name+".yaml"] = []byte("replicaCount: 1\n")
	}
	if len(estate.Clusters) > 0 && len(estate.Addons) > 0 {
		files[orchestrator.V4ClusterValuesDir+"/"+estate.Clusters[0].Name+"/"+estate.Addons[0].Name+".yaml"] = []byte("replicaCount: 1\n")
	}

	return files, nil
}
