# Marketplace

The **Marketplace** is Sharko's curated catalog of community Helm charts that you can add to your cluster fleet without leaving the UI. It surfaces the projects we've vetted (CNCF graduates, AWS EKS Blueprints, Bitnami baseline picks, and a small set of vendor-curated charts) along with an OpenSSF Scorecard signal so you can pick safer-by-default options first.

The Marketplace is a **read-only browse experience**. Submitting an addon goes through the same v1.20 GitOps PR flow as every other Sharko mutation — nothing lands in your `addons-catalog.yaml` until a pull request opens (and, if your active connection has `pr_auto_merge` enabled, merges automatically).

## Browsing

1. Open **Addons** in the left rail.
2. Switch to the **Marketplace** tab at the top of the page.
3. The Marketplace has two subtabs: **Browse curated** (default) and **Search ArtifactHub**. Browse is the filterable curated grid; Search is name-based discovery across our catalog and ArtifactHub.

For Helm charts that aren't in our catalog and aren't on ArtifactHub (internal repos, vendor charts hosted on a homepage CDN), use the **Add Addon** button on the Catalog tab — it auto-validates the repo URL and lists the available chart names. The Paste-Helm-URL flow that was a Marketplace subtab in early v1.21 builds was retired in v1.21 QA so the Marketplace stays focused on discovery.

### Browse curated

Use the sidebar to filter by **Category**, **Curated by** (e.g. `cncf-graduated`, `aws-eks-blueprints`), **License**, or **OpenSSF tier** (Strong / Moderate / Weak / Unknown). Categories OR within the axis; curators AND.

Filters are persisted in the URL, so `?mp_cat=security&mp_tier=strong` deep-links you to a specific slice.

Each card shows the chart name, a one-line description, the OpenSSF score badge, the license, the maintainers, and a docs link when one's published.

OpenSSF scores refresh once a day at 04:00 UTC against the public [Scorecard API](https://api.scorecard.dev). Scores are cached in-memory; on a fresh pod restart the catalog falls back to the bundled baseline scores until the next 04:00 UTC tick. If the Scorecard API is unreachable, the previous score sticks (no zeroing) and a warning is logged. The current refresh status is exposed via the Prometheus metrics `sharko_scorecard_refresh_total{status}` and `sharko_scorecard_last_refresh_timestamp`.

### Search ArtifactHub

Click **Search ArtifactHub** when you want a chart that isn't in our curated catalog. Type any name; results appear in two stacked sections:

- **Curated by Sharko** (top) — full catalog cards with the same in-page detail flow as Browse.
- **From ArtifactHub** (bottom) — slim cards tagged "ArtifactHub" with verified-publisher badge and star count when applicable.

Sharko proxies ArtifactHub server-side — your browser never calls them directly — so search results are cached for 10 minutes per query. If ArtifactHub is unreachable (network blocked, rate-limited, or down), the curated section still works and you'll see an amber banner: *"ArtifactHub unreachable — showing curated only."* with a **Retry connectivity** button that re-probes immediately.

When you click an ArtifactHub result, the same in-page detail view opens (just like a curated card) — pre-filled with the chart name, repo URL, license, maintainers, and the upstream README — all fetched from ArtifactHub's package detail. The Submit & PR flow is identical to a curated entry.

Use **Browse** when you know what you want from our vetted set; use **Search** for the long tail.

## Add to your catalog

Clicking a Marketplace card swaps the Marketplace tab for an **in-page addon detail view** (replacing the v1.21-pre Configure modal). The detail view has four sections, top to bottom:

1. **Hero** — addon icon, name, one-line description, category and curator chips, license, OpenSSF Scorecard tier, GitHub stars.
2. **Add to your catalog** — an embedded form (NOT a popup) with an explainer:
   > This creates an ArgoCD ApplicationSet for `<addon>` and adds an entry to your `addons-catalog.yaml`. The addon will be available to deploy on any cluster afterwards.
   Fields are **Display name** (defaults to the chart name), **Namespace** (defaults to the chart's recommended namespace), and **Chart version** (top-10 stable picker; tick **Show pre-releases** to expand). The submit button is labelled **Add to catalog** — wording the maintainer feedback singled out as the missing piece in the popup-style modal. There is no sync-wave field; you set it on the addon page after the PR merges.
3. **README** — the upstream chart README, fetched from ArtifactHub and rendered in-place. When ArtifactHub doesn't have a README for the chart you'll see a "No README available" empty state. A **View on ArtifactHub** link in the section header opens the upstream package page in a new tab.
4. **Footer** — Helm chart name, repo URL, docs URL, source URL, and the maintainer list.

Click **← Back to Marketplace** at the top to return to the grid; your filter state is preserved on the URL so you land back where you came from.

If the addon **already exists in your catalog**, the action panel collapses to a friendly link to `/addons/<name>` so you don't accidentally open a no-op PR. The same protection (with a 409 fallback) applies if you rename the entry to clash with an existing one mid-form.

The earlier "Configure `<addon>`" wording from the popup-style modal has been retired — it confused operators because clicking it didn't configure the addon on a cluster, it added it to the catalog (which then becomes available to enable per-cluster).

### Submit & PR flow

When you click **Add to catalog**, Sharko calls the existing `POST /api/v1/addons` endpoint — the same one used by the raw "Add Addon" form on the Catalog tab. The handler reuses v1.20's tiered Git plumbing:

1. **Tier 2 attribution.** The endpoint is registered as a Tier 2 (configuration) mutation, so Sharko prefers your personal GitHub PAT when one is configured (Settings → My Account). Without a PAT, the change is committed by the Sharko service account with a `Co-authored-by:` trailer for you, and an inline **AttributionNudge** banner appears next to the submit button explaining the fallback.
2. **PR opens against `addons-catalog.yaml`.** A branch is created (default prefix `sharko/`), a commit lands with both the catalog entry and a generated `addons-global-values/<name>.yaml` (see step 2a), and a pull request is opened.

   **(2a) Smart values seeding (v1.21).** Sharko fetches the chart's upstream `values.yaml` for the version you picked and runs the [smart-values pipeline](smart-values.md) before committing: cluster-specific fields are commented out at their original position, a per-cluster template block is appended at the bottom, and a `# sharko: managed=true` header is stamped on the front. If the chart can't be fetched (registry unreachable, version not found), Sharko falls back to a minimal `<name>:\n  enabled: false` stub — you can refresh the file later from the Values tab once connectivity is back.
3. **Toast + persistent banner.** As soon as the PR URL comes back, a toast appears in the bottom-right (`PR #N opened →` or `PR #N merged →` if your connection auto-merges) and the action panel grows a green banner with a clickable PR link so you can jump straight to GitHub. The toast and banner stay neutral about review state — auto-merge may have already fired server-side.
4. **Audit trail.** The action is recorded as the existing `addon_added` event with the originating UI flow in the `source` detail field (`marketplace` for curated cards, `artifacthub` for Search results, `manual` for the Add Addon form on the Catalog tab). Filtering audit by source surfaces Marketplace-driven additions vs. manual ones without inventing a new event name.
