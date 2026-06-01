# Catalog Parse Failure on Startup

**Severity:** P1

> **Verified:** Authored 2026-06-01 against `main` HEAD. The error
> message `"catalog: parse yaml: <reason>"` is verified against
> `internal/catalog/loader.go:332`, which fires inside
> `LoadBytesWithSource` when `yaml.NewDecoder(...).Decode(&root)`
> returns a non-EOF error. The same loader handles BOTH the embedded
> catalog (called via `Load()` → `LoadBytes(catalogembed.Bytes)`)
> and third-party catalog bodies fetched by the source-fetcher
> (called via `LoadBytesWithSource` / `LoadBytesWithVerifierAndSource`
> at `internal/catalog/sources/fetcher.go:702-704`). The
> "no entries found under 'addons:'" error at line 335 is a sibling
> shape with the same operator surface. Re-verify when the loader's
> YAML decoder library changes or when the embedded catalog source
> file is replaced.

The catalog YAML parser failed. This runbook covers both flavors of
the failure mode that share the same emission site
(`internal/catalog/loader.go:332`):

1. **Embedded catalog parse failure on startup** — extremely rare;
   indicates a development bug or a build-time corruption of
   `catalog/addons.yaml`. Without the embedded catalog, NO addons
   surface in the marketplace and dependent flows break. **This is
   the case that escalates toward P0** because it blocks every
   downstream operation.

2. **Third-party catalog source parse failure** — common; one of the
   URLs in `SHARKO_CATALOG_URLS` returned a body that YAML-parsed
   to garbage. The embedded catalog is unaffected; only entries
   from the failing source are missing. This is closely related to
   [`catalog-source-schema-validation-failed.md`](catalog-source-schema-validation-failed.md)
   — schema-validation is the next step after parse; this runbook
   covers parse failure specifically.

The operator distinguishes the two cases by **whether Sharko pod
starts at all** (case 1 → CrashLoopBackoff; case 2 → pod runs, one
source failed). The diagnosis and mitigation paths diverge based on
that signal.

This runbook is the entry point for "catalog YAML wouldn't parse."
For "catalog YAML parsed but the loader rejected an entry's
schema," see
[`catalog-source-schema-validation-failed.md`](catalog-source-schema-validation-failed.md).

---

## Symptoms

What an operator sees when this fires:

### Case 1: Embedded catalog parse failure on startup

- **Sharko pod fails to start** with the error in the pod logs:

  ```
  {"time":"...","level":"ERROR","msg":"catalog load failed","error":"catalog: parse yaml: yaml: line 247: did not find expected key"}
  ```

  The pod typically exits during startup initialization; Kubernetes
  enters `CrashLoopBackoff`.

- **`kubectl get pod -n <sharko-ns>`**:

  ```
  NAME                       READY   STATUS             RESTARTS   AGE
  sharko-7d8f9b6c5-x2k4t    0/1     CrashLoopBackoff   5          3m
  ```

- **No API surface available** — `/api/v1/health` returns connection
  refused (Sharko isn't listening on the port).

- **No marketplace entries** — when Sharko eventually starts, the
  `GET /api/v1/catalog/entries` endpoint returns empty.

- **Alerts fire**: `SharkoServiceDown` (or equivalent
  pod-availability alert) fires on Sharko being unreachable.
  This may be the first signal an operator sees.

### Case 2: Third-party catalog source parse failure

- **Sharko logs the warn line at the fetcher level**
  (`internal/catalog/sources/fetcher.go:708` wraps the parse error
  in the "schema validation failed" surface — see
  [`catalog-source-schema-validation-failed.md`](catalog-source-schema-validation-failed.md)
  for the merged failure path).

- **The pod is running** — `/api/v1/health` returns 200.

- **The Marketplace UI** shows the failing source with **Failed**
  status; the embedded catalog and other healthy sources continue
  to surface entries.

- **No fleet-wide alert** unless multiple sources fail
  simultaneously and the
  [`SharkoCatalogScanFastBurn`](budget-burn-runbook.md#sharkocatalogscanfastburn)
  alert fires.

If you see Case 1 symptoms, this is the runbook entry point. If you
see Case 2 symptoms specifically aligned with a single third-party
source URL, the per-source runbook
[`catalog-source-schema-validation-failed.md`](catalog-source-schema-validation-failed.md)
is more targeted.

---

## Diagnosis

Three checks. Step 1 distinguishes the two cases (embedded vs
third-party). Step 2 captures the exact parse error. Step 3
identifies which line / structure of the YAML is malformed.

### 1. Is this case 1 (embedded) or case 2 (third-party)?

```sh
# If the pod is CrashLoopBackoff, this is case 1:
kubectl -n <sharko-ns> get pod -l app=sharko -o custom-columns=NAME:.metadata.name,STATUS:.status.phase,RESTARTS:.status.containerStatuses[0].restartCount

# If the pod is running, fetch the source-status report:
curl -sS http://sharko/api/v1/catalog/sources \
  -H "Authorization: Bearer ${SHARKO_TOKEN}" \
  | jq '.[] | select(.status=="failed") | {url, source_fp, last_error}'
```

Case 1 (embedded) → pod is CrashLoopBackoff. Case 2 (third-party) →
pod is running, one or more sources have `status: "failed"` with a
parse error.

### 2. Capture the exact parse error

**For Case 1**, read from the previous container's logs (the
current one is still crash-looping):

```sh
kubectl -n <sharko-ns> logs deploy/sharko --previous \
  | jq -c 'select(.msg | test("catalog|parse yaml|load failed"; "i"))' \
  | head -10
```

Look for `"catalog: parse yaml: <yaml-library-error>"`. The
`<yaml-library-error>` includes the line number and the kind of
syntax issue (`did not find expected key`, `mapping values are not
allowed in this context`, `could not find expected ':'`, etc.).

**For Case 2**:

```sh
curl -sS http://sharko/api/v1/catalog/sources \
  -H "Authorization: Bearer ${SHARKO_TOKEN}" \
  | jq -r '.[] | select(.status=="failed") | "\(.url): \(.last_error)"'
```

Same error shape — `"schema validation: catalog: parse yaml: ..."`
(the source-fetcher wraps the loader error in `schema validation:`
context).

### 3. Identify the YAML structure that's malformed

The yaml library's error message includes a line number. For Case 1
the embedded source file is in the Sharko repo; for Case 2 the
source body needs to be fetched and inspected.

**For Case 1** (embedded, when you're investigating a development bug):

The embedded catalog source is `catalog/addons.yaml` in the Sharko
repo. The error line number maps directly. Fetch the file from
the version that crashed:

```sh
SHARKO_VERSION=$(kubectl -n <sharko-ns> get deployment sharko \
  -o jsonpath='{.spec.template.spec.containers[0].image}' \
  | awk -F: '{print $2}')
git -C /path/to/sharko-repo show "v${SHARKO_VERSION}:catalog/addons.yaml" \
  > /tmp/embedded-addons.yaml
ERR_LINE=$(grep -oE 'line [0-9]+' /tmp/parse-error | head -1 | awk '{print $2}')
sed -n "$((ERR_LINE-3)),$((ERR_LINE+3))p" /tmp/embedded-addons.yaml
```

The context window around the error line shows what's broken. For a
shipped release, this should not happen — escalate immediately if it
does (the catalog escaped CI somehow).

**For Case 2** (third-party):

```sh
FAILING_URL=<url-from-step-1>
SHARKO_POD=$(kubectl -n <sharko-ns> get pod -l app=sharko -o name | head -1)
kubectl -n <sharko-ns> exec "$SHARKO_POD" -- \
  wget -q -O - "$FAILING_URL" \
  > /tmp/failing-catalog.yaml

ERR_LINE=<from-error-message>
sed -n "$((ERR_LINE-3)),$((ERR_LINE+3))p" /tmp/failing-catalog.yaml
```

Common parse failures:

- Trailing tab in indentation (YAML rejects tabs)
- Unescaped colon in a value (e.g. `description: prod: this`)
- Unquoted string starting with `*` (alias-prefix collision)
- BOM byte at file start (some editors save with BOM)
- Mixed line endings (CRLF in some lines)

---

## Mitigation (try in order)

1. **For Case 1 (embedded) — roll back the Sharko deploy to the
   previous version.** A shipped Sharko binary with a broken
   embedded catalog is an emergency; the fastest path back to
   service is a rollback.

   ```sh
   helm rollback sharko -n <sharko-ns>
   kubectl -n <sharko-ns> rollout status deployment/sharko
   ```

   Verify the pod starts:

   ```sh
   kubectl -n <sharko-ns> get pod -l app=sharko
   curl -sS http://sharko/api/v1/health
   ```

   File an immediate issue with the parse error and the broken
   catalog version. This is Case 1's primary mitigation — the fix
   is upstream in the Sharko repo, not operator-side.

2. **For Case 2 (third-party) — remove the failing source from
   the active set.** This is the lowest-friction path while you
   wait for the source author to fix:

   ```sh
   helm upgrade --reuse-values \
     --set "catalog.sources={<remaining-source-urls>}" \
     sharko sharko/sharko -n <sharko-ns>
   ```

   The fetcher drops the source on the next config reload. The
   embedded catalog and other healthy sources continue to surface
   entries.

3. **For Case 2 — notify the source author with the exact parse
   error and the line context.** The source author has the most
   leverage; their next release can fix the YAML, and the fetcher
   picks up the fix automatically.

   Capture the error context from Diagnosis step 3 and email or
   file an issue against the source's repo. Include the YAML line
   number and the surrounding 3-line context. Most parse failures
   are 1-line fixes (rogue tab, missing colon).

4. **For Case 1 — if rollback isn't possible (the previous version
   has a known critical bug, or the new version has a
   non-catalog feature that's already in production use), patch
   the catalog at runtime via a ConfigMap override (advanced).**
   Sharko ships the embedded catalog as a Go embed; runtime
   override isn't supported out-of-the-box. The advanced workaround
   is to fork the Sharko image with a fixed `catalog/addons.yaml`
   and rebuild:

   ```sh
   # Local repair: fix the YAML, rebuild the image:
   # 1. Apply the fix in catalog/addons.yaml in a private branch
   # 2. Build with `make docker-build SHARKO_VERSION=<x>-hotfix`
   # 3. Push to your private registry
   # 4. Update Helm values to point at the hotfix image:
   helm upgrade --reuse-values \
     --set "image.repository=<private-registry>/sharko" \
     --set "image.tag=<x>-hotfix" \
     sharko sharko/sharko -n <sharko-ns>
   ```

   This is rare. Mitigation step 1 (rollback) is correct in almost
   every realistic scenario.

5. **Last resort — for Case 2 with embedded catalog also unhealthy
   (e.g. the embedded catalog had a startup warning that the third-
   party failure happened to surface), disable third-party catalogs
   entirely.** This is the "isolate to embedded-only" lane:

   ```sh
   helm upgrade --reuse-values \
     --set "catalog.sources={}" \
     sharko sharko/sharko -n <sharko-ns>
   ```

   Only the embedded catalog surfaces. Restoration of third-party
   sources is a follow-up action once each source author has
   shipped a fix.

---

## Root-cause patterns

### Source author shipped malformed YAML

The most common Case 2 cause. The source author hand-edited
`catalog.yaml`, didn't run `yamllint` or `sharko validate-config`,
and shipped a file with a syntax error. The fetcher's next run
catches it; the schema-validation log line fires; the source goes
Failed.

Diagnostic signature: Diagnosis step 2 returns a parse error
specifically mentioning a YAML-syntax issue (tab, missing colon,
unbalanced quote); the source author's previous release was healthy.

Fix is Mitigation step 3 (notify the author) plus Mitigation step 2
(remove the source until fixed).

### Hosting infrastructure injected content (proxy / WAF)

The source URL returns a non-YAML body because an upstream system
(a WAF, a proxy, a captive-portal) intercepted the request and
returned HTML or a JSON error. The body parses as YAML to
gibberish.

Diagnostic signature: Diagnosis step 3 shows the body starts with
`<!DOCTYPE html>` or `{"error":...}` instead of `addons:`. The
fetcher's `recordSchemaFailure` is the path that fires.

Fix is the infrastructure-side — the source URL's path through
your network is being intercepted. See
[`corporate-mitm-tls.md`](corporate-mitm-tls.md) for the proxy
angle.

### Sharko embedded catalog corrupted at build time (Case 1)

A CI process or a manual `make build` step truncated the
`catalog/addons.yaml` file or replaced it with a partial copy. The
shipped Sharko image's embedded catalog is unparseable.

Diagnostic signature: every freshly-deployed pod from the same
image fails identically. CI didn't catch it because the catalog
test fixture differs from the embedded source.

Fix is Mitigation step 1 (roll back the deploy) plus an immediate
issue against the build pipeline (Prevention).

### YAML library version skew (Case 1)

A Sharko dependency bump changed YAML-parsing behavior between
patch releases (e.g. stricter handling of trailing whitespace, BOM
rejection). The previous catalog version that parsed cleanly now
rejects.

Diagnostic signature: rare, but distinct — the embedded catalog
file is unchanged across versions; only the binary differs.

Fix is Mitigation step 1 (rollback) plus a Sharko-side dependency
review.

### Operator deployed with `SHARKO_CATALOG_URLS` pointing at a non-YAML URL

The operator set `SHARKO_CATALOG_URLS=https://internal-tool/api/v1/catalog`
where the endpoint actually returns JSON or HTML. The fetcher tries
to YAML-parse it and fails consistently.

Diagnostic signature: Diagnosis step 3 shows the body starts with
`{` or `<`. The URL points at a non-YAML endpoint.

Fix is Mitigation step 2 — correct the URL (typically point at the
actual `.yaml` file rather than an API endpoint).

---

## Prevention

- **Monitoring — embedded catalog load gate.** A V2-3.x follow-up
  metric `sharko_catalog_embedded_load_success` exposed as a binary
  gauge would let operators alert on Case 1 immediately. Today, the
  signal is the pod crash itself; Kubernetes-level monitoring
  (CrashLoopBackoff) catches it but doesn't identify the catalog as
  the cause.

- **Gating — CI smoke test on the embedded catalog.** The Sharko CI
  pipeline runs `go test ./internal/catalog/...` which includes a
  smoke load of the embedded catalog. A future addition: run
  `sharko validate-config catalog/addons.yaml` in CI as a separate
  step so the failure mode is named clearly when it fires.

- **Gating — source authors run `sharko validate-config` in CI.**
  Document the contract: source authors who publish catalog YAML
  should validate it in their own CI before merging. The Sharko
  team can ship a validator-as-a-container image so source authors
  don't need to build Sharko themselves.

- **Documentation — schema reference + worked examples.** The
  catalog-sources documentation
  ([`catalog-sources.md`](catalog-sources.md)) should ship a
  worked example: minimum-viable catalog YAML, full schema
  reference, common syntax pitfalls. Most parse failures trace to
  operators or source authors who didn't have a known-good template
  to start from.

- **Failover — embedded catalog acts as the safety net.** The
  embedded catalog is intentionally built into the Sharko binary
  so that third-party catalog failures cannot disable the
  marketplace entirely. Preserve this invariant on refactors —
  the embedded loader and the third-party fetcher should be
  independent failure domains.

- **Scheduled work — quarterly catalog schema audit.** Sharko's
  embedded loader has strict validation (`allowedCategories`,
  `allowedCuratedBy`, required fields). Quarterly, confirm the
  schema constants in `internal/catalog/loader.go` still match the
  shipped `catalog/addons.yaml` and that third-party source authors
  have been notified of any tightening.

---

## Related runbooks

- [`catalog-source-schema-validation-failed.md`](catalog-source-schema-validation-failed.md)
  — the post-parse stage: YAML parses, but the entry fails the
  schema check.
- [`catalog-source-http-fetch-failed.md`](catalog-source-http-fetch-failed.md)
  — adjacent fetch failure: HTTP didn't return a body at all.
- [`catalog-sources.md`](catalog-sources.md) — configuration
  reference for `SHARKO_CATALOG_URLS` / `catalog.sources`.
- [`catalog-trust-policy.md`](catalog-trust-policy.md) — adjacent
  signing-verification failure (parse-OK, signature-bad).
- [`catalog-trust-root-unavailable.md`](catalog-trust-root-unavailable.md)
  — P0 fleet-wide: trust-root infrastructure unreachable.
- [`oom-restart-loop.md`](oom-restart-loop.md) — adjacent
  pod-restart failure mode (Case 1 looks similar to OOM at the
  Kubernetes level; the differentiator is the catalog parse error
  in the logs).
- [`budget-burn-runbook.md#sharkocatalogscanfastburn`](budget-burn-runbook.md#sharkocatalogscanfastburn)
  — fleet-wide catalog-scan alert.
- [`failure-mode-index.md`](failure-mode-index.md) — master inventory.
- [`../developer-guide/logging.md`](../developer-guide/logging.md#correlation-ids)
  — `request_id` correlation pattern.

## Escalation

**For Case 1 (embedded catalog parse failure on a shipped Sharko
version), escalate immediately.** This is a critical Sharko-side bug
— a release escaped CI with a broken catalog. Email the maintainer:
`moran.weissman@gmail.com`. Include:

- This runbook URL
- The Sharko version that's failing
- The exact error from Diagnosis step 2
- Whether the previous version starts cleanly
- Whether you've rolled back already (Mitigation step 1)

This is the only P1 failure mode in this set that flags as
P0-adjacent — embedded catalog failure means no addons surface, no
marketplace works, the whole platform-engineering value of Sharko
disappears. Maintainer-side response should be the same business
day; coordinate via the issue.

**For Case 2 (third-party catalog parse failure), escalation is to
the source author**, not the maintainer. Sharko's behavior is
correct (skip the failed source, retain embedded). Only loop in
the maintainer if you suspect Sharko is misreporting (i.e. the
YAML looks correct to you but Sharko rejects it).

The maintainer is a single human, not a 24×7 rotation.

<!-- Style-guide compliance checklist (V2-4.1):
- [x] Title matches `# <Failure name>`
- [x] Severity line present (P1)
- [x] Verified-by-execution header + date current
- [x] Symptoms section appears BEFORE Diagnosis (Case 1 + Case 2 split)
- [x] Symptoms include exact log lines / error messages
- [x] Diagnosis has 3+ concrete checks (3 named, branched per case) with exact commands
- [x] Mitigation uses numbered list (1. 2. 3. 4. 5.) not bullets
- [x] Mitigation has 3-5 steps in priority order, each with rationale + exact command
- [x] Root-cause patterns section: 2+ named causes (5 named), 1-3 paragraphs each
- [x] Prevention section present and non-empty (NOT "TBD")
- [x] Related runbooks section present with multiple links
- [x] Intro is operator-on-call voice; flagged Case 1 P0-adjacency
- [x] Length within 300-800 line target
- [x] All cross-links resolve via mkdocs --strict
- [x] No emoji / no internal Slack / employee email
- [x] Alert names referenced (FastBurn, ServiceDown)
-->
