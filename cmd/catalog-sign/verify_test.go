// Tests for the release-time verify gate.
//
// These run in ordinary CI, so the Sigstore trust root and the real
// signature checks are replaced by a fake verifier. What they pin is the
// part a fake can prove: that the gate checks every entry, that it hands
// the verifier the canonical entry bytes and nothing else, and that every
// way of going wrong is fail-closed rather than fail-open.
//
// The cryptography itself is exercised elsewhere: by the roundtrip job in
// CI (real cosign, real Fulcio, real Rekor) and by the by-hand break tests
// run against the published v4.0.1 bundles.
package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/catalog/signing"
	"github.com/MoranWeissman/sharko/internal/catalog/sources"
)

// fakeVerifier answers every call the same way and records what it was
// asked about, so a test can assert on the payload the gate computed.
type fakeVerifier struct {
	verified bool
	issuer   string
	err      error
	payloads [][]byte
	bundles  [][]byte
	calls    int
	// failFor makes exactly one entry fail, keyed on a substring of the
	// payload. Used to prove one bad entry out of many stops the job.
	failFor string
	// policies records the trust policy the gate handed over, per call. The
	// gate is supposed to verify under the policy `sharko serve` will apply
	// to the catalogue once it is embedded — release-commit binding included
	// — so what it passes here is part of what is being tested.
	policies []sources.TrustPolicy
}

func (f *fakeVerifier) VerifyBundleBytes(_ context.Context, payload, bundleBytes []byte, policy sources.TrustPolicy) (bool, string, error) {
	f.calls++
	f.payloads = append(f.payloads, payload)
	f.bundles = append(f.bundles, bundleBytes)
	f.policies = append(f.policies, policy)
	if f.failFor != "" && strings.Contains(string(payload), f.failFor) {
		return false, "", nil
	}
	return f.verified, f.issuer, f.err
}

const testIdentity = "https://github.com/MoranWeissman/sharko/.github/workflows/release.yml@refs/heads/main"

// testReleaseCommit is the commit v4.0.1 was released from, taken from the
// tag object. The release workflow supplies the equivalent value from
// github.event.workflow_run.head_sha — from GitHub's record of what is being
// released, never from a certificate.
const testReleaseCommit = "faf109fbbccac14fbd17fd5fa8ffb7066b2a5406"

// depsFor wires a fake verifier and the shipped production trust policy.
func depsFor(v bundleVerifier) verifyDeps {
	return verifyDeps{
		NewVerifier: func(context.Context) (bundleVerifier, error) { return v, nil },
		LoadPolicy:  signing.LoadTrustPolicyFromEnv,
	}
}

// buildSignedDir produces a directory shaped exactly like the one the
// release workflow hands to the embed step: addons.yaml.signed plus one
// bundle per entry.
func buildSignedDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := run(options{OutDir: dir, ReleaseBaseURL: fakeReleaseBase},
		fakeSigner{bundleBytes: []byte("bundle-bytes-stand-in")}); err != nil {
		t.Fatalf("build signed dir: %v", err)
	}
	return dir
}

func verifyOpts(dir string) options {
	return options{
		OutDir:         dir,
		ReleaseBaseURL: fakeReleaseBase,
		Verify:         true,
		ReleaseCommit:  testReleaseCommit,
	}
}

// TestVerify_AcceptsAFullyVerifiedCatalog is the positive control. Without
// it, every fail-closed test below could be passing for the wrong reason.
func TestVerify_AcceptsAFullyVerifiedCatalog(t *testing.T) {
	dir := buildSignedDir(t)
	fv := &fakeVerifier{verified: true, issuer: testIdentity}
	if err := runVerify(context.Background(), verifyOpts(dir), io.Discard, depsFor(fv), nil); err != nil {
		t.Fatalf("runVerify on a fully verified catalog: %v", err)
	}
	entries := readSignedYAML(t, dir)
	if fv.calls != len(entries) {
		t.Fatalf("verifier called %d times, want once per entry (%d)", fv.calls, len(entries))
	}
}

// TestVerify_ChecksTheCanonicalEntryBytes — the gate must hand the
// verifier the same message the signer signed. If it handed over the raw
// YAML, or the entry including its Signature field, every signature would
// mismatch and the gate would be useless in the other direction.
func TestVerify_ChecksTheCanonicalEntryBytes(t *testing.T) {
	dir := buildSignedDir(t)
	fv := &fakeVerifier{verified: true, issuer: testIdentity}
	if err := runVerify(context.Background(), verifyOpts(dir), io.Discard, depsFor(fv), nil); err != nil {
		t.Fatalf("runVerify: %v", err)
	}
	entries := readSignedYAML(t, dir)
	if len(fv.payloads) != len(entries) {
		t.Fatalf("payload count %d, want %d", len(fv.payloads), len(entries))
	}
	for i, e := range entries {
		want, err := signing.CanonicalEntryBytes(e)
		if err != nil {
			t.Fatalf("CanonicalEntryBytes(%s): %v", e.Name, err)
		}
		if string(fv.payloads[i]) != string(want) {
			t.Errorf("entry %s: gate verified different bytes than CanonicalEntryBytes produced", e.Name)
		}
		if string(fv.bundles[i]) != "bundle-bytes-stand-in" {
			t.Errorf("entry %s: gate passed the wrong bundle bytes", e.Name)
		}
	}
}

// TestVerify_FailsClosed walks every way the gate can be wrong and proves
// each one is a refusal, not a shrug. Each case mutates a freshly built
// directory, so a mutation cannot leak into the next case.
func TestVerify_FailsClosed(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(t *testing.T, dir string)
		verify  bundleVerifier
		deps    func(bundleVerifier) verifyDeps
		wantSub string
	}{
		{
			name:    "signature does not verify",
			verify:  &fakeVerifier{verified: false},
			wantSub: "failed verification",
		},
		{
			name:    "verifier returns an infrastructure error",
			verify:  &fakeVerifier{err: errors.New("bundle parse exploded")},
			wantSub: "bundle parse exploded",
		},
		{
			name:    "one entry out of many fails",
			verify:  &fakeVerifier{verified: true, issuer: testIdentity, failFor: "name: argocd\n"},
			wantSub: "1 of",
		},
		{
			name: "trust root unavailable",
			deps: func(bundleVerifier) verifyDeps {
				return verifyDeps{
					NewVerifier: func(context.Context) (bundleVerifier, error) {
						return nil, errors.New("sigstore trust root: no network")
					},
					LoadPolicy: signing.LoadTrustPolicyFromEnv,
				}
			},
			wantSub: "no network",
		},
		{
			name: "trust policy has no identities",
			deps: func(v bundleVerifier) verifyDeps {
				return verifyDeps{
					NewVerifier: func(context.Context) (bundleVerifier, error) { return v, nil },
					LoadPolicy: func() (sources.TrustPolicy, error) {
						return sources.TrustPolicy{}, nil
					},
				}
			},
			wantSub: "no identities",
		},
		{
			name: "signed catalog missing",
			mutate: func(t *testing.T, dir string) {
				if err := os.Remove(filepath.Join(dir, signedCatalogFile)); err != nil {
					t.Fatal(err)
				}
			},
			wantSub: "read",
		},
		{
			name: "signed catalog empty",
			mutate: func(t *testing.T, dir string) {
				if err := os.WriteFile(filepath.Join(dir, signedCatalogFile), []byte("   \n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantSub: "is empty",
		},
		{
			name: "signed catalog is not a catalog",
			mutate: func(t *testing.T, dir string) {
				if err := os.WriteFile(filepath.Join(dir, signedCatalogFile), []byte("addons: []\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantSub: "validation parity",
		},
		{
			name: "a bundle file is missing",
			mutate: func(t *testing.T, dir string) {
				if err := os.Remove(filepath.Join(dir, "argocd.bundle")); err != nil {
					t.Fatal(err)
				}
			},
			wantSub: "read bundle",
		},
		{
			name: "a bundle file is empty",
			mutate: func(t *testing.T, dir string) {
				if err := os.WriteFile(filepath.Join(dir, "argocd.bundle"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantSub: "is empty",
		},
		{
			name: "an entry lost its signature URL",
			mutate: func(t *testing.T, dir string) {
				stripSignatureStanza(t, dir, "argocd.bundle")
			},
			wantSub: "no signature.bundle URL",
		},
		{
			name: "a signature URL does not match the release base",
			mutate: func(t *testing.T, dir string) {
				p := filepath.Join(dir, signedCatalogFile)
				b, err := os.ReadFile(p) //nolint:gosec // test temp dir
				if err != nil {
					t.Fatal(err)
				}
				out := strings.Replace(string(b),
					fakeReleaseBase+"/argocd.bundle",
					"https://elsewhere.invalid/argocd.bundle", 1)
				if out == string(b) {
					t.Fatal("URL substitution did not apply")
				}
				if err := os.WriteFile(p, []byte(out), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantSub: "want",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := buildSignedDir(t)
			if tc.mutate != nil {
				tc.mutate(t, dir)
			}
			v := tc.verify
			if v == nil {
				v = &fakeVerifier{verified: true, issuer: testIdentity}
			}
			mk := tc.deps
			if mk == nil {
				mk = depsFor
			}
			var out bytes.Buffer
			err := runVerify(context.Background(), verifyOpts(dir), &out, mk(v), nil)
			if err == nil {
				t.Fatalf("expected a refusal, got nil error. output:\n%s", out.String())
			}
			// The per-entry detail goes to the report; the error is the
			// exit status. Both are part of what an operator reads, so
			// the assertion covers the pair.
			combined := out.String() + "\n" + err.Error()
			if !strings.Contains(combined, tc.wantSub) {
				t.Fatalf("neither the report nor the error mentions %q. got:\n%s", tc.wantSub, combined)
			}
		})
	}
}

// stripSignatureStanza removes the two-line `signature:` / `bundle: ...`
// block whose URL ends in the given suffix. Line-level so the rest of the
// file is untouched, which is what an attacker downgrading one entry to
// unsigned would do.
func stripSignatureStanza(t *testing.T, dir, bundleSuffix string) {
	t.Helper()
	p := filepath.Join(dir, signedCatalogFile)
	b, err := os.ReadFile(p) //nolint:gosec // test temp dir
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(b), "\n")
	out := make([]string, 0, len(lines))
	dropped := 0
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "signature:" && i+1 < len(lines) &&
			strings.HasSuffix(strings.TrimSpace(lines[i+1]), bundleSuffix) {
			i++
			dropped++
			continue
		}
		out = append(out, lines[i])
	}
	if dropped != 1 {
		t.Fatalf("expected to drop exactly one signature stanza, dropped %d", dropped)
	}
	if err := os.WriteFile(p, []byte(strings.Join(out, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestVerify_RejectsIncompleteDeps — a future caller that forgets to wire
// a verifier must not get a silent pass.
func TestVerify_RejectsIncompleteDeps(t *testing.T) {
	dir := buildSignedDir(t)
	err := runVerify(context.Background(), verifyOpts(dir), io.Discard, verifyDeps{}, nil)
	if err == nil {
		t.Fatal("expected a refusal for empty deps, got nil")
	}
	if !strings.Contains(err.Error(), "incomplete dependencies") {
		t.Fatalf("error %q does not name the missing dependencies", err.Error())
	}
}

// TestProductionVerifyDeps_UsesTheShippedTrustPolicy — the release gate
// must verify under the same policy the runtime uses. A future edit that
// swapped in a hand-built, wider policy would defeat the whole point, so
// the wiring is pinned here.
func TestProductionVerifyDeps_UsesTheShippedTrustPolicy(t *testing.T) {
	deps := productionVerifyDeps(newReasonSink())
	got, err := deps.LoadPolicy()
	if err != nil {
		t.Fatalf("LoadPolicy: %v", err)
	}
	want, err := signing.LoadTrustPolicyFromEnv()
	if err != nil {
		t.Fatalf("LoadTrustPolicyFromEnv: %v", err)
	}
	if strings.Join(got.Identities, "|") != strings.Join(want.Identities, "|") {
		t.Errorf("identities differ from the shipped policy: got %v want %v", got.Identities, want.Identities)
	}
	if got.WorkflowRef != want.WorkflowRef {
		t.Errorf("workflow_ref differs from the shipped policy: got %q want %q", got.WorkflowRef, want.WorkflowRef)
	}
}

// TestReasonSink_CarriesTheVerifierReason — the failure line's whole value
// is that it names WHY. The verifier only ever puts that in a log
// attribute, so the sink is load-bearing: without it every failure would
// read "no reason recorded".
func TestReasonSink_CarriesTheVerifierReason(t *testing.T) {
	sink := newReasonSink()
	log := slog.New(sink)

	const reason = `cert-claim assertion failed: workflow_ref "refs/heads/main" does not match policy "^refs/tags/v.*$"`
	log.Warn("catalog signature verification failed", "source", "redacted", "reason", reason)
	if got := sink.take(); got != reason {
		t.Fatalf("sink recorded %q, want the verifier's reason attribute %q", got, reason)
	}
	// take() must clear, or one entry's reason gets reported against the
	// next entry.
	if second := sink.take(); second != "" {
		t.Fatalf("sink did not clear after take: %q", second)
	}
	// A success line is INFO and must never be picked up as a reason.
	log.Info("catalog signature verified", "identity", testIdentity)
	if got := sink.take(); got != "" {
		t.Fatalf("sink recorded an INFO line as a failure reason: %q", got)
	}
}

// --- S8: the release-commit binding at the release gate ---------------------

// TestVerify_RequiresTheReleaseCommit — the gate refuses to run at all
// without a commit to bind to, and refuses a value that is not a full commit
// hash.
//
// Fail-closed here matters as much as anywhere else in this file. A gate that
// accepted "no commit supplied" would check LESS than the runtime does, and
// would wave through a catalogue `sharko serve` then refuses — which is the
// exact shape of failure this whole step was added to stop.
func TestVerify_RequiresTheReleaseCommit(t *testing.T) {
	cases := []struct {
		name    string
		commit  string
		wantSub string
	}{
		{"missing", "", "--release-commit is required"},
		{"whitespace_only", "   ", "--release-commit is required"},
		{"short_hash", "faf109fb", "is not a full 40-character commit hash"},
		{"not_hex", "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", "is not a full 40-character commit hash"},
		{"version_string", "4.0.2", "is not a full 40-character commit hash"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := buildSignedDir(t)
			opts := verifyOpts(dir)
			opts.ReleaseCommit = tc.commit
			fv := &fakeVerifier{verified: true, issuer: testIdentity}
			err := runVerify(context.Background(), opts, io.Discard, depsFor(fv), nil)
			if err == nil {
				t.Fatalf("commit %q was accepted", tc.commit)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error %q does not say %q", err.Error(), tc.wantSub)
			}
			// And it must refuse BEFORE verifying anything, so a bad
			// invocation cannot be mistaken for a verified catalogue.
			if fv.calls != 0 {
				t.Errorf("the gate verified %d entries before refusing the commit", fv.calls)
			}
		})
	}
}

// TestVerify_UsesTheEmbeddedPolicy — the gate must verify under the policy
// the RUNTIME applies to an embedded catalogue, not the looser third-party
// one. That means the release-commit binding is on, the commit is the one
// passed in, and the workflow_ref claim policy is the one Sharko's own
// certificates can actually satisfy.
//
// Without this the gate and the runtime could disagree, and a disagreement
// only ever shows up after publication.
func TestVerify_UsesTheEmbeddedPolicy(t *testing.T) {
	dir := buildSignedDir(t)
	fv := &fakeVerifier{verified: true, issuer: testIdentity}
	if err := runVerify(context.Background(), verifyOpts(dir), io.Discard, depsFor(fv), nil); err != nil {
		t.Fatalf("runVerify: %v", err)
	}
	if len(fv.policies) == 0 {
		t.Fatal("the gate verified nothing, so no policy was observed")
	}
	want := signing.EmbeddedCatalogTrustPolicy(mustShippedPolicy(t), testReleaseCommit)
	for i, got := range fv.policies {
		if !got.RequireReleaseCommit {
			t.Fatalf("call %d: the gate verified under a policy with the release-commit "+
				"binding OFF, so it checks less than the runtime does", i)
		}
		if got.ReleaseCommit != testReleaseCommit {
			t.Errorf("call %d: ReleaseCommit = %q, want %q", i, got.ReleaseCommit, testReleaseCommit)
		}
		if got.WorkflowRef != want.WorkflowRef {
			t.Errorf("call %d: WorkflowRef = %q, want the embedded policy's %q",
				i, got.WorkflowRef, want.WorkflowRef)
		}
		if strings.Join(got.Identities, "|") != strings.Join(want.Identities, "|") {
			t.Errorf("call %d: identities differ from the shipped policy: got %v want %v",
				i, got.Identities, want.Identities)
		}
	}
}

// TestVerify_ReportsTheRequiredCommit — the report names the commit it bound
// to. A release log that does not say which commit was required cannot be
// audited after the fact.
func TestVerify_ReportsTheRequiredCommit(t *testing.T) {
	dir := buildSignedDir(t)
	var out bytes.Buffer
	fv := &fakeVerifier{verified: true, issuer: testIdentity}
	if err := runVerify(context.Background(), verifyOpts(dir), &out, depsFor(fv), nil); err != nil {
		t.Fatalf("runVerify: %v", err)
	}
	if !strings.Contains(out.String(), "required release commit = "+testReleaseCommit) {
		t.Errorf("the report does not name the required release commit:\n%s", out.String())
	}
}

func mustShippedPolicy(t *testing.T) sources.TrustPolicy {
	t.Helper()
	p, err := signing.LoadTrustPolicyFromEnv()
	if err != nil {
		t.Fatalf("LoadTrustPolicyFromEnv: %v", err)
	}
	return p
}

// --- S9: the entry name cannot steer a read out of --out ---------------------

// escapedBundleBody is the content planted OUTSIDE the output directory. It
// is deliberately a distinct string so a test can prove the gate never
// handed these bytes to the verifier, rather than only proving that
// something went wrong.
const escapedBundleBody = "bundle-bytes-from-outside-the-output-directory"

// rewriteEntryName rewrites one entry in addons.yaml.signed so its name and
// its signature.bundle URL both carry `newName`, and returns the file name
// the gate would build from it.
//
// It goes through the same unmarshal/marshal round trip the signing tool
// uses rather than doing string surgery, so the rewrite cannot silently
// apply to nothing — which is how a planted break ends up "passing" against
// an unmodified file.
func rewriteEntryName(t *testing.T, dir, oldName, newName string) {
	t.Helper()
	p := filepath.Join(dir, signedCatalogFile)
	data, err := os.ReadFile(p) //nolint:gosec // test temp dir
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Addons []catalog.CatalogEntry `yaml:"addons"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	found := 0
	for i := range raw.Addons {
		if raw.Addons[i].Name != oldName {
			continue
		}
		found++
		raw.Addons[i].Name = newName
		raw.Addons[i].Signature = &catalog.Signature{
			Bundle: fakeReleaseBase + "/" + newName + ".bundle",
		}
	}
	if found != 1 {
		t.Fatalf("expected exactly one entry named %q to rewrite, found %d", oldName, found)
	}
	out, err := yaml.Marshal(&raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, out, 0o600); err != nil {
		t.Fatal(err)
	}
	// Prove the rewrite actually landed. Without this the whole test could
	// pass against a file that was never changed.
	back, err := os.ReadFile(p) //nolint:gosec // test temp dir
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(back), "name: "+newName) {
		t.Fatalf("the rewrite did not land: %s does not carry name %q", p, newName)
	}
}

// TestVerify_RefusesAnEntryNameThatLeavesTheOutputDirectory — an entry name
// carrying a path element must not be able to point the bundle read at a
// file outside --out.
//
// Why this is a real hole and not scanner noise. At verify time the entry
// name comes out of addons.yaml.signed, and that file is exactly the thing
// this command exists to prove; it has not been verified yet when the name
// is read. Nothing upstream constrains the name to a file name either — the
// catalogue loader only requires it to be non-empty (validateEntry in
// internal/catalog/loader.go) and the committed JSON Schema puts no pattern
// on the list-shaped entries.
//
// The test plants a readable, non-empty file one directory ABOVE --out and
// points a rewritten entry at it. That matters: with the protection removed,
// the read succeeds, the stand-in verifier accepts it and the gate returns
// no error at all — so this test cannot pass for the boring reason that
// something was missing. And it asserts the SPECIFIC refusal plus the fact
// that the outside bytes never reached the verifier, because "an error
// happened" would also be true of a bundle that simply was not there.
func TestVerify_RefusesAnEntryNameThatLeavesTheOutputDirectory(t *testing.T) {
	dir := buildSignedDir(t)

	// One level up from --out, inside the test's own temp tree.
	outside := filepath.Join(filepath.Dir(dir), "escaped.bundle")
	if err := os.WriteFile(outside, []byte(escapedBundleBody), 0o600); err != nil {
		t.Fatal(err)
	}
	const escapingName = "../escaped"
	rewriteEntryName(t, dir, "argocd", escapingName)

	// Sanity: the planted file really is reachable by the path the old code
	// would have built, so a refusal below is the protection working and not
	// a missing file.
	if _, err := os.ReadFile(filepath.Join(dir, escapingName+".bundle")); err != nil { //nolint:gosec // test temp dir
		t.Fatalf("the planted file is not reachable, so this test would prove nothing: %v", err)
	}

	fv := &fakeVerifier{verified: true, issuer: testIdentity}
	var out bytes.Buffer
	err := runVerify(context.Background(), verifyOpts(dir), &out, depsFor(fv), nil)
	if err == nil {
		t.Fatalf("the gate accepted an entry name that reads outside --out. output:\n%s", out.String())
	}

	combined := out.String() + "\n" + err.Error()
	// The specific refusal, not just any error.
	if !strings.Contains(combined, "is not a plain file name") {
		t.Fatalf("the refusal does not say the entry name is malformed, so this test "+
			"would also pass for an unrelated failure. got:\n%s", combined)
	}
	if !strings.Contains(combined, escapingName) {
		t.Fatalf("the refusal does not name the offending entry %q. got:\n%s", escapingName, combined)
	}

	// And the bytes from outside the directory were never verified.
	for i, b := range fv.bundles {
		if string(b) == escapedBundleBody {
			t.Fatalf("call %d: the gate read and verified a file from outside --out", i)
		}
	}
}

// TestBundleFileName covers the shapes the plain-file-name check has to
// refuse, and the ordinary names it must keep accepting. Table-driven so a
// future addon name with a dot or an underscore in it is visibly still fine
// — the check is about path structure, not about a character allowlist.
func TestBundleFileName(t *testing.T) {
	ok := []string{"argocd", "cert-manager", "kube-prometheus-stack", "a", "a.b_c-1"}
	for _, name := range ok {
		got, err := bundleFileName(name)
		if err != nil {
			t.Errorf("bundleFileName(%q) refused an ordinary entry name: %v", name, err)
			continue
		}
		if got != name+".bundle" {
			t.Errorf("bundleFileName(%q) = %q, want %q", name, got, name+".bundle")
		}
	}

	bad := []string{
		"",
		"   ",
		".",
		"..",
		"../escaped",
		"../../etc/passwd",
		"sub/argocd",
		"/etc/passwd",
		`..\escaped`,
		"argocd/",
		"argocd\x00",
	}
	for _, name := range bad {
		if got, err := bundleFileName(name); err == nil {
			t.Errorf("bundleFileName(%q) returned %q with no error", name, got)
		}
	}
}

// TestVerify_ReadsOnlyInsideTheOutputDirectory — a symlink planted INSIDE
// --out must not be a way out either.
//
// The name check above cannot catch this one: the entry name stays a plain
// file name and it is the file system, not the string, that points
// elsewhere. This is the case os.Root closes, and it is why the fix is two
// layers rather than only a validated name.
func TestVerify_ReadsOnlyInsideTheOutputDirectory(t *testing.T) {
	dir := buildSignedDir(t)

	outside := filepath.Join(filepath.Dir(dir), "symlink-target.bundle")
	if err := os.WriteFile(outside, []byte(escapedBundleBody), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "argocd.bundle")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("this platform will not create the symlink this test needs: %v", err)
	}
	// Sanity: an ordinary read really does follow the link out.
	body, err := os.ReadFile(link) //nolint:gosec // test temp dir
	if err != nil || string(body) != escapedBundleBody {
		t.Fatalf("the planted symlink does not resolve, so this test would prove nothing (err=%v)", err)
	}

	fv := &fakeVerifier{verified: true, issuer: testIdentity}
	var out bytes.Buffer
	if err := runVerify(context.Background(), verifyOpts(dir), &out, depsFor(fv), nil); err == nil {
		t.Fatalf("the gate followed a symlink out of --out. output:\n%s", out.String())
	}
	for i, b := range fv.bundles {
		if string(b) == escapedBundleBody {
			t.Fatalf("call %d: the gate read and verified a file from outside --out", i)
		}
	}
}
