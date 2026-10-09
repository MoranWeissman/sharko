# Attribution & Permissions Model

**Status:** APPROVED 2026-04-17
**Date:** 2026-04-16 (proposal) / 2026-04-17 (decision)
**Owner:** Moran Weissman
**Decisions confirmed:** Tiered attribution + V2.x scoped RBAC roadmap + hold-public-release-until-V2-hardening

---

## Problem

Today, every Git commit Sharko makes is authored by `Sharko Bot <sharko-bot@users.noreply.github.com>` (`internal/gitprovider/github_write.go:77-79, :100-102`). This means:

- 5 admins sharing one Sharko deployment all show as one Git author
- `git blame` cannot identify who configured a wrong value
- PR review attribution is lost (all PRs appear bot-authored)
- Compliance audit ("who changed Y?") relies on Sharko's audit log alone — no Git-level second source of truth

For pre-production (v1.x) this is acceptable. For V2 production launch and CNCF maturity, this is a real gap.

---

## How peer products handle this (corrected, accurate)

| Product | Values / config editing | Mutation model |
|---|---|---|
| **ArgoCD UI** | Full — edit Application CR, inline `helm.values`, live resource manifests | Live (changes hit cluster); auto-sync optional from Git |
| **Cyclops** | Full Helm values UI (its core product) | Direct apply |
| **Rancher Manager** | Full | Direct apply |
| **Backstage** | Template scaffolding + plugin-defined editors | Mixed (often PR-based) |
| **Port** | Action-driven, can include YAML editing | PR-based |
| **Weave GitOps OSS** | Limited; Enterprise allows more | GitOps controller |
| **Sharko (today)** | `extra_helm_values` flat key=value, hidden under "Advanced" | **PR-based** (every mutation = Git PR) |

**Key insight:** the question isn't "should Sharko allow editing" — most peers do. It's "what's the editing UX, and does it preserve our PR-based mutation model?"

Sharko's PR-based architecture is the differentiator. A values editor that produces PRs is consistent with this. A values editor that bypasses Git would not be.

---

## Today's Git token retrieval (verified in code)

| Source | Used when |
|---|---|
| **Encrypted K8s Secret** `sharko-connections` (AES-256-GCM, configurable via `config.connectionSecretName`) | Default / production. User configures via Settings → Connections; Sharko encrypts at rest. See `internal/config/k8s_store.go:172`. |
| **Env var `GITHUB_TOKEN`** (from Helm `secrets.GITHUB_TOKEN`) | Dev only. Fallback when `SHARKO_DEV_MODE=true` AND no Connection-stored token. See `internal/service/connection.go:312-321`. |
| **External secret vault (Vault / AWS SM / ESO)** | ❌ Not supported for Sharko's own credentials. (Cluster credentials DO support this via `internal/providers/`.) |

External vault integration for Sharko's own Git/ArgoCD tokens is a V2 hardening item, not v1.20.

---

## Proposed model

### Tiered attribution

Two classes of mutation, with different attribution requirements:

**Tier 1 — Operational actions (service token OK):**
- Cluster register / deregister / test / diagnose / refresh / unadopt
- Addon upgrade / downgrade / enable / disable on cluster
- ArgoCD cluster secret sync
- Connection CRUD, AI config, dashboards
- PR refresh / delete, reconcile triggers

**Why service token is acceptable here:**
- The action is *replayable from the catalog* — if cert-manager goes to 1.14.5 across 5 clusters, anyone could have triggered it; the catalog is the truth
- Attribution lives in Sharko's audit log (who-clicked-what)
- Git history shows "platform did upgrade-cert-manager-to-1.14.5" — true and useful
- Cheap upgrade: add `Co-authored-by: <user>` trailer to the commit message → user gets attribution without infra change

**Tier 2 — Configuration changes (per-user PAT recommended via UX nudge, not enforced):**
- Edit global Helm values for an addon
- Edit per-cluster value overrides
- Edit catalog metadata (sync wave, sync options, ignore differences, additional sources)
- Anything that changes WHAT will be deployed, not WHEN/WHERE

**Why per-user attribution matters here:**
- These changes *define future state* — wrong replicaCount, wrong namespace, wrong feature flag → real outage
- They persist in Git as the new desired state — `git blame` is the natural debugging path
- Reviewing "who configured this addon to use 32GB memory" is a legitimate post-incident question
- The PR review loop benefits from real authorship

### "Set standard, don't enforce" principle

For local users today, force-marching everyone to per-user PAT is friction with no payoff. But:
- **Document** per-user PAT as the recommended pattern
- **UX nudge** in the UI on Tier 2 actions (yellow banner if no PAT configured)
- **No blocking** — users can choose service token mode
- **Audit log signal** — small icon shows attribution mode (🔑 service / 👤 per-user / 🤝 co-author)

This sets the cultural standard now and pays off when SSO arrives in V2.x.

### Implementation pattern

```
1. Service token: configured via UI Connections (encrypted K8s Secret).
   Default for all writes.

2. Per-user PAT: optional, set via Settings → My Account → "Add GitHub PAT".
   Encrypted per-user in Sharko's secret. (V2 hardening: support fetching
   from external vault.)

3. When a write happens:
   - Tier 1 (operational): use service token. Add Co-authored-by: <user>
     trailer to commit message and Triggered-by: in PR description.
   - Tier 2 (config): prefer per-user PAT if set; fall back to service
     token with a "no per-user attribution" warning shown in audit entry.

4. Power-user mode (Settings toggle): "Always use my PAT for all writes" —
   opt-in for users who want full attribution.
```

---

## Permission model roadmap

Three phases, intentionally avoiding ArgoCD's Casbin complexity until demand emerges.

### V2.0 (now / next)

**Three hardcoded roles:** Admin / Operator / Viewer. Global scope. Already shipped.

Capabilities by role (already in code):
- **Admin:** all mutations, all reads, all settings
- **Operator:** addon ops, cluster ops, no user/token mgmt
- **Viewer:** reads only

### V2.x (after V2 launch, when demand emerges)

**Resource-scoped roles** added on top of existing 3:
- `Operator on cluster:dev-eu` — operational actions limited to that cluster
- `Admin of addon:cert-manager` — Tier 2 (config) for that addon only
- `Viewer of project:platform-team` — reads scoped to a project

This solves 80% of "but what about this team only owns these clusters" requests without inventing a policy language.

Aligns naturally with the tiered attribution model:
- An "Operator on cluster:dev-eu" can do **Tier 1** actions on that cluster
- An "Admin of addon:cert-manager" can do **Tier 2** actions on that addon

### V3 (only if / when needed)

**Casbin / OPA-style policy engine.** Don't start here. ArgoCD added Casbin because thousands of production users demanded it — they earned it. Sharko hasn't shipped V2 yet. Premature complexity is worse than late.

---

## CNCF maturity assessment

| Stage | Sharko status |
|---|---|
| **Sandbox** (real project + working code + activity) | ✅ Pitchable now-ish; definitely after v1.20 |
| **Incubation** (2-3 production orgs, governance, sustained release, security model, contributor base) | ❌ ~35-45% after v1.20 |
| **Graduated** (large cross-org adoption, ecosystem) | ❌ Far away |

### Gap analysis for Incubation pitch

| Gap | Effort | Why it matters |
|---|---|---|
| **2-3 production users from different orgs** | Adoption (months/years) | CNCF requires real cross-org usage |
| **SSO / OIDC** | Medium | Local users alone is dev-grade |
| **Per-user Git attribution** | Small (this design) | Today's bot-author pattern is sandbox-grade |
| **Resource-scoped RBAC** (V2.x roadmap above) | Medium | Three global roles is too coarse |
| **External secret vault for own credentials** | Small | Enterprises mandate this |
| **HA / multi-replica** | Medium-large | Single replica is a SPOF |
| **Disaster recovery** (encryption key rotation, backup/restore) | Medium | Lose the encryption key, lose all connections |
| **Performance at scale** (proven at 100+ clusters × 50+ addons) | Medium | Currently tested at small scale |
| **Written threat model + third-party security review** | Medium | Standard CNCF expectation |
| **API stability contract** (deprecation policy, versioning, upgrade paths) | Medium | "We don't break v1 → v2" needs a written commitment |
| **Governance docs** (MAINTAINERS, GOVERNANCE, CODE_OF_CONDUCT, contribution ladder) | Small | CNCF asks for these |
| **E2E test coverage at scale** | Medium-large | Test against multiple Helm charts, multiple ArgoCD versions |
| **Sustained release cadence + community signals** | Time-only | Stars, real bug reports, external contributors |

### Trajectory

- After **v1.20** (values editor + per-user PAT + co-author trailer): **~40%** to incubation
- After **V2 hardening epic** (SSO + scoped RBAC + Vault integration + HA + threat model + governance): **~70%**
- After **6-12 months of community + adoption** (real users, contributors, public roadmap, blog posts, talks): **~85% — pitchable to incubation**

The product itself is more mature than 40% suggests. The 40% reflects the **CNCF process bar** (heavy on community / governance / cross-org adoption), not "is the code good."

---

## Concrete v1.20 add-on (this design's footprint)

1. **Tiered token resolution** — Tier 1 writes use service token; Tier 2 writes prefer per-user PAT
2. **Per-user PAT storage** — Sharko user profile gains an encrypted `github_token` field (and `azure_devops_pat` later); UI in Settings → My Account
3. **Co-author trailer always** — every commit, both tiers, includes `Co-authored-by: <user>` from audit context
4. **UX nudge for Tier 2 without PAT** — yellow banner, one-click "set up your token"
5. **Audit log shows attribution mode** — small icon: 🔑 (service token) / 👤 (per-user PAT) / 🤝 (co-author)
6. **No enforcement, no blocking** — just signal

---

## Out of scope (V2 hardening or later)

- External secret vault for Sharko's own Git/ArgoCD tokens (V2 hardening)
- GitHub App + per-user OAuth flow (V2.x — enables cleaner identity for orgs that allow it)
- SSO / OIDC integration (V2.x)
- Resource-scoped RBAC (V2.x)
- Casbin / OPA policy engine (V3, only if needed)
- HA multi-replica (V2 hardening)
- Encryption key rotation tooling (V2 hardening)
- Backup / restore for connection store (V2 hardening)

---

## Decisions needed

1. **Confirm tiered attribution model** (Tier 1 service token / Tier 2 prefer per-user PAT, no enforcement)
2. **Confirm permission roadmap** (V2.0 = 3 global roles, V2.x = scoped roles, V3 = policy engine only if demanded)
3. **Confirm release strategy** (don't release publicly until V2 hardening complete; pre-release with friendly early adopters first)

If yes on all three: fold the v1.20 add-on items above into the v1.20 spec. Defer the V2 hardening epic as a separate planning session.

---

## Related references

- Current Git provider: `internal/gitprovider/github_write.go`, `internal/gitprovider/azuredevops_impl.go`
- Connection storage: `internal/config/k8s_store.go`
- Connection token resolution: `internal/service/connection.go:309-330`
- RBAC: `internal/authz/`
- Audit log (v1.18): `internal/audit/`, `internal/api/audit_middleware.go`
- ArgoCD RBAC reference: https://argo-cd.readthedocs.io/en/stable/operator-manual/rbac/
