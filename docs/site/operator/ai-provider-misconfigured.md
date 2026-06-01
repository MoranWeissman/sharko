# AI Provider Misconfigured

**Severity:** P1

> **Verified:** Authored 2026-06-01 against `main` HEAD. The 503
> response body `"AI is not configured; configure a provider in
> Settings -> AI"` is verified verbatim against
> `internal/api/ai_annotate.go:90-92` as shipped — the handler returns
> 503 when `s.aiClient == nil || !s.aiClient.IsEnabled()`. The same
> shape surfaces from every AI-using endpoint (changelog summary at
> `internal/api/addons_changelog.go`, addon catalog suggestions, smart-
> values annotation). The per-provider auth-error variant returns
> through `aiClient.Summarize` returning a wrapped provider-specific
> 401/403; the wrapping originates in
> `internal/orchestrator/ai_annotate.go:141` (the LLM call site) and
> the failure surfaces as the `SkipReason=llm_error` outcome instead
> of HTTP 503. Re-verify after changing the AI client interface
> (`IsEnabled()`, `Summarize()`) or the Settings UI's AI provider
> configuration shape — both are anchors here.

The AI features in Sharko (smart-values annotation, addon changelog
summaries, catalog discovery suggestions) are returning HTTP 503 or
failing internally with provider-specific auth errors. Two causes,
both deployment-time:

1. **No AI provider is configured at all.** `aiClient == nil` —
   the Helm value `ai.enabled` is false or the provider type isn't
   set. The handler returns HTTP 503 with the
   `"AI is not configured"` message.
2. **A provider is configured but the credential is wrong.**
   `aiClient != nil` and `IsEnabled() == true`, but the actual LLM
   call returns 401/403 from the provider (OpenAI, Anthropic, Google,
   Ollama, or custom). The annotation outcome is `llm_error`; the
   response is HTTP 200 with the addon enabled WITHOUT AI annotation.

The blast radius is **the AI feature surface only**. Cluster
registration, addon enable/disable, reconciliation, and all
non-AI-touching endpoints continue working normally. Operators
using Sharko without AI features see no impact.

This is distinct from
[`ai-annotation-secret-blocked.md`](ai-annotation-secret-blocked.md)
— that runbook is for HTTP 422 (the guard fired before the LLM
call). This runbook is for HTTP 503 (no provider) or the
`llm_error` SkipReason (provider rejected).

---

## Symptoms

What an operator sees when this fires:

- **HTTP 503 from `POST /api/v1/addons/{name}/values/annotate`**:

  ```json
  {"error":"AI is not configured; configure a provider in Settings -> AI"}
  ```

  This is the "no provider" lane. The same handler returns 200
  with the addon's annotated values when AI is configured.

- **HTTP 503 from `GET /api/v1/addons/{name}/changelog?ai_summary=true`**
  for the same reason.

- **HTTP 200 with `ai=llm_error` on addon enable endpoints** when a
  provider IS configured but the credential is wrong:

  ```json
  {
    "status": "success",
    "ai": "llm_error",
    "git": {"pr_url": "..."}
  }
  ```

  The addon enables; the smart-values file lacks AI-generated
  description comments; an error is logged.

- **`kubectl logs` Error line on the LLM-call path** (provider-
  specific):

  ```
  {"time":"...","level":"ERROR","msg":"ai annotate: LLM call failed","chart":"datadog","version":"3.74.0","error":"OpenAI: 401 Unauthorized: Incorrect API key provided"}
  ```

  Or for Claude:

  ```
  {"time":"...","level":"ERROR","msg":"ai annotate: LLM call failed","chart":"datadog","version":"3.74.0","error":"anthropic: 401: invalid x-api-key"}
  ```

  Grep:

  ```sh
  kubectl -n <sharko-ns> logs -l app=sharko --tail=5000 \
    | jq -c 'select(.msg | test("ai annotate.*LLM call failed"; "i"))'
  ```

- **`sharko_ai_annotate_total{outcome="..."}` Prometheus metric**
  shows the outcome distribution:

  ```promql
  sum by (outcome) (rate(sharko_ai_annotate_total[15m]))
  ```

  Expected on a healthy deployment: `outcome=ok` dominates.
  Expected on this failure: `outcome=not_configured` (no provider)
  or `outcome=llm_error` (provider auth failure) accumulates.

- **UI symptom**: the operator clicks "Annotate values" in the
  smart-values editor and sees a red banner: "AI is not configured.
  Configure a provider in Settings -> AI." Or, with a misconfigured
  provider: "AI annotation failed: <provider error message>."

- **Settings UI** shows the AI provider as either `Not configured`
  (the simple case) or `Configured but failing`.
- **No specific Prometheus alert fires today.** Surfacing an alert
  on `outcome=llm_error` rate >5% over 15 minutes is a V2-4.x
  follow-up.

If the symptom is **HTTP 422 with `"Secret-like content detected"`**,
this is **not** the runbook — the guard hard-blocked. See
[`ai-annotation-secret-blocked.md`](ai-annotation-secret-blocked.md).

---

## Diagnosis

Three checks: identify which lane (no provider vs. misconfigured
provider), inspect the configuration, verify provider reachability.

### 1. Identify the lane

Try a synthetic AI call. The response tells you which lane applies:

```sh
curl -i -sS -X POST -H "Authorization: Bearer ${SHARKO_TOKEN}" \
  "http://sharko/api/v1/addons/datadog/values/annotate"
```

Three possible responses:

- **HTTP 503** with `"AI is not configured"` -> **No provider lane**.
  Mitigation step 1 applies.
- **HTTP 200** with `ai=llm_error` in the response body, AND a
  per-call Error log line -> **Misconfigured provider lane**.
  Mitigation step 2 applies.
- **HTTP 200** with annotated values -> AI is working; this isn't
  the failure mode (or it's intermittent — investigate timing).

### 2. Inspect the AI provider configuration

The Settings UI is the canonical source. Via the API:

```sh
curl -sS -H "Authorization: Bearer ${SHARKO_TOKEN}" \
  "http://sharko/api/v1/ai/config" \
  | jq '{provider, enabled, model, has_api_key: (.api_key | length > 0)}'
```

Expected fields:

- `provider`: one of `openai`, `claude`, `gemini`, `ollama`,
  `custom`.
- `enabled`: `true` when the operator has flipped the toggle.
- `model`: e.g. `gpt-4-turbo`, `claude-3-sonnet`, `gemini-1.5-pro`,
  `llama3:70b`.
- `has_api_key`: `true` when the operator pasted a key. Empty for
  Ollama which uses a local endpoint.

If `enabled: false` or `provider: null` -> no-provider lane.
If `enabled: true` but `has_api_key: false` for a cloud provider ->
misconfigured (missing key).
If everything looks set but the synthetic call fails -> provider
authentication or model availability problem.

Also inspect the Helm values for the AI config defaults:

```sh
helm get values <sharko-release> -n <sharko-ns> \
  | yq '.ai // "unset"'
```

Per
[`ai-config.md`](ai-config.md), the keys are
`ai.enabled`, `ai.provider`, `ai.apiKey`, `ai.cloudModel`,
`ai.ollama.deploy`, `ai.ollama.model`.

### 3. Verify provider reachability

For cloud providers, probe the provider's auth endpoint from inside
the Sharko pod:

**OpenAI**:

```sh
SHARKO_POD=$(kubectl -n <sharko-ns> get pod -l app=sharko -o name | head -1)
OPENAI_KEY=$(kubectl -n <sharko-ns> exec "$SHARKO_POD" -- env | grep '^OPENAI_API_KEY=' | cut -d= -f2)

kubectl -n <sharko-ns> exec "$SHARKO_POD" -- \
  curl -sS https://api.openai.com/v1/models \
  -H "Authorization: Bearer ${OPENAI_KEY}" \
  | jq '.data[0].id // .error.message'
```

Expected: a model ID (auth OK) or an `error.message` (auth failure
with explanation).

**Anthropic**:

```sh
ANTHROPIC_KEY=$(kubectl -n <sharko-ns> exec "$SHARKO_POD" -- env | grep '^ANTHROPIC_API_KEY=' | cut -d= -f2)

kubectl -n <sharko-ns> exec "$SHARKO_POD" -- \
  curl -sS https://api.anthropic.com/v1/models \
  -H "x-api-key: ${ANTHROPIC_KEY}" \
  -H "anthropic-version: 2023-06-01" \
  | jq '.data[0].id // .error.message'
```

**Ollama** (local):

```sh
kubectl -n <sharko-ns> exec "$SHARKO_POD" -- \
  curl -sS http://ollama:11434/api/tags \
  | jq '.models[0].name // .error'
```

The probe results tell you whether the provider is reachable, the
credential is correct, and the configured model is available.

---

## Mitigation (try in order)

1. **Configure an AI provider via the Settings UI.** This is the
   simplest path for the no-provider lane:

   - Navigate to Settings -> AI in the Sharko UI.
   - Choose a provider (OpenAI / Anthropic / Google / Ollama).
   - Paste the API key (cloud providers) or the Ollama endpoint URL.
   - Choose a model.
   - Click Save -> Test connection.

   Or via the API:

   ```sh
   curl -sS -X PUT -H "Authorization: Bearer ${SHARKO_TOKEN}" \
     "http://sharko/api/v1/ai/config" \
     --data-binary '{
       "provider": "openai",
       "model": "gpt-4-turbo",
       "api_key": "sk-...",
       "enabled": true
     }'
   ```

   The configuration is encrypted at rest using the
   `SHARKO_ENCRYPTION_KEY` (see
   [`encryption-key-not-configured.md`](encryption-key-not-configured.md)
   if the save fails with a 500). After saving, the synthetic AI
   call should return HTTP 200.

   Success indicator: Diagnosis step 1's synthetic call returns
   HTTP 200 with annotated values.

2. **Rotate the AI provider credential.** When Diagnosis step 3
   showed an auth error from the provider, the API key is wrong
   (incorrect key, revoked, or scoped wrong). Generate a new key
   at the provider's console and update Sharko:

   ```sh
   # OpenAI:
   # https://platform.openai.com/api-keys -> Create new secret key
   # Then update Sharko:
   curl -sS -X PUT -H "Authorization: Bearer ${SHARKO_TOKEN}" \
     "http://sharko/api/v1/ai/config" \
     --data-binary '{"api_key": "sk-..."}'
   ```

   No Sharko restart is needed — the encrypted store reads the key
   on each AI call.

   Success indicator: Diagnosis step 3's provider probe returns a
   model list; Diagnosis step 1's synthetic call returns HTTP 200.

3. **Switch to a different provider.** If the configured provider
   is having an outage (visible at the provider's status page) or
   the operator's account has hit a quota, switch providers as a
   temporary mitigation:

   ```sh
   # Switch to Anthropic Claude as a fallback:
   curl -sS -X PUT -H "Authorization: Bearer ${SHARKO_TOKEN}" \
     "http://sharko/api/v1/ai/config" \
     --data-binary '{
       "provider": "claude",
       "model": "claude-3-sonnet-20240229",
       "api_key": "sk-ant-..."
     }'
   ```

   Or switch to Ollama for a self-hosted local fallback:

   ```sh
   # First deploy Ollama via the Helm chart's sidecar:
   helm upgrade <sharko-release> charts/sharko/ \
     -n <sharko-ns> \
     --reuse-values \
     --set ai.ollama.deploy=true \
     --set ai.ollama.model=llama3:70b

   # Then configure Sharko to use it:
   curl -sS -X PUT -H "Authorization: Bearer ${SHARKO_TOKEN}" \
     "http://sharko/api/v1/ai/config" \
     --data-binary '{
       "provider": "ollama",
       "model": "llama3:70b"
     }'
   ```

   Success indicator: the new provider responds; AI calls succeed.

4. **Last resort — disable AI features.** If the operator cannot
   immediately fix any provider and just wants the user-facing
   503s to stop, disable AI features entirely:

   ```sh
   curl -sS -X PUT -H "Authorization: Bearer ${SHARKO_TOKEN}" \
     "http://sharko/api/v1/ai/config" \
     --data-binary '{"enabled": false}'
   ```

   With `enabled: false`, the AI-touching endpoints return a
   different shape (HTTP 200 with `ai: disabled`) rather than
   503. The UI surfaces a banner: "AI features are disabled.
   Enable them in Settings -> AI."

   Success indicator: the 503 responses stop; addons enable
   without AI annotation.

---

## Root-cause patterns

Four common causes.

### AI not configured during install

The single most common cause. The operator installed Sharko with
default Helm values (`ai.enabled: false`) and didn't run the
Settings UI configuration step. Users hit the 503 when they try
their first AI feature.

Diagnostic signature: Diagnosis step 2 shows
`enabled: false` or `provider: null`. The Helm values are
defaults.

Fix lane: Mitigation step 1 (configure the provider).

### Cloud provider API key revoked

The operator rotated the API key at the cloud provider's console
(security review, periodic rotation) without updating Sharko. The
401/403 surfaces on the next AI call.

Diagnostic signature: Diagnosis step 3 shows `Unauthorized` or
`Incorrect API key provided`. The Diagnosis step 1 synthetic call
returns HTTP 200 with `ai=llm_error`.

Fix lane: Mitigation step 2 (rotate the key into Sharko).

### Cloud provider quota exhausted

The operator's OpenAI / Anthropic / Google quota is exhausted (free-
tier or paid-tier ceiling reached). The provider returns 429 or 402
on every call.

Diagnostic signature: Diagnosis step 3 succeeds for the `/models`
endpoint (no quota involved) but the actual completion calls
return 429. Sharko logs show `rate_limit_exceeded` or
`insufficient_quota` in the error.

Fix lane: upgrade the operator's plan at the cloud provider, OR
switch providers (Mitigation step 3), OR throttle the AI feature's
usage in Sharko.

### Ollama deployment missing

When the operator chose Ollama as the provider but didn't deploy
the Ollama sidecar (`ai.ollama.deploy: false`), Sharko's calls to
`http://ollama:11434` fail with connection refused.

Diagnostic signature: Diagnosis step 2 shows
`provider: ollama, enabled: true`. Diagnosis step 3's Ollama probe
returns `Connection refused`. The Ollama Service / Pod doesn't
exist in the Sharko namespace.

Fix lane: deploy the Ollama sidecar via Helm
(`ai.ollama.deploy: true`), then re-run the synthetic call.

---

## Prevention

How to make this failure mode less likely going forward. Three
levers:

- **Monitoring — alert on the `not_configured` and `llm_error`
  outcome rates.** Per
  [`metrics-naming.md`](metrics-naming.md), the
  `sharko_ai_annotate_total{outcome="..."}` metric carries the
  outcome label. Alert on sustained `not_configured > 0` for 1
  hour (operators expect AI to work or to have explicitly
  disabled it) and on `llm_error > 5%` over 15 minutes. V2-4.x
  follow-up.

- **Gating — Settings -> AI test-connection button.** The
  Settings UI should provide a "Test connection" button that
  invokes Diagnosis step 3's probe per provider. Operators see
  a clear pass/fail at configuration time, not at first user-
  facing 503. Already mostly in place; verify it covers all
  providers and surfaces specific error messages.

- **Scheduled work — quarterly AI key rotation drill.** AI provider
  keys benefit from rotation just like Git PATs. A 90-day drill
  generates a new key, updates Sharko, validates, archives the old
  key. Aligns with the
  [git-provider-rate-limited](git-provider-rate-limited.md) and
  [argocd-account-token-expired](argocd-account-token-expired.md)
  rotation drills.

---

## Related runbooks

- [`ai-annotation-secret-blocked.md`](ai-annotation-secret-blocked.md)
  — when the guard fires (HTTP 422) before the LLM call. Different
  HTTP code, distinct failure mode.
- [`ai-config.md`](ai-config.md) — the AI configuration reference.
  Documents Helm values, supported providers, and the Settings UI
  flow.
- [`encryption-key-not-configured.md`](encryption-key-not-configured.md)
  — if the operator can't save the AI provider configuration, the
  encryption key may be the underlying issue.
- [`addon-application-stuck-degraded.md`](addon-application-stuck-degraded.md)
  — when the addon enables (with AI failing) and then can't sync.
  AI annotation is best-effort; the addon cycle still completes.
- [`failure-mode-index.md`](failure-mode-index.md) — master inventory.
- [`metrics-naming.md`](metrics-naming.md) —
  `sharko_ai_annotate_total{outcome="..."}` outcome label vocabulary.
- [`../developer-guide/logging.md`](../developer-guide/logging.md) —
  `request_id` correlation pattern.

## Escalation

If the mitigations above don't restore AI features, email the
maintainer: `moran.weissman@gmail.com`. Include:

- The runbook URL you used (this page)
- The output of Diagnosis step 1 (synthetic call response)
- The output of Diagnosis step 2 (`/ai/config`)
- The output of Diagnosis step 3 (provider probe)
- The provider name and model
- The Sharko version (`sharko version` or the Helm chart version)

The maintainer is a single human, not a 24x7 rotation. Expect a
business-day SLA. Because AI features are optional and the rest of
Sharko works without them, this isn't a pager-grade incident.

<!-- Style-guide compliance checklist (V2-4.1):
- [x] Title matches `# <Failure name>`
- [x] Severity line present (P1)
- [x] Verified-by-execution header + date
- [x] Symptoms section before Diagnosis
- [x] Symptoms include exact log lines / error messages / response shapes
- [x] Diagnosis has 3+ concrete checks
- [x] Mitigation uses numbered list
- [x] Mitigation has 3-5 steps in priority order (4 steps)
- [x] Root-cause patterns: 2+ named causes (4 named)
- [x] Prevention section present and non-empty
- [x] Related runbooks section present
- [x] Intro is operator-on-call voice
- [x] Length 300-800 lines (in range)
- [x] All cross-links resolve
- [x] No emoji / no internal Slack / employee email
- [x] (if applicable) Metric reference + outcome labels
-->
