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
apiVersion: sharko.io/v1
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

**EKS / IAM** (`argocd-k8s-auth` exec plugin):

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

Sharko also never stamps its `sharko.io/connectivity-check` label on a
connection it does not own.

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

## Related pages

- [If You Remove Sharko (no lock-in)](removing-sharko.md) — the same
  guest philosophy applied to the whole installation.
- [Cluster Reconciler reference](cluster-reconciler.md) — how the label
  sync loop works.
- [ArgoCD declarative setup — clusters](https://argo-cd.readthedocs.io/en/stable/operator-manual/declarative-setup/#clusters)
  — the authoritative cluster-secret reference.
