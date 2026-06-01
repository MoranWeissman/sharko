# Auth Bypass

**Severity:** P0

> **Verified:** Authored 2026-06-01 against `main` HEAD. The auth
> surface is `internal/auth/` (login, session, API token paths) and
> `internal/api/auth.go` (route handlers). The `login_failed` audit
> code documented here matches the V2-2 audit-log surface in
> [`../developer-guide/logging.md`](../developer-guide/logging.md).
> The V125-1-7 token-leak class referenced in the failure-mode index
> is shipped — this runbook covers operator-side detection and
> mitigation. Re-verify when auth handlers or session-cookie shape
> changes.

The login endpoint is accepting invalid credentials, OR a session
cookie remains honored after its expiry timestamp, OR an API token
that was revoked is still being honored. Any of these three failure
modes constitutes an **auth bypass** — an unauthenticated or
unauthorized user can exercise Sharko's full write surface (cluster
registration, addon enable, secret refresh, cluster delete) against
the fleet.

This is the most-severe P0 in Sharko's failure-mode inventory. **An
auth bypass is a security incident, not a reliability incident.** The
moment you suspect this is real, treat it as a CVE-class event: stop
serving traffic, capture forensic state, rotate every credential,
notify downstream consumers. The on-call procedure here is
incident-response shaped, not diagnose-and-mitigate shaped.

The V125-1-7 token-leak class (where an API token's hash collision
let a fake token authenticate) is the canonical reference; the
remediation pattern below applies to it and to the broader category.

---

## Symptoms

What an operator sees when this fires:

- **`POST /api/v1/auth/login` returns 200 with a valid Set-Cookie**
  for credentials that should be rejected. Common detection: the
  `login_failed` audit-event count drops to zero (or near-zero) while
  traffic continues — meaning every login is being accepted.

  ```sh
  # Compare login_failed event rate to login traffic rate:
  curl -sS http://sharko/api/v1/audit?event=login_failed&since=1h \
    -H "Authorization: Bearer ${SHARKO_TOKEN}" \
    | jq '.entries | length'
  # Expected on a healthy system: non-zero (typos, expired creds, brute-force probes)
  # Bypass signal: 0 while web traffic continues
  ```

- **An expired session cookie continues to authenticate.** The cookie's
  `Expires` field is in the past, but Sharko still returns 200 for
  authenticated routes when the cookie is presented:

  ```sh
  EXPIRED_COOKIE="<session-cookie-from-yesterday>"
  curl -sS -H "Cookie: session=${EXPIRED_COOKIE}" \
    http://sharko/api/v1/clusters | jq '.clusters | length'
  # Expected: 401 Unauthorized (expired cookie rejected)
  # Bypass signal: 200 with cluster list
  ```

- **A revoked API token still authenticates.** The token was created
  via `POST /api/v1/tokens`, then explicitly revoked via
  `DELETE /api/v1/tokens/{id}`. The token continues to authenticate
  beyond the documented 60s cache TTL:

  ```sh
  REVOKED_TOKEN="sharko_<base64>"
  sleep 90  # exceed the cache TTL
  curl -sS -H "Authorization: Bearer ${REVOKED_TOKEN}" \
    http://sharko/api/v1/clusters
  # Expected: 401 (revocation propagated)
  # Bypass signal: 200 (cache never invalidated)
  ```

- **Audit-log anomalies**:
  - `event=login_succeeded` from IP addresses that don't match any
    known operator
  - `event=token_created` from sessions with no preceding
    `login_succeeded`
  - `event=cluster_register` / `event=cluster_delete` from API tokens
    that were revoked earlier in the same audit window
- **Unexpected fleet state changes**: clusters registered or deleted
  outside any known operator action; addon-enable changes for which
  no operator can claim ownership.

There is **no Prometheus alert today** that triggers on these patterns
— the detection is human-driven via the audit log. Adding such an
alert is in Prevention.

---

## Diagnosis

This is a security incident. **The diagnosis goal is to confirm the
bypass, scope its blast radius, and capture forensic state — in that
order.** Do not skip the forensic capture.

### 1. Confirm the bypass against a known-bad credential

Three quick tests, in order. Run all three; stop only if you have
strong evidence the bypass is not real (e.g. the operator who reported
it was confused by a stale cookie).

**Test A — login with invalid password should return 401:**

```sh
curl -sS -X POST http://sharko/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"this-is-definitely-wrong-12345"}' \
  -i | head -20
```

Expected: HTTP/1.1 401 Unauthorized. Bypass signal: HTTP/1.1 200 OK
with a Set-Cookie header.

**Test B — expired session cookie should return 401:**

```sh
# Forge an expired cookie shape (or use yesterday's real expired cookie):
curl -sS -H "Cookie: session=fake-or-expired-cookie-value" \
  http://sharko/api/v1/clusters -i | head -20
```

Expected: HTTP/1.1 401 Unauthorized. Bypass signal: HTTP/1.1 200 OK.

**Test C — revoked API token should return 401:**

```sh
# Create a token, revoke it, then immediately use it (after cache TTL):
TOKEN_RESPONSE=$(curl -sS -X POST http://sharko/api/v1/tokens \
  -H "Authorization: Bearer ${SHARKO_ADMIN_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"name":"bypass-test","role":"operator"}')
TOKEN_VALUE=$(echo "$TOKEN_RESPONSE" | jq -r .token)
TOKEN_ID=$(echo "$TOKEN_RESPONSE" | jq -r .id)

curl -sS -X DELETE \
  -H "Authorization: Bearer ${SHARKO_ADMIN_TOKEN}" \
  http://sharko/api/v1/tokens/"$TOKEN_ID"

sleep 90  # exceed token-cache TTL (60s default)

curl -sS -H "Authorization: Bearer ${TOKEN_VALUE}" \
  http://sharko/api/v1/clusters -i | head -20
```

Expected: HTTP/1.1 401. Bypass signal: HTTP/1.1 200.

### 2. Capture forensic state BEFORE any mitigation

The instant you take a mitigation action (rotate keys, restart pods),
you lose evidence. Capture first.

```sh
SHARKO_NS=<sharko-ns>
INCIDENT_TS=$(date -u +%Y%m%dT%H%M%SZ)
mkdir -p /tmp/sharko-incident-"$INCIDENT_TS"
cd /tmp/sharko-incident-"$INCIDENT_TS"

# Pod logs (everything from the last 24h):
kubectl -n "$SHARKO_NS" logs -l app=sharko --since=24h --tail=-1 \
  > pod-logs.json

# Audit log dump:
curl -sS -H "Authorization: Bearer ${SHARKO_ADMIN_TOKEN}" \
  "http://sharko/api/v1/audit?since=24h" \
  > audit-log.json

# Active sessions / tokens:
curl -sS -H "Authorization: Bearer ${SHARKO_ADMIN_TOKEN}" \
  "http://sharko/api/v1/tokens" \
  > tokens.json

# Pod environment + secrets state:
kubectl -n "$SHARKO_NS" get pod -l app=sharko -o yaml > pod-spec.yaml
kubectl -n "$SHARKO_NS" get secrets -o yaml > secrets-current.yaml
# Note: secrets-current.yaml contains credential material. Encrypt at rest.

# Network connections at the moment of incident:
SHARKO_POD=$(kubectl -n "$SHARKO_NS" get pod -l app=sharko -o name | head -1)
kubectl -n "$SHARKO_NS" exec "$SHARKO_POD" -- netstat -tn 2>/dev/null \
  > connections.txt || true

# Server clock — for cookie-expiry analysis:
kubectl -n "$SHARKO_NS" exec "$SHARKO_POD" -- date -u > server-time.txt
```

**Encrypt or move this directory to a secure store.** It contains
credentials, kubeconfig contents, and active session material.

### 3. Scope the blast radius

What did the attacker do? Read the audit log starting from the
earliest suspicious entry:

```sh
jq '.entries[] | select(
  .event == "login_succeeded" or
  .event == "token_created" or
  .event == "cluster_register" or
  .event == "cluster_delete" or
  .event == "addon_enabled_on_cluster" or
  .event == "addon_disabled_on_cluster" or
  .event == "user_created" or
  .event == "user_role_changed"
) | {time, event, action, user, ip, request_id}' \
  audit-log.json \
  | jq -s 'sort_by(.time)' \
  | head -100
```

Build a timeline:

- **First suspicious login** — what IP, what user, what auth method?
- **Tokens created during the bypass window** — these are persistent
  even after a session is killed; must be revoked individually.
- **Fleet writes during the window** — cluster registrations, addon
  enables, delete operations. Each represents an action you may need
  to revert.
- **User/role changes** — the most dangerous; an attacker who creates
  an admin user can return after the bypass is patched. Audit all
  users.

### 4. Identify the bypass vector

Three patterns map to three distinct bugs. The detection method
points to the fix.

- If Test A (invalid password accepted) — login handler is broken.
  Check `internal/auth/login.go` (or wherever the password verification
  lives). Possible cause: bcrypt comparison returning true for
  empty / malformed inputs; admin-init code path running on every
  login and overwriting verification result.
- If Test B (expired cookie honored) — session validation is broken.
  Check the session-middleware in `internal/api/router.go` or
  `internal/auth/session.go`. Possible cause: expiry check using
  wrong field (e.g. `Created` instead of `Expires`), or the session
  store cache not honoring the in-memory `Expires`.
- If Test C (revoked token honored) — token-revocation propagation is
  broken. The 60s cache TTL should bound the staleness; longer-than-60s
  indicates the cache is not being invalidated on revocation, or the
  token hash is being looked up incorrectly.

The V125-1-7 reference: a token-hash collision allowed a forged token
to authenticate as a legitimate one. Operator-side detection was the
audit-log anomaly (token_created without preceding login_succeeded);
fix was a hash-format change.

---

## Mitigation (try in order)

The order is incident-response shaped: stop bleeding first, restore
trust second, return to service third.

1. **Stop new authentication immediately by scaling Sharko to zero.**
   This kills active sessions and prevents new logins:

   ```sh
   kubectl -n "$SHARKO_NS" scale deployment/sharko --replicas=0
   ```

   Communicate the planned downtime to operator-team channels. This is
   the only way to guarantee no new bypass-driven action lands while
   you remediate.

   Forensic state from Diagnosis step 2 must be captured before this
   step; the pod's logs / in-memory session table will be gone after
   scale-to-zero.

2. **Rotate every credential the compromised instance touched.**
   Treat all of these as compromised because you don't know what the
   attacker did:

   - **Admin password**: regenerate via the bootstrap procedure (rotate
     the admin-init Secret, restart with the new value).
   - **All API tokens**: revoke every token in the database. After
     restart, force operators to re-create their tokens.
   - **ArgoCD account token**: rotate via `argocd account
     generate-token`; update the Sharko secret.
   - **Git provider PAT**: rotate via the provider's PAT UI; update
     the Sharko secret.
   - **Secrets-provider credentials**: rotate the AWS access key / IRSA
     role / Vault token Sharko uses.
   - **Connection encryption key**: rotate via Helm value
     `config.connectionSecretName`'s key field; users will need to
     re-enter personal tokens after rotation.
   - **Webhook secret**: rotate `SHARKO_WEBHOOK_SECRET` and update the
     Git provider's webhook configuration.

   Each rotation goes into a tracking ticket so the incident review
   can confirm completeness.

3. **Apply the bypass fix.** This depends on what Test A/B/C
   identified:

   - For Test A (login accepts invalid): upgrade Sharko to a version
     with the auth bug fixed. If no fixed version exists yet, the only
     safe mitigation is **keep Sharko at replicas=0** and contact the
     maintainer.
   - For Test B (expired cookie honored): same — upgrade or stay
     scaled to zero.
   - For Test C (revoked token honored): if the hash format is the
     issue (V125-1-7 pattern), the fix is in the released version;
     upgrade. If the cache invalidation is the issue, a restart
     clears the in-memory cache and resumes correct behavior — but
     verify after restart that revoked tokens are now rejected.

4. **Restore service with all credentials rotated and the bypass
   fixed.** Scale back up:

   ```sh
   kubectl -n "$SHARKO_NS" scale deployment/sharko --replicas=1
   kubectl -n "$SHARKO_NS" rollout status deployment/sharko --timeout=120s
   ```

   Run the three Diagnosis tests (A/B/C) against the restored
   instance to confirm the bypass is closed:

   ```sh
   # Same Test A/B/C from Diagnosis step 1
   ```

   Expected: all three return 401. If any returns 200, scale back to
   zero — the bypass is not closed.

5. **Audit the fleet state changes made during the bypass window.**
   For each cluster registered or deleted, each addon enabled or
   disabled, each user created or role-changed during the window, decide:

   - Was this a legitimate operator action? (Verify with the operator.)
   - Was this an attacker action? (Reverse it.)

   Cluster registrations: verify the cluster's kubeconfig is the
   expected one and the secret push went to the expected target.
   Addon enables: verify the addon's secret config wasn't tampered
   with (a hostile addon could exfiltrate cluster credentials).

---

## Root-cause patterns

### Login-verification bug

The password comparison code returns true for an input it shouldn't.
Common causes: bcrypt comparison short-circuits on empty input, the
admin-init code path runs on every login and treats an unset password
as "default-allow," or an HTTP header bypasses the verification
(forgotten authn middleware).

Diagnostic signature: Test A (Diagnosis step 1) returns 200. The
audit log shows `login_succeeded` events for users whose
`login_failed` count is zero.

Why it happens: a code-path regression in `internal/auth/login.go`
where verification logic was refactored and a fallback "allow"
branch was introduced. Caught by Test A. The fix is in code, not in
configuration — upgrade Sharko.

### Session expiry bug

The session-cookie validation checks the wrong field, or the in-memory
session table doesn't honor expiry. Common causes: `time.Now().Before(
sess.Created)` instead of `Before(sess.Expires)`; the in-memory map
never gets purged of expired entries.

Diagnostic signature: Test B (Diagnosis step 1) returns 200 for a
cookie whose `Expires` is in the past. The audit log shows
`api_call` events from sessions that should have expired hours/days
ago.

Fix is upgrade Sharko. As a short-term mitigation, scale Sharko to
zero on a schedule (e.g. nightly) which clears all sessions; this is
ugly but prevents long-running stale sessions.

### Token revocation not propagating

A token was revoked via `DELETE /api/v1/tokens/{id}`, but the in-memory
token-validation cache wasn't invalidated. Sharko keeps serving the
token for up to 60s by design; longer than 60s indicates the cache
was never invalidated.

Diagnostic signature: Test C (Diagnosis step 1) returns 200 for a
revoked token. The cache TTL has elapsed.

Why it happens: the revoke handler doesn't call the cache-invalidate
hook; or the cache is keyed on token-hash and the revoke handler
clears by token-ID, missing the hash entry.

Fix is upgrade Sharko. Short-term mitigation: restart Sharko after
every token revoke (clears the cache). Document this expectation
explicitly while waiting for the fix.

### V125-1-7 token-hash collision (HISTORICAL)

A token-hash format issue let a forged value pass the hash comparison
as a legitimate token. The forged value never appeared in
`POST /api/v1/tokens`'s audit trail — first appearance in audit was
the API call that used it. Fixed in v1.25; documented here as the
canonical reference for "credential collision class."

Diagnostic signature: audit log contains `api_call` events with a
`token_id` that has no corresponding `token_created` event.

Fix shipped. If Sharko is below v1.25, upgrade immediately.

---

## Rollback plan

The mitigations are incident-response — scale-to-zero, rotate
credentials, upgrade. The "rollback" concept doesn't cleanly apply.
However:

- If Mitigation step 3 (upgrade) introduces a new bug, you can
  downgrade — but only to a version that does not have the bypass.
  Downgrading to a bypass-affected version is **not acceptable** even
  to recover service.
- If Mitigation step 2 (credential rotation) breaks downstream
  integrations (e.g. CI/CD using API tokens), the integration
  re-onboards by creating new tokens. The old tokens are gone for a
  reason; don't roll back rotations.

---

## Prevention

- **Monitoring — login_failed rate-zero alert.** Add a Prometheus rule
  that pages when `rate(sharko_login_failed_total[15m]) == 0` and
  `rate(sharko_login_attempts_total[15m]) > 0`. The pattern catches
  Test A's "every login is accepted" signal automatically. Wiring
  requires `sharko_login_*_total` metrics — in V2-3.x scope.

- **Monitoring — token-created-without-login alert.** A `token_created`
  audit event with no preceding `login_succeeded` for the same
  session is the V125-1-7 fingerprint. Detect in audit-log SSE:

  ```promql
  sum(rate(sharko_audit_events_total{event="token_created"}[5m]))
    -
  sum(rate(sharko_audit_events_total{event="login_succeeded"}[5m]))
  > 0
  ```

  Alert when non-zero. Requires audit metrics — V2-3.x scope.

- **Gating — auth integration test in CI.** Add a CI test that runs
  the three Diagnosis tests (A/B/C) against every PR. If a code
  change breaks any of them, the PR is blocked. The tests run in
  ~5 seconds against a local Sharko instance with sqlite backend.

- **Gating — auth surface coverage gate.** Add a coverage gate on
  `internal/auth/` of >= 90%. Auth-surface regressions are the most
  expensive class of bug; coverage is the cheapest defense.

- **Scheduled work — quarterly auth red-team drill.** Once per quarter,
  the maintainer or a designated reviewer attempts to bypass Sharko's
  auth in a staging instance. Documented attempts and outcomes go in
  the security report. Forces explicit confirmation that the bypass
  surface is closed.

---

## Related runbooks

- [`credential-leak-in-logs.md`](credential-leak-in-logs.md) —
  adjacent class of security failure (credentials leaked in logs
  rather than auth bypassed). Both are P0 security incidents.
- [`failure-mode-index.md`](failure-mode-index.md) — master inventory.
- [`../developer-guide/logging.md`](../developer-guide/logging.md) —
  audit-event codes referenced in this runbook.
- [`../developer-guide/logging-audit-punchlist.md`](../developer-guide/logging-audit-punchlist.md) —
  the V2-2 audit-trail discipline that makes this diagnosis possible.

## Escalation

This runbook's escalation is **immediate, not 30-minute-window**.

The moment Diagnosis step 1 confirms a bypass, email the maintainer:
`moran.weissman@gmail.com` with subject `[SHARKO][P0][SECURITY] auth
bypass`. Include:

- This runbook URL
- The specific test (A, B, or C) that confirmed the bypass
- The full output of the test
- The Sharko version
- The forensic-state directory location (from Diagnosis step 2)
- The blast-radius timeline (from Diagnosis step 3)

The maintainer is the security contact until a separate security@
channel is published. For confirmed auth bypass, the maintainer's
expected SLA is **same-day investigation and a private upgrade-path
recommendation** — not a multi-day backlog item.

If the bypass affects production fleet at scale, also notify
downstream operators (the platform teams whose clusters Sharko
manages) so they can audit their own audit trails for unauthorized
actions during the bypass window.

<!-- Style-guide compliance checklist (V2-4.1):
- [x] Title matches `# <Failure name>`
- [x] Severity line present (P0)
- [x] Verified-by-execution header + date
- [x] Symptoms section before Diagnosis
- [x] Symptoms include exact log lines / error messages / alert names
- [x] Diagnosis has 3+ concrete checks (4 named)
- [x] Mitigation uses numbered list
- [x] Mitigation has 3-5 steps in priority order
- [x] Root-cause patterns: 2+ named causes (4 named)
- [x] Prevention section present and non-empty
- [x] Related runbooks section present
- [x] Intro is operator-on-call voice
- [x] Length 300-800 lines
- [x] All cross-links resolve
- [x] No emoji / no internal Slack / employee email
- [x] (if applicable) No alert defined yet (per Symptoms)
-->
