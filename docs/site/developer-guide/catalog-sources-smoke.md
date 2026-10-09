# Third-party Catalog Source Smoke Runbook

> **Verified:** Updated 2026-08-25 — the failed-fetch example log lines
> in Step 3 now show what the log pipeline actually writes into the
> `err` field (a fixed type-derived label, never the error's own words);
> verified on that date by rendering real fetch and schema failures
> through the production log handler (`logging.NewHandler`) and by
> running `sharko validate-catalog` on a schema-invalid body. The rest
> of the walk is as originally authored.

Operator-facing smoke procedure for the third-party catalog fetcher
configured via `SHARKO_CATALOG_URLS`. Walk this once on a fresh Sharko
deployment when you first turn the feature on — it confirms that the
fetcher runs, the API surfaces your source, and the Browse UI renders
the merged entries with the correct source badges.

It is **not** a continuous monitoring procedure — the Prometheus
metrics emitted by the fetcher (`sharko_catalog_source_fetch_total`,
`sharko_catalog_source_last_success_timestamp`,
`sharko_catalog_source_entries`) are the right surface for ongoing
operational alerting. This page is the one-time "did I wire it up
right?" check.

If you have not configured the env vars yet, read
[Catalog Sources](../operator/catalog-sources.md) first for the env-var
reference, the HTTPS-only rule, and the SSRF guard.

## What you need

- A running Sharko deployment with admin credentials (the standard
  `/api/v1/auth/login` flow).
- Permission to set environment variables on the deployment (the
  Helm `env:` block under `values.yaml`, or whichever wrapper
  manages the pod env).
- A place to host the catalog YAML over HTTPS. The two
  easiest-to-stand-up options are:
    - **GitHub Gist** — raw URL like
      `https://gist.githubusercontent.com/<youruser>/<gist-id>/raw/catalog.yaml`.
      Public gists work; private gists require an unauth raw URL,
      which only public ones have.
    - **GitHub Release asset** — upload `catalog.yaml` to any
      release in any repo you own, then use the
      `https://github.com/<owner>/<repo>/releases/download/<tag>/catalog.yaml`
      URL.
- `curl` and `jq` on your local machine for the API check.
  Optionally `kubectl` if you want to tail the Sharko pod logs.

## Step 1 — Prepare a tiny third-party catalog YAML

Create a one-entry catalog file. The schema is the same Sharko uses
internally — `catalog.yaml` is a list of `addons:` entries; see the
[catalog scan runbook](catalog-scan-runbook.md) for
the full schema. A minimum smoke entry:

```yaml
addons:
  - id: smoke-test-addon
    name: Smoke Test Addon
    chart: podinfo
    repo: https://stefanprodan.github.io/podinfo
    version: 6.7.1
    namespace: smoke-test
    category: misc
    description: One-entry smoke test for SHARKO_CATALOG_URLS.
```

Pick a real chart (the example uses `podinfo` because it is a
well-known, publicly-hosted Helm chart). The fetcher only validates
the schema — it does not pre-flight that the Helm chart is
installable. That happens later, when an operator actually deploys
the addon.

Upload it. Confirm `curl -fsSL <your-url>` returns the YAML body.
If a `404` comes back here, fix it now — Sharko will record a
`failed` status and you will think the fetcher is broken when it is
the URL that is wrong.

## Step 2 — Point Sharko at the URL

Set the env vars. For a Helm-managed Sharko deployment, edit
`values.yaml`:

```yaml
env:
  - name: SHARKO_CATALOG_URLS
    value: https://gist.githubusercontent.com/youruser/<gist-id>/raw/catalog.yaml
  - name: SHARKO_CATALOG_REFRESH_INTERVAL
    value: "5m"
```

For local Docker / `make demo` testing:

```bash
export SHARKO_CATALOG_URLS=https://gist.githubusercontent.com/youruser/<gist-id>/raw/catalog.yaml
export SHARKO_CATALOG_REFRESH_INTERVAL=5m
```

Re-apply / restart Sharko so the new env reaches the pod.

!!! warning "URLs are not logged"
    Sharko never logs the configured URLs (they may encode auth
    tokens — see the [Catalog Sources](../operator/catalog-sources.md) page).
    Confirmation that the config landed comes from a single startup
    line that reports the **count**, not the URLs, plus the
    `/api/v1/catalog/sources` API response which is what this
    runbook checks.

## Step 3 — Confirm the startup log lines

Tail the Sharko pod log on restart. The runbook uses
`kubectl logs`; substitute your wrapper as needed:

```bash
kubectl logs -n sharko deployment/sharko --tail=200 | grep -i catalog
```

You should see — in order — three lines (counts and durations vary):

```
level=INFO msg="curated catalog loaded" entries=NN
level=INFO msg="third-party catalog sources configured" count=1 refresh_interval=5m0s allow_private=false
level=INFO msg="catalog sources fetcher started" count=1
```

If you instead see `no third-party catalogs configured, using embedded only`,
the env var did not reach the process. Re-check the Helm values, the
`Deployment.spec.template.spec.containers[].env` list, and that you
restarted the pod after the change.

If `SHARKO_CATALOG_URLS_ALLOW_PRIVATE=true` is set, Sharko also logs
a `WARN` line on startup — that is expected on home-lab / dev
deployments and `level=ERROR`-worthy on anything else.

### When a fetch fails

Successful fetches are intentionally silent in the log (the API +
Prometheus metrics are the operational surface). Failed fetches log
a single `WARN` line at component `catalog-sources` naming the source in
a `source` field:

```
level=WARN msg="catalog source fetch failed" component=catalog-sources source=redacted err="unclassified chain=*errors.errorString"
level=WARN msg="catalog source schema validation failed" component=catalog-sources source=redacted err="unclassified chain=*errors.errorString"
level=WARN msg="catalog source blocked by runtime SSRF guard" component=catalog-sources source=redacted err="unclassified chain=..."
```

The `source` field is always the single word `redacted` — never the
address, whatever it looks like, and nothing about the address is
worked into it: no hash of it, no part of it, no hint of how long it
was. That is deliberate: this log has no login in front of it, and a
private catalog is addressed by writing a token into the address's own
path, where nothing can tell it apart from an ordinary path segment —
so even an address that looks clean is treated as a secret. Every
configured source therefore reads `redacted`, and a line cannot tell
you which one it is about. The `err` field is also rewritten before it
is stored: the log pipeline replaces an error's own words with a fixed
description built from its Go types, because the raw words of a fetch
error routinely quote the address.

To work out which source a failure is about, read
`GET /api/v1/catalog/sources` (Step 4), which is behind a login. Its
rows all say `redacted` too, so tell them apart by their `status`,
`entry_count` and `last_fetched` values, and by the order of the list —
the rows follow the order of the configured addresses sorted
alphabetically.

Because the `err` field is a type label, the log does not tell you
WHY the fetch failed — check the URL yourself. The most common causes
are an HTTP 404 (URL typo or the gist/asset went away), an HTTP 403
(the URL requires auth Sharko isn't sending), a schema-invalid body
(the YAML is malformed or violates the addon schema), and the SSRF
block when a hostname resolves to a private IP. To tell them apart:
`curl -sS -o /dev/null -w '%{http_code} %{content_type}\n'` on the
address shows the HTTP status; `sharko validate-catalog` on the
fetched body prints the exact schema complaint (the same loader rules
the fetcher runs); and for the SSRF case, resolve the hostname and
check for a private IP — set
`SHARKO_CATALOG_URLS_ALLOW_PRIVATE=true` on trusted networks if that
is intentional, otherwise fix the URL.

## Step 4 — Verify the API

Hit `GET /api/v1/catalog/sources`. The endpoint requires an admin
bearer token; the standard login flow returns one:

```bash
# Set TOKEN to your admin bearer.
TOKEN=$(curl -fsSL -X POST "$SHARKO_URL/api/v1/auth/login" \
  -H 'Content-Type: application/json' \
  -d "{\"username\":\"admin\",\"password\":\"$ADMIN_PW\"}" | jq -r .token)

curl -fsSL -H "Authorization: Bearer $TOKEN" \
  "$SHARKO_URL/api/v1/catalog/sources" | jq .
```

The expected response shape is a **JSON array** with at least two
elements after you have added a third-party source — `embedded`
first, then one element per configured URL. The configured address
itself never appears; every third-party row's `url` is the fixed word
`redacted`, and the rows follow the configured addresses sorted
alphabetically:

```json
[
  {
    "url": "embedded",
    "status": "ok",
    "last_fetched": null,
    "entry_count": 142,
    "verified": true
  },
  {
    "url": "redacted",
    "status": "ok",
    "last_fetched": "2026-05-20T12:34:56Z",
    "entry_count": 1,
    "verified": false
  }
]
```

Field reference (from `internal/api/catalog_sources.go`):

| Field | Type | Meaning |
|-------|------|---------|
| `url` | string | Either the literal `"embedded"` (binary-shipped catalog, always first) or the fixed word `"redacted"` for every configured third-party source. The configured address never comes back in any form — a private catalog's token can sit in the address's own path, where nothing can tell it apart from an ordinary path segment, so the whole address is treated as a secret. `SHARKO_CATALOG_URLS` exists precisely so tokened/private URLs need not be committed to Git, and this endpoint is readable by any signed-in account including a viewer. Tell rows apart by `status`, `entry_count` and `last_fetched`; the rows follow the configured addresses sorted alphabetically. |
| `status` | string | `"ok"` = most recent fetch parsed cleanly. `"stale"` = most recent fetch failed but a previous one succeeded; entries are last-known-good. `"failed"` = fresh-start failure or schema violation; entries may be empty. Always `"ok"` for the embedded row. |
| `last_fetched` | string \| null | RFC3339 timestamp of the most recent **successful** fetch (not the most recent attempt). `null` when never succeeded. Always `null` for the embedded row. |
| `entry_count` | integer | Number of addon entries this source contributes to the merged catalog. |
| `verified` | boolean | Whether the source's sidecar signature passed trust-policy verification. Always `true` for the embedded row (binary trusts itself). For third-party rows this is `false` unless a `.bundle` sidecar exists at `<url>.bundle` and the signing identity matches the trust policy — see [Catalog Trust Policy](../operator/catalog-trust-policy.md). |
| `issuer` | string (optional) | Human-readable OIDC subject of the signer when `verified: true`. Omitted when empty. |

**Success means:**

- A `"url": "redacted"` element appears for your third-party source.
- `status` is `"ok"`.
- `last_fetched` is a recent RFC3339 timestamp.
- `entry_count` matches the number of `addons:` entries in your
  YAML file.

If `status` is `"failed"`, scroll back through the pod log (Step 3)
and read the `err` value — with one source configured, every
catalog-source `WARN` line is about it.

## Step 5 — Verify the Browse UI

Open the Sharko UI as the admin user, navigate to **Browse**, and
confirm:

- The addons from your third-party catalog appear alongside the
  embedded ones.
- Each addon tile shows a **source badge** indicating where the
  entry came from — "Embedded" vs the third-party source label,
  which always reads `redacted` (the configured address is never
  shown, not even on hover).
- The third-party entries that did **not** carry a valid signature
  bundle show an **Unverified** badge alongside the source badge.
  This is the expected state for a freshly-stood-up smoke source —
  signing is opt-in and you have not signed your gist's catalog
  YAML.

If the UI does not render the third-party entries, hard-refresh the
browser. The Browse view caches the catalog response in-memory for
the page lifetime; a SHARKO restart does not invalidate the
client-side cache.

## Step 6 — Optional — Force-refresh round-trip

The fetcher refreshes on the `SHARKO_CATALOG_REFRESH_INTERVAL`
cadence (default `1h`, minimum `1m`, maximum `24h`). For smoke
purposes you can force an immediate refresh via the admin-only
`POST /api/v1/catalog/sources/refresh` endpoint:

```bash
curl -fsSL -X POST -H "Authorization: Bearer $TOKEN" \
  "$SHARKO_URL/api/v1/catalog/sources/refresh" | jq .
```

The response is the same shape as `GET /catalog/sources`, but built
**after** the refresh completes. Use this to confirm that an
edit-and-republish loop against your gist is picked up without a
full Sharko restart. The endpoint is Tier-2 audit-logged — the
audit detail records how many sources were attempted and a count per
outcome, never the addresses themselves.

## Step 7 — Tear down

Once the smoke pass is green:

- Remove the demo `SHARKO_CATALOG_URLS` value (or replace it with
  your real production source list).
- Restart Sharko.
- Re-hit `GET /api/v1/catalog/sources` and confirm the third-party
  entry is gone and only the `embedded` row remains.

If you want to lean on automation for this whole flow, the
project ships a small validation script that runs Steps 4-6 against
a live Sharko instance:

```bash
SHARKO_URL=http://localhost:8080 \
ADMIN_PW=$(cat ~/.sharko-dev-pw) \
SHARKO_THIRDPARTY_URL='https://gist.githubusercontent.com/youruser/<gist-id>/raw/catalog.yaml' \
./scripts/smoke/third-party-catalog.sh
```

See `scripts/smoke/third-party-catalog.sh --help` for the full env
var list. The script asserts the same response shape and source
state this runbook walks manually, and exits non-zero if any
assertion fails — wire it into a periodic check on a known-good
catalog source if you want a heartbeat.

## What this runbook does **not** cover

- **Signature verification** — covered by [Catalog Trust Policy](../operator/catalog-trust-policy.md).
  To produce a `verified: true` third-party row you need a
  `.bundle` sidecar next to your catalog YAML and the signing
  identity in `SHARKO_CATALOG_TRUSTED_IDENTITIES`. Signing the
  smoke YAML is out of scope for "did the fetcher wire up?".
- **Multi-source merge ordering** — covered by the design notes in
  [Catalog Sources](../operator/catalog-sources.md). The embedded entry always
  wins on a name collision; this runbook uses a unique entry id
  (`smoke-test-addon`) to keep the smoke pass independent of merge
  semantics.
- **Continuous monitoring** — use the Prometheus metrics
  (`sharko_catalog_source_fetch_total{status="failed"}` rate,
  `sharko_catalog_source_last_success_timestamp` freshness) once
  the smoke pass is green. The metrics are the operational pulse
  the API endpoint cannot match in latency or aggregation.
