# Taking Over a Fleet That Is Already Running

> **Verified on: (pending the maintainer's walkthrough).** Every step
> below is implemented and covered by tests, but this recipe has not yet
> been run end to end against a real brownfield fleet. Treat the timings
> and the "what you will see" notes as expectations, not observations,
> until that line is updated.

Most people who find Sharko already have ArgoCD running. They have a
cluster secret per cluster, a handful of ApplicationSets, and addons
deployed to real workloads. The question is never "how do I start from
scratch" — it is **"how do I hand this over without breaking anything?"**

This is that recipe. It is written so you can do one cluster, stop, look
around, and come back tomorrow for the next one.

## The promise, up front

- **Nothing is deleted before you say so.** Not the cluster secret, not
  a label, not an Application. Every step that changes anything asks
  first and tells you what it will change.
- **The cluster keeps its name and its address.** Taking over does not
  rename anything, does not move anything, and does not re-point ArgoCD
  at a different API server.
- **The connection is never absent, not even for a moment.** The
  handover is a single edit to the secret that is already there — not a
  delete followed by a create. ArgoCD never sees the cluster disappear.
- **Values are copy-paste.** Whatever `values:` block your addon has in
  its current Helm release or Application, you paste it into Sharko's
  values file unchanged. There is no conversion, no new schema, no
  translation layer. If it worked in Helm, it works here.
- **You can stop at any step.** Each step leaves a working fleet.

## Before you start

You need:

- Sharko installed in the same cluster as ArgoCD, from the Sharko chart
  (the takeover checks need to read ApplicationSets, and the chart is
  what grants that).
- A repo in the v4 layout. If Sharko says your repo needs migrating,
  do that first — takeover writes the v4 files and nothing else.
- An admin role in Sharko. The checks are open to operators; the
  handover itself is admin-only.

---

## Step 0 — Make deletion safe first

**Do this before anything else, and do it for the whole fleet, not just
the cluster you are starting with.**

An ApplicationSet decides which clusters it deploys to. When a cluster
stops matching it, the default behaviour is to delete the Application it
generated — and deleting an Application prunes everything that
Application installed. That is the one way a takeover can hurt you, and
it is entirely avoidable.

For each ApplicationSet you care about, set **one** of these:

```yaml
apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
spec:
  syncPolicy:
    # Either: the Application may go away, but what it installed stays.
    preserveResourcesOnDeletion: true
```

```yaml
spec:
  syncPolicy:
    # Or: never delete Applications at all.
    applicationsSync: create-update   # or create-only
```

Then run the preflight (step 2) and confirm the ApplicationSet check
comes back green. That check exists purely so you do not have to audit
this by hand.

!!! note "Why this is step 0 and not step 4"
    Doing this first means every later step is reversible. Doing it
    later means there is a window where a mistake costs you workloads.

---

## Step 1 — Parity check

Before Sharko owns anything, make sure Sharko's picture of the cluster
matches reality.

1. Open the cluster in Sharko. It appears under discovered clusters —
   Sharko can see it, but does not manage it.
2. Compare what ArgoCD is deploying there with what you expect. The
   preflight in the next step gives you the full list, but do this with
   your own eyes too.
3. Note anything deployed outside ArgoCD (a plain `helm install`, a
   `kubectl apply`). Those are unaffected by everything below, but they
   are the things people forget they have.

Nothing has changed yet.

---

## Step 2 — Preflight

Open the cluster and choose **Take over this cluster**. The dialog opens
on four checks:

| Check | What it answers |
|---|---|
| Who owns this cluster's connection today | Is another tool — or another ArgoCD application — writing this cluster secret? |
| What happens if this cluster stops matching an ApplicationSet | Which ApplicationSets would delete workloads? (step 0) |
| What is running on this cluster right now | The full list of Applications pointed at it |
| Whether the name is already taken | Does Sharko already have a cluster with this name? |

From the API:

```bash
curl -s -H "Authorization: Bearer $SHARKO_TOKEN" \
  "$SHARKO_URL/api/v1/clusters/prod-eu/takeover/preflight" | jq
```

Each finding tells you what it means and what to do about it, in words.
There are three outcomes:

- **Green** — nothing to do, carry on.
- **Warning** — you have to read it and tick a box saying you have.
  Warnings do not stop you; they stop you doing it *by accident*.
- **Blocked** — the takeover is refused until you fix it. The only two
  blocking cases are "there is no connection to take over" and "that
  name already belongs to a different cluster".

**Fix something, then hit "Check again."** The same check turns green in
place. That loop is the point of the preflight — it is a read, it writes
nothing, and you can run it as many times as you like.

---

## Step 3 — The secret swap

This is the handover. Sharko becomes the owner of the ArgoCD cluster
secret that is already there.

Before you confirm, the dialog lists **every label currently on that
secret that came from the previous owner**, and — for each one — which
ApplicationSets pick clusters using it. Those labels are carried over
unchanged by default. Read that list. It is the thing that decides
whether anything notices the handover.

Confirm, and Sharko:

1. Opens a pull request adding the cluster to `fleet/connections.yaml`
   and creating an empty `clusters/<name>.yaml`.
2. Edits the existing cluster secret **in place**: adds its own
   ownership marker, keeps every label that was already there, and does
   not touch the credentials or the server address.

### The ordering, and why

The new secret has to keep the same name as the old one, and two secrets
cannot share a name in a namespace. So "create the new one first, then
delete the old" is not available — it would have to delete first, and in
that gap ArgoCD would drop the cluster and every Application pointed at
it would go to an unknown state.

So the swap is a single in-place edit of the one object that is already
there. There is no gap, and no half-finished state to recover from.

### Downtime, honestly

**Expected downtime: none.** The cluster secret is never absent, its
credentials never change, and no Application is touched. ArgoCD does not
resync anything as a result of the swap.

Two caveats worth stating plainly:

- If another ArgoCD application still renders this cluster secret from
  Git, its next sync will fight Sharko over it. The preflight warns you
  about exactly this — deal with it before you confirm, not after.
- Adding an ApplicationSet label later (step 4) *does* cause ArgoCD to
  act. That is a deliberate, separate step, one addon at a time.

### The Helm-CLI variant

If you installed Sharko with `helm install` and prefer the command line
throughout, the same handover is available from the API with no UI at
all:

```bash
# 1. Read the checks.
curl -s -H "Authorization: Bearer $SHARKO_TOKEN" \
  "$SHARKO_URL/api/v1/clusters/prod-eu/takeover/preflight" | jq '.summary, .findings[].title'

# 2. See the plan without changing anything.
curl -s -X POST -H "Authorization: Bearer $SHARKO_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"dry_run": true}' \
  "$SHARKO_URL/api/v1/clusters/prod-eu/takeover" | jq '.message, .dry_run'

# 3. Do it. Both flags are required when the preflight raised warnings.
curl -s -X POST -H "Authorization: Bearer $SHARKO_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"yes": true, "acknowledge_warnings": true}' \
  "$SHARKO_URL/api/v1/clusters/prod-eu/takeover" | jq
```

Add `"preserve_legacy_labels": false` only if you already know nothing
selects on the old labels. Leaving it out keeps them, which is the safe
default.

---

## Step 4 — Adopt the addons, one at a time

The takeover turns nothing on. Your addons are still deployed by
whatever deployed them before. Now you move them across, one addon per
cluster, at your own pace.

For each addon:

1. **Copy the values across.** Take the `values:` your current release
   uses and paste it into Sharko's values file for that addon — global
   values if the whole fleet should get them,
   `values/clusters/<cluster>/<addon>.yaml` if this cluster is
   different. No conversion. Same YAML.
2. **Check the chart version matches** what is deployed today. If it
   does not, Sharko will upgrade it the moment you enable it — decide
   whether you want that now or later.
3. **Enable the addon on the cluster** in Sharko. That opens a pull
   request; merging it is what makes ArgoCD act.
4. **Watch one sync**, confirm the Application is Healthy and Synced,
   then remove the old deployment path (the old Application, or the old
   `helm release`) so two things are not managing the same workload.
5. Move to the next addon.

Do not batch this. One addon, one merge, one look.

---

## Step 5 — Drop the old labels

Once every addon on the cluster is running through Sharko, the labels
you carried over in step 3 have usually done their job. Removing them is
its own explicit action, with its own confirmation:

Open the cluster and choose **Remove the old labels**. Sharko lists what
it would remove and — by name — every ApplicationSet that still picks
clusters using one of those labels, and what each of those would do if
this cluster stopped matching. You tick a box saying you have read that,
then confirm.

```bash
# Dry run first — this is what produces the warnings.
curl -s -X POST -H "Authorization: Bearer $SHARKO_TOKEN" \
  -H 'Content-Type: application/json' -d '{"dry_run": true}' \
  "$SHARKO_URL/api/v1/clusters/prod-eu/takeover/legacy-labels/drop" | jq

# Then, having read them:
curl -s -X POST -H "Authorization: Bearer $SHARKO_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"yes": true, "acknowledge_warnings": true}' \
  "$SHARKO_URL/api/v1/clusters/prod-eu/takeover/legacy-labels/drop" | jq
```

**This is the step where step 0 pays for itself.** If an ApplicationSet
still selects one of these labels and you did not make it deletion-safe,
removing the label is what deletes the workloads. Sharko warns you and
names it; step 0 makes the warning academic.

Some labels are never removable here: ArgoCD's own marker that makes the
secret count as a cluster, Sharko's ownership marker, and Sharko's addon
labels. Asking to remove any of those is refused with an explanation.

There is no rush. Leaving the old labels in place indefinitely is
perfectly fine — nothing degrades.

---

## Then: the next cluster

Go back to step 1. Step 0 you only do once (it is fleet-wide); steps 1
through 5 you repeat per cluster.

---

## Unregistering, later

If you ever want a cluster out of Sharko, the removal shows you its
consequences before it does anything:

```bash
curl -s -H "Authorization: Bearer $SHARKO_TOKEN" \
  "$SHARKO_URL/api/v1/clusters/prod-eu/unregister/consequences" | jq
```

It reads out what leaves the repo, what happens to the ArgoCD
connection, what is deployed there right now, and which ApplicationSets
may react to the labels the takeover carried over. Nothing is deleted
until you send the removal itself with an explicit confirmation.

If you want the cluster out of Sharko but want ArgoCD to keep talking to
it, ask for `"cleanup": "git"` — Sharko drops its own records and leaves
the connection exactly as it is.

## See also

- [Managing Cluster Connections Yourself](self-managed-connections.md)
  — for connections you would rather keep authoring by hand.
- [If You Remove Sharko (no lock-in)](removing-sharko.md) — the same
  honesty, at the whole-install level.
- [Reference — Cluster Reconciler](cluster-reconciler.md) — what keeps
  the labels in step with Git once a cluster is yours.
