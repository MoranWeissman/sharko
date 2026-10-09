// generator_files.go — renders a GeneratedEstate into the same two on-disk
// layouts the hand-written demo fixture ships (v3 wrapped + v4 flat), so a
// generated estate is indistinguishable in shape from the real thing (S1:
// "Both v3 AND v4 fixture layouts must still be produced").
package demo

import (
	"bytes"
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/schema"
)

// genManagedClusterEntry/genManagedClustersSpec mirror
// models.ManagedClusterEntry/ManagedClustersSpec's YAML shape exactly
// (name/region/labels), scoped locally so this package can render the v3
// WRAPPED envelope (apiVersion/kind/metadata/spec) directly with
// yaml.v3 — models.SaveManagedClusters only emits the newer FLAT v4 shape
// (schema.EncodeFlat), so it cannot be reused for the v3 file.
type genManagedClusterEntry struct {
	Name                string            `yaml:"name"`
	Region              string            `yaml:"region,omitempty"`
	Labels              map[string]string `yaml:"labels,omitempty"`
	ConnectionManagedBy string            `yaml:"connectionManagedBy,omitempty"`
}

type genManagedClustersSpec struct {
	Clusters []genManagedClusterEntry `yaml:"clusters"`
}

// renderV3ManagedClusters renders estate's registered clusters as a v3
// wrapped configuration/managed-clusters.yaml document — the same shape as
// mock_git.go's hand-written clusterAddonsYAML const, addon on/off state
// encoded as labels[addonName] = "enabled" plus an optional
// labels[addonName+"-version"] override for cells whose deployed version
// differs from the addon's generated catalog version.
func renderV3ManagedClusters(estate *GeneratedEstate) ([]byte, error) {
	catalogVersion := make(map[string]string, len(estate.Addons))
	for _, a := range estate.Addons {
		catalogVersion[a.Name] = a.CatalogVersion
	}

	selfManaged := make(map[string]bool, len(estate.SelfManagedClusterNames))
	for _, name := range estate.SelfManagedClusterNames {
		selfManaged[name] = true
	}

	entries := make([]genManagedClusterEntry, 0, len(estate.Clusters))
	for _, c := range estate.Clusters {
		labels := map[string]string{
			"env":    c.Env,
			"region": c.Region,
		}
		addonNames := make([]string, 0, len(c.Addons))
		for name := range c.Addons {
			addonNames = append(addonNames, name)
		}
		sort.Strings(addonNames)
		for _, name := range addonNames {
			version := c.Addons[name]
			labels[name] = "enabled"
			if version != "" && version != catalogVersion[name] {
				labels[name+"-version"] = version
			}
		}
		connectionManagedBy := ""
		if selfManaged[c.Name] {
			connectionManagedBy = "user"
		}
		entries = append(entries, genManagedClusterEntry{
			Name:                c.Name,
			Region:              c.Region,
			Labels:              labels,
			ConnectionManagedBy: connectionManagedBy,
		})
	}

	doc := schema.Envelope[genManagedClustersSpec]{
		APIVersion: schema.APIVersion,
		Kind:       schema.KindManagedClusters,
		Metadata:   schema.Metadata{Name: models.ManagedClustersMetadataName},
		Spec:       genManagedClustersSpec{Clusters: entries},
	}
	body, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, fmt.Errorf("rendering generated managed-clusters.yaml: %w", err)
	}

	var buf bytes.Buffer
	buf.WriteString(models.ManagedClustersSchemaHeader + "\n")
	buf.Write(body)
	return buf.Bytes(), nil
}

// renderV3AddonsCatalog renders estate's addons as a v3 wrapped
// configuration/addons-catalog.yaml document via the canonical writer
// (config.MarshalAddonCatalog) — every field it needs (Name/RepoURL/
// Chart/Version/Namespace) is already on generatedAddon.
func renderV3AddonsCatalog(estate *GeneratedEstate) ([]byte, error) {
	entries := make([]models.AddonCatalogEntry, 0, len(estate.Addons))
	for _, a := range estate.Addons {
		entries = append(entries, models.AddonCatalogEntry{
			Name:      a.Name,
			RepoURL:   a.RepoURL,
			Chart:     a.Chart,
			Version:   a.CatalogVersion,
			Namespace: a.Namespace,
		})
	}
	return config.MarshalAddonCatalog("addon-catalog", entries)
}

// buildV4DemoFilesFromEstate renders estate as the v4 flat layout —
// catalog.yaml, cluster-addons/<cluster>.yaml per cluster, the flat
// managed-clusters.yaml, and a couple of plain (unenveloped) values stubs —
// mirroring buildV4DemoFiles's hand-written default-estate output but
// driven entirely by the generated estate. Coexists with the v3 files
// (renderV3ManagedClusters/renderV3AddonsCatalog) by the same design as the
// default estate (v4_fixtures.go's package doc).
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
