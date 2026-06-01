# Migrating from v1.x to v2.0.0

## Short answer

There is no migration. v2.0.0 is Sharko's first production release.
v1.x builds were the pre-release / development cycle — they were never
officially released as production-supported.

## What if I have a v1.x dev install?

Reinstall fresh against v2.0.0. Sharko stores no state in its own
filesystem — your `managed-clusters.yaml`, `addons-catalog.yaml`, and
per-cluster overrides live in your git repository. A fresh v2.0.0
install pointed at the same repo recovers full state.

### Steps

1. Uninstall the v1.x Helm release:

    ```bash
    helm uninstall sharko --namespace sharko
    ```

    The `sharko` namespace itself can stay — only the release is removed.
    If you want a clean slate, also delete the namespace:

    ```bash
    kubectl delete namespace sharko
    ```

2. Install v2.0.0 with the same `repoUrl` and `repoBranch` as your v1.x
   install. Reuse your existing `sharko-values.yaml` if you have one:

    ```bash
    helm install sharko oci://ghcr.io/moranweissman/sharko/charts/sharko \
      --namespace sharko --create-namespace \
      -f sharko-values.yaml
    ```

    Or the minimal form, if your install was driven by `--set`:

    ```bash
    helm install sharko oci://ghcr.io/moranweissman/sharko/charts/sharko \
      --namespace sharko --create-namespace \
      --set secrets.GITHUB_TOKEN=<github-pat>
    ```

3. Open the dashboard — your clusters + addons should reappear within a
   reconcile cycle (~30s, the cluster reconciler tick interval). If you
   re-pointed at the same repo, you will see the same managed clusters
   and the same catalog as before.

See [Installation](installation.md) for the full install reference and
[Initial Credentials](installation.md#initial-credentials) for retrieving
the fresh v2.0.0 admin password.

## What if something doesn't load?

Open an issue at
[https://github.com/MoranWeissman/sharko/issues](https://github.com/MoranWeissman/sharko/issues)
with the log output from the fresh v2.0.0 install. v1.x configs that
don't parse cleanly into v2.0.0 are bugs we want to know about. Sharko
v2.0.0 ships JSON-Schema validation on every read, so any envelope-shape
issue surfaces with a clear error pointing at the offending file.

## What about downgrading?

There is no downgrade path. v2.0.0 is the first production release; the
v1.x line was development-only. There is nothing to downgrade to that
has production support.
