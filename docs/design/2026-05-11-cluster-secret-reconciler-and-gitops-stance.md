# Cluster-Secret Reconciler & GitOps Stance — Design Discussion

> **Status:** Discussion synthesis (not yet a committed design). Captures the architectural conversation between maintainer and tech-lead on 2026-05-11. Outcomes shape V125-1-7, V125-1-8, and a new V125-1-9 candidate. Pairs with the existing `section-3-template-and-secrets.md` "The PR Is the Gate" design which this discussion partially supersedes.
>
> **Authors:** Moran Weissman (maintainer); tech-lead (Claude).
>
> **Trigger:** Live Track B testing of V125-1-7 surfaced the deeper bug class V125-1-7 was only a band-aid for, prompting a reconsideration of Sharko's GitOps posture and reconciliation model.

---

## 1. The triggering observation

Maintainer's report:

> When I click add cluster via Sharko, it's supposed to create a PR so that a Secret will be created, right? But even though the PR is opened, I see secrets are being created.

Followed by a stronger framing:

> GitOps is not an option. I want to avoid a situation where Sharko creates something in cluster which is not in git. What happens if Sharko UI is not accessible? Is everything possible via CLI / kubectl? Or is kubectl not relevant because Sharko is not an operator?

These are the two principles that drove the rest of the discussion:

1. **Nothing in cluster that isn't in git.** (GitOps purity for declarative state.)
2. **Sharko shouldn't be a SPOF for cluster lifecycle operations.** (Recovery without Sharko UI possible.)

---

## 2. Why secrets get created pre-merge today (the diagnosis)

Tracing the live behavior in `internal/orchestrator/cluster.go`:

```
Step 1: Acquire credentials (cluster.go:95-125)
  - kubeconfig provider: parse inline kubeconfig (V125-1.1)
  - Else: o.credProvider.GetCredentials(name) → vault fetch

Step 3: Verify connectivity (cluster.go:128-147)
  - Stage 1 secret CRUD cycle on remote cluster

Step 3b: ArgoCD secret creation (cluster.go:227-262)
  - if argoSecretManager != nil && PRAutoMerge: Ensure() — creates Secret in argocd ns
  - if argoSecretManager != nil && !PRAutoMerge: skip, defer to reconciler (already correct)
  - if argoSecretManager == nil: this branch is skipped entirely

Step 4: Create addon secrets on remote cluster (cluster.go:264+)
  - Pre-merge by design (Section 3 doc — see below)

Step 5: Open PR (commitChanges)

Step 6: Direct ArgoCD API RegisterCluster (cluster.go:408+)
  - Skipped iff argoSecretCreated || (argoSecretManager != nil && !PRAutoMerge)
  - REACHED when argoSecretManager == nil (kubeconfig provider) → fires pre-merge ❌
```

**So the pre-creation has two paths:**

1. **AWS-SM / EKS path with auto-merge:** `argoSecretManager.Ensure` creates the Secret immediately. Merge follows in ~1s, so the orphan window is microscopic.
2. **AWS-SM / EKS path with manual merge:** Already deferred to the reconciler (line 259-262). No bug here.
3. **Kubeconfig path (any merge mode):** `argoSecretManager` is nil (the AWS-SM-based manager isn't applicable). Falls through to **Step 6 — direct ArgoCD API `RegisterCluster` call, pre-merge.** This is where the orphan-on-PR-close came from.

The bug class V125-1-7 surfaces is path 3. V125-1-7 is the recovery surface; the architectural fix needed to be V125-1-8.

---

## 3. The existing design's stance on this (and where it's incomplete)

`docs/design/section-3-template-and-secrets.md` § "Solution: The PR Is the Gate" (lines 279-326) documents the pre-creation as **intentional** for safety:

> ArgoCD can't deploy addons until BOTH (a) the cluster Secret with addon labels exists AND (b) the values file is on the main branch. Sharko controls both. Pre-creating the Secret prevents a race where ArgoCD's reconciler beats Sharko's "create remote secret" step → addon pod starts before its secrets exist → crashes.

The doc's flow:
1. Open PR (file in git, ArgoCD doesn't act on PRs)
2. Create addon secrets on remote cluster
3. **Create ArgoCD cluster Secret** (with addon labels) — pre-merge
4. Merge PR (auto or human)
5. ArgoCD acts — ApplicationSet matches the cluster + finds the values file on main → deploys

**The gap in this design:** it implicitly assumes the PR will eventually merge. Closed-without-merge wasn't modeled. The "PR is the Gate" claim breaks because step 3 already happened by the time step 4 fails.

---

## 4. Sharko is not an operator

Quick clarification on Sharko's runtime architecture, because it shapes every option below:

- Sharko is an **HTTP API server** (Deployment in cluster) + **CLI client** that hits the API.
- No CRDs, no controller-runtime, no informers, no leader election.
- The CLI is just an HTTP client, not a separate K8s tool.
- **Sharko down** = no UI, no CLI works, write operations are blocked. Reads via ArgoCD/git still work.
- Operator mode is mentioned in the V1 design notes as a future direction; sits in V3+ backlog.

So today's recovery story for "Sharko down":
- Read-only inspection of clusters via `kubectl -n argocd get secrets` and ArgoCD UI works.
- Write operations require Sharko alive. There is no kubectl-direct shortcut.
- This is a SPOF for write operations.

---

## 5. The collision risk that V125-1-7 (as shipped) didn't fully address

Sharko-created cluster Secrets carry **no ownership label** today (no `app.kubernetes.io/managed-by: sharko`). Sharko's view of "what clusters exist" is "whatever ArgoCD reports". This means:

| Scenario | Current behavior | Risk |
|---|---|---|
| User creates a cluster Secret manually (kubectl/UI/separate tool) | Appears in `not_in_git` lane; **also appears as an orphan in V125-1-7** | "Delete cluster Secret" button silently destroys user's manual work |
| User installs Sharko on top of an existing ArgoCD with N pre-existing clusters | All N show up as orphans | Could mass-delete legitimate clusters via UI |
| User deletes a Sharko-managed Secret manually | Sharko sees `connection_status: missing` | No automatic remediation |
| User mutates a Sharko Secret manually | No drift detection until next register/update | Silent overwrite |

V125-1-7 also conflicts with V125-2 (Adopt UI for unmanaged ArgoCD clusters): same population of clusters (in argocd ∖ git), opposite intents (delete-orphan vs adopt-unmanaged).

**Without an ownership marker on the Secret to distinguish "Sharko-created" from "external/operator-created", these two surfaces are double-claiming the same data.**

The fix is small and additive: every Sharko-created Secret gets the label `app.kubernetes.io/managed-by: sharko`. Every Sharko mutation/delete checks the label first.

---

## 6. GitOps compliance investigation

Investigated whether building on top of ArgoCD with Sharko's current pattern violates CNCF or ecosystem norms.

### CNCF OpenGitOps Working Group — the four principles

From [opengitops.dev](https://opengitops.dev/):

1. **Declarative** — desired state expressed declaratively
2. **Versioned and Immutable** — stored with full history (git)
3. **Pulled Automatically** — software agent pulls from source
4. **Continuously Reconciled** — agent observes actual state and converges to desired

ArgoCD itself is the canonical GitOps tool because it implements 3 + 4. Tools built on top of ArgoCD inherit the GitOps story for what ArgoCD reconciles; they're judged separately on what they themselves do.

### ArgoCD's own cluster registration is split into two paths

This was the key precedent finding. ArgoCD ships two cluster-registration paths and is explicit about the trade-off:

- **Imperative (NOT GitOps):** `argocd cluster add` — direct API call, creates Secret in argocd ns.
- **Declarative (GitOps-compliant):** Cluster Secret YAML with `argocd.argoproj.io/secret-type: cluster` label, committed to git wrapped in SealedSecrets/SOPS.

Community guidance (dev.to walkthroughs, Codefresh tutorials, ArgoCD's own docs on Secret Management) is uniform: **declarative is "the better approach"; CLI is for "quick setup"**.

**Sharko today does exactly what `argocd cluster add` does** — direct ArgoCD API call. It's the path ArgoCD itself documents as the non-GitOps shortcut. Not novel, not disqualifying.

### Where Sharko sits in the ecosystem

Sharko's category isn't "GitOps tool" (ArgoCD is). Sharko is a **GitOps control plane / GitOps producer** — opens PRs that drive ArgoCD. Comparable tools:

- Codefresh GitOps Cloud
- Akuity Platform
- Backstage + ArgoCD plugin
- Kargo (Argo project — environment promotion)
- GitOps Promoter
- PipeCD

**None of these are 100% GitOps internally.** They have imperative APIs that create resources, open PRs, mutate state. Judged on whether they ENABLE GitOps for what users do, not whether their internals are pure-pull. There is no CNCF rule that wrappers must be 100% GitOps. Argo itself isn't.

### Honest scoring of Sharko today

| Operation | Today | Pure GitOps? |
|---|---|---|
| Add addon to cluster | PR → values file → ArgoCD reconciles | ✅ |
| Configure addon | PR → values file → ArgoCD reconciles | ✅ |
| Enable/disable addon | PR → managed-clusters.yaml labels → ApplicationSet matches | ✅ |
| Edit values | PR → values file → ArgoCD reconciles | ✅ |
| Add cluster — managed-clusters.yaml entry | PR → git | ✅ |
| **Add cluster — ArgoCD cluster Secret** | Direct ArgoCD API (same as `argocd cluster add`) | ❌ |
| **Add cluster — addon secrets on remote cluster** | Sharko pushes via temp K8s client | ❌ |

So Sharko is ~85% GitOps-compliant. The two ❌ items are consistent with what ArgoCD itself does. The maintainer's principle ("nothing in cluster that's not in git") is **stricter than ArgoCD's own posture** — achievable, but requires architectural changes.

### CNCF acceptance bar for projects

CNCF Sandbox/Incubation/Graduation evaluates governance, contributors, releases, security posture — NOT "internally implements all 4 GitOps principles". This design choice doesn't block Sandbox aspiration in V2.x.

---

## 7. Options considered for the architectural fix

### Option B (the original V125-1-8 plan): Defer ArgoCD register to post-merge handler

Sharko opens PR → registers `OnPRMerged` callback via prTracker → handler creates ArgoCD Secret only after merge.

**Closes:** orphan-on-PR-close.
**Doesn't close:** "Sharko creates things not in git" — Sharko still creates the Secret, just at a different time. PR closed = no creation, but Sharko crash mid-handler = partial state.

### Option C: Cluster Secret YAML in git as SealedSecret / SOPS

Sharko writes encrypted Secret manifest into the registration PR. SealedSecrets controller decrypts on apply. ArgoCD's app-of-apps reconciles.

**Maintainer rejected:** new tool dependency (SealedSecrets / SOPS).

### Option D: ApplicationSet creates the cluster Secret

AppSet generator templated from managed-clusters.yaml renders a `Secret` per cluster.

**Why this fails:** the Secret data (server URL, CA, **bearer token**) has to come from somewhere. Every option for injecting creds into the AppSet template either:
- Puts plaintext in git (defeats the purpose), or
- Uses AVP / ArgoCD Vault Plugin (the **Redis-plaintext-cache leak** that maintainer explicitly remembered), or
- Uses an AppSet Plugin generator that pulls from vault → renders into template (same Redis-leak class), or
- Requires SealedSecrets / ESO (rejected dependency).

**Fundamental:** creds cannot flow through ArgoCD's rendering pipeline without ending up in Redis. AVP's deprecation taught the ecosystem this. AppSet template-of-Secret with vault-sourced creds is structurally the AVP problem with extra steps.

### Option E (chosen direction): Sharko-as-reconciler, no template-of-Secret in ArgoCD

The pattern that emerged from elimination:

1. **`managed-clusters.yaml` in git is THE source of truth** for "which clusters should exist in ArgoCD".
2. **ArgoCD never sees the cluster Secret in git.** Not in templates, not in plugins, not via AVP.
3. **Sharko runs a reconciliation goroutine** (not a real K8s operator — see §8):
   - Periodically (default 30s) reads managed-clusters.yaml from git
   - Lists ArgoCD cluster Secrets with `app.kubernetes.io/managed-by: sharko` label
   - In git ∖ argocd → fetch creds from vault → create Secret (with ownership label) in argocd ns
   - argocd ∖ git (with sharko label) → delete Secret
   - Without sharko label → never touched (operator-created cluster, V125-2 Adopt territory)
4. ArgoCD Applications/AppSets pick up the new cluster Secret like always.

### Why option E satisfies the principles

| Property | E |
|---|---|
| Nothing in cluster that's not in git | ✅ — Sharko reconciles **from** git |
| Creds never in git | ✅ — vault stays the cred source |
| Creds never in ArgoCD Redis | ✅ — Sharko writes Secret to argocd ns directly; ArgoCD only reads it |
| PR closed-without-merge → no orphan | ✅ — git unchanged, reconciler does nothing |
| Sharko down → cluster reg blocked | ⚠ — same SPOF as today; operator + CRD removes it (V3+) |
| New tool dependency | ✅ — none |
| Operational complexity | Low — one goroutine + label convention |
| Ownership safety (won't touch user clusters) | ✅ — managed-by label gates every mutation |
| Per-cluster Application overhead | None — Sharko writes the Secret directly |

It's **not GitOps in the strictest sense** (Sharko writes the Secret, not ArgoCD pulling from git). It IS GitOps in the operator-pattern sense (a controller reconciles desired state from a declarative source — the Argo project's own tools all do this).

---

## 8. "Can Sharko reconcile without being an operator?" — Yes

Sharko already has the pattern: `internal/prtracker/tracker.go`.

```go
type Tracker struct {
    cmStore     cmstore.Store     // ConfigMap-backed persistence
    gitProvider func() GitProvider // lazy git access
    auditFn     func(audit.Entry)
    onMergeFn   func(PRInfo)      // post-merge callback hook
}

func (t *Tracker) Start(ctx context.Context) {
    go func() {
        for {
            t.PollOnce(ctx)
            select { case <-ticker.C: case <-ctx.Done(): return }
        }
    }()
}
```

A new `internal/clusterreconciler` package would mirror this — ~200 LoC, no new dependencies.

### What you give up by NOT being a real operator

| Operator-only feature | Sharko substitute today | Real cost? |
|---|---|---|
| CRDs / `kubectl get sharkocluster` | managed-clusters.yaml in git | None — git is your source |
| `controllerutil.SetControllerReference` for owner refs | A label `app.kubernetes.io/managed-by: sharko` | Slightly weaker GC story |
| informers / watch streams | Periodic poll (30s) + on-demand trigger | Up-to-30s latency vs. ms watch |
| Leader election (HA, multi-replica) | Single Sharko pod | Same SPOF as today; add `k8s.io/client-go/tools/leaderelection` if HA |
| controller-runtime's reconcile dedupe | Idempotent reconcile loop | Same outcome, less framework |

For a single-Pod Sharko (current shape), none of these are blockers.

### When to graduate to operator mode (V3+)

- Active-active reconcilers (leader election + workqueue dedupe)
- Users want to `kubectl apply -f sharkocluster.yaml` directly
- kubectl-native discoverability (`kubectl describe sharkocluster`)
- Admission webhooks for validation
- Owner-reference GC across multiple resource types

None are V125 needs.

---

## 9. Two-direction reconciliation policy (the adoption question)

The Sharko-reconciler approach handles ONE direction (git → ArgoCD) automatically. The other direction (ArgoCD → git, when something exists in ArgoCD without a git entry) needs a separate, **human-initiated** path.

### Policy table

| Source change | Reconciler action | Direction |
|---|---|---|
| New entry in managed-clusters.yaml | Create Secret post-merge | git → ArgoCD (auto) |
| Entry removed from managed-clusters.yaml | Delete Secret (only if sharko-labeled) | git → ArgoCD (auto) |
| New ArgoCD Secret without sharko label | Surface for **Adopt** (user-initiated PR) | ArgoCD → git (manual) |
| ArgoCD Secret with sharko label deleted | Re-create from git (self-healing) | git → ArgoCD (auto) |
| ArgoCD Secret without sharko label deleted | Untracked — Sharko never knew about it | none |

### Why these defaults are right

- **One automatic direction** (git → ArgoCD) — predictable, no surprises, no fighting between reconciliation passes.
- **One human-initiated direction** (ArgoCD → git, via Adopt) — destructive imports require explicit consent. Auto-PR on every "new cluster Secret detected" is hostile (someone could be doing diagnostics with `kubectl apply`).
- **Self-healing for accidental deletions** — git is the safety net. An accidental `kubectl delete secret` for a Sharko-managed cluster gets repaired on the next reconcile tick.
- **Removal flows through git** — user opens PR (or clicks "Remove" in UI which opens the PR), PR merges, reconciler deletes the Secret. Symmetric with creation.
- **Ownership label is the gate** — Sharko never touches what it doesn't own. Solves the V125-1-7 ↔ V125-2 collision permanently.

### What's already there vs needs to be built

| Capability | Today | Need for V125 |
|---|---|---|
| Detect Secret in argocd ∖ git | ✅ — `internal/service/cluster.go:120-132` `not_in_git` lane | None (already there) |
| Adopt action (write to git) | ✅ — `internal/orchestrator/adopt.go` (V2 Epic 4) | UI surfacing (V125-2 backlog) |
| Reconciler git → ArgoCD with self-heal | ❌ | V125-1-8 (reframed) |
| Ownership label on Sharko-created Secrets | ❌ | V125-1-8 (alongside reconciler) |
| Cleanup of unlabeled orphan Secrets | ❌ in current V125-1-7 (deletes anything in argocd ∖ git ∖ pending — too broad) | Tighten V125-1-7 to require sharko label before delete |

---

## 10. How Sharko reads git for reconciliation (no clone needed)

Confirmed mechanism in current code:

```go
// internal/service/cluster.go:73
clusterData, err := gp.GetFileContent(ctx, s.managedClustersPath, "main")
```

Where `gp` is a `gitprovider.GitProvider` — for GitHub, the implementation calls the **GitHub Contents API**:

```
GET /repos/{owner}/{repo}/contents/configuration/managed-clusters.yaml?ref=main
```

GitLab and ADO providers use their respective Files APIs. **No clone, no local checkout, no disk dependency.** One HTTP GET returns the current contents of the file at HEAD of the specified ref.

### "Ref" handling

Today's reads use `s.gitopsCfg.BaseBranch` (configurable per connection, default `"main"`) — see `internal/api/clusters_write.go:344`, `repo_status.go:52`. A few callers in `service/cluster.go` hardcode `"main"` — minor inconsistency to clean up alongside V125-1-8.

### Stateless reconciliation — no revision tracking

The reconciler does NOT need to track "previous commit" or "what changed since last tick". K8s controller pattern:

```
On each tick:
  1. Read current desired state (managed-clusters.yaml from git API)
  2. Read current actual state (Sharko-labeled Secrets from ArgoCD)
  3. Compute set diff (in-git ∖ in-argocd, in-argocd ∖ in-git)
  4. For each delta → act
```

Idempotent, restart-safe, no state-loss recovery story to engineer. This is the same shape every K8s controller uses.

### Why ArgoCD clones but Sharko doesn't need to

ArgoCD clones because of what ArgoCD does: Helm rendering, Kustomize, multi-source apps, sync history per Application, side-by-side diff display, webhook invalidation. Sharko's reconciler reads one YAML file, parses it, compares names. **API fetch is the right tool.**

### Webhooks — optional optimization

Sharko could expose `POST /webhook/git` for instant reactivity (vs the 30s poll worst-case). Defer to V2; periodic poll + the prTracker `onMergeFn` callback (which already gives immediate trigger on Sharko-opened PRs) is enough for V1.

### Failure modes

| Failure | Behavior | Recovery |
|---|---|---|
| Git API down / rate-limited | Reconcile fails this tick; previous state persists | Next tick retries; idempotent |
| Git auth token expired | Reconcile fails; surfaces in audit | Admin rotates token |
| HTTP cache stale (theoretical) | Could serve stale file | Go stdlib `http.Client` doesn't auto-cache; safe by default |
| Sharko crashes mid-reconcile | Half-applied changes possible | Next reconcile picks up because it re-derives from current state |
| Multi-replica Sharko (HA) | All replicas reconcile concurrently → race | Add leader election if needed |

---

## 11. The "magic YAML file" concern (separate but adjacent)

Maintainer raised a separate worry:

> What disturbs me a bit is that the YAML structure like `cluster-addons` is heavily connected to Sharko logic, but from the user side it's just a YAML file. Not code, not CRD.

This is real. `managed-clusters.yaml` is functionally a schema-bearing resource, but the schema lives only in Sharko's Go parser — not as a CRD, not as a JSON Schema, not as anything externally validatable.

### Why it matters more once V125-1-8 lands

A reconciler-driven design makes the YAML the **authoritative source of truth**. Schema integrity goes from "nice to have" to "operationally critical": bad YAML = silent reconcile failures = potential incidents.

### The spectrum of fixes

| Tier | What you do | Effort | What you get |
|---|---|---|---|
| **0: Status quo** | Nothing | — | Schema-in-binary; discoverable only via docs |
| **1: Document the schema** | Reference page in mkdocs | half-day | Clear authoring docs; no validation |
| **2: Publish a JSON Schema** | Generate from Go structs; publish at stable URL | 1 day | Editor autocomplete via `# yaml-language-server: $schema=...` header; CI lint via `kubeconform` or `ajv` |
| **3: apiVersion/kind convention** | Add `apiVersion: sharko.io/v1` + `kind: ManagedClusters` + `spec:` to the YAML structure (still not a CRD, just looks like one) | 1-2 days (migration) | Self-describing files; matches K8s ecosystem norm; enables versioning/migration story |
| **4: Real CRD** | Operator mode — install CRD, controller reconciles, kubectl-native | weeks | Full K8s integration; apply-time validation; owner refs |

Tiers 1+2+3 are additive and don't require operator-mode transition. They're the pragmatic bridge.

### Recommended shape (pairs with V125-1-8)

1. **Versioned schema:**
   ```yaml
   # yaml-language-server: $schema=https://sharko.io/schemas/managed-clusters.v1.json
   apiVersion: sharko.io/v1
   kind: ManagedClusters
   spec:
     clusters:
       - name: prod-eu
         labels:
           team: platform
         addons:
           datadog: true
   ```

2. **Generate JSON Schema from Go types** (`go run ./cmd/schema-gen` → `docs/schemas/managed-clusters.v1.json`).

3. **Validation on PR** — Sharko CLI/CI hook validates YAML before commit. Reconciler also validates on read; rejects malformed files with a clear audit-log error rather than silent reconcile failure.

4. **Migration story** — when schema evolves (v1 → v2), Sharko reads both, writes v2. `apiVersion` field gives version negotiation.

5. **Same treatment for `addons-catalog.yaml`** — same concern, same fix.

This is mechanical work (~3-5 days). Lands cleanly **before** V125-1-8 so the reconciler reads against a stable, validated contract.

### Connection to operator mode

Tier 3 is also the right preparation for Tier 4. When you graduate to a CRD in V3+, the YAML is already shaped like a CR. Conversion is mechanical: register the same `apiVersion`/`kind`/`spec` shape as a CRD, and now `kubectl get managedclusters` works. Users keep authoring the same YAML. No migration shock.

---

## 12. Implications for V125+ stories

### V125-1-7 (orphan surface — already shipped, needs tightening)

**Problem identified:** the current implementation deletes ANY ArgoCD Secret that's in argocd ∖ git ∖ pending. Without an ownership label check, this risks mass-deleting user-managed Secrets when Sharko is layered onto an existing ArgoCD with pre-existing clusters.

**Required follow-up:**
- Add ownership label check to `handleDeleteOrphanCluster` — refuse to delete a Secret without `app.kubernetes.io/managed-by: sharko`.
- The orphan resolver should similarly only surface Secrets that carry the Sharko label — Secrets without the label are V125-2 Adopt territory, not orphan territory.
- Rename UI button from "Delete cluster Secret" (technical, confusing per maintainer feedback) to something the user mental model maps to: e.g. "Discard cancelled registration" or "Clean up orphan registration".

### V125-1-8 (architectural close — reframed)

**Old plan:** "Defer ArgoCD register to post-PR-merge handler" via prTracker `onMergeFn` callback.

**New plan:** Build `internal/clusterreconciler` package that reconciles ArgoCD cluster Secret state from `managed-clusters.yaml` in git. Mirror prtracker's goroutine + ConfigMap state + audit pattern. Wire prtracker `onMergeFn` to trigger the reconciler for low-latency post-merge convergence; periodic 30s tick is the safety net.

**Concrete deltas:**
1. New package `internal/clusterreconciler` — ~200 LoC mirroring prtracker structure.
2. Add ownership label `app.kubernetes.io/managed-by: sharko` on every Sharko-created Secret. Back-compat one-shot migration on first reconcile (label existing Sharko-managed Secrets).
3. Wire `reconciler.Start(ctx)` into `cmd/sharko/serve.go` near the prtracker bootstrap.
4. Wire `prTracker.SetOnMergeFn` to call `reconciler.Trigger()` for immediate post-merge reconcile.
5. Remove pre-merge `argoSecretManager.Ensure` and direct-API `RegisterCluster` calls from `internal/orchestrator/cluster.go` — they become dead code once the reconciler owns Secret lifecycle.
6. Remove `argoSecretManager` interface entirely if no remaining caller uses it.

**The hardest part isn't the operator question — it's the cleanup of the existing pre-merge code paths and proving the reconciler covers them.** Behavioral parity tests on every register path (kubeconfig, EKS-direct, EKS-discovery, batch, adopt) need to pass before the old paths can go.

### V125-1-9 (new candidate — schema versioning)

Frame as a prerequisite for V125-1-8 to be operationally safe.

1. Publish JSON Schema for `managed-clusters.yaml` at a stable URL.
2. Adopt `apiVersion: sharko.io/v1` + `kind: ManagedClusters` + `spec:` envelope.
3. `# yaml-language-server: $schema=...` header in every Sharko-written YAML.
4. Validate on PR (CI hook) and on reconciler read (audit-logged rejection).
5. Same treatment for `addons-catalog.yaml`.

Sequencing: V125-1-9 → V125-1-8 → V125-1-7 tightening (in that order; each depends on the previous for safety).

### V125-2 (Adopt UI)

Now becomes the natural complement to V125-1-7's tightened orphan surface. Same data (clusters in argocd ∖ git), now distinguishable by label:

- **Has sharko label** → orphan candidate (V125-1-7).
- **No sharko label** → unmanaged cluster, candidate for adoption (V125-2). The Adopt action writes managed-clusters.yaml entry **and** adds the sharko ownership label to the existing Secret, bringing it under management without re-registering.

V2 Epic 4 backend (`internal/orchestrator/adopt.go`) already exists; V125-2 is the UI surfacing.

### V3+ — operator mode (deferred, not blocked by anything above)

The recommended V125 work is intentionally a bridge. Tier 3 schema convention positions the codebase such that graduating to a real CRD is mechanical, not architectural. When the V3 conversation happens, every piece (label-based ownership, reconciler pattern, schema envelope, GitOps directionality) translates cleanly into the operator pattern.

---

## 13. Honest evaluation: stateless reconciler vs full K8s operator

**Question (maintainer):** What do you honestly think about this solution in its context and usage? K8s operator vs Pod which is a server and a stateless gitops?

**Answer:**

For Sharko v1.x today and the next 6-12 months: the stateless server + reconciler-goroutine pattern is the right choice. Operator mode would be over-engineering.

Here's why, given everything we discussed:

### What you're actually building

Sharko is a platform engineering control plane, not infrastructure. The user is a platform team member adding/removing clusters and toggling addons. Volume is low (tens of clusters, tens of addons, low write rate — minutes between operations). Latency tolerance is high (30s reconcile is fine; nobody's paging on a missed sync). The hard parts are UX and PR ergonomics, not reconciliation throughput.

For that workload profile, the operator pattern's strengths (HA via leader election, watch-stream sub-second latency, workqueue dedup at scale, owner-ref GC across thousands of resources) are mostly wasted. You'd be paying real complexity costs for benefits you don't consume.

### What the stateless pod gets you that matters

- **Single deployment artifact** — one Helm chart, one image, one Pod. Operator pattern adds CRDs to the install (cluster-admin permission needed at install time), webhook certs, RBAC for the controller-manager, leader-election lease. Each is a real install/upgrade failure mode.
- **Observability is HTTP** — `/metrics`, `/healthz`, request logs. Operator observability is "watch the controller logs and pray you can correlate reconcile loops to user actions." Sharko's audit log + per-request trace is genuinely better for the "what did Moran click and what happened" use case.
- **Restart resilience comes free from statelessness** — you already have it without leader election machinery.
- **The CLI works** — `sharko cluster add` is meaningful UX. `kubectl apply -f sharkocluster.yaml` is the operator equivalent and asks the user to author a CR they probably shouldn't be writing by hand.
- **Iteration speed** — every architectural change is "edit Go, redeploy Pod." Operator changes touch CRDs, conversion webhooks, version negotiation — much slower to evolve. Sharko is still finding its shape; lock-in costs hurt now.

### What you give up by NOT being an operator

- **HA active-active** — you have one Pod doing reconciliation. Sharko down = no reconciliation. For a platform tool used a few times a day, this is a non-issue. For something on the critical request path, it would be.
- **kubectl-native discoverability** — `kubectl get sharkocluster prod-eu` doesn't work. But your users are clicking buttons in a UI or running `sharko` CLI; they're not grepping `kubectl get all -n sharko-system`.
- **Owner-ref GC** — when you uninstall Sharko, the cluster Secrets it created don't auto-cleanup. Mitigation: a `sharko cleanup` CLI subcommand, or accept the manual `kubectl delete` on uninstall (rare event).
- **Apply-time schema validation** — covered by the JSON Schema + apiVersion/kind work we discussed. You get 80% of the validation benefit without the CRD machinery.

### Where the stateless pattern actually fits the philosophy better

The operator pattern is built for "the cluster is the API; declare desired state, controllers reconcile." That's right when the user authors YAML and `kubectl apply`s it.

But Sharko's actual interaction model is "the user opens a PR via a UI; the PR contains the desired state." Git is the API surface, not Kubernetes. ArgoCD is the operator (it reconciles git→cluster for Applications). Sharko is the tooling that helps users author git correctly — and a stateless service is exactly the right shape for that. An operator would impose a CR-as-API model on top of a flow that's fundamentally PR-driven.

### When the calculus would flip

I'd revisit operator mode if:

1. **You start managing thousands of clusters** — workqueue dedup and informer caches start mattering at that scale
2. **Other tools/CI need to declare clusters** — if Terraform/Crossplane/Backstage want to `kubectl apply -f managedcluster.yaml`, the CR is the right interface
3. **The reconciliation logic gets complex enough to need controller-runtime's primitives** — finalizers, multi-resource ownership, conditions tracking
4. **You hit the "Sharko is down so nothing reconciles" pain in production** — not theoretically, actually
5. **CNCF Sandbox/Incubation pushes you** — committee tends to favor operator-pattern projects

None of those apply today. They might in V3+. The stateless pattern doesn't preclude graduating later — the `apiVersion`/`kind` YAML shape we discussed is a deliberate forward-bridge to "make it a CRD when ready."

### The intellectually honest summary

The operator pattern is the prestigious choice in K8s circles. There's a real pull to "do it right" with CRDs and controller-runtime. But for a platform-engineering control plane at Sharko's current scale, the stateless server + reconciler-goroutine is **simpler, faster to evolve, easier to operate, and matches the actual interaction model better.** The operator pattern would be cargo-culting CNCF aesthetics over what the workload actually needs.

The right call is: **ship the stateless pattern now**, design the YAML schema to be CRD-shaped (apiVersion/kind), add ownership labels for safe coexistence with manual edits, and **revisit operator mode only when you hit a concrete problem the stateless pattern can't solve.**

That's the honest version. Operator mode is a perfectly defensible aspiration, but right now it would slow you down for benefits Sharko doesn't yet consume.

---

## 14. Open decisions / explicit deferrals

These are intentionally NOT decided yet — they need a separate conversation:

1. **Webhook for sub-30s reconcile latency** — defer to V2 unless real-world poll latency proves problematic.
2. **HA Sharko (multi-replica + leader election)** — defer to V2; current single-pod is fine.
3. **Migrating ConfirmationModal vs custom AlertDialog** for destructive actions — already in V125-3-8 backlog, separate concern.
4. **Per-cluster Application vs single cluster Secret** for the addon side — current single-Secret-per-cluster pattern works; don't change without specific evidence.
5. **Vault-fetch retry policy in the reconciler** — start with "fail this tick, log, try again next tick"; tighten if real workloads hit transient vault flakiness.

---

## 15. Maintainer-stated principles (recorded for future reference)

1. **GitOps is non-negotiable.** "I would want to avoid a situation where Sharko creates something in cluster which is not in git."
2. **No new tool dependencies for runtime.** Specifically rejected SealedSecrets / SOPS / ESO. Anything that requires installing more controllers in cluster needs a strong case.
3. **No AVP / Redis-leak-class patterns.** Creds must NOT pass through ArgoCD's render pipeline.
4. **Ship something useful first, iterate.** Lean workflow; not waiting on a perfect operator-mode rewrite to fix observable bugs.
5. **Ownership matters.** Sharko should not touch resources it didn't create.
6. **Schema integrity matters.** Once Sharko is reconciling from git, the file's schema becomes operationally critical and needs first-class tooling.

---

## 16. References

### Sharko design docs
- `docs/design/section-3-template-and-secrets.md` — "The PR Is the Gate" — the existing design this discussion partially supersedes
- `docs/design/2026-04-07-sharko-v1-design-decisions.md` — V1 architectural decisions
- `docs/design/IMPLEMENTATION-PLAN-V1.md` — line 34: "Direct commit to main is removed — every Git operation goes through a PR"

### Sharko code touchpoints
- `internal/orchestrator/cluster.go:227-262` — argoSecretManager.Ensure pre-merge path (AWS-SM)
- `internal/orchestrator/cluster.go:408+` — direct ArgoCD RegisterCluster pre-merge path (kubeconfig — the BUG-058 root cause)
- `internal/service/cluster.go:120-132` — `not_in_git` lane derivation
- `internal/prtracker/tracker.go` — the goroutine + poll + ConfigMap pattern V125-1-8 will mirror
- `internal/api/clusters_pending.go` — V125-1-5 pending-PR resolver
- `internal/api/clusters_orphans.go` + `clusters_orphan_delete.go` — V125-1-7 (needs tightening)
- `internal/orchestrator/adopt.go` — V2 Epic 4 backend (V125-2 UI surfaces this)

### CNCF / ecosystem references
- [OpenGitOps Principles — CNCF Working Group](https://opengitops.dev/)
- [ArgoCD Declarative Setup — cluster Secret schema](https://argo-cd.readthedocs.io/en/stable/operator-manual/declarative-setup/)
- [`argocd cluster add` — imperative path documentation](https://argo-cd.readthedocs.io/en/latest/user-guide/commands/argocd_cluster_add/)
- [ArgoCD Cluster Bootstrapping (App-of-Apps)](https://argo-cd.readthedocs.io/en/latest/operator-manual/cluster-bootstrapping/)
- [ArgoCD Secret Management](https://argo-cd.readthedocs.io/en/stable/operator-manual/secret-management/)
- [CNCF End User Survey 2025 — ArgoCD adoption majority](https://www.cncf.io/announcements/2025/07/24/cncf-end-user-survey-finds-argo-cd-as-majority-adopted-gitops-solution-for-kubernetes/)

---

## 17. TL;DR for future sessions

If you load this doc cold:

1. **Sharko is not a pure-GitOps tool today** — direct ArgoCD API calls for cluster Secret creation. This matches `argocd cluster add`'s posture; not a CNCF compliance issue.
2. **Maintainer wants stricter posture** — nothing in cluster that isn't in git, no new tool dependencies, no AVP-class leaks.
3. **Recommended architecture** — Sharko-as-reconciler pattern: goroutine reads `managed-clusters.yaml` from git via REST API (no clone), reconciles ArgoCD Secret state to match. Ownership label gates all mutations.
4. **Two-direction reconciliation policy** — automatic git → ArgoCD; human-initiated ArgoCD → git via Adopt. Self-healing for accidental Secret deletions.
5. **V125 work** — V125-1-9 (schema envelope) → V125-1-8 (reconciler + ownership label) → V125-1-7 tightening + V125-2 UI surfacing.
6. **Operator mode (V3+)** — natural evolution; no longer urgent because the reconciler pattern delivers ~95% of the value without the rewrite.
