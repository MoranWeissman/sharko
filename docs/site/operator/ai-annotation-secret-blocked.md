# AI Annotation Hard-Blocked by Secret-Leak Guard

**Severity:** P1

> **Verified:** Authored 2026-06-01 against `main` HEAD. The hard-
> block path (audit `event=ai_annotate_blocked`, log line
> `"ai annotate hard-blocked: secret-leak pattern matched"`, HTTP 422
> with the `aiAnnotateBlockedResponse` envelope, and the
> `SecretLeakError` typed error) is verified verbatim against
> `internal/orchestrator/ai_annotate.go:124-131` and
> `internal/api/ai_annotate.go:144-163` as shipped. The
> `ScanForSecrets` heuristic runs BEFORE any LLM network call — by
> design, the values payload never leaves the Sharko process when
> the guard fires. The locked decision (no override flag, no opt-out
> per request) is encoded at the call site. Re-verify before the
> heuristic regex set changes (`internal/orchestrator/secrets_scan.go`)
> or the audit event name changes — both are anchors for the
> diagnosis below.

The AI annotation pass on an addon's `values.yaml` was blocked
because Sharko's secret-leak guard matched a credential-shaped
substring in the upstream chart's values. The hard block runs
**before** any LLM network call, so no values content left the
Sharko process — the payload was never sent to OpenAI, Claude,
Gemini, or any other configured provider.

This is the **correct behavior**, not a bug: by design, when an
addon's stock `values.yaml` includes example or placeholder
credentials (`api_key: "your-key-here"`, a base64 blob, a JWT-shaped
string), Sharko refuses to send them to the LLM. The locked design
decision per the audit log is "no override flag — refuse to send
to the LLM if any pattern matches" so an operator cannot accidentally
disable the guard for one request.

The blast radius is **one addon's annotation pass**. Other addons
annotate normally; the affected addon's `values.yaml` is rendered
without AI annotations (the heuristic still runs and adds
`cluster-specific` markers; only the LLM-generated `# description`
comments are missing). The cluster operation that triggered the
annotation (typically `POST /addons/{name}` or
`PATCH /addons/{name}/values/annotate`) completes successfully with
`ai=secret_blocked` in the result envelope.

---

## Symptoms

What an operator sees when this fires:

- **HTTP 422** from `POST /api/v1/addons/{name}/values/annotate` with
  the typed response shape:

  ```json
  {
    "code": "secret_leak_blocked",
    "message": "Secret-like content detected in upstream values; AI annotation is hard-blocked.",
    "matches": [
      {"pattern": "api_key", "match": "<redacted: 21 chars>", "line": 14},
      {"pattern": "base64_blob", "match": "<redacted: 88 chars>", "line": 32}
    ]
  }
  ```

  The `matches` array is **redacted on emission** — the actual
  matched bytes are not echoed. Only the pattern name, match length,
  and line number surface. This is intentional; the audit-log entry
  must not contain the credential-shaped substring even on the
  failure path.

- **`kubectl logs` Warn line** from the orchestrator:

  ```
  {"time":"...","level":"WARN","msg":"ai annotate hard-blocked: secret-leak pattern matched","chart":"datadog","version":"3.74.0","match_count":2}
  ```

  Grep:

  ```sh
  kubectl -n <sharko-ns> logs -l app=sharko --tail=5000 \
    | jq -c 'select(.msg | test("ai annotate hard-blocked"; "i"))'
  ```

- **Audit log entry** with `event=ai_annotate_blocked`:

  ```sh
  curl -sS -H "Authorization: Bearer ${SHARKO_TOKEN}" \
    "http://sharko/api/v1/audit?event=ai_annotate_blocked&limit=20" \
    | jq -r '.[] | "\(.time) \(.resource) \(.detail)"'
  ```

  Sample output:

  ```
  2026-06-01T10:00:00Z addon:datadog chart=datadog version=3.74.0 matches=2
  ```

- **A secondary audit entry** with `event=secret_leak_blocked` and
  `surface=ai_annotate` is emitted (per `emitSecretLeakAuditBlock` in
  `internal/api/ai_annotate.go:156`) so the security review can
  enumerate every blocked attempt across handlers with one query:

  ```sh
  curl -sS -H "Authorization: Bearer ${SHARKO_TOKEN}" \
    "http://sharko/api/v1/audit?event=secret_leak_blocked&limit=20" \
    | jq -r '.[] | "\(.time) \(.detail)"'
  ```

- **`sharko_ai_annotate_total{outcome="secret_blocked"}` Prometheus
  metric** increments. Per
  [`metrics-naming.md`](metrics-naming.md), the outcome labels
  documented for this metric are
  `ok|not_configured|empty_input|oversize|secret_blocked|timeout|llm_error|parse_error|opted_out|disabled`.

- **The cluster operation that triggered the annotation completes
  successfully** with `ai=secret_blocked` in the response (per
  `internal/api/addons_write.go:170-176`). The addon enables / updates
  on the cluster fine; only the LLM-generated `# description` comments
  are absent in the rendered `addons-global-values/<addon>.yaml`.

- **UI symptom**: the operator sees a banner on the addon
  configuration page: "Secret-like content detected in upstream
  values; AI annotation is hard-blocked. The addon will be enabled
  without AI-generated description comments. Review the chart's
  upstream values for credential placeholders; see runbook."

- **No specific Prometheus alert fires today.** A sustained
  rise in `secret_blocked` per addon may indicate a chart-side
  regression (upstream chart starts shipping example credentials)
  — wiring an alert on the metric is a V2-4.x follow-up.

If the symptom is **HTTP 503** with `"AI is not configured"` (no
provider configured) instead of HTTP 422, this is a different
failure mode. Jump to
[`ai-provider-misconfigured.md`](ai-provider-misconfigured.md).

---

## Diagnosis

Three checks: confirm the block was correct, identify which
pattern fired, decide whether the upstream chart needs a fix.

### 1. Confirm the audit-log entry matches the operator's request

Cross-reference the `request_id` from the operator's request with
the audit entry, per the
[V2-2.2 correlation pattern](../developer-guide/logging.md#correlation-ids):

```sh
REQUEST_ID=req-<from operator's response headers>
kubectl -n <sharko-ns> logs -l app=sharko --tail=5000 \
  | jq -c "select(.request_id == \"$REQUEST_ID\")" \
  | jq -r '"\(.time) \(.level) \(.msg) \(.chart // \"\") \(.match_count // \"\")"'
```

Expected output: a single Warn line `"ai annotate hard-blocked:
secret-leak pattern matched"` with the matching `chart`,
`version`, and `match_count` fields. If multiple Warn lines fire on
the same request_id, multiple addons' annotations were blocked in
the same batch.

### 2. Identify which pattern fired

The response body's `matches` array names the pattern but not the
content. Common patterns and what they catch (per
`internal/orchestrator/secrets_scan.go`):

| Pattern name | Catches |
|---|---|
| `api_key` | `api_key:`, `apikey:`, `api-key:` followed by a non-empty value |
| `aws_access_key` | `AKIA[0-9A-Z]{16}` or `ASIA[0-9A-Z]{16}` |
| `bearer_token` | `Bearer ` followed by a token-shaped string |
| `base64_blob` | Long base64 strings (>40 chars) outside of known-safe contexts |
| `jwt` | `eyJ`-prefixed three-segment dot-separated string |
| `password` | `password:` followed by a non-empty value |
| `private_key` | `-----BEGIN ... PRIVATE KEY-----` |

The pattern name + line number from the response body is enough to
locate the offending content in the upstream chart's values file.

### 3. Inspect the upstream values file

Fetch the chart's `values.yaml` directly from the upstream Helm
registry:

```sh
ADDON_CHART=datadog
ADDON_VERSION=3.74.0
ADDON_REPO=https://helm.datadoghq.com

helm pull "$ADDON_REPO" --version "$ADDON_VERSION" \
  --untar --untardir /tmp/chart-inspect/

# Read the values.yaml:
cat /tmp/chart-inspect/$ADDON_CHART/values.yaml | grep -n -A 1 -B 1 -E "api_key|password|bearer|AKIA"
```

The lines matching the patterns are the ones that tripped the
guard. Three possible interpretations:

- **Placeholder content** (e.g. `api_key: "<your-key-here>"`,
  `password: "changeme"`) — the chart's authors expect the value to
  be overridden per deployment. Sharko's guard is being cautious;
  the placeholder isn't a real credential. Mitigation step 2 covers
  this.
- **Example content** (e.g. an embedded example AWS access key for
  documentation purposes) — uncommon, but real. Mitigation step 2
  also covers this.
- **An actual leaked credential in upstream values** (e.g. the
  chart authors committed a real key by mistake) — rare but it
  happens. The block is correct; the right action is to report the
  upstream issue and refuse to deploy this version of the chart.
  Mitigation step 3 covers this.

---

## Mitigation (try in order)

The block is **by design**. The mitigation is not "disable the
guard" (no operator override exists); it's "decide whether the
charts's stock values are safe, and either route around the
LLM-annotation step or fix the upstream chart."

1. **Enable the addon without AI annotation.** The blocked
   annotation is a non-blocking failure: the cluster operation
   completes successfully without it. If the operator just wanted
   the addon enabled and didn't specifically need AI-generated
   description comments, the operation already succeeded — no
   further action is needed.

   Verify:

   ```sh
   curl -sS -H "Authorization: Bearer ${SHARKO_TOKEN}" \
     "http://sharko/api/v1/addons/$ADDON" \
     | jq '{enabled, ai_state: .ai}'
   ```

   Expected: `enabled: true`, `ai_state: "secret_blocked"`. The
   addon is enabled and configured; only the LLM annotation is
   absent. This is the cheapest path.

   Success indicator: the addon is enabled on the cluster (visible
   in `argocd app list`); the operator can proceed with their next
   task.

2. **Pre-redact the upstream values and submit the redacted version
   for annotation.** When the operator specifically wants AI
   annotation (e.g. for the global-values smart-values layer) and
   the upstream content has placeholder credentials, the path is
   to take the upstream values, redact the credential-shaped lines
   (replace with `# REDACTED`), and submit those redacted values
   to a separate annotation endpoint or just paste them into the
   smart-values editor.

   ```sh
   helm pull "$ADDON_REPO" --version "$ADDON_VERSION" --untar --untardir /tmp/chart-inspect/
   sed -i.bak \
     -e 's/api_key:.*/api_key:  # REDACTED — set via cluster overrides/' \
     -e 's/password:.*/password:  # REDACTED — set via cluster overrides/' \
     /tmp/chart-inspect/$ADDON_CHART/values.yaml
   ```

   Then paste the redacted file into the smart-values editor in the
   UI, or attach it to the addon's global values file via the
   `PUT /api/v1/addons/{name}/values` endpoint. The annotation pass
   on the redacted content succeeds (no patterns match), and the
   operator gets LLM-generated comments without leaking placeholders.

   Success indicator: a fresh annotation attempt against the
   redacted content returns HTTP 200 with the annotated YAML.

3. **Report the upstream chart and pin to a known-safe version.** If
   Diagnosis step 3 surfaced what looks like a real leaked
   credential (not a placeholder), report it upstream to the chart
   maintainer. In the meantime, pin Sharko's catalog entry to a
   prior version known to be clean:

   ```sh
   # Inspect the per-cluster version override:
   curl -sS -H "Authorization: Bearer ${SHARKO_TOKEN}" \
     "http://sharko/api/v1/addons/$ADDON_CHART" | jq '{version, latest_version}'

   # Pin to a known-safe earlier version:
   curl -sS -X POST -H "Authorization: Bearer ${SHARKO_TOKEN}" \
     "http://sharko/api/v1/addons/$ADDON_CHART/upgrade" \
     --data-binary '{"version":"<safe-version>", "cluster":"<cluster>"}'
   ```

   Open the upstream issue with the chart author, ref the line
   number from the audit, and request a fix.

   Success indicator: subsequent annotation attempts on the safe
   version pass; the upstream issue is tracked for follow-up.

4. **Opt the addon out of AI annotation entirely.** If the operator
   genuinely doesn't want AI annotation for this addon (privacy
   policy, organizational policy, or just preference), set the AI
   opt-out flag on the global values file:

   ```sh
   curl -sS -X PUT -H "Authorization: Bearer ${SHARKO_TOKEN}" \
     "http://sharko/api/v1/addons/$ADDON/values/ai-opt-out" \
     --data-binary '{"opted_out": true}'
   ```

   After opt-out, the annotation endpoint refuses with HTTP 409
   `"this addon is opted out of AI annotation"`. The heuristic
   layer still runs (cluster-specific path markers); only the LLM
   layer is skipped, by operator request.

   Success indicator: subsequent annotation attempts return HTTP
   409 with the opt-out reason. The addon enables normally without
   AI annotation.

---

## Root-cause patterns

Three common causes.

### Placeholder credentials in upstream chart values

By far the most common cause. The chart authors include example or
placeholder values for required credentials (`api_key: "your-key"`)
expecting deployers to override them. Sharko's guard is correctly
pattern-matching these placeholders even though they aren't real
credentials.

Diagnostic signature: Diagnosis step 3 shows generic placeholder
strings (`"your-key-here"`, `"changeme"`, `"<token>"`) at the
flagged line numbers. The pattern that fired is usually
`api_key`, `password`, or `bearer_token`.

Fix lane: Mitigation step 1 (proceed without annotation) or step 2
(pre-redact and re-annotate). Both are routine.

### Embedded example credentials in chart documentation

Some charts include verbose comments in their values files with
example values for educational purposes — sometimes including
real-looking (but example) credentials. Sharko's guard catches
these along with real credentials.

Diagnostic signature: Diagnosis step 3 shows the flagged content is
inside a comment block or a documentation-shaped section. The
content "looks" like a credential (right shape) but is contextually
labeled as an example.

Fix lane: Mitigation step 2 (pre-redact). The chart maintainer
might be receptive to a PR rewriting the example as an obvious
placeholder.

### Actual leaked credential in upstream chart

Rare but real. A chart maintainer accidentally committed a real
credential to the chart's values file (e.g. a test API key, a
service-account token for the chart author's own dev cluster).

Diagnostic signature: Diagnosis step 3 surfaces a credential-
shaped value that's not labeled as an example. The pattern is
often `aws_access_key`, `jwt`, or `private_key` — patterns less
likely to surface in placeholder content.

Fix lane: Mitigation step 3 (report upstream, pin to a safe
version). Don't deploy this chart version until the credential is
removed upstream — Sharko's guard is doing exactly what it should.

---

## Prevention

How to make this failure mode less likely going forward. Three
levers:

- **Monitoring — alert on `sharko_ai_annotate_total{outcome="secret_blocked"}`
  growth per addon.** A normal addon should annotate cleanly across
  the fleet; a rising `secret_blocked` count is a signal that an
  upstream chart's values changed in a way that newly trips the
  guard. Wire an alert on >0 per chart per day. V2-4.x follow-up.

- **Gating — catalog scan validates chart values at scan time.** The
  daily catalog scanner bot
  ([`../developer-guide/catalog-scan-runbook.md`](../developer-guide/catalog-scan-runbook.md))
  could pre-run the `ScanForSecrets` heuristic over each chart's
  `values.yaml` and surface findings as catalog warnings. That moves
  the detection from "annotation attempt" to "catalog onboarding" —
  the operator sees the warning before configuring the addon.

- **Scheduled work — quarterly review of the heuristic regex set.**
  The `ScanForSecrets` patterns at
  `internal/orchestrator/secrets_scan.go` will drift relative to the
  shape of real upstream chart values over time. A quarterly review
  checks false-positive rate (audit log: `secret_blocked` count vs.
  ground-truth real-credential count) and adjusts the patterns. Too
  loose -> credentials leak; too strict -> annotation rarely works.

---

## Related runbooks

- [`ai-provider-misconfigured.md`](ai-provider-misconfigured.md) —
  when the AI provider itself isn't configured (no API key, no
  provider selected). HTTP 503, not HTTP 422.
- [`credential-leak-in-logs.md`](credential-leak-in-logs.md) — the
  P0 sibling for credential exposure in logs (vs. in chart values).
  Different surface, related guard discipline.
- [`addon-application-stuck-degraded.md`](addon-application-stuck-degraded.md)
  — when the addon enables (annotation blocked or otherwise) and
  then fails to sync. Different failure mode downstream.
- [`failure-mode-index.md`](failure-mode-index.md) — master inventory.
- [`metrics-naming.md`](metrics-naming.md) — documents
  `sharko_ai_annotate_total{outcome="..."}` and the `secret_blocked`
  outcome label.
- [`../developer-guide/logging.md`](../developer-guide/logging.md) —
  `request_id` correlation pattern.
- [`../developer-guide/catalog-scan-runbook.md`](../developer-guide/catalog-scan-runbook.md)
  — catalog scanner discipline; one possible Prevention surface.

## Escalation

If the operator believes the guard is firing on truly-safe content
and Mitigation step 2 (pre-redact + re-annotate) isn't acceptable
for their workflow, email the maintainer: `moran.weissman@gmail.com`.
Include:

- The runbook URL you used (this page)
- The addon chart name + version
- The pattern name from the response body's `matches` array
- The line numbers (NOT the matched content) from the response body
- The Sharko version (`sharko version` or the Helm chart version)

The maintainer is a single human, not a 24x7 rotation. Expect a
business-day SLA. Because the guard is by-design and the addon
operation completes successfully, this is not a pager-grade
incident.

<!-- Style-guide compliance checklist (V2-4.1):
- [x] Title matches `# <Failure name>`
- [x] Severity line present (P1)
- [x] Verified-by-execution header + date
- [x] Symptoms section before Diagnosis
- [x] Symptoms include exact log lines / error messages / response shapes
- [x] Diagnosis has 3+ concrete checks
- [x] Mitigation uses numbered list
- [x] Mitigation has 3-5 steps in priority order (4 steps)
- [x] Root-cause patterns: 2+ named causes (3 named)
- [x] Prevention section present and non-empty
- [x] Related runbooks section present
- [x] Intro is operator-on-call voice
- [x] Length 300-800 lines (in range)
- [x] All cross-links resolve
- [x] No emoji / no internal Slack / employee email
- [x] (if applicable) Metric reference includes outcome label
-->
