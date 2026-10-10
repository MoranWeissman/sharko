# AI features don't work

Sharko's AI features (notes in values files, upgrade summaries, the assistant) need an AI provider such as OpenAI, Claude, Gemini or Ollama. Either no provider is set up, or the provider turns Sharko's requests down.

**How urgent:** Low. AI features are optional. Clusters, addons and everything else keep working without them.

Before you start, set up the shell variables from [First checks](troubleshooting.md#set-up-once).

## What you see

There are two different cases.

**Case 1: no provider is set up.**

- **AI annotate** on an addon's values page fails. The API call `POST /api/v1/addons/{name}/values/annotate` answers with HTTP 503:

  ```json
  {"error": "AI is not configured; configure a provider in Settings → AI"}
  ```

- **Settings > AI Provider** shows **Not Configured**.
- The test call answers with HTTP 503 and `AI not configured`:

  ```sh
  curl -s -X POST -H "Authorization: Bearer $SHARKO_TOKEN" "$SHARKO_URL/api/v1/ai/test" | jq
  ```

**Case 2: a provider is set up, but its calls fail.**

- **AI annotate** still opens a pull request, but the values file has no AI notes in it. The API answer has an `ai_skip_reason` of `llm_error`, or `timeout` when the provider took more than 30 seconds.
- The logs show why:

  ```sh
  kubectl -n "$SHARKO_NS" logs deploy/sharko --since=1h | grep 'ai annotate fell through to heuristic-only'
  ```

  Each line has `reason` (`llm_error` or `timeout`) and `error`. The `error` field holds the provider's own message, for example a wrong key or a used-up quota.

- The test call answers with HTTP 500 and the provider's error message:

  ```sh
  curl -s -X POST -H "Authorization: Bearer $SHARKO_TOKEN" "$SHARKO_URL/api/v1/ai/test" | jq
  ```

- The metric `sharko_ai_annotate_total` goes up with `outcome="llm_error"` or `outcome="timeout"`. Sharko does not ship an alert for it.

If **AI annotate** answers with HTTP 422 and `secret_detected_blocked`, the provider is fine. See [AI notes were not added because the chart values look like they hold a secret](ai-annotation-secret-blocked.md).

## Why it happens

Sharko gets its AI settings from two places:

- **The Helm chart.** The `ai` settings: `ai.enabled`, `ai.provider`, `ai.apiKey`, `ai.cloudModel`, `ai.baseURL`, `ai.authHeader`, `ai.maxIterations`, and `ai.ollama.*`. AI is off by default (`ai.enabled: false`).
- **The UI**, on **Settings > AI Provider**. What you save there is kept, encrypted, in the Kubernetes Secret `sharko-ai-config`.

When Sharko starts, it uses what was saved in the UI. But if the Helm chart also sets the provider, model or address, the Helm values win for those fields. The API key you saved in the UI is kept.

Common reasons it fails:

- **AI was never set up.** The chart default is off, and nobody set a provider in the UI.
- **The API key is wrong, was deleted, or ran out of quota** at the provider.
- **The model name is wrong**, or your account can't use that model.
- **Ollama is chosen, but no Ollama is running** where Sharko looks for it. When `ai.ollama.deploy` is `false` and `ai.ollama.url` is empty, Sharko looks at `http://localhost:11434`, and there is no Ollama inside the Sharko pod.
- **A network rule or company proxy blocks** the pod from reaching the provider.

## How to fix it

### 1. See what is set up now

```sh
curl -s -H "Authorization: Bearer $SHARKO_TOKEN" "$SHARKO_URL/api/v1/ai/config" | jq
```

You get `current_provider`, a list of `available_providers` (each with `id`, `configured` and `model`) and `annotate_on_seed`. The API key is never shown. An empty `current_provider`, or `none`, means Case 1.

Also look at the Helm values:

```sh
helm -n "$SHARKO_NS" get values sharko | grep -A12 '^ai:'
```

### 2. Set up the provider in the UI

1. Open **Settings > AI Provider** and click **Configure** (or **Edit**).
2. Pick the provider: Gemini, Claude, OpenAI, Ollama, or Custom OpenAI-compatible.
3. Paste the API key. For Ollama, give the Ollama address. For a custom provider, give the base URL.
4. Pick the model.
5. Click **Test AI**. It sends one short request and does not save anything. Fix what it complains about until it says **Connected**.
6. Click **Save**.

To change only the key, open **Edit** and paste the new key. Then test and save.

### 3. Or set it up with the API

`POST /api/v1/ai/config` replaces the whole AI setting. Always send the provider, the key and the model together. If you leave out `api_key`, the saved key is cleared. You need an admin token. Allowed `provider` values are `openai`, `claude`, `gemini`, `ollama`, `custom-openai` and `none`.

First test the setting without saving it. Use the real key in place of `<your key>`:

```sh
curl -s -X POST -H "Authorization: Bearer $SHARKO_TOKEN" -H "Content-Type: application/json" \
  "$SHARKO_URL/api/v1/ai/test-config" \
  -d '{"provider": "openai", "api_key": "<your key>", "model": "<model name>"}' | jq
```

`"status": "ok"` means it works. `"status": "error"` comes with the provider's message. Then save it:

```sh
curl -s -X POST -H "Authorization: Bearer $SHARKO_TOKEN" -H "Content-Type: application/json" \
  "$SHARKO_URL/api/v1/ai/config" \
  -d '{"provider": "openai", "api_key": "<your key>", "model": "<model name>"}' | jq
```

For Ollama, send `"ollama_url"` instead of `"api_key"`. For `custom-openai`, also send `"base_url"`. No restart is needed. Keep the key out of your shell history if you can, for example by typing it into the UI instead.

If saving fails with `failed to persist AI config`, see [Sharko's encryption key is missing or changed](encryption-key-not-configured.md).

### 4. Or set it up in the Helm chart

Put the settings in a values file. The key goes into the chart's Secret, not into the pod settings:

```yaml
ai:
  enabled: true
  provider: openai
  apiKey: "<your key>"
  cloudModel: "<model name>"
```

For Ollama run by the chart, use `provider: ollama` and `ollama.deploy: true`. The chart then starts an Ollama pod and points Sharko at it. For an Ollama you already run, set `ollama.url` instead.

```sh
helm -n "$SHARKO_NS" upgrade sharko oci://ghcr.io/moranweissman/sharko/sharko \
  --reuse-values -f my-ai-values.yaml
```

Remember: if a key was saved in the UI before, that saved key is still used after a restart. To use the Helm key instead, save the new key in the UI too, or save it once with the API as in step 3.

### 5. Or turn AI off

If you don't want AI features, click **Disable** on **Settings > AI Provider**. This saves the provider as `none`. If your Helm values set `ai.enabled: true` with a provider, also set `ai.enabled: false`. If you don't, the Helm provider comes back at the next restart.

## How to check it's fixed

1. The test call works:

   ```sh
   curl -s -X POST -H "Authorization: Bearer $SHARKO_TOKEN" "$SHARKO_URL/api/v1/ai/test" | jq
   ```

   You get `"status": "ok"` and a short answer from the AI.

2. **AI annotate** on one addon opens a pull request, and the answer has no `ai_skip_reason`:

   ```sh
   curl -s -X POST -H "Authorization: Bearer $SHARKO_TOKEN" "$SHARKO_URL/api/v1/addons/<addon>/values/annotate" | jq '{pr_url, merged, ai_skip_reason}'
   ```

   Note that this opens a real pull request in your Git repo.

3. No new failures in the logs:

   ```sh
   kubectl -n "$SHARKO_NS" logs deploy/sharko --since=10m | grep 'ai annotate fell through'
   ```

If you turned AI off, **Settings > AI Provider** shows **Not Configured**, and that is the expected state.

## If that didn't work

Open an issue at <https://github.com/MoranWeissman/sharko/issues>. Include:

- the Sharko version (`sharko version`, or the `version` field from `/api/v1/health`)
- the log lines from the last hour that mention `ai annotate` or `ai config`
- the output of `GET /api/v1/ai/config` (it has no key in it)
- what you tried from this page

Never paste your API key into the issue.

Related pages:

- [AI configuration](ai-config.md)
- [AI notes were not added because the chart values look like they hold a secret](ai-annotation-secret-blocked.md)
- [Sharko's encryption key is missing or changed](encryption-key-not-configured.md)
- [First checks](troubleshooting.md)
