package signing

// releasecommit_test.go — S8: the release-commit binding.
//
// # What is being tested, and why it needed its own file
//
// Sharko's release workflow runs on `workflow_run`. For that trigger Fulcio
// stamps the certificate's workflow_ref from the ref the workflow FILE lives
// on, which is always `refs/heads/main`, never the tag being built. That is
// measurable: the certificates inside all 45 published v4.0.1 bundles carry
// workflow_ref `refs/heads/main`, and the shipped default policy demanded
// `^refs/tags/v.*$`, so every one of the 45 was refused while the signature,
// the payload digest, the Fulcio chain, the Rekor inclusion proof and the SAN
// all verified correctly.
//
// A workflow_ref assertion therefore cannot tell a release build apart from
// any other run of the same workflow file. The source-commit claim can: it
// names the commit that was built. Binding it to the commit THIS BINARY was
// released from accepts the genuine catalogue signed for this release and
// refuses a signature made from any other commit.
//
// # The trap these tests are shaped around
//
// A test that asserts only "an error happened" passes with the guard removed,
// because an unrelated failure — a parse error, a missing trust root, a
// network refusal — produces an error too. That has already happened on this
// repository. So every case below asserts the SPECIFIC refusal text, and the
// end-to-end cases assert that the earlier checks PASSED before this one
// refused.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"log/slog"
	"math/big"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sigstore/sigstore-go/pkg/testing/ca"

	"github.com/MoranWeissman/sharko/internal/catalog/sources"
)

// v401ReleaseCommit is the commit v4.0.1 was released from, established from
// the tag and not from any certificate: `v4.0.1` is an annotated tag object
// `24834cae046279c412accc9bd02aeae74c4995e4` whose target commit is this
// value, confirmed both from the local object store and from GitHub's own
// git/tags API.
//
// The certificates in all 45 published v4.0.1 bundles claim exactly this
// commit, which is what makes the real bundles a usable fixture for the
// by-hand runs — and what makes the constant below a usable stand-in for
// "some other commit".
const v401ReleaseCommit = "faf109fbbccac14fbd17fd5fa8ffb7066b2a5406"

// mainAfterRelease is the tip of `main` well after v4.0.1 shipped. It is here
// to pin the case that actually happens: `main` moves on, the release commit
// becomes an ancestor of it (57 commits behind, at the time this was
// written), and a build released from the older commit must still accept its
// own catalogue while refusing a signature made from the newer one. Using the
// tip of `main` as the expected value is the mistake this constant exists to
// test against, never to enable.
const mainAfterRelease = "b4879d135f6e611726ec542a1192b25a7ab03a4c"

// certWithCommitClaims builds a self-signed in-memory certificate carrying
// the Fulcio source-commit extensions. Self-signed is fine: assertReleaseCommit
// only reads the Extensions slice and never validates a chain — in production
// sigstore-go has already validated the chain before the assertion is
// reached.
//
// The two OIDs are encoded differently and the difference is not cosmetic.
// The deprecated "1.1 to 1.6" family stores a bare Go string, so .1.3 goes in
// raw. Everything from .1.8 up is a DER-encoded string, so .1.13 has to be
// marshalled. Getting that backwards produces a certificate whose claim reads
// as empty, which would make a wrong-commit test pass for the wrong reason.
func certWithCommitClaims(t *testing.T, sourceRepositoryDigest, githubWorkflowSHA string) *x509.Certificate {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "s8-release-commit-test"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
	}
	if githubWorkflowSHA != "" {
		// OID 1.3.6.1.4.1.57264.1.3 — raw string, deprecated family.
		tmpl.ExtraExtensions = append(tmpl.ExtraExtensions, pkix.Extension{
			Id:    asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 3},
			Value: []byte(githubWorkflowSHA),
		})
	}
	if sourceRepositoryDigest != "" {
		// OID 1.3.6.1.4.1.57264.1.13 — DER-encoded string.
		der, merr := asn1.Marshal(sourceRepositoryDigest)
		if merr != nil {
			t.Fatalf("asn1.Marshal(%q): %v", sourceRepositoryDigest, merr)
		}
		tmpl.ExtraExtensions = append(tmpl.ExtraExtensions, pkix.Extension{
			Id:    asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 13},
			Value: der,
		})
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("x509.CreateCertificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("x509.ParseCertificate: %v", err)
	}
	return cert
}

// TestCertWithCommitClaims_EncodesBothFields is the control on the helper
// above. Without it a wrong-commit test could pass because the certificate
// carried no claim at all rather than because the claim differed — the
// classic bad break. It asserts the helper really round-trips each field
// through certSourceCommit, including which field wins when both are present.
func TestCertWithCommitClaims_EncodesBothFields(t *testing.T) {
	t.Run("source_repository_digest_round_trips", func(t *testing.T) {
		cert := certWithCommitClaims(t, v401ReleaseCommit, "")
		claim, field := certSourceCommit(cert)
		if claim != v401ReleaseCommit {
			t.Fatalf("claim = %q, want %q — the DER encoding is wrong, so every "+
				"test using this helper would be testing an absent claim", claim, v401ReleaseCommit)
		}
		if field != fieldSourceRepositoryDigest {
			t.Errorf("field = %q, want %q", field, fieldSourceRepositoryDigest)
		}
	})
	t.Run("github_workflow_sha_round_trips", func(t *testing.T) {
		cert := certWithCommitClaims(t, "", v401ReleaseCommit)
		claim, field := certSourceCommit(cert)
		if claim != v401ReleaseCommit {
			t.Fatalf("claim = %q, want %q", claim, v401ReleaseCommit)
		}
		if field != fieldGithubWorkflowSHA {
			t.Errorf("field = %q, want %q", field, fieldGithubWorkflowSHA)
		}
	})
	t.Run("prefers_the_non_deprecated_field", func(t *testing.T) {
		// Both present and disagreeing. sourceRepositoryDigest must win:
		// githubWorkflowSHA is marked Deprecated in sigstore-go and is only a
		// fallback for certificates minted before the newer field existed.
		cert := certWithCommitClaims(t, v401ReleaseCommit, mainAfterRelease)
		claim, field := certSourceCommit(cert)
		if claim != v401ReleaseCommit || field != fieldSourceRepositoryDigest {
			t.Errorf("claim/field = %q/%q, want %q/%q",
				claim, field, v401ReleaseCommit, fieldSourceRepositoryDigest)
		}
	})
	t.Run("no_claim_at_all", func(t *testing.T) {
		cert := certWithCommitClaims(t, "", "")
		claim, field := certSourceCommit(cert)
		if claim != "" || field != "" {
			t.Errorf("claim/field = %q/%q, want both empty", claim, field)
		}
	})
}

// TestAssertReleaseCommit_Matrix is the single source of truth for the
// assertion contract. Every case names the exact refusal text it expects, so
// none of them can pass on an unrelated error.
func TestAssertReleaseCommit_Matrix(t *testing.T) {
	cases := []struct {
		name string
		// certDigest / certSHA: "" means the certificate lacks that extension.
		certDigest       string
		certSHA          string
		expected         string
		require          bool
		wantOK           bool
		wantReasonSubstr []string
	}{
		{
			// The third-party path, and the reason the field is additive: a
			// policy built directly (fetcher, unit test, fixture) never opts
			// in, so nothing it verifies is affected.
			name:       "not_required_skips_even_with_no_claim",
			certDigest: "",
			certSHA:    "",
			expected:   "",
			require:    false,
			wantOK:     true,
		},
		{
			name:       "not_required_ignores_a_mismatch",
			certDigest: mainAfterRelease,
			expected:   v401ReleaseCommit,
			require:    false,
			wantOK:     true,
		},
		{
			name:       "matching_commit_accepted",
			certDigest: v401ReleaseCommit,
			expected:   v401ReleaseCommit,
			require:    true,
			wantOK:     true,
		},
		{
			name:     "matching_commit_accepted_via_deprecated_field",
			certSHA:  v401ReleaseCommit,
			expected: v401ReleaseCommit,
			require:  true,
			wantOK:   true,
		},
		{
			name:       "uppercase_claim_still_matches",
			certDigest: strings.ToUpper(v401ReleaseCommit),
			expected:   v401ReleaseCommit,
			require:    true,
			wantOK:     true,
		},
		{
			// THE WRONG-COMMIT CASE, in the shape that actually happens:
			// `main` has moved on past the release commit. A certificate
			// claiming the newer commit is a genuine certificate — it is just
			// not this release's.
			name:       "main_moved_on_past_the_release_commit",
			certDigest: mainAfterRelease,
			expected:   v401ReleaseCommit,
			require:    true,
			wantOK:     false,
			wantReasonSubstr: []string{
				"release-commit binding failed",
				"claims source commit " + mainAfterRelease,
				"released from commit " + v401ReleaseCommit,
			},
		},
		{
			// The mirror image: a build of the newer commit handed the older
			// release's catalogue. Rolling a signature forward is refused
			// exactly like rolling one back.
			name:       "older_release_signature_on_a_newer_build",
			certDigest: v401ReleaseCommit,
			expected:   mainAfterRelease,
			require:    true,
			wantOK:     false,
			wantReasonSubstr: []string{
				"claims source commit " + v401ReleaseCommit,
				"released from commit " + mainAfterRelease,
			},
		},
		{
			// A prefix must NOT satisfy the check. Two commits can share a
			// prefix, so a prefix comparison is a weaker check, and both sides
			// here are full values so there is nothing to gain by loosening.
			name:             "a_shared_prefix_is_not_a_match",
			certDigest:       v401ReleaseCommit[:39] + "0",
			expected:         v401ReleaseCommit,
			require:          true,
			wantOK:           false,
			wantReasonSubstr: []string{"claims source commit"},
		},
		{
			// THE MISSING-CLAIM CASE. The certificate carries no source-commit
			// claim, so there is nothing to bind. Both fields that were looked
			// for are named, so the reader is not left guessing which.
			name:       "certificate_carries_no_commit_claim",
			certDigest: "",
			certSHA:    "",
			expected:   v401ReleaseCommit,
			require:    true,
			wantOK:     false,
			wantReasonSubstr: []string{
				"certificate carries no source-commit claim",
				"1.3.6.1.4.1.57264.1.13",
				"1.3.6.1.4.1.57264.1.3",
				v401ReleaseCommit,
			},
		},
		{
			// THE MISSING-EXPECTED-VALUE CASE, and the one branch that must
			// never become a skip. A development build, or an image built
			// without the COMMIT build argument, has no release commit to
			// compare against — so it refuses and says which build shapes do
			// that, rather than waving the signature through.
			name:       "required_but_this_build_has_no_release_commit",
			certDigest: v401ReleaseCommit,
			expected:   "",
			require:    true,
			wantOK:     false,
			wantReasonSubstr: []string{
				"this build carries no release commit",
				"COMMIT build argument",
				"refuses to mark its own catalogue verified rather than skip the check",
			},
		},
		{
			// Defensive: a caller wiring a raw policy with a truncated value
			// must not get a prefix match out of it.
			name:       "truncated_expected_value_is_refused_not_prefixed",
			certDigest: v401ReleaseCommit,
			expected:   v401ReleaseCommit[:12],
			require:    true,
			wantOK:     false,
			wantReasonSubstr: []string{
				"is not a full 40-character commit hash",
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cert := certWithCommitClaims(t, tc.certDigest, tc.certSHA)
			reason, ok := assertReleaseCommit(cert, tc.expected, tc.require)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (reason=%q)", ok, tc.wantOK, reason)
			}
			if tc.wantOK {
				if reason != "" {
					t.Errorf("expected empty reason on accept/skip, got %q", reason)
				}
				return
			}
			for _, want := range tc.wantReasonSubstr {
				if !strings.Contains(reason, want) {
					t.Errorf("reason does not name %q\n  reason = %q", want, reason)
				}
			}
		})
	}
}

// TestVerifyEntity_ReleaseCommitRefusesAGenuineSignature is the end-to-end
// missing-claim case, and it matters more than it looks.
//
// VirtualSigstore mints a certificate with a real chain, a real Rekor entry
// and a SAN the trust policy accepts — but with no Fulcio source-commit
// extension, because the test CA only sets the SAN and the issuer. So this
// is a FULLY VALID signature that the verifier accepts all the way through
// signature, payload digest, chain, transparency log and identity, and then
// refuses on the commit binding alone.
//
// The assertion is on that specific refusal, not on `verified == false`. A
// test satisfied by "not verified" would pass with the binding deleted, since
// plenty of unrelated problems also produce a false.
func TestVerifyEntity_ReleaseCommitRefusesAGenuineSignature(t *testing.T) {
	vs, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatalf("NewVirtualSigstore: %v", err)
	}
	payload := []byte("a genuinely signed payload whose cert has no source-commit claim")
	entity, err := vs.Sign(testIdentity, testIssuer, payload)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// The control: with the binding switched off, this exact entity verifies.
	// That is what proves the refusal below comes from the binding and from
	// nothing else.
	base := sources.TrustPolicy{Identities: []string{`^test@example\.com$`}}
	v, rec := withRecordedLogger(t, vs)
	verified, issuer, verr := v.verifyEntity(
		context.Background(), entity, payload, base, "https://example.invalid/x.bundle")
	if verr != nil {
		t.Fatalf("control: unexpected infra error: %v", verr)
	}
	if !verified {
		t.Fatalf("control: the entity must verify without the binding, else this "+
			"test would prove nothing; reasons=%v", recordedReasons(t, rec))
	}
	if issuer == "" {
		t.Fatal("control: verified with an empty issuer")
	}

	// Now the same entity under a policy that requires the release commit.
	bound := base
	bound.RequireReleaseCommit = true
	bound.ReleaseCommit = v401ReleaseCommit

	v2, rec2 := withRecordedLogger(t, vs)
	verified, issuer, verr = v2.verifyEntity(
		context.Background(), entity, payload, bound, "https://example.invalid/x.bundle")
	if verr != nil {
		t.Fatalf("unexpected infra error: %v", verr)
	}
	if verified {
		t.Fatal("a certificate with no source-commit claim was accepted")
	}
	if issuer != "" {
		t.Errorf("issuer = %q, want empty on refusal", issuer)
	}
	warns := recordedReasons(t, rec2)
	if len(warns) == 0 {
		t.Fatal("no WARN recorded, so the refusal reason went nowhere")
	}
	joined := strings.Join(warns, "\n")
	if !strings.Contains(joined, "certificate carries no source-commit claim") {
		t.Errorf("the refusal did not come from the release-commit binding.\n"+
			"This is the trap: without this assertion the test would pass on any\n"+
			"unrelated failure.\n  warnings = %q", joined)
	}
	if !strings.Contains(joined, v401ReleaseCommit) {
		t.Errorf("the refusal does not name the expected release commit %q\n  warnings = %q",
			v401ReleaseCommit, joined)
	}
}

// TestVerifyEntity_NoReleaseCommitStampRefuses is the end-to-end form of the
// missing-metadata branch: the policy requires the binding and the build has
// no release commit to compare against. A fully valid signature is refused,
// and the log says why in words an operator can act on.
func TestVerifyEntity_NoReleaseCommitStampRefuses(t *testing.T) {
	vs, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatalf("NewVirtualSigstore: %v", err)
	}
	payload := []byte("valid signature, unstamped build")
	entity, err := vs.Sign(testIdentity, testIssuer, payload)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// Exactly what EmbeddedCatalogTrustPolicy produces for a `go build` with
	// no ldflags: the requirement on, the expected value empty.
	policy := sources.TrustPolicy{
		Identities:           []string{`^test@example\.com$`},
		RequireReleaseCommit: true,
		ReleaseCommit:        "",
	}
	v, rec := withRecordedLogger(t, vs)
	verified, _, verr := v.verifyEntity(
		context.Background(), entity, payload, policy, "https://example.invalid/x.bundle")
	if verr != nil {
		t.Fatalf("unexpected infra error: %v", verr)
	}
	if verified {
		t.Fatal("an unstamped build accepted its own catalogue — that is the silent " +
			"bypass this branch exists to prevent")
	}
	joined := strings.Join(recordedReasons(t, rec), "\n")
	if !strings.Contains(joined, "this build carries no release commit") {
		t.Errorf("refusal did not name the missing stamp\n  warnings = %q", joined)
	}
}

// TestEmbeddedCatalogTrustPolicy drives the constructor the shipped runtime
// uses, from the shipped defaults, with no env var set.
func TestEmbeddedCatalogTrustPolicy(t *testing.T) {
	unsetIdentities(t)
	unsetWorkflowRefEnv(t)

	base, err := LoadTrustPolicyFromEnv()
	if err != nil {
		t.Fatalf("LoadTrustPolicyFromEnv: %v", err)
	}

	t.Run("release_build_gets_the_commit", func(t *testing.T) {
		p := EmbeddedCatalogTrustPolicy(base, v401ReleaseCommit)
		if !p.RequireReleaseCommit {
			t.Error("RequireReleaseCommit is false on the embedded policy")
		}
		if p.ReleaseCommit != v401ReleaseCommit {
			t.Errorf("ReleaseCommit = %q, want %q", p.ReleaseCommit, v401ReleaseCommit)
		}
		if p.WorkflowRef != EmbeddedCatalogWorkflowRef {
			t.Errorf("WorkflowRef = %q, want %q", p.WorkflowRef, EmbeddedCatalogWorkflowRef)
		}
	})

	t.Run("uppercase_stamp_is_lowercased", func(t *testing.T) {
		p := EmbeddedCatalogTrustPolicy(base, strings.ToUpper(v401ReleaseCommit))
		if p.ReleaseCommit != v401ReleaseCommit {
			t.Errorf("ReleaseCommit = %q, want it lowercased to %q",
				p.ReleaseCommit, v401ReleaseCommit)
		}
	})

	// Every build shape that is NOT a release must land on "required, with
	// nothing to compare" — which refuses — and never on "not required",
	// which would accept.
	for _, stamp := range []string{"dev", "", "faf109fb", "faf109f", "not-a-sha", v401ReleaseCommit + "0"} {
		t.Run("unstamped_build_"+stamp, func(t *testing.T) {
			p := EmbeddedCatalogTrustPolicy(base, stamp)
			if !p.RequireReleaseCommit {
				t.Fatalf("stamp %q turned the requirement OFF — that is the silent "+
					"bypass, not the refusal", stamp)
			}
			if p.ReleaseCommit != "" {
				t.Errorf("stamp %q produced ReleaseCommit %q, want empty", stamp, p.ReleaseCommit)
			}
		})
	}

	t.Run("does_not_mutate_the_base_policy", func(t *testing.T) {
		before := base
		p := EmbeddedCatalogTrustPolicy(base, v401ReleaseCommit)
		if base.RequireReleaseCommit || base.ReleaseCommit != "" {
			t.Error("the base policy was mutated — the third-party path shares it")
		}
		if base.WorkflowRef != before.WorkflowRef {
			t.Errorf("base WorkflowRef changed from %q to %q", before.WorkflowRef, base.WorkflowRef)
		}
		// The identity slice must be a copy, not an alias.
		if len(p.Identities) > 0 {
			p.Identities[0] = "^$"
			if base.Identities[0] == "^$" {
				t.Error("Identities is aliased — editing one policy changes the other")
			}
		}
	})

	t.Run("operator_workflow_ref_override_still_wins", func(t *testing.T) {
		t.Setenv(EnvTrustedWorkflowRef, `^refs/heads/(main|release-.*)$`)
		withOverride, err := LoadTrustPolicyFromEnv()
		if err != nil {
			t.Fatalf("LoadTrustPolicyFromEnv: %v", err)
		}
		p := EmbeddedCatalogTrustPolicy(withOverride, v401ReleaseCommit)
		if p.WorkflowRef != `^refs/heads/(main|release-.*)$` {
			t.Errorf("WorkflowRef = %q — an operator's explicit setting was replaced",
				p.WorkflowRef)
		}
	})
}

// TestThirdPartyPolicy_NeverCarriesTheCommitRequirement is the structural
// half of constraint 6. LoadTrustPolicyFromEnv is what the third-party
// fetcher gets, and it must never come back with the binding on — under the
// defaults, under an operator's own identity list, or with the
// <defaults> token in the mix.
func TestThirdPartyPolicy_NeverCarriesTheCommitRequirement(t *testing.T) {
	cases := []struct {
		name       string
		identities string
	}{
		{"defaults", ""},
		{"operator_only", `^https://github\.com/acme/.*$`},
		{"defaults_plus_operator", DefaultsToken + `,^https://github\.com/acme/.*$`},
		{"trust_nothing", "^$"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.identities == "" {
				unsetIdentities(t)
			} else {
				t.Setenv(EnvTrustedIdentities, tc.identities)
			}
			p, err := LoadTrustPolicyFromEnv()
			if err != nil {
				t.Fatalf("LoadTrustPolicyFromEnv: %v", err)
			}
			if p.RequireReleaseCommit {
				t.Error("the third-party policy requires Sharko's release commit — " +
					"that would refuse every third-party signature, whose publisher " +
					"naturally signs from a different commit")
			}
			if p.ReleaseCommit != "" {
				t.Errorf("ReleaseCommit = %q, want empty on the third-party policy",
					p.ReleaseCommit)
			}
		})
	}
}

// TestShippedDefaults_AreMutuallySatisfiable is the durable guard against the
// exact defect S8 fixes.
//
// The two shipped defaults contradicted each other: the Sharko identity
// pattern ends `@refs/heads/main`, while DefaultTrustedWorkflowRef demanded
// `^refs/tags/v.*$`. No certificate can satisfy both, so every signature
// Sharko produced was refused — and nothing went red, because each default
// had its own test and neither test looked at the other.
//
// This test looks at both. It reads the ref out of the Sharko identity
// pattern and requires the workflow_ref policy that governs that catalogue to
// accept it. It deliberately checks the EMBEDDED policy's ref, because that
// is the one applied to Sharko's own signatures; the third-party default is
// free to demand tags, and does.
func TestShippedDefaults_AreMutuallySatisfiable(t *testing.T) {
	unsetIdentities(t)
	unsetWorkflowRefEnv(t)
	base, err := LoadTrustPolicyFromEnv()
	if err != nil {
		t.Fatalf("LoadTrustPolicyFromEnv: %v", err)
	}
	embedded := EmbeddedCatalogTrustPolicy(base, v401ReleaseCommit)

	// The SAN Fulcio actually mints for Sharko's release workflow, measured
	// from all 45 published v4.0.1 certificates.
	const realSAN = "https://github.com/MoranWeissman/sharko/.github/workflows/release.yml@refs/heads/main"
	const realWorkflowRef = "refs/heads/main"

	// Half one: the identity list accepts the real SAN. (Pinned elsewhere too,
	// deliberately — the point here is that both halves are checked together.)
	matched := false
	for _, pat := range embedded.Identities {
		if regexpMustMatch(t, pat, realSAN) {
			matched = true
			break
		}
	}
	if !matched {
		t.Fatalf("no shipped identity pattern accepts the SAN Sharko's own release "+
			"workflow mints (%q); identities = %v", realSAN, embedded.Identities)
	}

	// Half two: the workflow_ref policy applied to that same catalogue accepts
	// the ref that same certificate carries. This is the half that was missing.
	if !regexpMustMatch(t, embedded.WorkflowRef, realWorkflowRef) {
		t.Fatalf("the embedded catalogue's workflow_ref policy %q refuses the ref "+
			"Sharko's own certificates carry (%q). That is the S8 defect: two "+
			"defaults that cannot both be satisfied, so every signature Sharko "+
			"produces is refused while the cryptography is sound.",
			embedded.WorkflowRef, realWorkflowRef)
	}

	// And the whole chain, on a certificate shaped like the real ones.
	cert := certWithCommitClaims(t, v401ReleaseCommit, v401ReleaseCommit)
	if reason, ok := assertReleaseCommit(cert, embedded.ReleaseCommit, embedded.RequireReleaseCommit); !ok {
		t.Fatalf("a certificate claiming the release commit was refused: %s", reason)
	}
}

// TestEmbeddedWorkflowRefStaysAnchoredToMain — the embedded ref policy must
// stay tight. `refs/heads/.*` would accept a signature produced by the
// workflow file as it lives on any branch, which is exactly what the anchored
// identity default was written to prevent.
func TestEmbeddedWorkflowRefStaysAnchoredToMain(t *testing.T) {
	if !regexpMustMatch(t, EmbeddedCatalogWorkflowRef, "refs/heads/main") {
		t.Fatalf("%q does not match refs/heads/main", EmbeddedCatalogWorkflowRef)
	}
	for _, ref := range []string{
		"refs/heads/feature-branch",
		"refs/heads/main-ish",
		"refs/pull/123/merge",
		"refs/tags/v4.0.2",
		"",
	} {
		if regexpMustMatch(t, EmbeddedCatalogWorkflowRef, ref) {
			t.Errorf("%q unexpectedly matched %q", EmbeddedCatalogWorkflowRef, ref)
		}
	}
}

// TestDefaultTrustedWorkflowRef_IsUntouched pins that the third-party default
// was NOT moved. The narrow fix applies a different ref to Sharko's own
// catalogue; it does not change what third-party publishers are held to.
func TestDefaultTrustedWorkflowRef_IsUntouched(t *testing.T) {
	if DefaultTrustedWorkflowRef != `^refs/tags/v.*$` {
		t.Errorf("DefaultTrustedWorkflowRef = %q — the third-party default moved. "+
			"S8's fix is supposed to leave it alone and narrow only Sharko's own "+
			"catalogue.", DefaultTrustedWorkflowRef)
	}
}

func TestIsFullCommitSHA(t *testing.T) {
	good := []string{
		v401ReleaseCommit,
		mainAfterRelease,
		strings.ToUpper(v401ReleaseCommit),
		"0000000000000000000000000000000000000000",
	}
	for _, s := range good {
		if !IsFullCommitSHA(s) {
			t.Errorf("IsFullCommitSHA(%q) = false, want true", s)
		}
	}
	bad := []string{
		"", "dev", "faf109fb", "faf109fbbccac14fbd17fd5fa8ffb7066b2a540",
		v401ReleaseCommit + "a",
		"faf109fbbccac14fbd17fd5fa8ffb7066b2a540g",
		" faf109fbbccac14fbd17fd5fa8ffb7066b2a5406",
		"faf109fbbccac14fbd17fd5fa8ffb7066b2a5406 ",
		"4.0.1",
	}
	for _, s := range bad {
		if IsFullCommitSHA(s) {
			t.Errorf("IsFullCommitSHA(%q) = true, want false", s)
		}
	}
}

// regexpMustMatch keeps the pattern-compile noise out of the tests above. A
// pattern that does not compile is a test failure, never a silent false.
func regexpMustMatch(t *testing.T, pattern, s string) bool {
	t.Helper()
	re, err := regexp.Compile(pattern)
	if err != nil {
		t.Fatalf("pattern %q does not compile: %v", pattern, err)
	}
	return re.MatchString(s)
}

// recordedReasons pulls every `reason` attribute off the captured records.
// The verifier collapses each legitimate refusal to (false, "", nil) and puts
// the distinguishing detail in that attribute, so this is the only place the
// specific refusal is observable — and asserting on the specific refusal is
// the whole point: "not verified" on its own would pass with the check
// deleted.
func recordedReasons(t *testing.T, rec *recordedLogger) []string {
	t.Helper()
	var out []string
	for _, r := range rec.Records() {
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "reason" {
				out = append(out, a.Value.String())
				return false
			}
			return true
		})
	}
	return out
}
