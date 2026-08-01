// Package orchestrator — the v4 bootstrap seed (v4 Wave 1 Story 4.2).
//
// docs/design/2026-07-30-v4-data-file-format.md §1 defines exactly what a
// first-run bootstrap PR contains: the empty data folders, the engine pin,
// and a README — nothing else. "Nothing else" is the operative word: the
// v3 bootstrap path walked templates/bootstrap/ (a Helm chart of
// ApplicationSet templates, a catalog seed, per-addon values stubs, a
// connectivity-check ConfigMap, a repository-secret template...) and
// committed the whole tree. None of that exists in v4 — the engine chart
// (charts/sharko-engine/) IS that logic now, published once and pinned by
// one file. BuildV4SeedFiles is the single generator for the seed; there
// is no template tree to drift from any more.
package orchestrator

import (
	"fmt"
	"strings"

	"github.com/MoranWeissman/sharko/internal/engineversion"
)

// DefaultEngineChartRepoURL is the OCI registry path BuildV4SeedFiles pins
// the engine chart to when GitOpsConfig.EngineChartRepoURL is unset — the
// path Story 2.4's release pipeline (.github/workflows/release.yml) pushes
// and cosign-signs the sharko-engine chart under
// ("oci://ghcr.io/moranweissman/sharko/sharko-engine:vX.Y.Z" per that
// workflow's signing step comment).
const DefaultEngineChartRepoURL = "ghcr.io/moranweissman/sharko"

// v4SeedFolders are the empty data folders the bootstrap PR creates. Git
// cannot track an empty directory, so each one gets a .gitkeep placeholder
// — that is a git limitation, not Sharko data.
//
// Only the folders that genuinely hold MANY files are here. The three
// single files — managed-clusters.yaml, catalog.yaml and sharko-engine.yaml
// — sit at the repo root with no folder around them (design doc
// 2026-07-31-catalog-approved-model.md §2), so there is nothing to .gitkeep
// for them. A folder gets born the day a per-file split is really needed,
// not before.
var v4SeedFolders = []string{
	V4ClustersDir,
	"values/global",
	"values/clusters",
}

// BuildV4SeedFiles returns the exact, complete set of files the v4
// bootstrap PR commits: one .gitkeep per empty data folder, the engine pin
// (sharko-engine.yaml, at BootstrapRootAppPath), and README.md.
// Nothing else — no AppProject, no Chart.yaml, no catalog seed, no addon
// values stubs. This is the ONLY writer of the bootstrap PR's file set;
// CollectBootstrapFiles and the synchronous InitRepo both call this rather
// than re-deriving it, so "exactly the seed" (Story 4.2's acceptance
// criterion) has one place that can go wrong.
func BuildV4SeedFiles(gitops GitOpsConfig, paths RepoPathsConfig) map[string][]byte {
	files := make(map[string][]byte, len(v4SeedFolders)+2)

	for _, folder := range v4SeedFolders {
		files[folder+"/.gitkeep"] = []byte{}
	}

	files[BootstrapRootAppPath] = buildEnginePin(gitops, paths)
	files["README.md"] = buildV4ReadmeMD()

	return files
}

// buildEnginePin renders sharko-engine.yaml — the whole of Sharko's
// deployment logic, reduced to one pointer (design doc §2.5). Every value
// is resolved from server-side config at write time; there are no
// placeholder tokens left for a later substitution pass (contrast the v3
// bootstrap path's replacePlaceholdersFull), because v4's engine pin is
// generated fresh per repo rather than copied from a static template.
//
// The file opens with a plain-English comment header (v4 naming polish,
// item 3) — a YAML comment ahead of apiVersion, which every YAML parser
// (including ArgoCD's own) ignores as content, so it costs nothing and
// tells a person what this one real Kubernetes manifest in the repo is for
// before they read a line of it.
func buildEnginePin(gitops GitOpsConfig, paths RepoPathsConfig) []byte {
	repoURL := gitops.RepoURL
	branch := gitops.BaseBranch
	if branch == "" {
		branch = "main"
	}
	engineRepoURL := gitops.EngineChartRepoURL
	if engineRepoURL == "" {
		engineRepoURL = DefaultEngineChartRepoURL
	}

	var b strings.Builder
	b.WriteString(sharkoEngineYAMLHeader)
	b.WriteString("apiVersion: argoproj.io/v1alpha1\n")
	b.WriteString("kind: Application\n")
	b.WriteString("metadata:\n")
	fmt.Fprintf(&b, "  name: %s\n", BootstrapRootAppName)
	b.WriteString("  namespace: argocd\n")
	b.WriteString("  labels:\n")
	b.WriteString("    app.kubernetes.io/managed-by: sharko\n")
	b.WriteString("  finalizers:\n")
	b.WriteString("    - resources-finalizer.argocd.argoproj.io\n")
	b.WriteString("spec:\n")
	b.WriteString("  project: default\n")
	b.WriteString("  sources:\n")
	fmt.Fprintf(&b, "    - repoURL: %s\n", engineRepoURL)
	fmt.Fprintf(&b, "      chart: %s\n", engineversion.BundledChartName)
	fmt.Fprintf(&b, "      targetRevision: %s\n", engineversion.BundledVersion)
	b.WriteString("      helm:\n")
	b.WriteString("        ignoreMissingValueFiles: true\n")
	b.WriteString("        valueFiles:\n")
	b.WriteString("          - $values/catalog.yaml\n")
	b.WriteString("        parameters:\n")
	b.WriteString("          - name: repo.url\n")
	fmt.Fprintf(&b, "            value: %s\n", repoURL)
	b.WriteString("          - name: repo.revision\n")
	fmt.Fprintf(&b, "            value: %s\n", branch)
	b.WriteString("          - name: hostCluster.name\n")
	fmt.Fprintf(&b, "            value: %q\n", paths.HostClusterName)
	// engineChart.repoURL mirrors this source's own repoURL back into a
	// Helm value (chart 0.3.0) so the connectivity-check ApplicationSet
	// (charts/sharko-engine/templates/connectivity-check.yaml) can point a
	// generated, per-cluster Application back at "this same chart, this
	// same version" without writing any manifest into the user's
	// data-only repo — the same self-referential trick, using the same
	// registry location this Application source is already pulling from.
	b.WriteString("          - name: engineChart.repoURL\n")
	fmt.Fprintf(&b, "            value: %s\n", engineRepoURL)
	fmt.Fprintf(&b, "    - repoURL: %s\n", repoURL)
	fmt.Fprintf(&b, "      targetRevision: %s\n", branch)
	b.WriteString("      ref: values\n")
	b.WriteString("  destination:\n")
	b.WriteString("    server: https://kubernetes.default.svc\n")
	b.WriteString("    namespace: argocd\n")
	b.WriteString("  syncPolicy:\n")
	b.WriteString("    automated:\n")
	b.WriteString("      prune: true\n")
	b.WriteString("      selfHeal: true\n")
	b.WriteString("    syncOptions:\n")
	b.WriteString("      - CreateNamespace=true\n")

	return []byte(b.String())
}

// buildV4ReadmeMD renders the bootstrap seed's README.md — plain English,
// no Helm knowledge assumed, matching design doc §1's file tree and the
// "no templates" explanation a user reads before touching anything else in
// the repo.
func buildV4ReadmeMD() []byte {
	return []byte(`# Sharko Addons Repository

This repository is managed by [Sharko](https://github.com/MoranWeissman/sharko).
Every file in it is something a person wrote on purpose, or one pointer file
Sharko itself keeps up to date — there is nothing generated and nothing to
render to understand what is running.

## Layout

` + "```" + `text
managed-clusters.yaml   the clusters Sharko manages, and how it reaches them
catalog.yaml            the addons your org has approved for those clusters
sharko-engine.yaml      which version of Sharko's engine chart to run — the
                        ONLY moving part Sharko ships. Upgrading it is a
                        pull request that changes one line.
cluster-addons/         which addons run on each cluster, at which version,
                        tuned how (one file per cluster:
                        cluster-addons/<cluster-name>.yaml)
values/                 Helm values for each addon
                        values/global/<addon>.yaml              — everywhere
                        values/clusters/<cluster>/<addon>.yaml  — one cluster
` + "```" + `

A folder with only a ` + "`.gitkeep`" + ` file in it is empty on purpose — a file that
is not there means "empty", never an error. Sharko creates each real file
the first time it has something to put in it (for example, the first
cluster you register creates ` + "`managed-clusters.yaml`" + `).

` + "`catalog.yaml`" + ` starts out empty, on purpose: nothing runs in your org that
somebody did not put there. Adding an addon to it copies the whole entry in
— chart, repo, version, namespace — so the pull request reviewer sees
exactly what is entering the org, and this repository on its own tells the
whole story.

## How it works

Sharko manages this repository through pull requests — every change is
reviewable before it merges. The engine pin (` + "`sharko-engine.yaml`" + `)
tells ArgoCD which version of Sharko's engine chart to run; that chart
reads ` + "`catalog.yaml`" + `, ` + "`cluster-addons/`" + `, and ` + "`values/`" + ` and turns them into
one ArgoCD ApplicationSet per approved addon. There are no other templates
or generated files in this repository — see
https://github.com/MoranWeissman/sharko/blob/main/docs/design/2026-07-30-v4-data-file-format.md
for the full contract.
`)
}
