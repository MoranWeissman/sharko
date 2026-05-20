# Catalog Trust Policy

Sharko verifies catalog signatures with [Sigstore](https://www.sigstore.dev/)
keyless signing (Fulcio short-lived certs + Rekor transparency log). The
**trust policy** decides which signing identities Sharko accepts as
"verified" — entries signed by an identity outside the policy still load,
but they surface as **Unverified** in the UI and on the API.

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

## Troubleshooting

### Entries show as "Unverified" in the UI

Three possible causes:

1. **The entry is unsigned** — no `signature.bundle` in the catalog YAML.
   The default for unsigned entries is `verified: false`; Sharko loads
   them anyway (so the catalog still works), but the UI clearly labels
   them. Action: ask the catalog publisher to sign the entry, or
   accept the Unverified state if you trust the source for other reasons.
2. **The signature is invalid** — bundle bytes are corrupt, the cert
   chain doesn't validate, or the Rekor inclusion proof is missing.
   Sharko logs a `WARN` line under component `catalog-signing` with the
   reason. The URL is logged as a 10-character SHA-256 fingerprint
   (`source_fp`) — never the raw URL — to avoid leaking auth tokens
   embedded in catalog paths.
3. **The signature is valid but the identity isn't trusted.** Sharko
   logs:

   ```
   level=WARN msg="catalog signature verification failed"
       source_fp=<10-char hex>
       reason="signature verified but identity not in trust policy: <subject>"
   ```

   Action: add the subject (or an anchoring regex) to
   `SHARKO_CATALOG_TRUSTED_IDENTITIES` and restart.

### How do I see what identities are loaded?

Sharko logs a single startup line:

```
level=INFO msg="catalog trust policy loaded" identity_count=2
```

The raw patterns are intentionally **not logged** — they aren't secrets,
but they can leak org structure in shared logs (think internal repo
names, partner org slugs). The count is enough for ops triage; the
authoritative pattern list is the env var the operator set.

### How do I make Sharko trust nothing?

Set `SHARKO_CATALOG_TRUSTED_IDENTITIES=^$`. The regex `^$` matches the
empty string only — no real OIDC subject is empty, so every signed entry
surfaces as Unverified. This is the documented escape hatch for the
"audit-only, trust no one" posture.

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
