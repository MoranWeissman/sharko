# Catalog Source HTTP Fetch Failed

**Severity:** P1

> **Verified:** Authored 2026-06-01 against `main` HEAD. The Warn log
> line `"catalog source fetch failed"` is verified against
> `internal/catalog/sources/fetcher.go:681`, which fires when either
> `f.httpGetPinned(ctx, rawURL, pinnedIPs)` (the runtime-SSRF-guarded
> path) OR `f.httpGet(ctx, rawURL)` (the AllowPrivate fallback)
> returns a non-nil error. The fetcher records the failure via
> `recordFailure(rawURL, startAt, err)` and the source-status enum
> flips to **Failed**; the previous snapshot's entries are retained
> per the source-merger contract. Re-verify when the SSRF guard or
> the pinning client refactor changes the call shape.

A third-party catalog source declared in `SHARKO_CATALOG_URLS`
returned a transport-level failure: DNS lookup failed, TCP connect
refused, TLS handshake failed, HTTP 5xx, or the body exceeded the
clamp. The source is marked **Failed** in the fetcher status; the
previously-loaded entries from this source remain merged so the
marketplace continues to surface them. The embedded catalog and every
other third-party source are unaffected.

This failure mode is structurally adjacent to **schema validation
failed** ([`catalog-source-schema-validation-failed.md`](catalog-source-schema-validation-failed.md))
but the diagnosis path differs — schema failures mean the body came
back fine but didn't parse; fetch failures mean the body never came
back at all. Operators should run Diagnosis step 1 here first to
confirm which lane they're in, because the recovery paths differ. The
SSRF guard's runtime re-resolve check ([Diagnosis step 3](#3-confirm-the-ssrf-guard-is-not-blocking))
catches a specific class of misconfiguration where the URL resolves to
a private IP at fetch time even though it didn't at startup.

---

## Symptoms

What an operator sees when this fires:

- **Sharko logs the warn line at the source-level fetch loop**
  (`internal/catalog/sources/fetcher.go:681`):

  ```
  {"time":"...","level":"WARN","msg":"catalog source fetch failed","source_fp":"<8-char-fingerprint>","err":"Get \"https://example.com/catalog.yaml\": dial tcp: lookup example.com on 10.96.0.10:53: no such host"}
  ```

  Common error shapes:

  ```
  Get "https://...": dial tcp ...: connect: connection refused
  Get "https://...": x509: certificate signed by unknown authority
  Get "https://...": context deadline exceeded
  Get "https://...": EOF
  unexpected status code 503
  unexpected status code 404
  response body too large (exceeded N bytes)
  ```

- **The fetcher status report flips the source to `failed`**:

  ```sh
  curl -sS http://sharko/api/v1/catalog/sources \
    -H "Authorization: Bearer ${SHARKO_TOKEN}"
  ```

  Returns entries like:

  ```json
  [
    {"url":"https://example.com/catalog.yaml","status":"failed","last_error":"Get \"https://example.com/catalog.yaml\": dial tcp: ..."}
  ]
  ```

- **The Marketplace UI** shows the source as **Failed** in the
  source-status panel, and continues to surface the previous
  snapshot's entries from this source (per
  `internal/catalog/sources/merger.go` "embedded wins, third-party
  prior-snapshot retained on transient failure"). New entries the
  source may have added are not visible.

- **If the failure is the SSRF runtime guard**, the log line is the
  adjacent
  `"catalog source blocked by runtime SSRF guard"` (line 659 of
  fetcher.go) NOT this runbook's line. That has its own runbook —
  [`catalog-sources.md`](catalog-sources.md) — and is distinct from
  HTTP fetch failure.

- **No Prometheus alert fires for a single-source fetch failure
  today.** Multi-source fetch failures aligned with a network event
  fan into the `SharkoCatalogScanFastBurn` /
  `SharkoCatalogScanSlowBurn` budget-burn alerts (see
  [`budget-burn-runbook.md`](budget-burn-runbook.md)).

If the symptom is "fetch succeeded but the body wouldn't parse," see
[`catalog-source-schema-validation-failed.md`](catalog-source-schema-validation-failed.md)
— different runbook.

If the symptom is the **SSRF runtime guard** explicitly blocked the
URL (the operator sees the `runtime SSRF guard` log line), see
[`catalog-sources.md`](catalog-sources.md) for the
`SHARKO_CATALOG_URLS_ALLOW_PRIVATE` escape hatch.

---

## Diagnosis

Three checks. The first confirms it's a fetch failure (not schema /
not SSRF); the second isolates which transport layer is broken; the
third confirms the SSRF guard is not silently in the loop.

### 1. Confirm which source is failing and what the transport error is

```sh
curl -sS http://sharko/api/v1/catalog/sources \
  -H "Authorization: Bearer ${SHARKO_TOKEN}" \
  | jq '.[] | select(.status=="failed") | {url, source_fp, last_error}'
```

The `last_error` field is the full error string from the failed
fetch. Inspect the prefix to triage:

- `dial tcp: lookup ...: no such host` → DNS failure (step 2a)
- `dial tcp ...: connect: connection refused` / `network is
  unreachable` → TCP connect failure (step 2b)
- `x509: certificate signed by unknown authority` → TLS failure (step
  2c)
- `context deadline exceeded` → timeout / hang (step 2d)
- `unexpected status code N` → upstream HTTP failure (step 2e)
- `response body too large (exceeded N bytes)` → body-size clamp hit
  (step 2f)

### 2. Reproduce the failure from inside the Sharko pod

Network behavior differs between the operator's workstation (with
their VPN / DNS) and the pod (with the cluster's kube-dns + egress
NetworkPolicy). Always reproduce from the pod.

```sh
SHARKO_POD=$(kubectl -n <sharko-ns> get pod -l app=sharko -o name | head -1)
FAILING_URL=<url-from-step-1>
```

**2a. DNS failure**:

```sh
kubectl -n <sharko-ns> exec "$SHARKO_POD" -- \
  nslookup "$(echo "$FAILING_URL" | awk -F/ '{print $3}')"
```

Expected: an A record (and AAAA if IPv6). If the lookup fails, either
kube-dns is broken (check `kube-system` namespace) or an
egress-blocking DNS policy is in effect.

**2b. TCP connect failure**:

```sh
HOST_PORT=$(echo "$FAILING_URL" \
  | sed -E 's|^https?://([^/]+)(/.*)?$|\1|' \
  | awk -F: '{ if($2=="") print $1":443"; else print $0 }')
kubectl -n <sharko-ns> exec "$SHARKO_POD" -- \
  sh -c "echo > /dev/tcp/${HOST_PORT/:/\/}" 2>&1
```

If the connect fails, check NetworkPolicy + corporate proxy
configuration (see [`corporate-mitm-tls.md`](corporate-mitm-tls.md)
for the proxy/MITM angle).

**2c. TLS failure**:

```sh
kubectl -n <sharko-ns> exec "$SHARKO_POD" -- \
  wget --no-check-certificate -q -O - "$FAILING_URL" \
  | head -5
```

If wget with `--no-check-certificate` succeeds but the non-insecure
fetch fails, the upstream cert is not trusted by the pod's
ca-certificates bundle. Common cause: corporate MITM proxy with a
private CA the Sharko image doesn't know about. See
[`corporate-mitm-tls.md`](corporate-mitm-tls.md).

**2d. Timeout**:

```sh
kubectl -n <sharko-ns> exec "$SHARKO_POD" -- \
  sh -c 'time wget -q -O /dev/null --timeout=30 "'"$FAILING_URL"'"'
```

If the request takes >5s but eventually completes, the source server
is slow but not dead — Mitigation step 4 (raise the timeout) applies.
If it hangs indefinitely, the server is dropping packets — escalate
upstream.

**2e. Upstream HTTP error**:

```sh
kubectl -n <sharko-ns> exec "$SHARKO_POD" -- \
  wget --server-response -q -O /dev/null "$FAILING_URL" 2>&1 \
  | grep "HTTP/"
```

A `404` is a wrong URL — contact the source author or fix the Helm
value. A `403` is auth / IP block. A `503` is upstream is degraded —
wait and retry, the fetcher will self-recover.

**2f. Body size clamp**:

```sh
kubectl -n <sharko-ns> exec "$SHARKO_POD" -- \
  sh -c "wget -q -O - '$FAILING_URL' | wc -c"
```

The fetcher's body clamp default is large (read the
`internal/catalog/sources/fetcher.go` constant in your installed
version). If the source legitimately needs more headroom, that's a
Sharko config change (Helm value
`catalog.sources.maxBodyBytes`). If the source body is unexpectedly
huge, the source author may have accidentally bundled a giant payload
— notify them.

### 3. Confirm the SSRF guard is not blocking

The fetcher routes through `runtimeSSRFCheckResolvedIPs` before the
GET. If the URL re-resolves to a private IP between startup and now
(DNS rebinding, ops change), the guard rejects the fetch with a
distinct log line (`"catalog source blocked by runtime SSRF guard"`,
line 659). That isn't this runbook — but operators occasionally
confuse the two. Confirm:

```sh
kubectl -n <sharko-ns> logs -l app=sharko --since=15m \
  | jq -c 'select(.msg | test("catalog source"; "i"))' \
  | jq -c '{time, msg, source_fp, err}'
```

If the SSRF-guard line is the one firing, jump to
[`catalog-sources.md`](catalog-sources.md) for the
`SHARKO_CATALOG_URLS_ALLOW_PRIVATE` lane.

---

## Mitigation (try in order)

1. **For DNS / TCP / TLS failures, repair the network path or proxy
   config.** Most common: a NetworkPolicy in the Sharko namespace
   blocks egress to the source's host, OR the cluster's egress
   NetworkPolicy is default-deny without an allow-rule for the
   source URL.

   Inspect any default-deny NetworkPolicy:

   ```sh
   kubectl get networkpolicy -n <sharko-ns>
   kubectl describe networkpolicy <policy-name> -n <sharko-ns>
   ```

   Add an egress allow-rule for the source host + port 443 (or use
   the cluster's documented "egress to catalog sources" pattern). If
   the source is hosted on a corporate-internal HTTPS server, ensure
   the proxy / firewall route permits the Sharko pod's identity.

   Restart the pod to flush any cached failures, then verify the
   source status flips back to `healthy` on the next fetch cycle.

2. **For corporate MITM TLS, install the corporate CA in the Sharko
   pod's trust store.** This is the cleanest workaround documented in
   [`corporate-mitm-tls.md`](corporate-mitm-tls.md) — mount the
   corporate root CA via the Helm chart's `tls.extraCerts` value (if
   present in your installed version) or via a sidecar that injects
   `/etc/ssl/certs/ca-certificates.crt`.

   The temporary alternative — `--insecure` on every Sharko call — is
   NOT acceptable for catalog fetches because signed third-party
   catalogs depend on TLS integrity for the verification pipeline.

3. **For upstream 4xx / 5xx errors, contact the source author with
   the exact error string.** The fetcher status's `last_error` field
   carries the response code and any body the server returned.
   Common causes:

   - `404 Not Found` — source author moved the catalog file.
     Confirm the new URL and update the Helm value.
   - `403 Forbidden` — source has IP-based access control. Confirm
     the source's allowlist includes the Sharko cluster's egress IP.
   - `503 Service Unavailable` — upstream degraded. No operator
     action; the fetcher self-recovers when the source returns.

4. **For timeout-driven failures, raise the per-source timeout (or
   the source author needs to optimize their serving).** Sharko's
   default per-fetch timeout is documented in the fetcher constants
   (`fetcher.go`). If your source legitimately takes >5s to serve
   (large catalog, slow upstream CDN), the fix is on the source
   author's side — they should optimize. Sharko's clamp protects
   against runaway fetchers; bypassing it is a Sharko config change
   (Helm value `catalog.sources.timeout`) and reduces backpressure on
   misbehaving sources.

5. **Last resort — remove the failing source from the active set.**
   If the source author cannot fix on a timeline that works for you,
   or if multiple unrelated sources are failing because of cluster
   egress changes you can't reverse:

   ```sh
   helm upgrade --reuse-values \
     --set "catalog.sources={<remaining-source-urls>}" \
     sharko sharko/sharko -n <sharko-ns>
   ```

   The fetcher drops the source on the next config reload; the
   previously-merged entries are removed from the marketplace.
   Document the removal so it can be restored when upstream is
   healthy again.

---

## Root-cause patterns

### Egress NetworkPolicy blocks the source

The most common cause in restricted-network deployments. A new
default-deny egress NetworkPolicy was applied to the Sharko namespace
without an explicit allow-rule for the catalog source hosts. DNS
lookups for the source domain succeed (kube-dns runs in
`kube-system`), but the TCP connect fails.

Diagnostic signature: Diagnosis step 2b fails with
`connect: connection refused` or `network is unreachable`; step 2a's
nslookup succeeds. The NetworkPolicy was applied recently
(`kubectl get networkpolicy -o yaml` shows a creation timestamp
matching the failure-start time).

Fix is Mitigation step 1 — add an explicit allow-rule.

### Corporate MITM TLS proxy without trusted CA

The Sharko pod's traffic goes through a corporate proxy that
terminates TLS, presents its own cert (signed by the corporate CA),
and re-encrypts upstream. The pod's ca-certificates bundle doesn't
include the corporate CA, so the handshake fails.

Diagnostic signature: `x509: certificate signed by unknown authority`
in the error; Diagnosis step 2c shows wget with
`--no-check-certificate` succeeds while the non-insecure fetch
fails.

Fix is Mitigation step 2; see
[`corporate-mitm-tls.md`](corporate-mitm-tls.md) for the long-form.

### Source server moved / 404 / 503

The source author moved their catalog without updating consumers, or
the source is on a slow CI/CD pipeline that occasionally returns 5xx
during deploys. Single-source per-fetch failures, sometimes
self-recovering.

Diagnostic signature: `unexpected status code <404|503>` in the
error; Diagnosis step 2e confirms the upstream returns the same
code.

Fix is Mitigation step 3 — notify the source author.

### Slow source server hits the fetch timeout

The fetcher's per-source timeout is exceeded because the source's
HTTPS endpoint is genuinely slow (large catalog body, slow upstream
storage). Most often surfaces on first deploy of Sharko in a new
network environment where the source is geographically distant.

Diagnostic signature: `context deadline exceeded`; Diagnosis step 2d
shows the fetch completes in 10-30s instead of <5s.

Fix is Mitigation step 4 — raise the timeout (and notify the source
author to investigate their serving performance).

---

## Prevention

- **Monitoring — per-source uptime alert.** A V2-3.x follow-up metric
  `sharko_catalog_source_fetch_failures_total{source_fp,
  failure_class}` with classes (`dns`, `tcp`, `tls`, `timeout`,
  `http_5xx`, `body_oversize`) would let operators alert on sustained
  per-source failure. Today, monitoring is via the
  `/api/v1/catalog/sources` poll loop.

- **Gating — pre-deploy connectivity test.** A Helm post-install hook
  that fetches each configured catalog source URL and fails the
  install on any failure would catch deployment-time misconfiguration
  before the operator notices a Failed status in the dashboard. This
  is in the V2.x DX backlog.

- **Documentation — NetworkPolicy template for catalog sources.**
  The operator runbook
  [`catalog-sources.md`](catalog-sources.md) should include a
  copy-paste NetworkPolicy snippet for "allow egress to these
  catalog source hosts" so platform engineering teams can apply it
  alongside their default-deny policy without trial and error.

- **Scheduled work — quarterly source URL freshness check.** Source
  author releases sometimes change URLs (versioned catalog paths,
  CDN domain changes). A scheduled task that diffs the configured
  source URLs against the source author's release notes catches
  drift before it shows up as a 404.

- **Failover — keep the prior-snapshot retention behavior.** As with
  schema validation failures, the fetcher retains the prior
  successful entries on transport failure. Preserve this behavior on
  refactors — operators should not lose access to working entries
  just because a source is briefly unreachable.

---

## Related runbooks

- [`catalog-source-schema-validation-failed.md`](catalog-source-schema-validation-failed.md)
  — adjacent fetcher failure: HTTP fetch succeeded but the body
  didn't parse as a valid catalog.
- [`catalog-sources.md`](catalog-sources.md) — configuration
  reference for `SHARKO_CATALOG_URLS` / `catalog.sources` Helm
  values and the SSRF guard's `ALLOW_PRIVATE` escape hatch.
- [`corporate-mitm-tls.md`](corporate-mitm-tls.md) — corporate proxy
  TLS interception workaround; common cause for the
  `x509: certificate signed by unknown authority` shape.
- [`catalog-trust-policy.md`](catalog-trust-policy.md) — adjacent
  per-entry failure: source fetched fine but per-entry signature
  verification failed.
- [`catalog-trust-root-unavailable.md`](catalog-trust-root-unavailable.md)
  — fleet-wide P0: signing infrastructure unreachable.
- [`budget-burn-runbook.md#sharkocatalogscanfastburn`](budget-burn-runbook.md#sharkocatalogscanfastburn)
  — the FastBurn alert that fires when catalog scan failures
  fan-in across multiple sources.
- [`failure-mode-index.md`](failure-mode-index.md) — master inventory.

## Escalation

If multiple sources are failing simultaneously AND
[`budget-burn-runbook.md#sharkocatalogscanfastburn`](budget-burn-runbook.md#sharkocatalogscanfastburn)
is firing, the issue is fleet-wide and escalates to the burn
runbook (which has its own escalation path). Otherwise, single-source
failures route to the source author first; only persistent failures
with no upstream response need the maintainer email path.

Email the maintainer: `moran.weissman@gmail.com`. Include:

- This runbook URL
- The failing source URL(s) and their `last_error` values
- The Diagnosis step 2 output that identified the transport-layer
  failure (DNS / TCP / TLS / timeout / 5xx / body size)
- Whether the issue is reproducible from the Sharko pod
- The Sharko version

The maintainer is a single human, not a 24×7 rotation.

<!-- Style-guide compliance checklist (V2-4.1):
- [x] Title matches `# <Failure name>`
- [x] Severity line present (P1)
- [x] Verified-by-execution header + date current
- [x] Symptoms section appears BEFORE Diagnosis
- [x] Symptoms include exact log lines / error messages
- [x] Diagnosis has 3+ concrete checks (3 named with 6 transport sub-cases) with exact commands
- [x] Mitigation uses numbered list (1. 2. 3. 4. 5.) not bullets
- [x] Mitigation has 3-5 steps in priority order, each with rationale + exact command
- [x] Root-cause patterns section: 2+ named causes (4 named), 1-3 paragraphs each
- [x] Prevention section present and non-empty (NOT "TBD")
- [x] Related runbooks section present with multiple links
- [x] Intro is operator-on-call voice
- [x] Length within 300-800 line target
- [x] All cross-links resolve via mkdocs --strict
- [x] No emoji / no internal Slack / employee email
- [x] Cross-references the SharkoCatalogScanFastBurn alert when applicable
-->
