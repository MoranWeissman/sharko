# Maintainers

This file lists the active maintainers of the Sharko project. Maintainers
have commit access to the repository, the ability to triage and close
issues, and the responsibility to review and merge pull requests.

## Current Maintainers

| Name           | GitHub                                            | Email                       | Role         |
| -------------- | ------------------------------------------------- | --------------------------- | ------------ |
| Moran Weissman | [@MoranWeissman](https://github.com/MoranWeissman) | moran.weissman@gmail.com    | Lead / BDFL  |

## Emeritus Maintainers

None yet. When a maintainer steps back from active maintenance, they move
here and retain commit access at the project's discretion.

## Becoming a Maintainer

Sharko is currently in pre-release (`v1.x`) and is led by a single
maintainer (BDFL model — see [GOVERNANCE.md](GOVERNANCE.md)). As the
project grows toward `v2.0.0` GA and CNCF Sandbox acceptance, we will
add additional maintainers from the contributor community.

To be considered for maintainership, a contributor should demonstrate
**all** of the following:

1. **Sustained, substantive contribution.** A track record of merged
   pull requests over several months — not a single drive-by patch.
   This typically means 6+ months of regular contribution and 10+ merged
   PRs of meaningful scope (features, non-trivial bug fixes, test
   coverage, documentation).
2. **Architectural understanding.** A demonstrated understanding of the
   Sharko architecture: the orchestrator pattern, the ArgoCD-only
   coupling, the two-operation catalog/deploy model, the GitOps-first
   stance (PR-only writes), the ownership-label gate
   (`app.kubernetes.io/managed-by: sharko`), and the schema-envelope
   discipline.
3. **Code review quality.** A history of helpful, technically rigorous
   review comments on others' PRs — not just opening PRs of one's own.
4. **Community judgment.** A demonstrated ability to engage
   constructively in issue discussions, RFC threads, and design
   reviews. The maintainer role is partly a stewardship role.
5. **Maintainer recommendation.** An existing maintainer must propose
   the candidate. Other maintainers may concur or object.

The decision to add a new maintainer is made by consensus of existing
maintainers. While Sharko remains BDFL-led, the lead maintainer has
final say; once a steering committee exists (see
[GOVERNANCE.md](GOVERNANCE.md)), the committee will own the decision.

## Maintainer Responsibilities

- Triage and respond to incoming issues within a reasonable window
  (best-effort 5 business days; faster for security disclosures — see
  [SECURITY.md](SECURITY.md)).
- Review pull requests against the project's quality bar
  (architecture, security, tests, documentation).
- Maintain the project's documentation and keep it in sync with the
  code (the "verified-by-execution" rule for runbooks).
- Cut releases following the documented release process
  (see [CONTRIBUTING.md](CONTRIBUTING.md)).
- Uphold and enforce the [Code of Conduct](CODE_OF_CONDUCT.md).
- Be responsive to security disclosures per [SECURITY.md](SECURITY.md).

## Stepping Down

A maintainer may step down at any time by opening a PR moving their
entry to the **Emeritus** section. Maintainers who become inactive
(no review or merge activity for 6+ months) may be moved to Emeritus
by the remaining maintainers.

## Contact

For project governance questions: open a [GitHub
Discussion](https://github.com/MoranWeissman/sharko/discussions) (once
Discussions are enabled) or open an issue.

For Code of Conduct concerns: see [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).

For security disclosures: see [SECURITY.md](SECURITY.md).
