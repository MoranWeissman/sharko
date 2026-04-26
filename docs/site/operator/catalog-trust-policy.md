# Catalog Trust Policy

Sharko verifies catalog signatures with [Sigstore](https://www.sigstore.dev/)
keyless signing (Fulcio short-lived certs + Rekor transparency log). The
**trust policy** decides which signing identities Sharko accepts as
"verified" — entries signed by an identity outside the policy still load,
but they surface as **Unverified** in the UI and on the API.

The policy is configured at startup via a single environment variable:
`SHARKO_CATALOG_TRUSTED_IDENTITIES`.

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
| `^https://github\.com/MoranWeissman/sharko/\.github/workflows/release\.yml@refs/tags/v.*$` | Sharko's own release workflow. From V123-2.5 onwards the release pipeline signs the embedded catalog; this default keeps fresh installs showing "Verified" pills on the embedded entries without operator intervention. |

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
- **Future:** hot reload, per-source policy overrides, Settings-page
  exposure of the policy.
