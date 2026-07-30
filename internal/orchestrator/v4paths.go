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
	"fmt"
	"path"
	"strings"

	"github.com/MoranWeissman/sharko/internal/models"
)

// V4ConnectionsPath is where cluster credentials/registration info lives
// in a v4 repo (design doc §2.4): "fleet/connections.yaml". Same kind
// (ManagedClusters) and shape models.LoadManagedClusters already reads —
// only the path and, per design doc §2.4, the MEANING of the labels field
// change (v4 no longer authors addon on/off keys there; that lives in
// clusters/*.yaml instead — see EnableAddonV4/DisableAddonV4).
const V4ConnectionsPath = "fleet/connections.yaml"

// V4ClustersDir is where one ClusterAddons file per cluster lives
// (design doc §2.1): "clusters/<cluster-name>.yaml".
const V4ClustersDir = "clusters"

// V4GlobalValuesDir is where addon Helm values that apply to every
// cluster live (design doc §2.2): "values/global/<addon>.yaml".
const V4GlobalValuesDir = "values/global"

// V4ClusterValuesDir is the parent of the per-cluster Helm values tree
// (design doc §2.2): "values/clusters/<cluster>/<addon>.yaml".
const V4ClusterValuesDir = "values/clusters"

// checkV4PathSegment is the belt-and-braces guard on every cluster/addon
// name that becomes part of a v4 commit path.
//
// The request edge (internal/api) already rejects anything that does not
// match models.ResourceNamePattern, and this is the second, independent
// check that runs no matter how the orchestrator was called (CLI, a future
// caller, a test). It exists because path.Join CLEANS its result: joining
// "clusters" with "../../engine/application.yaml" quietly yields
// "engine/application.yaml", so an unchecked name here would let a caller
// rewrite the engine pin — or any other file in the repo — through what
// looks like an ordinary enable-addon request. Go 1.22's ServeMux hands a
// URL-encoded "..%2F" to PathValue already decoded, so the traversal never
// has to survive a router to reach us.
//
// kind is the word used in the message ("cluster" / "addon") so the caller
// sees which of the two names was wrong.
func checkV4PathSegment(kind, name string) error {
	if name == "" {
		return fmt.Errorf("%s name is required", kind)
	}
	if models.LooksLikePathSegmentEscape(name) {
		return fmt.Errorf("invalid %s name %q: a name cannot contain a slash, a backslash, or \"..\"", kind, name)
	}
	if !models.IsValidResourceName(name) {
		return fmt.Errorf("invalid %s name %q: %s", kind, name, models.InvalidResourceNameMessage)
	}
	return nil
}

// joinUnder joins rest onto dir and asserts the CLEANED result is still
// inside dir. The name check above already makes an escape impossible; this
// asserts it on the computed string too, so the invariant is a property of
// the path builder rather than of a check somewhere else in the file.
func joinUnder(dir string, rest ...string) (string, error) {
	joined := path.Join(append([]string{dir}, rest...)...)
	cleanDir := path.Clean(dir)
	if !strings.HasPrefix(joined, cleanDir+"/") {
		return "", fmt.Errorf("refusing to write %q: the computed path is outside %s/", joined, cleanDir)
	}
	return joined, nil
}

// v4ClusterAddonsPath returns the commit path for a cluster's
// ClusterAddons file. Errors on a name that could escape V4ClustersDir.
func v4ClusterAddonsPath(clusterName string) (string, error) {
	if err := checkV4PathSegment("cluster", clusterName); err != nil {
		return "", err
	}
	return joinUnder(V4ClustersDir, clusterName+".yaml")
}

// v4GlobalValuesPath returns the commit path for an addon's fleet-wide
// values file. Errors on a name that could escape V4GlobalValuesDir.
func v4GlobalValuesPath(addonName string) (string, error) {
	if err := checkV4PathSegment("addon", addonName); err != nil {
		return "", err
	}
	return joinUnder(V4GlobalValuesDir, addonName+".yaml")
}

// v4ClusterValuesPath returns the commit path for an addon's per-cluster
// values override file. Errors on either name being able to escape
// V4ClusterValuesDir.
func v4ClusterValuesPath(clusterName, addonName string) (string, error) {
	if err := checkV4PathSegment("cluster", clusterName); err != nil {
		return "", err
	}
	if err := checkV4PathSegment("addon", addonName); err != nil {
		return "", err
	}
	return joinUnder(V4ClusterValuesDir, clusterName, addonName+".yaml")
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
