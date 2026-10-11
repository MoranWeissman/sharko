# A third-party catalog can't be downloaded

Sharko tried to download one of your third-party addon catalogs and the download did not work. Sharko keeps running, and it keeps the entries from the last good download, if there was one.

**How urgent:** Low. Only the addons from that one catalog are missing or out of date. Everything else keeps working.

Before you start, set up the shell variables from [First checks](troubleshooting.md#set-up-once).

## What you see

- In the UI, **Settings > Catalog Sources** shows the source with the status `stale` or `failed`. `stale` means it worked before and the last try did not. `failed` means it has not worked once since Sharko started.
- In the Marketplace, addons from that catalog are missing (when the status is `failed`) or not updated (when it is `stale`).
- The logs have this warning:

  ```sh
  kubectl -n "$SHARKO_NS" logs deploy/sharko --since=1h | grep 'catalog source fetch failed'
  ```

  The line looks like this:

  ```json
  {"level":"WARN","msg":"catalog source fetch failed","source":"redacted","err":"dns chain=*url.Error>*net.OpError>*net.DNSError"}
  ```

  The `source` field always says `redacted`. Sharko never writes the address of a source into the logs.

  The `err` field is a short label, not the full error text. The first word tells you what kind of problem it was:

  | First word | What it means |
  |---|---|
  | `dns` | The host name could not be looked up. |
  | `connection-refused` | The server is there but refused the connection. |
  | `timeout` | The download took longer than 30 seconds. |
  | `connection-reset` | The connection was cut in the middle. |
  | `host-unreachable`, `network-unreachable` | Sharko's network can't get to that address. |
  | `eof` | The server closed the connection too early. |
  | `unclassified` | Anything else. This includes a server that answered with an error code such as 404 or 503. That shows as `unclassified chain=*errors.errorString`, not as the number. |

- If the address leads into a private or local network, you see a different warning instead:

  ```sh
  kubectl -n "$SHARKO_NS" logs deploy/sharko --since=1h | grep 'catalog source blocked by runtime SSRF guard'
  ```

  That means Sharko refused to connect, on purpose, because the address leads into a private network.

- You can also ask the API for the list of sources:

  ```sh
  curl -s -H "Authorization: Bearer $SHARKO_TOKEN" "$SHARKO_URL/api/v1/marketplace/sources" | jq
  ```

  The first row is always the built-in catalog (`"url": "embedded"`). Every other row has `"url": "redacted"`. Those rows are in the order of the source addresses sorted A to Z, so you can match them with your own list. Look at `status`, `last_fetched` and `entry_count`. The API does not return the error text.

- The metric `sharko_catalog_source_fetch_total` goes up with `status="stale"` or `status="failed"`. Its `url` label is always `redacted`, so all third-party sources share one line. Sharko does not ship an alert for this.

## Why it happens

Sharko downloads each third-party catalog when it starts, and then again on a timer (`SHARKO_CATALOG_REFRESH_INTERVAL`, one hour by default). Each download is one HTTPS request. It must finish within 30 seconds, and the file must be smaller than 8 MiB. You can't change these limits.

The usual reasons a download fails:

- **A network rule blocks it.** A Kubernetes NetworkPolicy or a firewall does not let the Sharko pod reach that host.
- **A company proxy opens your HTTPS traffic** and signs it with its own certificate. Sharko does not trust that certificate. See [Corporate TLS inspection](corporate-mitm-tls.md).
- **The server moved, is down, or returns an error** such as 404 or 503.
- **The server is too slow**, and the download takes more than 30 seconds.
- **The address leads into a private network.** Sharko blocks private, loopback and link-local addresses unless you allow them with `SHARKO_CATALOG_URLS_ALLOW_PRIVATE=true`. Only do that on a network you trust.

## How to fix it

### 1. Find the addresses you set

The logs and the API hide the addresses, so look at your own settings. Sharko takes its list of sources from one of two places:

- **A file in your Git repo**, `configuration/marketplace-sources.yaml`. Sharko uses this file first, when Git is connected and the file exists. At startup the logs say `loaded marketplace sources from git file`.
- **The environment variable `SHARKO_CATALOG_URLS`**, a list of `https://` addresses split by commas. At startup the logs say `loaded catalog sources from env`.

To see the environment variable:

```sh
kubectl -n "$SHARKO_NS" get deploy sharko \
  -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="SHARKO_CATALOG_URLS")].value}'; echo
```

### 2. Try the download yourself

From your own machine (use your real address in place of the example):

```sh
curl -sS -o /dev/null -w '%{http_code}\n' --max-time 30 "https://catalog.example.com/addons.yaml"
```

- `200` means the file is there. If Sharko still can't get it, the problem is the network path from the cluster. Go to step 3.
- `404` means the file moved. Get the new address from the catalog's owner and go to step 4.
- `5xx` means the server is having trouble. Wait, or take the source out for now (see [Remove the source](#remove-the-source)).
- A timeout or a certificate error means the server is slow, or its certificate is not trusted.

### 3. Try the download from inside the cluster

Your machine and the cluster may be on different networks. You can start a short-lived test pod in the same namespace. It deletes itself when it is done:

```sh
kubectl -n "$SHARKO_NS" run catalog-fetch-test --rm -i --restart=Never \
  --image=curlimages/curl -- \
  curl -sS -o /dev/null -w '%{http_code}\n' --max-time 30 "https://catalog.example.com/addons.yaml"
```

This test pod is not the Sharko pod. A NetworkPolicy that only selects Sharko does not apply to it. So if the test pod works and Sharko still fails, look at the NetworkPolicies in the namespace:

```sh
kubectl -n "$SHARKO_NS" get networkpolicy
```

Find the ones that select the pod label `app.kubernetes.io/name=sharko`, and allow outbound HTTPS (port 443) to the catalog host. If the test pod shows a certificate error, follow [Corporate TLS inspection](corporate-mitm-tls.md).

### 4. If the address is wrong, change it

Change the address in the same place you found it in step 1. Then restart Sharko the same way as in [Remove the source](#remove-the-source). Sharko reads the list of sources only at startup.

### 5. Ask Sharko to try again now

After you fix the network, you don't have to wait for the timer. Click **Refresh now** on **Settings > Catalog Sources**, or call the API with an admin token:

```sh
curl -s -X POST -H "Authorization: Bearer $SHARKO_TOKEN" "$SHARKO_URL/api/v1/marketplace/sources/refresh" | jq
```

### Remove the source

Use this when a third-party catalog is broken and you want Sharko to stop using it. Other pages link here.

Sharko reads the list of sources only when it starts, so after any change you need a restart.

**If your sources are in the Git file** `configuration/marketplace-sources.yaml`:

1. Edit the file in your Git repo. Delete the entry under `spec.sources` that you want to remove. Leave the rest of the file as it is (`apiVersion: sharko.dev/v1`, `kind: MarketplaceSources`).
2. Merge the change.
3. Restart Sharko so it reads the file again:

   ```sh
   kubectl -n "$SHARKO_NS" rollout restart deploy/sharko
   kubectl -n "$SHARKO_NS" rollout status deploy/sharko
   ```

**If your sources are in `SHARKO_CATALOG_URLS`:**

The Helm chart has no special setting for catalog sources. You set the variable through `extraEnv`. Write a small values file with the addresses you want to keep:

```yaml
# catalog-sources.yaml
extraEnv:
  - name: SHARKO_CATALOG_URLS
    value: "https://catalog-one.example.com/addons.yaml,https://catalog-two.example.com/addons.yaml"
```

To remove every third-party source, use `value: ""`.

`extraEnv` is a list, and Helm replaces the whole list. So copy every other `extraEnv` entry you already use into this file too. Then upgrade:

```sh
helm -n "$SHARKO_NS" upgrade sharko oci://ghcr.io/moranweissman/sharko/sharko \
  --reuse-values -f catalog-sources.yaml
```

The upgrade changes the pod settings, so Kubernetes restarts Sharko by itself.

The **Settings > Catalog Sources** page can't edit this list. The page says so too.

## How to check it's fixed

1. The status is `ok`:

   ```sh
   curl -s -H "Authorization: Bearer $SHARKO_TOKEN" "$SHARKO_URL/api/v1/marketplace/sources" | jq '.[] | {url, status, last_fetched, entry_count}'
   ```

   If you removed the source, it is no longer in the list.

2. There are no new warnings in the last few minutes:

   ```sh
   kubectl -n "$SHARKO_NS" logs deploy/sharko --since=10m | grep 'catalog source fetch failed'
   ```

3. The addons from that catalog show up in the Marketplace again.

## If that didn't work

Open an issue at <https://github.com/MoranWeissman/sharko/issues>. Include:

- the Sharko version (`sharko version`, or the `version` field from `/api/v1/health`)
- the log lines from the last hour that mention `catalog source`
- what you tried from this page

Please don't paste the source addresses if they are private.

Related pages:

- [Third-party catalog sources](catalog-sources.md)
- [A third-party catalog fails the format check](catalog-source-schema-validation-failed.md)
- [Corporate TLS inspection](corporate-mitm-tls.md)
- [First checks](troubleshooting.md)
