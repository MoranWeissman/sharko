# Secrets

**Secrets** is the area of the UI where Sharko shows the Secrets it works with. One sidebar item, two subpages, each with its own URL:

- **Cluster connections** (`/secrets/connections`) — Secrets Sharko uses to register clusters with Argo CD. One cluster connection Secret per cluster.
- **Addon secrets** (`/secrets/addons`) — Secrets Sharko delivers from configured backends to addons on remote clusters. This page also shows leftover ("orphaned") Secrets whose source is gone from Git.

Each subpage lists one row per Secret, worst-first, and a row opens its own detail page showing what Sharko compares it against and what it found. Older links to `/secret-sync` and `/secrets` still work — they redirect into the right subpage.

This area is not a secrets manager, and it does not list every Kubernetes Secret in a cluster — only the Secrets Sharko registers, delivers, or left behind. It doesn't hold secret values, and it can't show you one. What it holds is a record of **deliveries** — what Sharko was told to apply, when it last checked, and whether the cluster matches. The rest of this page explains what that record is built on, and exactly where the line is.

## The four OpenGitOps principles

The Secrets area exists to make Sharko's own GitOps behavior legible, row by row. The [OpenGitOps principles](https://opengitops.dev/) — declarative, versioned, pulled automatically, continuously reconciled — map onto specific parts of the two pages:

| Principle | Where it shows up on the page |
|-----------|-------------------------------|
| **Declarative** | The **Source** column states what a row is checked against — connection rows read `checked against git`, addon-values rows read `checked against <the real backend name>` (AWS Secrets Manager, a Kubernetes Secret, and so on). Opening a row shows the exact file path in Git (for a connection) or the store key/pointer path (for a values secret) — the desired state always has one named, checkable location, never an implied one. |
| **Versioned** | Every connection row's detail card shows the Git commit it was last checked against — a short hash with the full commit available on hover, next to the file path in the repo. Nothing about what Sharko applied is undocumented history; it's a specific commit you can look up. |
| **Pulled automatically** | The engine strip at the top of each page states its engine's cadence in plain words ("Sharko re-checks it every 30 seconds, and right after each merge" / "Sharko checks it every 5 minutes and repairs it automatically") and the last time a check actually ran. Nothing is pushed to Sharko — it pulls on its own schedule, and the page says what that schedule is. |
| **Continuously reconciled** | Sharko treats **drift** and **a failed check** as two different facts, never conflated. A connection row that's out of sync says plainly whether *Git moved* (a newer commit changed what the secret should be) or *the cluster moved* (something changed it outside Git) — that's drift, a real mismatch Sharko can act on. A row whose `last_check_error` is set is a different fact entirely: the check itself didn't finish, so there's nothing yet to compare. And reconciliation itself is scoped: Sharko only repairs a row that carries its own label (`app.kubernetes.io/managed-by: sharko`) — a secret it created or was handed ownership of. A secret somebody else owns shows as **foreign** and Sharko never touches it, reconcile tick or not. |

## Secret delivery tools — you may already have one

The **Addon secrets** page's engine applies secret values from your secrets store into the clusters where an addon needs them — that's a real, ongoing job, and some teams already have a tool doing exactly that job before Sharko ever shows up.

If you already have a tool that delivers secrets into your clusters (External Secrets Operator, Sealed Secrets, a vault agent, and others), you may prefer to leave this engine off and let that tool keep doing it. These are named only as examples of the category — this isn't a comparison against any one of them, and Sharko has no opinion on which one you use.

**Turning it off**: **Settings → Addon Values Engine** has a switch, admin-only. Off, the engine runs no passes at all — it stops both checking values against the store and applying them to clusters. Rows on Addon secrets keep showing whatever they last knew (their last check, their last state) — they don't go blank and they don't lie about being current. The engine strip says plainly: *"Addon values engine is switched off."* One exception: a single row's own **Refresh** and **Sync** buttons still work even with the engine off — that's an explicit action you took on one secret, not a pass, and Sharko treats it that way.

**Connection secrets get no such caveat.** The cluster-connection engine — the one that tells ArgoCD a cluster exists and which addons it runs — has no off switch, on purpose. That delivery is Sharko's own job, not something another tool would already be doing, so there's no "maybe you don't want this" question to ask.

## The boundary promise

**Sharko may describe the delivery, never the secret.**

Every row, every card, every audit entry in this area and everywhere else in Sharko describes what happened to a secret — where it came from, when it was checked, whether it matches — and never what a secret contains. That split is not a UI choice that could change later; it's load-bearing everywhere Sharko talks about secrets, in code and in words. Sharko says *"Sharko applies the value"* — never *"Sharko rotates the secret"* — because applying is the delivery fact Sharko actually knows, and rotation is something it doesn't do and doesn't track.

Concretely, Sharko will **never, on its own or automatically**:

- Let you type, paste, or otherwise set a secret's contents through the UI — there's no field for a value anywhere in this area. Sync does write a secret on the cluster once you confirm it (that's the delivery job this whole page exists for), but it always pushes the value **from** the secrets store — never from anything typed into Sharko.
- Show a secret's value, its length, or a hash of it.
- Browse the secrets store — Sharko reads exactly the keys a catalog entry names, nothing more, nothing exploratory.
- Rotate a secret, or track when one is due to expire.

There is exactly one exception: an operator's explicit, confirmed **Delete** of an **orphaned** leftover — see below. Even that only ever touches a secret carrying Sharko's own ownership markers, and it never happens without a person reading a confirm dialog that names the exact secret and clicking through it.

What Sharko *does* show — a secret's name, namespace, the commit or store key it's checked against, when it was last checked, whether it matches, and a plain-English record of when Sharko itself last wrote to it — is the delivery record. That's the whole boundary: the delivery is Sharko's story to tell; the secret itself never is.

## Orphaned leftovers

An **orphaned** row is a secret Sharko wrote to a cluster at some point, whose source someone later deleted from Git — the addon definition (or cluster registration) that once asked for this secret is gone, but the secret itself is still sitting on the cluster, still carrying Sharko's own labels. Nothing asks for it anymore.

Sharko never deletes an orphaned secret on its own. It shows up on the Addon secrets page with its own **Orphaned** status (a violet dot, distinct from every other state on the page) so you know it's there, and it stays there — same as any other row — until you decide what to do with it.

**Delete** is the one action an orphaned row offers, and it's operator-or-admin only. Clicking it opens a confirm dialog that names exactly what's about to happen — the secret's namespace and name, the cluster it's on, and that this cannot be undone — before anything happens. Cancel does nothing at all; no request is made unless you confirm.

Delete carries its own safety gates, checked again at the moment you confirm, not just when the row was last refreshed:

- Only a secret that still carries **Sharko's own ownership label and provenance marker** is eligible — the same labels that make a row show up as Sharko-managed anywhere else on this page.
- The secret is **re-checked against Git right then** — if something in Git has started asking for it again since the row last loaded, or if its ownership marker changed, the delete is refused and nothing is touched.
- The action is **recorded in the in-memory activity history**, the same as every other write Sharko makes.

If a delete is refused, the UI shows the server's plain-English reason — never a raw error — so you know exactly why nothing happened.

## Why there's no separate delete-lock

Nothing in this area will ever delete a live secret out from under you *automatically*, but that protection isn't a special lock these pages add — it's the two protections Sharko already has everywhere, plus the one explicit, human-confirmed exception described above:

1. **The pull request gate.** Every change to what Sharko manages goes through Git as a PR — nothing is removed from a cluster because someone clicked something in the UI without that change first landing in a reviewed commit. (Deleting an orphaned leftover is the one action on this page that doesn't go through this gate, precisely because there's no Git file left to open a PR against — that's the whole reason it needs its own confirm dialog and its own re-check at delete time.)
2. **ArgoCD's `preserveResourcesOnDeletion`.** Sharko's ArgoCD Applications are configured so that removing an addon from Git does not delete the Kubernetes Secrets that addon's values live in — the Secret is orphaned from Sharko's bookkeeping, not deleted from the cluster. This is exactly how an orphaned row comes to exist in the first place.

Both of those are deliberate, existing GitOps behavior — not something bolted onto the Secrets area specifically. These pages just show you the result: a secret that's no longer in Git's plan shows up honestly (as **orphaned**, or **not on the cluster** once Sharko itself stops tracking it), never as a surprise deletion Sharko performed on its own.

## Related pages

- [Secrets Provider](secrets-provider.md) — configuring where cluster credentials and addon secret values come from.
- [GitOps Drift Detection and Self-Heal](drift-and-sync.md) — the cluster-connection engine's own drift and self-heal story (the addon values engine follows the same reconcile-only-what-it-owns rule, described above).
- [Status Vocabulary](status-vocabulary.md) — what each status color and name means across the rest of Sharko's UI.
