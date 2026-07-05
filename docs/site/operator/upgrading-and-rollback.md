# Upgrading & Rollback Safety

> **Verified:** 2026-07-05 — guard behavior verified by unit tests
> (`internal/schema/apiversion_forwardguard_v2cleanup60_test.go`,
> `internal/clusterreconciler/sweep_guard_v2cleanup60_test.go`) against the
> V2-cleanup-60.2 implementation; the v2.1.x-reads-v2.2.0-file failure
> mode itself was verified during the 2026-07-05 post-feature review.

This page covers what can go wrong **across Sharko versions** — upgrading,
rolling back, and running more than one Sharko instance against the same
Git repo. Read it before your first upgrade to v2.2.0 or later.

## Why version transitions need care

v2.2.0 moved Sharko's config-file identity from the old `sharko.io/v1` API
group to the maintainer-owned `sharko.dev/v1` group. Reading is compatible
in both directions for all of v2.x — old files keep working. **Writing is
not**: the first write operation after upgrading rewrites
`managed-clusters.yaml` with the new group, and Sharko binaries at v2.1.x
or older cannot read that file.

Worse than "cannot read": an old binary does not error on the new file. It
silently parses it as an **empty cluster list**, and its cleanup sweep then
deletes every Sharko-managed ArgoCD cluster secret it sees — within one
30-second tick. Clusters registered with a pasted (inline) kubeconfig are
unrecoverable at that point, because their credentials existed only in the
deleted secret.

Sharko v2.2.1 adds guards so a **new** binary can never repeat this class
of failure (see [The guards in v2.2.1 and later](#the-guards-in-v221-and-later)).
Nothing can fix binaries that already shipped — which is why the two rules
below are absolute.

## Rule 1 — no rollback to v2.1.x after a write

Once a Sharko at v2.2.0+ has performed **any write** (registering,
removing, or updating a cluster; enabling or disabling an addon; anything
that opens a PR against `managed-clusters.yaml`), do **not** downgrade
that instance to v2.1.x or older.

If you must roll back before any write happened, that is safe — the files
still carry the old group until the first write.

If you rolled back after a write and the old binary is running: **stop it
immediately**, check whether your ArgoCD cluster secrets survived
(`kubectl get secrets -n argocd -l app.kubernetes.io/managed-by=sharko`),
and re-register any cluster whose secret is gone. Inline-registered
clusters need their kubeconfig pasted again.

One more thing to know about rolling back: from v2.2.1, any cluster you
register records a new `credsSource` field in its `managed-clusters.yaml`
entry. A v2.2.0-or-older binary can't read a file with that field — schema
validation rejects it outright, so the old binary fails loudly and refuses
to start instead of silently misreading the file. That's a safe failure
(no empty-list misread, no sweep), but it's still one more reason not to
roll back to an older binary once you've written anything.

## Rule 2 — multiple instances on one repo upgrade together

Two Sharko instances sharing one Git repo must run the same major.minor
line across the v2.1 → v2.2 boundary. A not-yet-upgraded instance reading
a file written by an upgraded one is exactly the rollback scenario above,
with the same result. Upgrade all instances in one maintenance window,
before any of them writes.

## Connectivity check after upgrading (pre-v2.2.0 templates)

The connectivity-check label also moved to the new name in v2.2.0. If your
bootstrap templates were rendered **before** v2.2.0, the already-deployed
connectivity-check ApplicationSet still selects the **old** label — and the
first reconcile after upgrading migrates the secret labels to the new name,
so the ApplicationSet stops matching.

Symptom: clusters with **zero addons** show **"Unknown"** connectivity in
the UI instead of "verified". Clusters that run at least one addon are
unaffected (their health comes from the addon itself).

Fix: re-render / refresh the bootstrap templates from your upgraded Sharko
so the check ApplicationSet selects the new label. Until you do, the
"Unknown" state is cosmetic — nothing is broken on the cluster itself.

## The guards in v2.2.1 and later

Two forward guards shipped in v2.2.1 make the new binary refuse to act on
the ambiguous inputs that made the old failure possible. You may meet them
as error messages; both are working as intended.

### Unrecognized sharko apiVersion

If a config file declares an `apiVersion` in the `sharko.*` family that
this binary does not recognize (for example `sharko.dev/v2` written by a
future release), every reader now fails hard with:

```
unrecognized Sharko apiVersion "sharko.dev/v2": this file was written by a
newer or unknown Sharko — refusing to guess how to read it ...
```

The file is never silently treated as empty. What to do: upgrade the
Sharko instance that logged the error to a version that understands the
file. Do not hand-edit the `apiVersion` to silence the error unless you
know the payload shape is compatible.

Files with no `apiVersion` (legacy bare YAML) and non-Sharko files are
unaffected — they keep parsing exactly as before.

### Orphan sweep held

The cluster reconciler's cleanup sweep now refuses to run when all three
of these are true at once:

1. the desired state parsed from `managed-clusters.yaml` contains **zero**
   clusters, and
2. the file **exists non-empty** in Git, and
3. at least one Sharko-labeled ArgoCD cluster secret is still live.

That combination means "Git suddenly says nothing while the live fleet
says something" — the signature of a misread file, not of a real
fleet-wide removal. Instead of deleting anything, the reconciler skips the
sweep for that tick, logs an Error, and emits an `orphan_sweep_held` audit
event (visible in `/api/v1/audit`). It re-evaluates every tick, so the
hold clears itself as soon as the mismatch is resolved.

What to do when you see `orphan_sweep_held`:

1. Open `managed-clusters.yaml` on the branch Sharko reads (default
   `main`) and check its `apiVersion` and contents. A version/format
   mismatch (file written by a different Sharko version) is the most
   likely cause.
2. If the file is wrong, fix the source of the bad write (usually: finish
   upgrading every instance that shares the repo).
3. If you genuinely want a zero-cluster fleet, remove the clusters through
   Sharko (removal deletes their secrets as part of the flow), or delete
   the leftover labeled secrets by hand. Once no Sharko-labeled secret
   remains, the guard disarms on the next tick.

Fresh installs are not affected: a missing `managed-clusters.yaml` (or a
genuinely empty file) never triggers the hold.

## Related pages

- [Failure Mode Index](failure-mode-index.md) — Ctrl-F your error message.
- [Reference — Cluster Reconciler](cluster-reconciler.md) — how the sweep
  and the ownership label work.
- [Managing Cluster Connections Yourself](self-managed-connections.md) —
  self-managed connections are never touched by the sweep in any case.
