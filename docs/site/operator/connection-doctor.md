# Connection Doctor

The connection doctor runs six checks on one cluster's connection and tells
you, for each one, whether it passed. Use it when **Test connection** fails
and you want to know *which* part is broken.

Each check really does the thing. It fetches the credentials, reads the
secret paths, assumes the AWS role, and writes a test Secret on the cluster.
It does not just look at a policy and guess. A policy can look right and
still be wrong: a typo in a role ARN, a secret someone deleted five minutes
ago, or an EKS access entry nobody added. The doctor finds those, because it
either works or it doesn't.

For how Sharko picks credentials in the first place, see
[Cluster Connectivity Model](cluster-connectivity-model.md).

## How to run it

**In the UI:** open the cluster, go to the **Diagnostics** tab, and under
**Diagnose connection** click **Run** (or **Re-run**).

**Through the API:**

```bash
curl -X POST https://sharko.example.com/api/v1/clusters/prod-us/doctor \
  -H "Authorization: Bearer $SHARKO_TOKEN"
```

The answer looks like this:

```json
{
  "checks": [
    {
      "id": "connection-credentials",
      "status": "pass",
      "detail": "Sharko can read the connection credentials for cluster \"prod-us\"."
    },
    {
      "id": "addon-secret-paths",
      "status": "not-applicable",
      "detail": "No addon enabled on this cluster declares any secrets, so there is nothing to check."
    },
    {
      "id": "assume-role",
      "status": "pass",
      "detail": "Sharko successfully assumed role \"arn:aws:iam::123456789012:role/SharkoSpokeRole\"."
    },
    {
      "id": "cluster-access",
      "status": "fail",
      "detail": "...",
      "fix": "The role works in AWS, but the cluster doesn't trust it yet — add an EKS access entry (or aws-auth mapping) for this role."
    },
    {
      "id": "secret-ownership",
      "status": "not-applicable",
      "detail": "Cluster \"prod-us\"'s connection secret is managed by Sharko directly — foreign-ownership checks only apply to self-managed (user-owned) connections."
    },
    {
      "id": "connectivity-app-drift",
      "status": "not-applicable",
      "detail": "..."
    }
  ],
  "overall": "partial"
}
```

The whole run stops after about 30 seconds. Each check stops after about 10
seconds, so one slow check can't hold up the others.

Only check 4 changes anything: it creates a test Secret on the cluster,
reads it back and deletes it. If the test namespace was not there, it
creates it and deletes it again at the end. The other checks only read.

### The doctor is not the same as "Check permissions"

The **Diagnostics** tab also has **Check permissions**
(`POST /api/v1/clusters/{name}/diagnose`). That one looks at Sharko's
Kubernetes permissions on the cluster, namespace by namespace, and suggests
RBAC changes. Use the doctor first to find which part is broken. Use
**Check permissions** when the doctor says Sharko got in but is not allowed
to do something.

## What each status means

Each check comes back with one of four statuses:

| Status | Meaning |
| --- | --- |
| `pass` | The check worked. |
| `fail` | The check failed. The `fix` field says what to do. |
| `warn` | Something looks wrong, but Sharko is not sure. Read the `fix` field. |
| `not-applicable` | This check does not apply to how this cluster is set up. It is not a problem. |

## Reading the overall result

| Overall | Meaning |
| --- | --- |
| `pass` | Nothing failed and nothing warned. Some checks may be `not-applicable`. That is normal. |
| `partial` | Some checks passed and some failed. Or nothing failed, but at least one check warned. Read each check. |
| `fail` | Every check that ran failed, and none passed. Nothing about this connection works right now. |

`not-applicable` never counts toward the overall result. A cluster with no
addons that need secrets will usually show `addon-secret-paths:
not-applicable` and still get `pass` overall. That is healthy.

## The six checks

The checks run on their own. One failing does not stop the others, so you
always get the fullest picture. The one exception: check 4 needs the
credentials from check 1. If check 1 failed, check 4 is skipped and says so.

### 1. `connection-credentials` — can Sharko read this cluster's connection credentials?

This uses the same steps as the **Test connection** button. **Pass** means
Sharko read the credentials from wherever they are kept: a credentials
backend, or the ArgoCD cluster Secret itself.

**If it fails:**

- Sharko has no credentials backend or ArgoCD connection set up. Set one up
  in **Settings → Connections**.
- The cluster uses AWS IAM and Sharko could not get a token. Give Sharko's
  own AWS identity (IRSA or EKS Pod Identity) the right to get a token for
  this cluster: `sts:AssumeRole` on the named role, or direct EKS access if
  no role is set. See [AWS IAM Cluster Auth](aws-iam-cluster-auth.md).
- The cluster's ArgoCD Secret uses an exec plugin that Sharko does not
  support. Sharko never runs exec plugin programs. Set up the connection
  again with a bearer token, a client certificate, or AWS IAM. See
  [Cluster Connectivity Model](cluster-connectivity-model.md#what-still-needs-additional-setup).

### 2. `addon-secret-paths` — can Sharko read every secret the cluster's addons need?

Some addons in the catalog list secrets they need (in a `secrets:` block).
For every such addon that is on for this cluster, this check reads each
secret path from your secrets backend. It never writes. It reads the same
way Sharko does when it puts addon secrets on the cluster.

**Not-applicable** when the cluster is not in `managed-clusters.yaml` yet,
or when none of its addons need secrets. Most clusters show this.

**If it fails:**

- Sharko has no active Git connection. Connect a Git repository in
  **Settings → Connections**, then run the doctor again.
- One or more paths can't be read. The message names the first addon, key
  and path that failed. Check that the path exists in your secrets backend,
  and that Sharko's identity is allowed to read it.

### 3. `assume-role` — if the cluster uses an AWS role, can Sharko assume it?

**Not-applicable** when the connection uses no AWS role from another
account. That covers bearer-token and client-certificate clusters, and EKS
clusters Sharko reaches with its own identity and no role. When there is a
role, this check really calls AWS STS `AssumeRole`.

**If it fails:** in AWS, check that the role's trust policy includes
Sharko's own AWS identity, and that Sharko's identity is allowed
`sts:AssumeRole` on it. See
[EKS Hub-and-Spoke Identity](eks-hub-and-spoke-identity.md) for the full
setup.

### 4. `cluster-access` — does the cluster accept Sharko's credentials?

This does the same test as the **Test connection** button. **Pass** means
Sharko created a test Secret on the cluster, read it back, and deleted it.
If Sharko could not remove a test object afterwards, the check still
passes and its detail says what was left behind.
That proves the connection works all the way through, not just that the
credentials exist. If check 1 failed, this check is skipped, because there
is nothing to test with.

**If it fails:**

- In most cases, check Sharko's RBAC permissions on the cluster.
  **Check permissions** on the **Diagnostics** tab shows them namespace by
  namespace.
- **If check 3 passed and this check failed**, the fix text says:
  *"The role works in AWS, but the cluster doesn't trust it yet — add an EKS
  access entry (or aws-auth mapping) for this role."*
  This is the most common EKS setup mistake. AWS lets Sharko assume the
  role, but nobody told the *cluster* to trust that role. AWS IAM and
  Kubernetes RBAC are two separate gates, and passing one says nothing about
  the other. Add an EKS access entry for the role from check 3 (or an
  `aws-auth` ConfigMap entry on older clusters), then run the doctor again.

### 5. `secret-ownership` — does another ArgoCD Application also write this cluster's connection Secret?

This only matters when **you** manage the cluster's connection
(`connectionManagedBy: user`, see
[Managing Cluster Connections Yourself](self-managed-connections.md)).

- **Not-applicable** when Sharko manages the connection, or when you have
  not created the Secret yet.
- **Pass** when the Secret has no ArgoCD marks from another Application.
- **Fail** when a mark clearly shows that another Application renders the
  Secret. The message names that Application.
- **Warn** when the sign is weak, for example only an
  `app.kubernetes.io/instance` label, which may come from a plain Helm
  release. The message names what it found.

Adopt and Register use the same test. So the doctor and those steps always
agree on what counts as another owner.

**If it fails or warns:** in the named Application's files, make sure it
does not set Sharko's addon label keys, and does not use the `Replace=true`
sync option. Do not add an `ignoreDifferences` rule instead. That hides the
fight but does not stop it. The full story is in
[Managing Cluster Connections Yourself → When another ArgoCD Application also renders this secret](self-managed-connections.md#when-another-argocd-application-also-renders-this-secret).

### 6. `connectivity-app-drift` — is the connectivity check app where it should be?

A cluster with no addons gets a small placeholder app from ArgoCD, called
the connectivity check. It proves ArgoCD can really reach the cluster. The
cluster's Secret gets the label `sharko.dev/connectivity-check: enabled`
for this. This check makes sure the label and the app line up.

- **Not-applicable** when the cluster does not have that label, when ArgoCD
  has no Secret for it yet, or when ArgoCD has not confirmed the connection
  yet. It is also not-applicable when a real addon is deployed, because the
  placeholder app steps aside then.
- **Pass** when the label is there and the app exists in ArgoCD.
- **Fail** when Sharko manages the connection, the cluster has no addons,
  and the label is missing. Sharko adds this label by itself. If it does
  not show up, check that Sharko's cluster connection part is running (see
  [Cluster Reconciler](cluster-reconciler.md)).
- **Warn** when the cluster is connected, has the label and no addons, but
  the app is missing.

**If it warns:** the connectivity check comes inside the `sharko-engine`
chart (since chart version 0.3.0). Check that the engine Application
(`sharko-engine.yaml`) is healthy and uses chart version 0.3.0 or newer. If
it is older, merge the engine upgrade pull request, then run the doctor
again.

## Related pages

- [Cluster Connectivity Model](cluster-connectivity-model.md) — how Sharko
  picks credentials, which checks 1 to 4 test.
- [Managing Cluster Connections Yourself](self-managed-connections.md) —
  connections you manage, and the problem check 5 looks for.
- [EKS Hub-and-Spoke Identity](eks-hub-and-spoke-identity.md) — the AWS
  role and trust policy setup behind checks 3 and 4.
- [Cluster Reconciler](cluster-reconciler.md) — the last check result for
  each cluster, and the **Refresh** and **Sync addon labels** buttons, for
  when the connection is fine but the addon labels are not right.
