// Package orchestrator — values editor commit helpers (v1.20).
//
// These functions are the "Write a single YAML file and open a PR" primitives
// used by the values-editor API endpoints. They are intentionally thin —
// validation (schema, YAML parse) happens at the API layer; this layer just
// touches Git.
//
// Both helpers reuse commitChanges so they pick up the standard branch naming,
// PR creation, optional auto-merge, and the shared Git mutex. They also
// inherit CommitAttribution from ctx (set by Server.GitProviderForTier).

package orchestrator

import (
	"bytes"
	"context"
	"fmt"
	"path"
	"strings"

	"gopkg.in/yaml.v3"
)

// SetGlobalAddonValues writes the full default values YAML for an addon at
// the global scope and opens a PR. The caller must pass the full file
// contents — this is intentional: a values file is the source of truth and
// we want the diff in the PR to reflect exactly what the user typed.
//
// The path written is `<paths.GlobalValues>/<addonName>.yaml`. The
// content is validated as parseable YAML before any Git activity happens.
func (o *Orchestrator) SetGlobalAddonValues(ctx context.Context, addonName, valuesYAML string, autoMerge *bool) (*GitResult, error) {
	return o.SetGlobalAddonValuesWithOp(ctx, addonName, valuesYAML, "values-edit", "", autoMerge, false)
}

// SetGlobalAddonValuesWithOp is the tracking-aware variant of
// SetGlobalAddonValues. The opCode argument controls which dashboard
// PR-panel filter chip the resulting PR lands under — handlers that
// invoke the values writer for non-edit reasons (e.g. AI annotate, AI
// opt-out toggle) should pass their own canonical code.
//
// titleOverride lets callers provide a more specific PR title than the
// default "Update global values for X" — empty string keeps the default.
//
// dryRun, when true, returns a preview of what would be committed (no side effects).
func (o *Orchestrator) SetGlobalAddonValuesWithOp(ctx context.Context, addonName, valuesYAML, opCode, titleOverride string, autoMerge *bool, dryRun bool) (*GitResult, error) {
	if addonName == "" {
		return nil, fmt.Errorf("addon name is required")
	}
	if err := validateYAML(valuesYAML); err != nil {
		return nil, fmt.Errorf("invalid YAML: %w", err)
	}

	dir := strings.TrimSuffix(o.paths.GlobalValues, "/")
	if dir == "" {
		dir = "configuration/addons-global-values"
	}
	filePath := path.Join(dir, addonName+".yaml")

	title := titleOverride
	if title == "" {
		title = fmt.Sprintf("Update global values for %s", addonName)
	}

	// Dry-run exit point: return a preview with no side effects.
	if dryRun {
		action := o.fileAction(ctx, filePath)
		oldContent, _ := o.readFileIfExists(ctx, filePath)
		newContent := []byte(valuesYAML)
		diff := o.buildFileDiff(filePath, oldContent, newContent, action)
		return &GitResult{
			DryRun: &DryRunResult{
				EffectiveAddons: []string{addonName},
				FilesToWrite:    []FilePreview{{Path: filePath, Action: action, Diff: diff}},
				PRTitle:         title,
				SecretsToCreate: []string{},
			},
		}, nil
	}

	files := map[string][]byte{filePath: []byte(valuesYAML)}
	op := fmt.Sprintf("update global values for %s", addonName)

	gitResult, err := o.commitChangesWithMeta(ctx, files, nil, op,
		o.prMeta(autoMerge, opCode, title, "", addonName))
	if err != nil {
		return nil, fmt.Errorf("committing values for addon %q: %w", addonName, err)
	}
	gitResult.ValuesFile = filePath
	return gitResult, nil
}

// SetClusterAddonValues replaces the per-cluster overrides for one addon on
// one cluster. The cluster's overrides file is the canonical YAML map
// `<addonName>: { ... }` plus an optional `clusterGlobalValues:` block; this
// function only mutates the addon's section, leaving the rest untouched.
//
// `overridesYAML` is the YAML for the addon's section ONLY (the inner map),
// not the whole file. Pass an empty string to remove the addon's overrides
// (the addon section is deleted from the file). The result of the merge is
// validated before write.
//
// dryRun, when true, returns a preview of what would be committed (no side effects).
func (o *Orchestrator) SetClusterAddonValues(ctx context.Context, clusterName, addonName, overridesYAML string, autoMerge *bool, dryRun bool) (*GitResult, error) {
	if clusterName == "" {
		return nil, fmt.Errorf("cluster name is required")
	}
	if addonName == "" {
		return nil, fmt.Errorf("addon name is required")
	}

	// Referential integrity (V2-cleanup-22, Part 2 / decision #3): writing
	// per-cluster values for an addon that is not in the catalog produces
	// orphan config. We only enforce membership when SETTING values; an
	// empty overridesYAML means "remove this addon's overrides", which must
	// stay allowed as cleanup even after the addon left the catalog. A
	// genuine catalog read failure surfaces (→ 502); an absent addon returns
	// *AddonNotInCatalogError (→ 4xx).
	if strings.TrimSpace(overridesYAML) != "" {
		if _, err := o.requireAddonsInCatalog(ctx, []string{addonName}); err != nil {
			return nil, err
		}
	}

	dir := strings.TrimSuffix(o.paths.ClusterValues, "/")
	if dir == "" {
		dir = "configuration/addons-clusters-values"
	}
	filePath := path.Join(dir, clusterName+".yaml")

	// Read the current cluster file (if present); fall back to an empty map.
	current, err := o.git.GetFileContent(ctx, filePath, o.gitops.BaseBranch)
	if err != nil {
		// File may not exist yet — that's OK, we'll create it.
		current = []byte{}
	}

	merged, err := mergeAddonSection(current, addonName, overridesYAML)
	if err != nil {
		return nil, fmt.Errorf("merging overrides for addon %q on cluster %q: %w", addonName, clusterName, err)
	}

	// Dry-run exit point: return a preview with no side effects.
	if dryRun {
		action := o.fileAction(ctx, filePath)
		diff := o.buildFileDiff(filePath, current, merged, action)
		return &GitResult{
			DryRun: &DryRunResult{
				EffectiveAddons: []string{addonName},
				FilesToWrite:    []FilePreview{{Path: filePath, Action: action, Diff: diff}},
				PRTitle:         fmt.Sprintf("Update %s overrides on cluster %s", addonName, clusterName),
				SecretsToCreate: []string{},
			},
		}, nil
	}

	files := map[string][]byte{filePath: merged}
	op := fmt.Sprintf("update %s overrides on cluster %s", addonName, clusterName)

	gitResult, err := o.commitChangesWithMeta(ctx, files, nil, op,
		o.prMeta(autoMerge, "values-edit", fmt.Sprintf("Update %s overrides on cluster %s", addonName, clusterName), clusterName, addonName))
	if err != nil {
		return nil, fmt.Errorf("committing cluster overrides: %w", err)
	}
	gitResult.ValuesFile = filePath
	return gitResult, nil
}

// CommitFilesAsPR is a thin orchestrator wrapper around commitChanges for
// callers that already have a complete file map ready to write. The
// migration endpoint (v1.21 Bundle 5) uses this to push the unwrapped
// global-values files in a single PR.
//
// The operation string is the human-readable PR title and audit detail —
// it's run through the same branch-name sanitiser as commitChanges.
//
// Backwards-compatible variant — does NOT track the PR. New callers should
// prefer CommitFilesAsPRWithMeta so the resulting PR appears on the
// dashboard panel.
func (o *Orchestrator) CommitFilesAsPR(ctx context.Context, files map[string][]byte, operation string) (*GitResult, error) {
	if len(files) == 0 {
		return nil, fmt.Errorf("no files to commit")
	}
	return o.commitChanges(ctx, files, nil, operation)
}

// CommitFilesAsPRWithMeta is the tracking-aware variant of CommitFilesAsPR.
// The supplied meta drives the dashboard PR-panel filter chip + per-row
// badge.
func (o *Orchestrator) CommitFilesAsPRWithMeta(ctx context.Context, files map[string][]byte, operation string, meta PRMetadata) (*GitResult, error) {
	if len(files) == 0 {
		return nil, fmt.Errorf("no files to commit")
	}
	return o.commitChangesWithMeta(ctx, files, nil, operation, meta)
}

// validateYAML returns nil if s parses as YAML (empty input is allowed).
func validateYAML(s string) error {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var any interface{}
	return yaml.Unmarshal([]byte(s), &any)
}

// mergeAddonSection takes an existing cluster-overrides file, replaces (or
// removes) the section for the named addon, and returns the new YAML bytes.
// It preserves any other top-level keys — `clusterGlobalValues`, other
// addons — AND the file's shape: comments, blank lines, key order, and
// indent. It does this by editing a yaml.Node document in place instead of
// round-tripping through map[string]interface{}, which would flatten all of
// that (V2-cleanup-83.2).
//
// If overridesYAML is empty (or whitespace), the addon's section is deleted.
func mergeAddonSection(existing []byte, addonName, overridesYAML string) ([]byte, error) {
	root, rawLines, err := parseClusterValuesDoc(existing)
	if err != nil {
		return nil, err
	}
	restoreBlankLinePredecessors(root, rawLines)

	idx := findMappingKey(root, addonName)

	if strings.TrimSpace(overridesYAML) == "" {
		// Delete path: drop the key/value pair entirely, leaving every other
		// key, its comments, and its blank-line spacing untouched.
		if idx != -1 {
			root.Content = append(root.Content[:idx], root.Content[idx+2:]...)
		}
	} else {
		var sectionDoc yaml.Node
		if err := yaml.Unmarshal([]byte(overridesYAML), &sectionDoc); err != nil {
			return nil, fmt.Errorf("parsing addon overrides: %w", err)
		}
		if len(sectionDoc.Content) == 0 {
			return nil, fmt.Errorf("parsing addon overrides: empty document")
		}
		valueNode := sectionDoc.Content[0]

		if idx != -1 {
			// Replace only the value node; the key node (and any comment or
			// blank-line spacing attached to it) is left exactly as parsed.
			root.Content[idx+1] = valueNode
		} else {
			keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: addonName}
			root.Content = append(root.Content, keyNode, valueNode)
		}
	}

	doc := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("serializing merged cluster file: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("serializing merged cluster file: %w", err)
	}
	return buf.Bytes(), nil
}

// parseClusterValuesDoc unmarshals existing into a mapping node, tolerating
// an empty or absent file (fresh document, same as today's empty-map
// behavior). It also returns the raw source split into lines, needed by
// restoreBlankLinePredecessors.
func parseClusterValuesDoc(existing []byte) (root *yaml.Node, rawLines []string, err error) {
	if len(bytes.TrimSpace(existing)) == 0 {
		return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}, nil, nil
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(existing, &doc); err != nil {
		return nil, nil, fmt.Errorf("parsing existing cluster file: %w", err)
	}
	if len(doc.Content) == 0 {
		return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}, nil, nil
	}

	root = doc.Content[0]
	if root.Kind != yaml.MappingNode {
		// Existing file wasn't a mapping (e.g. a bare scalar or list) —
		// start a fresh mapping rather than fail; any stray top-of-file
		// comment on the old root is preserved on the new one.
		root = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", HeadComment: root.HeadComment}
	}
	return root, strings.Split(string(existing), "\n"), nil
}

// findMappingKey returns the Content index of the key node matching name in
// a mapping node's Content (which alternates key, value, key, value, ...),
// or -1 if not present.
func findMappingKey(root *yaml.Node, name string) int {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == name {
			return i
		}
	}
	return -1
}

// restoreBlankLinePredecessors re-attaches the "there was a blank line right
// above this key" information that yaml.v3 silently drops on decode (it only
// tracks comment text, not bare blank lines). Without this, every blank line
// separating sections in a hand-written or generator-written values file
// would vanish the first time the file went through an edit.
//
// It works by finding, for each top-level key, the source line the key's
// block (comment + key) started on, and checking whether the line directly
// above that was blank. If so, the key's HeadComment gets a leading "\n" —
// which yaml.v3's encoder renders back out as a blank line before the key.
func restoreBlankLinePredecessors(root *yaml.Node, rawLines []string) {
	if len(rawLines) == 0 {
		return
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		key := root.Content[i]
		startLine := key.Line - headCommentLineCount(key.HeadComment)
		precedingIdx := startLine - 2 // 0-indexed line directly above startLine
		if precedingIdx < 0 || precedingIdx >= len(rawLines) {
			continue
		}
		if strings.TrimSpace(rawLines[precedingIdx]) == "" && !strings.HasPrefix(key.HeadComment, "\n") {
			key.HeadComment = "\n" + key.HeadComment
		}
	}
}

// headCommentLineCount returns how many source lines a HeadComment spans.
func headCommentLineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}
