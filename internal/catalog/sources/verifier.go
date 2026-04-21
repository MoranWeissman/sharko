// Package sources implements the third-party catalog fetch loop (v1.23
// Subsystem A of docs/design/2026-04-20-v1.23-catalog-extensibility.md).
//
// The fetcher periodically pulls configured HTTPS URLs, validates each
// payload against the catalog schema (via the shared loader), and keeps
// an in-memory last-successful snapshot per URL so a transient upstream
// failure never drops the catalog. If a sidecar signature (`.sig` /
// `.bundle`) is present for a URL AND a SidecarVerifier is wired in, the
// fetcher delegates verification to it and records the result on the
// snapshot. The fetcher itself does not link against the signing
// package — see the SidecarVerifier contract below.
package sources

import "context"

// TrustPolicy is the subset of verification config the fetcher forwards
// to its SidecarVerifier. Today it carries only the OIDC identity
// allowlist — future policy knobs (issuer pins, log inclusion proof
// requirements, offline bundle paths) can be added without touching the
// fetcher.
//
// Identities are the regex list configured via
// SHARKO_CATALOG_TRUSTED_IDENTITIES (design §3.4). The verifier is the
// authoritative interpreter of these patterns; the fetcher treats them
// as opaque strings.
type TrustPolicy struct {
	// Identities is the comma-split list of cosign certificate-identity
	// regex patterns. An empty slice means "trust nothing" — verifiers
	// should reject every signature rather than fall back to a default.
	Identities []string
}

// SidecarVerifier is the narrow contract that Subsystem A calls into
// when it detects a sidecar signature next to a fetched catalog
// payload. Subsystem B (`internal/catalog/signing/`) supplies the
// concrete implementation in V123-2.2; until then, the fetcher is
// constructed with a nil verifier and every fetched entry inherits
// `verified: false`.
//
// IMPORTANT: this interface lives in the fetcher package on purpose.
// Keeping the contract here means the fetcher package does not import
// anything from `internal/catalog/signing/`. The dependency direction
// is Subsystem B → Subsystem A (signing implements fetcher's
// interface), never the other way around. This preserves the design
// invariant in §3.3.1: if signing is not compiled in, fetcher still
// works — it just silently skips verification.
//
// Parameters:
//   - ctx           — fetcher's request context; honour cancellation.
//   - catalogBytes  — the raw YAML bytes that were just fetched. The
//     verifier must treat these as untrusted; signature verification
//     is what establishes trust.
//   - sidecarURL    — absolute URL of the `.sig` or `.bundle` file
//     that was discovered (the fetcher HEAD-probed it and got 2xx).
//   - trustPolicy   — the identity allowlist; the verifier uses this
//     to decide whether the signer is acceptable.
//
// Return contract:
//   - verified  — true IFF the signature is valid AND the signing
//     identity matches the trust policy. On any failure (fetch error,
//     bad signature, untrusted identity) this is false.
//   - issuer    — human-readable signing identity (OIDC subject) when
//     verified is true; empty string otherwise. The fetcher records
//     this on the snapshot so the API / UI can show "Verified by
//     <issuer>".
//   - err       — non-nil only for infrastructure errors (network
//     failure fetching the bundle, malformed bundle). A legitimate
//     "signature doesn't match" or "identity not trusted" MUST come
//     back as (false, "", nil) — not as an error — because the
//     fetcher's job is to record the result and keep going, not to
//     retry on untrusted signatures.
type SidecarVerifier interface {
	Verify(
		ctx context.Context,
		catalogBytes []byte,
		sidecarURL string,
		trustPolicy TrustPolicy,
	) (verified bool, issuer string, err error)
}
