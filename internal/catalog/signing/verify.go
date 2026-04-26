package signing

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/fulcio/certificate"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/verify"

	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/catalog/sources"
)

// maxBundleBytes caps how much we'll read from a sidecar HTTP body.
// Sigstore bundle JSONs are tiny (a few KB at most). Anything bigger is
// either misconfiguration or an attempt to make us OOM on the bundle
// fetch.
const maxBundleBytes int64 = 1 << 20 // 1 MiB

// defaultBundleFetchTimeout is the per-bundle HTTP fetch ceiling when
// the caller does not supply a configured client. Tight because the
// bundle is small and we don't want a slow signer to hold up catalog
// loading.
const defaultBundleFetchTimeout = 30 * time.Second

// trustedMaterialProvider is the abstraction the verifier uses to
// resolve the Sigstore trust root (Fulcio CAs + Rekor pubkeys + CT
// logs). In production the provider returns the bundled public-good
// trust root (or one fetched via TUF — TODO post-V123-2.2). In tests
// the provider returns the *ca.VirtualSigstore (which itself implements
// root.TrustedMaterial).
type trustedMaterialProvider interface {
	TrustedMaterial(ctx context.Context) (root.TrustedMaterial, error)
}

// Verifier is the production SidecarVerifier implementation (whole-file
// path) AND the per-entry fetch wrapper. Construct via NewVerifier.
//
// Zero value is not usable. The Verifier holds an HTTP client for
// bundle-URL fetches and a trustedMaterialProvider for the Sigstore
// trust root. Both are pluggable via constructor options for tests.
type Verifier struct {
	httpClient *http.Client
	trust      trustedMaterialProvider
	log        *slog.Logger
}

// VerifierOption configures a Verifier at construction time.
type VerifierOption func(*Verifier)

// WithHTTPClient overrides the HTTP client used for bundle-URL fetches.
// Production callers pass a configured client (proxies, timeouts);
// tests pass an httptest.Server-backed client that talks to a local
// bundle stub.
func WithHTTPClient(c *http.Client) VerifierOption {
	return func(v *Verifier) {
		if c != nil {
			v.httpClient = c
		}
	}
}

// WithTrustedMaterial overrides the trust root provider. Tests pass a
// VirtualSigstore-backed provider so cert validation passes without
// reaching out to Fulcio. Production gets a baked-in default that
// uses the public-good Sigstore root.
func WithTrustedMaterial(tm root.TrustedMaterial) VerifierOption {
	return func(v *Verifier) {
		v.trust = staticTrust{tm: tm}
	}
}

// staticTrust adapts a fixed root.TrustedMaterial to the
// trustedMaterialProvider interface.
type staticTrust struct{ tm root.TrustedMaterial }

func (s staticTrust) TrustedMaterial(ctx context.Context) (root.TrustedMaterial, error) {
	if s.tm == nil {
		return nil, errors.New("trust root not configured")
	}
	return s.tm, nil
}

// NewVerifier constructs a Verifier ready to use. If no
// WithTrustedMaterial option is supplied, the verifier returns
// (false, "", err) on every call until trust is configured — V123-2.3
// will land the env-var-driven config that supplies a real trust root
// (TUF-fetched). This is a deliberate fail-closed default: a misconfigured
// Sharko refuses to mark anything verified rather than silently
// trusting nothing-or-everything.
//
// nil httpClient is fine — a sane default with a 30s timeout is used.
func NewVerifier(httpClient *http.Client, opts ...VerifierOption) *Verifier {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultBundleFetchTimeout}
	}
	v := &Verifier{
		httpClient: httpClient,
		trust:      staticTrust{}, // unconfigured by default — fail closed
		log:        slog.Default().With("component", "catalog-signing"),
	}
	for _, opt := range opts {
		opt(v)
	}
	return v
}

// Verify implements sources.SidecarVerifier — the whole-file path. The
// fetcher discovered a `.bundle` sidecar URL next to a fetched catalog
// YAML and is asking us whether it verifies against catalogBytes under
// the configured trust policy.
//
// Return contract is the SidecarVerifier contract verbatim — see
// internal/catalog/sources/verifier.go. Sig-mismatch / untrusted-identity
// → (false, "", nil), not an error. Network fetch failure or malformed
// bundle → (false, "", err).
func (v *Verifier) Verify(
	ctx context.Context,
	catalogBytes []byte,
	sidecarURL string,
	trustPolicy sources.TrustPolicy,
) (verified bool, issuer string, err error) {
	bundleBytes, err := v.fetchBundle(ctx, sidecarURL)
	if err != nil {
		return false, "", fmt.Errorf("fetch sidecar bundle: %w", err)
	}
	return v.verifyBundleBytes(ctx, catalogBytes, bundleBytes, trustPolicy, sidecarURL)
}

// VerifyEntry is the per-entry verifier called by the catalog loader
// when an individual CatalogEntry has a non-nil Signature.Bundle URL.
// The caller is responsible for computing the canonical entry bytes
// (via signing.CanonicalEntryBytes) and passing them as `payload`.
//
// Same return contract as Verify — sig-mismatch / untrusted-identity
// → (false, "", nil); infra failure → (false, "", err).
func (v *Verifier) VerifyEntry(
	ctx context.Context,
	canonicalEntryBytes []byte,
	bundleURL string,
	trustPolicy sources.TrustPolicy,
) (verified bool, issuer string, err error) {
	bundleBytes, err := v.fetchBundle(ctx, bundleURL)
	if err != nil {
		return false, "", fmt.Errorf("fetch entry bundle: %w", err)
	}
	return v.verifyBundleBytes(ctx, canonicalEntryBytes, bundleBytes, trustPolicy, bundleURL)
}

// VerifyEntryFunc returns a closure that conforms to
// catalog.VerifyEntryFunc — closing over the verifier itself and the
// trust policy so the loader doesn't have to know about either. This
// is the canonical wiring point for cmd/sharko/serve.go.
//
// The trust policy can't be a parameter on the loader callback because
// the loader package cannot import sources.TrustPolicy without
// creating an import cycle. Closing it in here is the single-statement
// fix.
func (v *Verifier) VerifyEntryFunc(trustPolicy sources.TrustPolicy) catalog.VerifyEntryFunc {
	return func(ctx context.Context, canonicalEntryBytes []byte, bundleURL string) (bool, string, error) {
		return v.VerifyEntry(ctx, canonicalEntryBytes, bundleURL, trustPolicy)
	}
}

// verifyBundleBytes parses the bundle bytes and runs the verification
// core. Splitting parse-from-bytes out of the SignedEntity core lets
// unit tests verify a *ca.TestEntity directly (which already implements
// verify.SignedEntity) without needing to mint a serialized bundle.
func (v *Verifier) verifyBundleBytes(
	ctx context.Context,
	payload []byte,
	bundleBytes []byte,
	trustPolicy sources.TrustPolicy,
	urlForFingerprint string,
) (bool, string, error) {
	b := &bundle.Bundle{}
	if err := b.UnmarshalJSON(bundleBytes); err != nil {
		return false, "", fmt.Errorf("parse bundle: %w", err)
	}
	return v.verifyEntity(ctx, b, payload, trustPolicy, urlForFingerprint)
}

// verifyEntity runs the verification primitive against any
// verify.SignedEntity. This is the single place all verification
// outcomes are decided — the production Verify/VerifyEntry paths funnel
// through here after parsing bundle bytes, and tests funnel through here
// directly with a *ca.TestEntity.
//
// Steps:
//  1. Fail-closed check: TrustPolicy.Identities empty → reject.
//  2. Compile each Identities regex; reject any malformed regex
//     (infrastructure error — this is configuration, not signature).
//  3. Resolve trust root from the configured provider.
//  4. Construct a sigstore-go Verifier with sensible defaults: require
//     transparency-log inclusion (Rekor), accept the bundle's own
//     observer timestamps (the Rekor SignedEntryTimestamp covers cert
//     validity at the moment of signing, which is what we want).
//  5. Build the verification policy: artifact = SHA-256(payload),
//     identity = "match anything" (we re-check identity ourselves
//     against TrustPolicy regexes after, so the policy is a
//     WithoutIdentitiesUnsafe to avoid double-matching against the
//     sigstore-side regex shape).
//  6. Run Verify. On verification failure (cert-chain bad, sig bad,
//     no Rekor entry) return (false, "", nil) — the design treats
//     unverifiable bundles the same as missing bundles.
//  7. Extract OIDC subject from the verified cert.
//  8. Match subject against compiled TrustPolicy regexes. No match →
//     (false, "", nil) (untrusted identity).
//  9. Match → (true, subject, nil). Log success at INFO with the
//     subject and the URL fingerprint.
func (v *Verifier) verifyEntity(
	ctx context.Context,
	entity verify.SignedEntity,
	payload []byte,
	trustPolicy sources.TrustPolicy,
	urlForFingerprint string,
) (bool, string, error) {
	// Step 1: fail-closed when no identities are configured.
	if len(trustPolicy.Identities) == 0 {
		v.logFailure(urlForFingerprint, "no trusted identities configured (fail-closed)")
		return false, "", nil
	}

	// Step 2: compile all identity regexes up front.
	patterns, err := compileIdentityPatterns(trustPolicy.Identities)
	if err != nil {
		return false, "", fmt.Errorf("compile trust policy: %w", err)
	}

	// Step 3: resolve trust root.
	tm, err := v.trust.TrustedMaterial(ctx)
	if err != nil {
		return false, "", fmt.Errorf("trust root: %w", err)
	}

	// Step 4: build verifier with the standard "require Rekor inclusion"
	// posture. WithObserverTimestamps(1) accepts the bundle's signed
	// entry timestamp from Rekor, which is the canonical attestation of
	// "the cert was valid when this was signed" for short-lived Fulcio
	// keyless certs.
	sev, err := verify.NewVerifier(tm,
		verify.WithTransparencyLog(1),
		verify.WithObserverTimestamps(1),
	)
	if err != nil {
		return false, "", fmt.Errorf("construct sigstore verifier: %w", err)
	}

	// Step 5: build the verification policy. We pin the artifact to a
	// pre-computed SHA-256 of the payload (faster than streaming via
	// WithArtifact for an in-memory byte slice) and skip the
	// sigstore-side identity matching — we apply our TrustPolicy regex
	// after extracting the subject from the verified cert. This keeps
	// the regex semantics under our control and lets us return the
	// (issuer-empty-string) sentinel on untrusted identity without
	// conflating it with a sig-mismatch error.
	digest := sha256.Sum256(payload)
	policy := verify.NewPolicy(
		verify.WithArtifactDigest("sha256", digest[:]),
		verify.WithoutIdentitiesUnsafe(),
	)

	// Step 6: run the verifier. A non-nil error here means the bundle
	// failed cryptographic verification — bad signature, cert chain
	// untrusted, no Rekor entry, etc. Per the SidecarVerifier contract
	// these are NOT infrastructure errors; they're "this signature
	// doesn't verify" → (false, "", nil).
	if _, verr := sev.Verify(entity, policy); verr != nil {
		v.logFailure(urlForFingerprint, "bundle verification failed: "+verr.Error())
		return false, "", nil
	}

	// Step 7: pull the OIDC subject out of the verified cert. The
	// VerificationContent on the entity always carries the leaf cert for
	// a Sigstore keyless bundle; PublicKey is for non-keyless paths
	// which the v1.23 design doesn't support.
	vc, err := entity.VerificationContent()
	if err != nil {
		// The verifier accepted the entity but we can't extract its
		// content — this is a corrupt bundle that somehow passed
		// verification. Treat as infra error so the operator notices.
		return false, "", fmt.Errorf("extract verification content: %w", err)
	}
	cert := vc.Certificate()
	if cert == nil {
		// Same: keyless verification with no cert is impossible. If we
		// got here something's wrong with the bundle structure.
		return false, "", errors.New("verified entity has no certificate (keyless required)")
	}
	subject, err := extractSubject(cert)
	if err != nil {
		return false, "", fmt.Errorf("extract OIDC subject: %w", err)
	}

	// Step 8: match against trust policy regexes.
	if !matchAnyPattern(subject, patterns) {
		v.logFailure(urlForFingerprint,
			"signature verified but identity not in trust policy: "+subject)
		return false, "", nil
	}

	// Step 9: success. Log the subject (which IS in the cert and is not
	// URL-related, so logging it is fine — it's the operator's whole
	// point in configuring the trust policy).
	v.log.Info("catalog signature verified",
		"source_fp", urlFingerprint(urlForFingerprint),
		"identity", subject)
	return true, subject, nil
}

// fetchBundle pulls the bundle JSON over HTTP. Honors the caller's
// ctx for cancellation and clamps body size to maxBundleBytes.
func (v *Verifier) fetchBundle(ctx context.Context, bundleURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, bundleURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.dev.sigstore.bundle+json, application/json, */*;q=0.5")
	req.Header.Set("User-Agent", "sharko-catalog-signing/1.0")

	resp, err := v.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.CopyN(io.Discard, resp.Body, 1024)
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBundleBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if int64(len(body)) > maxBundleBytes {
		return nil, fmt.Errorf("bundle exceeds %d bytes", maxBundleBytes)
	}
	return body, nil
}

// logFailure emits a WARN log for a verification failure on a bundle
// the caller actually fetched. Uses the URL fingerprint, never the URL
// itself (paths may encode auth tokens — see V123-1.1 Gotcha #1).
func (v *Verifier) logFailure(rawURL, reason string) {
	v.log.Warn("catalog signature verification failed",
		"source_fp", urlFingerprint(rawURL),
		"reason", reason)
}

// urlFingerprint returns a 10-char SHA-256 prefix of the URL — same
// convention as internal/catalog/sources/fetcher.go's urlFingerprint
// helper. Kept local rather than re-exported from sources to preserve
// the one-way dependency direction.
func urlFingerprint(u string) string {
	if u == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(u))
	return hex.EncodeToString(sum[:])[:10]
}

// compileIdentityPatterns turns each TrustPolicy.Identities entry into
// a *regexp.Regexp. A malformed pattern is an operator-side
// configuration error — surface it as a non-nil error so the caller
// can fail loudly at startup / fetch rather than silently rejecting
// every signature.
func compileIdentityPatterns(identities []string) ([]*regexp.Regexp, error) {
	out := make([]*regexp.Regexp, 0, len(identities))
	for _, raw := range identities {
		re, err := regexp.Compile(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid trust policy regex %q: %w", raw, err)
		}
		out = append(out, re)
	}
	return out, nil
}

// matchAnyPattern returns true when `subject` matches at least one
// compiled pattern. We use MatchString (substring), not full-match —
// that mirrors cosign's certificate-identity-regexp semantics where
// `^` and `$` anchors are explicit when the operator wants a strict
// match. This is documented in the trust policy env var help text
// (V123-2.3).
func matchAnyPattern(subject string, patterns []*regexp.Regexp) bool {
	for _, re := range patterns {
		if re.MatchString(subject) {
			return true
		}
	}
	return false
}

// extractSubject pulls the OIDC subject out of a Fulcio leaf cert.
// Uses sigstore-go's own SummarizeCertificate which knows the SAN
// extension layout (handles email SANs and OtherName SANs uniformly).
func extractSubject(cert *x509.Certificate) (string, error) {
	summary, err := certificate.SummarizeCertificate(cert)
	if err != nil {
		return "", err
	}
	if summary.SubjectAlternativeName == "" {
		return "", errors.New("certificate has empty SAN")
	}
	return summary.SubjectAlternativeName, nil
}
