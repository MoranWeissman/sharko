// Package orchestrator — per-cluster template seeding.
//
// When a user enables an existing addon on a new cluster, Sharko reads
// the trailing per-cluster template block from the global values file
// (`addons-global-values/<addon>.yaml`) and seeds the addon's stanza
// inside `addons-clusters-values/<cluster>.yaml`. The seeded stanza is
// the SAME flat-keyed YAML that `perClusterTemplate` emits, with the
// `# ` prefix removed. The user can then edit per-cluster fields via
// the per-cluster overrides editor.
//
// Idempotency rules:
//
//   - If the cluster's file already has an `<addon>:` stanza with
//     non-`enabled` fields under it, do NOT touch it. The seeder only
//     fires on initial enable, and only when the addon's stanza is
//     either absent or contains only `enabled: true/false`.
//
//   - Other addons' stanzas in the cluster file are never touched.
//
//   - Existing per-cluster files with no template carry on — the seeder
//     simply has nothing to add and returns ok=false.

package orchestrator

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// seedPerClusterTemplate looks up the global values file for `addonName`
// in the user's repo, parses the trailing per-cluster template block,
// and returns a new cluster YAML with the template fields seeded under
// the addon's stanza. Returns (nil, false) when no seeding is needed
// (template absent, stanza already populated, or any read failure —
// best-effort).
//
// `existingValues` is the cluster file contents BEFORE generateClusterValues
// rewrote it; we use it to detect "stanza already has user fields" so we
// can skip seeding without re-reading the file.
//
// `regenerated` is the freshly-written cluster YAML (output of
// generateClusterValues) — the seeder appends its template under the
// addon stanza inside this body.
func (o *Orchestrator) seedPerClusterTemplate(ctx context.Context, clusterName, addonName string, existingValues, regenerated []byte) ([]byte, bool) {
	// Step 1: bail out if the existing cluster file already has fields
	// for this addon beyond the `enabled:` boolean. We never overwrite
	// user-authored values.
	if hasAddonStanzaUserFields(existingValues, addonName) {
		return nil, false
	}

	// Step 2: read the global values file's template block.
	dir := strings.TrimSuffix(o.paths.GlobalValues, "/")
	if dir == "" {
		dir = "configuration/addons-global-values"
	}
	globalPath := path.Join(dir, addonName+".yaml")
	globalContent, err := o.git.GetFileContent(ctx, globalPath, o.gitops.BaseBranch)
	if err != nil || len(globalContent) == 0 {
		return nil, false
	}
	templateLeaves := ExtractClusterTemplateLeaves(globalContent, addonName)
	if len(templateLeaves) == 0 {
		return nil, false
	}

	// Step 3: merge the template leaves into the addon's stanza of the
	// regenerated file. We use yaml.Marshal here because the addon
	// stanza is small and we need to inject nested keys reliably; the
	// rest of the file is the deterministic generateClusterValues
	// output, which round-trips cleanly through yaml.
	merged, err := injectTemplateLeaves(regenerated, addonName, templateLeaves)
	if err != nil {
		return nil, false
	}
	return merged, true
}

// hasAddonStanzaUserFields scans the cluster YAML for an `<addon>:` block
// and returns true when the block contains any key other than `enabled`.
// We use a textual scan to stay aligned with how `extractAddonsFromValues`
// reads the same file — both functions have to be tolerant of the
// generated layout (no anchors, simple two-space indent).
func hasAddonStanzaUserFields(clusterYAML []byte, addonName string) bool {
	if len(clusterYAML) == 0 {
		return false
	}
	root := map[string]interface{}{}
	if err := yaml.Unmarshal(clusterYAML, &root); err != nil {
		return false
	}
	section, ok := root[addonName].(map[string]interface{})
	if !ok {
		return false
	}
	for k := range section {
		if k != "enabled" {
			return true
		}
	}
	return false
}

// ExtractClusterTemplateLeaves parses the trailing per-cluster template
// block of a generated global values file and returns the leaf paths it
// declares (without the addon-name prefix). Exported for tests.
//
// Recognizes both the rendered formats:
//   - `#   "ingress.host": <set per cluster>`  (nested via dotted-key)
//   - `#   replicaCount: <set per cluster>`    (top-level scalar)
//
// The function tolerates legacy files without the template marker by
// returning an empty slice.
func ExtractClusterTemplateLeaves(globalYAML []byte, addonName string) []string {
	const marker = "# --- per-cluster overrides template ---"
	idx := strings.Index(string(globalYAML), marker)
	if idx == -1 {
		return nil
	}
	body := string(globalYAML[idx:])
	prefix := "#   "

	out := []string{}
	for _, raw := range strings.Split(body, "\n") {
		line := raw
		// Skip the marker, the "Copy under …" hint, the addon-name
		// header, and any blank lines.
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		rest := strings.TrimPrefix(line, prefix)
		// Strip trailing whitespace.
		rest = strings.TrimRight(rest, " \t\r")
		if rest == "" {
			continue
		}
		// Lines look like one of:
		//   "ingress.host": <set per cluster>
		//   replicaCount: <set per cluster>
		colonIdx := strings.LastIndex(rest, ":")
		if colonIdx == -1 {
			continue
		}
		key := strings.TrimSpace(rest[:colonIdx])
		// Strip wrapping quotes (we render dotted keys with %q).
		if len(key) >= 2 && key[0] == '"' && key[len(key)-1] == '"' {
			key = key[1 : len(key)-1]
		}
		if key == "" {
			continue
		}
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// injectTemplateLeaves takes the cluster YAML and records that the addon
// is enabled, then appends the addon's overridable fields as a trailing
// COMMENT block (NOT live values). It:
//  1. Parses the cluster YAML into a generic map.
//  2. Ensures `<addonName>:` exists; if it doesn't, creates it with
//     `enabled: true` (the EnableAddon path called us, so the addon
//     is necessarily being enabled). NO placeholder leaves are written
//     as live values.
//  3. Re-serializes the map back to YAML.
//  4. Appends a commented "here's what you can override" hint block
//     mirroring the global-values writer (perClusterTemplate), so the
//     file `helm template`s cleanly — a field the chart expects to be a
//     number/object never arrives as the literal text "<set per cluster>".
//
// Why comments instead of live placeholders: the bootstrap appset feeds the
// addon stanza to Helm verbatim (`{{ $addonKey | toYaml }}`). A live
// `"<set per cluster>"` string under a key the chart expects to be typed
// breaks the render or deploys garbage (V2-cleanup-19). The user fills in
// real values by uncommenting the hint lines and editing them.
//
// Yes, this loses comments from the regenerated body above the addon
// stanza. That's acceptable for the cluster overrides file because
// generateClusterValues already emits a fixed, comment-light layout — we
// are not preserving user-authored comments here.
func injectTemplateLeaves(clusterYAML []byte, addonName string, leaves []string) ([]byte, error) {
	root := map[string]interface{}{}
	if len(clusterYAML) > 0 {
		if err := yaml.Unmarshal(clusterYAML, &root); err != nil {
			return nil, fmt.Errorf("parsing cluster YAML: %w", err)
		}
	}
	if root == nil {
		root = map[string]interface{}{}
	}

	section, _ := root[addonName].(map[string]interface{})
	if section == nil {
		section = map[string]interface{}{}
	}
	if _, ok := section["enabled"]; !ok {
		section["enabled"] = true
	}
	root[addonName] = section

	out, err := yaml.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("serializing cluster YAML: %w", err)
	}

	// Append the override hints as a trailing COMMENT block. These are
	// hints only — never live YAML — so the file renders cleanly. Mirrors
	// the global-values writer's perClusterTemplate block.
	hint := renderClusterOverrideHints(addonName, leaves)
	if hint != "" {
		if len(out) > 0 && out[len(out)-1] != '\n' {
			out = append(out, '\n')
		}
		out = append(out, '\n')
		out = append(out, []byte(hint)...)
	}
	return out, nil
}

// renderClusterOverrideHints renders the addon's overridable leaf paths as a
// commented YAML block the user can uncomment and fill in. Returns "" when
// there are no leaves. Mirrors perClusterTemplate (smart_values.go) so the
// hint style is identical to what the global-values file already emits.
func renderClusterOverrideHints(addonName string, leaves []string) string {
	if len(leaves) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# --- per-cluster overrides for ")
	b.WriteString(addonName)
	b.WriteString(" ---\n")
	b.WriteString("# Uncomment and set real values for any field this cluster needs to override.\n")
	b.WriteString("# ")
	b.WriteString(addonName)
	b.WriteString(":\n")
	for _, leaf := range leaves {
		b.WriteString("#   ")
		b.WriteString(renderTemplateLine(leaf))
		b.WriteString("\n")
	}
	return b.String()
}
