package orchestrator

import (
	"fmt"
	"sort"
	"strings"
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
func generateClusterValues(clusterName string, region string, addons map[string]bool) []byte {
	var b strings.Builder

	b.WriteString("# Cluster values for " + clusterName + "\n")
	b.WriteString("clusterGlobalValues:\n")
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
