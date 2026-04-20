# Supply chain — verifying Sharko release artifacts

Starting with v1.21, every Sharko release is signed by [cosign](https://github.com/sigstore/cosign) using the keyless (OIDC) flow. Signatures are produced by the GitHub Actions release workflow itself; the certificate identity is bound to this repository so a valid signature proves the artifact came from a real release run, not a hand-built binary.

What gets signed:

| Artifact | Format | How to verify |
|----------|--------|---------------|
| Container image (`ghcr.io/moranweissman/sharko:vX.Y.Z`) | Cosign signature in OCI registry | `cosign verify` |
| Helm OCI chart (`oci://ghcr.io/moranweissman/sharko/sharko:X.Y.Z`) | Cosign signature in OCI registry | `cosign verify` |
| GitHub release archives (`sharko_X.Y.Z_<os>_<arch>.tar.gz`, `checksums.txt`) | Detached `.sig` + `.pem` published with the release | `cosign verify-blob` |

The signing run also produces a CycloneDX SBOM that is published as a release asset.

## Verifying the container image

Install cosign once:

```bash
brew install cosign      # macOS
# or download from https://github.com/sigstore/cosign/releases
```

Verify a release tag:

```bash
TAG=v1.21.0
cosign verify ghcr.io/moranweissman/sharko:${TAG} \
  --certificate-identity-regexp 'https://github.com/MoranWeissman/sharko/.github/workflows/release.yml@.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

A valid signature prints a JSON blob containing the certificate subject (the workflow ref) and the Rekor transparency-log entry. A bad signature exits non-zero with a clear error.

## Verifying the Helm chart

```bash
TAG=v1.21.0
VERSION=${TAG#v}
cosign verify ghcr.io/moranweissman/sharko/sharko:${VERSION} \
  --certificate-identity-regexp 'https://github.com/MoranWeissman/sharko/.github/workflows/release.yml@.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

If verification passes you can install the chart with the usual command:

```bash
helm install sharko oci://ghcr.io/moranweissman/sharko/sharko --version ${VERSION}
```

## Verifying release binaries

For each archive in the GitHub release there is a matching `<archive>.sig` and `<archive>.pem`. Download all three plus `checksums.txt`, then:

```bash
TAG=v1.21.0
ARCHIVE=sharko_${TAG#v}_linux_amd64.tar.gz
cosign verify-blob ${ARCHIVE} \
  --signature  ${ARCHIVE}.sig \
  --certificate ${ARCHIVE}.pem \
  --certificate-identity-regexp 'https://github.com/MoranWeissman/sharko/.github/workflows/release.yml@.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

You can also verify `checksums.txt` the same way and then check archive integrity with `sha256sum -c checksums.txt`.

## Why these specific identity flags

`--certificate-identity-regexp` pins the signature to a workflow file inside this repo. Without it, any workflow under `github.com/MoranWeissman` could mint a valid certificate, which weakens the guarantee.

`--certificate-oidc-issuer` pins the OIDC provider to GitHub Actions. This blocks signatures minted by other Sigstore identity providers (e.g. an attacker's Google account).

Together the two flags answer the question "did this artifact come from a release.yml run on the MoranWeissman/sharko main branch?" — exactly the property an operator needs before pulling and running.

## What if verification fails?

Treat it as a critical signal — do not deploy:

1. Confirm the tag exists in [the GitHub releases page](https://github.com/MoranWeissman/sharko/releases) and the workflow run shows green.
2. Re-fetch the artifact from the release page in case of partial download.
3. If the failure persists, file an issue with the cosign output. Do not work around verification by ignoring the error.

## Per-PR images are not signed

The per-PR images published by `pr-docker.yml` are intentionally unsigned. They are short-lived test artifacts (deleted on PR close) and the cost of OIDC-signing every PR push is not justified. Production-bound deployments must use `vX.Y.Z` tags only.
