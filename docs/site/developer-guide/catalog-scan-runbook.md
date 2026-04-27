# Catalog Scan Runbook

Operational guide for reviewers of `catalog-scan` bot PRs.

The bot was introduced in **v1.23** (Epic V123-3). It opens one **draft PR per scan day** with proposed catalog additions and updates derived from upstream sources. Reviewers triage these PRs — the bot **never** auto-merges.

---

## What this is

`catalog-scan` is a daily, stateless GitHub Actions workflow (`.github/workflows/catalog-scan.yml`) that:

1. Runs `node scripts/catalog-scan.mjs` against `catalog/addons.yaml`.
2. Each registered plugin fetches an upstream source (CNCF Landscape, AWS EKS Blueprints, …) and emits normalized catalog entries.
3. A diff against the existing catalog produces a **changeset** (`adds[]` + `updates[]`).
4. If the changeset is non-empty, `scripts/catalog-scan/pr-open.mjs` pre-computes review signals (Scorecard, license allow-list, chart resolvability) and opens a single draft PR with labels `catalog-scan` + `needs-review`.

Design context: see [v1.23 catalog extensibility design doc §7.3](https://github.com/MoranWeissman/sharko/blob/main/docs/design/2026-04-20-v1.23-catalog-extensibility.md) — draft-to-main + label gating + NEVER auto-merge per NFR-V123-7.

**Guarantees:**

- Cron schedule: `0 4 * * *` (daily 04:00 UTC). `workflow_dispatch` for manual runs.
- Stateless — every run is independent; no caching between runs.
- One open PR at a time. The concurrency guard skips opening if a `catalog-scan`-labeled PR is already open OR the target branch already exists.
- Draft PR only. Labels `catalog-scan` + `needs-review` always applied.
- Workflow permissions are exactly `contents: write` + `pull-requests: write`. No `automerge` permission, no `actions: write`.
- Commit author is `Moran Weissman <moran.weissman@gmail.com>`; the PR is opened by `github-actions[bot]`.

---

## Sources scanned

Two production plugins ship with v1.23. Both can produce zero proposals on a given run; that's normal (see [Known limitations](#known-limitations)).

| Plugin | Upstream source | Notes |
|---|---|---|
| `cncf-landscape` | [`cncf/landscape` `landscape.yml`](https://github.com/cncf/landscape) | Filters to projects that surface a Helm chart URL. Most CNCF projects don't, so the typical `fetched_count` is **0**. |
| `aws-eks-blueprints` | [`aws-quickstart/cdk-eks-blueprints`](https://github.com/aws-quickstart/cdk-eks-blueprints) `lib/addons/<addon>/index.ts` | Paginates the GitHub API, fetches each `index.ts` raw, regex-extracts chart constants. Handles both `HELM_CHART_*` constants and the `defaultProps = {chart, repository, version, namespace}` object-literal pattern via fallback regex priority. |

Adding a third plugin is a contained change — see [Adding a new scanner plugin](#adding-a-new-scanner-plugin).

Maturity / trust gates are intentionally LOW: the scanner only emits *proposals*. Reviewers (you) are the trust gate. The plugin contract requires plugins to emit normalized entries that conform to `catalog/schema.json`, but TODO markers are allowed in `description`, `license`, and `maintainers` — see [Editing a proposal](#editing-a-proposal).

---

## Reading the PR body

The PR body has two markdown tables. Reviewer attention should be on **Proposals**.

### Scanner runs table

| Column | Meaning |
|---|---|
| `Plugin` | Plugin name from `scripts/catalog-scan/plugins/*.mjs`. |
| `Fetched` | Raw item count the plugin produced (pre-diff). `0` is common for `cncf-landscape`. |
| `Error` | Per-plugin failure message. **Non-fatal** — other plugins still run, and a partial PR is still opened. |

### Proposals table

Five columns — read left to right.

| Column | Values | Reviewer interpretation |
|---|---|---|
| **Action** | `add`, `update` | `update` only diffs the fields the scanner emitted (typically `version`, sometimes `maintainers`). All other fields on the existing entry are untouched. |
| **Name** | Slugified addon name | Must be DNS-safe per `catalog/schema.json`. Reviewer checks for semantic collision with an existing entry. |
| **Scorecard** | `<0..10> · <YYYY-MM-DD>` or `unknown` | OpenSSF Scorecard score for the addon's source repo. Populated only when the plugin emits a `source_url` pointing to github.com. `unknown` is **expected** for most EKS Blueprints adds (their `repo:` is a vendor Helm URL, not github.com). |
| **License** | `Apache-2.0 (ok)`, `MIT (ok)`, `<other> (flagged)`, `unknown` | Read from the chart's `index.yaml` (top-level `license` field OR `annotations['artifacthub.io/license']`). Allow-list per `catalog/schema.json`: `Apache-2.0`, `BSD-3-Clause`, `MIT`, `MPL-2.0`. `flagged` means a non-allow-list value found — reviewer must verify or close. |
| **Chart resolves** | `ok`, `missing`, `oci-not-checked`, `unknown` | Whether the chart is fetchable. `ok` = found in the repo's `index.yaml`. `missing` = fetched but not in `entries[chartName]` (bad repo URL or wrong chart name). `oci-not-checked` = `oci://` repos can't be index.yaml-checked (skipped, not a problem). `unknown` = fetch failed. |
| **Source** | Plugin name | Which plugin proposed this entry. |

> **Blank or `unknown` cells are expected** in many cases — the bot tries to populate signals but never fabricates them. See [Known limitations](#known-limitations).

---

## Triage decision tree

For each proposed row, decide:

```
                       Proposal row
                            │
                ┌───────────┴───────────┐
                │                       │
        License == flagged?      Chart resolves == missing?
        Or non-Helm/experimental? │  And no obvious fix?
                │                       │
               yes                     yes
                │                       │
                └───────────┬───────────┘
                            │
                   CLOSE WITHOUT MERGE
                            │
                            └─► reopen manually if upstream fixes it

                     All signals green
                     (Scorecard ok, License ok or unknown-with-justification,
                      Chart resolves ok)
                            │
                  Are there TODO markers
                  to fill in?
                  (description, license=unknown, maintainers placeholders)
                            │
                ┌───────────┴───────────┐
                │                       │
               yes                     no
                │                       │
        EDIT bot's branch        EDIT (cosmetic / category)
        + push (CI re-runs)       and MERGE
                │
                └─► MERGE when CI green
```

### Close without merge

Use this path when:

- License is `flagged` AND there is no quick path to allow-listing.
- Chart resolves `missing` AND there is no obvious correct repo / chart name.
- Addon name collides semantically with an existing catalog entry.
- Addon is non-Helm (CRD-only, operator without a chart, manual-install).
- Addon is clearly experimental / unmaintained / incompatible with Sharko's curated tier.

Closing the PR also closes the bot's branch from the reviewer's perspective. Tomorrow's scan will produce a fresh PR; if you want to *prevent* a re-proposal, raise an issue or add the entry to a deny-list (V2 hardening — not yet implemented).

### Edit and merge

Use this path when signals are green and the proposal is acceptable. The expected work:

- Replace TODO markers (see [Editing a proposal](#editing-a-proposal)).
- Tweak `category` / `default_namespace` / `description` to match catalog conventions.
- Push to the bot's branch (`catalog-scan/<YYYY-MM-DD>`). CI re-runs schema validation and the catalog loader's tests.
- Mark the PR ready-for-review and merge once green.

### Request changes — does NOT apply

The bot can't respond to PR review comments. Either edit the bot's branch directly OR close + recreate the entry manually via a normal contributor PR.

---

## Editing a proposal

The scanner emits TODO markers in fields it cannot reliably populate. Each marker is schema-valid (so the catalog loader passes), but is clearly intent-marking for reviewers:

| Field | Marker emitted | Reviewer action |
|---|---|---|
| `description` | `'<TODO: human description>'` | Write a clear 1-sentence summary (what the addon does, who installs it). |
| `license` | `'unknown'` | Replace with the real SPDX value from the chart's `Chart.yaml` annotations OR the upstream LICENSE file. |
| `maintainers` | `['<TODO: derive from chart repo>']` | Replace with the actual maintainer list from `Chart.yaml` `maintainers:` OR the upstream README. |
| `default_namespace` | `'<slug>-system'` | Refine if the addon installs into a different convention (`kube-system`, `cert-manager`, etc.). |

After edits, push the branch update. CI runs:

- Schema validation against `catalog/schema.json`.
- The Go catalog loader's tests (`go test ./internal/catalog/...`).
- The scanner's own test suite (regression check on `lib/yaml-edit.mjs`).

When CI is green and the entries look right, mark the PR ready-for-review and merge.

---

## Adding a new scanner plugin

The plugin contract is documented in [`scripts/catalog-scan/plugins/README.md`](https://github.com/MoranWeissman/sharko/blob/main/scripts/catalog-scan/plugins/README.md). Highlights:

- A plugin is a `.mjs` file exporting `name` (string) and `async fetch(ctx)` returning normalized entries.
- `ctx.http` is fetch-with-retries (1s/2s/4s backoff) + UA `sharko-catalog-scan/1.0`. Plugins MUST use `ctx.http`, never bare `fetch`.
- `ctx.logger` is the JSON-lines stderr logger. Use `ctx.logger.child({plugin: name})` for nested context.

Two reference implementations:

- [`cncf-landscape.mjs`](https://github.com/MoranWeissman/sharko/blob/main/scripts/catalog-scan/plugins/cncf-landscape.mjs) — read-once-then-filter pattern; good template for upstream YAML/JSON sources.
- [`aws-eks-blueprints.mjs`](https://github.com/MoranWeissman/sharko/blob/main/scripts/catalog-scan/plugins/aws-eks-blueprints.mjs) — paginated GitHub API + per-item raw fetch + regex-extract pattern; good template for sources requiring auth or multiple API calls.

Test pattern: 6-8 `node:test` cases mirroring the existing tests. Stub `ctx.http` and `ctx.logger`; never make real network calls in tests.

Smoke run before opening a PR:

```bash
npm install --prefix scripts
GITHUB_TOKEN=$(gh auth token) node scripts/catalog-scan.mjs --catalog catalog/addons.yaml
node scripts/catalog-scan/pr-open.mjs --dry-run
```

The dry-run prints the markdown PR body to stdout so you can verify your plugin's entries render correctly.

> **Do NOT change the changeset shape.** `pr-open.mjs` consumes a stable JSON contract (`schema_version: '1.0'`, `scanner_runs[]`, `adds[]`, `updates[]`). New plugins emit normalized entries; the diff is computed centrally.

---

## Operations

### One-time setup

These got us once during V123-3.4 — confirm before relying on the bot.

**Repo labels.** `gh pr create --label` fails if the label doesn't exist:

```bash
gh label create catalog-scan --color BFD4F2
gh label create needs-review --color FBCA04
```

**Workflow permissions.** GitHub repo Settings → Actions → General → Workflow permissions → check **"Allow GitHub Actions to create and approve pull requests"**. Without this, `gh pr create` fails with:

```
GraphQL: GitHub Actions is not permitted to create or approve pull requests
```

### Manual trigger

```bash
gh workflow run "Catalog Scan"
```

### Local dry-run preview

Reviewers can preview tomorrow's PR body locally:

```bash
make catalog-scan-pr   # runs scanner + pr-open.mjs --dry-run
```

### Troubleshooting

| Symptom | Cause / fix |
|---|---|
| `rate-limit low (remaining < 10)` warning in scanner logs | Set `GITHUB_TOKEN`. Workflow does this automatically. Local: `GITHUB_TOKEN=$(gh auth token)`. |
| Workflow run completes, exit 0, no PR opened | Either zero proposals (expected — catalog matches upstream), OR the concurrency guard tripped (open `catalog-scan` PR already exists, OR today's branch already exists). Check `gh pr list --label catalog-scan --state open`. |
| Workflow fails with `GraphQL: GitHub Actions is not permitted...` | Repo Actions setting (above) — flip the toggle. |
| Workflow fails with `could not find label` | Labels missing — create them (above). |
| Plugin error appears in `scanner_runs` table but PR opens anyway | By design. Other plugins ran. The failed plugin's contribution is missing; investigate via the workflow run log. |
| `cncf-landscape | fetched=0` | Expected. CNCF landscape.yml is sparse on Helm metadata. Not a bug. |

---

## Known limitations

- **CNCF landscape.yml is sparse on Helm chart metadata.** Most graduated/incubating CNCF projects don't surface a chart URL in the upstream YAML. The `cncf-landscape` plugin will often produce **0 proposals**. Future plugins (or upstream landscape.yml enrichment) will close this gap.
- **`cdk-eks-blueprints` source format may drift.** The `aws-eks-blueprints` plugin's regex extractors cover both `HELM_CHART_*` constants and the `defaultProps = {...}` object-literal pattern. New patterns in upstream would require updating the regex priority order in `aws-eks-blueprints.mjs`.
- **Signal pre-compute is best-effort.** Scorecard requires a github.com `source_url`; license requires `Chart.yaml` annotations OR an `index.yaml` `license` field; chart resolvability requires `https://` chart repos (`oci://` is skipped). Reviewers verify manually when signals are `unknown`.
- **The bot does NOT detect deletions.** Removing an entry from upstream does not remove it from the catalog. Humans handle removals via direct CODEOWNERS-gated edits.
- **No state between runs.** The bot doesn't remember that you closed yesterday's proposal for `foo`. If `foo` still appears in upstream tomorrow, it will be re-proposed. (V2 hardening: persistent deny-list — not yet implemented.)
- **Stateless concurrency guard has a small race window.** Two scheduled runs firing within seconds (e.g. `workflow_dispatch` + `cron`) could both pass the open-PR check. Acceptable risk — duplicate PRs are reviewer-visible; the second is closed immediately.
