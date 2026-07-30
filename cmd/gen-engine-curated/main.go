// Command gen-engine-curated reads catalog/addons.yaml (the monorepo's own
// curated addon catalog — the single source of truth per design doc
// docs/design/2026-07-30-v4-data-file-format.md §4.7) and regenerates the
// GENERATED block inside charts/sharko-engine/values.yaml's `curated.addons`
// section.
//
// Why this exists (v4 Wave 1 Story 2.4/2.5 follow-up — the flagged gap): the
// engine chart merges `.Values.curated.addons` (the shipped catalog, baked
// into the chart at build time) with `.Values.spec.addons` (the user's own
// catalog/addons.yaml delta) — design doc §4.7:
//
//	merged = mergeOverwrite(deepCopy(curated.addons), spec.addons)
//
// Before this generator existed, charts/sharko-engine/values.yaml shipped
// `curated: addons: {}` — an empty map. That silently defeated the whole
// point of "your delta only needs to say what's different": every user
// switching on a Sharko-curated addon (cert-manager, sealed-secrets, ...)
// had to fully respecify repoURL/chart/namespace themselves in their own
// catalog/addons.yaml, because the shipped chart carried none of it.
//
// Where the output goes, and why not a separate file: `helm package
// charts/sharko-engine` (the release pipeline's actual packaging command —
// see .github/workflows/release.yml) bakes in exactly the chart directory's
// own values.yaml. A sibling `values-curated.yaml` would never make it into
// the packaged/signed .tgz. So generation must write INTO
// charts/sharko-engine/values.yaml itself. Full-file YAML marshal would
// destroy the file's hand-written comments (its main documentation), so
// this generator instead does marker-based text splicing: it replaces only
// the lines between the `# gen-engine-curated:begin` and
// `# gen-engine-curated:end` sentinels, leaving every other line — repo,
// hostCluster, project, spec, and every comment — byte-identical.
//
// Two ways to get the data were considered:
//
//  1. Re-parse catalog/addons.yaml by hand in this command. Rejected:
//     internal/catalog already owns validation (required fields, allowed
//     categories, ...) — re-implementing that here would drift the moment
//     the schema gains a field, the exact two-copies-drift problem §4.7
//     calls out for the DATA itself. Loading through catalog.Load() keeps
//     one parser or validator.
//  2. Load via catalog.Load() (embedded catalog/addons.yaml) and project
//     only the three fields the engine chart's ApplicationSet template
//     actually reads for a shipped addon: repoURL (from `repo`), chart,
//     and namespace (from `default_namespace`). Chosen. No `version` field
//     is ever emitted — design decision D7: a version baked into a signed
//     chart goes stale, so Sharko writes the running version into the
//     USER's own catalog/addons.yaml, never into the shipped catalog.
//
// Usage:
//
//	go run ./cmd/gen-engine-curated
//
// or via the Makefile:
//
//	make generate-engine-curated
//
// CI's "Engine Curated Up To Date" job (.github/workflows/ci.yml) re-runs
// this and fails the build on any diff — same shape as "Schemas Up To
// Date", "Provider Types Up To Date", and "Engine Version Up To Date".
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/MoranWeissman/sharko/internal/catalog"
)

// valuesYAMLPath is hard-coded (no CLI flag) for the same reason
// cmd/gen-engine-version's chartYAMLPath is: it is part of the public
// contract between this generator, the CI gate, and the marker comments
// inside the file itself.
const valuesYAMLPath = "charts/sharko-engine/values.yaml"

const (
	beginMarker = "  # gen-engine-curated:begin"
	endMarker   = "  # gen-engine-curated:end"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gen-engine-curated: "+err.Error())
		os.Exit(1)
	}
}

func run() error {
	cat, err := catalog.Load()
	if err != nil {
		return fmt.Errorf("loading catalog/addons.yaml: %w", err)
	}

	raw, err := os.ReadFile(valuesYAMLPath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", valuesYAMLPath, err)
	}

	block := renderCuratedBlock(cat.Entries())

	updated, err := spliceBetweenMarkers(string(raw), block)
	if err != nil {
		return fmt.Errorf("%s: %w", valuesYAMLPath, err)
	}

	if err := os.WriteFile(valuesYAMLPath, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", valuesYAMLPath, err)
	}

	fmt.Printf("gen-engine-curated: wrote %d curated addon entries into %s\n", len(cat.Entries()), valuesYAMLPath)
	return nil
}

// renderCuratedBlock builds the `addons:` mapping text (indented to sit
// under `curated:` at 2 spaces, so entries land at 4 and fields at 6,
// matching the file's existing indentation convention). entries is assumed
// already sorted by name — catalog.Load() guarantees this.
//
// Only the three fields the engine's ApplicationSet template
// (charts/sharko-engine/templates/appset.yaml) actually reads for a shipped
// addon are emitted: repoURL, chart, namespace. Everything else about an
// addon (description, docs, security score, ...) is Marketplace-browse
// metadata the engine has no use for. No `settings` block is emitted —
// catalog/addons.yaml has no structured settings field today (its `quirks`
// are deliberately free-text operational notes, not machine-actionable
// Argo CD settings — see design doc §4.7's own note on this). When a real
// per-addon setting default is needed, it belongs here once the catalog
// schema grows a structured field for it.
func renderCuratedBlock(entries []catalog.CatalogEntry) string {
	if len(entries) == 0 {
		return "  addons: {}\n"
	}
	var b strings.Builder
	b.WriteString("  addons:\n")
	for _, e := range entries {
		fmt.Fprintf(&b, "    %s:\n", yamlScalar(e.Name))
		fmt.Fprintf(&b, "      repoURL: %s\n", yamlScalar(e.Repo))
		fmt.Fprintf(&b, "      chart: %s\n", yamlScalar(e.Chart))
		fmt.Fprintf(&b, "      namespace: %s\n", yamlScalar(e.DefaultNamespace))
	}
	return b.String()
}

// yamlScalar quotes a scalar only when necessary for valid, unambiguous
// YAML (values containing "://" like repo URLs, or anything starting with a
// character that YAML could otherwise misparse). Addon names, chart names,
// and namespaces are plain DNS-safe tokens and render unquoted for
// readability; repo URLs always contain "://" and are quoted so the colon
// can never be misread as a mapping separator.
func yamlScalar(s string) string {
	if strings.Contains(s, "://") || s == "" {
		return fmt.Sprintf("%q", s)
	}
	return s
}

// spliceBetweenMarkers replaces every line strictly between beginMarker and
// endMarker (both kept, unchanged) with block. Returns an error if either
// marker is missing or out of order — a loud failure is much better than
// silently no-op'ing on a renamed/removed marker.
func spliceBetweenMarkers(original, block string) (string, error) {
	lines := strings.Split(original, "\n")
	beginIdx, endIdx := -1, -1
	for i, line := range lines {
		if strings.TrimRight(line, " ") == beginMarker {
			beginIdx = i
		}
		if strings.TrimRight(line, " ") == endMarker {
			endIdx = i
			break
		}
	}
	if beginIdx == -1 {
		return "", fmt.Errorf("marker %q not found", strings.TrimSpace(beginMarker))
	}
	if endIdx == -1 {
		return "", fmt.Errorf("marker %q not found", strings.TrimSpace(endMarker))
	}
	if endIdx <= beginIdx {
		return "", fmt.Errorf("marker %q appears before %q", strings.TrimSpace(endMarker), strings.TrimSpace(beginMarker))
	}

	var out strings.Builder
	out.WriteString(strings.Join(lines[:beginIdx+1], "\n"))
	out.WriteString("\n")
	out.WriteString(block)
	out.WriteString(strings.Join(lines[endIdx:], "\n"))
	return out.String(), nil
}
