//go:build publishedbundles

// fixture_publishedbundles_test.go builds an on-disk copy of exactly what
// the v4.0.1 release pipeline handed to its embed step: the
// addons.yaml.signed that `catalog-sign` emits, sitting next to the real
// published `*.bundle` assets.
//
// It exists so the new verify gate can be break-tested by hand against
// real signatures, a real Fulcio cert chain and real Rekor inclusion —
// rather than against a fixture whose signatures were never real. The
// signing step is stubbed (fakeSigner) only to produce the byte-exact
// addons.yaml.signed; every bundle it writes is then overwritten by the
// genuine published asset.
//
// Behind a build tag because it needs a directory of downloaded release
// assets. It asserts nothing about verification — the by-hand runs drive
// the real CLI against what this writes.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildFixture_FromPublishedBundles(t *testing.T) {
	bundleDir := strings.TrimSpace(os.Getenv("SHARKO_AUDIT_BUNDLE_DIR"))
	outDir := strings.TrimSpace(os.Getenv("SHARKO_AUDIT_FIXTURE_OUT"))
	baseURL := strings.TrimSpace(os.Getenv("SHARKO_AUDIT_RELEASE_BASE"))
	if bundleDir == "" || outDir == "" || baseURL == "" {
		t.Fatal("SHARKO_AUDIT_BUNDLE_DIR, SHARKO_AUDIT_FIXTURE_OUT and SHARKO_AUDIT_RELEASE_BASE must all be set")
	}
	if err := os.MkdirAll(outDir, 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", outDir, err)
	}

	// Produce the byte-exact addons.yaml.signed the release emitted.
	if err := run(options{OutDir: outDir, ReleaseBaseURL: baseURL},
		fakeSigner{bundleBytes: []byte("placeholder-overwritten-below")}); err != nil {
		t.Fatalf("run (signing orchestration with a stub signer): %v", err)
	}

	// Replace every stub bundle with the genuine published asset.
	entries := readSignedYAML(t, outDir)
	replaced := 0
	for _, e := range entries {
		src := filepath.Join(bundleDir, e.Name+".bundle")
		real, err := os.ReadFile(src) // bundleDir is the path an operator sets to run this harness by hand
		if err != nil {
			t.Fatalf("read published bundle %s: %v", src, err)
		}
		if err := os.WriteFile(filepath.Join(outDir, e.Name+".bundle"), real, 0o600); err != nil {
			t.Fatalf("write fixture bundle for %s: %v", e.Name, err)
		}
		replaced++
	}
	// The stub also wrote .sig/.pem/.payload companions. The release did
	// too, so leaving them is faithful; they are not inputs to the gate.
	t.Logf("fixture built at %s: %d entries, %d real bundles installed", outDir, len(entries), replaced)
}
