// Package orchestrator — v4 repo-layout path helpers (v4 Wave 1 Story 4.3).
//
// Like EnginePinPath (enginepin.go), these paths are NOT read from
// RepoPathsConfig / server Helm values — the v4 data-file format fixes
// them (design doc §2.1, §2.2), the same way the engine chart's own
// generator arms hard-code "clusters/<name>.yaml". A per-connection
// override would let a repo silently stop matching what the engine
// actually reads.
package orchestrator

import (
	"context"
	"path"
)

// V4ConnectionsPath is where cluster credentials/registration info lives
// in a v4 repo (design doc §2.4): "fleet/connections.yaml". Same kind
// (ManagedClusters) and shape models.LoadManagedClusters already reads —
// only the path and, per design doc §2.4, the MEANING of the labels field
// change (v4 no longer authors addon on/off keys there; that lives in
// clusters/*.yaml instead — see EnableAddonV4/DisableAddonV4).
const V4ConnectionsPath = "fleet/connections.yaml"

// V4ClustersDir is where one ClusterAssignment file per cluster lives
// (design doc §2.1): "clusters/<cluster-name>.yaml".
const V4ClustersDir = "clusters"

// V4GlobalValuesDir is where addon Helm values that apply to every
// cluster live (design doc §2.2): "values/global/<addon>.yaml".
const V4GlobalValuesDir = "values/global"

// V4ClusterValuesDir is the parent of the per-cluster Helm values tree
// (design doc §2.2): "values/clusters/<cluster>/<addon>.yaml".
const V4ClusterValuesDir = "values/clusters"

// v4ClusterAssignmentPath returns the commit path for a cluster's
// ClusterAssignment file.
func v4ClusterAssignmentPath(clusterName string) string {
	return path.Join(V4ClustersDir, clusterName+".yaml")
}

// v4GlobalValuesPath returns the commit path for an addon's fleet-wide
// values file.
func v4GlobalValuesPath(addonName string) string {
	return path.Join(V4GlobalValuesDir, addonName+".yaml")
}

// v4ClusterValuesPath returns the commit path for an addon's per-cluster
// values override file.
func v4ClusterValuesPath(clusterName, addonName string) string {
	return path.Join(V4ClusterValuesDir, clusterName, addonName+".yaml")
}

// isV4Repo reports whether the connected repo is v4-format, using the
// same probe CheckEnginePin (enginepin.go) and
// AddonService.GetVersionMatrix (internal/service/addon.go) already use:
// the engine pin (EnginePinPath) resolving to non-empty content. "No pin
// found" is the ordinary, non-error "not a v4 repo yet" case — never
// treated as a hard failure here either.
func (o *Orchestrator) isV4Repo(ctx context.Context) bool {
	content, ok := o.readFileIfExists(ctx, EnginePinPath)
	return ok && len(content) > 0
}
