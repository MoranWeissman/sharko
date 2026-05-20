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
	policy := sources.TrustPolicy{Identities: []string{trustRegex}}

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
  - Trust policy regex (%s=%q) doesn't match the cert SAN the
    workflow's OIDC token produces.

Verifier log:
%s`, trustRegexEnvVar, trustRegex, logBuf.String())
	}
	if issuer == "" {
		t.Fatalf("verified=true but issuer is empty — verifier contract violation\nverifier log:\n%s", logBuf.String())
	}
	t.Logf("roundtrip PASS: entry=%s issuer=%s", entry.Name, issuer)
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
