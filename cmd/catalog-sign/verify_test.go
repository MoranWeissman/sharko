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
}

func (f *fakeVerifier) VerifyBundleBytes(_ context.Context, payload, bundleBytes []byte, _ sources.TrustPolicy) (bool, string, error) {
	f.calls++
	f.payloads = append(f.payloads, payload)
	f.bundles = append(f.bundles, bundleBytes)
	if f.failFor != "" && strings.Contains(string(payload), f.failFor) {
		return false, "", nil
	}
	return f.verified, f.issuer, f.err
}

const testIdentity = "https://github.com/MoranWeissman/sharko/.github/workflows/release.yml@refs/heads/main"

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
	return options{OutDir: dir, ReleaseBaseURL: fakeReleaseBase, Verify: true}
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
