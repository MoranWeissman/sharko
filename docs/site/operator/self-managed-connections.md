# Managing Cluster Connections Yourself

Sharko is a guest on **your** ArgoCD. ArgoCD reaches each cluster through a
Kubernetes Secret called the ArgoCD cluster Secret. By default, when Sharko
registers a cluster, it creates that Secret and keeps its credentials up to
date.

You may want to own that Secret yourself. Maybe it comes from Terraform,
from an external-secrets setup, or from plain `kubectl`. If so, set the
cluster to **"connection managed by the user"**. This is a setting per
cluster.

For a cluster in this mode:

- **Sharko never writes, rotates or deletes the ArgoCD cluster Secret.** Not
  at registration, not in its regular checks, and not when you remove the
  cluster from Sharko.
- **Sharko only manages the addon labels on it.** Sharko reads which addons
  are on for the cluster in `cluster-addons/<cluster>.yaml`. It then adds
  one label per addon to your Secret, and touches nothing else. These labels
  are all ArgoCD needs to deploy the addons.
- **Credentials are optional.** If you give Sharko a kubeconfig at
  registration, it only uses it to test the connection. If you don't,
  registration skips the test and goes straight to writing the Git record.

![Two ownership modes, both ending at the ApplicationSet. When Sharko manages the connection, Sharko creates and rotates the ArgoCD cluster Secret and writes credentials plus addon labels. When you manage it, you create the Secret by hand and Sharko only ever adds addon labels to it.](../assets/diagrams/05-connection-ownership.drawio.svg)

Clusters you **adopt** from an existing ArgoCD get this mode on their own.
They already have a Secret that you or your tools created. That is the
whole point of adopting.

## How to turn it on

**In the UI:** go to **Clusters → Add Cluster**. In the **Connection
managed by** list, pick **Manage a cluster ArgoCD already connects to**.
The default choice is **Sharko (default)**.

**Through the API:**

```bash
curl -X POST https://sharko.example.com/api/v1/clusters \
  -H "Authorization: Bearer $SHARKO_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "prod-us",
    "connection_managed_by": "user"
  }'
```

Registering a cluster never turns on any addons. After registration, turn
addons on one at a time with
`POST /api/v1/v4/clusters/prod-us/addons/<addon>`. Every cluster uses this
same call, whoever manages its connection. If you send an `addons` field
when you register, Sharko ignores it and writes a note in the server log.

**In Git:** the mode is saved on the cluster's entry in
`managed-clusters.yaml`, at the root of your GitOps repo:

```yaml
apiVersion: sharko.dev/v1
kind: ManagedClusters
clusters:
  - name: prod-us
    connectionManagedBy: user   # leave it out (or use "sharko") for the default
    labels:
      team: payments            # your own labels, never an addon name
```

If `connectionManagedBy` is missing, Sharko manages the connection.

The `labels` block in `managed-clusters.yaml` is for **your** own labels,
like team or region. It is not where you turn addons on. Which addons run
on `prod-us` lives in its own file, `cluster-addons/prod-us.yaml`:

```yaml
apiVersion: sharko.dev/v1
kind: ClusterAddons
cluster: prod-us
addons:
  monitoring:
    enabled: true
```

Sharko reads this file and adds the label
`addons.sharko.dev/monitoring: enabled` to your Secret. ArgoCD's
ApplicationSet looks for that label to decide where to deploy the addon.

!!! note "Repo still on the older layout?"
    If your repo has not been through the
    [migration](migrating-to-the-new-format.md) yet, the cluster list lives
    in the repo's `configuration/` folder. On that layout you turn an addon
    on with a plain label right on the cluster's entry
    (`labels: {monitoring: enabled}`), not with a `cluster-addons/` file.
    The rest of this page is the same on both layouts.

## The secret you create

ArgoCD finds a cluster through a Kubernetes Secret in the `argocd`
namespace that has the label `argocd.argoproj.io/secret-type: cluster`. You
create it once. Sharko finds it **by name**: the Secret's name must be
exactly the cluster's name in Sharko.

Here are three common shapes. ArgoCD's own
[declarative setup — clusters](https://argo-cd.readthedocs.io/en/stable/operator-manual/declarative-setup/#clusters)
page has the full reference.

**Bearer token** (a service account token, for kind or any Kubernetes):

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: prod-us                      # MUST match the cluster name in Sharko
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

**Client certificate** (kubeadm or on-prem kubeconfigs):

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

**EKS with AWS IAM** (the `argocd-k8s-auth` helper). Sharko reads the
cluster name and role out of this shape, and gets a token with its own AWS
identity. See [EKS Hub-and-Spoke Identity](eks-hub-and-spoke-identity.md)
for the AWS roles this needs.

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

Apply it with:

```bash
kubectl apply -n argocd -f prod-us-cluster-secret.yaml
```

## Which labels Sharko manages, and which it never touches

When you manage the connection, Sharko only manages **addon labels**. There
is one label for each addon that is on in `cluster-addons/<cluster>.yaml`.
An addon that is off, or not in the file, gets no label. Everything else on
the Secret is yours, and Sharko leaves it exactly as it is:

| On your Secret | What Sharko does |
| --- | --- |
| `stringData` / `data` (server, config, credentials) | Never changes it |
| `argocd.argoproj.io/secret-type: cluster` | Never adds or removes it. You set it. |
| Your own labels (`team: payments`, …) | Leaves them as they are |
| Annotations | Never changes them |
| Addon labels (`addons.sharko.dev/monitoring: enabled`, …) | **Managed by Sharko.** Checked and fixed every 30 seconds. |
| `app.kubernetes.io/managed-by: sharko` | Never adds it. If it is left over from a time when Sharko managed this connection, Sharko **removes** it (see [Switching modes](#switching-modes)). |

Sharko also never adds its `sharko.dev/connectivity-check` label to a
connection it does not own.

## When another ArgoCD Application also renders this secret

Some teams keep cluster Secrets in Git and let a separate ArgoCD
Application put them into the cluster. For example, Terraform writes the
Secret file into a repo, and an Application points at that folder. If your
Secret gets to the cluster this way, watch out for two problems:

- **The Application uses `syncOptions: [Replace=true]`.** A `Replace` sync
  writes the whole Secret again every time that Application syncs. That
  wipes Sharko's addon labels. Sharko puts them back within 30 seconds, so
  your addons do not go away for good, but the two keep undoing each other's
  work.
- **The Application's file sets one of Sharko's addon label keys.** Say the
  file sets `addons.sharko.dev/monitoring: disabled` and Sharko wants
  `enabled`. ArgoCD keeps putting back the value from the file, and Sharko
  keeps writing its own value. This fight repeats about every 30 seconds,
  and nothing tells you unless you look.

Sharko tells you about both problems in three places:

- **When you adopt or register the cluster.** ArgoCD marks the things an
  Application renders. The mark is the `argocd.argoproj.io/tracking-id`
  annotation, or the `app.kubernetes.io/instance` label, depending on how
  your ArgoCD is set up. If the Secret has such a mark, Sharko names the
  owner it found.
    - If the mark clearly shows that another Application renders this exact
      Secret, or the Secret has another tool's ownership label, Sharko
      refuses. Stop the other tool first.
    - If the mark is only a weak sign (it may be left over, or it may be a
      plain Helm release label), Sharko warns you and asks you to confirm
      first.
- **In the [connection doctor](connection-doctor.md).** Its
  `secret-ownership` check looks for these marks on Secrets you manage. It
  passes when there is no mark. It fails, and names the Application, when
  the mark clearly shows another owner. It warns when the sign is weak.
- **In Sharko's regular check.** Every 30 seconds Sharko compares the
  Secret in ArgoCD with what Git says (this is called a
  [reconcile](../glossary.md#reconcile)). Sharko notices when a label it
  just wrote comes back with a different value. A change you made yourself
  in `cluster-addons/<cluster>.yaml` does not count. If the same label is
  undone two checks in a row, the cluster's last check result still says
  `succeeded`, because Sharko is still putting its labels back. But the
  message says that something keeps overwriting them.

In every case the fix is on the OTHER Application. Either remove
`Replace=true` from its `syncOptions`, or stop setting Sharko's addon label
keys in the file it renders. Let Sharko own those keys, and let the other
Application own everything else on the Secret.

!!! warning "`ignoreDifferences` is not the fix"
    You may want to make the fight go quiet with an `ignoreDifferences`
    rule on the other Application, so ArgoCD stops looking at the fields the
    two tools disagree on. Do not do that here. It does not stop the fight.
    Both tools keep writing, the last one to write wins, and you just stop
    seeing it. The only real fix is the one above: make ONE tool the owner
    of each part of the Secret. (`ignoreDifferences` is fine for other
    things, like addons that change their own replica counts. A Secret that
    two tools write is not one of them.)

## Registered but no secret yet?

You can register the cluster in Sharko before you create the Secret. Until
the Secret exists, ArgoCD shows the cluster as unreachable. Sharko writes a
`cluster_secret_user_pending` entry in Activity History that says: *"connection
is managed by the user; create the ArgoCD cluster Secret by hand"*. Nothing
fails and nothing gets deleted. Within 30 seconds of you creating the
Secret, Sharko finds it and adds the addon labels.

## Switching modes

Both ways are a one-line change to the cluster's entry in
`managed-clusters.yaml`, made through a pull request like every other
Sharko change.

**From Sharko-managed to managed by you:** add `connectionManagedBy: user`
to the entry. Within 30 seconds, Sharko lets go of the existing Secret. It
removes its `app.kubernetes.io/managed-by: sharko` label, so no Sharko
cleanup can ever delete the Secret. From then on it only manages addon
labels. The credentials Sharko last wrote stay in place. Replace them with
your own whenever you like.

**From managed by you back to Sharko-managed:** remove the
`connectionManagedBy: user` line. Sharko only takes over a connection it
can rebuild, so two things must be true:

1. Sharko can get the cluster's credentials. They must be stored in your
   credentials backend under the cluster name (or under the entry's
   `secretPath`).
2. You have deleted your own Secret
   (`kubectl delete secret -n argocd <cluster-name>`). Sharko will not
   overwrite a Secret with the same name that it does not own. That same
   rule is what keeps your Secrets safe the other way round.

Within 30 seconds of the delete, Sharko creates the Secret again from the
stored credentials, with the right addon labels.

## Removing a cluster whose connection you manage

`DELETE /api/v1/clusters/{name}` (with `cleanup=all`, the default) removes
the cluster from `managed-clusters.yaml` and cleans up what Sharko made.
But it **leaves your ArgoCD cluster Secret in place**, and the response
says so. Delete the Secret yourself if you no longer want ArgoCD connected
to that cluster.

## Switching to managed by you and then removing? Switch, wait, then remove {#switching-to-self-managed-and-then-removing-flip-wait-for-a-sync-then-remove}

If you want to take over a connection **and** remove the cluster from
Sharko, do it in two steps with a short wait between them. Never do both in
one pull request.

1. **Switch the mode:** add `connectionManagedBy: user` to the cluster's
   entry and merge that pull request.
2. **Wait up to 30 seconds.** In that time Sharko removes its
   `app.kubernetes.io/managed-by: sharko` label from the Secret. After that,
   Sharko can no longer delete it. Check it:

   ```bash
   kubectl get secret -n argocd <cluster-name> \
     -o jsonpath='{.metadata.labels.app\.kubernetes\.io/managed-by}'
   ```

   If this prints nothing, the label is gone.
3. **Then remove the cluster** (in the UI, through the API, or with a
   pull request).

**Why the wait matters.** Sharko removes its label when its regular check
sees the entry in Git with `connectionManagedBy: user`. If one pull request
switches the mode and removes the entry at the same time, no check ever sees
the switch. The entry is just gone. Sharko then finds a Secret that still
has its label but no entry in Git, and it deletes Secrets like that. Your
connection would go down with it.

Two safety nets help, but don't skip the wait:

- **Removing through Sharko** (`DELETE /api/v1/clusters/{name}` or the UI)
  reads the mode from the entry *before* it removes it, and removes Sharko's
  label at that moment. The response lists a `strip_sharko_ownership_label`
  step. Deleting the entry straight in Git skips this. Nothing removes the
  label, and Sharko can still delete the Secret.
- **Sharko never deletes a Secret that does not have its label.** Say you
  try a removal again after its pull request merged, so the entry is
  already gone. Sharko refuses to delete, reports
  `skip_argocd_secret_not_sharko_labeled`, and explains why in plain words.
  Your own Secret is not deleted just because the entry that said "managed
  by the user" is gone.

## Turning off an addon on a cluster whose connection you manage

Edit `cluster-addons/prod-us.yaml`. Either delete the addon's block, or set
`enabled: false`:

```yaml
apiVersion: sharko.dev/v1
kind: ClusterAddons
cluster: prod-us
addons:
  monitoring:
    enabled: false   # or remove this block. Both work.
  logging:
    enabled: true
```

Either way, within 30 seconds Sharko removes the
`addons.sharko.dev/monitoring` label from your Secret. The ApplicationSet
then stops matching the cluster, and ArgoCD removes the addon. This is the
same as on a cluster Sharko manages.

An addon that is missing from `addons:` and an addon with `enabled: false`
mean the same thing. So Sharko removes the label in both cases. (The
`labels:` block in `managed-clusters.yaml` works differently. Sharko only
ever adds those labels to your Secret, and never removes one.)

!!! note "Repo still on the older layout?"
    On a repo that has not been [migrated](migrating-to-the-new-format.md)
    yet, this is different. Sharko only ever *adds* addon labels to your
    Secret and never removes one, because it cannot tell an old addon label
    apart from one of your own labels. So deleting an addon's line from
    `labels:` in the old cluster list does nothing. Set it to `disabled` and
    keep the line instead. Once the repo is migrated, this goes away.

## Related pages

- [If You Remove Sharko (no lock-in)](removing-sharko.md) — the same "guest
  on your ArgoCD" idea, for the whole install.
- [Connection Doctor](connection-doctor.md) — all six checks, including
  `secret-ownership`, and how to run them.
- [Cluster Reconciler](cluster-reconciler.md) — how Sharko's regular check
  works, and the **Refresh** and **Sync addon labels** buttons.
- [ArgoCD declarative setup — clusters](https://argo-cd.readthedocs.io/en/stable/operator-manual/declarative-setup/#clusters)
  — ArgoCD's own reference for cluster Secrets.
