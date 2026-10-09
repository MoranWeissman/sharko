// Package orchestrator — legacy `<addon>:` wrap detector + unwrapper for
// global values files (v1.21 Bundle 5).
//
// Background: pre-Bundle-5 versions of Sharko wrote the global values file
// under an `<addonName>:` (or `<chartName>:`) root key. The ApplicationSet
// template passes that file directly to Helm via `valueFiles:`, so Helm
// looked for chart values at the document root and silently ignored the
// nested values. Bundle 5 fixes the writer; this helper migrates existing
// files written by older versions.
//
// The detector + unwrapper here is purposefully textual: it preserves
// comments, blank lines, and key ordering. Round-tripping through
// yaml.Marshal would lose comments — and the smart-values header is the
// most important comment in the file, since it gates the version-mismatch
// banner.
//
// Detection rules (ALL must hold for `wasWrapped` to be true):
//   1. Ignoring blank/comment lines, the FIRST non-blank line is a
//      `key:` parent at indent 0 with no inline value.
//   2. The key (case-insensitive) matches `expectedAddonName` or
//      `expectedChartName` (when provided).
//   3. There are NO OTHER non-comment top-level keys (a multi-root file
//      is left alone — it's not a wrap pattern).
//
// When all three hold, every non-blank, non-comment line beneath the root
// is shifted left by the detected child indent. Comment-only lines and
// blank lines pass through unchanged. The root key line is removed.
//
// When ANY check fails, the input is returned verbatim with
// `wasWrapped == false`. No error is returned for the no-op case.

package orchestrator

import (
	"fmt"
	"strings"
)

// UnwrapGlobalValuesFile detects the legacy `<addonName>:` (or
// `<chartName>:`) wrap pattern and returns the unwrapped bytes. When the
// file is already unwrapped (or cannot be detected as a wrap), the input
// is returned verbatim with `wasWrapped == false`.
//
// `expectedChartName` may be empty when the caller has no catalog
// reference handy; in that case only `expectedAddonName` is matched.
//
// The error return is reserved for malformed input cases the caller may
// want to surface (e.g. truly invalid YAML at the root); detection
// failures DO NOT error — they return (input, false, nil).
func UnwrapGlobalValuesFile(yamlContent []byte, expectedAddonName, expectedChartName string) (unwrapped []byte, wasWrapped bool, err error) {
	if len(yamlContent) == 0 {
		return yamlContent, false, nil
	}
	if strings.TrimSpace(expectedAddonName) == "" {
		return yamlContent, false, fmt.Errorf("expectedAddonName is required")
	}

	// Pass 1: walk lines, find the candidate root, verify single-root.
	lines := strings.Split(string(yamlContent), "\n")
	rootIdx := -1
	childIndent := -1

	for i, raw := range lines {
		trim := strings.TrimSpace(raw)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		indent := leadingSpaces(raw)
		colonIdx := strings.Index(trim, ":")
		if rootIdx == -1 {
			// First non-blank, non-comment line — must be `key:` at indent 0
			// with no inline scalar.
			if indent != 0 || colonIdx == -1 {
				return yamlContent, false, nil
			}
			key := strings.TrimSpace(trim[:colonIdx])
			value := strings.TrimSpace(trim[colonIdx+1:])
			if value != "" && value != "{}" && value != "[]" {
				return yamlContent, false, nil
			}
			matches := strings.EqualFold(key, expectedAddonName) ||
				(expectedChartName != "" && strings.EqualFold(key, expectedChartName))
			if !matches {
				return yamlContent, false, nil
			}
			rootIdx = i
			continue
		}
		// Already found root — anything else at indent 0 disqualifies.
		if indent == 0 {
			return yamlContent, false, nil
		}
		if childIndent == -1 {
			childIndent = indent
		}
	}

	// No root, or root with no children → nothing to unwrap.
	if rootIdx == -1 {
		return yamlContent, false, nil
	}
	if childIndent <= 0 {
		// Root key with empty body. Treat as unwrapped (returns the file
		// minus the lone root line so the result is a valid empty doc).
		var b strings.Builder
		for i, raw := range lines {
			if i == rootIdx {
				continue
			}
			b.WriteString(raw)
			if i < len(lines)-1 {
				b.WriteString("\n")
			}
		}
		return []byte(b.String()), true, nil
	}

	// Pass 2: rewrite. Drop the root key line. For every other line:
	//   - If it sits ABOVE the root (file header comments), keep verbatim.
	//   - Otherwise, strip up to childIndent leading spaces. Lines with
	//     less leading whitespace (rare — comments at file edges) pass
	//     through with whatever leading whitespace they have.
	var b strings.Builder
	for i, raw := range lines {
		if i == rootIdx {
			continue
		}
		if strings.TrimSpace(raw) == "" {
			b.WriteString(raw)
			if i < len(lines)-1 {
				b.WriteString("\n")
			}
			continue
		}
		if i < rootIdx {
			b.WriteString(raw)
			if i < len(lines)-1 {
				b.WriteString("\n")
			}
			continue
		}
		out := raw
		strip := childIndent
		for strip > 0 && len(out) > 0 && out[0] == ' ' {
			out = out[1:]
			strip--
		}
		b.WriteString(out)
		if i < len(lines)-1 {
			b.WriteString("\n")
		}
	}
	return []byte(b.String()), true, nil
}

// DetectLegacyWrap is a lightweight read-side helper: returns true when
// the file looks like the legacy `<addonName>:` (or `<chartName>:`) wrap
// pattern. Used by the values-schema endpoint to flag the file for the
// migration banner WITHOUT performing the rewrite.
func DetectLegacyWrap(yamlContent []byte, expectedAddonName, expectedChartName string) bool {
	if len(yamlContent) == 0 {
		return false
	}
	_, wrapped, err := UnwrapGlobalValuesFile(yamlContent, expectedAddonName, expectedChartName)
	if err != nil {
		return false
	}
	return wrapped
}
