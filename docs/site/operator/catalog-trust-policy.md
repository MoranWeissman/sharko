# Catalog Trust Policy

**Severity:** P1

> **Verified:** Authored 2026-06-01 against `main` HEAD as part of
> V2-4.4 (existing-runbook style compliance refresh). The env-var
> names, default identity list, default workflow_ref regex, log
> messages (`catalog signature verification failed`,
> `catalog source sidecar verification errored`), and the startup
> validation error format are verified against
> `internal/catalog/signing/policy.go` and
> `internal/catalog/signing/verify.go` as shipped in v1.23 (V123-2.3)
> and v1.24 (V124-1.4). Re-verify before changing the env-var names,
> the `<defaults>` magic-token semantics, or the startup error
> message — operators searching for this page will Ctrl-F the verbatim
> error string.

If you are here because your marketplace shows entries as
**Unverified** when you expected them verified, jump to
[Symptoms](#symptoms) → [Diagnosis](#diagnosis) →
[Mitigation](#mitigation-try-in-order). The rest of this page is the
reference for the two env vars (`SHARKO_CATALOG_TRUSTED_IDENTITIES`
and `SHARKO_CATALOG_TRUSTED_WORKFLOW_REF`) that govern the trust
policy — operators on-boarding signed catalogs typically read the
reference half end-to-end at first, then return to the runbook half
when a specific entry fails verification.

Severity is **P1** because the failure is per-entry (one catalog
entry surfacing as Unverified does not break the catalog) but
operators repeatedly hit this when on-boarding internal catalogs or
when sigstore root rotation lands; tickets pile up if the policy is
misconfigured fleet-wide.

---

## Symptoms

What an operator sees when this fires:

- Marketplace UI shows the **Unverified** badge on catalog entries
  that the operator expected to display as **Verified**.
- `GET /api/v1/catalog/sources` response shows `verified: false` for
  the source whose URL was supposed to ship signed entries.
- Sharko pod logs contain at least one `WARN` line at component
  `catalog-signing` with one of these `reason` payloads:

  ```
  level=WARN msg="catalog signature verification failed"
      source_fp=<10-char hex>
      reason="signature verified but identity not in trust policy: <subject>"
  ```

  ```
  level=WARN msg="catalog signature verification failed"
      source_fp=<10-char hex>
      reason="cert-claim assertion failed: workflow_ref \"<actual>\" does not match policy \"<configured>\""
  ```

  ```
  level=WARN msg="catalog source sidecar verification errored"
  ```

- No Sharko-specific alert fires by default — verification failure
  is per-entry diagnostic-only at the metric layer; the
  `sharko_catalog_source_entries{verified="false"}` gauge ticks up
  but does not page.
- The catalog still works — Unverified entries surface and can be
  installed; the operator just sees the badge and the API field.

If the symptom is "the entire catalog stopped loading" (no entries,
not just unverified ones), this is **not** the right runbook —
that's
[`catalog-parse-failure-on-startup.md`](catalog-parse-failure-on-startup.md)
or
[`catalog-trust-root-unavailable.md`](catalog-trust-root-unavailable.md).

---

## Diagnosis

Where to look to determine which of the four trust-policy failure
modes you have. Three checks, in this order.

### 1. Read the `reason` from the WARN line

```sh
kubectl logs -n sharko deploy/sharko --tail=2000 \
  | grep "catalog signature verification failed"
```

The `reason` field is the discriminator:

- `signature verified but identity not in trust policy: <subject>`
  → the SAN regex check failed. The signer is valid; the policy does
  not trust it. Skip to Mitigation step 1.
- `cert-claim assertion failed: workflow_ref ... does not match policy ...`
  → the SAN check passed but the V124-1.4 workflow_ref claim does
  not match. Skip to Mitigation step 2.
- `signature bundle invalid` / `cert chain validation failed` /
  `rekor inclusion proof missing` → the signature itself is broken,
  not the policy. The bundle bytes are corrupt or stale. Skip to
  Mitigation step 4.
- No WARN lines at all + entries still Unverified → the entries are
  **unsigned** (no `signature.bundle` sidecar in the catalog YAML).
  See Mitigation step 3.

### 2. Read the current trust policy at startup

```sh
kubectl logs -n sharko deploy/sharko --tail=2000 \
  | grep "catalog trust policy loaded"
```

Expected: a single startup line:

```
level=INFO msg="catalog trust policy loaded" identity_count=2
```

`identity_count` is the number of compiled regex patterns in
`SHARKO_CATALOG_TRUSTED_IDENTITIES`. The raw patterns are
intentionally **not logged** (they can leak internal org structure
in shared log destinations). The authoritative pattern list is the
env var the operator set.

If `identity_count` is unexpectedly low, the env var is unset / empty
(defaults only — count 2) or you over-trimmed your custom list when
applying Helm changes. Check the rendered Deployment env block:

```sh
kubectl get -n sharko deploy/sharko -o yaml \
  | grep -A2 "SHARKO_CATALOG_TRUSTED_IDENTITIES"
```

### 3. Read the expected signing identity off the failing entry

```sh
# Source fingerprint maps to a URL — re-hash to confirm
printf '%s' "<the catalog URL you expected signed>" \
  | shasum -a 256 | head -c 10
```

Compare the resulting fingerprint to the `source_fp` field in the
WARN line from step 1. Once matched, capture the OIDC subject (cert
SAN) from the signing run by re-fetching the bundle directly:

```sh
curl -fsSL "<catalog-url>.bundle" -o failing.bundle
cosign verify-blob \
  --bundle failing.bundle \
  --certificate-identity-regexp '.*' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  "<catalog-url>" 2>&1 | grep -i identity
```

The SAN you see here is the identity that needs to match one of the
configured trust patterns. If your `SHARKO_CATALOG_TRUSTED_IDENTITIES`
list contains a regex that should match this SAN, the regex is
miswritten; if not, you need to add a pattern (Mitigation step 1).

---

## Mitigation (try in order)

### 1. Add the trusted identity (most common fix)

The SAN regex did not include the signer. Append the signer's
workflow URL (anchored) to `SHARKO_CATALOG_TRUSTED_IDENTITIES`,
keeping the `<defaults>` magic token so you do not lose the public
CNCF + Sharko-release identities:

```sh
# Example: trust your-org's release workflow
SHARKO_CATALOG_TRUSTED_IDENTITIES='<defaults>,^https://github\.com/your-org/.*/\.github/workflows/release\.yml@refs/.*$'
```

Apply via Helm and restart Sharko (the trust policy is read once at
startup; hot-reload is not supported).

```sh
helm upgrade sharko sharko/sharko -n sharko -f values.yaml
kubectl rollout restart deployment/sharko -n sharko
```

Verify after restart by re-running the diagnosis step 1 grep — the
WARN line should disappear for the affected `source_fp` and the
marketplace entry should flip to Verified within one fetch cycle
(default 1h; force-refresh via the admin
`POST /api/v1/catalog/sources/refresh` endpoint to confirm without
waiting).

### 2. Adjust the workflow_ref policy (V124-1.4 layer)

The SAN matched but `SHARKO_CATALOG_TRUSTED_WORKFLOW_REF` does not
permit the workflow ref the signer ran against. Either widen the
policy or change how your catalog is signed.

Widen the policy (if your release pipeline signs on `main` instead
of tags):

```sh
SHARKO_CATALOG_TRUSTED_WORKFLOW_REF='^refs/heads/(main|release-.*)$'
```

Or accept any ref (escape hatch — re-disables the V124-1.4
cryptographic assertion; do this only if you are intentionally
willing to trust non-tag-built signatures from the SAN-matched
identity):

```sh
SHARKO_CATALOG_TRUSTED_WORKFLOW_REF='.*'
```

Apply + restart as in step 1.

### 3. Sign the catalog entry (if unsigned)

If diagnosis found no WARN lines but entries are still Unverified,
the catalog YAML does not carry a `signature.bundle` sidecar URL on
those entries. This is the expected state for fresh third-party
catalogs — signing is opt-in. The catalog publisher needs to:

- Sign the catalog YAML with cosign keyless (`cosign sign-blob
  --bundle <output>.bundle <catalog>.yaml`).
- Host the resulting `.bundle` file at the catalog YAML's URL with
  the `.bundle` suffix appended.
- Update the catalog YAML's `signature.bundle` field to point at
  the sidecar URL.

After the catalog re-fetches (next refresh tick or admin
force-refresh), Sharko will verify the sidecar and the entry will
flip Verified.

For your own internal catalog, the
[catalog scan runbook](../developer-guide/catalog-scan-runbook.md)
covers the recommended publishing pipeline.

### 4. Repair a corrupt signature bundle

If the diagnosis showed `signature bundle invalid` / cert-chain
errors / missing Rekor proof, the bundle bytes are broken. The
catalog publisher needs to re-sign and re-upload. There is nothing
the consumer can do to recover from a corrupt bundle locally; the
verification check is cryptographic.

Common publisher-side causes:

- The bundle file was edited in a text editor (CRLF or BOM
  corruption — bundles are binary).
- The signing run failed mid-way and a partial bundle was uploaded.
- A `cosign` version mismatch produced an older bundle format that
  the loaded sigstore-go library does not understand (run
  `cosign version` and align with Sharko's pinned sigstore-go
  major version).

### 5. "Trust nothing" escape hatch

If the operator wants every signed entry to surface as Unverified
regardless of the actual signer (audit-only posture), set:

```sh
SHARKO_CATALOG_TRUSTED_IDENTITIES='^$'
```

The regex `^$` matches the empty string only — no real OIDC subject
is empty, so every signed entry surfaces as Unverified. This is the
documented escape hatch for the "I want manual review of every
signed entry" workflow.

---

## Root-cause patterns

### Missing trusted identity for the actual signer

The single most common cause: the operator added a third-party
catalog whose signer the policy does not trust. The defaults
(`<defaults>`) trust CNCF org workflows and Sharko's own release
pipeline; everyone else needs an explicit regex. Operators
on-boarding a single internal catalog hit this on the first deploy;
operators on-boarding multiple internal teams' catalogs hit this
each time a new team's signing identity surfaces. The fix is
Mitigation step 1.

### V124-1.4 cert-claim assertion mismatch

After V124-1.4 landed, Sharko cryptographically asserts the cert's
`workflow_ref` claim against `SHARKO_CATALOG_TRUSTED_WORKFLOW_REF`
in addition to the SAN regex. Operators whose release pipeline signs
from a non-tag ref (e.g. nightly main-branch signing) hit this even
when their SAN was previously trusted. Symptom: WARN with
`cert-claim assertion failed`. Fix: Mitigation step 2.

### Catalog publisher rotated signing identity

Sigstore is keyless — every workflow run gets a fresh short-lived
cert from Fulcio. If the catalog publisher migrates their release
pipeline (different workflow file name, different org slug, signing
on a different ref), the SAN changes. The configured regex no longer
matches. Same symptom as cause one; the fix is to update the
trust regex to match the new SAN shape.

### sigstore-go library version skew

Sharko ships with a pinned sigstore-go version. If the catalog
publisher signs with a much newer or older cosign that produces a
bundle format Sharko cannot parse, verification fails with
`signature bundle invalid` or `unknown bundle format`. Fix: align
cosign versions between publisher and consumer; the
[catalog scan runbook](../developer-guide/catalog-scan-runbook.md)
documents the pinned versions.

---

## Prevention

How to make this failure mode less likely going forward.

- **Pre-stage the trust policy in IaC.** Codify
  `SHARKO_CATALOG_TRUSTED_IDENTITIES` and
  `SHARKO_CATALOG_TRUSTED_WORKFLOW_REF` in the Helm values for
  every environment (dev / staging / prod). Sharko-release defaults
  are conservative; internal catalogs need explicit additions, and
  the values file is the right place to keep them under review.
- **Monitor unverified entries with a metric alert.** Add a Prometheus
  alert on `sharko_catalog_source_entries{verified="false"} > 0` for
  sources that are supposed to be signed. Catches the case where the
  catalog publisher silently stopped signing.
- **Pin cosign in publisher pipelines.** Catalog publishers should pin
  the cosign version in their CI to a known-compatible major; bundle
  format drift is the second most common preventable cause of this
  failure mode. The
  [catalog scan runbook](../developer-guide/catalog-scan-runbook.md)
  documents the version Sharko's sigstore-go library expects.
- **Audit `<defaults>` after upgrading Sharko minor versions.** When
  Sharko adds a new default identity (or removes one — rare), the
  `<defaults>` token expansion changes. Operators relying on the
  defaults should re-run the diagnosis Step 2 after a Sharko minor
  upgrade to confirm `identity_count` matches expectations.

---

## Related runbooks

- [`catalog-trust-root-unavailable.md`](catalog-trust-root-unavailable.md)
  — P0 runbook for when the Sigstore trust root itself cannot load
  (TUF outage); every catalog signature fails verification at once,
  not just one entry.
- [`catalog-source-schema-validation-failed.md`](catalog-source-schema-validation-failed.md)
  — different P1 failure on the same fetcher surface; entry skipped
  before signature verification runs.
- [`catalog-source-http-fetch-failed.md`](catalog-source-http-fetch-failed.md)
  — third-party catalog HTTP fetch failure; same fetcher, different
  failure path.
- [`catalog-parse-failure-on-startup.md`](catalog-parse-failure-on-startup.md)
  — catalog YAML malformed at parse time; happens before signature
  verification.
- [`catalog-sources.md`](catalog-sources.md) — env-var reference for
  the third-party catalog sources surface.
- [`../developer-guide/catalog-scan-runbook.md`](../developer-guide/catalog-scan-runbook.md)
  — catalog publishing + signing workflow on the publisher side.
- [`failure-mode-index.md`](failure-mode-index.md) — master inventory
  of operator-facing failures.

## Escalation

If the mitigations above do not flip the affected entries to
Verified within one fetch cycle (default 1h, or immediately after a
force-refresh via `POST /api/v1/catalog/sources/refresh`), email the
maintainer: `moran.weissman@gmail.com`. Include:

- The runbook URL you used (this page)
- The exact WARN line from diagnosis step 1 (including `source_fp`
  and `reason`)
- The startup line from diagnosis step 2 (`identity_count`)
- The configured value of `SHARKO_CATALOG_TRUSTED_IDENTITIES` and
  `SHARKO_CATALOG_TRUSTED_WORKFLOW_REF`
- The Sharko version (`sharko version`)
- The catalog URL that is failing verification

The maintainer is a single human, not a 24x7 rotation. Expect a
business-day SLA. Catalog-trust policy issues are usually fixable in
config; deeper sigstore / Fulcio incidents may take longer.

---

# Reference — env vars and policy semantics

The remainder of this page is the reference for the trust-policy env
vars. Operators on-boarding signed catalogs typically read this
end-to-end the first time; the runbook sections above cover the
"something is failing right now" case.

The policy is configured at startup via two environment variables:

- `SHARKO_CATALOG_TRUSTED_IDENTITIES` — regex list against the cert SAN
  (the OIDC subject — for GitHub Actions, the workflow URL).
- `SHARKO_CATALOG_TRUSTED_WORKFLOW_REF` — defense-in-depth regex against
  the cert's GitHub `workflow_ref` claim (the Git ref the workflow ran
  against; default `^refs/tags/v.*$`).

Both checks must pass for an entry to verify. The cert-claim assertion
narrows trust BEYOND the SAN regex: even an attacker whose SAN matches
the identity list must also have come from a workflow ref the operator
allows.

## What the policy does

When Sharko loads a catalog entry that carries a `signature.bundle`
sidecar URL (the per-entry signing path landed in **v1.23 / V123-2.2**), it:

1. Fetches the Sigstore bundle.
2. Verifies the cert chain against the public-good Fulcio root.
3. Verifies the Rekor inclusion proof.
4. Extracts the OIDC subject (cert SAN) — for GitHub Actions this is the
   workflow URL.
5. **Matches the subject against the configured trust policy regexes.**
6. If at least one regex matches → `verified: true` and the issuer is
   recorded for the UI badge. Otherwise → `verified: false`.

Step 5 is what this page is about.

## Default identities

When `SHARKO_CATALOG_TRUSTED_IDENTITIES` is unset (or empty), Sharko uses
this conservative default list:

| Pattern | Why |
|---------|-----|
| `^https://github\.com/cncf/.*/\.github/workflows/.*$` | Any signed workflow under the CNCF org. Sharko's positioning targets CNCF-curated addons, so trusting CNCF workflows out of the box matches the project's curation stance. |
| `^https://github\.com/MoranWeissman/sharko/\.github/workflows/release\.yml@refs/heads/main$` | Sharko's own release workflow. From V123-2.5 onwards the release pipeline signs the embedded catalog; this default keeps fresh installs showing "Verified" pills on the embedded entries without operator intervention. The SAN anchors to `refs/heads/main` because Fulcio mints `job_workflow_ref` (the workflow file's ref at job start), not the triggering tag — release.yml runs as a `workflow_run`-triggered job whose `job_workflow_ref` is always `refs/heads/main`. Tag-context is enforced cryptographically by `SHARKO_CATALOG_TRUSTED_WORKFLOW_REF` (V124-1.4 — see below). |

Operators with no internal catalogs can ship the defaults as-is. Operators
with internal catalogs typically want **defaults + their own org regex**
— see the next section.

## Configuring

Set `SHARKO_CATALOG_TRUSTED_IDENTITIES` to a comma-separated list of Go
regex patterns. The literal token `<defaults>` (case-sensitive, exact
match) expands to the default list at the matching position.

```bash
# Defaults only — the same as leaving the var unset, but explicit.
SHARKO_CATALOG_TRUSTED_IDENTITIES=<defaults>

# Defaults + your internal CI workflow (recommended for most operators).
SHARKO_CATALOG_TRUSTED_IDENTITIES=<defaults>,^https://github\.com/myorg/.*/\.github/workflows/.*$

# Internal-only — defaults are NOT auto-merged when the token is missing.
SHARKO_CATALOG_TRUSTED_IDENTITIES=^https://github\.com/myorg/.*/\.github/workflows/.*$
```

Via Helm (example fragment for `values.yaml`):

```yaml
env:
  - name: SHARKO_CATALOG_TRUSTED_IDENTITIES
    value: "<defaults>,^https://github\\.com/myorg/.*/\\.github/workflows/.*$"
```

Note the escaped backslashes in YAML — `\.` becomes `\\.` once inside a
double-quoted YAML scalar.

## Examples

| Env var value | Active regexes | When to use this |
|---------------|----------------|------------------|
| *(unset)* | both defaults | Fresh installs, public CNCF charts only |
| *(empty)* | both defaults | Same as unset; treated identically |
| `<defaults>` | both defaults | Explicit "I want the defaults" — useful in IaC where empty means "remove this var" |
| `<defaults>,^https://github\.com/myorg/.*/\.github/workflows/.*$` | defaults + your org | Most common: keep the public defaults and add your own org's CI |
| `^https://github\.com/myorg/.*/\.github/workflows/.*$,<defaults>` | your org + defaults | Same as above; the `<defaults>` token expands at its own position. Order matters only for first-match-wins log lines. |
| `^https://github\.com/myorg/.*/\.github/workflows/.*$` | your org only | Override entirely — defaults excluded by intent |
| `^$` | one regex matching no string | "Trust nothing" escape hatch — every signed entry surfaces as Unverified |

## Workflow_ref claim assertion (V124-1.4)

`SHARKO_CATALOG_TRUSTED_WORKFLOW_REF` adds a cryptographic assertion on
the cert's GitHub `workflow_ref` claim — the Fulcio extension that
records the Git ref the workflow ran against (OID 1.3.6.1.4.1.57264.1.6).
The assertion runs AFTER the SAN regex check passes, so both must match
for an entry to verify.

### Why it matters

Pre-V124-1.4 the SAN regex was the only narrowing on WHO signed. Sharko's
own `release.yml` gates on `if: startsWith(workflow_run.head_branch, 'v')`
to ensure only tag-built releases sign — but that's a **trigger-time
guard**, not a **cryptographic assertion**. An attacker who matched the
SAN regex (or a misconfigured fork whose `release.yml` ran from a
non-tag ref) could ship a signed-looking malicious entry. The cert-claim
assertion closes that gap: only signatures from a workflow running
against a matching ref are accepted.

### Default

When `SHARKO_CATALOG_TRUSTED_WORKFLOW_REF` is unset or empty:

```
^refs/tags/v.*$
```

This is the secure default — Sharko's own release pipeline signs only on
tag refs of the form `v...`, so the default mirrors that. Operators with
non-tag-driven release pipelines override via the env var.

### Configuring

```bash
# Default — only tag refs of the form `v...` are accepted. Same as unset.
SHARKO_CATALOG_TRUSTED_WORKFLOW_REF=^refs/tags/v.*$

# Operator with a branch-based release pipeline (e.g. signs on every
# merge to main + every release branch).
SHARKO_CATALOG_TRUSTED_WORKFLOW_REF=^refs/heads/(main|release-.*)$

# Operator who wants to accept entries signed by non-GitHub-Actions
# issuers too (whose cert has no workflow_ref extension at all). The
# `.*` regex matches anything, INCLUDING the empty claim.
SHARKO_CATALOG_TRUSTED_WORKFLOW_REF=.*
```

Via Helm:

```yaml
env:
  - name: SHARKO_CATALOG_TRUSTED_WORKFLOW_REF
    value: "^refs/tags/v.*$"
```

### Examples

| Env var value | Effective policy | When to use |
|---------------|------------------|-------------|
| *(unset)* | `^refs/tags/v.*$` | Default secure posture — only tag-built signatures accepted |
| `^refs/tags/v.*$` | same as unset | Explicit "I want the default" — useful in IaC where empty means "remove this var" |
| `^refs/heads/main$` | main-branch CI only | Operators whose release pipeline signs on every main merge instead of tag pushes |
| `.*` | accept any ref (including empty) | Escape hatch for catalogs signed by non-GitHub-Actions issuers — DISABLES the cert-claim assertion |
| `^refs/(heads/main|tags/v.*)$` | main OR tag | Mixed-mode pipelines that sign both nightly snapshots and release tags |

### Failure mode

When the SAN check passes but the cert-claim assertion fails, Sharko
logs a `WARN` line under component `catalog-signing`:

```
level=WARN msg="catalog signature verification failed"
    source_fp=<10-char hex>
    reason="cert-claim assertion failed: workflow_ref \"refs/heads/feature-branch\" does not match policy \"^refs/tags/v.*$\""
```

The entry surfaces as Unverified in the UI and on the API — the loader
keeps loading it (no hard fail) so the catalog stays available.

### Validation

The regex is compiled at startup. A malformed pattern is a fatal startup
error with the env var name and the offending pattern in the message —
same posture as `SHARKO_CATALOG_TRUSTED_IDENTITIES`.

## Cert SAN format

The regex matches against the OIDC subject (Subject Alternative Name) on
the leaf cert that Fulcio issued for the signing run. For a GitHub
Actions keyless signing this is the full workflow URL:

```
https://github.com/<org>/<repo>/.github/workflows/<workflow-file>@refs/<heads|tags>/<ref>
```

Concrete examples:

```
https://github.com/cncf/cert-manager/.github/workflows/release.yaml@refs/heads/main
https://github.com/MoranWeissman/sharko/.github/workflows/release.yml@refs/tags/v1.23.0
```

The `^` and `$` anchors are **recommended but not enforced** by Sharko's
parser. An unanchored regex matches as a substring — that's the
operator's choice. For least-surprise pinning, anchor your patterns.

For the canonical SAN format reference (other CI systems, non-GitHub
issuers, the email-SAN path) see the [sigstore-go docs](https://github.com/sigstore/sigstore-go/blob/main/README.md)
and the [Sigstore certificate identity reference](https://docs.sigstore.dev/cosign/verifying/verify/#about-keyless-verification).

## Validation

Every pattern is compiled at startup. A pattern that fails to compile is
a fatal startup error — Sharko refuses to start with a clear message
naming the offending pattern:

```
load catalog trust policy: SHARKO_CATALOG_TRUSTED_IDENTITIES: invalid regex "[unbalanced": ...
```

This matches the V123-1.1 `SHARKO_CATALOG_URLS` posture: misconfiguration
gets caught at the point of deployment, not later when an entry
mysteriously fails to verify.

## Hot reload

Not supported. The trust policy is read once at startup and held for the
process lifetime. To change the policy:

1. Update the env var (Helm value, ConfigMap reference, etc.).
2. Restart the Sharko pod.

This matches the `SHARKO_CATALOG_URLS` posture — the catalog config
surface is deliberately env-driven and restart-only for v1.23. A future
release may add a hot-reload watcher; until then a restart is the
supported way to apply policy changes.

## What lands in v1.23 vs later

- **v1.23 — V123-2.2 (already shipped):** per-entry verification path,
  `verified` and `signature_identity` JSON fields on every catalog
  endpoint.
- **v1.23 — V123-2.3 (this page):** `SHARKO_CATALOG_TRUSTED_IDENTITIES`
  env-var parser with `<defaults>` magic-token semantics.
- **v1.23 — V123-2.4:** UI verified badge + "Signed only" pseudo-filter
  on Browse.
- **v1.23 — V123-2.5:** the Sharko release pipeline starts signing the
  embedded catalog — the second default identity finally has signatures
  to verify against.
- **v1.24 — V124-1.4 (this page):** `SHARKO_CATALOG_TRUSTED_WORKFLOW_REF`
  cert-claim assertion layered on top of the SAN regex. Default
  `^refs/tags/v.*$` cryptographically pins trust to tag-built signatures.
- **Future:** hot reload, per-source policy overrides, Settings-page
  exposure of the policy.

<!-- Style-guide compliance checklist (V2-4.1):
- [x] Title matches `# <Failure name>` (page covers the trust-policy failure mode)
- [x] Severity line present (P1)
- [x] Verified-by-execution header + date
- [x] Symptoms section before Diagnosis
- [x] Symptoms include exact log lines / error messages / API responses
- [x] Diagnosis has 3 concrete checks
- [x] Mitigation uses numbered list
- [x] Mitigation has 5 steps in priority order
- [x] Root-cause patterns: 4 named causes
- [x] Prevention section present and non-empty
- [x] Related runbooks section present
- [x] Intro is operator-on-call voice (runbook half leads, reference half follows behind divider)
- [x] Length in 300-800 line range
- [x] All cross-links resolve
- [x] No emoji / no internal Slack / employee email
- [x] (if applicable) Alert name from prometheusrules.yaml referenced — N/A (no alert for this failure mode)
-->

