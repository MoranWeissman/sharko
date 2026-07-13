# Third-party Catalog Sources

> **Reference page, not a runbook.** This page documents the
> third-party catalog source configuration surface
> (`SHARKO_CATALOG_URLS`, the git-native `configuration/marketplace-sources.yaml`, the SSRF guard, refresh cadence). If you are
> diagnosing a fetcher failure, search
> [`failure-mode-index.md`](failure-mode-index.md) — the failures that
> surface during catalog source fetch are covered by
> [`catalog-source-http-fetch-failed.md`](catalog-source-http-fetch-failed.md),
> [`catalog-source-schema-validation-failed.md`](catalog-source-schema-validation-failed.md),
> and the SSRF-blocked variant tracked in the index. For the trust-policy
> half of catalog sources, see
> [`catalog-trust-policy.md`](catalog-trust-policy.md).

Sharko ships with an embedded curated addon catalog. Starting with **v1.23**
operators can extend it with one or more HTTPS-served catalog
YAML files — for internal charts, partner catalogs, or org-specific
curation — without forking Sharko. Starting with **v3.0.0**, you can declare these sources in a git-native file (`configuration/marketplace-sources.yaml`) instead of (or in addition to) the `SHARKO_CATALOG_URLS` environment variable.

The embedded catalog is always the baseline. Third-party catalogs are
additive, and the embedded entry wins on a name collision.

## Configuration Options

Sharko reads third-party catalog sources from two places (you can use one or both):

1. **Environment variable** `SHARKO_CATALOG_URLS` — comma-separated list of HTTPS URLs. Use this for private/tokened sources or temporary test sources.
2. **Git file** `configuration/marketplace-sources.yaml` — enveloped YAML with `spec.sources[]`. Use this for public/tokenless sources your team should track in GitOps. See [Marketplace Sources Configuration](marketplace-sources-config.md) for the schema and examples.

At startup and on every refresh interval, Sharko merges both lists (deduplicating by canonical URL) and fetches each source.

### Environment variables

| Variable | Required | Default | Notes |
|----------|----------|---------|-------|
| `SHARKO_CATALOG_URLS` | No | *(unset)* | Comma-separated list of HTTPS URLs pointing at catalog YAML files. Unset or empty = **no env-var sources** (git file or embedded-only mode). |
| `SHARKO_CATALOG_REFRESH_INTERVAL` | No | `1h` | Go duration (e.g. `30m`, `2h`). Bounded to `[1m, 24h]`. Values outside that range fail at startup. Applies to both env-var and git-file sources. |
| `SHARKO_CATALOG_URLS_ALLOW_PRIVATE` | No | `false` | Set to `true` only on trusted networks (home-lab, dev) to disable the SSRF guard. **See the warning below.** Applies to both env-var and git-file sources. |

## Example

```bash
# Production — public HTTPS catalog served by your org.
SHARKO_CATALOG_URLS=https://catalogs.example.com/sharko/addons.yaml
SHARKO_CATALOG_REFRESH_INTERVAL=30m
```

Multiple sources are comma-separated:

```bash
SHARKO_CATALOG_URLS=https://catalogs.example.com/internal.yaml,https://catalogs.example.com/partner.yaml
```

Via Helm (example fragment for `values.yaml`):

```yaml
env:
  - name: SHARKO_CATALOG_URLS
    value: https://catalogs.example.com/sharko/addons.yaml
  - name: SHARKO_CATALOG_REFRESH_INTERVAL
    value: 1h
```

## HTTPS-only rule

Sharko refuses to load anything that is not `https://`:

- `http://…` → rejected at startup.
- `file:///…` → rejected at startup.
- Any other scheme → rejected at startup.

The rationale is straightforward — Sharko pulls the catalog on behalf of
every user in the cluster, and a plaintext catalog channel would let an
on-path attacker swap chart repositories or images. If you need to serve
a catalog internally, terminate TLS in front of the catalog file (internal
load balancer, ingress, object-store pre-signed URL, etc.) and point
`SHARKO_CATALOG_URLS` at the HTTPS endpoint.

## SSRF guard

At startup Sharko validates every configured URL against a blocklist of
host ranges that a third-party catalog URL has no legitimate reason to
point at:

- Loopback: `127.0.0.0/8`, `::1`
- RFC1918 private: `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`
- IPv6 unique-local: `fc00::/7`
- Link-local: `169.254.0.0/16`, `fe80::/10`
- Unspecified: `0.0.0.0`, `::`

Literal IPs in the URL are checked directly; hostnames are resolved via
DNS and every resulting address is checked. If any match, the URL is
rejected with a startup error. DNS failures are fail-open (the runtime
fetcher will retry), so catalog hosts that are temporarily unresolvable
do not block Sharko from starting — but a statically private-IP URL in
your config will.

The guard folds in a v1.22 code-review concern: without it, a privileged
catalog puller running inside the cluster could be aimed at the EC2 IMDS
endpoint (`169.254.169.254`), an internal ArgoCD API, or a sibling
Sharko instance, and manifest poisoning would become trivial.

### Escape hatch (home-lab / dev)

For operators running Sharko against a home-lab or dev cluster where the
catalog genuinely lives at an RFC1918 address, set:

```bash
SHARKO_CATALOG_URLS_ALLOW_PRIVATE=true
```

!!! danger
    Only enable this on a network you fully trust. Disabling the SSRF
    guard lets any catalog URL point at cluster-local or cloud-metadata
    endpoints, which opens a manifest-injection path if the catalog URL
    is ever supplied by an untrusted party. Sharko logs a WARN on every
    startup when this flag is enabled.

## Refresh cadence

- Minimum: `1m` — sub-minute refresh would hammer the upstream on every
  tick without changing anything meaningful.
- Maximum: `24h` — staler than a day defeats the point of having a
  refresh loop at all; fall back to a redeploy for catalogs that update
  on a weekly cadence.
- Default: `1h`.

Values outside the `[1m, 24h]` range cause a startup error so the
operator notices the misconfiguration immediately.

## What happens when both env and git file are absent?

Sharko logs `no third-party catalogs configured, using embedded only`
and skips the fetch loop entirely. The embedded curated catalog is the
sole source of truth. No startup error.

## Related Pages

- [Marketplace Sources Configuration](marketplace-sources-config.md) — git-native `configuration/marketplace-sources.yaml` schema and usage
- [Marketplace Architecture](marketplace-architecture.md) — three-layer model (Marketplace / Catalog / Enablement)
- [Catalog Scan Runbook](../developer-guide/catalog-scan-runbook.md) — discovery bot that proposes new addons from configured sources
