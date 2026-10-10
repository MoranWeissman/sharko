# Extra Marketplace sources

Sharko comes with a built-in list of addons for the Marketplace. You can
add more addons to the Marketplace from your own lists, for example a
list of your company's internal charts, or a list a partner gives you.
You do not need to change Sharko to do this.

An extra source is an `https://` address of a catalog file in Sharko's
own format: the same format as Sharko's built-in list,
[`catalog/addons.yaml`](https://github.com/MoranWeissman/sharko/blob/main/catalog/addons.yaml).
A plain Helm chart repository index (`index.yaml`) is not a source.
Sharko cannot read it.

Adding a source only adds addons to the Marketplace, where people can
look at them. Nothing from a source runs anywhere until someone adds it
to your Catalog through a reviewed pull request. See
[Marketplace and Catalog](marketplace-architecture.md).

## How the extra entries fit with the built-in list

- The built-in list is always there. Extra sources only add to it.
- If an extra source has an addon with the same name as a built-in one,
  the built-in one is shown.
- If two extra sources have an addon with the same name, the one from
  the source whose address comes first in alphabetical order is shown.
- If a source cannot be downloaded or read, its entries are left out
  until it works again. The rest of the Marketplace keeps working.

## Where to list your sources

There are two places. Sharko uses only one of them:

1. **The Git file** `configuration/marketplace-sources.yaml` in your
   GitOps repository. Good for sources with no password in the address,
   because changes go through pull requests. See
   [The marketplace-sources.yaml file](marketplace-sources-config.md).
2. **The setting** `SHARKO_CATALOG_URLS`. Good for sources that need a
   token, and for trying a source out.

When Sharko starts, it first looks for the Git file. If Git is connected
at that moment and the file is there, Sharko uses the file and ignores
`SHARKO_CATALOG_URLS`. If Git is not connected yet, or the file is not
there, Sharko uses `SHARKO_CATALOG_URLS`. The two lists are never
combined.

Sharko reads the list only when it starts. After you change either one,
restart Sharko:

```sh
kubectl rollout restart deployment/sharko -n sharko
```

If anything in the list is wrong, for example an address that is not
`https://`, Sharko does not start. The error says which entry is wrong
by its position in the list, such as "the 2nd source". It does not print the
address, because an address can hold a token.

If neither place lists any source, Sharko logs
`no third-party catalogs configured, using embedded only` and shows only
the built-in list. That is not an error.

## The settings

| Setting | Default | What it does |
|---|---|---|
| `SHARKO_CATALOG_URLS` | not set | A comma-separated list of `https://` addresses of catalog files. |
| `SHARKO_CATALOG_REFRESH_INTERVAL` | `1h` | How often Sharko downloads each source again, for example `30m` or `2h`. Must be between `1m` and `24h`, or Sharko does not start. |
| `SHARKO_CATALOG_URLS_ALLOW_PRIVATE` | `false` | Set to `true` to turn off the private-address check below. Only on a network you fully trust. |

These settings are used only when the sources come from
`SHARKO_CATALOG_URLS`. When Sharko uses the Git file, the refresh time
comes from the file, and the private-address check is always on.

Set them with `extraEnv` in your Helm values:

```yaml
extraEnv:
  - name: SHARKO_CATALOG_URLS
    value: "https://catalogs.example.com/internal.yaml,https://catalogs.example.com/partner.yaml"
  - name: SHARKO_CATALOG_REFRESH_INTERVAL
    value: "30m"
```

## Sources that need a token

Some catalog hosts need a token to download the file. With Sharko, the
token has to be part of the address path, for example:

```
https://catalogs.example.com/t/<your-token>/addons.yaml
```

Sharko throws away three parts of every address before it uses it: a
user name and password written before the host (`https://user:pass@host`),
anything after a `?`, and anything after a `#`. So a token in any of
those places is lost, and the download is made without it.

Keep a tokened address out of Git. Put it in a Kubernetes Secret and pass
it in with `extraEnv`:

```sh
kubectl create secret generic sharko-catalog-urls -n sharko \
  --from-literal=urls='https://catalogs.example.com/t/<your-token>/addons.yaml'
```

```yaml
extraEnv:
  - name: SHARKO_CATALOG_URLS
    valueFrom:
      secretKeyRef:
        name: sharko-catalog-urls
        key: urls
```

Sharko never writes a source address to its logs, and never sends one
back from the API. In logs and in `GET /api/v1/marketplace/sources`,
every extra source shows as the word `redacted`.

## Only https:// addresses

Sharko refuses any address that does not start with `https://`.
`http://`, `file://` and every other kind are refused when Sharko starts.

Sharko downloads catalogs for everyone who uses it. Over plain `http://`,
anyone on the network path could change the file on the way, for example
to point an addon at a different chart. If your catalog file is only on
an internal server, put HTTPS in front of it (an internal load balancer,
an ingress, or a storage service with HTTPS addresses) and use that
address.

## The private-address check {#ssrf-guard}

When Sharko starts, it checks that no source points at an address inside
your own network. This stops someone who can change the source list from
using Sharko to reach machines that should not be reachable from
outside. (This kind of attack is called SSRF: making a server send
requests for you.) Sharko refuses addresses in these ranges:

- the machine itself: `localhost`, `127.0.0.0/8`, `::1`;
- private networks: `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`,
  `fc00::/7`;
- link-local addresses: `169.254.0.0/16`, `fe80::/10`. This includes
  `169.254.169.254`, the address where cloud machines get their own
  passwords and keys (on AWS it is called IMDS);
- empty addresses: `0.0.0.0`, `::`.

If the address uses a host name, Sharko looks up the name and checks
every address it gets back. If the lookup fails, Sharko still starts and
tries the download later. An address that is clearly private always
stops startup.

### Turning the check off on a home lab

If your catalog really is on a private address, for example in a home
lab, set:

```yaml
extraEnv:
  - name: SHARKO_CATALOG_URLS_ALLOW_PRIVATE
    value: "true"
```

!!! danger
    Only do this on a network you fully trust. With the check off, a
    source can point at anything inside your cluster or at your cloud
    machine's password address. Sharko logs a warning every time it
    starts with this on. It works only for `SHARKO_CATALOG_URLS`. The Git
    file always keeps the check on.

## Checking your sources

To see each source and whether it works, call
`GET /api/v1/marketplace/sources` (you need to be logged in). The first
row is always the built-in list. Each extra source has one row with:

- `url`: always `redacted`;
- `status`: `ok`, `stale` or `failed`;
- `last_fetched`: when it last downloaded without errors;
- `entry_count`: how many addons it adds;
- `verified`: whether its signature was checked and trusted. See
  [Marketplace signature settings](catalog-trust-settings.md).

To download every source again now, an admin can call
`POST /api/v1/marketplace/sources/refresh`. This does not re-read the
list of sources. For that, restart Sharko.

## Related pages

- [The marketplace-sources.yaml file](marketplace-sources-config.md)
- [Marketplace and Catalog](marketplace-architecture.md)
- [Marketplace signature settings](catalog-trust-settings.md)
- [Fixing Unverified Marketplace entries](catalog-trust-policy.md)
- [A catalog source cannot be downloaded](catalog-source-http-fetch-failed.md)
- [A catalog source fails its format check](catalog-source-schema-validation-failed.md)
