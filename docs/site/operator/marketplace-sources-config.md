# Marketplace Sources Configuration (git-native)

Starting with **v3.0.0**, Sharko supports declaring third-party Marketplace sources in a Git file instead of (or in addition to) the `SHARKO_CATALOG_URLS` environment variable. This keeps your addon discovery sources in GitOps alongside your catalog and cluster config.

## File Location

```
configuration/marketplace-sources.yaml
```

This file lives in your GitOps repository next to `addons-catalog.yaml` and `managed-clusters.yaml`.

## Schema

The file is an enveloped document with the following structure:

```yaml
apiVersion: sharko.io/v1
kind: MarketplaceSources
metadata:
  name: marketplace-sources
spec:
  sources:
    - url: https://catalogs.example.com/internal-addons.yaml
    - url: https://catalogs.partner.com/curated-charts.yaml
```

- **`apiVersion`** (required): Must be `sharko.io/v1`.
- **`kind`** (required): Must be `MarketplaceSources`.
- **`metadata.name`** (required): Must be `marketplace-sources`.
- **`spec.sources`** (required): A list of source objects. Each source must have a `url` field.
- **`url`** (required): HTTPS URL pointing to a catalog YAML file that conforms to Sharko's catalog schema (see `catalog/schema.json`).

## When to Use the Git File vs the Env Var

| Use Case | Recommended Approach |
|----------|---------------------|
| **Public or tokenless third-party sources** | Git file (`configuration/marketplace-sources.yaml`) |
| **Private sources with auth tokens in the URL** | Env var (`SHARKO_CATALOG_URLS`) |
| **Multiple public sources your team should track** | Git file — reviewable, auditable, versioned with your catalog |
| **Temporary test source (dev/staging only)** | Env var — no Git commit needed |

**CRITICAL: Token-Leak Caveat**

Marketplace source URLs can encode authentication tokens in the path or query string (e.g., `https://catalogs.example.com/addons.yaml?token=abc123`). If you commit a tokenized URL to the git file, **you leak the token into your Git history**. Never commit URLs with embedded secrets.

For private/tokened sources, use the `SHARKO_CATALOG_URLS` environment variable instead. The env var is read from Sharko's runtime environment (Kubernetes Secret, Helm values, or container env) and is not committed to Git.

## How Sharko Reads Sources

At startup and on every refresh interval (default: 1 hour), Sharko:

1. Reads `SHARKO_CATALOG_URLS` env var (comma-separated list of HTTPS URLs).
2. Reads `configuration/marketplace-sources.yaml` from the GitOps repo (if present).
3. Merges both lists (deduplicating by canonical URL).
4. Fetches each source URL and loads the catalog entries into the Marketplace browse surface.

If the git file is absent, Sharko continues using only the env var. If both are absent, Sharko uses the embedded curated catalog only (no third-party sources).

## Validation

Sharko validates every URL at startup:

- Must be `https://` (not `http://`, `file://`, or any other scheme).
- Must not resolve to a private/loopback/link-local address (SSRF guard). See [Third-party Catalog Sources](catalog-sources.md#ssrf-guard) for details.
- Duplicates are collapsed (case-insensitive host, trailing-slash-normalized path).

If any URL fails validation, Sharko logs an error and skips that source. The server continues running with the valid sources.

## Example

```yaml
apiVersion: sharko.io/v1
kind: MarketplaceSources
metadata:
  name: marketplace-sources
spec:
  sources:
    # Internal curated catalog (public HTTPS, no auth)
    - url: https://catalogs.example.com/internal-addons.yaml
    # Partner-provided charts (public HTTPS, no auth)
    - url: https://charts.partner.com/sharko-catalog.yaml
```

To add a new source:

1. Edit `configuration/marketplace-sources.yaml` in your GitOps repo.
2. Open a pull request.
3. Review and merge.
4. Sharko re-reads the file on the next refresh tick (within 1 hour by default) or on restart.

## Backward Compatibility

If you are upgrading from a version without this feature:

- Your existing `SHARKO_CATALOG_URLS` env var continues to work unchanged.
- The git file is optional. You can migrate to it incrementally (or never).
- Both approaches can coexist. Sharko merges the lists at runtime.

## Related Pages

- [Third-party Catalog Sources](catalog-sources.md) — env var reference, SSRF guard, refresh cadence
- [Marketplace Architecture](marketplace-architecture.md) — three-layer model (Marketplace / Catalog / Enablement)
- [Catalog Scan Runbook](../developer-guide/catalog-scan-runbook.md) — discovery bot that proposes new addons
