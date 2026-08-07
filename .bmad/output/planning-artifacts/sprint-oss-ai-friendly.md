# Sprint: OSS + AI-contributor-friendly repo

Date: 2026-08-07. Lead: main session (Fable). Implementation agents: Sonnet, worktree-isolated. License check: Opus.
Backlog: the 18 findings from the OSS review of 2026-08-01. This sprint is words, config, and file hygiene — no product code.

Baseline: main at `87ead83`. The main checkout carries two deliberate local modifications that must survive:
`.bmad/output/implementation-artifacts/sprint-status.yaml` (never committed) and `.claude/team/frontend-expert.md`
(the 2026-08-02 info-text-vs-error-text addition — WANTED, gets committed as part of the sanitized file in lane B).

## Lane L0 — BMAD license check (Opus, read-only, no worktree)

Size: S. Covers finding 4 (the gate half).
Look inside `_bmad/` (installed pack v6.2.2, no LICENSE file found locally in `_bmad/` or `.claude/skills/`) and
web-check the upstream BMAD-METHOD project's license and redistribution terms. Verdict needed:
may we commit `.claude/skills/` (67 bmad-* skill folders, ~11MB text) into this Apache-2.0 repo, and under what
attribution requirements? Also report: do the skill folders work standalone, or do they depend on the gitignored
`_bmad/` engine at runtime? Output: verdict + required attribution text, if any. Gates lane B's shape.

## Lane A — Truth pass (Sonnet, worktree)

Size: M. Covers findings 1, 2, 3, 8, 12, 15, 16, 17. Pure text; single commit.

- **Versions tell the truth** (1): README says "v2.0.0 — production release"; GOVERNANCE.md, MAINTAINERS.md,
  CONTRIBUTING.md say "pre-release v1.x"; `.claude/team/product-manager.md` says v1.x. Reality: v3.0.0 is the
  latest tag, v4 in progress. Fix all five. Add one line to CONTRIBUTING's release process: update these files
  when a new major is tagged.
- **One changelog** (2): CHANGELOG.md is dead at v2.0.2 while `docs/site/release-notes.md` is current. Make
  CHANGELOG.md a short pointer to release-notes, keep an Unreleased section (carry over the current Unreleased
  entry about the shelved operator). One history, not two.
- **README docs links** (3): add a link/badge for https://sharko.readthedocs.io/. Repoint the Documentation
  table at the docs site's pages. The four legacy files (`docs/api-contract.md`, `docs/architecture.md`,
  `docs/user-guide.md`, `docs/developer-guide.md`) stay in the repo as raw reference — say so in one line.
- **Badges** (8): add the real CI workflow badge; drop the three hand-written static badges (go/react/typescript).
- **Discussions honesty** (12): Discussions is OFF. Three spots point at it: README ~line 329, CONTRIBUTING
  (~line 37 and the "Discussions and Response Cadence" section), `.github/ISSUE_TEMPLATE/config.yml`. Make them
  honest for the OFF state: questions go to the issue tracker. (Flip-back note goes in the final report to Moran.)
- **Quickstart target** (15): README says `make e2e-setup && make e2e` — neither exists. Real targets per
  Makefile: `make test-e2e-fast` / `make test-e2e`. Fix the line.
- **One secret example** (16): keep `secrets.env.example`, delete `.env.secrets.example`, one README line saying
  copy `secrets.env.example` → `secrets.env`. Root `config.yaml` stays (local-mode stub) — add a clear comment
  header saying what it is.
- **NOTICE** (17): add a 3-line Apache-2.0 NOTICE file (project name, copyright Moran Weissman + contributors,
  license pointer).

## Lane C — Hygiene (.github only) (Sonnet, worktree — can run alongside A, no shared files)

Size: S. Covers findings 7 and the workflow half of 10. Single commit.

- **Dependabot** (7): new `.github/dependabot.yml` — gomod at `/`, npm at `/ui`, github-actions; weekly.
- **govulncheck** (7): add a govulncheck step to the existing `go-build-test` job in `.github/workflows/ci.yml`.
- **Bot identity** (10): `.github/workflows/argocd-matrix.yml` ~line 260 and `catalog-scan.yml` ~line 68 use the
  maintainer's personal email as the git committer. Switch both to `github-actions[bot]` /
  `41898282+github-actions[bot]@users.noreply.github.com`. Check the sibling `git config user.name` lines too.

## Lane B — AI machinery goes public (Sonnet, worktree; needs L0 verdict + A merged first)

Size: L. Covers findings 4, 5, 6, 9, 18. Single commit (large).

- **Skills** (4): if L0 says redistribution is allowed — commit `.claude/skills/` (already grep-verified free of
  personal strings; agent re-verifies), plus any attribution L0 requires. If not allowed — keep ignored and write
  the honest paragraph instead. Either way: CLAUDE.md and CONTRIBUTING get a plain paragraph — where BMAD comes
  from, how to install it, and that without it the `.claude/team/` role files are the fallback; CLAUDE.md stops
  ordering agents to invoke skills that may not exist without saying what to do then.
- **Delegation hook** (5): commit `.claude/hooks/enforce-delegation.sh` (content already reviewed — clean, no
  personal content). `.claude/settings.json` already references it.
- **Role-file sanitize** (6): `tech-lead.md`, `implementer.md`, `frontend-expert.md`, `devops-agent.md`.
  Absolute repo-root paths → repo-relative. The private-memory startup step → "read
  `.claude/checkpoints/LATEST.md` if present, else `git log`". The git-identity line → "use your own git
  identity; NEVER add Co-Authored-By". Same treatment for CLAUDE.md's git-user line. IMPORTANT: the agent must
  read `frontend-expert.md` from the MAIN tree (read-only) — the local uncommitted 2026-08-02
  info-text-vs-error-text section is wanted content and must be in the committed sanitized file.
- **.gitignore rework** (9): replace the blanket `.claude/` ignore with explicit rules: ignore
  `.claude/checkpoints/`, `.claude/worktrees/`, `.claude/settings.local.json` (and `.claude/skills/` only if L0
  said no). Add `/playground` and `__pycache__/`. Keep everything else that's ignored today ignored — write the
  tracked intent down as comments.
- **Developing-with-AI guide** (18): distill the 7-file `.workflow-playbook/` (~1100 lines) into ONE page at
  `docs/site/developer-guide/developing-with-ai.md` — how this repo is built with Claude Code: role files, BMAD
  flow, worktree-isolated dispatch, quality gates, checkpoints. Strip personal paths/anecdotes that don't
  generalize. Put it in mkdocs nav, link it from CONTRIBUTING's AI section. Lead call on the playbook source:
  publish the distilled page only; the raw `.workflow-playbook/` stays local (its value is captured, its voice is
  personal). `mkdocs build --strict` must pass.

## Lane D — Personal-file handling (Sonnet, worktree; after B, shares .gitignore + docs nav)

Size: M. Covers findings 10 (.bmad sweep), 11, 13, 14. Agent commit + one integration-time commit.

- **sprint-status untrack** (13): NOT done by the agent. Done at integration, in the main checkout, on the lane-D
  branch: `git rm --cached .bmad/output/implementation-artifacts/sprint-status.yaml` + DCO commit. Working copy
  never touched; verified intact afterwards. The agent only adds the `.gitignore` line for it.
- **TODO files → issues** (14): `docs/TODO.md`, `TODO-v2.md`, `TODO-addon-upgrade-ux.md`,
  `TODO-argocd-diagnostics.md`. Agent sorts items: dead/shipped items die with the file; still-live items become
  drafted issue bodies (plain titles) returned to the lead — the lead creates them with `gh issue create`.
  Anything that reads like a real roadmap goes into the existing `docs/site/community/roadmap.md`. Delete the
  four files.
- **Smoke runbook goes general** (11): rewrite `docs/site/developer-guide/personal-smoke-runbook.md` as
  `contributor-smoke-walk.md` — same content, general voice, no personal machine specifics; update mkdocs nav.
  `logging-audit-punchlist.md`: content becomes a drafted issue body (lead files it), page deleted, nav fixed.
  `mkdocs build --strict` green.
- **.bmad sweep** (10): in the 8 tracked `.bmad/output/**` files carrying absolute local paths or the personal
  email in git-config examples: replace with repo-relative paths / generic wording. Do NOT delete the files —
  they are valued worked examples. Deliberately NOT swept: maintainer-contact emails in SECURITY.md,
  MAINTAINERS.md, CODE_OF_CONDUCT.md, and the "email the maintainer" lines in operator/developer runbooks —
  those are on purpose.
- Also commit this plan file (it lives in `.bmad/output/planning-artifacts/`, worked-example territory).

## Order and integration

1. Dispatch L0 (opus) + lane A + lane C in parallel (A and C share no files).
2. Integrate A, then C: one at a time, in an integration worktree branched off fresh origin/main —
   `sprint/oss-ai-<lane>` → cherry-pick → full CI-honest gates → push → PR → squash auto-merge → verify on
   `git log origin/main` → main checkout `git pull`.
3. Dispatch B when L0's verdict is in and A is merged. Integrate B the same way.
   After B merges: verify the merged `frontend-expert.md` contains the local addition, back the local copy up to
   the job tmp dir, align the working copy to the merged version (`git show origin/main:<path> >` — content is
   landed, nothing is discarded), then `git pull`.
4. Dispatch D after B. Integrate D; the sprint-status `git rm --cached` commit happens on the lane-D branch at
   integration. After D merges, before `git pull`: `mv` the locally-modified sprint-status.yaml aside, pull
   (incoming deletion + local deletion agree), `mv` it back — untracked, ignored, modifications intact.
5. Gates every lane (CI-honest): `GOTOOLCHAIN=local go build ./... && go vet ./... && go build -tags e2e ./...`;
   `env -u AWS_PROFILE -u AWS_DEFAULT_PROFILE KUBECONFIG=/nonexistent go test ./internal/...`;
   `cd ui && npm ci && npm run build && CI=true npx vitest run`; `mkdocs build --strict` when docs change.
   Known vitest flake ("window is not defined" in SecretsProviderSection.tsx) → one rerun if it's the only failure.

## Needs Moran (the only thing)

- The GitHub Discussions toggle. Lane A writes the links for the current OFF state. If Moran flips Discussions
  on, revert three spots: README community section, CONTRIBUTING (intro + response-cadence section),
  `.github/ISSUE_TEMPLATE/config.yml`.

## Out of scope (deliberate)

- No product code, no API changes, no swagger regen.
- No sweep of maintainer-contact emails that are public on purpose.
- `_bmad/` engine stays gitignored regardless of verdict (12MB installed tool; install instructions instead).
- `.workflow-playbook/` raw files stay local; the distilled docs page carries the content.
