package main

// catalog_trust_signer_test.go — S11 through the shipped wiring.
//
// The signing package tests S11 against policies it builds itself. These
// tests take the two policies from buildCatalogTrustPolicies — the exact
// function serve.go calls — and run real signatures through the real
// verifier under them. Every bundle is minted by signingtest: a real
// certificate chain, a real transparency-log promise and the payload's real
// digest, from one in-process trust root. Each certificate claims the
// MATCHING release commit, so the only thing wrong with a refused one is the
// thing the case is named after, and each refusal is checked by its exact
// reason text.

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"testing"

	"github.com/MoranWeissman/sharko/internal/catalog/signing"
	"github.com/MoranWeissman/sharko/internal/catalog/signing/signingtest"
	"github.com/MoranWeissman/sharko/internal/catalog/sources"
)

const (
	wiringGitHubIssuer   = "https://token.actions.githubusercontent.com"
	wiringSharkoIdentity = "https://github.com/MoranWeissman/sharko/.github/workflows/release.yml@refs/heads/main"
	wiringCNCFIdentity   = "https://github.com/cncf/example-project/.github/workflows/release.yml@refs/heads/main"
	wiringOtherSharko    = "https://github.com/MoranWeissman/sharko/.github/workflows/ci.yml@refs/heads/main"
	wiringWrongIssuer    = "https://accounts.example.com"
)

var wiringPayload = []byte("s11 wiring payload")

// wiringReasons records every `reason` attribute the verifier logs.
type wiringReasons struct {
	mu      sync.Mutex
	reasons []string
}

func (r *wiringReasons) Enabled(context.Context, slog.Level) bool { return true }
func (r *wiringReasons) Handle(_ context.Context, rec slog.Record) error {
	rec.Attrs(func(a slog.Attr) bool {
		if a.Key == "reason" {
			r.mu.Lock()
			r.reasons = append(r.reasons, a.Value.String())
			r.mu.Unlock()
		}
		return true
	})
	return nil
}
func (r *wiringReasons) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *wiringReasons) WithGroup(string) slog.Handler      { return r }
func (r *wiringReasons) take() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.reasons
	r.reasons = nil
	return out
}

type wiringHarness struct {
	ss  *signingtest.Sigstore
	v   *signing.Verifier
	rec *wiringReasons
}

func newWiringHarness(t *testing.T) *wiringHarness {
	t.Helper()
	ss, err := signingtest.New()
	if err != nil {
		t.Fatalf("signingtest.New: %v", err)
	}
	rec := &wiringReasons{}
	return &wiringHarness{
		ss:  ss,
		v:   signing.NewVerifier(nil, signing.WithTrustedMaterial(ss.TrustedMaterial()), signing.WithLogger(slog.New(rec))),
		rec: rec,
	}
}

func (h *wiringHarness) check(t *testing.T, c signingtest.Claims, p sources.TrustPolicy) (bool, []string) {
	t.Helper()
	b, err := h.ss.SignBundle(wiringPayload, c)
	if err != nil {
		t.Fatalf("SignBundle: %v", err)
	}
	_ = h.rec.take()
	ok, _, err := h.v.VerifyBundleBytes(context.Background(), wiringPayload, b, p)
	if err != nil {
		t.Fatalf("infrastructure error, so this case measured nothing: %v", err)
	}
	return ok, h.rec.take()
}

func genuineWiringClaims() signingtest.Claims {
	return signingtest.Claims{
		SAN:            wiringSharkoIdentity,
		Issuer:         wiringGitHubIssuer,
		IssuerEncoding: signingtest.IssuerV2,
		WorkflowRef:    "refs/heads/main",
		SourceCommit:   testReleaseCommit,
	}
}

func mustPolicies(t *testing.T) catalogTrustPolicies {
	t.Helper()
	p, err := buildCatalogTrustPolicies(testReleaseCommit)
	if err != nil {
		t.Fatalf("buildCatalogTrustPolicies: %v", err)
	}
	return p
}

// TestCatalogTrustPolicies_EmbeddedAcceptsOnlySharkosReleaseSigner drives
// every S11 case through the shipped policies.
func TestCatalogTrustPolicies_EmbeddedAcceptsOnlySharkosReleaseSigner(t *testing.T) {
	identityMismatch := func(got string) string {
		return fmt.Sprintf("signer check failed: certificate identity %q is not the required identity %q",
			got, wiringSharkoIdentity)
	}
	cases := []struct {
		name       string
		identities string // "" = shipped defaults
		claims     func() signingtest.Claims
		want       string // "" = must be accepted
	}{
		{
			name:   "genuine_signer_is_accepted",
			claims: genuineWiringClaims,
		},
		{
			name: "wrong_issuer",
			claims: func() signingtest.Claims {
				c := genuineWiringClaims()
				c.Issuer = wiringWrongIssuer
				return c
			},
			want: fmt.Sprintf("signer check failed: certificate OIDC issuer %q is not the required issuer %q",
				wiringWrongIssuer, wiringGitHubIssuer),
		},
		{
			name: "missing_issuer",
			claims: func() signingtest.Claims {
				c := genuineWiringClaims()
				c.IssuerEncoding = signingtest.NoIssuer
				return c
			},
			want: fmt.Sprintf("signer check failed: certificate carries no OIDC issuer "+
				"(OID 1.3.6.1.4.1.57264.1.8 or 1.3.6.1.4.1.57264.1.1), but %q is required", wiringGitHubIssuer),
		},
		{
			name:       "different_signer_with_a_widened_operator_list",
			identities: `^https://github\.com/MoranWeissman/sharko/.*$`,
			claims: func() signingtest.Claims {
				c := genuineWiringClaims()
				c.SAN = wiringOtherSharko
				return c
			},
			want: identityMismatch(wiringOtherSharko),
		},
		{
			name: "cncf_signer_matching_the_default_pattern",
			claims: func() signingtest.Claims {
				c := genuineWiringClaims()
				c.SAN = wiringCNCFIdentity
				return c
			},
			want: identityMismatch(wiringCNCFIdentity),
		},
		{
			name:       "trust_nothing_refuses_the_genuine_signer",
			identities: "^$",
			claims:     genuineWiringClaims,
			want:       "signature verified but identity not in trust policy: " + wiringSharkoIdentity,
		},
		{
			name:       "operator_list_without_sharko_refuses_the_genuine_signer",
			identities: `^https://github\.com/cncf/.*/\.github/workflows/.*$`,
			claims:     genuineWiringClaims,
			want:       "signature verified but identity not in trust policy: " + wiringSharkoIdentity,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearTrustEnv(t)
			if tc.identities != "" {
				t.Setenv(signing.EnvTrustedIdentities, tc.identities)
			}
			h := newWiringHarness(t)
			ok, reasons := h.check(t, tc.claims(), mustPolicies(t).Embedded)
			if tc.want == "" {
				if !ok {
					t.Fatalf("the genuine signer was refused: %q", reasons)
				}
				return
			}
			if ok {
				t.Fatalf("accepted; want refused for: %s", tc.want)
			}
			if len(reasons) != 1 || reasons[0] != tc.want {
				t.Fatalf("refused for the wrong reason.\n got: %q\nwant: %q", reasons, tc.want)
			}
		})
	}
}

// TestCatalogTrustPolicies_ThirdPartyStillAcceptsACNCFSigner is the control
// for the CNCF case: the same kind of certificate is accepted by the shipped
// THIRD-PARTY policy, including one whose issuer is not GitHub's. So the
// embedded refusal comes from the embedded pins alone, and third-party
// behaviour has not changed.
func TestCatalogTrustPolicies_ThirdPartyStillAcceptsACNCFSigner(t *testing.T) {
	clearTrustEnv(t)
	h := newWiringHarness(t)
	third := mustPolicies(t).ThirdParty
	for _, issuer := range []string{wiringGitHubIssuer, wiringWrongIssuer} {
		c := signingtest.Claims{
			SAN:            wiringCNCFIdentity,
			Issuer:         issuer,
			IssuerEncoding: signingtest.IssuerV2,
			WorkflowRef:    "refs/tags/v1.2.3",
		}
		if ok, reasons := h.check(t, c, third); !ok {
			t.Fatalf("issuer %q: the third-party policy refused a CNCF signer: %q", issuer, reasons)
		}
	}
}
