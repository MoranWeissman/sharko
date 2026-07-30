// Package gitops — v4 cluster-addons mutator (v4 Wave 1 Story 4.3).
//
// clusters/<cluster-name>.yaml (kind ClusterAddons, design doc §2.1) is
// a brand-new v4 kind with no legacy bare-YAML precedent, so — unlike the
// v3 managed-clusters.yaml / addons-catalog.yaml mutators in this package —
// there is no back-compat branch to preserve. This mutator always
// round-trips through models.LoadClusterAddons / SaveClusterAddons
// (parse-mutate-marshal), which means every value it produces is already
// JSON-Schema-validated before the bytes ever reach a commit — the "generated
// files pass sharko validate" guarantee Story 4.3's gate requires falls out
// of reusing that reader/writer pair rather than needing separate proof.
package gitops

import (
	"fmt"

	"github.com/MoranWeissman/sharko/internal/models"
)

// SetClusterAddonsAddon upserts one addon entry in a
// clusters/<cluster-name>.yaml document: always sets Enabled, and
// conditionally touches Version and Settings — both are pointers so "leave
// as-is" is distinguishable from "set to empty/zero":
//
//   - version == nil   → leave the existing pin (if any) untouched. This is
//     what a plain enable/disable that doesn't mention a version uses —
//     design doc §2.1: "false keeps the entry — and its settings — but
//     stops deploying" means disabling must NOT silently clear a pin.
//   - version != nil, *version == ""  → explicitly clear the pin (the
//     design doc's own upgrade-PR worked example, §6: removing the
//     `version:` line makes the cluster follow the catalog again).
//   - version != nil, *version != ""  → set/replace the pin.
//
// settings follows the same nil-means-untouched rule, but replaces
// wholesale when non-nil (never a partial patch) — same "list fields
// replace whole" precedent as the rest of the v4 format.
//
// data may be empty (new cluster, no assignment file yet) — the document
// is bootstrapped with spec.cluster = clusterName and an empty addons map,
// matching design doc §2.1's "may be an empty map ({})". clusterName is
// REQUIRED even when data is non-empty, both to seed a fresh document and
// to catch a caller accidentally mutating the wrong file — this function
// only writes spec.addons; it never touches spec.cluster on an existing
// document (a filename/spec.cluster mismatch is sharko validate's job to
// catch, not this mutator's to silently paper over).
func SetClusterAddonsAddon(
	data []byte,
	clusterName, addonName string,
	enabled bool,
	version *string,
	settings *models.ClusterAddonsAddonSettings,
) ([]byte, error) {
	spec, err := loadOrBootstrapClusterAddons(data, clusterName)
	if err != nil {
		return nil, fmt.Errorf("SetClusterAddonsAddon: %w", err)
	}

	if spec.Addons == nil {
		spec.Addons = make(map[string]models.ClusterAddonsAddon)
	}
	entry := spec.Addons[addonName]
	entry.Enabled = enabled
	if version != nil {
		entry.Version = *version
	}
	if settings != nil {
		entry.Settings = settings
	}
	spec.Addons[addonName] = entry

	return models.SaveClusterAddons(spec)
}

// RemoveClusterAddonsAddon deletes addonName's entry entirely from a
// clusters/<cluster-name>.yaml document. Unlike SetClusterAddonsAddon
// with enabled=false (which KEEPS the entry — design doc §2.1: "false
// keeps the entry — and its settings — but stops deploying"), this drops
// the block completely. Used by the v3-parity "cleanup=all" removal path,
// never by ordinary disable. Returns an error if the addon has no entry —
// mirrors the not-found contract of the v3 cluster/catalog mutators in
// this package.
func RemoveClusterAddonsAddon(data []byte, clusterName, addonName string) ([]byte, error) {
	spec, err := models.LoadClusterAddons(data)
	if err != nil {
		return nil, fmt.Errorf("RemoveClusterAddonsAddon: %w", err)
	}
	if _, ok := spec.Addons[addonName]; !ok {
		return nil, fmt.Errorf("addon %q not found in clusters/%s.yaml", addonName, clusterName)
	}
	delete(spec.Addons, addonName)
	return models.SaveClusterAddons(spec)
}

// loadOrBootstrapClusterAddons parses an existing clusters/<name>.yaml
// body, or — when data is empty/whitespace-only — returns a fresh spec
// seeded with spec.cluster = clusterName and an empty addons map. Mirrors
// loadOrBootstrap (yaml_mutator_cluster.go) for the v3 managed-clusters
// file, adapted to the v4 kind's required spec.cluster field.
func loadOrBootstrapClusterAddons(data []byte, clusterName string) (models.ClusterAddonsSpec, error) {
	if len(trimSpace(data)) == 0 {
		return models.ClusterAddonsSpec{
			Cluster: clusterName,
			Addons:  map[string]models.ClusterAddonsAddon{},
		}, nil
	}
	return models.LoadClusterAddons(data)
}
