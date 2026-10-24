# Changelog

This file only tracks unreleased changes. The full per-version release
history lives in one place:
[docs/site/release-notes.md](docs/site/release-notes.md), also published at
[https://sharko.readthedocs.io/en/latest/release-notes/](https://sharko.readthedocs.io/en/latest/release-notes/).

## [Unreleased]

### Changed (API)

- `GET /api/v1/clusters/available` now has a different meaning and a different shape. It reads ArgoCD and lists the clusters ArgoCD already knows that Sharko does not manage yet (name, server address, Secret name, whether it can be reached). The old list, of clusters in your secrets provider, moved to `GET /api/v1/providers/clusters`. If you call the old address in a script, point it at the new one. `sharko add-clusters --from-provider` already does.
- `GET /api/v1/clusters` items now say if a cluster is adopted or taken over, and carry the labels ArgoCD has on it. Pending changes now say what kind of change waits (register, adopt, take over or remove) and the pull request number.
- New: `GET /api/v1/connections/git-defaults`, read-only. The first-run wizard uses it to fill in the Git provider and address from the Helm values.
- `GET /api/v1/clusters/{name}` for a cluster ArgoCD knows but Sharko does not manage now answers 404 with `"code": "not_managed"`.

### Changed (behaviour)

- Remove and un-adopt wait for the pull request. Nothing changes on the cluster or its ArgoCD connection until the pull request is merged. After the merge, Sharko finishes the job. If you close the pull request, nothing changes.
- Adopt also waits for the pull request. When it merges, Sharko takes over the ArgoCD connection and marks it adopted.
- Adopt now shows the same warnings as take over, and every reason that blocks it. In the UI each warning has a tick box. On the command line, use `--acknowledge`.
- Taking over twice no longer opens a second pull request. It says the take over is already waiting. The comment lines at the top of `managed-clusters.yaml` stay where they are.
- Sharko now finds a cluster's ArgoCD connection by the name inside the Secret, the name ArgoCD shows, not by the Secret's Kubernetes name. If two Secrets carry the same cluster name, Sharko stops and asks you to rename one.
- Take over only warns about ApplicationSets that really pick the cluster.
- Orphan leftovers list only a Secret Sharko made for a registration that never finished. Discarding one while a pull request is open now says what the pull request waits to do (register, adopt, take over or remove).
- "Remove, keep the connection" now takes Sharko's label off the connection for every kind of cluster.

### Changed (command line)

- An error prints once, with no full usage text after it.
- `sharko takeover` without `--acknowledge` prints the checks once, and names the flag `--acknowledge`.
- `sharko remove-cluster`, `unadopt-cluster` and `adopt` say "PR opened, waiting for merge" when the pull request waits, and "removed" or "adopted" only when it is really done. `adopt --dry-run` says "would adopt".
- The help texts are corrected: no "values file" in `adopt`, one default shown for `--preserve-legacy-labels`, and `test-cluster` is true for clusters whose connection ArgoCD holds.

### Changed (UI)

- The Clusters page has a line under the title for clusters ArgoCD already knows. A cluster Sharko does not manage has its own page with Adopt and Take ownership. The status legend is always visible and the popover always shows its three lines.
- The first-run wizard has a **Git provider** choice (work it out from the address, GitHub, or Gitea). An address Sharko does not know now asks you to choose, instead of guessing GitHub.
- The pending table has a **Change** column. "Remove the old labels" shows only for adopted or taken-over clusters.

### Fixed

- Check permissions works on a cluster with no `sharko-test` namespace (it makes and removes one), shows each fix once, and no longer names a made-up group.
- Diagnose connection on a new repo with no catalog file says so and is not a failure.
- An empty Gitea repo reads as "not set up yet" and no longer as a connection error.
- The docs now match the screens and commands for adopt, take over, remove, un-adopt, and the status words.
