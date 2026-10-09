// Package gitops — engine-pin mutator.
//
// sharko-engine.yaml is NOT a Sharko envelope (design doc section
// 2.0) — it is a real ArgoCD Application object, applied as-is by ArgoCD.
// The catalog and cluster mutators in this package round-trip their
// files through parse-mutate-marshal (typed struct -> canonical yaml.v3
// emit), which is fine for Sharko's own enveloped files but WRONG here:
// AC 2.5 requires the pin-bump PR to change ONLY the targetRevision
// line(s), nothing else — no reformatting, no reordering, no dropped
// comments. A full re-marshal cannot make that guarantee; a targeted
// single-line edit of the original bytes can, by construction.
//
// UpdateEnginePinVersion therefore does NOT round-trip the document. It
// parses only to LOCATE the correct line (yaml.Node gives byte-accurate
// Line numbers), then splices the original source text at exactly that
// line. Every other byte — comments, key order, blank lines, the second
// source's own targetRevision (the git branch, e.g. "main") — passes
// through untouched.
package gitops

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// targetRevisionLineRE captures a `targetRevision:` line's three parts:
// the prefix up to and including any opening quote, the value token, and
// the closing quote plus anything after it (trailing comment, whitespace).
// Preserving groups 1 and 3 verbatim is what keeps the diff to the value
// only — quoting style and trailing comments survive the edit unchanged.
var targetRevisionLineRE = regexp.MustCompile(`^(\s*targetRevision:\s*['"]?)([^'"#\s]+)(.*)$`)

// UpdateEnginePinVersion changes the engine chart's pinned version inside
// sharko-engine.yaml — the file docs/design/2026-07-30-v4-data-file-format.md
// section 2.5 calls "the engine pin". engineChartName distinguishes the
// engine chart's Helm source (which carries both a `chart:` field and the
// `targetRevision:` to change) from the file's second source, the git
// values ref (which carries a `targetRevision:` too — the branch name,
// e.g. "main" — but no `chart:` field). Only the first is ever touched.
//
// Returns an error if the document does not parse, or if no source with a
// matching `chart:` field is found — both indicate the file is not the
// shape Sharko itself writes, which should never be silently "fixed" by
// this function.
func UpdateEnginePinVersion(data []byte, engineChartName, newVersion string) ([]byte, error) {
	if newVersion == "" {
		return nil, fmt.Errorf("new version is required")
	}

	trNode, err := locateEnginePinTargetRevision(data, engineChartName)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(string(data), "\n")
	lineIdx := trNode.Line - 1 // yaml.Node.Line is 1-based
	if lineIdx < 0 || lineIdx >= len(lines) {
		return nil, fmt.Errorf("sharko-engine.yaml: targetRevision located at an out-of-range line (%d)", trNode.Line)
	}

	m := targetRevisionLineRE.FindStringSubmatch(lines[lineIdx])
	if m == nil {
		return nil, fmt.Errorf("sharko-engine.yaml: line %d does not match the expected 'targetRevision: <value>' shape: %q", trNode.Line, lines[lineIdx])
	}
	lines[lineIdx] = m[1] + newVersion + m[3]

	return []byte(strings.Join(lines, "\n")), nil
}

// EnginePinVersion is the read-only counterpart to UpdateEnginePinVersion:
// it returns the engine chart's currently pinned targetRevision without
// modifying anything. Used by the pin-bump check (is an upgrade even
// available?) before deciding whether to open a PR.
func EnginePinVersion(data []byte, engineChartName string) (string, error) {
	trNode, err := locateEnginePinTargetRevision(data, engineChartName)
	if err != nil {
		return "", err
	}
	return trNode.Value, nil
}

// locateEnginePinTargetRevision parses data as sharko-engine.yaml and
// returns the yaml.Node for the targetRevision scalar inside the source
// whose chart field equals engineChartName — the shared lookup behind both
// EnginePinVersion (read) and UpdateEnginePinVersion (read then splice).
func locateEnginePinTargetRevision(data []byte, engineChartName string) (*yaml.Node, error) {
	if engineChartName == "" {
		return nil, fmt.Errorf("engine chart name is required")
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing sharko-engine.yaml: %w", err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("sharko-engine.yaml: expected a YAML mapping at the document root")
	}
	root := doc.Content[0]

	spec := mapValue(root, "spec")
	if spec == nil {
		return nil, fmt.Errorf("sharko-engine.yaml: missing spec")
	}
	sources := mapValue(spec, "sources")
	if sources == nil || sources.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("sharko-engine.yaml: missing spec.sources")
	}

	for _, item := range sources.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		chartVal := mapValue(item, "chart")
		if chartVal == nil || chartVal.Value != engineChartName {
			continue
		}
		trNode := mapValue(item, "targetRevision")
		if trNode == nil {
			return nil, fmt.Errorf("sharko-engine.yaml: source with chart %q has no targetRevision", engineChartName)
		}
		return trNode, nil
	}
	return nil, fmt.Errorf("sharko-engine.yaml: no source with chart %q found — is this the engine pin file Sharko wrote?", engineChartName)
}

// mapValue returns the value node for key in a YAML mapping node, or nil
// if n is not a mapping or the key is absent. Content alternates
// key, value, key, value, ...
func mapValue(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}
