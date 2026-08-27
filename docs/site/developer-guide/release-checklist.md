# Release Checklist — What a Tag Must Prove Before It Publishes

This page is the plain-English answer to "what actually has to pass before
a `vX.Y.Z` tag turns into a published release?" It documents the gate
enforced by `.github/workflows/release.yml`, so it should stay in sync
with that file — if you change what the gate requires, update this page
in the same PR.

See [Release Process](https://github.com/MoranWeissman/sharko/blob/main/CONTRIBUTING.md#release-process)
in `CONTRIBUTING.md` for who cuts a release and when. This page is the
"what has to be green" reference that bullet list points at.

## The five things a tag has to prove

Pushing a `v*` tag does not publish anything by itself. It only starts
the chain. Nothing is uploaded, pushed, or signed until **all five** of
these have run against that exact tagged commit and come back clean:

1. **The standard CI suite** — `ci.yml`: Go build, vet, unit tests,
   `govulncheck`, the UI build + tests + lint, the swagger/schema/
   provider-type drift checks, Helm chart lint + template render, and the
   forbidden-content security scan. This is the same suite every PR runs.
2. **The full end-to-end suite (kind + real ArgoCD)** — the ~10-15 minute
   suite that boots a real kind cluster and a real ArgoCD, normally only
   run on PRs carrying the `e2e` label or on the nightly schedule (see
   [End-to-End Testing](e2e-testing.md)). A release runs it for real,
   every time, against the tag.
3. **The live-Gitea write loop** — the one test that talks to a real
   Gitea server instead of the in-repo fake, catching drift between the
   fake's stub shapes and Gitea's actual REST API.
4. **The Helm-mode end-to-end subset** — installs Sharko via
   `helm upgrade --install` into a kind cluster the same way an operator
   would, rather than booting the server in-process.
5. **The perf regression gate** — the same p99-regression check that
   normally only runs on PRs (`perf-regression.yml`), comparing the
   tagged commit against the recorded baselines in
   [Perf Baselines](../operator/perf-baselines.md). No `skip-perf-gate`
   escape hatch here — that label is for PR authors trading off an
   intentional regression against review speed, and a release tag has no
   PR to attach a label to.

And one more that isn't test evidence but is still required:

6. **A strict docs build** — `mkdocs build --strict` over the whole docs
   site. Strict mode fails on unresolved internal links, missing nav
   entries, and orphan pages, so a release can't publish with a docs site
   that wouldn't actually build.

## Where this lives in the workflow

`.github/workflows/release.yml` triggers on `workflow_run` once `ci.yml`
finishes on the tag — so item 1 above was always required, before this
gate existed. What was missing until this gate was added: items 2-6 all
normally skip on an ordinary tag push (they're label-gated, PR-only, or,
in the docs-build case, not wired into any CI workflow at all). A tag
could publish having proven none of them.

The gate is five jobs named `release-gate-*` in `release.yml`, each
running the real check directly against the tagged commit, plus a
`release-evidence-gate` job that reads all five results and fails loudly
if any of them isn't a plain `success` — including a job that got
**skipped** rather than one that failed outright. Every publish step
(signing, image build/push, Helm chart push, CLI binaries via goreleaser)
depends on `release-evidence-gate` succeeding first. If any evidence job
fails or is skipped, nothing downstream runs and the release workflow run
itself shows red.

### What the published release page shows

The GitHub release page title and body are configured in
`.goreleaser.yaml`, in the `release:` block. For v4.0.0 and forward:

- **The title** carries "— technical preview" after the tag name (via
  `name_template`), so the download page itself says the release is a
  preview without an operator needing to read into the body.
- **The body** opens with a durable warning blockquote stating the
  technical-preview status, the supported artifact range
  (`v4.0.0`-or-later only), and the fact that earlier releases remain
  retired. This warning sits above the CI-verified ArgoCD tested-range
  line, which follows it with a blank line between them.

The release page remains GitHub's "Latest" release — that's correct,
because v4 replaces the unsafe and retired v3 release. The metadata on
the page itself carries the preview label.

## Why this runs on every tag, every time

This is slower than the old behavior — the full kind-backed suite alone
adds ten-plus minutes, and running three separate kind-backed lanes back
to back adds real runtime to every release. That's an accepted trade:
honesty about what a release has actually proven matters more than
shaving minutes off the release workflow. A tag that publishes without
having run the e2e suite, the perf gate, or a docs build isn't actually
faster to ship — it just moves the risk of finding out to whoever
installs it.

## If you need to change what the gate requires

- **Adding a new evidence job:** add a `release-gate-*` job to
  `release.yml` mirroring the real check (see the existing jobs for the
  pattern — they intentionally duplicate steps from `e2e.yml` /
  `perf-regression.yml` rather than calling them as reusable workflows),
  add it to `release-evidence-gate`'s `needs:` list, and add a `check`
  line for it in that job's verification script. Update this page's
  "five things" list in the same PR.
- **Removing or weakening a check:** don't, without discussing it first —
  this gate exists specifically because a release published once without
  this evidence. If a check is genuinely no longer relevant (e.g. a test
  lane gets retired), remove its `release-gate-*` job, its entry in
  `release-evidence-gate`'s `needs:` and verification script, and its line
  here, all in the same PR, with the reason in the commit message.
