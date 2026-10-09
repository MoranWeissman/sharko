---
title: "Sharko v2.0.0 — Third-Party Security Review Prep Bundle"
audience: "External security consultant (CNCF-coordinated or directly contracted) unfamiliar with Sharko"
author: "Moran Weissman <moran.weissman@gmail.com>"
date: "2026-06-02"
companion: "docs/design/2026-06-02-threat-model-v2.md (Sharko v2.0.0 threat model)"
status: "Pre-launch — review engagement not yet scheduled"
---

# Sharko v2.0.0 — Third-Party Security Review Prep Bundle

> This bundle exists so that a security consultant unfamiliar with
> Sharko can ramp up in under one working day, focus their effort on
> the highest-leverage areas, and avoid re-flagging gaps the
> maintainer is already tracking. It is intentionally written for
> someone arriving at the repo cold; if you are already familiar
> with Sharko, skip to [§3 Suggested review focus areas](#3-suggested-review-focus-areas).
>
> Companion artifact: the v2.0.0 threat model at
> `docs/design/2026-06-02-threat-model-v2.md`. Section numbers below
> referenced as "TM §N" point into that document.

## 1. Project overview

### What Sharko is

Sharko is an **addon management server for Kubernetes fleets,
built on ArgoCD**. It is a server-first product — the HTTP API is
the product, the CLI and web UI are clients of that API. Sharko
sits between a human operator (platform engineer / SRE) and the
underlying GitOps engine (ArgoCD), handling the day-to-day
operations of enabling, configuring, and upgrading Helm-shaped
addons across a fleet of Kubernetes clusters.

The core mental model is:

1. The operator's Git repository holds the source-of-truth YAML
   (`managed-clusters.yaml`, `addon-catalog.yaml`, per-cluster Helm
   values).
2. Sharko writes to that repository via pull requests (PR-only Git
   flow, never direct commits), with auto-merge gated by the
   operator's branch-protection configuration.
3. Sharko reconciles ArgoCD's view of the fleet (ArgoCD cluster
   Secrets) from the source-of-truth YAML on a 30-second
   safety-net cadence plus an immediate post-merge trigger.
4. ArgoCD performs the actual cluster apply.
5. Sharko surfaces fleet state (drift, version matrix,
   marketplace, audit log) back to the operator via the UI and
   API.

### Who uses Sharko

The intended adopter profile:

- Platform engineering / SRE teams operating a fleet of 5–50
  Kubernetes clusters (typically EKS today; GKE / AKS / on-prem
  in scope for v3+).
- Already using ArgoCD as their GitOps engine.
- Currently managing addons either by hand-edited
  ApplicationSets, individual Helm releases, or a homegrown
  fleet-management script.

Pre-v2.0.0, **there are zero production adopters by maintainer
intent**. The v1.x line was the development cycle; v2.0.0 is the
first production release. The
[`ADOPTERS.md`](https://github.com/MoranWeissman/sharko/blob/main/ADOPTERS.md)
shipped in V2-6.1 is the public adopter registry; populating it
is part of post-launch.

### What threats matter most to Sharko

In rough order of impact-on-adopters:

1. **Fleet-wide credential leak** — Sharko holds the keys to
   every managed cluster (kubeconfigs / IAM-role assumptions /
   AWS-SM secret values). A Sharko compromise that exfiltrates
   cluster credentials = fleet compromise. The V2-2.4
   `RedactHandler` + push-based reconciliation + AES-256-GCM
   at-rest are the layered defenses.
2. **Cluster Secret tampering** — Modification of the ArgoCD
   cluster Secret in Sharko's own namespace, causing ArgoCD to
   apply to the wrong cluster. The V125-1-8 ownership-label gate
   + reconciler convergence is the layered defense.
3. **Supply-chain compromise of the catalog** — A malicious
   catalog entry that surfaces as Verified to the operator and
   gets installed across the fleet. The V123 cosign signing +
   anchored trust-regex + TUF cache is the layered defense.
4. **Auth bypass** — Unauthenticated access to mutating
   endpoints, or session-cookie / API-key honored beyond its
   validity. The V125-1-7 token-leak class closure + per-handler
   `requireAdmin` discipline + `audit_coverage_test.go`
   regression suite are the layered defenses.
5. **Secret leak in logs** — Credential-shaped values reaching
   stdout / log aggregator / S3 bucket. The V2-2.4 RedactHandler
   is the primary defense; remaining call-site cleanup is
   tracked in
   [`logging-audit-punchlist.md`](https://github.com/MoranWeissman/sharko/blob/main/docs/site/developer-guide/logging-audit-punchlist.md).

### What is explicitly out of scope for Sharko

- ArgoCD itself (upstream threat model applies).
- Downstream addon contents (cert-manager, external-secrets, etc.
  — operator chooses these, Sharko delivers them with integrity
  guarantees but not content guarantees).
- Cloud-provider infrastructure (AWS IAM, EKS control plane).
- The operator's source-of-truth Git repository's security
  posture (operator owns it).

## 2. Repository map

### Where to start reading

Reviewers typically read these first (estimated 30–45 minutes for
the orientation pass):

1. **[`CLAUDE.md`](https://github.com/MoranWeissman/sharko/blob/main/CLAUDE.md)** — project conventions and constraints. Tells you the rules the maintainer applies to every change (e.g. "every API endpoint must have swagger annotations and regenerated docs", "no internal-org references anywhere").
2. **[`README.md`](https://github.com/MoranWeissman/sharko/blob/main/README.md)** — high-level overview + quickstart.
3. **[`SECURITY.md`](https://github.com/MoranWeissman/sharko/blob/main/SECURITY.md)** — disclosure policy, reporting channel, in-scope / out-of-scope security paths.
4. **[`docs/design/2026-06-02-threat-model-v2.md`](https://github.com/MoranWeissman/sharko/blob/main/docs/design/2026-06-02-threat-model-v2.md)** — this review's companion document.
5. **[`.claude/team/security-auditor.md`](https://github.com/MoranWeissman/sharko/blob/main/.claude/team/security-auditor.md)** — internal audit checklist; covers the maintainer's own discipline expectations.
6. **[`docs/site/operator/security.md`](https://github.com/MoranWeissman/sharko/blob/main/docs/site/operator/security.md)** — operator-facing security reference.

### Top-level layout

```
sharko/
├── cmd/
│   ├── sharko/                # Main binary (server + CLI)
│   ├── catalog-scan-bot/      # Daily catalog scanner bot
│   └── schema-gen/            # Schema regeneration tool
├── internal/                  # Go source (private packages)
│   ├── api/                   # HTTP handlers + middleware + routing
│   ├── auth/                  # Login + session management
│   ├── authz/                 # RBAC tier enforcement
│   ├── argosecrets/           # ArgoCD cluster Secret writes
│   ├── audit/                 # Audit log + SSE stream
│   ├── catalog/               # Catalog cache + loader
│   │   ├── signing/           # Cosign verification
│   │   └── sources/           # Catalog source types
│   ├── clusterreconciler/     # V125-1-8 reconciler
│   ├── crypto/                # AES-256-GCM at-rest encryption
│   ├── gitprovider/           # GitHub PAT / OAuth + PR creation
│   ├── helm/                  # Helm CLI shell-out wrapper
│   ├── logging/               # slog discipline + RedactHandler
│   ├── metrics/               # Prometheus exposition
│   ├── orchestrator/          # Operation routing + Git mutex
│   ├── prtracker/             # PR-merge polling + onMerge callback
│   ├── providers/             # AWS SM / K8s Secrets / ArgoCD
│   ├── remoteclient/          # Connect-operate-disconnect on managed clusters
│   ├── schema/                # Envelope + JSON Schema validation
│   └── secrets/               # Addon-secret push reconciler
├── ui/                        # React + Vite frontend (bundled into binary)
├── charts/sharko/             # Helm chart (Restricted PSS compliant)
├── docs/                      # Documentation
│   ├── design/                # Design RFCs (THIS DOCUMENT)
│   ├── site/                  # MkDocs site (operator + developer guides)
│   └── swagger/               # Auto-generated OpenAPI 2.0 spec
├── tests/e2e/                 # kind-backed e2e harness
├── SECURITY.md                # Disclosure policy
├── CLAUDE.md                  # Project conventions
├── MAINTAINERS.md
├── GOVERNANCE.md
└── CODE_OF_CONDUCT.md
```

### Where the load-bearing code lives

| Concern | Location | Notes |
|---|---|---|
| Auth (login, session, API keys) | `internal/auth/`, `internal/api/auth.go` | Bcrypt + crypto/rand cookies. V125-1-7 token-leak class closed. |
| RBAC enforcement | `internal/authz/`, `internal/api/middleware.go` | `requireAdmin` first thing on every write handler. Per-handler list in [`security-auditor.md` §2](https://github.com/MoranWeissman/sharko/blob/main/.claude/team/security-auditor.md). |
| Audit log | `internal/audit/`, `internal/api/audit*.go` | Stable action codes; SSE stream at `/api/v1/audit/stream`; `audit_coverage_test.go` regression suite. |
| Cluster credential handling | `internal/providers/` | AWS SM (`aws_sm.go`, `aws_auth.go`), K8s Secrets (`k8s_secrets.go`), ArgoCD (`argocd_provider.go`). Push-based, no caching. |
| ArgoCD cluster Secret writes | `internal/argosecrets/` (especially `manager.go` + `labels.go`) | Owns the `app.kubernetes.io/managed-by: sharko` label. Shared `BuildSecretConfigJSON` + `BuildClusterSecretLabels` wrappers. |
| Cluster reconciler (V125-1-8) | `internal/clusterreconciler/` | 30s tick + immediate post-merge trigger. Reconciles from `managed-clusters.yaml`. |
| Catalog signing | `internal/catalog/signing/` (especially `verify.go`, `policy.go`) | cosign-keyless via Fulcio + Rekor; TUF cache; anchored trust regex on `workflow_run` SAN. |
| Schema envelope (V125-1-9) | `internal/schema/` | JSON Schema validation; the trust boundary for YAML inputs. |
| Smart-values AI pipeline | `internal/orchestrator/ai_annotate.go`, `ai_guard.go` | V121-7 secret-leak guard (`secret_leak_blocked` audit event). |
| SSRF guard | `internal/api/` middleware + `security.md` § SSRF | RFC1918 / link-local / loopback deny + optional `SHARKO_URL_ALLOWLIST`. |
| Webhook signature verification | `internal/api/webhooks.go` | HMAC-SHA256 via `X-Hub-Signature-256` + `SHARKO_WEBHOOK_SECRET`. |
| At-rest encryption (Sharko's own tokens) | `internal/crypto/` + `internal/auth/` | AES-256-GCM via `SHARKO_ENCRYPTION_KEY`. |
| RedactHandler (defense-in-depth on logs) | `internal/logging/redact.go` | First in slog handler chain; redacts credential-shaped attribute values. |

### Where the tests live

| Test type | Location | How to run |
|---|---|---|
| Unit tests | per-package `*_test.go` | `go test ./...` |
| Race-detection tests | per-package | `go test ./... -race -count=1` |
| Audit coverage regression | `internal/api/audit_coverage_test.go` | `go test ./internal/api -run TestAuditCoverage` |
| In-process e2e (~30s) | `tests/e2e/` | `make test-e2e-fast` |
| Full kind-backed e2e (~10-15 min) | `tests/e2e/` | `make test-e2e` |
| Webhook signature tests | `internal/api/webhooks_test.go` | `go test ./internal/api -run TestWebhook` |
| Validation suite | `internal/schema/` | `go test ./internal/schema` |

The CI pipeline runs unit + race + e2e-fast + lint + swagger-check
+ schemas-up-to-date + validate-sharko-config + helm-validate +
security-scan + actionlint on every PR. The `make test-e2e` kind
suite is the release-gate run.

## 3. Suggested review focus areas

These are the areas where the maintainer most wants fresh eyes.
They are ordered by leverage — the highest-impact-per-reviewer-hour
first. The maintainer's claim is that these areas are *defended*;
the request is for verification that the defense is *correct* and
*complete*.

### Focus 1: Cluster credential handling in `internal/providers/` (HIGH leverage)

**Why this matters.** Cluster credentials are Sharko's most
sensitive asset class. A leak here is a fleet-wide compromise.
The providers are also the place where Sharko's defenses
(push-based, no caching, RedactHandler) meet the upstream
realities (AWS-SM API shapes, K8s Secret formats).

**Specific files to read carefully.**

- `internal/providers/provider.go` — provider interface.
- `internal/providers/aws_sm.go` — AWS Secrets Manager fetch.
- `internal/providers/aws_auth.go` — IRSA / EKS STS token mint.
- `internal/providers/k8s_secrets.go` — K8s Secrets fallback.
- `internal/providers/kubeconfig_parser.go` — parses fetched
  kubeconfig blobs.
- `internal/providers/argocd_provider.go` — ArgoCD-side Secret
  read.

**Questions the maintainer would like answered.**

- Does the connect-operate-disconnect contract hold? Verify no
  long-lived kubeconfig caching anywhere in
  `internal/remoteclient/` or the provider implementations.
- Is the AWS SDK retry loop bounded? An unbounded retry could
  hold a kubeconfig in memory longer than intended.
- Does the IRSA token refresh path correctly invalidate stale
  tokens before reuse?
- Is the `internal/providers/kubeconfig_parser.go` defensively
  written against adversarial inputs (e.g. kubeconfigs with
  embedded shell commands in the `exec` plugin format)?

**Maintainer's own confidence level.** Moderate-high. The push-
based model is design-level enforced. The remaining hot spot is
the `exec` plugin support in kubeconfigs — see TM §5.3 B3-S +
B3-E for the residual risk discussion.

### Focus 2: ArgoCD Secret writes — cross-namespace RBAC + idempotency (HIGH leverage)

**Why this matters.** Sharko writes ArgoCD cluster Secrets into
the ArgoCD namespace, which is a different K8s namespace from
Sharko's own. The cross-namespace RBAC and the idempotency of
writes are the two failure modes most likely to result in
silent drift.

**Specific files to read carefully.**

- `internal/argosecrets/manager.go` — the canonical writer.
- `internal/argosecrets/labels.go` — `IsManagedBySharko` +
  `ApplyManagedBySharkoLabel`.
- `internal/clusterreconciler/reconciler.go` — converges the
  Secret store from `managed-clusters.yaml`.
- `internal/clusterreconciler/safety.go` (if exists; the
  ownership-label gate enforcement) — every delete checks
  `IsManagedBySharko` before acting.
- `charts/sharko/templates/rbac.yaml` — the ClusterRole granting
  ArgoCD-namespace Secret read + write.

**Questions the maintainer would like answered.**

- Is every Secret-delete code path gated by
  `IsManagedBySharko(secret)`? A bypass is a critical finding per
  [`security-auditor.md` §11](https://github.com/MoranWeissman/sharko/blob/main/.claude/team/security-auditor.md#11-ownership-label-gate-v125-1-8).
- Are the Secret payloads byte-identical between the
  `argosecrets.Manager` (orchestrator path) and the
  `clusterreconciler.Reconciler` (reconciler path)? Both share
  the `BuildSecretConfigJSON` + `BuildClusterSecretLabels`
  wrappers; verify no path bypasses the wrappers.
- Does the reconciler correctly handle the case where the
  source-of-truth YAML lists a cluster the ArgoCD namespace
  doesn't have a Secret for (create) AND the case where the
  ArgoCD namespace has a Sharko-owned Secret for a cluster the
  YAML doesn't list (orphan-delete with label-gate check)?
- Is the K8s API retry-on-conflict (optimistic concurrency)
  correctly handled? `resourceVersion` mismatches should retry,
  not silently lose writes.

**Maintainer's own confidence level.** High on the happy path
(V125-1-8 was 3 RC cycles in production-shape e2e). Moderate on
the edge cases (concurrent reconciler + orchestrator writes —
the V125-1-8 design has a comment about this in
`docs/design/2026-05-11-cluster-secret-reconciler-and-gitops-stance.md`).

### Focus 3: Webhook signature paths (HIGH leverage)

**Why this matters.** Webhook endpoints are internet-facing by
default. Signature verification is the only defense between
"public endpoint" and "fleet-wide reconcile trigger". A
verification flaw — timing attack, comparison bug, secret
fall-open path — is a CVE-class finding.

**Specific files to read carefully.**

- `internal/api/webhooks.go` — main webhook handler, signature
  verification at line 54 (per
  `grep -n "SHARKO_WEBHOOK_SECRET" internal/api/webhooks.go`).
- `internal/api/webhooks_test.go` — test suite verifying the
  positive + negative cases (good sig, bad sig, empty secret).

**Questions the maintainer would like answered.**

- Is the HMAC comparison constant-time? `hmac.Equal` or
  `subtle.ConstantTimeCompare` should be used; a `==` byte
  comparison is a timing-attack surface.
- When `SHARKO_WEBHOOK_SECRET` is empty, what happens?
  Per `security.md` § Webhook Security, an empty secret
  *disables* verification. Is the empty-secret path defensible
  in operator-error scenarios?
- Are replay attacks defended? GitHub provides an `X-GitHub-Delivery`
  ID; is Sharko using it? (Likely no for v2.0.0 — flag as
  follow-up.)
- Does the verification consume the request body in a way that
  later handlers can still read it? If the body is consumed by
  the verifier and the handler re-reads, the handler will see
  an empty body — possibly causing a silent no-op.

**Maintainer's own confidence level.** Moderate. Test coverage
is present; the constant-time comparison + replay defense are
the most-likely gotchas.

### Focus 4: Smart-values AI pipeline secret-leak detection (HIGH leverage)

**Why this matters.** The V121-7 secret-leak guard hard-blocks
the LLM call on any secret-like pattern in `values.yaml`. The
guard is the only thing between an operator's misconfigured
`values.yaml` and an LLM-provider third-party seeing their
API keys.

**Specific files to read carefully.**

- `internal/orchestrator/ai_annotate.go` — the LLM call site.
- `internal/orchestrator/ai_guard.go` — the secret-leak guard
  with pattern set + audit code emission.
- `internal/orchestrator/ai_annotate_test.go` — coverage of
  positive (block) + negative (allow) cases.
- `internal/orchestrator/ai_guard_test.go` — coverage of the
  individual patterns.
- `docs/site/operator/security.md` § Secret-leak guard on AI
  annotation — operator-facing reference.

**Questions the maintainer would like answered.**

- Is the pattern set comprehensive? The current set: AWS keys,
  GitHub PATs, JWTs, PEM blocks, Slack tokens, Google API keys,
  generic API key / password assignments, high-entropy base64
  blobs. Are there secret shapes the set misses?
- Is the guard called BEFORE every LLM-provider request, with
  no bypass path? A bypass would be a critical finding.
- Does the audit event (`secret_leak_blocked`) include enough
  context for forensic review (handler source, chart + version,
  match count, pattern names — but never the matched bytes)?
- What happens if the LLM provider's API is bypassed (e.g.
  Ollama running on the operator's own infra)? The guard still
  fires regardless of provider — verify this.

**Maintainer's own confidence level.** High on the canonical
path (V121-7 was extensively tested). Lower on the "exotic
secret shape" coverage — the maintainer would specifically
appreciate fuzzing against the guard pattern set.

### Focus 5: Helm chart RBAC scope (MEDIUM leverage)

**Why this matters.** The Helm chart's ClusterRole defines
Sharko's K8s permissions. Overly broad permissions = fleet-wide
blast radius on Sharko compromise. The chart ships with what the
maintainer believes is narrow scope, but a reviewer with deep
K8s RBAC knowledge can spot opportunities for further narrowing.

**Specific files to read carefully.**

- `charts/sharko/templates/rbac.yaml` — ClusterRole + binding.
- `charts/sharko/templates/serviceaccount.yaml` — SA definition.
- `charts/sharko/templates/deployment.yaml` — SA → pod binding.
- `charts/sharko/values.yaml` — RBAC-relevant defaults
  (`rbac.argocdNamespace`, `config.nodeAccess`, etc.).
- `docs/site/operator/security.md` § RBAC — operator-facing
  documentation.

**Questions the maintainer would like answered.**

- Is the ClusterRole as narrow as it can be while still serving
  the Sharko API surface? Specifically:
  - `get`, `list`, `watch` on ArgoCD CRDs — could any be
    dropped?
  - Secret read + write in the ArgoCD namespace — could this be
    split into separate roles for read vs write?
  - Node read (for the Dashboard widget) — the `config.nodeAccess`
    flag exists to disable this; is the disable path tested?
- Are there any cluster-scoped permissions that could be reduced
  to namespace-scoped?
- Does the Helm chart enforce the Restricted PSS standard, or
  does it just default to it? An operator who overrides
  `securityContext` should not silently downgrade.

**Maintainer's own confidence level.** Moderate. The chart was
last RBAC-audited at v1.25.0-pre.0 (V125-1-8 narrowed the ArgoCD
interaction). The cluster-scope vs namespace-scope question is
a known trade-off.

### Other focus areas worth a pass if budget allows

- **Schema envelope validation (`internal/schema/`)** — the trust
  boundary for YAML inputs. Per
  [`security-auditor.md` §12](https://github.com/MoranWeissman/sharko/blob/main/.claude/team/security-auditor.md#12-schema--envelope-integrity-v125-1-9),
  any direct `yaml.Unmarshal` over untrusted bytes that bypasses
  the validator is a critical finding.
- **Catalog signing surface (`internal/catalog/signing/`)** —
  V123-2 was production-hardened with 4 RC cycles; the
  [`security-auditor.md` § Catalog signing surface](https://github.com/MoranWeissman/sharko/blob/main/.claude/team/security-auditor.md#catalog-signing-surface-v123-2)
  section documents the four landmines (TUF cache path, per-entry
  verification, trust regex anchoring, workflow_run SAN encoding,
  modern Sigstore Bundle format).
- **Bundled UI dependency CVEs** — `ui/package.json`; the
  frontend dependency surface is not as deeply scanned as the Go
  surface today. A pass with `npm audit` + dependency scope
  review would be valuable.

## 4. Threat model pointer + summary

The threat model at
`docs/design/2026-06-02-threat-model-v2.md` (TM) is the
companion document. The structure:

- **TM §3** — Asset inventory (7 assets).
- **TM §4** — Trust boundaries (6 primary + 2 ancillary).
- **TM §5** — STRIDE per primary boundary (6×6 = 36 cells, each
  with threat description + existing mitigation + residual risk +
  gap flag if any).
- **TM §6** — Ancillary boundaries (lighter coverage).
- **TM §7** — Attack surface table cross-linked to the
  [`api-stability.md`](https://github.com/MoranWeissman/sharko/blob/main/docs/site/developer-guide/api-stability.md)
  128-endpoint tier inventory.
- **TM §8** — OWASP Top 10 (2021) mapping.
- **TM §9** — CNCF / SLSA supply-chain analysis (Sharko is at
  SLSA Build L2 with a documented L3 path).
- **TM §10** — Comprehensive mitigations table (40 entries; ~95%
  reference V2-shipped artifacts).
- **TM §11** — Identified gaps (11 entries; all tracked in the
  public roadmap).
- **TM §12** — Disclosure process (best-effort solo-maintainer
  baseline pending CNCF Sandbox transition).
- **TM §13** — References.

### Top-line threat model claims

1. **Defense in depth at every long-lived credential surface.**
   Not zero residual risk — the maintainer claims documented
   layered defense.
2. **Ownership-label gate is the canonical "mine" signal.** Every
   ArgoCD cluster Secret Sharko writes carries
   `app.kubernetes.io/managed-by: sharko`, and every Secret-delete
   path checks the label before acting.
3. **No AVP, no Redis bridge.** Sharko's secret-handling model is
   push-based and architecturally rejects bridging through ArgoCD's
   Redis cache. This is a settled design decision; see
   [`security-auditor.md` §13](https://github.com/MoranWeissman/sharko/blob/main/.claude/team/security-auditor.md#13-no-avp--no-redis-leak-design-constraint).
4. **Schema envelope is the trust boundary for YAML.** Every read
   of `managed-clusters.yaml` and `addon-catalog.yaml` must go
   through the envelope-aware loader; direct `yaml.Unmarshal` over
   untrusted bytes is a critical finding.
5. **Catalog trust is anchored, not unanchored.** Operator-supplied
   trust regexes must use `^...$`; the policy loader rejects
   unanchored patterns at load.

## 5. Existing security artifacts (so you don't re-flag)

The list below is the V2-shipped + load-bearing pre-V2 security
artifacts. A reviewer who flags any of these as "missing" is
likely working from incomplete context. The threat model § 10
has the comprehensive 40-row table; this section is the
abbreviated cheat sheet.

### Logging + observability

- **V2-2.4 RedactHandler slog wrapper** (PR
  [#368](https://github.com/MoranWeissman/sharko/pull/368)).
  First in slog handler chain. Redacts credential-shaped values
  before serialization. Defense-in-depth.
- **V2-2.x audit log discipline** (PR
  [#367](https://github.com/MoranWeissman/sharko/pull/367)).
  Stable audit action codes + `request_id` correlation +
  `/audit/stream` SSE.
- **V2-3 Prometheus telemetry + multi-burn-rate alerts** (PRs
  [#371-373](https://github.com/MoranWeissman/sharko/pull/371)).
  Operational visibility is security visibility.

### Operator runbooks (V2-4)

- **35 operator runbooks** covering every P0 + P1 failure mode.
- **12 P0 security-shaped runbooks**: `auth-bypass.md`,
  `credential-leak-in-logs.md`, `secret-push-silently-failed.md`,
  `secrets-provider-unreachable.md`, `oom-restart-loop.md`,
  `reconciler-crash-loop.md`, `argocd-upstream-unreachable.md`,
  `argocd-pr-merge-no-converge.md`, `git-provider-unreachable.md`,
  `catalog-trust-root-unavailable.md`, `init-operation-deadlocked.md`.
- **`failure-mode-index.md`** with 57 modes (18 P0 / 22 P1 / 17 P2).

### Catalog supply chain (V123 + V124)

- cosign-keyless signing on container image + Helm chart + release
  binaries.
- Per-entry catalog signing with anchored trust regex on `workflow_run`
  SAN.
- TUF cache on writable path under read-only rootfs.
- Modern Sigstore Bundle format end-to-end.

### Cluster integrity (V125)

- **V125-1-8 cluster reconciler + ownership-label gate**.
  Canonical writer of ArgoCD cluster Secrets; safety-net 30s
  cadence + immediate post-merge trigger.
- **V125-1-10 ArgoCD-Secret-only reads**. Least privilege.
- **V125-1-11 typed `ProviderConfig` split**. Compile-time
  prevention of cross-domain credential leakage.
- **V125-1-9 schema envelope** with read-time JSON Schema
  validation. Trust boundary for YAML inputs.

### Governance + commit provenance (V2-6)

- **V2-6.1 SECURITY.md** + `MAINTAINERS.md` + `GOVERNANCE.md` +
  `CODE_OF_CONDUCT.md` + `CONTRIBUTING.md` + `ADOPTERS.md`.
- **V2-6.4 DCO `Signed-off-by`** per commit.

### Auth + RBAC

- bcrypt password + API-key hashing (cost 10).
- API-key plaintext shown ONCE on creation.
- Session cookies: 32-byte `crypto/rand`, HttpOnly, SameSite=Lax,
  24h lifetime.
- Rate limiting on `/auth/login` (10/IP/min) + admin writes
  (30/IP/min).
- Three RBAC tiers (Admin / Operator / Viewer); `requireAdmin` on
  every write handler.
- `audit_coverage_test.go` regression suite verifies every
  mutating handler emits an audit entry.

### Network + transport

- Security headers (CSP / HSTS / X-Frame-Options /
  X-Content-Type-Options / Referrer-Policy).
- CSRF tokens on mutating endpoints.
- SSRF guard on URL-fetching endpoints (RFC1918 / link-local /
  loopback deny + optional `SHARKO_URL_ALLOWLIST`).
- HMAC-SHA256 webhook verification.

### At rest

- AES-256-GCM encryption on `sharko-connections` Secret
  (`SHARKO_ENCRYPTION_KEY`).
- Tiered Git attribution: Tier 1 service / Tier 2 per-user PAT
  AES-256-GCM-encrypted.

### Pod hardening

- Restricted PSS-compliant pod (non-root, read-only rootfs, no
  priv-esc, all caps dropped).
- Helm chart ServiceAccount + narrow ClusterRole.

### AI pipeline

- V121-7 secret-leak guard on AI annotation (`secret_leak_blocked`
  audit code). Hard-block on any secret-like pattern in
  `values.yaml`. No override.

## 6. Known tracked gaps (so you don't surprise the maintainer)

The threat model §11 has the full list with cross-links to the
public roadmap. The summary below is the cheat sheet — if a
reviewer flags any of these as "find", the maintainer's response
will be "yes, tracked, here's the line in the roadmap."

| # | Gap | Severity | Tracked in |
|---|---|---|---|
| G01 | Fine-grained RBAC (per-cluster / per-environment / per-addon scopes) | Medium | [`roadmap.md` § Medium-term — Fine-grained RBAC](https://github.com/MoranWeissman/sharko/blob/main/docs/site/community/roadmap.md) |
| G02 | SSO / OIDC with group → role mapping | Medium | [`roadmap.md` § Medium-term — SSO / OIDC](https://github.com/MoranWeissman/sharko/blob/main/docs/site/community/roadmap.md) |
| G03 | External vault for Sharko's own service-identity tokens | Medium | [`roadmap.md` § Medium-term — Cloud-provider full support](https://github.com/MoranWeissman/sharko/blob/main/docs/site/community/roadmap.md) |
| G04 | HA multi-replica (single-pod SPOF today) | Medium | [`roadmap.md` § Operator mode (v3+)](https://github.com/MoranWeissman/sharko/blob/main/docs/site/community/roadmap.md) |
| G05 | Encryption key rotation tooling (manual today) | Low | v2.x near-term post-launch |
| G06 | Formal disclosure SLO (best-effort solo today) | n/a (process) | `SECURITY.md` + this TM |
| G07 | GitHub Security Advisory CVE flow (no CNA today) | n/a (process) | `SECURITY.md` |
| G08 | Scale testing > 100 clusters | Low | v2.x near-term post-launch |
| G09 | Periodic dependency auto-update pipeline (manual today) | Low | v2.x near-term post-launch |
| G10 | Periodic credential-shape scan on rendered API responses (manual today) | Low | v2.x near-term post-launch |
| G11 | Tamper-evident audit log (append-only today; downstream immutability is operator-managed) | Medium | v3+ topic |

If you find a *different* gap not in this list, that's exactly the
kind of finding the maintainer is paying for. If you find a gap
that IS in this list, please reference the row above in your
report so the maintainer can confirm it's the same item.

## 7. How to reach the maintainer

### Disclosure channel (security findings)

- **Email:** `moran.weissman@gmail.com`
- **Subject line:** `[Sharko Security] <one-line summary>`
- **PGP:** available on request

The maintainer is solo and aims for:

- 5 business days for acknowledgment.
- 30 days for HIGH severity fix from acknowledgment.
- 90 days for MEDIUM severity fix.
- No SLO on LOW (tracked in backlog).

For coordinated disclosure, please indicate your intended public
disclosure date in your initial email so the maintainer can plan
a release + advisory cadence.

### Engagement channel (review questions, scope clarifications)

- Same email as above. Subject: `[Sharko Review] <question>`.
- Async-first; the maintainer is in a single timezone (currently
  CET / UTC+1).
- Expect 24–48 hour turnaround on non-blocking questions; faster
  on blockers.

### Escalation path for HIGH-severity findings

If you find something HIGH severity that requires immediate
attention:

1. Email with subject `[Sharko Security] [URGENT] <summary>`.
2. If no response within 24 hours, the maintainer is likely
   on holiday — escalate by:
   - Opening a Confidential Issue on GitHub (do NOT post
     details publicly).
   - Pinging `@MoranWeissman` on the GitHub issue.
3. Public disclosure should follow the agreed coordinated
   timeline.

### What to do if you find evidence of *active exploitation* in the wild

This is not a hypothetical for v2.0.0 (no production adopters
yet), but the policy:

- Email immediately with subject
  `[Sharko Security] [ACTIVE EXPLOITATION] <CVE-class summary>`.
- Aim to coordinate on disclosure within 48 hours.
- The maintainer will prioritize fix + release above all other
  work.

## 8. Engagement scope template

This is a template for what a CNCF-coordinated or directly-
contracted security engagement could cover. The maintainer's
recommended scope ordering is by leverage (highest-impact first);
adjust based on consultant expertise and budget.

### In-scope artifacts

- The Sharko HTTP API (~128 endpoints; tier inventory in
  [`api-stability.md`](https://github.com/MoranWeissman/sharko/blob/main/docs/site/developer-guide/api-stability.md)).
- The Sharko CLI.
- The Sharko web UI (bundled into the binary; React + Vite).
- The Helm chart (`charts/sharko/`) including the Restricted PSS
  compliance claim.
- The container image (`ghcr.io/moranweissman/sharko`) and the
  cosign signature.
- The catalog signing pipeline + trust policy.
- The schema envelope validation surface.

### Suggested engagement components

1. **Code review** (recommended highest priority).
   - Targets: the five focus areas in [§3](#3-suggested-review-focus-areas).
   - Estimated effort: 3–5 reviewer-days.
   - Expected deliverable: ranked findings list with severity +
     CVE-shape + recommended remediation per finding.

2. **SAST scan**.
   - Targets: full `internal/` and `cmd/` Go source.
   - Recommended tools: Semgrep, CodeQL, gosec.
   - Expected deliverable: triaged SAST findings with maintainer-
     marked false-positives carved out.
   - Sharko-specific carve-outs:
     - The `internal/schema/` envelope validator: this code
       intentionally allows pre-validation byte handling that
       would look suspicious to a SAST. The trust boundary is
       the validator itself.
     - The `internal/catalog/signing/` paths: V123-2 was
       production-hardened with 4 RC cycles. Re-running SAST
       here is fine but the maintainer is unlikely to act on
       SAST-only findings against this surface without a
       specific exploit narrative.
     - The `internal/orchestrator/ai_guard.go` pattern set:
       intentional regex matching against credential shapes;
       SAST may flag the regex compilation.

3. **DAST scan**.
   - Targets: a deployed Sharko instance in a `kind` test
     cluster.
   - Recommended tools: OWASP ZAP, Burp Suite, nuclei.
   - Setup: `make test-e2e` brings up a kind cluster + Sharko +
     ArgoCD + an in-cluster fake Git server. The maintainer can
     provide a ready-to-go fixture.
   - Expected deliverable: triaged DAST findings with
     reproduction steps.
   - Sharko-specific carve-outs:
     - The `/metrics` endpoint is intentionally unauthenticated
       (operator NetworkPolicy is the defense).
     - The `/swagger/*` endpoint documents the API surface;
       operators can disable it.

4. **Threat-model validation**.
   - Target: this document + the threat model.
   - Expected deliverable: review-back on the asset inventory,
     trust boundaries, STRIDE coverage, OWASP mapping, and
     gaps. Specifically valuable: any boundary or asset the
     maintainer missed, any STRIDE cell where the documented
     mitigation does not actually cover the documented threat,
     any gap that should be elevated above its current
     severity.

5. **Dependency audit**.
   - Targets: `go.mod` (direct + indirect Go dependencies),
     `ui/package.json` (frontend dependencies), `charts/sharko/Chart.yaml`
     (Helm dependencies, if any).
   - Recommended tools: govulncheck, npm audit, trivy.
   - Expected deliverable: triaged dependency findings with
     suggested upgrade paths.

6. **Helm chart review**.
   - Target: `charts/sharko/`.
   - Specific focus: RBAC scope, Pod Security Standard
     compliance, NetworkPolicy template, the Restricted PSS
     defaults.
   - Recommended tools: Polaris, datree, kubelinter.
   - Expected deliverable: chart-level findings with suggested
     `values.yaml` defaults adjustments.

7. **Reproducible-build verification** (lower priority for
   v2.0.0).
   - Target: verify the release artifact (`ghcr.io/...:vX.Y.Z`)
     was built from the public source at the tagged commit.
   - The cosign signature already proves provenance; this
     adds reproducibility.
   - Expected deliverable: confirmation + any remediation
     needed to enable a fully reproducible build.

### Out of scope (please confirm before testing)

- **Active disruption of any running ArgoCD instance.** Sharko
  depends on ArgoCD; testing against a real ArgoCD requires
  operator coordination.
- **The maintainer's own GitHub account.** Per CNCF responsible-
  disclosure norms; any finding here should go to GitHub's
  security team, not Sharko's maintainer.
- **The Sigstore public-good infrastructure** (Fulcio, Rekor).
  Same reasoning — upstream.
- **The cloud-provider services Sharko integrates with** (AWS-SM,
  EKS). Upstream.

### Maintainer expectations

- All findings shared confidentially via the disclosure channel
  before any public disclosure.
- Coordination on disclosure timing so a fix can be released
  alongside the advisory.
- Credit (unless the reviewer prefers anonymity) in the release
  notes + security advisory.
- For findings that the reviewer ranks as "no action recommended,
  documented for awareness", a short rationale so the maintainer
  can choose to document it in the threat model rather than
  silently drop it.

### Engagement timing

- The maintainer's ideal cadence: the review concludes 4–6 weeks
  before the v2.0.0 tag, so HIGH findings have a clear fix
  window.
- v2.0.0 tag is currently estimated 6–8 weeks out (pending
  V2-6.5 merge + V2 retrospective). This review would be the
  ideal moment to engage.
- Post-v2.0.0 cadence: targeting one substantive review per
  major release (so v3.0.0 would warrant a fresh engagement).
  Interim review for high-impact V2.x features is welcome but
  not required.

---

*This bundle is the maintainer's structured handoff to a security
consultant. If you are reading this in the context of an actual
engagement, please confirm scope with the maintainer before
beginning — the maintainer's recommendations above may be
out of date by the time you arrive; the GitHub repo and this
document at HEAD are the source of truth.*
