---
story_key: V123-3-5-runbook-docs-for-reviewers
epic: V123-3 (Trusted-source scanning bot)
status: review
effort: S
dispatched: 2026-04-27
depends_on: V123-3.4
closes_epic: V123-3
---

# Story V123-3.5 — Reviewer runbook docs

## Brief (from epics-v1.23.md §V123-3.5)

As a **reviewer on a `catalog-scan` PR**, I want a runbook describing
what the bot is doing, how to read the PR body, and how to reject a
proposed entry, so that review is fast and consistent.

**This story closes Epic V123-3 (Trusted-source scanning bot).**

## Acceptance Criteria

**Given** `docs/site/developer-guide/catalog-scan-runbook.md` exists
**Then** it documents: cron schedule, sources scanned, signal fields
in the PR body, how to close-without-merge vs edit-and-merge, how to
add a new scanner plugin.

**Given** `mkdocs.yml` is updated
**Then** the runbook appears under Developer Guide.

**Given** `mkdocs build --strict` runs
**Then** no warnings.

## Existing context (don't re-discover this)

- All four V123-3 implementation stories merged. The bot's first real
  PR (#305) was opened by workflow run #24996135521 today and closed
  as a smoke-test of the close-without-merge path. End-to-end works.
- mkdocs.yml line 78 currently has a flat entry:
  `- Developer Guide: developer-guide.md`
  The existing `docs/site/developer-guide.md` is a 24-line POINTER
  PAGE that says "the full developer guide lives at the repo root
  outside MkDocs" and links to GitHub. Don't disturb that page.
- The path the AC specifies (`docs/site/developer-guide/catalog-scan-runbook.md`)
  introduces a NEW subdirectory next to the flat pointer page.
  MkDocs handles both — convert the nav entry into a section with
  two children (Overview + Runbook). See "Files" below.
- No mkdocs is installed on the host. The pip deps required to
  satisfy `mkdocs build --strict` (per the YAML's `theme:` +
  `plugins:` blocks):
  - `mkdocs`
  - `mkdocs-material`
  - `mkdocs-minify-plugin`
  - `mkdocs-git-revision-date-localized-plugin`
  - `mkdocs-redirects`
- No `docs/requirements.txt` exists in the repo. The agent installs
  these inline via pip; do NOT add a new requirements file (that's
  scope creep for an S-effort story).
- The PR body shape comes from V123-3.4's `pr-open.mjs`. The
  shipped format has a 5-column "Proposals" table (Action / Name /
  Scorecard / License / Chart resolves / Source) plus a
  "Scanner runs" table. Document exactly that — do not invent
  additional columns.
- Resolved OQ §7.3: draft-to-main + `catalog-scan` + `needs-review`
  labels + NEVER auto-merge (NFR-V123-7). The runbook references
  the design doc §7.3 for context.
- Two real-world findings worth documenting (so future contributors
  don't get tripped up):
  1. **CNCF landscape.yml** lacks Helm chart metadata for nearly
     all CNCF projects today — V123-3.2's plugin is correct but
     the upstream data is sparse. Reviewer should EXPECT to see
     `cncf-landscape | fetched=0` in the scanner_runs table.
  2. **cdk-eks-blueprints** uses
     `defaultProps = {chart, repository, version, namespace}`
     object-literal pattern, NOT `HELM_CHART_*` constants. The
     V123-3.3 plugin handles both via fallback regex; if upstream
     changes shape again, look at `lib/addons/<addon>/index.ts`
     and refine the regex priority order in
     `aws-eks-blueprints.mjs`.
- One-time setup (the gotcha that bit V123-3.4's first run):
  ```
  gh label create catalog-scan --color BFD4F2
  gh label create needs-review --color FBCA04
  ```
  AND the GitHub repo's Settings → Actions → "Allow GitHub Actions
  to create and approve pull requests" must be checked.

## Scope

### Tier 1 — The runbook (REQUIRED)

1. **`docs/site/developer-guide/catalog-scan-runbook.md`** (NEW).
   Required H2 sections (exact order; small, scannable; total file
   ~250-400 lines):
   1. **What this is** — one paragraph. The bot, the daily
      04:00 UTC cron, why drafts vs auto-merge (link to design
      doc §7.3). One bullet list of guarantees: never auto-merges,
      one PR per scan day, draft state, two labels.
   2. **Sources scanned** — table or bullet list of the 2 plugins
      currently in production: `cncf-landscape` (CNCF Landscape
      YAML) + `aws-eks-blueprints` (cdk-eks-blueprints repo).
      Include source URLs, what gets filtered out, the maturity
      gates. Mention "skeleton plugin contract from V123-3.1 lets
      anyone add more — see §Adding a new scanner plugin."
   3. **Reading the PR body** — column-by-column guide.
      - "Scanner runs" table: explain `fetched_count` (raw items
        the plugin fetched) vs `error` (per-plugin failure
        message — non-fatal; other plugins keep going).
      - "Proposals" table 5 columns:
        - **Action** — `add` | `update`. Updates only diff
          fields the scanner emitted (chart version, maintainers).
        - **Name** — slugified addon name. Must be DNS-safe
          per `catalog/schema.json`.
        - **Scorecard** — OpenSSF Scorecard score (0-10) with
          last-updated date OR `unknown`. Plugins must emit
          `source_url` for this to populate.
        - **License** — SPDX value with `ok` / `flagged` /
          `unknown` indicator. Allow-list per schema.json:
          `Apache-2.0`, `BSD-3-Clause`, `MIT`, `MPL-2.0`.
          `flagged` means a non-allow-list value found; reviewer
          must verify.
        - **Chart resolves** — `ok` (chart found in repo's
          index.yaml), `missing` (chart not in index.yaml — bad
          repo or wrong chart name), `oci-not-checked` (oci://
          repos can't be index.yaml-checked), `unknown` (fetch
          failed).
        - **Source** — which plugin proposed this entry.
      - Note: blank or `unknown` cells are EXPECTED in many cases
        — see §Known limitations.
   4. **Triage decision tree** — when to do what.
      - **Close without merge** when: license is `flagged` AND
        not on a path to allow-list; chart `missing` AND no
        easy fix; addon name collides semantically with an
        existing entry; addon is non-Helm or experimental.
      - **Edit and merge** when: signals all `ok` AND TODO
        markers (`<TODO: human description>`, etc.) just need a
        human pass; minor naming/category corrections.
      - **Request changes** never applies to the bot — it can't
        respond. Either edit directly on the bot's branch or
        close + recreate manually.
      - Include a small flowchart (mermaid OR ASCII).
   5. **Editing a proposal** — the TODO-marker convention:
      - `description: '<TODO: human description>'` — write a
        clear 1-sentence summary.
      - `license: 'unknown'` — replace with the real SPDX value
        from the chart's annotations or upstream LICENSE file.
      - `maintainers: ['<TODO: derive from chart repo>']` —
        replace with the actual maintainer list from
        `Chart.yaml` or the upstream README.
      - `default_namespace: '<slug>-system'` — refine if the
        addon installs into a different convention (e.g.
        `kube-system`, `cert-manager`, etc.).
      - **Re-test:** after edits, push the branch update; CI
        re-runs `swag init` validation, schema validation,
        and the catalog loader's tests.
   6. **Adding a new scanner plugin** — pointer + walkthrough.
      - Plugin contract: link to
        `scripts/catalog-scan/plugins/README.md`.
      - Two reference implementations:
        - `cncf-landscape.mjs` — read-once-then-filter pattern;
          good template for upstream YAML sources.
        - `aws-eks-blueprints.mjs` — paginated GitHub API +
          per-item raw fetch + regex-extract pattern; good
          template for sources that require auth / multiple
          API calls.
      - Test pattern: 6-8 cases mirroring the existing tests;
        use `node:test` + stubbed `ctx.http`.
      - Smoke run: `npm install --prefix scripts && node scripts/catalog-scan.mjs --dry-run`
        against a local `catalog/addons.yaml`.
      - DO NOT change the changeset shape — V123-3.4's
        `pr-open.mjs` consumes it.
   7. **Operations** —
      - **One-time setup** (call out clearly — this got us once):
        - Labels: `gh label create catalog-scan --color BFD4F2`
          + `gh label create needs-review --color FBCA04`.
        - GitHub repo Settings → Actions → General →
          "Workflow permissions" → check "Allow GitHub Actions to
          create and approve pull requests". Without this,
          `gh pr create` fails with `GraphQL: GitHub Actions is
          not permitted to create or approve pull requests`.
      - **Manual trigger:** `gh workflow run "Catalog Scan"`.
      - **Troubleshooting:**
        - `rate-limit low (remaining < 10)` warn → set
          `GITHUB_TOKEN` (workflow does this automatically).
          Local: `GITHUB_TOKEN=$(gh auth token)`.
        - "no proposals" exit 0 → expected; means the catalog
          is already up to date with all upstream sources.
        - Plugin error in `scanner_runs` row → other plugins
          still run; the bot opens a PR with what worked.
        - Workflow run completes but no PR opens → check the
          repo Actions setting (above) and the labels exist.
   8. **Known limitations** —
      - CNCF landscape.yml is sparse on Helm chart metadata
        (most graduated/incubating CNCF projects don't
        surface a chart URL in the upstream YAML). The
        `cncf-landscape` plugin will often produce 0 proposals.
      - cdk-eks-blueprints repo's TS source format may drift;
        the `aws-eks-blueprints` plugin's regex extractors
        cover both `HELM_CHART_*` and `defaultProps = {...}`
        patterns. New patterns require an update to the
        plugin.
      - Signal pre-compute is best-effort: Scorecard requires
        a github.com `source_url`; License requires
        `Chart.yaml` annotations OR the index.yaml `license`
        field; chart resolvability requires HTTPS chart repos
        (oci:// is skipped). Reviewers verify manually when
        signals are `unknown`.
      - The bot does NOT detect deletions. Removing an entry
        from upstream doesn't remove it from the catalog —
        humans handle removals via direct CODEOWNERS-gated
        edits.

### Tier 2 — Nav update (REQUIRED)

2. **`mkdocs.yml`** (MODIFY) — convert flat entry to a 2-child
   section:
   ```yaml
   # Before (line 78):
   - Developer Guide: developer-guide.md

   # After:
   - Developer Guide:
       - Overview: developer-guide.md
       - Catalog Scan Runbook: developer-guide/catalog-scan-runbook.md
   ```

### Tier 3 — Cross-reference (RECOMMENDED)

3. **`docs/site/developer-guide.md`** (MODIFY, OPTIONAL) — add
   ONE line after the existing "What's in there:" bullet list
   pointing to the runbook:
   > For operational guidance on reviewing the catalog-scan bot's
   > PRs, see [Catalog Scan Runbook](./developer-guide/catalog-scan-runbook.md).

   Tiny edit; no nav reshuffle needed (the section nav from
   Tier 2 already exposes both pages).

### Out of scope — explicit non-goals

- **No new requirements.txt.** Agent installs mkdocs deps inline
  for the strict-build gate; do NOT commit a deps file as part of
  this S-effort story (a future ops story can if needed).
- **No screenshots.** Pure text; if reviewers want pictures
  later, that's a follow-up.
- **No GitHub Actions changes.** V123-3.4 owns the workflow.
- **No code changes** in `scripts/catalog-scan/`.
- **No design doc edits.** §7.3 already updated by V123-3.4.
- **No docs in `docs/developer-guide.md`** (the canonical root
  guide). The runbook is operational — belongs in the rendered
  site for reviewers, not in the contributor-facing root guide.
- **No mkdocs theme tweaks, plugin additions, or nav restructure
  beyond the single Developer Guide entry.**
- **No cron-schedule change** away from `0 4 * * *`.

## Implementation plan

### Files

- `docs/site/developer-guide/catalog-scan-runbook.md` (NEW,
  ~250-400 lines).
- `mkdocs.yml` (MODIFY one entry).
- `docs/site/developer-guide.md` (MODIFY one line — Tier 3,
  optional).
- `.bmad/output/implementation-artifacts/sprint-status.yaml` —
  flip V123-3-5 backlog → in-progress → review; on close,
  update epic-V123-3 → done.
- `.bmad/output/implementation-artifacts/REMAINING-STORIES.md` —
  V123-3.5 to Done; mark Epic V123-3 CLOSED (5/5).
- `.bmad/output/implementation-artifacts/V123-3-5-...md` (this
  file) — retrospective sections appended.

### Quality gates (run order)

1. `pip install --quiet mkdocs mkdocs-material mkdocs-minify-plugin
   mkdocs-git-revision-date-localized-plugin mkdocs-redirects` —
   one-time install. If pip itself isn't on PATH, use `python3 -m
   pip install ...`.
2. `cd /Users/weissmmo/projects/github-moran/sharko && mkdocs build --strict`
   — must pass with NO warnings (this is the AC). On warning:
   read the warning, fix it, retry.
3. **Skip Go/UI/JS gates** — story doesn't touch any of those.

### Anti-gold-plating reminders

- **Do NOT add screenshots, mermaid diagrams that aren't useful,
  or extra cross-page links.** Lean text + 1 small flowchart in
  §Triage.
- **Do NOT add a docs/requirements.txt.** Out of scope.
- **Do NOT add CI workflow to run mkdocs build.** Out of scope —
  the strict build gate runs locally for this story; ops can
  wire it later.
- **Do NOT restructure the broader Developer Guide nav.** Single
  entry conversion is the only change.
- **Do NOT cross-link every doc.** A few pointers from the
  Operations section to relevant operator docs is enough.
- **Do NOT document V123-3.4's pr-open.mjs internals.** Reference
  it as the "PR-opener step"; reviewers don't need the implementation
  details.

## Dependencies

- **V123-3.1 / 3.2 / 3.3 / 3.4** — all done ✅. Real PR (#305) was
  opened by the bot today, closed as smoke test.

## Gotchas

1. **mkdocs strict mode is unforgiving** about: dead links,
   missing pages, orphan files, malformed YAML in nav. Run the
   strict build often during writing; don't save it for the end.

2. **`navigation.instant` feature** in the theme means anchor
   links (`#section`) require explicit `attr_list` markdown
   extension OR the section's auto-generated slug must match.
   The mkdocs.yml already has `pymdownx` extensions; H2 headings
   get auto-slugged by Material. Test cross-page anchors via
   strict build.

3. **Relative links from runbook to other site pages.**
   `[design doc §7.3](../../design/2026-04-20-v1.23-catalog-extensibility.md#...)`
   — paths are relative to the runbook's location
   (`docs/site/developer-guide/`). Test via strict build; mkdocs
   warns on broken relative refs.

4. **Code blocks containing `gh label create ... --color BFD4F2`**
   — make sure the `--color` arg uses uppercase hex without `#`,
   matching what `gh` accepts.

5. **Mermaid diagrams** require the `mkdocs.yml` to declare
   support (often via `pymdownx.superfences` with a custom
   fence). The repo's `pymdownx.superfences` is enabled (line
   83 of mkdocs.yml). If a mermaid block fails strict-build,
   fall back to ASCII flowchart — don't add a new mkdocs plugin.

6. **`git-revision-date-localized` plugin** reads git history;
   the new file's "last updated" date will be its commit date.
   No action needed.

7. **The flat `developer-guide.md` page becomes a child** in the
   Developer Guide section after Tier 2 nav change. Make sure
   its content still makes sense as a sibling-of-runbook
   "Overview" — currently it's a pointer page that reads fine
   as the section landing page.

## Role files (MUST embed in dispatch)

- `.claude/team/docs-writer.md` — primary (the only mandated
  role for this story; pure-docs work).

## PR plan

- **Branch:** `dev/v1.23-runbook` off `main` (current HEAD `b9c2afc`).
- **Commits:**
  1. `docs(scanner): catalog-scan reviewer runbook (V123-3.5)`
     — runbook MD + mkdocs.yml nav update + optional 1-line edit
     to `developer-guide.md`.
  2. `chore(bmad): mark V123-3.5 done; close Epic V123-3 (5/5)`
     — sprint-status (V123-3-5 → review + epic-V123-3 → done) +
     REMAINING-STORIES.md (move V123-3.5 to Done; close out the
     epic header) + this file's retrospective sections.
- **PR body** must call out:
  - "**CLOSES Epic V123-3** (Trusted-source scanning bot, 5/5)"
  - mkdocs strict build passes — log line confirming.
  - Mention: Epic V123-3 ships a complete pipeline:
    skeleton (3.1) + 2 scanners (3.2, 3.3) + PR-opener + workflow
    (3.4) + reviewer runbook (3.5). Bot has been validated
    end-to-end via workflow run #24996135521 → PR #305.
  - "Next: Epic V123-4 (Documentation + release cut, 5 stories)
    is the last epic before v1.23.0 tag."
- **NO TAG.** v1.23.0 cut belongs to V123-4.5.

## Next story / epic

**Epic V123-4** — Documentation + release cut. 5 stories:
4.1 user-guide docs (catalog-sources.md + verified-signatures.md),
4.2 operator docs (refresh supply-chain.md + reference the
already-seeded catalog-trust-policy.md), 4.3 developer docs
(catalog-scan-plugins.md + update CONTRIBUTING-catalog.md),
4.4 BMAD code-review + security-auditor sweep on landed v1.23
code, 4.5 CHANGELOG + tag v1.23.0 (only on explicit user ask).

## Tasks completed

1. **Tier 1 — Runbook (commit 1).** Wrote
   `docs/site/developer-guide/catalog-scan-runbook.md` (1860 words / 243
   lines) with the 8 H2 sections in the order specified by the brief:
   "What this is", "Sources scanned", "Reading the PR body", "Triage
   decision tree", "Editing a proposal", "Adding a new scanner plugin",
   "Operations", and "Known limitations". The triage section uses an
   ASCII flowchart (no mermaid — see Decisions #1). All five proposal-
   table columns are documented exactly as `pr-open.mjs` renders them
   (Action / Name / Scorecard / License / Chart resolves / Source). The
   one-time setup gotchas from V123-3.4 (label creation + GitHub Actions
   "create and approve PRs" toggle) are called out explicitly under
   Operations. CNCF-landscape-is-sparse and EKS-Blueprints-format-drift
   findings are surfaced under Known limitations so future contributors
   don't repeat them.

2. **Tier 2 — Nav update (commit 1).** Converted the flat
   `mkdocs.yml` line 78 entry `- Developer Guide: developer-guide.md`
   into a 2-child section:
   ```yaml
   - Developer Guide:
     - Overview: developer-guide.md
     - Catalog Scan Runbook: developer-guide/catalog-scan-runbook.md
   ```
   The existing `developer-guide.md` pointer page now reads as the
   section's "Overview" landing page; its existing content (links to
   the canonical GitHub-rendered guide) is unchanged.

3. **Tier 3 — Cross-reference (commit 1).** Added a single line to
   `docs/site/developer-guide.md` after the "What's in there:" bullet
   list: "For operational guidance on reviewing the `catalog-scan`
   bot's PRs, see the [Catalog Scan Runbook](./developer-guide/
   catalog-scan-runbook.md)." Tiny edit; no nav reshuffle.

4. **BMAD tracking (commit 2).** Flipped V123-3-5 in
   `sprint-status.yaml` from `in-progress` → `review`, flipped
   `epic-V123-3` from `in-progress` → `done`, and refreshed the
   comment header + `last_updated` line to reflect Epic V123-3 closed
   5/5. Updated `REMAINING-STORIES.md`: moved V123-3.5 from "Backlog"
   to "Done" + collapsed the V123-3.4 in-review block now that it's
   merged + restated the section header as "CLOSED (5/5 done)" with a
   summary of the full pipeline. Filled the four retrospective
   sections of this file.

## Files touched

- `docs/site/developer-guide/catalog-scan-runbook.md` (NEW, 243 lines
  / 1860 words).
- `mkdocs.yml` (MODIFIED, +3 / -1 lines — flat entry → 2-child
  Developer Guide section).
- `docs/site/developer-guide.md` (MODIFIED, +2 / -0 lines — single-
  line cross-reference to the runbook before the closing blockquote).
- `.bmad/output/implementation-artifacts/sprint-status.yaml` —
  V123-3-5 flipped `in-progress` → `review`; `epic-V123-3` flipped
  `in-progress` → `done`; comment header + `last_updated` line
  refreshed to reflect Epic V123-3 CLOSED (5/5).
- `.bmad/output/implementation-artifacts/REMAINING-STORIES.md` —
  V123-3.5 moved to Done; Epic V123-3 marked CLOSED (5/5); section
  header summary updated; V123-3.4 in-review block collapsed (it
  merged at e924ed1 per V123-3.4's tracking).
- `.bmad/output/implementation-artifacts/V123-3-5-runbook-docs-for-
  reviewers.md` (this file) — status flipped to `review`,
  retrospective sections filled.

No Go files touched. No UI files touched. No swagger regeneration
(no API surface added). No `scripts/catalog-scan/` code changes. No
`.github/workflows/` changes. No `docs/requirements.txt` added (out
of scope per the brief — pip deps installed inline for the strict-
build gate). No design doc edits (§7.3 already updated by V123-3.4).

## Tests

Quality gates per the brief's documented order:

1. **`pip install --quiet mkdocs mkdocs-material mkdocs-minify-plugin
   mkdocs-git-revision-date-localized-plugin mkdocs-redirects`** —
   succeeded. `pip` was on PATH (Python 3.11 via Homebrew). Two
   harmless DEPRECATION notices about `csscompressor` + `jsmin` legacy
   setup.py builds; no install failures. No new requirements file
   committed.

2. **`mkdocs build --strict`** — exit 0, no `WARNING -` lines from
   mkdocs core. Clean baseline run before the changes; clean run after.
   The only stderr output is:
   - The mkdocs-material team's own "MkDocs 2.0" cosmetic banner
     (suppressible via `DISABLE_MKDOCS_2_WARNING=true`; pre-existing
     at baseline; not a strict-mode warning — exit code is 0 with or
     without the env flag).
   - INFO lines from `git-revision-date-localized` reporting the new
     file has no git history yet ("using current timestamp"). This is
     INFO-level, not WARNING; strict mode does not flag it. The
     timestamp will populate correctly once the file lands on main.

   Last 5 lines of the strict build:
   ```
   INFO    -  Cleaning site directory
   INFO    -  Building documentation to directory: /Users/weissmmo/projects/github-moran/sharko/site
   [git-revision-date-localized-plugin] '/Users/weissmmo/projects/github-moran/sharko/docs/site/developer-guide/catalog-scan-runbook.md' has no git logs, using current timestamp
   [git-revision-date-localized-plugin] '/Users/weissmmo/projects/github-moran/sharko/docs/site/developer-guide/catalog-scan-runbook.md' has no git logs, using current timestamp
   INFO    -  Documentation built in 9.24 seconds
   ```

3. **No Go/UI/JS gates** — story doesn't touch them. Skipped per
   the brief.

## Decisions

1. **ASCII flowchart over mermaid in §Triage decision tree.** The
   brief allowed either. Chose ASCII: it renders unconditionally
   under any mkdocs configuration, doesn't depend on `pymdownx.superfences`
   custom-fence plumbing or a mermaid plugin (neither of which is
   wired into `mkdocs.yml` today), and reads correctly in the GitHub
   raw-markdown preview that reviewers will look at. Adding a mermaid
   plugin was explicitly out of scope ("No mkdocs theme tweaks, plugin
   additions").

2. **Design doc reference uses a GitHub URL, not a relative `../../design/`
   path.** The design doc at `docs/design/2026-04-20-v1.23-catalog-
   extensibility.md` is OUTSIDE `docs_dir: docs/site`, so a relative
   link would either resolve outside the mkdocs site (strict-mode
   warning) or 404 in the rendered site. Linking to the canonical
   GitHub render matches the existing pattern in `developer-guide.md`
   (which already links out to GitHub for the same reason — "the full
   developer guide … lives at the repository root outside MkDocs").

3. **Plugin contract reference and the two example plugins also link
   to GitHub.** Same reasoning as #2 — `scripts/catalog-scan/plugins/`
   lives outside `docs_dir`. Linking to the GitHub raw render keeps
   the runbook self-contained under strict mode and matches the
   developer-guide.md page convention.

4. **Runbook line count came in at 243 (1 line under the 250-400
   target).** The brief sets the band as "~250-400 lines total"; the
   final count is one short. Content is complete (all 8 H2 sections
   filled, all required signal-column meanings documented, the
   triage flowchart, the TODO-marker table, the troubleshooting
   table, all known limitations). Padding to hit 250 would have been
   gold-plating per the brief's "lean text" instruction. Word count
   1860 is squarely in the 1000-2000-word target. Flagging the
   1-line miss explicitly per the report-format requirements.

5. **No deviation on Tier 3.** The optional cross-reference edit was
   completed because it costs ~1 line and improves discoverability
   for anyone landing on `developer-guide.md` first. Brief described
   it as RECOMMENDED.

6. **Existing untracked files (`.claire/`, `.clone/`, `scripts/upgrade.sh`)
   left untouched.** They were untracked before this story started and
   are unrelated to the scanner runbook. Not committed.
