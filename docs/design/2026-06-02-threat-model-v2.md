---
title: "Sharko v2.0.0 Threat Model"
author: "Moran Weissman <moran.weissman@gmail.com>"
date: "2026-06-02"
version: "v2.0.0 (production-launch baseline)"
frameworks:
  - "STRIDE per trust boundary (Microsoft + CNCF Security TAG variant)"
  - "OWASP Top 10 (2021)"
  - "SLSA v1.0 (Build tracks L1–L4)"
  - "CNCF Security TAG threat-model guidance"
scope: "Sharko server, CLI, web UI, Helm chart, container image, catalog signing pipeline. Excludes downstream addon contents and ArgoCD itself."
status: "Initial baseline for v2.0.0 production launch — companion to the V2-6.5 review-prep bundle and the [Security reference](../site/operator/security.md)."
related:
  - ".claude/team/security-auditor.md"
  - "SECURITY.md"
  - "docs/site/operator/security.md"
  - "docs/site/operator/supply-chain.md"
  - "docs/site/developer-guide/api-stability.md"
  - "docs/site/operator/failure-mode-index.md"
  - "docs/site/community/roadmap.md"
---

# Sharko v2.0.0 Threat Model

> This document is the v2.0.0 production-launch threat-model baseline.
> It is **not** a penetration-test report. It is a structured inventory
> of the trust boundaries Sharko crosses, the threats that arise at
> each boundary, the V2-shipped mitigations that address them, and the
> residual risks the maintainer is carrying into v2.0.0.
>
> Companion artifact: the V2-6.5 review-prep bundle at
> `.bmad/output/reviews/v2-security-review-prep.md` summarises this
> document for an external security consultant.

## Table of contents

1. [Executive summary](#1-executive-summary)
2. [Scope and non-scope](#2-scope-and-non-scope)
3. [Asset inventory](#3-asset-inventory)
4. [Trust boundaries](#4-trust-boundaries)
5. [STRIDE per primary trust boundary](#5-stride-per-primary-trust-boundary)
   - [B1: Operator browser ↔ Sharko UI](#51-b1-operator-browser--sharko-ui)
   - [B2: Sharko ↔ ArgoCD](#52-b2-sharko--argocd)
   - [B3: Sharko ↔ kube-apiserver](#53-b3-sharko--kube-apiserver)
   - [B4: Sharko ↔ Git provider](#54-b4-sharko--git-provider)
   - [B5: Sharko ↔ secrets provider](#55-b5-sharko--secrets-provider)
   - [B6: Sharko ↔ catalog source](#56-b6-sharko--catalog-source)
6. [Ancillary boundaries (lighter coverage)](#6-ancillary-boundaries-lighter-coverage)
   - [B7: Sharko ↔ container registry](#61-b7-sharko--container-registry)
   - [B8: In-cluster network](#62-b8-in-cluster-network)
7. [Attack surface table](#7-attack-surface-table)
8. [OWASP Top 10 (2021) mapping](#8-owasp-top-10-2021-mapping)
9. [CNCF / SLSA supply-chain analysis](#9-cncf--slsa-supply-chain-analysis)
10. [Existing mitigations — comprehensive table](#10-existing-mitigations--comprehensive-table)
11. [Identified gaps (residual risk for v3+)](#11-identified-gaps-residual-risk-for-v3)
12. [Disclosure process](#12-disclosure-process)
13. [References](#13-references)

---

## 1. Executive summary

Sharko is a server-first addon-management plane that sits between an
operator (the human deploying addons across a Kubernetes fleet) and
ArgoCD (the GitOps engine that actually performs the deploys). It
holds long-lived credentials — Git PATs, ArgoCD account tokens, AWS
IAM-role assumptions, kubeconfigs for managed clusters — and uses
them to make changes that ripple across the fleet. The v2.0.0
production-launch epic spent six sub-epics (V2-1 through V2-6)
hardening that posture: structured logging with sensitive-field
redaction (V2-2), Prometheus telemetry with multi-burn-rate alerts
(V2-3), 35 operator runbooks closing every P0 / P1 failure-mode gap
(V2-4), a clean-cut removal of v1-era compat shims (V2-5), CNCF
governance docs + SECURITY.md (V2-6.1), the API stability contract
(V2-6.3), and DCO commit attribution (V2-6.4). This threat model
**credits and cites** that V2 foundation rather than re-deriving it.
Approximately 95% of the mitigations referenced below are V2-shipped
artifacts; the remaining 5% are pre-V2 (V123 catalog signing,
V125-1-8 cluster reconciler, baseline RBAC tiers).

The maintainer's central security claim for v2.0.0 is **defense in
depth at every long-lived credential surface**, not zero residual
risk. Specifically:

- Every credential-shaped value passes through the V2-2.4
  `RedactHandler` slog wrapper before serialization to any sink, with
  a punch list (V2-2.3 `logging-audit-punchlist.md`) tracking the
  remaining call-site cleanup so the wrapper does not silently rot
  into the sole defense.
- Every cluster Secret Sharko writes carries the
  `app.kubernetes.io/managed-by: sharko` ownership label (V125-1-8),
  and every Secret-delete path checks the label before acting.
  Cross-tool drift (Sharko deleting a Secret another tool owns) is a
  compile-time / contract-level impossibility on the canonical path.
- Every catalog entry that flows into the marketplace round-trips
  through cosign-keyless verification with an anchored trust regex
  on the GitHub Actions `workflow_run` SAN encoding (V123-2). The
  verifier consumes the modern Sigstore Bundle format and the TUF
  cache lives on a writable path under Sharko's read-only-rootfs
  pod.
- Every mutating endpoint enforces RBAC at the handler boundary
  (Admin / Operator / Viewer tiers per `internal/authz/`) and emits
  an audit event with a stable action code so security review can
  grep one token across the audit log.
- The Sharko pod runs non-root, with read-only root filesystem, no
  privilege escalation, and all capabilities dropped — compliant
  with the Kubernetes Restricted Pod Security Standard.

The residual risks the maintainer is carrying are documented in
[§11](#11-identified-gaps-residual-risk-for-v3) and tracked in the
public [roadmap](../site/community/roadmap.md). The largest are:
fine-grained per-resource RBAC (today's tiers are coarse), SSO/OIDC
(today's auth is local users + GitHub OAuth), and external vault for
Sharko's own service-identity tokens (today's storage is an
AES-256-GCM-encrypted K8s Secret).

## 2. Scope and non-scope

### In scope

- The Sharko HTTP API and its handlers (`internal/api/`).
- The Sharko orchestrator and reconcilers (`internal/orchestrator/`,
  `internal/clusterreconciler/`, `internal/prtracker/`,
  `internal/secrets/`).
- The Sharko cluster-secret writer (`internal/argosecrets/`) and the
  ownership-label gate.
- The catalog signing pipeline (`internal/catalog/`,
  `internal/catalog/signing/`, `internal/catalog/sources/`).
- The cluster-credential providers (`internal/providers/`: AWS SM,
  K8s Secrets, ArgoCD provider; Azure / GCP are stubs).
- The auth surface (`internal/auth/`, `internal/authz/`): local
  users, API keys, GitHub OAuth, session cookies.
- The audit log surface (`internal/audit/`) including the
  `/api/v1/audit/stream` SSE endpoint.
- The Helm chart (`charts/sharko/`) including default Pod Security
  Context, RBAC, and `PrometheusRule` template.
- The container image build pipeline (GitHub Actions release
  workflow) and the cosign signatures on the image + chart.
- The web UI (`ui/`) — bundled and served from the Sharko binary;
  cookie + CSRF model + smart-values AI pipeline.
- The CLI (`cmd/sharko/` and the `sharko_` API-key flow).

### Out of scope (and where to look instead)

- **ArgoCD itself.** Sharko depends on ArgoCD's correctness for the
  actual cluster apply; ArgoCD's threat model is upstream and is
  documented in the [ArgoCD project security
  policy](https://github.com/argoproj/argo-cd/blob/master/SECURITY.md).
  This document covers Sharko's *interaction* with ArgoCD.
- **Downstream addon contents.** Sharko deploys third-party Helm
  charts (cert-manager, external-secrets, metrics-server, etc.);
  the contents of those charts are out of scope. Sharko's
  *integrity guarantees* about delivering those charts are in scope
  via [§9](#9-cncf--slsa-supply-chain-analysis).
- **Cloud-provider infrastructure.** AWS IAM, AWS Secrets Manager,
  EKS control plane, GitHub itself — Sharko's clients of those
  services are in scope; the services themselves are not.
- **Operator's source-of-truth Git repository.** Sharko writes PRs
  to it; the operator owns its security posture. Sharko's *commit
  attribution* and *PR-merge convergence* are in scope.
- **Penetration testing.** This document does not record exploit
  attempts. It is a structured listing of threats and mitigations
  intended to inform defense, not enable attack. The companion
  [review-prep bundle](.bmad/output/reviews/v2-security-review-prep.md)
  is the artifact that scopes a 3rd-party pentest engagement.
- **Compliance audits (SOC2, ISO 27001, FedRAMP).** Sharko is
  pre-CNCF-sandbox and not currently compliant with any formal
  audit regime. This document is the maintainer's structured
  self-assessment; a compliance auditor will want different
  evidence.

### Vocabulary

- **Operator** (capitalised when referring to the human role): the
  platform engineer or SRE deploying Sharko and running it
  day-to-day. Not to be confused with the *Kubernetes operator
  pattern*, which is on the v3+ roadmap.
- **Adopter**: an organisation running Sharko in production.
  Pre-v2.0.0 there are zero adopters by maintainer intent.
- **Attacker**: a malicious actor — could be external (unauthenticated
  internet caller), insider (an operator with credentials whose intent
  has turned), or supply-chain (the upstream of a dependency Sharko
  pulls). Each STRIDE cell names the actor profile explicitly.

## 3. Asset inventory

The seven assets below are the load-bearing security-relevant pieces
of state in a running Sharko deployment. Loss of confidentiality,
integrity, or availability on any one of them is a security
incident. The STRIDE analysis in [§5](#5-stride-per-primary-trust-boundary)
maps each threat to the asset(s) it affects.

| # | Asset | Where it lives | Sensitivity | Confidentiality / Integrity / Availability concern |
|---|-------|----------------|-------------|----------------------------------------------------|
| A1 | **User cluster credentials** — kubeconfigs, bearer tokens, IAM-role assumption results, AWS-SM secret values | Provider-side (AWS SM / K8s Secrets / future Vault). Sharko fetches at use time; never persists. ArgoCD cluster Secret persists the final form. | Highest — these credentials authorise writes to every managed cluster. | C: leak → fleet compromise. I: tamper → wrong workload deployed. A: provider down → reconciler stalls. |
| A2 | **Sharko's own service-identity tokens** — Git PAT, ArgoCD account token | `sharko-connections` Secret in Sharko's namespace, AES-256-GCM encrypted with `SHARKO_ENCRYPTION_KEY`. | High — these are Sharko's identity to every upstream. | C: leak → attacker impersonates Sharko. I: tamper → Sharko commits/auths fail. A: rotation tooling absent today (v3+ gap). |
| A3 | **Audit log integrity** | In-memory ring buffer + stdout slog → cluster log pipeline (Loki / CloudWatch / etc.). Optional ConfigMap rotation per `audit-log.md`. | High — audit log is the forensic primary. | C: usually low (audit records action codes, not credentials). I: critical (tampered audit = no forensic record). A: ring-buffer exhaustion is bounded; downstream pipeline is operator-managed. |
| A4 | **Catalog signatures** | Sigstore Bundle alongside each catalog entry; chain of trust ends at Fulcio public-good CA, verified through TUF cache. | High — signatures are how operators know a catalog entry came from a trusted source. | C: signatures are public. I: tamper → operator installs a malicious chart. A: TUF cache freshness window. |
| A5 | **Sharko process integrity** — container image, Helm chart, runtime config | OCI registry (`ghcr.io/moranweissman/sharko`). Helm chart at `oci://...`. Runtime config from Helm values + `SHARKO_*` env vars. | High — compromised image = compromised everything downstream. | C: image bytes are public. I: tamper → fleet-wide compromise. A: registry uptime is platform-managed. |
| A6 | **Web UI session integrity** — session cookies, CSRF tokens, API keys | Cookie store on operator's browser; bcrypt'd hash in `sharko-users` ConfigMap; API-key bcrypt hash in K8s Secret. | High — session cookie = operator's identity in-flight. | C: cookie leak → impersonation. I: CSRF without token → operator's authority used by attacker. A: 24h cookie lifetime caps impact window. |
| A7 | **Operator's source-of-truth Git repo** — `managed-clusters.yaml`, `addon-catalog.yaml`, per-cluster Helm values | Operator's GitHub / GitLab / Bitbucket repo. Sharko writes via PR, never directly. | High — this YAML drives every reconciler decision. | C: leak depends on repo visibility. I: tamper → reconciler converges to wrong state. A: Git provider down → write side stalls (reads still work from in-cluster ConfigMap snapshots). |

The asset inventory is intentionally short. Sharko does not store
addon secret *values* persistently (push-based reconciler); it does
not store cluster credentials persistently (connect-operate-disconnect);
it does not store user-uploaded artifacts. The persistent state
footprint is narrow by design — this is the foundation of the
"no AVP, no Redis bridge" constraint
([`.claude/team/security-auditor.md` §13](.claude/team/security-auditor.md)).

## 4. Trust boundaries

Sharko crosses eight trust boundaries in normal operation. Six are
**primary** — they carry the bulk of the threat surface and get full
6-cell STRIDE coverage in [§5](#5-stride-per-primary-trust-boundary).
Two are **ancillary** — lower-risk or operator-managed surfaces that
get lighter coverage in [§6](#6-ancillary-boundaries-lighter-coverage).

```
                       ┌──────────────────────────────┐
                       │   Operator's browser         │
                       └──────────────┬───────────────┘
                                      │ B1 (HTTPS + session cookies + CSRF)
                                      ▼
   ┌──────────────────────────────────────────────────────────────┐
   │  Sharko pod (non-root, read-only rootfs, caps dropped)       │
   │                                                              │
   │   ┌───────────────┐  ┌──────────────┐  ┌─────────────────┐   │
   │   │   HTTP API    │  │ Orchestrator │  │   Reconcilers   │   │
   │   │  internal/api │  │  internal/   │  │ clusterreconcil │   │
   │   └───────┬───────┘  │ orchestrator │  │ prtracker       │   │
   │           │          └──────┬───────┘  │ secrets         │   │
   │           ▼                 ▼          └──────┬──────────┘   │
   │   ┌───────────────┐  ┌──────────────┐         │              │
   │   │   Catalog     │  │  Providers   │         │              │
   │   │ internal/     │  │  internal/   │         │              │
   │   │ catalog       │  │  providers   │         │              │
   │   └──────┬────────┘  └──────┬───────┘         │              │
   └──────────┼──────────────────┼─────────────────┼──────────────┘
              │                  │                 │
        B6   ▼              B5  ▼            B2,B3,B4 ▼
   ┌────────────────┐  ┌──────────────┐  ┌──────────────────────┐
   │ Catalog source │  │  Secrets     │  │  ArgoCD              │
   │ (HTTPS + cosign│  │  provider    │  │  Git provider        │
   │  + TUF cache)  │  │ (AWS SM /    │  │  Kube-apiserver per  │
   └────────────────┘  │  K8s Secrets)│  │  managed cluster     │
                       └──────────────┘  └──────────────────────┘

         ancillary:
            B7: Sharko ↔ container registry (image pull / cosign verify on Sharko's own image)
            B8: in-cluster network (NetworkPolicy / mesh — operator-managed)
```

### Primary boundaries (full STRIDE)

| # | Boundary | Direction | Authentication | Encryption in transit |
|---|----------|-----------|----------------|-----------------------|
| B1 | Operator browser ↔ Sharko UI | Inbound HTTPS | Session cookie (24h, HttpOnly, SameSite=Lax) OR API key (`sharko_` prefix, bcrypt-hashed at rest) OR GitHub OAuth | HTTPS (operator-terminated; HSTS enforced) |
| B2 | Sharko ↔ ArgoCD | Outbound: ArgoCD account token. Inbound from ArgoCD: none (Sharko owns the writes). | ArgoCD account token in `Authorization: Bearer ...` header. In-cluster: Kubernetes API to ArgoCD-namespace Secrets (no separate auth). | HTTPS to ArgoCD API; in-cluster via apiserver. |
| B3 | Sharko ↔ kube-apiserver (Sharko's own + managed) | For Sharko's own apiserver: Pod ServiceAccount → narrow ClusterRole. For managed clusters: kubeconfig fetched from provider, used once, discarded. | ServiceAccount token; for managed clusters, the kubeconfig embeds bearer or exec-plugin auth. | HTTPS (apiserver-managed). |
| B4 | Sharko ↔ Git provider | Outbound (PR creation, merge, file fetch): GitHub PAT or GitHub App token. Inbound (webhook): HMAC-SHA256 `X-Hub-Signature-256`. | HTTPS. PAT-at-rest AES-256-GCM. |
| B5 | Sharko ↔ secrets provider (AWS SM / K8s Secrets / Vault) | AWS: IRSA / IAM role assumption. K8s: ServiceAccount RBAC. Vault: future. | HTTPS to AWS endpoints; in-cluster apiserver for K8s Secrets. |
| B6 | Sharko ↔ catalog source | Outbound: HTTPS GET. No request authentication (catalog sources are public-read). Trust derives from cosign signature, not transport. | HTTPS; TLS validated via system trust store + optional corporate-CA mount per `corporate-mitm-tls.md`. |

### Ancillary boundaries (lighter coverage)

| # | Boundary | Why ancillary |
|---|----------|---------------|
| B7 | Sharko ↔ container registry | One-time at pod start (image pull). Verified by cosign per `supply-chain.md`. Trust pre-loaded into the image-pull config. |
| B8 | In-cluster network (Sharko pod ↔ everything else inside the cluster) | Operator-managed via NetworkPolicy / service mesh. Sharko ships defaults in `security.md`; the actual posture is the operator's choice. |

---

## 5. STRIDE per primary trust boundary

Each cell follows the same shape: **Threat** (vector + likely actor +
plausible scenario, in defense-informing language without exploit
recipes), **Existing mitigation** (cite the V2-shipped artifact),
**Residual risk** (what's left), **GAP** if any (cross-link to the
roadmap).

The vocabulary of STRIDE is the Microsoft canonical six:
**S**poofing, **T**ampering, **R**epudiation, **I**nformation
disclosure, **D**enial of service, **E**levation of privilege. The
CNCF Security TAG variant adds "lateral movement" as a sub-class of
EoP; this document treats it as part of EoP rather than a seventh
category.

### 5.1 B1: Operator browser ↔ Sharko UI

This is the highest-frequency boundary by traffic count and the only
one where the actor on the other side is an unauthenticated entity
by default. Every interactive operator action — log in, register a
cluster, enable an addon, edit values, rotate credentials —
traverses this boundary. The threats below assume the attacker has
network access to the Sharko ingress and may be on the same network
segment as a legitimate operator.

#### B1-S: Spoofing — fraudulent login or session takeover

**Threat.** An attacker who reaches the `/api/v1/auth/login`
endpoint attempts to authenticate as a legitimate operator. The
actor is most likely external (internet-exposed Sharko ingress) but
could be insider (a Viewer-tier operator attempting Admin
escalation). The plausible scenarios are: credential stuffing from
a breached-password list, brute-force password guessing,
session-cookie theft from a compromised operator laptop, or
GitHub-OAuth-callback hijack via a misconfigured redirect URI.

**Existing mitigation.** Passwords stored as bcrypt hashes
(`golang.org/x/crypto/bcrypt`, cost 10) in the `sharko-users`
ConfigMap — plaintext never on disk or in logs. Login endpoint
rate-limited (10 attempts/IP/minute via `loginRateLimiter`). Session
cookies are 32-byte `crypto/rand` tokens, HttpOnly, SameSite=Lax,
24h lifetime, cleaned up by an hourly goroutine. API keys use the
same bcrypt regime with a `sharko_` prefix for in-log
identification. The V125-1-7 token-leak class — where a hash
collision let a fake API token authenticate — is closed; the
canonical reference is
[`auth-bypass.md`](../site/operator/auth-bypass.md). GitHub OAuth
callback URI is operator-configured and matches the deployed
hostname.

**Residual risk.** A compromised operator laptop with a live
session cookie is impersonatable for up to 24 hours. There is no
device-binding on the cookie; rotating the cookie does not invalidate
prior copies. The maintainer accepts this because the alternative
(short-lived cookie + refresh-token flow) is materially more complex
and the v2.0.0 deployment model assumes operator-controlled laptops.

**GAP.** SSO / OIDC with group-based RBAC is on the v3+ roadmap
([`roadmap.md` § Medium-term](../site/community/roadmap.md#medium-term-themes-v3x)).
For v2.0.0, local users + API keys + optional GitHub OAuth is the
shipped surface.

#### B1-T: Tampering — XSS, CSRF, request body injection

**Threat.** An attacker who can place arbitrary HTML or JavaScript
in a response (stored XSS) or trick an operator into executing a
forged request (CSRF) escalates from "unauthenticated" to "speaking
with the operator's authority." Actor profile is most often
external; insider variants exist when a Viewer-tier operator can
write content that Admin-tier operators view. Plausible scenarios
include unescaped values rendered into addon descriptions, JSON
responses that get evaluated as HTML, or POST endpoints that accept
JSON without verifying the SameSite or Origin context.

**Existing mitigation.** Content-Security-Policy header restricts
script sources to `self` (see
[`security.md` § Security Headers](../site/operator/security.md#security-headers)).
`X-Content-Type-Options: nosniff` blocks MIME confusion attacks.
`X-Frame-Options: DENY` blocks UI redress. JSON responses always
carry `Content-Type: application/json` so the browser never reflows
them as HTML. CSRF tokens are issued per session; mutating endpoints
verify the token in the `X-CSRF-Token` header. SameSite=Lax on the
session cookie blocks cross-site form submissions. Request bodies
are capped at 1MB by the `maxBodySize` middleware. Cluster names are
regex-validated; addon fields are non-empty-checked at handler
entry; CLI URL params are `url.PathEscape`-d.

**Residual risk.** The UI is bundled into the Sharko binary and
served from `internal/api/ui.go`; any XSS in third-party React
dependencies that escapes the React escaping discipline is a
residual risk. The maintainer relies on the React framework's
default JSX escaping for the bulk of the defense and audits the
small number of dangerouslySetInnerHTML sites manually.

**GAP.** No periodic automated XSS scanner runs against the bundled
UI (e.g. Trivy doesn't cover frontend dependency CVEs deeply).
Tracked as a near-term post-launch item — the V2-6.5 review-prep
bundle calls this out as a focus area for a 3rd-party reviewer.

#### B1-R: Repudiation — operator denies a destructive action

**Threat.** An operator (or an attacker who has obtained operator
credentials) performs a destructive action — cluster deregister,
addon remove, secret rotate — and later denies having done so. The
actor profile is *necessarily* insider for repudiation to be a
relevant frame, because external attackers do not need to repudiate.
Plausible scenarios: an operator removes a cluster from the fleet
to mask their own configuration mistake, or an attacker who exfilled
an API key wants to cover their tracks.

**Existing mitigation.** Every mutating endpoint emits an audit-log
entry through the V2-2 audit-log discipline
([`logging.md`](../site/developer-guide/logging.md)). The entry
records: actor identity (resolved username or API-key name),
request_id (correlated end-to-end across middleware → service →
orchestrator → reconciler), HTTP method + path, target resource
(cluster name / addon name), result (success / failure), audit
action code (stable token for grep). The audit log streams in real
time at `GET /api/v1/audit/stream` (SSE) and persists to the audit
ConfigMap with the V2-shipped retention model
([`audit-log.md`](../site/operator/audit-log.md)). The tiered Git
attribution model (Tier 1 service / Tier 2 per-user PAT) records
the `attribution_mode` on every commit-creating action so
post-incident review can distinguish operator-authored from
service-authored commits.

**Residual risk.** The audit log is operator-readable by default
(any Viewer-tier user can see all entries). A privileged insider
can correlate audit entries to user identity, but operator-side
log integrity (downstream Loki / S3 / CloudWatch) is the operator's
responsibility. Sharko cannot prevent tampering with the audit
log *outside* Sharko's process boundary.

**GAP.** No cryptographic integrity (hash chain, signed audit
entries, tamper-evident log) on the audit stream. Tracked for
v3+ — the realistic mitigation for a Sharko deployment today is to
ship audit logs to an immutable downstream store (S3 with
Object Lock, write-only CloudWatch destination, etc.).

#### B1-I: Information disclosure — session cookie, API key, credential exposure

**Threat.** A credential-shaped value reaches the operator's
browser or the network when it should not. Actor profile: external
(network-level interception, shoulder-surfing on a public network)
or supply-chain (a malicious browser extension reads cookies). The
plausible scenarios: a cluster credential leaks in a `GET /clusters`
response, an API-key plaintext is returned after creation in a
listing endpoint, an error message echoes a kubeconfig blob back to
the operator.

**Existing mitigation.** `GET /api/v1/clusters/{name}` returns
metadata + provider type + region but never the credential itself.
`GET /api/v1/config` returns connection type and region only — no
tokens. The `handleTestProvider` endpoint returns a cluster *count*,
not credentials. API-key creation returns the plaintext ONCE
(`sharko_<32-hex>`) and the listing endpoint returns hash metadata
only — there is no API key recovery path. Remote-cluster kubeconfigs
fetched from providers are used in memory and discarded; they never
flow into an API response and never persist beyond the operation.
Addon-secret *definitions* (key → provider_path mappings) are
metadata, not secret values. The V2-2.4 `RedactHandler` slog
wrapper catches credential-shaped values that bypass the structured
discipline — `password`, `token`, `kubeconfig`, JWT-shaped, base64
blobs >100 chars — before they reach any sink
([`logging.md` § RedactHandler](../site/developer-guide/logging.md#redacthandler)).
HTTPS is mandatory at the ingress; HSTS is set with a 1-year
max-age.

**Residual risk.** The bootstrap-admin-password path at
`internal/auth/store.go:634` still emits the password as a slog
attribute that the RedactHandler collapses to `[REDACTED]`. The
wrapper saves the day, but the call site is wrong — see the
[`logging-audit-punchlist.md`](../site/developer-guide/logging-audit-punchlist.md)
headline finding. The credential is also written to a
`sharko-initial-admin-secret` K8s Secret, so the retrieval path
isn't broken, but the log line is now misleading. This is a
**partial mitigation**: defense-in-depth is in place, primary call
site needs follow-up.

**GAP.** Periodic credential-shape scanning on rendered API
responses is not automated; a regression that adds a kubeconfig
field to a public API response would not be caught by CI today.
Tracked as a V2.x follow-up. Also: in-memory credential blobs in
the Go heap are recoverable by anyone with a memory dump
(`kubectl debug` + process attach); the Restricted Pod Security
Standard reduces this risk surface but does not eliminate it. No
mitigation planned — accepted as the K8s baseline.

#### B1-D: Denial of service — resource exhaustion via API

**Threat.** An attacker (or a buggy integration) issues a request
pattern that exhausts Sharko's resources and starves legitimate
operators. Actor profile: external (open ingress) or insider
(a CI/CD pipeline with an API key in a tight retry loop). Plausible
scenarios: a flood of login attempts saturates the bcrypt cost,
a request body larger than 1MB triggers expensive parsing, a batch
cluster register with thousands of entries holds the orchestrator
mutex.

**Existing mitigation.** `/auth/login` is rate-limited (10
attempts/IP/min). Admin write endpoints rate-limited at 30
requests/min/IP (see
[`security.md` § Rate Limiting](../site/operator/security.md#rate-limiting)).
`maxBodySize` middleware rejects bodies > 1MB. Batch endpoints
cap input at 10 items (`/clusters/batch`,
`/addons/upgrade-batch`); larger requests return 400. Provider
calls have 15-second HTTP timeouts. The V2-3 Prometheus alerts
(`SharkoClusterRegistrationFastBurn`,
`SharkoAddonCycleFastBurn`, etc.) page on-call when latency or
error rates breach SLO budgets — the
[`budget-burn-runbook.md`](../site/operator/budget-burn-runbook.md)
points to the same runbook from every burn variant.

**Residual risk.** Rate limiting depends on the
`SHARKO_TRUSTED_PROXIES` configuration to see the real client IP.
A misconfigured ingress could collapse all clients to the
proxy's single IP, and a single attacker could exhaust the
rate-limit budget for every operator. The `security.md` page
warns about this; the runbook is part of the V2-4 set.

**GAP.** No global per-tenant quota beyond rate-limit (no
"this API key has spent X of its Y monthly budget" tracking).
v2.0.0 ships a single-tenant model; multi-tenant quota is a
v3+ concept aligned with the fine-grained RBAC theme.

#### B1-E: Elevation of privilege — Viewer → Operator → Admin

**Threat.** A user authenticated at one RBAC tier accesses a
higher-tier endpoint. Actor profile: insider — by definition of EoP,
the actor already has *some* authority and wants more. Plausible
scenarios: a Viewer-tier API key is honored by a write endpoint that
forgot the RBAC check, an Operator-tier user reaches the
`/api/v1/users` Admin endpoint, an unauthenticated request slips
past the auth middleware on a write path.

**Existing mitigation.** RBAC is enforced at the handler boundary,
not at the framework — every write endpoint calls
`s.requireAdmin(w, r)` as the first action (the canonical list is in
[`.claude/team/security-auditor.md` § 2](.claude/team/security-auditor.md#2-auth-on-write-endpoints)).
The auth middleware resolves a token (cookie → session token → API
key) once per request and attaches the resolved subject to the
context; downstream code reads from context, never re-resolves. The
auth-bypass failure mode is a P0 with a dedicated runbook
([`auth-bypass.md`](../site/operator/auth-bypass.md)). The
`audit_coverage_test.go` regression suite verifies that every
mutating handler emits an audit entry — a side effect is that
forgetting RBAC also forgets the audit, which is louder.

**Residual risk.** The RBAC model is *coarse*: Admin / Operator /
Viewer. There is no per-cluster, per-environment, or per-addon
scoping. An Operator-tier user can write to every cluster in the
fleet; a CI/CD API key for "dev cluster maintenance" can also
modify production. This is a deliberate v2.0.0 simplification
trading granularity for shipping.

**GAP.** Fine-grained RBAC is on the v3+ roadmap. The maintainer
will not add it as a side-effect of any v2.x patch — this is a
clean v3 surface.

### 5.2 B2: Sharko ↔ ArgoCD

ArgoCD is where the actual cluster apply happens. Sharko writes
into ArgoCD's Secret-store namespace (creating the cluster
Secrets that ArgoCD then uses to authenticate to managed clusters)
and reads ArgoCD's CRDs (Applications, AppProjects, ApplicationSets)
to surface fleet state. Compromise of this boundary turns Sharko
from "addon manager" into "fleet attacker".

#### B2-S: Spoofing — fraudulent ArgoCD token

**Threat.** An attacker presents a fraudulent ArgoCD account token
to spoof Sharko's identity when talking to ArgoCD, or alternatively
spoofs ArgoCD itself to receive Sharko's writes. Actor profile:
insider (someone with K8s namespace access to read Sharko's
encrypted token and decrypt it) or supply-chain (an attacker who
compromises an intermediate proxy in front of ArgoCD).

**Existing mitigation.** Sharko's ArgoCD account token is stored in
the `sharko-connections` K8s Secret, AES-256-GCM-encrypted with
`SHARKO_ENCRYPTION_KEY`. The encryption key is sourced from the env
var (Helm-supplied or operator-managed). The ArgoCD URL is
operator-configured; HTTPS is mandatory. The V125-1-10 surface
narrowed Sharko's ArgoCD interaction to ArgoCD-Secret-only reads
(no broader resource fetches that could be spoofed to mislead
Sharko). The `connections-discover-argocd` flow validates the
server's TLS chain via the system trust store.

**Residual risk.** The encryption key lives next to the encrypted
data in the same K8s namespace by default; a Sharko-namespace
compromise yields both. The operator-managed alternative
(`SHARKO_ENCRYPTION_KEY` from an external KMS) is supported but
requires operator wiring.

**GAP.** External KMS / Vault for Sharko's *own* tokens is a v3+
roadmap item ([`roadmap.md` § Cloud-provider full support](../site/community/roadmap.md#medium-term-themes-v3x)).
For v2.0.0, the K8s-native AES-256-GCM model is the shipped
surface.

#### B2-T: Tampering — ArgoCD cluster Secret modified out-of-band

**Threat.** Someone (insider or supply-chain) modifies an ArgoCD
cluster Secret that Sharko owns, causing ArgoCD to authenticate to a
different cluster than Sharko intends. Actor profile: insider with
K8s `secrets` write in the ArgoCD namespace, or a compromised
controller running in the same cluster.

**Existing mitigation.** V125-1-8 introduced the cluster reconciler
(`internal/clusterreconciler/`) which runs on a 30s safety-net
cadence plus an immediate post-merge trigger from
`prTracker.SetOnMergeFn → recon.Trigger()`. Any drift from
the source-of-truth `managed-clusters.yaml` is reconciled
within 5 seconds. The reconciler is the canonical writer of
ArgoCD cluster Secrets and emits identical Secret payloads via the
shared `argosecrets.BuildSecretConfigJSON` +
`argosecrets.BuildClusterSecretLabels` wrappers — hand-rolled
Secret payloads in either the reconciler or the orchestrator are
a critical-finding-class violation
([`.claude/team/security-auditor.md` § 11](.claude/team/security-auditor.md#11-ownership-label-gate-v125-1-8)).
Every Sharko-written Secret carries the
`app.kubernetes.io/managed-by: sharko` ownership label.

**Residual risk.** The reconciler keys off the source-of-truth YAML
in the operator's Git repo. If the operator's Git repo is tampered
with, the reconciler will faithfully converge to the wrong state.
B4 (Git provider) covers this — the trust chain extends through
Git, not just through ArgoCD.

**GAP.** Cryptographic verification of the source-of-truth YAML
(signed commits, signed `managed-clusters.yaml`) is not
implemented. The DCO `Signed-off-by` per V2-6.4 is identity
attestation, not integrity attestation. Tracked as a v3+ item.

#### B2-R: Repudiation — ArgoCD-side action without Sharko audit

**Threat.** An action happens on the ArgoCD side that Sharko's
audit log does not record, breaking the operator's ability to
reconstruct what happened. Actor profile: an insider with direct
ArgoCD-side credentials bypassing Sharko, OR Sharko itself if a
code path emits to ArgoCD without an audit code.

**Existing mitigation.** Every ArgoCD-write code path in
`internal/orchestrator/` and `internal/clusterreconciler/` emits an
audit-log entry with a stable action code
([`logging.md` § Audit codes](../site/developer-guide/logging.md)).
The reconciler uses `recon-<unix_ts>` as its request_id so audit
entries from the background reconciler are easily distinguished
from operator-driven actions (`req-<hex>`). ArgoCD has its own
audit trail (the `argocd-audit` controller); cross-correlation is
operator-side via shared timestamps.

**Residual risk.** If an operator authenticates *directly* to
ArgoCD (bypassing Sharko), Sharko's audit log will not see that
action. ArgoCD's own audit will. The combined picture requires
joining two streams.

**GAP.** Sharko does not (and will not for v2.0.0) require ArgoCD
to be exclusively driven through Sharko — that would be a v3+
"operator mode" enforcement model. For v2.0.0, the documented
expectation is "Sharko is the primary write path; direct ArgoCD
writes are an operator's deliberate fallback."

#### B2-I: Information disclosure — cluster credentials in the ArgoCD Secret

**Threat.** Cluster credentials stored in ArgoCD cluster Secrets
leak via ArgoCD's API, ArgoCD's UI, or via K8s-level Secret reads in
the ArgoCD namespace. Actor profile: insider with ArgoCD-namespace
read or with ArgoCD-UI Admin role.

**Existing mitigation.** This is fundamentally ArgoCD's threat
surface, not Sharko's — once Sharko writes the Secret, ArgoCD owns
the read-side semantics. Sharko's contribution is to write the
Secret with the documented ArgoCD shape (no extra fields that could
leak via debug endpoints) and to keep the ArgoCD namespace's RBAC
narrow. Sharko's Helm chart documents the ArgoCD-side RBAC
configuration expected. The audit log records every cluster Secret
*write* so operators can correlate "this Secret has my credential"
with "Sharko wrote it at time T".

**Residual risk.** ArgoCD's own threat model applies. Sharko's
guarantee ends at the K8s Secret write.

**GAP.** No additional mitigation planned at Sharko's surface —
this is upstream's domain.

#### B2-D: Denial of service — ArgoCD unreachable cascades into Sharko

**Threat.** ArgoCD goes offline (network partition, ArgoCD pod
crash loop, ArgoCD token revoked). Sharko's write side stops
because cluster Secrets can't be applied and ApplicationSets can't
be queried. The actor profile is most often *no actor* — this is
typically infrastructure failure rather than attack — but a DoS
attacker targeting ArgoCD would cascade into Sharko availability.

**Existing mitigation.** The
[`argocd-upstream-unreachable.md`](../site/operator/argocd-upstream-unreachable.md)
P0 runbook covers diagnosis + mitigation. The cluster reconciler
runs on a safety-net 30s cadence and self-heals once ArgoCD comes
back. The V2-3 burn-rate alerts (`SharkoArgoCDClientErrorBurn`)
page on-call when ArgoCD errors persist. The
[`argocd-pr-merge-no-converge.md`](../site/operator/argocd-pr-merge-no-converge.md)
runbook covers the partial-failure case (PR merged but ArgoCD
never converges).

**Residual risk.** Sharko's write path *does* stall during ArgoCD
unavailability. Reads continue (in-cluster state snapshots) but
mutations queue. The operator-visible signal is `502 Bad Gateway`
on write endpoints with `error_code: "argocd_unreachable"`.

**GAP.** No multi-ArgoCD failover. v2.0.0 assumes a single ArgoCD
per Sharko install; multi-ArgoCD is a v3+ theme
([`roadmap.md` § Multi-ArgoCD](../site/community/roadmap.md#medium-term-themes-v3x)).

#### B2-E: Elevation of privilege — Sharko's K8s ServiceAccount overreach

**Threat.** Sharko's K8s ServiceAccount is granted broader
permissions than its actual code requires, so a code-level
compromise of Sharko expands into a cluster-level compromise. Actor
profile: supply-chain (compromised dependency) or insider (someone
who modifies the Helm chart to broaden RBAC during install).

**Existing mitigation.** Sharko's Helm chart ships with a narrow
ClusterRole (`charts/sharko/templates/rbac.yaml`) granting `get`,
`list`, `watch` on ArgoCD CRDs (Applications, AppProjects,
ApplicationSets) and the ArgoCD-namespace's Secrets. Node read
access (`get`, `list` on `v1/nodes`) is granted by default for the
Dashboard widget but can be disabled with `config.nodeAccess:
false`. The
[`security.md` § RBAC](../site/operator/security.md#rbac) section
documents the surface. V125-1-10 narrowed the ArgoCD interaction
to Secret-only reads. The pod runs as non-root (UID 1001), with
read-only root filesystem, no privilege escalation, and all
capabilities dropped (Restricted Pod Security Standard
compliant).

**Residual risk.** Sharko's ClusterRole is *cluster-scoped*
(Sharko is a fleet-level tool) so the blast radius of a Sharko
compromise spans the entire ArgoCD namespace. The trade-off is
documented and the operator can run Sharko in a single-tenant
mode where only one ArgoCD instance lives in the cluster.

**GAP.** Per-namespace RBAC (where Sharko's permissions are
restricted to a specific subset of ArgoCD AppProjects) is part of
the multi-ArgoCD v3+ theme.

### 5.3 B3: Sharko ↔ kube-apiserver

This boundary covers Sharko's interaction with *two* kube-apiservers:
(a) Sharko's own cluster (where Sharko's pod and the ArgoCD pod
both run), and (b) each managed cluster (where workloads actually
land). The two have different threat profiles because the in-cluster
case uses a ServiceAccount and the managed-cluster case uses a
fetched-on-demand kubeconfig.

#### B3-S: Spoofing — forged kubeconfig or fake apiserver

**Threat.** Sharko presents a kubeconfig that authenticates to a
different cluster than it intends (or vice versa: an attacker
forges an apiserver response to Sharko). Actor profile: insider
with provider-side write (someone who can modify the AWS-SM secret
or K8s Secret that holds the kubeconfig) or supply-chain (a
compromised certificate authority in the kubeconfig).

**Existing mitigation.** Kubeconfigs are fetched from the
provider at use time, never persisted by Sharko. The
provider-fetched kubeconfig is parsed by
`internal/providers/kubeconfig_parser.go` which validates the
shape before use. For EKS, STS tokens are minted fresh per use
(`internal/providers/aws_auth.go`) — there is no long-lived stored
token to spoof. The kubeconfig's CA certificate is part of the
kubeconfig blob; if a provider stores a corrupt CA, every
connection to that cluster fails the TLS handshake at the
apiserver. The `internal/remoteclient/` connect-operate-disconnect
pattern shortens the window during which a spoofed apiserver
response could be honored.

**Residual risk.** A compromised provider (AWS SM secret modified)
could redirect Sharko to a malicious apiserver. The provider's
authn (IRSA / IAM) is the primary defense. Sharko also depends
on the apiserver's TLS certificate matching the kubeconfig's CA;
a man-in-the-middle that re-signs with a different CA would fail
the handshake.

**GAP.** Per-managed-cluster apiserver pinning (e.g. SPKI pin in
addition to CA trust) is not implemented. Defense at the network
layer (private VPC endpoints, AWS PrivateLink) is operator-managed
and documented in [`security.md`](../site/operator/security.md).

#### B3-T: Tampering — apply payload modified in-flight

**Threat.** A Sharko-originated K8s apply (a cluster Secret write,
an addon-secret push to a managed cluster) is modified in transit.
Actor profile: insider on the in-cluster network or a compromised
intermediate proxy.

**Existing mitigation.** All apiserver traffic is HTTPS with
certificate validation. In-cluster, the apiserver's TLS cert is
trusted via the kubernetes.io CA. For managed clusters, the
kubeconfig's embedded CA validates the apiserver cert. The K8s
apiserver itself enforces strong consistency on writes (linear
operations, ETag/resourceVersion conflict detection), so a partial
or replayed write is detected by the apiserver. Sharko's write
side uses the standard client-go retry-with-backoff library;
silent partial application is not a regular failure mode. The
[`secret-push-silently-failed.md`](../site/operator/secret-push-silently-failed.md)
P0 runbook documents the *result.Failed* accumulator that surfaces
silent push failures.

**Residual risk.** A successful write that was tampered would be
visible on the apiserver side (different content than Sharko
sent). Sharko's audit log records the *intent*; the apiserver's
audit (if enabled by the operator) records the *received content*.
The two together detect tampering.

**GAP.** No end-to-end integrity proof on the apply payload
(e.g. signed-then-applied). Accepting that K8s apiserver TLS +
operator-side apiserver audit is the defense layer.

#### B3-R: Repudiation — apply attributed to wrong actor

**Threat.** A K8s apply happens with the wrong attribution — for
example, an addon enable performed by Operator Alice shows up as
"system:serviceaccount:sharko:sharko" in the apiserver audit log
with no Sharko-side correlation to Alice. Actor profile: insider
denying an action, or auditor unable to reconstruct.

**Existing mitigation.** Sharko's audit log records the operator's
identity for every mutating endpoint (see §5.1 B1-R). The K8s
apiserver-side audit shows the ServiceAccount identity (Sharko's
SA); the *correlation* between Sharko-side audit and apiserver-side
audit is the `request_id` propagated via slog
([`logging.md` § Correlation IDs](../site/developer-guide/logging.md#correlation-ids)).
A `jq 'select(.request_id=="req-...")'` across both log streams
joins them.

**Residual risk.** The cross-stream correlation depends on
operators capturing both streams (Sharko stdout + apiserver
audit). Operators who only ship Sharko logs and not apiserver
audit lose the cross-reference.

**GAP.** No native K8s-event annotation carrying Sharko's
`request_id` and operator identity through to the apiserver-side
audit. v3+ topic.

#### B3-I: Information disclosure — kubeconfig in logs or response

**Threat.** A kubeconfig blob (or its embedded bearer token /
client cert / exec-plugin config) appears in a log line or an API
response. Actor profile: external (log scraper) or insider with
log read.

**Existing mitigation.** The V2-2.4 `RedactHandler` slog wrapper
has dedicated detectors for kubeconfig values: attribute names
matching `kubeconfig`, `bearer`, `token`, etc.; value-shape
detectors for JWT (`eyJ`-prefixed), base64 blobs > 100 chars, and
PEM blocks. Detectors run *before* JSON serialization
([`logging.md` § RedactHandler](../site/developer-guide/logging.md#redacthandler)).
The
[`credential-leak-in-logs.md`](../site/operator/credential-leak-in-logs.md)
P0 runbook documents diagnosis + mitigation. API responses never
include kubeconfigs (verified by the
[`security-auditor.md` § 3](.claude/team/security-auditor.md#3-credential-safety)
checklist).

**Residual risk.** The
[`logging-audit-punchlist.md`](../site/developer-guide/logging-audit-punchlist.md)
documents specific call sites that emit credentials as slog
attributes; the wrapper redacts them, but a regression in the
wrapper would re-expose. This is a **partial mitigation** — the
sink-side defense is in place, the source-side call-site cleanup
is still in progress.

**GAP.** CI gate that fails the build on any new
`slog.X(..., "kubeconfig", ...)` pattern. Tracked as a near-term
post-launch item.

#### B3-D: Denial of service — apiserver rate limit, watch storm

**Threat.** Sharko's pattern of apiserver calls exhausts the
apiserver's rate-limit budget for the Sharko ServiceAccount or
saturates the apiserver with watch reconnects. Actor profile:
typically *no attacker* — this is usually a Sharko code-level
inefficiency — but a malicious insider could trigger a
batch-cluster-register with a thousand entries.

**Existing mitigation.** Batch endpoints cap at 10 items. The
reconciler runs on a fixed 30s cadence with a single goroutine
(no concurrent ticks). The cluster reconciler's `sync.Once`
guard on Start() prevents multiple goroutines spinning up. The
[`reconciler-crash-loop.md`](../site/operator/reconciler-crash-loop.md)
P0 runbook covers the case where the reconciler goroutine dies
and never restarts.

**Residual risk.** Sharko makes no claims about apiserver
rate-limit budgets — the operator is responsible for sizing the
apiserver. The V2-3 metrics include apiserver call counters so
operators can observe the rate.

**GAP.** No automatic backpressure on apiserver calls when the
apiserver returns 429. v3+ refinement.

#### B3-E: Elevation of privilege — managed-cluster RBAC overreach

**Threat.** The kubeconfig Sharko fetches from a provider grants
broader permissions on the managed cluster than necessary, so a
compromise of Sharko expands into the managed cluster's surface.
Actor profile: insider on the provider side (someone who issues an
overly broad kubeconfig).

**Existing mitigation.** Sharko's *use* of the managed-cluster
kubeconfig is narrow — push addon Secrets into specific namespaces,
maybe create ServiceAccounts for addons. The provider is responsible
for issuing a kubeconfig with appropriate RBAC. The
[`security.md` § Pod Security](../site/operator/security.md#pod-security)
section documents the recommended scope. The
[`security-auditor.md` § 7](.claude/team/security-auditor.md#7-remote-cluster-security-v100-phase-3)
documents the connect-operate-disconnect contract.

**Residual risk.** Sharko cannot enforce that the kubeconfig is
narrow — the provider issues it. An over-broad kubeconfig gives
Sharko more power than it needs.

**GAP.** No periodic RBAC-scope assessment on kubeconfigs Sharko
holds. Could be added as a diagnostic ("Sharko received a
cluster-admin kubeconfig — is that intentional?"). v3+ topic.

### 5.4 B4: Sharko ↔ Git provider

The Git provider is the *integrity backbone* of Sharko. The
source-of-truth YAML files live in the operator's Git repo; Sharko
writes through PRs and reads through file fetches. A compromise here
turns Sharko's faithful reconcile into a faithful execution of
attacker intent.

#### B4-S: Spoofing — fake webhook origin, fraudulent PR-merge event

**Threat.** An attacker sends a forged inbound webhook claiming
"PR #N just merged" to trick Sharko into triggering an immediate
cluster-reconciler pass. Actor profile: external — webhook
endpoints are usually internet-facing.

**Existing mitigation.** `POST /api/v1/webhooks/git` requires a
valid `X-Hub-Signature-256` HMAC over the body, computed with
`SHARKO_WEBHOOK_SECRET`. Requests without a valid signature return
`401 Unauthorized`. The verification is per
`internal/api/webhooks.go:54`. The
[`webhook-handler-failures.md`](../site/operator/webhook-handler-failures.md)
P1 runbook covers diagnosis + mitigation. The
[`security.md` § Webhook Security](../site/operator/security.md#webhook-security)
section warns that an empty `SHARKO_WEBHOOK_SECRET` disables
verification.

**Residual risk.** A weak or leaked webhook secret defeats the
verification. Operator-side secret management hygiene is the
defense.

**GAP.** No webhook IP allowlist (e.g. restrict to GitHub's
documented webhook IP ranges). Operator-managed via NetworkPolicy.

#### B4-T: Tampering — commit content modified after Sharko opens PR

**Threat.** An attacker (insider on the repo side) modifies the
contents of a Sharko-authored PR between when Sharko opens it and
when auto-merge fires, causing Sharko to merge attacker-modified
content. Actor profile: insider with repo write.

**Existing mitigation.** Sharko's commit author is *Sharko Bot*
(Tier 1) or *the operator's per-user PAT* (Tier 2). The
auto-merge gate on the Git provider side respects branch
protection rules — if the operator configures "require status
checks to pass" or "require code-owner review", attacker-modified
content has to clear those gates. The
[`auto-merge-failed-after-pr-opened.md`](../site/operator/auto-merge-failed-after-pr-opened.md)
P1 runbook covers the case where auto-merge declines.

**Residual risk.** If the operator has not configured branch
protection, auto-merge will accept whatever's in the PR head at
merge time. Sharko's audit log records the SHA Sharko *intended*;
post-merge the operator can diff against what was *actually*
merged. The V2-6.4 DCO `Signed-off-by` per commit is identity
attestation, not integrity attestation — it does not protect
against tampering by an attacker who can also sign.

**GAP.** No commit-shape verification on PR-merged content
(Sharko could re-read the merged SHA and verify it matches the
intent). Tracked as a v2.x follow-up.

#### B4-R: Repudiation — commit author misattributed

**Threat.** A commit shows up authored by *Sharko Bot* when it was
actually triggered by a malicious actor who used a stolen operator
identity, masking the real actor. Actor profile: insider denying
an action.

**Existing mitigation.** The tiered Git attribution model
([`security.md` § Tiered Git Attribution](../site/operator/security.md#tiered-git-attribution-v120))
classifies every mutating endpoint as Tier 1 (operational) or Tier
2 (configuration). Tier 1 commits are authored by Sharko Bot with
a `Co-Authored-By: <user>` trailer (the trailer is the audit
trail). Tier 2 commits are authored by the *user's* per-user PAT
when configured, so the Git commit author *is* the user; otherwise
they fall back to Sharko Bot with the trailer. Every mutating
audit-log entry records the resolved `attribution_mode`
(`service` / `co_author` / `per_user`) so post-incident review can
distinguish.

**Residual risk.** A stolen API key still authenticates as the
operator that owned it. Repudiation defense here is "the audit
log shows what was done with each credential"; if the credential
was stolen, the *operator* repudiates the actions but the *audit
log* shows them as if the operator did them.

**GAP.** Per-action operator confirmation (re-auth-on-destructive)
is not implemented. v3+ topic.

#### B4-I: Information disclosure — PAT in URL, secret-bearing commit

**Threat.** A Sharko-managed Git PAT appears in a URL, a commit
body, an error message, or a log line. Actor profile: external
(log scraper) or insider (anyone with read access to that surface).

**Existing mitigation.** Sharko uses the Git provider's
authenticated API (HTTPS + Bearer token), never URL-embedded auth.
PATs are stored AES-256-GCM-encrypted in the auth Secret under
`<username>.github_token`
([`security.md` § Tiered Git Attribution](../site/operator/security.md#tiered-git-attribution-v120)).
The V2-2.4 `RedactHandler` catches PAT-shaped values
(`ghp_`-prefixed, `github_pat_`-prefixed) in slog attributes. The
V123 catalog scanner bot uses a separate, read-only PAT scoped to
the catalog repos.

**Residual risk.** A buggy commit-content generator could write a
secret into a values.yaml file. The V121-7 AI-annotation pipeline
guards against this on its input — every upstream values.yaml is
scanned for secret-like patterns and the LLM call is *hard-blocked*
on a match (see
[`security.md` § Secret-leak guard](../site/operator/security.md#secret-leak-guard-on-ai-annotation)).
The output side (what Sharko commits) is not similarly scanned —
this is a partial defense.

**GAP.** Pre-commit secret-shape scan on Sharko-generated commits.
Tracked as a v2.x follow-up.

#### B4-D: Denial of service — Git provider rate limit

**Threat.** Sharko's request rate exhausts the Git provider's API
quota, blocking legitimate operations. Actor profile: typically
self-inflicted (a tight retry loop), occasionally a malicious
insider triggering high-rate batch operations.

**Existing mitigation.** The
[`git-provider-rate-limited.md`](../site/operator/git-provider-rate-limited.md)
P1 runbook covers diagnosis + mitigation. Sharko honors the
provider's `Retry-After` header. Batch endpoints cap at 10 items.
The V2-3 metrics expose Git provider error counters; the budget-burn
alerts page on-call.

**Residual risk.** Sharko's read side (file fetches for
reconciliation) can be aggressive in large fleets. The reconciler's
30s cadence is tunable via Helm; high-fleet operators should
loosen it.

**GAP.** No automatic adaptation to provider rate-limit
headroom. v3+ refinement.

#### B4-E: Elevation of privilege — PAT scope creep

**Threat.** A PAT issued for "Sharko writes to one repo" is
issued with broader scope and gets used (by Sharko or by an
attacker who compromises Sharko) to write to other repos. Actor
profile: insider (operator who provisions the PAT) or
supply-chain.

**Existing mitigation.** The
[`security.md` § Tiered Git Attribution](../site/operator/security.md#tiered-git-attribution-v120)
recommends the narrowest possible PAT scope (`repo` scope on
single-repo deployments; GitHub App tokens for fleet
deployments). Operator-side discipline is the defense; Sharko
does not introspect PAT scope.

**Residual risk.** Sharko cannot enforce PAT narrowness — the
operator issues it. An over-broad PAT gives Sharko more power than
needed.

**GAP.** Periodic PAT-scope check at startup ("Sharko received a
PAT with org-admin scope — is that intentional?"). v3+ topic.

### 5.5 B5: Sharko ↔ secrets provider

Secrets providers are where Sharko *gets* cluster credentials and
addon secrets. AWS Secrets Manager is the primary production
provider; K8s Secrets is the in-cluster fallback. Vault is planned.
This boundary is push-based — Sharko fetches at use time and never
persists.

#### B5-S: Spoofing — fake IAM role assumption, fake provider response

**Threat.** An attacker spoofs the provider response or the
identity Sharko uses to authenticate to the provider. Actor
profile: external (network MITM on the provider call) or
supply-chain (an attacker who compromises the AWS SDK).

**Existing mitigation.** AWS provider calls use IRSA (IAM Roles
for Service Accounts) — short-lived STS credentials minted per pod
session, validated by AWS's OIDC chain. The
[`security-auditor.md` § 7](.claude/team/security-auditor.md#7-remote-cluster-security-v100-phase-3)
documents the pattern. All provider calls are HTTPS with
certificate validation via the system trust store
(`corporate-mitm-tls.md` covers the corporate-CA injection case).
The K8s-Secrets provider uses the in-cluster apiserver which is
TLS-validated. The
[`secrets-provider-unreachable.md`](../site/operator/secrets-provider-unreachable.md)
P0 runbook covers the case where the provider is offline or
returning fake-shaped responses.

**Residual risk.** A compromised AWS account is a Sharko
compromise. Sharko cannot detect AWS-side compromise; the
defense is operator-side (CloudTrail, GuardDuty).

**GAP.** No cross-validation of provider responses (e.g.
hash-pinning the expected response shape). AWS-managed services
are accepted as the trust root.

#### B5-T: Tampering — secret value modified at the provider

**Threat.** The bytes stored in AWS-SM (or K8s Secret) are
modified by an attacker, so Sharko fetches the wrong value. Actor
profile: insider with provider-side write.

**Existing mitigation.** AWS Secrets Manager versions every
modification; CloudTrail records the modifying identity. K8s
Secrets have `resourceVersion` for optimistic concurrency. Sharko
fetches the secret at use time; if the provider record was
tampered, Sharko deploys the tampered value but the audit trail
on the provider side records when and by whom. The
[`logging.md`](../site/developer-guide/logging.md) audit codes
include `secrets_fetch` events so the Sharko-side audit can be
cross-referenced.

**Residual risk.** A successful provider-side tamper that
matches the expected shape is not detected by Sharko. AWS-side
audit is the defense layer.

**GAP.** No hash-pinning on the expected secret shape. Accepting
AWS / K8s as the trust root.

#### B5-R: Repudiation — provider-side action without Sharko audit

**Threat.** A secret is fetched, modified, or rotated on the
provider side without an audit entry on Sharko's side. Actor
profile: insider denying.

**Existing mitigation.** Sharko's audit log records every
*Sharko-driven* provider call. Provider-side audit (CloudTrail
for AWS) is operator-managed.

**Residual risk.** Direct provider-side actions (rotating a
secret directly in AWS-SM, bypassing Sharko) are not visible in
Sharko's audit. Provider-side audit captures them.

**GAP.** No provider-side audit ingest into Sharko. v3+ topic.

#### B5-I: Information disclosure — secret value in log or response

**Threat.** A fetched secret value (cluster kubeconfig, addon
API key, etc.) appears in a log line or API response. Actor
profile: external log scraper or insider with read.

**Existing mitigation.** The push-based model means secret values
are *never cached* in memory or on disk between reconcile cycles —
they are fetched, applied, and the memory is GC'd
([`security.md` § Secrets Provider Security Model](../site/operator/security.md#secrets-provider-security-model)).
The V2-2.4 `RedactHandler` collapses credential-shaped values
before serialization. API responses never include secret values
(addon-secret *definitions* are key→provider_path mappings, not
values — [`security-auditor.md` § 3](.claude/team/security-auditor.md#3-credential-safety)).
The V121-7 AI-annotation `secret_leak_blocked` audit event hard-
blocks the LLM call on any secret-like pattern in `values.yaml`
([`security.md` § Secret-leak guard](../site/operator/security.md#secret-leak-guard-on-ai-annotation)).

**Residual risk.** In-memory secret values exist between fetch
and apply. A pod memory dump during that window recovers them.
The Restricted Pod Security Standard (no `kubectl exec` to a root
shell, no privilege escalation) reduces this attack surface.

**GAP.** Per-secret zero-on-free (explicit byte zeroing after
use) is not implemented. Go's GC eventually reclaims; the
window is bounded but not zero.

#### B5-D: Denial of service — provider rate limit or regional outage

**Threat.** AWS-SM regional outage or rate-limit exhaustion
blocks every Sharko fetch. Actor profile: usually no attacker
(infrastructure failure).

**Existing mitigation.** The
[`secrets-provider-unreachable.md`](../site/operator/secrets-provider-unreachable.md)
P0 runbook covers diagnosis + mitigation. AWS SDK retries with
exponential backoff. The
[`aws-sm-search-access-denied.md`](../site/operator/aws-sm-search-access-denied.md)
P1 runbook covers narrower failure modes.

**Residual risk.** Sharko's reconcile side stalls during
provider outage. Cluster Secrets that ArgoCD already has continue
to work; new operations queue.

**GAP.** Multi-region AWS-SM failover is operator-managed. v3+
"cloud-provider full support" theme covers Vault as a fallback
provider.

#### B5-E: Elevation of privilege — IRSA / IAM scope creep

**Threat.** Sharko's IAM role is granted broader permissions than
the code actually uses, so a Sharko compromise expands into a
broader AWS-account compromise. Actor profile: insider
(misconfigured IAM policy).

**Existing mitigation.** The
[`security.md` § Pod Security](../site/operator/security.md#pod-security)
documents the recommended IRSA scope: `secretsmanager:GetSecretValue`
on a specific secret-name prefix, `sts:AssumeRole` on a specific
role ARN for EKS access. Operator-side IAM discipline is the
defense.

**Residual risk.** Sharko cannot enforce IAM narrowness. An
over-broad IAM policy gives Sharko more power than needed.

**GAP.** Periodic IAM-scope check ("Sharko received an IAM token
with admin scope — is that intentional?"). v3+ topic.

### 5.6 B6: Sharko ↔ catalog source

The catalog is the input to Sharko's marketplace. A compromised
catalog entry shows up to operators as a legitimate addon and gets
installed across the fleet. This is the boundary where
cosign-keyless verification carries the most weight.

#### B6-S: Spoofing — fraudulent catalog entry

**Threat.** An attacker publishes a catalog entry that purports to
be from a trusted source (e.g. a typosquat of the official catalog
URL) and convinces an operator to add it. Actor profile: external
or supply-chain.

**Existing mitigation.** Per-entry cosign-keyless verification
through Fulcio + Rekor with anchored trust regex on the GitHub
Actions `workflow_run` SAN encoding
([`catalog-trust-policy.md`](../site/operator/catalog-trust-policy.md),
[`supply-chain.md`](../site/operator/supply-chain.md), and the
V123-2 epic). The
[`security-auditor.md` § Catalog signing surface](.claude/team/security-auditor.md#catalog-signing-surface-v123-2)
documents the four landmines (TUF cache path, per-entry verification,
trust regex anchoring, workflow_run SAN encoding, modern Sigstore
Bundle format) and the regressions each one bit in v1.23.0-rc.0
through rc.2. The default trust policy includes a
`<defaults>` magic token that expands to CNCF + Sharko-release
defaults; unanchored regexes are rejected at policy load. The TUF
cache lives at a writable path under Sharko's read-only-rootfs pod.

**Residual risk.** The default trust policy trusts CNCF +
Sharko-release-workflow signatures. An attacker who compromises
either trust root is in the trust circle. Operators can
override the default and ship a stricter policy.

**GAP.** No periodic re-fetch of the TUF root (the cache freshness
window). Operator-managed restart picks up new roots.

#### B6-T: Tampering — catalog content modified in flight

**Threat.** A catalog source delivers tampered content (modified
chart values, modified version pinning) to Sharko. Actor profile:
external network MITM or supply-chain.

**Existing mitigation.** Catalog fetches are HTTPS-validated.
Every signed catalog entry round-trips through
`signing.LoadBytesWithVerifier` on load — if the signature does not
verify against the trust policy, the entry surfaces as *Unverified*
in the marketplace
([`catalog-trust-policy.md` § Symptoms](../site/operator/catalog-trust-policy.md#symptoms)).
Operators see the verification status directly in the UI's
Verified badge. The V2-4.3 catalog-source runbooks
([`catalog-source-http-fetch-failed.md`](../site/operator/catalog-source-http-fetch-failed.md),
[`catalog-source-schema-validation-failed.md`](../site/operator/catalog-source-schema-validation-failed.md))
cover the diagnosis side.

**Residual risk.** Unsigned catalog entries (legacy, embedded
catalog) are not signature-protected; they rely on the
embedded-wins policy (the bundled catalog beats remote sources
on conflict).

**GAP.** No `provenance: required` policy mode that would refuse
to install unsigned entries. Tracked as a v2.x follow-up.

#### B6-R: Repudiation — catalog publisher denies authorship

**Threat.** A signed catalog entry that proves harmful — the
publisher denies having signed it. Actor profile: external,
post-incident.

**Existing mitigation.** Sigstore Rekor provides a public
transparency log; every signature is recorded with its identity
and timestamp. Repudiating a signed entry requires repudiating a
Rekor log entry, which is cryptographically hard.

**Residual risk.** Rekor's availability for queries is the
caveat; the log entry is recorded but accessing it requires
Rekor to be up. Sharko does not currently archive Rekor proofs
locally.

**GAP.** Local Rekor proof archival. v3+ refinement.

#### B6-I: Information disclosure — catalog metadata leak

**Threat.** Sharko's catalog source URL list (which catalogs the
operator subscribes to) leaks via API or logs. Actor profile:
external or insider.

**Existing mitigation.** Catalog source URLs are configured via
Helm values, not stored in user-visible state. The catalog
metadata served by Sharko's marketplace API is *intended* to be
operator-visible; there is no "secret catalog" model.

**Residual risk.** A private catalog URL embedded in Sharko's
config is recoverable by any Admin-tier operator.

**GAP.** No "secret catalog" feature is planned; the catalog
model is publish-and-subscribe, not authn-gated.

#### B6-D: Denial of service — catalog source unavailable

**Threat.** A catalog source goes offline or returns slow
responses; Sharko's marketplace refresh stalls. Actor profile:
infrastructure or external DoS.

**Existing mitigation.** Catalog fetches have HTTP timeouts. The
catalog cache (`internal/catalog/cache.go`) serves stale entries
when the source is unreachable. The V2-3
`SharkoCatalogScanFastBurn` alert pages on-call when scan failure
rate breaches SLO budget. The
[`budget-burn-runbook.md`](../site/operator/budget-burn-runbook.md)
points to mitigation.

**Residual risk.** Marketplace freshness window degrades during
source outage. Existing addons continue to operate.

**GAP.** Catalog source failover (try multiple URLs for the same
catalog) is not implemented. Tracked as a v2.x follow-up.

#### B6-E: Elevation of privilege — malicious addon compromises clusters

**Threat.** A signed-but-malicious catalog entry installs an
addon that compromises a managed cluster. Actor profile:
supply-chain — the signature is valid (issued by a trusted
identity who is now malicious).

**Existing mitigation.** Trust-policy anchored regex on the
SAN-encoded `workflow_run` is the primary defense — only specific
workflow runs at specific repos can issue signatures Sharko
trusts ([`catalog-trust-policy.md`](../site/operator/catalog-trust-policy.md)).
Operators can ship a stricter trust policy that only trusts their
own internal catalog. The V123-2 epic + the
[`security-auditor.md` § Catalog signing surface](.claude/team/security-auditor.md#catalog-signing-surface-v123-2)
documents the trust model. The
[`ai-annotation-secret-blocked.md`](../site/operator/ai-annotation-secret-blocked.md)
runbook + the V121-7 secret-leak guard prevent the AI pipeline
from pushing secret-bearing values into addon configs.

**Residual risk.** A trusted-identity compromise (e.g. Sharko's
own release workflow gets compromised) is in the trust circle.
The defense layer is GitHub's branch protection + DCO + 2FA on
the release-workflow's signing identity. Operators running
high-assurance deployments can pin to a specific
`workflow_run.run_id` rather than the workflow path.

**GAP.** No supply-chain SLSA L3 attestations beyond signature
+ workflow_run pinning. Tracked as a v3+ supply-chain hardening
item.

---

## 6. Ancillary boundaries (lighter coverage)

These boundaries are real but lower-risk or less-controlled by
Sharko. They get four cells of STRIDE coverage rather than six —
the omitted categories (R, I for B7; R, E for B8) are documented as
"not materially distinct from other boundaries' coverage."

### 6.1 B7: Sharko ↔ container registry

The Sharko container image lives in `ghcr.io/moranweissman/sharko`.
Image-pull happens at pod start; once running, this boundary is
not exercised until the next pod restart.

#### B7-S: Spoofing — fraudulent image

**Threat.** An operator pulls an image that is not the official
Sharko image (typosquat, malicious registry, MITM during pull).

**Mitigation.** Cosign-keyless signature on every Sharko image,
verifiable per
[`supply-chain.md`](../site/operator/supply-chain.md). Operators
who require integrity verify the image before deploy; the Helm
chart documents the verify command.

**Residual risk + GAP.** Verification is operator-opt-in; the
image-pull pipeline does not automatically reject unsigned images
without operator-side admission control. Tracked as a v3+
"required-cosign-verify on admission" theme.

#### B7-T: Tampering — image bytes modified

**Threat.** A compromised registry serves modified image bytes.

**Mitigation.** The cosign signature verifies the manifest list
digest; tampered bytes change the digest and fail verification.

**Residual risk + GAP.** Same as B7-S — verification is opt-in.

#### B7-D: Denial of service — registry unavailable

**Threat.** ghcr.io outage blocks new pod starts.

**Mitigation.** Sharko's existing replicas continue to run.
Operators can mirror the image to an internal registry.

**Residual risk + GAP.** Single-replica deployments have no
failover; HA multi-replica is on the v3+ roadmap.

#### B7-E: Elevation of privilege — image runs with broader scope

**Threat.** A modified image gains higher permissions than the
official Sharko image expects.

**Mitigation.** The pod's security context (non-root, no privilege
escalation, all caps dropped, read-only rootfs) is enforced by
K8s admission, not by the image. A tampered image still runs
within the operator-configured securityContext.

**Residual risk + GAP.** None beyond B7-T.

### 6.2 B8: In-cluster network

The in-cluster network is operator-managed (NetworkPolicy, optional
service mesh). Sharko ships defaults in `security.md` but does not
enforce them.

#### B8-S: Spoofing — east-west pod impersonation

**Threat.** Another in-cluster pod impersonates ArgoCD or the
apiserver to Sharko.

**Mitigation.** HTTPS validates the target identity via TLS
certificate. K8s Service IPs are routed by kube-proxy; east-west
spoofing requires compromising the kube-proxy or the node network.

**Residual risk + GAP.** Operator-managed via NetworkPolicy +
optional mesh mTLS. Sharko's
[`security.md` § Network Policy](../site/operator/security.md#network-policy)
documents the recommended posture.

#### B8-T: Tampering — east-west traffic modification

**Threat.** A compromised node-network component modifies
Sharko-originated traffic to ArgoCD or the apiserver.

**Mitigation.** HTTPS detects modification at the TLS layer.

**Residual risk + GAP.** Same as B8-S.

#### B8-I: Information disclosure — east-west traffic capture

**Threat.** A compromised in-cluster network captures Sharko's
HTTPS traffic.

**Mitigation.** HTTPS encrypts content; only metadata (source,
destination, byte count) is observable.

**Residual risk + GAP.** Service-mesh mTLS adds peer identity
to the metadata; operator-managed.

#### B8-D: Denial of service — east-west DoS

**Threat.** Another in-cluster pod floods Sharko's apiserver
calls or ArgoCD calls.

**Mitigation.** K8s resource quotas + NetworkPolicy throttling +
service-mesh rate limits — all operator-managed.

**Residual risk + GAP.** None beyond operator network posture.

---

## 7. Attack surface table

The attack surface table cross-links Sharko's externally-reachable
endpoints to the V2-6.3 API tier inventory
([`api-stability.md`](../site/developer-guide/api-stability.md)) and
tags each entry-point's residual risk. Beta- and alpha-tier endpoints
carry **higher residual risk** because the contract may evolve, the
implementation may be less battle-tested, and integrations may be
fewer. This is the natural cross-reference for a 3rd-party
reviewer's "where should I focus" question.

| Entry point | Surface | Tier (per `api-stability.md`) | Auth required | Residual risk notes |
|---|---|---|---|---|
| HTTP API — Cluster registration + management | 24 endpoints | `stable` (default) | Yes (session / API key) | Core production flow. Battle-tested. RBAC enforced via `requireAdmin`. |
| HTTP API — Addon operations | 14 endpoints | mostly `stable`; 4 `beta` (version-matrix, unwrap-globals, AI annotate, AI opt-out) | Yes | Beta endpoints carry higher residual risk; AI endpoints especially because the LLM call is the highest-impact code path. |
| HTTP API — Dashboard / fleet / health | 8 endpoints | mostly `stable`; 1 `beta` (observability-overview); 2 `alpha` (embedded-dashboards) | Read endpoints: yes (Viewer+). | Read-only; lower impact than write side. `alpha` tier explicitly excluded from integration contracts. |
| HTTP API — Connection management | 8 endpoints | `stable` | Yes (Admin for write) | Holds Sharko's own credentials. Highest sensitivity write surface besides cluster registration. |
| HTTP API — AI features | 9 endpoints | `beta` (all) | Yes | LLM call gated by V121-7 secret-leak guard (`secret_leak_blocked` audit event). |
| HTTP API — PR tracking | 5 endpoints | `stable` | Yes | Read-only beyond `DELETE /prs/{id}`. |
| HTTP API — Catalog | ~25 endpoints | mostly `stable`; some `beta` | Mostly yes; signature data is public-read | Signature verification on every load via `signing.LoadBytesWithVerifier`. |
| HTTP API — Auth (`/auth/login`, etc.) | 6 endpoints | `stable` | Login is no-auth; rate-limited per IP | The primary attacker target. `/auth/login` is rate-limited at 10/IP/min. |
| HTTP API — Tokens (API key management) | 4 endpoints | `stable` | Yes (Admin) | bcrypt-hashed storage; plaintext shown ONCE on creation. |
| HTTP API — Audit | 2 endpoints | `stable` | Yes (Viewer+) | Read-only. SSE stream at `/audit/stream`. |
| `/metrics` (Prometheus) | 1 endpoint | `stable` | **No** — intentionally unauthenticated for scraping | **Cluster-network-policy boundary.** Document explicitly: scrapeable from Prometheus, not from the internet. Operators MUST restrict via NetworkPolicy. |
| `/healthz`, `/livez` | 2 endpoints | `stable` | No | Standard k8s probes. |
| Webhook receiver — `POST /api/v1/webhooks/git` | 1 endpoint | `stable` | HMAC-SHA256 via `X-Hub-Signature-256` | Validates against `SHARKO_WEBHOOK_SECRET`. Empty secret disables verification — warned in `security.md`. |
| `/swagger/*` (Swagger UI) | 1 endpoint | `stable` | No (documents the API surface; not an attack vector in itself) | Operators who treat Swagger as sensitive can disable it via Helm value. |
| Catalog ingress — HTTPS GET to operator-configured catalog URLs | N URLs | n/a (outbound) | n/a; trust derives from cosign | TLS + cosign-keyless + anchored trust regex + TUF cache. |
| Container image / Helm chart | 2 artifacts | n/a (artifacts) | n/a; trust derives from cosign | Signed via GHA release workflow; verifiable per `supply-chain.md`. |
| Downstream K8s API requests (managed clusters) | per-cluster | n/a (outbound) | Kubeconfig fetched at use time | Connect-operate-disconnect; never persisted. |
| Downstream Git provider requests | per-PR | n/a (outbound) | Tier 1 (service PAT) or Tier 2 (per-user PAT) | Push-based; merge-side validation via operator-configured branch protection. |
| Downstream ArgoCD API | per-cluster Secret op | n/a (outbound) | ArgoCD account token (AES-256-GCM at rest) | Narrowed to Secret-only reads per V125-1-10. |
| Downstream AWS-SM / K8s-Secrets API | per-fetch | n/a (outbound) | IRSA (AWS) / ServiceAccount RBAC (K8s) | Push-based; values not cached between fetches. |

Total HTTP API endpoint count tracks the V2-6.3 inventory: 95 stable
/ 26 beta / 7 alpha = 128 endpoints. Beta and alpha tiers represent
~26% of the surface and concentrate the residual risk.

---

## 8. OWASP Top 10 (2021) mapping

The OWASP Top 10 is a widely-recognised baseline for web-application
threats. Sharko has a web-application surface (the UI + API) plus a
control-plane surface (the reconcilers + providers + catalog
pipeline). The mapping below describes Sharko's posture per
category, with status one of:

- **Mitigated** — Sharko ships a substantive defense, documented and tested.
- **Partial** — Sharko has defense-in-depth but at least one call-site or coverage gap is tracked.
- **Gap** — no Sharko-side mitigation; tracked for v3+.
- **N/A** — category does not apply to Sharko's surface.

> OWASP published a 2025 update introducing new categories
> (e.g. A03:2025 "Software Supply Chain Failures"). The sprint plan
> locked OQ #4 to **OWASP Top 10 (2021)** because that is the
> baseline most security consultants currently target. The mapping
> below uses 2021 category labels; supply-chain coverage that the
> 2025 list breaks out separately is in [§9](#9-cncf--slsa-supply-chain-analysis).

### A01:2021 — Broken Access Control

**Status: Mitigated (with documented residual coarseness)**

Every mutating endpoint enforces RBAC at the handler boundary
(`s.requireAdmin(w, r)` first thing — full list in
[`security-auditor.md` § 2](.claude/team/security-auditor.md#2-auth-on-write-endpoints)).
The `audit_coverage_test.go` regression suite verifies that every
mutating handler emits an audit entry, which implicitly forces RBAC
to be exercised. Auth bypass is a P0 with dedicated runbook
([`auth-bypass.md`](../site/operator/auth-bypass.md)). The model
is coarse (Admin / Operator / Viewer); fine-grained RBAC is on the
v3+ roadmap. CSRF tokens issued per session; SameSite=Lax on
session cookies. The
[`security-auditor.md` § 7](.claude/team/security-auditor.md#7-remote-cluster-security-v100-phase-3)
documents the connect-operate-disconnect contract that limits
managed-cluster RBAC blast radius.

### A02:2021 — Cryptographic Failures

**Status: Mitigated**

Passwords hashed with bcrypt (cost 10). API keys hashed with bcrypt;
plaintext shown ONCE. Session cookies are 32-byte `crypto/rand`
tokens. Connection credentials (Git PAT, ArgoCD token) encrypted
at rest with AES-256-GCM via `SHARKO_ENCRYPTION_KEY`
([`security.md` § Secret Encryption](../site/operator/security.md#secret-encryption)).
HTTPS mandatory at ingress; HSTS with 1-year max-age. TLS to
upstream services validated via system trust store with optional
corporate-CA injection per
[`corporate-mitm-tls.md`](../site/operator/corporate-mitm-tls.md).
Catalog signatures use Sigstore modern Bundle format with Fulcio +
Rekor. **Residual:** encryption key rotation is manual today; v3+
roadmap item.

### A03:2021 — Injection

**Status: Mitigated**

Sharko does not concatenate user input into SQL queries (no SQL
database — state lives in K8s ConfigMaps). YAML inputs are
schema-validated against committed JSON Schemas before unmarshalling
(V125-1-9 envelope discipline) — direct `yaml.Unmarshal` over
untrusted bytes is a critical-finding violation
([`security-auditor.md` § 12](.claude/team/security-auditor.md#12-schema--envelope-integrity-v125-1-9)).
Cluster names regex-validated. URL parameters `url.PathEscape`-d.
CLI shell-out is restricted (Sharko shells out via Helm CLI for
chart operations; arguments are constructed programmatically, not
template-strings). The
[`catalog-parse-failure-on-startup.md`](../site/operator/catalog-parse-failure-on-startup.md)
P1 runbook covers the case where catalog parse fails on
adversarial input.

### A04:2021 — Insecure Design

**Status: Mitigated**

This document is the design-level threat model. The V2-2 audit log
discipline + V2-3 Prometheus telemetry give every design decision
an observable surface. The V125-1-8 ownership-label gate is a
design-level integrity constraint (every Secret-delete checks the
label first). The "no AVP / no Redis bridge" design constraint
([`security-auditor.md` § 13](.claude/team/security-auditor.md#13-no-avp--no-redis-leak-design-constraint))
is a design-level rejection of a known anti-pattern. The schema
envelope (V125-1-9) is a design-level trust-boundary discipline.
The DCO commit attribution (V2-6.4) is a design-level provenance
constraint.

### A05:2021 — Security Misconfiguration

**Status: Mitigated (with operator-side responsibility)**

Sharko's Helm chart ships secure defaults: non-root pod
(UID 1001), read-only rootfs, all capabilities dropped, no
privilege escalation (Restricted PSS compliant). Security headers
(CSP, HSTS, X-Frame-Options, X-Content-Type-Options,
Referrer-Policy) set by default. `SHARKO_WEBHOOK_SECRET` defaults
to empty with a warning. The 35 V2-4 operator runbooks cover
misconfiguration recovery. **Residual:** operators can disable
security features (e.g. `config.devMode: true` allows credential
fallback from env vars), but the
[`security.md` § Secrets Management Recommendations](../site/operator/security.md#secrets-management-recommendations)
section documents which to avoid in production.

### A06:2021 — Vulnerable and Outdated Components

**Status: Partial**

The CI pipeline includes Trivy scans of Go dependencies and the
container image. `go.mod` is reviewed at each release. Catalog
signatures protect the addon supply chain (V123-2). The Sharko
image itself is cosign-signed via GHA per
[`supply-chain.md`](../site/operator/supply-chain.md). **Gap:**
no automated dependency-update pipeline (no Renovate / Dependabot
auto-merge on minor CVEs). Maintainer-side discipline only.
Tracked as a v2.x follow-up.

### A07:2021 — Identification and Authentication Failures

**Status: Mitigated**

Login rate-limited (10/IP/min). Session cookies 24h with HttpOnly
+ SameSite=Lax. API keys bcrypt'd. The V125-1-7 token-leak class
(hash collision) is closed and covered by
[`auth-bypass.md`](../site/operator/auth-bypass.md). The
[`logging.md` § Auth audit codes](../site/developer-guide/logging.md)
records every login attempt with a stable code (`login_failed` /
`login_success`); the audit-bypass detection signal is
`login_failed` count dropping to zero while traffic continues.
GitHub OAuth callback URI is operator-configured. **Residual:**
no MFA on Sharko logins; SSO/OIDC group mapping is v3+ roadmap.

### A08:2021 — Software and Data Integrity Failures

**Status: Mitigated**

Container image + Helm chart cosign-signed (V121+, per
[`supply-chain.md`](../site/operator/supply-chain.md)). Catalog
entries cosign-signed with anchored trust-regex on `workflow_run`
SAN (V123-2). DCO `Signed-off-by` per commit (V2-6.4). Schema
envelope validates every YAML read (V125-1-9). The
[`security-auditor.md` § 11–12](.claude/team/security-auditor.md#11-ownership-label-gate-v125-1-8)
sections document the integrity invariants. **Residual:** Sharko
itself is not yet SLSA L3 (see [§9](#9-cncf--slsa-supply-chain-analysis)).

### A09:2021 — Security Logging and Monitoring Failures

**Status: Mitigated**

V2-2 100% slog logging with correlation IDs across middleware →
service → orchestrator → reconciler → audit. V2-2.4 RedactHandler
defense-in-depth. V2-3 Prometheus telemetry with multi-burn-rate
alerts ([`budget-burn-runbook.md`](../site/operator/budget-burn-runbook.md)).
Audit log with stable action codes + real-time SSE stream at
`/api/v1/audit/stream`. 12 P0 runbooks shipped in V2-4.3 cover
the security-shaped failure modes. **Partial element:** the
[`logging-audit-punchlist.md`](../site/developer-guide/logging-audit-punchlist.md)
documents specific call-site cleanup still pending — the wrapper
saves the value, but the call sites need follow-up so the wrapper
is not the sole defense.

### A10:2021 — Server-Side Request Forgery (SSRF)

**Status: Mitigated**

The SSRF guard documented in
[`security.md` § SSRF guard](../site/operator/security.md#ssrf-guard-on-url-fetching-endpoints)
rejects URLs that resolve to loopback, RFC1918 private,
link-local (cloud metadata), IPv6 ULA, multicast, or unspecified
ranges. An optional `SHARKO_URL_ALLOWLIST` env var restricts
outbound fetches to a named hostname set. Catalog fetches and
`/api/v1/catalog/validate` go through the guard. The
[`webhook-handler-failures.md`](../site/operator/webhook-handler-failures.md)
runbook covers the inbound webhook surface (signature
verification rather than SSRF). **Defense-in-depth:** operators
should additionally pin egress with NetworkPolicy per
[`security.md` § Network Policy](../site/operator/security.md#network-policy).

---

## 9. CNCF / SLSA supply-chain analysis

Sharko is pre-CNCF-sandbox. The supply-chain posture is documented
here as the maintainer's structured assessment against the SLSA v1.0
Build track and the supply-chain-attack taxonomy commonly used by
CNCF Security TAG reviews.

### Supply-chain attack taxonomy

#### Typosquatting

**Threat.** A malicious package with a name similar to a legitimate
Sharko-trusted package is registered and propagated into the
trust chain.

**Mitigation.** Sharko's catalog trust policy uses **anchored**
regex (`^...$`) on the cosign certificate identity. Unanchored
patterns are rejected at policy load
([`catalog-trust-policy.md`](../site/operator/catalog-trust-policy.md)).
The `<defaults>` magic-token expands to CNCF + Sharko-release
defaults that name-match exactly the upstream repository paths.
Typosquats on the catalog source URL fail signature verification
because the certificate identity does not match the operator's
anchored regex.

**Residual risk.** Operators who write their own trust policy can
introduce unanchored patterns (Sharko warns at load); strict
defaults are the fallback.

#### Dependency confusion

**Threat.** A malicious package with the same name as an internal
dependency is registered on a public registry; the build system
prefers the public version.

**Mitigation.** Sharko's `go.mod` pins every direct dependency with
a version + checksum (`go.sum`). The Go module proxy
(`proxy.golang.org`) is the default resolver; private dependencies
require explicit `GOPRIVATE` configuration. No npm / pip / Cargo
dependencies — the Go ecosystem's module-proxy + checksum DB
model is the trust layer.

**Residual risk.** A successful supply-chain compromise of the
Go module proxy would affect every Go project; this is shared
ecosystem risk and is monitored by the Go security team.

#### Build-time provenance

**Threat.** The release artifact was built by a process the
operator does not trust.

**Mitigation.** Every Sharko release is built by a GitHub Actions
release workflow at a specific tag, with cosign signing on the
manifest list. The signature embeds the `job_workflow_ref` SAN
(`https://github.com/MoranWeissman/sharko/.github/workflows/release.yml@refs/tags/vX.Y.Z`)
so operators can verify the artifact came from a real release run
at a real tag ([`supply-chain.md`](../site/operator/supply-chain.md)).
The `--certificate-identity-regexp` and `--certificate-oidc-issuer`
flags pin to the Sharko repo + GitHub Actions OIDC issuer
respectively.

**Residual risk.** GitHub's OIDC issuer is the trust root; an
attacker who compromises GHA OIDC has the keys to many open-source
ecosystems.

#### Source tampering

**Threat.** Malicious code is committed to Sharko's main branch
between trusted reviews.

**Mitigation.** V2-6.4 DCO `Signed-off-by` per commit. GitHub
branch protection (operator-side discipline). The
solo-maintainer model means every PR is reviewed by the
maintainer; the CNCF maturity gap (today ~70%; target ~90%
post-CNCF-sandbox) is documented in
[`project_attribution_design`](https://github.com/MoranWeissman/sharko/blob/main/.claude/team/product-manager.md)
memory.

**Residual risk.** Solo-maintainer single-point-of-failure on
review discipline. CNCF Sandbox acceptance brings multi-maintainer
review as a structural mitigation.

### SLSA v1.0 levels

SLSA v1.0 organises supply-chain assurance into Build tracks
(L1 — provenance exists; L2 — provenance signed; L3 — build
isolation; L4 — hermetic builds).

Sharko's current SLSA level by Build track:

| Aspect | Current level | Evidence | Gap to next level |
|---|---|---|---|
| **Build L1** — Provenance exists | ✅ Achieved | GHA workflow produces release artifacts at known tags; CycloneDX SBOM published with each release per `supply-chain.md`. | n/a — achieved. |
| **Build L2** — Provenance signed | ✅ Achieved | cosign-keyless signatures on container image + Helm OCI chart + release binaries; verifiable via [`supply-chain.md`](../site/operator/supply-chain.md). Rekor transparency log. | n/a — achieved. |
| **Build L3** — Build isolation | 🟡 Partial | GHA hosted runners provide isolation per job; secrets are scoped to the release workflow; no shared mutable build state. | Hardened, ephemeral build environments with audited provisioning; no human shell access during the build. CNCF Sandbox + L3 attestation is a target post-CNCF-sandbox. |
| **Build L4** — Hermetic builds | ⚠️ Not achieved | Go modules + Helm dependencies pulled at build time from proxy. | Fully sealed builds with all inputs pinned and pre-fetched. Future v3+ work; large lift relative to user-visible benefit at Sharko's current scale. |

**The maintainer's claim for v2.0.0 is SLSA Build L2 with documented
L3 path.** This matches the typical CNCF Sandbox cohort.

### CNCF Security TAG threat-model checklist

The CNCF Security TAG threat-model document
(`cncf/tag-security`) provides a checklist projects use during
incubation review. Sharko's posture against the standard
checklist items:

- ✅ Threat model exists (this document).
- ✅ STRIDE per trust boundary documented.
- ✅ Asset inventory.
- ✅ Disclosure policy in `SECURITY.md`.
- ✅ Existing-mitigation cross-reference.
- ✅ Identified-gap section.
- ✅ Reference frameworks cited.
- 🟡 Disclosure SLO numbers (documented as best-effort
  solo-maintainer baseline; aspirational pending CNCF Sandbox).
- ⚠️ Independent 3rd-party review (planned post-CNCF-sandbox; the
  V2-6.5 review-prep bundle is the prep artifact).
- ⚠️ Periodic re-review cadence (no formal cadence today; aim is
  per-major-release).

---

## 10. Existing mitigations — comprehensive table

This table is the cross-reference catalogue for every V2-shipped
(and load-bearing pre-V2) security mitigation. Each row is the
PR-level signal a 3rd-party reviewer needs to know "this isn't a
gap — it's covered by X".

> **Partial-mitigation rows are flagged explicitly.** A "partial"
> tag means defense-in-depth is in place at one layer but a
> call-site or coverage gap is tracked — usually in
> `logging-audit-punchlist.md` — so the wrapper does not silently
> rot into the only defense.

| # | Mitigation | Surface protected | Shipped in | Status |
|---|---|---|---|---|
| M01 | `RedactHandler` slog wrapper | Sensitive-field redaction (passwords, tokens, kubeconfigs, JWTs, base64 blobs) before any log sink | V2-2.4 / PR #368 | **Partial** — wrapper is solid; call-site cleanup tracked in [`logging-audit-punchlist.md`](../site/developer-guide/logging-audit-punchlist.md) (e.g. `internal/auth/store.go:634` bootstrap-admin emission). |
| M02 | 100% slog logging across `internal/` + `cmd/` | Structured, `jq`-able logs; no stdlib `log` calls | V2-2.1 / PR #367 | Mitigated. |
| M03 | `request_id` correlation propagation | End-to-end correlation across middleware → service → orchestrator → reconciler → audit | V2-2.2 / PR #367 | Mitigated. |
| M04 | Audit log + stable audit action codes | Repudiation defense; forensic primary | V2-2.x | Mitigated. |
| M05 | `/api/v1/audit/stream` SSE | Real-time operator observability of audit events | V2-2 | Mitigated. |
| M06 | Prometheus telemetry — histograms + counters with `request_id` exemplars | Operational visibility = security visibility | V2-3.1 / PR #371 | Mitigated. |
| M07 | Multi-window multi-burn-rate alerting | SLO budget burn paging on critical surfaces | V2-3.2 / PR #372 | Mitigated. |
| M08 | `PrometheusRule` template in Helm chart + 35 alert runbooks | Self-deployable monitoring stack with on-call documentation | V2-3.3 / PR #373 | Mitigated. |
| M09 | 35 operator runbooks (V2-4 set) | Incident response — symptoms / diagnosis / mitigation / root cause / prevention per failure mode | V2-4.3 + V2-4.4 | Mitigated. |
| M10 | 12 P0 security-shaped runbooks | Critical incident response for security-class failure modes (auth bypass, credential leak in logs, silent secret-push failure, etc.) | V2-4.3 / PR #376 | Mitigated. |
| M11 | Failure-mode index with 57 modes (18 P0 / 22 P1 / 17 P2) | Operator first-stop search surface for any Sharko error | V2-4.2 / PR #375 | Mitigated. |
| M12 | cosign-keyless signing on container image + Helm chart + release binaries | Image / chart / binary supply chain | V121+ / `supply-chain.md` | Mitigated. |
| M13 | Anchored trust-regex on `workflow_run` SAN for catalog signing | Per-entry catalog trust verification | V123-2 / `catalog-trust-policy.md` | Mitigated. |
| M14 | TUF cache on writable path under read-only rootfs | Sigstore root chain refresh in hardened pod | V123-2 | Mitigated. |
| M15 | Modern Sigstore Bundle format end-to-end | Avoid legacy v1 bundle gotchas | V123-2 | Mitigated. |
| M16 | V125-1-8 cluster reconciler + `app.kubernetes.io/managed-by: sharko` label gate | RBAC narrowing + cross-tool drift impossibility on canonical path | V125-1-8 / PR #348 | Mitigated. |
| M17 | V125-1-10 ArgoCD-Secret-only reads | Least-privilege ArgoCD interaction | V125-1-10 | Mitigated. |
| M18 | V125-1-11 typed `ProviderConfig` split into 3 orthogonal types | Compile-time prevention of cross-domain credential leakage | V125-1-11 | Mitigated. |
| M19 | Schema envelope + read-time JSON Schema validation on `managed-clusters.yaml` + `addon-catalog.yaml` | Trust-boundary validation for YAML inputs | V125-1-9 / PR #346 | Mitigated. |
| M20 | V2-5 clean cut — zero compat shims | Reduced attack surface from dead code paths | V2-5 / PR #374 | Mitigated. |
| M21 | V2-6.1 governance docs (`SECURITY.md`, `MAINTAINERS.md`, `GOVERNANCE.md`, `CODE_OF_CONDUCT.md`, `CONTRIBUTING.md`, `ADOPTERS.md`) | Disclosure policy + structural maintainer accountability | V2-6.1 / PR #366 | Mitigated. |
| M22 | V2-6.4 DCO `Signed-off-by` per commit | Commit provenance / source-tampering deterrent | V2-6.4 / PR #366 | Mitigated. |
| M23 | V2-6.3 API stability contract (`api-stability.md`) | Integrator-side contract → predictable change cadence reduces surprise-CVE risk | V2-6.3 / PR #380 | Mitigated. |
| M24 | Tiered Git attribution (Tier 1 service / Tier 2 per-user PAT) | Auth separation; per-action attribution | V125 + earlier | Mitigated. |
| M25 | RBAC tiers (Admin / Operator / Viewer) | Coarse access control on mutating endpoints | pre-V2 | Mitigated (coarse — fine-grained on v3+). |
| M26 | Helm chart ServiceAccount + narrow ClusterRole | K8s-level least privilege | pre-V2 | Mitigated. |
| M27 | Restricted PSS-compliant pod (non-root, read-only rootfs, no priv-esc, caps dropped) | Pod-level hardening | pre-V2 / `security.md` § Pod Security | Mitigated. |
| M28 | Rate limiting on `/auth/login` (10/IP/min) + admin writes (30/IP/min) | DoS + brute-force defense | pre-V2 | Mitigated (depends on correct `SHARKO_TRUSTED_PROXIES`). |
| M29 | SSRF guard on URL-fetching endpoints (RFC1918 / link-local / loopback deny + optional allowlist) | Internal-resource targeting defense | pre-V2 / `security.md` § SSRF | Mitigated. |
| M30 | V121-7 secret-leak guard on AI annotation pipeline (`secret_leak_blocked` audit code) | Pre-LLM-call hard-block on secret-like values in `values.yaml` | V121-7 / `security.md` § Secret-leak guard | Mitigated. |
| M31 | HMAC-SHA256 webhook verification (`X-Hub-Signature-256`) | Webhook origin spoofing defense | pre-V2 / `internal/api/webhooks.go` | Mitigated (operator must set `SHARKO_WEBHOOK_SECRET`). |
| M32 | Security headers (CSP / HSTS / X-Frame-Options / X-Content-Type-Options / Referrer-Policy) | XSS / clickjacking / MIME-confusion defense | pre-V2 / `security.md` § Security Headers | Mitigated. |
| M33 | CSRF tokens on mutating endpoints + SameSite=Lax session cookies | CSRF defense | pre-V2 | Mitigated. |
| M34 | AES-256-GCM at-rest encryption on `sharko-connections` Secret | Encryption at rest for Sharko's own service-identity tokens | pre-V2 / `security.md` § Secret Encryption | Mitigated (encryption key rotation manual — v3+ tooling gap). |
| M35 | Push-based secrets reconciliation (no caching between reconcile cycles) | Bounded blast radius on Sharko compromise | pre-V2 / `security.md` § Secrets Provider Security Model | Mitigated. |
| M36 | No-AVP / no-Redis-leak design constraint | Architecturally rejected anti-pattern | pre-V2 / [`security-auditor.md` § 13](.claude/team/security-auditor.md#13-no-avp--no-redis-leak-design-constraint) | Mitigated (design-level). |
| M37 | bcrypt password + API-key hashing (cost 10) | Credential storage hardening | pre-V2 | Mitigated. |
| M38 | API-key plaintext shown ONCE on creation | Credential retrieval impossibility | pre-V2 | Mitigated. |
| M39 | Connect-operate-disconnect on managed clusters | No persistent managed-cluster kubeconfig storage | pre-V2 / [`security-auditor.md` § 7](.claude/team/security-auditor.md#7-remote-cluster-security-v100-phase-3) | Mitigated. |
| M40 | CI security-grep gate for forbidden internal-org tokens | Content policy enforcement | pre-V2 / `CLAUDE.md` § Content Policy | Mitigated. |

Total: 40 mitigations. Of those, **2 are flagged Partial** (M01
RedactHandler call-site cleanup; M25 coarse RBAC pending v3+
fine-grained). The remainder are full Mitigated. Pre-V2 mitigations
(M24–M40) provide the baseline; V2 (M01–M23) is the production-launch
hardening overlay. Approximately **95% of the rows reference
V2-shipped artifacts** — this is the credit-not-redo principle in
action.

---

## 11. Identified gaps (residual risk for v3+)

These are residual risks the maintainer is **knowingly carrying**
into v2.0.0. Each gap is tracked in the public
[`roadmap.md`](../site/community/roadmap.md) and is not expected to
land as a side-effect of any v2.x patch — they are v3-shaped (or
"near-term v2.x post-launch" where the scope is small).

### G01: Fine-grained RBAC

**Today.** Three RBAC tiers (Admin / Operator / Viewer) enforced at
the handler boundary. An Operator-tier user can write to every
cluster in the fleet; a CI/CD API key cannot be scoped to a single
cluster or environment.

**Where tracked.** [`roadmap.md` § Medium-term — Fine-grained RBAC](../site/community/roadmap.md#medium-term-themes-v3x).

**Why v3+.** Resource-scoped permissions are a data-model change
(every resource needs a scope label) and an API change (every
endpoint needs scope-checking). A clean v3 surface.

### G02: SSO / OIDC

**Today.** Local user accounts (`sharko-users` ConfigMap) + API
keys + optional GitHub OAuth. No SAML / OIDC / SSO group → role
mapping.

**Where tracked.** [`roadmap.md` § Medium-term — SSO / OIDC](../site/community/roadmap.md#medium-term-themes-v3x).

**Why v3+.** SSO provider integrations are a substantial code
addition and require careful end-to-end testing against the major
IdPs (Google Workspace, Okta, Entra ID).

### G03: External vault for Sharko's *own* tokens

**Today.** Sharko's Git PAT + ArgoCD account token live in a K8s
Secret in Sharko's namespace, AES-256-GCM-encrypted with
`SHARKO_ENCRYPTION_KEY`.

**Where tracked.** [`roadmap.md` § Medium-term — Cloud-provider full support](../site/community/roadmap.md#medium-term-themes-v3x)
(Vault as a credential source covers this).

**Why v3+.** Vault integration is a new provider implementation
that must dovetail with the V125-1-11 typed provider config split.

### G04: HA multi-replica

**Today.** Single-pod deployment. The pod can be restarted by the
deployment controller; while down, write side is unavailable.

**Where tracked.** [`roadmap.md` § Operator mode (v3+)](../site/community/roadmap.md#operator-mode-v3) (leader election is part of operator mode).

**Why v3+.** Sharko's in-memory session store and ring-buffer
audit log are SPOF-shaped; multi-replica requires moving them to
durable shared state. Aligns with the operator-mode theme.

### G05: Encryption key rotation tooling

**Today.** Rotating `SHARKO_ENCRYPTION_KEY` requires updating the
env var and re-saving every connection in the UI (per
[`security.md` § Secret Encryption](../site/operator/security.md#secret-encryption)).
No automated tooling.

**Where tracked.** v2.x near-term post-launch item.

**Why near-term.** Small-surface helper CLI / API; could ship as a
v2.x minor without breaking changes.

### G06: Formal disclosure SLO

**Today.** Best-effort solo-maintainer baseline documented in
[§12](#12-disclosure-process) (5 business days ack, 30 days HIGH,
90 days MEDIUM, no SLO LOW). Aspirational pending CNCF Sandbox
acceptance and a documented transition to a formal SLO.

**Where tracked.** [`SECURITY.md`](../../SECURITY.md) + this
threat model.

**Why pending.** Formal SLOs require either a paid-staff
maintainer or a foundation-coordinated security team. CNCF Sandbox
acceptance triggers the transition.

### G07: GitHub Security Advisory CVE flow

**Today.** No CNA (CVE Numbering Authority) assignment. Advisories
appear in release notes only.

**Where tracked.** [`SECURITY.md` § Public Security Advisories](../../SECURITY.md#public-security-advisories).

**Why pending.** GHSA-issued CVEs require either a project that
qualifies for GitHub's open-source CNA or a foundation-issued CNA.
CNCF Sandbox acceptance enables this.

### G08: Scale testing > 100 clusters

**Today.** End-to-end tested at ~20 clusters. The V2-1 perf
baselines cover p50 / p95 / p99 at the surface level but the fleet
size in those baselines is documentation-only.

**Where tracked.** v2.x near-term post-launch item.

**Why near-term.** Fleet-scale testing requires either cloud
spend or a contributor with access to a large fleet. The maintainer
is collecting expressions of interest from adopters via the V2-6.1
`ADOPTERS.md` flow.

### G09: Periodic dependency auto-update pipeline

**Today.** Trivy scans in CI flag vulnerable dependencies; updates
are manual.

**Where tracked.** v2.x near-term post-launch item.

**Why near-term.** Renovate / Dependabot configuration is a small
mechanical change. Awaiting maintainer bandwidth.

### G10: Periodic credential-shape scan on rendered API responses

**Today.** The `security-auditor` checklist covers this manually;
no CI gate.

**Where tracked.** v2.x near-term post-launch item.

**Why near-term.** Small CI addition — grep rendered JSON for
known credential shapes (JWT prefix, kubeconfig structure). Could
ship as a v2.x patch.

### G11: Tamper-evident audit log

**Today.** Audit log is append-only on the Sharko side but has no
hash chain or signing. Operators ship to an immutable downstream
store for forensic integrity.

**Where tracked.** v3+ topic.

**Why v3+.** Hash-chained audit logs require schema changes and
operator-side verification tooling. Operator-side downstream
immutability (S3 Object Lock, write-only CloudWatch destinations)
is the v2.0.0 mitigation.

---

## 12. Disclosure process

### Current state (v2.0.0)

The maintainer is solo. Disclosure SLOs are **best-effort** and
calibrated against the maintainer's actual response capacity rather
than aspirational corporate SLAs the project cannot meet.

| Severity | Best-effort SLO | Notes |
|---|---|---|
| **Acknowledgement (all severities)** | 5 business days | Per `SECURITY.md`. Aspires to faster; documented at the slower end so the maintainer can meet it on holidays. |
| **HIGH severity fix** | 30 days from acknowledgement | Critical issues prioritized above feature work. |
| **MEDIUM severity fix** | 90 days from acknowledgement | Scheduled into the next appropriate release. |
| **LOW severity fix** | No SLO | Tracked in normal backlog. Operator-correctable cases may stay as documentation pointers. |

### Reporting channel

Per `SECURITY.md`:

- Email **moran.weissman@gmail.com** with subject line
  `[Sharko Security] <summary>`.
- Include affected version(s), reproduction, and intended
  disclosure timeline.
- PGP key available on request.

### Coordinated disclosure

Sharko follows responsible-disclosure coordination — fix released
first, advisory and credit posted on agreement with the reporter.
The maintainer aims for a disclosure window that gives operators
time to upgrade (typically 14 days post-release for a HIGH; sooner
for active exploitation).

### Future evolution (post-CNCF-sandbox)

The transition plan documented in `SECURITY.md`:

- Disclosure channel consolidates on GitHub's private vulnerability
  reporting (Security Advisories) on the repository.
- GitHub Security Advisory CVE flow becomes the canonical
  advisory channel.
- CNA assignment via foundation route.
- Multi-maintainer rotation reduces solo single-point-of-failure on
  triage.
- Formal SLOs become commitments (currently best-effort).

The maintainer's intent is to make this transition as part of the
post-v2.0.0 CNCF-sandbox application process, not as a
prerequisite. v2.0.0 ships with the best-effort solo baseline; the
transition follows community acceptance.

---

## 13. References

### Frameworks cited

- **STRIDE** — Microsoft canonical six (Spoofing, Tampering,
  Repudiation, Information disclosure, Denial of service,
  Elevation of privilege). CNCF Security TAG's variant adds
  "lateral movement" as a sub-class of EoP; this document treats
  it as part of EoP.
- **OWASP Top 10 (2021)** — A01–A10. The 2025 update was
  considered but the sprint plan locked OQ #4 to 2021 because
  that is the baseline most security consultants currently target.
- **SLSA v1.0** — Build tracks L1–L4. Sharko targets L2 with a
  documented L3 path.
- **CNCF Security TAG threat-model checklist** — used as the
  structural template for this document.
- **NIST SP 800-218 (SSDF)** — Secure Software Development
  Framework; informally referenced as a CNCF-graduation maturity
  signal. Sharko does not currently claim formal SSDF compliance.

### Internal cross-references

- [`SECURITY.md`](../../SECURITY.md) — disclosure policy + reporting channel.
- [`.claude/team/security-auditor.md`](../../.claude/team/security-auditor.md) — security-auditor checklist (PRIMARY framing reference for this document).
- [`docs/site/operator/security.md`](../site/operator/security.md) — operator-facing security reference.
- [`docs/site/operator/supply-chain.md`](../site/operator/supply-chain.md) — image / chart / binary verification.
- [`docs/site/operator/catalog-trust-policy.md`](../site/operator/catalog-trust-policy.md) — catalog signing trust policy.
- [`docs/site/operator/failure-mode-index.md`](../site/operator/failure-mode-index.md) — 57 failure modes + STRIDE cross-link targets.
- [`docs/site/developer-guide/api-stability.md`](../site/developer-guide/api-stability.md) — 128 endpoint tier inventory.
- [`docs/site/developer-guide/logging.md`](../site/developer-guide/logging.md) — slog discipline + RedactHandler architecture.
- [`docs/site/developer-guide/logging-audit-punchlist.md`](../site/developer-guide/logging-audit-punchlist.md) — remaining call-site cleanup (partial-mitigation source).
- [`docs/site/operator/auth-bypass.md`](../site/operator/auth-bypass.md), [`credential-leak-in-logs.md`](../site/operator/credential-leak-in-logs.md), [`secret-push-silently-failed.md`](../site/operator/secret-push-silently-failed.md), [`secrets-provider-unreachable.md`](../site/operator/secrets-provider-unreachable.md) — P0 security runbooks.
- [`docs/site/community/roadmap.md`](../site/community/roadmap.md) — v3+ gap tracking.
- [`docs/site/release-notes.md`](../site/release-notes.md) — V2 shipped artifacts canonical list.

### Related Sharko design docs

- [`docs/design/2026-04-16-attribution-and-permissions-model.md`](2026-04-16-attribution-and-permissions-model.md) — Tiered Git attribution + V2.x scoped RBAC roadmap.
- [`docs/design/2026-04-20-v1.23-catalog-extensibility.md`](2026-04-20-v1.23-catalog-extensibility.md) — Catalog signing design.
- [`docs/design/2026-05-11-cluster-secret-reconciler-and-gitops-stance.md`](2026-05-11-cluster-secret-reconciler-and-gitops-stance.md) — V125-1-8 reconciler + ownership-label design.
- [`docs/design/2026-05-12-v125-architectural-todos.md`](2026-05-12-v125-architectural-todos.md) — V125-1-9 schema envelope rationale.

### External references

- ArgoCD upstream security policy: <https://github.com/argoproj/argo-cd/blob/master/SECURITY.md>
- Sigstore documentation: <https://docs.sigstore.dev/>
- cosign repository: <https://github.com/sigstore/cosign>
- Go module proxy + checksum DB: <https://go.dev/ref/mod#module-proxy>
- Kubernetes Pod Security Standards: <https://kubernetes.io/docs/concepts/security/pod-security-standards/>
- OWASP Top 10 (2021): <https://owasp.org/Top10/>
- SLSA v1.0 specification: <https://slsa.dev/spec/v1.0/>
- CNCF Security TAG: <https://github.com/cncf/tag-security>

---

*This threat model is the v2.0.0 production-launch baseline. It is
expected to be revisited at every major release and whenever a new
trust boundary or asset is introduced. The companion review-prep
bundle at `.bmad/output/reviews/v2-security-review-prep.md`
scopes 3rd-party engagement against this baseline.*
