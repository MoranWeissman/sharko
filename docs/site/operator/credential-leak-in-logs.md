# Credential Leak in Logs

**Severity:** P0

> **Verified:** Authored 2026-06-01 against `main` HEAD. The
> `RedactHandler` wrapper in `internal/logging/redact.go` is the V2-2.4
> shipped surface; it is **first in the slog handler chain** wired in
> `cmd/sharko/serve.go`, so credential-shaped attribute values are
> redacted before serialization (verified per the architecture diagram
> in [`../developer-guide/logging.md`](../developer-guide/logging.md)).
> The bootstrap-admin-password emission site at
> `internal/auth/store.go:634` is the canonical "headline finding" from
> [`../developer-guide/logging-audit-punchlist.md`](../developer-guide/logging-audit-punchlist.md).
> Re-verify when handler-chain order changes or new auth init call sites
> appear in `internal/auth/`.

A credential — admin password, kubeconfig bearer token, JWT, base64-
encoded vault secret, GitHub PAT — appeared verbatim in Sharko's
structured logs. The `RedactHandler` defense-in-depth wrapper is shipped
and active, but a specific log emission site bypassed its heuristics
(either because the attribute name didn't match the credential-name
patterns the wrapper looks for, or because the value didn't match any
of the three credential-shape detectors).

This runbook covers **two related but distinct failure modes** that
share the same diagnosis + mitigation:

1. **Bootstrap admin password leak** — the admin password emitted to
   logs as plain-text during the bootstrap-init code path at
   `internal/auth/store.go:634`. The `RedactHandler` (V2-2.4) now
   collapses the value to `[REDACTED]`, but the call site is still
   wrong (per the
   [`logging-audit-punchlist.md`](../developer-guide/logging-audit-punchlist.md)
   headline finding); a regression in the wrapper would re-expose admin
   credentials.
2. **Kubeconfig / bearer-token / generic credential leak** — any
   credential-shaped value that bypasses the `RedactHandler`'s three
   detectors (attribute name patterns, JWT shape, base64 shape).

Both are P0 because credentials in logs is **immediate compromise of
every system the credential authorizes against**. Logs are shipped to
log aggregators, S3 buckets, third-party SaaS — anywhere with a copy
must be treated as having the credential.

---

## Symptoms

What an operator sees when this fires:

- **JWT-shaped value (starts with `eyJ`) appears in a log line as a
  value** (not as `[REDACTED]`):

  ```sh
  kubectl -n <sharko-ns> logs -l app=sharko --tail=10000 \
    | grep -E '"[a-zA-Z_]+":\s*"eyJ[A-Za-z0-9._-]{20,}"' \
    | head -5
  ```

  Example bad line:
  ```
  {"time":"...","level":"INFO","msg":"argocd call","token":"eyJhbGciOiJSUzI1NiI..."}
  ```

- **Kubeconfig YAML appears in a log line:**

  ```sh
  kubectl -n <sharko-ns> logs -l app=sharko --tail=10000 \
    | grep -E "apiVersion: v1|kind: Config|current-context:" \
    | head -5
  ```

  Example bad line (the entire kubeconfig is rendered inside a single
  log message attribute):
  ```
  {"time":"...","level":"DEBUG","msg":"cluster credentials","kubeconfig":"apiVersion: v1\nkind: Config\nclusters: ..."}
  ```

- **Base64 blob >100 chars in a log line:**

  ```sh
  kubectl -n <sharko-ns> logs -l app=sharko --tail=10000 \
    | grep -oE '"[A-Za-z0-9+/=]{100,}"' \
    | head -5
  ```

- **Admin password literal in log line** (the bootstrap-admin-password
  failure mode):

  ```sh
  kubectl -n <sharko-ns> logs -l app=sharko --tail=10000 \
    | grep -E "admin.*password|initialPassword" \
    | grep -vE "REDACTED|\\[redacted\\]|<<REDACTED>>" \
    | head -5
  ```

  Example bad line:
  ```
  {"time":"...","level":"INFO","msg":"admin bootstrap","username":"admin","password":"Tr0ub4dor&3"}
  ```

- **GitHub PAT shape (`ghp_*`, `github_pat_*`, or `ghs_*`) in a log
  line:**

  ```sh
  kubectl -n <sharko-ns> logs -l app=sharko --tail=10000 \
    | grep -oE '(ghp_|github_pat_|ghs_)[A-Za-z0-9_]{20,}' \
    | head -5
  ```

- **Alert** — currently no Prometheus alert today; detection is via
  log-shipper pattern-match rules (e.g. Loki query, Splunk alert) or
  the manual greps above. Adding a Prometheus alert that scrapes a
  log-shipper-side counter is in Prevention.

The failure mode is **silent** — Sharko continues to operate normally;
only an explicit search of the log stream surfaces the leak.

---

## Diagnosis

The goal of diagnosis is to (a) confirm the leak is real, (b) identify
the specific credential leaked, (c) scope which downstream copies of
the log exist, and (d) identify the emission call site so it can be
fixed.

### 1. Confirm the leak in live logs

Run the five Symptoms greps above against the most recent log window.
If any returns hits, the leak is confirmed.

For each hit, capture:

- The `request_id` (correlates to the calling operation)
- The `time` (bounds the window)
- The attribute key (e.g. `"token"`, `"kubeconfig"`, `"password"`) —
  this is the leak's call-site fingerprint
- The credential type (JWT? PAT? kubeconfig? bcrypt hash?
  base64 blob?)

### 2. Identify the call site

The attribute key from step 1 maps to the call site in code. Search:

```sh
# Find where the attribute is emitted (relative to the worktree):
cd /path/to/sharko-source
grep -rn '"<attribute-key>"' internal/ cmd/
```

Common call sites by attribute key:

| Attribute key | Likely package | Notes |
|---|---|---|
| `"token"` | `internal/auth/` or `internal/argocd/` | Bearer token leaked |
| `"kubeconfig"` | `internal/providers/`, `internal/remoteclient/` | Cluster credential leaked |
| `"password"` | `internal/auth/store.go:634` | Bootstrap admin password (canonical case) |
| `"secret"` | `internal/orchestrator/secrets.go` | Addon secret value leaked |
| `"data"` | various (Helm chart values, secret payloads) | Generic credential |

Verify the call site against
[`../developer-guide/logging-audit-punchlist.md`](../developer-guide/logging-audit-punchlist.md)
to see whether the leak is the known headline finding (bootstrap admin
password) or a new instance.

### 3. Verify the `RedactHandler` is wired

The handler must be FIRST in the chain. Check
`cmd/sharko/serve.go`:

```sh
grep -A 5 "slog.New\|NewRedactHandler\|JSONHandler" cmd/sharko/serve.go
```

Expected: `RedactHandler` wraps the `JSONHandler` and the wrapped
handler is passed to `slog.New(...)` then `slog.SetDefault(...)`. Per
the architecture in
[`../developer-guide/logging.md`](../developer-guide/logging.md):

```
slog.Info(...) → RedactHandler → JSONHandler → stdout
```

If the order is reversed (`JSONHandler` first), redaction never
runs. **This is the most common wiring regression.**

If the wrapper is wired correctly but the leak still happens, the
specific call site bypasses the heuristics — see step 4.

### 4. Determine why `RedactHandler` missed the leak

The handler has three detectors:

- **Attribute-name pattern match** — keys like `password`, `token`,
  `secret`, `pat`, `kubeconfig`, `auth` are redacted by name.
- **JWT shape detector** — values starting with `eyJ` and containing
  two `.` separators are redacted.
- **Base64 blob detector** — values that look like base64 and exceed
  a length threshold are redacted.

If the leak passed all three:

- The attribute key didn't match any name pattern (e.g. attribute
  named `"connection_string"`, `"creds"`, `"k8s_config"`).
- The value isn't a JWT (e.g. a plain GitHub PAT `ghp_xxx`, a bcrypt
  hash, a raw password string).
- The value isn't long enough to trip the base64 detector (e.g. a
  short token).

The combination of "non-obvious attribute name" + "non-JWT credential
shape" is the hole; fixing the call site (don't log the credential at
all) is the only durable fix.

### 5. Scope downstream copies of the log

Logs ship to multiple places. Each is now compromised. Identify:

```sh
# Pod logs — kubectl-readable, retained per pod lifecycle.
kubectl -n <sharko-ns> get pod -l app=sharko -o jsonpath='{.items[0].metadata.creationTimestamp}'

# Container log driver — Fluent Bit, Fluentd, Vector, etc.
kubectl -n <log-ns> get pods -l app.kubernetes.io/component=log-shipper

# Log aggregator — Loki, Elasticsearch, Splunk, Datadog, etc.
# (administrative knowledge — find the URL/dashboard.)
```

For each destination, the credential exists in storage until the
retention window expires. **The mitigation must address each
destination independently.**

---

## Mitigation (try in order)

The order is incident-response shaped: stop new leak first, rotate
exposed credential second, purge logs third.

1. **Rotate the leaked credential immediately.** Whatever credential
   was exposed, treat as compromised. Depending on what leaked:

   - **Admin password** — rotate via the bootstrap procedure. Generate
     a new admin Secret, restart Sharko:
     ```sh
     # Get current admin secret:
     kubectl -n <sharko-ns> get secret <sharko-release> \
       -o jsonpath='{.data.admin\.initialPassword}' | base64 -d
     # Generate a new password and patch the secret:
     NEW_PW=$(openssl rand -base64 32)
     kubectl -n <sharko-ns> patch secret <sharko-release> \
       --type='json' \
       -p="[{\"op\":\"replace\",\"path\":\"/data/admin.initialPassword\",\"value\":\"$(echo -n "$NEW_PW" | base64)\"}]"
     # Restart so the bootstrap path re-runs:
     kubectl -n <sharko-ns> rollout restart deployment/sharko
     ```
     Then change the admin user's password via the UI (the rotated
     "initial" secret is the bootstrap fallback only).

   - **ArgoCD account token** — rotate via `argocd account
     generate-token --account sharko`; update the Sharko secret.

   - **GitHub PAT** — rotate via the provider's PAT UI. Update the
     Sharko secret. See
     [`git-provider-unreachable.md`](git-provider-unreachable.md)
     Mitigation step 2 for the procedure.

   - **Kubeconfig / remote cluster bearer token** — the leaked
     credential is for a downstream cluster, not for Sharko itself.
     Rotate by:
     - For EKS clusters: rotate the underlying IAM role's STS
       trust policy (forces token regeneration on next mint).
     - For Vault-managed kubeconfigs: rotate the Vault-issued
       certificate / token.
     - For static-token clusters: rotate the service account that
       owns the token; update the kubeconfig in the secrets-store.

   - **Addon vault secret** — rotate the value in the secrets-store.
     Re-trigger `POST /api/v1/clusters/{name}/secrets/refresh` for
     every cluster that uses the addon.

2. **Purge the credential from all log destinations.** Each retention
   window contains the credential:

   - **Pod logs** — pod restart clears the in-memory ring; logs
     remain on disk until the next pod-rotate. Force restart with
     log truncation:
     ```sh
     kubectl -n <sharko-ns> rollout restart deployment/sharko
     ```
   - **Log aggregator (Loki / Elasticsearch / Splunk / Datadog)** —
     issue a delete query for the leaked credential. Each aggregator's
     procedure is different; consult the aggregator team. Document
     the deletion in the incident ticket.
   - **S3 / object-store archives** — if logs are archived to S3
     (common with Fluent Bit), delete the affected object(s):
     ```sh
     aws s3 rm s3://<log-bucket>/sharko/<date>/ --recursive \
       --include "<window>*"
     ```
   - **Third-party SaaS** — if logs ship to Datadog, New Relic, etc.,
     each has its own log-deletion API; some require account-level
     ticket. **Document the request.**

   Acknowledge that purging is best-effort. The credential **must be
   treated as if it remains in some copy of the logs**; that's why
   Mitigation step 1 (rotation) is the durable fix.

3. **Fix the emission call site.** This is the code change that
   prevents recurrence. Two patterns:

   - **Don't log the value at all** — replace `slog.Info("...", "token",
     tok)` with `slog.Info("...")`. Log structural info (length, hash
     prefix) if useful, never the value itself.
   - **Add the attribute name to the `RedactHandler` pattern list** —
     extend `internal/logging/redact.go` to recognize the new
     attribute name. This is the lighter-touch fix but still relies
     on the wrapper being correctly wired.

   File a PR with the fix. Until the PR ships, the call site keeps
   leaking on every code path that hits it.

4. **Verify the `RedactHandler` is wired correctly going forward.**
   Re-run Diagnosis step 3. Add an integration test to CI that fails
   the build if the handler wiring regresses (e.g. log a known
   credential-shape value and assert it's `[REDACTED]` in stdout).

5. **Last resort — disable log collection during the leak window.**
   If purge across destinations is infeasible (e.g. multi-region S3
   buckets with long retention), the only way to bound the leak is
   to disable log forwarding for the affected window:
   ```sh
   kubectl -n <log-ns> scale deployment/log-shipper --replicas=0
   ```
   This is destructive (you lose all logs during the window, not just
   the credential lines) but bounds the leak. Document the rationale.

---

## Root-cause patterns

### Direct emission of credential as slog attribute

A developer wrote `slog.Info("msg", "token", tokenValue)`. The attribute
key `"token"` matches the `RedactHandler` pattern list and is
redacted to `[REDACTED]` — IF the wrapper is wired correctly. If the
wrapper is mis-wired or the attribute name doesn't match the pattern
list, the value lands in stdout.

Diagnostic signature: log line shows the credential value verbatim with
an obviously-credential-related attribute key.

Why it happens: the developer used a non-standard attribute name
(`"creds"`, `"k8s_config"`, `"connection"`) that wasn't on the redact
list. Or the developer wired `JSONHandler` before `RedactHandler` and
the redaction never fires.

Fix: don't log credentials at all. Replace with a hash or length, or
remove the attribute entirely. The wrapper is defense-in-depth, not
the primary defense.

### Credential nested in a struct that's logged

A code path does `slog.Info("response", "result", response)` where
`response` is a struct containing a credential field. JSON marshaling
flattens the struct; the credential lands in stdout as a nested field
with the struct field's name as the key.

Diagnostic signature: log line shows `"result":{"...":"<credential>"}`
with the credential in a sub-field.

Why it happens: developers tend to log "the whole response" for
debugging convenience. Structs with `json:"-"` tags on credential
fields would prevent this; absence of the tag is the bug.

Fix: add `json:"-"` to credential fields in struct definitions.
Audit every struct in `internal/auth/`, `internal/providers/`,
`internal/argocd/` for fields that should be `json:"-"`.

### Error message contains credential

A common pattern: `fmt.Errorf("failed: %v", req)` where `req` includes
the credential in its body. The error is logged at the call site, and
the entire `%v` representation lands in stdout — including the
credential.

Diagnostic signature: log line shows `"error":"failed: ...
<credential>..."` — credential is inside the error string, not as a
separate attribute.

Why it happens: Go error formatting is naive; developers write
`%v` and don't realize the formatted value contains secrets.

Fix: wrap errors with explicit message control. Replace
`fmt.Errorf("failed: %v", req)` with `fmt.Errorf("failed: %s", req.SafeString())`
where `SafeString()` redacts credential fields.

### RedactHandler wired AFTER the JSON handler

The slog handler chain runs in registration order. If `RedactHandler`
is registered AFTER `JSONHandler`, the JSON serializer runs first
and emits the credential to stdout BEFORE redaction has a chance to
act. The wrapper appears to be present but is silently no-op.

Diagnostic signature: every log line that should have been redacted
shows the credential verbatim, EVEN for attribute keys that are on
the redaction pattern list.

Why it happens: a recent refactor of `cmd/sharko/serve.go`
re-ordered the handler chain. Code review didn't catch the order
mistake because both handlers are present.

Fix: re-order the chain so `RedactHandler` wraps `JSONHandler` (NOT
the other way round). Add an integration test that asserts the
chain order is correct.

---

## Rollback plan

The mitigations are incident-response shaped — most are not reversible
(rotating a credential cannot be "un-rotated"; deleting log archives
is permanent). The only reversible step is:

- **If the log-shipper scale-to-zero (Mitigation step 5) breaks
  observability for an unrelated incident**, re-enable:
  ```sh
  kubectl -n <log-ns> scale deployment/log-shipper --replicas=<original>
  ```

For the rotation steps (Mitigation step 1), the rollback path is
"restore from backup of the secrets-store" — but only if the
credential is verified to not be in compromised log copies. Don't roll
back rotations on a credential whose log presence isn't fully purged.

---

## Prevention

- **CI — credential-shape grep in logs.** Add a CI job that runs
  Sharko in a test harness, exercises the auth + provider + secrets
  surfaces, and greps the captured stdout for credential-shapes
  (JWTs, kubeconfigs, base64 blobs >100 chars, PAT prefixes). If
  any are found, the build fails. This catches new leak sites
  before they ship.

- **CI — `RedactHandler` order integration test.** Add a test that
  initializes Sharko's logging stack, emits a known credential-shape
  attribute value, and asserts the stdout output is `[REDACTED]`.
  Catches the wiring-regression cause.

- **Code review — credential field `json:"-"` audit.** Standing
  reviewer instruction: every struct definition with a credential
  field must have `json:"-"` on that field. Add as a guideline in
  `.claude/team/code-reviewer.md`.

- **Tooling — pre-commit hook for `slog.Info("..., "token", ...)`
  patterns.** A simple regex pre-commit hook that flags
  `slog\.(Info|Warn|Error|Debug)\(.*"(token|password|secret|kubeconfig|pat)"`
  catches direct emission at write time.

- **Scheduled work — quarterly log audit.** Once per quarter, run the
  Symptoms greps against the production log aggregator's 30-day
  retention window. Document findings. Catches any leak that escaped
  CI.

- **Monitoring — log-shipper-side credential detection.** Configure
  the log shipper (Fluent Bit, Vector) with a credential-shape
  filter that:
  - Drops the line if it matches a JWT / PAT pattern AND the line
    is not already redacted, AND
  - Increments a counter `log_credential_leak_dropped_total` that
    Sharko's monitoring can alert on.
  This is the cheapest pre-aggregator defense and the one most
  likely to catch new leak sites in production.

---

## Related runbooks

- [`auth-bypass.md`](auth-bypass.md) — adjacent class of security
  failure (auth bypass rather than credential leak). Both are P0
  security incidents.
- [`failure-mode-index.md`](failure-mode-index.md) — master inventory.
- [`../developer-guide/logging.md`](../developer-guide/logging.md) —
  V2-2.4 RedactHandler architecture and the slog handler chain order.
- [`../developer-guide/logging-audit-punchlist.md`](../developer-guide/logging-audit-punchlist.md) —
  the engineering punchlist that flagged the bootstrap admin password
  emission as the headline finding.

## Escalation

This runbook's escalation is **immediate, not 30-minute-window**.

The moment Diagnosis step 1 confirms a leak, email the maintainer:
`moran.weissman@gmail.com` with subject `[SHARKO][P0][SECURITY]
credential leak in logs`. Include:

- This runbook URL
- The credential type (JWT, kubeconfig, PAT, admin password, vault
  secret)
- The attribute key from the offending log line (e.g. `"token"`)
- The grep command and a redacted version of one matched log line
  (replace the credential value with `<REDACTED-FOR-REPORT>`)
- The window of leak (first occurrence timestamp → most recent
  occurrence timestamp)
- The Sharko version
- The log destinations that received the leaked lines (pod logs,
  Loki, S3, etc.)
- The rotation status of the leaked credential

The maintainer is the security contact until a separate security@
channel is published. For confirmed credential leak, the maintainer's
expected SLA is **same-day investigation and a hotfix release for the
emission site** — not a multi-day backlog item.

Notify any downstream operators whose clusters or secrets stores were
referenced by the leaked credential so they can rotate independently
on their side.

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
- [x] (if applicable) No alert defined yet (per Symptoms)
-->
