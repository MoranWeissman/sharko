# Permissions and Blast Radius

This page lists every permission Sharko holds, what each one is for, and what an attacker who gets hold of one could reach — so you can grant least privilege and know exactly where the boundaries are.

The one sentence to carry away from this page:

> A compromised Sharko pod may access every secret permitted by its configured backend identity. Backend IAM/RBAC boundaries are therefore part of Sharko's security boundary.

Sharko's own application roles (viewer / operator / admin) control who may *ask* Sharko to do things. They cannot shrink what the pod itself could read if it were compromised — only the backend's IAM policy or Kubernetes RBAC can do that. Do not treat application RBAC as a substitute for a tight backend identity.

## The permissions table

| Identity / boundary | What it grants | Used for | Blast radius if abused |
|---|---|---|---|
| **Hub ServiceAccount — ClusterRole** (`charts/sharko/templates/rbac.yaml`) | Read-only `get/list/watch` on ArgoCD `applications`, `appprojects`, `applicationsets`; optionally `get/list` on nodes (`config.nodeAccess`, default on) | Fleet status, drift views, takeover preflight | Read ArgoCD's view of the fleet. No Secret access — the old cluster-wide Secret-read rule was removed (least privilege, story 152.F) |
| **Hub ServiceAccount — Role in the ArgoCD namespace** | Full CRUD on Secrets in that one namespace; `patch/delete` on ApplicationSets and `patch` on Applications (used only by the v3→v4 migration) | Writing and repairing cluster-connection Secrets | Rewrite cluster connections in the ArgoCD namespace — hub-only, one namespace |
| **Hub ServiceAccount — Role in the release namespace** | `get/update` on Sharko's own named Secrets (auth, connections, AI config, initial-admin, API tokens, migration settings); blanket `create` on Secrets (Kubernetes cannot restrict `create` by name); ConfigMaps; `create` on Events | Sharko's own state: encrypted connection settings, bcrypt-hashed users and tokens | Sharko's own configuration and credential store, one namespace |
| **Hub ServiceAccount — Role per configured backend namespace** | Read-only `get/list` on Secrets in each namespace listed in `rbac.k8sSecretsProviderNamespaces` (plus the release namespace) | The Kubernetes secrets backend reading cluster credentials and addon-secret values | Read every Secret in those configured namespaces — this IS the backend identity for the K8s backend; keep the list short |
| **AWS IRSA role** (annotation on the ServiceAccount, `serviceAccount.annotations`) | Whatever the IAM policy grants; Sharko needs `secretsmanager:GetSecretValue` (plus `eks:DescribeCluster` and optionally `sts:AssumeRole` for EKS cluster credentials) | The AWS Secrets Manager backend | Read every secret the IAM policy allows — this IS the backend identity for AWS; scope it to the prefix (example below) |
| **AWS prefix boundary** (enforced in code, `internal/providers/boundary.go`) | Addon-secret value reads only under the configured secret-name prefix; an **empty prefix refuses every read** | Keeping addon-secret reads inside the area you configured, even if IAM is wider | A caller cannot use Sharko to read outside the prefix — but this is defense in depth, not a replacement for a scoped IAM policy |
| **Kubernetes backend namespace boundary** (enforced in code) | Addon-secret value reads only inside the one configured namespace; a path naming another namespace is refused before any API call | Same, for the K8s backend | Same — code-level; the RBAC Role above is the real wall |
| **Remote-cluster credentials** (fetched from the backend per operation, never stored by Sharko) | Whatever the kubeconfig or token grants on that cluster | Pushing destination Secrets; listing Sharko-managed Secrets; deleting a disabled addon's Secrets on request, and confirmed leftovers | Whatever that credential can do on that cluster — scope it to Secrets in the addon namespaces (example below) |
| **ArgoCD's own permissions** | ArgoCD deploys addon workloads; Sharko never deploys directly | Deployment, sync, self-heal | ArgoCD's blast radius is its own — see your ArgoCD RBAC |

## Network paths Sharko needs

All outbound from the Sharko pod, all TLS:

- **Your Git host** (HTTPS) — reading data files, opening pull requests.
- **The hub Kubernetes API** — its own Secrets/ConfigMaps and the ArgoCD namespace.
- **Each managed cluster's Kubernetes API** (certificate-verified HTTPS; Sharko refuses to send a secret value over a skip-verify or plain-http connection).
- **AWS Secrets Manager** regional endpoint (only with the AWS backend).

Sharko listens on one port for its UI/API and needs nothing inbound from remote clusters — they never call back.

## Who can trigger what

All API access requires authentication; these are the minimum roles per action:

| Action | Minimum role |
|---|---|
| See Secret Sync rows and status (`GET /api/v1/system/managed-secrets`, `GET /api/v1/secrets/status`) | viewer |
| Manual sync — everything (`POST /api/v1/secrets/reconcile`), fleet-wide read-only check (`POST /api/v1/secrets/check`), one cluster (`POST /api/v1/clusters/{name}/secrets/refresh`), one row's Refresh/Sync | operator |
| Delete a confirmed leftover ("orphaned") Secret | operator |
| Switch the Secret Sync engine on or off (Settings) | admin |

A manual trigger can only deliver what Git already defines — see [the data flow](engine-and-secret-sync.md#secret-sync-the-data-flow-boundary-by-boundary). And when the destination Secret exists without Sharko's `app.kubernetes.io/managed-by: sharko` label, every write path refuses with a fixed sentence: *"this secret already exists on the cluster and Sharko did not create it, so Sharko will not change or remove it"* — a Secret managed by another tool is a boundary, not a target.

## Least-privilege examples

### AWS Secrets Manager backend

Scope the IRSA role's policy to the prefix you configure on the connection (here `sharko/addons/`), and nothing else:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": "secretsmanager:GetSecretValue",
      "Resource": "arn:aws:secretsmanager:<region>:<account-id>:secret:sharko/addons/*"
    }
  ]
}
```

Then set the same value as the connection's prefix, so the code boundary and the IAM boundary agree. Sharko refuses any read outside the prefix before calling AWS — and refuses *all* reads if the prefix is left empty — but the IAM policy is what bounds a compromised pod.

### Kubernetes Secrets backend

Give Sharko read access to exactly one namespace that holds only the secrets Sharko should deliver (here `sharko-addon-secrets`):

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: sharko-secrets-provider
  namespace: sharko-addon-secrets
rules:
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["get", "list"]
```

The Sharko chart generates this Role/RoleBinding pair for every namespace in `rbac.k8sSecretsProviderNamespaces`. Configure the connection's namespace to match, and don't mix unrelated secrets into it: everything in that namespace is inside Sharko's backend identity.

### Remote-cluster credential for destination writes

The kubeconfig or token stored in the backend for each cluster should be a ServiceAccount scoped to Secrets in the namespaces addons actually use — not `cluster-admin`:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: sharko-secret-sync
  namespace: monitoring   # one per addon namespace Sharko delivers into
rules:
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["get", "list", "create", "update", "delete"]
```

`delete` is only used by explicitly requested operations — disabling an addon with full cleanup, updating a cluster's addons, or the confirmed leftover delete — and every one of those goes through the same ownership gate. The scheduled pass never deletes anything.

## Where to go next

- [The Engine and Secret Sync](engine-and-secret-sync.md) — the full data flow these permissions serve.
- [Threat Model](threat-model.md) — attacker footholds and residual risk.
- [Secret Sync Debugging](../operator/secret-sync-debugging.md) — every refusal above, as it looks when you hit it.
