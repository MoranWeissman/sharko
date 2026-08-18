# Migrating Off Pasted (Inline) Credentials

> **Verified:** 2026-08-18 — every door this page uses already exists and was
> verified against the code and the product ruling's migration-path check
> (2026-08-17): the credentials providers, the schema-validated
> `managed-clusters.yaml` write, the read-only connection check, and the
> guarded repair. The end-to-end playground walk of this exact sequence is
> part of the current round's acceptance gate and is scheduled, not yet run —
> this header will be updated when it has.

A cluster registered with a **pasted (inline) kubeconfig** carries a
credential that exists **only in the live ArgoCD cluster Secret**. It is never
stored in Git, so Sharko cannot restore it from Git — if that Secret is lost,
the connection is gone with it. On the connection page such a cluster shows
**Verification incomplete** with the sentence:

> This connection's credential exists only in the live Secret and cannot be
> restored from Git.

This page moves that cluster onto a **supported credentials provider**, where
Git holds a durable reference and Sharko can verify and rebuild the full
connection. Every step uses a door that already exists — a provider your
backend already reads, a normal Git pull request, and the existing guarded
repair action. No new tooling, no downtime for the cluster's addons (they
deploy through Git → ArgoCD and never depend on Sharko's own cluster access).

## What you need to know before starting

- **Sharko never exports the pasted value.** There is no button, API call, or
  log line that returns the credential out of the live Secret — that is a
  deliberate security rule. You supply the kubeconfig yourself: use your
  original copy, or create a fresh one from the cluster if you no longer have
  it (a fresh one is the better choice anyway).
- **Nothing happens automatically.** Sharko does not migrate, convert, or
  delete anything on its own. Every step below is yours, and the final write
  is an explicit admin action pinned to a commit you reviewed.
- **Nothing is deleted.** The live Secret is updated **in place** by the
  repair — never deleted and recreated — and every label, annotation, or data
  key that is not Sharko's own is preserved untouched.

## Step 1 — store the kubeconfig in a supported provider

Put a working kubeconfig for the cluster into one of the credential backends
Sharko reads:

- **A Kubernetes Secret** the configured backend can read, or
- **AWS Secrets Manager**.

The stored value is the raw kubeconfig YAML. For example, as a Kubernetes
Secret:

```bash
kubectl create secret generic my-cluster-kubeconfig \
  -n <the namespace your credentials backend reads> \
  --from-file=kubeconfig=./my-cluster.kubeconfig
```

If you no longer have the original kubeconfig, create a fresh one from the
cluster itself (a service-account token with the permissions ArgoCD needs).
The old pasted credential does not have to be recovered — it is being
replaced.

## Step 2 — point the cluster's Git entry at the stored credential

Open a normal pull request on `managed-clusters.yaml` in your GitOps repo,
changing that cluster's `credsSource` to `secret-kubeconfig` and setting
`secretPath` to where you stored the kubeconfig in step 1:

```yaml
apiVersion: sharko.dev/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: my-cluster
      credsSource: secret-kubeconfig   # was: inline-kubeconfig
      secretPath: my-cluster-kubeconfig
```

The file is schema-validated on write, and the change goes through your
ordinary review process — this PR is the moment the connection's definition
becomes fully durable in Git.

## Step 3 — check, then apply through the guarded repair

After the PR merges:

1. Open the cluster's connection page and run the **connection check**
   ("Check again"). The check is read-only. With the credential now stored in
   a backend, the connection is checked at **full scope** — Sharko rebuilds
   the expected Secret from the Git definition, resolving the credential
   value from your provider, and compares it byte for byte, without
   returning any value.
2. The check will report the live connection (still carrying the old pasted
   credential) as out of sync with what Git now defines. Review the
   difference — sensitive fields show only their path and a status word,
   never a value.
3. An **admin** applies it with **Repair connection**. The repair uses the
   exact commit shown at check time (it refuses if the branch has moved
   since), updates the Secret **in place**, and preserves all foreign labels,
   annotations, and data keys.

When a following check comes back clean, the page shows **Connection synced**
— the full connection, credential included, is now verified on every check
against the Git definition with its credential reference resolved from your
provider.

## What does NOT happen

- **No automatic migration** — Sharko never moves a credential between
  storage places by itself.
- **No value is ever displayed** — not during the check, not in the repair
  confirmation, not in any response or log.
- **Nothing is deleted** — the live Secret is updated in place, and the
  stored value in your provider is only ever read.

## Related pages

- [Managing Cluster Connections](../user-guide/connections.md#allow-legacy-inline-credentials)
  — the "Allow legacy inline credentials" setting and why new pasted
  registrations are off by default.
- [Security](security.md#secrets-management-recommendations) — the
  recommendations this migration satisfies.
- [Managing Clusters](../user-guide/clusters.md#adding-a-cluster) — the
  credential sources available at registration.
