# Third-party Catalog Sources

Sharko ships with an embedded curated addon catalog. Starting with **v1.23**
operators can extend it with one or more private HTTPS-served catalog
YAML files — for internal charts, partner catalogs, or org-specific
curation — without forking Sharko.

The embedded catalog is always the baseline. Third-party catalogs are
additive, and the embedded entry wins on a name collision.

## Environment variables

| Variable | Required | Default | Notes |
|----------|----------|---------|-------|
| `SHARKO_CATALOG_URLS` | No | *(unset)* | Comma-separated list of HTTPS URLs pointing at catalog YAML files. Unset or empty = **embedded-only mode** (no fetch loop runs). |
| `SHARKO_CATALOG_REFRESH_INTERVAL` | No | `1h` | Go duration (e.g. `30m`, `2h`). Bounded to `[1m, 24h]`. Values outside that range fail at startup. |
| `SHARKO_CATALOG_URLS_ALLOW_PRIVATE` | No | `false` | Set to `true` only on trusted networks (home-lab, dev) to disable the SSRF guard. **See the warning below.** |

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

## What happens when the env is unset?

Sharko logs `no third-party catalogs configured, using embedded only`
and skips the fetch loop entirely. The embedded curated catalog is the
sole source of truth. No startup error.

## What lands in v1.23 vs later

- **v1.23 — Story V123-1.1 (this page):** parse + validate +
  stash the config on the server. No fetcher yet, no UI.
- **v1.23 — Story V123-1.2:** fetch loop + YAML validation +
  last-successful snapshot + `GET /api/v1/catalog/sources`.
- **v1.23 — Story V123-1.3+:** source attribution on catalog entries,
  UI tile showing embedded vs third-party provenance.
- **v1.24+:** editable Settings page backed by a ConfigMap (deferred per
  the design doc §7.1 resolution — env-only for v1.23).
