//go:build publishedbundles

// published_bundles_commit_test.go — S8's audit harness. Not part of any gate.
//
// S2's harness in published_bundles_test.go measured the 45 published v4.0.1
// bundles under LoadTrustPolicyFromEnv — which is now the THIRD-PARTY policy,
// and which still demands `^refs/tags/v.*$`. That measurement stands and is
// left alone; it is the record of the defect.
//
// This harness measures the same 45 bundles under the policy that actually
// applies to them: the EMBEDDED-catalogue policy, built by
// EmbeddedCatalogTrustPolicy from the commit the release was cut at. It runs
// each bundle three ways so the result cannot be misread:
//
//  1. the correct release commit  — must ACCEPT
//  2. a different commit          — must REFUSE, on the commit binding
//  3. no commit at all            — must REFUSE, on the missing stamp
//
// Case 2 is the wrong-commit break test done with real cryptography: these are
// genuine signatures, with a real Fulcio chain and a real Rekor inclusion
// proof, and the ONLY thing wrong with them is that they were made from a
// different commit than the one the policy expects. Case 3 is the
// missing-metadata break test, likewise on real signatures.
//
// It never sets SHARKO_SKIP_TUF_NETWORK, and it refuses to run if any trust
// env var is set, because a widened policy would silently change what is
// being measured.
//
// Run it like this:
//
//	SHARKO_AUDIT_CATALOG=/tmp/s8/addons.tag.yaml \
//	SHARKO_AUDIT_BUNDLE_DIR=/tmp/s8/bundles \
//	SHARKO_AUDIT_RELEASE_COMMIT=faf109fbbccac14fbd17fd5fa8ffb7066b2a5406 \
//	SHARKO_AUDIT_WRONG_COMMIT=b4879d135f6e611726ec542a1192b25a7ab03a4c \
//	go test -tags=publishedbundles -count=1 -v -timeout=20m \
//	  -run TestAudit_PublishedBundlesBoundToTheReleaseCommit ./internal/catalog/signing/
package signing

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/fulcio/certificate"

	"gopkg.in/yaml.v3"

	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/catalog/sources"
)

type commitAuditRow struct {
	Entry string

	// Under the correct release commit.
	OKRight     bool
	IssuerRight string
	ReasonRight string
	ErrRight    string

	// Under a different commit.
	OKWrong     bool
	ReasonWrong string
	ErrWrong    string

	// Under the binding required with no commit available.
	OKUnstamped     bool
	ReasonUnstamped string
	ErrUnstamped    string

	// What the certificate itself claims, read out rather than assumed.
	ClaimDigest    string
	ClaimSHA       string
	ClaimRef       string
	ClaimSAN       string
	ClaimIssuer    string
	ClaimTrigger   string
	CertParseError string
}

func TestAudit_PublishedBundlesBoundToTheReleaseCommit(t *testing.T) {
	catalogPath := strings.TrimSpace(os.Getenv("SHARKO_AUDIT_CATALOG"))
	bundleDir := strings.TrimSpace(os.Getenv("SHARKO_AUDIT_BUNDLE_DIR"))
	releaseCommit := strings.TrimSpace(os.Getenv("SHARKO_AUDIT_RELEASE_COMMIT"))
	wrongCommit := strings.TrimSpace(os.Getenv("SHARKO_AUDIT_WRONG_COMMIT"))
	if catalogPath == "" || bundleDir == "" || releaseCommit == "" || wrongCommit == "" {
		t.Fatal("SHARKO_AUDIT_CATALOG, SHARKO_AUDIT_BUNDLE_DIR, " +
			"SHARKO_AUDIT_RELEASE_COMMIT and SHARKO_AUDIT_WRONG_COMMIT must all be set")
	}
	if !IsFullCommitSHA(releaseCommit) {
		t.Fatalf("SHARKO_AUDIT_RELEASE_COMMIT %q is not a full commit hash", releaseCommit)
	}
	if !IsFullCommitSHA(wrongCommit) {
		t.Fatalf("SHARKO_AUDIT_WRONG_COMMIT %q is not a full commit hash", wrongCommit)
	}
	if strings.EqualFold(releaseCommit, wrongCommit) {
		t.Fatal("the two commits are the same, so the wrong-commit case would prove nothing")
	}

	for _, envVar := range []string{EnvTrustedIdentities, EnvTrustedWorkflowRef, "SHARKO_SKIP_TUF_NETWORK"} {
		if v := strings.TrimSpace(os.Getenv(envVar)); v != "" {
			t.Fatalf("%s is set to %q — this harness must run under the shipped defaults only", envVar, v)
		}
	}

	base, err := LoadTrustPolicyFromEnv()
	if err != nil {
		t.Fatalf("LoadTrustPolicyFromEnv: %v", err)
	}
	right := EmbeddedCatalogTrustPolicy(base, releaseCommit)
	wrong := EmbeddedCatalogTrustPolicy(base, wrongCommit)
	unstamped := EmbeddedCatalogTrustPolicy(base, "dev")

	t.Logf("SHIPPED EMBEDDED POLICY (from LoadTrustPolicyFromEnv + EmbeddedCatalogTrustPolicy)")
	for i, id := range right.Identities {
		t.Logf("  identity[%d]  = %s", i, id)
	}
	t.Logf("  workflow_ref = %s", right.WorkflowRef)
	t.Logf("  release commit required = %v", right.RequireReleaseCommit)
	t.Logf("  release commit expected = %s", right.ReleaseCommit)
	t.Logf("WRONG-COMMIT CONTROL   = %s", wrong.ReleaseCommit)
	t.Logf("UNSTAMPED CONTROL      = required=%v expected=%q",
		unstamped.RequireReleaseCommit, unstamped.ReleaseCommit)
	t.Logf("THIRD-PARTY POLICY, untouched: workflow_ref = %s (required=%v)",
		base.WorkflowRef, base.RequireReleaseCommit)

	trustRoot, err := LoadProductionTrustedRoot(context.Background())
	if err != nil {
		t.Fatalf("LoadProductionTrustedRoot (no skip escape hatch used): %v", err)
	}
	t.Logf("sigstore trust root loaded over the network")

	rec := &reasonRecorder{}
	v := NewVerifier(nil, WithTrustedMaterial(trustRoot), WithLogger(slog.New(rec)))

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

	files, err := filepath.Glob(filepath.Join(bundleDir, "*.bundle"))
	if err != nil {
		t.Fatalf("glob bundle dir: %v", err)
	}
	t.Logf("catalog %s: %d entries; bundle dir %s: %d files",
		catalogPath, len(raw.Addons), bundleDir, len(files))

	rows := make([]commitAuditRow, 0, len(raw.Addons))
	for i := range raw.Addons {
		e := raw.Addons[i]
		row := commitAuditRow{Entry: e.Name}

		payload, cerr := CanonicalEntryBytes(e)
		if cerr != nil {
			row.ErrRight = "canonicalize: " + cerr.Error()
			rows = append(rows, row)
			continue
		}
		bundleBytes, rerr := os.ReadFile(filepath.Join(bundleDir, e.Name+".bundle")) //nolint:gosec // operator-supplied audit input
		if rerr != nil {
			row.ErrRight = "read bundle: " + rerr.Error()
			rows = append(rows, row)
			continue
		}

		// Read the certificate's own claims so the report says what the
		// certificate holds rather than inferring it from the verdict.
		row.ClaimDigest, row.ClaimSHA, row.ClaimRef, row.ClaimSAN,
			row.ClaimIssuer, row.ClaimTrigger, row.CertParseError = surveyCert(bundleBytes)

		_ = rec.takeAll()
		row.OKRight, row.IssuerRight, row.ErrRight = verifyOnce(t, v, payload, bundleBytes, right, rec, &row.ReasonRight)
		row.OKWrong, _, row.ErrWrong = verifyOnce(t, v, payload, bundleBytes, wrong, rec, &row.ReasonWrong)
		row.OKUnstamped, _, row.ErrUnstamped = verifyOnce(t, v, payload, bundleBytes, unstamped, rec, &row.ReasonUnstamped)

		rows = append(rows, row)
	}

	// --- report -------------------------------------------------------------
	passRight, failRight := 0, 0
	passWrong, passUnstamped := 0, 0
	t.Logf("")
	t.Logf("PER-BUNDLE RESULTS (%d attempted)", len(rows))
	t.Logf("%-28s %-6s %-6s %-6s %s", "bundle", "right", "wrong", "nostmp", "detail")
	for _, r := range rows {
		if r.OKRight {
			passRight++
		} else {
			failRight++
		}
		if r.OKWrong {
			passWrong++
		}
		if r.OKUnstamped {
			passUnstamped++
		}
		detail := "identity=" + r.IssuerRight
		if !r.OKRight {
			detail = "REFUSED: " + firstNonEmpty(r.ReasonRight, r.ErrRight, "no reason recorded")
		}
		t.Logf("%-28s %-6s %-6s %-6s %s",
			r.Entry+".bundle", verdict(r.OKRight), verdict(r.OKWrong), verdict(r.OKUnstamped), detail)
	}

	t.Logf("")
	t.Logf("UNDER THE CORRECT RELEASE COMMIT (%s): %d PASS, %d FAIL, %d attempted",
		releaseCommit, passRight, failRight, len(rows))
	t.Logf("UNDER A DIFFERENT COMMIT        (%s): %d accepted (must be 0)",
		wrongCommit, passWrong)
	t.Logf("WITH NO RELEASE COMMIT AVAILABLE:     %d accepted (must be 0)", passUnstamped)

	t.Logf("")
	t.Logf("CERTIFICATE CLAIMS, read out of the certificates themselves:")
	distinct := map[string]int{}
	for _, r := range rows {
		key := strings.Join([]string{
			"sourceRepositoryDigest=" + r.ClaimDigest,
			"githubWorkflowSHA=" + r.ClaimSHA,
			"workflowRef=" + r.ClaimRef,
			"trigger=" + r.ClaimTrigger,
			"issuer=" + r.ClaimIssuer,
			"san=" + r.ClaimSAN,
		}, "  ")
		distinct[key]++
	}
	keys := make([]string, 0, len(distinct))
	for k := range distinct {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Logf("  [%d bundles] %s", distinct[k], k)
	}

	t.Logf("")
	t.Logf("DISTINCT REFUSAL REASONS, wrong-commit run:")
	for _, k := range distinctReasons(rows, func(r commitAuditRow) string { return r.ReasonWrong }) {
		t.Logf("  %s", k)
	}
	t.Logf("DISTINCT REFUSAL REASONS, no-release-commit run:")
	for _, k := range distinctReasons(rows, func(r commitAuditRow) string { return r.ReasonUnstamped }) {
		t.Logf("  %s", k)
	}

	// --- assertions ---------------------------------------------------------
	if failRight > 0 {
		t.Errorf("%d of %d published bundles do not verify under the shipped embedded "+
			"policy at the release commit — see the per-bundle lines above",
			failRight, len(rows))
	}
	if passWrong > 0 {
		t.Errorf("%d bundles were accepted under a DIFFERENT commit — the release-commit "+
			"binding is not doing anything", passWrong)
	}
	if passUnstamped > 0 {
		t.Errorf("%d bundles were accepted with no release commit available — that is the "+
			"silent bypass the binding exists to prevent", passUnstamped)
	}
	// Every wrong-commit refusal must come from the BINDING, not from something
	// unrelated. A refusal for the wrong reason is a bad break, not a pass.
	for _, r := range rows {
		if !strings.Contains(r.ReasonWrong, "release-commit binding failed") ||
			!strings.Contains(r.ReasonWrong, "claims source commit") {
			t.Errorf("%s: the wrong-commit run was refused for an unrelated reason, so it "+
				"proves nothing about the binding: %q", r.Entry, r.ReasonWrong)
		}
		if !strings.Contains(r.ReasonUnstamped, "this build carries no release commit") {
			t.Errorf("%s: the no-release-commit run was refused for an unrelated reason: %q",
				r.Entry, r.ReasonUnstamped)
		}
	}
}

func verifyOnce(
	t *testing.T,
	v *Verifier,
	payload, bundleBytes []byte,
	policy sources.TrustPolicy,
	rec *reasonRecorder,
	reasonOut *string,
) (bool, string, string) {
	t.Helper()
	_ = rec.takeAll()
	ok, issuer, err := v.VerifyBundleBytes(context.Background(), payload, bundleBytes, policy)
	if rs := rec.takeAll(); len(rs) > 0 {
		*reasonOut = strings.Join(rs, " | ")
	}
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	return ok, issuer, errStr
}

// surveyCert reads the claims out of a bundle's leaf certificate. This is what
// confirms WHICH field carries the source-commit claim, rather than taking an
// OID table's word for it.
func surveyCert(bundleBytes []byte) (digest, sha, ref, san, issuer, trigger, parseErr string) {
	b := &bundle.Bundle{}
	if err := b.UnmarshalJSON(bundleBytes); err != nil {
		return "", "", "", "", "", "", "parse bundle: " + err.Error()
	}
	vc, err := b.VerificationContent()
	if err != nil {
		return "", "", "", "", "", "", "verification content: " + err.Error()
	}
	cert := vc.Certificate()
	if cert == nil {
		return "", "", "", "", "", "", "no certificate"
	}
	ext, err := certificate.ParseExtensions(cert.Extensions)
	if err != nil {
		return "", "", "", "", "", "", "parse extensions: " + err.Error()
	}
	if summary, serr := certificate.SummarizeCertificate(cert); serr == nil {
		san = summary.SubjectAlternativeName
	}
	return ext.SourceRepositoryDigest, ext.GithubWorkflowSHA, ext.GithubWorkflowRef,
		san, ext.Issuer, ext.BuildTrigger, ""
}

func distinctReasons(rows []commitAuditRow, pick func(commitAuditRow) string) []string {
	seen := map[string]int{}
	for _, r := range rows {
		seen[pick(r)]++
	}
	out := make([]string, 0, len(seen))
	for k, n := range seen {
		out = append(out, "["+itoa(n)+" bundles] "+k)
	}
	sort.Strings(out)
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func verdict(ok bool) string {
	if ok {
		return "PASS"
	}
	return "REFUSE"
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
