package orchestrator

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// BuildFileDiff computes a unified diff string for the given file change.
// Exported wrapper for use by api package (unwrap-globals endpoint).
func (o *Orchestrator) BuildFileDiff(path string, oldBytes []byte, newContent []byte, action string) string {
	return o.buildFileDiff(path, oldBytes, newContent, action)
}

// buildFileDiff computes a unified diff string for the given file change.
// For values files (per-cluster or global), it redacts secret values in BOTH
// old and new content before diffing, so unchanged secrets appear as unchanged
// <redacted> values and the diff highlights only structural/key changes without
// ever exposing a secret value.
//
// For create actions: oldBytes is empty, all lines are additions.
// For delete actions: newContent is empty, all lines are deletions.
// For update actions: produces a mixed diff of changes.
//
// For non-values files (catalog, managed-clusters, etc.), content is diffed
// as-is since those files are safe by construction (flags/versions/URLs, no secrets).
func (o *Orchestrator) buildFileDiff(path string, oldBytes []byte, newContent []byte, action string) string {
	// Redact values files in both old and new before diffing, so secret values
	// never appear in the diff but structural changes remain visible.
	oldRedacted := o.redactValuesContent(path, oldBytes)
	newRedacted := o.redactValuesContent(path, newContent)

	// Split into lines for line-based diffing
	oldLines := splitDiffLines(string(oldRedacted))
	newLines := splitDiffLines(string(newRedacted))

	// Build simple line-by-line diff
	var buf bytes.Buffer
	buf.WriteString(fmt.Sprintf("--- %s\n", path))
	buf.WriteString(fmt.Sprintf("+++ %s\n", path))

	// Simple line-based diff: show all deletions, then all additions
	// This is not optimal but sufficient for preview purposes
	for _, line := range oldLines {
		if !containsLine(newLines, line) {
			buf.WriteString("-")
			buf.WriteString(line)
			buf.WriteString("\n")
		}
	}
	for _, line := range newLines {
		if !containsLine(oldLines, line) {
			buf.WriteString("+")
			buf.WriteString(line)
			buf.WriteString("\n")
		}
	}

	return buf.String()
}

func splitDiffLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	// Remove trailing empty line from split
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func containsLine(lines []string, target string) bool {
	for _, line := range lines {
		if line == target {
			return true
		}
	}
	return false
}

// redactValuesContent redacts secret values from per-cluster or global values
// files by replacing every scalar leaf value with <redacted> while preserving
// YAML structure, keys, and list shapes. This allows meaningful diffs that show
// structural changes without exposing secret values.
//
// For non-values paths (catalog, managed-clusters, etc.), returns content
// unchanged since those files are safe by construction.
//
// If YAML parsing fails, returns a single-line redacted placeholder to ensure
// no raw bytes from an unparseable file leak into a preview.
func (o *Orchestrator) redactValuesContent(path string, content []byte) []byte {
	if len(content) == 0 {
		return content
	}

	// Determine if this is a values file that requires redaction.
	// Values files live under ClusterValues or GlobalValues directories.
	isValuesFile := false
	if o.paths.ClusterValues != "" && isUnderDirectory(path, o.paths.ClusterValues) {
		isValuesFile = true
	}
	if o.paths.GlobalValues != "" && isUnderDirectory(path, o.paths.GlobalValues) {
		isValuesFile = true
	}

	if !isValuesFile {
		// Safe by construction: catalog, managed-clusters, etc. contain only
		// flags/versions/URLs, no secrets.
		return content
	}

	// Parse and redact the YAML
	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		// Unparseable values file: return a safe placeholder so no raw bytes leak
		return []byte("<redacted: unparseable values file>\n")
	}

	// Redact all scalar leaf values recursively
	redactYAMLNode(&root)

	// Marshal back to YAML
	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(&root); err != nil {
		return []byte("<redacted: re-marshal failed>\n")
	}

	return buf.Bytes()
}

// isUnderDirectory checks if the given path is under the specified directory.
func isUnderDirectory(path, dir string) bool {
	relPath, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	// If the relative path starts with "..", the file is outside the directory
	return !strings.HasPrefix(relPath, "..")
}

// redactYAMLNode recursively walks a yaml.Node tree and replaces all scalar
// leaf values with "<redacted>", preserving keys, structure, and list shapes.
func redactYAMLNode(node *yaml.Node) {
	if node == nil {
		return
	}

	switch node.Kind {
	case yaml.DocumentNode:
		// Document node: recurse into content
		for _, child := range node.Content {
			redactYAMLNode(child)
		}
	case yaml.MappingNode:
		// Mapping node: content is [key1, value1, key2, value2, ...]
		// Keys stay unchanged, values get recursed/redacted
		for i := 0; i < len(node.Content); i += 2 {
			// Don't redact keys (i), only values (i+1)
			if i+1 < len(node.Content) {
				redactYAMLNode(node.Content[i+1])
			}
		}
	case yaml.SequenceNode:
		// Sequence node: content is [item1, item2, ...]
		for _, child := range node.Content {
			redactYAMLNode(child)
		}
	case yaml.ScalarNode:
		// Scalar node: this is a leaf value, redact it
		node.Value = "<redacted>"
	case yaml.AliasNode:
		// Alias node: follow the alias
		redactYAMLNode(node.Alias)
	}
}
