// Tests for the catalog-sign tool. The real cosign CLI is replaced by a
// fakeSigner so the orchestration (canonical-bytes → URL stamping →
// addons.yaml.signed emission) can be exercised hermetically.
//
// Live Fulcio/Rekor calls are intentionally NOT covered here — they would
// be flaky in CI and the V123-2.5 brief explicitly defers integration to
// the first real tag push after merge.
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	catalogembed "github.com/MoranWeissman/sharko/catalog"
	"github.com/MoranWeissman/sharko/internal/catalog"
)

// fakeSigner writes a constant byte payload to the bundle path so tests can
// assert that the signed file layout is correct without needing a real
// signing keypair or transparency log.
type fakeSigner struct {
	bundleBytes []byte
}

func (f fakeSigner) SignBlob(payloadPath string, out signOutputs) error {
	if err := os.WriteFile(out.BundlePath, f.bundleBytes, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(out.SigPath, []byte("fake-sig"), 0o644); err != nil {
		return err
	}
	return os.WriteFile(out.CertPath, []byte("fake-cert"), 0o644)
}

const fakeReleaseBase = "https://github.com/MoranWeissman/sharko/releases/download/v9.9.9-test"

// readSignedYAML loads addons.yaml.signed back into the same shape the
// signing tool emits, so assertions can poke at individual entries.
func readSignedYAML(t *testing.T, dir string) []catalog.CatalogEntry {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "addons.yaml.signed"))
	if err != nil {
		t.Fatalf("read addons.yaml.signed: %v", err)
	}
	var raw struct {
		Addons []catalog.CatalogEntry `yaml:"addons"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal addons.yaml.signed: %v", err)
	}
	return raw.Addons
}

// TestCatalogSign_StampsURLOnEveryEntry — every entry's signature.bundle
// must equal `<release-base>/<name>.bundle`. This is the verifier's
// fetch-time contract; if the URL isn't deterministic, runtime
// verification has nothing to download.
func TestCatalogSign_StampsURLOnEveryEntry(t *testing.T) {
	tmp := t.TempDir()
	opts := options{OutDir: tmp, ReleaseBaseURL: fakeReleaseBase}
	if err := run(opts, fakeSigner{bundleBytes: []byte("fake-bundle")}); err != nil {
		t.Fatalf("run: %v", err)
	}

	entries := readSignedYAML(t, tmp)
	if len(entries) == 0 {
		t.Fatal("signed catalog has zero entries")
	}
	for _, e := range entries {
		if e.Signature == nil {
			t.Errorf("entry %q has nil Signature", e.Name)
			continue
		}
		want := fakeReleaseBase + "/" + e.Name + ".bundle"
		if e.Signature.Bundle != want {
			t.Errorf("entry %q: signature.bundle = %q, want %q",
				e.Name, e.Signature.Bundle, want)
		}
		// And the bundle file itself must have been emitted to disk —
		// goreleaser's `release.extra_files` glob depends on it.
		bundlePath := filepath.Join(tmp, e.Name+".bundle")
		if _, err := os.Stat(bundlePath); err != nil {
			t.Errorf("bundle file missing for %q: %v", e.Name, err)
		}
	}
}

// TestCatalogSign_PreservesEntryFields — every original field on the
// embedded entry must round-trip into addons.yaml.signed unchanged. Only
// the Signature field is allowed to be added. A subtle mutation (lost
// curated_by tag, dropped maintainer) would silently weaken downstream
// trust signals.
func TestCatalogSign_PreservesEntryFields(t *testing.T) {
	// Re-parse the embedded YAML the same way the tool does so we have
	// a baseline to compare against (rather than calling Load(), which
	// also sets Source = "embedded" and SecurityTier — runtime fields
	// not present in the on-disk YAML).
	var original struct {
		Addons []catalog.CatalogEntry `yaml:"addons"`
	}
	if err := yaml.Unmarshal(catalogembed.AddonsYAML(), &original); err != nil {
		t.Fatalf("unmarshal embedded yaml: %v", err)
	}
	byName := make(map[string]catalog.CatalogEntry, len(original.Addons))
	for _, e := range original.Addons {
		byName[e.Name] = e
	}

	tmp := t.TempDir()
	opts := options{OutDir: tmp, ReleaseBaseURL: fakeReleaseBase}
	if err := run(opts, fakeSigner{bundleBytes: []byte("fake-bundle")}); err != nil {
		t.Fatalf("run: %v", err)
	}
	signed := readSignedYAML(t, tmp)
	if len(signed) != len(original.Addons) {
		t.Fatalf("entry count drift: original=%d signed=%d",
			len(original.Addons), len(signed))
	}

	for _, got := range signed {
		want, ok := byName[got.Name]
		if !ok {
			t.Errorf("signed catalog has unexpected entry %q", got.Name)
			continue
		}
		if got.Signature == nil {
			t.Errorf("entry %q lost its Signature stamp", got.Name)
		}
		// Zero out Signature on both sides so the rest of the struct
		// can be compared field-by-field without distractions. Each
		// assertion below is explicit so a future field addition can't
		// silently slip through `reflect.DeepEqual`.
		got.Signature = nil
		if got.Name != want.Name {
			t.Errorf("%s: Name drift", want.Name)
		}
		if got.Description != want.Description {
			t.Errorf("%s: Description drift", want.Name)
		}
		if got.Chart != want.Chart {
			t.Errorf("%s: Chart drift", want.Name)
		}
		if got.Repo != want.Repo {
			t.Errorf("%s: Repo drift", want.Name)
		}
		if got.DefaultNamespace != want.DefaultNamespace {
			t.Errorf("%s: DefaultNamespace drift", want.Name)
		}
		if got.DefaultSyncWave != want.DefaultSyncWave {
			t.Errorf("%s: DefaultSyncWave drift", want.Name)
		}
		if got.DocsURL != want.DocsURL {
			t.Errorf("%s: DocsURL drift", want.Name)
		}
		if got.Homepage != want.Homepage {
			t.Errorf("%s: Homepage drift", want.Name)
		}
		if got.SourceURL != want.SourceURL {
			t.Errorf("%s: SourceURL drift", want.Name)
		}
		if strings.Join(got.Maintainers, ",") != strings.Join(want.Maintainers, ",") {
			t.Errorf("%s: Maintainers drift: got=%v want=%v",
				want.Name, got.Maintainers, want.Maintainers)
		}
		if got.License != want.License {
			t.Errorf("%s: License drift", want.Name)
		}
		if got.Category != want.Category {
			t.Errorf("%s: Category drift", want.Name)
		}
		if strings.Join(got.CuratedBy, ",") != strings.Join(want.CuratedBy, ",") {
			t.Errorf("%s: CuratedBy drift: got=%v want=%v",
				want.Name, got.CuratedBy, want.CuratedBy)
		}
		if got.SecurityScore.Known != want.SecurityScore.Known ||
			got.SecurityScore.Value != want.SecurityScore.Value {
			t.Errorf("%s: SecurityScore drift", want.Name)
		}
		if got.SecurityScoreUpdated != want.SecurityScoreUpdated {
			t.Errorf("%s: SecurityScoreUpdated drift", want.Name)
		}
		if got.GitHubStars != want.GitHubStars {
			t.Errorf("%s: GitHubStars drift", want.Name)
		}
		if got.MinKubernetesVersion != want.MinKubernetesVersion {
			t.Errorf("%s: MinKubernetesVersion drift", want.Name)
		}
		if got.Deprecated != want.Deprecated {
			t.Errorf("%s: Deprecated drift", want.Name)
		}
		if got.SupersededBy != want.SupersededBy {
			t.Errorf("%s: SupersededBy drift", want.Name)
		}
	}
}

// TestCatalogSign_DeterministicOrdering — two consecutive runs against
// the same inputs must produce byte-identical addons.yaml.signed. This is
// the operator-side guarantee that re-running the release pipeline
// against the same commit produces the same signed bytes (modulo cosign's
// own non-determinism, which is stubbed out by fakeSigner). Without it,
// reproducible-build tooling and any future "catalog drift" diff would
// break.
func TestCatalogSign_DeterministicOrdering(t *testing.T) {
	tmp1 := t.TempDir()
	tmp2 := t.TempDir()
	opts1 := options{OutDir: tmp1, ReleaseBaseURL: fakeReleaseBase}
	opts2 := options{OutDir: tmp2, ReleaseBaseURL: fakeReleaseBase}
	fake := fakeSigner{bundleBytes: []byte("fake-bundle-deterministic")}

	if err := run(opts1, fake); err != nil {
		t.Fatalf("run #1: %v", err)
	}
	if err := run(opts2, fake); err != nil {
		t.Fatalf("run #2: %v", err)
	}

	first, err := os.ReadFile(filepath.Join(tmp1, "addons.yaml.signed"))
	if err != nil {
		t.Fatalf("read run #1: %v", err)
	}
	second, err := os.ReadFile(filepath.Join(tmp2, "addons.yaml.signed"))
	if err != nil {
		t.Fatalf("read run #2: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("addons.yaml.signed differs between runs (len %d vs %d)",
			len(first), len(second))
	}
}

// TestCatalogSign_RejectsMissingReleaseBaseURL — guard the CLI contract
// so a release workflow with a misconfigured env can't accidentally emit
// a signed catalog stamped with empty URLs.
func TestCatalogSign_RejectsMissingReleaseBaseURL(t *testing.T) {
	tmp := t.TempDir()
	err := run(options{OutDir: tmp}, fakeSigner{bundleBytes: []byte("x")})
	if err == nil {
		t.Fatal("expected error for missing --release-base-url, got nil")
	}
	if !strings.Contains(err.Error(), "release-base-url") {
		t.Errorf("error %q does not mention release-base-url", err.Error())
	}
}

// TestSignBlobArgs_IncludesNewBundleFormat pins the rc.1 regression. The
// runtime verifier (sigstore-go bundle parser) only accepts the modern
// Sigstore Bundle format (mediaType / verificationMaterial /
// messageSignature). cosign 2.4.x emits that shape only when
// --new-bundle-format is on the argv; without the flag it falls back to
// the legacy `{base64Signature, cert}` shape and every signed catalog
// entry surfaces Verified=false. If anyone removes the flag this test
// must fail loudly so we never re-ship a broken bundle format.
func TestSignBlobArgs_IncludesNewBundleFormat(t *testing.T) {
	out := signOutputs{
		BundlePath: "/tmp/example.bundle",
		SigPath:    "/tmp/example.sig",
		CertPath:   "/tmp/example.pem",
	}
	args := cosignCLI{}.signBlobArgs("/tmp/example.payload", out)

	if !slices.Contains(args, "--new-bundle-format") {
		t.Fatalf("signBlobArgs missing --new-bundle-format flag; got %v", args)
	}
	// Sanity: the first arg is still the cosign subcommand and the
	// payload path is still the trailing positional. A future refactor
	// that reorders the args must update this test deliberately rather
	// than slipping a regression through.
	if len(args) == 0 || args[0] != "sign-blob" {
		t.Fatalf("expected first arg to be \"sign-blob\", got %v", args)
	}
	if args[len(args)-1] != "/tmp/example.payload" {
		t.Fatalf("expected last arg to be the payload path, got %v", args)
	}
}
