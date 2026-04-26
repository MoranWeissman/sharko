// Command catalog-sign signs each entry in catalog/addons.yaml using cosign
// keyless (sign-blob) and emits:
//
//   - <out>/<entry-name>.bundle      — Sigstore bundle per entry.
//   - <out>/<entry-name>.sig         — base64 signature (companion).
//   - <out>/<entry-name>.pem         — Fulcio cert (companion).
//   - <out>/addons.yaml.signed       — original YAML with `signature:` added.
//
// Used only by the release workflow (Story V123-2.5). Calls the same
// signing.CanonicalEntryBytes function the runtime verifier uses, so signing
// and verification agree on the message bytes.
//
// CLI shell-out to the cosign binary is fine here — NFR-V123-6's "no cosign
// CLI shell-out" rule is for the runtime verifier inside the Sharko binary
// (V123-2.2), not for the release pipeline. Cosign 2.4.x flag set
// (--bundle, --output-signature, --output-certificate, --yes) confirmed via
// context7 against the pinned `cosign-release: v2.4.1` in release.yml.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"gopkg.in/yaml.v3"

	catalogembed "github.com/MoranWeissman/sharko/catalog"
	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/catalog/signing"
)

// options captures the parsed CLI flags so run() is purely testable.
type options struct {
	OutDir         string
	ReleaseBaseURL string
}

// signOutputs collects the per-entry artifact paths a signer must produce.
// Kept as a struct (not three string args) so future bundle-format changes
// don't churn the interface.
type signOutputs struct {
	BundlePath string
	SigPath    string
	CertPath   string
}

// signer is the cosign abstraction. Production uses cosignCLI (shell-out);
// tests use a fake that writes deterministic bytes so the orchestration
// can be exercised without live Fulcio/Rekor calls.
type signer interface {
	SignBlob(payloadPath string, out signOutputs) error
}

// cosignCLI invokes the system `cosign` binary with sign-blob keyless flags.
// All three outputs (bundle + sig + cert) are emitted so the bundle is the
// authoritative artifact while the .sig/.pem companions remain available for
// any downstream consumer that prefers the split-file format.
type cosignCLI struct{}

func (cosignCLI) SignBlob(payloadPath string, out signOutputs) error {
	cmd := exec.Command("cosign", "sign-blob",
		"--yes",
		"--bundle", out.BundlePath,
		"--output-signature", out.SigPath,
		"--output-certificate", out.CertPath,
		payloadPath,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func main() {
	opts := options{}
	flag.StringVar(&opts.OutDir, "out", "_dist/catalog", "output directory for bundles + signed YAML")
	flag.StringVar(&opts.ReleaseBaseURL, "release-base-url", "",
		"base URL for release assets (e.g., https://github.com/MoranWeissman/sharko/releases/download/v1.2.3)")
	flag.Parse()

	if err := run(opts, cosignCLI{}); err != nil {
		fmt.Fprintf(os.Stderr, "catalog-sign: %v\n", err)
		os.Exit(1)
	}
}

// run is the testable entry point. It reads the embedded catalog bytes,
// validates them via the canonical loader (parity with the runtime), and
// signs every entry through `s`. The mutation pass works on a fresh
// re-unmarshal — never on the loaded *Catalog — so the signing path can't
// inadvertently flip computed-only fields like Source or SecurityTier.
func run(opts options, s signer) error {
	if opts.ReleaseBaseURL == "" {
		return fmt.Errorf("--release-base-url is required")
	}
	if s == nil {
		return fmt.Errorf("nil signer")
	}
	if err := os.MkdirAll(opts.OutDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", opts.OutDir, err)
	}

	yamlBytes := catalogembed.AddonsYAML()

	// Validation parity — the loader rejects schema errors the runtime
	// would also reject. Failing here means a broken catalog never makes
	// it past CI to a release. The loaded *Catalog itself is discarded;
	// we mutate a separate raw struct to avoid touching computed fields.
	if _, err := catalog.LoadBytes(yamlBytes); err != nil {
		return fmt.Errorf("load catalog (validation parity): %w", err)
	}

	var raw struct {
		Addons []catalog.CatalogEntry `yaml:"addons"`
	}
	if err := yaml.Unmarshal(yamlBytes, &raw); err != nil {
		return fmt.Errorf("re-unmarshal catalog: %w", err)
	}
	if len(raw.Addons) == 0 {
		return fmt.Errorf("catalog has no entries under 'addons:'")
	}

	for i := range raw.Addons {
		e := raw.Addons[i]
		// Canonical bytes come from the SAME function the runtime
		// verifier uses. If this drifts, every signature mismatches.
		canonical, err := signing.CanonicalEntryBytes(e)
		if err != nil {
			return fmt.Errorf("canonical bytes for %q: %w", e.Name, err)
		}

		payloadPath := filepath.Join(opts.OutDir, e.Name+".payload")
		out := signOutputs{
			BundlePath: filepath.Join(opts.OutDir, e.Name+".bundle"),
			SigPath:    filepath.Join(opts.OutDir, e.Name+".sig"),
			CertPath:   filepath.Join(opts.OutDir, e.Name+".pem"),
		}
		if err := os.WriteFile(payloadPath, canonical, 0o644); err != nil {
			return fmt.Errorf("write payload for %q: %w", e.Name, err)
		}
		if err := s.SignBlob(payloadPath, out); err != nil {
			return fmt.Errorf("sign %q: %w", e.Name, err)
		}

		// Stamp the deterministic release-asset URL. The release isn't
		// published yet at sign time, but the URL pattern is fixed:
		// `<base>/<name>.bundle`. By the time any verifier fetches it,
		// the goreleaser job has uploaded the bundle as a release asset.
		raw.Addons[i].Signature = &catalog.Signature{
			Bundle: fmt.Sprintf("%s/%s.bundle", opts.ReleaseBaseURL, e.Name),
		}
	}

	signedYAML, err := yaml.Marshal(&raw)
	if err != nil {
		return fmt.Errorf("marshal signed yaml: %w", err)
	}
	signedPath := filepath.Join(opts.OutDir, "addons.yaml.signed")
	if err := os.WriteFile(signedPath, signedYAML, 0o644); err != nil {
		return fmt.Errorf("write signed yaml: %w", err)
	}

	fmt.Printf("catalog-sign: signed %d entries → %s\n", len(raw.Addons), opts.OutDir)
	return nil
}
