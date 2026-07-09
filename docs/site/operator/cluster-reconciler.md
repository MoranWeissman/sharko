# Cluster Reconciler (V1.25)

> **Reference page (architecture) + embedded troubleshooting
> mini-runbooks.** The first two-thirds of this page documents the
> reconciler's ownership semantics, cadence, two-direction policy, and
> recovery scenarios. The Troubleshooting section near the bottom
> contains the failure-specific lanes that
> [`failure-mode-index.md`](failure-mode-index.md) deep-links into —
> the section anchors there (`#what-if-managed-clustersyaml-has-a-schema-validation-error`,
> `#what-if-a-labeled-secret-is-accidentally-deleted-kubectl-delete`,
> `#what-happens-if-a-user-removes-the-label-manually`) are
> load-bearing. Standalone P0 / P1 runbooks for distinct reconciler
> failures live at
> [`reconciler-crash-loop.md`](reconciler-crash-loop.md) and
> [`cluster-reconciler-dependency-missing.md`](cluster-reconciler-dependency-missing.md);
> this page is the architecture reference operators read once to
> understand the reconciler and the embedded troubleshooting
> sub-sections are the inline mitigations for the specific failures
> the index points at.

Sharko v1.25 introduces an in-Pod **cluster-secret reconciler** that owns
the lifecycle of ArgoCD cluster Secrets. This page is the operator
runbook: ownership semantics, reconcile cadence, the two-direction
policy, recovery scenarios, coexistence with externally-created
clusters, and troubleshooting.

If you only operate Sharko (don't author K8s manifests by hand), the
headline is:

> **You don't have to do anything.** First cluster registration after
> upgrading to v1.25 is reconciled post-merge — the ArgoCD cluster
> Secret is created automatically once the PR merges, tagged with an
> ownership label, and removed automatically when the cluster is
> removed from `managed-clusters.yaml`. No manual `kubectl` work.

The rest of this page is for operators who need to reason about the
reconciler when something goes wrong, or who run Sharko on top of an
existing ArgoCD with pre-existing cluster Secrets.

---

## Overview

Sharko runs a single in-Pod goroutine (no CRD, no controller-runtime,
no operator deployment) that reads `managed-clusters.yaml` from your
bootstrap git repo and reconciles ArgoCD's cluster Secret state to
match. Every cluster Sharko creates carries the ownership label
`app.kubernetes.io/managed-by: sharko`; the reconciler only ever
mutates Secrets that carry that label. Externally-created cluster
Secrets are surfaced for adoption (V125-2 backlog) and never touched
by the reconciler.

The reconciler is the V1.25 architectural answer to the
PR-closed-without-merge orphan bug — Secrets are now only ever created
**after** a registration PR merges, so a cancelled PR cannot leave an
orphan Secret behind. See the design discussion at
[`docs/design/2026-05-11-cluster-secret-reconciler-and-gitops-stance.md`](https://github.com/MoranWeissman/sharko/blob/main/docs/design/2026-05-11-cluster-secret-reconciler-and-gitops-stance.md)
for the architectural reasoning.

---

## Ownership label: `app.kubernetes.io/managed-by: sharko`

The reconciler uses a single label as its safety gate.

### What it is

Every ArgoCD cluster Secret that Sharko creates in the `argocd`
namespace carries the standard Kubernetes-recommended ownership label:

```yaml
metadata:
  labels:
    app.kubernetes.io/managed-by: sharko
    argocd.argoproj.io/secret-type: cluster
    # ...plus any addon-enable labels Sharko writes from managed-clusters.yaml
```

The label is applied at Secret-create time inside
`internal/clusterreconciler/labels.go` via `ApplyManagedBySharkoLabel`
and the reconciler's `createOne` path. Idempotent — re-applies are no-ops.

### Why it exists

Before V1.25, Sharko's view of "what clusters exist" was "whatever
ArgoCD reports", with no way to distinguish Sharko-created Secrets
from operator-created or kubectl-created Secrets. That made two
surfaces unsafe to ship:

- The V125-1-7 "Delete cluster Secret" orphan-cleanup flow could
  destroy a user's manually-created cluster Secret if it happened to
  be absent from `managed-clusters.yaml`.
- The V125-2 Adopt UI (planned) needs to surface exactly the
  *unmanaged* Secrets — the inverse of the orphan-cleanup set.

The label is the gate that makes both surfaces safe: Sharko mutates
only labeled Secrets, and the Adopt UI surfaces only unlabeled ones.

### When Sharko applies it

On every Secret it creates — there is no other path. There is no
back-fill migration in v1.25 (Sharko is pre-prod and there are no
unlabeled legacy Sharko-Secrets in the field).

### What happens if a user removes the label manually

Sharko stops touching the Secret. From the reconciler's perspective
the Secret no longer exists — it falls out of the "labeled Secrets in
argocd" set used for diff. The cluster also disappears from the
orphan-resolver lane (V125-1-7), so the "Discard cancelled
registration" UI button won't act on it either. The Secret enters
"externally managed" territory; V125-2's Adopt UI will surface it as
an adoption candidate.

### What happens if a user adds the label to a Secret Sharko didn't create

The next reconcile tick will read the labeled Secret, fail to find a
matching entry in `managed-clusters.yaml`, classify it as an
in-argocd ∖ in-git delta, and **delete it** as part of the
self-consistency pass. This is the inverse of the missing-cluster case
and it is intentional — the reconciler exists to make ArgoCD state
match the git declaration.

**Operator action if you see this happen unintentionally:** either add
the cluster to `managed-clusters.yaml` (preferred — declare the
desired state) or remove the label from the Secret to take it out of
the reconciler's purview.

---

## Reconciliation cadence

Two triggers drive a `PollOnce` call:

- **Periodic tick** — default 30 seconds (`DefaultTickInterval` in
  `internal/clusterreconciler/reconciler.go`). Hardcoded in v1.25;
  Helm-values configurability is deferred to a V125-2 polish pass.
- **Immediate post-merge trigger** — `cmd/sharko/serve.go` wires
  `prTracker.SetOnMergeFn` so that every Sharko-opened PR that merges
  calls `reconciler.Trigger()`. The trigger channel has buffer 1, so
  back-to-back merges coalesce — the reconciler will see *at least
  one* tick after the last merge and converge.
- **Manual "Sync now" trigger** (V2-cleanup-89.4) — `POST
  /api/v1/clusters/{name}/reconcile` fires the same `Trigger()` channel
  on demand. It's a fleet-wide pass, not a scoped single-cluster
  reconcile (`pollOnce` always diffs the full desired-vs-live set in
  one pass, so a full pass is the same work the 30s tick already does
  — triggering it early is cheap). Returns `202` immediately; poll `GET
  /clusters/{name}` and read the updated `last_reconcile` field once
  the triggered pass completes. In the UI, this is the **Sync now**
  button on the cluster detail page.

The 30s periodic tick is the safety net for:

- out-of-band git changes (someone edited `managed-clusters.yaml`
  directly on the main branch instead of via Sharko),
- accidental Secret deletion (`kubectl delete secret ...` in the
  argocd namespace),
- vault transient outages (the next tick retries).

End-to-end convergence latency in the happy path (PR merged via
Sharko-UI auto-merge) is under 5 seconds, proved by
`tests/e2e/lifecycle/reconciler_test.go::TestE2E_RegisterCluster_PostMergeReconcile_CreatesSecret`.

---

## Per-cluster reconcile visibility (`last_reconcile`)

Before V2-cleanup-89.4, a reconcile failure for one specific cluster (a
vault fetch error, a rejected K8s API call, a self-managed connection
still waiting on the user to create its Secret) went to the server log
and the audit log only — ArgoCD would show a failed apply, and Sharko
showed nothing. `GET /clusters/{name}` (and the clusters list/detail
endpoints) now include a `last_reconcile` field:

```json
{
  "last_reconcile": {
    "time": "2026-07-09T14:32:07Z",
    "outcome": "failed",
    "message": "vault_get_failed: ..."
  }
}
```

`outcome` is one of `succeeded`, `failed`, or `skipped`. This is
**derived, in-memory state only** — never written to git, never
persisted across a restart. A server restart loses the history, but the
next tick (at most `DefaultTickInterval` later) repopulates it, exactly
like the reconcile loop itself is self-healing. The field is
absent/`null` when the reconciler hasn't processed this cluster on this
server instance yet (fresh startup, or a registration PR that hasn't
merged). In the UI, this renders as a "Last sync" line on the cluster
detail page.

### Label-fight detection (V2-cleanup-89.5)

For a **self-managed** connection, the reconciler only ever merges
addon labels onto the Secret you created — it never touches anything
else. If that same Secret is *also* rendered from Git by a separate
ArgoCD Application, the two can end up fighting over the addon-label
keys. The reconciler tracks consecutive ticks where a label it just
wrote comes back with a different live value than the one it wrote —
not merely "changed" (an addon toggle in `managed-clusters.yaml`
changing what Sharko itself wants is never flagged as a fight). After
**2 consecutive reverted ticks**, `last_reconcile.outcome` stays
`succeeded` (Sharko is still successfully re-applying its labels every
tick — nothing is actually broken from Sharko's side) but `message`
carries a plain-English warning naming the pattern. Full writeup,
including the fix, at [Managing Cluster Connections Yourself → When
another ArgoCD Application also renders this
secret](self-managed-connections.md#when-another-argocd-application-also-renders-this-secret).

---

## Two-direction policy

The reconciler handles one direction automatically (git → ArgoCD) and
defers the other direction (ArgoCD → git) to a human-initiated Adopt
action (V125-2 backlog).

| Source change                                       | Reconciler action                              | Direction               | Status   |
| --------------------------------------------------- | ---------------------------------------------- | ----------------------- | -------- |
| New entry in `managed-clusters.yaml`                | Create labeled Secret in argocd namespace      | git → ArgoCD (auto)     | ✓ shipped |
| Entry removed from `managed-clusters.yaml`          | Delete labeled Secret                          | git → ArgoCD (auto)     | ✓ shipped |
| ArgoCD Secret **without** sharko label appears      | Ignored (never touched; V125-2 Adopt surfaces) | ArgoCD → git (manual)   | ✓ shipped |
| Labeled Secret deleted out-of-band                  | Re-create from git + vault on next tick        | git → ArgoCD (auto)     | ✓ shipped |
| Unlabeled Secret deleted                            | Untracked — never Sharko's problem             | none                    | ✓ shipped |

Two principles drive the asymmetry:

- **One direction is automatic** because git is the declared source of
  truth and convergence is well-defined.
- **The other direction is human-initiated** because importing an
  existing Secret is destructive (rewriting `managed-clusters.yaml`)
  and requires explicit operator intent — someone might be running
  diagnostics with `kubectl apply` and a silent auto-PR would be
  hostile.

---

## Recovery scenarios

### What if Sharko is down?

Cluster *registration* (creating new entries in `managed-clusters.yaml`
via Sharko's API/UI) is blocked because no PR can be opened. Existing
labeled cluster Secrets in the argocd namespace are **unchanged** —
ArgoCD continues to use them for in-flight syncs. Read paths via the
ArgoCD UI and `kubectl -n argocd get secrets` continue to work.

Sharko is a single-pod SPOF for *write* operations. This is the same
posture as v1.24 and earlier — V1.25 does not change the SPOF
characteristic. HA / leader-election work is deferred to V2 per the
design doc §14.

### What if vault is transiently down?

The reconciler logs an error per affected cluster, audit-logs the
failure with `cluster_secret_create_failed` / `vault_get_failed`, and
**continues** to other clusters in the same tick (per-cluster error
isolation in `pollOnce`). Other clusters whose creds are cacheable or
unaffected will still reconcile in the same tick. The failed cluster
retries on the next tick — there is no exponential backoff in v1.25
(design §14; deferred to V2 if production load reveals the need).

### What if a labeled Secret is accidentally deleted (`kubectl delete`)?

**Self-healing.** The next reconciler tick observes the in-git ∖
in-argocd delta, re-fetches credentials from vault, and re-creates the
Secret with the ownership label and addon-enable labels. Up to a 30s
recovery window, or immediately if you click **Sync now** (or `POST
/clusters/{name}/reconcile`) — see "Troubleshooting" below.

### What if `managed-clusters.yaml` has a schema-validation error?

The reconciler refuses to act on the invalid file. The V125-1-9
read-time validator (integrated into `models.LoadManagedClusters`)
returns a structured error, the reconciler audit-logs
`schema_validation_failed`, and **no partial reconcile happens** —
existing labeled Secrets remain untouched until the file validates
again. The fix is to correct the YAML via a follow-up PR; the next
tick after the fix converges normally.

This is the operational-safety guarantee V125-1-9 was built to
provide. Run `sharko validate-config configuration/` locally to
exercise the same validator surface before opening a PR.

### What if git is rate-limited or unreachable?

The reconciler logs the git-fetch error, audit-logs
`git_fetch_failed`, and **skips this tick**. Existing labeled Secrets
remain unchanged (no destructive action on incomplete information).
The next tick retries.

GitHub Contents API rate limits in practice: the reconciler makes one
GET per tick (the `managed-clusters.yaml` file at `main`). At a 30s
tick, that's 120 reads per hour per Sharko Pod — well under any
sensible PAT rate limit.

---

## Coexistence with externally-created clusters

Pre-existing ArgoCD cluster Secrets that don't carry the
`app.kubernetes.io/managed-by: sharko` label are entirely ignored by
the reconciler. They show up in ArgoCD's dashboard as
ArgoCD-known clusters, but Sharko's "in-git" list does not include
them and the orphan-cleanup flow refuses to touch them.

**Future direction:** the V125-2 Adopt UI (planned) will surface these
unmanaged Secrets and let you import them into Sharko management — the
Adopt action writes a `managed-clusters.yaml` entry **and** adds the
sharko ownership label to the existing Secret, bringing it under
management without re-registering or rotating credentials.

> **Do not** try to delete an unlabeled Secret via Sharko's "Discard
> cancelled registration" button (formerly "Delete cluster Secret").
> The orphan-delete endpoint now refuses (HTTP 400, V125-1-8.2
> tightening) for Secrets without the sharko label. If you want to
> delete an externally-managed cluster Secret, use `kubectl -n argocd
> delete secret <name>` directly.

---

## Troubleshooting

### "My cluster's Secret didn't appear after merging the PR"

1. Check the audit log for `cluster_secret_create` events around the
   merge time. Use the Audit Log UI or `GET /api/v1/audit?
   action=cluster_secret_create&since=<time>`.
2. If you see `cluster_secret_create_failed` or `vault_get_failed`,
   the cluster's credentials are not retrievable. Check vault for the
   cluster's creds path and ensure your Sharko vault provider has
   access.
3. If you see no events at all for the cluster, the reconciler may
   not have run yet (wait up to 30s) or `managed-clusters.yaml` may
   have a schema-validation error blocking the entire tick — check
   for `schema_validation_failed`.
4. If you see `git_fetch_failed`, check the git provider PAT and
   GitHub/GitLab/ADO API status.

### "A cluster I removed from `managed-clusters.yaml` is still in ArgoCD"

1. Verify the Secret carries the sharko label:
   `kubectl -n argocd get secret <name> -o yaml | grep managed-by`.
   If the label is absent, the reconciler will not delete the Secret
   — it is treated as externally-managed.
2. If the label is present, check the audit log for
   `cluster_secret_delete` events on subsequent ticks. A delete
   failure (RBAC, API server down) will appear as
   `cluster_secret_delete_failed` with a per-cluster error.
3. Worst case: confirm `managed-clusters.yaml` actually omits the
   cluster on the `main` branch (the reconciler reads `main`, not your
   working branch).

### "Sharko keeps trying to delete my cluster Secret"

You likely have a Secret with the sharko label but no corresponding
entry in `managed-clusters.yaml`. The reconciler is doing its job —
converging ArgoCD state to match the declared git state. Two fixes:

- **Preferred:** add the cluster to `managed-clusters.yaml` (declare
  the desired state).
- **Alternative:** remove the `app.kubernetes.io/managed-by: sharko`
  label from the Secret to take it out of the reconciler's purview.

### "How do I force a reconcile right now without waiting 30s?"

Two ways, as of V2-cleanup-89.4:

- **UI:** click **Sync now** on the cluster's detail page.
- **API:** `POST /api/v1/clusters/{name}/reconcile` — returns `202`
  immediately (the reconcile itself runs asynchronously), then poll
  `GET /clusters/{name}` and read the updated `last_reconcile` field
  once the triggered pass completes.

Any merge of a Sharko-opened PR also still triggers an immediate
reconcile automatically via `prTracker.SetOnMergeFn` — the manual
trigger above is for the cases that aren't a PR merge (an out-of-band
git edit, a manually-recreated Secret, or just wanting to confirm
convergence without waiting for the safety-net tick).

### Common audit-log actions to grep for

| Action                            | Meaning                                                  |
| --------------------------------- | -------------------------------------------------------- |
| `cluster_secret_create`           | Reconciler created a Secret in argocd ns                  |
| `cluster_secret_create_failed`    | Create attempt failed (per-cluster error isolated)      |
| `cluster_secret_delete`           | Reconciler deleted a labeled Secret                      |
| `cluster_secret_delete_failed`    | Delete attempt failed                                    |
| `vault_get_failed`                | Vault returned an error fetching cluster creds          |
| `git_fetch_failed`                | Git Contents API returned an error                       |
| `schema_validation_failed`        | `managed-clusters.yaml` failed envelope/schema check    |

---

## Implementation references

- **`internal/clusterreconciler/reconciler.go`** — the `Reconciler`
  struct, `Start` / `Stop` / `Trigger` lifecycle, and `pollOnce` diff +
  act loop with per-cluster error isolation. ~200 LoC mirroring the
  shape of `internal/prtracker/tracker.go`.
- **`internal/clusterreconciler/labels.go`** — `LabelManagedBy` /
  `LabelValueSharko` constants and the `IsManagedBySharko` /
  `ApplyManagedBySharkoLabel` helpers.
- **`internal/clusterreconciler/poll_test.go`** — 7 unit tests covering
  create / delete / skip-unlabeled / idempotency / per-cluster vault
  failure / invalid YAML rejection / git-fetch failure.
- **`internal/clusterreconciler/reconciler_test.go`** +
  **`labels_test.go`** — lifecycle and label-helper unit coverage.
- **`tests/e2e/lifecycle/reconciler_test.go`** — 3 end-to-end tests
  running against the kind harness: register → post-merge Secret
  creation, remove → reconciler deletion, accidental-delete →
  self-healing.
- **`cmd/sharko/serve.go`** — production wiring: `clusterreconciler.New`
  near the prtracker bootstrap, `prTracker.SetOnMergeFn(reconciler.Trigger)`
  for immediate post-merge convergence, `recon.Start(ctx)` for the
  periodic tick.
- **`internal/api/clusters_orphan_delete.go`** +
  **`internal/api/clusters_orphan_ownership.go`** — V125-1-8.2 label
  gate on the orphan-delete endpoint (rejects unlabeled Secrets with
  HTTP 400).
- **`internal/api/clusters_orphans.go`** — V125-1-8.2 resolver gate
  (only surfaces labeled Secrets in the orphan lane).
- **`internal/gitops/yaml_mutator_cluster.go`** — V125-1-8.3
  envelope-aware writer replacing the legacy line-level mutators
  (closes task #257).

---

## Related reading

- [Configuration reference](./configuration.md) — Helm values surface
  (the reconciler is on-by-default; no operator opt-in required).
- [Audit log](./audit-log.md) — query syntax for the events listed
  above.
- [Connection Doctor](./connection-doctor.md) — an on-demand,
  per-cluster check that includes a `secret-ownership` check covering
  the same label-fight failure mode this reconciler self-detects over
  time.
- [Managing Cluster Connections Yourself](./self-managed-connections.md)
  — the self-managed connection mode this reconciler's two-direction
  policy and label-fight detection both key off.
- Architecture decision: cluster reconciler — the design discussion
  that motivated this work
  (`docs/design/2026-05-11-cluster-secret-reconciler-and-gitops-stance.md`
  in the Sharko repo). Not part of the published docs site.
