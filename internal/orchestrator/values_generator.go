package orchestrator

import (
	"fmt"
	"sort"
	"strings"

	"github.com/MoranWeissman/sharko/internal/models"
)

// generateClusterValues creates the YAML content for a cluster values file.
// The output follows the convention expected by the ArgoCD addon applications:
//
//	clusterGlobalValues:
//	  region: eu-west-1
//
//	monitoring:
//	  enabled: true
//	logging:
//	  enabled: false
//
// catalog is accepted for API stability with existing callers but is no
// longer used: the per-addon namespace used to feed a write-only `_sharko`
// block that nothing read back (enablement truth is the cluster labels, and
// the addons ApplicationSet takes the namespace from the catalog directly).
func generateClusterValues(clusterName string, region string, addons map[string]bool, catalog []models.AddonCatalogEntry) []byte {
	var b strings.Builder

	b.WriteString("# Cluster values for " + clusterName + "\n")
	b.WriteString("clusterGlobalValues:\n")
	b.WriteString("  # Optional. Define a value once here and reuse it in the addon sections below\n")
	b.WriteString("  # with a YAML anchor, so you don't repeat yourself. Delete this block if you\n")
	b.WriteString("  # don't need it — nothing depends on it.\n")
	b.WriteString("  # Example — define an anchor here:\n")
	b.WriteString("  #   region: &region eu-west-1\n")
	b.WriteString("  # then reference it from an addon section further down:\n")
	b.WriteString("  #   podinfo:\n")
	b.WriteString("  #     location: *region\n")
	if region != "" {
		b.WriteString(fmt.Sprintf("  region: %s\n", region))
	}

	if len(addons) > 0 {
		b.WriteString("\n")

		// Sort addon names for deterministic output.
		names := make([]string, 0, len(addons))
		for name := range addons {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			b.WriteString(fmt.Sprintf("%s:\n  enabled: %t\n", name, addons[name]))
		}
	}

	return []byte(b.String())
}
