// Package gitops provides line-level YAML mutation utilities for
// addons-catalog.yaml.
//
// V125-1-8.3 / closes #257: the cluster-side mutators that used to live
// here (EnableAddonLabel, DisableAddonLabel, AddClusterEntry,
// RemoveClusterEntry, UpdateClusterSecretPath, plus the setAddonLabel
// shared helper) were replaced by V125-1-9 envelope-aware versions in
// yaml_mutator_cluster.go. They route through models.LoadManagedClusters
// + SaveManagedClusters instead of the byte-level line scanner that
// silently broke against the apiVersion/kind/spec envelope (the indent
// assumption — cluster entries at indent 2 — became wrong once the
// envelope wrapped them at indent 4 under `spec.clusters:`).
//
// The catalog-side mutators below remain line-level because no V125-1-9
// envelope writer exists for addons-catalog.yaml yet — that file still
// uses the legacy bare `applicationsets:` shape. The catalog envelope
// is V125-1-9.x follow-up scope.
package gitops

import (
	"fmt"
	"strings"
)

// UpdateCatalogVersion updates the version: field for the given addon inside
// an addons-catalog.yaml document.
func UpdateCatalogVersion(data []byte, addonName, newVersion string) ([]byte, error) {
	lines := strings.Split(string(data), "\n")

	// Find the applicationset entry: "  - name: <addonName>"
	appIdx := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "- name: "+addonName {
			appIdx = i
			break
		}
	}
	if appIdx == -1 {
		return nil, fmt.Errorf("addon %q not found in addons-catalog.yaml", addonName)
	}

	appIndent := leadingSpaces(lines[appIdx])

	// Find the version: line within this block.
	for i := appIdx + 1; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		if trimmed == "" {
			continue
		}
		// Next list item at same indent — we've left the block.
		if strings.HasPrefix(trimmed, "- ") && leadingSpaces(line) <= appIndent {
			break
		}

		if strings.HasPrefix(trimmed, "version:") {
			colonPos := strings.Index(line, "version:")
			prefix := line[:colonPos+len("version:")]
			lines[i] = prefix + " " + newVersion
			return []byte(strings.Join(lines, "\n")), nil
		}
	}

	return nil, fmt.Errorf("version field not found for addon %q", addonName)
}

// ---------------------------------------------------------------------------
// Catalog mutation helpers
// ---------------------------------------------------------------------------

// CatalogEntryInput holds the fields for a new addons-catalog.yaml entry.
// Namespace, SyncWave, Path and DependsOn are optional (zero value means not set).
type CatalogEntryInput struct {
	Name      string
	RepoURL   string
	Chart     string
	Version   string
	Namespace string   // optional
	SyncWave  int      // optional, 0 = not set
	Path      string   // optional, for git-sourced addons
	DependsOn []string // optional, addon names this entry depends on
}

// AddCatalogEntry appends a new entry to the applicationsets array in an
// addons-catalog.yaml document.  It returns an error if an entry with the
// same name already exists or if the document has no applicationsets key.
func AddCatalogEntry(data []byte, entry CatalogEntryInput) ([]byte, error) {
	lines := strings.Split(string(data), "\n")

	// Verify applicationsets: exists.
	appSetsIdx := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "applicationsets:" {
			appSetsIdx = i
			break
		}
	}
	if appSetsIdx == -1 {
		return nil, fmt.Errorf("applicationsets: key not found in catalog")
	}

	// Check for duplicate.
	for _, line := range lines {
		if strings.TrimSpace(line) == "- name: "+entry.Name {
			return nil, fmt.Errorf("addon %q already exists in catalog", entry.Name)
		}
	}

	// Find the last line that belongs to the applicationsets array.
	// We walk forward from appSetsIdx+1.  Any line that is blank or has
	// indent >= 2 (the array item indent) belongs to the block; a line with
	// indent < 2 and non-blank would be a new top-level key.
	lastContentIdx := appSetsIdx
	for i := appSetsIdx + 1; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			// blank lines inside the array are fine — keep going but don't
			// update lastContentIdx past the last real content.
			continue
		}
		if leadingSpaces(line) < 2 {
			// Hit a top-level key — stop.
			break
		}
		lastContentIdx = i
	}

	// Build the new entry block (2-space list indent, 4-space field indent).
	newLines := []string{
		"  - name: " + entry.Name,
		"    repoURL: " + entry.RepoURL,
		"    chart: " + entry.Chart,
		"    version: " + entry.Version,
	}
	if entry.Namespace != "" {
		newLines = append(newLines, "    namespace: "+entry.Namespace)
	}
	if entry.SyncWave != 0 {
		newLines = append(newLines, fmt.Sprintf("    syncWave: %d", entry.SyncWave))
	}
	if entry.Path != "" {
		newLines = append(newLines, "    path: "+entry.Path)
	}
	if len(entry.DependsOn) > 0 {
		// Render as an inline YAML list: dependsOn: [a, b, c]
		deps := strings.Join(entry.DependsOn, ", ")
		newLines = append(newLines, "    dependsOn: ["+deps+"]")
	}

	// Insert a blank separator then the new block after lastContentIdx.
	insertAt := lastContentIdx + 1
	// Insert in reverse order so each insertLine call puts the line at the
	// right position.
	for j := len(newLines) - 1; j >= 0; j-- {
		lines = insertLine(lines, insertAt, newLines[j])
	}
	// Add a blank line between the previous last entry and the new one.
	lines = insertLine(lines, insertAt, "")

	return []byte(strings.Join(lines, "\n")), nil
}

// ---------------------------------------------------------------------------
// Cluster entry helpers — moved to yaml_mutator_cluster.go in V125-1-8.3
//
// AddClusterEntry, RemoveClusterEntry, UpdateClusterSecretPath,
// EnableAddonLabel, DisableAddonLabel and their shared helpers live in
// yaml_mutator_cluster.go now and route through models.LoadManagedClusters
// + SaveManagedClusters instead of byte-level line manipulation.
// See yaml_mutator_cluster.go doc comment for the full rationale.
// ---------------------------------------------------------------------------

// RemoveCatalogEntry removes the entire block for addonName from the
// applicationsets array, including any comment lines that appear directly
// above the entry (between the previous entry's last line and this entry's
// "- name:" line).
func RemoveCatalogEntry(data []byte, addonName string) ([]byte, error) {
	lines := strings.Split(string(data), "\n")

	// Find the "  - name: <addonName>" line.
	appIdx := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "- name: "+addonName {
			appIdx = i
			break
		}
	}
	if appIdx == -1 {
		return nil, fmt.Errorf("addon %q not found in catalog", addonName)
	}

	appIndent := leadingSpaces(lines[appIdx])

	// Walk backward from appIdx to collect any comment lines that belong to
	// this entry (blank lines stop the search; non-blank non-comment lines stop it).
	startIdx := appIdx
	for i := appIdx - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			break
		}
		if strings.HasPrefix(trimmed, "#") {
			startIdx = i
		} else {
			break
		}
	}

	// Walk forward to find the last line of this entry block.
	endIdx := appIdx
	for i := appIdx + 1; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			break
		}
		if strings.HasPrefix(trimmed, "- ") && leadingSpaces(line) <= appIndent {
			break
		}
		endIdx = i
	}

	// Also consume one blank line after the block if present.
	trailingBlank := 0
	if endIdx+1 < len(lines) && strings.TrimSpace(lines[endIdx+1]) == "" {
		trailingBlank = 1
	}

	// Remove lines[startIdx .. endIdx+trailingBlank] inclusive.
	result := make([]string, 0, len(lines)-(endIdx-startIdx+1+trailingBlank))
	result = append(result, lines[:startIdx]...)
	result = append(result, lines[endIdx+1+trailingBlank:]...)

	return []byte(strings.Join(result, "\n")), nil
}

// UpdateCatalogEntry finds the entry for addonName in addons-catalog.yaml and
// applies the given field updates.  If a field already exists in the entry its
// value is replaced; if it doesn't exist it is appended after the last field.
// Updating "name" is not allowed and returns an error.
func UpdateCatalogEntry(data []byte, addonName string, updates map[string]string) ([]byte, error) {
	if _, ok := updates["name"]; ok {
		return nil, fmt.Errorf("updating name is not allowed")
	}

	lines := strings.Split(string(data), "\n")

	// Find the entry.
	appIdx := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "- name: "+addonName {
			appIdx = i
			break
		}
	}
	if appIdx == -1 {
		return nil, fmt.Errorf("addon %q not found in catalog", addonName)
	}

	appIndent := leadingSpaces(lines[appIdx])

	// Collect the indices of lines that belong to this entry block.
	// Also track which update keys we've already applied.
	applied := make(map[string]bool)
	lastFieldIdx := appIdx // last line of this entry

	for i := appIdx + 1; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		if trimmed == "" {
			break
		}
		if strings.HasPrefix(trimmed, "- ") && leadingSpaces(line) <= appIndent {
			break
		}

		lastFieldIdx = i

		// Check if this line holds one of the fields we want to update.
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			if newVal, ok := updates[key]; ok {
				colonPos := strings.Index(line, key+":")
				prefix := line[:colonPos+len(key)+1]
				lines[i] = prefix + " " + newVal
				applied[key] = true
			}
		}
	}

	// Append any fields that weren't already present.
	// Use a stable order: alphabetical.
	fieldIndent := strings.Repeat(" ", appIndent+2)
	keys := make([]string, 0, len(updates))
	for k := range updates {
		keys = append(keys, k)
	}
	// Sort for determinism.
	for i := 0; i < len(keys)-1; i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[i] > keys[j] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	insertAt := lastFieldIdx + 1
	for _, k := range keys {
		if !applied[k] {
			lines = insertLine(lines, insertAt, fieldIndent+k+": "+updates[k])
			insertAt++
		}
	}

	return []byte(strings.Join(lines, "\n")), nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func leadingSpaces(s string) int {
	return len(s) - len(strings.TrimLeft(s, " "))
}

func insertLine(lines []string, at int, line string) []string {
	if at >= len(lines) {
		return append(lines, line)
	}
	lines = append(lines, "")
	copy(lines[at+1:], lines[at:])
	lines[at] = line
	return lines
}
