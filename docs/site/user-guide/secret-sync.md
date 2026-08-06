# Secret Sync

**Secret Sync** (`/secret-sync` in the UI — older links to `/secrets` still work) is the one page that lists every secret Sharko manages: the connection secret behind each cluster registration, and every addon's secret values. One row per secret, worst-first, with a card that opens on click showing what Sharko compares it against and what it found.

This page is not a secrets manager. It doesn't hold secret values, and it can't show you one. What it holds is a record of **deliveries** — what Sharko was told to apply, when it last checked, and whether the cluster matches. The rest of this page explains what that record is built on, and exactly where the line is.

## The four OpenGitOps principles

Secret Sync exists to make Sharko's own GitOps behavior legible, row by row. The [OpenGitOps principles](https://opengitops.dev/) — declarative, versioned, pulled automatically, continuously reconciled — map onto specific parts of the page:

| Principle | Where it shows up on the page |
|-----------|-------------------------------|
| **Declarative** | The **Source** column states what a row is checked against — connection rows read `cluster connection · follows git`, addon-values rows read `addon values · follows <the real backend name>` (AWS Secrets Manager, a Kubernetes Secret, and so on). Opening a row shows the exact file path in Git (for a connection) or the store key/pointer path (for a values secret) — the desired state always has one named, checkable location, never an implied one. |
| **Versioned** | Every connection row's detail card shows the Git commit it was last checked against — a short hash with the full commit available on hover, next to the file path in the repo. Nothing about what Sharko applied is undocumented history; it's a specific commit you can look up. |
| **Pulled automatically** | The engine strip at the top of the page states each engine's cadence in plain words ("Sharko re-checks it every 30 seconds, and right after each merge" / "Sharko checks it every 5 minutes and repairs it automatically") and the last time a check actually ran. Nothing is pushed to Sharko — it pulls on its own schedule, and the page says what that schedule is. |
| **Continuously reconciled** | Sharko treats **drift** and **a failed check** as two different facts, never conflated. A connection row that's out of sync says plainly whether *git moved* (a newer commit changed what the secret should be) or *the cluster moved* (something changed it outside git) — that's drift, a real mismatch Sharko can act on. A row whose `last_check_error` is set is a different fact entirely: the check itself didn't finish, so there's nothing yet to compare. And reconciliation itself is scoped: Sharko only repairs a row that carries its own label (`app.kubernetes.io/managed-by: sharko`) — a secret it created or was handed ownership of. A secret somebody else owns shows as **foreign** and Sharko never touches it, reconcile tick or not. |

## Secret delivery tools — you may already have one

Secret Sync's **addon values** engine applies secret values from your secrets store into the clusters where an addon needs them — that's a real, ongoing job, and some teams already have a tool doing exactly that job before Sharko ever shows up.

If you already have a tool that delivers secrets into your clusters (External Secrets Operator, Sealed Secrets, a vault agent, and others), you may prefer to leave this engine off and let that tool keep doing it. These are named only as examples of the category — this isn't a comparison against any one of them, and Sharko has no opinion on which one you use.

**Turning it off**: **Settings → Addon Values Engine** has a switch, admin-only. Off, the engine runs no passes at all — it stops both checking values against the store and applying them to clusters. Rows on this page keep showing whatever they last knew (their last check, their last state) — they don't go blank and they don't lie about being current. The engine strip says plainly: *"Addon values engine is switched off."*

**Connection secrets get no such caveat.** The cluster-connection engine — the one that tells ArgoCD a cluster exists and which addons it runs — has no off switch, on purpose. That delivery is Sharko's own job, not something another tool would already be doing, so there's no "maybe you don't want this" question to ask.

## The boundary promise

**Sharko may describe the delivery, never the secret.**

Every row, every card, every audit entry on this page and everywhere else in Sharko describes what happened to a secret — where it came from, when it was checked, whether it matches — and never what a secret contains. That split is not a UI choice that could change later; it's load-bearing everywhere Sharko talks about secrets, in code and in words. Sharko says *"Sharko applies the value"* — never *"Sharko rotates the secret"* — because applying is the delivery fact Sharko actually knows, and rotation is something it doesn't do and doesn't track.

Concretely, Sharko will **never**:

- Create, edit, or delete a secret from the UI.
- Show a secret's value, its length, or a hash of it.
- Browse the secrets store — Sharko reads exactly the keys a catalog entry names, nothing more, nothing exploratory.
- Rotate a secret, or track when one is due to expire.

What Sharko *does* show — a secret's name, namespace, the commit or store key it's checked against, when it was last checked, whether it matches, and a plain-English record of when Sharko itself last wrote to it — is the delivery record. That's the whole boundary: the delivery is Sharko's story to tell; the secret itself never is.

## Why there's no separate delete-lock

Nothing on this page will ever delete a live secret out from under you, but that protection isn't a special lock this page adds — it's the two protections Sharko already has everywhere:

1. **The pull request gate.** Every change to what Sharko manages goes through Git as a PR — nothing is removed from a cluster because someone clicked something in the UI without that change first landing in a reviewed commit.
2. **ArgoCD's `preserveResourcesOnDeletion`.** Sharko's ArgoCD Applications are configured so that removing an addon from Git does not delete the Kubernetes Secrets that addon's values live in — the Secret is orphaned from Sharko's bookkeeping, not deleted from the cluster.

Both of those are deliberate, existing GitOps behavior — not something bolted onto Secret Sync specifically. This page just shows you the result: a secret that's no longer in Git's plan shows up honestly (as **not on the cluster**, or drops off the list once you tell Sharko to stop tracking it), never as a surprise deletion Sharko performed on its own.

## Related pages

- [Secrets Provider](secrets-provider.md) — configuring where cluster credentials and addon secret values come from.
- [GitOps Drift Detection and Self-Heal](drift-and-sync.md) — the cluster-connection engine's own drift and self-heal story (Secret Sync's addon-values engine follows the same reconcile-only-what-it-owns rule, described above).
- [Status Vocabulary](status-vocabulary.md) — what each status color and name means across the rest of Sharko's UI.
