# Sharko won't start because its built-in catalog is broken

Sharko carries its own list of addons, built into the program. When Sharko starts, it reads that list. If the list can't be read, Sharko stops right away and the pod keeps restarting.

**How urgent:** High. Sharko is down until you fix it. This should never happen with an official release image. It almost always means a self-built or damaged image.

Before you start, set up the shell variables from [First checks](troubleshooting.md#set-up-once).

## What you see

- The pod keeps restarting:

  ```sh
  kubectl -n "$SHARKO_NS" get pods -l app.kubernetes.io/name=sharko
  ```

  The `STATUS` column shows `CrashLoopBackOff` or `Error`, and `RESTARTS` keeps going up.

- `/api/v1/health` does not answer, because Sharko is not running.
- The logs of the last failed start have a line that starts with `load curated catalog:`. This line is plain text, not JSON:

  ```sh
  kubectl -n "$SHARKO_NS" logs deploy/sharko --previous | grep 'load curated catalog'
  ```

  You usually see it twice: once with `Error: ` in front (followed by the help text), and once on its own. For example:

  ```text
  Error: load curated catalog: catalog: parse yaml: yaml: line 247: did not find expected key
  ```

  Other forms you may see after `load curated catalog:`:

  | Text | What it means |
  |---|---|
  | `catalog: parse yaml: ...` | The list is not valid YAML. |
  | `catalog: empty payload` | The list is empty. |
  | `catalog: no entries found under 'addons:'` | The list has no addons in it. |
  | `catalog: entry #N (name="x"): ...` | One entry breaks a rule. The text after the colon says which rule. |
  | `catalog: duplicate entry name ...` | Two entries have the same name. |

When Sharko starts normally, it logs `curated catalog loaded` with the number of entries. If you see that line, this page is not your problem.

A problem with a **third-party** catalog never stops Sharko. For those, see [A third-party catalog fails the format check](catalog-source-schema-validation-failed.md) or [A third-party catalog can't be downloaded](catalog-source-http-fetch-failed.md).

## Why it happens

The built-in catalog is the file `catalog/addons.yaml` in the Sharko source code. It is put inside the program when the image is built. Each official release is checked before it ships, so a broken built-in catalog points to one of these:

- **You built your own image** from changed source code, and the change broke `catalog/addons.yaml`.
- **The image is damaged**, or it is not the image you think it is (a wrong tag, or a mirror that serves something else).

## How to fix it

### 1. Check which image is running

```sh
kubectl -n "$SHARKO_NS" get deploy sharko -o jsonpath='{.spec.template.spec.containers[0].image}'; echo
```

An official image is `ghcr.io/moranweissman/sharko:<version>`. If you see another registry or a tag you don't know, that is the likely cause.

### 2. Go back to an image that worked

If this started after an upgrade, roll the Helm release back to the last version that ran:

```sh
helm -n "$SHARKO_NS" history sharko
helm -n "$SHARKO_NS" rollback sharko <revision>
```

Or point the chart at a known official image:

```sh
helm -n "$SHARKO_NS" upgrade sharko oci://ghcr.io/moranweissman/sharko/sharko \
  --reuse-values \
  --set image.repository=ghcr.io/moranweissman/sharko \
  --set image.tag=<a released version>
```

### 3. If you build your own image, check the file before you build

In your copy of the source code, run Sharko's own check on the built-in catalog. It uses the same rules Sharko uses at startup, and it prints the exact problem:

```sh
sharko validate-catalog catalog/addons.yaml
```

It prints `OK   catalog/addons.yaml` when the file is good. Otherwise it prints `FAIL catalog/addons.yaml:` and the problem, for example:

```text
FAIL catalog/addons.yaml: catalog: entry #12 (name="my-addon"): missing required field: license
```

Fix the file, run the check again until it says `OK`, then build and push a new image.

## How to check it's fixed

1. The pod is running and ready:

   ```sh
   kubectl -n "$SHARKO_NS" get pods -l app.kubernetes.io/name=sharko
   ```

2. The startup log shows the catalog was read:

   ```sh
   kubectl -n "$SHARKO_NS" logs deploy/sharko --since=10m | grep 'curated catalog loaded'
   ```

3. Sharko answers and the Marketplace has addons:

   ```sh
   curl -s -H "Authorization: Bearer $SHARKO_TOKEN" "$SHARKO_URL/api/v1/health" | jq
   curl -s -H "Authorization: Bearer $SHARKO_TOKEN" "$SHARKO_URL/api/v1/marketplace/addons" | jq '.total'
   ```

## If that didn't work

Open an issue at <https://github.com/MoranWeissman/sharko/issues>. Include:

- the Sharko version (`sharko version`, or the image tag from step 1, since `/api/v1/health` won't answer while the pod is down)
- the log lines that mention `load curated catalog`, from `kubectl logs --previous`
- what you tried from this page

Related pages:

- [A third-party catalog fails the format check](catalog-source-schema-validation-failed.md)
- [A third-party catalog can't be downloaded](catalog-source-http-fetch-failed.md)
- [First checks](troubleshooting.md)
