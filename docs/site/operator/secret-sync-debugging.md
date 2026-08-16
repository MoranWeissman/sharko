# Secret Sync Debugging — Safe Runbook

> **Verified:** 2026-08-16 — every read-only step on this page was executed against the live playground (kind hub, image `sharko:playground-d2adca0d`) through the same API doors and kubectl commands shown here. The two paths that require *breaking* something (a backend failure, an ownership refusal) are marked inline: the backend-failure signals were additionally verified against the captured closure proofs of 2026-08-14, and the ownership-refusal behavior is pinned by code and tests, with a live break-and-diagnose exercise scheduled separately.

This page walks every way addon Secret Sync can fail, and how to diagnose each one **without ever seeing a secret value**. That's not a style choice — it's the whole design: every status, log line, audit entry, and error sentence Sharko produces about a secret describes the *delivery*, never the content. The commands below follow the same rule.

**The hard rule for every command on this page:** never print a Secret's YAML or its `.data`/`stringData` — no values, no lengths, no hashes, no encoded forms. Where a live Secret must be inspected, use metadata-only forms (`-o jsonpath` on `.metadata.*` fields), exactly as shown. Never run `kubectl get secret ... -o yaml` while debugging.

## Where status is visible (the four doors)

All of these are read-only and safe. Replace `$SHARKO` with your server URL and use any authenticated session (viewer role is enough to look):

1. **The UI** — **Secrets → Addon secrets**: one row per delivery, worst-first, with a plain sentence per problem, plus the engine strip at the top (cadence, last check, on/off state).
2. **The API** —

    ```bash
    # Rows, engine state, and any leftover secrets — names and statuses only
    curl -s $SHARKO/api/v1/system/managed-secrets -H "Authorization: Bearer $TOKEN"

    # The last completed pass's counters (checked/created/updated/errors)
    curl -s $SHARKO/api/v1/secrets/status -H "Authorization: Bearer $TOKEN"
    ```

3. **Metrics** —

    ```bash
    curl -s $SHARKO/metrics | grep -E 'sharko_reconciler|sharko_managed_secrets_state'
    ```

    The useful ones: `sharko_reconciler_last_run_timestamp{engine="addon_values"}` (is it running), `sharko_reconciler_enabled` (is it switched on), `sharko_managed_secrets_state` (how many rows in each state), `sharko_reconciler_item_failures_total` (failure reason codes).

4. **Logs and audit** — the engine logs under a `[secrets]` prefix with a per-pass `request_id`; audit entries record every manual trigger and every actual write:

    ```bash
    kubectl logs deploy/sharko -n sharko --since=30m | grep '\[secrets\]'
    curl -s "$SHARKO/api/v1/audit?limit=20" -H "Authorization: Bearer $TOKEN"
    ```

    Log lines name the cluster, addon, secret name, and namespace — never a value. Error text from a credentials backend is replaced with one fixed sentence before it is logged, so even a misbehaving backend SDK can't leak through a log line.

Now the failure paths, one by one.

---

## 1. Definition missing or invalid

**What you see:** the row's status is `skipped` with the sentence *"the secret definition in the catalog has no \<field\> — fill that in and Sharko can push it"*; the engine strip says a definition is incomplete. A single-row action on a pair Git doesn't define at all is refused with a sentence naming the three possible reasons (cluster not registered, addon not enabled, no push block).

**Likely boundary:** your Git data files — the catalog entry's push block is missing its secret name, namespace, or keys.

**Safe fix:** complete the push block in `catalog.yaml` through the normal PR flow. Nothing was attempted against any cluster, so there is nothing to clean up.

## 2. Secret Sync is switched off

**What you see:** the engine strip says *"Addon values engine is switched off"*; `POST /api/v1/secrets/check` returns 409 with that sentence; the metric `sharko_reconciler_enabled{engine="addon_values"}` is `0`; the log says `[secrets] addon-values engine is switched off — skipping reconcile`. Rows keep showing their last-known facts — they don't go blank and don't pretend to be current.

**Likely boundary:** a deliberate admin choice (Settings → Addon Values Engine), often because another tool (e.g. External Secrets Operator) delivers these secrets.

**Safe fix:** if it should be on, an admin flips the switch in Settings. Note one deliberate exception: a single row's own Refresh and Sync buttons still work with the engine off — an explicit action on one secret is not a pass.

## 3. Backend authentication failure

*(Signals verified live 2026-08-16 — the playground's own log shows this path — and against the captured closure proofs, 2026-08-14, which additionally prove a backend fix is picked up immediately with no restart.)*

**What you see:** for an addon secret's value, the row shows an error with a fixed sentence — *"Sharko couldn't fetch a secret's value from the secrets store. Click Refresh to try again."* For a cluster's own sign-in details, read during a **repair attempt** (a write), the fixed sentence is *"Sharko could not read this cluster's sign-in details from the configured credentials source. The server log for this request id says which step failed."*

On the **connection page**, a plain check (not a repair) that can't read the credentials source shows a different fixed sentence — *"Sharko could not read this cluster's configured credentials source from the secrets backend, so it could not work out what the connection should look like. Check the secrets backend connection and try again."* — and the Repair button is withdrawn until a check succeeds. This is what the connection page shows immediately: it runs this check the moment you open the connection panel, and again every time you click **Check again**.

That same check also updates this cluster's row on the fleet list right away — the connection page and the fleet list share one record, so a check you just ran is never stale there. For a cluster nobody has opened a connection page for, a background pass re-checks every managed cluster on its own schedule instead: `SHARKO_CONNECTION_CREDENTIAL_CHECK_INTERVAL` (default 15 minutes). That background pass is what keeps the fleet list's credential-drift badge current for clusters you haven't looked at yourself.

**What the log carries:** the check itself only logs a `WARN` line naming the cluster — `[connection-comparison] could not read this cluster's configured credentials source`. It does not carry a `step=` field. `step=` only shows up in the log line from a **repair attempt** (a write — e.g. `step=sts`, `step=mint-eks-token`), never from a plain check.

**What metadata you may inspect:** the connection's configured backend type and namespace/prefix/region on the Settings page. If you're chasing a failed *repair* rather than a check, the log's `step=` field on that attempt narrows it further — but that field is repair-only.

**Likely boundary:** the backend identity — the IRSA role's IAM policy, the Kubernetes RBAC Role for the backend namespace, or a backend connection pointed at the wrong namespace/prefix/region.

**Safe fix:** fix the backend connection or its identity. Sharko resolves the live backend at use time, so the very next check sees the fix — no restart needed (proven in the captured closure evidence).

## 4. AWS prefix refusal

**What you see:** a canned refusal sentence naming the refused path and the configured prefix — e.g. *"…is outside the prefix … this AWS Secrets Manager connection is allowed to read"* — or, with no prefix configured at all, the sentence explaining that an empty prefix would mean the whole AWS account, so every read is refused until a prefix is set. The log has `[provider] GetSecretValue refused: path outside the configured boundary`. No AWS call was made.

**Likely boundary:** the in-code prefix boundary doing its job — the catalog's definition points outside the area you configured.

**Safe fix:** either the definition's path is wrong (fix it in the catalog via PR) or the secret genuinely lives outside the prefix — move it under the prefix in AWS. Think twice before widening the prefix: it is part of your blast-radius boundary (see [Permissions and Blast Radius](../architecture/permissions-and-blast-radius.md)).

## 5. Kubernetes namespace-boundary refusal

**What you see:** a canned sentence naming both namespaces: *"…points at namespace X, but this Kubernetes secrets connection is only allowed to read secrets in namespace Y"*. No API call was made.

**Likely boundary:** same as above, for the K8s backend — a definition path names a namespace other than the connection's one configured namespace.

**Safe fix:** fix the path in the catalog, or move the source secret into the configured namespace. The RBAC Role the chart generates only covers configured namespaces, so widening requires both a config change and `rbac.k8sSecretsProviderNamespaces`.

## 6. Destination TLS refusal

**What you see:** the row records a deliberate refusal: *"this cluster's connection is set up to skip certificate checks, so Sharko will not send a secret over it"*; the engine strip's version tells you to fix that cluster's connection. This fires for `insecure-skip-tls-verify: true`, ArgoCD's `insecure: true`, and plain `http://` servers alike.

**Likely boundary:** the destination cluster's own registration — its connection can't be certificate-verified.

**Safe fix:** re-register the connection with a proper CA bundle. There is no override: reads and deletes still work on such a cluster, but no secret value will ever be sent over an unverifiable wire.

## 7. Destination ownership conflict (foreign Secret)

*(Refusal sentences and never-write behavior pinned by code and tests — `internal/remoteclient/secrets.go`, `internal/secrets/ownership_gate_test.go`. The live break-and-diagnose exercise ran 2026-08-16, in the closure round: a second reviewer diagnosed a real foreign Secret live, and the refusal sentence was verified through the real Sync door. The state gauge and foreign-status plumbing were also verified live against the captured closure proofs, 2026-08-14.)*

**What you see:** the row's own **Refresh** answers right away with outcome `foreign` — **no error**, because this is a boundary, not a failure. That answer updates this row's state immediately, everywhere it's shown, including the fleet-wide Addon secrets table. A row nobody has clicked Refresh on only picks up `foreign` once the addon-values engine's own scheduled pass reaches it — its configured interval, `SHARKO_SECRET_RECONCILE_INTERVAL`, default five minutes. Either way, a Sync attempt through the API answers with the fixed sentence *"Someone else created this one — Sharko will not touch it."* Nothing was written, on any trigger, and no later pass "fixes" this by itself — an ownership refusal always needs a human decision (see Safe fix below).

**What metadata you may inspect** (metadata only — never the data):

```bash
kubectl get secret <name> -n <namespace> --context <cluster> \
  -o jsonpath='{.metadata.labels}{"\n"}{.metadata.annotations}{"\n"}'
```

A Sharko-owned Secret carries `app.kubernetes.io/managed-by: sharko` plus `sharko.dev/addon` / `sharko.dev/source` / `sharko.dev/written-at` annotations. A foreign one lacks the label — that's the whole verdict.

**Likely boundary:** the ownership gate — something else (a person, ESO, Sealed Secrets, a Helm chart) created a Secret with the name your catalog entry wants.

**Safe fix:** pick a different destination secret name in the catalog, or deliberately retire the other owner and delete its Secret through *that tool's* process, after which Sharko creates its own on the next pass. Never hand-label someone else's Secret as Sharko's — that transfers ownership of content Sharko has never verified.

## 8. Remote-cluster authorization or connectivity failure

**What you see:** the row's error sentence is *"Sharko couldn't get credentials for one of the clusters…"* or *"Sharko couldn't connect to one of the clusters…"* or *"Sharko tried to write a secret on a cluster and the write failed."* The metric `sharko_reconciler_item_failures_total` buckets these under reason codes (`credentials`, `connect`, `write_failed`), and the row's consecutive-failures count climbs — three in a row raises the row warning.

**Likely boundary:** the per-cluster credential (expired token, deleted ServiceAccount, missing RBAC on the destination namespace) or plain network reachability.

**Safe fix:** run the cluster's connection test/doctor from the Clusters page; check the destination ServiceAccount still has `get/create/update` on Secrets in the addon's namespace. The [per-cluster least-privilege example](../architecture/permissions-and-blast-radius.md#remote-cluster-credential-for-destination-writes) shows the intended shape.

## 9. Scheduled reconciliation not running

**What you see, and one honest quirk (verified live):** `sharko_reconciler_last_run_timestamp{engine="addon_values"}` advancing every interval (default 5 minutes) is the reliable "it's alive" signal. `GET /api/v1/secrets/status` shows the last pass **that had work or errors** — on an estate with *zero* addon-secret definitions it keeps reporting the zero time (`0001-01-01…`) forever while the engine is in fact running fine and logging `[secrets] no addons with secret definitions — nothing to reconcile` each pass. Check the metric and that log line before concluding the engine is stuck.

**Likely boundary:** the Sharko pod itself (crashlooping, just restarted), no Git connection yet (the log says `no Git connection — skipping reconcile`), or the engine switched off (path 2).

**Safe fix:** `kubectl get pods -n sharko`, then the log grep from the top of this page. Every pass logs `[secrets] reconcile started` with its own `request_id`.

## 10. Manual trigger refused

**What you see:** an HTTP status that says exactly which gate refused you:

| Status | Meaning | Fix |
|---|---|---|
| 401 | Not signed in / token invalid or expired | Sign in again or renew the token |
| 403 | Signed in below operator | Manual sync needs the operator role or higher |
| 409 | The engine is switched off (`/secrets/check`) | Settings → Addon Values Engine (admin) |
| 503 | No Secret Sync engine is configured (no backend wired) | Configure a secrets backend connection first |
| Refusal sentence | *"this cluster is not in the managed clusters list in Git — nothing to refresh"* / *"Git does not define an addon-values secret for this addon on this cluster — nothing to refresh"* | The definition has to land in Git first — there is no manual override |

**Likely boundary:** authentication, the role model, or the Git-approved-definitions rule — a manual trigger can never deliver something Git doesn't define.

## 11. Rotation not reaching the destination

**What you see:** you rotated a value in the backend, but the destination hasn't caught up. A read-only check (the row's **Refresh**, or `POST /api/v1/secrets/check`) reports the row `out_of_sync` — that check deliberately never writes. The scheduled pass, or the row's **Sync**, is what writes; after a successful write the row returns to in-sync and an audit entry records it. The last actual write time is also stamped on the destination Secret itself, safely inspectable as metadata:

```bash
kubectl get secret <name> -n <namespace> --context <cluster> \
  -o jsonpath='{.metadata.annotations.sharko\.dev/written-at}{"\n"}'
```

**Likely boundary:** none yet — `out_of_sync` after a check is the system working. If it *stays* out of sync across scheduled passes, some write is failing: look for the row's error sentence, which routes you to paths 3–8.

**Safe fix:** wait one interval or click Sync on the row. If the row shows `foreign`, that's path 7 — the rotation is reaching a Secret Sharko refuses to touch.

## 12. Sharko restart or outage

**What you see:** after a restart, delivery records are empty — rows read as "not checked since restart" (all records are in-memory, and the honest choice is a blank rather than a fabricated timestamp). `GET /api/v1/secrets/status` shows the zero time until the first completed pass with work.

**What it does NOT mean:** nothing on any cluster changed. Destination Secrets stay exactly as they were — shutdown makes zero calls to remote clusters (pinned by test), and existing addons keep running and syncing via ArgoCD. Only rotation delivery and repair paused during the outage.

**Safe fix:** none needed. The first scheduled pass (within the interval, default 5 minutes) rebuilds the records; an operator can hurry it with `POST /api/v1/secrets/check` (read-only) or `POST /api/v1/secrets/reconcile`.

---

## What this page never asks you to do

- Print a Secret's `.data`, `stringData`, or YAML — no command above does, and none should.
- Compare values, lengths, or hashes by hand — Sharko's own check does the comparison and reports only the verdict.
- Raise log verbosity as a debugging step — this page does not recommend it; the default log level already carries every signal listed above.

## Where to go next

- [The Engine and Secret Sync](../architecture/engine-and-secret-sync.md) — the data flow each failure path lives on.
- [Permissions and Blast Radius](../architecture/permissions-and-blast-radius.md) — the boundaries these refusals enforce.
- [Threat Model](../architecture/threat-model.md) — why the refusals are shaped this way.
- [Secrets (user guide)](../user-guide/secret-sync.md) — the same rows as a user sees them.
