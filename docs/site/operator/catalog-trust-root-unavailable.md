# Signed catalog entries all show as not verified

When Sharko starts, it downloads a small file from Sigstore that it needs to check catalog signatures. If that download fails, Sharko still starts, but it can't check any signature. Every signed catalog entry then shows as not verified.

**How urgent:** Medium. Addons still work. Nothing is blocked. But "verified" no longer tells you anything until you fix this.

Before you start, set up the shell variables from [First checks](troubleshooting.md#set-up-once).

## What you see

- In the Marketplace, the **Signature** filter has a **Signed only** box with a count next to it. That count drops to 0.
- The API shows no verified entries, even though some entries are signed:

  ```sh
  curl -s -H "Authorization: Bearer $SHARKO_TOKEN" "$SHARKO_URL/api/v1/marketplace/addons" \
    | jq '{total, verified: ([.addons[]|select(.verified)]|length), signed: ([.addons[]|select(.signature)]|length)}'
  ```

  The problem is this one when `signed` is more than 0 and `verified` is 0.

- The startup logs have this warning:

  ```sh
  kubectl -n "$SHARKO_NS" logs deploy/sharko --since=1h | grep 'sigstore trust root unavailable'
  ```

  ```json
  {"level":"WARN","msg":"sigstore trust root unavailable; signed entries will surface as unverified","err":"tuf.New: ..."}
  ```

  The `err` field holds the real reason. It starts with one of these:

  | Start of `err` | What it means |
  |---|---|
  | `tuf.New: ...` | Sharko could not set up the download. Often the cache folder can't be written, for example `mkdir /tmp/sigstore-tuf: read-only file system`, or the Sigstore server can't be reached. |
  | `tuf GetTarget(trusted_root.json): ...` | Sharko reached Sigstore but could not get the file. |
  | `parse trusted_root.json: ...` | Sharko got the file but could not read it. |

- After that, each signed entry adds its own warning, `catalog entry signature verification errored`. Those are a result of this problem, not a separate one.

When the download works, Sharko logs `sigstore trust root loaded` at startup instead.

## Why it happens

Catalog entries can carry a signature made with Sigstore, a free public service for signing software. To check a signature, Sharko needs Sigstore's list of trusted keys. It downloads that list once, at startup, from `https://tuf-repo-cdn.sigstore.dev` (TUF is the name of the safe-download method Sigstore uses). It saves a copy in a cache folder, `/tmp/sigstore-tuf` by default.

The common reasons it fails:

- **The cache folder can't be written.** The Sharko image has a read-only root filesystem. Helm chart versions v1.23 through v4.0.2 did not mount a writable `/tmp`, so the download always failed there. Chart v4.0.3 fixed this.
- **A network rule blocks the pod** from reaching `tuf-repo-cdn.sigstore.dev` on port 443.
- **A company proxy opens HTTPS traffic** and signs it with its own certificate, which Sharko does not trust. See [Corporate TLS inspection](corporate-mitm-tls.md).
- **Sigstore itself has a problem** for a while.

Sharko only tries at startup. Even after the cause is gone, the entries stay not verified until Sharko restarts.

"Verified" is only shown as information. Sharko never blocks an addon because it is not verified, so there is nothing to switch off while you fix this.

## How to fix it

### 1. Read the reason

Run the log command from [What you see](#what-you-see) and look at the `err` field. It tells you which of the steps below you need.

### 2. If the cache folder can't be written

You see `read-only file system` in `err`. Do one of these:

- Upgrade to Helm chart v4.0.3 or later. It mounts a writable `/tmp`.
- Or keep your chart, and point the cache at the data folder, which is always writable. Add this to your values file:

  ```yaml
  extraEnv:
    - name: SHARKO_SIGSTORE_TUF_CACHE
      value: /app/data/sigstore-tuf
  ```

  `extraEnv` is a list, and Helm replaces the whole list, so copy every other `extraEnv` entry you already use into the file too. Then upgrade:

  ```sh
  helm -n "$SHARKO_NS" upgrade sharko oci://ghcr.io/moranweissman/sharko/sharko \
    --reuse-values -f my-values.yaml
  ```

Do **not** set `SHARKO_TRUSTED_ROOT_PATH`, even if an older guide tells you to. Sharko does not read that name, and an unknown `SHARKO_` name stops Sharko from starting. If you set it before, remove it:

```sh
kubectl -n "$SHARKO_NS" set env deploy/sharko SHARKO_TRUSTED_ROOT_PATH-
```

If you set it through Helm `extraEnv`, take it out of your values file as well, or the next upgrade puts it back. The only Sigstore setting Sharko reads is `SHARKO_SIGSTORE_TUF_CACHE`. It names the cache folder. It can't replace the download.

### 3. If Sigstore can't be reached

First check from your own machine:

```sh
curl -sS -o /dev/null -w '%{http_code}\n' https://tuf-repo-cdn.sigstore.dev/timestamp.json
```

If that also fails, Sigstore may be having trouble. Wait a while, then restart Sharko (step 5).

If it works from your machine, check from inside the cluster with a short-lived test pod. It deletes itself when it is done:

```sh
kubectl -n "$SHARKO_NS" run sigstore-test --rm -i --restart=Never \
  --image=curlimages/curl -- \
  curl -sS -o /dev/null -w '%{http_code}\n' https://tuf-repo-cdn.sigstore.dev/timestamp.json
```

- A certificate error means a company proxy is in the way. Follow [Corporate TLS inspection](corporate-mitm-tls.md).
- A timeout or a refused connection means the network blocks it. Ask your network team to allow outbound HTTPS (port 443) to `tuf-repo-cdn.sigstore.dev`.

This test pod is not the Sharko pod, so a NetworkPolicy that only selects Sharko does not apply to it. If the test pod works, look at the NetworkPolicies that select `app.kubernetes.io/name=sharko`:

```sh
kubectl -n "$SHARKO_NS" get networkpolicy
```

They must allow outbound port 443 to that host.

### 4. If the file can't be read

You see `parse trusted_root.json` in `err`. This is rare. It can be a short problem on Sigstore's side, or a proxy that changes the file on the way. Restart Sharko (step 5). If it keeps happening, check for a proxy, as in step 3.

### 5. Restart Sharko

Sharko only downloads the file at startup, so restart it after any fix. A Helm upgrade that changes the pod settings already does this for you.

```sh
kubectl -n "$SHARKO_NS" rollout restart deploy/sharko
kubectl -n "$SHARKO_NS" rollout status deploy/sharko
```

## How to check it's fixed

1. The startup log says the file loaded:

   ```sh
   kubectl -n "$SHARKO_NS" logs deploy/sharko --since=10m | grep 'sigstore trust root'
   ```

   You should see `sigstore trust root loaded`, and no `unavailable` line.

2. Signed entries are verified again:

   ```sh
   curl -s -H "Authorization: Bearer $SHARKO_TOKEN" "$SHARKO_URL/api/v1/marketplace/addons" \
     | jq '{total, verified: ([.addons[]|select(.verified)]|length), signed: ([.addons[]|select(.signature)]|length)}'
   ```

   `verified` is more than 0.

3. The **Signed only** count in the Marketplace is back above 0.

If `verified` is still 0 for Sharko's own built-in entries, look for this warning at startup: `this build carries no release commit, so its own embedded catalog entries cannot be bound to a release and will surface as unverified`. That comes from a self-built image, not from Sigstore. Official releases log `embedded catalog signatures are bound to this build's release commit` instead.

## If that didn't work

Open an issue at <https://github.com/MoranWeissman/sharko/issues>. Include:

- the Sharko version (`sharko version`, or the `version` field from `/api/v1/health`)
- the log lines from the last hour that mention `sigstore`
- what you tried from this page

Related pages:

- [Catalog trust policy](catalog-trust-policy.md)
- [Third-party catalog sources](catalog-sources.md)
- [Corporate TLS inspection](corporate-mitm-tls.md)
- [First checks](troubleshooting.md)
