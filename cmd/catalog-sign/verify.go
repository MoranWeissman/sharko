// verify.go is the release pipeline's proof that the bundles it just
// produced actually verify.
//
// Why this exists. The release workflow used to decide that the signed
// catalogue was good if `addons.yaml.signed` existed and was non-empty,
// and then copied it over `catalog/addons.yaml` so `//go:embed` baked it
// into the release binaries and the container image. A file that exists
// and is non-empty but carries a bad, truncated, mismatched or substituted
// signature passed that check. The owner's words: "checking that a file
// exists or is nonempty is insufficient."
//
// What this does instead. It runs the SAME verifier the Sharko binary runs
// at startup (internal/catalog/signing.Verifier), against the SAME
// canonical entry bytes the signer signed, under the SAME trust policy
// production uses (signing.LoadTrustPolicyFromEnv), with the SAME Sigstore
// trust root production fetches (signing.LoadProductionTrustedRoot). No
// new signature logic is written here and no cosign shell-out is added —
// this is the runtime check, moved earlier.
//
// The consequence is deliberate: if the release pipeline produces a
// catalogue that `sharko serve` would refuse to mark verified, the release
// stops before the catalogue is embedded, instead of shipping a binary
// whose every entry silently shows up unverified.
//
// Fail-closed everywhere. A missing bundle, an unreadable bundle, an entry
// with no signature URL, a URL that does not match the release-asset
// convention, an unreachable trust root, or a signature that does not
// verify — each of those is a failure, and the command exits non-zero
// naming every entry that failed and why.
package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/catalog/signing"
	"github.com/MoranWeissman/sharko/internal/catalog/sources"
)

// signedCatalogFile is the name catalog-sign writes and the release
// workflow embeds. Named once so the writer and the checker cannot drift.
const signedCatalogFile = "addons.yaml.signed"

// bundleVerifier is the verification surface runVerify needs. The
// production implementation is *signing.Verifier; tests substitute a fake
// so the orchestration (which entries get checked, what counts as a
// failure, what the exit status is) can be exercised without Fulcio,
// Rekor or a network.
type bundleVerifier interface {
	VerifyBundleBytes(ctx context.Context, payload, bundleBytes []byte, policy sources.TrustPolicy) (bool, string, error)
}

// verifyDeps are the two things runVerify cannot construct for itself
// without reaching the network.
type verifyDeps struct {
	// NewVerifier resolves the Sigstore trust root and returns a verifier
	// bound to it. An error here is fatal: a release must not embed a
	// catalogue whose signatures were never actually checked.
	NewVerifier func(ctx context.Context) (bundleVerifier, error)
	// LoadPolicy returns the trust policy to verify under. Production
	// passes signing.LoadTrustPolicyFromEnv so the release-time policy is
	// byte-for-byte the runtime policy.
	LoadPolicy func() (sources.TrustPolicy, error)
}

// reasonSink captures the WARN line the verifier emits when it rejects a
// bundle. The verifier's contract collapses every legitimate rejection to
// (false, "", nil) and puts the distinguishing detail in a log attribute,
// so without this the release log would say "did not verify" and nothing
// more. With it, the failure line names the entry AND the reason.
type reasonSink struct {
	mu     sync.Mutex
	last   string
	stderr slog.Handler
}

func newReasonSink() *reasonSink {
	return &reasonSink{stderr: slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})}
}

func (s *reasonSink) Enabled(_ context.Context, l slog.Level) bool { return l >= slog.LevelWarn }

func (s *reasonSink) Handle(ctx context.Context, rec slog.Record) error {
	var reason string
	rec.Attrs(func(a slog.Attr) bool {
		if a.Key == "reason" {
			reason = a.Value.String()
			return false
		}
		return true
	})
	s.mu.Lock()
	if reason != "" {
		s.last = reason
	} else {
		s.last = rec.Message
	}
	s.mu.Unlock()
	return s.stderr.Handle(ctx, rec)
}

func (s *reasonSink) WithAttrs([]slog.Attr) slog.Handler { return s }
func (s *reasonSink) WithGroup(string) slog.Handler      { return s }

// take returns the most recent reason and clears it, so a reason from one
// entry can never be reported against the next one.
func (s *reasonSink) take() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.last
	s.last = ""
	return r
}

// productionVerifyDeps wires the real trust root and the real trust
// policy. Note what is NOT here: no override of
// SHARKO_CATALOG_TRUSTED_IDENTITIES, no override of
// SHARKO_CATALOG_TRUSTED_WORKFLOW_REF, and no use of
// SHARKO_SKIP_TUF_NETWORK. Widening the policy or skipping the trust root
// would turn this gate back into the thing it replaced.
func productionVerifyDeps(sink *reasonSink) verifyDeps {
	return verifyDeps{
		NewVerifier: func(ctx context.Context) (bundleVerifier, error) {
			trustRoot, err := signing.LoadProductionTrustedRoot(ctx)
			if err != nil {
				return nil, fmt.Errorf("sigstore trust root: %w", err)
			}
			return signing.NewVerifier(nil, /* default http client */
				signing.WithTrustedMaterial(trustRoot),
				signing.WithLogger(slog.New(sink)),
			), nil
		},
		LoadPolicy: signing.LoadTrustPolicyFromEnv,
	}
}

// runVerify checks every entry in <out>/addons.yaml.signed against the
// bundle sitting next to it in <out>, and returns a non-nil error if a
// single entry fails. The caller (main) turns that into a non-zero exit,
// which is what stops the release workflow before the `cp` that embeds the
// catalogue.
func runVerify(ctx context.Context, opts options, w io.Writer, deps verifyDeps, sink *reasonSink) error {
	if deps.NewVerifier == nil || deps.LoadPolicy == nil {
		return fmt.Errorf("verify: incomplete dependencies")
	}

	signedPath := filepath.Join(opts.OutDir, signedCatalogFile)
	yamlBytes, err := os.ReadFile(signedPath) //nolint:gosec // path comes from the release workflow's own --out flag
	if err != nil {
		return fmt.Errorf("read %s: %w", signedPath, err)
	}
	if len(bytes.TrimSpace(yamlBytes)) == 0 {
		return fmt.Errorf("%s is empty", signedPath)
	}

	// Validation parity with the runtime loader, exactly as the signing
	// path does it. A signed catalogue that the runtime loader would reject
	// must never be embedded either.
	if _, err := catalog.LoadBytes(yamlBytes); err != nil {
		return fmt.Errorf("load %s (validation parity): %w", signedCatalogFile, err)
	}
	var raw struct {
		Addons []catalog.CatalogEntry `yaml:"addons"`
	}
	if err := yaml.Unmarshal(yamlBytes, &raw); err != nil {
		return fmt.Errorf("re-unmarshal %s: %w", signedCatalogFile, err)
	}
	if len(raw.Addons) == 0 {
		return fmt.Errorf("%s has no entries under 'addons:'", signedCatalogFile)
	}

	policy, err := deps.LoadPolicy()
	if err != nil {
		return fmt.Errorf("load trust policy: %w", err)
	}
	if len(policy.Identities) == 0 {
		return fmt.Errorf("trust policy has no identities — nothing could ever verify")
	}
	fmt.Fprintf(w, "catalog-sign verify: %d entries in %s\n", len(raw.Addons), signedPath)
	for i, id := range policy.Identities {
		fmt.Fprintf(w, "catalog-sign verify: trusted identity[%d] = %s\n", i, id)
	}
	fmt.Fprintf(w, "catalog-sign verify: workflow_ref policy = %s\n", policy.WorkflowRef)

	verifier, err := deps.NewVerifier(ctx)
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}

	var failures []string
	for i := range raw.Addons {
		e := raw.Addons[i]
		name := e.Name

		if e.Signature == nil || strings.TrimSpace(e.Signature.Bundle) == "" {
			failures = append(failures, fmt.Sprintf(
				"%s: entry carries no signature.bundle URL in the signed catalogue", name))
			continue
		}
		// The release-asset convention is `<base>/<name>.bundle`. A URL
		// that breaks it means the embedded binary would fetch something
		// that is not there, so verification at runtime would fail on a
		// catalogue that passed here.
		wantURL := name + ".bundle"
		if opts.ReleaseBaseURL != "" {
			wantURL = strings.TrimSuffix(opts.ReleaseBaseURL, "/") + "/" + name + ".bundle"
			if e.Signature.Bundle != wantURL {
				failures = append(failures, fmt.Sprintf(
					"%s: signature.bundle is %q, want %q", name, e.Signature.Bundle, wantURL))
				continue
			}
		} else if !strings.HasSuffix(e.Signature.Bundle, "/"+wantURL) {
			failures = append(failures, fmt.Sprintf(
				"%s: signature.bundle %q does not end in %q", name, e.Signature.Bundle, "/"+wantURL))
			continue
		}

		bundlePath := filepath.Join(opts.OutDir, name+".bundle")
		bundleBytes, err := os.ReadFile(bundlePath) //nolint:gosec // path is <out>/<entry name>.bundle
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: read bundle: %v", name, err))
			continue
		}
		if len(bytes.TrimSpace(bundleBytes)) == 0 {
			failures = append(failures, fmt.Sprintf("%s: bundle %s is empty", name, bundlePath))
			continue
		}

		// The message the signature attests to, from the same function the
		// signer and the runtime verifier both call.
		payload, err := signing.CanonicalEntryBytes(e)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: canonical bytes: %v", name, err))
			continue
		}

		if sink != nil {
			_ = sink.take() // never attribute a previous entry's reason to this one
		}
		ok, issuer, verr := verifier.VerifyBundleBytes(ctx, payload, bundleBytes, policy)
		switch {
		case verr != nil:
			failures = append(failures, fmt.Sprintf("%s: verification errored: %v", name, verr))
		case !ok:
			reason := "no reason recorded"
			if sink != nil {
				if r := sink.take(); r != "" {
					reason = r
				}
			}
			failures = append(failures, fmt.Sprintf("%s: signature did not verify: %s", name, reason))
		default:
			fmt.Fprintf(w, "catalog-sign verify: OK   %-32s identity=%s\n", name, issuer)
		}
	}

	if len(failures) > 0 {
		for _, f := range failures {
			fmt.Fprintf(w, "catalog-sign verify: FAIL %s\n", f)
		}
		return fmt.Errorf(
			"%d of %d signed catalogue entries failed verification — refusing to embed",
			len(failures), len(raw.Addons))
	}
	fmt.Fprintf(w, "catalog-sign verify: all %d entries verified\n", len(raw.Addons))
	return nil
}
