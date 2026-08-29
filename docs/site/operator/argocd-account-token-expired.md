# ArgoCD Account Token Expired or Revoked

**Severity:** P1

> **Verified:** Authored 2026-06-01 against Sharko as shipped. Sharko
> talks to ArgoCD with an ArgoCD **account token** as a bearer token —
> not a Kubernetes ServiceAccount. Both read and write calls surface
> ArgoCD's 401/403 with the status code preserved in the error. The
> audit-log signal for the success counterpart is
> `cluster_secret_create`, written by the cluster-Secret writer and by
> the cluster reconciler. This failure is distinct from the "ArgoCD
> unreachable" P0 case: connectivity is fine, the request is just
> unauthorized.
> Reviewed 2026-08-29 — wording only; no step in this runbook changed.

ArgoCD's HTTP API is **reachable** from the Sharko pod (TCP connect
+ TLS succeed), but every authenticated call returns 401 Unauthorized
or 403 Forbidden. The cause is almost always: the ArgoCD account
token Sharko was configured with has expired, was rotated upstream
without re-provisioning, or was explicitly revoked.

This is **distinct from "ArgoCD unreachable"**
([`argocd-upstream-unreachable.md`](argocd-upstream-unreachable.md)) —
that's a P0 because the network path is down, no calls succeed at
all, and the fleet is in an unknown state. **Token expiry is P1**
because the connectivity story is intact, the failure is localized to
auth, and the fix is a credential rotation — the rest of the fleet
state is preserved.

Operators commonly hit this after a planned ArgoCD upgrade (the
account tokens config-map gets clobbered), after a security cleanup
(tokens reviewed and revoked), or after a long-lived deployment where
the token's documented lifetime expired. The fix is to mint a new
token in ArgoCD and re-provision it into the Sharko pod's secret. The
runbook walks through identifying which of the three sub-cases
applies and how to apply the new token cleanly.

---

## Symptoms

What an operator sees when this fires:

- **Every cluster registration / addon-cycle write operation that
  goes through ArgoCD fails with 401 or 403** in the API response
  body, with the wrapped reason from the ArgoCD upstream:

  ```
  HTTP/1.1 502 Bad Gateway
  {"error":"argocd cluster register failed: 401 Unauthorized: {\"error\":\"invalid session\",\"code\":16,\"message\":\"invalid session\"}"}
  ```

  Or:

  ```
  {"error":"argocd cluster register failed: 403 Forbidden: {\"error\":\"permission denied\"}"}
  ```

- **Sharko logs the failure at error level on every ArgoCD call**:

  ```
  {"time":"...","level":"ERROR","msg":"argocd client error","request_id":"req-...","method":"POST","url":"https://argocd-server.argocd.svc/api/v1/clusters","status":401,"body":"invalid session"}
  ```

  The `body` field carries the upstream error verbatim. Repeated
  401 entries from `argocd_call=*` log lines confirm the failure is
  auth, not transport.

- **ArgoCD-touching audit events stop succeeding** — no
  `cluster_secret_create` actions, no `addon_enabled_on_cluster`
  successes. The audit log shows failed write attempts that all
  point at the same upstream auth error:

  ```sh
  curl -sS http://sharko/api/v1/audit?limit=20 \
    -H "Authorization: Bearer ${SHARKO_TOKEN}" \
    | jq '[.[] | select(.result=="failed" and (.error // "" | test("401|403|unauthorized|permission denied"; "i")))] | .[] | {time, action, cluster, error}'
  ```

- **Cluster TEST operations on existing ArgoCD-shaped Secrets still
  succeed** (the test path reads from the K8s Secret, not from
  ArgoCD's API). This is a critical distinguishing signal — TEST
  works, but REGISTER / ADOPT / ADDON-WRITE fail. Operators sometimes
  misread this as "Sharko works for tests but not for writes" without
  realizing it's an upstream auth failure.

- **No specific Prometheus alert fires** on a clean 401/403 boundary
  today. Sustained write-failures fan into
  [`SharkoClusterRegistrationFastBurn`](budget-burn-runbook.md#sharkoclusterregistrationfastburn)
  /
  [`SharkoAddonCycleFastBurn`](budget-burn-runbook.md#sharkoaddoncyclefastburn)
  but the alert doesn't distinguish auth failure from generic
  registration failure.

If the symptom is "every ArgoCD call returns 502 connection refused"
or "no active ArgoCD connection," this is the P0 unreachable case —
jump to
[`argocd-upstream-unreachable.md`](argocd-upstream-unreachable.md).

If the symptom is "writes succeed but the cluster never appears in
ArgoCD" (PR merged, audit shows `pr_merged` then nothing), this is
[`argocd-pr-merge-no-converge.md`](argocd-pr-merge-no-converge.md).

---

## Diagnosis

Four checks. Step 1 confirms 401/403 is the actual failure shape
(not 502). Step 2 confirms ArgoCD itself works (rule out the upstream
being broken). Step 3 distinguishes expired vs revoked vs rotated.
Step 4 inspects the Sharko pod's current token secret.

### 1. Confirm the failure is 401/403, not connection-level

```sh
SHARKO_POD=$(kubectl -n <sharko-ns> get pod -l app=sharko -o name | head -1)

# What ArgoCD server URL does Sharko have configured? Ask Sharko — the URL
# lives on the active connection, and the pod env only carries it when the
# install declares SHARKO_CONN_ARGOCD_SERVER_URL, which most do not.
ARGOCD_SERVER=$(curl -sS -H "Authorization: Bearer ${SHARKO_ADMIN_TOKEN}" \
  "http://sharko/api/v1/connections/" \
  | jq -r '.connections[] | select(.is_active) | .argocd_server_url')

# Empty means no URL was configured and Sharko discovered one at runtime
# from the argocd-server Service in the ArgoCD namespace.

# Probe ArgoCD's /api/v1/version with NO auth to confirm reachability:
kubectl -n <sharko-ns> exec "$SHARKO_POD" -- \
  wget --server-response -q -O /dev/null "$ARGOCD_SERVER/api/v1/version" 2>&1 \
  | grep "HTTP/"
```

Expected: `HTTP/1.1 200 OK` (ArgoCD's `/version` is unauthenticated)
or `HTTP/1.1 401` (it's a write-only auth). Either confirms TCP+TLS+
HTTP all work. If you see `HTTP/1.1 502` or a connect timeout, this
is the P0 case — jump to
[`argocd-upstream-unreachable.md`](argocd-upstream-unreachable.md).

### 2. Confirm ArgoCD is healthy upstream

```sh
ARGOCD_NS=<argocd-namespace>
kubectl -n "$ARGOCD_NS" get pods
kubectl -n "$ARGOCD_NS" logs deploy/argocd-server --tail=200 \
  | grep -iE 'auth|token|jwt|deny|forbid' | head -20
```

You're looking for `Auth failed: ...` lines from `argocd-server`
that correlate with the Sharko failure timestamps. The
`argocd-server` logs the rejected token's prefix in the failure
message; if you see "session not found" or "token expired" or
"token revoked" — that confirms this runbook.

### 3. Distinguish expired vs revoked vs rotated

ArgoCD-server's log line lets you pick which:

- **Expired** — log line contains `"token is expired"` or
  `"exp claim"`. The token's `exp` is past current wall-clock.
- **Revoked** — log line contains `"token has been revoked"` or
  `"session not found"`. An admin called the revoke API or rotated
  the account's token.
- **Rotated without re-provision** — log line contains
  `"invalid signature"` or `"key not found"`. The ArgoCD account's
  signing key changed (often via upgrade); the old token's signature
  no longer verifies.

```sh
kubectl -n "$ARGOCD_NS" logs deploy/argocd-server --since=1h \
  | jq -c 'select(.msg // "" | test("token|session|JWT|auth"; "i"))' \
  | head -10
```

If the logs are not structured-JSON (older ArgoCD), grep for the
phrases above. Each maps to a different mitigation lane.

### 4. Inspect the Sharko pod's current ArgoCD token

**Where the token actually lives.** Sharko does not take its ArgoCD token
from a deployment environment variable. The token is part of the active
connection, held AES-256-GCM encrypted inside the connection Secret
(`sharko-connections` by default, `config.connectionSecretName` in Helm
values) and decrypted in memory with `SHARKO_ENCRYPTION_KEY`. You cannot
read it out of the pod spec, and there is no `SHARKO_ARGOCD_TOKEN`
environment variable — no Sharko build reads that name.

The one exception is dev mode: with `SHARKO_DEV_MODE=true` and no token on
the connection, Sharko falls back to the plain `ARGOCD_TOKEN` environment
variable (no `SHARKO_` prefix). That fallback is for local development and
should never be how a production install is wired.

Confirm which source is in play:

```sh
# Dev-mode fallback in use? Both of these must be set for it to apply.
kubectl -n <sharko-ns> get deployment sharko \
  -o jsonpath='{.spec.template.spec.containers[0].env}' \
  | jq -c '.[] | select(.name == "SHARKO_DEV_MODE" or .name == "ARGOCD_TOKEN") | {name, hasValue: (.value != null)}'

# Otherwise the token is on the connection. Ask Sharko, not kubectl:
curl -sS -H "Authorization: Bearer ${SHARKO_ADMIN_TOKEN}" \
  "http://sharko/api/v1/connections/" \
  | jq '{active: .active_connection, connections: [.connections[] | {name, is_active, argocd_server_url, argocd_token_masked}]}'
```

The API never returns the token value. To inspect the JWT's claims you need
the copy you issued in ArgoCD, or you issue a fresh one — which is the
mitigation below anyway. If you still hold the plaintext:

```sh
echo "$TOKEN" | cut -d'.' -f2 | base64 -d 2>/dev/null | jq
```

Look at `exp` (expiration), `iat` (issued-at), `sub` (subject /
account name). If `exp` is in the past, the token expired. If `sub`
doesn't match an account that exists in ArgoCD, the account was
deleted.

---

## Mitigation (try in order)

1. **Mint a new ArgoCD account token and re-provision it.** This is
   the standard fix for expired or revoked tokens.

   Log into ArgoCD as an admin (CLI):

   ```sh
   argocd login <argocd-host> --username admin --password '<password>'

   # Get the current account that Sharko uses:
   argocd account get-user-info
   # Note the username (typically "sharko" — match against the existing
   # JWT's sub claim from Diagnosis step 4).

   # Mint a new token for the account:
   NEW_TOKEN=$(argocd account generate-token --account sharko --expires-in 365d)
   echo "Token minted; first 30 chars: ${NEW_TOKEN:0:30}..."
   ```

   Update the Sharko-side Secret:

   ```sh
   kubectl -n <sharko-ns> patch secret "$TOKEN_SECRET_NAME" \
     --type='json' \
     -p="[{\"op\":\"replace\",\"path\":\"/data/argocd-token\",\"value\":\"$(echo -n "$NEW_TOKEN" | base64 -w0)\"}]"
   ```

   Restart the Sharko pod so it re-reads the Secret:

   ```sh
   kubectl -n <sharko-ns> rollout restart deployment/sharko
   kubectl -n <sharko-ns> rollout status deployment/sharko
   ```

   Success indicator: a fresh `POST /api/v1/clusters/<name>/test`
   returns 200; the audit log shows new `cluster_secret_create`
   actions succeeding; ArgoCD's `argocd-server` log stops emitting
   auth-failure lines for the Sharko user.

2. **If Diagnosis step 3 indicated "rotated without re-provision"
   (signature mismatch), the ArgoCD signing key was rotated.**
   Common after ArgoCD upgrades that clobbered
   `argocd-secret`'s `server.secretkey`.

   Run Mitigation step 1 — minting a new token uses the new signing
   key, so the new token will verify. Existing tokens for any
   account are invalidated by this rotation; you'd need to
   re-provision every consumer (not just Sharko) if there are
   other clients.

3. **If the account itself was deleted (Diagnosis step 4 `sub`
   doesn't match any current account), re-create the account in
   ArgoCD.** ArgoCD accounts are configured in the
   `argocd-cm` ConfigMap:

   ```sh
   kubectl -n "$ARGOCD_NS" edit configmap argocd-cm
   ```

   Ensure there's an entry like:

   ```yaml
   data:
     accounts.sharko: apiKey
     accounts.sharko.enabled: "true"
   ```

   Then set the RBAC rules:

   ```sh
   kubectl -n "$ARGOCD_NS" edit configmap argocd-rbac-cm
   ```

   Ensure the Sharko user has the required policy:

   ```yaml
   data:
     policy.csv: |
       p, role:sharko-writer, clusters, *, *, allow
       p, role:sharko-writer, projects, *, *, allow
       p, role:sharko-writer, repositories, *, *, allow
       p, role:sharko-writer, applications, get, */*, allow
       p, role:sharko-writer, applications, create, */*, allow
       g, sharko, role:sharko-writer
   ```

   The applications line is deliberately split rather than written as
   `applications, *, *`. The wildcard verb includes `sync`, and Sharko
   does not need `sync` for addons to be deployed: Git holds the desired
   state and ArgoCD applies it without being asked. Granting `sync` is a
   separate, optional decision, and it should be scoped to Sharko's own
   AppProject rather than to `*/*` — see
   [Letting Sharko restart a sync](security.md#letting-sharko-restart-a-sync).

   After the ConfigMap changes, re-mint the token (Mitigation step
   1).

4. **If the token-rotation Helm value path is wrong (token re-
   provisioning didn't take effect), debug the Sharko-side
   credential injection.** The token is stored on the active
   connection, encrypted in the connection Secret — not in the
   deployment's environment. Two things usually go wrong: the new
   token was saved onto a connection that is not the active one,
   or `SHARKO_ENCRYPTION_KEY` changed so the stored value can no
   longer be decrypted.

   ```sh
   # Which connection is active, and is Sharko happy with it?
   curl -sS -H "Authorization: Bearer ${SHARKO_ADMIN_TOKEN}" \
     "http://sharko/api/v1/connections/" \
     | jq '{active: .active_connection, connections: [.connections[] | {name, is_active, argocd_token_masked}]}'

   # Decryption failures show up in the pod log at startup and on save:
   kubectl -n <sharko-ns> logs deployment/sharko --tail=200 | grep -i "decrypt\|encryption key"
   ```

   If decryption is the problem, re-enter the token through
   **Settings → Connections** with the current key in place. If the new
   token went onto the wrong connection, make the right one active from
   the same page (or `POST /api/v1/connections/active`).

5. **Last resort — bypass account-token auth and use admin
   credentials temporarily.** This is for the case where ArgoCD's
   account configuration is broken and you can't fix it quickly.
   Mint a short-lived token from the admin account:

   ```sh
   argocd login <argocd-host> --username admin --password '<admin-pw>'
   SHORT_TOKEN=$(argocd account generate-token --account admin --expires-in 4h)
   ```

   Apply it via Mitigation step 1's patch. The 4-hour expiry forces
   you to fix the real account before the short-term token expires.
   **Admin tokens are full-cluster privilege** — use only for the
   shortest possible window.

---

## Root-cause patterns

### Token reached its documented expiry

The token was minted with `--expires-in <duration>` (e.g. 365d).
After the duration, ArgoCD rejects every call. No one revoked it;
no one rotated; the calendar caught up.

Diagnostic signature: Diagnosis step 4's `exp` claim is in the past;
Diagnosis step 3's ArgoCD log shows `"token is expired"`.

Fix is Mitigation step 1 — mint a new token with a longer lifetime
or a documented rotation schedule.

### Token revoked by an admin

A security review walked through ArgoCD's active tokens (via
`argocd account get` or the UI's Settings → Tokens) and revoked the
Sharko token because it appeared unused, looked suspicious, or was
issued by someone no longer on the team.

Diagnostic signature: ArgoCD log shows `"token has been revoked"`
or the token's entry is missing from
`argocd account get sharko --show-tokens`.

Fix is Mitigation step 1 plus a conversation with the security
team to document the token's purpose and renewal cadence (Prevention).

### ArgoCD upgraded; signing key rotated

A `helm upgrade argocd` (or a manual reapply) regenerated the
`argocd-secret`'s `server.secretkey`, invalidating every issued
token's signature. Sharko's token still has a valid `exp` claim
but the signature won't verify against the new key.

Diagnostic signature: Diagnosis step 3 shows `"invalid signature"`
or `"key not found"`; the failure-start time aligns with the
ArgoCD-server pod restart timestamp.

Fix is Mitigation step 2 (same as step 1, plus re-provision other
consumers if there are any).

### ArgoCD account deleted

An admin removed the `accounts.sharko` entry from `argocd-cm` —
sometimes accidentally (mass-edit), sometimes deliberately. The
token's `sub` claim no longer matches a registered account.

Diagnostic signature: ArgoCD log shows
`"account 'sharko' does not exist"`; Diagnosis step 4 shows the
token is otherwise valid but `argocd account get sharko` returns
not-found.

Fix is Mitigation step 3 (re-create the account) followed by step
1 (mint a new token).

### Secret key rotation tool clobbered the Sharko token

A platform-wide secrets-rotation tool rotated all K8s Secrets in
the Sharko namespace, including the ArgoCD-token Secret. The new
value is invalid (random bytes, not a JWT); Sharko sends it to
ArgoCD on every call; ArgoCD rejects with "invalid signature."

Diagnostic signature: Diagnosis step 4's token preview doesn't
look like a JWT (no `eyJ` prefix); the secret's
`metadata.managedFields` shows a recent update from a non-Sharko
controller.

Fix is Mitigation step 1 plus excluding the Sharko token from
your automatic rotation tool (or wiring the tool to also re-mint
in ArgoCD).

---

## Prevention

- **Monitoring — token-expiry watch.** Sharko does not export this
  metric today. The alert below is a design sketch for a future release,
  not something you can deploy now. The sketch: expose
  `sharko_argocd_token_expires_in_seconds` as a gauge — decode the JWT's
  `exp` claim at startup and on every refresh — so operators could alert
  at T-7d. Today, the only signal is the failure itself after expiry.

- **Gating — startup token-health probe.** Sharko at startup should
  call `argocd account get-user-info` (or any minimally-privileged
  authenticated endpoint) and refuse to start if it returns
  401/403. Catches the misconfiguration before any operation fails.
  Already partially in place via the V125-1-9 startup wiring; verify
  it covers the auth-failure case.

- **Gating — exclude Sharko token from auto-rotation tools.** If
  your platform runs a secrets-rotation controller (CertManager,
  custom controller, etc.), explicitly exclude the ArgoCD-token
  Secret. A `cert-manager.io/disable-auto-renew: "true"` or
  equivalent label on the Secret is the standard pattern.

- **Documentation — token lifetime + rotation contract.** Document
  the operator runbook for token rotation: mint with
  `--expires-in 365d`, calendar a rotation 30 days before expiry,
  re-mint via Mitigation step 1, verify with cluster test, retire
  the old token in ArgoCD's UI. Reduce the surprise factor.

- **Scheduled work — quarterly auth-health audit.** A periodic
  job (a CronJob in `<sharko-ns>` or a manual ticket) that runs
  the Diagnosis steps 1-4 and confirms the token still has >90d
  to expiry, still resolves to a valid account, and still passes
  a write-probe.

- **Failover — secondary token for break-glass.** Mint a second
  account-token, store it in a sealed-secret somewhere accessible
  to on-call, but NOT injected into Sharko by default. When the
  primary fails, swap. Avoids the "ArgoCD admin login required" path
  of Mitigation step 5.

---

## Related runbooks

- [`argocd-upstream-unreachable.md`](argocd-upstream-unreachable.md)
  — P0 escalation when the connection itself is broken (not just
  auth).
- [`argocd-cluster-secret-corruption.md`](argocd-cluster-secret-corruption.md)
  — adjacent failure: a specific cluster's ArgoCD Secret is
  malformed (per-cluster, not fleet-wide).
- [`argocd-pr-merge-no-converge.md`](argocd-pr-merge-no-converge.md)
  — sibling failure: Sharko writes succeed but ArgoCD doesn't
  converge (different upstream issue).
- [`cluster-reconciler.md`](cluster-reconciler.md) — V125-1-8
  reconciler context; reconciler also fails on auth errors.
- [`auth-bypass.md`](auth-bypass.md) — P0 security-related auth
  failure on the Sharko-side (operators authenticating to Sharko,
  not Sharko-to-ArgoCD).
- [`budget-burn-runbook.md#sharkoclusterregistrationfastburn`](budget-burn-runbook.md#sharkoclusterregistrationfastburn)
  — fleet-wide registration alert that fires when sustained auth
  failure burns budget.
- [`failure-mode-index.md`](failure-mode-index.md) — master inventory.
- [`../developer-guide/logging.md`](../developer-guide/logging.md#correlation-ids)
  — `request_id` correlation pattern.

## Escalation

If Mitigation steps 1-3 don't restore ArgoCD writes AND
[`SharkoClusterRegistrationFastBurn`](budget-burn-runbook.md#sharkoclusterregistrationfastburn)
is firing, email the maintainer: `moran.weissman@gmail.com`. Include:

- This runbook URL
- The output of Diagnosis step 3 (ArgoCD-side error class)
- The output of Diagnosis step 4 (JWT claims, REDACT the signature)
- Whether the token Secret has been recently rotated by an external
  controller
- The Sharko version + the ArgoCD version

**Never paste the actual token into the escalation.** REDACT
everything after the first 30 characters; the maintainer doesn't
need the full token to triage.

The maintainer is a single human, not a 24×7 rotation.
