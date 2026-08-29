# Catalog Signing Trust Root Unavailable

**Severity:** P0

> **Verified:** Authored 2026-06-01 against Sharko as shipped. Sharko
> loads the Sigstore trust root by fetching the `trusted_root.json` target
> over TUF; `"parse trusted_root.json"` is the exact parse-error string it
> reports. Trust-policy semantics are on
> [`catalog-trust-policy.md`](catalog-trust-policy.md).
> Reviewed 2026-08-29 — wording only; no step in this runbook changed.

Sharko cannot load Sigstore's `trusted_root.json` from the public-good
TUF infrastructure. Every catalog entry that depends on signature
verification — every cosign-keyless-signed third-party Helm chart in
the marketplace — fails verification and surfaces as **Unverified** in
the UI. Operators who configured a trust policy expecting verified
sources see every entry as Unverified; they cannot distinguish a
genuine signature failure (someone tampered with a chart) from a
trust-root infrastructure failure.

This is P0 because the user-visible state is **wrong**: the marketplace
is rendering "no entries can be trusted" when in fact the verification
pipeline is broken upstream of any specific entry. The operator's trust
decisions are based on bad data, and an operator paging on "the entire
marketplace is unverified" will burn out fast unless this runbook
routes them to the actual cause.

This is **not** a per-entry signature failure (those are P1; see
[`catalog-trust-policy.md`](catalog-trust-policy.md) for the
per-entry runbook). This is the case where the **root of trust itself**
is unreachable — Sigstore's TUF metadata can't be fetched, or the
trusted-root target itself is unparseable.

---

## Symptoms

What an operator sees when this fires:

- **Every catalog entry in the Marketplace UI shows "Unverified" badge**
  when the operator's trust policy expects them to be verified.
  Single-entry failures don't fit this symptom — fleet-wide
  Unverified is the signal.
- **Sharko logs at startup show a TUF fetch failure**:

  ```
  {"time":"...","level":"ERROR","msg":"catalog signing: trusted_root.json load failed","error":"tuf GetTarget(trusted_root.json): ..."}
  ```

  OR the parse error variant:

  ```
  {"time":"...","level":"ERROR","msg":"catalog signing: trusted_root.json load failed","error":"parse trusted_root.json: ..."}
  ```

- **`GET /api/v1/catalog/entries`** (or whatever the marketplace
  read endpoint is) returns entries with `verified: false` and a
  `signature_error` field containing strings like:
  ```
  "trusted material unavailable"
  "no trust root configured"
  "verifier not initialized"
  ```

- **No specific Prometheus alert today.** Detection is via UI signal
  ("everything is unverified") + log grep. Adding a startup-failure
  metric is in Prevention.

If the symptom is "**one** catalog entry is unverified" while others
are verified, this is **not** the runbook — that's the per-entry
signature failure case covered in
[`catalog-trust-policy.md`](catalog-trust-policy.md). The single-page
test: are ALL signed entries failing, or just one?

---

## Diagnosis

Three checks. Each narrows whether the failure is upstream (Sigstore
TUF down), network (Sharko can't reach the TUF URL), or local (the
fetched trusted root won't parse).

### 1. Confirm the failure is fleet-wide (not per-entry)

Compare verified counts to total counts:

```sh
curl -sS http://sharko/api/v1/catalog/entries \
  -H "Authorization: Bearer ${SHARKO_TOKEN}" \
  | jq '{
      total: (. | length),
      verified: ([.[] | select(.verified == true)] | length),
      unverified: ([.[] | select(.verified == false)] | length)
    }'
```

Expected on a healthy fleet: `verified` count > 0, roughly matching the
count of signed entries. Bypass signal: `verified: 0` with
`unverified` equal to the total signed count.

If a partial mix is present (some verified, some not), the failure is
per-entry; this is not the runbook.

### 2. Confirm the TUF infrastructure is reachable

The Sigstore public-good TUF lives at
`https://tuf-repo-cdn.sigstore.dev`. Probe from the Sharko pod:

```sh
SHARKO_NS=<sharko-ns>
SHARKO_POD=$(kubectl -n "$SHARKO_NS" get pod -l app=sharko -o name | head -1)

kubectl -n "$SHARKO_NS" exec "$SHARKO_POD" -- \
  wget -q -O /dev/null --no-check-certificate \
  "https://tuf-repo-cdn.sigstore.dev/1.root.json" \
  && echo "TUF reachable" \
  || echo "TUF UNREACHABLE"
```

Three possible outcomes:

- **"TUF reachable"** — network and TLS both work. The failure is
  local (parse error) or auth (corporate proxy stripping headers).
  Jump to step 3.
- **"TUF UNREACHABLE"** with timeout — egress NetworkPolicy blocks the
  TUF CDN. Jump to Mitigation step 2.
- **TLS handshake error / x509: unknown authority** — corporate
  MITM TLS interception. See
  [`corporate-mitm-tls.md`](corporate-mitm-tls.md) for the corporate
  CA setup.

Cross-reference Sigstore's status page
(`https://status.sigstore.dev/`) for active TUF infrastructure
incidents. They are rare but do happen; verify before assuming the
failure is local.

### 3. Inspect the startup log for the parse failure

```sh
kubectl -n "$SHARKO_NS" logs -l app=sharko --tail=10000 \
  | jq -c 'select(.msg | test("trusted_root|catalog signing"; "i"))' \
  | head -10
```

Two log shapes:

- `"tuf GetTarget(trusted_root.json): ..."` — the TUF fetch itself
  failed. Network or service issue. Jump to Mitigation step 2.
- `"parse trusted_root.json: ..."` — the fetch succeeded but the
  body is malformed JSON. Either Sigstore shipped a bad metadata
  version (very rare), or a corporate proxy is rewriting the JSON
  body. Jump to Mitigation step 3.

### 4. Check the startup log for the verifier initialization

```sh
kubectl -n "$SHARKO_NS" logs -l app=sharko --tail=10000 \
  | jq -c 'select(.msg | test("verifier|signing.*init|signing.*ready"; "i"))' \
  | head -10
```

Expected on healthy: a line like `"catalog signing verifier ready"`
emitted once at startup. Absence indicates the verifier never
initialized — the trusted-root failure cascaded into the verifier
init being skipped, so every subsequent verify is a no-op (and
defaults to Unverified).

---

## Mitigation (try in order)

1. **Restart Sharko.** The trusted-root fetch happens at process
   start; if the failure was a transient network blip, a fresh
   start retries cleanly.

   ```sh
   kubectl -n "$SHARKO_NS" rollout restart deployment/sharko
   kubectl -n "$SHARKO_NS" rollout status deployment/sharko --timeout=120s
   ```

   Then verify the marketplace:

   ```sh
   curl -sS http://sharko/api/v1/catalog/entries \
     -H "Authorization: Bearer ${SHARKO_TOKEN}" \
     | jq '[.[] | select(.verified == true)] | length'
   ```

   Success indicator: non-zero `verified` count.

2. **Repair the egress path to Sigstore.** If Diagnosis step 2
   showed TUF unreachable, allow egress in NetworkPolicy:

   ```yaml
   apiVersion: networking.k8s.io/v1
   kind: NetworkPolicy
   metadata:
     name: allow-sharko-egress-to-sigstore
     namespace: <sharko-ns>
   spec:
     podSelector:
       matchLabels:
         app: sharko
     policyTypes:
       - Egress
     egress:
       - to:
           - ipBlock:
               cidr: 0.0.0.0/0
         ports:
           - protocol: TCP
             port: 443
   ```

   For more-restrictive environments, the Sigstore endpoints to allow
   are:
   - `tuf-repo-cdn.sigstore.dev` (TUF metadata + targets CDN)
   - `rekor.sigstore.dev` (transparency log queries during verify)
   - `fulcio.sigstore.dev` (cert chain validation)

   Apply and restart Sharko.

3. **If the TUF fetch returns a malformed body, you cannot point Sharko
   at a trusted root on disk.** Sharko has no such override. The only
   Sigstore path setting it reads is `SHARKO_SIGSTORE_TUF_CACHE`
   (`internal/catalog/signing/tufroot.go`, default `/tmp/sigstore-tuf`),
   and that names the directory TUF *caches into* — it does not bypass TUF
   and it will not accept a hand-placed `trusted_root.json` as a substitute
   for a TUF fetch.

   Earlier versions of this page told you to set `SHARKO_TRUSTED_ROOT_PATH`
   to a mounted file. **Do not do that.** Sharko has never read that name,
   and from this release a `SHARKO_` name Sharko does not recognise stops
   the server at startup — so the command that was supposed to get you past
   an unreachable TUF root now stops Sharko from starting at all.

   What the cache setting IS good for: giving the cache a persistent,
   writable home so a pod restart does not force a fresh network fetch,
   and so a read-only root filesystem does not break the default
   `/tmp/sigstore-tuf` path.

   ```sh
   kubectl -n "$SHARKO_NS" set env deployment/sharko \
     SHARKO_SIGSTORE_TUF_CACHE=/var/lib/sharko/sigstore
   ```

   If the cache already holds a good, unexpired fetch, keeping that
   directory across restarts buys you time while the upstream problem is
   fixed. If it does not, go to mitigation 4 — turning signature
   enforcement off temporarily is the supported way through, and it is
   honest about what it costs.

4. **Mitigate user-visible state by disabling signature enforcement
   temporarily.** If verification can't be restored quickly, the
   operator can choose to allow Unverified entries to be used:

   ```sh
   # Adjust the trust-policy regex to be permissive (NOT RECOMMENDED
   # for production; document the rationale):
   helm upgrade --reuse-values \
     --set catalog.trustPolicy.mode=permissive \
     sharko sharko/sharko -n "$SHARKO_NS"
   ```

   This makes every catalog entry usable regardless of signature
   state. **Security regression.** Acceptable only as a short-term
   bridge while step 3 is being prepared. Re-tighten the trust policy
   the moment the trusted root is restored.

5. **Last resort — corporate proxy CA installation.** If Diagnosis
   step 2 showed an x509-unknown-authority error (TLS interception),
   install the corporate CA cert into Sharko's trust store per
   [`corporate-mitm-tls.md`](corporate-mitm-tls.md). The TUF client
   then accepts the proxy's certificate and the fetch succeeds.

---

## Root-cause patterns

### Sigstore TUF infrastructure outage

The Sigstore public-good TUF (`tuf-repo-cdn.sigstore.dev`) is
unreachable or returning 5xx. Rare but possible — Sigstore is a CNCF
project, not an AWS-grade SLA service.

Diagnostic signature: Diagnosis step 2's wget returns timeout or 5xx.
Sigstore's status page lists an active incident. Other Sigstore-
dependent projects (cosign verify, in-toto attestations) are also
failing globally.

Fix is to wait it out, using a persistent TUF cache (Mitigation step 3)
as a bridge if one is already warm, or turning enforcement off
temporarily (Mitigation step 4) if it is not.

### Corporate proxy / NetworkPolicy egress block

Egress to `tuf-repo-cdn.sigstore.dev` is blocked. Common in restrictive
enterprise environments where outbound HTTPS to public CDNs requires
explicit allow-listing.

Diagnostic signature: Diagnosis step 2's wget returns timeout or
"connection refused"; `nslookup` from the pod resolves the hostname
correctly. The egress firewall logs (if accessible) show a denied
connection.

Fix is Mitigation step 2 (NetworkPolicy allow) or the corporate
firewall allow-list entry. Document in the install procedure.

### Corporate TLS MITM without CA installed

A corporate egress proxy intercepts TLS to inspect traffic. The
proxy re-signs certificates with its own CA, which Sharko doesn't
trust. The TUF fetch fails with `x509: unknown authority`.

Diagnostic signature: Diagnosis step 2's wget fails with x509 /
certificate error; Sigstore's status page shows no incidents; the
operator confirms a corporate proxy is in place.

Fix is to install the corporate CA per
[`corporate-mitm-tls.md`](corporate-mitm-tls.md). One-time setup;
once the CA is in the trust store, the fetch succeeds.

### Malformed trusted root from a CDN cache

Rare: the CDN returned a corrupted or partial body for
`trusted_root.json`. Sharko's parse fails. A retry from a different
edge node may succeed.

Diagnostic signature: Diagnosis step 3 shows
`"parse trusted_root.json: ..."`. The wget probe in step 2 either
succeeds (returning garbled bytes) or returns 200 with a non-JSON
body.

Fix is a Sharko restart (Mitigation step 1). The TUF client retries on
restart and typically lands on a healthy edge. If the malformed
response is consistent across restarts, a persistent TUF cache
(Mitigation step 3) keeps the last good fetch alive across that
restart; if there is no good fetch to keep, go to Mitigation step 4.

---

## Rollback plan

Mitigation steps 1, 2, 5 are non-destructive (restart, NetworkPolicy
update, CA install).

For Mitigation step 3 (persistent TUF cache directory):

1. To go back to the stock cache location once Sigstore is healthy:
   ```sh
   kubectl -n "$SHARKO_NS" set env deployment/sharko \
     SHARKO_SIGSTORE_TUF_CACHE-
   kubectl -n "$SHARKO_NS" rollout restart deployment/sharko
   ```

   Keeping the mounted cache is fine too — it is a cache location, not a
   trust decision.

2. If someone set `SHARKO_TRUSTED_ROOT_PATH` on the deployment before
   reading this, remove it — otherwise the server will refuse to start:

   ```sh
   kubectl -n "$SHARKO_NS" set env deployment/sharko SHARKO_TRUSTED_ROOT_PATH-
   kubectl -n "$SHARKO_NS" rollout restart deployment/sharko
   ```

3. Verify the TUF path works (Diagnosis step 2 wget returns 200).

For Mitigation step 4 (trust-policy permissive mode):

1. Restore strict trust-policy:
   ```sh
   helm upgrade --reuse-values \
     --set catalog.trustPolicy.mode=strict \
     sharko sharko/sharko -n "$SHARKO_NS"
   ```

2. Verify marketplace shows verified entries again.

3. Audit the catalog for any entries that were used during the
   permissive window — they were not signature-validated and should
   be re-validated against the now-strict policy.

---

## Prevention

- **Monitoring — startup failure metric.** Sharko does not export this
  metric today. The alert below is a design sketch for a future release,
  not something you can deploy now. The sketch: register
  `sharko_catalog_verifier_initialized{result="success|failed"}` as a
  Counter incremented once at startup, and alert when the failed-bucket
  count is non-zero. Nothing in `internal/catalog/signing/` registers or
  writes it, so the only startup signal available right now is the log
  line.

- **Monitoring — TUF fetch reachability.** Sharko does not export this
  metric today. The alert below is a design sketch for a future release,
  not something you can deploy now. The sketch: a periodic background
  check that re-validates TUF reachability and exposes a gauge —

  ```
  sharko_sigstore_tuf_reachable == 0
  ```

  — alerting when it sits at 0 for more than 5 minutes. It would catch
  the slow-degradation case where TUF starts failing mid-runtime (rare,
  but the verifier caches the trusted root and won't re-fetch until
  restart).

- **Gating — a trusted root that does not come from TUF. NOT BUILT.**
  Sharko today can only get its trusted root from TUF; there is no code
  path that reads one from disk. Shipping the chart with a bundled
  `trusted_root.json` would make a TUF outage irrelevant, but it needs a
  product change first, not a Helm value. Recorded here as the fix worth
  having, explicitly not as something an operator can do now.

- **Scheduled work — quarterly TUF-cache drill.** Once per quarter,
  clear `SHARKO_SIGSTORE_TUF_CACHE`, restart, and confirm a cold fetch
  still validates a known-signed catalog entry. Catches an egress rule
  or a CA change that only bites on a cold start, before a real Sigstore
  rotation forces one.

---

## Related runbooks

- [`catalog-trust-policy.md`](catalog-trust-policy.md) — per-entry
  signature failures (P1). Covers cert-chain semantics, trust-policy
  regex, workflow_ref assertions.
- [`catalog-sources.md`](catalog-sources.md) — third-party catalog
  source configuration. The trust root applies to entries from
  these sources.
- [`corporate-mitm-tls.md`](corporate-mitm-tls.md) — corporate proxy
  TLS interception. Often the root cause of TUF reachability failures
  in restricted environments.
- [`failure-mode-index.md`](failure-mode-index.md) — master inventory.
- [`../developer-guide/logging.md`](../developer-guide/logging.md) —
  log-grep correlation patterns.

## Escalation

If Mitigation steps 1-3 do not restore verifier initialization within
30 minutes — or if the trusted-root parse failure persists across
restarts — email the maintainer: `moran.weissman@gmail.com`. Include:

- This runbook URL
- The output of Diagnosis steps 2 and 3
- The Sigstore status-page snapshot (if a public incident is in
  progress)
- The Sharko version
- The marketplace verified-count snapshot from Diagnosis step 1
- Whether you applied Mitigation step 4 (permissive trust policy)
  and the rationale

The maintainer is a single human, not a 24×7 rotation. Catalog
trust failures are P0 because they undermine the user's
trust-policy decisions; expect a same-business-day investigation.
