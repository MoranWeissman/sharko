# Migrating Off Pasted (Inline) Credentials

A cluster registered with a **pasted (inline) kubeconfig** has a
credential that exists **only in the live ArgoCD cluster Secret**. It is
never stored in Git, so Sharko cannot restore it from Git. If that Secret
is lost, the connection is lost with it. On the connection page such a
cluster shows **Verification incomplete** with the sentence:

> This connection's credential exists only in the live Secret and cannot be
> restored from Git.

This page moves that cluster's credential into a secrets store Sharko can
read: a Kubernetes Secret, or AWS Secrets Manager. After that, Git holds a
pointer to the credential, and Sharko can check and rebuild the whole
connection.

You use only things Sharko already has: the secrets store, a normal pull
request, and the **Repair connection** action. Your addons keep running
the whole time. ArgoCD deploys them from Git, and that does not depend on
Sharko's own access to the cluster.

This exact sequence has not yet been tried end to end on a real cluster.
Each step on its own is part of Sharko today.

## What you need to know before starting

- **Sharko never gives the pasted value back.** No button, API call or log
  line returns the credential from the live Secret. That is on purpose.
  You supply the kubeconfig yourself: use your own copy, or make a fresh
  one from the cluster. A fresh one is the better choice anyway.
- **Nothing happens by itself.** Sharko does not move, convert or delete
  anything on its own. You do every step below. The last step is an admin
  action tied to a commit you reviewed.
- **Nothing is deleted.** The repair changes the live Secret **in place**.
  It does not delete it and create it again. Every label, annotation and
  data key that is not Sharko's own stays as it is.

## Step 1 — store the kubeconfig in a secrets store

Sharko reads cluster credentials from the backend set in
`connection.provider.type`. Put a working kubeconfig for the cluster into
that backend:

- **`k8s-secrets`**: a Kubernetes Secret with the kubeconfig under the key
  `kubeconfig`.
- **`aws-sm`**: a secret in AWS Secrets Manager.

The stored value is the plain kubeconfig YAML.

If `connection.provider.type` is empty or `argocd`, set it to one of
these two first. You can do that in your Helm values or under
**Settings → Secrets Provider**.

For `k8s-secrets`, Sharko reads Secrets in the namespace set by the Helm
value `connection.provider.namespace`. If that is empty, it reads the
namespace `sharko`. Sharko also needs permission to read Secrets there:
the release namespace is always allowed, and any other namespace must be
listed in `rbac.k8sSecretsProviderNamespaces`.

For example, with Sharko installed in `sharko` and the default namespace:

```bash
kubectl create secret generic my-cluster-kubeconfig \
  -n sharko \
  --from-file=kubeconfig=./my-cluster.kubeconfig
```

If you no longer have the original kubeconfig, make a fresh one from the
cluster (for example with a service account token that has the
permissions ArgoCD needs). You do not need to get the old pasted
credential back. You are replacing it.

## Step 2 — point the cluster's Git entry at the stored credential

Open a normal pull request on `managed-clusters.yaml` in your GitOps repo.
For that cluster, change `credsSource` to `secret-kubeconfig`, and set
`secretPath` to the name you stored the kubeconfig under in step 1:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/MoranWeissman/sharko/main/docs/schemas/managed-clusters.v1.json
apiVersion: sharko.dev/v1
kind: ManagedClusters
clusters:
  - name: my-cluster
    credsSource: secret-kubeconfig   # was: inline-kubeconfig
    secretPath: my-cluster-kubeconfig
```

Keep the file in this shape: `apiVersion`, `kind` and `clusters` at the
top, with no `spec:` around them. That is the shape Sharko writes on v4.

Sharko checks the file against its schema when it next reads it, not when
you open the pull request. To check it before you merge, run this on the
changed file, on your machine or in CI:

```bash
sharko validate-config managed-clusters.yaml
```

The change goes through your normal review. Once it merges, the whole
connection is defined in Git.

## Step 3 — check, then apply with Repair connection

After the pull request merges:

1. Open the cluster's connection page and press **Check again**. The
   check only reads; it changes nothing. Now that the credential is in a
   secrets store, the check is complete: it covers every field of the
   connection, the credential too. Sharko builds the Secret that Git
   describes, reads the credential from your secrets store, and compares
   the result with the live Secret, byte for byte. It never shows the
   value.
2. The check will report the live connection as out of sync, because it
   still holds the old pasted credential. Look at the difference.
   Sensitive fields show only their path and a status word, never a
   value.
3. An **admin** applies it with **Repair connection**. The repair uses the
   exact commit shown when you ran the check. If the branch has moved
   since then, it refuses. It changes the Secret in place, and keeps every
   label, annotation and data key that is not Sharko's own.

When the next check comes back clean, the page shows **Connection
synced**. From then on, every check covers the whole connection,
credential included, against what Git says.

## What does NOT happen

- **No automatic move.** Sharko never moves a credential from one place
  to another by itself.
- **No value is ever shown.** Not during the check, not in the repair
  confirmation, not in any response or log.
- **Nothing is deleted.** The live Secret is changed in place. Sharko only
  ever reads the value in your secrets store.

## Related pages

- [Managing Cluster Connections](../user-guide/connections.md#allow-legacy-inline-credentials)
  — the "Allow legacy inline credentials" setting, and why new pasted
  registrations are off by default.
- [Security](security-threat-model.md#secrets-management-recommendations) — the
  advice this migration follows.
- [Managing Clusters](../user-guide/clusters.md#adding-a-cluster) — the
  ways to give Sharko a cluster's credentials when you register it.
