# Editing Helm Values

Starting in **v1.20**, Sharko ships an in-app editor for the Helm values that drive every addon. You can edit either the global default values for an addon, or the per-cluster overrides that customise that addon for one specific cluster — both flows go through the same PR-based GitOps workflow as every other Sharko mutation.

## Two scopes

| Scope | What it changes | Where it lives in Git | Who's affected |
|-------|-----------------|-----------------------|----------------|
| **Global** | Default values for an addon | `configuration/addons-global-values/<addon>.yaml` | Every cluster running this addon |
| **Per-cluster overrides** | Just one addon on one cluster | `configuration/addons-clusters-values/<cluster>.yaml`, under the addon's section | Only the chosen cluster |

Per-cluster overrides win when both are set, exactly the way Helm value composition works.

## Editing global values

1. Open the addon detail page (Addons → click any addon).
2. Click the **Values** tab in the left rail.
3. The current YAML loads into the editor. The editor has two views:
   - **YAML** — a monospace text editor with live YAML validation. Errors show in the status strip below the editor and disable the Submit button until fixed.
   - **Diff** — a side-by-side line-aligned diff showing the current file vs. your edits.
4. Make your changes. The status strip at the bottom of the YAML view reports unsaved changes.
5. Click **Submit changes**. Sharko opens a pull request titled `update global values for <addon>`.
6. Once the PR is merged (manually, or automatically if your active connection has `pr_auto_merge` enabled), ArgoCD reconciles the change to every cluster running this addon.

A toast shows the PR link as soon as it's opened, and a persistent green confirmation banner stays in the editor with a clickable PR link until you reload the page.

If you'd rather use GitHub's web editor, click **Edit in GitHub** in the editor header — the link deep-links to the values file on the active connection's default branch.

## Editing per-cluster overrides

1. Open the cluster detail page (Clusters → click any cluster).
2. Click the **Config** tab in the left rail.
3. At the top of the Config panel you'll find an **Addon picker**. Select the addon you want to override. A banner above the editor explains: *"Anything here overrides global values. Leave empty to use the global defaults."*
4. The editor loads the addon's current overrides section (or stays empty if no overrides exist yet). The editor and submit flow are identical to the global editor — same YAML/Diff tabs, same diff preview, same nudge logic.
5. Submit your changes — Sharko opens a PR titled `update <addon> overrides on cluster <cluster>` that touches only the section for this addon in that cluster's overrides file. Other addons and the cluster's `clusterGlobalValues:` block are preserved.
6. To **clear** an override (return to the global default for this cluster), submit an empty editor. Sharko removes the addon's section from the cluster file in the same PR-based flow.

Below the editor you'll find the existing **Cluster Override** diff panel — once your PR merges, refresh the page and the diff updates to show the new overrides applied.

## Pull upstream defaults

Next to **Submit changes** in the global-values editor you'll find a **Pull upstream defaults** button. Clicking it opens a confirmation modal:

> This will replace the current `values.yaml` with upstream defaults from `<chart>@<version>`. Your edits will be lost. Continue?

On confirm, Sharko:

1. Downloads the chart tarball from the configured Helm repo.
2. Extracts the chart's `values.yaml` (comments and formatting preserved).
3. Wraps it under the `<addonName>:` key that Sharko's global-values convention uses.
4. Opens a PR titled `pull upstream defaults for <addon>` with the resulting file.

This is the fastest way to refresh your global values when the chart version moves and exposes new keys you want to override. Only the `replace` merge strategy is implemented in v1.20.1 — a `merge_keep_overrides` strategy that preserves your edits and only adds new upstream keys is on the v1.21 roadmap.

**Per-cluster overrides** don't offer this button — overrides should stay deltas-only. Edit the global values and let ArgoCD reconcile.

## Recent changes panel

Beneath each editor, the **Recent changes (last 5)** panel lists recently-merged pull requests that touched that values file (or the cluster overrides file, for per-cluster editors). Each row links straight to the GitHub PR; a **View all on GitHub** link at the top right opens GitHub's PR search filtered by the file path.

The list is backed by a 5-minute in-memory cache on the server — if you just merged a PR and don't see it yet, wait a few minutes or refresh the page.

## Diff labels

When you flip to the **Diff** tab the two columns are labelled **Currently in Git** (the file as it exists on the default branch) and **Your changes** (your pending edits). A small caption above the diff reminds you:

> The PR will replace `Currently in Git` with `Your changes`.

If you haven't edited anything yet, the Diff tab shows a friendly "No changes yet" placeholder instead of an empty diff.

## Schema-aware editing (when available)

If your addon ships a `values.schema.json`, you can publish it to the GitOps repo as `configuration/addons-global-values/<addon>.schema.json` (next to the values file) and Sharko will surface a hint banner above the editor listing the schema's top-level keys. This helps catch typos in key names without leaving the editor.

Full schema-driven autocomplete (Monaco-powered) is on the v1.21 roadmap — the v1.20 editor is a textarea with YAML validation and the schema hint, kept intentionally light to avoid a multi-megabyte editor bundle.

## How the PR workflow attributes your edit

Both editors land Tier 2 (configuration) writes, which means:

- **If you've configured a personal GitHub PAT** in **Settings → My Account**, the resulting Git commit is authored by you — your name shows in `git blame`, and the PR review surface shows your avatar instead of `Sharko Bot`'s.
- **If you haven't**, the commit is authored by the Sharko service account, and you're added as a `Co-authored-by:` trailer. The editor pops up a yellow nudge banner explaining this and linking to **Settings → My Account** so you can fix it for next time. The action still succeeds — the nudge is informational.

For more on the attribution model, see [Git Attribution](attribution.md).

## Things that are NOT in the v1.20 editor

These are intentional v1.20 cuts; pencilled in for later releases:

- **Schema-driven autocomplete** in YAML mode (v1.21 candidate)
- **Quick-edit form** generated from the schema (v1.21 candidate)
- **History view** of past edits to a values file (v1.x — depends on PR tracker enrichment)
- **One-click rollback** from a history entry
- **Per-key permissions / value-level RBAC** (V2.x — see [V2 roadmap](../../design/2026-04-16-attribution-and-permissions-model.md))

If you hit a workflow you wish was in here, file an issue.
