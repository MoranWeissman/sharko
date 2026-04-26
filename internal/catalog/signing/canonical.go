// Package signing implements per-entry and per-catalog cosign-keyless
// signature verification (v1.23 Subsystem B / Story V123-2.2).
//
// Two verification surfaces are exposed:
//
//  1. Verifier — implements sources.SidecarVerifier, used by the fetcher
//     when a `.bundle` sidecar is discovered next to a fetched catalog
//     YAML file (whole-file path).
//  2. Verifier.VerifyEntry — per-entry path, supplied by serve.go to the
//     catalog loader as a VerifyEntryFunc callback.
//
// Dependency direction: signing → sources, signing → catalog. The
// reverse (sources/loader importing signing) is forbidden — see the
// long-form rationale in internal/catalog/sources/verifier.go.
//
// Trust policy semantics (fail-closed, per design §3.4):
//
//   - Empty TrustPolicy.Identities → reject every signature, return
//     (false, "", nil). NEVER fall back to "trust well-known issuers"
//     or "trust everything." An empty policy is the operator-explicit
//     "I haven't configured trust yet" state.
//
//   - Each Identities entry is a Go regexp matched against the OIDC
//     subject (cert SAN). Patterns are compiled once per Verify call
//     to avoid stateful caching complexity.
//
// Return contract:
//
//   - (false, "", nil) — legitimate verification failure. Sig mismatch,
//     untrusted identity, missing Rekor inclusion. The caller records the
//     outcome on the snapshot/entry and continues. Not an error.
//
//   - (false, "", err) — infrastructure failure. Network fetch failed,
//     bundle bytes unparseable, cert chain malformed. The caller surfaces
//     this in logs but typically retains the prior snapshot.
//
// No URL is ever logged in this package. Sidecar URLs may encode auth
// tokens (Gotcha #1 from V123-1.1). Use the 10-char SHA-256 fingerprint
// helper urlFingerprint when an identifier is genuinely needed.
package signing

import (
	"github.com/MoranWeissman/sharko/internal/catalog"
)

// CanonicalEntryBytes returns the deterministic YAML serialization of
// the entry MINUS its Signature field and the runtime-only fields the
// loader/fetcher compute after parse (Verified, SignatureIdentity,
// Source, SecurityTier). This byte slice is the message that a per-entry
// cosign signature attests to.
//
// Implementation delegates to CatalogEntry.CanonicalBytes so there is
// exactly one place that decides which fields are "signed" vs
// "computed at load." If a future field is added to CatalogEntry that
// should be excluded from the signed payload, it must be added to
// CatalogEntry.canonicalBytes — and only there.
//
// TestCanonicalEntryBytes_Deterministic in verify_test.go asserts the
// byte-stability contract.
func CanonicalEntryBytes(e catalog.CatalogEntry) ([]byte, error) {
	return e.CanonicalBytes()
}
