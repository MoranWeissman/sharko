# Roadmap

This is Sharko's public roadmap. It describes the **themes** the
maintainer intends to pursue, plus the items that have explicitly
been ruled OUT. It does **not** publish commit dates, and the inclusion
of a theme below is not a contract that it will ship in any specific release.

If you are looking for **what shipped in a specific release**, read
[`release-notes.md`](../release-notes.md). The roadmap describes intent;
release notes describe reality.

**Current state:** Sharko v4 is a technical preview for evaluation and staging environments. It is not supported for production use.

## What this roadmap is — and isn't

**Is:**

- A description of the maintainer's working priorities.
- A signal to adopters about where the project is heading.
- A surface for community input — see [How to influence the roadmap](#how-to-influence-the-roadmap) below.

**Isn't:**

- A commitment to ship any specific theme in any specific release.
- A schedule. Sharko is solo-maintained; themes reflect intent, commit dates depend on community and maintainer capacity.
- A contract. The API stability contract lives at [`api-stability.md`](../developer-guide/api-stability.md).

## What shipped in v4.0

v4 is Sharko's current release line. The v4 work focused on the new data-file format and takeover capabilities. v4 is a technical preview — it runs as a single instance only and is not yet supported for production.

Major capabilities:

- **New data-file format.** v4 replaces the old combined-values-file model with per-cluster addon assignment files (`cluster-addons/<cluster>.yaml`), a root catalog (`catalog.yaml`), and split values files (`values/global/` and `values/clusters/`). The engine chart reads these files and creates ArgoCD Applications.
- **Takeover.** v4 can take over clusters ArgoCD already manages instead of only registering brand-new ones. `sharko takeover` makes Sharko the owner of an existing cluster's connection.
- **Signed catalog verification.** Sharko verifies catalog signatures using Sigstore cosign and refuses unsigned catalogs unless explicitly allowed.
- **Activity history.** In-memory audit log with real-time SSE streaming, tracking all mutating operations.
- **API keys with expiry.** 90-day default expiry on all API tokens, with renewal support that keeps the token value unchanged.

## Near-term themes

Small items that can ship as v4 patches or minors:

- **Adopter feedback fixes.** Bug reports and papercuts from real v4 installs.
- **v3 to v4 migration tooling.** Automated migration from the old data-file format to the new one.
- **Docs in plain English.** Rewrite operator docs and runbooks to be clearer and match v4 reality.
- **GCP and Azure provider support.** Full support for GKE with Workload Identity and GCP Secret Manager, plus AKS with Workload Identity and Azure Key Vault. v4 supports AWS Secrets Manager and Kubernetes Secrets today.
- **Marketplace improvements.** Third-party catalog sources, better search, addon dependencies.

## Medium-term themes

Larger pieces of work for future releases. Each theme is intent, not commitment.

- **Fine-grained RBAC.** Per-cluster, per-environment, per-addon permissions. API key scoping. v4 has three roles: Admin, Operator, Viewer.
- **SSO and OIDC.** SAML and OIDC providers (Google Workspace, Okta, Entra ID, GitHub OAuth). v4 has local user accounts and API keys only.
- **Multiple connections.** Support for multiple ArgoCD instances per Sharko (one per environment or business unit). Multiple Git connections. v4 manages one connection at a time.
- **Rule-based auto-merge.** Conditions for auto-merge: label-based, environment-based, time-based, with `CODEOWNERS` integration. v4 has a binary toggle.
- **Grafana dashboard.** Pre-built dashboard JSON for cluster health, addon status, version matrix. v4 ships metrics and alerts.
- **Backup and restore.** Export the full Sharko configuration to a file and restore it into a fresh install.
- **External Secrets Operator template.** Reference template for teams that want cluster credentials to flow through ESO instead of Sharko's built-in reconciler.
- **Cluster templates, upgrade planner, rollback UI, cost visibility.**

## Explicitly OFF the roadmap

These directions are not deferred — they are **rejected**. They will
not be reconsidered unless a fundamental constraint changes.

- **Non-Helm addons (Crossplane operators, raw kustomize, raw
  manifests, OLM operators).** Rejected 2026-04-20. Sharko stays
  Helm-focused. The catalog, the smart-values pipeline, the upgrade
  planner, the ApplicationSet generators, and the ArgoCD wiring all
  assume Helm; supporting other addon shapes would either fork every
  one of those surfaces or weaken the Helm-shaped guarantees that
  make Sharko useful in the first place. If you have a non-Helm
  workload, the right tool is ArgoCD directly.

## How to influence the roadmap

Highest-leverage feedback comes from **real adopters with concrete
use cases**. "We are running Sharko in production at N clusters and
hit X" is far more actionable than "Sharko should also do Y."

In order of preference:

- **GitHub Discussions — Roadmap input category.** Open a discussion
  describing the problem, your current workaround, and what
  capability would unblock you. Discussions stay open longer than
  issues and are the maintainer's preferred surface for shaping
  intent:
  [https://github.com/MoranWeissman/sharko/discussions](https://github.com/MoranWeissman/sharko/discussions).
- **Feature issue template.** When the problem is concrete enough to
  formalise as a request, use the
  [Feature Request template](https://github.com/MoranWeissman/sharko/issues/new?template=feature.yml).
  The template asks for the problem statement, target users, and how
  you would verify the feature works — answering those three
  questions thoroughly is what moves an item from "interesting" to
  "scheduled."
- **CONTRIBUTING guide.** For larger themes, the
  [CONTRIBUTING](https://github.com/MoranWeissman/sharko/blob/main/CONTRIBUTING.md)
  guide describes the issue → discussion → PR flow plus the DCO
  sign-off requirement. Code contributions are welcome, but please
  open a discussion before starting work on anything roadmap-shaped
  so the design can be aligned.

The maintainer reads everything but cannot promise to act on every
input. Themes move up the priority list when multiple adopters hit
the same constraint, when the proposed change is small and bounded,
or when someone shows up with a contribution proposal.
