# Git Webhook Handler Failures

**Severity:** P1

>  **Verified:** Re-checked 2026-08-21 against `main` HEAD. The handler
> response codes (`400`, `401`, `200`) and exact error bodies are verified
> verbatim against `internal/api/webhooks.go`. There is now exactly ONE
> refusal message — `"the Git webhook accepts only a request signed with
> the shared secret set by an operator"` — and it is what a caller gets
> whether no shared secret is set, no signature arrived, or a signature
> did not match. Those three cases used to answer differently, which let
> anyone ask the endpoint whether it was protected; they are one answer
> now on purpose, so **you cannot tell them apart from the response**.
> The 400s (`"could not read request body"`, `"invalid push event
> payload"`) are unchanged. HMAC-SHA256 verification at
> `verifyGitHubSignature` uses `hmac.Equal` for constant-time comparison;
> the shared secret is read from the `SHARKO_WEBHOOK_SECRET` env var.

The Git provider's webhook delivery is reaching Sharko but the
handler is rejecting it. The provider's webhook-management UI shows
delivery failures: HTTP 401 on signature-validation failure (the
common case) or HTTP 400 on body / payload parsing failure (rarer
but real).

This runbook covers two adjacent failure-mode rows from the
[failure-mode index](failure-mode-index.md):

- "Webhook handler returns 401 (Git provider webhook signature didn't
  validate)" — `internal/api/webhooks.go` writes the single refusal
  message with `HTTP 401`.
- "Webhook receive error (any code path)" — the broader bucket
  covering 400s (`"could not read request body"`,
  `"invalid push event payload"`) and any other non-2xx response.

Both share the same diagnosis tree: **is the webhook reaching us at
all? does the signature validate? did the payload parse?** They share
the same upstream surface (the Git provider's webhook delivery log)
and the same mitigation lanes (rotate the secret, re-create the
webhook, fix the payload shape). Per the
[style guide's grouping rule](../developer-guide/runbook-style-guide.md#when-to-write-one-runbook-vs-multiple),
these are documented as one runbook.

The blast radius is **bounded**: the cluster-secret reconciler still
runs on its 30s safety-net tick, so missed webhook events are
self-corrected within 30 seconds. The user-visible impact is
**PR-tracker state diverges from reality until the next poll** —
the dashboard surfaces stale `open` status for PRs that have
actually merged.

---

## Symptoms

What an operator sees when this fires:

- **GitHub webhook delivery log** (Settings -> Webhooks -> Recent
  Deliveries on the upstream repo) shows red X marks instead of green
  checks. The response code column is `401` (signature) or `400`
  (body/payload). The response body — visible by clicking the
  delivery row — is one of:

  ```json
  {"error":"the Git webhook accepts only a request signed with the shared secret set by an operator"}
  {"error":"could not read request body"}
  {"error":"invalid push event payload"}
  ```

- **Sharko logs** show the handler entry but no follow-up Info line
  saying `External push detected`:

  ```sh
  kubectl -n <sharko-ns> logs -l app=sharko --tail=2000 \
    | jq -c 'select(.msg | test("webhook|push detected|External push"; "i"))'
  ```

  Expected on success: a single `INFO` line `"External push
  detected"`. On signature failure no such line exists — the handler
  bails before the work block — but you may see access-log entries
  from the HTTP middleware with status 401.

- **PR-tracker state diverges**: a PR that was merged 5+ minutes ago
  still shows `open` in the dashboard PR panel; the underlying file
  was modified on the base branch (verifiable via direct GitHub UI
  check) but Sharko's view is stale.

  ```sh
  curl -sS -H "Authorization: Bearer ${SHARKO_TOKEN}" \
    "http://sharko/api/v1/prs?status=open" \
    | jq -r '.[] | select(.pr_url) | "\(.pr_id) \(.pr_url) \(.last_status)"'
  ```

  Cross-check each PR's actual status via:

  ```sh
  curl -sS -H "Authorization: token ${GITHUB_PAT}" \
    https://api.github.com/repos/<org>/<repo>/pulls/<id> | jq .state
  ```

  If GitHub says `"closed"` but Sharko says `"open"`, the webhook
  isn't getting through.

- **Reconciler self-heals within 30s** — within 30 seconds of the PR
  merge, the next safety-net reconciler tick reads the post-merge
  `managed-clusters.yaml` and converges. This is the bounded blast
  radius. Without webhook events, convergence latency rises from
  "seconds" to "up to 30 seconds." Operators usually catch the
  failure when the lag becomes user-visible.
- **No specific Prometheus alert fires for webhook 401s today.**
  This is a V2-4.x follow-up — wire a per-status-code metric on the
  webhook handler and alert on sustained 401 / 400 rate.

If the symptom is **HTTP 502** or **HTTP 5xx** on webhook delivery,
this is **not** the runbook — Sharko itself is unreachable, not
just the webhook handler. Jump to
[`oom-restart-loop.md`](oom-restart-loop.md) or the broader
[`reconciler-crash-loop.md`](reconciler-crash-loop.md).

---

## Diagnosis

Three checks: confirm the webhook is reaching Sharko at all, identify
the exact error shape, isolate signature-vs-payload failure.

### 1. Confirm webhook delivery reaches Sharko

The first question is "is the webhook even arriving?" If the Git
provider can't reach the Sharko ingress, the delivery log shows
connection errors (DNS, TCP refused, TLS) and the handler logs are
silent. Verify reachability from the public internet (or wherever
the provider lives):

```sh
# Test the webhook URL directly:
WEBHOOK_URL=https://sharko.example.com/api/v1/webhooks/git

curl -i -sS -X POST "$WEBHOOK_URL" \
  -H "X-GitHub-Event: ping" \
  -H "Content-Type: application/json" \
  --data-binary '{}'
```

Expected response: **HTTP 401 with the refusal message**. An unsigned
probe is always refused — including a `ping`, which your Git provider
signs like anything else. That response confirms the URL is reachable
and the handler is running; it says nothing about whether a shared
secret is configured, and it is not meant to.

If you get DNS failure, TCP refused, TLS handshake error, or HTTP
5xx, the webhook delivery isn't reaching the handler. Check ingress
configuration, NetworkPolicies, and the Sharko pod's health. This is
not the runbook for that.

### 2. Inspect the GitHub delivery log

In the upstream repo's `Settings -> Webhooks -> Recent Deliveries`,
click the most-recent failed delivery. The panel shows:

- The **Request** tab: full headers (including
  `X-Hub-Signature-256`) and JSON body.
- The **Response** tab: status code and body Sharko returned.

The response body's `error` field narrows it to one of three lanes:

| Response body | Lane |
|---|---|
| `"the Git webhook accepts only a request signed with the shared secret set by an operator"` | The signature did not satisfy the server. One message covers all of: no shared secret set on Sharko, no signature sent, signature does not match. Tell them apart from the server side (step 3), not from the response |
| `"could not read request body"` | TLS termination or proxy is mangling the body |
| `"invalid push event payload"` | Non-JSON or schema-incompatible body (e.g. webhook is configured for an event other than `push`) |

### 3. Verify the secret on both ends

Read Sharko's webhook secret from the deployment and compare with
the secret configured in the upstream webhook:

```sh
SHARKO_NS=<sharko-ns>
kubectl -n "$SHARKO_NS" get deployment sharko -o yaml \
  | yq '.spec.template.spec.containers[0].env[] | select(.name == "SHARKO_WEBHOOK_SECRET")'
```

Two outcomes:

- **`name: SHARKO_WEBHOOK_SECRET, valueFrom: { secretKeyRef: {...} }`** —
  read the actual value:

  ```sh
  SECRET_NAME=<from above>
  SECRET_KEY=<from above>
  kubectl -n "$SHARKO_NS" get secret "$SECRET_NAME" \
    -o jsonpath="{.data.$SECRET_KEY}" | base64 -d
  ```

- **Field missing entirely or value empty** — the server is running
  with no shared secret, so the webhook is closed and every call to it
  is refused. That is the expected state on a fresh install; it is not
  a fault, and nothing else is broken by it. Set the shared secret on
  both sides (Mitigation step 1) if you want the endpoint to work.

In the GitHub UI: `Settings -> Webhooks -> Edit -> Secret`. The
field shows `••••••••••• (Click to change)`. You cannot read the
existing value — you have to either regenerate or trust that they
match. The mitigation lane assumes you regenerate.

For the signature math, GitHub computes
`hmac.sha256(secret).hexdigest(body)` and sends
`sha256=<hex>` in `X-Hub-Signature-256`. Sharko's
`verifyGitHubSignature` (in `internal/api/webhooks.go`) does the same
computation and `hmac.Equal`s the result. With no shared secret set on
Sharko there is nothing to compute against, and the call is refused
rather than waved through.

---

## Mitigation (try in order)

1. **Re-configure the webhook with a freshly-generated shared
   secret.** The simplest fix and the one that always works. Generate
   a new random secret, set it in both the upstream webhook UI and
   Sharko's secret, and restart Sharko.

   ```sh
   # Generate a strong random secret.
   NEW_SECRET=$(openssl rand -hex 32)

   # Update Sharko's secret:
   kubectl -n "$SHARKO_NS" patch secret <sharko-release> \
     --type='json' \
     -p='[{"op":"replace","path":"/data/SHARKO_WEBHOOK_SECRET","value":"'"$(echo -n "$NEW_SECRET" | base64)"'"}]'

   kubectl -n "$SHARKO_NS" rollout restart deployment/sharko
   kubectl -n "$SHARKO_NS" rollout status deployment/sharko --timeout=120s

   # Then in the upstream repo: Settings -> Webhooks -> Edit -> paste
   # NEW_SECRET into the Secret field -> Update webhook.
   ```

   Success indicator: in the upstream delivery log, the next delivery
   (use the "Redeliver" button on any past delivery) shows HTTP 200.
   Sharko logs an `INFO` line `External push detected`.

2. **Fix the webhook event subscription.** If Diagnosis step 2 showed
   `"invalid push event payload"`, the webhook is configured to send
   events other than `push` (e.g. `pull_request`, `issues`,
   `discussions`). The handler is push-specific.

   In the upstream UI: `Settings -> Webhooks -> Edit -> Which events
   would you like to trigger this webhook?` -> select **Just the
   `push` event**. Save.

   Alternative: leave the subscription as-is but trust the handler's
   `X-GitHub-Event: ping` carve-out (it returns 200 on ping). The
   handler rejects only `push` events with malformed payloads —
   other event types get rejected at the JSON unmarshal stage with
   a 400. Operators who want to triage multiple event types should
   filter at the GitHub side.

   Success indicator: same as step 1 — successful redeliveries show
   HTTP 200.

3. **Check the body separately, without turning the check off.**
   There is no way to make Sharko accept an unsigned call — clearing
   `SHARKO_WEBHOOK_SECRET` closes the endpoint, it does not open it, so
   the old "unset it and see" trick tells you nothing. To find out
   whether the body is reaching Sharko intact, sign it yourself with
   the value Sharko actually has and send it from outside:

   ```sh
   SECRET=<the value read in Diagnosis step 3>
   BODY='{"ref":"refs/heads/main","commits":[]}'
   SIG="sha256=$(printf '%s' "$BODY" \
     | openssl dgst -sha256 -hmac "$SECRET" -r | cut -d' ' -f1)"

   curl -i -sS -X POST "$WEBHOOK_URL" \
     -H "X-GitHub-Event: push" \
     -H "X-Hub-Signature-256: $SIG" \
     -H "Content-Type: application/json" \
     --data-binary "$BODY"
   ```

   If that returns HTTP 200 but real deliveries do not, the body is
   being changed in transit — go to the ingress / proxy cause below.
   If it is refused too, Sharko's copy of the shared secret is not what
   you think it is; go back to step 1.

   Success indicator: the hand-signed call returns HTTP 200.

4. **Last resort — wait for reconciler convergence.** The cluster-
   secret reconciler runs its 30s safety-net tick regardless of
   webhook health. If webhook delivery is blocked end-to-end and
   step 1-3 don't resolve it within the operator's patience window,
   the fleet still converges within 30 seconds of any PR merge —
   PR-tracker state will be stale until the next dashboard refresh
   but cluster-secret state stays correct.

   Document the webhook gap and schedule the fix; the system is not
   in an emergency state, just degraded.

   Success indicator: cluster-secret state stays correct (verified
   via `audit?event=cluster_secret_reconcile`); PR-tracker state is
   stale until manual refresh (`POST /api/v1/prs/{id}/refresh`).

---

## Root-cause patterns

Four common causes.

### Webhook secret was rotated on one side but not the other

The most common cause. An operator rotated the webhook secret in
the GitHub UI (because the previous secret was compromised, or as
part of a security review) but didn't update Sharko's
`SHARKO_WEBHOOK_SECRET` env var. Every subsequent delivery returns
401.

Diagnostic signature: the webhook delivery log shows successful 200
responses up to a specific timestamp, then continuous 401s after —
corresponding to when the secret was rotated.

Fix lane: Mitigation step 1 (rotate both sides to a fresh secret).

### Webhook was created without specifying a secret

The webhook was set up in the GitHub UI without filling in the
Secret field. GitHub then sends no signature header at all, and every
delivery is refused with the single 401 message.

Diagnostic signature: the GitHub UI shows the Secret field as empty.
The response body cannot tell you this on its own — one message covers
every refusal — so this one is diagnosed from the provider side.

Fix lane: Mitigation step 1 (set the secret on both sides).

### Webhook event subscription is wider than `push`

GitHub allows subscribing to dozens of event types. If the operator
selected "Send me everything," the handler receives `pull_request`,
`issues`, `discussions`, `installation`, etc. events as `push`-shaped
JSON — none of them parse as `gitHubPushEvent`, so the handler
returns 400 `"invalid push event payload"`.

Diagnostic signature: Diagnosis step 2 shows the exact string
`"invalid push event payload"`. The webhook delivery log shows
mixed delivery types (some succeed, the rest 400).

Fix lane: Mitigation step 2 (narrow subscription to `push` only).

### TLS proxy / ingress is rewriting the body

Less common, but real. An ingress controller or TLS-terminating
proxy is decoding the body, modifying it (e.g. stripping whitespace,
re-encoding JSON), then re-emitting it. The HMAC is computed over
the original body; the re-encoded body produces a different HMAC;
the signature check fails.

Diagnostic signature: the hand-signed call in Mitigation step 3
succeeds when sent from close to the pod but fails through the
ingress, and provider deliveries fail with the 401. The TLS / ingress
configuration was changed recently.

Fix lane: configure the ingress / proxy to pass the body through
without modification. nginx-ingress, for example, defaults to
buffering but not modifying; if `nginx.ingress.kubernetes.io/configuration-snippet`
or similar is rewriting JSON, remove the snippet. This is an
ingress configuration issue, not a Sharko issue.

---

## Prevention

How to make this failure mode less likely going forward. Three
levers:

- **Monitoring — expose a webhook delivery success metric.** Sharko does
  not export this metric today. The alert below is a design sketch for a
  future release, not something you can deploy now. The sketch: record
  per-status-code rates on the webhook handler (e.g.
  `sharko_webhook_requests_total{status="200|400|401"}`) and alert on a
  sustained 4xx rate above 5% over 5 minutes. Wiring this into
  `internal/metrics/` and `prometheusrules.yaml` is a V2-4.x follow-up.
  The bounded-impact nature (the reconciler self-heals) keeps this at P1
  even with full webhook breakage; the alert would catch the silent
  state-divergence earlier.

- **Gating — webhook secret in installation runbook.** The chart ships
  `secrets.webhookSecret` empty, and while it is empty the webhook is
  closed — so an operator who never sets it has a closed endpoint, not
  an open one. The installation runbook should still call out: "Set
  `SHARKO_WEBHOOK_SECRET` in your secret, and use the same value in the
  GitHub webhook's Secret field." A pre-flight check at
  Sharko startup that says "webhook secret is set; webhook is
  configured with matching secret? (run a self-test)" would catch
  rotation drift at the moment it happens. The self-test belongs in
  a `sharko self-test webhook` CLI subcommand (V2-4.x follow-up).

- **Scheduled work — quarterly webhook redelivery drill.** A
  scheduled task that picks a random recent delivery in the GitHub
  UI and clicks Redeliver, then verifies via Sharko's audit log
  that the redelivery succeeded. The drill catches the slow drift
  cases (TLS proxy update, secret rotation that worked at the time
  but broke later) before they accumulate.

---

## Related runbooks

- [`git-provider-unreachable.md`](git-provider-unreachable.md) — the
  P0 sibling. If signed webhooks succeed but Sharko's outbound calls
  to the same Git provider fail, the failure is asymmetric — Sharko
  can be reached but can't reach out.
- [`argocd-pr-merge-no-converge.md`](argocd-pr-merge-no-converge.md)
  — when PRs merge but ArgoCD doesn't see the change. Webhook
  failures are one possible cause (PR-tracker is stale; reconciler
  hasn't run); 30s safety-net tick still corrects this.
- [`cluster-reconciler.md`](cluster-reconciler.md) — the 30s
  safety-net tick that bounds webhook-failure blast radius.
- [`auto-merge-failed-after-pr-opened.md`](auto-merge-failed-after-pr-opened.md)
  — when the PR didn't merge at all (so there's nothing for the
  webhook to deliver).
- [`failure-mode-index.md`](failure-mode-index.md) — master inventory.
- [`../developer-guide/logging.md`](../developer-guide/logging.md) —
  `request_id` correlation pattern used throughout.

## Escalation

If the mitigations above do not resolve the failure within 2 hours
and webhook delivery remains broken, email the maintainer:
`moran.weissman@gmail.com`. Include:

- The runbook URL you used (this page)
- The output of Diagnosis step 1 (ping probe response)
- The output of Diagnosis step 2 (failed delivery error body)
- Screenshot of the GitHub webhook delivery log (Recent Deliveries)
- The Sharko version (`sharko version` or the Helm chart version)

The maintainer is a single human, not a 24x7 rotation. Expect a
business-day SLA. Because the reconciler self-heals within 30s,
webhook failures are not a paging-severity incident even when
sustained — the fleet continues to converge.

<!-- Style-guide compliance checklist (V2-4.1):
- [x] Title matches `# <Failure name>`
- [x] Severity line present (P1)
- [x] Verified-by-execution header + date
- [x] Symptoms section before Diagnosis
- [x] Symptoms include exact log lines / error messages
- [x] Diagnosis has 3+ concrete checks
- [x] Mitigation uses numbered list
- [x] Mitigation has 3-5 steps in priority order (4 steps)
- [x] Root-cause patterns: 2+ named causes (4 named)
- [x] Prevention section present and non-empty
- [x] Related runbooks section present
- [x] Intro is operator-on-call voice
- [x] Length 300-800 lines (in range)
- [x] All cross-links resolve (mkdocs build verifies)
- [x] No emoji / no internal Slack / employee email
- [x] (if applicable) Alert reference noted as V2-4.x follow-up
-->
