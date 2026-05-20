# Third-party Catalog Source Smoke Runbook

Operator-facing smoke procedure for the third-party catalog fetcher
(shipped in **v1.23** under `SHARKO_CATALOG_URLS`, hardened across
V123-1.x and V123-2.x). Walk this once on a fresh Sharko deployment
when you first turn the feature on — it confirms that the fetcher
runs, the API surfaces your source, and the Browse UI renders the
merged entries with the correct source badges.

It is **not** a continuous monitoring procedure — the Prometheus
metrics emitted by the fetcher (`sharko_catalog_source_fetch_total`,
`sharko_catalog_source_last_success_timestamp`,
`sharko_catalog_source_entries`) are the right surface for ongoing
operational alerting. This page is the one-time "did I wire it up
right?" check.

If you have not configured the env vars yet, read
[Catalog Sources](catalog-sources.md) first for the env-var
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
[catalog scan runbook](../developer-guide/catalog-scan-runbook.md) for
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
    tokens — see the [Catalog Sources](catalog-sources.md) page).
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
a single `WARN` line at component `catalog-sources` with a 10-character
**source fingerprint** instead of the URL:

```
level=WARN msg="catalog source fetch failed" component=catalog-sources source_fp=a1b2c3d4e5 err="http 404"
level=WARN msg="catalog source schema validation failed" component=catalog-sources source_fp=a1b2c3d4e5 err="schema validation: ..."
level=WARN msg="catalog source blocked by runtime SSRF guard" component=catalog-sources source_fp=a1b2c3d4e5 err="resolves to private address 10.0.0.5"
```

The fingerprint is a 10-character prefix of `SHA-256(url)`. It is
stable across restarts for the same URL, so you can correlate a WARN
line to a specific entry in your `SHARKO_CATALOG_URLS` list by
hashing the URL yourself:

```bash
printf '%s' "https://gist.githubusercontent.com/youruser/<gist-id>/raw/catalog.yaml" | shasum -a 256 | head -c 10
```

If you see a `WARN` line and the API response (Step 4) reports
`status: "failed"`, the entry under that fingerprint is the
problem. The most common causes are `http 404` (URL typo or the
gist/asset went away), `http 403` (the URL requires auth Sharko
isn't sending), `schema validation: ...` (the YAML is malformed
or violates the addon schema), and the SSRF block when a hostname
resolves to a private IP — set
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
first, then one element per configured URL, sorted by URL:

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
    "url": "https://gist.githubusercontent.com/youruser/<gist-id>/raw/catalog.yaml",
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
| `url` | string | Either the literal `"embedded"` (binary-shipped catalog, always first) or the third-party URL verbatim. |
| `status` | string | `"ok"` = most recent fetch parsed cleanly. `"stale"` = most recent fetch failed but a previous one succeeded; entries are last-known-good. `"failed"` = fresh-start failure or schema violation; entries may be empty. Always `"ok"` for the embedded row. |
| `last_fetched` | string \| null | RFC3339 timestamp of the most recent **successful** fetch (not the most recent attempt). `null` when never succeeded. Always `null` for the embedded row. |
| `entry_count` | integer | Number of addon entries this source contributes to the merged catalog. |
| `verified` | boolean | Whether the source's sidecar signature passed trust-policy verification. Always `true` for the embedded row (binary trusts itself). For third-party rows this is `false` unless a `.bundle` sidecar exists at `<url>.bundle` and the signing identity matches the trust policy — see [Catalog Trust Policy](catalog-trust-policy.md). |
| `issuer` | string (optional) | Human-readable OIDC subject of the signer when `verified: true`. Omitted when empty. |

**Success means:**

- Your third-party URL appears as an array element.
- `status` is `"ok"`.
- `last_fetched` is a recent RFC3339 timestamp.
- `entry_count` matches the number of `addons:` entries in your
  YAML file.

If `status` is `"failed"`, scroll back through the pod log for the
matching `source_fp` (Step 3) and read the `err` value.

## Step 5 — Verify the Browse UI

Open the Sharko UI as the admin user, navigate to **Browse**, and
confirm:

- The addons from your third-party catalog appear alongside the
  embedded ones.
- Each addon tile shows a **source badge** indicating where the
  entry came from — "Embedded" vs the third-party source label.
  Hover the badge to see the source URL.
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
audit detail records the list of attempted URLs and per-URL status.

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

- **Signature verification** — covered by [Catalog Trust Policy](catalog-trust-policy.md).
  To produce a `verified: true` third-party row you need a
  `.bundle` sidecar next to your catalog YAML and the signing
  identity in `SHARKO_CATALOG_TRUSTED_IDENTITIES`. Signing the
  smoke YAML is out of scope for "did the fetcher wire up?".
- **Multi-source merge ordering** — covered by the design notes in
  [Catalog Sources](catalog-sources.md). The embedded entry always
  wins on a name collision; this runbook uses a unique entry id
  (`smoke-test-addon`) to keep the smoke pass independent of merge
  semantics.
- **Continuous monitoring** — use the Prometheus metrics
  (`sharko_catalog_source_fetch_total{status="failed"}` rate,
  `sharko_catalog_source_last_success_timestamp` freshness) once
  the smoke pass is green. The metrics are the operational pulse
  the API endpoint cannot match in latency or aggregation.
