package clusterreconciler

import (
	"context"
	"errors"
	"path"
	"strings"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/logging"
	"github.com/MoranWeissman/sharko/internal/models"
)

// v4ClustersDir is the v4 data-file format's per-cluster assignment folder
// (design doc docs/design/2026-07-30-v4-data-file-format.md §2.1 —
// "clusters/<cluster-name>.yaml"). Fixed by the format, not configurable:
// the engine chart's own ApplicationSet generator arm hard-codes the same
// literal (charts/sharko-engine/templates/appset.yaml), so a configurable
// override here would let a repo silently stop matching what the engine
// reads. Kept in lockstep with orchestrator.V4ClustersDir — clusterreconciler
// cannot import orchestrator, the same dependency boundary v4ConnectionsPath
// already lives with.
const v4ClustersDir = "clusters"

// readV4AddonLabels builds the desired addon-enablement labels for every
// cluster on a v4 repo, straight from clusters/*.yaml.
//
// This is the piece that makes "enable an addon" actually deploy anything
// on a v4 repo. The v4 engine's ApplicationSet selects clusters by
// `addons.sharko.dev/<addon>: enabled` on the ArgoCD cluster Secret, but v4
// registration writes NO labels onto the connection record
// (fleet/connections.yaml's labels block stays empty by design — design doc
// §2.4/D9 moved addon on/off out of it), and EnableAddonV4 writes only
// clusters/<cluster>.yaml. Something has to turn the assignment file into
// the label the engine matches on; that something is this reconciler, which
// is already the single writer of those labels for v3.
//
// Shape of the result: cluster name -> label map. ONLY enabled addons
// produce a key. An addon whose entry says enabled:false, and an addon with
// no entry at all, are both simply absent — that absence is what lets the
// convergence paths (SyncManagedClusterLabels' full convergence,
// SyncLabelsOnly's stale-v4 prune) actually REMOVE the label when somebody
// disables the addon.
//
// Failure stance matches the rest of pollOnce's read step: a missing
// clusters/ directory (a v4 repo with no clusters registered yet) is not an
// error, it is an empty map. A single unreadable or unparseable assignment
// file is logged and skipped rather than aborting the whole tick — the
// other clusters still converge, and the affected cluster keeps whatever
// labels it already has rather than having them all wiped by a
// "successfully read zero addons" lie.
func readV4AddonLabels(ctx context.Context, gp gitprovider.GitProvider, branch string) map[string]map[string]string {
	log := logging.LoggerFromContext(ctx)
	out := make(map[string]map[string]string)

	entries, err := gp.ListDirectory(ctx, v4ClustersDir, branch)
	if err != nil {
		if !errors.Is(err, gitprovider.ErrFileNotFound) {
			log.Warn("[clusterreconciler] could not list the v4 clusters/ folder — no addon labels derived this tick",
				"dir", v4ClustersDir, "branch", branch, "error", err)
		}
		return out
	}

	for _, name := range entries {
		if !strings.HasSuffix(name, ".yaml") {
			continue // .gitkeep and any non-YAML entry
		}
		filePath := path.Join(v4ClustersDir, name)
		body, readErr := gp.GetFileContent(ctx, filePath, branch)
		if readErr != nil {
			log.Warn("[clusterreconciler] could not read a v4 cluster assignment file — skipping it, other clusters still converge",
				"path", filePath, "branch", branch, "error", readErr)
			continue
		}
		spec, parseErr := models.LoadClusterAssignment(body)
		if parseErr != nil {
			log.Warn("[clusterreconciler] a v4 cluster assignment file was rejected — skipping it, other clusters still converge",
				"path", filePath, "error", parseErr)
			continue
		}
		if spec.Cluster == "" {
			log.Warn("[clusterreconciler] a v4 cluster assignment file names no cluster — skipping it", "path", filePath)
			continue
		}
		out[spec.Cluster] = v4LabelsFor(spec)
	}
	return out
}

// v4LabelsFor turns one ClusterAssignment into the addon labels Sharko
// wants on that cluster's ArgoCD Secret. Enabled addons only; the value is
// the canonical models.LabelEnabled, which is the exact literal the engine's
// selector matches on.
//
// Addon names that could not produce a legal Kubernetes label key are
// skipped with no attempt to write them: the v4 write path already refuses
// such a name (internal/orchestrator's checkV4PathSegment), so this only
// fires on a hand-authored file, and a bad name must not be allowed to fail
// the whole cluster's Secret update.
func v4LabelsFor(spec models.ClusterAssignmentSpec) map[string]string {
	labels := make(map[string]string, len(spec.Addons))
	for addon, entry := range spec.Addons {
		if !entry.Enabled {
			continue
		}
		if !models.IsValidResourceName(addon) {
			continue
		}
		labels[models.V4AddonLabelKey(addon)] = models.LabelEnabled
	}
	return labels
}

// mergeV4AddonLabels combines the connection record's own labels (env,
// region, anything a person put in fleet/connections.yaml's labels block)
// with the addon labels derived from clusters/<name>.yaml. The derived
// addon labels win on a key collision — clusters/<name>.yaml is the v4
// source of truth for addon on/off, and nothing else is allowed to
// contradict it.
//
// v4Labels may be nil (a v3 repo, or a v4 cluster with no assignment file
// yet), in which case the base map is returned unchanged — which is what
// keeps the v3 path byte-identical.
func mergeV4AddonLabels(base, v4Labels map[string]string) map[string]string {
	if len(v4Labels) == 0 {
		return base
	}
	merged := make(map[string]string, len(base)+len(v4Labels))
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range v4Labels {
		merged[k] = v
	}
	return merged
}

// hasStaleV4AddonLabels reports whether the live Secret carries an
// addons.sharko.dev/ key that the desired set no longer declares.
//
// It exists because labelsMatch is a SUBSET check — it asks "is everything
// git wants present?", which is the right question for v3 (where turning an
// addon off writes `<addon>: disabled`, a value change labelsMatch catches)
// but the wrong one for v4 (where turning an addon off REMOVES the key, and
// a subset check happily reports in-sync while the stale `enabled` label
// keeps the addon deployed). Only consulted on the v4 path, so v3's in-sync
// decision is untouched.
func hasStaleV4AddonLabels(desired, have map[string]string) bool {
	for k := range have {
		if !models.IsV4AddonLabelKey(k) {
			continue
		}
		if _, want := desired[k]; !want {
			return true
		}
	}
	return false
}
