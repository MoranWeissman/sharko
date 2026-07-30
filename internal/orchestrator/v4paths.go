// Package orchestrator — v4 repo-layout path helpers (v4 Wave 1 Story 4.3).
//
// Like EnginePinPath (enginepin.go), these paths are NOT read from
// RepoPathsConfig / server Helm values — the v4 data-file format fixes
// them (design doc §2.1, §2.2), the same way the engine chart's own
// generator arms hard-code "clusters/<name>.yaml". A per-connection
// override would let a repo silently stop matching what the engine
// actually reads.
package orchestrator

import "path"

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
