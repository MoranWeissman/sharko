package orchestrator

import "gopkg.in/yaml.v3"

// ClusterValuesWrapped reports whether a per-cluster values file has all its
// settings inside a block named after the add-on, in a way Helm ignores.
//
// "One top block named like the add-on" is not enough to say so. Some charts
// really have a top block with their own name: reloader, datadog, loki,
// tempo, falco, opencost. For those a file that starts with "reloader:" is
// correct and works, and telling the user to move the settings out of it
// would break it. So the add-on's global values file (values/global/
// <addon>.yaml) is asked what the chart's top settings are:
//
//   - The chart has a top setting with that name: the file is fine.
//   - Sharko wrote the global file, so it holds every chart default, and
//     none of them has that name: the file is wrapped.
//   - A person wrote the global file, so it may hold only a few settings:
//     the file is called wrapped only when a setting inside the block is one
//     of the top settings of the global file, which shows the block is one
//     level too deep.
//   - There is no global file, or it holds no settings: Sharko cannot tell,
//     and says nothing. A missed warning is better than a false one that
//     tells the user to break a working file.
func ClusterValuesWrapped(clusterFile, globalFile []byte, addon string) bool {
	if !DetectLegacyWrap(clusterFile, addon, "") {
		return false
	}
	block := yamlDocRoot(clusterFile)
	if block == nil || len(block.Content) != 2 {
		return false
	}
	blockName := block.Content[0].Value

	chartTop := topLevelKeys(yamlDocRoot(globalFile))
	if len(chartTop) == 0 {
		return false
	}
	if chartTop[blockName] {
		return false
	}
	if ParseSmartValuesHeader(globalFile).Managed {
		return true
	}
	for inner := range topLevelKeys(block.Content[1]) {
		if chartTop[inner] {
			return true
		}
	}
	return false
}

// topLevelKeys returns the keys of a YAML mapping. A nil or non-mapping node
// has none.
func topLevelKeys(n *yaml.Node) map[string]bool {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	keys := make(map[string]bool, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		keys[n.Content[i].Value] = true
	}
	return keys
}
