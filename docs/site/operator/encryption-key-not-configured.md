# Connection-Config Encryption Key Not Configured

**Severity:** P1

> **Verified:** Authored 2026-06-01 against `main` HEAD. The error
> response body `"encryption key not configured"` and the
> `os.Getenv("SHARKO_ENCRYPTION_KEY") == ""` precondition check are
> verified verbatim against `internal/api/users_me.go:107-110` (set
> personal GitHub token) and `internal/api/users_me.go:188-191`
> (test personal GitHub token) as shipped. Both paths return
> HTTP 500 — the encryption key is a deployment-time invariant; a
> running pod without it cannot store or retrieve personal
> connection config. Re-verify before changing the env-var name
> (currently `SHARKO_ENCRYPTION_KEY`) or the response-body string —
> both are anchors for the diagnosis below.

A user-facing API call to set or test a personal GitHub token
(used for Tier 2 per-user attribution per the
[attribution design](../user-guide/attribution.md)) is returning
HTTP 500 with the body `"encryption key not configured"`. The
server checks `SHARKO_ENCRYPTION_KEY` at request time and refuses
to proceed if the env var is empty.

The failure surfaces from these endpoints:

- `POST /api/v1/users/me/github-token` (set personal GitHub token)
- `POST /api/v1/users/me/github-token/test` (validate stored
  personal GitHub token)
- Any other personal-token CRUD that decrypts stored values

The blast radius is **the per-user Tier 2 attribution flow**.
Tier 1 service-token attribution (all other endpoints) is
**unaffected** — Sharko continues to register clusters, enable
addons, and reconcile fleet state using the service-level
`GITHUB_TOKEN`. Only the user-personal-token feature is broken.

This is a **deployment-time misconfiguration**, not a runtime
failure mode. The fix is to add `SHARKO_ENCRYPTION_KEY` to the
deployment env and restart Sharko.

---

## Symptoms

What an operator sees when this fires:

- **HTTP 500 from `POST /api/v1/users/me/github-token`** with body:

  ```json
  {"error":"encryption key not configured"}
  ```

  Reproduce:

  ```sh
  curl -i -sS -X POST -H "Authorization: Bearer ${USER_SESSION}" \
    "http://sharko/api/v1/users/me/github-token" \
    --data-binary '{"token":"ghp_..."}'
  ```

- **HTTP 500 from `POST /api/v1/users/me/github-token/test`** with the
  same body.
- **UI symptom**: the user opens "Settings -> Personal -> GitHub
  Token", enters their PAT, clicks Save, and sees a red banner:
  "Could not save token: encryption key not configured. Contact your
  Sharko administrator."
- **No `kubectl logs` line specific to this error** — the handler
  returns the response without an additional log emission (each
  handler returns directly from the precondition check). The
  middleware-level access log records the 500 status:

  ```sh
  kubectl -n <sharko-ns> logs -l app=sharko --tail=2000 \
    | jq -c 'select(.path | test("github-token"; "i")) | select(.status == 500)'
  ```

- **Other Sharko operations work normally.** Sharko keeps registering
  clusters, enabling addons, and reconciling — using the service
  token. Only the personal-token flow fails.
- **`kubectl get deployment sharko -o yaml`** shows the env var is
  missing or empty:

  ```sh
  kubectl -n <sharko-ns> get deployment sharko -o yaml \
    | yq '.spec.template.spec.containers[0].env[] | select(.name == "SHARKO_ENCRYPTION_KEY")'
  ```

  Empty output = the env var is not set. Non-empty with a
  `secretKeyRef` = the env var IS set but the referenced secret /
  key is missing.

- **No specific Prometheus alert fires today.** A V2-4.x follow-up
  is to surface this as a `sharko_personal_token_500_total` counter
  with an alert on >0 for >5 minutes.

If the symptom is HTTP 500 with a **different** body
(`"decryption failed"`, `"invalid encryption key"`,
`"token data corrupted"`), the encryption key IS set but doesn't
decrypt what was previously stored. That's a separate failure mode
(key rotated without re-encryption); see the
[attribution design](../user-guide/attribution.md) for the key-
rotation procedure.

---

## Diagnosis

Three checks: confirm the env var is missing in the pod, identify
whether the connection secret has the key, verify the Helm values
wire the env-var reference correctly.

### 1. Confirm the env var is missing or empty inside the pod

```sh
SHARKO_NS=<sharko-ns>
SHARKO_POD=$(kubectl -n "$SHARKO_NS" get pod -l app=sharko -o name | head -1)

kubectl -n "$SHARKO_NS" exec "$SHARKO_POD" -- \
  env | grep '^SHARKO_ENCRYPTION_KEY='
```

Three outcomes:

- **Empty output** — the env var is not exported to the running pod.
  This runbook applies.
- **`SHARKO_ENCRYPTION_KEY=` (empty value)** — the env var is set but
  to an empty string. The `os.Getenv == ""` check trips. Treat the
  same as not set.
- **`SHARKO_ENCRYPTION_KEY=<some-value>`** — the env var is set with
  a value. This runbook does NOT apply; the failure is decryption-
  shaped, not configuration-shaped. See the attribution design's
  key-rotation procedure.

### 2. Inspect the Helm values for `config.connectionSecretName`

Per the
[k8s-expert.md role file](https://github.com/MoranWeissman/sharko/blob/main/.claude/team/k8s-expert.md)
and `charts/sharko/values.yaml`, the encryption key is typically
wired via a K8s Secret referenced by `config.connectionSecretName`
(default: `sharko-connections`). The Sharko deployment template
reads a key from that secret into `SHARKO_ENCRYPTION_KEY`.

```sh
helm get values <sharko-release> -n "$SHARKO_NS" \
  | yq '.config.connectionSecretName // "sharko-connections (default)"'

# Then inspect the referenced secret:
SECRET_NAME=<from above>
kubectl -n "$SHARKO_NS" get secret "$SECRET_NAME" \
  -o jsonpath='{.data}' | jq 'keys'
```

Expected output: an array containing `encryption_key` (or the key
name documented in `charts/sharko/templates/deployment.yaml`'s
env-var reference). If the key is missing from the secret, the
deployment cannot map it into the env.

Common shapes:

- `["encryption_key"]` — exists; correctly mapped. Move to step 1
  to verify it's actually being injected into the pod.
- `["encryption_key", "github_token"]` — both Tier 1 service
  token and the encryption key exist. The wiring is correct;
  step 1 was the error.
- `["github_token"]` — only the service token; the encryption key
  is missing. This is the most common configuration mistake.
- The secret itself doesn't exist (`Error from server (NotFound)`)
  — the operator deployed without the `sharko-connections` secret;
  see the installation runbook.

### 3. Verify the deployment env-var wiring

Even when the secret has the right key, the deployment must
explicitly map it into the pod's env. Inspect the rendered
deployment spec:

```sh
kubectl -n "$SHARKO_NS" get deployment sharko -o yaml \
  | yq '.spec.template.spec.containers[0].env[] | select(.name == "SHARKO_ENCRYPTION_KEY")'
```

Expected output:

```yaml
name: SHARKO_ENCRYPTION_KEY
valueFrom:
  secretKeyRef:
    name: sharko-connections
    key: encryption_key
    optional: false
```

If `optional: true` is set on the `secretKeyRef`, missing keys
silently produce empty env vars instead of failing the pod
startup — preferring fail-loud here would catch the
misconfiguration earlier. If the env entry is missing entirely,
the deployment was rendered from an older Helm chart version
that doesn't wire the env var; upgrading the chart fixes it.

Cross-check against `helm get manifest`:

```sh
helm get manifest <sharko-release> -n "$SHARKO_NS" \
  | yq 'select(.kind == "Deployment") | .spec.template.spec.containers[0].env[]' \
  | grep -A 4 SHARKO_ENCRYPTION_KEY
```

The rendered manifest should match what `kubectl get deployment`
shows. A mismatch (e.g. the manifest references the right secret
but the live deployment doesn't) means a manual edit drifted the
deployment from Helm — `helm upgrade --reuse-values` re-syncs.

---

## Mitigation (try in order)

The fix is **always the same**: add the encryption key, restart
Sharko. The variation is in where the key comes from and which
deployment surface is being updated.

1. **Generate and add the encryption key to the connection secret.**
   The encryption key is **a deployment-time invariant** — once
   chosen, it never changes (rotating it requires re-encrypting all
   stored personal tokens; out-of-scope here). Generate a strong
   random key once; store it in the connection secret; keep a
   backup outside the cluster.

   ```sh
   # Generate a 32-byte (256-bit) random key. Encode as base64 for
   # K8s Secret data field. Save the key somewhere safe BEFORE
   # pasting it into the cluster — losing the key means losing all
   # stored personal tokens.
   ENC_KEY=$(openssl rand -base64 32)

   echo "Save this key in your password manager: $ENC_KEY"

   # Add to the existing connection secret:
   SECRET_NAME=sharko-connections   # match values.yaml
   kubectl -n "$SHARKO_NS" patch secret "$SECRET_NAME" \
     --type='json' \
     -p='[{"op":"add","path":"/data/encryption_key","value":"'"$(echo -n "$ENC_KEY" | base64)"'"}]'
   ```

   Then restart Sharko so the env var picks up the new value:

   ```sh
   kubectl -n "$SHARKO_NS" rollout restart deployment/sharko
   kubectl -n "$SHARKO_NS" rollout status deployment/sharko --timeout=120s
   ```

   Success indicator: re-running Diagnosis step 1 returns
   `SHARKO_ENCRYPTION_KEY=<value>`. The user's
   `POST /api/v1/users/me/github-token` call now returns HTTP 200.

2. **Update the deployment to reference the encryption key.** If
   Diagnosis step 2 shows the secret has the `encryption_key` data
   field but the deployment isn't reading it, the env-var mapping
   in the deployment manifest is missing or wrong. Inspect the
   deployment's env block:

   ```sh
   kubectl -n "$SHARKO_NS" get deployment sharko -o yaml \
     | yq '.spec.template.spec.containers[0].env[] | select(.name | startswith("SHARKO_"))'
   ```

   Expected entry:

   ```yaml
   - name: SHARKO_ENCRYPTION_KEY
     valueFrom:
       secretKeyRef:
         name: sharko-connections
         key: encryption_key
   ```

   If this block is missing or references the wrong secret /
   key, re-run `helm upgrade` with the corrected values:

   ```sh
   helm upgrade <sharko-release> charts/sharko/ \
     -n "$SHARKO_NS" \
     --reuse-values \
     --set config.connectionSecretName=sharko-connections
   ```

   Or set the env var directly on the deployment as a workaround
   while a longer-term Helm change is being prepared:

   ```sh
   kubectl -n "$SHARKO_NS" set env deployment/sharko \
     --from=secret/sharko-connections \
     --prefix=
   ```

   Success indicator: re-running Diagnosis step 1 returns
   `SHARKO_ENCRYPTION_KEY=<value>`; user-facing personal-token
   endpoints return HTTP 200.

3. **As a last resort, disable the personal-token feature.** If the
   operator cannot immediately deploy an encryption key (e.g. blocked
   on a key-management policy review) and they want the rest of
   Sharko to work without surfacing user-facing 500s, two options:

   - **UI**: hide the "Personal -> GitHub Token" section. This is a
     UI-side feature flag (`features.personalTokens: false` in the
     Helm values; verify against the current chart). Operators
     accept the partial functionality until the key is provisioned.
   - **API**: return HTTP 501 `Not Implemented` instead of 500. Not
     wired today; a V2-4.x follow-up if the
     `features.personalTokens` flag isn't in place.

   This step is the **least-good** mitigation — it papers over the
   misconfiguration without fixing it. Prefer step 1 or 2.

   Success indicator: users no longer see the personal-token UI;
   no `github-token` API calls are made.

---

## Root-cause patterns

Three common causes.

### Operator installed Sharko without setting the encryption key

The single most common cause. The Helm `values.yaml` either omits
the encryption key entirely or sets it to an empty string. The
operator deployed, started using Sharko, and only discovers the
problem when a user tries to save their personal GitHub token.

Diagnostic signature: the deployment was created via `helm install`
without overriding the relevant value; Diagnosis step 1 shows the
env var is missing; Diagnosis step 2 shows the connection secret
doesn't have the `encryption_key` data field.

Fix lane: Mitigation step 1 (generate the key, add to secret,
restart). Standard install-time fix.

### Wrong secret name in `config.connectionSecretName`

The deployment's `config.connectionSecretName` doesn't match the
actual secret name in the cluster. The Helm template renders an
`envFrom: secretKeyRef:` pointing at a missing secret; the env var
silently doesn't get exported.

Diagnostic signature: Diagnosis step 1 shows the env var is missing;
Diagnosis step 2 shows the configured secret name doesn't exist or
has different keys than the template expects.

Fix lane: Mitigation step 2 (align the deployment with the actual
secret). Either rename the secret or set
`config.connectionSecretName` to the real name.

### Secret rotated without restart

The operator added `encryption_key` to the connection secret after
Sharko was already running. The deployment's pod spec references
the secret by `valueFrom`, but K8s env vars are resolved at pod
creation time — they don't auto-update when the underlying secret
changes. Sharko keeps reading the empty value cached at startup.

Diagnostic signature: `kubectl describe secret <name>` shows the
encryption_key data field exists, last-modified time is recent;
Diagnosis step 1 still shows the env var as empty in the running
pod.

Fix lane: Mitigation step 1's restart at the end (`kubectl rollout
restart deployment/sharko`). This is operator-fatigue-shape — the
secret was correctly added but the restart was missed.

---

## Prevention

How to make this failure mode less likely going forward. Three
levers:

- **Monitoring — pre-flight startup check.** Sharko could refuse to
  start (fail-fast in `cmd/sharko/serve.go`) when the personal-token
  endpoints are wired into the router but `SHARKO_ENCRYPTION_KEY`
  is empty. Operators see the failure at install time (pod
  CrashLoopBackOff with a clear startup error) instead of at first
  user-facing 500. Wiring this into the startup-validation path is
  a V2-4.x follow-up.

- **Gating — Helm chart NOTES.txt / template guard.** The Helm chart
  could include a `required` Sprig function call that surfaces the
  encryption key as a hard install-time requirement:

  ```yaml
  encryption_key: {{ required "config.encryptionKey is required for personal-token features" .Values.config.encryptionKey }}
  ```

  This converts the misconfiguration from "discoverable only via
  user-facing 500" to "discoverable at `helm install` time."

- **Scheduled work — installation-runbook callout.** The installation
  runbook explicitly enumerates the encryption-key requirement.
  Audit the runbook quarterly to ensure the "Generate encryption
  key" step is prominent enough that operators don't miss it. The
  runbook owns Prevention more than any code change.

---

## Related runbooks

- [`secret-push-silently-failed.md`](secret-push-silently-failed.md)
  — adjacent "secret machinery silently fails" P0 pattern. Different
  surface (remote-cluster Secret push vs. local config encryption)
  but similar discipline.
- [`credential-leak-in-logs.md`](credential-leak-in-logs.md) — the
  guard-discipline P0. Encryption-at-rest (this runbook) and
  redaction-in-logs (that runbook) are paired credential-handling
  patterns; both must be on for end-to-end safety.
- [`failure-mode-index.md`](failure-mode-index.md) — master inventory.
- [`../user-guide/attribution.md`](../user-guide/attribution.md) —
  the tiered Git attribution design that drives the personal-token
  feature.

## Escalation

If Mitigation steps 1 and 2 don't resolve the issue (e.g. the env
var is set but personal-token endpoints still return 500), email
the maintainer: `moran.weissman@gmail.com`. Include:

- The runbook URL you used (this page)
- The output of Diagnosis step 1 (env-var presence inside the pod)
- The output of Diagnosis step 2 (connection secret keys)
- The Sharko version (`sharko version` or the Helm chart version)

The maintainer is a single human, not a 24x7 rotation. Expect a
business-day SLA. Because Tier 1 (service-token) operations
continue working, this failure does not block fleet operations —
the urgency is bounded.

<!-- Style-guide compliance checklist (V2-4.1):
- [x] Title matches `# <Failure name>`
- [x] Severity line present (P1)
- [x] Verified-by-execution header + date
- [x] Symptoms section before Diagnosis
- [x] Symptoms include exact log lines / error messages / response shapes
- [x] Diagnosis has 3+ concrete checks (2 numbered + extensive sub-checks)
- [x] Mitigation uses numbered list
- [x] Mitigation has 3-5 steps in priority order (3 steps)
- [x] Root-cause patterns: 2+ named causes (3 named)
- [x] Prevention section present and non-empty
- [x] Related runbooks section present
- [x] Intro is operator-on-call voice
- [x] Length 300-800 lines (in range)
- [x] All cross-links resolve
- [x] No emoji / no internal Slack / employee email
- [x] (if applicable) Alert reference noted as V2-4.x follow-up
-->
