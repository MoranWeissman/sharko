# AI configuration (v1.21)

Sharko's AI integration powers two features:

- **Upgrade analysis** (v1.18+): summarises chart upgrades, flags risk, lists action items.
- **Smart-values AI annotation** (v1.21 Epic V121-7): adds inline `# description` comments to generated values files and improves cluster-specific field detection. The annotation pass is an optional second pass on top of the deterministic [smart-values pipeline](../user-guide/smart-values.md) — the heuristic split runs whether or not AI is configured.

Both run against the same configured provider. This guide is for operators wiring the provider, sizing token budgets, and watching cost.

## Configuring a provider

Settings → AI exposes the supported providers:

| Provider | Key field | Notes |
| --- | --- | --- |
| Gemini (Google) | `AIza...` API key | Default; cheapest for Sharko's prompt sizes (Flash tier). |
| Claude (Anthropic) | `sk-ant-...` API key | Higher quality on annotation, ~3× cost. |
| OpenAI | `sk-...` API key | GPT-4o or GPT-4o-mini. |
| Ollama (local) | URL only | No API key. Runs in-cluster or on a sidecar. |
| Custom OpenAI-compatible | API key + base URL | For self-hosted gateways. |

The configuration is persisted as an encrypted Kubernetes Secret named `sharko-ai-config` in the namespace where Sharko runs. The secret is encrypted with the same key Sharko uses for connection credentials (the `SHARKO_ENCRYPTION_KEY` env var). Rotating the key requires re-saving the AI config.

## The "Annotate values on generate" toggle

A toggle in the AI section controls whether the V121-7 annotate pass runs on Add Addon and Refresh from upstream. Default-ON when AI is configured for the first time. Toggling OFF disables annotation globally; per-addon opt-out is a separate finer-grained control.

When the toggle is OFF, the values file's header carries `# AI annotation: disabled`. Operators can grep for this state across the addons repo if they want to find files that haven't been annotated.

## Cost considerations

Sharko's annotation prompt is bounded but not tiny — typical chart `values.yaml` files run 2-15 KB, and the LLM response is similar. With Gemini Flash:

- ~1500 tokens input per chart (5 KB upstream values).
- ~1000 tokens output per chart (annotations + cluster-specific paths).
- ~$0.0002 per addon-add at current Gemini Flash pricing.

Multiplied across a typical 20-50 addon catalog, the bootstrap cost is ~$0.02. Refresh-from-upstream re-runs the call, so the steady-state cost depends on how often you refresh — typically a few times a year per addon. Most operators see total annotation spend under $1/year.

If your chart `values.yaml` is significantly larger (50 KB+), Sharko skips the LLM call entirely and falls back to heuristic-only output. The 50 KB cap keeps a single addon-add from blowing through your token budget.

## Latency cap

The annotation pass has a 30-second latency cap. If the LLM doesn't respond within 30s, Sharko aborts and uses heuristic-only output. This bound keeps Add Addon non-blocking on flaky LLM providers.

For Ollama operators: the latency cap is the same regardless of model size. Heavyweight local models may consistently time out — track the `sharko_ai_annotate_total{outcome="timeout"}` counter and switch to a faster model if the rate exceeds ~10%.

## Secret-leak hard block

A regex pre-scan runs **before** any values bytes leave Sharko. The scanner looks for AWS keys, GitHub PATs, JWTs, Google API keys, Slack tokens, PEM private keys, and generic `apiKey: <16+ chars>` / `password: <16+ chars>` / `token: <16+ chars>` assignments.

On any match, the LLM call is **hard-blocked**. There is no "send anyway" override. The annotation pass falls through to heuristic-only output and the operator sees a redacted match summary in the audit log:

```
audit_log: {"event": "ai_annotate_blocked", "addon": "demo-chart", "detail": "chart=demo version=1.0 matches=3"}
```

The actual secret values never appear in audit, logs, or HTTP responses — fields are masked with `***`.

The locked design choice is that any chart shipping real secrets in `values.yaml` should be fixed at the chart level (move the secret to a SealedSecret / ExternalSecret reference). Sharko's job is to refuse to make the leak worse.

## Metrics

Prometheus metrics for AI annotation:

```
# Total calls by outcome.
sharko_ai_annotate_total{outcome="ok"}
sharko_ai_annotate_total{outcome="not_configured"}
sharko_ai_annotate_total{outcome="opted_out"}
sharko_ai_annotate_total{outcome="oversize"}
sharko_ai_annotate_total{outcome="secret_blocked"}
sharko_ai_annotate_total{outcome="timeout"}
sharko_ai_annotate_total{outcome="llm_error"}
sharko_ai_annotate_total{outcome="parse_error"}

# Latency histogram, partitioned by outcome.
sharko_ai_annotate_latency_seconds_bucket{outcome="ok"}
sharko_ai_annotate_latency_seconds_count{outcome="ok"}
sharko_ai_annotate_latency_seconds_sum{outcome="ok"}
```

### Suggested alerts

- **High `secret_blocked` rate** (>5/hour): a chart in your catalog is shipping secret-like values. Inspect recent Add Addon attempts and clean the chart.
- **High `timeout` rate** (>10% of calls): your LLM provider is degraded. Switch providers or disable annotation temporarily.
- **`llm_error` spike**: usually an API key issue (rotated, quota exceeded). Test from Settings → AI → Test.

## Disabling annotation entirely

If you want AI annotation off globally without removing the AI config (the upgrade analysis still works), flip the **Annotate values on generate** toggle in Settings → AI. The audit log records `ai_config_updated` with the new state.

To disable annotation for a single addon, use the per-addon opt-out toggle on the addon's Catalog tab — that's persisted as a `# sharko: ai-annotate=off` directive in the values file header and survives chart upgrades.

## What's NOT cached

Sharko does not cache LLM responses in a Kubernetes ConfigMap, BoltDB, or any other Sharko-side store. The annotated values file in your Git repo IS the cache — once an addon is generated, the file lives in `configuration/addons-global-values/<addon>.yaml` and subsequent reads serve from Git. A fresh LLM call only happens when the chart version changes (catalog pin moves) and the user clicks Refresh from upstream.

This is a deliberate design choice. The Git repo is the source of truth; Sharko is stateless w.r.t. annotation results.
