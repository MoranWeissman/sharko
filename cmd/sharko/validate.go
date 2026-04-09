package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func init() {
	rootCmd.AddCommand(validateCmd)
}

var validateCmd = &cobra.Command{
	Use:   "validate [path]",
	Short: "Validate Sharko configuration files",
	Long: `Validates addons-catalog.yaml and managed-clusters.yaml schema.

If a path is supplied the files are read from disk.  Each entry is checked for
required fields and all errors are printed before exiting with a non-zero status
if any validation failed.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		repoPath := "."
		if len(args) == 1 {
			repoPath = args[0]
		}

		// Resolve the two canonical file locations.
		// Try managed-clusters.yaml first; fall back to cluster-addons.yaml for
		// repositories that have not yet been migrated.
		catalogPath := filepath.Join(repoPath, "configuration", "addons-catalog.yaml")
		managedClustersPath := filepath.Join(repoPath, "configuration", "managed-clusters.yaml")
		if _, statErr := os.Stat(managedClustersPath); os.IsNotExist(statErr) {
			legacyPath := filepath.Join(repoPath, "configuration", "cluster-addons.yaml")
			if _, legacyErr := os.Stat(legacyPath); legacyErr == nil {
				managedClustersPath = legacyPath
			}
		}

		allOK := true

		// --- addons-catalog.yaml ---
		catalogData, err := os.ReadFile(catalogPath)
		if err != nil {
			fmt.Printf("  [SKIP] %s: %v\n", catalogPath, err)
		} else {
			errs := validateCatalog(catalogData)
			if len(errs) == 0 {
				fmt.Printf("  [OK]  %s\n", catalogPath)
			} else {
				fmt.Printf("  [FAIL] %s\n", catalogPath)
				for _, e := range errs {
					fmt.Printf("         - %s\n", e)
				}
				allOK = false
			}
		}

		// --- managed-clusters.yaml ---
		clusterData, err := os.ReadFile(managedClustersPath)
		if err != nil {
			fmt.Printf("  [SKIP] %s: %v\n", managedClustersPath, err)
		} else {
			errs := validateClusterAddons(clusterData)
			if len(errs) == 0 {
				fmt.Printf("  [OK]  %s\n", managedClustersPath)
			} else {
				fmt.Printf("  [FAIL] %s\n", managedClustersPath)
				for _, e := range errs {
					fmt.Printf("         - %s\n", e)
				}
				allOK = false
			}
		}

		if !allOK {
			return fmt.Errorf("validation failed")
		}
		return nil
	},
}

// addonsCatalogFile mirrors the structure used by the config parser.
type addonsCatalogFile struct {
	ApplicationSets []struct {
		Name    string `yaml:"name"`
		RepoURL string `yaml:"repoURL"`
		Chart   string `yaml:"chart"`
		Version string `yaml:"version"`
	} `yaml:"applicationsets"`
}

// clusterAddonsFile mirrors the structure used by the config parser.
type clusterAddonsFile struct {
	Clusters []struct {
		Name string `yaml:"name"`
	} `yaml:"clusters"`
}

// validateCatalog parses addons-catalog.yaml and returns a list of validation
// error strings.  An empty slice means the file is valid.
func validateCatalog(data []byte) []string {
	var file addonsCatalogFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return []string{"YAML parse error: " + err.Error()}
	}

	var errs []string
	if len(file.ApplicationSets) == 0 {
		errs = append(errs, "no applicationsets entries found")
		return errs
	}

	for i, entry := range file.ApplicationSets {
		label := entry.Name
		if label == "" {
			label = fmt.Sprintf("entry[%d]", i)
		}
		var missing []string
		if strings.TrimSpace(entry.Name) == "" {
			missing = append(missing, "name")
		}
		if strings.TrimSpace(entry.RepoURL) == "" {
			missing = append(missing, "repoURL")
		}
		if strings.TrimSpace(entry.Chart) == "" {
			missing = append(missing, "chart")
		}
		if strings.TrimSpace(entry.Version) == "" {
			missing = append(missing, "version")
		}
		if len(missing) > 0 {
			errs = append(errs, fmt.Sprintf("%s: missing required fields: %s", label, strings.Join(missing, ", ")))
		}
	}
	return errs
}

// validateClusterAddons parses managed-clusters.yaml and returns a list of
// validation error strings.  An empty slice means the file is valid.
func validateClusterAddons(data []byte) []string {
	var file clusterAddonsFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return []string{"YAML parse error: " + err.Error()}
	}

	var errs []string
	if len(file.Clusters) == 0 {
		errs = append(errs, "no clusters entries found")
		return errs
	}

	for i, entry := range file.Clusters {
		if strings.TrimSpace(entry.Name) == "" {
			errs = append(errs, fmt.Sprintf("clusters[%d]: missing required field: name", i))
		}
	}
	return errs
}
