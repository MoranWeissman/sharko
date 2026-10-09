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
// "cluster-addons/<cluster-name>.yaml"). Fixed by the format, not configurable:
// the engine chart's own ApplicationSet generator arm hard-codes the same
// literal (charts/sharko-engine/templates/appset.yaml), so a configurable
// override here would let a repo silently stop matching what the engine
// reads. Kept in lockstep with orchestrator.V4ClustersDir — clusterreconciler
// cannot import orchestrator, the same dependency boundary V4ManagedClustersPath
// already lives with.
const v4ClustersDir = "cluster-addons"

// v4ClusterFilePath is the in-repo path of one cluster's assignment file.
// The name is the format's convention (design doc §2.1) and the engine
// chart depends on it, so deriving it from the cluster name is safe.
func v4ClusterFilePath(cluster string) string {
	return path.Join(v4ClustersDir, cluster+".yaml")
}

// v4Assignments is everything one reconcile tick managed to learn from a v4
// repo's cluster-addons/ folder. It is deliberately more than a plain map: the
// reconciler now WRITES these labels on a normal tick (no opt-in setting
// gates it any more), so "I read zero addons for this cluster" and "I could
// not read this cluster's file at all" must not look the same. The first
// means the cluster runs nothing and its stale labels should go; the second
// means Sharko does not know, and touching the live labels would silently
// undeploy the cluster's addons over a git hiccup or a YAML typo.
//
// A nil *v4Assignments means "not a v4 repo" — every method is nil-safe and
// the v3 path never allocates one.
type v4Assignments struct {
	// labels maps cluster name -> the addon labels derived from that
	// cluster's cluster-addons/<name>.yaml. Only enabled addons get a key.
	labels map[string]map[string]string

	// unknown holds the clusters whose assignment file was listed but could
	// not be read, parsed, or did not name a cluster. Their desired addon
	// set is UNKNOWN this tick, which is not the same as empty.
	unknown map[string]struct{}

	// listFailed is true when the cluster-addons/ listing itself failed for a
	// reason other than "the folder does not exist" — no cluster's desired
	// addon set is known this tick.
	listFailed bool
}

// labelsFor returns the addon labels derived for one cluster, or nil when
// the cluster has no assignment file (which means "runs no addons"). Safe on
// a nil receiver — that is the v3 path, where there is nothing to derive.
func (a *v4Assignments) labelsFor(cluster string) map[string]string {
	if a == nil {
		return nil
	}
	return a.labels[cluster]
}

// desiredKnown reports whether this tick actually knows which addons the
// named cluster should be running. False means Sharko must leave the live
// addon labels alone rather than converge them to a set it could not read.
// A nil receiver (v3 repo) is false — there is no v4 desired set at all.
func (a *v4Assignments) desiredKnown(cluster string) bool {
	if a == nil || a.listFailed {
		return false
	}
	_, bad := a.unknown[cluster]
	return !bad
}

// readV4AddonLabels builds the desired addon-enablement labels for every
// cluster on a v4 repo, straight from cluster-addons/*.yaml.
//
// This is the piece that makes "enable an addon" actually deploy anything
// on a v4 repo. The v4 engine's ApplicationSet selects clusters by
// `addons.sharko.dev/<addon>: enabled` on the ArgoCD cluster Secret, but v4
// registration writes NO labels onto the connection record
// (managed-clusters.yaml's labels block stays empty by design — design doc
// §2.4/D9 moved addon on/off out of it), and EnableAddonV4 writes only
// cluster-addons/<cluster>.yaml. Something has to turn the assignment file into
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
// cluster-addons/ directory (a v4 repo with no clusters registered yet) is not an
// error, it is an empty set. A single unreadable or unparseable assignment
// file is logged and skipped rather than aborting the whole tick — the
// other clusters still converge. The affected cluster is recorded in
// `unknown` so the write paths leave its live labels alone instead of
// wiping them on a "successfully read zero addons" lie; the same goes,
// fleet-wide, for a cluster-addons/ listing that failed outright.
func readV4AddonLabels(ctx context.Context, gp gitprovider.GitProvider, branch string) *v4Assignments {
	log := logging.LoggerFromContext(ctx)
	out := &v4Assignments{
		labels:  make(map[string]map[string]string),
		unknown: make(map[string]struct{}),
	}

	entries, err := gp.ListDirectory(ctx, v4ClustersDir, branch)
	if err != nil {
		if !errors.Is(err, gitprovider.ErrFileNotFound) {
			out.listFailed = true
			log.Warn("[clusterreconciler] could not list the v4 cluster-addons/ folder — no addon labels derived this tick, and none will be changed",
				"dir", v4ClustersDir, "branch", branch, "error", err)
		}
		return out
	}

	for _, name := range entries {
		if !strings.HasSuffix(name, ".yaml") {
			continue // .gitkeep and any non-YAML entry
		}
		filePath := path.Join(v4ClustersDir, name)
		// The file name is the cluster name by the format's convention, so
		// a file we could not open still tells us WHICH cluster to leave
		// alone this tick.
		stem := strings.TrimSuffix(name, ".yaml")
		body, readErr := gp.GetFileContent(ctx, filePath, branch)
		if readErr != nil {
			out.unknown[stem] = struct{}{}
			log.Warn("[clusterreconciler] could not read a v4 cluster assignment file — leaving that cluster's addon labels alone, other clusters still converge",
				"path", filePath, "branch", branch, "error", readErr)
			continue
		}
		spec, parseErr := models.LoadClusterAddons(body)
		if parseErr != nil {
			out.unknown[stem] = struct{}{}
			log.Warn("[clusterreconciler] a v4 cluster assignment file was rejected — leaving that cluster's addon labels alone, other clusters still converge",
				"path", filePath, "error", parseErr)
			continue
		}
		if spec.Cluster == "" {
			out.unknown[stem] = struct{}{}
			log.Warn("[clusterreconciler] a v4 cluster assignment file names no cluster — leaving that cluster's addon labels alone", "path", filePath)
			continue
		}
		out.labels[spec.Cluster] = v4LabelsFor(spec)
	}
	return out
}

// v4LabelsFor turns one ClusterAddons into the addon labels Sharko
// wants on that cluster's ArgoCD Secret. Enabled addons only; the value is
// the canonical models.LabelEnabled, which is the exact literal the engine's
// selector matches on.
//
// Addon names that could not produce a legal Kubernetes label key are
// skipped with no attempt to write them: the v4 write path already refuses
// such a name (internal/orchestrator's checkV4PathSegment), so this only
// fires on a hand-authored file, and a bad name must not be allowed to fail
// the whole cluster's Secret update.
func v4LabelsFor(spec models.ClusterAddonsSpec) map[string]string {
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
// region, anything a person put in managed-clusters.yaml's labels block)
// with the addon labels derived from cluster-addons/<name>.yaml. The derived
// addon labels win on a key collision — cluster-addons/<name>.yaml is the v4
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

// driftTouchesV4AddonKeys reports whether a detected label drift involves at
// least one addons.sharko.dev/ key — added, removed, or with a changed
// value. That is the exact question "does this cluster's ArgoCD Secret
// disagree with cluster-addons/<name>.yaml about which addons run here?", and it
// is what tells the reconciler to converge the labels on this tick instead
// of only reporting the drift.
//
// Everything else (a v3-style bare key, a stray key somebody added by hand)
// stays under the opt-in managed-cluster self-heal setting, exactly as
// before — this predicate is the whole boundary between the two.
func driftTouchesV4AddonKeys(drift *LabelDrift) bool {
	if drift == nil {
		return false
	}
	for _, keys := range [][]string{drift.Added, drift.Removed, drift.Changed} {
		for _, k := range keys {
			if models.IsV4AddonLabelKey(k) {
				return true
			}
		}
	}
	return false
}
