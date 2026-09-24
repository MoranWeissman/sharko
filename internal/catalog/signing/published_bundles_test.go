//go:build publishedbundles

// published_bundles_test.go is an audit harness, not part of any gate.
//
// It drives the production Verifier over a directory of already-published
// `*.bundle` release assets and the catalogue those bundles attest, under
// the production trust policy, and prints one line per bundle. It is
// behind the `publishedbundles` build tag because it needs three things a
// normal test run must never require: a directory of downloaded release
// assets, a catalogue file, and network reach to the Sigstore TUF mirror.
//
// It never sets SHARKO_SKIP_TUF_NETWORK. Skipping the trust root would
// turn "not checked" into a clean-looking run, which is the opposite of
// what this harness is for.
//
// READ THIS BEFORE RUNNING IT (S8). The policy it measures under —
// LoadTrustPolicyFromEnv — is the THIRD-PARTY policy, and that policy still
// asks for a `^refs/tags/v.*$` workflow_ref, which no certificate Sharko's own
// `workflow_run`-triggered release workflow produces can satisfy. So this
// harness still reports 45 FAIL, and that is the correct answer to the
// question it asks: "what does the third-party policy make of Sharko's own
// bundles?" It is the record of the S2 finding and is deliberately left alone.
//
// For the question that matters at runtime — "does Sharko's own build accept
// its own catalogue?" — use TestAudit_PublishedBundlesBoundToTheReleaseCommit
// in published_bundles_commit_test.go. That one applies the EMBEDDED policy,
// the one `sharko serve` actually uses on the catalogue baked into it, and
// under the correct release commit all 45 pass.
//
// Run it like this (paths are examples; both env vars are required):
//
//	SHARKO_AUDIT_CATALOG=/tmp/addons.yaml \
//	SHARKO_AUDIT_BUNDLE_DIR=/tmp/bundles \
//	go test -tags=publishedbundles -count=1 -v -timeout=20m \
//	  -run TestAudit_PublishedBundles ./internal/catalog/signing/
package signing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/catalog/sources"
)

// reasonRecorder captures the WARN lines the verifier emits. The verifier
// collapses every legitimate rejection to (false, "", nil) and puts the
// distinguishing detail in a log attribute, so this is the only way to
// report WHICH check inside the verifier rejected a bundle.
type reasonRecorder struct {
	mu      sync.Mutex
	reasons []string
}

func (r *reasonRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (r *reasonRecorder) Handle(_ context.Context, rec slog.Record) error {
	if rec.Level < slog.LevelWarn {
		return nil
	}
	var reason string
	rec.Attrs(func(a slog.Attr) bool {
		if a.Key == "reason" {
			reason = a.Value.String()
			return false
		}
		return true
	})
	if reason == "" {
		reason = rec.Message
	}
	r.mu.Lock()
	r.reasons = append(r.reasons, reason)
	r.mu.Unlock()
	return nil
}

func (r *reasonRecorder) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *reasonRecorder) WithGroup(string) slog.Handler      { return r }

func (r *reasonRecorder) takeAll() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.reasons
	r.reasons = nil
	return out
}

// auditResult is one row of the report.
type auditResult struct {
	Entry           string
	BundleFile      string
	BundleBytes     int
	PayloadSHA256   string
	Verified        bool
	Issuer          string
	InfraErr        string
	Reasons         []string
	DiagVerified    bool
	DiagIssuer      string
	DiagReasons     []string
	DiagInfraErr    string
	BundleReadErr   string
	CanonicalizeErr string
}

func TestAudit_PublishedBundles(t *testing.T) {
	catalogPath := strings.TrimSpace(os.Getenv("SHARKO_AUDIT_CATALOG"))
	bundleDir := strings.TrimSpace(os.Getenv("SHARKO_AUDIT_BUNDLE_DIR"))
	if catalogPath == "" || bundleDir == "" {
		t.Fatal("SHARKO_AUDIT_CATALOG and SHARKO_AUDIT_BUNDLE_DIR must both be set")
	}

	// Refuse to run under a widened policy. The whole point of this harness
	// is that the policy is the shipped production default; an operator
	// override in the environment would silently change what is being
	// measured.
	for _, envVar := range []string{EnvTrustedIdentities, EnvTrustedWorkflowRef, "SHARKO_SKIP_TUF_NETWORK"} {
		if v := strings.TrimSpace(os.Getenv(envVar)); v != "" {
			t.Fatalf("%s is set to %q — this harness must run under the shipped defaults only", envVar, v)
		}
	}

	policy, err := LoadTrustPolicyFromEnv()
	if err != nil {
		t.Fatalf("LoadTrustPolicyFromEnv: %v", err)
	}
	if policy.WorkflowRef != DefaultTrustedWorkflowRef {
		t.Fatalf("policy.WorkflowRef = %q, want the production default %q",
			policy.WorkflowRef, DefaultTrustedWorkflowRef)
	}
	t.Logf("TRUST POLICY (production defaults, from signing.LoadTrustPolicyFromEnv)")
	for i, id := range policy.Identities {
		t.Logf("  identity[%d] = %s", i, id)
	}
	t.Logf("  workflow_ref = %s", policy.WorkflowRef)

	// Real Sigstore trust root over the network — the same call
	// cmd/sharko/serve.go makes at startup.
	trustRoot, err := LoadProductionTrustedRoot(context.Background())
	if err != nil {
		t.Fatalf("LoadProductionTrustedRoot (no skip escape hatch used): %v", err)
	}
	t.Logf("sigstore trust root loaded over the network")

	rec := &reasonRecorder{}
	v := NewVerifier(nil,
		WithTrustedMaterial(trustRoot),
		WithLogger(slog.New(rec)),
	)

	// The DIAGNOSTIC policy exists only to isolate which check rejects a
	// bundle. WorkflowRef == "" makes assertWorkflowRef a no-op (documented
	// back-compat path), so a bundle that passes here but fails above was
	// rejected by the cert-claim assertion and by nothing else. This is
	// never a pass and is never what the production runtime uses.
	diagPolicy := sources.TrustPolicy{Identities: policy.Identities, WorkflowRef: ""}

	yamlBytes, err := os.ReadFile(catalogPath) //nolint:gosec // operator-supplied audit input
	if err != nil {
		t.Fatalf("read catalog %s: %v", catalogPath, err)
	}
	var raw struct {
		Addons []catalog.CatalogEntry `yaml:"addons"`
	}
	if err := yaml.Unmarshal(yamlBytes, &raw); err != nil {
		t.Fatalf("unmarshal catalog: %v", err)
	}
	if len(raw.Addons) == 0 {
		t.Fatal("catalog has no entries under 'addons:'")
	}
	t.Logf("catalog %s: %d entries", catalogPath, len(raw.Addons))

	// Referenced-vs-present cross-check. The catalogue references
	// `<release-base>/<name>.bundle`, so the referenced set is derived from
	// the entry names.
	onDisk := map[string]bool{}
	files, err := filepath.Glob(filepath.Join(bundleDir, "*.bundle"))
	if err != nil {
		t.Fatalf("glob bundle dir: %v", err)
	}
	for _, f := range files {
		onDisk[filepath.Base(f)] = true
	}
	referenced := map[string]bool{}
	for _, e := range raw.Addons {
		referenced[e.Name+".bundle"] = true
	}
	var missing, unreferenced []string
	for name := range referenced {
		if !onDisk[name] {
			missing = append(missing, name)
		}
	}
	for name := range onDisk {
		if !referenced[name] {
			unreferenced = append(unreferenced, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(unreferenced)
	t.Logf("CROSS-CHECK: %d bundles referenced by the catalogue, %d present in %s",
		len(referenced), len(onDisk), bundleDir)
	t.Logf("CROSS-CHECK: referenced-but-missing = %d %v", len(missing), missing)
	t.Logf("CROSS-CHECK: present-but-unreferenced = %d %v", len(unreferenced), unreferenced)

	results := make([]auditResult, 0, len(raw.Addons))
	for i := range raw.Addons {
		e := raw.Addons[i]
		r := auditResult{Entry: e.Name, BundleFile: e.Name + ".bundle"}

		payload, cerr := CanonicalEntryBytes(e)
		if cerr != nil {
			r.CanonicalizeErr = cerr.Error()
			results = append(results, r)
			continue
		}
		sum := sha256.Sum256(payload)
		r.PayloadSHA256 = hex.EncodeToString(sum[:])

		bundleBytes, rerr := os.ReadFile(filepath.Join(bundleDir, r.BundleFile)) //nolint:gosec // operator-supplied audit input
		if rerr != nil {
			r.BundleReadErr = rerr.Error()
			results = append(results, r)
			continue
		}
		r.BundleBytes = len(bundleBytes)

		_ = rec.takeAll() // drop anything left over from the previous entry
		ok, issuer, verr := v.VerifyBundleBytes(context.Background(), payload, bundleBytes, policy)
		r.Verified, r.Issuer, r.Reasons = ok, issuer, rec.takeAll()
		if verr != nil {
			r.InfraErr = verr.Error()
		}

		dok, dissuer, dverr := v.VerifyBundleBytes(context.Background(), payload, bundleBytes, diagPolicy)
		r.DiagVerified, r.DiagIssuer, r.DiagReasons = dok, dissuer, rec.takeAll()
		if dverr != nil {
			r.DiagInfraErr = dverr.Error()
		}

		results = append(results, r)
	}

	pass, fail := 0, 0
	t.Logf("")
	t.Logf("PER-BUNDLE RESULTS under the production trust policy (%d attempted)", len(results))
	for _, r := range results {
		verdict := "FAIL"
		if r.Verified {
			verdict = "PASS"
			pass++
		} else {
			fail++
		}
		t.Logf("%-28s %-34s %s payload=%s bytes=%d", r.BundleFile, verdict, verdictDetail(r), r.PayloadSHA256[:16], r.BundleBytes)
	}
	t.Logf("")
	t.Logf("counted: %d PASS, %d FAIL, %d attempted", pass, fail, len(results))

	// Distinct rejection reasons, so a uniform cause is visible as uniform.
	byReason := map[string]int{}
	for _, r := range results {
		for _, reason := range r.Reasons {
			byReason[reason]++
		}
	}
	keys := make([]string, 0, len(byReason))
	for k := range byReason {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	t.Logf("")
	t.Logf("DISTINCT REJECTION REASONS from the verifier:")
	for _, k := range keys {
		t.Logf("  [%d bundles] %s", byReason[k], k)
	}

	diagPass := 0
	for _, r := range results {
		if r.DiagVerified {
			diagPass++
		}
	}
	t.Logf("")
	t.Logf("DIAGNOSTIC ISOLATION (NOT a pass, NOT the production policy):")
	t.Logf("  with the cert-claim assertion disabled, %d of %d verify.", diagPass, len(results))
	t.Logf("  That isolates signature + Rekor inclusion + cert chain + SAN trust")
	t.Logf("  from the workflow_ref cert-claim assertion. It does not make any")
	t.Logf("  bundle acceptable to the shipped runtime.")
	diagIssuers := map[string]int{}
	for _, r := range results {
		if r.DiagIssuer != "" {
			diagIssuers[r.DiagIssuer]++
		}
	}
	for issuer, n := range diagIssuers {
		t.Logf("  SAN accepted by the trust policy for %d bundles: %s", n, issuer)
	}

	// Report, do not soften. The harness records the outcome; it does not
	// decide whether a uniform failure is acceptable.
	if fail > 0 {
		t.Errorf("%d of %d published bundles do not verify under the production trust policy — see the per-bundle lines above", fail, len(results))
	}
	if len(missing) > 0 {
		t.Errorf("%d bundles referenced by the catalogue are missing from the asset set: %v", len(missing), missing)
	}
}

var claimRe = regexp.MustCompile(`workflow_ref "([^"]*)" does not match policy "([^"]*)"`)

func verdictDetail(r auditResult) string {
	switch {
	case r.CanonicalizeErr != "":
		return "canonicalize error: " + r.CanonicalizeErr
	case r.BundleReadErr != "":
		return "bundle unreadable: " + r.BundleReadErr
	case r.InfraErr != "":
		return "infra error: " + r.InfraErr
	case r.Verified:
		return "identity=" + r.Issuer
	case len(r.Reasons) > 0:
		if m := claimRe.FindStringSubmatch(r.Reasons[0]); m != nil {
			return fmt.Sprintf("cert-claim: ref=%q vs policy=%q", m[1], m[2])
		}
		return r.Reasons[0]
	default:
		return "rejected with no recorded reason"
	}
}
