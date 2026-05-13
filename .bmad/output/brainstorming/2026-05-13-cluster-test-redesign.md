# Brainstorm — Cluster connectivity test redesign

**Date:** 2026-05-13
**Maintainer:** Moran Weissman
**Triggered by:** Live debugging of "Test cluster" UX on kind dev install
**Out of scope:** EKS/AKS/GKE IAM/OIDC token flow (deferred — "different story"); BUG-040/042 (separate UI work)

## Status: LOCKED 2026-05-13

Maintainer Moran Weissman signed off on all four positions below; this brainstorm is now closed and superseded by `docs/design/2026-05-13-cluster-connectivity-test-redesign.md`.

1. ❌ Stored `provider_type` label **DROPPED** — would force every API/CLI/CRD caller to provide a type field, too dirty.
2. ✅ Test path = **runtime introspection** of ArgoCD cluster Secret structure. Routing:
   - `bearerToken` + `caData/insecure` → ready-to-use (kind / minikube / EC2-direct / static-token EKS)
   - `awsAuthConfig` → IAM mode → needs cloud creds → surface specific actionable error
   - `execProviderConfig` → exec plugin → not supported in v1.x → surface specific error
3. ✅ **UI type badge SHIP in v1.25** — hostname-derived at render time, never stored. Mapping:
   - `*.eks.amazonaws.com` → orange "EKS" pill
   - `*.azmk8s.io` → blue "AKS" pill
   - `*.gke.io` / `*.googleapis.com` → red "GKE" pill
   - `kind-*` / `localhost` / `127.0.0.1` → gray "kind" pill
   - `*.minikube.io` → gray "minikube" pill
   - other → gray "Self-hosted" (or omit)
4. ✅ `argocdProvider` (new built-in `CredentialsProvider`) ships **AFTER V125-1-8** — cleanest semantics, Sharko fully owns the secret by then.

**Sequence:** V125-1-9 (schema envelope) → V125-1-8 (cluster reconciler + ownership label) → V125-1-7 tightening (label-aware orphan filter) → V125-1-10 (`argocdProvider` + per-type test routing + UI badge).

The thread-level recommendations below stand for historical context. The locked decisions above supersede them.

## Problem

Today's "Test cluster" feature requires a separate **secrets backend** (Vault / AWS-SM / file-store) on the active connection to fetch cluster credentials. On dev installs (kind/minikube) there's no such backend → feature is permanently unavailable, users are confused. But the ArgoCD cluster Secret Sharko already owns *contains the same connection credentials* — so the "secrets backend" requirement is artificial for non-cloud K8s. Three coupled threads to redesign: (1) label clusters by type, (2) surface why Test needs what it needs, (3) reuse the ArgoCD secret as a built-in credentials source.

---

## Thread 1 — Cluster type labeling (`provider_type`)

**Options:**
- A. Auto-detect at registration via API server hostname pattern (`*.eks.amazonaws.com` → eks; `*.azmk8s.io` → aks; `*.gke.io` → gke; localhost/172.* → kind/minikube; else `self-hosted`)
- B. User picks from dropdown at registration; auto-suggest from hostname
- C. Skip for v1; add later if needed

**Recommendation: A + manual override.** Auto-detect from hostname covers 95% of cases with zero clicks. User can override if wrong. Field stored in `managed-clusters.yaml` (lands as part of V125-1-9 envelope, not a separate migration). Existing clusters: backfill on first reconciler pass — auto-detect, write back via PR like any other change.

**Why:** Type labeling is foundational — Threads 2+3 both need it for routing logic. Hostname detection is deterministic for the cloud cases that matter; `kind` self-identifies in kubeconfig context name (`kind-*`). Drives UI rendering too (e.g., AWS-only actions only show for `eks`).

---

## Thread 2 — Test pre-requisites UX

**Options:**
- A. Hide Test button when no backend (BUG-041, already filed — **OBSOLETED by Thread 3**)
- B. Disabled-with-tooltip explaining why + Settings link
- C. Show Test as enabled, route per cluster type (Thread 3 path); only error when actually unsupported (e.g., EKS without IAM)

**Recommendation: C.** With Thread 3, Test works out-of-the-box for `kind`/`minikube`/`self-hosted`/`ec2-direct`. Only `eks`/`aks`/`gke` clusters surface the "needs IAM/OIDC creds" message — and that message points to a *cluster-type-specific* Settings page (e.g., "Configure AWS IAM role for cluster `prod-eu-eks`") rather than a generic "set up secrets backend".

**Bonus:** Mention the security model (no creds in git) ONCE in the init wizard's connection-setup step, not on every Test click. Docs page at `docs/site/operator/cluster-connectivity-model.md` for the deep "why".

---

## Thread 3 — Reuse the ArgoCD cluster Secret (the big one)

**Options:**
- A. New `argocdProvider` implementing the `CredentialsProvider` interface; reads K8s API creds from `argocd` namespace cluster-secrets. Default for non-cloud cluster types.
- B. Same, but make it the ONLY built-in provider; relegate Vault/AWS-SM to "external/optional" plugins
- C. Status quo + better docs explaining how to set up Vault for testing

**Recommendation: A.** Implements the maintainer's insight cleanly. Sharko already has read access to `argocd` namespace (V125-1-8 makes it the OWNER), so this is reading state Sharko controls — no new attack surface, no new permission grant. Per-type routing:

| Cluster type | Provider used by Test | Notes |
|---|---|---|
| `kind` / `minikube` | `argocdProvider` | static bearer in ArgoCD secret → works directly |
| `self-hosted` / `ec2-direct` | `argocdProvider` | same |
| `eks` (static token) | `argocdProvider` | works until token expires; refresh via cloud auth (out of scope) |
| `eks` (aws-iam-authenticator) | `awsIamProvider` (future) | needs cloud creds — separate brainstorm |
| `aks` / `gke` | analogous future providers | separate brainstorm |

**Conceptually clean:** doesn't muddy the "no kubeconfigs in git" story — Sharko reads from ArgoCD's secret store (cluster-local), not from git or its own duplicate store. `argocd` is the SoR for cluster credentials; Sharko writes them there (V125-1-8) and reads them back for Test (this work).

**Boundary with V125-1-8:** strictly improves the V125-1-8 story — single source of truth (ArgoCD secret) for both reconciliation and connectivity testing. No circular concern.

---

## Cross-cutting concerns

1. **Thread 3 hard-depends on Thread 1.** Can't route per-type without the type. Land Thread 1 first (or together).
2. **Thread 1 hard-depends on V125-1-9** (schema envelope) for clean field addition to `managed-clusters.yaml`. Sequence: V125-1-9 → cluster-type labeling → ArgoCD-as-creds-provider.
3. **Thread 3 obsoletes BUG-041** (hide Test button) for non-cloud clusters. Update BUG-041 to "hide Test button only for cloud cluster types missing their auth config."
4. **Thread 2 docs work** can ship in parallel — no code dependency.
5. **EKS/AKS/GKE token-refresh flow** is its own brainstorm. This work makes it cleaner to add (per-type provider plugin pattern), but doesn't ship it.

---

## Decision asks for maintainer

1. ✅ Confirm Thread 1 default = **auto-detect from hostname + manual override**?
2. ✅ Confirm Thread 3 path = **`argocdProvider` as new built-in, default for non-cloud types**?
3. ⚠️ Sequence: ship cluster-type labeling alongside V125-1-9 (extends scope by ~1 story), or as a separate post-V125-1-9 mini-epic? Recommend **bundle with V125-1-9** — the field belongs in the envelope, splitting causes a second migration.
4. ⚠️ V125-1-8 dependency: should ArgoCD-as-creds-provider land **after** V125-1-8 (cleaner semantics — Sharko fully owns the secret) or **before** (decoupled — read-only of whatever's in ArgoCD)? Recommend **after** for cleanliness.

---

## Recommended next step

1. Maintainer confirms decisions 1–4 above
2. Promote this brainstorm into a proper design doc: `docs/design/2026-05-13-cluster-connectivity-test-redesign.md` (~3-4 hr)
3. Update `epics-v125-1-9.md` to add the `provider_type` field as a 7th story
4. After V125-1-9 + V125-1-8 ship → BMAD-plan a "V125-1-10: ArgoCD-as-credentials-provider + per-type test routing" sprint
5. Defer EKS/AKS/GKE token-refresh brainstorm until V125 architectural work clears
