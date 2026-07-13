# Marketplace Architecture — Three-Layer Model

Sharko's addon system has three distinct layers. Understanding them prevents confusion when managing addons across your fleet.

## The Three Layers

| Layer | What It Is | Where It Lives | Purpose |
|-------|------------|----------------|---------|
| **Marketplace** | Browse/discover surface with metadata | Embedded `catalog/addons.yaml` + optional third-party sources | Shows what addons exist to discover — metadata only (name, description, docs, license, maintainers, OpenSSF score, curation tags). Deploys nothing. |
| **Catalog** | Deployable list | `configuration/addons-catalog.yaml` in your GitOps repo | The list of addons your team can deploy. Each entry has a Helm chart, repo, version, and namespace. This is what ArgoCD reads. |
| **Enablement** | Per-cluster labels | `configuration/managed-clusters.yaml` + cluster-specific values files | What is actually running on each cluster. You enable an addon on a cluster by adding a label; ArgoCD sees the label and creates the Application. |

### In Plain English

- **Marketplace = what's available to browse.** It's the menu. You can open the Marketplace tab, filter by category or OpenSSF tier, and read about addons. Clicking an addon shows its README, version picker, and license. Nothing gets deployed until you click "Add to catalog."
- **Catalog = what your team can deploy.** Once you add an addon from the Marketplace (or manually), it lands in `addons-catalog.yaml` in your GitOps repo. This is the approved, deployable list. ArgoCD's ApplicationSet reads this file to know which addons exist.
- **Enablement = what's running where.** After an addon is in the catalog, you enable it per-cluster by toggling it on in the UI (which opens a PR to add a label to that cluster's entry in `managed-clusters.yaml`). Once the label is present, ArgoCD deploys the addon to that cluster.

## Flow: Discovery → Deployment

```
User browses Marketplace
    ↓
Clicks "Add to catalog" on cert-manager
    ↓
Sharko opens a PR adding:
  - Entry in addons-catalog.yaml (chart, repo, version, namespace)
  - Generated addons-global-values/cert-manager.yaml (Helm values)
    ↓
PR merges
    ↓
cert-manager is now in the Catalog (deployable but not yet running)
    ↓
User opens cluster detail page, toggles cert-manager ON
    ↓
Sharko opens a PR adding label to that cluster's entry
    ↓
PR merges
    ↓
ArgoCD ApplicationSet sees:
  cluster has label → addon exists in catalog → create Application
    ↓
cert-manager is now Enabled on that cluster (running)
```

## Why Three Layers?

The separation is intentional:

- **Marketplace sources can be untrusted third-party URLs.** You configure `SHARKO_CATALOG_URLS` or `configuration/marketplace-sources.yaml` to pull additional addon metadata from partner catalogs, vendor-hosted lists, or internal curation servers. These sources only populate the Marketplace browse surface — they do NOT write to your GitOps repo. A malicious third-party source can show you fake entries in the Marketplace, but it cannot deploy anything without you clicking "Add to catalog" and reviewing the PR.
- **Catalog is your team's approved menu.** Every addon in `addons-catalog.yaml` went through a pull request that someone reviewed. This is the trust boundary. ArgoCD only deploys what's in this file.
- **Enablement is per-cluster control.** A cluster can have cert-manager enabled while another cluster does not. This is GitOps-native cluster labeling — the same pattern ArgoCD uses for everything else.

## Marketplace Source Refreshes vs Catalog Updates

- **Marketplace sources refresh automatically** (default: every 1 hour, configurable via `SHARKO_CATALOG_REFRESH_INTERVAL`). Sharko fetches the latest metadata from configured URLs and updates the in-memory view of the Marketplace browse tab. This is read-only — no Git writes.
- **Catalog updates are manual.** You must click "Add to catalog" in the Marketplace or use the "Add Addon" button on the Catalog tab. Every catalog change opens a PR.
- **The discovery bot (see below) automates proposals.** It runs daily, scans upstream sources, and opens a PR proposing new addons or version updates. You review and merge (or close) the PR — the bot never auto-merges.

## Related Pages

- [Third-party Catalog Sources](catalog-sources.md) — how to configure `SHARKO_CATALOG_URLS` and the git-native `marketplace-sources.yaml`
- [Marketplace Sources Configuration](marketplace-sources-config.md) — reference for the `configuration/marketplace-sources.yaml` file schema
- [Catalog Scan Runbook](../developer-guide/catalog-scan-runbook.md) — how the discovery bot works and how to review its PRs
- [Managing Addons](../user-guide/addons.md) — add/remove/upgrade addons in the Catalog
- [Marketplace](../user-guide/marketplace.md) — browse and discover addons in the UI
