---
stepsCompleted: [1, 2, 3]
inputDocuments:
  - User dispatch 2026-05-21 — "cut v1.25.0-pre.0 tag bundling V125-1-9 + V125-1-8 + interim V125-1-10/11/13.x/13.y + V126-1..5. Requires Chart.yaml bump + release notes consolidation + RC stabilization."
  - charts/sharko/Chart.yaml (currently version=1.19.0, appVersion="1.19.0" — STALE relative to last published tag v1.23.0-pre.0)
  - docs/site/release-notes.md (canonical release notes file, already wired in mkdocs nav at line 3)
  - main HEAD: da298893 — chore(sprint-status): mark V125-1-8 (cluster reconciler) done + planning artifact (#349)
  - No existing v1.25.* tag (verified: `git tag -l 'v1.25*'` returns empty)
workflowType: 'release-cut'
project_name: 'sharko'
version: 'v1.25.0-pre.0'
user_name: 'Moran'
date: '2026-05-21'
---

# Release v1.25.0-pre.0 — Schema envelope + cluster reconciler + DX bundle

## Sprint goal (verbatim, locked 2026-05-21)

> "cut v1.25.0-pre.0 tag bundling V125-1-9 + V125-1-8 + interim V125-1-10/11/13.x/13.y + V126-1..5. Requires Chart.yaml bump + release notes consolidation + RC stabilization." — Moran

Two-phase sprint: 1 agent-dispatched story (bump + notes) → 1 orchestrator-driven story (tag + watch pipeline + smoke).

## What's in this release (consolidation source)

### Architectural epics (the headline)

- **V125-1-9** — Schema envelope + JSON Schema + read-time validation + `sharko validate-config` CLI + CI gates (commit `1ed2de6d`, PR #346, sprint-status epic-V125-1-9-schema-envelope)
- **V125-1-8** — Cluster reconciler + ownership labels + GitOps stance fix (closes #257) (commit `5966e244`, PR #348, sprint-status epic-V125-1-8-cluster-reconciler)

### Provider + infra (interim hotfixes folded in)

- **V125-1-10** — ArgoCDProvider auto-default in-cluster + namespace cross-contamination fix (PR #327 + #328 hotfix bundle)
- **V125-1-11** — Typed ProviderConfig split (3 new types, compat shim retired in Wave F)
- **V125-1-13.x** — In-cluster gitfake + git-host allowlist (env-gated SHARKO_GIT_ALLOWLIST_HOSTS)
- **V125-1-13.y** — In-process e2e mock-provider wiring + tiered_git override bypass fix

### Bug fixes + UX (V126-1..3)

- **V126-1** — BUG-033: UI cluster status flips to connected after registration (`db154763`, PR #338)
- **V126-2** — DESIGN-01: bootstrap ships empty addons-catalog by default (`8b8bb36f`, PR #340)
- **V126-3** — DESIGN-02: "Running on N/M clusters" badge + Catalog tab rename (`51236115`, PR #341)

### Developer experience (V126-4..5)

- **V126-4** — e2e harness heartbeat + cleanup hardening + Git URL validator explicit-fields override (`c96fbf54`, PR #344)
- **V126-5** — `sharko-dev.sh upgrade` subcommand + `ready` preflight resource check (`a1dc26c5`, PR #343)

### Tooling refresh (lands separately, not in this release-cut PR)

- Role-file refresh (task #263) — `.claude/team/*.md` 13-file rewrite via agent `ae48ebb29f590c2df`, commit `14ffd634`. Orchestrator cherry-picks into chore PR + auto-merge. NOT in v1.25.0-pre.0 release notes (internal tooling, not shipped surface).

## Branch + release strategy

- **Branch:** `dev/v1.25.0-pre.0-release` cut from main HEAD `da298893`
- Single worktree, single agent for Story 25-pre.0.1
- Single PR; auto-merge per `feedback_auto_merge_when_green` when CI green
- **PR title:** `release: v1.25.0-pre.0 — schema envelope + cluster reconciler + DX bundle`
- Tag cut on main HEAD AFTER PR merge (orchestrator-driven; NOT agent work)

## Hard rule reminder (CLAUDE.md)

> "Never retag an existing version. Every code change — no matter how small — gets a new semver version."

If anything in the release pipeline fails after tag push: cut `v1.25.0-pre.1`. Never delete and recreate `v1.25.0-pre.0`.

## Quality gates (Story 25-pre.0.1)

- `helm lint charts/sharko` — must pass
- `helm template charts/sharko --version 1.25.0-pre.0` — must render
- `bash -c "grep -rn '1\.19\.\\|1\.24\.' --include='*.yaml' --include='*.json' --include='*.md' charts/ docs/ ui/ cmd/ internal/ scripts/ Makefile Dockerfile 2>/dev/null"` — audit; agent judges which hits are historic vs need bump
- `mkdocs build --strict` — release notes site builds cleanly
- `go build ./...` — sanity (no ldflags surprise)
- NO swagger regen (no @Router changes)
- NO `Co-Authored-By`, NO `--no-verify`, NO `git push` from worktree, NO `git tag` from agent

## Forbidden

- NO `Co-Authored-By` trailers (CLAUDE.md hard rule)
- NO `--no-verify`
- NO `git push` from worktree
- NO `git tag` from worktree (orchestrator-only)
- NO scope creep — only Chart.yaml + version sentinels + release notes
- DO NOT touch `.claude/team/*.md` (role-refresh chore PR owns those)
- DO NOT bundle the role-file refresh into this PR

## Role files (Story 25-pre.0.1 dispatch)

- `.claude/team/tech-lead.md` (always)
- `.claude/team/devops-agent.md` (Chart.yaml, version sentinels, release pipeline awareness)
- `.claude/team/docs-writer.md` (release-notes.md consolidation, mkdocs --strict)

## MCP tools

- **Serena MCP** — find version sentinels via `search_for_pattern '1\.19\.'` + `'1\.24\.'`; locate the existing `docs/site/release-notes.md` top section for format reference

---

## Story 25-pre.0.1 — Chart.yaml bump + version sentinels + release notes consolidation (single agent, single PR)

### Subtasks

1. **Pre-flight verification (from worktree CWD):**
   - `git tag -l 'v1.25.0*'` → must be empty (sanity; orchestrator already verified)
   - Confirm current Chart.yaml line shows `version: 1.19.0` + `appVersion: "1.19.0"`
2. **Bump `charts/sharko/Chart.yaml`:** `version: 1.25.0-pre.0` + `appVersion: "1.25.0-pre.0"`
3. **Audit-and-bump other version sentinels:**
   - `git grep -ln '1\.19\.' -- charts/ docs/ ui/ cmd/ internal/ scripts/ Makefile Dockerfile` → judge each hit; bump only if it's a "current version" reference (NOT a historic changelog entry, NOT a release-notes entry for a past version)
   - `git grep -ln '1\.24\.' -- charts/ docs/ ui/ cmd/ internal/ scripts/ Makefile Dockerfile` → same judgment; expected hits: `--version` flags in docs/install examples, helm chart README, possibly `personal-smoke-runbook.md`. Bump to `1.25.0-pre.0`.
   - **Do NOT touch** Go ldflags-stamped versions (CI sets those at build time)
4. **Append release notes section** at the TOP of `docs/site/release-notes.md` (above any existing `## v1.X.Y` section). Format mirrors the existing file's section shape. Include:
   - `## v1.25.0-pre.0 — 2026-05-21` (use actual date when agent runs if different)
   - 3-4 line highlight paragraph framing V125-1-9 + V125-1-8 as the architectural milestone (read-contract + reconciler; YAML becomes operational source of truth)
   - Grouped bullets per surface (architectural epics / provider+infra / bug fixes+UX / DX) per the consolidation source above
   - Each bullet links its commit SHA + PR number
   - **Migration / upgrade notes subsection (3-5 lines max):**
     - V125-1-9 envelope: existing 1.19.x/1.24.x configs auto-detect as legacy unwrapped → still load; run `sharko validate-config configuration/` pre-upgrade to surface any latent shape issues
     - V125-1-8 reconciler: transparent; no operator action required
     - NO breaking changes
5. **NOT in scope of this story:** the role-file refresh PR (separate chore), CHANGELOG.md (if it diverges from release-notes.md the canonical source is the docs site)

### Acceptance criteria

- [ ] `charts/sharko/Chart.yaml` reports `version: 1.25.0-pre.0` + `appVersion: "1.25.0-pre.0"`
- [ ] `helm lint charts/sharko` passes
- [ ] `helm template charts/sharko --version 1.25.0-pre.0` renders without error
- [ ] No remaining `1.19.` or `1.24.` reference that should be the current version (historic refs OK)
- [ ] `docs/site/release-notes.md` has new `## v1.25.0-pre.0` section at top with bulleted scope + migration notes
- [ ] `mkdocs build --strict` passes
- [ ] `go build ./...` clean
- [ ] Single commit on `dev/v1.25.0-pre.0-release`
- [ ] NO `Co-Authored-By` trailer, NO `--no-verify`

### Commit message

```
release: v1.25.0-pre.0 — schema envelope + cluster reconciler + DX bundle

Chart bumped 1.19.0 → 1.25.0-pre.0 (Chart was stale relative to tag stream).

Bundles:
- V125-1-9 schema envelope (PR #346, commit 1ed2de6d)
- V125-1-8 cluster reconciler (PR #348, commit 5966e244)
- V125-1-10/11/13.x/13.y interim provider + e2e fixes
- V126-1 BUG-033 (PR #338), V126-2 DESIGN-01 (PR #340), V126-3 DESIGN-02 (PR #341)
- V126-4 e2e harness QoL (PR #344), V126-5 sharko-dev.sh DX (PR #343)

Release notes consolidated in docs/site/release-notes.md.
Migration: V125-1-9 auto-detects legacy unwrapped YAML; sharko validate-config
recommended pre-upgrade. V125-1-8 reconciler is transparent. NO breaking changes.

Refs: release-v1.25.0-pre.0.md
```

---

## Story 25-pre.0.2 — Tag cut + release pipeline watch + smoke (orchestrator-driven, NO agent)

### Orchestrator runbook (executes AFTER PR #?? merges to main)

```bash
# 0. From main repo root (NOT a worktree); confirm clean + sync'd to merged main
cd "$(git rev-parse --show-toplevel)"
git checkout main
git pull --ff-only origin main
git status -s  # must be clean
git tag -l 'v1.25.0*'  # must still be empty before tagging

# 1. Cut the annotated tag on the merged commit
git tag -a v1.25.0-pre.0 -m "v1.25.0-pre.0 — schema envelope + cluster reconciler + DX bundle

V125-1-9 schema envelope (PR #346)
V125-1-8 cluster reconciler (PR #348)
V125-1-10/11/13.x/13.y interim provider + e2e fixes
V126-1..5 bug fixes + UX + DX bundle

See docs/site/release-notes.md for full notes."

# 2. CONFIRM WITH MAINTAINER BEFORE PUSH (hard-to-reverse, visible action per CLAUDE.md)
#    --> only proceed after explicit maintainer ack in chat
git push origin v1.25.0-pre.0

# 3. Watch the release pipeline (per feedback_wait_for_ci)
gh run watch  # or: gh run list --workflow=release.yml --limit 1

# 4. On green: smoke-verify both artifacts
docker pull ghcr.io/moranweissman/sharko:1.25.0-pre.0
docker run --rm ghcr.io/moranweissman/sharko:1.25.0-pre.0 --version
helm pull oci://ghcr.io/moranweissman/sharko/charts/sharko --version 1.25.0-pre.0
# verify the pulled tgz metadata reports 1.25.0-pre.0

# 5. Report to maintainer; mark sprint done in sprint-status.yaml
```

### If pipeline fails

- Diagnose root cause from `gh run view` output (most common: cosign signing, goreleaser dirty tree, Helm OCI push auth, container build)
- Cut a single hotfix bundle PR
- Bump Chart.yaml + appVersion to `1.25.0-pre.1`
- After hotfix PR merges, repeat the tag-cut runbook with `v1.25.0-pre.1`
- **NEVER** delete + recreate `v1.25.0-pre.0` (CLAUDE.md hard rule)

### Acceptance

- [ ] Tag `v1.25.0-pre.0` exists on main HEAD (post-merge SHA)
- [ ] Container at `ghcr.io/moranweissman/sharko:1.25.0-pre.0` pulls + reports version
- [ ] Helm chart at `oci://ghcr.io/moranweissman/sharko/charts/sharko:1.25.0-pre.0` pulls + metadata matches
- [ ] Cosign signatures verify for both artifacts (cosign log link captured)
- [ ] Maintainer notified; sprint marked done

---

## Sequencing + dispatch order

| Wave | Story | Mode | Estimated time |
|------|-------|------|---------------|
| A | 25-pre.0.1 | Agent (single worktree) | 30-60 min agent + ~5 min PR CI |
| B | 25-pre.0.2 | Orchestrator (tag + watch + smoke) | ~15 min pipeline + smoke if green; +30-60 min per hotfix cycle if red |

Total best case: ~75-90 min. Total worst case (one hotfix cycle for a pipeline issue): ~3 hours.

## Risk register

| Risk | Likelihood | Mitigation |
|------|-----------|-----------|
| Cosign signing fails (TUF cache or workflow_run SAN mismatch) | Med — V123 had 4 RC cycles for this | Cut pre.1 with the fix; the V123 trust regex is already in workflows |
| Goreleaser dirty tree (V123-2.5 type issue) | Low — fixed in v1.23 pipeline | Cut pre.1 if it recurs |
| Helm OCI push auth (ghcr token expiry) | Low | Cut pre.1 after token refresh |
| Chart bump misses a hidden version sentinel | Low | Audit step in Story 25-pre.0.1 catches; agent judges |
| Mkdocs strict fails on the new release-notes section | Low | Agent runs mkdocs build --strict as a gate |

## All OQs PRE-RESOLVED

Per `feedback_decide_dont_ask_technical_oqs` (count: 11 locked, 0 surfaced to maintainer):

1. Version → `1.25.0-pre.0`
2. Branch → `dev/v1.25.0-pre.0-release`
3. PR title → `release: v1.25.0-pre.0 — schema envelope + cluster reconciler + DX bundle`
4. Pre.0 vs rc.0 → `pre.0` (V123 pattern)
5. Tag operation → orchestrator-driven (NOT agent) per "Executing actions with care"
6. Release notes home → `docs/site/release-notes.md` (verified present, in mkdocs nav)
7. Section placement → top of file
8. Version sentinels → Chart.yaml (version + appVersion) + any `--version` examples in docs/READMEs; NOT Go ldflags
9. Auto-merge → YES once CI green
10. Migration notes → 3-5 lines (envelope auto-detect + reconciler-transparent + no breaking)
11. Pipeline failure → cut `pre.1` (never retag)

## Done definition

- [ ] Story 25-pre.0.1 shipped on `dev/v1.25.0-pre.0-release` + PR auto-merged to main
- [ ] Tag `v1.25.0-pre.0` cut on main HEAD (post-maintainer-ack)
- [ ] Container + Helm chart + cosign signatures verified
- [ ] Sprint marked done in `sprint-status.yaml`
- [ ] Maintainer notified
