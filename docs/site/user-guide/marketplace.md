# Marketplace

The **Marketplace** is Sharko's curated catalog of community Helm charts that you can add to your cluster fleet without leaving the UI. It surfaces the projects we've vetted (CNCF graduates, AWS EKS Blueprints, Bitnami baseline picks, and a small set of vendor-curated charts) along with an OpenSSF Scorecard signal so you can pick safer-by-default options first.

The Marketplace is a **read-only browse experience**. Submitting an addon goes through the same v1.20 GitOps PR flow as every other Sharko mutation — nothing lands in your `addons-catalog.yaml` until a pull request opens (and, if your active connection has `pr_auto_merge` enabled, merges automatically).

## Browsing

1. Open **Addons** in the left rail.
2. Switch to the **Marketplace** tab at the top of the page.
3. The Marketplace has three subtabs: **Browse curated** (default), **Search ArtifactHub**, and **Paste Helm URL**. Browse is the filterable curated grid; Search is name-based discovery across our catalog and ArtifactHub; Paste is the power-user escape hatch for charts that aren't in either.

### Browse curated

Use the sidebar to filter by **Category**, **Curated by** (e.g. `cncf-graduated`, `aws-eks-blueprints`), **License**, or **OpenSSF tier** (Strong / Moderate / Weak / Unknown). Categories OR within the axis; curators AND.

Filters are persisted in the URL, so `?mp_cat=security&mp_tier=strong` deep-links you to a specific slice.

Each card shows the chart name, a one-line description, the OpenSSF score badge, the license, the maintainers, and a docs link when one's published.

OpenSSF scores refresh once a day at 04:00 UTC against the public [Scorecard API](https://api.scorecard.dev). Scores are cached in-memory; on a fresh pod restart the catalog falls back to the bundled baseline scores until the next 04:00 UTC tick. If the Scorecard API is unreachable, the previous score sticks (no zeroing) and a warning is logged. The current refresh status is exposed via the Prometheus metrics `sharko_scorecard_refresh_total{status}` and `sharko_scorecard_last_refresh_timestamp`.

### Search ArtifactHub

Click **Search ArtifactHub** when you want a chart that isn't in our curated catalog. Type any name; results appear in two stacked sections:

- **Curated by Sharko** (top) — full catalog cards with the same Configure flow as Browse.
- **From ArtifactHub** (bottom) — slim cards tagged "ArtifactHub" with verified-publisher badge and star count when applicable.

Sharko proxies ArtifactHub server-side — your browser never calls them directly — so search results are cached for 10 minutes per query. If ArtifactHub is unreachable (network blocked, rate-limited, or down), the curated section still works and you'll see an amber banner: *"ArtifactHub unreachable — showing curated only."* with a **Retry connectivity** button that re-probes immediately.

When you click an ArtifactHub result, the Configure modal opens pre-filled with the chart name, repo URL, and (best-effort) license + maintainers fetched from ArtifactHub's package detail. The Submit & PR flow is identical to a curated entry.

Use **Browse** when you know what you want from our vetted set; use **Search** for the long tail.

### Paste Helm URL (power-user)

When the chart isn't in our curated catalog **and** isn't on ArtifactHub — internal repos, vendor charts hosted on a homepage CDN, or anything Sharko hasn't indexed — open the **Paste Helm URL** tab.

You provide the **chart repo URL** (e.g. `https://charts.jetstack.io`) and the **chart name** (e.g. `cert-manager`). Click **Validate** (or press <kbd>Enter</kbd>) and Sharko fetches `<repo>/index.yaml`, parses it, and confirms the chart exists. On success you see a green check with the version count and the latest stable version, plus the chart's description if the repo publishes one. Click **Configure** and the standard Configure modal opens pre-filled.

Validation errors are structured so you get a targeted hint, not a generic stack trace:

| Error | Meaning |
|-------|---------|
| **Repository unreachable** | The URL was syntactically valid but `<repo>/index.yaml` returned non-200 or didn't respond. Check the URL and that the repo is reachable from the Sharko server. |
| **Chart not found in this repo** | `index.yaml` was fetched and parsed, but no chart with that name exists. Names are case-sensitive and must match an entry under `entries:`. |
| **Repository index is malformed** | `index.yaml` was downloaded but isn't valid YAML. The repo is misconfigured. |
| **Validation timed out** | The repo took longer than 8 seconds. Retry; if persistent, check upstream. |

Optional **chart version** field auto-fills with the latest stable on validate; you can override it before clicking Configure to pin a specific version.

The Submit & PR flow from the Configure modal is identical to the other tabs — the audit detail just records `source=paste_url` so you can filter by origin in the audit log.

## Configure & submit

Clicking a card opens the **Configure** modal — a pre-filled form built from the curated entry's defaults:

- **Display name** — defaults to the canonical chart name. You can rename it (e.g. `cert-manager-eu`) if you want a second copy of the same chart with a different config.
- **Namespace** — defaults to the chart's recommended namespace.
- **Sync wave** — defaults to the chart's recommended position in the deploy ordering. Lower numbers deploy first.
- **Chart version** — populated from the chart's `index.yaml` via Sharko's catalog versions endpoint. Top-5 stable by default; tick **Show pre-releases** to see all.

### Submit & PR flow

When you click **Submit & open PR**, Sharko calls the existing `POST /api/v1/addons` endpoint — the same one used by the raw "Add Addon" form on the Catalog tab. The handler reuses v1.20's tiered Git plumbing:

1. **Tier 2 attribution.** The endpoint is registered as a Tier 2 (configuration) mutation, so Sharko prefers your personal GitHub PAT when one is configured (Settings → My Account). Without a PAT, the change is committed by the Sharko service account with a `Co-authored-by:` trailer for you, and an inline **AttributionNudge** banner appears next to Submit explaining the fallback.
2. **PR opens against `addons-catalog.yaml`.** A branch is created (default prefix `sharko/`), a commit lands with both the catalog entry and a generated `addons-global-values/<name>.yaml` (see step 2a), and a pull request is opened.

   **(2a) Smart values seeding (v1.21).** Sharko fetches the chart's upstream `values.yaml` for the version you picked and runs the [smart-values pipeline](smart-values.md) before committing: cluster-specific fields are commented out at their original position, a per-cluster template block is appended at the bottom, and a `# sharko: managed=true` header is stamped on the front. If the chart can't be fetched (registry unreachable, version not found), Sharko falls back to a minimal `<name>:\n  enabled: false` stub — you can refresh the file later from the Values tab once connectivity is back.
3. **Toast + persistent banner.** As soon as the PR URL comes back, a toast appears in the bottom-right (`PR #N opened →` or `PR #N merged →` if your connection auto-merges) and the modal grows a green banner with a clickable PR link so you can jump straight to GitHub. The toast and banner stay neutral about review state — auto-merge may have already fired server-side.
4. **Audit trail.** The action is recorded as the existing `addon_added` event with the originating subtab in the `source` detail field (`marketplace` for Browse, `artifacthub` for Search, `paste_url` for Paste Helm URL, `manual` for the legacy Add Addon form). Filtering audit by source surfaces Marketplace-driven additions vs. manual ones without inventing a new event name.

If the addon **already exists in your catalog**, both the modal's pre-flight check and the server's 409 response render the same friendly inline message — *"<name> is already in the catalog. Open its page to edit or enable it on a cluster."* — with a deep link to the existing addon's detail page. Submit stays disabled until you rename the candidate or close the modal. No no-op PRs are opened.
