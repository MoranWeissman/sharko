# Migrating a Repo to the New Format

Sharko changed the way it stores things. If your repository still uses the
older layout, Sharko offers you a migration: **one pull request** converts
the whole repository, and everything running keeps running.

This page explains what that pull request does, what Sharko does in ArgoCD
around it, and what to do if anything stops halfway.

## What you'll see

A yellow banner on the Dashboard:

> **This repo uses the older v3 layout** — one pull request migrates it.
> Reads keep working until then.

Two buttons: **Preview migration** (shows every file the pull request would
touch, changes nothing) and **Open migration PR**.

While the banner is up, you can still look at everything — clusters,
addons, values, pull requests. Only writes are paused, because a migration
raised against a repository that is still changing is a merge conflict
waiting to happen.

## What happens to your catalog

Your old catalog was, in practice, already your org's approved list — every
addon your fleet ran, side by side, whether Sharko shipped it or you added
it yourself. So the conversion is simple: every entry converts straight
across into a full entry in the new `catalog.yaml` — chart, repo, version,
namespace, settings, all of it. Nothing is compared against a shipped
default and nothing is dropped for "matching" it, because there is no
shipped default to compare against any more. What you ran before, you keep
running after, and it stays visible in your own repo.

If an old entry had a `secrets:` block (the credentials the addon needs),
that moves into the new entry too, as its needed-secrets list — same place
the rest of the addon's install information now lives, and nothing is left
behind in the pull request description.

## The part that isn't in the repository

This is the bit worth understanding, because it is why the migration takes
a moment before it opens the pull request.

Your addons are deployed by ArgoCD ApplicationSets. Those ApplicationSets
do not live in your repository — they were created in your ArgoCD when
Sharko first set the fleet up, and they have been sitting there ever since.
Each one works out where to deploy by reading two things:

- a label on each cluster's connection (`cert-manager: enabled`, say), and
- a per-cluster values file in your repository.

The migration pull request changes **both** of those. It moves the values
files to their new home, and once it merges, the cluster labels get their
new names.

An ApplicationSet that can no longer find either one produces nothing — and
an ApplicationSet that produces nothing deletes the Applications it made.
In the old setup, deleting one of those Applications also removes
everything it installed. On a live fleet, that is every addon on every
cluster.

So Sharko does not just open the pull request.

## What Sharko actually does

**Before the pull request is opened**, while everything is still working
normally:

1. It finds the ApplicationSets from the old setup — the ones that pick
   clusters by an addon name.
2. It sets each of them to leave running workloads alone.
3. It takes the "delete everything I installed" marker off the Applications
   they created.

After this, those ApplicationSets are harmless. They can lose their inputs,
they can be removed outright, and either way nothing you have running is
touched. The pull request body records exactly what was done, so you can
see it before you merge.

**After the pull request merges:**

4. Sharko removes the old ApplicationSets.
5. Then it applies `engine.yaml`, which starts the new engine.

The new engine creates its own ApplicationSets, which create Applications
with **the same names as before** (`cert-manager-prod-eu`, and so on).
ArgoCD recognises the things already running under those names as belonging
to them and takes them over in place. Nothing is reinstalled. Nothing is
removed and put back.

Step 4 has to happen before step 5, because two ApplicationSets cannot both
own an Application of the same name — the old owner has to be gone before
the new one arrives.

## When Sharko refuses

If your repository has clusters registered and Sharko **cannot reach the
ArgoCD that runs them**, it refuses the migration and writes nothing. The
message says so in as many words.

That is deliberate. Migrating the files alone, on a live fleet, is the one
thing that takes your addons down. Connect Sharko to the right ArgoCD and
try again.

If nothing is actually running any more — an ArgoCD that has been torn
down, a repository Sharko never deployed from — you can send

```json
{ "yes": true, "runtime_handoff": "skip" }
```

to `POST /api/v1/migration/migrate`, and Sharko will migrate the files only.
Use it when you are sure. A repository with no clusters registered at all
needs nothing extra: Sharko sees there is nothing to disturb and migrates
straight away.

## If it stops halfway

The second half normally runs by itself the moment the pull request merges.
If it did not — you merged the pull request yourself in a browser tab
Sharko never heard about, Sharko restarted at the wrong moment, or ArgoCD
was not connected at the time — you will see a banner:

> **The migration is not finished in ArgoCD**

Your addons are still running while this shows. The old ApplicationSets are
harmless (step 2 and 3 already happened), they are just still there, and the
new engine is not in charge yet. Press **Finish migration**, or call

```
POST /api/v1/migration/complete
```

It is safe to run as many times as you like.

One case it will stop and tell you about: an ApplicationSet from the old
setup that is still able to remove what it installed. That means the
"before" steps never ran for it. Sharko will not delete it — deleting it is
exactly the move that would take your addons down. Set
`preserveResourcesOnDeletion: true` on that ApplicationSet yourself, then
press Finish again.

## Reverting

The pull request is one commit. Revert it and your repository is exactly as
it was.

The ArgoCD side is a little different: the old ApplicationSets are gone once
the migration finishes, and reverting the pull request does not bring them
back. But nothing was uninstalled at any point, so your addons are still
running — you would re-create the old setup by re-bootstrapping, not by
recovering from an outage.

## What the pull request tells you

Read the "Worth reading before you merge" section. Sharko puts a note there
for anything it could not carry across exactly, for example:

- a version pin for an addon a cluster was not actually running (kept, with
  the addon switched off, so turning it back on later gives you that same
  version);
- values for a cluster that is not in your registry (moved, not deleted, so
  nothing is lost — they just deploy nothing until you register that
  cluster);
- values for an addon that is not in your addon list (moved for the same
  reason).

Nothing is thrown away silently. If Sharko cannot place something, it says
so and leaves it where it is.

## Permissions this needs

Sharko needs `patch` and `delete` on ApplicationSets, and `patch` on
Applications, **in the ArgoCD namespace only**. The Helm chart grants this
as a namespaced Role; the cluster-wide permissions stay read-only. Nothing
outside the migration uses them.
