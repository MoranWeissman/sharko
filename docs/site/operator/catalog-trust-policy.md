# Catalog Trust Policy

**Severity:** P1

> **Verified:** Updated 2026-08-25 — the `source` log field is now always
> the fixed word `redacted`; log-line shapes re-checked on that date.
> Originally authored 2026-06-01 against Sharko as shipped. The env-var
> names, the default identity list, the default workflow_ref regex, the
> log messages (`catalog signature verification failed`,
> `catalog source sidecar verification errored`) and the startup
> validation error format are all verified verbatim. Re-verify before
> changing the env-var names, the `<defaults>` magic-token semantics or
> the startup error message — an operator who finds this page will Ctrl-F
> the exact error string.
> Reviewed 2026-08-29 — wording only; no step in this runbook changed.

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

## Read this first if `v4.0.1` shows every built-in entry as Unverified

`v4.0.1` refuses the signatures on its own built-in catalogue, and the
cause is the default trust policy it ships with rather than anything
wrong with the signatures.

**It is not evidence of tampering.** That distinction matters, because a
refused signature is exactly what tampering would also look like. All 45
bundles published with `v4.0.1` were re-checked and all 45 are genuine:
the signature matches, the payload digest matches, the Fulcio
certificate chain validates, the Rekor transparency-log entry is present
and valid, and the signing identity is Sharko's own release workflow.
One check refused them, and it refused all 45 in exactly the same way.

The two settings `v4.0.1` ships with cannot both be satisfied by any
certificate:

| Setting | `v4.0.1` default | What a Sharko release certificate actually carries |
|---|---|---|
| `SHARKO_CATALOG_TRUSTED_IDENTITIES` (the Sharko pattern) | ends `@refs/heads/main` | `…/release.yml@refs/heads/main` — matches |
| `SHARKO_CATALOG_TRUSTED_WORKFLOW_REF` | `^refs/tags/v.*$` | `refs/heads/main` — does not match |

Sharko's release workflow is triggered by `workflow_run`. For that
trigger the certificate's `workflow_ref` claim records the ref the
workflow **file** sits on, not the tag being built, so it is always
`refs/heads/main`. A policy asking for a tag ref there can never be
satisfied.

**The workaround on `v4.0.1`, and it works today:**

```bash
SHARKO_CATALOG_TRUSTED_WORKFLOW_REF=^refs/heads/main$
```

```yaml
# Via Helm
env:
  - name: SHARKO_CATALOG_TRUSTED_WORKFLOW_REF
    value: "^refs/heads/main$"
```

That is the documented override, it changes nothing about the signature,
digest, chain, transparency-log or identity checks, and the built-in
entries verify with it set.

**A binary you already have keeps refusing, whatever the source says
later.** The policy is compiled into the binary, so nothing about a
later source change reaches a copy already on your disk or in your
cluster. There are two ways forward and only two: set the override
above, or move to a version that carries the fix. Which versions carry
it is answered by
[the releases page](https://github.com/MoranWeissman/sharko/releases).

**What the fix does, so you know what to expect after moving.** For
Sharko's **own built-in** catalogue the ref check is replaced by a
stricter one: the certificate must name the exact commit the running
binary was built from. See
[Release-commit binding](#release-commit-binding-built-in-catalogue-only).
Catalogues you fetch from `SHARKO_CATALOG_URLS` or from
`configuration/marketplace-sources.yaml` are not affected by that
change — everything on this page about them still applies as written.

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
      source=redacted
      reason="signature verified but identity not in trust policy: <subject>"
  ```

  ```
  level=WARN msg="catalog signature verification failed"
      source=redacted
      reason="cert-claim assertion failed: workflow_ref \"<actual>\" does not match policy \"<configured>\""
  ```

  ```
  level=WARN msg="catalog source sidecar verification errored"
  ```

- **No alert fires, and no metric can be made to fire one.** Sharko
  shows per-entry verification in two places only: the badge in the UI
  and the field in the API response. There is **no metric** carrying
  the verified/unverified split. `sharko_catalog_source_entries` exists
  but is labelled by `url` alone — it counts entries per source and
  says nothing about whether they were verified. Watching this failure
  mode means watching the UI or polling the API, not Prometheus.
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
  → the SAN check passed but the workflow_ref claim does
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

The WARN line never names an address — `source` is always the fixed
word `redacted`. List the configured sources with
`GET /api/v1/catalog/sources` (behind a login) to see which row is
unverified; its rows also all read `redacted`, so match by status,
entry count and position against your own configured address list.
Once you know which source it is, capture the OIDC subject (cert
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
WARN line should disappear for the affected source and the
marketplace entry should flip to Verified within one fetch cycle
(default 1h; force-refresh via the admin
`POST /api/v1/catalog/sources/refresh` endpoint to confirm without
waiting).

### 2. Adjust the workflow_ref policy

The SAN matched but `SHARKO_CATALOG_TRUSTED_WORKFLOW_REF` does not
permit the workflow ref the signer ran against. Either widen the
policy or change how your catalog is signed.

Widen the policy (if your release pipeline signs on `main` instead
of tags):

```sh
SHARKO_CATALOG_TRUSTED_WORKFLOW_REF='^refs/heads/(main|release-.*)$'
```

Or accept any ref (escape hatch — this turns the cryptographic
assertion off; do this only if you are intentionally
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

### Cert-claim assertion mismatch

Sharko cryptographically asserts the cert's
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
- The exact WARN line from diagnosis step 1 (including `source`
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
sidecar URL, it:

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
| `^https://github\.com/MoranWeissman/sharko/\.github/workflows/release\.yml@refs/heads/main$` | Sharko's own release workflow. The release pipeline signs the embedded catalog, this default keeps fresh installs showing "Verified" pills on the embedded entries without operator intervention. The SAN anchors to `refs/heads/main` because Fulcio mints `job_workflow_ref` (the workflow file's ref at job start), not the triggering tag — release.yml runs as a `workflow_run`-triggered job whose `job_workflow_ref` is always `refs/heads/main`. Tag-context is enforced cryptographically by `SHARKO_CATALOG_TRUSTED_WORKFLOW_REF` (see below). |

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

## Workflow_ref claim assertion

`SHARKO_CATALOG_TRUSTED_WORKFLOW_REF` adds a cryptographic assertion on
the cert's GitHub `workflow_ref` claim — the Fulcio extension that
records the Git ref the workflow ran against (OID 1.3.6.1.4.1.57264.1.6).
The assertion runs AFTER the SAN regex check passes, so both must match
for an entry to verify.

### Why it matters

In earlier versions the SAN regex was the only narrowing on WHO signed. Sharko's
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
    source=redacted
    reason="cert-claim assertion failed: workflow_ref \"refs/heads/feature-branch\" does not match policy \"^refs/tags/v.*$\""
```

The entry surfaces as Unverified in the UI and on the API — the loader
keeps loading it (no hard fail) so the catalog stays available.

### Validation

The regex is compiled at startup. A malformed pattern is a fatal startup
error with the env var name and the offending pattern in the message —
same posture as `SHARKO_CATALOG_TRUSTED_IDENTITIES`.

### Which ref each catalogue is held to

The default above, `^refs/tags/v.*$`, is what a **third-party** catalogue
is held to, and it has not changed. Sharko's own built-in catalogue is
held to `^refs/heads/main$` instead, because that is the ref its own
release certificates actually carry — see
[Read this first](#read-this-first-if-v401-shows-every-built-in-entry-as-unverified)
for why, and the section below for the check that replaced the tag
requirement.

If you set `SHARKO_CATALOG_TRUSTED_WORKFLOW_REF` yourself, your value
wins for both, exactly as before. Sharko does not quietly substitute
anything over a setting you made on purpose.

## Release-commit binding (built-in catalogue only)

Sharko's built-in catalogue is signed by Sharko's own release workflow,
from the same commit the binary was built from. That makes a much
tighter check possible than "some trusted identity signed something":
**the certificate must name the exact commit this binary was released
from.**

### What it compares, and why both sides are needed

| Side | Where it comes from |
|---|---|
| the claim | the certificate's `sourceRepositoryDigest` field (OID `1.3.6.1.4.1.57264.1.13`), falling back to the older `githubWorkflowSHA` (OID `1.3.6.1.4.1.57264.1.3`). Both sit inside the signed certificate, so only Fulcio can set them. |
| the expectation | the commit stamped into the binary at build time by the release pipeline (`-X main.commit`). Fixed at link time, so nothing the certificate says can move it. |

Two things it is deliberately **not**:

- it is **not** the certificate compared with itself. If both sides came
  off the certificate the check would always pass and prove nothing.
- it is **not** the current tip of `main`. A release commit is normally an
  ancestor of `main` by the time anybody runs the binary, so using the
  tip would refuse the genuine catalogue of every release except the
  newest commit in the repository.

### What it accepts and refuses

- the certificate names this build's release commit → the entry verifies.
- the certificate names a **different** commit → refused, and the log
  names both commits. This covers the case the check exists for: a
  genuine, fully valid signature made from another commit — an earlier
  release's catalogue, or one signed later on `main` — is still not
  *this* release's catalogue.
- the certificate carries **no** commit claim → refused, naming both
  fields that were looked for.
- **this build carries no release commit** → refused. A development build
  (`go build` with no link flags, or `make build`, which stamps an
  abbreviated hash) has no release commit, and neither does a container
  image built without the `COMMIT` build argument. Sharko refuses rather
  than skipping: there is no version of this check that quietly passes
  because there was nothing to compare against.

The comparison is on full 40-character hashes and is case-insensitive.
An abbreviated hash never satisfies it — two commits can share a prefix,
and both sides of this comparison hold full hashes anyway.

### What a developer sees

Nothing changes for an ordinary development build. The `catalog/addons.yaml`
in the repository carries no signatures at all, so there is nothing for
the verifier to check and nothing to refuse. A development build that
does load a signed catalogue gets its entries marked unverified with this
line at startup, which says plainly what is missing:

```
level=WARN msg="this build carries no release commit, so its own embedded
    catalog entries cannot be bound to a release and will surface as
    unverified; a release binary is stamped with the commit it was built from"
    build_commit=dev
```

A release binary logs the other side of it at INFO, naming the commit it
will require:

```
level=INFO msg="embedded catalog signatures are bound to this build's release commit"
    release_commit=<40-character hash>
```

### Third-party catalogues are not affected

The binding applies to the built-in catalogue and to nothing else. A
third-party publisher signs their catalogue from their own repository at
their own commit, which has no relationship to Sharko's release commit
and never will — so requiring a match there would refuse every
third-party signature there has ever been. The separation is in the
code's shape rather than in a rule somebody has to remember: the two
catalogues are handed two separate policies, and the one carrying the
binding can only be produced by the constructor the built-in catalogue's
loader uses.

There is no environment variable that switches the binding off. A build
either carries a release commit or it does not.

### Failure mode

```
level=WARN msg="catalog signature verification failed"
    source=redacted
    reason="release-commit binding failed: certificate sourceRepositoryDigest
        (OID 1.3.6.1.4.1.57264.1.13) claims source commit <a> but this build
        was released from commit <b>"
```

The entry surfaces as Unverified in the UI and on the API; the loader
keeps loading it, so the catalog stays available.

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

This matches the `SHARKO_CATALOG_URLS` posture: misconfiguration
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

## How catalog signing arrived

Every piece below ships today. The order is here because it explains why
the settings are shaped the way they are.

- **Per-entry verification**, with `verified` and `signature_identity`
  fields on every catalog endpoint.
- **`SHARKO_CATALOG_TRUSTED_IDENTITIES`**, with `<defaults>` magic-token
  semantics — the subject of this page.
- **The verified badge in the UI**, plus a "Signed only" filter on the
  browse surface.
- **The Sharko release pipeline signing the embedded catalog**, so the
  second default identity has signatures to verify against.
- **`SHARKO_CATALOG_TRUSTED_WORKFLOW_REF`**, a cert-claim assertion on
  top of the SAN regex. The default `^refs/tags/v.*$` cryptographically
  pins trust to tag-built signatures.
- **Future:** hot reload, per-source policy overrides, Settings-page
  exposure of the policy.
