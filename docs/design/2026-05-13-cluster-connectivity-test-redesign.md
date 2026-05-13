# Cluster Connectivity Test Redesign

> **Status:** APPROVED 2026-05-13. Maintainer signoff captured against `.bmad/output/brainstorming/2026-05-13-cluster-test-redesign.md`. This doc supersedes that brainstorm — the thread-level recommendations there are retained for historical context only.
>
> **Date:** 2026-05-13
>
> **Authors:** Moran Weissman (maintainer); tech-lead (Claude).
>
> **Supersedes:** `.bmad/output/brainstorming/2026-05-13-cluster-test-redesign.md`
>
> **Related docs:**
> - `docs/design/2026-05-12-v125-architectural-todos.md` — V125 architectural roadmap (schema envelope, reconciler, ownership label).
> - `docs/design/2026-05-11-cluster-secret-reconciler-and-gitops-stance.md` — why Sharko owns its solution end-to-end and why `argocdProvider` is the right credentials surface for the Test path.
> - `.bmad/output/planning-artifacts/epics-v125-1-9.md` — V125-1-9 schema envelope plan (the prerequisite for V125-1-8 reconciler this work depends on).

---

## 1. Problem

Today's "Test cluster" feature requires a separate **secrets backend** (Vault / AWS-SM / file-store) on the active connection to fetch cluster credentials. On dev installs (kind/minikube) there is no such backend → the feature is permanently unavailable, users hit a confusing dead end. The deeper insight is that the ArgoCD cluster Secret Sharko already owns *contains the same connection credentials Test needs* — the "secrets backend" requirement for non-cloud K8s clusters is artificial. After V125-1-8 (Sharko owns the cluster Secret end-to-end via the reconciler + `app.kubernetes.io/managed-by: sharko` label), the cleanest way to power Test is to read straight from that Secret, with auth-method routing driven by what's actually inside it — no stored type field, no extra credentials-provider config.

---

## 2. Approach

Four locked positions, each addressing one layer of the redesign. They compose into a single coherent path: introspect the Secret to pick the auth method, render a hostname-derived badge for human recognition, and ship `argocdProvider` after V125-1-8 so ownership semantics are clean.

### 2.1 No stored cluster-type label — runtime introspection only

**Decision:** Drop the proposed `provider_type` field from `managed-clusters.yaml`. No stored type label of any kind.

**Rationale:** Adding a typed field would force every API caller, every CLI subcommand, every future CRD schema (V3+) to provide and validate it. Wizard, `sharko add-cluster`, batch register, adopt, future operator-mode CRs — all carry the cost. The field also drifts from reality (someone retypes the cluster, the label is wrong forever) and creates a silent divergence between "what we labeled it" and "what its credentials actually look like." Runtime introspection is one source of truth: the ArgoCD cluster Secret. If we need to know how to talk to the cluster, we look at how it's already configured to be talked to.

### 2.2 Auth-method detection from ArgoCD Secret structure

**Decision:** Test routes per the structure of the ArgoCD cluster Secret's `config` JSON blob, detected at runtime each time Test runs.

ArgoCD's cluster Secret `config` field is a JSON document with one of three shapes — `bearerToken`, `awsAuthConfig`, or `execProviderConfig`. Sharko inspects the parsed config and dispatches:

| Detected field | Path taken | UX surface |
|---|---|---|
| `bearerToken` + `caData`/`insecure` | Ready-to-use — issue token-authenticated request to cluster API server | Test runs to completion (kind, minikube, EC2-direct, static-token EKS) |
| `awsAuthConfig` | IAM mode — needs cloud creds to mint a token | Specific actionable error: "This cluster uses AWS IAM authentication. Configure AWS credentials for the Sharko pod's role to enable Test" — point to the IAM setup runbook |
| `execProviderConfig` | Exec plugin mode — runs an out-of-process binary to mint creds | Specific actionable error: "This cluster uses exec-plugin auth (e.g., aws-iam-authenticator). Exec plugins are not supported in v1.x. Tracked for v2." |

The Secret-shape detection lives in a small helper next to the Test handler — no new package, no external API change. Errors are returned with stable identifier codes so the UI can render type-specific copy + a deep link to the matching docs page (rather than a generic "Test unavailable" toast).

**Why this is safe:** post-V125-1-8 every Sharko-managed cluster Secret is owned by Sharko (label-gated), so reading its `config` is reading our own state. Non-Sharko Secrets are invisible to this surface (decision C from the gitops-stance doc).

### 2.3 UI type badge — hostname → pill at render time

**Decision:** Ship a cosmetic "type pill" badge in v1.25 alongside the redesign. Derived purely from the cluster's API server hostname at render time. Never stored.

| Hostname pattern | Pill | Color |
|---|---|---|
| `*.eks.amazonaws.com` | `EKS` | orange |
| `*.azmk8s.io` | `AKS` | blue |
| `*.gke.io` / `*.googleapis.com` | `GKE` | red |
| `kind-*` / `localhost` / `127.0.0.1` | `kind` | gray |
| `*.minikube.io` | `minikube` | gray |
| anything else | `Self-hosted` (or omit) | gray |

The pill is a small React component that takes the `server` URL string and returns a styled span. No backend involvement, no data model change. If the heuristic gets it wrong (e.g., an EKS cluster behind a custom DNS name shows as `Self-hosted`), the cost is purely cosmetic — the Test path still works correctly because it routes off Secret structure (§2.2), not the badge.

**Limitation accepted:** custom-DNS EKS / private-link clusters will misidentify. This is fine because the badge is recognition-aid, not behavior-driver. If the misidentification ever becomes a real complaint we can add an explicit `displayType` override field — but per the principle in §2.1, we don't add it preemptively.

### 2.4 `argocdProvider` as a new built-in `CredentialsProvider`

**Decision:** Add `argocdProvider` as a new built-in implementation of the existing `CredentialsProvider` interface. It reads cluster credentials from the ArgoCD cluster Secret in the `argocd` namespace (the same Secret Sharko's reconciler maintains after V125-1-8). Becomes the **default** provider for non-cloud auth methods (`bearerToken` shape from §2.2). Ships **after V125-1-8** so ownership semantics are clean — Sharko reads back state Sharko itself wrote, no read-from-someone-else's-Secret ambiguity.

**Why this slots cleanly in:**

- The `CredentialsProvider` interface already exists for vault / AWS-SM / file-store. `argocdProvider` is one more implementation, no architectural shift.
- Sharko already has RBAC to read the `argocd` ns Secret (gets it as part of V125-1-8).
- Test no longer requires a separate secrets backend on the active connection — the connection's "credentials store" defaults to "the ArgoCD cluster Secret you already wrote." Dev installs (kind/minikube) just work with no extra config.
- For cloud-auth clusters (`awsAuthConfig` / `execProviderConfig` shapes), `argocdProvider` returns the same actionable error as §2.2 routing — those paths still need their type-specific provider work, scoped out of v1.x.

**Why after V125-1-8 specifically:** before V125-1-8, ArgoCD cluster Secrets were created via the orchestrator's pre-merge code paths (decisions varied by provider). The "Sharko owns this Secret" assertion is post-V125-1-8 only. Reading-from-Secret semantics are clean iff we wrote it; otherwise we'd be effectively adopting whatever's there, which decision C in the gitops-stance doc explicitly forbids.

---

## 3. Out of scope

- **EKS / AKS / GKE token-refresh flows** — separate brainstorm. The detection here surfaces actionable errors for those paths but does not implement the cloud-creds plumbing.
- **BUG-040, BUG-042** — separate UI work in their own bundles. Mentioned only because they were on the same maintainer-pushback that prompted this brainstorm.
- **Storing labels of any kind for cluster type** — explicitly rejected (§2.1). No `provider_type`, no `displayType`, no `clusterFlavor`. If a future need surfaces, that's a separate decision.
- **Migrating existing pre-V125-1-8 Sharko-created Secrets to be `argocdProvider`-readable** — covered by V125-1-8's one-shot label migration; not separately scoped here.
- **Replacing vault/AWS-SM providers** — they stay. `argocdProvider` is additive, becomes the default for non-cloud, but is not the only built-in.

---

## 4. Sequence and hard dependencies

```
V125-1-9 (schema envelope)  →  V125-1-8 (reconciler + ownership label)  →  V125-1-7 tightening (label-aware orphan filter)  →  V125-1-10 (this work)
```

- **V125-1-9 → V125-1-8:** the reconciler must read against a stable validated contract; bad YAML = silent reconcile failures (per gitops-stance §11). Already the locked V125 sequence.
- **V125-1-8 → V125-1-7 tightening:** the orphan filter can only tighten to "Sharko-labeled only" once V125-1-8 has placed the ownership label.
- **V125-1-7 tightening → V125-1-10:** `argocdProvider` reads from Secrets whose lifecycle is now fully under Sharko's reconciler — no race against orphan cleanup, no risk of reading half-managed state.

V125-1-10 is the new epic ID for this work (Test redesign + `argocdProvider` + UI badge bundled). To be planned via BMAD after V125-1-7 tightening lands.

---

## 5. Open questions for V125-1-10 implementation phase

These are not blocking decisions — they are the things the V125-1-10 dev agent will need to choose during implementation. Captured here so the planning agent later doesn't have to re-discover them.

1. **Where `argocdProvider` lives in the codebase.** Probably `internal/providers/argocd_provider.go` next to the existing AWS-SM / file-store provider implementations. Confirm during dispatch — agent picks placement that matches existing convention.
2. **How the UI badge component is exposed.** Standalone `<ClusterTypeBadge server={s} />` component? Inline helper inside the existing `ClusterCard` / `ClusterRow` views? Probably standalone for reuse on the detail page + clusters list. Agent decides.
3. **Exact error-message copy** for the three Test detection branches (§2.2). Should be drafted alongside the docs-writer agent so the wording matches the existing tone of operator docs and points at the correct runbook URLs (which may not exist yet — the IAM-mode path probably needs a new runbook page).
4. **Whether the bearer-token Test path also re-runs Secret-CRUD verification** (Stage 1 of today's flow), or just hits the cluster API for `/version`. Today's Test does CRUD; we may want to keep it for parity. Trade-off: faster vs. more thorough.
5. **Test result caching strategy.** Today every Test click is a fresh round-trip. Cheap for a few clusters, costly for fleets. May want a 30s-debounce or a "last tested at" surface. Defer to V125-1-10 unless real complaints arrive.

---

## 6. References

- Brainstorm (now superseded, locked status block at top): `.bmad/output/brainstorming/2026-05-13-cluster-test-redesign.md`
- V125-1-9 schema envelope plan: `.bmad/output/planning-artifacts/epics-v125-1-9.md`
- V125 architectural TODOs (V125-1-9 / V125-1-8 / V125-1-7 tightening / V125-2 cleanup): `docs/design/2026-05-12-v125-architectural-todos.md`
- Cluster reconciler + GitOps stance (architectural source-of-truth for ownership label, decision C "Sharko owns its solution," `argocdProvider` rationale): `docs/design/2026-05-11-cluster-secret-reconciler-and-gitops-stance.md`
- Section 3 design (the existing pre-merge "PR Is the Gate" stance V125-1-8 supersedes): `docs/design/section-3-template-and-secrets.md`
