// Package gitops — cluster-side managed-clusters.yaml mutators.
//
// These use parse-mutate-marshal via models.LoadManagedClusters +
// SaveManagedClusters instead of line-level string manipulation.
// Line-level mutators would have to be indent-aware to handle the
// apiVersion/kind/metadata/spec envelope (cluster entries at indent 4
// under `spec.clusters:`) — parse-mutate-marshal sidesteps that entire
// class of brittleness.
//
// Trade-off: the mutators no longer preserve inline comments, blank-line
// separators, or original key ordering inside cluster entries — the
// yaml.v3 marshaller emits canonical formatting. The schema header
// (models.ManagedClustersSchemaHeader) is always preserved as line 1
// because SaveManagedClusters prepends it on every emit.
//
// Behavioural contract (matches the legacy mutators where possible):
//
//   - AddClusterEntry is idempotent on duplicate name (silent skip) to
//     preserve the adoption-after-partial-failure retry semantics that
//     internal/orchestrator/cluster.go and adopt.go relied on.
//   - RemoveClusterEntry returns an error when the cluster is not found
//     (callers in orchestrator/remove.go and unadopt.go treat the error
//     as a warning, not a fatal — same as before).
//   - EnableAddonLabel / DisableAddonLabel return an error when the
//     cluster is not found (same caller contract as before).
//   - UpdateClusterSecretPath: empty secretPath removes the field;
//     non-empty sets/replaces it. Returns an error when the cluster is
//     not found.
//
// Output bytes always carry the envelope (the canonical Save emission).
// Legacy bare-YAML inputs are accepted on read (back-compat per
// LoadManagedClusters contract) and silently upgraded to the envelope
// on the next emit.
package gitops

import (
	"fmt"

	"github.com/MoranWeissman/sharko/internal/models"
)

// ClusterEntryInput holds the fields for a new managed-clusters.yaml
// entry. Region and SecretPath are optional (zero value means not set).
// Labels use the canonical "enabled"/"disabled" addon vocabulary
// (models.LabelEnabled / models.LabelDisabled) — the orchestrator builds
// this map via models.AddonLabelValue. The schema accepts any label shape.
type ClusterEntryInput struct {
	Name       string
	Region     string            // optional
	SecretPath string            // optional
	Labels     map[string]string // addon labels, e.g. {"cert-manager": "enabled"}
}

// AddClusterEntry adds a new cluster to the managed-clusters.yaml spec.
// Idempotent on duplicate name (silent skip, no error) to preserve the
// retry-after-partial-failure semantics the orchestrator relied on with
// any legacy line-level behavior. Returns the canonical enveloped
// document.
func AddClusterEntry(data []byte, entry ClusterEntryInput) ([]byte, error) {
	spec, err := loadOrBootstrap(data)
	if err != nil {
		return nil, fmt.Errorf("AddClusterEntry: %w", err)
	}

	// Idempotent: silently skip duplicates so retries after partial
	// failures (orchestrator.RegisterCluster's "values file landed but
	// secret creation failed" path) don't surface a misleading error.
	for _, c := range spec.Clusters {
		if c.Name == entry.Name {
			return models.SaveManagedClusters(spec)
		}
	}

	newEntry := models.ManagedClusterEntry{
		Name:       entry.Name,
		Region:     entry.Region,
		SecretPath: entry.SecretPath,
	}
	// Always set Labels to a non-nil map when labels are provided, so
	// the emitted YAML carries `labels: { ... }` rather than `labels: null`.
	// When entry.Labels is nil/empty, omit the field entirely — the
	// reader treats absent labels and `labels: []` and `labels: {}`
	// identically (config.parseLabels returns an empty map for all three).
	if len(entry.Labels) > 0 {
		newEntry.Labels = copyLabels(entry.Labels)
	}
	spec.Clusters = append(spec.Clusters, newEntry)

	return models.SaveManagedClusters(spec)
}

// RemoveClusterEntry removes the named cluster from the spec. Returns an
// error when the cluster is not found (caller contract preserved).
func RemoveClusterEntry(data []byte, name string) ([]byte, error) {
	spec, err := models.LoadManagedClusters(data)
	if err != nil {
		return nil, fmt.Errorf("RemoveClusterEntry: %w", err)
	}

	filtered := spec.Clusters[:0]
	found := false
	for _, c := range spec.Clusters {
		if c.Name == name {
			found = true
			continue
		}
		filtered = append(filtered, c)
	}
	if !found {
		return nil, fmt.Errorf("cluster %q not found in managed-clusters.yaml", name)
	}
	spec.Clusters = filtered

	return models.SaveManagedClusters(spec)
}

// EnableAddonLabel sets addonName to the canonical "enabled" value
// (models.LabelEnabled) in the labels block of the given cluster. This is
// the value the live ArgoCD ApplicationSet selector + GetEnabledAddons
// require for the addon to deploy.
func EnableAddonLabel(data []byte, clusterName, addonName string) ([]byte, error) {
	return setClusterAddonLabel(data, clusterName, addonName, models.LabelEnabled)
}

// DisableAddonLabel sets addonName to the canonical "disabled" value
// (models.LabelDisabled) in the labels block of the given cluster.
func DisableAddonLabel(data []byte, clusterName, addonName string) ([]byte, error) {
	return setClusterAddonLabel(data, clusterName, addonName, models.LabelDisabled)
}

// setClusterAddonLabel is the shared implementation behind Enable / Disable.
// Replaces the legacy setAddonLabel byte-level scanner.
func setClusterAddonLabel(data []byte, clusterName, addonName, value string) ([]byte, error) {
	spec, err := models.LoadManagedClusters(data)
	if err != nil {
		return nil, fmt.Errorf("setClusterAddonLabel: %w", err)
	}

	found := false
	for i := range spec.Clusters {
		if spec.Clusters[i].Name != clusterName {
			continue
		}
		found = true
		labels := normaliseLabels(spec.Clusters[i].Labels)
		labels[addonName] = value
		spec.Clusters[i].Labels = labels
		break
	}
	if !found {
		return nil, fmt.Errorf("cluster %q not found in managed-clusters.yaml", clusterName)
	}

	return models.SaveManagedClusters(spec)
}

// UpdateClusterSecretPath sets or clears the secretPath field for a
// cluster entry. Empty secretPath removes the field (yaml omitempty);
// non-empty sets/replaces it. Returns an error when the cluster is not
// found.
func UpdateClusterSecretPath(data []byte, clusterName, secretPath string) ([]byte, error) {
	spec, err := models.LoadManagedClusters(data)
	if err != nil {
		return nil, fmt.Errorf("UpdateClusterSecretPath: %w", err)
	}

	found := false
	for i := range spec.Clusters {
		if spec.Clusters[i].Name != clusterName {
			continue
		}
		found = true
		spec.Clusters[i].SecretPath = secretPath
		break
	}
	if !found {
		return nil, fmt.Errorf("cluster %q not found in managed-clusters.yaml", clusterName)
	}

	return models.SaveManagedClusters(spec)
}

// loadOrBootstrap parses an existing managed-clusters.yaml body or
// returns an empty spec when the body is empty / whitespace-only.
// SaveManagedClusters will emit the full envelope on save.
func loadOrBootstrap(data []byte) (models.ManagedClustersSpec, error) {
	if len(trimSpace(data)) == 0 {
		return models.ManagedClustersSpec{}, nil
	}
	return models.LoadManagedClusters(data)
}

// trimSpace is a byte-level whitespace check that avoids allocating a
// string just to ask "is this body empty/whitespace?".
func trimSpace(data []byte) []byte {
	start := 0
	end := len(data)
	for start < end && isYAMLSpace(data[start]) {
		start++
	}
	for end > start && isYAMLSpace(data[end-1]) {
		end--
	}
	return data[start:end]
}

func isYAMLSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// normaliseLabels converts the labels-field interface{} (which may be
// nil, a map[string]interface{} from yaml.Unmarshal, a map[string]string,
// or the legacy []interface{} sentinel for empty-array) into a fresh
// map[string]string suitable for in-place mutation + remarshal.
//
// Mirrors internal/config.parseLabels — kept package-local rather than
// importing config because importing config from gitops would invert
// the dependency direction (config already loads via the same models).
func normaliseLabels(raw interface{}) map[string]string {
	if raw == nil {
		return map[string]string{}
	}
	switch v := raw.(type) {
	case map[string]string:
		out := make(map[string]string, len(v))
		for k, val := range v {
			out[k] = val
		}
		return out
	case map[string]interface{}:
		out := make(map[string]string, len(v))
		for k, val := range v {
			out[k] = fmt.Sprintf("%v", val)
		}
		return out
	case []interface{}:
		// Legacy `labels: []` empty-array sentinel.
		return map[string]string{}
	default:
		return map[string]string{}
	}
}

// copyLabels duplicates a map so writes to the returned map cannot
// mutate the caller's. Used by AddClusterEntry to take ownership of
// the caller's label map.
func copyLabels(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
