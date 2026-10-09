package main

// verify_signer_test.go — S11 at the release gate, with real signatures.
//
// The other gate tests use a fake verifier. These use the REAL verifier and
// real bundles minted by signingtest (a real certificate chain, a real
// transparency-log promise, the payload's real digest, from one in-process
// trust root), signed over the real catalogue by the gate's own signing
// path. The policy comes from the production LoadPolicy, and runVerify turns
// it into the embedded policy itself, so what is tested is that the gate
// picks up the exact issuer and signer pins through
// signing.EmbeddedCatalogTrustPolicy rather than a copy of them.

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/catalog/signing"
	"github.com/MoranWeissman/sharko/internal/catalog/signing/signingtest"
)

// harnessSigner signs each payload file with a certificate carrying claims.
type harnessSigner struct {
	ss     *signingtest.Sigstore
	claims signingtest.Claims
}

func (h harnessSigner) SignBlob(payloadPath string, out signOutputs) error {
	payload, err := os.ReadFile(payloadPath)
	if err != nil {
		return err
	}
	b, err := h.ss.SignBundle(payload, h.claims)
	if err != nil {
		return err
	}
	if err := os.WriteFile(out.BundlePath, b, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(out.SigPath, []byte("unused"), 0o600); err != nil {
		return err
	}
	return os.WriteFile(out.CertPath, []byte("unused"), 0o600)
}

func clearGateTrustEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{signing.EnvTrustedIdentities, signing.EnvTrustedWorkflowRef} {
		if v, ok := os.LookupEnv(k); ok {
			if err := os.Unsetenv(k); err != nil {
				t.Fatalf("unsetenv %s: %v", k, err)
			}
			t.Cleanup(func() { _ = os.Setenv(k, v) })
		}
	}
}

// runGate signs the real catalogue with claims and runs the real gate over
// it. Returns the gate's error and its report.
func runGate(t *testing.T, claims signingtest.Claims) (report string, entries int, gateErr error) {
	t.Helper()
	ss, err := signingtest.New()
	if err != nil {
		t.Fatalf("signingtest.New: %v", err)
	}
	dir := t.TempDir()
	if err := run(options{OutDir: dir, ReleaseBaseURL: fakeReleaseBase}, harnessSigner{ss: ss, claims: claims}); err != nil {
		t.Fatalf("sign the catalogue: %v", err)
	}
	entries = len(readSignedYAML(t, dir))
	sink := newReasonSink()
	deps := verifyDeps{
		NewVerifier: func(context.Context) (bundleVerifier, error) {
			return signing.NewVerifier(nil,
				signing.WithTrustedMaterial(ss.TrustedMaterial()),
				signing.WithLogger(slog.New(sink)),
			), nil
		},
		LoadPolicy: signing.LoadTrustPolicyFromEnv,
	}
	var out bytes.Buffer
	gateErr = runVerify(context.Background(), verifyOpts(dir), &out, deps, sink)
	return out.String(), entries, gateErr
}

func gateGenuineClaims() signingtest.Claims {
	return signingtest.Claims{
		SAN:            testIdentity,
		Issuer:         "https://token.actions.githubusercontent.com",
		IssuerEncoding: signingtest.IssuerV2,
		WorkflowRef:    "refs/heads/main",
		SourceCommit:   testReleaseCommit,
	}
}

// TestVerifyGate_AcceptsOnlySharkosReleaseSigner — every entry of the real
// catalogue, signed for real, through the real gate. The genuine signer
// passes; each other signer fails on EVERY entry, for its exact reason.
func TestVerifyGate_AcceptsOnlySharkosReleaseSigner(t *testing.T) {
	const cncf = "https://github.com/cncf/example-project/.github/workflows/release.yml@refs/heads/main"
	identityMismatch := func(got string) string {
		return fmt.Sprintf("signer check failed: certificate identity %q is not the required identity %q",
			got, testIdentity)
	}
	cases := []struct {
		name       string
		identities string
		claims     func() signingtest.Claims
		want       string // "" = the gate must pass
	}{
		{name: "genuine_signer_passes", claims: gateGenuineClaims},
		{
			name: "wrong_issuer",
			claims: func() signingtest.Claims {
				c := gateGenuineClaims()
				c.Issuer = "https://accounts.example.com"
				return c
			},
			want: `signer check failed: certificate OIDC issuer "https://accounts.example.com" is not the required issuer "https://token.actions.githubusercontent.com"`,
		},
		{
			name: "missing_issuer",
			claims: func() signingtest.Claims {
				c := gateGenuineClaims()
				c.IssuerEncoding = signingtest.NoIssuer
				return c
			},
			want: `signer check failed: certificate carries no OIDC issuer (OID 1.3.6.1.4.1.57264.1.8 or 1.3.6.1.4.1.57264.1.1), but "https://token.actions.githubusercontent.com" is required`,
		},
		{
			name: "cncf_signer_matching_the_default_pattern",
			claims: func() signingtest.Claims {
				c := gateGenuineClaims()
				c.SAN = cncf
				return c
			},
			want: identityMismatch(cncf),
		},
		{
			name:       "trust_nothing",
			identities: "^$",
			claims:     gateGenuineClaims,
			want:       "signature verified but identity not in trust policy: " + testIdentity,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearGateTrustEnv(t)
			if tc.identities != "" {
				t.Setenv(signing.EnvTrustedIdentities, tc.identities)
			}
			report, entries, gateErr := runGate(t, tc.claims())
			if entries == 0 {
				t.Fatal("the catalogue has no entries, so the gate checked nothing")
			}
			t.Logf("the gate checked %d real catalogue entries", entries)
			if tc.want == "" {
				if gateErr != nil {
					t.Fatalf("the gate refused the genuine signer: %v\n%s", gateErr, report)
				}
				if n := strings.Count(report, "catalog-sign verify: OK "); n != entries {
					t.Fatalf("%d of %d entries reported OK\n%s", n, entries, report)
				}
				for _, line := range []string{
					"catalog-sign verify: required issuer = https://token.actions.githubusercontent.com\n",
					"catalog-sign verify: required identity = " + testIdentity + "\n",
				} {
					if !strings.Contains(report, line) {
						t.Errorf("the report does not say %q", strings.TrimSpace(line))
					}
				}
				return
			}
			if gateErr == nil {
				t.Fatalf("the gate passed; want every entry refused for: %s\n%s", tc.want, report)
			}
			wantLine := ": signature did not verify: " + tc.want + "\n"
			if n := strings.Count(report, wantLine); n != entries {
				t.Fatalf("%d of %d entries were refused for the expected reason %q\n%s",
					n, entries, tc.want, report)
			}
			if strings.Contains(report, "catalog-sign verify: OK ") {
				t.Fatalf("some entry passed\n%s", report)
			}
		})
	}
}
