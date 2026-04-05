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
//	_sharko:
//	  enabledAddonNamespaces: "monitoring,logging"
//	  enabledAddons:
//	    - name: monitoring
//	      namespace: monitoring
func generateClusterValues(clusterName string, region string, addons map[string]bool, catalog []models.AddonCatalogEntry) []byte {
	var b strings.Builder

	b.WriteString("# Cluster values for " + clusterName + "\n")
	b.WriteString("clusterGlobalValues:\n")
	if region != "" {
		b.WriteString(fmt.Sprintf("  region: %s\n", region))
	}

	var names []string
	if len(addons) > 0 {
		b.WriteString("\n")

		// Sort addon names for deterministic output.
		names = make([]string, 0, len(addons))
		for name := range addons {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			b.WriteString(fmt.Sprintf("%s:\n  enabled: %t\n", name, addons[name]))
		}
	}

	// Compute _sharko block with enabled addon namespaces
	enabledAddons := []struct{ name, ns string }{}
	for _, name := range names {
		if addons[name] {
			ns := name // default namespace = addon name
			for _, entry := range catalog {
				if entry.AppName == name && entry.Namespace != "" {
					ns = entry.Namespace
					break
				}
			}
			enabledAddons = append(enabledAddons, struct{ name, ns string }{name, ns})
		}
	}

	if len(enabledAddons) > 0 {
		b.WriteString("\n# Auto-computed by Sharko — do not edit manually\n")
		b.WriteString("_sharko:\n")

		nsNames := make([]string, 0, len(enabledAddons))
		for _, a := range enabledAddons {
			nsNames = append(nsNames, a.ns)
		}
		b.WriteString(fmt.Sprintf("  enabledAddonNamespaces: \"%s\"\n", strings.Join(nsNames, ",")))

		b.WriteString("  enabledAddons:\n")
		for _, a := range enabledAddons {
			b.WriteString(fmt.Sprintf("    - name: %s\n      namespace: %s\n", a.name, a.ns))
		}
	}

	return []byte(b.String())
}
