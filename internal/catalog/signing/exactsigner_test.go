package signing

// exactsigner_test.go — S11: Sharko's own embedded catalogue accepts one
// signer only.
//
// Before S11 the embedded catalogue was checked against the identity
// PATTERN list, and the shipped list also trusts every CNCF workflow (for
// third-party catalogues). So a signature from any CNCF workflow, made at
// the right commit, would have passed for Sharko's own catalogue. Nothing
// checked the OIDC issuer either.
//
// Every case here uses a real signature: a real certificate chain, a real
// transparency-log promise and a real payload digest, all minted by
// signingtest from one in-process trust root. Each certificate claims the
// MATCHING release commit and a workflow_ref the policy accepts, so the only
// thing wrong with a refused one is the thing the case is named after. Each
// refusal is checked by its exact reason text: "not verified" on its own
// would still pass with the new check deleted, because an unrelated failure
// also comes back as "not verified".
//
// Policies are built the way `sharko serve` builds them —
// LoadTrustPolicyFromEnv, then EmbeddedCatalogTrustPolicy — and bundles go
// through the public VerifyBundleBytes, the same path the release gate uses.

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/catalog/signing/signingtest"
	"github.com/MoranWeissman/sharko/internal/catalog/sources"
)

// Written out as literals, not taken from the constants under test, so a
// change to either constant shows up here as a failure.
const (
	s11GitHubIssuer   = "https://token.actions.githubusercontent.com"
	s11SharkoIdentity = "https://github.com/MoranWeissman/sharko/.github/workflows/release.yml@refs/heads/main"
	// A CNCF workflow SAN. It matches the shipped CNCF default pattern.
	s11CNCFIdentity = "https://github.com/cncf/example-project/.github/workflows/release.yml@refs/heads/main"
	// Another workflow in Sharko's own repository. Not the release workflow.
	s11OtherSharkoIdentity = "https://github.com/MoranWeissman/sharko/.github/workflows/ci.yml@refs/heads/main"
	s11WrongIssuer         = "https://accounts.example.com"
)

var s11Payload = []byte("s11 embedded catalogue entry payload")

// genuineClaims is exactly what Sharko's release workflow's certificate
// carries, measured on all 45 published v4.0.1 certificates.
func genuineClaims() signingtest.Claims {
	return signingtest.Claims{
		SAN:            s11SharkoIdentity,
		Issuer:         s11GitHubIssuer,
		IssuerEncoding: signingtest.IssuerV2,
		WorkflowRef:    "refs/heads/main",
		SourceCommit:   v401ReleaseCommit,
	}
}

type s11Harness struct {
	ss  *signingtest.Sigstore
	v   *Verifier
	rec *recordedLogger
}

func newS11Harness(t *testing.T) *s11Harness {
	t.Helper()
	ss, err := signingtest.New()
	if err != nil {
		t.Fatalf("signingtest.New: %v", err)
	}
	rec := &recordedLogger{}
	return &s11Harness{
		ss:  ss,
		v:   NewVerifier(nil, WithTrustedMaterial(ss.TrustedMaterial()), WithLogger(slog.New(rec))),
		rec: rec,
	}
}

func (h *s11Harness) bundle(t *testing.T, c signingtest.Claims) []byte {
	t.Helper()
	b, err := h.ss.SignBundle(s11Payload, c)
	if err != nil {
		t.Fatalf("SignBundle: %v", err)
	}
	return b
}

// verify returns whether the bundle verified and every refusal reason the
// verifier logged. An infrastructure error fails the test outright: it
// would mean the case measured nothing about the policy.
func (h *s11Harness) verify(t *testing.T, bundleBytes []byte, p sources.TrustPolicy) (bool, string, []string) {
	t.Helper()
	h.rec.Reset()
	ok, identity, err := h.v.VerifyBundleBytes(context.Background(), s11Payload, bundleBytes, p)
	if err != nil {
		t.Fatalf("VerifyBundleBytes returned an infrastructure error, so this case measured "+
			"nothing about the policy: %v", err)
	}
	return ok, identity, recordedReasons(t, h.rec)
}

// mustRefuseFor asserts a refusal whose ONLY logged reason is exactly want.
func (h *s11Harness) mustRefuseFor(t *testing.T, bundleBytes []byte, p sources.TrustPolicy, want string) {
	t.Helper()
	ok, identity, reasons := h.verify(t, bundleBytes, p)
	if ok {
		t.Fatalf("accepted (identity %q); want a refusal for: %s", identity, want)
	}
	if len(reasons) != 1 || reasons[0] != want {
		t.Fatalf("refused, but not for the expected reason.\n got reasons: %q\nwant exactly: %q",
			reasons, want)
	}
}

func (h *s11Harness) mustAccept(t *testing.T, bundleBytes []byte, p sources.TrustPolicy, wantIdentity string) {
	t.Helper()
	ok, identity, reasons := h.verify(t, bundleBytes, p)
	if !ok {
		t.Fatalf("refused; want accepted. reasons: %q", reasons)
	}
	if identity != wantIdentity {
		t.Fatalf("accepted as %q, want %q", identity, wantIdentity)
	}
}

// shippedPolicies builds both runtime policies from the environment as it
// currently stands, the same two calls `sharko serve` makes.
func shippedPolicies(t *testing.T) (embedded, thirdParty sources.TrustPolicy) {
	t.Helper()
	base, err := LoadTrustPolicyFromEnv()
	if err != nil {
		t.Fatalf("LoadTrustPolicyFromEnv: %v", err)
	}
	return EmbeddedCatalogTrustPolicy(base, v401ReleaseCommit), base
}

func wantIssuerMismatch(got string) string {
	return fmt.Sprintf("signer check failed: certificate OIDC issuer %q is not the required issuer %q",
		got, s11GitHubIssuer)
}

func wantIssuerMissing() string {
	return fmt.Sprintf("signer check failed: certificate carries no OIDC issuer "+
		"(OID 1.3.6.1.4.1.57264.1.8 or 1.3.6.1.4.1.57264.1.1), but %q is required", s11GitHubIssuer)
}

func wantIdentityMismatch(got string) string {
	return fmt.Sprintf("signer check failed: certificate identity %q is not the required identity %q",
		got, s11SharkoIdentity)
}

func wantNotInTrustPolicy(got string) string {
	return "signature verified but identity not in trust policy: " + got
}

// TestEmbeddedCatalogTrustPolicy_PinsTheExactSigner — the constructor sets
// both pins, to the right values, whatever the operator configured. And the
// Sharko release identity still matches the shipped Sharko pattern and does
// NOT match the CNCF one, so the two lists cannot drift apart unnoticed.
func TestEmbeddedCatalogTrustPolicy_PinsTheExactSigner(t *testing.T) {
	cases := []struct{ name, identities, workflowRef string }{
		{"defaults", "", ""},
		{"trust_nothing", "^$", ""},
		{"operator_widened", `^https://github\.com/.*$`, ".*"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.identities == "" {
				unsetIdentities(t)
			} else {
				t.Setenv(EnvTrustedIdentities, tc.identities)
			}
			if tc.workflowRef == "" {
				unsetWorkflowRefEnv(t)
			} else {
				t.Setenv(EnvTrustedWorkflowRef, tc.workflowRef)
			}
			embedded, _ := shippedPolicies(t)
			if embedded.RequiredIssuer != s11GitHubIssuer {
				t.Errorf("RequiredIssuer = %q, want %q", embedded.RequiredIssuer, s11GitHubIssuer)
			}
			if embedded.RequiredIdentity != s11SharkoIdentity {
				t.Errorf("RequiredIdentity = %q, want %q", embedded.RequiredIdentity, s11SharkoIdentity)
			}
		})
	}

	if EmbeddedCatalogIdentity != s11SharkoIdentity || EmbeddedCatalogIssuer != s11GitHubIssuer {
		t.Fatalf("constants changed: identity %q issuer %q", EmbeddedCatalogIdentity, EmbeddedCatalogIssuer)
	}
}

// TestEmbeddedCatalogIdentity_MatchesTheShippedPattern — the exact identity
// must still satisfy the shipped Sharko pattern (otherwise the default
// install would refuse its own catalogue) and must not be something only
// the CNCF pattern accepts.
func TestEmbeddedCatalogIdentity_MatchesTheShippedPattern(t *testing.T) {
	var sharkoHit, cncfHit bool
	for _, p := range DefaultTrustedIdentities {
		m := regexpMustMatch(t, p, EmbeddedCatalogIdentity)
		if strings.Contains(p, "MoranWeissman/sharko") {
			sharkoHit = m
		}
		if strings.Contains(p, "/cncf/") {
			cncfHit = m
		}
	}
	if !sharkoHit {
		t.Errorf("EmbeddedCatalogIdentity %q does not match the shipped Sharko pattern", EmbeddedCatalogIdentity)
	}
	if cncfHit {
		t.Errorf("EmbeddedCatalogIdentity %q matches the CNCF pattern; the two must be distinct", EmbeddedCatalogIdentity)
	}
	if !regexpMustMatch(t, DefaultTrustedIdentities[0], s11CNCFIdentity) {
		t.Fatalf("the CNCF test identity %q does not match the CNCF default pattern, "+
			"so the CNCF-signer case below would prove nothing", s11CNCFIdentity)
	}
}

// TestEmbeddedPolicy_AcceptsTheGenuineSigner is the positive control. If
// the harness or the policy were broken, every refusal below could be
// passing for the wrong reason. Both issuer extensions are accepted: the
// current one (.1.8) and the older one (.1.1).
func TestEmbeddedPolicy_AcceptsTheGenuineSigner(t *testing.T) {
	unsetIdentities(t)
	unsetWorkflowRefEnv(t)
	h := newS11Harness(t)
	embedded, _ := shippedPolicies(t)

	h.mustAccept(t, h.bundle(t, genuineClaims()), embedded, s11SharkoIdentity)

	old := genuineClaims()
	old.IssuerEncoding = signingtest.IssuerV1
	h.mustAccept(t, h.bundle(t, old), embedded, s11SharkoIdentity)
}

// TestEmbeddedPolicy_RefusesTheWrongIssuer — the genuine SAN, the right
// commit, but a certificate vouched for by a different OIDC issuer.
func TestEmbeddedPolicy_RefusesTheWrongIssuer(t *testing.T) {
	unsetIdentities(t)
	unsetWorkflowRefEnv(t)
	h := newS11Harness(t)
	embedded, _ := shippedPolicies(t)

	for _, enc := range []signingtest.IssuerEncoding{signingtest.IssuerV2, signingtest.IssuerV1} {
		c := genuineClaims()
		c.Issuer = s11WrongIssuer
		c.IssuerEncoding = enc
		h.mustRefuseFor(t, h.bundle(t, c), embedded, wantIssuerMismatch(s11WrongIssuer))
	}
	// A near miss: same host, trailing slash. Equality, not a pattern.
	c := genuineClaims()
	c.Issuer = s11GitHubIssuer + "/"
	h.mustRefuseFor(t, h.bundle(t, c), embedded, wantIssuerMismatch(s11GitHubIssuer+"/"))
}

// TestEmbeddedPolicy_RefusesAMissingIssuer — no issuer extension at all is
// a refusal with its own reason, never a skip.
func TestEmbeddedPolicy_RefusesAMissingIssuer(t *testing.T) {
	unsetIdentities(t)
	unsetWorkflowRefEnv(t)
	h := newS11Harness(t)
	embedded, _ := shippedPolicies(t)

	c := genuineClaims()
	c.IssuerEncoding = signingtest.NoIssuer
	h.mustRefuseFor(t, h.bundle(t, c), embedded, wantIssuerMissing())
}

// TestEmbeddedPolicy_RefusesADifferentSigner — another workflow in Sharko's
// own repository, the right issuer and commit. An operator has widened the
// identity list so the pattern check passes; the exact identity still
// refuses it. Operator settings cannot widen the embedded catalogue.
func TestEmbeddedPolicy_RefusesADifferentSigner(t *testing.T) {
	t.Setenv(EnvTrustedIdentities, `^https://github\.com/MoranWeissman/sharko/.*$`)
	unsetWorkflowRefEnv(t)
	h := newS11Harness(t)
	embedded, _ := shippedPolicies(t)

	c := genuineClaims()
	c.SAN = s11OtherSharkoIdentity
	h.mustRefuseFor(t, h.bundle(t, c), embedded, wantIdentityMismatch(s11OtherSharkoIdentity))

	// Near miss on the release identity itself: a different ref.
	c.SAN = "https://github.com/MoranWeissman/sharko/.github/workflows/release.yml@refs/heads/mainx"
	h.mustRefuseFor(t, h.bundle(t, c), embedded, wantIdentityMismatch(c.SAN))

	// Control: the same widened setting still accepts the genuine signer.
	h.mustAccept(t, h.bundle(t, genuineClaims()), embedded, s11SharkoIdentity)
}

// TestEmbeddedPolicy_RefusesACNCFSigner — the case S11 exists for. A CNCF
// workflow, GitHub's issuer, the right commit, and a SAN the SHIPPED CNCF
// default pattern matches. The same bundle is accepted by the third-party
// policy, which proves the bundle is sound and that the refusal comes only
// from the embedded pin.
func TestEmbeddedPolicy_RefusesACNCFSigner(t *testing.T) {
	unsetIdentities(t)
	unsetWorkflowRefEnv(t)
	h := newS11Harness(t)
	embedded, thirdParty := shippedPolicies(t)

	c := genuineClaims()
	c.SAN = s11CNCFIdentity
	// A tag ref, so the third-party default workflow_ref (^refs/tags/v.*$)
	// accepts it too and the control below is a real acceptance.
	c.WorkflowRef = "refs/tags/v1.2.3"
	b := h.bundle(t, c)

	h.mustRefuseFor(t, b, embedded, wantIdentityMismatch(s11CNCFIdentity))
	h.mustAccept(t, b, thirdParty, s11CNCFIdentity)
}

// TestThirdPartyPolicy_HasNoIssuerOrIdentityPin — third-party behaviour is
// unchanged: a CNCF certificate with a non-GitHub issuer, or with no issuer
// at all, is accepted exactly as before S11.
func TestThirdPartyPolicy_HasNoIssuerOrIdentityPin(t *testing.T) {
	unsetIdentities(t)
	unsetWorkflowRefEnv(t)
	h := newS11Harness(t)
	_, thirdParty := shippedPolicies(t)

	for _, enc := range []signingtest.IssuerEncoding{signingtest.IssuerV2, signingtest.NoIssuer} {
		c := signingtest.Claims{
			SAN:            s11CNCFIdentity,
			Issuer:         s11WrongIssuer,
			IssuerEncoding: enc,
			WorkflowRef:    "refs/tags/v1.2.3",
		}
		h.mustAccept(t, h.bundle(t, c), thirdParty, s11CNCFIdentity)
	}
}

// TestEmbeddedPolicy_OperatorCanOnlyNarrow — the SAN must ALSO match an
// operator pattern. `^$` trusts nothing, and a list that leaves out
// Sharko's identity refuses the genuine signer. Both refusals come from the
// pattern check, which runs before the exact pins.
func TestEmbeddedPolicy_OperatorCanOnlyNarrow(t *testing.T) {
	cases := []struct{ name, identities string }{
		{"trust_nothing", "^$"},
		{"list_without_sharko", `^https://github\.com/cncf/.*/\.github/workflows/.*$,^https://github\.com/acme/.*$`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(EnvTrustedIdentities, tc.identities)
			unsetWorkflowRefEnv(t)
			h := newS11Harness(t)
			embedded, _ := shippedPolicies(t)
			h.mustRefuseFor(t, h.bundle(t, genuineClaims()), embedded, wantNotInTrustPolicy(s11SharkoIdentity))
		})
	}
}

// TestS11Harness_IsARealSignature — the harness bundles are checked by the
// full sigstore-go verification, not waved through. A bundle from one
// in-process trust root is refused by a verifier holding another, and a
// changed payload is refused, both by the signature check itself.
func TestS11Harness_IsARealSignature(t *testing.T) {
	unsetIdentities(t)
	unsetWorkflowRefEnv(t)
	embedded, _ := shippedPolicies(t)
	a, b := newS11Harness(t), newS11Harness(t)
	bundleFromA := a.bundle(t, genuineClaims())

	ok, _, reasons := b.verify(t, bundleFromA, embedded)
	if ok || len(reasons) != 1 || !strings.HasPrefix(reasons[0], "bundle verification failed: ") {
		t.Fatalf("a bundle from another trust root: ok=%v reasons=%q; want a signature-check refusal", ok, reasons)
	}

	a.rec.Reset()
	ok, _, err := a.v.VerifyBundleBytes(context.Background(), []byte("a different payload"), bundleFromA, embedded)
	if err != nil {
		t.Fatalf("VerifyBundleBytes: %v", err)
	}
	reasons = recordedReasons(t, a.rec)
	if ok || len(reasons) != 1 || !strings.HasPrefix(reasons[0], "bundle verification failed: ") {
		t.Fatalf("a changed payload: ok=%v reasons=%q; want a signature-check refusal", ok, reasons)
	}

	// And the wrong commit is still refused by the binding, after the new
	// checks pass: S11 sits in front of it, not in place of it.
	c := genuineClaims()
	c.SourceCommit = mainAfterRelease
	ok, _, reasons = a.verify(t, a.bundle(t, c), embedded)
	if ok || len(reasons) != 1 || !strings.HasPrefix(reasons[0], "release-commit binding failed: ") {
		t.Fatalf("wrong commit: ok=%v reasons=%q; want the release-commit binding refusal", ok, reasons)
	}
}
