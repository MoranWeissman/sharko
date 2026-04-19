// Package orchestrator — smart values layer (v1.21 Epic V121-6).
//
// The smart values layer turns an upstream Helm chart's `values.yaml` into
// two things in a single Sharko-managed file:
//
//  1. A "global values" body — every field from the chart, except those
//     classified as cluster-specific, are kept at their original position
//     with their original value. Cluster-specific fields are commented-out
//     in place (placeholder `# <key>: <cluster-specific>`).
//
//  2. A trailing per-cluster template block — a YAML-shaped, fully
//     commented block at the bottom of the same file showing the
//     cluster-specific fields under the addon's stanza key, ready to be
//     uncommented and dropped into a cluster's overrides file
//     (`configuration/addons-clusters-values/<cluster>.yaml`).
//
// Both outputs are wrapped in a self-describing header (see header.go).
//
// Locked design (Moran, 2026-04-19):
//   - Heuristic-only when AI is not configured. The LLM call is additive
//     when available — it never replaces the heuristic, only adds paths.
//   - No ConfigMap/BoltDB cache. Git is the cache: once an addon is
//     processed, the annotated file lives in the user's addons repo.
//   - The global values file is wrapped under the addon name as the
//     top-level key, matching the v1.20 convention used by
//     SetGlobalAddonValues.
//
// Tradeoff in this implementation: the splitter operates on the upstream
// values.yaml *line by line* rather than round-tripping through a YAML
// parser. The reason is that yaml.Unmarshal/Marshal strips comments and
// loses key ordering, both of which are part of the user-facing value of
// the generated file. Tests in smart_values_test.go cover the cases where
// this textual approach is fragile (deeply nested maps, list children,
// multi-line strings).
//
// The textual approach is intentionally conservative: when it sees a
// scalar leaf at a path that matches the heuristic, it comments out that
// single line and replaces the value with `<cluster-specific>`. It does
// NOT recursively comment out an entire sub-map; that would change the
// shape of the file in ways the per-cluster template block already covers.

package orchestrator

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// ─── Heuristic: cluster-specific field detection ────────────────────────────
//
// `clusterSpecificPatterns` is the closed list from the design doc §4.4.2.
// Each pattern is matched against the FULL dotted path of a leaf field
// (e.g. `ingress.tls[0].hosts[0]` or `controller.replicaCount`). Matching
// is case-insensitive and uses simple `*` glob semantics where `*` matches
// any non-`.` segment. We deliberately don't use full glob libraries —
// these patterns are short and the rules are explicit.
//
// The patterns mirror the ones documented in the design as the v1.21
// shipping default. A schema-PR can extend the list; until then this is
// the source of truth for what counts as "cluster-specific".
var clusterSpecificPatterns = []string{
	// Connection / ingress targets
	"*.host",
	"*.hostname",
	"*.domain",
	"*.endpoint",
	"*.url",
	// TLS / ingress structure
	"*.ingress.*",
	"*.ingress",
	"*.tls.*",
	"*.tls",
	// Sizing
	"*.replicacount",
	"*.replicas",
	// Resource sizing — almost always cluster-specific (cluster size, tier)
	"*.resources.*",
	"*.resources",
	// Storage
	"*.persistence.*",
	"*.persistence",
	"*.storageclass",
	"*.storageclassname",
	"*.pvc.*",
	"*.pvc",
	// Scheduling
	"*.nodeselector",
	"*.nodeselector.*",
	"*.tolerations",
	"*.tolerations.*",
	"*.affinity",
	"*.affinity.*",
	// Secrets / external integrations
	"*.externalsecret",
	"*.externalsecret.*",
	"*.externalsecrets",
	"*.externalsecrets.*",
	"*.existingsecret",
	"*.secretname",
	// Cluster identity
	"*.clustername",
	"*.region",
	"*.environment",
	// IRSA / Workload Identity
	"*.serviceaccount.annotations.*",
}

// matchesClusterSpecific returns true when the dotted path matches at least
// one pattern in clusterSpecificPatterns. Both pattern and path are
// lowercased before comparison; `*` matches one path segment.
func matchesClusterSpecific(dottedPath string) bool {
	hay := strings.ToLower(dottedPath)
	for _, pat := range clusterSpecificPatterns {
		if globMatch(strings.ToLower(pat), hay) {
			return true
		}
	}
	return false
}

// globMatch implements a dotted-glob matcher with three rules for `*`:
//
//   - Leading `*` (e.g. `*.host`)  — matches any prefix of zero or more
//     segments. So `*.host` matches both `host` and `service.host`.
//   - Trailing `*` (e.g. `*.ingress.*`) — matches any suffix of zero or
//     more segments. So `*.ingress.*` matches `ingress`, `ingress.tls`,
//     and `controller.ingress.tls.hosts`.
//   - Interior `*` (e.g. `*.serviceaccount.annotations.*`) — also
//     follows the prefix-zero-or-more semantic when at position 0.
//
// We pick this slightly liberal interpretation deliberately — the
// "leading `*` matches zero segments" rule lets the design's pattern
// list cover top-level chart fields (`domain`, `clusterName`,
// `replicaCount`) without forcing every entry to be doubled with a no-`*`
// version. The patterns and the matcher are co-designed; tests pin both.
func globMatch(pattern, candidate string) bool {
	pp := strings.Split(pattern, ".")
	cp := strings.Split(candidate, ".")
	return globMatchSegments(pp, cp)
}

// globMatchSegments returns true when pp matches cp. It handles leading
// and trailing `*` as zero-or-more segment wildcards.
func globMatchSegments(pp, cp []string) bool {
	// Leading `*` — try matching zero, one, two… segments greedily.
	if len(pp) > 0 && pp[0] == "*" {
		// Trailing-only `*` (single-segment pattern `*`) → match anything.
		if len(pp) == 1 {
			return true
		}
		// Try every prefix of cp (including empty) for the leading `*`.
		for skip := 0; skip <= len(cp); skip++ {
			if globMatchSegments(pp[1:], cp[skip:]) {
				return true
			}
		}
		return false
	}
	// Trailing `*` — accepts any suffix at this point.
	if len(pp) == 1 && pp[0] == "*" {
		return true
	}
	// Both empty → match.
	if len(pp) == 0 && len(cp) == 0 {
		return true
	}
	// One empty, other not → no match.
	if len(pp) == 0 || len(cp) == 0 {
		return false
	}
	if pp[0] != cp[0] {
		return false
	}
	return globMatchSegments(pp[1:], cp[1:])
}

// ClassifyClusterSpecificFields runs the heuristic over a chart's
// values.yaml bytes and returns the sorted set of dotted paths that
// matched. Used by callers that want the classification list for logging,
// audit detail, or a UI preview without doing the full split.
//
// The function is pure: no I/O, no AI call. It's the same engine the
// splitter uses, exposed independently for testability.
func ClassifyClusterSpecificFields(valuesYAML []byte) []string {
	if len(valuesYAML) == 0 {
		return nil
	}
	hits := map[string]struct{}{}
	for _, p := range walkLeafPaths(valuesYAML) {
		if matchesClusterSpecific(p) {
			hits[p] = struct{}{}
		}
	}
	out := make([]string, 0, len(hits))
	for p := range hits {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// smartFrame is the parser stack entry shared between walkLeafPaths and
// the splitter. Defined at package scope so helper functions can take
// `[]smartFrame` without redeclaring the anonymous struct each time.
type smartFrame struct {
	indent int
	key    string
}

// walkLeafPaths returns every leaf dotted path in the YAML. Lines are
// parsed textually so we keep ordering and comments stable for the split
// step; the parse here is intentionally simple (top-level + map nesting,
// no list-element tracking — list elements all share their parent path).
func walkLeafPaths(valuesYAML []byte) []string {
	var stack []smartFrame
	var paths []string

	for _, raw := range strings.Split(string(valuesYAML), "\n") {
		line := raw
		// Skip blank lines and comments.
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}

		indent := leadingSpaces(line)
		// Pop any stack frames at the same or deeper indent.
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}

		colonIdx := strings.Index(trim, ":")
		// Lines like "- name: foo" — list child; treat the key as a leaf
		// under the parent path.
		if strings.HasPrefix(trim, "-") {
			afterDash := strings.TrimSpace(strings.TrimPrefix(trim, "-"))
			if afterDash == "" {
				continue
			}
			cIdx := strings.Index(afterDash, ":")
			if cIdx == -1 {
				continue
			}
			key := afterDash[:cIdx]
			parent := buildPath(stack, "")
			full := parent
			if full == "" {
				full = key
			} else {
				full = full + "." + key
			}
			paths = append(paths, full)
			continue
		}
		if colonIdx == -1 {
			continue
		}
		key := strings.TrimSpace(trim[:colonIdx])
		value := strings.TrimSpace(trim[colonIdx+1:])

		full := buildPath(stack, key)
		// Map / sequence parents (no inline value) — push frame, also emit
		// the path so patterns like `*.ingress` (without `.*`) match the
		// parent itself.
		if value == "" || value == "{}" || value == "[]" {
			paths = append(paths, full)
			stack = append(stack, smartFrame{indent: indent, key: key})
			continue
		}
		// Scalar leaf.
		paths = append(paths, full)
	}
	return paths
}

func leadingSpaces(s string) int {
	n := 0
	for _, r := range s {
		if r == ' ' {
			n++
			continue
		}
		break
	}
	return n
}

func buildPath(stack []smartFrame, leaf string) string {
	parts := make([]string, 0, len(stack)+1)
	for _, f := range stack {
		parts = append(parts, f.key)
	}
	if leaf != "" {
		parts = append(parts, leaf)
	}
	return strings.Join(parts, ".")
}

// ─── Splitter: emit global file + trailing template block ───────────────────

// SmartValuesInput is the input to SplitUpstreamValues.
type SmartValuesInput struct {
	AddonName     string
	Chart         string
	Version       string
	RepoURL       string
	UpstreamValues []byte // raw chart values.yaml bytes (comments preserved)
	AIAnnotated    bool   // true if an AI annotation pass also ran
	AIOptOut       bool   // true if the user opted out per-addon

	// ExtraClusterSpecificPaths is the optional union-additive set of
	// dotted paths the AI annotator returned (V121-7 Story 7.2). These
	// paths are merged into the heuristic's classification — never
	// subtractive. Empty when AI was skipped.
	ExtraClusterSpecificPaths []string
}

// SmartValuesOutput is the result of SplitUpstreamValues.
type SmartValuesOutput struct {
	// File is the bytes to commit at
	// `configuration/addons-global-values/<addon>.yaml`.
	File []byte
	// ClusterSpecificPaths is the sorted list of dotted paths that were
	// classified as cluster-specific (used for audit detail).
	ClusterSpecificPaths []string
}

// SplitUpstreamValues runs the smart-values pipeline:
//  1. Classify cluster-specific paths via the heuristic.
//  2. Emit the global file with cluster-specific scalar leaves
//     commented out at their original position.
//  3. Append a per-cluster template block under the addon name,
//     ready to be copied into `addons-clusters-values/<cluster>.yaml`.
//  4. Stamp the self-describing header on the front.
//
// The function is pure: no I/O, no AI call. AI annotation is layered
// orthogonally by the caller (Epic V121-7 plumbs that in via the same
// header flag); for v1.21 the heuristic-only path ships first.
//
// Root-cause fix (v1.21 QA Bundle 4, Fix #6 — Velero double-wrap):
// some upstream chart values.yaml files (Velero, a few Bitnami-style
// charts) put all of their settings under a single top-level key that
// matches the chart name (`velero:`). Sharko's wrapper used to add
// another `<addon>:` line on top, producing `velero:\n  velero:\n    …`.
// We now detect that case textually (single non-comment top-level key
// equal to the addon or chart name) and unwrap one level of indentation
// before re-wrapping.
func SplitUpstreamValues(in SmartValuesInput) SmartValuesOutput {
	in.UpstreamValues = unwrapChartNameRoot(in.UpstreamValues, in.AddonName, in.Chart)
	clusterPaths := ClassifyClusterSpecificFields(in.UpstreamValues)

	// V121-7.2 union: merge AI-suggested cluster-specific paths into the
	// heuristic set. We require the path to actually exist as a leaf in
	// the upstream values (via walkLeafPaths) before honoring it — the
	// LLM occasionally hallucinates paths and we don't want to inject
	// `<cluster-specific>` markers under keys that aren't there.
	if len(in.ExtraClusterSpecificPaths) > 0 {
		valid := map[string]struct{}{}
		for _, p := range walkLeafPaths(in.UpstreamValues) {
			valid[p] = struct{}{}
		}
		seen := map[string]struct{}{}
		for _, p := range clusterPaths {
			seen[p] = struct{}{}
		}
		for _, p := range in.ExtraClusterSpecificPaths {
			if _, ok := valid[p]; !ok {
				continue
			}
			if _, dup := seen[p]; dup {
				continue
			}
			seen[p] = struct{}{}
			clusterPaths = append(clusterPaths, p)
		}
		sort.Strings(clusterPaths)
	}

	// Step 2: emit the global body with the addon-name top-level key.
	// We re-use the v1.20 convention: every line of the upstream values
	// gets indented two spaces and prefixed under `<addon>:`. Lines whose
	// dotted path matches a cluster-specific pattern are commented out
	// with `<cluster-specific>` placeholder values.
	var b strings.Builder
	header := WriteSmartValuesHeader(SmartValuesHeader{
		Chart:       in.Chart,
		Version:     in.Version,
		RepoURL:     in.RepoURL,
		AIAnnotated: in.AIAnnotated,
		AIOptOut:    in.AIOptOut,
	})
	b.WriteString(header)

	b.WriteString(in.AddonName)
	b.WriteString(":\n")

	clusterSet := map[string]struct{}{}
	for _, p := range clusterPaths {
		clusterSet[p] = struct{}{}
	}

	// Track current map nesting so we can rebuild the dotted path per line.
	var stack []smartFrame

	for _, raw := range strings.Split(string(in.UpstreamValues), "\n") {
		line := raw
		trim := strings.TrimSpace(line)

		// Blank lines / comments are passed through verbatim, just indented
		// under the addon-name key.
		if trim == "" {
			b.WriteString("\n")
			continue
		}
		if strings.HasPrefix(trim, "#") {
			b.WriteString("  ")
			b.WriteString(line)
			b.WriteString("\n")
			continue
		}

		indent := leadingSpaces(line)
		// Pop frames at same/deeper indent.
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}

		// Compute the dotted path for THIS line.
		var full string
		if strings.HasPrefix(trim, "-") {
			// List child (e.g. "- name: foo") — path uses parent + element key
			afterDash := strings.TrimSpace(strings.TrimPrefix(trim, "-"))
			if cIdx := strings.Index(afterDash, ":"); cIdx != -1 {
				key := afterDash[:cIdx]
				parent := framePath(stack)
				if parent == "" {
					full = key
				} else {
					full = parent + "." + key
				}
			}
			// We never comment-out individual list children — list shape
			// matters more than item-level annotation, and the per-cluster
			// template carries the full sub-tree below. Pass through.
			b.WriteString("  ")
			b.WriteString(line)
			b.WriteString("\n")
			continue
		}
		colonIdx := strings.Index(trim, ":")
		if colonIdx == -1 {
			// Continuation of a multi-line scalar (block literal etc.) —
			// pass through.
			b.WriteString("  ")
			b.WriteString(line)
			b.WriteString("\n")
			continue
		}
		key := strings.TrimSpace(trim[:colonIdx])
		value := strings.TrimSpace(trim[colonIdx+1:])

		full = framePathWith(stack, key)

		_, isClusterSpecific := clusterSet[full]

		if value == "" || value == "{}" || value == "[]" {
			// Map parent. If the parent itself matches (e.g. `*.ingress`
			// without a `.*` suffix is in the patterns), we DO NOT comment
			// it out — sub-map children carry the cluster-specific bits
			// via the template block. Pass through and push the frame.
			b.WriteString("  ")
			b.WriteString(line)
			b.WriteString("\n")
			stack = append(stack, smartFrame{indent: indent, key: key})
			continue
		}

		// Scalar leaf.
		if isClusterSpecific {
			// Replace the line with a `# <key>: <cluster-specific>`
			// comment that preserves indentation. The original value is
			// dropped from the global file because it's not a sane
			// cluster-default.
			leadingPad := strings.Repeat(" ", indent)
			b.WriteString("  ")
			b.WriteString(leadingPad)
			b.WriteString("# ")
			b.WriteString(key)
			b.WriteString(": <cluster-specific>")
			b.WriteString("\n")
			continue
		}

		// Pass-through.
		b.WriteString("  ")
		b.WriteString(line)
		b.WriteString("\n")
	}

	// Step 3: per-cluster template block at the bottom.
	tpl := perClusterTemplate(in.AddonName, clusterPaths)
	b.WriteString("\n")
	b.WriteString(tpl)

	return SmartValuesOutput{
		File:                 []byte(b.String()),
		ClusterSpecificPaths: clusterPaths,
	}
}

// framePath renders the current dotted path captured by the parser stack.
func framePath(stack []smartFrame) string {
	parts := make([]string, 0, len(stack))
	for _, f := range stack {
		parts = append(parts, f.key)
	}
	return strings.Join(parts, ".")
}

// framePathWith renders parent stack + leaf key.
func framePathWith(stack []smartFrame, leaf string) string {
	parts := make([]string, 0, len(stack)+1)
	for _, f := range stack {
		parts = append(parts, f.key)
	}
	parts = append(parts, leaf)
	return strings.Join(parts, ".")
}

// perClusterTemplate returns the trailing commented block. Format matches
// design §4.4.1 step 4.
//
// The block intentionally renders ONLY the cluster-specific paths (one
// leaf per line) under the addon name as a flat-keyed YAML block that the
// user copies into a per-cluster overrides file. We do not try to nest
// the template — flat dotted keys are easier to spot-check during PR
// review and survive copy-paste in cases where only one or two cluster-
// specific keys actually need a per-cluster value.
func perClusterTemplate(addonName string, clusterPaths []string) string {
	var b strings.Builder
	b.WriteString("# --- per-cluster overrides template ---\n")
	b.WriteString("# Copy under the addon's stanza in configuration/addons-clusters-values/<cluster>.yaml.\n")
	if len(clusterPaths) == 0 {
		b.WriteString("# (no cluster-specific fields detected — no overrides expected)\n")
		return b.String()
	}
	b.WriteString("# ")
	b.WriteString(addonName)
	b.WriteString(":\n")
	for _, p := range clusterPaths {
		b.WriteString("#   ")
		b.WriteString(renderTemplateLine(p))
		b.WriteString("\n")
	}
	return b.String()
}

// renderTemplateLine turns a dotted path into a flat-keyed YAML line. We
// preserve the original ordering (sorted lexicographically by the
// classifier) and use a placeholder value the user can spot. Nested paths
// use a single quoted string key so the YAML round-trips when the user
// uncomments it.
func renderTemplateLine(dottedPath string) string {
	if !strings.Contains(dottedPath, ".") {
		return fmt.Sprintf("%s: <set per cluster>", dottedPath)
	}
	// For nested paths, preserve the dotted form as a comment hint and
	// emit a quoted-key YAML stanza. The user typically rewrites this
	// into nested form when they paste it into the cluster file; the
	// quoted-key form is valid YAML either way.
	return fmt.Sprintf("%q: <set per cluster>", dottedPath)
}

// ─── Helpers used by the orchestrator add-flow ──────────────────────────────

// GenerateGlobalValuesFile is the orchestrator-facing entry point. It
// returns the bytes ready to commit at `<paths.GlobalValues>/<name>.yaml`.
//
// Pure function — caller is responsible for fetching upstream values and
// committing the result. Keeping HTTP/Git out makes it trivial to unit-
// test the splitter.
//
// `extraClusterPaths` is the optional set of LLM-suggested dotted paths
// to UNION with the heuristic's classification (V121-7 Story 7.2). Pass
// nil for the heuristic-only path (no AI, AI skipped, etc.).
func GenerateGlobalValuesFile(addonName, chart, version, repoURL string, upstream []byte, aiAnnotated, aiOptOut bool, extraClusterPaths ...string) []byte {
	out := SplitUpstreamValues(SmartValuesInput{
		AddonName:                 addonName,
		Chart:                     chart,
		Version:                   version,
		RepoURL:                   repoURL,
		UpstreamValues:            upstream,
		AIAnnotated:               aiAnnotated,
		AIOptOut:                  aiOptOut,
		ExtraClusterSpecificPaths: extraClusterPaths,
	})
	return out.File
}

// UnwrapChartNameRoot is the exported alias of unwrapChartNameRoot so the
// API layer's preview-merge endpoint can apply the same root-key detection
// the smart-values writer uses internally. Keeps double-wrap behaviour
// consistent across the seed flow and the diff-and-merge flow.
func UnwrapChartNameRoot(values []byte, addonName, chartName string) []byte {
	return unwrapChartNameRoot(values, addonName, chartName)
}

// unwrapChartNameRoot detects the case where the upstream values.yaml has
// a single top-level key matching the chart or addon name, and returns the
// inner content unwrapped (one indentation level removed).
//
// Why this exists: a few popular charts (notably Velero, some Bitnami-style
// charts) ship a values.yaml that already begins with `<chartName>:` at the
// top level. Sharko's smart-values writer wraps the upstream file under
// `<addonName>:` to match the v1.20 convention used by SetGlobalAddonValues
// — without this unwrap, the result is a double-wrap (`velero:\n  velero:\n
// …`) which is wrong shape for the Helm chart.
//
// The detector is intentionally textual and conservative:
//   - It walks lines, ignoring blanks and comment-only lines, until it
//     finds the FIRST non-comment line at indent==0.
//   - It must be a `key:` line (no inline value).
//   - The key must match (case-insensitive) the addon or chart name.
//   - There must be NO OTHER top-level non-comment keys further down.
//
// When all three are true, every non-blank, non-comment line beneath that
// root key is shifted left by the detected child indent (typically 2 spaces).
// Comment-only lines and blank lines pass through unchanged.
//
// When ANY check fails, the input is returned verbatim (no-op).
func unwrapChartNameRoot(values []byte, addonName, chartName string) []byte {
	if len(values) == 0 {
		return values
	}
	// Pass 1: find the candidate root and verify uniqueness.
	lines := strings.Split(string(values), "\n")
	var (
		rootIdx     = -1
		rootKey     string
		rootKeyLine string
		// childIndent is the indent of the first child line under the root.
		childIndent = -1
	)
	for i, raw := range lines {
		trim := strings.TrimSpace(raw)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		indent := leadingSpaces(raw)
		colonIdx := strings.Index(trim, ":")
		if rootIdx == -1 {
			// First non-blank/non-comment line — must be at indent 0 and
			// look like a parent map key (key: with no inline value).
			if indent != 0 || colonIdx == -1 {
				return values
			}
			key := strings.TrimSpace(trim[:colonIdx])
			value := strings.TrimSpace(trim[colonIdx+1:])
			if value != "" && value != "{}" && value != "[]" {
				return values
			}
			matches := strings.EqualFold(key, addonName) ||
				(chartName != "" && strings.EqualFold(key, chartName))
			if !matches {
				return values
			}
			rootIdx = i
			rootKey = key
			rootKeyLine = raw
			continue
		}
		// We found the root already — any other top-level key disqualifies.
		if indent == 0 {
			return values
		}
		if childIndent == -1 {
			childIndent = indent
		}
	}
	if rootIdx == -1 || childIndent <= 0 {
		// No root found, or no children under it — leave as-is.
		_ = rootKey
		_ = rootKeyLine
		return values
	}

	// Pass 2: rewrite. Drop the root key line; for every other line, strip
	// up to childIndent spaces of leading indentation. Lines that already
	// have less leading whitespace (comments at file edges, etc.) are
	// passed through untouched.
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
		// Comment-only lines that sit ABOVE the root key (file header
		// comments, license blurbs) — keep verbatim. We detect them by
		// position: anything before rootIdx.
		if i < rootIdx {
			b.WriteString(raw)
			if i < len(lines)-1 {
				b.WriteString("\n")
			}
			continue
		}
		// Strip up to childIndent leading spaces.
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
	return []byte(b.String())
}

// GlobalValuesPath returns the canonical path inside the user's repo
// where the global values file for an addon lives.
func GlobalValuesPath(globalDir, addonName string) string {
	dir := strings.TrimSuffix(globalDir, "/")
	if dir == "" {
		dir = "configuration/addons-global-values"
	}
	return path.Join(dir, addonName+".yaml")
}
