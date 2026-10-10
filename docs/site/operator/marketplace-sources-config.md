# The marketplace-sources.yaml file

You can list your extra Marketplace sources in a file in your GitOps
repository, instead of in the `SHARKO_CATALOG_URLS` setting. Then adding
or removing a source goes through a pull request like any other change.

This page covers only the file. What a source is, how tokens work, and
the address checks are all explained in
[Extra Marketplace sources](catalog-sources.md).

## Where it goes

```
configuration/marketplace-sources.yaml
```

The path is from the top of your GitOps repository. Sharko reads it from
the base branch of your Git connection.

## What it looks like

```yaml
apiVersion: sharko.dev/v1
kind: MarketplaceSources
metadata:
  name: marketplace-sources
spec:
  refreshInterval: 1h
  sources:
    - url: https://catalogs.example.com/internal-addons.yaml
    - url: https://charts.partner.example.com/sharko-catalog.yaml
```

| Field | Needed | What it is |
|---|---|---|
| `apiVersion` | yes | `sharko.dev/v1`. The older `sharko.io/v1` is still read. |
| `kind` | yes | `MarketplaceSources` |
| `metadata.name` | yes | Any name. `marketplace-sources` is the usual one. |
| `spec.sources` | yes | The list of sources. Each one has a `url`: the `https://` address of a catalog file in Sharko's format. |
| `spec.refreshInterval` | no | How often Sharko downloads each source again. Default `1h`. Must be between `1m` and `24h`. |

## Never put a token in this file

Anything in this file is in your Git history for good, even after you
delete it. If a source needs a token, use `SHARKO_CATALOG_URLS` with a
Kubernetes Secret instead. See
[Sources that need a token](catalog-sources.md#sources-that-need-a-token).

## When Sharko reads it

Sharko reads this file once, when it starts, and only if Git is already
connected at that moment. So:

- After you merge a change to the file, restart Sharko:

  ```sh
  kubectl rollout restart deployment/sharko -n sharko
  ```

- If you connect Git for the first time after Sharko started, Sharko does
  not read the file until the next restart.

If the file is there, Sharko uses it and ignores `SHARKO_CATALOG_URLS`
completely. The two lists are never combined.

## When the file is wrong

If the file has a mistake, Sharko does not start. That includes a
missing field, an address that is not `https://`, or an address inside
your own network. The error names the file and which entry is wrong, by
its position. It does not print the address.

The private-address check is always on for this file.
`SHARKO_CATALOG_URLS_ALLOW_PRIVATE` does not change it. If two entries
are the same address, Sharko uses it once.

## Related pages

- [Extra Marketplace sources](catalog-sources.md)
- [Marketplace and Catalog](marketplace-architecture.md)
