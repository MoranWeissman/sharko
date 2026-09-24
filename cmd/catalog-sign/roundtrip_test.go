//go:build roundtrip
// +build roundtrip

// Real-cosign roundtrip test (V124-1.3).
//
// Closes the validation gap that necessitated four throwaway -rc tags
// during v1.23: every rc surfaced a production-only bug because the unit
// suite stubbed the cosign binary with a fakeSigner (deliberately — fast,
// hermetic) but no CI test exercised the actual bytes flowing from
// `cosign sign-blob --new-bundle-format` through the production
// signing.Verifier reader. Writer ↔ reader byte-format drift could only
// surface on a real tag push to main.
//
// This test runs ONLY when invoked with `-tags=roundtrip` and a real
// cosign binary on $PATH. The dedicated CI workflow
// `.github/workflows/catalog-sign-roundtrip.yml` is the production caller;
// `go test ./...` without the tag continues to pass with no cosign required.
//
// What it does:
//  1. Loads the fixture catalog (testdata/roundtrip/addons.yaml) — two
//     synthetic entries, NOT the real curated catalog.
//  2. For one entry, calls signing.CanonicalEntryBytes — the exact same
//     function the production writer (cmd/catalog-sign/main.go run()) and
//     the production reader (internal/catalog/signing/verify.go) use to
//     produce/consume the signed message bytes.
//  3. Calls the PRODUCTION cosignCLI{}.SignBlob — same code path, same
//     argv, same --new-bundle-format flag — to produce a real Sigstore
//     bundle file on disk. Keyless flow uses the GitHub Actions OIDC
//     token; Fulcio mints a short-lived cert with the workflow ref in the
//     SAN; Rekor records the signature.
//  4. Reads the resulting bundle bytes back, fetches the public-good
//     Sigstore trust root via signing.LoadProductionTrustedRoot, and
//     hands the bytes + the canonical payload to signing.Verifier
//     (which is the in-process reader serve.go wires up at boot).
//  5. Trust policy regex is derived from the running workflow's expected
//     OIDC SAN — defaults to a permissive match-anything-from-this-repo
//     pattern, overridable via SHARKO_ROUNDTRIP_TRUST_REGEX so the
//     pattern can be tightened (or run against a different repo fork)
//     without code changes.
//  6. Asserts verified=true. A divergence between writer and reader byte
//     format would fail this assertion loud — the exact regression the
//     v1.23 rc-tag chain kept surfacing.
//  7. Drives the release-commit binding over that same real certificate:
//     a different commit must be refused by the binding, a build with no
//     release commit must be refused rather than skipped, and the
//     certificate's source-commit claim must equal GITHUB_SHA. See
//     assertReleaseCommitBinding for exactly what each of those proves.
//     This is the only place the binding meets a genuine Fulcio
//     certificate outside a release, because the workflow the binding
//     ships in triggers on workflow_run and no pull-request check
//     reaches it.
//
// Failure modes (all loud):
//   - cosign binary missing → SkipNow with a clear message (so a local
//     dev run without cosign installed degrades to "skipped" rather than
//     "failed").
//   - SHARKO_SKIP_TUF_NETWORK set → SkipNow (parity with the existing
//     tufroot_test.go convention; air-gapped CI environments).
//   - TUF fetch fails → Fatal (trust root is non-optional for the test).
//   - cosign sign-blob fails → Fatal (often missing OIDC token or
//     network; surface verbatim so the operator can fix).
//   - verifier returns verified=false → Fatal with a "writer/reader
//     byte-format DIVERGED" error message so the failure is unambiguous
//     in CI logs.
package main

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/catalog/signing"
	"github.com/MoranWeissman/sharko/internal/catalog/sources"
)

const (
	// fixtureRelPath is the on-disk location of the synthetic fixture
	// catalog. Tests run from the package directory (cmd/catalog-sign),
	// so this is relative to that.
	fixtureRelPath = "testdata/roundtrip/addons.yaml"

	// skipTUFNetworkEnvVar mirrors the convention established by
	// tufroot_test.go's TestLoadProductionTrustedRoot_Smoke. Set to any
	// non-empty value to skip every test in this file that needs to
	// fetch the Sigstore trust root.
	skipTUFNetworkEnvVar = "SHARKO_SKIP_TUF_NETWORK"

	// trustRegexEnvVar lets the CI workflow (or a contributor running
	// the test locally with a different identity) override the trust
	// policy regex applied to the cert SAN. Defaults to a permissive
	// "any SAN issued under the MoranWeissman/sharko repo" pattern so
	// the test passes for PRs from the canonical repo without any env
	// configuration.
	trustRegexEnvVar = "SHARKO_ROUNDTRIP_TRUST_REGEX"

	// defaultTrustRegex matches any GitHub Actions workflow SAN from
	// the canonical repo. The roundtrip workflow's SAN will look like
	//   https://github.com/MoranWeissman/sharko/.github/workflows/catalog-sign-roundtrip.yml@refs/heads/<branch>
	// or
	//   https://github.com/MoranWeissman/sharko/.github/workflows/catalog-sign-roundtrip.yml@refs/pull/<n>/merge
	// — both match this anchored pattern.
	defaultTrustRegex = `^https://github\.com/MoranWeissman/sharko/\.github/workflows/.*$`

	// V124-1.4 — cert-claim assertion regex. Roundtrip CI runs against
	// pull_request / push events on this repo, NOT against tag refs, so
	// the production default (`^refs/tags/v.*$`) would reject the test
	// bundle. The roundtrip overrides this with a permissive pattern
	// that accepts any `refs/heads/...` or `refs/pull/...` ref — which
	// is the actual workflow_ref shape minted by Fulcio for the
	// pull_request and push triggers in catalog-sign-roundtrip.yml.
	//
	// Overridable via SHARKO_ROUNDTRIP_WORKFLOW_REF_REGEX so an operator
	// running this test from a different trigger (e.g. tag) can tighten
	// or relax the pattern without code changes.
	workflowRefRegexEnvVar = "SHARKO_ROUNDTRIP_WORKFLOW_REF_REGEX"

	// defaultWorkflowRefRegex matches the workflow_ref shape minted for
	// pull_request (`refs/pull/<n>/merge`), push to a branch
	// (`refs/heads/<branch>`), and tag pushes (`refs/tags/<tag>`). The
	// pattern is permissive on purpose — the roundtrip test asserts
	// byte-format and trust-pipeline correctness, NOT that the policy
	// shipped to production matches every possible CI trigger.
	defaultWorkflowRefRegex = `^refs/(heads/.*|pull/.*|tags/.*)$`

	// githubSHAEnvVar is the commit GitHub Actions says the run was
	// triggered by. Every GitHub-hosted job has it set; a laptop run does
	// not, which is the one case the agreement check below steps around.
	githubSHAEnvVar = "GITHUB_SHA"

	// wrongCommitControl is a syntactically valid full commit hash that is
	// not a commit in any repository. It is the control for the
	// release-commit binding: handing it to the binding must produce a
	// refusal, which is what proves the binding is doing work rather than
	// waving everything through.
	wrongCommitControl = "0123456789abcdef0123456789abcdef01234567"
)

// TestRoundtrip_RealCosignAndVerifier is the only test in this file. It
// is the single roundtrip assertion: signed-by-production-writer bytes
// verify-by-production-reader. Failing this test means the v1.23 class
// of bugs has resurfaced.
func TestRoundtrip_RealCosignAndVerifier(t *testing.T) {
	if os.Getenv(skipTUFNetworkEnvVar) != "" {
		t.Skipf("%s is set; skipping network-dependent roundtrip test", skipTUFNetworkEnvVar)
	}
	if _, err := exec.LookPath("cosign"); err != nil {
		t.Skipf("cosign binary not on $PATH (%v); skipping roundtrip test", err)
	}
	// Local-dev ergonomics: cosign keyless needs an ambient OIDC token.
	// In GitHub Actions that comes from `permissions: id-token: write`;
	// outside CI cosign falls back to an interactive browser device flow
	// that blocks the test for ~5 minutes before timing out. Skip when
	// not in CI unless the operator explicitly opts in by setting
	// SHARKO_ROUNDTRIP_ALLOW_INTERACTIVE — that escape hatch keeps the
	// "I want to test this on my laptop" workflow alive without
	// forcing every laptop run to block for the device-flow timeout.
	if os.Getenv("CI") == "" && os.Getenv("GITHUB_ACTIONS") == "" &&
		os.Getenv("SHARKO_ROUNDTRIP_ALLOW_INTERACTIVE") == "" {
		t.Skip("not running in CI and SHARKO_ROUNDTRIP_ALLOW_INTERACTIVE unset; " +
			"skipping (cosign keyless without an ambient OIDC token would block on " +
			"the browser device flow). Set SHARKO_ROUNDTRIP_ALLOW_INTERACTIVE=1 to " +
			"override and run the interactive sign-in.")
	}

	// Step 1: load the synthetic fixture. We deliberately do NOT use
	// the production catalog here — the goal is to test byte-format
	// agreement, not catalog content. A small fixture also keeps the
	// CI job fast (one sign call vs. one per real catalog entry).
	fixtureBytes, err := os.ReadFile(fixtureRelPath)
	if err != nil {
		t.Fatalf("read fixture %s: %v (cwd=%s)", fixtureRelPath, err, mustCWD())
	}
	var fixture struct {
		Addons []catalog.CatalogEntry `yaml:"addons"`
	}
	if err := yaml.Unmarshal(fixtureBytes, &fixture); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	if len(fixture.Addons) == 0 {
		t.Fatal("fixture has zero entries — check testdata/roundtrip/addons.yaml")
	}
	// Validation parity: the runtime loader rejects the same schema
	// errors the writer rejects. If the fixture is malformed, fail here
	// rather than later in a confusing cosign error.
	if _, err := catalog.LoadBytes(fixtureBytes); err != nil {
		t.Fatalf("fixture failed catalog validation: %v", err)
	}

	entry := fixture.Addons[0]
	t.Logf("roundtrip entry: %s", entry.Name)

	// Step 2: produce the canonical payload bytes — the SAME function the
	// production writer (cmd/catalog-sign/main.go) and the production
	// reader (internal/catalog/signing/verify.go) use. This is the
	// single source of truth for "what bytes are actually signed."
	canonical, err := signing.CanonicalEntryBytes(entry)
	if err != nil {
		t.Fatalf("CanonicalEntryBytes(%s): %v", entry.Name, err)
	}
	t.Logf("canonical payload: %d bytes", len(canonical))

	// Step 3: invoke the PRODUCTION cosignCLI signer — same code path as
	// the release pipeline. If anyone deletes --new-bundle-format from
	// signBlobArgs, this test produces a legacy-format bundle that the
	// modern sigstore-go reader refuses, and the verify step below fails
	// loud — pinning the v1.23 rc.1 regression cryptographically.
	tmp := t.TempDir()
	payloadPath := filepath.Join(tmp, entry.Name+".payload")
	if err := os.WriteFile(payloadPath, canonical, 0o644); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	out := signOutputs{
		BundlePath: filepath.Join(tmp, entry.Name+".bundle"),
		SigPath:    filepath.Join(tmp, entry.Name+".sig"),
		CertPath:   filepath.Join(tmp, entry.Name+".pem"),
	}
	// COSIGN_YES suppresses the interactive prompt in non-TTY contexts
	// (mirrors release.yml's env). The os.Setenv form is fine here
	// because the test is single-goroutine and the workflow process exits
	// after one test run.
	t.Setenv("COSIGN_YES", "true")
	if err := (cosignCLI{}).SignBlob(payloadPath, out); err != nil {
		t.Fatalf("cosign sign-blob failed: %v\n"+
			"(common cause: missing GitHub Actions OIDC token — this test "+
			"requires `permissions: id-token: write` in the workflow)", err)
	}
	bundleBytes, err := os.ReadFile(out.BundlePath)
	if err != nil {
		t.Fatalf("read produced bundle: %v", err)
	}
	t.Logf("cosign produced bundle: %d bytes", len(bundleBytes))

	// Step 4: assemble the production reader. Trust root comes from the
	// real public-good Sigstore TUF mirror (same code path serve.go runs
	// at boot). Trust policy is the regex described in defaultTrustRegex
	// — overridable so the test works for forks/dev branches.
	ctx := context.Background()
	tr, err := signing.LoadProductionTrustedRoot(ctx)
	if err != nil {
		t.Fatalf("LoadProductionTrustedRoot: %v (set %s=1 to skip if air-gapped)", err, skipTUFNetworkEnvVar)
	}

	trustRegex := os.Getenv(trustRegexEnvVar)
	if trustRegex == "" {
		trustRegex = defaultTrustRegex
	}
	// Sanity-compile the regex up front so a malformed override fails
	// here rather than inside the verifier (where the error wording is
	// less helpful for someone wiring the env var).
	if _, err := regexp.Compile(trustRegex); err != nil {
		t.Fatalf("invalid %s=%q: %v", trustRegexEnvVar, trustRegex, err)
	}
	// V124-1.4 — cert-claim assertion. Configured separately from the
	// SAN regex so the roundtrip exercises the full production verify
	// pipeline (SAN check + workflow_ref check), pinning a regression
	// in either layer.
	workflowRefRegex := os.Getenv(workflowRefRegexEnvVar)
	if workflowRefRegex == "" {
		workflowRefRegex = defaultWorkflowRefRegex
	}
	if _, err := regexp.Compile(workflowRefRegex); err != nil {
		t.Fatalf("invalid %s=%q: %v", workflowRefRegexEnvVar, workflowRefRegex, err)
	}
	policy := sources.TrustPolicy{
		Identities:  []string{trustRegex},
		WorkflowRef: workflowRefRegex,
	}

	// Use the in-process verifyBundleBytes path via the per-entry HTTP
	// wrapper — host the bundle on a local httptest server so the test
	// exercises the same Verifier.VerifyEntry codepath the loader runs
	// in production (HTTP fetch + parse + verify), not just the in-memory
	// verifyBundleBytes shortcut. That covers an extra layer of "are the
	// bytes round-tripping cleanly through HTTP" without adding flakiness.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(bundleBytes)
	}))
	defer srv.Close()

	// Capture verifier log output so a failure shows the verifier's
	// `reason` attribute (sig-mismatch vs untrusted-identity vs other),
	// which is otherwise invisible — all three collapse to the same
	// public (false, "", nil) return.
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	v := signing.NewVerifier(srv.Client(),
		signing.WithTrustedMaterial(tr),
		signing.WithLogger(logger),
	)

	verified, issuer, verr := v.VerifyEntry(ctx, canonical, srv.URL+"/"+entry.Name+".bundle", policy)
	if verr != nil {
		t.Fatalf("verifier returned infrastructure error: %v\nverifier log:\n%s",
			verr, logBuf.String())
	}
	if !verified {
		t.Fatalf(`WRITER ↔ READER BYTE-FORMAT DIVERGED.

The production cosignCLI signer produced a bundle the production
signing.Verifier reader refused to verify. This is the exact regression
class that ate 4 throwaway -rc tags in the v1.23 release ship.

Common root causes:
  - --new-bundle-format flag missing from cosignCLI.signBlobArgs
    (cosign falls back to the legacy {base64Signature, cert} shape that
    sigstore-go's bundle parser refuses).
  - canonicalBytes contract change in catalog/loader.go that the
    signing.CanonicalEntryBytes accessor no longer mirrors.
  - sigstore-go major-version bump that changed the bundle parser
    contract without a writer-side counterpart.
  - Trust policy SAN regex (%s=%q) doesn't match the cert SAN the
    workflow's OIDC token produces.
  - V124-1.4 cert-claim assertion (%s=%q) doesn't match the cert's
    workflow_ref claim — check the workflow trigger (pull_request vs
    push vs tag) and the policy regex shape.

Verifier log:
%s`, trustRegexEnvVar, trustRegex, workflowRefRegexEnvVar, workflowRefRegex, logBuf.String())
	}
	if issuer == "" {
		t.Fatalf("verified=true but issuer is empty — verifier contract violation\nverifier log:\n%s", logBuf.String())
	}
	t.Logf("roundtrip PASS: entry=%s issuer=%s", entry.Name, issuer)

	// Step 5: the release-commit binding, against the certificate that was
	// just minted. Everything above this line is the byte-format roundtrip;
	// everything below it is the binding.
	assertReleaseCommitBinding(t, ctx, v, canonical,
		srv.URL+"/"+entry.Name+".bundle", policy, &logBuf)
}

// assertReleaseCommitBinding drives the release-commit binding through the
// production Verifier against the real Fulcio certificate the signing step
// above just obtained.
//
// # Why this belongs here and nowhere else
//
// The binding is what `sharko serve` applies to its own embedded catalogue,
// and the release workflow's verify gate applies the same code before the
// catalogue is embedded. Neither of those paths runs on a pull request:
// release.yml triggers on `workflow_run`, so no PR check reaches it, and the
// first execution inside GitHub Actions would otherwise be the first real
// release. This job already mints a genuine keyless Fulcio certificate on
// every pull request and push, so it is the only place the binding can meet a
// real certificate before a release depends on it. A local run cannot: a valid
// Sigstore bundle cannot be minted without an OIDC token, and the test fixtures
// that stand in for one cannot carry Fulcio's commit extensions.
//
// # Which claim carries the commit, and where it comes from
//
// Fulcio's GitHub Actions issuer fills four extensions from three OIDC token
// claims: `sourceRepositoryDigest` (OID 1.3.6.1.4.1.57264.1.13) and the older
// `githubWorkflowSHA` (.1.3) both come from the token's `sha` claim — the
// commit the run was triggered by; `buildSignerDigest` (.1.10) comes from
// `job_workflow_sha` and `buildConfigDigest` (.1.19) from `workflow_sha`, and
// both of those describe the workflow FILE rather than the source. The
// production extractor reads .1.13 and falls back to .1.3, so what it reads is
// the `sha` claim either way. GitHub sets `GITHUB_SHA` from the same commit, on
// both of this workflow's triggers — on a pull request that is the ephemeral
// merge commit of `refs/pull/<n>/merge`, which is also what `actions/checkout`
// leaves at HEAD, and on a push it is the pushed commit.
//
// # What each assertion proves, and what it does not
//
//   - "a different commit is refused" is the load-bearing one. It hands the
//     binding a valid-shaped hash that is not the certificate's claim and
//     requires a refusal whose reason names the binding. That proves three
//     things at once: the production extractor pulled a claim out of a real
//     Fulcio certificate (a refusal for a MISSING claim says so in different
//     words, and is failed here), the comparison rejects a mismatch, and the
//     whole thing is reached through the same VerifyEntry the loader calls.
//   - "no release commit available is refused" pins the missing-stamp branch
//     on a genuine signature rather than on a fixture. An unstamped build must
//     refuse, never skip.
//   - "the claim equals GITHUB_SHA" is the agreement check, and it is the one
//     assertion that compares the certificate against something outside it.
//     It is NOT a comparison of a value with itself: the expected side is read
//     out of the runner's environment and the claimed side out of a signed
//     certificate extension. If it ever fails, that is information about what
//     Fulcio stamps on this trigger and not a signing bug — the failure message
//     says so, and the verifier log it prints names both values.
//
// None of this proves anything about the `workflow_run` trigger the real
// release uses. A release certificate is minted from a different event, and the
// only evidence about that trigger is the 45 published v4.0.1 certificates,
// which were decoded by hand.
//
// It adds no network use: the bundle is served by the same in-process httptest
// server the roundtrip already stood up, and the trust root is the one already
// fetched. So a network hiccup cannot surface here as a binding failure.
func assertReleaseCommitBinding(
	t *testing.T,
	ctx context.Context,
	v *signing.Verifier,
	canonical []byte,
	bundleURL string,
	base sources.TrustPolicy,
	logBuf *bytes.Buffer,
) {
	t.Helper()

	// bound copies the policy the roundtrip already verified under and turns
	// the binding on, so identity and workflow_ref are known to pass and the
	// only thing under test is the commit comparison.
	bound := func(commit string) sources.TrustPolicy {
		p := base
		p.RequireReleaseCommit = true
		p.ReleaseCommit = commit
		return p
	}
	verify := func(t *testing.T, label string, p sources.TrustPolicy) (bool, string) {
		t.Helper()
		logBuf.Reset()
		verified, _, err := v.VerifyEntry(ctx, canonical, bundleURL, p)
		if err != nil {
			t.Fatalf("%s: verifier returned an infrastructure error, so this case measured "+
				"nothing about the binding: %v\nverifier log:\n%s", label, err, logBuf.String())
		}
		return verified, logBuf.String()
	}

	t.Run("a different commit is refused", func(t *testing.T) {
		verified, log := verify(t, "wrong-commit control", bound(wrongCommitControl))
		if verified {
			t.Fatalf("the binding accepted a certificate whose source-commit claim cannot be "+
				"%s, so it is not comparing anything. Verifier log:\n%s", wrongCommitControl, log)
		}
		// Refused for the RIGHT reason. A refusal because the certificate had
		// no claim at all would also be a refusal, and would mean the
		// extractor read nothing out of a real Fulcio certificate — which is
		// most of what this test exists to check.
		for _, want := range []string{"release-commit binding failed", "claims source commit"} {
			if !strings.Contains(log, want) {
				t.Fatalf("the wrong-commit run was refused, but not by the binding, so it proves "+
					"nothing: the reason does not contain %q.\nVerifier log:\n%s", want, log)
			}
		}
		// The reason line names the claim the production extractor read out of
		// the certificate, so this is where the measured value is recorded for
		// whoever reads the workflow log later.
		t.Logf("the production extractor read this claim out of the real Fulcio certificate: %s",
			strings.TrimSpace(log))
	})

	t.Run("no release commit available is refused", func(t *testing.T) {
		verified, log := verify(t, "unstamped control", bound(""))
		if verified {
			t.Fatalf("a build with no release commit accepted its own signed catalogue. That is "+
				"the silent bypass the binding exists to prevent.\nVerifier log:\n%s", log)
		}
		if !strings.Contains(log, "this build carries no release commit") {
			t.Fatalf("the unstamped run was refused for an unrelated reason, so the missing-stamp "+
				"branch is unproven.\nVerifier log:\n%s", log)
		}
	})

	t.Run("the claim equals GITHUB_SHA", func(t *testing.T) {
		sha := strings.TrimSpace(os.Getenv(githubSHAEnvVar))
		if sha == "" || !signing.IsFullCommitSHA(sha) {
			// Not inside a GitHub Actions job — a laptop run with
			// SHARKO_ROUNDTRIP_ALLOW_INTERACTIVE, for instance. This is a
			// statement about the environment, not about the product, so it
			// skips rather than passing quietly or failing.
			t.Skipf("%s is %q, which is not a full 40-character commit hash, so there is nothing "+
				"outside the certificate to compare its claim against", githubSHAEnvVar, sha)
		}
		if strings.EqualFold(sha, wrongCommitControl) {
			t.Fatalf("%s equals the wrong-commit control %s, so the control above proved nothing",
				githubSHAEnvVar, wrongCommitControl)
		}
		verified, log := verify(t, "GITHUB_SHA agreement", bound(sha))
		if !verified {
			t.Fatalf(`THE CERTIFICATE'S SOURCE-COMMIT CLAIM IS NOT %s=%s.

Read the verifier log below: it names the commit the certificate claims and the
commit that was expected. If the two are simply different commits, this is a
fact about what Fulcio stamps for the %q trigger, NOT a signing failure and NOT
evidence of tampering — Fulcio fills sourceRepositoryDigest (OID
1.3.6.1.4.1.57264.1.13) from the OIDC token's "sha" claim, and this assertion
is the check that the two agree. Fix the assertion to match what was measured,
and say in this comment what the two values were.

If instead the log says the certificate carries NO source-commit claim, that is
a real regression in the extraction path: sigstore-go stopped exposing the
extension, or Fulcio stopped stamping it.

Verifier log:
%s`, githubSHAEnvVar, sha, os.Getenv("GITHUB_EVENT_NAME"), log)
		}
		t.Logf("release-commit binding PASS: the certificate's source-commit claim equals "+
			"%s=%s on the %q trigger", githubSHAEnvVar, sha, os.Getenv("GITHUB_EVENT_NAME"))
	})
}

// mustCWD returns the current working directory for diagnostic messages.
// Returns "?" on error so the test failure message is still readable.
func mustCWD() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "?"
	}
	return cwd
}
