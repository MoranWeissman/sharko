# A third-party catalog fails the format check

Sharko downloaded one of your third-party addon catalogs, but the file did not pass Sharko's format check. Sharko keeps running, and it keeps the entries from the last good download, if there was one.

**How urgent:** Low. Only the addons from that one catalog are missing or out of date. Everything else keeps working.

Before you start, set up the shell variables from [First checks](troubleshooting.md#set-up-once).

## What you see

- In the UI, **Settings > Catalog Sources** shows the source with the status `failed`.
- In the Marketplace, new addons from that catalog do not show up. If an earlier download of the same catalog worked, its old entries are still there.
- The logs have this warning:

  ```sh
  kubectl -n "$SHARKO_NS" logs deploy/sharko --since=1h | grep 'catalog source schema validation failed'
  ```

  The line looks like this:

  ```json
  {"level":"WARN","msg":"catalog source schema validation failed","source":"redacted","err":"unclassified chain=*errors.errorString"}
  ```

  The `source` field always says `redacted`. Sharko never writes the address of a source into the logs.

  The `err` field is only a short label. It does **not** say what is wrong with the file. To find that out, you check the file yourself (see [How to fix it](#how-to-fix-it)).

- The API list of sources shows the same status:

  ```sh
  curl -s -H "Authorization: Bearer $SHARKO_TOKEN" "$SHARKO_URL/api/v1/marketplace/sources" | jq '.[] | {url, status, last_fetched, entry_count}'
  ```

  The first row is the built-in catalog (`"url": "embedded"`). Every other row has `"url": "redacted"`. Those rows are in the order of the source addresses sorted A to Z, so you can match them with your own list. The API does not return the error text.

- The metric `sharko_catalog_source_fetch_total` goes up with `status="failed"`. Sharko does not ship an alert for this.

If the download itself fails (no file comes back at all), you are on the wrong page. See [A third-party catalog can't be downloaded](catalog-source-http-fetch-failed.md).

If Sharko does not start at all because of its own built-in catalog, see [Sharko won't start because its built-in catalog is broken](catalog-parse-failure-on-startup.md).

## Why it happens

A third-party catalog is a YAML file with a list of addons under `addons:`. Sharko checks every entry with the same rules it uses for its own built-in catalog. If one entry breaks a rule, the whole file is turned down.

Common reasons:

- **A required field is missing.** Every entry needs `name`, `description`, `chart`, `repo`, `default_namespace`, `license`, at least one `maintainers` entry, `category` and `curated_by`.
- **The category is not one Sharko knows.** Allowed values: `security`, `observability`, `networking`, `autoscaling`, `gitops`, `storage`, `database`, `backup`, `chaos`, `developer-tools`.
- **A `curated_by` tag is not one Sharko knows**, or the same tag is listed twice. Allowed values: `cncf-graduated`, `cncf-incubating`, `cncf-sandbox`, `aws-eks-blueprints`, `azure-aks-addon`, `gke-marketplace`, `artifacthub-verified`, `artifacthub-official`.
- **The `repo` is not a web address.** It must start with `http://`, `https://` or `oci://`.
- **The signature part is incomplete.** When an entry has `signature`, it also needs `signature.bundle`, and that must be an `https://` address.
- **`security_score` has a bad value.** It must be a number from 0 to 10, or the word `unknown`.
- **Two entries have the same name.**
- **The address does not point at the YAML file.** For example it points at a web page, a folder listing or a login page. Then the file is not YAML at all.
- **The file is empty**, or has no entries under `addons:`.

## How to fix it

### 1. Find the address

The logs and the API hide the addresses, so look at your own settings. Sharko takes its list from the Git file `configuration/marketplace-sources.yaml` (if it exists), or else from the `SHARKO_CATALOG_URLS` environment variable. To see the variable:

```sh
kubectl -n "$SHARKO_NS" get deploy sharko \
  -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="SHARKO_CATALOG_URLS")].value}'; echo
```

### 2. Download the file and check it

From your own machine (use your real address in place of the example):

```sh
curl -sS -o third-party-catalog.yaml "https://catalog.example.com/addons.yaml"
head -20 third-party-catalog.yaml
```

If the start of the file is HTML or a login page, the address is wrong. Get the right address from the catalog's owner.

If it looks like YAML, run Sharko's own check on it. The `sharko` command line tool runs the same rules as the server:

```sh
sharko validate-catalog third-party-catalog.yaml
```

It prints `OK   third-party-catalog.yaml` when the file is good. Otherwise it prints `FAIL third-party-catalog.yaml:` and then the exact problem, for example:

```text
FAIL third-party-catalog.yaml: catalog: entry #3 (name="my-addon"): category "monitoring" is not in the allowed set (see catalog/schema.json)
```

### 3. Get the file fixed

The file belongs to whoever publishes that catalog. Send them the line from step 2. When they publish a fixed file, ask Sharko to try again. Click **Refresh now** on **Settings > Catalog Sources**, or call the API with an admin token:

```sh
curl -s -X POST -H "Authorization: Bearer $SHARKO_TOKEN" "$SHARKO_URL/api/v1/marketplace/sources/refresh" | jq
```

You don't need a restart for this, because the address did not change.

### 4. If you can't wait, take the source out

Follow [Remove the source](catalog-source-http-fetch-failed.md#remove-the-source). Add it back when the file is fixed.

## How to check it's fixed

1. `sharko validate-catalog` prints `OK` for a fresh copy of the file.
2. The source shows `ok` and has entries:

   ```sh
   curl -s -H "Authorization: Bearer $SHARKO_TOKEN" "$SHARKO_URL/api/v1/marketplace/sources" | jq '.[] | {url, status, entry_count}'
   ```

3. There are no new warnings in the last few minutes:

   ```sh
   kubectl -n "$SHARKO_NS" logs deploy/sharko --since=10m | grep 'catalog source schema validation failed'
   ```

4. The new addons from that catalog show up in the Marketplace.

## If that didn't work

Open an issue at <https://github.com/MoranWeissman/sharko/issues>. Include:

- the Sharko version (`sharko version`, or the `version` field from `/api/v1/health`)
- the log lines from the last hour that mention `catalog source`
- the output of `sharko validate-catalog` on the file
- what you tried from this page

Please don't paste the source address if it is private.

Related pages:

- [Third-party catalog sources](catalog-sources.md)
- [A third-party catalog can't be downloaded](catalog-source-http-fetch-failed.md)
- [Catalog trust policy](catalog-trust-policy.md), if your problem is that entries show as not verified
- [First checks](troubleshooting.md)
