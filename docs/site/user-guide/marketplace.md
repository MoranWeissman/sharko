# Marketplace

The **Marketplace** is Sharko's curated catalog of community Helm charts that you can add to your cluster fleet without leaving the UI. It surfaces the projects we've vetted (CNCF graduates, AWS EKS Blueprints, Bitnami baseline picks, and a small set of vendor-curated charts) along with an OpenSSF Scorecard signal so you can pick safer-by-default options first.

The Marketplace is a **read-only browse experience**. Submitting an addon goes through the same v1.20 GitOps PR flow as every other Sharko mutation — nothing lands in your `addons-catalog.yaml` until a pull request opens (and, if your active connection has `pr_auto_merge` enabled, merges automatically).

## Browsing

1. Open **Addons** in the left rail.
2. Switch to the **Marketplace** tab at the top of the page.
3. The Marketplace has two subtabs: **Browse curated** (default) and **Search ArtifactHub**. Browse is the filterable curated grid; Search is name-based discovery across our catalog and ArtifactHub.

### Browse curated

Use the sidebar to filter by **Category**, **Curated by** (e.g. `cncf-graduated`, `aws-eks-blueprints`), **License**, or **OpenSSF tier** (Strong / Moderate / Weak / Unknown). Categories OR within the axis; curators AND.

Filters are persisted in the URL, so `?mp_cat=security&mp_tier=strong` deep-links you to a specific slice.

Each card shows the chart name, a one-line description, the OpenSSF score badge, the license, the maintainers, and a docs link when one's published.

### Search ArtifactHub

Click **Search ArtifactHub** when you want a chart that isn't in our curated catalog. Type any name; results appear in two stacked sections:

- **Curated by Sharko** (top) — full catalog cards with the same Configure flow as Browse.
- **From ArtifactHub** (bottom) — slim cards tagged "ArtifactHub" with verified-publisher badge and star count when applicable.

Sharko proxies ArtifactHub server-side — your browser never calls them directly — so search results are cached for 10 minutes per query. If ArtifactHub is unreachable (network blocked, rate-limited, or down), the curated section still works and you'll see an amber banner: *"ArtifactHub unreachable — showing curated only."* with a **Retry connectivity** button that re-probes immediately.

When you click an ArtifactHub result, the Configure modal opens pre-filled with the chart name, repo URL, and (best-effort) license + maintainers fetched from ArtifactHub's package detail. The Submit & PR flow is identical to a curated entry.

Use **Browse** when you know what you want from our vetted set; use **Search** for the long tail.

## Configure & submit

Clicking a card opens the **Configure** modal — a pre-filled form built from the curated entry's defaults:

- **Display name** — defaults to the canonical chart name. You can rename it (e.g. `cert-manager-eu`) if you want a second copy of the same chart with a different config.
- **Namespace** — defaults to the chart's recommended namespace.
- **Sync wave** — defaults to the chart's recommended position in the deploy ordering. Lower numbers deploy first.
- **Chart version** — populated from the chart's `index.yaml` via Sharko's catalog versions endpoint. Top-5 stable by default; tick **Show pre-releases** to see all.

### Submit & PR flow

When you click **Submit & open PR**, Sharko calls the existing `POST /api/v1/addons` endpoint — the same one used by the raw "Add Addon" form on the Catalog tab. The handler reuses v1.20's tiered Git plumbing:

1. **Tier 2 attribution.** The endpoint is registered as a Tier 2 (configuration) mutation, so Sharko prefers your personal GitHub PAT when one is configured (Settings → My Account). Without a PAT, the change is committed by the Sharko service account with a `Co-authored-by:` trailer for you, and an inline **AttributionNudge** banner appears next to Submit explaining the fallback.
2. **PR opens against `addons-catalog.yaml`.** A branch is created (default prefix `sharko/`), a commit lands with both the catalog entry and a starter `addons-global-values/<name>.yaml`, and a pull request is opened.
3. **Toast + persistent banner.** As soon as the PR URL comes back, a toast appears in the bottom-right (`PR #N opened →` or `PR #N merged →` if your connection auto-merges) and the modal grows a green banner with a clickable PR link so you can jump straight to GitHub. The toast and banner stay neutral about review state — auto-merge may have already fired server-side.
4. **Audit trail.** The action is recorded as the existing `addon_added` event with `source=marketplace` in the detail string. Filtering audit by source surfaces Marketplace-driven additions vs. manual ones without inventing a new event name.

If the addon **already exists in your catalog**, both the modal's pre-flight check and the server's 409 response render the same friendly inline message — *"<name> is already in the catalog. Open its page to edit or enable it on a cluster."* — with a deep link to the existing addon's detail page. Submit stays disabled until you rename the candidate or close the modal. No no-op PRs are opened.
