package orchestrator

import (
	"bytes"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// BuildFileDiff computes a unified diff string for the given file change.
// Exported wrapper for use by api package (unwrap-globals endpoint).
func (o *Orchestrator) BuildFileDiff(path string, oldBytes []byte, newContent []byte, action string) string {
	return o.buildFileDiff(path, oldBytes, newContent, action)
}

// diffContextLines is how many unchanged lines a hunk shows around each
// change, the same default `diff -u` and git use.
const diffContextLines = 3

// maxLCSCells caps the table the line matcher builds (old lines x new
// lines, after the common start and end are trimmed). Past it the changed
// middle is shown as "all old lines removed, all new lines added" — still
// in order and still true, just not the smallest possible diff. Preview
// files are small; this only guards against a pathological request.
const maxLCSCells = 4_000_000

// buildFileDiff computes a unified diff string for the given file change.
// For values files (per-cluster or global), it redacts secret values in BOTH
// old and new content before diffing, so unchanged secrets appear as unchanged
// <redacted> values and the diff highlights only structural/key changes without
// ever exposing a secret value.
//
// The diff is a real, in-order line diff (longest common subsequence),
// printed in unified format: "--- path" / "+++ path" headers, then one
// "@@ -a,b +c,d @@" hunk per group of changes with up to diffContextLines
// unchanged lines (prefixed " ") around the removed ("-") and added ("+")
// lines. The earlier version printed a line only when it appeared nowhere
// in the other file, which hid every changed line that happened to repeat
// elsewhere (a new addon's "enabled: true" next to other addons' identical
// line), so a preview could not show what really changes.
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

	oldLines := splitDiffLines(string(oldRedacted))
	newLines := splitDiffLines(string(newRedacted))

	var buf bytes.Buffer
	buf.WriteString(fmt.Sprintf("--- %s\n", path))
	buf.WriteString(fmt.Sprintf("+++ %s\n", path))
	writeUnifiedHunks(&buf, lineDiff(oldLines, newLines), diffContextLines)
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

// diffOp is one line of a line diff: ' ' unchanged, '-' removed, '+' added.
type diffOp struct {
	kind byte
	text string
}

// lineDiff returns the in-order edit script that turns a into b. The
// common start and end are matched first (the usual case: a small edit in
// a larger file), then the changed middle goes through a longest-common-
// subsequence match so lines that repeat elsewhere in the file are still
// shown exactly where they change.
func lineDiff(a, b []string) []diffOp {
	ops := make([]diffOp, 0, len(a)+len(b))

	prefix := 0
	for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(a)-prefix && suffix < len(b)-prefix &&
		a[len(a)-1-suffix] == b[len(b)-1-suffix] {
		suffix++
	}

	for _, line := range a[:prefix] {
		ops = append(ops, diffOp{' ', line})
	}
	ops = append(ops, lcsDiff(a[prefix:len(a)-suffix], b[prefix:len(b)-suffix])...)
	for _, line := range a[len(a)-suffix:] {
		ops = append(ops, diffOp{' ', line})
	}
	return ops
}

// lcsDiff is the plain dynamic-programming LCS line diff. On a tie it
// prefers removing before adding, so a replaced line reads "-old" then
// "+new", the way every diff tool prints it.
func lcsDiff(a, b []string) []diffOp {
	n, m := len(a), len(b)
	var ops []diffOp
	if n == 0 || m == 0 || n*m > maxLCSCells {
		for _, line := range a {
			ops = append(ops, diffOp{'-', line})
		}
		for _, line := range b {
			ops = append(ops, diffOp{'+', line})
		}
		return ops
	}

	// lcs[i*(m+1)+j] = length of the LCS of a[i:] and b[j:].
	w := m + 1
	lcs := make([]int32, (n+1)*w)
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i*w+j] = lcs[(i+1)*w+j+1] + 1
			} else if lcs[(i+1)*w+j] >= lcs[i*w+j+1] {
				lcs[i*w+j] = lcs[(i+1)*w+j]
			} else {
				lcs[i*w+j] = lcs[i*w+j+1]
			}
		}
	}

	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{' ', a[i]})
			i++
			j++
		case lcs[(i+1)*w+j] >= lcs[i*w+j+1]:
			ops = append(ops, diffOp{'-', a[i]})
			i++
		default:
			ops = append(ops, diffOp{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{'-', a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{'+', b[j]})
	}
	return ops
}

// writeUnifiedHunks prints the edit script as unified-diff hunks. Changes
// closer together than 2*context unchanged lines share one hunk. An edit
// script with no changes prints nothing (the caller already wrote the
// file headers).
func writeUnifiedHunks(buf *bytes.Buffer, ops []diffOp, context int) {
	// oldLine[k] / newLine[k] = how many old / new lines come before ops[k].
	oldLine := make([]int, len(ops)+1)
	newLine := make([]int, len(ops)+1)
	for k, op := range ops {
		oldLine[k+1], newLine[k+1] = oldLine[k], newLine[k]
		if op.kind != '+' {
			oldLine[k+1]++
		}
		if op.kind != '-' {
			newLine[k+1]++
		}
	}

	k := 0
	for k < len(ops) {
		// Find the next change.
		for k < len(ops) && ops[k].kind == ' ' {
			k++
		}
		if k == len(ops) {
			return
		}
		start := k - context
		if start < 0 {
			start = 0
		}
		// Extend the hunk while the next change is within 2*context lines.
		end := k
		for {
			for end < len(ops) && ops[end].kind != ' ' {
				end++
			}
			gap := end
			for gap < len(ops) && ops[gap].kind == ' ' {
				gap++
			}
			if gap < len(ops) && gap-end <= 2*context {
				end = gap
				continue
			}
			break
		}
		stop := end + context
		if stop > len(ops) {
			stop = len(ops)
		}

		oldCount := oldLine[stop] - oldLine[start]
		newCount := newLine[stop] - newLine[start]
		buf.WriteString(fmt.Sprintf("@@ -%s +%s @@\n",
			hunkRange(oldLine[start], oldCount), hunkRange(newLine[start], newCount)))
		for _, op := range ops[start:stop] {
			buf.WriteByte(op.kind)
			buf.WriteString(op.text)
			buf.WriteString("\n")
		}
		k = stop
	}
}

// hunkRange prints one side of a hunk header the way GNU diff does:
// "start,count" with a 1-based start, and for an empty side the line
// BEFORE the hunk (so a brand-new file is "-0,0").
func hunkRange(before, count int) string {
	if count == 0 {
		return fmt.Sprintf("%d,0", before)
	}
	return fmt.Sprintf("%d,%d", before+1, count)
}

// redactValuesContent redacts secret values from per-cluster or global values
// files while preserving YAML structure, keys, and list shapes. Scalars under
// secret-looking keys, and scalars that look like secrets, become <redacted>
// (see redactYAMLNode). This allows meaningful diffs that show structural
// changes without exposing secret values.
//
// Comments. Every YAML comment is removed from a values file before it is
// shown, because a comment in a person's values file can hold anything —
// an old password someone commented out, a token pasted "for later" — and
// no key-based rule can see inside it. The ONE exception is the comment
// lines Sharko writes itself when it scaffolds a values file
// (globalValuesStub, clusterValuesStub, and the per-cluster override
// hints): a line in the file's opening comment block is kept only when it
// is, character for character, a line Sharko would write for this exact
// path — its text is fixed or built only from the addon and cluster names
// already in the path, so showing it reveals nothing new. See
// sharkoCommentAllowed.
//
// A values file that holds no values at all (empty, or comments only)
// shows as no lines — never as the word "null", which is what printing an
// empty YAML document back out produces.
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
	// Values files live under ClusterValues or GlobalValues directories
	// (v3, per-connection-configured) or under the fixed v4 values/
	// tree (V4GlobalValuesDir / V4ClusterValuesDir — v4 Wave 1 Story 4.3;
	// unlike the v3 paths these are NOT server-configurable, so they must
	// be checked unconditionally, not gated on a RepoPathsConfig field
	// being set).
	isValuesFile := false
	if o.paths.ClusterValues != "" && isUnderDirectory(path, o.paths.ClusterValues) {
		isValuesFile = true
	}
	if o.paths.GlobalValues != "" && isUnderDirectory(path, o.paths.GlobalValues) {
		isValuesFile = true
	}
	if isUnderDirectory(path, V4GlobalValuesDir) || isUnderDirectory(path, V4ClusterValuesDir) {
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

	var out bytes.Buffer
	out.WriteString(sharkoHeaderComments(path, content))

	if isEmptyYAMLDocument(&root) {
		return out.Bytes()
	}

	// Drop every comment the parser attached to the tree (see the doc
	// comment above), then redact secret-looking scalar values (W3b fix).
	stripYAMLComments(&root)
	redactYAMLNode(&root)

	// Marshal back to YAML
	var body bytes.Buffer
	encoder := yaml.NewEncoder(&body)
	encoder.SetIndent(2)
	if err := encoder.Encode(&root); err != nil {
		return []byte("<redacted: re-marshal failed>\n")
	}
	out.Write(body.Bytes())
	return out.Bytes()
}

// isEmptyYAMLDocument reports whether the parsed file holds no value at
// all: empty or whitespace input, a bare "---", or comments only. yaml.v3
// models a comments-only file as a document holding one implicit null
// (tag !!null, empty text); printed back out that becomes "null", a value
// the file never had. An explicit "null" or "~" written by a person keeps
// its text and is NOT treated as empty.
func isEmptyYAMLDocument(root *yaml.Node) bool {
	if root.Kind == 0 {
		return true
	}
	if root.Kind != yaml.DocumentNode {
		return false
	}
	if len(root.Content) == 0 {
		return true
	}
	if len(root.Content) == 1 {
		n := root.Content[0]
		return n.Kind == yaml.ScalarNode && n.Tag == "!!null" && n.Value == "" && n.Style == 0
	}
	return false
}

// stripYAMLComments removes every head, line and foot comment from the
// tree, so none of them reach the printed preview.
func stripYAMLComments(node *yaml.Node) {
	if node == nil {
		return
	}
	node.HeadComment = ""
	node.LineComment = ""
	node.FootComment = ""
	for _, child := range node.Content {
		stripYAMLComments(child)
	}
}

// sharkoHeaderComments returns the comment lines from the file's opening
// comment block (everything before the first line that is not blank and
// not a comment) that Sharko itself writes for this path, in their
// original order, each ending in a newline. Any other comment is dropped.
// Blank lines between kept comments are kept (one at most), so a seeded
// stub reads the way it does in the file.
func sharkoHeaderComments(path string, content []byte) string {
	var kept []string
	pendingBlank := false
	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimRight(raw, " \t\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			pendingBlank = len(kept) > 0
			continue
		}
		if !strings.HasPrefix(trimmed, "#") {
			break // first real YAML line: the opening block is over
		}
		if !sharkoCommentAllowed(path, line) {
			continue
		}
		if pendingBlank {
			kept = append(kept, "")
		}
		pendingBlank = false
		kept = append(kept, line)
	}
	if len(kept) == 0 {
		return ""
	}
	return strings.Join(kept, "\n") + "\n"
}

// sharkoHintHeaderLines are the two fixed lines renderV4ClusterOverrideHints
// writes above the per-cluster override hints.
var sharkoHintHeaderLines = map[string]bool{
	"# --- per-cluster overrides ---":                                                true,
	"# Uncomment and set a real value for any field this cluster needs to override.": true,
}

// sharkoHintPlaceholderRe matches an override hint line with the fixed
// placeholder value (renderTemplateLine): "# key: <set per cluster>" or
// "# \"a.b\": <set per cluster>". The key is a values key, which the
// redacted preview shows in clear anyway; the value is fixed text.
var sharkoHintPlaceholderRe = regexp.MustCompile(`^# (?:[A-Za-z0-9_.\-]+|"[^"\\]*"): <set per cluster>$`)

// sharkoCommentAllowed reports whether line is, exactly, a comment line
// Sharko writes when it scaffolds the values file at path. Only the v4
// values tree has such scaffolds; the addon and cluster names come from
// the path itself, so nothing here can carry text the path does not
// already show.
func sharkoCommentAllowed(path, line string) bool {
	rel, err := filepath.Rel(V4ClusterValuesDir, path)
	if err == nil && !strings.HasPrefix(rel, "..") {
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) != 2 || !strings.HasSuffix(parts[1], ".yaml") {
			return false
		}
		cluster, addon := parts[0], strings.TrimSuffix(parts[1], ".yaml")
		if lineIn(clusterValuesStub(addon, cluster), line) || sharkoHintHeaderLines[line] {
			return true
		}
		if sharkoHintPlaceholderRe.MatchString(line) {
			return true
		}
		return isClusterNameFactHint(line, cluster)
	}
	rel, err = filepath.Rel(V4GlobalValuesDir, path)
	if err == nil && !strings.HasPrefix(rel, "..") {
		if strings.Contains(filepath.ToSlash(rel), "/") || !strings.HasSuffix(rel, ".yaml") {
			return false
		}
		return lineIn(globalValuesStub(strings.TrimSuffix(rel, ".yaml")), line)
	}
	return false
}

// isClusterNameFactHint matches the one hint whose value is a real fact
// (renderV4TemplateLine): a key ending in "clusterName" set to this
// file's own cluster name, exactly as Sharko renders it.
func isClusterNameFactHint(line, cluster string) bool {
	body, ok := strings.CutPrefix(line, "# ")
	if !ok {
		return false
	}
	idx := strings.LastIndex(body, ": ")
	if idx <= 0 {
		return false
	}
	key := body[:idx]
	if strings.HasPrefix(key, `"`) {
		unq, err := strconv.Unquote(key)
		if err != nil {
			return false
		}
		key = unq
	}
	segs := strings.Split(key, ".")
	if strings.ToLower(segs[len(segs)-1]) != clusterNameFactSuffix {
		return false
	}
	// Rebuild the line the way Sharko writes it; only an exact match counts,
	// so the value can only ever be this file's own cluster name.
	return renderV4TemplateLine(key, cluster) == body
}

// lineIn reports whether line is exactly one of the lines of stub.
func lineIn(stub []byte, line string) bool {
	for _, l := range strings.Split(string(stub), "\n") {
		if l != "" && l == line {
			return true
		}
	}
	return false
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
