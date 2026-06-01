# Git Provider Upstream Unreachable

**Severity:** P0

> **Verified:** Authored 2026-06-01 against `main` HEAD. The 502 error
> string `"no active Git connection"` is verified verbatim against
> `internal/api/addon_ops.go:79,196`, `internal/api/addons_write.go:74,248,338`,
> `internal/api/ai_annotate.go:102,252`, `internal/api/clusters_adopt.go:55,165`,
> and `internal/api/cluster_secrets.go:143` as shipped. The
> `request_id` correlation pattern referenced in Diagnosis is the V2-2.2
> shipped surface ([`../developer-guide/logging.md`](../developer-guide/logging.md)).
> Re-verify before changing the `writeError` body string or the V2-3
> recording-rule names.

Every API path that needs to open a PR is failing. Every cluster
registration, every adoption, every addon enable/disable, every secret
refresh that goes through Git is returning 502. The fleet is in a frozen
state: no writes can land, but the existing reconciled state is intact
(read-only operations and ArgoCD self-management still work). Page
on-call.

This runbook is the mirror image of
[`argocd-upstream-unreachable.md`](argocd-upstream-unreachable.md) but
for the Git provider — GitHub, GitLab, Bitbucket, or whatever
`internal/git/` is configured to talk to. The diagnosis path is the same
shape (probe the upstream, probe the auth, probe the network) but the
provider-specific details differ.

---

## Symptoms

What an operator sees when this fires:

- HTTP `502 Bad Gateway` response from every API write that opens a PR:
  `POST /api/v1/clusters`, `POST /api/v1/clusters/batch`,
  `POST /api/v1/clusters/{name}/adopt`,
  `DELETE /api/v1/clusters/{name}`, `PATCH /api/v1/clusters/{name}`,
  `POST /api/v1/clusters/{name}/secrets/refresh`,
  `POST /api/v1/addons`, `DELETE /api/v1/addons/{name}`,
  `PATCH /api/v1/addons/{name}`, `POST /api/v1/addons/{name}/upgrade`,
  `POST /api/v1/addons/upgrade-batch`, `POST /api/v1/ai/annotate-values`.
- Response body verbatim: `{"error":"no active Git connection: <err>"}`
  where `<err>` is the underlying transport / auth error (DNS resolution,
  TCP refused, TLS handshake, 401/403, 429 rate limit, or context
  timeout).
- UI: the Cluster create/update/delete buttons fail with a banner
  "Git connection lost — cannot open PR." The Marketplace and Catalog
  read pages still render (catalog is local-state), but every action
  that would open a PR errors immediately.
- `kubectl logs` line burst (one per failed handler invocation):

  ```
  {"time":"...","level":"WARN","msg":"git reachability probe failed","request_id":"req-...","error":"..."}
  ```

- Audit-log entries with `action=pr_open` and `result=failed` cluster in
  a tight time window. PR-tracker poll lines (`request_id="prtrack-..."`)
  show repeated retry attempts that all fail.
- Alerts that fire when this is sustained:
  - `SharkoClusterRegistrationFastBurn` (PR-open is in the
    cluster_registration SLO path) — see
    [`budget-burn-runbook.md`](budget-burn-runbook.md#sharkoclusterregistrationfastburn)
  - `SharkoAddonCycleFastBurn` (PR-open is in the addon_cycle SLO path) —
    see
    [`budget-burn-runbook.md`](budget-burn-runbook.md#sharkoaddoncyclefastburn)

If the symptom is *one* PR failing while others succeed, this is not the
runbook — that's per-PR (branch protection, merge conflict). This
runbook is for the **fleet-wide** "every PR-open fails" case.

---

## Diagnosis

Three checks, in this order. Each narrows whether the failure is in the
Git provider's API, in Sharko's auth, or in the network path between
them.

### 1. Is the Git provider's API itself up?

Don't trust the status page; probe it from where it matters. From a
host with internet access:

```sh
curl -sS -o /dev/null -w "%{http_code}\n" https://api.github.com/zen
# (substitute api.gitlab.com / your-bitbucket-host for non-GitHub)
```

Expected: `200`. If you get a 5xx or a timeout, the provider is having
an outage. Cross-reference the provider's status page
(`https://www.githubstatus.com/`, `https://status.gitlab.com/`) and
wait it out — Sharko has no fix here, but the symptom is correct.

### 2. Is Sharko's PAT (Personal Access Token) still valid?

Read the PAT from the secret and probe the provider's authenticated
endpoint:

```sh
SHARKO_NS=<sharko-ns>
GITHUB_TOKEN=$(kubectl -n "$SHARKO_NS" get secret <sharko-release> \
  -o jsonpath='{.data.GITHUB_TOKEN}' | base64 -d)

curl -sS -H "Authorization: Bearer ${GITHUB_TOKEN}" \
  https://api.github.com/user
```

Three outcomes:

- **JSON with the user/account/login matching what Sharko is configured
  for** — PAT is valid. The failure is intermittent, rate-related, or
  scoped (the PAT lacks a specific scope; see step 3).
- **`401 Bad credentials`** — PAT was revoked, deleted, or rotated
  out-of-band. Jump to root cause "PAT expired or revoked".
- **`403 Forbidden`** with `X-RateLimit-Remaining: 0` — rate-limited.
  Jump to root cause "Rate limit exhausted".

For GitLab, the equivalent probe:

```sh
curl -sS -H "PRIVATE-TOKEN: ${GITLAB_TOKEN}" \
  https://gitlab.example.com/api/v4/user
```

For Bitbucket:

```sh
curl -sS -u "<workspace>:${BITBUCKET_TOKEN}" \
  https://api.bitbucket.org/2.0/user
```

### 3. Does the PAT have the right scopes?

A PAT can authenticate (`/user` returns 200) but lack the scopes Sharko
needs to write to the bootstrap repo. Probe the repo directly:

```sh
ORG=<your-org>
REPO=<your-bootstrap-repo>

curl -sS -H "Authorization: Bearer ${GITHUB_TOKEN}" \
  -o /dev/null -w "%{http_code}\n" \
  "https://api.github.com/repos/${ORG}/${REPO}"

# Probe write access (this is a dry-run — branch list does not write):
curl -sS -H "Authorization: Bearer ${GITHUB_TOKEN}" \
  "https://api.github.com/repos/${ORG}/${REPO}/branches" \
  -o /dev/null -w "%{http_code}\n"

# Probe PR-open permission (will return 422 if scopes are fine):
curl -sS -H "Authorization: Bearer ${GITHUB_TOKEN}" \
  -X POST \
  -H "Content-Type: application/json" \
  -d '{"title":"probe","head":"nonexistent","base":"main"}' \
  "https://api.github.com/repos/${ORG}/${REPO}/pulls" \
  -o /dev/null -w "%{http_code}\n"
```

Expected status codes:

- Repo GET: `200` (404 means the org/repo name is wrong or the PAT
  doesn't have access — jump to root cause "PAT missing repo scope").
- Branches GET: `200` (`403` means missing `repo` scope, or the
  organisation requires SAML SSO sign-on for PAT — see root cause
  "Organization SAML SSO required").
- PR POST probe: `422 Unprocessable Entity` (this is correct — the
  request is malformed, but the auth and scope were accepted). `403`
  means the PAT lacks `repo` write scope; `404` means the repo is
  invisible to the PAT.

### 4. Is the network path from Sharko to the Git provider working?

Exec into the Sharko pod and probe directly:

```sh
SHARKO_POD=$(kubectl -n "$SHARKO_NS" get pod -l app=sharko -o name | head -1)
kubectl -n "$SHARKO_NS" exec "$SHARKO_POD" -- \
  wget -q -O - --no-check-certificate \
  "https://api.github.com/zen"
```

If this fails with "connection refused" or "connection timed out" while
the external probe in Diagnosis step 1 succeeds, the failure is a
NetworkPolicy, an egress proxy, or a corporate-MITM TLS issue — see
root cause "Egress / NetworkPolicy block" and the
[corporate-mitm-tls runbook](corporate-mitm-tls.md).

### 5. Cluster log-driven check: `jq` the Sharko logs

```sh
kubectl -n "$SHARKO_NS" logs -l app=sharko --tail=2000 \
  | jq -c 'select(.msg | test("git|no active Git|rate limit"; "i"))' \
  | head -50
```

A burst of `"git reachability probe failed"` lines clustered in a window
pinpoints when reachability broke. Cross-reference with the Git
provider's status page or recent egress-policy changes in the cluster
to identify the trigger.

---

## Mitigation (try in order)

Most-likely-to-restore-fastest first. Stop at the first step that
restores reachability — don't keep walking the list.

1. **If Diagnosis step 1 showed the provider is down — wait it out.**
   No Sharko-side fix exists. Pause inflight automation that would
   otherwise queue requests:

   ```sh
   # Pause any CI/cron that calls Sharko's write API
   kubectl -n <ci-ns> patch cronjob/cluster-onboarder \
     -p '{"spec":{"suspend":true}}'
   ```

   Sharko itself does not retry hard against the provider — every API
   call is one shot per request. The PR-tracker poll continues at its
   cadence and will succeed automatically once the provider returns.

   Success indicator: Diagnosis step 1's `curl` against
   `https://api.github.com/zen` returns `200`.

2. **Rotate the PAT.** If Diagnosis step 2 returned 401, the PAT was
   revoked. Create a new PAT with the required scopes:

   - GitHub fine-grained PAT: select the bootstrap repo, grant
     `Contents: Read and write`, `Pull requests: Read and write`,
     `Metadata: Read`.
   - GitHub classic PAT: `repo` scope (full), `workflow` scope (for
     PR-merge with required status checks).
   - GitLab: `api` scope at minimum; `read_repository` + `write_repository`
     if the project uses scoped tokens.
   - Bitbucket: workspace app password with `Repositories: Read, Write`
     and `Pull requests: Read, Write`.

   Update the secret and restart Sharko:

   ```sh
   NEW_PAT=<paste>
   kubectl -n "$SHARKO_NS" patch secret <sharko-release> \
     --type='json' \
     -p="[{\"op\":\"replace\",\"path\":\"/data/GITHUB_TOKEN\",\"value\":\"$(echo -n "$NEW_PAT" | base64)\"}]"
   kubectl -n "$SHARKO_NS" rollout restart deployment/sharko
   kubectl -n "$SHARKO_NS" rollout status deployment/sharko --timeout=120s
   ```

   Success indicator: a synthetic PR-open via
   `POST /api/v1/clusters/{name}/test` or a no-op
   `PATCH /api/v1/addons/{name}` returns success. Logs show
   `"git reachability probe ok"`.

3. **Wait out the rate limit, then back off.** If Diagnosis step 2
   returned 403 with `X-RateLimit-Remaining: 0`, the PAT's quota is
   exhausted. The window is hourly; check the reset time:

   ```sh
   curl -sS -I -H "Authorization: Bearer ${GITHUB_TOKEN}" \
     https://api.github.com/user 2>&1 \
     | grep -E "(X-RateLimit-Remaining|X-RateLimit-Reset)"
   ```

   `X-RateLimit-Reset` is a Unix timestamp. Convert and wait:

   ```sh
   date -d @$(curl -sS -I -H "Authorization: Bearer ${GITHUB_TOKEN}" \
     https://api.github.com/user 2>&1 \
     | awk -F: '/X-RateLimit-Reset/ {gsub(/\r/,"",$2); print $2+0}')
   ```

   Then back off the cadence — pause batch registration / mass adoption
   that's exhausting the budget. To prevent recurrence, rotate to a
   PAT for a different account (GitHub authenticated requests are
   accounted per-account, not per-token).

4. **Repair the NetworkPolicy or egress path.** If Diagnosis step 4
   showed Sharko's pod can't reach the provider while an external host
   can, the egress is blocked. Inspect:

   ```sh
   kubectl get networkpolicy -n "$SHARKO_NS"
   kubectl get networkpolicy -n kube-system  # CNI-imposed default policies
   ```

   If a recently applied policy blocks egress to `*.github.com`,
   `*.gitlab.com`, or your provider's domain, add an egress-allow
   policy:

   ```yaml
   apiVersion: networking.k8s.io/v1
   kind: NetworkPolicy
   metadata:
     name: allow-sharko-egress-to-git
     namespace: <sharko-ns>
   spec:
     podSelector:
       matchLabels:
         app: sharko
     policyTypes:
       - Egress
     egress:
       - to:
           - namespaceSelector: {}  # allow DNS
         ports:
           - protocol: UDP
             port: 53
       - to:
           - ipBlock:
               cidr: 0.0.0.0/0
               except:
                 - 10.0.0.0/8
                 - 172.16.0.0/12
                 - 192.168.0.0/16  # internal-only blocks
         ports:
           - protocol: TCP
             port: 443
   ```

   If an egress proxy is in play (corporate environment), set
   `HTTPS_PROXY` and `NO_PROXY` in the Sharko deployment env vars per
   the [corporate-mitm-tls runbook](corporate-mitm-tls.md).

   Success indicator: the wget probe in Diagnosis step 4 returns 200.

5. **Last resort — switch to a backup PAT or backup provider.** If the
   primary PAT can't be rotated immediately (account locked, SSO
   re-authorization pending), Sharko can be reconfigured to use a
   different PAT for the same repo. If your organization keeps a
   break-glass account with PAT pre-generated, swap to it:

   ```sh
   BREAKGLASS_PAT=<from-secrets-store>
   kubectl -n "$SHARKO_NS" patch secret <sharko-release> \
     --type='json' \
     -p="[{\"op\":\"replace\",\"path\":\"/data/GITHUB_TOKEN\",\"value\":\"$(echo -n "$BREAKGLASS_PAT" | base64)\"}]"
   kubectl -n "$SHARKO_NS" rollout restart deployment/sharko
   ```

   The break-glass account must have the same scopes on the bootstrap
   repo. Document the rotation in the audit-log.

---

## Root-cause patterns

### Provider outage

The provider itself (GitHub, GitLab, etc.) is in a partial or full
outage. Sharko reports unreachable correctly; the failure is genuinely
upstream.

Diagnostic signature: the external probe in Diagnosis step 1 fails or
returns 5xx. The provider's status page lists an active incident. Sharko's
log burst aligns with the incident's start time.

Fix is in the provider, not in Sharko. Mitigation step 1 (pause CI/cron
that calls Sharko) prevents queue buildup. Sharko self-recovers once
the provider returns — the next API call from a client triggers the
PR-open path, and the first successful probe flips
`git_reachable: false → true`.

### PAT expired or revoked

The PAT Sharko was deployed with is no longer accepted by the provider.
Network path is fine, but every authenticated call returns 401 or
`"Bad credentials"`.

Diagnostic signature: Diagnosis step 2 returns 401. The audit log shows
PR-open attempts clustering at the moment of failure; before that
moment, the same PR-open path succeeded.

Why it happens (in priority order):

- Someone revoked the PAT manually via the provider's UI ("audit our
  PATs and revoke unused ones" rollout). Sharko's PAT was misidentified
  as unused because it doesn't have a human owner who'll notice.
- The PAT expired. GitHub fine-grained PATs default to 30 / 60 / 90-day
  expiry; the operator who created the PAT didn't set "no expiry" and
  the calendar hit.
- The PAT's account hit an SSO re-authentication requirement and PAT
  authentication is suspended pending sign-in. Common in
  enterprise GitHub with SAML SSO enforced.

Fix is Mitigation step 2 (rotate PAT). For SSO-bound PATs, the operator
must visit the GitHub PAT page and click "Configure SSO" → "Authorize"
for each org the PAT needs to access.

### Rate limit exhausted

The PAT's hourly quota is consumed. GitHub authenticated calls have a
5000/hour limit per account; a burst of batch registration / fleet-wide
adoption can blow through this in minutes.

Diagnostic signature: Diagnosis step 2 returns 403 with
`X-RateLimit-Remaining: 0`. The log burst starts after a known
high-volume operation (mass registration, fleet adoption, catalog
re-scan).

Why it happens: Sharko's batch endpoints
(`POST /api/v1/clusters/batch`, `POST /api/v1/addons/upgrade-batch`)
issue one provider call per cluster / per addon. A 200-cluster batch on
a single PAT exhausts the quota.

Fix is Mitigation step 3 (wait out the reset), and prevention is to
shard onto multiple accounts or use a GitHub App (App-installed
credentials have a higher and separate quota — 5000/hour per
installation).

### Egress / NetworkPolicy block

A cluster-level NetworkPolicy, a CNI default-deny, or an egress proxy
config blocks Sharko → provider. Common after a security review,
post-CNCF policy rollout, or after a corporate proxy is enforced.

Diagnostic signature: Diagnosis step 4 fails (in-pod probe times out)
while Diagnosis step 1 succeeds (external probe works). DNS resolves
fine; TCP doesn't.

Fix is Mitigation step 4 (add egress-allow policy). For corporate-MITM
environments, the additional fix is to install the corporate CA into
Sharko's trust store — see the
[corporate-mitm-tls runbook](corporate-mitm-tls.md).

---

## Rollback plan

Mitigation steps 1, 3, 4 are non-destructive; no rollback needed.

For Mitigation step 2 (PAT rotation) — if the new PAT has fewer scopes
than the old one and other operations break, the rollback path:

1. Restore the previous PAT from your secrets-store backup (HashiCorp
   Vault audit log, AWS Secrets Manager version history, etc.).
2. Patch the secret back and restart Sharko.

For Mitigation step 5 (break-glass account) — after the incident:

1. Revoke the break-glass PAT explicitly (don't leave it active).
2. Document the break-glass usage in the audit log.
3. Update the break-glass procedure if there were friction points.

---

## Prevention

- **Monitoring — pre-page on PAT rate-limit headroom.** Add a Sharko
  recording rule that scrapes `X-RateLimit-Remaining` from the most
  recent provider call and alerts when it drops below 20%:

  ```promql
  sharko_github_rate_limit_remaining_ratio < 0.20
  ```

  This pages before exhaustion, not after. Wiring requires Sharko to
  surface the rate-limit headers as a metric — a P1 follow-up under
  the V2-3.x metric backlog.

- **Gating — make the PAT no-expiry, and document the rotation cadence.**
  Set the PAT to "no expiry" at creation, and put a 90-day rotation
  drill on the operator calendar. The drill rotates the PAT in
  staging, verifies Sharko picks up the new token, then rotates in
  production. The cadence catches drift before a real incident.

- **Gating — use a GitHub App, not a PAT, for the bootstrap repo.**
  GitHub Apps have a separate rate-limit budget, finer-grained
  permissions, and an audit-log that distinguishes machine-driven
  calls from human ones. Sharko's roadmap (V3+) tracks GitHub App
  support; until it ships, a PAT for a dedicated machine-user
  account with no SSO entanglement is the closest approximation.

- **Scheduled work — egress-allow NetworkPolicy template in the
  Helm chart.** Add `charts/sharko/templates/networkpolicy-egress.yaml`
  (conditional on `networkPolicies.enabled`) so a default-deny
  cluster ships with the right egress-allow rule. Prevents the
  NetworkPolicy regression cause when corporate security tightens.

---

## Related runbooks

- [`argocd-upstream-unreachable.md`](argocd-upstream-unreachable.md) —
  the mirror failure mode for the ArgoCD upstream. If both fail at
  once, the cause is cross-namespace / cluster-wide (NetworkPolicy
  default-deny applied, kube-apiserver under pressure, node-level
  iptables corruption).
- [`corporate-mitm-tls.md`](corporate-mitm-tls.md) — corporate-proxy
  TLS-interception failures. Surfaces with similar 502s when the
  corporate CA isn't in Sharko's trust store.
- [`budget-burn-runbook.md`](budget-burn-runbook.md) — V2-3 burn-rate
  alerts. `SharkoClusterRegistrationFastBurn` /
  `SharkoAddonCycleFastBurn` fire when this failure is sustained.
- [`failure-mode-index.md`](failure-mode-index.md) — master inventory.
- [`../developer-guide/logging.md`](../developer-guide/logging.md) —
  `request_id` correlation pattern.

## Escalation

If the mitigations above do not resolve the failure within 30 minutes,
email the maintainer: `moran.weissman@gmail.com`. Include:

- The runbook URL you used (this page)
- The provider name (GitHub, GitLab, Bitbucket, on-prem name)
- The provider status-page snapshot if an outage is in progress
- The Sharko version
- A 5-minute window of relevant logs filtered by `request_id` per the
  [correlation pattern](../developer-guide/logging.md#correlation-ids)
- The output of Diagnosis steps 1, 2, and 4

The maintainer is a single human, not a 24×7 rotation.

<!-- Style-guide compliance checklist (V2-4.1):
- [x] Title matches `# <Failure name>`
- [x] Severity line present (P0)
- [x] Verified-by-execution header + date
- [x] Symptoms section before Diagnosis
- [x] Symptoms include exact log lines / error messages / alert names
- [x] Diagnosis has 3+ concrete checks (5 named)
- [x] Mitigation uses numbered list
- [x] Mitigation has 3-5 steps in priority order
- [x] Root-cause patterns: 2+ named causes (4 named)
- [x] Prevention section present and non-empty
- [x] Related runbooks section present
- [x] Intro is operator-on-call voice
- [x] Length 300-800 lines
- [x] All cross-links resolve
- [x] No emoji / no internal Slack / employee email
- [x] (if applicable) Alert name from prometheusrules.yaml referenced
-->
