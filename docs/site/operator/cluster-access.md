# What access Sharko and ArgoCD need on your clusters

This page is about the clusters your addons run on (the spoke clusters). It
says what access ArgoCD needs there, how Sharko gets its access, and what
Sharko really does on each cluster.

For the access Sharko has on its own cluster (the one it is installed in),
see [Security → RBAC](security.md#rbac).

## ArgoCD needs wide access on every cluster

ArgoCD is the tool that deploys your addons. Addons create things that
belong to the whole cluster, like namespaces, custom resource definitions
and cluster roles. So the login ArgoCD uses for a cluster needs wide
access there. In practice this is close to cluster admin.

This is normal for any tool that deploys to a cluster. Security teams
already accept it for ArgoCD. Sharko does not change what ArgoCD needs.

## Sharko uses the same access ArgoCD has

Today Sharko does not have its own login for a spoke cluster. When Sharko
talks to a spoke cluster, it uses the same access ArgoCD has:

- **Clusters added with a kubeconfig or a token.** Sharko uses the same
  credential that is in ArgoCD's cluster Secret.
- **EKS clusters that sign in with an IAM role.** Sharko gets a fresh
  token for the same IAM role ArgoCD uses. The token comes from Sharko's
  own AWS identity, but the role, and so the access on the cluster, is the
  same.
- **EKS logins kept in AWS Secrets Manager.** Sharko gets a token and also
  writes that token into ArgoCD's cluster Secret. ArgoCD then uses the
  token Sharko got.

So Sharko does not add a new level of access to your clusters. But it is
a second place that holds that access. And Sharko has more ways in than
ArgoCD's controller does: people log in to it, it has API keys and a REST
API, and it calls out to your Git host, to the marketplace and to the AI
provider if you turned it on.

**Protect the Sharko pod and its namespace the same way you protect
ArgoCD.** Give Sharko a namespace of its own, keep nothing else in it, and
limit who can open a shell in the pod or read Secrets in that namespace.
[A hardened install, step by step](security-hardened-example.md) shows one
full example.

A separate, smaller Sharko login for each cluster is on the list for
later.

## What Sharko itself does on a spoke cluster

Sharko never deploys addons itself. ArgoCD does that. On a spoke cluster,
Sharko only does the things in this list.

- **Reads the Kubernetes version** of the cluster. This is open to any
  logged-in user by default.
- **Puts addon secrets on the cluster.** When an addon in your catalog
  lists secrets it needs, Sharko creates, reads, updates and deletes those
  Secrets in the addon's namespace from the catalog. This happens when you
  register a cluster, when you turn an addon on or off, when you remove or
  unadopt a cluster, and on a timer (every 5 minutes by default) to keep the values
  fresh. Sharko does not create the addon namespaces. It will not change
  or delete a Secret that does not carry its label
  `app.kubernetes.io/managed-by: sharko`.
- **Checks the connection.** **Test connection**, the Connection Doctor,
  registering a cluster and adopting a cluster all run the same check:
  Sharko makes sure the test namespace exists (it creates it if it is
  missing), then writes a small test Secret there, reads it back and
  deletes it. The test namespace is `sharko-test` unless you set
  `SHARKO_TEST_NAMESPACE`. If Sharko created the namespace in this check,
  it deletes it again at the end. A namespace that was already there is
  never deleted. If Sharko cannot remove what it made, the check still
  passes, but the result says what was left behind so you can delete it
  by hand.
- **A preview only reads.** When you preview registering or adopting a
  cluster, Sharko reads the Kubernetes version and checks whether the test
  namespace is there. It writes nothing on the cluster. So a preview does
  not check write access, and it says so: "Write access was not checked
  because this is a preview."
- **Diagnose** (`POST /api/v1/clusters/{name}/diagnose`) lists namespaces,
  reads the test namespace, and writes, reads and deletes a test Secret
  named `sharko-diagnose-test` there.
- **Looks for leftover addon secrets.** After every addon secrets run
  (every 5 minutes by default), Sharko lists the Secrets that carry its
  label. It lists them only in the namespaces where it could have put an
  addon secret, never across the whole cluster. See the next section for
  which namespaces those are. If you choose to delete a leftover one,
  Sharko reads it and deletes it.
- **Shows the addon secrets on a cluster.** The cluster's secrets list
  lists Sharko-labelled Secrets in those same namespaces. If there are
  none, the list is empty and Sharko lists nothing. Opening one addon
  secret reads that one Secret.

Kubernetes cannot limit a list of Secrets to one label. So the right to
list Secrets in a namespace lets the caller read every Secret in that
namespace, values included. Sharko only asks for its own label, but the
access itself is not smaller. That is why Sharko only lists in namespaces
it uses.

### Where Sharko looks for leftover addon secrets

Sharko looks in two kinds of namespaces on each cluster:

- every namespace an addon secret in the catalog names right now, and
- every namespace an addon secret in the catalog named before. Sharko
  keeps this list for each cluster, so that when you remove an addon from
  the catalog, Sharko still looks in its namespace and finds the secret it
  left behind.

A namespace leaves that list once the catalog no longer names it and
Sharko has looked there and found none of its addon secrets.

When Sharko runs in a cluster, it keeps the list in a ConfigMap named
`sharko-addon-secret-namespaces` in its own namespace, so a restart does
not lose it. When it runs outside a cluster (for example on a laptop), the
list is kept in memory only and starts empty again after a restart.

Sharko will not find a leftover secret in these cases:

- The addon was removed from the catalog before you upgraded to a Sharko
  version that keeps this list. Sharko never saw that namespace in the
  catalog, so it does not look there.
- The `sharko-addon-secret-namespaces` ConfigMap was deleted, or Sharko
  ran outside a cluster and restarted, after the addon was removed.
- Someone copied a Sharko secret into a namespace no addon in the catalog
  ever named.

## The smallest access that would cover Sharko's own work

!!! warning "For reference and planning only"
    Sharko cannot use a separate login yet, so this is for reference and
    for planning; do not give this to ArgoCD's login or ArgoCD will stop
    syncing.

This example covers everything in the list above, and nothing more. Make
one `Role` for each addon namespace in your catalog. Sharko does not need
to list Secrets across the whole cluster.

When you remove an addon from the catalog, keep its `Role` until Sharko
has found its leftover secret and you have deleted it. Without the `Role`,
Sharko cannot look in that namespace.

Create the test namespace yourself before you add the `sharko-test` `Role`
below. Sharko never deletes a test namespace it did not create, so the
`Role` stays. If Sharko creates the namespace itself, it deletes it at the
end of the check, and any `Role` inside it goes with it.

```yaml
# Across the whole cluster.
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: sharko-spoke
rules:
  - apiGroups: [""]
    resources: ["namespaces"]
    verbs: ["get", "list", "create", "delete"]   # the connection check and Diagnose
---
# One of these for each addon namespace in the catalog.
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: sharko-addon-secrets
  namespace: datadog                   # the addon's namespace
rules:
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["get", "list", "create", "update", "delete"]   # list: the leftover-secret scan and the secrets list
---
# The test namespace (sharko-test, or your SHARKO_TEST_NAMESPACE).
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: sharko-test
  namespace: sharko-test
rules:
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["get", "create", "delete"]
```

You would also need a `ClusterRoleBinding` and one `RoleBinding` for each
`Role`, all pointing at the login Sharko would use.

## Related pages

- [Security → RBAC](security.md#rbac): the access Sharko has on its own
  cluster.
- [Cluster Connectivity Model](cluster-connectivity-model.md): where Sharko
  reads the credentials for a cluster.
- [Connecting EKS Clusters with IAM](eks-hub-and-spoke-identity.md): the
  IAM roles for ArgoCD, the spoke accounts and Sharko.
- [Install ArgoCD for Sharko](install-argocd.md).
