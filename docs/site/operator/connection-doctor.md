# Connection Doctor

> **Reference page, not a runbook.** This page explains what the
> connection doctor checks, what each pass/fail/not-applicable verdict
> means, and how to act on a failure. If you already know which check
> failed and just need the fix, jump to the matching section below. For
> the general credential model these checks exercise, see [Cluster
> Connectivity Model](cluster-connectivity-model.md).

`POST /api/v1/clusters/{name}/doctor` runs five real-attempt checks
against a cluster's connection and returns a structured verdict for
each — no guessing, no IAM policy simulation, an actual fetch / read /
assume / write against the real system every time. In the UI, it's the
**Run connection doctor** button on the cluster detail page.

## Why "real attempt" matters

Sharko already has an IAM policy simulator (the Diagnose tool,
`internal/diagnose`) that tells you what a policy document *should*
allow. The doctor is deliberately different: every check is a live
action against the live system — fetch the credentials, read the
secret paths, assume the role, create-and-delete a test Secret on the
cluster. A policy simulator can say "this looks right" and still be
wrong (a typo'd role ARN, a secret that was deleted five minutes ago,
an EKS access entry nobody added). The doctor can't be fooled by a
correct-looking policy — it either works or it doesn't.

## How to run it

**In the UI:** cluster detail page → **Run connection doctor**.

**Via the API:**

```bash
curl -X POST https://sharko.example.com/api/v1/clusters/prod-us/doctor \
  -H "Authorization: Bearer $SHARKO_TOKEN"
```

Response shape:

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
      "detail": "Sharko's connection test on cluster \"prod-us\" failed: ...",
      "fix": "The role works in AWS, but the cluster doesn't trust it yet — add an EKS access entry (or aws-auth mapping) for this role."
    },
    {
      "id": "secret-ownership",
      "status": "not-applicable",
      "detail": "Cluster \"prod-us\"'s connection secret is managed by Sharko directly — foreign-ownership checks only apply to self-managed (user-owned) connections."
    }
  ],
  "overall": "partial"
}
```

The whole run is bounded to about 30 seconds; each check individually
is bounded to about 10 seconds so one slow dependency can't stall the
rest.

## Reading the overall verdict

| Overall | Meaning |
|---------|---------|
| `pass` | Nothing failed. Checks may still be `not-applicable` (that's expected — see below) but none returned `fail`. |
| `partial` | At least one check passed and at least one failed. The connection works for *something* but not everything — read each check individually. |
| `fail` | Every check that ran returned `fail`. Nothing about this connection currently works. |

`not-applicable` never counts toward `pass` or `fail` in that rollup —
it means "this check doesn't apply to this cluster's setup," not
"untested." A cluster with no secret-bearing addons enabled will
routinely show `addon-secret-paths: not-applicable` inside an
otherwise-`pass` overall verdict, and that's the expected, healthy
shape.

## The five checks

Each check below is independent — a failure in one does not stop the
others from running (the response always gives you the fullest
picture available), with one real dependency: **cluster-access** needs
the credentials **connection-credentials** fetched to build a client
at all, so it reports `not-applicable` rather than failing outright
when those credentials never arrived.

### 1. `connection-credentials` — can Sharko read this cluster's connection credentials?

The same fetch path the Test connection button uses
(`fetchClusterCredentials`) — no new logic invented for the doctor.
**Pass** means Sharko successfully read the credentials from wherever
they're configured to live (a secrets backend, or the ArgoCD cluster
Secret itself).

**Common fix guidance on failure:**

- No secrets backend or ArgoCD connection configured at all → configure
  one in **Settings → Connections**.
- ArgoCD rejects the read with an IAM-required error → give Sharko's
  own AWS identity (IRSA / EKS Pod Identity) permission to mint a
  token for this cluster (`sts:AssumeRole` on the named role, or
  direct EKS access if no role is set).
- The cluster's ArgoCD Secret uses an exec-plugin Sharko doesn't
  support → re-register the connection with a supported auth method
  (bearer token, client certificate, or AWS IAM). Sharko never runs
  exec-plugin binaries — see [Cluster Connectivity
  Model](cluster-connectivity-model.md#what-still-needs-additional-setup).

### 2. `addon-secret-paths` — can Sharko read every secret path this cluster's enabled addons need?

For every addon enabled on the cluster whose catalog entry declares a
`secrets:` block, this check reads (never writes) every provider path
those secrets reference — the identical read path the secrets
reconciler itself uses to push addon secrets. **Not-applicable** when
the cluster isn't in `managed-clusters.yaml` yet, or when none of its
enabled addons declare any secrets at all — most clusters will show
this routinely.

**Common fix guidance on failure:**

- No active Git connection → connect a Git repository in **Settings →
  Connections**, then re-run the doctor.
- One or more paths unreadable → the failure message names the first
  failing addon, key, and path. Check that path exists in the secrets
  backend and that Sharko's identity can read it.

### 3. `assume-role` — if a cross-account IAM role is in play, can Sharko actually assume it?

**Not-applicable** when this cluster's connection doesn't involve a
cross-account role at all (a plain bearer-token or client-cert
cluster, or an EKS cluster reachable with Sharko's own identity with
no role to assume). When a role *is* in play, this check performs a
real STS `AssumeRole` call.

**Fix on failure:** in AWS, check that the named role's trust policy
includes Sharko's own IAM identity, and that Sharko's identity has
`sts:AssumeRole` permission on it. See [EKS Hub-and-Spoke
Identity](eks-hub-and-spoke-identity.md) for the full IAM recipe.

### 4. `cluster-access` — does the cluster itself accept the credentials Sharko holds?

Reuses the exact same secret create/read/delete cycle (`verify.Stage1`)
the Test connection button runs — not a second, different probe.
**Not-applicable** when check 1 never produced usable credentials
(nothing to test with — see check 1's result instead). **Pass** means
Sharko created a test Secret on the target cluster, read it back, and
deleted it — proof the connection works end to end, not just that
credentials exist.

**Fix on failure:**

- Generic case: check Sharko's RBAC permissions on the cluster (the
  Diagnose tool gives a namespace-level permission breakdown).
- **The specific case worth calling out by name:** if check 3
  (`assume-role`) just passed and THIS check still fails, the fix text
  says exactly that —
  > *"The role works in AWS, but the cluster doesn't trust it yet —
  > add an EKS access entry (or aws-auth mapping) for this role."*
  This is the single most common EKS setup gap: the IAM side is
  correctly wired (AWS lets Sharko assume the role), but nobody told
  the *cluster* to trust that role's identity. AWS IAM and Kubernetes
  RBAC are two separate trust boundaries — passing one says nothing
  about the other. Add an EKS access entry (or an `aws-auth` ConfigMap
  mapping on clusters that predate access entries) for the role named
  in the earlier check, then re-run the doctor.

### 5. `secret-ownership` — is another ArgoCD Application also rendering this cluster's connection secret?

Only meaningful for a **self-managed** connection
(`connectionManagedBy: user` — see [Managing Cluster Connections
Yourself](self-managed-connections.md)). **Not-applicable** for a
Sharko-managed connection (Sharko is the Secret's sole writer there —
a foreign tracking marker on a Sharko-owned Secret would be a
different, out-of-scope problem) and for a self-managed connection
whose Secret the user hasn't created yet. **Pass** means the Secret
carries no ArgoCD tracking markers from another Application. **Fail**
means it does — the failure names the owning Application.

This is the same detection call (`argosecrets.Manager.GetTrackingOwner`)
that Adopt and Register already use for their upfront warning, so the
doctor and that warning can never disagree about what counts as a
foreign owner.

**Fix on failure:** in the named Application's manifest, make sure it
doesn't define Sharko's addon-label keys and doesn't use the
`Replace=true` sync option — otherwise the two will keep fighting over
this Secret. The full failure-mode writeup, including the reconciler's
label-fight detection that catches an *ongoing* fight even when this
check itself is run only once, lives in [Managing Cluster Connections
Yourself → When another ArgoCD Application also renders this
secret](self-managed-connections.md#when-another-argocd-application-also-renders-this-secret).

## Related pages

- [Cluster Connectivity Model](cluster-connectivity-model.md) — the
  credential-selection model the first four checks exercise.
- [Managing Cluster Connections Yourself](self-managed-connections.md)
  — self-managed connections, and the foreign-ownership failure mode
  check 5 detects.
- [EKS Hub-and-Spoke Identity](eks-hub-and-spoke-identity.md) — the IAM
  role/trust-policy recipe behind checks 3 and 4.
- [Cluster Reconciler reference](cluster-reconciler.md) — per-cluster
  `last_reconcile` visibility and the manual "Sync now" trigger, for
  when the connection is fine but the addon labels aren't converging.
