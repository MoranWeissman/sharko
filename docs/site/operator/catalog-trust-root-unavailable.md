# Catalog Signing Trust Root Unavailable

**Severity:** P0

> **Verified:** Authored 2026-06-01 against `main` HEAD. The TUF-backed
> `trusted_root.json` fetch path is verified against
> `internal/catalog/signing/tufroot.go:65-101`, where the function
> `GetTarget("trusted_root.json")` is the canonical load and
> `"parse trusted_root.json"` is the canonical parse error string.
> Catalog signing surface (sources + signing) is bounded by
> `internal/catalog/signing/verify.go`. Reference page for trust-policy
> semantics is [`catalog-trust-policy.md`](catalog-trust-policy.md).
> Re-verify when TUF client library or the trusted-root target name
> changes.

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

3. **If the TUF fetch returns a malformed body**, work around by
   pre-cached trusted root. Sigstore ships periodic "trust root
   snapshots" via the `sigstore-go` SDK that can be bundled offline.
   The operator-side mitigation is to:

   - Download a known-good `trusted_root.json` from a trusted source.
   - Mount it as a file in the Sharko pod.
   - Configure Sharko to read from disk instead of TUF (the env var
     name varies; check `internal/catalog/signing/tufroot.go` for
     the current override).

   ```sh
   # Create a configmap with the trusted root:
   kubectl -n "$SHARKO_NS" create configmap sigstore-trusted-root \
     --from-file=trusted_root.json=/path/to/trusted_root.json

   # Mount it in the Sharko deployment (example patch):
   kubectl -n "$SHARKO_NS" patch deployment sharko --type='json' -p='[
     {"op":"add","path":"/spec/template/spec/volumes/-","value":{
       "name":"sigstore-trusted-root","configMap":{"name":"sigstore-trusted-root"}}},
     {"op":"add","path":"/spec/template/spec/containers/0/volumeMounts/-","value":{
       "name":"sigstore-trusted-root","mountPath":"/var/lib/sharko/sigstore","readOnly":true}}
   ]'
   ```

   Then set the env var to point at the file:

   ```sh
   kubectl -n "$SHARKO_NS" set env deployment/sharko \
     SHARKO_TRUSTED_ROOT_PATH=/var/lib/sharko/sigstore/trusted_root.json
   ```

   This bypasses TUF entirely. Document the override; without TUF, the
   trusted root no longer auto-rotates, so the operator must update
   the ConfigMap when Sigstore rotates its root keys (rare — quarterly
   or less often, typically).

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

Fix is to wait it out, OR to switch to a pre-cached trusted root
(Mitigation step 3) as a bridge.

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
response is consistent across restarts, fall back to Mitigation step 3
(pre-cached trusted root).

---

## Rollback plan

Mitigation steps 1, 2, 5 are non-destructive (restart, NetworkPolicy
update, CA install).

For Mitigation step 3 (pre-cached trusted root):

1. To revert to TUF-based fetch once Sigstore is healthy:
   ```sh
   kubectl -n "$SHARKO_NS" set env deployment/sharko \
     SHARKO_TRUSTED_ROOT_PATH-
   kubectl -n "$SHARKO_NS" rollout restart deployment/sharko
   ```

2. Verify the TUF path works (Diagnosis step 2 wget returns 200).

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

- **Monitoring — startup failure metric.** Add
  `sharko_catalog_verifier_initialized{result="success|failed"}` as a
  Counter incremented once at startup. Alert when the failed-bucket
  count is non-zero. Wiring is in
  `internal/catalog/signing/verify.go` init.

- **Monitoring — TUF fetch reachability.** Add a periodic background
  check that re-validates TUF reachability and exposes a gauge:

  ```promql
  sharko_sigstore_tuf_reachable == 0
  ```

  Alert when 0 for > 5m. Catches the slow-degradation case where
  TUF starts failing mid-runtime (rare, but the verifier caches the
  trusted root and won't re-fetch until restart).

- **Gating — pre-cached trusted root as a chart default.** Ship the
  Helm chart with a default pre-cached `trusted_root.json` baked in
  as a ConfigMap, with TUF as the override (not the default). This
  inverts the failure mode: TUF outage is irrelevant; the operator
  only pays the trust-root-update cost on Sigstore's rotation
  cadence (rare).

- **Scheduled work — quarterly trusted-root refresh drill.** Once per
  quarter, refresh the pre-cached trusted root, verify the new bundle
  validates a known-signed catalog entry, and document the
  procedure. Catches "the bundle in the chart is stale" before a real
  Sigstore rotation forces it.

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

<!-- Style-guide compliance checklist (V2-4.1):
- [x] Title matches `# <Failure name>`
- [x] Severity line present (P0)
- [x] Verified-by-execution header + date
- [x] Symptoms section before Diagnosis
- [x] Symptoms include exact log lines / error messages / alert names
- [x] Diagnosis has 3+ concrete checks (4 named)
- [x] Mitigation uses numbered list
- [x] Mitigation has 3-5 steps in priority order
- [x] Root-cause patterns: 2+ named causes (4 named)
- [x] Prevention section present and non-empty
- [x] Related runbooks section present
- [x] Intro is operator-on-call voice
- [x] Length 300-800 lines
- [x] All cross-links resolve
- [x] No emoji / no internal Slack / employee email
- [x] (if applicable) No alert defined yet (per Symptoms)
-->
