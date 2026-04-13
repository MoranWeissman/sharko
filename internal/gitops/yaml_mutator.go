// Package gitops provides line-level YAML mutation utilities for
// cluster-addons.yaml and addons-catalog.yaml.  The functions operate on raw
// bytes using string/line manipulation so that comments, anchors, and
// formatting are preserved exactly as they were.
package gitops

import (
	"fmt"
	"regexp"
	"strings"
)

// EnableAddonLabel sets addonName: enabled in the labels block of the given
// cluster inside a cluster-addons.yaml document.  If the label already exists
// its value is overwritten; otherwise a new line is appended to the labels
// block.
func EnableAddonLabel(data []byte, clusterName, addonName string) ([]byte, error) {
	return setAddonLabel(data, clusterName, addonName, "enabled")
}

// DisableAddonLabel sets addonName: disabled in the labels block of the given
// cluster inside a cluster-addons.yaml document.
func DisableAddonLabel(data []byte, clusterName, addonName string) ([]byte, error) {
	return setAddonLabel(data, clusterName, addonName, "disabled")
}

// setAddonLabel is the shared implementation for Enable/DisableAddonLabel.
func setAddonLabel(data []byte, clusterName, addonName, value string) ([]byte, error) {
	lines := strings.Split(string(data), "\n")

	// Locate the cluster block: find the line "  - name: <clusterName>"
	clusterIdx := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "- name: "+clusterName {
			clusterIdx = i
			break
		}
	}
	if clusterIdx == -1 {
		return nil, fmt.Errorf("cluster %q not found in cluster-addons.yaml", clusterName)
	}

	// Determine the indentation of the "- name:" line to detect cluster boundaries.
	clusterIndent := leadingSpaces(lines[clusterIdx])

	// Find the labels: line within this cluster block.
	labelsIdx := -1
	labelsIndent := 0
	for i := clusterIdx + 1; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		// If we hit the next list item at the same or lesser indent, stop.
		if strings.HasPrefix(trimmed, "- name:") && leadingSpaces(line) <= clusterIndent {
			break
		}
		if strings.HasPrefix(trimmed, "labels:") {
			labelsIdx = i
			labelsIndent = leadingSpaces(line)
			break
		}
	}
	if labelsIdx == -1 {
		return nil, fmt.Errorf("labels block not found for cluster %q", clusterName)
	}

	// Handle labels: [] (empty array) — replace with a block and insert the label.
	if strings.Contains(lines[labelsIdx], "[]") {
		indent := leadingSpaces(lines[labelsIdx])
		lines[labelsIdx] = strings.Repeat(" ", indent) + "labels:"
		newLabel := strings.Repeat(" ", indent+2) + addonName + ": " + value
		lines = insertLine(lines, labelsIdx+1, newLabel)
		return []byte(strings.Join(lines, "\n")), nil
	}

	// The label entries are indented more than the "labels:" line.
	entryIndent := labelsIndent + 2 // standard YAML indent
	// Detect actual entry indent from the first non-comment, non-blank line
	// after labels:.
	for i := labelsIdx + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		sp := leadingSpaces(lines[i])
		if sp > labelsIndent {
			entryIndent = sp
		}
		break
	}

	// Check for commented-out label and uncomment it if found.
	commentPattern := regexp.MustCompile(`^\s*#\s*` + regexp.QuoteMeta(addonName) + `:\s+\S+`)
	labelEnd := labelsIdx + 1
	for i := labelsIdx + 1; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			break
		}
		sp := leadingSpaces(line)
		if !strings.HasPrefix(trimmed, "#") && sp < entryIndent {
			break
		}
		labelEnd = i + 1
	}
	for i := labelsIdx + 1; i < labelEnd; i++ {
		if commentPattern.MatchString(lines[i]) {
			// Uncomment and set the desired value.
			indent := entryIndent
			lines[i] = strings.Repeat(" ", indent) + addonName + ": " + value
			return []byte(strings.Join(lines, "\n")), nil
		}
	}

	// Scan label entries to see if addonName already exists.
	existingIdx := -1
	lastLabelIdx := labelsIdx // last line that belongs to the labels block
	for i := labelsIdx + 1; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// blank or comment lines inside the block — keep scanning
		if trimmed == "" {
			break // blank line ends block
		}
		if strings.HasPrefix(trimmed, "#") {
			lastLabelIdx = i
			continue
		}

		sp := leadingSpaces(line)
		if sp < entryIndent {
			break // left the labels block
		}

		lastLabelIdx = i

		// Check if this line is our addon label.
		key := strings.SplitN(trimmed, ":", 2)[0]
		if key == addonName {
			existingIdx = i
		}
	}

	if existingIdx != -1 {
		// Replace the value on the existing line.
		line := lines[existingIdx]
		colonPos := strings.Index(line, addonName+":")
		prefix := line[:colonPos+len(addonName)+1]
		// Preserve any trailing inline comment (though unusual for these files).
		lines[existingIdx] = prefix + " " + value
	} else {
		// Insert a new label line after the last label entry.
		newLine := strings.Repeat(" ", entryIndent) + addonName + ": " + value
		// Insert after lastLabelIdx.
		lines = insertLine(lines, lastLabelIdx+1, newLine)
	}

	return []byte(strings.Join(lines, "\n")), nil
}

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
// Cluster entry helpers
// ---------------------------------------------------------------------------

// ClusterEntryInput holds the fields for a new cluster-addons.yaml entry.
// Region and SecretPath are optional (zero value means not set).
// Labels should use "true"/"false" format to match cluster-addons.yaml convention.
type ClusterEntryInput struct {
	Name       string
	Region     string            // optional
	SecretPath string            // optional
	Labels     map[string]string // addon labels, e.g. {"cert-manager": "true"}
}

// AddClusterEntry appends a new entry to the clusters array in a
// cluster-addons.yaml document.  It returns an error if an entry with the
// same name already exists.  If the document has no clusters: key, one is
// created.
//
// If data is empty or contains only whitespace, a minimal document is
// bootstrapped with a clusters: header before inserting.
func AddClusterEntry(data []byte, entry ClusterEntryInput) ([]byte, error) {
	// Bootstrap empty document.
	if len(strings.TrimSpace(string(data))) == 0 {
		data = []byte("clusters:\n")
	}

	lines := strings.Split(string(data), "\n")

	// Locate clusters: key.
	clustersIdx := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "clusters:" {
			clustersIdx = i
			break
		}
	}
	if clustersIdx == -1 {
		// Append clusters: section at the end.
		lines = append(lines, "clusters:")
		clustersIdx = len(lines) - 1
	}

	// Check for duplicate.
	for _, line := range lines {
		if strings.TrimSpace(line) == "- name: "+entry.Name {
			// Already present — skip silently (adoption path).
			return data, nil
		}
	}

	// Find the last line that belongs to the clusters array.
	// Lines with indent >= 2, or blank lines between entries, belong to the block.
	// A non-blank line with indent < 2 is a new top-level key — stop there.
	lastContentIdx := clustersIdx
	for i := clustersIdx + 1; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			continue
		}
		if leadingSpaces(line) < 2 {
			break
		}
		lastContentIdx = i
	}

	// Build the new entry block (2-space list indent, 4-space field indent).
	newLines := []string{
		"  - name: " + entry.Name,
	}
	if entry.Region != "" {
		newLines = append(newLines, "    region: "+entry.Region)
	}
	if entry.SecretPath != "" {
		newLines = append(newLines, "    secretPath: "+entry.SecretPath)
	}
	if len(entry.Labels) > 0 {
		newLines = append(newLines, "    labels:")
		// Sort label keys for deterministic output.
		keys := make([]string, 0, len(entry.Labels))
		for k := range entry.Labels {
			keys = append(keys, k)
		}
		for i := 0; i < len(keys)-1; i++ {
			for j := i + 1; j < len(keys); j++ {
				if keys[i] > keys[j] {
					keys[i], keys[j] = keys[j], keys[i]
				}
			}
		}
		for _, k := range keys {
			newLines = append(newLines, "      "+k+": "+entry.Labels[k])
		}
	} else {
		newLines = append(newLines, "    labels: {}")
	}

	// Insert a blank separator then the new block after lastContentIdx.
	insertAt := lastContentIdx + 1
	// Insert in reverse order so each insertLine call puts the line at the
	// right position.
	for j := len(newLines) - 1; j >= 0; j-- {
		lines = insertLine(lines, insertAt, newLines[j])
	}
	// Add a blank line between the previous last entry and the new one,
	// but only when there was already content in the array (not just the header).
	if lastContentIdx > clustersIdx {
		lines = insertLine(lines, insertAt, "")
	}

	return []byte(strings.Join(lines, "\n")), nil
}

// UpdateClusterSecretPath updates (or adds/removes) the secretPath field for a
// cluster entry in managed-clusters.yaml. If secretPath is empty, the field is
// removed. Returns an error if the cluster is not found.
func UpdateClusterSecretPath(data []byte, clusterName, secretPath string) ([]byte, error) {
	lines := strings.Split(string(data), "\n")

	// Find the "  - name: <clusterName>" line.
	entryIdx := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "- name: "+clusterName {
			entryIdx = i
			break
		}
	}
	if entryIdx == -1 {
		return nil, fmt.Errorf("cluster %q not found in managed-clusters.yaml", clusterName)
	}

	// Find the extent of this entry (ends at next list item or non-indented line).
	endIdx := entryIdx + 1
	for endIdx < len(lines) {
		line := lines[endIdx]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			endIdx++
			continue
		}
		if leadingSpaces(line) < 4 && strings.HasPrefix(trimmed, "- ") {
			break
		}
		if leadingSpaces(line) < 2 && trimmed != "" {
			break
		}
		endIdx++
	}

	// Look for existing secretPath line within this entry.
	secretPathIdx := -1
	for i := entryIdx + 1; i < endIdx; i++ {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "secretPath:") {
			secretPathIdx = i
			break
		}
	}

	if secretPath == "" {
		// Remove the secretPath line if it exists.
		if secretPathIdx != -1 {
			lines = append(lines[:secretPathIdx], lines[secretPathIdx+1:]...)
		}
	} else if secretPathIdx != -1 {
		// Update existing secretPath line.
		lines[secretPathIdx] = "    secretPath: " + secretPath
	} else {
		// Insert secretPath after the name line (or after region if present).
		insertAt := entryIdx + 1
		for i := entryIdx + 1; i < endIdx; i++ {
			trimmed := strings.TrimSpace(lines[i])
			if strings.HasPrefix(trimmed, "region:") {
				insertAt = i + 1
				break
			}
		}
		lines = insertLine(lines, insertAt, "    secretPath: "+secretPath)
	}

	return []byte(strings.Join(lines, "\n")), nil
}

// RemoveClusterEntry removes the named cluster entry from the clusters array
// in a managed-clusters.yaml document. Returns an error if the cluster is not found.
func RemoveClusterEntry(data []byte, name string) ([]byte, error) {
	lines := strings.Split(string(data), "\n")

	// Find the "  - name: <name>" line.
	entryIdx := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "- name: "+name {
			entryIdx = i
			break
		}
	}
	if entryIdx == -1 {
		return nil, fmt.Errorf("cluster %q not found in managed-clusters.yaml", name)
	}

	// Determine the entry's extent: from entryIdx to the line before the next
	// top-level list item (line starting with "  -") or end of clusters block.
	endIdx := entryIdx + 1
	for endIdx < len(lines) {
		line := lines[endIdx]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			endIdx++
			continue
		}
		// New list item at indent 2 or a new top-level key at indent 0.
		if leadingSpaces(line) < 4 && strings.HasPrefix(trimmed, "- ") {
			break
		}
		if leadingSpaces(line) < 2 && trimmed != "" {
			break
		}
		endIdx++
	}

	// Consume trailing blank lines.
	for endIdx < len(lines) && strings.TrimSpace(lines[endIdx]) == "" {
		endIdx++
	}

	// Also consume leading blank lines above the entry (separator between entries).
	startIdx := entryIdx
	for startIdx > 0 && strings.TrimSpace(lines[startIdx-1]) == "" {
		startIdx--
	}

	// Remove lines [startIdx, endIdx).
	result := make([]string, 0, len(lines)-(endIdx-startIdx))
	result = append(result, lines[:startIdx]...)
	result = append(result, lines[endIdx:]...)

	return []byte(strings.Join(result, "\n")), nil
}

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
