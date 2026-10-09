// Package orchestrator — v4 repo-layout path helpers (v4 Wave 1 Story 4.3).
//
// Like EnginePinPath (enginepin.go), these paths are NOT read from
// RepoPathsConfig / server Helm values — the v4 data-file format fixes
// them (design doc §2.1, §2.2), the same way the engine chart's own
// generator arms hard-code "cluster-addons/<name>.yaml". A per-connection
// override would let a repo silently stop matching what the engine
// actually reads.
package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
)

// V4ManagedClustersPath is where the list of clusters Sharko manages lives
// in a v4 repo: a root file called "managed-clusters.yaml". Same kind
// (ManagedClusters) and shape models.LoadManagedClusters already reads —
// only the path and the MEANING of the labels field change (v4 no longer
// authors addon on/off keys there; that lives in cluster-addons/*.yaml instead —
// see EnableAddonV4/DisableAddonV4).
//
// It used to be fleet/connections.yaml. Two things changed (design doc
// 2026-07-31-catalog-approved-model.md §10): the "fleet" folder went away
// because one file does not need a folder, and the word "connections" was
// given up because it already meant something else — Settings, Connections
// is where Sharko's OWN credentials for git, ArgoCD and vault live, which
// are server state and never go in the repo. One name per idea now: the
// file managed-clusters.yaml holds kind ManagedClusters and the UI page
// that shows it is called Managed Clusters.
//
// This file, catalog.yaml and sharko-engine.yaml are ROOT files. They are
// fixed, complete paths — no name from a request is ever joined onto them —
// so they never go near checkV4PathSegment/joinUnder. Those guard the paths
// that ARE built from user input: cluster-addons/<name>.yaml and the values
// tree.
const V4ManagedClustersPath = "managed-clusters.yaml"

// V4ClustersDir is where one ClusterAddons file per cluster lives
// (design doc §2.1): "cluster-addons/<cluster-name>.yaml". Named
// "cluster-addons" (not "clusters") because the files inside are per-cluster
// ADDON ASSIGNMENTS, not cluster records — those live in
// managed-clusters.yaml (v4 naming polish).
const V4ClustersDir = "cluster-addons"

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
// "cluster-addons" with "../../sharko-engine.yaml" quietly yields
// "sharko-engine.yaml", so an unchecked name here would let a caller
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

// V4GlobalValuesPath is the exported form of v4GlobalValuesPath, for
// API-layer callers (the AI annotate endpoints) that need the v4 commit
// path before they have an *Orchestrator in hand. Same validation, same
// error.
func V4GlobalValuesPath(addonName string) (string, error) {
	return v4GlobalValuesPath(addonName)
}

// ChartValuesFetchResult is what a ChartValuesFetcherFn returns: the
// upstream chart values.yaml bytes, plus whatever AI annotation the
// caller's wiring already ran on them. AddToCatalog itself makes no AI
// call — the fetcher wiring (internal/api) is where the existing
// AnnotateValues call site lives, exactly as it does for the v3 AddAddon
// door; see catalog_org.go's wiring of this type for the reasoning.
type ChartValuesFetchResult struct {
	UpstreamValues            []byte
	AIAnnotated               bool
	ExtraClusterSpecificPaths []string
}

// ChartValuesFetcherFn resolves a chart's official values.yaml for a
// catalog entry the moment its final chart+version are known.
//
// AddToCatalog calls this itself, AFTER buildCatalogEntry resolves the
// entry — rather than the caller pre-fetching before the request reaches
// the orchestrator — because a from_marketplace entry with no explicit
// version only gets its final version from latestVersionFn partway
// through AddToCatalog. Contrast with v3's AddAddon, which takes
// already-fetched UpstreamValues because that door always requires the
// version up front.
//
// Wired via SetChartValuesFetcher; nil means "no fetcher configured" —
// AddToCatalog then falls back to the comment-only stub for every addon,
// which is also why every orchestrator unit test (none of which wires a
// fetcher) keeps exercising the pre-v4-smartvalues stub path unchanged.
type ChartValuesFetcherFn func(ctx context.Context, addonName, repoURL, chart, version string) (ChartValuesFetchResult, error)

// SetChartValuesFetcher wires in the upstream chart values.yaml fetcher
// used by AddToCatalog's global-values scaffold (v4 smartvalues wave).
// Optional — see the field/type doc comments for the nil behaviour.
func (o *Orchestrator) SetChartValuesFetcher(fn ChartValuesFetcherFn) {
	o.chartValuesFetcherFn = fn
}

// v4GenerateGlobalValues is AddToCatalog's replacement for the old
// "always write the comment-only stub" step. It fetches the chart's
// official values.yaml (when a fetcher is wired) and runs it through the
// v4 smart-values pipeline; on any failure — no fetcher wired, the fetch
// itself failing, an empty response — it falls back to the comment-only
// stub. The add NEVER fails because of this: every return path here is a
// valid comment-only-or-generated YAML document, never an error.
func (o *Orchestrator) v4GenerateGlobalValues(ctx context.Context, addonName, repoURL, chart, version string) []byte {
	if o.chartValuesFetcherFn == nil {
		return globalValuesStub(addonName)
	}
	res, err := o.chartValuesFetcherFn(ctx, addonName, repoURL, chart, version)
	if err != nil || len(res.UpstreamValues) == 0 {
		return v4GlobalValuesFetchFailedStub(addonName, chart, version)
	}
	return GenerateGlobalValuesFileV4(
		addonName, chart, version, repoURL, res.UpstreamValues,
		res.AIAnnotated, false,
		res.ExtraClusterSpecificPaths...,
	)
}

// v4GlobalValuesFetchFailedStub is the honest fallback when the chart's
// values.yaml could not be fetched (unreachable registry, missing
// values.yaml, oversize, or simply no fetcher wired for this call). It is
// the same comment-only stub as globalValuesStub, with one extra leading
// line naming why the file has no chart defaults yet, so the file still
// parses as an empty YAML document (no invented values) while telling the
// person what to do next.
func v4GlobalValuesFetchFailedStub(addonName, chart, version string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b,
		"# Sharko could not fetch %s@%s's chart values right now (network or registry error) — this file starts empty.\n"+
			"# Run AI annotate on this addon later, or Refresh from upstream, to fill it in once the chart is reachable.\n",
		chart, version)
	b.Write(globalValuesStub(addonName))
	return []byte(b.String())
}

// globalValuesStub and clusterValuesStub are the comment-only scaffold
// content written for a values file that does not exist yet (v4-walkfix W1
// items 5 and 6): AddToCatalog scaffolds values/global/<addon>.yaml for
// every addon it adds (single, batch, and the add+enable combo), and
// EnableAddonV4 / AddToCatalog's enable half scaffolds
// values/clusters/<cluster>/<addon>.yaml whenever the enable request
// carries no explicit values. Neither ever invents a default VALUE — those
// belong to the catalog entry or the chart's own defaults — only a
// plain-English pointer to where overrides go. Both are comment-only YAML
// (parses as an empty document), which the engine chart's
// ignoreMissingValueFiles: true / Helm's own empty-values handling already
// treats as "no overrides" — see tests/enginerender's coverage for the
// exact proof. Neither function ever OVERWRITES an existing file — every
// call site checks existence first and skips when the file is already
// there, hand-created or not.
func globalValuesStub(addonName string) []byte {
	return []byte(fmt.Sprintf(
		"# Helm values for %s, applied to every cluster that enables it.\n"+
			"# Cluster-specific overrides live in values/clusters/<cluster>/%s.yaml.\n"+
			"# An empty file means the chart's own defaults.\n",
		addonName, addonName))
}

func clusterValuesStub(addonName, clusterName string) []byte {
	return []byte(fmt.Sprintf(
		"# Helm values for %s on %s only. These override values/global/%s.yaml.\n"+
			"# An empty file means no cluster-specific overrides.\n",
		addonName, clusterName, addonName))
}

// isV4Repo reports whether the connected repo is v4-format, using the
// same probe CheckEnginePin (enginepin.go) and
// AddonService.GetVersionMatrix (internal/service/addon.go) already use:
// the engine pin (EnginePinPath) resolving to non-empty content.
//
// It answers (bool, error), and the error half is load-bearing. The
// earlier form swallowed every read failure into "not v4", which made
// refuseOnV4Repo fail OPEN: for one unreachable moment — an expired
// token, a rate limit, a network blip — a genuinely v4 repo looked like a
// v3 one, and the v3 write it was supposed to refuse went ahead and
// recreated configuration/managed-clusters.yaml. That is precisely the
// second-registry hijack ErrV4RepoUnsupported exists to prevent: the
// reconciler prefers the v3 file whenever both exist, so every cluster
// registered the v4 way vanishes from the desired state and has its
// ArgoCD connection Secret deleted.
//
// So: a genuinely ABSENT pin is (false, nil) — the ordinary "not a v4
// repo yet" answer. Any OTHER failure is (false, err), and the write
// gates turn that into a refusal. Read and status paths are free to keep
// treating an error as "not v4"; they say so at their call site.
// IsV4Repo is the exported, fail-closed form of isV4Repo — for API-layer
// callers that pick a v3-vs-v4 commit PATH themselves (rather than routing
// the whole write through an orchestrator method) and need the same
// "guessing costs the fleet" discipline every other v4 write gate uses.
// The PATCH /clusters/{name} secret_path writer (clusters_write.go) is the
// one caller today: it chooses between managed-clusters.yaml (v4) and
// configuration/managed-clusters.yaml (v3) and must not guess wrong in the
// "not v4" direction, for the exact reason isV4Repo's doc comment above
// describes.
func (o *Orchestrator) IsV4Repo(ctx context.Context) (bool, error) {
	return o.isV4Repo(ctx)
}

func (o *Orchestrator) isV4Repo(ctx context.Context) (bool, error) {
	if o.git == nil {
		return false, nil
	}
	content, err := o.git.GetFileContent(ctx, EnginePinPath, o.gitops.BaseBranch)
	if err == nil {
		return len(content) > 0, nil
	}
	if errors.Is(err, gitprovider.ErrFileNotFound) {
		return false, nil
	}
	return false, fmt.Errorf(
		"could not read %s from the %s branch, so Sharko cannot tell which layout this repo uses: %w",
		EnginePinPath, o.gitops.BaseBranch, err)
}

// isV4RepoLenient is the old, error-swallowing form, kept for the read and
// status paths where a wrong answer costs nothing worse than a slightly
// stale view — and NAMED so that every remaining use of it is a visible,
// deliberate choice rather than an accident of the signature.
//
// Never call this before a write.
func (o *Orchestrator) isV4RepoLenient(ctx context.Context) bool {
	v4, err := o.isV4Repo(ctx)
	return err == nil && v4
}
