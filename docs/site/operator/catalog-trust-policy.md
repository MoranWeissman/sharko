# Fixing Unverified Marketplace entries

Use this page when the Marketplace shows addons as **Unverified** and you
expected them to show as **Verified**.

An Unverified badge does not stop anything. The addon still shows up in
the Marketplace and you can still add it to your Catalog. The badge only
tells you Sharko could not confirm who signed it.

The two settings that decide which signers Sharko trusts are explained in
[Marketplace signature settings](catalog-trust-settings.md). This page is
about finding out why an entry is Unverified and fixing it.

If the Marketplace shows no entries at all, this is the wrong page. Read
[The built-in addon list fails to load at startup](catalog-parse-failure-on-startup.md)
or [Sharko cannot load the Sigstore trust root](catalog-trust-root-unavailable.md)
instead.

## If you run v4.0.1 or v4.0.2 {#read-this-first-if-v401-shows-every-built-in-entry-as-unverified}

On a Helm install of `v4.0.1`, every addon in Sharko's built-in list shows
as Unverified. **The signatures are fine. Nothing was tampered with.** Two
bugs in that version cause it:

- Sharko cannot save the Sigstore trust data, because the container's
  disk is read-only. Fixed in `v4.0.3`.
- Sharko's default settings ask for a signature made from a version tag,
  but Sharko's own release signs from the `main` branch. No signature can
  pass both. Fixed in `v4.0.2`.

The best fix is to upgrade to the newest version on
[the releases page](https://github.com/MoranWeissman/sharko/releases).
If you cannot upgrade yet, add these settings to your Helm values.
`v4.0.1` needs both. `v4.0.2` needs only the second one.

```yaml
extraEnv:
  - name: SHARKO_CATALOG_TRUSTED_WORKFLOW_REF
    value: "^refs/heads/main$"
  - name: SHARKO_SIGSTORE_TUF_CACHE
    value: "/app/data/sigstore-tuf"
```

A Sharko you already installed keeps the settings it was built with. A
fix that lands later does not reach it. You either set the settings above
or move to a fixed version. More detail is in the
[v4.0.3 release notes](../release-notes.md#v403-helm-installs-can-cache-the-sigstore-trust-root).

`v3.0.0` and older are no longer supported. Upgrade instead.

## What you see

- The **Unverified** badge on Marketplace entries you expected to be
  Verified.
- In the reply from `GET /api/v1/marketplace/sources`, the source shows
  `verified: false`.
- In the Sharko logs, a warning like one of these:

  ```
  level=WARN msg="catalog signature verification failed"
      source=redacted
      reason="signature verified but identity not in trust policy: <signer>"
  ```

  ```
  level=WARN msg="catalog signature verification failed"
      source=redacted
      reason="cert-claim assertion failed: workflow_ref \"<actual>\" does not match policy \"<configured>\""
  ```

  ```
  level=WARN msg="catalog source sidecar verification errored"
  ```

No alert fires for this, and you cannot build one from Sharko's metrics.
No metric says whether an entry is Verified. To watch for it, look at the
badges in the Marketplace or ask the API.

## Find the reason

### 1. Read the reason in the log

```sh
kubectl logs -n sharko deploy/sharko --tail=2000 \
  | grep "catalog signature verification failed"
```

The `reason` tells you what went wrong:

| The reason starts with | What it means | Fix |
|---|---|---|
| `signature verified but identity not in trust policy` | The signature is good, but your settings do not trust whoever signed it. | [Fix 1](#1-add-the-trusted-identity-most-common-fix) |
| `cert-claim assertion failed: workflow_ref` | The signer is trusted, but it signed from a branch or tag your settings do not allow. | [Fix 2](#2-adjust-the-workflow_ref-policy) |
| `signer check failed` | Only on Sharko's built-in list. The entry was not signed by Sharko's own release. No setting changes this, on purpose. | [Fix 6](#6-a-signer-check-or-release-commit-failure-on-the-built-in-list) |
| `release-commit binding failed` | Only on Sharko's built-in list. The signature belongs to a different Sharko version, or this copy of Sharko was not built by the release. | [Fix 6](#6-a-signer-check-or-release-commit-failure-on-the-built-in-list) |
| `bundle verification failed` | The signature file itself is broken. | [Fix 4](#4-repair-a-corrupt-signature-bundle) |
| `no trusted identities configured` | Your list of trusted signers is empty, for example because it holds only commas. | [Fix 1](#1-add-the-trusted-identity-most-common-fix) |

If there is no warning at all but the entries are still Unverified, the
entries are not signed. See [Fix 3](#3-sign-the-catalog-entry-if-unsigned).

### 2. Check what Sharko loaded at startup

```sh
kubectl logs -n sharko deploy/sharko --tail=2000 \
  | grep "catalog trust policy loaded"
```

You should see one line:

```
level=INFO msg="catalog trust policy loaded" identity_count=2
```

`identity_count` is how many signer patterns Sharko trusts. With no
settings of your own, it is 2. Sharko does not print the patterns
themselves, because they can show the names of your internal teams. To
see what you set, look at the pod:

```sh
kubectl get -n sharko deploy/sharko -o yaml \
  | grep -A2 "SHARKO_CATALOG_TRUSTED_IDENTITIES"
```

### 3. Find out who signed the failing source

The log never shows a source address. It always prints the word
`redacted`, because an address can hold a password. The API does the
same. To find which source is failing, call
`GET /api/v1/marketplace/sources` (you need to be logged in) and compare
each row's status, entry count and position with your own list of
sources.

Then ask cosign who signed it:

```sh
curl -fsSL "<catalog-url>.bundle" -o failing.bundle
cosign verify-blob \
  --bundle failing.bundle \
  --certificate-identity-regexp '.*' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  "<catalog-url>" 2>&1 | grep -i identity
```

The identity it prints must match one of your trusted patterns. If you
have a pattern that should match it, the pattern has a mistake in it. If
you do not, add one (Fix 1).

## Fixes (try them in order)

### 1. Add the trusted identity (most common fix)

Add the signer's workflow to `SHARKO_CATALOG_TRUSTED_IDENTITIES`. Keep the
word `<defaults>` in the list, so you keep the signers Sharko trusts out
of the box:

```yaml
extraEnv:
  - name: SHARKO_CATALOG_TRUSTED_IDENTITIES
    value: "<defaults>,^https://github\\.com/your-org/.*/\\.github/workflows/release\\.yml@refs/.*$"
```

Sharko reads this setting only when it starts. Apply it with Helm:

```sh
helm upgrade sharko oci://ghcr.io/moranweissman/sharko/sharko \
  --version X.Y.Z -n sharko -f values.yaml
```

Changing `extraEnv` changes the pod, so Kubernetes starts a new Sharko
pod with the new setting. If the pod did not restart, restart it:

```sh
kubectl rollout restart deployment/sharko -n sharko
```

Then run step 1 of "Find the reason" again. The warning should be gone.
Sharko fetches extra sources again every hour by default. To fetch now
instead of waiting, an admin can call
`POST /api/v1/marketplace/sources/refresh`.

### 2. Adjust the workflow_ref policy

The signer is trusted, but it signed from a branch or tag that
`SHARKO_CATALOG_TRUSTED_WORKFLOW_REF` does not allow. By default only
version tags such as `refs/tags/v1.2.3` are allowed. If the publisher
signs from `main` or from release branches, allow those:

```yaml
extraEnv:
  - name: SHARKO_CATALOG_TRUSTED_WORKFLOW_REF
    value: "^refs/heads/(main|release-.*)$"
```

You can also set it to `.*`, which allows any branch or tag. That turns
this check off completely. Only do it if you trust everything the signer
signs, from any branch.

Apply it the same way as Fix 1.

### 3. Sign the catalog entry (if unsigned)

No warning plus Unverified means the entries carry no signature. That is
normal for a new outside catalog, because signing is optional. Whoever
publishes the catalog needs to:

- sign the catalog file with cosign keyless signing
  (`cosign sign-blob --bundle <catalog>.yaml.bundle <catalog>.yaml`);
- put the `.bundle` file next to the catalog file, at the same address
  with `.bundle` on the end;
- or point each entry's `signature.bundle` field at its own bundle file.

After Sharko fetches the catalog again, the entries show as Verified.

### 4. Repair a corrupt signature bundle

`bundle verification failed` means the signature file is broken. You
cannot fix that on your side. The publisher has to sign again and upload
the new file. Common reasons:

- The bundle file was opened and saved in a text editor, which changed
  its line endings.
- A signing run failed part way and uploaded half a file.
- The publisher used a cosign version that writes a bundle format Sharko
  cannot read. Ask them to use a recent cosign.

### 5. The "trust nothing" setting

If you want every signed entry to show as Unverified, so that a person
reviews every one, set:

```yaml
extraEnv:
  - name: SHARKO_CATALOG_TRUSTED_IDENTITIES
    value: "^$"
```

`^$` matches only an empty name, and no real signer has an empty name.
Every signed entry then logs `signature verified but identity not in
trust policy`, which is what you asked for.

### 6. A signer check or release-commit failure on the built-in list

Sharko's built-in addon list is held to stricter rules than outside
catalogs. It must be signed by Sharko's own release, from the exact
version you are running. These two reasons mean that did not happen:

- `signer check failed`: someone other than Sharko's release signed it.
- `release-commit binding failed`: the signature was made for a different
  Sharko version, or your copy of Sharko was not built by the release.

If you built Sharko yourself (with `go build`, `make build`, or a
container image built without the `COMMIT` build setting), every built-in
entry shows as Unverified. That is expected. Sharko says so in a warning
when it starts:

```
level=WARN msg="this build carries no release commit, so its own embedded catalog entries cannot be bound to a release and will surface as unverified; a release binary is stamped with the commit it was built from"
```

If you run an official release and still see these reasons, do not trust
those entries. Download the release again and check it as shown in
[Checking that a Sharko release is genuine](supply-chain.md), then open
an issue.

## Why it happens

- **The signer is not on your list.** This is the most common reason. Out
  of the box, Sharko trusts workflows in the CNCF organization on GitHub
  and its own release. Every other publisher needs its own pattern (Fix 1).
- **The publisher signs from a branch, not a tag.** The default only
  allows version tags (Fix 2).
- **The publisher changed how they sign.** If they rename their workflow
  file, move to another GitHub organization, or sign from another branch,
  the signer name changes and your pattern stops matching. Update the
  pattern.
- **The publisher's cosign is too old or too new** and writes a bundle
  Sharko cannot read (Fix 4).

## How to avoid it

- **Keep both settings in your Helm values** for every environment, so
  they get reviewed like any other change.
- **Ask catalog publishers to fix their cosign version** in their own
  release pipeline, so the bundle format does not change by surprise.
- **After upgrading Sharko, check `identity_count`** (step 2 above). If a
  new Sharko version adds or removes a default signer, `<defaults>` will
  hold a different list.

## If none of this works

Open an issue on
[GitHub](https://github.com/MoranWeissman/sharko/issues) and include:

- the warning line from step 1, with its `reason`;
- the `identity_count` line from step 2;
- what you set for `SHARKO_CATALOG_TRUSTED_IDENTITIES` and
  `SHARKO_CATALOG_TRUSTED_WORKFLOW_REF`;
- your Sharko version (`sharko version`).

Do not paste a catalog address that holds a token.

## Related pages

- [Marketplace signature settings](catalog-trust-settings.md): what each
  setting does, its default, and examples.
- [Sharko cannot load the Sigstore trust root](catalog-trust-root-unavailable.md):
  every signature fails at once, not just some entries.
- [A catalog source fails its format check](catalog-source-schema-validation-failed.md):
  the entry is skipped before its signature is checked.
- [A catalog source cannot be downloaded](catalog-source-http-fetch-failed.md).
- [Extra Marketplace sources](catalog-sources.md): how to add outside
  catalogs.
- [All failure pages](failure-mode-index.md).
