# Governance

This document describes how the Sharko project is governed. It complements
[MAINTAINERS.md](MAINTAINERS.md) (who runs the project) and
[CONTRIBUTING.md](CONTRIBUTING.md) (how to participate).

## Current Model: BDFL (Benevolent Dictator For Now)

Sharko is in **pre-release** (`v1.x`) and is led by a single maintainer,
Moran Weissman, who acts as Benevolent Dictator For Now (BDFL). The
BDFL has final say on technical direction, scope, release timing, and
maintainer additions.

This model is intentional and appropriate for the project's current
stage:

- The project is **pre-`v2.0.0`** — the API surface, architecture, and
  scope are still solidifying. A single architectural voice keeps the
  product coherent.
- The contributor community is small. A heavyweight governance
  structure would impose overhead without proportional benefit.
- Settled decisions (see
  [`.claude/team/product-manager.md`](.claude/team/product-manager.md))
  are tracked explicitly so contributors know what is up for debate
  and what is not.

The BDFL still operates transparently:

- All planning happens in the open via GitHub issues, BMAD planning
  artifacts under `.bmad/output/`, and design docs under `docs/design/`.
- All changes ship via pull requests — no direct commits to `main`.
- All decisions of architectural significance are recorded in the
  `.bmad/output/` artifacts or `docs/design/` proposals.

## Transition Plan: From BDFL to Steering Committee

The BDFL model is a **transitional** structure, not the end state. As
Sharko approaches CNCF Sandbox acceptance and grows its contributor
base, governance will evolve toward a multi-maintainer steering
committee.

### Trigger conditions

The transition from BDFL to a steering committee will begin when **at
least three** of the following conditions are met:

1. Sharko has been **accepted into the CNCF Sandbox** (or an equivalent
   neutral foundation home).
2. There are at least **three active maintainers** drawn from at least
   **two distinct organizations** (per CNCF maintainer-diversity
   guidance).
3. The project has shipped **`v2.0.0` GA** (the first production
   release — see
   [`.claude/team/product-manager.md`](.claude/team/product-manager.md)).
4. There are at least **five organizations listed in
   [ADOPTERS.md](ADOPTERS.md)** running Sharko in production or
   staging.
5. The BDFL has been actively soliciting and accepting external
   technical proposals for at least six months.

### Steering committee shape (post-transition)

When the trigger conditions are met, the BDFL will propose a steering
committee structure for community review. The expected shape:

- **3-5 members** drawn from the active maintainers.
- **No more than 2 members from any single organization** (CNCF
  diversity requirement).
- **Decision rule: lazy consensus.** Decisions are proposed in writing,
  open for objection for 5 business days; absence of objection is
  consent. Formal vote (simple majority) only when consensus fails.
- **Term length: 1 year**, with overlapping seats (no full-committee
  turnover in a single cycle).
- **Public meeting cadence: monthly**, with minutes published in the
  repository.
- **Maintainer additions / removals:** decided by the steering
  committee per the criteria in [MAINTAINERS.md](MAINTAINERS.md).

The BDFL will step into the role of one steering-committee member on
transition; the lead-maintainer designation is retired.

## Decision-Making

### Architectural decisions

While Sharko is BDFL-led:

- Anyone may propose an architectural change via a GitHub issue or a
  design doc PR under `docs/design/`.
- The BDFL responds within a best-effort 5-business-day window.
- The BDFL may accept the proposal as-is, request changes, defer it
  (with explicit reason and target milestone), or reject it (with
  explicit reason).

Once a steering committee exists, the lazy-consensus rule applies.

### Settled decisions

Some decisions are **settled** and not subject to re-litigation without
an exceptional reason. The current list is maintained at
[`.claude/team/product-manager.md`](.claude/team/product-manager.md)
under "Settled Decisions". Examples include "ArgoCD only, no Flux",
"server-first, not standalone CLI", and "PR-only Git flow, no direct
commits".

If you want to propose changing a settled decision, open a design-doc
PR under `docs/design/` explaining the new context and the trade-offs.

### Release decisions

Release cadence and version numbering follow the rules in
[CONTRIBUTING.md](CONTRIBUTING.md) and the
[Sharko version strategy](.claude/team/product-manager.md):

- `v1.x` — pre-release; new features ship as minor bumps, fixes as
  patch bumps; expect breaking changes between minors until GA.
- `v2.0.0` — first production-ready release; SemVer applies strictly
  from this point forward.

The BDFL (or, post-transition, the steering committee) decides when a
release is ready.

## Code of Conduct

All participants in the Sharko community are subject to the [Code of
Conduct](CODE_OF_CONDUCT.md). Enforcement is the responsibility of the
current maintainers (during BDFL period) and the steering committee
(post-transition).

## Security Disclosures

Security disclosure handling is described in [SECURITY.md](SECURITY.md).
Disclosures go to the current security contact (see SECURITY.md for the
current address); response is the joint responsibility of the
maintainers.

## Trademark, Logo, and Brand

The Sharko name and logo are owned by the project's lead maintainer
until the project is donated to a neutral foundation. On CNCF Sandbox
acceptance (or equivalent), trademark and logo ownership will transfer
to the foundation per its standard terms.

## Changes to This Document

Changes to GOVERNANCE.md require a pull request, the same review
process as any other change, and (post-transition) steering-committee
approval. While Sharko is BDFL-led, the BDFL decides — but proposed
governance changes are still expected to be open for community comment
for at least 10 business days before merge.

## References

- [MAINTAINERS.md](MAINTAINERS.md) — current and emeritus maintainers
- [CONTRIBUTING.md](CONTRIBUTING.md) — how to contribute
- [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) — community standards
- [SECURITY.md](SECURITY.md) — security disclosure
- [ADOPTERS.md](ADOPTERS.md) — organizations using Sharko
- [CNCF Sandbox criteria](https://github.com/cncf/sandbox)
- [CNCF project-governance template](https://contribute.cncf.io/maintainers/governance/)
