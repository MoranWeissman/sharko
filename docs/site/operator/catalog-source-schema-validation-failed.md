# Catalog Source Schema Validation Failed

**Severity:** P1

> **Verified:** Authored 2026-06-01 against `main` HEAD. The Warn log
> line `"catalog source schema validation failed"` is verified against
> `internal/catalog/sources/fetcher.go:708`, where it surfaces when the
> downstream `catalog.LoadBytesWithSource` /
> `catalog.LoadBytesWithVerifierAndSource` call returns a parse / enum
> / required-field error. The fetcher does NOT discard the prior
> snapshot on schema failure (per fetcher.go:710 +
> `recordSchemaFailure`); the previously-loaded entries from this
> source remain merged into the curated set. Re-verify when
> `LoadBytesWithSource` error wrapping or the fetcher status enum
> changes.

A third-party catalog source declared in `SHARKO_CATALOG_URLS` (or the
equivalent Helm value) returned a body that parsed as YAML but failed
the curated-catalog schema check — wrong enum value for `category` or
`curated_by`, missing required field, duplicate entry name, or an
empty `addons:` list. The source is marked as **Failed** in the
fetcher's status report; **the previous snapshot's entries from this
source are retained** so the marketplace continues to surface them
until the source author ships a fix. The embedded catalog and every
other third-party source are unaffected.

The failure is per-source and per-fetch. The fetcher retries on its
configured cadence (default per-source); a schema-fixed body on the
next fetch flips the source back to **Healthy** without operator
action. This runbook is for the case where an operator notices a
source is persistently Failed and needs to root-cause whether the
problem is upstream (the source author shipped a bad release), local
config (the source URL points to the wrong path), or a Sharko-side
schema drift (a new catalog field is required in v1.x+N but the
source was authored for v1.x+M).

---

## Symptoms

What an operator sees when this fires:

- **Sharko logs the warn line at the source-level fetch loop**
  (`internal/catalog/sources/fetcher.go:708`):

  ```
  {"time":"...","level":"WARN","msg":"catalog source schema validation failed","source_fp":"<8-char-fingerprint>","err":"schema validation: catalog: entry #3 (name=\"datadog\"): category \"telemetry\" is not in allowed set"}
  ```

  Or for required-field misses:

  ```
  {"time":"...","level":"WARN","msg":"catalog source schema validation failed","source_fp":"<fp>","err":"schema validation: catalog: entry #1 (name=\"\"): name is required"}
  ```

  Or for empty payloads / missing `addons:` root:

  ```
  {"time":"...","level":"WARN","msg":"catalog source schema validation failed","source_fp":"<fp>","err":"schema validation: catalog: no entries found under 'addons:'"}
  ```

  The `source_fp` is a stable 8-character fingerprint of the source
  URL. Operators correlate the fingerprint with the configured
  `SHARKO_CATALOG_URLS` list (Diagnosis step 1).

- **The fetcher status report flips the source to `failed`**:

  ```sh
  curl -sS http://sharko/api/v1/catalog/sources \
    -H "Authorization: Bearer ${SHARKO_TOKEN}"
  ```

  Returns entries like:

  ```json
  [
    {"url":"https://example.com/catalog.yaml","status":"failed","last_error":"schema validation: ..."}
  ]
  ```

- **The Marketplace UI** shows the source as **Failed** in the
  source-status panel, and shows the **previous snapshot's entries**
  from this source if any prior successful fetch loaded entries from
  it. New entries (anything the source added in the broken release)
  do NOT appear.

- **No Prometheus alert fires for a single-source schema failure
  today.** This is per-source per-cadence; the proactive metric is on
  the V2-3.x follow-up list.

If the symptom is "every catalog source is failing" (including the
embedded one), this is **not** the right runbook — see
[`catalog-parse-failure-on-startup.md`](catalog-parse-failure-on-startup.md)
(catalog loader failure on startup is P1 escalating toward P0 when
embedded is broken).

If the symptom is "this source's signature is unverified" rather than
"schema invalid," see
[`catalog-trust-policy.md`](catalog-trust-policy.md) — that's the
signing path, not the schema path.

---

## Diagnosis

Three checks. The first identifies which configured source is the
failing one; the second pulls the body for direct inspection; the
third confirms whether the failure is local-config drift or
upstream-author error.

### 1. Map the `source_fp` to the configured URL

The fingerprint in the log line is a stable 8-character hash of the
source URL. Get the configured URL list and the per-source status:

```sh
curl -sS http://sharko/api/v1/catalog/sources \
  -H "Authorization: Bearer ${SHARKO_TOKEN}" \
  | jq '.[] | {url, source_fp, status, last_error}'
```

The output pairs each source URL with its fingerprint and status. The
failing source has `status: "failed"` and `last_error` containing the
schema error message.

### 2. Pull the source body and validate locally

Once you have the failing URL, fetch it directly (use the same
Authorization header pattern your source requires; most public
catalogs are anonymous):

```sh
FAILING_URL=<url-from-step-1>
curl -sS "$FAILING_URL" > /tmp/failing-catalog.yaml
head -50 /tmp/failing-catalog.yaml
```

Then validate against the embedded schema using the Sharko CLI's
catalog validator (if shipped — falls back to manual schema-diff if
the CLI subcommand isn't present in your installed version):

```sh
# CLI path (preferred when available):
sharko validate-config /tmp/failing-catalog.yaml

# Manual path (for older versions):
# Check for: required fields (name, description, chart, repo,
# default_namespace, default_sync_wave), allowed categories
# (security, observability, networking, autoscaling, gitops, storage,
# database, backup, chaos, developer-tools), allowed curated_by
# values (cncf-graduated, cncf-incubating, cncf-sandbox,
# aws-eks-blueprints, azure-aks-addon, gke-marketplace,
# artifacthub-verified, artifacthub-official).
```

The exact schema lives in `catalog/schema.json` (embedded) and is
mirrored in `internal/catalog/loader.go` (constants
`allowedCategories`, `allowedCuratedBy`). If the failing field is
outside these enums, the source author needs to update their YAML; if
inside the enum but spelled differently (e.g. `"observability "` with
a trailing space), the author has a typo.

### 3. Confirm the failure is upstream and not Sharko-side drift

Sharko occasionally tightens the catalog schema (new required field,
new enum member). If multiple unrelated sources started failing at the
same time, the cause is more likely a Sharko upgrade than a coordinated
upstream regression.

Cross-reference:

```sh
# Sharko version:
curl -sS http://sharko/api/v1/version | jq

# Sharko deployment age (last rollout):
kubectl -n <sharko-ns> get deployment sharko \
  -o jsonpath='{.status.conditions[?(@.type=="Progressing")].lastTransitionTime}'

# Source-status history (when did this source last succeed?):
curl -sS http://sharko/api/v1/catalog/sources \
  -H "Authorization: Bearer ${SHARKO_TOKEN}" \
  | jq '.[] | {url, last_success_at, last_fetch_at}'
```

If `last_success_at` predates the most recent Sharko upgrade and the
upstream source has NOT changed (verify via the source's git log or
release notes), the failure is Sharko-side schema drift. Mitigation
step 4 covers the rollback / Helm pin path.

---

## Mitigation (try in order)

1. **Notify the source author and wait for an upstream fix.** The
   most common cause is the source author shipping a release that
   doesn't conform to the schema. Schema validation failures are
   per-fetch; the next successful fetch self-heals the status.

   Send the source author the specific error from
   `last_error` so they don't have to reproduce. The fetcher status
   API returns the full error in a single field.

   While waiting: the marketplace continues to surface the previous
   snapshot's entries from this source (per
   `internal/catalog/sources/fetcher.go:710` —
   `recordSchemaFailure` does NOT clear the prior entries). Operators
   experience zero user-visible degradation as long as the prior
   snapshot is recent.

2. **If the failure is "no entries found under `addons:`" the source
   URL is wrong.** Verify the URL points at the actual catalog file
   and not an index page / directory listing / 404 returning HTML:

   ```sh
   curl -sS -o /dev/null -w '%{http_code} %{content_type}\n' "$FAILING_URL"
   ```

   Expected: `200 application/yaml` or `200 text/yaml` or
   `200 text/plain`. A `200 text/html` is the giveaway — the URL
   returned a webpage, which YAML-parsed produces no `addons:` root.

   Fix the Helm value:

   ```sh
   helm upgrade --reuse-values \
     --set "catalog.sources={https://example.com/catalog/addons.yaml,https://other.com/catalog.yaml}" \
     sharko sharko/sharko -n <sharko-ns>
   ```

   The path-correction is operator-side; the fetcher will pick up the
   new URL on its next refresh cycle (or sooner if you trigger a
   manual refresh).

3. **If the failure is an enum / required-field issue and the source
   author cannot fix it on a timeline that works for you, remove the
   source from the active set.** This is a deliberate trade-off:
   removing the source means losing access to all its entries (not
   just the broken release). Document the removal in your runbook so
   it's restored once upstream is fixed:

   ```sh
   helm upgrade --reuse-values \
     --set "catalog.sources={<remaining-source-urls>}" \
     sharko sharko/sharko -n <sharko-ns>
   ```

   The fetcher drops the source on the next config reload; the
   previously-merged entries are removed from the marketplace.

4. **If Diagnosis step 3 indicated Sharko-side schema drift, roll back
   the Sharko upgrade or pin the catalog source author at a known-good
   release.** Schema tightening is a breaking change for third-party
   catalogs; release notes call it out, but operators may have missed
   the note.

   Roll back Sharko to the previous minor:

   ```sh
   helm rollback sharko -n <sharko-ns>
   ```

   Or pin the source URL to the last-good catalog version:

   ```sh
   helm upgrade --reuse-values \
     --set "catalog.sources={https://example.com/catalog/v1.21.yaml}" \
     sharko sharko/sharko -n <sharko-ns>
   ```

   File an issue against the source author with the schema change
   notes so they can ship a new release.

5. **Last resort — disable third-party catalog sources entirely.**
   If multiple sources are failing simultaneously and the embedded
   catalog is sufficient for your platform engineering needs:

   ```sh
   helm upgrade --reuse-values \
     --set "catalog.sources={}" \
     sharko sharko/sharko -n <sharko-ns>
   ```

   The embedded catalog still surfaces; only third-party additions
   are removed. This is a permanent reduction in marketplace scope —
   use only when running clean is more valuable than waiting for the
   source author.

---

## Root-cause patterns

### Source author shipped an enum mismatch

The most common cause. The source author added a new entry with
`category: telemetry` or `curated_by: home-grown` — values outside
the embedded `allowedCategories` / `allowedCuratedBy` sets defined
in `internal/catalog/loader.go`. The catalog rejects the entire
file because the loader fails on the first invalid entry rather than
skipping it (intentional — schema-violating entries are unsafe to
surface as curated content).

Diagnostic signature: `last_error` contains `"category \"X\" is not
in allowed set"` or `"curated_by \"Y\" is not in allowed set"`.

Fix is Mitigation step 1 (upstream fix) or step 3 (drop the source).

### Source URL points at a directory / index page

The fetcher GETs the configured URL and YAML-parses the body. A URL
that returns `text/html` (404 page, GitHub directory listing, NGINX
welcome page) parses as YAML to a non-`addons:` document, producing
the `"no entries found under 'addons:'"` error.

Diagnostic signature: `last_error` contains `"no entries found"` AND
`curl` on the URL returns `text/html` / `text/plain` with non-YAML
content.

Fix is Mitigation step 2 — correct the URL.

### Sharko schema tightened between releases

Sharko's `allowedCategories` / `allowedCuratedBy` sets are versioned
constants in `internal/catalog/loader.go`. A future minor adds
`network-security` to allowed categories; an older third-party
catalog using `network-security` would now succeed, but a future
removal of `developer-tools` would reject older catalogs that still
use it.

Diagnostic signature: multiple unrelated source failures starting at
the same time, aligned with a Sharko upgrade rollout.

Fix is Mitigation step 4 — roll back or pin source versions, and ship
catalog updates upstream coordinated with Sharko schema changes.

### Source author shipped an empty release / mid-deploy state

The fetcher hit the source URL during the source's own deployment —
the file was being rewritten and was momentarily empty or partial.
The next fetch (per cadence) succeeds.

Diagnostic signature: source recovers without operator action; the
`status: "failed"` flips back to `status: "healthy"` on the next
fetch cycle. If it persists, this is not the cause.

Fix: no action — the fetcher self-recovers.

---

## Prevention

- **Monitoring — per-source status duration alert.** A V2-3.x
  follow-up metric `sharko_catalog_source_failed_seconds{source_fp}`
  would alert when a source has been in `failed` state for >24h
  (signaling the source author hasn't pushed a fix and you need to
  notify them). Today, monitoring is via the
  `/api/v1/catalog/sources` poll loop.

- **Gating — source author CI catches schema violations upstream.**
  The standard pattern is: source authors run `sharko validate-config
  catalog.yaml` in their own CI before publishing. The validator
  catches the same schema errors the fetcher would, before the
  release lands. Publish the validator-as-a-container image so source
  authors can use it independently of their Sharko deployment.

- **Documentation — source-author contract page.** Link
  source authors to
  [`catalog-sources.md`](catalog-sources.md) which documents the
  schema, the supported enum sets, and the validator-in-CI pattern.
  Schema changes between Sharko minors are called out in
  release-notes — source authors should subscribe to releases.

- **Scheduled work — quarterly schema compatibility review.** When
  the Sharko team plans a schema change, they MUST diff
  `allowedCategories` / `allowedCuratedBy` against the known
  third-party catalog ecosystem before merging. Reduce the
  third-party-breakage blast radius on schema tightening.

- **Failover — keep the prior snapshot retention behavior.** The
  current fetcher retains the prior successful entries on schema
  failure (per `recordSchemaFailure`). This is the right product
  decision and should be preserved across refactors — operators
  should not lose access to working entries just because the source
  author shipped a bad release.

---

## Related runbooks

- [`catalog-source-http-fetch-failed.md`](catalog-source-http-fetch-failed.md)
  — adjacent fetcher failure mode: HTTP fetch failed (network,
  TLS, 5xx) before schema validation even ran.
- [`catalog-parse-failure-on-startup.md`](catalog-parse-failure-on-startup.md)
  — sibling failure on the EMBEDDED catalog loader. Treat as P1
  escalating to P0 because embedded failure means no addons surface
  at all.
- [`catalog-trust-policy.md`](catalog-trust-policy.md) — adjacent
  source-level failure: schema valid but signature verification
  failed.
- [`catalog-trust-root-unavailable.md`](catalog-trust-root-unavailable.md)
  — fleet-wide P0 case where the entire signing infrastructure is
  unreachable.
- [`catalog-sources.md`](catalog-sources.md) — configuration
  reference for `SHARKO_CATALOG_URLS` / `catalog.sources` Helm
  values, source-author conventions.
- [`failure-mode-index.md`](failure-mode-index.md) — master inventory.
- [`../developer-guide/logging.md`](../developer-guide/logging.md#correlation-ids)
  — request_id correlation; useful when correlating fetcher cycles
  to specific log lines.

## Escalation

If multiple sources are persistently failing AND Diagnosis step 3
indicates Sharko-side schema drift, email the maintainer:
`moran.weissman@gmail.com`. Include:

- This runbook URL
- The list of failing source URLs and their `last_error` values
- The Sharko version and the previous version (if a rollback was
  attempted)
- Whether each source has shipped a release in the last 30 days

For single-source failures where the source author is responsive,
escalation isn't needed — the source author owns the fix.

The maintainer is a single human, not a 24×7 rotation.

<!-- Style-guide compliance checklist (V2-4.1):
- [x] Title matches `# <Failure name>`
- [x] Severity line present (P1)
- [x] Verified-by-execution header + date current
- [x] Symptoms section appears BEFORE Diagnosis
- [x] Symptoms include exact log lines / error messages
- [x] Diagnosis has 3+ concrete checks (3 named) with exact commands
- [x] Mitigation uses numbered list (1. 2. 3. 4. 5.) not bullets
- [x] Mitigation has 3-5 steps in priority order, each with rationale + exact command
- [x] Root-cause patterns section: 2+ named causes (4 named), 1-3 paragraphs each
- [x] Prevention section present and non-empty (NOT "TBD")
- [x] Related runbooks section present with multiple links
- [x] Intro is operator-on-call voice
- [x] Length within 300-800 line target
- [x] All cross-links resolve via mkdocs --strict
- [x] No emoji / no internal Slack / employee email
- [x] No Prom alert applicable today (per-source failure)
-->
