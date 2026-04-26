---
generated: 2026-04-21
scope: v1.23 catalog extensibility
source_epic_file: .bmad/output/planning-artifacts/epics-v1.23.md
source_sprint_status: .bmad/output/implementation-artifacts/sprint-status.yaml
---

# Remaining stories — v1.23 Catalog Extensibility

Authoritative backlog lives in `epics-v1.23.md` + `sprint-status.yaml`.
This file is a fast punch-list for dispatch order.

**Sequencing rule** (per v1.22+v1.23 code-review report):
Epic V123-1 → V123-2 → V123-3 → V123-4.

**Story cadence:** one story → one branch off `main` → one PR → merge (no tag).
Each story ships with a retrospective record at
`.bmad/output/implementation-artifacts/V123-N-M-<slug>.md`.

---

## Done ✅

- **V123-1.1** env parser + SSRF guard — PR #267 → `bf6186a`
- **V123-1.2** fetch loop + snapshots + SidecarVerifier interface — PR #268 → `324ec8b`
- **V123-1.3** merger + embedded-wins conflict rule — PR #270 → `836b26c`
- **V123-1.4** Source attribution on API entries + ListFrom helper — PR #272 → `8887674`
- **V123-1.5** GET /api/v1/catalog/sources endpoint + swagger — PR #274 → `b4f1d76`
- **V123-1.6** POST /api/v1/catalog/sources/refresh Tier-2 force-refresh — PR #276 → `eea0abb`
- **V123-1.7** UI source badge on Browse tiles + AddonDetail — PR #278 → `f3c4cdf`
- **V123-1.8** Settings → Catalog Sources admin section (read-only env-only) — PR #280 → `c54ab95`
- **V123-1.9** fetcher gaps + merger coverage + full-loop integration test — PR #282 → `4e87d6e`

**Epic V123-1 (Third-party private catalogs) — CLOSED (9/9 done).**

- **V123-2.1** schema v1.1 — optional per-entry signature field — PR #284 → `b06eee1`
- **V123-2.2** cosign keyless verifier (sigstore-go) + OQ §7.2 resolution — PR #286 → `8bb8074`

---

## Epic V123-2 — Per-entry cosign signing (4 remaining)

### V123-2.3 — Trust policy via `SHARKO_CATALOG_TRUSTED_IDENTITIES`
- Env var: comma-separated cert-identity regexes (e.g., `^https://github\.com/MoranWeissman/.*$`).
- Empty → no third-party trust (reject all signed).
- Feed into `TrustPolicy{Identities}` from V123-1.2.
- Doc at `docs/site/operator/catalog-trust-policy.md`.

### V123-2.4 — UI verified badge + signed pseudo-filter
- Green "verified" pill next to entry name when `verified: true`.
- Browse filter chip: `Signed only`.
- **Depends on:** V123-2.2 (`verified` flag populated).

### V123-2.5 — Release pipeline: sign embedded catalog entries
- Extend `.github/workflows/release.yml` to sign `embedded-addons-catalog.yaml` with cosign keyless.
- Publish `.bundle` sidecar to release artifacts.
- Embedded catalog ships `verified: true` out-of-the-box.

### V123-2.6 — Tests: verification happy / mismatch / unsigned / untrusted
- Fake-sigstore test harness (or use `cosign`'s test fixtures).
- Coverage: valid sig + trusted identity (pass), valid sig + untrusted (reject), invalid sig (reject), no sig + signing required (reject), no sig + signing optional (pass).

---

## Epic V123-3 — Trusted-source scanning bot (5 stories, all backlog)

### V123-3.1 — `scripts/catalog-scan.mjs` skeleton + plugin interface
- Node.js script; pluggable sources.
- Interface: `{name, discover() → [{name, repo, chart, version, trust_score}], annotate(entry)}`.

### V123-3.2 — CNCF Landscape scanner plugin
- Pull `landscape.yml`, filter for Kubernetes Helm addons, map to schema.

### V123-3.3 — AWS EKS Blueprints scanner plugin
- Parse `addons/` directory from `aws-ia/terraform-aws-eks-blueprints-addons`.

### V123-3.4 — PR-opening logic + GitHub workflow ⚠ resolves open question §7.3
- Nightly cron `.github/workflows/catalog-scan.yml`.
- Diff scanned entries vs. embedded catalog; open PR with additions/updates.
- **Open question §7.3:** auto-merge policy — recommend label `catalog-bot` + human review required.

### V123-3.5 — Runbook docs for reviewers
- `docs/site/developer-guide/catalog-scan-runbook.md`: how to review a bot PR, trust score rubric, reject criteria.

---

## Epic V123-4 — Documentation + release cut (5 stories, all backlog)

### V123-4.1 — User-guide docs
- `docs/site/user-guide/catalog-sources.md`
- `docs/site/user-guide/verified-signatures.md`
- Update `mkdocs.yml` nav.

### V123-4.2 — Operator docs
- `docs/site/operator/catalog-trust-policy.md` (already seeded in V123-2.3).
- Update `docs/site/operator/supply-chain.md` with catalog-signing section.

### V123-4.3 — Developer docs
- `docs/site/developer-guide/catalog-scan-plugins.md`
- Update `CONTRIBUTING-catalog.md` (how to contribute a new embedded entry).

### V123-4.4 — `bmad-code-review` + `security-auditor` sweep
- Full review of v1.23 landed code against design doc.
- Artifact: `.bmad/output/reviews/v1.23-code-review.md`.

### V123-4.5 — Changelog + merge `design/v1.23-extensibility` → main + tag v1.23.0
- Only cut tag when user explicitly asks (per `feedback_release_cadence.md`).
- CHANGELOG entry covers all 4 epics.

---

## Beyond v1.23

- **V2 hardening epic** (task #82): SSO/OIDC, scoped RBAC, external vault, HA
  multi-replica, encryption key rotation, written threat model, governance files,
  API stability commitments, E2E scale tests. See
  `~/.claude/projects/.../memory/project_sharko_roadmap.md` + `project_attribution_design.md`.
