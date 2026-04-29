# Verified Signatures

Sharko's marketplace and addon detail pages show a **Verified** badge next
to each catalog entry. This page explains what that badge means, why it
matters, and what to do if you see an addon marked **Unverified**.

If you are an operator looking to configure which signing identities your
Sharko instance trusts, see
[Catalog Trust Policy](../operator/catalog-trust-policy.md) — that page
covers the deep-config side. This page is for users.

## What "Verified" means

Every catalog entry in Sharko can carry a [Sigstore](https://www.sigstore.dev/)
**keyless** signature. Sigstore signatures don't rely on long-lived signing
keys — they pin a signature to a specific OIDC identity (for example, a
GitHub Actions workflow URL), with the signing certificate logged in a
public transparency log.

When Sharko loads a catalog entry, it checks the signature against the
**trust policy** your operator has configured.

| Badge | What it means |
|-------|---------------|
| **Verified** (green) | The entry carried a valid Sigstore signature, and the signing identity matched the operator's trust policy. The signing identity (for example, a Sharko release workflow URL) is shown in the addon detail page. |
| **Unverified** (grey) | Either the entry did not carry a signature, or the signature did not match the trust policy. The entry still loads and can still be installed — you just don't have signature-backed provenance for it. |

The badge is purely informational. **Sharko does not block install** on
Unverified entries — it surfaces the trust state and lets you decide.

## Why it matters

Verified entries give you a **supply-chain provenance** signal: you can
trace the catalog entry back to a specific signing identity (for example,
"this entry was signed by the Sharko release workflow on tag `v1.23.0`"
or "this entry was signed by my org's internal release workflow").

An **Unverified** entry isn't necessarily compromised. It just means
Sharko can't tie it to a trusted signing identity. There are a few common
reasons an entry shows as Unverified:

- **The catalog source doesn't sign its entries.** Many third-party
  catalogs ship without signatures; that's the operator-of-the-catalog's
  call.
- **The signature didn't match your operator's trust policy.** Your
  operator chose a conservative policy, and the signing identity isn't
  in it.
- **The entry was added before signing was wired in.** Older catalog
  snapshots may not carry the `signature` field at all.

The right response to Unverified depends on which of these applies — see
the two sections below.

## Where the badge appears

The Verified state surfaces in three places:

- **Marketplace browse tiles** — every catalog tile carries the badge in
  the upper corner. Use the **Signed only** filter on the Marketplace
  filters bar to hide Unverified entries entirely if you only want to see
  verified addons.
- **Addon detail page** — the badge appears next to the addon name, with
  the signing identity shown when the entry is Verified.
- **API responses** — the catalog API returns two fields per entry:
  `verified` (boolean) and `signature_identity` (the OIDC subject from the
  signing certificate, when verified). External tools like Backstage or
  CI scripts can read these to make their own trust decisions.

## What to do if you see Unverified on an *embedded* addon

Embedded addons are the ones that ship with Sharko itself (the curated
catalog). From `v1.23` onwards, every embedded entry is signed by the
Sharko release workflow.

If an embedded entry shows as Unverified on a fresh install, the most
likely cause is that your operator has set a custom trust policy that
doesn't include the Sharko release workflow identity. The fix:

1. Open the addon detail page and note the **signing identity** shown
   in the signature panel (if any).
2. Ask your operator to confirm that the Sharko release workflow URL
   is included in the trust policy. The recommended pattern is to
   keep the built-in defaults via the `<defaults>` token rather than
   overriding from scratch — see
   [Catalog Trust Policy](../operator/catalog-trust-policy.md) for
   the exact env-var configuration.

You can still install Unverified embedded entries while the policy is
being adjusted; the data plane behavior is identical.

## What to do if you see Unverified on a *third-party* addon

Third-party addons come from catalog sources your operator has added
via `SHARKO_CATALOG_URLS`. The signing story for these addons is the
responsibility of whoever publishes the third-party catalog — Sharko
just verifies whatever signature (or lack of signature) the catalog
ships.

If you see Unverified on a third-party addon:

- **It's safe to install** if you trust the catalog source the same way
  you'd trust any other internal Helm chart repo.
- **The supply-chain story is weaker** than for Verified entries — you
  don't have a cryptographic link between the catalog entry and a
  specific signing workflow. If supply-chain provenance matters for your
  use case, ask the catalog publisher to add Sigstore-keyless signing,
  or move to a catalog that already does.
- **Use the Signed-only filter** on Marketplace if you want to default
  to verified entries only and explicitly opt in when you need an
  unverified one.
