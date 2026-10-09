package metrics

// contract_test.go — the published metrics contract.
//
// The promise this file keeps: if a Helm alert rule, a Grafana panel, a
// ```promql block or a sentence in the operator docs names a Sharko
// metric or a label on one, that metric and that label exist in the
// registry the /metrics handler serves.
//
// It replaces TestMetricsRegistered, which built a 25-entry map of metric
// names, never read a value out of it, and asserted only
// `if descCount != 25`. Two registered metrics were renamed out from
// under the shipped alert rule and that test still reported ok. A count
// that stays 25 whatever the metrics are called is decoration.
//
// # Reading a failure
//
// Two kinds:
//
//	UNKNOWN-METRIC  something names a sharko_ series the registry does
//	                not describe. Either the name is wrong, or the metric
//	                was renamed in code and the consumer was not.
//	BAD-LABEL       the metric is real but the label is not one of its
//	                label names, so the query silently matches nothing.
//
// # The baseline, and why it is a list and not a number
//
// The documentation on this branch still carries the defects story B12
// found; they are being corrected on the docs branch. So the check ships
// with the outstanding findings written down. That list is exact strings,
// not a count: a count-based baseline lets somebody fix one defect and
// introduce another and stay green, which is the same trap as a count of
// descriptions that never changes.
//
// The list only ever goes DOWN. Fixing a listed finding makes this test
// fail, telling you to delete the entry. Adding a new one makes it fail
// too. There is no way to make it pass by adding to the list without
// naming exactly the file and the metric you are excusing.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// findingKey identifies a finding independently of line numbers, which
// move whenever anybody edits a paragraph.
type findingKey struct {
	kind   string // UNKNOWN-METRIC | BAD-LABEL
	file   string
	metric string
	label  string // BAD-LABEL only
}

func (k findingKey) String() string {
	if k.label != "" {
		return fmt.Sprintf("%s | %s | %s | label=%s", k.kind, k.file, k.metric, k.label)
	}
	return fmt.Sprintf("%s | %s | %s", k.kind, k.file, k.metric)
}

// inlineExemption is one token the INLINE PROSE scanner matches that was
// never a metric name, together with the file that proves what it really
// is.
//
// evidence is a repo-relative path OUTSIDE the documentation. It proves
// the claim in one of the only two ways a token like this is ever real:
// either the path IS the thing (a Go source file whose name is the token),
// or the file's own bytes contain the token (a chart label key, a build
// tag). Documentation cannot be its own evidence — a page saying a token
// exists is the claim, not the proof, and accepting it is how the previous
// configuration allowlist counted its own strings as evidence.
type inlineExemption struct {
	why      string
	evidence string
}

// inlineExemptions are tokens the inline prose scanner matches that were
// never metric names.
//
// This list is the dangerous part of the whole check — an exemption list
// is exactly how the previous configuration guard went decorative. Four
// things hold it shut, all enforced by
// TestInlineExemptions_CannotHideARealMetric and
// TestExemptionsApplyToInlineProseOnly:
//
//	SCOPE     an exemption is consulted for inline prose and NOTHING else.
//	          A Helm alert rule, a Grafana panel and a ```promql fence are
//	          never exemptable, whatever is written here. That is the hole
//	          the adversarial review proved: one line here made a
//	          nonexistent metric inside a SHIPPED alert rule go green.
//	EVIDENCE  every entry names a real non-documentation file that really
//	          carries the token.
//	USED      an entry that silences nothing is deleted, so the list cannot
//	          quietly accumulate.
//	RATCHET   maxInlineExemptions. The list may not grow.
var inlineExemptions = map[string]inlineExemption{
	"sharko_helm": {
		why:      "a Go source filename — tests/e2e/harness/sharko_helm.go — named in the developer guide's harness table",
		evidence: "tests/e2e/harness/sharko_helm.go",
	},
	"sharko_helm_auth": {
		why:      "a Go source filename — tests/e2e/harness/sharko_helm_auth.go — the Helm-mode e2e auth bootstrap, named in the same table",
		evidence: "tests/e2e/harness/sharko_helm_auth.go",
	},
	"sharko_path": {
		why:      "a LABEL on the SLO alert rules (the SLO path id), not a metric name",
		evidence: "charts/sharko/templates/prometheusrules.yaml",
	},
	"sharko_unverified_dest_ok": {
		why:      "a Go build tag that turns off the TLS destination guard for one test file, named in the release notes",
		evidence: "internal/remoteclient/tlsguard_bypass_test.go",
	},
}

// maxInlineExemptions is the ratchet on the exemption list.
//
// THIS NUMBER MUST ONLY EVER GO DOWN. The test fails when the list is
// longer (somebody added an exemption) and when it is shorter (somebody
// removed one and left the number behind). Adding a fifth exemption is
// therefore never a one-line change that nobody sees: it is an edit to
// this line, with this comment on it, in the same diff.
//
// Before this, nothing at all ratcheted the list — which is how the
// adversarial review moved a recorded finding out of knownOpenFindings
// into the exemptions with an invented reason and went green.
const maxInlineExemptions = 4

// exemptionApplies says whether the exemption list may silence this
// reference.
//
// It is a function, not three lines inside the loop, because the scoping
// rule is the whole point and it needs a test of its own that drives every
// source name. See TestExemptionsApplyToInlineProseOnly.
func exemptionApplies(ref reference) bool {
	if ref.source != sourceInlineDoc {
		return false
	}
	_, listed := inlineExemptions[ref.metric]
	return listed
}

// knownOpenFindings is the outstanding contract breakage on this branch.
// Every entry is a documentation defect story B12 found; the corrections
// land on the docs branch, not here.
//
// THIS LIST MUST ONLY EVER GET SHORTER. Delete entries as they are fixed;
// the test names the ones that are already gone.
//
// Two things it does NOT contain, which is the point: the Helm chart's
// thirty alerting and recording rules, and all six Grafana dashboard
// panels. Every metric name and every label matcher in both resolves
// against the registry today.
var knownOpenFindings = map[string]string{
	// --- named as an alert to add, with no such metric registered.
	// --- Most carry a hedge in the same paragraph; two do not, and
	// --- catalog-trust-root-unavailable.md even names a wiring file ---
	"UNKNOWN-METRIC | docs/site/operator/catalog-trust-root-unavailable.md | sharko_catalog_verifier_initialized":           "asserts the wiring is in internal/catalog/signing/verify.go init — there is no such metric",
	"UNKNOWN-METRIC | docs/site/operator/adopt-cluster-entry-write-failed.md | sharko_adopt_partial_state_total":            "adoption partial-state counter, never registered",
	"UNKNOWN-METRIC | docs/site/operator/adopt-managed-by-label-read-failed.md | sharko_adopt_label_read_failed_total":      "adoption label-read counter, never registered",
	"UNKNOWN-METRIC | docs/site/operator/argocd-account-token-expired.md | sharko_argocd_token_expires_in_seconds":          "ArgoCD token expiry gauge, never registered",
	"UNKNOWN-METRIC | docs/site/operator/argocd-cluster-secret-corruption.md | sharko_cluster_test_status":                  "cluster-test status gauge, never registered",
	"UNKNOWN-METRIC | docs/site/operator/argocd-exec-plugin-auth-unsupported.md | sharko_cluster_test_errors_total":         "cluster-test error counter, never registered",
	"UNKNOWN-METRIC | docs/site/operator/argocd-pr-merge-no-converge.md | sharko_audit_events_total":                        "audit-event counter, never registered",
	"UNKNOWN-METRIC | docs/site/operator/auth-bypass.md | sharko_login":                                                     "from the wildcard mention `sharko_login_*_total`; the page says the wiring is future work",
	"UNKNOWN-METRIC | docs/site/operator/auth-bypass.md | sharko_login_attempts_total":                                      "login-attempt counter, never registered",
	"UNKNOWN-METRIC | docs/site/operator/auth-bypass.md | sharko_login_failed_total":                                        "login-failure counter, never registered",
	"UNKNOWN-METRIC | docs/site/operator/auto-merge-failed-after-pr-opened.md | sharko_pr_auto_merge_failed_total":          "auto-merge failure counter, never registered",
	"UNKNOWN-METRIC | docs/site/operator/catalog-parse-failure-on-startup.md | sharko_catalog_embedded_load_success":        "embedded-catalog load gauge, never registered",
	"UNKNOWN-METRIC | docs/site/operator/catalog-source-schema-validation-failed.md | sharko_catalog_source_failed_seconds": "catalog-source failure timestamp, never registered",
	"UNKNOWN-METRIC | docs/site/operator/cluster-reconciler-dependency-missing.md | sharko_reconciler_ticks_skipped_total":  "skipped-tick counter, never registered",
	"UNKNOWN-METRIC | docs/site/operator/reconciler-crash-loop.md | sharko_reconciler_ticks_total":                          "reconciler tick counter, never registered",
	"UNKNOWN-METRIC | docs/site/operator/eks-token-generation-failed.md | sharko_provider_eks_token_errors_total":           "EKS token error counter, never registered",
	"UNKNOWN-METRIC | docs/site/operator/encryption-key-not-configured.md | sharko_personal_token_500_total":                "personal-token 500 counter, never registered",
	"UNKNOWN-METRIC | docs/site/operator/git-provider-rate-limited.md | sharko_github_rate_limit_remaining":                 "GitHub rate-limit gauge, never registered",
	"UNKNOWN-METRIC | docs/site/operator/init-operation-abandoned.md | sharko_init_abandoned_total":                         "abandoned-init counter, never registered",
	"UNKNOWN-METRIC | docs/site/operator/secret-push-silently-failed.md | sharko_addon_secret_push_failures_total":          "addon-secret push failure counter, never registered",
	"UNKNOWN-METRIC | docs/site/operator/webhook-handler-failures.md | sharko_webhook_requests_total":                       "webhook request counter, never registered",
}

// TestPublishedMetricsContract is the check.
func TestPublishedMetricsContract(t *testing.T) {
	root := repoRoot(t)
	series := authoritativeSeries(t)
	refs := allReferences(t, root)

	if len(refs) < 50 {
		t.Fatalf("only %d metric references found across the chart, dashboards and docs — the consumer scan is broken, not the consumers", len(refs))
	}

	found := map[findingKey][]reference{}
	for _, ref := range refs {
		if exemptionApplies(ref) {
			continue
		}
		spec, known := series[ref.metric]
		if !known {
			k := findingKey{kind: "UNKNOWN-METRIC", file: ref.file, metric: ref.metric}
			found[k] = append(found[k], ref)
			continue
		}
		for _, l := range ref.labels {
			if spec.labels[l] || scrapeTimeLabels[l] {
				continue
			}
			k := findingKey{kind: "BAD-LABEL", file: ref.file, metric: ref.metric, label: l}
			found[k] = append(found[k], ref)
		}
	}

	var fresh []string
	for k, occurrences := range found {
		if _, known := knownOpenFindings[k.String()]; known {
			continue
		}
		where := occurrences[0].String()
		detail := k.String() + "  first seen at " + where
		if k.kind == "BAD-LABEL" {
			detail += "  (real labels: " + strings.Join(sortedLabels(series[k.metric].labels), ",") + ")"
		}
		fresh = append(fresh, detail)
	}

	var fixed []string
	for entry := range knownOpenFindings {
		stillThere := false
		for k := range found {
			if k.String() == entry {
				stillThere = true
				break
			}
		}
		if !stillThere {
			fixed = append(fixed, entry)
		}
	}

	if len(fresh) > 0 {
		sort.Strings(fresh)
		t.Errorf("%d metric contract breakage(s) that are not in the recorded baseline:\n\n  %s\n\n"+
			"Something published names a metric or a label Sharko does not have. Correct the "+
			"name, correct the label, or register the metric. Adding the finding to "+
			"knownOpenFindings is not a fix — that list is the record of defects already being "+
			"corrected on the docs branch, and it must only ever get shorter.",
			len(fresh), strings.Join(fresh, "\n  "))
	}

	if len(fixed) > 0 {
		sort.Strings(fixed)
		t.Errorf("%d recorded finding(s) no longer occur — delete them from knownOpenFindings:\n\n  %s\n\n"+
			"This is the ratchet. A baseline that is allowed to stay larger than reality is a "+
			"place for a new defect to hide.",
			len(fixed), strings.Join(fixed, "\n  "))
	}
}

// TestExemptionsApplyToInlineProseOnly is the scope rule, driven over
// every source name a reference can carry.
//
// This is the blocker the adversarial review proved. The exemption list is
// documented as being about the inline prose scanner, and it was applied
// in the loop over EVERY reference — so one line here silenced a
// nonexistent metric inside a shipped Helm alert rule, and the check said
// ok. A rule that is only true in a comment is not a rule.
func TestExemptionsApplyToInlineProseOnly(t *testing.T) {
	// A token that really is on the exemption list, so the only thing
	// deciding the answer is the source.
	var exempted string
	for token := range inlineExemptions {
		if exempted == "" || token < exempted {
			exempted = token
		}
	}
	if exempted == "" {
		t.Fatal("the exemption list is empty, so this test would prove nothing")
	}

	for _, source := range allSources {
		got := exemptionApplies(reference{source: source, metric: exempted})
		want := source == sourceInlineDoc
		if got != want {
			t.Errorf("exemptionApplies for a %s reference to %q = %v, want %v.\n"+
				"Only inline prose is exemptable. A chart rule, a dashboard panel and a "+
				"promql fence are published queries an operator deploys; an exemption that "+
				"reaches them hides a metric that does not exist inside an alert.",
				source, exempted, got, want)
		}
	}

	// And the source names are real: a rename in the consumer scanners
	// that left this list stale would make the scope test pass while
	// exempting everything.
	produced := map[string]bool{}
	for _, ref := range allReferences(t, repoRoot(t)) {
		produced[ref.source] = true
	}
	known := map[string]bool{}
	for _, s := range allSources {
		known[s] = true
	}
	for s := range produced {
		if !known[s] {
			t.Errorf("the consumer scan produced source %q, which is not one of the names the "+
				"scope rule knows about (%v). An unknown source is not covered by the scope "+
				"test, so nothing proves exemptions do not reach it.", s, allSources)
		}
	}
	if !produced[sourceInlineDoc] {
		t.Error("the consumer scan produced no inline-doc references at all — the one source " +
			"exemptions apply to is not being produced, so this whole test is decoration")
	}
	if !produced[sourceChartRule] {
		t.Error("the consumer scan produced no chart-rule references — the source the blocker " +
			"was proved against is not being produced")
	}
}

// TestInlineExemptions_CannotHideARealMetric is the meta-test on the
// exemption list. Without it the exemptions are a free pass: silence the
// {verified="false"} sentence by exempting
// sharko_catalog_source_entries and the whole check goes quiet on a real
// metric.
func TestInlineExemptions_CannotHideARealMetric(t *testing.T) {
	series := authoritativeSeries(t)
	root := repoRoot(t)

	// THE RATCHET. Growth is the failure mode: every entry here is a
	// place a real defect can be parked. Shrinking is fine and expected —
	// it just has to come with the number.
	switch {
	case len(inlineExemptions) > maxInlineExemptions:
		t.Errorf("the exemption list has grown to %d entries; maxInlineExemptions is %d.\n"+
			"An exemption is not a fix. Correct the sentence in the documentation, or if the "+
			"token really is not a metric, say so here AND raise this number in the same diff "+
			"so a reviewer sees the list got wider.", len(inlineExemptions), maxInlineExemptions)
	case len(inlineExemptions) < maxInlineExemptions:
		t.Errorf("the exemption list is down to %d entries; maxInlineExemptions still says %d. "+
			"Lower it — a cap left above reality is room for a new entry nobody notices.",
			len(inlineExemptions), maxInlineExemptions)
	}

	// USED. An entry that silences nothing is either stale or was never
	// about anything the scanner produces.
	silenced := map[string]bool{}
	for _, ref := range inlineDocRefs(t, root) {
		if _, listed := inlineExemptions[ref.metric]; listed {
			silenced[ref.metric] = true
		}
	}

	for token, ex := range inlineExemptions {
		why := ex.why
		if strings.TrimSpace(why) == "" {
			t.Errorf("exemption %q has no reason — every entry needs one line saying what the token really is", token)
		}

		if !silenced[token] {
			t.Errorf("exemption %q silences nothing: no inline prose in the documentation names it. "+
				"Delete the entry and lower maxInlineExemptions.", token)
		}

		checkExemptionEvidence(t, root, token, ex)

		// Exact strings only. A pattern or a prefix would exempt names
		// nobody has read.
		if strings.ContainsAny(token, "*?[]().|+^$\\") || strings.HasSuffix(token, "_") {
			t.Errorf("exemption %q looks like a pattern or a prefix — exemptions are exact metric-shaped tokens only", token)
		}
		if !strings.HasPrefix(token, metricNamePrefix) {
			t.Errorf("exemption %q does not even look like a metric name, so nothing would ever match it", token)
		}

		// The one that matters: an exempted token must not be a metric
		// Sharko really publishes.
		if _, real := series[token]; real {
			t.Errorf("exemption %q IS a registered metric — exempting it silences every label defect on a "+
				"real metric, which is the exact failure this check exists to catch. Remove the entry and "+
				"fix the reference instead.", token)
		}

		// Metric-naming suffixes. A token ending like a metric almost
		// certainly is one, and if it is not registered the honest
		// outcome is an UNKNOWN-METRIC finding, not an exemption.
		for _, suffix := range []string{"_total", "_seconds", "_timestamp", "_count", "_bucket"} {
			if strings.HasSuffix(token, suffix) {
				t.Errorf("exemption %q ends in %q — that is a metric name shape. If Sharko does not "+
					"register it, the reference is a defect to fix, not a token to exempt.", token, suffix)
			}
		}
	}
}

// checkExemptionEvidence proves the entry's claim about what the token
// really is, instead of believing its sentence.
//
// Two shapes, because those are the only two ways one of these tokens has
// ever been real:
//
//	the path IS the thing   a Go source file named after the token. Its
//	                        own bytes need not repeat its name, and
//	                        requiring that would be a rule about comments.
//	the file carries it     a chart label key, a build tag, an identifier —
//	                        the token appears literally in the file.
//
// The evidence may not live under docs/. A page that names the token is
// the claim being excused; letting it double as the proof is the
// self-evidence failure the whole registry rewrite was about.
func checkExemptionEvidence(t *testing.T, root, token string, ex inlineExemption) {
	t.Helper()

	rel := filepath.ToSlash(strings.TrimSpace(ex.evidence))
	if rel == "" {
		t.Errorf("exemption %q names no evidence file. Name the file that really carries the token — "+
			"a reason on its own is a sentence anybody can write.", token)
		return
	}
	if rel == "docs" || strings.HasPrefix(rel, "docs/") || rel == "README.md" {
		t.Errorf("exemption %q offers %s as evidence, which is documentation. The documentation is "+
			"what is being excused; it cannot also be the proof.", token, rel)
		return
	}
	if strings.HasSuffix(rel, "contract_test.go") {
		t.Errorf("exemption %q offers %s as evidence — that is this guard naming itself, which is "+
			"exactly how the previous allowlist counted its own strings as evidence", token, rel)
		return
	}

	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Errorf("exemption %q names evidence %s, which cannot be read: %v. The file was renamed or "+
			"deleted, or the entry was never true.", token, rel, err)
		return
	}

	base := filepath.Base(rel)
	nameIsTheToken := base == token || strings.TrimSuffix(base, filepath.Ext(base)) == token
	if nameIsTheToken {
		return
	}
	if !strings.Contains(string(body), token) {
		t.Errorf("exemption %q names evidence %s, but that file neither is named after the token nor "+
			"contains it. Nothing there says the token is anything other than a metric name.", token, rel)
	}
}

// TestKnownOpenFindings_AreWellFormed keeps the baseline honest in the
// two ways a baseline rots: entries nobody can parse, and entries with no
// reason attached.
func TestKnownOpenFindings_AreWellFormed(t *testing.T) {
	for entry, why := range knownOpenFindings {
		if strings.TrimSpace(why) == "" {
			t.Errorf("baseline entry %q has no reason", entry)
		}
		parts := strings.Split(entry, " | ")
		if len(parts) < 3 {
			t.Errorf("baseline entry %q is not in the recorded shape KIND | file | metric[ | label=x]", entry)
			continue
		}
		if parts[0] != "UNKNOWN-METRIC" && parts[0] != "BAD-LABEL" {
			t.Errorf("baseline entry %q has an unknown kind %q", entry, parts[0])
		}
		if !strings.HasPrefix(parts[2], metricNamePrefix) {
			t.Errorf("baseline entry %q does not name a sharko_ metric", entry)
		}
	}
}
