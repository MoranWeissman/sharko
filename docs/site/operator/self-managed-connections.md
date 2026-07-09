# Managing Cluster Connections Yourself

Sharko is a guest on **your** ArgoCD. By default, when Sharko registers a
cluster it also creates the ArgoCD cluster secret for it and keeps its
credentials up to date. If you would rather own that connection yourself —
because your cluster secrets come from Terraform, an external-secrets
pipeline, or plain `kubectl` — turn on the per-cluster mode
**"connection managed by: me"**.

In that mode, for that cluster:

- **Sharko never writes, rotates, or deletes the ArgoCD cluster secret.**
  Not at registration, not from the reconcilers, not when you remove the
  cluster from Sharko.
- **Sharko only manages the addon labels on it.** The reconcilers merge
  your addon selections (`monitoring: enabled`, `logging: disabled`, …)
  onto the secret you created, and touch nothing else — the labels are the
  only thing ArgoCD's ApplicationSet selector needs to deploy addons.
- **Credentials become optional.** If you give Sharko a kubeconfig at
  registration it only uses it to test connectivity; if you don't,
  registration skips the test and goes straight to the Git record.

![Two ownership modes both converge at the ApplicationSet: sharko-managed, where Sharko creates and rotates the ArgoCD cluster Secret and the reconciler writes credentials plus addon labels; and self-managed, where you create the Secret by hand and the reconciler only ever merges addon labels onto it.](../assets/diagrams/05-connection-ownership.drawio.svg)

Clusters you **adopt** from an existing ArgoCD get this mode automatically
— they already have a secret you (or your tooling) created, and that is
the whole point of adopting.

## How to turn it on

**In the UI:** in *Clusters → Add Cluster*, the first question is
*"Who manages the ArgoCD connection?"* Pick
*"I do — Sharko only manages addon labels"*.

**Via the API:**

```bash
curl -X POST https://sharko.example.com/api/v1/clusters \
  -H "Authorization: Bearer $SHARKO_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "prod-us",
    "connection_managed_by": "user",
    "addons": {"monitoring": true}
  }'
```

**In Git:** the mode is recorded on the cluster's entry in
`configuration/managed-clusters.yaml`:

```yaml
apiVersion: sharko.dev/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: prod-us
      connectionManagedBy: user   # omit the field (or use "sharko") for the default
      labels:
        monitoring: enabled
```

An absent `connectionManagedBy` means Sharko-managed — every file written
before this field existed keeps working unchanged.

## The secret you create

ArgoCD discovers a cluster through a Kubernetes Secret in the `argocd`
namespace that carries the label `argocd.argoproj.io/secret-type: cluster`.
You create it once; Sharko finds it **by name** — the secret's name must be
exactly your cluster's name as registered in Sharko.

Three common shapes, briefly. The full reference is ArgoCD's own
[declarative setup — clusters](https://argo-cd.readthedocs.io/en/stable/operator-manual/declarative-setup/#clusters)
documentation.

**Bearer token** (service-account token — kind, generic Kubernetes):

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: prod-us                      # MUST match the Sharko cluster name
  namespace: argocd
  labels:
    argocd.argoproj.io/secret-type: cluster
type: Opaque
stringData:
  name: prod-us
  server: https://prod-us.example.com:6443
  config: |
    {
      "bearerToken": "<service-account token>",
      "tlsClientConfig": {
        "insecure": false,
        "caData": "<base64-encoded CA certificate>"
      }
    }
```

**Client certificate** (kubeadm / on-prem kubeconfigs):

```yaml
stringData:
  name: prod-us
  server: https://prod-us.example.com:6443
  config: |
    {
      "tlsClientConfig": {
        "insecure": false,
        "certData": "<base64-encoded client certificate>",
        "keyData": "<base64-encoded client key>",
        "caData": "<base64-encoded CA certificate>"
      }
    }
```

**EKS / IAM** (`argocd-k8s-auth` exec plugin — Sharko parses this shape and
mints a token with its own AWS identity; see
[EKS Hub-and-Spoke Identity](eks-hub-and-spoke-identity.md) for the IAM
roles this depends on):

```yaml
stringData:
  name: prod-us
  server: https://XXXX.gr7.us-east-1.eks.amazonaws.com
  config: |
    {
      "execProviderConfig": {
        "command": "argocd-k8s-auth",
        "args": ["aws", "--cluster-name", "prod-us"],
        "apiVersion": "client.authentication.k8s.io/v1beta1"
      },
      "tlsClientConfig": {
        "insecure": false,
        "caData": "<base64-encoded CA certificate>"
      }
    }
```

Apply with:

```bash
kubectl apply -n argocd -f prod-us-cluster-secret.yaml
```

## Which labels Sharko manages — and which it never touches

On a self-managed connection, the reconcilers converge **addon labels
only**: one label per addon, valued `enabled` or `disabled`, mirrored from
the cluster's entry in `managed-clusters.yaml`. Everything else is yours,
verbatim:

| On your secret                              | Sharko's behavior                     |
| ------------------------------------------- | ------------------------------------- |
| `stringData` / `data` (server, config, credentials) | Never read for writes, never modified |
| `argocd.argoproj.io/secret-type: cluster`   | Never added, never removed — you set it |
| Your own labels (`team: payments`, …)       | Kept as-is                            |
| Annotations                                 | Never modified                        |
| Addon labels (`monitoring: enabled`, …)     | **Managed by Sharko** — merged on every reconcile tick |
| `app.kubernetes.io/managed-by: sharko`      | Never added; a leftover from an earlier Sharko-managed life is **removed** (see mode switching) |

Sharko also never stamps its `sharko.dev/connectivity-check` label on a
connection it does not own.

## When another ArgoCD Application also renders this secret

Some teams manage cluster secrets by committing them to Git and letting a
separate ArgoCD Application sync them into the cluster — Terraform writing
the secret manifest into a repo, with an Application pointed at that path,
is a common shape. If that's how *your* secret gets to the cluster, watch
for two failure modes:

- **`syncOptions: [Replace=true]` on that Application.** A `Replace` sync
  overwrites the whole object on every sync of that Application, wiping
  Sharko's addon labels along with it. Sharko re-applies them on its next
  tick, so you won't lose addon deployment forever, but the two will keep
  stepping on each other.
- **The Application's manifest defines one of Sharko's addon-label keys.**
  If the committed manifest sets, say, `monitoring: disabled` on the
  secret and Sharko wants `monitoring: enabled`, ArgoCD's self-heal keeps
  reverting Sharko's write back to the manifest's value — a silent fight
  that repeats every ~30 seconds.

Sharko surfaces both cases without asking you to go digging first:

- **At adopt or registration**, if the secret already carries an ArgoCD
  tracking marker (the `argocd.argoproj.io/tracking-id` annotation, or the
  `app.kubernetes.io/instance` label, depending on your ArgoCD's
  `trackingMethod`), Sharko names the owning Application in a warning on
  the operation's response — it does not block the adopt or registration.
- **The connection doctor** (`POST /clusters/{name}/doctor`) runs a
  `secret-ownership` check for self-managed connections: pass when no
  foreign marker is found, fail with the owning Application's name and the
  same fix text when one is.
- **The reconciler** keeps a running count of consecutive ticks where a
  label it just wrote comes back with a different value than the one it
  wrote — not merely "changed" (an addon toggle in
  `managed-clusters.yaml` changes what Sharko wants too, and that's never
  flagged). After 2 ticks in a row of the SAME reverted value, the
  cluster's `last_reconcile` outcome stays `succeeded` (Sharko is still
  re-applying its labels every tick) but the message tells you something
  keeps overwriting them.

The fix, in every case, is on the OTHER Application: either drop
`Replace=true` from its `syncOptions`, or stop defining Sharko's addon
label keys in the manifest it renders — let Sharko own those keys and let
the other Application own everything else on the secret.

## Registered but no secret yet?

You can register the cluster in Sharko before creating the secret. Until
the secret exists, the cluster shows as unreachable in ArgoCD and the
reconciler logs a visible waiting state (audit event
`cluster_secret_user_pending`: *"connection is managed by the user;
create the ArgoCD cluster Secret by hand"*). Nothing errors and nothing
retries destructively — the next reconcile tick (every 30 seconds) picks
the secret up as soon as you create it and syncs the addon labels onto it.

## Switching modes

Both directions are a one-line Git edit to the cluster's entry in
`configuration/managed-clusters.yaml` (via a PR, like every other Sharko
change).

**Sharko-managed → self-managed:** add `connectionManagedBy: user` to the
entry. On the next reconcile tick Sharko releases the existing secret: it
removes its `app.kubernetes.io/managed-by: sharko` ownership label (so no
Sharko cleanup can ever delete the secret) and from then on only syncs
addon labels. The credential material Sharko last wrote stays in place —
replace it with your own whenever you like.

**Self-managed → Sharko-managed:** remove the `connectionManagedBy: user`
line. Sharko only takes over a connection it can rebuild, so two things
must be true:

1. The cluster's credentials are available to Sharko — stored in your
   configured secrets backend under the cluster name (or the entry's
   `secretPath`).
2. Your hand-made secret is deleted
   (`kubectl delete secret -n argocd <cluster-name>`). Sharko deliberately
   refuses to overwrite a same-name secret it does not own — that
   protection is what keeps your secrets safe in the other direction.

Within one reconcile tick of the delete, Sharko recreates the secret from
the backend credentials with the right addon labels.

## Removing a self-managed cluster

`DELETE /api/v1/clusters/{name}` with `cleanup: all` removes the cluster
from `managed-clusters.yaml` and cleans up Sharko's own artifacts, but
**leaves your ArgoCD cluster secret in place** — the response says so
explicitly. Delete the secret yourself if you no longer want ArgoCD
connected to that cluster.

## Switching to self-managed and then removing? Flip, wait for a sync, then remove

If you plan to take over a connection **and** remove the cluster from
Sharko, do it in two steps with a pause in between — never in one Git PR:

1. **Flip the mode:** add `connectionManagedBy: user` to the cluster's
   entry and merge that PR.
2. **Wait for one reconcile tick** (up to 30 seconds). This is when Sharko
   removes its `app.kubernetes.io/managed-by: sharko` ownership label from
   the secret — the handover that makes the secret delete-proof. Verify:

   ```bash
   kubectl get secret -n argocd <cluster-name> \
     -o jsonpath='{.metadata.labels.app\.kubernetes\.io/managed-by}'
   ```

   Empty output means the handover happened.
3. **Then remove the cluster** (via the UI, the API, or a Git PR).

**Why the pause matters:** the label handover runs when a reconcile tick
sees the entry in Git with `connectionManagedBy: user`. If one PR both
flips the mode and removes the entry, no tick ever sees the flip — the
entry is just *gone*, and Sharko's orphan sweep sees a secret that still
carries the Sharko ownership label with no Git entry behind it. That is
exactly the pattern the sweep deletes. Your connection would go down with
it.

Two safety nets soften this, but don't skip the pause:

- **Removal through Sharko** (`DELETE /api/v1/clusters/{name}` or the UI)
  reads the mode from the entry *before* removing it and strips the
  ownership label at removal time (the removal response lists a
  `strip_sharko_ownership_label` step). A direct Git edit that deletes the
  entry bypasses this — nothing strips the label, and the sweep can still
  take the secret.
- **Sharko never deletes a secret without its ownership label.** A removal
  retried after its PR merged (entry already gone) refuses the delete and
  reports `skip_argocd_secret_not_sharko_labeled` with a plain-English
  explanation instead — your hand-made secret is not deleted just because
  the entry that said "managed by me" no longer exists.
## Turning off an addon on a self-managed cluster — set `disabled`, don't delete the line

On a Sharko-managed cluster, removing an addon's line from `labels:` is
enough — Sharko replaces the whole label set on every write, so a label with
no corresponding entry just disappears.

**A self-managed cluster is different.** The reconcilers only ever *merge*
addon labels onto your secret — they never remove one, because there is no
reliable way to tell "this label is a stale addon Sharko should clean up"
apart from "this is your own label that happens to look similar." Removing
an addon's line from the cluster's entry in `managed-clusters.yaml` means
Sharko simply stops mentioning that label — it does **not** go back and
delete it from your secret. The label (and whatever it was pointing the
ArgoCD ApplicationSet selector at) stays exactly as it was.

To actually turn an addon off for a self-managed cluster, set its value to
`disabled` instead of deleting the line:

```yaml
apiVersion: sharko.dev/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: prod-us
      connectionManagedBy: user
      labels:
        monitoring: disabled   # turns it off — keep the line
        logging: enabled
```

The next reconcile tick merges `monitoring: disabled` onto your secret, the
ApplicationSet selector stops matching, and the addon's Application prunes —
exactly like the Sharko-managed case. Deleting the `monitoring:` line
instead would leave whatever value was last written (`enabled`, most likely)
untouched on your secret, so the addon would keep deploying.

## Related pages

- [If You Remove Sharko (no lock-in)](removing-sharko.md) — the same
  guest philosophy applied to the whole installation.
- [Cluster Reconciler reference](cluster-reconciler.md) — how the label
  sync loop works.
- [ArgoCD declarative setup — clusters](https://argo-cd.readthedocs.io/en/stable/operator-manual/declarative-setup/#clusters)
  — the authoritative cluster-secret reference.
