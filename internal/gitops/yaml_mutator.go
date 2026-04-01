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

	// Find the applicationset entry: "  - appName: <addonName>"
	appIdx := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "- appName: "+addonName {
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
