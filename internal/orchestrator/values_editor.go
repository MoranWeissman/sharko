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
func (o *Orchestrator) SetGlobalAddonValues(ctx context.Context, addonName, valuesYAML string) (*GitResult, error) {
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

	files := map[string][]byte{filePath: []byte(valuesYAML)}
	op := fmt.Sprintf("update global values for %s", addonName)

	gitResult, err := o.commitChanges(ctx, files, nil, op)
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
func (o *Orchestrator) SetClusterAddonValues(ctx context.Context, clusterName, addonName, overridesYAML string) (*GitResult, error) {
	if clusterName == "" {
		return nil, fmt.Errorf("cluster name is required")
	}
	if addonName == "" {
		return nil, fmt.Errorf("addon name is required")
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

	files := map[string][]byte{filePath: merged}
	op := fmt.Sprintf("update %s overrides on cluster %s", addonName, clusterName)

	gitResult, err := o.commitChanges(ctx, files, nil, op)
	if err != nil {
		return nil, fmt.Errorf("committing cluster overrides: %w", err)
	}
	gitResult.ValuesFile = filePath
	return gitResult, nil
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
// addons — by round-tripping through map[string]interface{}.
//
// If overridesYAML is empty (or whitespace), the addon's section is deleted.
func mergeAddonSection(existing []byte, addonName, overridesYAML string) ([]byte, error) {
	root := map[string]interface{}{}
	if len(existing) > 0 {
		if err := yaml.Unmarshal(existing, &root); err != nil {
			return nil, fmt.Errorf("parsing existing cluster file: %w", err)
		}
		if root == nil {
			root = map[string]interface{}{}
		}
	}

	if strings.TrimSpace(overridesYAML) == "" {
		delete(root, addonName)
	} else {
		var section interface{}
		if err := yaml.Unmarshal([]byte(overridesYAML), &section); err != nil {
			return nil, fmt.Errorf("parsing addon overrides: %w", err)
		}
		root[addonName] = section
	}

	out, err := yaml.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("serializing merged cluster file: %w", err)
	}
	return out, nil
}
