# Marketplace signature settings

Addons in the Marketplace can be signed. When an entry is signed, Sharko
checks the signature and shows a **Verified** badge if it trusts the
signer, or **Unverified** if it does not. This page explains the two
settings that decide which signers Sharko trusts.

If entries show as Unverified and you want to fix that, start with
[Fixing Unverified Marketplace entries](catalog-trust-policy.md).

## How Sharko checks a signed entry

Sharko uses Sigstore "keyless" signing. There is no long-lived private
key. Instead, the publisher's GitHub release run gets a short-lived
certificate that names the exact workflow file, and the branch or tag, it
ran from. When an entry has a signature, Sharko:

1. downloads the signature file (the "bundle");
2. checks that the signature matches the entry and that the certificate
   is real;
3. checks that the signature is in Rekor, the public log of signatures;
4. reads the signer's name from the certificate. For GitHub this is the
   address of the workflow file, for example
   `https://github.com/your-org/charts/.github/workflows/release.yml@refs/tags/v1.2.0`;
5. checks that name against `SHARKO_CATALOG_TRUSTED_IDENTITIES`;
6. checks the branch or tag the workflow ran from against
   `SHARKO_CATALOG_TRUSTED_WORKFLOW_REF`.

Only if every step passes does the entry show as Verified. If a step
fails, the entry still shows up and still works. It is just marked
Unverified.

## The two settings

| Setting | What it checks | Default |
|---|---|---|
| `SHARKO_CATALOG_TRUSTED_IDENTITIES` | Who signed: a comma-separated list of patterns for the signer's name | the two default signers below |
| `SHARKO_CATALOG_TRUSTED_WORKFLOW_REF` | Which branch or tag the signing workflow ran from: one pattern | `^refs/tags/v.*$` (version tags only) |

Both are patterns in Go regular-expression syntax. Set them with
`extraEnv` in your Helm values:

```yaml
extraEnv:
  - name: SHARKO_CATALOG_TRUSTED_IDENTITIES
    value: "<defaults>,^https://github\\.com/myorg/.*/\\.github/workflows/.*$"
  - name: SHARKO_CATALOG_TRUSTED_WORKFLOW_REF
    value: "^refs/tags/v.*$"
```

Inside double quotes in YAML, a backslash has to be written twice, so
`\.` becomes `\\.`.

Sharko reads both settings only when it starts. To change them, change
your Helm values and upgrade. Changing `extraEnv` starts a new Sharko pod
with the new settings.

If a pattern is not valid, Sharko does not start. The error names the
setting and the bad pattern:

```
load catalog trust policy: SHARKO_CATALOG_TRUSTED_IDENTITIES: invalid regex "[unbalanced": ...
```

## Who signed: SHARKO_CATALOG_TRUSTED_IDENTITIES

### The default signers

If you do not set it, or set it to an empty value, Sharko trusts these
two:

| Pattern | Who it is |
|---|---|
| `^https://github\.com/cncf/.*/\.github/workflows/.*$` | Any workflow in the CNCF organization on GitHub. Most addons in the Marketplace are CNCF projects. |
| `^https://github\.com/MoranWeissman/sharko/\.github/workflows/release\.yml@refs/heads/main$` | Sharko's own release, which signs the built-in addon list. |

Sharko's own release name ends in `refs/heads/main`, not in a version
tag, because of how GitHub names the workflow: the name records the
branch the workflow file lives on, which is `main`, not the tag being
released.

### Adding your own signers with `<defaults>`

The word `<defaults>` in the list stands for the two default signers. Put
it in the list to keep them and add your own. It must be written exactly
like that, in lower case.

If you leave `<defaults>` out, the defaults are not added. Only your own
patterns count.

| Value | Signers Sharko trusts | When to use it |
|---|---|---|
| not set | the two defaults | You only use the built-in list and CNCF charts. |
| empty | the two defaults | Same as not set. |
| `<defaults>` | the two defaults | You want to write it out on purpose, for example in a values file where an empty value would be removed. |
| `<defaults>,^https://github\.com/myorg/.*/\.github/workflows/.*$` | the defaults and your organization | The usual choice: keep the defaults and add your own. |
| `^https://github\.com/myorg/.*/\.github/workflows/.*$,<defaults>` | the same | Order does not change what is trusted. |
| `^https://github\.com/myorg/.*/\.github/workflows/.*$` | your organization only | You do not want the defaults. |
| `^$` | no one | Every signed entry shows as Unverified, so a person reviews each one. |

### Always use `^` and `$`

A pattern matches anywhere inside the signer's name unless it starts
with `^` and ends with `$`. So `github.com/myorg/` would also match a
name like `https://attacker.example.com/?x=github.com/myorg/`. Sharko does
not add `^` and `$` for you, because that would change what you asked
for. It logs a warning at startup for every pattern that is missing them.

## Which branch or tag: SHARKO_CATALOG_TRUSTED_WORKFLOW_REF

This is a second check on top of the signer's name. Even if the name
matches, the signing workflow must have run from a branch or tag this
pattern allows. That stops a trusted publisher's test runs on a feature
branch from counting as a release.

| Value | What is allowed | When to use it |
|---|---|---|
| not set | `^refs/tags/v.*$`: version tags such as `refs/tags/v1.2.0` | The default. Fits publishers who sign when they push a version tag. |
| `^refs/tags/v.*$` | the same | You want to write it out on purpose. |
| `^refs/heads/main$` | only the `main` branch | The publisher signs on every merge to `main`. |
| `^refs/(heads/main|tags/v.*)$` | `main` or a version tag | The publisher signs both. |
| `.*` | anything, including no branch at all | Turns this check off. Needed for signers that are not GitHub Actions, whose certificates do not name a branch. |

When this check fails, Sharko logs:

```
level=WARN msg="catalog signature verification failed"
    source=redacted
    reason="cert-claim assertion failed: workflow_ref \"refs/heads/feature-branch\" does not match policy \"^refs/tags/v.*$\""
```

## Sharko's built-in addon list has stricter rules

The rules above are what outside catalogs are held to. Sharko's own
built-in addon list is signed by one workflow only, Sharko's own release,
so Sharko checks it more strictly. Every one of these has to pass:

1. **The certificate comes from GitHub Actions**, exactly.
2. **The signer is exactly Sharko's release workflow on `main`.** This is
   an exact match, not a pattern. The CNCF default does not count here.
3. **The signer also matches your `SHARKO_CATALOG_TRUSTED_IDENTITIES`.**
   Your list can only make this stricter. If your list leaves out
   Sharko's release, or is `^$`, the built-in entries show as Unverified.
   A wider pattern does not let any other signer in.
4. **The branch is `main`.** That is where Sharko's release really runs,
   so the built-in list uses `^refs/heads/main$` instead of the tag
   default. If you set `SHARKO_CATALOG_TRUSTED_WORKFLOW_REF` yourself,
   your value is used here too.
5. **The signature was made for this exact version.** The certificate
   records the code version (commit) it was made from. Sharko's release
   writes the same commit into Sharko itself when it builds it. The two
   must be the same. So a real signature from another Sharko version, or
   from a later change on `main`, does not count for the version you run.

No setting turns these checks off.

A copy of Sharko you built yourself has no release commit written into
it, so its built-in entries always show as Unverified. Sharko says so in
a warning when it starts. An official release instead logs which commit
it expects:

```
level=INFO msg="embedded catalog signatures are bound to this build's release commit"
    release_commit=<40-character commit>
```

Outside catalogs skip checks 1, 2 and 5. Their publishers sign from their
own repositories, so Sharko's release commit has nothing to do with them.
A CNCF signer, or any signer on your list, still verifies there.

## Related pages

- [Fixing Unverified Marketplace entries](catalog-trust-policy.md)
- [Extra Marketplace sources](catalog-sources.md)
- [Checking that a Sharko release is genuine](supply-chain.md)
- [Sigstore: how keyless checks work](https://docs.sigstore.dev/cosign/verifying/verify/#about-keyless-verification)
