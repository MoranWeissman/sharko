# Threat Model

This page states plainly what an attacker gets at each foothold in a Sharko deployment, which boundaries stop them, and which risks remain after everything Sharko enforces. It describes the system as currently shipped — every claim here is checked against the code, not against intentions.

If you haven't yet, read [The Engine and Secret Sync](engine-and-secret-sync.md) first — this page leans on the data flow and the three-way split (engine chart / cluster-connection credentials / addon Secret Sync) explained there.

## Trust boundaries

Sharko sits between four things it does not control, and each edge is a boundary with its own checks:

| Boundary | What crosses it | What enforces it |
|---|---|---|
| **Sharko ↔ your Git repo** | Data files (catalog, assignments, values, engine pin) — never secret values. Writes go through pull requests | Your Git host's own auth and review rules; schema validation on every read |
| **Sharko ↔ the secrets backend** | Secret values, read-only, fetched per pass | The backend identity (IAM policy / K8s RBAC) plus the in-code prefix/namespace boundary — see [Permissions and Blast Radius](permissions-and-blast-radius.md) |
| **Sharko ↔ each managed cluster** | Destination Secrets, over certificate-verified HTTPS only | The TLS refusal (no value over skip-verify or plain http) and the ownership gate (no write to a Secret Sharko didn't create) |
| **Users ↔ Sharko** | API calls through the UI, CLI, or REST | Authentication on every endpoint; viewer/operator/admin roles per action; audit on writes |

## What an attacker gets at each foothold

**A leaked viewer login.** Read access to fleet state and delivery records: names, namespaces, statuses, timestamps, canned error sentences. No secret values — the API's secret views return key names with every value blanked server-side, and error text shown to users is mapped to fixed sentences before it leaves the server. They cannot trigger a sync.

**A leaked operator login or API token.** Everything above, plus manual sync triggers and leftover-Secret deletion. The important limit: a manual trigger can only deliver what Git already defines — the request carries no backend path and no destination, so an operator credential is not a "read anything from the backend" credential. The worst honest summary: they can make Sharko re-deliver Git-approved secrets to their Git-approved destinations, and delete Sharko-owned Secrets that nothing references anymore. Every action lands in the audit log under the token's name.

**A leaked admin login.** Control of Sharko's configuration: users, connections, the secrets-backend settings, the engine's on/off switch. An admin can point the backend connection at a different namespace or prefix — but the backend identity (IAM/RBAC) still bounds what the pod can actually read, which is why that identity must be scoped tightly. Admin actions are audited.

**Write access to your Git repo.** The strongest application-level foothold, by design — Git is the source of truth. Someone who can merge to the data-file branch can enable addons, change values, and change *where* a secret's value is read from (the backend path in a push definition) or where it lands. They still cannot exfiltrate a value through Sharko: the delivery only writes into clusters, boundary checks keep reads inside the configured prefix/namespace, and no API response returns values. Your defense is your Git review process — treat the data-file repo's merge rights like production access, because they are.

**A network position between Sharko and a cluster.** Nothing. Sharko refuses to send a value over a connection that skips certificate checks or uses plain http, so a man-in-the-middle can't downgrade a delivery into readable traffic; they can only make deliveries fail, which shows up as errors on the Secrets pages.

**A compromised secrets backend.** Game over for the secrets in it, with or without Sharko — the backend is where values live. Sharko neither adds to nor subtracts from that risk; it holds no second copy to steal (no cache, no persistence of values).

**A compromised Sharko pod.** The honest worst case:

> A compromised Sharko pod may access every secret permitted by its configured backend identity. Backend IAM/RBAC boundaries are therefore part of Sharko's security boundary.

The pod also holds the hub RBAC listed in [Permissions and Blast Radius](permissions-and-blast-radius.md) (cluster-connection Secrets in the ArgoCD namespace, its own state in the release namespace) and can reach each managed cluster with whatever the stored per-cluster credential grants. This is why the least-privilege examples on that page matter more than any Sharko setting: application roles cannot shrink what a compromised pod could read — only the backend identity and the per-cluster credentials can.

## Residual risks, stated plainly

- **The backend identity is the real perimeter.** Scope it (prefix-bound IAM policy, single-namespace RBAC) or accept that a pod compromise reads everything it can.
- **Per-cluster credentials are as strong as you made them.** A `cluster-admin` kubeconfig stored in the backend makes every consumer of it — including a compromised Sharko — cluster-admin. Scope them to Secrets in addon namespaces.
- **Secret values transit Sharko memory.** For the moment between backend fetch and destination write, the value exists in the server process. There is no way to deliver a secret without holding it; Sharko keeps that window per-item and keeps no copy afterward.
- **Git merge rights are production rights.** Sharko deliberately trusts the merged state of your repo. Review rules on that repo are part of your security posture, not optional hygiene.
- **The audit log is in-memory by default.** For long-term forensics, ship Sharko's structured logs to your log system; the in-process audit ring is for operational visibility, not retention.
- **Availability is not replicated.** Sharko runs as a single instance; if it is down, rotation delivery and repair pause (deployments and existing Secrets are unaffected — ArgoCD and the clusters keep running from what's already in place).

## What Sharko will never do, as a matter of design

- Accept a secret value typed into the UI or API, or return one out of it.
- Deliver from any definition that is not merged in Git.
- Write to, or delete, a Secret it did not create (the `app.kubernetes.io/managed-by: sharko` ownership gate at the single write choke point).
- Send a value over an unverified or unencrypted connection.
- Bridge secret values through ArgoCD manifests, ArgoCD's Redis, or a vault-injection plugin — the delivery is direct, push-based, and audit-reviewable, and that constraint is locked.

## Reporting a problem

Security reports go through [SECURITY.md](https://github.com/MoranWeissman/sharko/blob/main/SECURITY.md) — please don't file them as public issues.

## Where to go next

- [The Engine and Secret Sync](engine-and-secret-sync.md) — the architecture and data flow behind every claim here.
- [Permissions and Blast Radius](permissions-and-blast-radius.md) — the permission-by-permission version, with least-privilege examples.
- [Secret Sync Debugging](../operator/secret-sync-debugging.md) — diagnosing failures without ever seeing a value.
