package orchestrator

import (
	"bytes"
	"fmt"
	"path/filepath"
	"regexp"
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

	// Redact secret-looking scalar values recursively (W3b fix)
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

// sensitiveKeysExact is a case-insensitive set of canonical secret key names.
// Reuses the same detection heuristic as internal/logging/redact.go for consistency.
var sensitiveKeysExact = []string{
	"token",
	"password",
	"kubeconfig",
	"secret",
	"pat",
	"bearer_token",
	"authorization",
	"api_key",
	"apikey",
	"auth_token",
	"access_token",
	"refresh_token",
	"private_key",
	"cert_data",
	"key", // catch bare "key" (api.key, etc.) — common secret field name
}

// sensitiveKeySuffixes catches dynamic secret key names like db_password, argocd_token.
var sensitiveKeySuffixes = []string{
	"_token",
	"_password",
	"_secret",
	"_key",
}

// jwtRegex matches canonical three-segment JWTs (base64url header.payload.signature).
var jwtRegex = regexp.MustCompile(`^eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$`)

// base64BlobRegex matches base64-encoded blobs (kubeconfig fragments, certificates).
var base64BlobRegex = regexp.MustCompile(`^[A-Za-z0-9+/=]+$`)

const base64BlobMinLen = 100

// isSensitiveKey returns true if the key matches a canonical secret name (case-insensitive)
// or ends with a secret suffix (_token, _password, _secret, _key).
// Reuses the same heuristic as internal/logging/redact.go for consistency.
func isSensitiveKey(key string) bool {
	if key == "" {
		return false
	}
	// Exact match, case-insensitive
	for _, candidate := range sensitiveKeysExact {
		if strings.EqualFold(key, candidate) {
			return true
		}
	}
	// Suffix match, case-insensitive
	lower := strings.ToLower(key)
	for _, suffix := range sensitiveKeySuffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

// shouldRedactValue returns true if the string value looks like a secret:
// JWT-shaped, or a base64 blob >100 chars.
func shouldRedactValue(s string) bool {
	if jwtRegex.MatchString(s) {
		return true
	}
	if len(s) >= base64BlobMinLen && base64BlobRegex.MatchString(s) {
		return true
	}
	return false
}

// redactYAMLNode recursively walks a yaml.Node tree and redacts secret-looking values.
//
// REDACTION RULE (security-auditor approved):
// - Scalar values under secret-looking keys (token, password, *_secret, etc.) → always <redacted>
// - Scalar values that look like secrets (JWTs, base64 blobs >100 chars) → always <redacted>
// - Non-secret scalars (booleans, numbers, non-secret strings) → revealed in clear
// - YAML type tags (!!null, !!bool, !!str) → stripped from output
//
// Conservative: when in doubt, redact. This loosens the prior "redact-all-values" behavior
// to show plainly non-secret config (region: eu-west-1, enabled: true) while keeping the
// secret-safe default for anything that could be a credential.
//
// The keyPath parameter tracks the parent key so we can apply key-based redaction to
// mapping values. Pass "" for the root call.
func redactYAMLNode(node *yaml.Node) {
	redactYAMLNodeWithKey(node, "")
}

func redactYAMLNodeWithKey(node *yaml.Node, keyPath string) {
	if node == nil {
		return
	}

	// Strip YAML type tags (W3c fix) — these leak internal type info (!!null, !!bool)
	// that clutters the diff. Keep Tag empty so the encoder uses default type inference.
	node.Tag = ""

	switch node.Kind {
	case yaml.DocumentNode:
		// Document node: recurse into content
		for _, child := range node.Content {
			redactYAMLNodeWithKey(child, "")
		}
	case yaml.MappingNode:
		// Mapping node: content is [key1, value1, key2, value2, ...]
		// Keys stay unchanged, values get recursed with key context
		for i := 0; i < len(node.Content); i += 2 {
			keyNode := node.Content[i]
			valueNode := node.Content[i+1]
			// Strip tag from key too
			keyNode.Tag = ""
			// Recurse into value with the key name for context
			redactYAMLNodeWithKey(valueNode, keyNode.Value)
		}
	case yaml.SequenceNode:
		// Sequence node: content is [item1, item2, ...]
		for _, child := range node.Content {
			redactYAMLNodeWithKey(child, keyPath)
		}
	case yaml.ScalarNode:
		// Scalar node: decide whether to redact based on key and value
		if shouldRedactScalar(keyPath, node.Value) {
			node.Value = "<redacted>"
		}
		// else: reveal the value in clear (non-secret scalar)
	case yaml.AliasNode:
		// Alias node: follow the alias
		if node.Alias != nil {
			redactYAMLNodeWithKey(node.Alias, keyPath)
		}
	}
}

// shouldRedactScalar returns true if the scalar should be redacted based on:
// 1. The key looks secret-like (token, password, *_secret, *_key, etc.)
// 2. The value looks secret-like (JWT, base64 blob >100 chars)
//
// Otherwise returns false → reveal the value (booleans, numbers, region names, etc.)
func shouldRedactScalar(key, value string) bool {
	// Key-based redaction: if the key is secret-shaped, redact regardless of value
	if isSensitiveKey(key) {
		return true
	}
	// Value-based redaction: if the value looks like a secret (JWT, base64 blob), redact
	if shouldRedactValue(value) {
		return true
	}
	// Otherwise: safe to reveal (booleans like "true", numbers, region names, etc.)
	return false
}
