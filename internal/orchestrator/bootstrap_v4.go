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

// v4SeedFolders are the empty data folders the bootstrap PR creates (design
// doc §1's file tree, minus engine/ which carries the pin itself, not a
// .gitkeep). Git cannot track an empty directory, so each one gets a
// .gitkeep placeholder — that is a git limitation, not Sharko data (design
// doc §1, "What the bootstrap PR actually contains").
var v4SeedFolders = []string{
	"clusters",
	"fleet",
	"values/global",
	"values/clusters",
	"catalog",
}

// BuildV4SeedFiles returns the exact, complete set of files the v4
// bootstrap PR commits: one .gitkeep per empty data folder, the engine pin
// (engine/application.yaml, at BootstrapRootAppPath), and README.md.
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

// buildEnginePin renders engine/application.yaml — the whole of Sharko's
// deployment logic, reduced to one pointer (design doc §2.5). Every value
// is resolved from server-side config at write time; there are no
// placeholder tokens left for a later substitution pass (contrast the v3
// bootstrap path's replacePlaceholdersFull), because v4's engine pin is
// generated fresh per repo rather than copied from a static template.
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
	b.WriteString("          - $values/catalog/addons.yaml\n")
	b.WriteString("        parameters:\n")
	b.WriteString("          - name: repo.url\n")
	fmt.Fprintf(&b, "            value: %s\n", repoURL)
	b.WriteString("          - name: repo.revision\n")
	fmt.Fprintf(&b, "            value: %s\n", branch)
	b.WriteString("          - name: hostCluster.name\n")
	fmt.Fprintf(&b, "            value: %q\n", paths.HostClusterName)
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
clusters/     which addons run on each cluster, at which version, tuned how
              (one file per cluster: clusters/<cluster-name>.yaml)
fleet/        how Sharko reaches each cluster (fleet/connections.yaml)
catalog/      your own addons, and your changes to the shipped ones
              (catalog/addons.yaml — only your changes, never a full copy)
values/       Helm values for each addon
              values/global/<addon>.yaml       — everywhere
              values/clusters/<cluster>/<addon>.yaml — one cluster only
engine/       the engine pin (engine/application.yaml) — the ONLY moving
              part Sharko ships. Upgrading the engine is a pull request
              that changes one line.
` + "```" + `

A folder with only a ` + "`.gitkeep`" + ` file in it is empty on purpose — a file that
is not there means "empty", never an error. Sharko creates each real file
the first time it has something to put in it (for example, the first
cluster you register creates ` + "`fleet/connections.yaml`" + `).

## How it works

Sharko manages this repository through pull requests — every change is
reviewable before it merges. The engine pin (` + "`engine/application.yaml`" + `)
tells ArgoCD which version of Sharko's engine chart to run; that chart
reads ` + "`clusters/`" + `, ` + "`catalog/addons.yaml`" + `, and ` + "`values/`" + ` and turns them into
one ArgoCD ApplicationSet per addon. There are no other templates or
generated files in this repository — see
https://github.com/MoranWeissman/sharko/blob/main/docs/design/2026-07-30-v4-data-file-format.md
for the full contract.
`)
}
