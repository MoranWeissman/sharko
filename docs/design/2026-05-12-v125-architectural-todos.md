# V125 Architectural TODOs — Cluster-Secret Reconciler, Schema, GitOps Stance

> **Status:** Working TODO synthesizing the architectural conversation captured in `2026-05-11-cluster-secret-reconciler-and-gitops-stance.md`. Decisions converged across multiple sessions; this file is the action-ready summary.
>
> **Author:** Moran Weissman + tech-lead synthesis.
>
> **Use:** This file is intentionally action-oriented. For the *why* behind each decision, see the linked design doc. For the *what to do next*, read straight through.

---

## 1. Context — what triggered this work

Track B testing on `dev/v1.24-cleanup` surfaced an orphan-cluster bug:

> "I closed the PR without approving. Sharko still shows all the data about this new 'not in git' and not yet 'added' to argocd cluster."

Diagnosis: `internal/orchestrator/cluster.go:408` creates the ArgoCD cluster Secret directly via the ArgoCD API **before** the registration PR opens (when `argoSecretManager` is nil — i.e., kubeconfig provider). PR close → orphan Secret in argocd ns. V125-1-7 added a recovery surface; deeper architectural questions emerged from the discussion.

Conversation arc: the orphan symptom → ownership semantics → GitOps compliance → Sharko's reason to exist → operator vs goroutine reconciler → schema concerns → the converged plan below.

---

## 2. Decisions made — committed for V125+

### A. Sharko stays as a platform-engineering control plane (Answer 1)

Not pivoting to Backstage-style portal, not pivoting to operator-only kubectl interface. **Sharko continues to be a UI/CLI/HTTP-API control plane that authors PRs and orchestrates ArgoCD.** Existential question resolved: "Sharko remains as it is."

Implications:
- The two-tools tax (Sharko + ArgoCD) is accepted; mitigated by clean scope boundaries.
- "Magic YAML" mitigation comes from schema work (V125-1-9), not from CRDs.
- Operator mode (CRDs + controller-runtime) is **deferred to V3+**.

### B. No external runtime tool dependencies

**Hard rule, settled:** Sharko V2.0.0 will not require SealedSecrets, SOPS, ESO, the K8s Secrets Store CSI Driver, or any other secret-injection infrastructure beyond what comes with ArgoCD itself.

This forecloses several otherwise-attractive architectures. The chosen path works around this by keeping Sharko as the secret gateway (see decision D below).

### C. Sharko owns its solution end-to-end; no adoption of externally-created clusters

Hard rule, settled. Verbatim:

> "If a Secret suddenly appears in the cluster, it was deployed somehow — manually or GitOps — who knows? From that moment on we can't really adopt it and alter the secret from Sharko side because it may reconcile back to desired state. Maybe! Not for sure. Perhaps we should not allow adopting at all."

Then:

> "I think it's OK if Sharko is the owner of this solution and is not supporting clusters added outside of Sharko."

**The principle:** Sharko sees only what Sharko created. Anything in argocd ns without the `app.kubernetes.io/managed-by: sharko` label is invisible to Sharko's UI, untouched by Sharko's reconciler, out of scope for any Sharko operation.

Implications:
- **V125-2 (Adopt UI / Discovered Clusters surface) is dropped from the V125 plan.** Backlog entry can be removed/closed.
- **V2 Epic 4 backend** (`internal/orchestrator/adopt.go`) becomes dead code. Remove in a separate cleanup commit when convenient.
- Operators with pre-existing ArgoCD clusters wanting to migrate must `kubectl delete` the old Secret and re-register via Sharko. One-time per cluster.
- No "Discovered Clusters" or "Name Conflicts" surfaces are needed; refusal-on-conflict is a registration-time toast/error.

### D. Sharko remains the secret-creation gateway

Sharko's pod fetches credentials from vault and writes the ArgoCD cluster Secret directly via the K8s API. This is the only viable option given decisions B + C without going operator-mode.

| Option | Verdict |
|---|---|
| Sharko Pod calls K8s API directly with vault-fetched creds | ✅ **Chosen** |
| Sharko Pod calls ArgoCD HTTP API (today's code path) | ⚠ Works but ArgoCD-API-dependent; switch to direct K8s API |
| Plain Secret in git | ❌ Creds-in-git rejected |
| External tool (SealedSecrets/ESO/etc.) | ❌ Decision B |
| CRD + controller (operator) | ❌ Decision E |
| Job-scheduled creation | ❌ Brittle, same access requirements as Sharko Pod |

### E. Operator mode is deferred (NOT for V2.0.0)

Discussed in detail; the extra layer (CR between git and Secret) is overhead without value-add for Sharko's expected user (who lives in Sharko UI + git, not kubectl). Costs:
- 6-9 weeks of work
- Helm install becomes more complex (CRDs, ClusterRole, optional webhook)
- CRD lifecycle (versioning, conversion webhooks)
- Slower git→Secret latency (ArgoCD reconcile interval becomes the bottleneck)

Benefits exist (kubectl-native, owner-ref GC, schema validation at apply time, external integration) but don't pay off for the current user persona.

**Revisit when:**
- Managing thousands of clusters (not tens)
- Other tools need to declare clusters via `kubectl apply`
- HA active-active reconcilers required
- "Sharko down → nothing reconciles" actually hits in production
- CNCF Sandbox/Incubation pushes toward operator pattern

### F. ApplicationSet pattern stays as-is (Cluster generator with label selector)

Decided not to switch to Plugin/Git/List generators. The Cluster-generator-with-labels pattern is the most idiomatic ArgoCD path. Sharko's reconciler maintains labels on Sharko-owned Secrets — that's part of "Sharko owns the Secret end-to-end."

Implication: the "decoupled labels" architectures (B, C-1, C-2 from the design doc) are NOT being pursued. They were valuable to articulate but the trade-offs (Sharko-as-runtime-AppSet-dependency, generated files, file proliferation) didn't earn their keep.

### G. Schema envelope for managed-clusters.yaml + addons-catalog.yaml

Adopt apiVersion/kind/spec envelope + published JSON Schema. Not a CRD (no operator), but CRD-shaped so future operator-mode graduation is mechanical, not architectural.

Format example:
```yaml
# yaml-language-server: $schema=https://sharko.io/schemas/managed-clusters.v1.json
apiVersion: sharko.io/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: prod-eu
      server: https://prod-eu-api.example.com
      secretPath: clusters/prod-eu
      labels: { team: platform }
      addons: { cert-manager: true, datadog: false }
```

Single-doc-with-array (DRY). NOT multi-doc YAML (would repeat apiVersion/kind 50 times for 50 clusters).

### H. Filename: `managed-clusters.yaml` is correct

Drop the legacy `cluster-addons.yaml` alias when convenient. The current name correctly describes the resource (registry of managed clusters), not the relation (addons attached to clusters).

---

## 3. V125 work plan — ordered

Sequence matters. Each story builds on the prior:

```
V125-1-9 (schema envelope + validation)
    ↓
V125-1-8 (reconciler + ownership label + retire pre-merge orchestrator paths)
    ↓
V125-1-7 tightening (label-aware orphan filter)
    ↓
V125-2 cleanup (delete adopt.go dead code; close backlog entry)
```

Why this order:
- V125-1-9 first: V125-1-8's reconciler MUST read against a stable, validated contract; bad YAML → silent reconcile failures. The schema work makes the reconciler operationally safe.
- V125-1-8 second: the core architectural change. Ownership label introduced here.
- V125-1-7 tightening third: now that ownership label exists, V125-1-7 can refuse to delete unlabeled Secrets.
- V125-2 cleanup last: dead-code removal once the reconciler proves out.

---

## 4. V125-1-9 — Schema envelope + JSON Schema

### Goal
Make `managed-clusters.yaml` and `addons-catalog.yaml` self-describing, schema-validated, editor-friendly. Bridge to operator mode in V3+.

### Scope
1. **Adopt envelope** in both files:
   ```yaml
   apiVersion: sharko.io/v1
   kind: ManagedClusters    # or AddonCatalog
   metadata:
     name: managed-clusters
   spec:
     ...
   ```
2. **Generate JSON Schema** from Go struct definitions:
   - `cmd/schema-gen/main.go` (new) emits `docs/schemas/managed-clusters.v1.json` and `docs/schemas/addon-catalog.v1.json`
   - Hosted at a stable URL (publish via the docs site or as a release artifact)
3. **Header in every Sharko-written YAML**:
   ```yaml
   # yaml-language-server: $schema=https://sharko.io/schemas/managed-clusters.v1.json
   ```
4. **Validation on PR** — Sharko's CLI or CI hook runs validation on proposed YAML changes. Reject malformed YAML before merge.
5. **Validation on read** — reconciler validates loaded YAML; rejects malformed file with audit-logged error rather than silent reconcile failure.
6. **Migration**:
   - Reader supports both old (no envelope) and new (with envelope) formats during transition
   - On first write after upgrade, Sharko writes the envelope shape
   - Eventually deprecate the legacy reader (V126+)
7. **Same treatment for `addons-catalog.yaml`** (rename to `addon-catalog.yaml` singular while we're at it; keep alias for back-compat)

### Files
- `cmd/schema-gen/main.go` (new)
- `docs/schemas/managed-clusters.v1.json` (generated, committed)
- `docs/schemas/addon-catalog.v1.json` (generated, committed)
- `internal/models/cluster.go` — add envelope-aware parsing
- `internal/catalog/loader.go` — add envelope-aware parsing
- `templates/bootstrap/configuration/managed-clusters.yaml` — update template with envelope + schema header
- `templates/bootstrap/configuration/addons-catalog.yaml` — same
- `internal/demo/mock_git.go` — update demo seed to use envelope shape
- CLI: `sharko validate-config <file>` (new) — exit 0/1 based on schema validation
- CI/PR hook: integrate validation into the existing PR workflow

### Out of scope
- CRD installation (that's operator mode)
- Server-side validation webhook (operator mode)
- Multi-version schema migration framework (just have v1 for now)

---

## 5. V125-1-8 — Reconciler + ownership label + retire pre-merge paths

### Goal
Replace the orchestrator's pre-merge ArgoCD API calls with a goroutine-based reconciler that converges ArgoCD cluster Secret state to managed-clusters.yaml. Establish ownership semantics via labels.

### Scope

#### 5.1 — New package: `internal/clusterreconciler`

~200 LoC mirroring `internal/prtracker/tracker.go`'s structure:

```go
type Reconciler struct {
    cmStore      cmstore.Store
    gitProvider  func() GitProvider
    argocdClient func() ArgocdWriter   // K8s-API-based, not ArgoCD-HTTP-API-based
    credProvider providers.CredentialsProvider
    auditFn      func(audit.Entry)
    triggerCh    chan struct{}
}

func (r *Reconciler) Start(ctx context.Context)
func (r *Reconciler) Trigger()
func (r *Reconciler) ReconcileOnce(ctx context.Context) error
```

Reconcile loop (per tick, every 30s + on Trigger):

```
1. Fetch managed-clusters.yaml from git via REST API at HEAD of BaseBranch
2. Parse → desired = { name → { server, secretPath, addons } }
3. List Secrets in argocd ns matching:
     argocd.argoproj.io/secret-type = cluster
     app.kubernetes.io/managed-by  = sharko
   → existing = { name → { current_labels, ... } }
4. For each name in desired:
     a. If not in existing → fetch creds → CREATE Secret with addon labels + ownership label
     b. If in existing AND labels differ → PATCH labels (creds untouched)
     c. If in existing AND labels match → no-op
5. For each name in existing but not in desired → DELETE Secret
6. Audit-log every action; surface per-cluster failures in CR status equivalent
   (via /api/v1/clusters response — extend with reconciler status field)
```

#### 5.2 — Direct K8s API for Secret writes

Replace `internal/argocd/client_write.go::RegisterCluster` (HTTP API) with direct client-go calls:

```go
// internal/argocd/cluster_secrets.go (new)
func CreateClusterSecret(ctx context.Context, k8s kubernetes.Interface, spec ClusterSpec, kc *Kubeconfig) error
func PatchClusterSecretLabels(ctx context.Context, k8s kubernetes.Interface, name string, labels map[string]string) error
func DeleteClusterSecret(ctx context.Context, k8s kubernetes.Interface, name string) error
```

The Secret format (must match ArgoCD's contract):
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: cluster-<name>
  namespace: argocd
  labels:
    argocd.argoproj.io/secret-type: cluster
    app.kubernetes.io/managed-by: sharko        # NEW — ownership marker
    <addon labels per managed-clusters.yaml>
type: Opaque
stringData:
  name: <cluster_name>
  server: <server_url>
  config: |
    {
      "bearerToken": "<vault-fetched>",
      "tlsClientConfig": { "insecure": false, "caData": "<vault-fetched>" }
    }
```

#### 5.3 — RBAC for argocd-ns Secret management

Add to Sharko's Helm chart:

```yaml
# templates/rbac-argocd.yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: sharko-cluster-secret-manager
  namespace: argocd
rules:
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: sharko-cluster-secret-manager
  namespace: argocd
subjects:
  - kind: ServiceAccount
    name: sharko
    namespace: sharko
roleRef:
  kind: Role
  name: sharko-cluster-secret-manager
  apiGroup: rbac.authorization.k8s.io
```

#### 5.4 — Wire reconciler into serve.go

```go
// cmd/sharko/serve.go (additions)
reconciler := clusterreconciler.New(...)
reconciler.Start(ctx)
prTracker.SetOnMergeFn(func(pr prtracker.PRInfo) {
    if pr.Operation == "register-cluster" || pr.Operation == "addon-enable" || ... {
        reconciler.Trigger()
    }
})
```

#### 5.5 — Strip the orchestrator's pre-merge paths

In `internal/orchestrator/cluster.go`:
- Delete the `if o.argoSecretManager != nil && o.gitops.PRAutoMerge` block (lines 229-262) — ArgoCD secret creation is now reconciler's job
- Delete the direct `ArgoCD API RegisterCluster` block (lines ~408+) — same reason
- The orchestrator's `RegisterCluster` becomes purely "open PR adding entry to managed-clusters.yaml"; that's it
- Same simplification for adopt.go, batch.go (adopt.go deleted entirely; see decision C)
- The `argoSecretManager` interface and its AWS-SM implementation likely become unused — remove

Estimated deletions: ~300-500 LoC. The orchestrator shrinks substantially.

#### 5.6 — Surface reconciler state in `/api/v1/clusters`

Per-cluster status fields:
```go
type ClusterStatus struct {
    LastReconciledAt time.Time
    ReconcileError   string  // empty on success
    SecretCreated    bool
    AddonLabelsApplied bool
}
```

Surface in the FE so users can see "this cluster's Secret was successfully reconciled at 14:23" or "creds fetch from vault failed; will retry."

#### 5.7 — Tests

- `internal/clusterreconciler/reconciler_test.go` — full reconcile-loop coverage:
  - Cluster in git, not in argocd → CREATE with creds + labels
  - Cluster in argocd (sharko-labeled), not in git → DELETE
  - Labels drift → PATCH labels only (creds preserved)
  - Vault fetch fails → log + skip + retry next tick
  - Git unreachable → log + skip + retry next tick
  - Secret in argocd WITHOUT sharko label → ignored (decision C)
- Integration test: end-to-end with a fake K8s API + mock git provider + mock vault

### Files
- `internal/clusterreconciler/` (new package, ~5-7 files)
- `internal/argocd/cluster_secrets.go` (new — direct K8s API helpers)
- `cmd/sharko/serve.go` (wire reconciler)
- `internal/orchestrator/cluster.go` (substantial deletions)
- `internal/orchestrator/adopt.go` (delete entirely)
- `internal/orchestrator/batch.go` (simplify)
- `internal/argocd/client_write.go` (delete RegisterCluster, related methods)
- `internal/providers/aws_sm.go` (still used for credential fetching — unchanged)
- `internal/api/clusters.go` (extend response with reconciler status fields)
- `templates/sharko/templates/rbac-argocd.yaml` (new Helm template)
- `internal/api/clusters_pending.go` (V125-1-5; logic stays)
- `ui/src/views/ClustersOverview.tsx` (surface reconciler status per cluster)

### Migration concerns
- **Existing Sharko-managed clusters:** on first reconcile after upgrade, Sharko detects Secrets without the new ownership label. Option: add a one-shot migration on startup that labels existing Secrets matching managed-clusters.yaml entries. Document carefully — this is a one-way migration.
- **In-flight registrations from old code:** PRs opened on old code that haven't merged yet. After upgrade, the old code path is gone; the reconciler picks them up after merge naturally. Should be fine but document.

### Out of scope for V125-1-8
- HA / leader election (single Sharko pod assumption)
- Webhook for sub-30s latency (periodic poll + onMergeFn is enough)
- Reconciler dashboard / metrics endpoint (nice-to-have; defer)

---

## 6. V125-1-7 — Orphan surface tightening (small follow-up to V125-1-8)

### Already shipped
Commit `08d9ec68` — orphan resolver + DELETE endpoint + UI section.

### Still needed (after V125-1-8)
1. **Tighten orphan definition:** an orphan is now a Secret that has the `app.kubernetes.io/managed-by: sharko` label AND is not in managed-clusters.yaml AND is not in pending_registrations. The resolver in `internal/api/clusters_orphans.go:resolveOrphanRegistrations` must add the label-presence check.
2. **DELETE handler safety check:** `handleDeleteOrphanCluster` must refuse to delete a Secret without the sharko label. Defense-in-depth even though the orphan resolver should never surface non-Sharko Secrets.
3. **Rename UI button** from "Delete cluster Secret" (technical, confusing per maintainer feedback) to **"Discard cancelled registration"** or similar plain language.
4. **Live-test bug:** the 500 error reported on `DELETE /api/v1/clusters/sharko-target-1/orphan` still needs root-cause diagnosis. Need server logs to pinpoint which step failed (`delete_orphan_cluster_list`, `delete_orphan_cluster_argocd_list`, or `delete_orphan_cluster_argocd_delete`). User to provide log line; bundle fix into V125-1-7.1.

### Files
- `internal/api/clusters_orphans.go` (label-presence filter)
- `internal/api/clusters_orphan_delete.go` (label-check safety)
- `ui/src/views/ClustersOverview.tsx` (button label)

### Out of scope
- Bulk cleanup
- Auto-cleanup (V125-1-8 makes orphans rare; manual cleanup is the safety net)

---

## 7. V125-2 cleanup — dead code removal

After V125-1-8 lands and is validated:

1. Delete `internal/orchestrator/adopt.go` (V2 Epic 4 backend)
2. Delete the related test file
3. Remove the `/api/v1/clusters/adopt` route registration from `internal/api/router.go`
4. Remove related types from `internal/api/clusters_write.go`
5. Update `.bmad/output/implementation-artifacts/sprint-status.yaml` — close V125-2 epic with note "Decision C: no adoption; backend removed"
6. Update any docs referencing the Adopt feature

### Files
- `internal/orchestrator/adopt.go` — DELETE
- `internal/orchestrator/adopt_test.go` — DELETE
- `internal/api/router.go` — remove adopt route
- `internal/api/clusters_write.go` — remove adopt request/response types
- Various docs

---

## 8. Documentation tasks

### 8.1 — Update the existing design doc
`docs/design/2026-05-11-cluster-secret-reconciler-and-gitops-stance.md` should be updated:
- Section 12 (Implications for V125+ stories) — finalize per this TODO file
- Add explicit "Decision Made" annotations to the architectural choices
- Note that operator mode is V3+ (deferred), and decoupled-labels architectures (B, C) are not pursued
- Reference this TODO file as the action-ready summary

### 8.2 — Stake-in-ground operator docs

Add to the user-facing operator install guide:

> **Sharko manages clusters it creates.** ArgoCD clusters that exist before Sharko is installed, or that are created outside of Sharko after installation, are not visible to Sharko and remain managed by whatever tool created them. To bring an existing cluster under Sharko management, you must `kubectl delete` the existing ArgoCD cluster Secret and register the cluster via Sharko.

### 8.3 — Architecture overview doc

`docs/site/architecture/cluster-registration.md` (new) should explain:
- The reconciler-from-git pattern
- The ownership label convention
- Why "Sharko owns its solution" is the design choice
- How users add/remove/toggle clusters
- The vault → Sharko → argocd ns flow
- The two failure modes (vault unreachable, git unreachable) and how they recover

### 8.4 — Schema reference docs

`docs/site/reference/managed-clusters-schema.md` (new) — full schema reference for the YAML, generated from the JSON Schema or hand-written, with examples.

### 8.5 — Migration guide

`docs/site/operator/migrating-from-pre-v2.md` (new, written when V2.0.0 release approaches) — step-by-step for operators upgrading from any v1.x preview to V2.0.0:
- Schema migration (envelope addition)
- Ownership label migration (one-shot at first reconcile)
- Removal of pre-merge ArgoCD API code paths
- What changes for the user

---

## 9. Open questions / explicit deferrals

These are intentionally NOT decided. Revisit when triggered:

| Question | Defer reason | Trigger to revisit |
|---|---|---|
| Webhook for sub-30s reconcile latency | Periodic poll is enough for current scale | Real-world poll latency causes UX complaints |
| HA Sharko (multi-replica + leader election) | Single-pod is fine for current scale | Real-world need for active-active reconcilers |
| Operator mode (CRDs + controller-runtime) | Costs > benefits for current user persona; ~6-9 weeks of work | (1) thousands-of-clusters scale, (2) external tools want kubectl-apply integration, (3) CNCF Sandbox push, (4) "Sharko down" pain hits in prod |
| Decoupled label management (Architectures B, C) | Doesn't earn its complexity at current scope | If operator mode happens, revisit alongside |
| Replacing legacy `cluster-addons.yaml` alias | Low value | When V125-1-9 ships and the new envelope is established |
| Cred mutation in `data` (rotation) detection | Reconciler currently only checks labels, not creds | If real-world cred-rotation drift becomes a problem |
| Per-cluster Application file split (Architecture C from design doc) | Single-file approach works | If 50+ cluster orgs hit merge-conflict pain |

---

## 10. Live-test items still pending

From V125-1-7 validation that was in flight when this design conversation began:

1. **The 500 error on DELETE orphan endpoint** — need server log line to diagnose. Maintainer to run:
   ```bash
   kubectl -n sharko logs deploy/sharko --tail=80 | grep -E "delete_orphan|orphan|sharko-target-1"
   ```
2. **UI label rework** — rename "Delete cluster Secret" → "Discard cancelled registration" (bundled with V125-1-7.1 fix for the 500)
3. **End-to-end orphan flow validation** — register kind cluster in manual mode, close PR without merging, verify orphan surfaces correctly, click "Discard", verify cleanup

---

## 11. Sequence of dispatches (when ready)

In dependency order:

1. **V125-1-7.1** — small fix bundle: 500 error on orphan delete + button rename. Independent of V125-1-9/8. Can ship anytime.
2. **V125-1-9** — schema envelope + JSON Schema + validation. ~3-5 days. Must land before V125-1-8.
3. **V125-1-8** — reconciler + ownership label + retire pre-merge paths. ~1 week (was previously estimated as ~3 days; the strip-old-code work expands it). The big one.
4. **V125-1-7 tightening** — label-aware orphan filter. ~half-day. Trivial after V125-1-8 lands.
5. **V125-2 cleanup** — delete adopt.go + related code. ~half-day.

Total: ~2 weeks of focused work for the architectural cleanup.

After all of this lands:
- v1.24 / v1.25 boundary becomes meaningful (the architectural shift is significant)
- V2.0.0 release planning can begin with clean foundations
- Operator mode (V3+) becomes a coherent next-step instead of a band-aid

---

## 12. Cross-references

- **Source design doc** (the architectural conversation in full): `docs/design/2026-05-11-cluster-secret-reconciler-and-gitops-stance.md` — 17 sections including "Honest evaluation: stateless reconciler vs full K8s operator"
- **Sprint status** (BMAD tracking): `.bmad/output/implementation-artifacts/sprint-status.yaml` — V125-1 epic, stories V125-1-1 through V125-1-8
- **Section 3 design** (the original pre-merge rationale that this work supersedes): `docs/design/section-3-template-and-secrets.md`
- **V1 design decisions**: `docs/design/2026-04-07-sharko-v1-design-decisions.md`
- **Implementation Plan V1**: `docs/design/IMPLEMENTATION-PLAN-V1.md` (line 34: "Direct commit to main is removed — every Git operation goes through a PR")

---

## 13. TL;DR for cold starts

If someone reads this file fresh:

1. **What Sharko is:** a platform-engineering control plane that authors PRs and orchestrates ArgoCD. Not an operator. Not a SealedSecrets/ESO replacement. Not adopting clusters it didn't create.
2. **The architectural change in V125:** stop creating ArgoCD cluster Secrets pre-merge in the orchestrator; instead, run a goroutine reconciler that converges Secret state from `managed-clusters.yaml`. Use the `app.kubernetes.io/managed-by: sharko` label as the ownership marker.
3. **The schema change in V125:** wrap `managed-clusters.yaml` and `addons-catalog.yaml` in apiVersion/kind/spec envelope; publish JSON Schema; validate at PR time and on read.
4. **What's NOT changing:** Sharko stays as control plane, vault-fetching stays Sharko's job, ApplicationSet uses Cluster generator with label selectors (no decoupled-label refactor), no operator pattern.
5. **Order of work:** V125-1-9 (schema) → V125-1-8 (reconciler) → V125-1-7 tightening + V125-2 cleanup. ~2 weeks total.
6. **Operator mode (V3+):** deferred; revisit when scale or external integration demands it.
