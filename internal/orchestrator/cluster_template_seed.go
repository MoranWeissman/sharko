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
	"bytes"
	"context"
	"fmt"
	"path"
	"regexp"
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

// ─── v4 per-addon cluster-values seeding ────────────────────────────────────
//
// v3's seedPerClusterTemplate/injectTemplateLeaves above merge the hint
// block into a shared per-cluster file that holds every addon under its
// own `<addon>:` stanza. v4's per-cluster values file
// (values/clusters/<cluster>/<addon>.yaml) is already scoped to one
// addon, so there is no stanza to find or inject into — the seeding is
// just "does the global file have a template block, and if so, append
// its hints, unwrapped, under the plain W1 stub header".

// seedV4ClusterValuesStub returns the per-addon cluster values file to
// write for a v4 enable (or add-and-enable combo) request that carries no
// explicit values. When the addon's global values file
// (values/global/<addon>.yaml) exists on the base branch and has a
// per-cluster template block, the stub gets the plain-English W1 header
// PLUS a commented hint for every cluster-specific field the smart-values
// classifier found — so a person editing the file by hand sees exactly
// what to fill in without having to go read the global file first. No
// global file, a global file that is itself a fetch-failed/comment-only
// stub, or a template block with nothing in it, all fall back to the
// plain W1 stub unchanged (clusterValuesStub).
//
// Unlike v3's injectTemplateLeaves, this never writes the fields as live
// YAML values — same reasoning as v3 (V2-cleanup-19): a literal
// "<set per cluster>" string under a key the chart expects to be typed
// breaks the Helm render. The v4 file needs no addon-name stanza wrapper
// either way (it is already scoped to one addon), so the hints are the
// flat dotted/scalar keys straight from ExtractClusterTemplateLeaves.
func (o *Orchestrator) seedV4ClusterValuesStub(ctx context.Context, addonName, clusterName string) []byte {
	globalPath, err := v4GlobalValuesPath(addonName)
	if err != nil {
		return clusterValuesStub(addonName, clusterName)
	}
	globalContent, _ := o.readFileIfExists(ctx, globalPath)
	return seedV4ClusterValuesStubFromGlobal(addonName, clusterName, globalContent)
}

// seedV4ClusterValuesStubFromGlobal is the pure (no-I/O) half of
// seedV4ClusterValuesStub: given the global values file's content already
// in hand, it returns the per-addon cluster values file to write. Split
// out so AddToCatalog's add-and-enable combo can pass the global content
// it JUST generated in the same request (still only in the in-memory
// `files` map, not yet committed to git) instead of re-reading git and
// seeing nothing there yet.
func seedV4ClusterValuesStubFromGlobal(addonName, clusterName string, globalContent []byte) []byte {
	if len(globalContent) == 0 {
		return clusterValuesStub(addonName, clusterName)
	}
	leaves := ExtractClusterTemplateLeaves(globalContent, addonName)
	if len(leaves) == 0 {
		return clusterValuesStub(addonName, clusterName)
	}
	var b bytes.Buffer
	b.Write(clusterValuesStub(addonName, clusterName))
	b.WriteString("\n")
	b.WriteString(renderV4ClusterOverrideHints(leaves, clusterName))
	return b.Bytes()
}

// renderV4ClusterOverrideHints is the v4 counterpart of
// renderClusterOverrideHints: the same commented-hint shape, minus the
// addon-name stanza wrapper — a v4 per-cluster values file already IS
// scoped to one addon, so there is nothing to nest under.
//
// clusterName lets a leaf that IS a known fact (Story FS-1) carry the real
// value instead of the generic placeholder — see renderV4TemplateLine.
func renderV4ClusterOverrideHints(leaves []string, clusterName string) string {
	if len(leaves) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# --- per-cluster overrides ---\n")
	b.WriteString("# Uncomment and set a real value for any field this cluster needs to override.\n")
	for _, leaf := range leaves {
		b.WriteString("# ")
		b.WriteString(renderV4TemplateLine(leaf, clusterName))
		b.WriteString("\n")
	}
	return b.String()
}

// clusterNameFactSuffix is the lowercased final dotted-path segment that
// marks a leaf as carrying the ONE known fact Story FS-1 is scoped to: the
// cluster name itself. Every other cluster-specific leaf (region,
// environment, account, host, ...) stays an unknown — Sharko never
// invents a value it doesn't actually have.
const clusterNameFactSuffix = "clustername"

// yamlAmbiguousScalar matches a value that YAML would parse as a bool,
// null, or number rather than a string if left unquoted — e.g. a cluster
// literally named "true" or "123". models.ResourceNamePattern allows both
// shapes (letters-only "true", digits-only "123"), so the substituted
// fact-hint value needs an explicit quote in that case to stay a string
// once the user uncomments the line.
var yamlAmbiguousScalar = regexp.MustCompile(`(?i)^(true|false|yes|no|on|off|null|~|[0-9]+)$`)

// renderV4TemplateLine is renderTemplateLine's v4 sibling: same output
// shape for every leaf EXCEPT one whose final dotted-path segment is the
// known cluster-name fact, which gets the real cluster name as its value
// instead of "<set per cluster>". Does not touch renderTemplateLine
// itself — that function is shared with v3, which has no such fact to
// fill in.
func renderV4TemplateLine(dottedPath, clusterName string) string {
	segments := strings.Split(dottedPath, ".")
	finalSegment := segments[len(segments)-1]
	if strings.ToLower(finalSegment) != clusterNameFactSuffix {
		return renderTemplateLine(dottedPath)
	}
	value := clusterName
	if yamlAmbiguousScalar.MatchString(clusterName) {
		value = fmt.Sprintf("%q", clusterName)
	}
	if !strings.Contains(dottedPath, ".") {
		return fmt.Sprintf("%s: %s", dottedPath, value)
	}
	return fmt.Sprintf("%q: %s", dottedPath, value)
}
