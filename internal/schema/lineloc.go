// Line-number resolution for schema validation failures.
//
// santhosh-tekuri/jsonschema validates decoded JSON-shaped values (map/
// slice/scalar) and has no notion of "source line" — a *jsonschema.
// ValidationError only carries an InstanceLocation, an RFC 6901 JSON
// Pointer (e.g. "/spec/addons/cert-manager/version") describing WHERE in
// the document tree the violation happened, not what line of the
// original file that was.
//
// v4 Wave 1 Story 2.6 requires `sharko validate-config` to name the
// file, the reason, AND the line for a broken data file. This file
// bridges that gap: it re-parses the same YAML bytes into a yaml.v3
// node tree (which DOES carry 1-based source line numbers on every
// node) and walks the same JSON Pointer segments through that tree.
package schema

import (
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// LineForInstanceLocation parses body as YAML and resolves the 1-based
// source line of the node addressed by instanceLocation — an RFC 6901
// JSON Pointer of the shape *jsonschema.ValidationError.InstanceLocation
// produces (see ValidationFailure.Locations).
//
// Best-effort by design. Two situations fall back to a still-useful
// approximation rather than failing outright:
//
//   - A "missing required property" violation points at a property that,
//     by definition, doesn't exist on disk — it has no line of its own.
//     stepYAMLNode reports !ok for that segment and the walk stops,
//     returning the line of the last node it DID resolve: the object
//     that should have held the missing field. That's still an
//     actionable jump target ("go to line 7 — the addons.cert-manager
//     block is missing something") rather than no line at all.
//   - Any other unresolvable segment (an out-of-range array index, a
//     segment against a scalar) behaves the same way.
//
// Returns (0, false) only when body itself doesn't parse as YAML at
// all, or is empty — callers should treat that as "no line info
// available" and print the violation without a line suffix rather than
// surfacing a second error over the one the schema validator already
// reported.
func LineForInstanceLocation(body []byte, instanceLocation string) (int, bool) {
	var doc yaml.Node
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return 0, false
	}
	if len(doc.Content) == 0 {
		return 0, false
	}
	node := doc.Content[0]
	for _, seg := range jsonPointerSegments(instanceLocation) {
		next, ok := stepYAMLNode(node, seg)
		if !ok {
			break
		}
		node = next
	}
	if node == nil {
		return 0, false
	}
	return node.Line, true
}

// jsonPointerSegments splits an RFC 6901 JSON Pointer into its unescaped
// segments. "" and "/" both mean "the document root" (no segments).
func jsonPointerSegments(pointer string) []string {
	if pointer == "" || pointer == "/" {
		return nil
	}
	trimmed := strings.TrimPrefix(pointer, "/")
	parts := strings.Split(trimmed, "/")
	for i, p := range parts {
		// RFC 6901 §4 unescaping order matters: ~1 before ~0.
		p = strings.ReplaceAll(p, "~1", "/")
		p = strings.ReplaceAll(p, "~0", "~")
		parts[i] = p
	}
	return parts
}

// stepYAMLNode resolves one JSON Pointer segment against a yaml.v3 node
// and returns the value node it points at. Mapping nodes look the
// segment up as a string key (via MappingValue); sequence nodes parse it
// as a 0-based index. Any other combination (segment against a scalar,
// out-of-range index, missing key) reports ok=false.
func stepYAMLNode(n *yaml.Node, seg string) (value *yaml.Node, ok bool) {
	for n != nil && n.Kind == yaml.AliasNode {
		// Sharko config never uses YAML anchors today, but resolving
		// through Alias defensively means a future hand-authored file
		// that does won't silently break line resolution.
		n = n.Alias
	}
	if n == nil {
		return nil, false
	}
	switch n.Kind {
	case yaml.MappingNode:
		_, v, found := MappingValue(n, seg)
		return v, found
	case yaml.SequenceNode:
		idx, err := strconv.Atoi(seg)
		if err != nil || idx < 0 || idx >= len(n.Content) {
			return nil, false
		}
		return n.Content[idx], true
	default:
		return nil, false
	}
}

// MappingValue looks up key in a YAML mapping node and returns both the
// key node and the value node, so a caller that needs the KEY's own
// source line (e.g. "this field isn't allowed here, and here's exactly
// where you wrote it") doesn't have to re-walk the tree. ok is false
// when n is nil, not a mapping, or the key isn't present.
//
// Exported for cmd/sharko's validate-config CLI, which uses it to build
// the ClusterAddons-specific semantic checks (filename-vs-
// spec.cluster, the preserveResourcesOnDeletion redirect) that a generic
// JSON Schema can't express — schema.MappingValue is the same primitive
// stepYAMLNode uses internally, kept in sync deliberately.
func MappingValue(n *yaml.Node, key string) (keyNode, valueNode *yaml.Node, ok bool) {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil, nil, false
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i], n.Content[i+1], true
		}
	}
	return nil, nil, false
}
