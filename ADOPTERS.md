# Adopters

This document lists organizations that have adopted Sharko. If your
organization is using Sharko — in production, staging, or as a proof
of concept — please consider adding yourselves below. Public adoption
signals matter: they help the project mature, attract contributors,
and (importantly) support the project's CNCF Sandbox application.

## How to Add Your Organization

1. Fork the repository.
2. Add a new row to the table below.
3. Open a pull request titled `docs: add <Organization> to ADOPTERS.md`.
4. The PR will be merged by a maintainer; no further review process
   is required beyond the standard PR checks.

You can be as specific or as vague as you like. We respect your privacy
— if you cannot disclose your organization name or contact details
publicly, please reach out via the
[security contact](SECURITY.md) (used here as a general confidential
channel) and we can record your usage internally for aggregate
counts only.

## Adoption Status Definitions

- **Production** — Sharko is in your production GitOps pipeline,
  managing addons across one or more production clusters.
- **Staging** — Sharko is in your pre-production environment with
  the intent to promote to production.
- **POC / Evaluation** — You are actively evaluating Sharko on a
  test cluster or in `make demo` mode and have committed engineering
  time to the evaluation.

## Template

Copy the row below, fill in your details, and add to the table:

```
| Your Organization | One-line use case description | Production / Staging / POC | @your-github-handle or email |
```

## Adopters

| Organization | Use Case | Status | Contact |
| ------------ | -------- | ------ | ------- |
| _Your organization could be here — open a PR!_ | | | |

<!--
Example row (uncomment and adapt when adding):

| Acme Corp | Managing 12 EKS clusters across 3 regions; uses AWS SM provider for kubeconfig delivery | Production | @acme-platform-team |

Real entries replace the placeholder row above.
-->

## Why It Matters

Public adoption is one of the criteria the
[CNCF Sandbox process](https://github.com/cncf/sandbox) considers when
evaluating new projects. It is also a signal to other potential users
that Sharko is being run by real organizations, not just its
maintainers.

For the project's CNCF Sandbox application target, we are aiming for
at least 5 organizations listed here before submission. See
[GOVERNANCE.md](GOVERNANCE.md) for the broader trigger conditions
that move the project from BDFL to steering-committee governance.
