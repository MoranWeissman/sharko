package helm

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// DiffType represents the type of value change between versions.
type DiffType string

const (
	DiffAdded   DiffType = "added"
	DiffRemoved DiffType = "removed"
	DiffChanged DiffType = "changed"
)

// ValueDiff represents a single difference in values.yaml between two chart versions.
type ValueDiff struct {
	Path     string   `json:"path"`
	Type     DiffType `json:"type"`
	OldValue string   `json:"old_value,omitempty"`
	NewValue string   `json:"new_value,omitempty"`
}

// ConflictEntry represents a conflict between a configured value and a changed default.
type ConflictEntry struct {
	Path            string `json:"path"`
	ConfiguredValue string `json:"configured_value"`
	OldDefault      string `json:"old_default"`
	NewDefault      string `json:"new_default"`
	Source          string `json:"source"` // "global" or cluster name
}

// UpgradeAnalysis holds the full upgrade impact analysis results.
type UpgradeAnalysis struct {
	AddonName      string          `json:"addon_name"`
	CurrentVersion string          `json:"current_version"`
	TargetVersion  string          `json:"target_version"`
	TotalChanges   int             `json:"total_changes"`
	Added          []ValueDiff     `json:"added"`
	Removed        []ValueDiff     `json:"removed"`
	Changed        []ValueDiff     `json:"changed"`
	Conflicts      []ConflictEntry `json:"conflicts"`
}

// DiffValues compares two YAML strings and returns the differences.
func DiffValues(oldYAML, newYAML string) (added, removed, changed []ValueDiff, err error) {
	var oldMap, newMap map[string]interface{}

	if err := yaml.Unmarshal([]byte(oldYAML), &oldMap); err != nil {
		return nil, nil, nil, fmt.Errorf("parsing old YAML: %w", err)
	}
	if err := yaml.Unmarshal([]byte(newYAML), &newMap); err != nil {
		return nil, nil, nil, fmt.Errorf("parsing new YAML: %w", err)
	}

	flatOld := flatten("", oldMap)
	flatNew := flatten("", newMap)

	// Find added and changed
	for path, newVal := range flatNew {
		oldVal, exists := flatOld[path]
		if !exists {
			added = append(added, ValueDiff{Path: path, Type: DiffAdded, NewValue: newVal})
		} else if oldVal != newVal {
			changed = append(changed, ValueDiff{Path: path, Type: DiffChanged, OldValue: oldVal, NewValue: newVal})
		}
	}

	// Find removed
	for path, oldVal := range flatOld {
		if _, exists := flatNew[path]; !exists {
			removed = append(removed, ValueDiff{Path: path, Type: DiffRemoved, OldValue: oldVal})
		}
	}

	return added, removed, changed, nil
}

// FindConflicts checks if any configured values conflict with changes between old and new defaults.
func FindConflicts(configYAML, oldDefaultYAML, newDefaultYAML string) ([]ConflictEntry, error) {
	var configMap map[string]interface{}
	if err := yaml.Unmarshal([]byte(configYAML), &configMap); err != nil {
		return nil, fmt.Errorf("parsing config YAML: %w", err)
	}

	_, _, changed, err := DiffValues(oldDefaultYAML, newDefaultYAML)
	if err != nil {
		return nil, err
	}

	flatConfig := flatten("", configMap)

	var conflicts []ConflictEntry
	for _, diff := range changed {
		if configVal, exists := flatConfig[diff.Path]; exists {
			conflicts = append(conflicts, ConflictEntry{
				Path:            diff.Path,
				ConfiguredValue: configVal,
				OldDefault:      diff.OldValue,
				NewDefault:      diff.NewValue,
			})
		}
	}

	return conflicts, nil
}

// flatten converts a nested map to flat dot-separated key paths.
func flatten(prefix string, m map[string]interface{}) map[string]string {
	result := make(map[string]string)
	for k, v := range m {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		switch val := v.(type) {
		case map[string]interface{}:
			for subK, subV := range flatten(path, val) {
				result[subK] = subV
			}
		default:
			result[path] = fmt.Sprintf("%v", val)
		}
	}
	return result
}
