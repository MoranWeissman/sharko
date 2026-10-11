# Checking that a Sharko release is genuine

Every Sharko release is signed. Before you run a release, you can check
that it really came from Sharko's own release run on GitHub, and not
from someone who built a copy by hand.

You do the check with [cosign](https://github.com/sigstore/cosign), a
free tool for checking signatures. Sharko uses "keyless" signing: there
is no long-lived private key to steal. Instead, GitHub gives each release
run a short-lived certificate that names the exact workflow file and
branch it ran from. The signature is also written to Rekor, a public log
of signatures, so nobody can quietly swap it later.

Words on this page you may not know are explained in the
[glossary](../glossary.md).

## What is signed

| What | Where | Command to check it |
|---|---|---|
| Container image (`linux/amd64` and `linux/arm64`) | `ghcr.io/moranweissman/sharko:vX.Y.Z` | `cosign verify` |
| Helm chart | `oci://ghcr.io/moranweissman/sharko/sharko:X.Y.Z` | `cosign verify` |
| Engine Helm chart | `oci://ghcr.io/moranweissman/sharko/sharko-engine:X.Y.Z` | `cosign verify` |
| CLI archives and `checksums.txt` on the releases page | `sharko_X.Y.Z_<os>_<arch>.tar.gz`, each with a `.sig` and a `.pem` file next to it | `cosign verify-blob` |
| The package list (SBOM) of the container image, one per architecture | attached to the image in the registry, and as `sharko-image-linux-<arch>.cdx.json` on the releases page | `cosign verify-attestation` |

An SBOM is a list of every package that went into something. Each CLI
archive also has its own SBOM next to it on the releases page
(`sharko_X.Y.Z_<os>_<arch>.tar.gz.sbom.json`). That one lists only the Go
modules in the CLI, not what is installed inside the container image.
For the container image, use the image SBOM below.

Install cosign once:

```bash
brew install cosign      # macOS
# or download it from https://github.com/sigstore/cosign/releases
```

## Check the container image

```bash
TAG=v4.0.6
cosign verify ghcr.io/moranweissman/sharko:${TAG} \
  --certificate-identity-regexp '^https://github\.com/MoranWeissman/sharko/\.github/workflows/release\.yml@refs/heads/main$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

If the signature is good, cosign prints some JSON that names the
workflow that signed the image and its entry in the public log. If the
signature is bad or missing, cosign exits with an error.

The image is built for two kinds of machine at once. Cosign signs the
list that points to both, so this one command checks both. If you run
`cosign verify` against the address of just one architecture, it says
"no signatures found". That is normal, not a fault.

## Check the Helm chart

```bash
VERSION=4.0.6
cosign verify ghcr.io/moranweissman/sharko/sharko:${VERSION} \
  --certificate-identity-regexp '^https://github\.com/MoranWeissman/sharko/\.github/workflows/release\.yml@refs/heads/main$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

If the check passes, install the chart as usual:

```bash
helm install sharko oci://ghcr.io/moranweissman/sharko/sharko --version ${VERSION}
```

## Check the engine chart

The engine chart has its own version number. It does not follow the
Sharko version, so look up the version you want in
`charts/sharko-engine/Chart.yaml` or on the releases page.

The engine chart can be signed by two different workflows: the main
release workflow, or a separate workflow that publishes only the engine
chart. The command below accepts both, and nothing else:

```bash
ENGINE_VERSION=X.Y.Z
cosign verify ghcr.io/moranweissman/sharko/sharko-engine:${ENGINE_VERSION} \
  --certificate-identity-regexp '^https://github\.com/MoranWeissman/sharko/\.github/workflows/(release|engine-chart-publish)\.yml@refs/heads/main$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

If the check passes, you can pull it:

```bash
helm pull oci://ghcr.io/moranweissman/sharko/sharko-engine --version ${ENGINE_VERSION} --untar
```

You do not normally pull the engine chart yourself. ArgoCD pulls it, at
the version written in `sharko-engine.yaml` in your GitOps repository.
Pulling it by hand is useful when you want to look at a new version
before you change that file.

## Check the CLI archives

Each archive on the releases page has a `.sig` file (the signature) and a
`.pem` file (the certificate) next to it. Download all three, plus
`checksums.txt`, then:

```bash
TAG=v4.0.6
ARCHIVE=sharko_${TAG#v}_linux_amd64.tar.gz
cosign verify-blob ${ARCHIVE} \
  --signature  ${ARCHIVE}.sig \
  --certificate ${ARCHIVE}.pem \
  --certificate-identity-regexp '^https://github\.com/MoranWeissman/sharko/\.github/workflows/release\.yml@refs/heads/main$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

You can check `checksums.txt` the same way, and then check every archive
against it with `sha256sum -c checksums.txt`.

## Check the container image SBOM {#verifying-the-container-image-sbom}

!!! warning "No tag published before 2026-09-24 has an image SBOM"
    The image SBOM was added to the release on 2026-09-24. No tag
    published before that date has one, `v4.0.1` included. For those tags,
    `cosign verify-attestation` fails with "no matching attestations".
    That means there is nothing to check, not that a check failed.

    To see whether a tag has one, look for the two
    `sharko-image-linux-*.cdx.json` files on that release on
    [the releases page](https://github.com/MoranWeissman/sharko/releases).

The image SBOM lists both the Alpine packages and the Go modules inside
the image. There is one per architecture, and each one is signed and
attached to that architecture's own image, not to the tag. So first look
up the digest (the fixed fingerprint) of the architecture you want, then
check it:

```bash
TAG=v4.0.6
IMAGE=ghcr.io/moranweissman/sharko
ARCH=amd64          # or arm64

# The tag points at a list of images. Pick the one for your architecture.
ARCH_DIGEST=$(docker buildx imagetools inspect --raw ${IMAGE}:${TAG} \
  | jq -r --arg a "${ARCH}" \
      '.manifests[] | select(.platform.os == "linux" and .platform.architecture == $a) | .digest')

cosign verify-attestation ${IMAGE}@${ARCH_DIGEST} \
  --type cyclonedx \
  --certificate-identity-regexp '^https://github\.com/MoranWeissman/sharko/\.github/workflows/release\.yml@refs/heads/main$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  | jq -r '.payload' | base64 -d | jq '.predicate' > image-sbom-${ARCH}.cdx.json
```

The SBOM names the digest it belongs to, so you can confirm it matches
the image you just checked:

```bash
jq -r '.metadata.component.version' image-sbom-${ARCH}.cdx.json   # must be the same as ${ARCH_DIGEST}
```

The same files are on the releases page, which is easier if you only
want to read one:

```bash
gh release download ${TAG} -R MoranWeissman/sharko --pattern 'sharko-image-linux-*.cdx.json'
```

A file on the releases page has no signature of its own, though. If you
need to trust what it says, check the signed copy in the registry as
shown above.

## Why the two identity settings matter {#why-these-specific-identity-flags}

`--certificate-identity-regexp` says which workflow must have signed. It
names one workflow file, in this repository, on the `main` branch. The
`^` at the start and the `$` at the end make it match the whole name and
nothing longer. Without this setting, a signature made by any GitHub
workflow at all would pass.

`--certificate-oidc-issuer` says the certificate must come from GitHub
Actions. Without it, someone could sign with a certificate from another
login provider, such as their own Google account.

Together they answer one question: did this come from Sharko's own
release run on the `main` branch?

## If a check fails

Do not deploy it.

1. Check that the tag exists on
   [the releases page](https://github.com/MoranWeissman/sharko/releases)
   and that its release run finished without errors.
2. Download the file again, in case the first download was cut short.
3. If it still fails, open an issue on GitHub and paste the cosign
   output. Do not skip the check to get past the error.

Only trust a version that has a page on the releases page. A release run
publishes the container image before it builds the release page, so if
a run fails part way, the image tag can be in the registry with no
release page. Treat that as a run that did not finish, and do not deploy
it. The tags `latest`, `X.Y` and `X` only move after the image and its
SBOM have passed every check, so they never point at an image that
failed one.

## Sharko also checks its built-in addon list

Everything above is a check you do yourself, with cosign, before you run
Sharko. Sharko also does its own check while it runs: every addon in its
built-in Marketplace list is signed, and Sharko checks those signatures
when it starts. The result is the **Verified** badge in the Marketplace.

These are two separate checks with separate settings. A `cosign verify`
that passes tells you nothing about the badges, and the other way round.

If the badges show **Unverified** when you expected **Verified**, or if
you run `v4.0.1` or `v4.0.2`, read
[Fixing Unverified Marketplace entries](catalog-trust-policy.md).

## Images built for pull requests are not signed

The images built for pull requests are test copies. They are deleted
when the pull request closes, and they are not signed. Anything you run
for real should use a `vX.Y.Z` tag.
