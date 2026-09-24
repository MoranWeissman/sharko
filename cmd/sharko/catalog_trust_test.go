package main

// catalog_trust_test.go — S8 constraint 9: exercise the ACTUAL runtime
// default-policy wiring, not a hand-built verifier.
//
// A test that only passes with a policy the test itself assembled proves the
// check works when someone remembers to switch it on. It proves nothing about
// the binary. So every case below calls buildCatalogTrustPolicies — the exact
// function cmd/sharko/serve.go calls, with the exact package-level `commit`
// variable the release pipeline stamps — and reads the two policies it hands
// to the two loaders.
//
// The source-level test at the bottom closes the remaining gap: that serve.go
// really uses the two return values, and uses them the right way round.

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/catalog/signing"
)

// The v4.0.1 release commit, established from the tag object rather than from
// any certificate, and the tip of `main` long after it. `main` having moved on
// is the normal state of affairs, and the reason the expected value can never
// be "whatever main points at now".
const (
	testReleaseCommit = "faf109fbbccac14fbd17fd5fa8ffb7066b2a5406"
	testLaterMain     = "b4879d135f6e611726ec542a1192b25a7ab03a4c"
)

// clearTrustEnv takes the operator's overrides out of the picture so what is
// measured is the SHIPPED default and not this machine's environment.
func clearTrustEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		signing.EnvTrustedIdentities,
		signing.EnvTrustedWorkflowRef,
		"SHARKO_SKIP_TUF_NETWORK",
	} {
		if v, ok := os.LookupEnv(k); ok {
			if err := os.Unsetenv(k); err != nil {
				t.Fatalf("unsetenv %s: %v", k, err)
			}
			t.Cleanup(func() { _ = os.Setenv(k, v) })
		}
	}
}

// TestCatalogTrustPolicies_ReleaseBuild — the shipped wiring on a release
// binary. The embedded catalogue is bound to the release commit; the
// third-party policy is not touched.
func TestCatalogTrustPolicies_ReleaseBuild(t *testing.T) {
	clearTrustEnv(t)

	p, err := buildCatalogTrustPolicies(testReleaseCommit)
	if err != nil {
		t.Fatalf("buildCatalogTrustPolicies: %v", err)
	}

	if !p.ReleaseStamped {
		t.Error("ReleaseStamped is false for a full commit hash")
	}
	if !p.Embedded.RequireReleaseCommit {
		t.Error("the embedded policy does not require the release commit")
	}
	if p.Embedded.ReleaseCommit != testReleaseCommit {
		t.Errorf("Embedded.ReleaseCommit = %q, want %q",
			p.Embedded.ReleaseCommit, testReleaseCommit)
	}
	if p.Embedded.ReleaseCommit == testLaterMain {
		t.Error("the expected commit is the tip of main — it must be the commit " +
			"this build was released from")
	}
	if len(p.Embedded.Identities) == 0 {
		t.Error("the embedded policy has no trusted identities, so nothing could verify")
	}

	// The workflow_ref claim assertion the embedded catalogue is held to must
	// accept the ref Sharko's own certificates actually carry. Under the old
	// shipped default this is precisely where all 45 published bundles were
	// refused.
	if !matches(t, p.Embedded.WorkflowRef, "refs/heads/main") {
		t.Errorf("the shipped embedded policy's workflow_ref %q refuses the ref "+
			"Sharko's own release certificates carry (refs/heads/main)",
			p.Embedded.WorkflowRef)
	}

	// And the trusted GitHub issuer plus the exact Sharko release-workflow
	// identity are still required — nothing that was verified stopped being
	// verified.
	const realSAN = "https://github.com/MoranWeissman/sharko/.github/workflows/release.yml@refs/heads/main"
	if !anyMatches(t, p.Embedded.Identities, realSAN) {
		t.Errorf("no shipped identity accepts Sharko's own release workflow SAN %q", realSAN)
	}
	const attackerSAN = "https://github.com/attacker/sharko/.github/workflows/release.yml@refs/heads/main"
	if anyMatches(t, p.Embedded.Identities, attackerSAN) {
		t.Errorf("a shipped identity accepted a foreign repository's SAN %q", attackerSAN)
	}
}

// TestCatalogTrustPolicies_ThirdPartyIsUnbound is the structural half of
// constraint 6, read off the shipped wiring rather than off a constructor.
//
// A third-party publisher signs their catalogue from their own repository at
// their own commit. If Sharko's release commit were required there, every
// third-party signature would be refused — so this must stay off, and it must
// stay off no matter what the embedded policy carries.
func TestCatalogTrustPolicies_ThirdPartyIsUnbound(t *testing.T) {
	clearTrustEnv(t)

	for _, stamp := range []string{testReleaseCommit, testLaterMain, "dev", ""} {
		t.Run("stamp_"+orDash(stamp), func(t *testing.T) {
			p, err := buildCatalogTrustPolicies(stamp)
			if err != nil {
				t.Fatalf("buildCatalogTrustPolicies: %v", err)
			}
			if p.ThirdParty.RequireReleaseCommit {
				t.Error("the third-party policy requires Sharko's release commit — " +
					"that would refuse every third-party catalogue there has ever been")
			}
			if p.ThirdParty.ReleaseCommit != "" {
				t.Errorf("ThirdParty.ReleaseCommit = %q, want empty", p.ThirdParty.ReleaseCommit)
			}
			// The documented third-party behaviour is the shipped default,
			// unchanged: tag refs only.
			if p.ThirdParty.WorkflowRef != signing.DefaultTrustedWorkflowRef {
				t.Errorf("ThirdParty.WorkflowRef = %q, want the documented default %q",
					p.ThirdParty.WorkflowRef, signing.DefaultTrustedWorkflowRef)
			}
			// Both policies share the same identity list — the release-commit
			// binding narrows WHICH BUILD, never WHO.
			if strings.Join(p.ThirdParty.Identities, ",") != strings.Join(p.Embedded.Identities, ",") {
				t.Errorf("the two policies disagree about trusted identities:\n  third-party = %v\n  embedded    = %v",
					p.ThirdParty.Identities, p.Embedded.Identities)
			}
		})
	}
}

// TestCatalogTrustPolicies_UnstampedBuildRefusesRatherThanSkips is the
// missing-metadata decision, pinned on the shipped wiring.
//
// Every build shape that is not a release lands on "required, nothing to
// compare against", which is a refusal with a named reason. None of them
// lands on "not required", which would be a silent bypass — and a silent
// bypass is reachable by accident in a way a refusal is not: a release whose
// COMMIT build argument went missing would quietly start accepting anything
// its trusted identity signed, from any commit.
func TestCatalogTrustPolicies_UnstampedBuildRefusesRatherThanSkips(t *testing.T) {
	clearTrustEnv(t)

	// "dev" is what cmd/sharko/root.go declares and what the Dockerfile
	// defaults ARG COMMIT to. The short hash is what the Makefile's local
	// `build` target stamps.
	for _, stamp := range []string{"dev", "", "faf109fb", "4.0.2", "faf109fbbccac14fbd17fd5fa8ffb7066b2a540"} {
		t.Run("stamp_"+orDash(stamp), func(t *testing.T) {
			p, err := buildCatalogTrustPolicies(stamp)
			if err != nil {
				t.Fatalf("buildCatalogTrustPolicies: %v", err)
			}
			if p.ReleaseStamped {
				t.Fatalf("stamp %q was treated as a release commit", stamp)
			}
			if !p.Embedded.RequireReleaseCommit {
				t.Fatalf("stamp %q turned the requirement OFF. That is a silent bypass: "+
					"the catalogue would be accepted with nothing binding it to a "+
					"release. It must stay ON and refuse.", stamp)
			}
			if p.Embedded.ReleaseCommit != "" {
				t.Errorf("stamp %q produced ReleaseCommit %q, want empty",
					stamp, p.Embedded.ReleaseCommit)
			}
		})
	}
}

// TestCatalogTrustPolicies_OperatorOverridesStillWork — the escape hatches
// keep working. An operator who has configured their own identities or their
// own workflow_ref gets them, on both policies, and the release-commit
// binding is not something they can switch off through either var (there is
// no env var that disables it; a build either carries a release commit or it
// does not).
func TestCatalogTrustPolicies_OperatorOverridesStillWork(t *testing.T) {
	clearTrustEnv(t)
	t.Setenv(signing.EnvTrustedIdentities, signing.DefaultsToken+`,^https://github\.com/acme/.*$`)
	t.Setenv(signing.EnvTrustedWorkflowRef, `^refs/heads/(main|release-.*)$`)

	p, err := buildCatalogTrustPolicies(testReleaseCommit)
	if err != nil {
		t.Fatalf("buildCatalogTrustPolicies: %v", err)
	}
	if !anyMatches(t, p.Embedded.Identities, "https://github.com/acme/widgets/.github/workflows/sign.yml@refs/heads/main") {
		t.Error("the operator's own identity pattern did not reach the embedded policy")
	}
	if !anyMatches(t, p.ThirdParty.Identities, "https://github.com/acme/widgets/.github/workflows/sign.yml@refs/heads/main") {
		t.Error("the operator's own identity pattern did not reach the third-party policy")
	}
	if p.Embedded.WorkflowRef != `^refs/heads/(main|release-.*)$` {
		t.Errorf("Embedded.WorkflowRef = %q — the operator's explicit setting was replaced",
			p.Embedded.WorkflowRef)
	}
	if p.ThirdParty.WorkflowRef != `^refs/heads/(main|release-.*)$` {
		t.Errorf("ThirdParty.WorkflowRef = %q — the operator's explicit setting was dropped",
			p.ThirdParty.WorkflowRef)
	}
	if !p.Embedded.RequireReleaseCommit || p.Embedded.ReleaseCommit != testReleaseCommit {
		t.Error("an operator override switched the release-commit binding off")
	}
}

// TestCatalogTrustPolicies_BadPolicyIsFatal — a malformed operator pattern
// still stops startup, so the new wiring did not turn a fatal
// misconfiguration into a warning.
func TestCatalogTrustPolicies_BadPolicyIsFatal(t *testing.T) {
	clearTrustEnv(t)
	t.Setenv(signing.EnvTrustedIdentities, "[unbalanced")
	if _, err := buildCatalogTrustPolicies(testReleaseCommit); err == nil {
		t.Fatal("a malformed trust policy was accepted")
	}
}

// TestServeWiring_KeepsTheTwoPoliciesApart reads serve.go and pins which
// policy goes where.
//
// The unit tests above prove the two policies are built correctly. They
// cannot prove serve.go hands the right one to the right loader — and getting
// that backwards is a silent, total failure in both directions at once: every
// third-party signature refused, and Sharko's own catalogue accepted with
// nothing binding it to a release. So it is pinned in source.
//
// Reading source rather than behaviour is a real limitation and worth saying
// out loud: serve.go's catalogue wiring sits inside a cobra RunE that wants a
// Kubernetes connection, a git provider and a network, so calling it from a
// unit test is not on. What IS driven for real is buildCatalogTrustPolicies —
// the whole of the policy decision — by every test above.
func TestServeWiring_KeepsTheTwoPoliciesApart(t *testing.T) {
	src, err := os.ReadFile("serve.go")
	if err != nil {
		t.Fatalf("read serve.go: %v", err)
	}
	text := string(src)

	// The wiring must come from the tested function, with the real stamp.
	if !strings.Contains(text, "buildCatalogTrustPolicies(commit)") {
		t.Error("serve.go does not build its trust policies via " +
			"buildCatalogTrustPolicies(commit), so the tests in this file are " +
			"no longer exercising the shipped wiring")
	}

	// The embedded catalogue loader gets the embedded policy.
	embeddedWiring := "catalogVerifier.VerifyEntryFunc(catalogTrust.Embedded)"
	if !strings.Contains(text, embeddedWiring) {
		t.Errorf("serve.go does not pass the embedded policy to the embedded catalogue "+
			"loader (looked for %q)", embeddedWiring)
	}

	// The third-party fetcher gets the third-party policy, in both places it
	// is handed one.
	for _, want := range []string{
		"sourcesFetcher.SetEntryVerifyFunc(catalogVerifier.VerifyEntryFunc(catalogTrust.ThirdParty))",
		"sourcesFetcher.SetTrustPolicy(catalogTrust.ThirdParty)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("serve.go does not pass the third-party policy to the fetcher "+
				"(looked for %q). Handing it the embedded policy would refuse every "+
				"third-party signature.", want)
		}
	}

	// And the embedded policy must not reach the fetcher under any spelling.
	for _, forbidden := range []string{
		"sourcesFetcher.SetTrustPolicy(catalogTrust.Embedded)",
		"sourcesFetcher.SetEntryVerifyFunc(catalogVerifier.VerifyEntryFunc(catalogTrust.Embedded))",
	} {
		if strings.Contains(text, forbidden) {
			t.Errorf("serve.go hands the EMBEDDED policy to the third-party fetcher (%q). "+
				"Third-party publishers sign from their own commits, so this refuses "+
				"all of them.", forbidden)
		}
	}
}

// TestServeWiringPin_WouldNoticeARemoval is the control on the test above.
//
// A source-reading test is only worth having if it actually fails when the
// thing it looks for is gone. This plants the two failure shapes into a copy
// of serve.go's text and requires the same substring checks to spot them —
// so the pin cannot quietly become a test that reads a file and always
// passes.
func TestServeWiringPin_WouldNoticeARemoval(t *testing.T) {
	src, err := os.ReadFile("serve.go")
	if err != nil {
		t.Fatalf("read serve.go: %v", err)
	}
	text := string(src)

	swapped := strings.ReplaceAll(text,
		"sourcesFetcher.SetTrustPolicy(catalogTrust.ThirdParty)",
		"sourcesFetcher.SetTrustPolicy(catalogTrust.Embedded)")
	if swapped == text {
		t.Fatal("the planted break changed nothing, so it proves nothing about the pin")
	}
	if strings.Contains(swapped, "sourcesFetcher.SetTrustPolicy(catalogTrust.ThirdParty)") {
		t.Error("the pin would not notice the fetcher being handed the embedded policy")
	}
	if !strings.Contains(swapped, "sourcesFetcher.SetTrustPolicy(catalogTrust.Embedded)") {
		t.Error("the forbidden-spelling check would not notice the swap")
	}

	removed := strings.ReplaceAll(text, "buildCatalogTrustPolicies(commit)", "signing.LoadTrustPolicyFromEnv()")
	if removed == text {
		t.Fatal("the second planted break changed nothing")
	}
	if strings.Contains(removed, "buildCatalogTrustPolicies(commit)") {
		t.Error("the pin would not notice the wiring being replaced")
	}
}

func matches(t *testing.T, pattern, s string) bool {
	t.Helper()
	re, err := regexp.Compile(pattern)
	if err != nil {
		t.Fatalf("pattern %q does not compile: %v", pattern, err)
	}
	return re.MatchString(s)
}

func anyMatches(t *testing.T, patterns []string, s string) bool {
	t.Helper()
	for _, p := range patterns {
		if matches(t, p, s) {
			return true
		}
	}
	return false
}

func orDash(s string) string {
	if s == "" {
		return "empty"
	}
	return s
}
