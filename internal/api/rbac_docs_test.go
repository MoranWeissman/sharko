package api

// rbac_docs_test.go — the two pages that describe Sharko's Kubernetes
// permissions must keep describing what the chart actually grants.
//
// # Why this exists
//
// docs/site/technical-preview.md and docs/site/operator/security.md both spell
// out, for somebody deciding whether to install Sharko, exactly what a
// takeover of the Sharko pod would give an attacker. On 2026-08-20 both had
// drifted from charts/sharko/templates/rbac.yaml and had to be corrected by
// hand. Nothing stopped them drifting again: no test read either page against
// the chart, so the chart could get wider and both pages would go on saying
// the old, smaller thing.
//
// The existing precedent for pinning a documentation sentence to the code it
// describes is trusted_proxy_docs_test.go, which reads the shipped security
// page and asks the production parser whether the value it suggests is really
// refused. This file follows that shape: it reads the shipped chart, works out
// what it grants, and then requires the pages to say so.
//
// # It fails in BOTH directions, which is the whole point
//
//   - The chart gets wider and nobody touches the pages. Every rule in the
//     chart is pinned as an exact LIST below. A new rule, a new verb, a
//     dropped resourceNames restriction or a rule that moves behind a
//     condition all change that list and fail here, naming what changed, so
//     whoever widened the chart has to go and update the pages.
//   - A claim on a page goes stale. Each load-bearing claim is derived FROM
//     the chart and looked for in the page text, so deleting or weakening the
//     sentence fails. And any list of verbs written in a sentence about
//     Secrets has to match a real rule — a page cannot invent a permission
//     Sharko does not have, or quietly keep one it lost.
//
// # There is no count and no floor
//
// The chart pin is a list compared with !=, not a "at least N rules" floor.
// Reading the chart file, finding no rules in it, or finding no Secrets rule
// at all is fatal rather than a quiet pass.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const (
	rbacTemplatePath   = "charts/sharko/templates/rbac.yaml"
	previewPagePath    = "docs/site/technical-preview.md"
	rbacSecurityPage   = "docs/site/operator/security.md"
	rbacFullnameMarker = "<name from the release>"
)

// rbacRule is one entry under a Role's or ClusterRole's `rules:`.
type rbacRule struct {
	// role is the object the rule belongs to, as kind plus the suffix the
	// chart appends to the release name ("" for the base ClusterRole).
	role string
	// enclosing lists the template blocks the rule sits inside, outermost
	// first. A rule with only the chart's own `if .Values.rbac.create` wrapper
	// is created unconditionally for anybody who installs the chart.
	enclosing     []string
	apiGroups     []string
	resources     []string
	resourceNames []string // nil when the rule is not restricted by name
	verbs         []string
}

// restrictedByName reports whether this rule is limited to named objects.
// Kubernetes cannot scope `create` by name, which is why one rule in the auth
// Role is deliberately unrestricted — the pages say so, and this is how that
// claim is checked rather than trusted.
func (r rbacRule) restrictedByName() bool { return len(r.resourceNames) > 0 }

// unconditional reports whether the rule is created for every install, rather
// than only when an operator switches something on.
func (r rbacRule) unconditional() bool {
	return len(r.enclosing) == 1 && r.enclosing[0] == "if .Values.rbac.create"
}

func (r rbacRule) String() string {
	names := "any name"
	if r.restrictedByName() {
		names = "names=" + strings.Join(r.resourceNames, "+")
	}
	return r.role +
		" | in " + strings.Join(r.enclosing, " > ") +
		" | groups=" + strings.Join(r.apiGroups, "+") +
		" | resources=" + strings.Join(r.resources, "+") +
		" | " + names +
		" | verbs=" + strings.Join(r.verbs, ",")
}

var (
	rbacBlockOpen  = regexp.MustCompile(`\{\{-?\s*(if|range|with)\s+(.*?)\s*-?\}\}`)
	rbacBlockEnd   = regexp.MustCompile(`\{\{-?\s*end\s*-?\}\}`)
	rbacQuoted     = regexp.MustCompile(`"([^"]*)"`)
	rbacTemplateGo = regexp.MustCompile(`\{\{.*?\}\}`)
)

// parseRbacTemplate reads the shipped chart template and returns every rule in
// it. The file is a Go template rather than plain YAML, so it is read as text:
// what matters here is which rules exist, what they grant, and which template
// blocks they sit inside — and all three survive as text.
func parseRbacTemplate(t *testing.T, root string) []rbacRule {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, rbacTemplatePath))
	if err != nil {
		t.Fatalf("reading %s: %v — this guard cannot check anything", rbacTemplatePath, err)
	}

	var (
		rules     []rbacRule
		enclosing []string
		kind      string
		roleName  string
		inMeta    bool
		current   *rbacRule
	)

	flush := func() {
		if current != nil {
			rules = append(rules, *current)
			current = nil
		}
	}

	for _, raw := range strings.Split(string(body), "\n") {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)

		// Template block bookkeeping first, so a rule knows what it sits in.
		if m := rbacBlockOpen.FindStringSubmatch(trimmed); m != nil {
			enclosing = append(enclosing, m[1]+" "+m[2])
		} else if rbacBlockEnd.MatchString(trimmed) {
			if len(enclosing) == 0 {
				t.Fatalf("%s has an {{ end }} with no matching block — the guard's reading of the file is wrong", rbacTemplatePath)
			}
			enclosing = enclosing[:len(enclosing)-1]
		}

		if trimmed == "---" {
			flush()
			kind, roleName, inMeta = "", "", false
			continue
		}
		if strings.HasPrefix(trimmed, "kind:") {
			// Only the first kind in a document is the object's own kind;
			// roleRef and subjects carry a kind too.
			if kind == "" {
				kind = strings.TrimSpace(strings.TrimPrefix(trimmed, "kind:"))
			}
			continue
		}
		if trimmed == "metadata:" {
			inMeta = true
			continue
		}
		if inMeta && strings.HasPrefix(trimmed, "name:") {
			name := strings.TrimSpace(strings.TrimPrefix(trimmed, "name:"))
			// The release name is a template expression; what identifies the
			// object here is the suffix the chart appends to it.
			if idx := strings.LastIndex(name, "}}"); idx >= 0 {
				name = name[idx+2:]
			}
			roleName = kind + name
			inMeta = false
			continue
		}
		if trimmed == "rules:" || trimmed == "subjects:" {
			inMeta = false
			continue
		}

		if strings.HasPrefix(trimmed, "- apiGroups:") {
			flush()
			current = &rbacRule{
				role:      roleName,
				enclosing: append([]string(nil), enclosing...),
				apiGroups: rbacListValues(trimmed),
			}
			continue
		}
		if current == nil {
			continue
		}
		switch {
		case strings.HasPrefix(trimmed, "resources:"):
			current.resources = rbacListValues(trimmed)
		case strings.HasPrefix(trimmed, "resourceNames:"):
			current.resourceNames = rbacListValues(trimmed)
		case strings.HasPrefix(trimmed, "verbs:"):
			current.verbs = rbacListValues(trimmed)
		}
	}
	flush()

	if len(rules) == 0 {
		t.Fatalf("no rules were read out of %s at all — the guard is looking at nothing", rbacTemplatePath)
	}
	if len(enclosing) != 0 {
		t.Fatalf("%s left template blocks open (%v) — the guard's reading of the file is wrong, so every "+
			"\"is this rule conditional\" answer below is unreliable", rbacTemplatePath, enclosing)
	}
	return rules
}

// rbacListValues pulls the values out of an inline YAML list. A value written
// as a template expression — a Secret name coming from the release name or
// from a chart value — is recorded as a marker rather than guessed at: what
// matters for every claim on the pages is that the rule IS restricted by name,
// not which name it happens to resolve to.
func rbacListValues(line string) []string {
	_, after, ok := strings.Cut(line, ":")
	if !ok {
		return nil
	}
	// Template expressions are blanked out BEFORE the quoted values are read.
	// A name built by the chart — `{{ .Values.config.connectionSecretName |
	// default "sharko-connections" | quote }}` — has a quoted string INSIDE
	// the expression, and reading that string would record whatever default
	// happens to be written there today as if it were the rule's fixed name.
	hadTemplate := rbacTemplateGo.MatchString(after)
	after = rbacTemplateGo.ReplaceAllString(after, "")

	var out []string
	for _, m := range rbacQuoted.FindAllStringSubmatch(after, -1) {
		out = append(out, m[1])
	}
	if hadTemplate {
		out = append(out, rbacFullnameMarker)
	}
	return out
}

// secretsRules returns only the rules that grant something on Secrets — the
// ones both pages make promises about.
func secretsRules(rules []rbacRule) []rbacRule {
	var out []rbacRule
	for _, r := range rules {
		for _, res := range r.resources {
			if res == "secrets" {
				out = append(out, r)
				break
			}
		}
	}
	return out
}

// rbacRulesAsShipped is the exact list of rules the chart grants today, one
// line each. It is compared with != and nothing else — no floor, no "at
// least", no subset.
//
// Changing the chart is meant to fail here. That is not friction for its own
// sake: these are the permissions two published pages promise an operator, so
// a change to them is a change to a published promise, and somebody has to go
// and update the promise in the same breath.
var rbacRulesAsShipped = []string{
	`ClusterRole | in if .Values.rbac.create | groups=argoproj.io | resources=applications+appprojects+applicationsets | any name | verbs=get,list,watch`,
	`ClusterRole | in if .Values.rbac.create > if .Values.config.nodeAccess | groups= | resources=nodes | any name | verbs=get,list`,
	`Role-argocd-secrets | in if .Values.rbac.create | groups= | resources=secrets | any name | verbs=get,list,watch,create,update,patch,delete`,
	`Role-argocd-secrets | in if .Values.rbac.create | groups=argoproj.io | resources=applicationsets | any name | verbs=get,list,watch,patch,update,delete`,
	`Role-argocd-secrets | in if .Values.rbac.create | groups=argoproj.io | resources=applications | any name | verbs=get,list,watch,patch`,
	`Role-auth | in if .Values.rbac.create | groups= | resources=secrets | names=` + rbacFullnameMarker + ` | verbs=get,update`,
	`Role-auth | in if .Values.rbac.create | groups= | resources=secrets | names=sharko-migration-settings | verbs=get,create,update,delete`,
	`Role-auth | in if .Values.rbac.create | groups= | resources=secrets | names=` + rbacFullnameMarker + ` | verbs=get,update`,
	`Role-auth | in if .Values.rbac.create | groups= | resources=secrets | names=sharko-ai-config | verbs=get,update`,
	`Role-auth | in if .Values.rbac.create | groups= | resources=secrets | any name | verbs=create`,
	`Role-auth | in if .Values.rbac.create | groups= | resources=secrets | names=sharko-initial-admin-secret | verbs=get,update,delete`,
	`Role-auth | in if .Values.rbac.create | groups= | resources=secrets | names=sharko-api-tokens | verbs=get,update`,
	`Role-auth | in if .Values.rbac.create | groups= | resources=configmaps | any name | verbs=get,list,create,update,delete`,
	`Role-auth | in if .Values.rbac.create | groups= | resources=configmaps | names=` + rbacFullnameMarker + ` | verbs=get`,
	`Role-auth | in if .Values.rbac.create | groups= | resources=events | any name | verbs=create`,
	`Role-secrets-provider | in if .Values.rbac.create > range $ns := ($secretsProviderNamespaces | uniq) | groups= | resources=secrets | any name | verbs=get,list`,
}

// TestRbacChart_GrantsExactlyTheRulesTheDocsWereWrittenAgainst is the pin.
func TestRbacChart_GrantsExactlyTheRulesTheDocsWereWrittenAgainst(t *testing.T) {
	root := repoRootForDocsGuard(t)
	rules := parseRbacTemplate(t, root)

	var got []string
	for _, r := range rules {
		got = append(got, r.String())
	}
	if strings.Join(got, "\n") != strings.Join(rbacRulesAsShipped, "\n") {
		t.Errorf("the chart no longer grants what the published pages describe.\n\nnow:\n  %s\n\nwritten down here:\n  %s\n\n"+
			"docs/site/technical-preview.md and docs/site/operator/security.md tell an operator exactly what a\n"+
			"takeover of the Sharko pod would give somebody. If this list changed, that answer changed. Update\n"+
			"both pages and then update rbacRulesAsShipped, in that order.",
			strings.Join(got, "\n  "), strings.Join(rbacRulesAsShipped, "\n  "))
	}

	if len(secretsRules(rules)) == 0 {
		t.Fatal("not one rule in the chart grants anything on Secrets — either the chart changed beyond " +
			"recognition or the reader is broken, and every check below would pass on an empty list")
	}
}

// rbacVerb is every verb Kubernetes RBAC has for these resources. All of them,
// not just the ones the chart uses today — and "deletecollection" comes before
// "delete" because regexp alternation takes the first branch that matches, so
// the shorter spelling listed first would swallow the longer one.
//
// The full list is load-bearing. With only the seven verbs the chart grants, a
// page could claim "get, list, watch, create, update, patch, delete,
// deletecollection" and the reader would stop at "delete", match a real rule,
// and pass — the invented permission was invisible. That was a real green
// break test on 2026-08-21, and it was the guard at fault, not the page.
const rbacVerb = `deletecollection|get|list|watch|create|update|patch|delete|\*`

// rbacVerbRun finds a list of Kubernetes verbs written out in a sentence:
// "get, list, watch, create, update, patch, delete" or "get and list".
var rbacVerbRun = regexp.MustCompile(`\b(` + rbacVerb + `)((?:(?:,\s*|\s+and\s+)(?:` + rbacVerb + `))+)\b`)

// hostPermissionSections names, per page, the stretch that describes what the
// CHART grants on the cluster Sharko runs on.
//
// The boundary matters. Both pages also describe what Sharko does with
// credentials it holds for OTHER clusters — "Sharko reaches your fleet's
// clusters directly to create, update and delete addon Secrets there" — and
// that is not a rule in this chart at all. Reading it as one would have the
// guard demand a chart rule that must never exist.
//
// A marker that has gone missing is FATAL rather than "scan the whole page" or
// "scan nothing": a rewritten heading must send somebody back here to say what
// the new boundary is.
var hostPermissionSections = []struct{ path, from, to string }{
	{
		path: previewPagePath,
		from: "### Across the whole cluster Sharko runs on",
		to:   "### Outside Kubernetes",
	},
	{
		path: rbacSecurityPage,
		from: "Sharko creates one `ClusterRole`",
		to:   "## Secret Encryption",
	},
}

// pageSection returns the normalised text between a section's two markers.
func pageSection(t *testing.T, root string, sec struct{ path, from, to string }) string {
	t.Helper()
	whole := normalisedPage(t, root, sec.path)
	from := strings.Join(strings.Fields(sec.from), " ")
	to := strings.Join(strings.Fields(sec.to), " ")

	start := strings.Index(whole, from)
	if start < 0 {
		t.Fatalf("%s no longer contains %q, so this guard cannot tell which part of the page is about "+
			"the permissions this chart grants. Point it at the new heading.", sec.path, sec.from)
	}
	end := strings.Index(whole[start:], to)
	if end < 0 {
		t.Fatalf("%s no longer contains %q after %q, so this guard would read to the end of the page and "+
			"start objecting to sentences about other clusters. Point it at the new heading.", sec.path, sec.to, sec.from)
	}
	section := whole[start : start+end]
	if strings.TrimSpace(section) == "" {
		t.Fatalf("the section between %q and %q in %s is empty — this guard would check nothing", sec.from, sec.to, sec.path)
	}
	return section
}

// normalisedPage collapses every run of whitespace to a single space, so a
// claim that is wrapped across two lines still reads as one sentence.
func normalisedPage(t *testing.T, root, rel string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	return strings.Join(strings.Fields(string(body)), " ")
}

// TestRbacDocs_EveryVerbListAboutSecretsMatchesARealRule is the direction that
// catches a stale or invented claim.
//
// Both pages write out, in words, what Sharko may do to Secrets. Every such
// list on either page has to be exactly the verbs of some rule the chart
// really has — so a page cannot keep a permission that was removed, and cannot
// claim one that was never granted. And every Secrets rule with more than one
// verb has to be written out somewhere, so a rule cannot be widened while the
// pages stay quiet about it.
func TestRbacDocs_EveryVerbListAboutSecretsMatchesARealRule(t *testing.T) {
	root := repoRootForDocsGuard(t)
	rules := secretsRules(parseRbacTemplate(t, root))

	granted := map[string]bool{}
	for _, r := range rules {
		granted[verbKey(r.verbs)] = true
	}

	written := map[string]bool{}
	for _, page := range hostPermissionSections {
		text := pageSection(t, root, page)
		// Only sentences about Secrets. The same pages also describe the
		// ApplicationSet permissions in the same section, and those are a
		// different rule with different verbs.
		for _, sentence := range strings.Split(text, ". ") {
			if !strings.Contains(strings.ToLower(sentence), "secret") {
				continue
			}
			for _, m := range rbacVerbRun.FindAllString(sentence, -1) {
				key := verbKey(splitVerbRun(m))
				written[key] = true
				if !granted[key] {
					t.Errorf("%s says Sharko may %q on Secrets, and no rule in %s grants exactly that.\n"+
						"Either the chart lost a permission the page still promises, or the page is claiming "+
						"one Sharko never had. Both are wrong to publish.\nsentence: %s",
						page.path, m, rbacTemplatePath, strings.TrimSpace(sentence))
				}
			}
		}
	}

	for _, r := range rules {
		if len(r.verbs) < 2 {
			// A single-verb rule reads as plain English ("permission to
			// create Secrets") rather than as a list, and is checked by name
			// in the claims test below.
			continue
		}
		if r.restrictedByName() {
			// A rule limited to Sharko's own named Secrets is summarised on
			// both pages as "a short list of Sharko's own named Secrets", by
			// name rather than by verb, and that is the right level of detail
			// for it: the reader's question is whose Secrets Sharko can touch,
			// and the answer is "only its own". The rules that answer that
			// question with "anybody's" are the ones whose verbs have to be
			// written out, and they are the ones left here.
			continue
		}
		if !written[verbKey(r.verbs)] {
			t.Errorf("the chart grants %s on Secrets (%s), and neither published page writes that list out.\n"+
				"An operator deciding whether to install Sharko reads those pages to find out what a takeover "+
				"of the pod would give somebody. A permission nobody wrote down is a permission nobody agreed to.",
				strings.Join(r.verbs, ", "), r.role)
		}
	}
}

// verbKey turns a set of verbs into a comparable key, order-independent —
// a page may list them in any order and still be describing the same rule.
func verbKey(verbs []string) string {
	sorted := append([]string(nil), verbs...)
	sort.Strings(sorted)
	return strings.Join(sorted, ",")
}

// splitVerbRun turns "get, list and watch" back into its verbs.
func splitVerbRun(run string) []string {
	run = strings.ReplaceAll(run, " and ", ",")
	var out []string
	for _, part := range strings.Split(run, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// TestRbacDocs_BothPagesStillCarryTheClaimsTheChartMakesTrue checks the claims
// that are not a verb list: whether a rule is limited to named objects, and
// whether it exists for everybody or only when something is switched on.
//
// Each claim is derived from the chart and then looked for in the page, so
// deleting the sentence fails, and changing the chart underneath it fails too.
func TestRbacDocs_BothPagesStillCarryTheClaimsTheChartMakesTrue(t *testing.T) {
	root := repoRootForDocsGuard(t)
	rules := parseRbacTemplate(t, root)

	preview := normalisedPage(t, root, previewPagePath)
	security := normalisedPage(t, root, rbacSecurityPage)

	// 1. The cluster-wide role. It grants nothing on Secrets, and both pages
	// say so in as many words — that sentence is the single most load-bearing
	// claim on either page.
	clusterWideSecrets := false
	for _, r := range secretsRules(rules) {
		if strings.HasPrefix(r.role, "ClusterRole") {
			clusterWideSecrets = true
		}
	}
	if clusterWideSecrets {
		t.Errorf("the cluster-wide ClusterRole in %s now grants something on Secrets. %s says "+
			"Sharko has \"no cluster-wide read of Secrets\" and %s says \"No Secrets rule\". Both are now false, "+
			"and a cluster-wide list on Secrets hands over their contents.",
			rbacTemplatePath, previewPagePath, rbacSecurityPage)
	} else {
		mustSay(t, previewPagePath, preview, "no cluster-wide read of Secrets")
		mustSay(t, rbacSecurityPage, security, "**No Secrets rule.**")
	}

	// 2. The two Secrets rules that are NOT limited to named objects, and the
	// one that cannot be. Whether a rule is name-scoped is the difference
	// between "Sharko reads its own Secrets" and "Sharko reads yours".
	for _, r := range secretsRules(rules) {
		switch {
		case r.role == "Role-argocd-secrets" && !r.restrictedByName():
			mustSay(t, rbacSecurityPage, security, "not only the ones Sharko created")
			mustSay(t, previewPagePath, preview, "It is not restricted to the Secrets Sharko made")
		case r.role == "Role-secrets-provider" && !r.restrictedByName():
			mustSay(t, rbacSecurityPage, security, "there is no `resourceNames` restriction")
			mustSay(t, previewPagePath, preview, "with no list of names and no exception")
		case r.role == "Role-auth" && !r.restrictedByName():
			// The create rule. Kubernetes cannot scope create by name, so
			// this one is unavoidable — and both pages say why, because an
			// unexplained exception is the one people quietly widen.
			mustSay(t, rbacSecurityPage, security, "Kubernetes cannot scope `create` by name")
			mustSay(t, previewPagePath, preview, "Kubernetes cannot restrict `create` to specific names")
		}
	}

	// 3. Which rules exist for everybody. The Secrets grants are not opt-in:
	// installing the chart is enough. Node access IS opt-out, and the pages
	// say so — if that ever flipped, the pages would be describing a choice
	// nobody has.
	for _, r := range secretsRules(rules) {
		if !r.unconditional() && r.role != "Role-secrets-provider" {
			t.Errorf("the Secrets rule on %s is now created only inside %v. Both published pages describe it "+
				"as something every install gets. Say on the pages what now switches it on.",
				r.role, r.enclosing)
		}
	}
	// The secrets-provider Role is repeated once per namespace, and the
	// release namespace is always in that list whether an operator asked for
	// it or not. Both pages carry that exact warning.
	mustSay(t, rbacSecurityPage, security, "the release namespace is always included, whether or not you list it")
	mustSay(t, previewPagePath, preview, "the chart adds it whether or not you asked for it")

	for _, r := range rules {
		if len(r.resources) == 1 && r.resources[0] == "nodes" {
			if r.unconditional() {
				t.Errorf("the Node read rule is no longer behind a switch, but both pages tell operators they "+
					"can turn it off with config.nodeAccess=false. Chart says: %v", r.enclosing)
			}
			mustSay(t, previewPagePath, preview, "Turn it off with `config.nodeAccess=false`")
			mustSay(t, rbacSecurityPage, security, "granted by default")
		}
	}
}

// mustSay fails when a page has stopped carrying a claim the chart makes true.
func mustSay(t *testing.T, page, normalised, claim string) {
	t.Helper()
	want := strings.Join(strings.Fields(claim), " ")
	if !strings.Contains(normalised, want) {
		t.Errorf("%s no longer says %q.\nThe chart still grants what that sentence describes, so removing it "+
			"leaves an operator reading a smaller permission than Sharko really has.", page, claim)
	}
}

// TestRbacDocsGuard_ReadsTheChartRatherThanAssumingIt is the self-proof: the
// parser is run over a small template holding every shape it claims to
// understand, and must report exactly those rules.
//
// Without this, a parser that quietly returned an empty list for one Role
// would make every check above pass by finding nothing to object to.
func TestRbacDocsGuard_ReadsTheChartRatherThanAssumingIt(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(rbacTemplatePath)), 0o755); err != nil {
		t.Fatalf("building the probe tree: %v", err)
	}
	probe := `{{- if .Values.rbac.create }}
kind: ClusterRole
metadata:
  name: {{ include "sharko.fullname" . }}
rules:
  - apiGroups: ["argoproj.io"]
    resources: ["applications"]
    verbs: ["get", "list"]
  {{- if .Values.config.nodeAccess }}
  - apiGroups: [""]
    resources: ["nodes"]
    verbs: ["get", "list"]
  {{- end }}
---
kind: Role
metadata:
  name: {{ include "sharko.fullname" . }}-auth
  namespace: {{ .Release.Namespace }}
rules:
  - apiGroups: [""]
    resources: ["secrets"]
    resourceNames: ["sharko-api-tokens"]
    verbs: ["get", "update"]
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["create"]
subjects:
  - kind: ServiceAccount
    name: whatever
{{- end }}
`
	if err := os.WriteFile(filepath.Join(dir, rbacTemplatePath), []byte(probe), 0o644); err != nil {
		t.Fatalf("writing the probe: %v", err)
	}

	var got []string
	for _, r := range parseRbacTemplate(t, dir) {
		got = append(got, r.String())
	}
	want := []string{
		`ClusterRole | in if .Values.rbac.create | groups=argoproj.io | resources=applications | any name | verbs=get,list`,
		`ClusterRole | in if .Values.rbac.create > if .Values.config.nodeAccess | groups= | resources=nodes | any name | verbs=get,list`,
		`Role-auth | in if .Values.rbac.create | groups= | resources=secrets | names=sharko-api-tokens | verbs=get,update`,
		`Role-auth | in if .Values.rbac.create | groups= | resources=secrets | any name | verbs=create`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the chart reader found:\n  %s\n\nwant:\n  %s\n\nEvery check in this file leans on it, so a "+
			"reader that misses a rule makes them all pass on nothing.",
			strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}

	// The two judgements the claims above are built on, stated rather than
	// implied: what "restricted by name" and "unconditional" mean.
	rules := parseRbacTemplate(t, dir)
	if !rules[2].restrictedByName() {
		t.Error("a rule with resourceNames is not being read as restricted by name — every \"reads only its own Secrets\" claim would then be checked against nothing")
	}
	if rules[3].restrictedByName() {
		t.Error("a rule with NO resourceNames is being read as restricted by name — the guard would call an unrestricted Secret grant a narrow one")
	}
	if !rules[0].unconditional() {
		t.Error("a rule behind nothing but the chart's own rbac.create wrapper is not being read as unconditional")
	}
	if rules[1].unconditional() {
		t.Error("a rule behind config.nodeAccess is being read as unconditional — the guard would stop noticing when an opt-in permission becomes default-on")
	}
}

// TestRbacVerbRun_ReadsAVerbListOutOfASentence pins the sentence reader in
// both directions, because getting it wrong one way hides a stale promise and
// the other way fires on ordinary English until somebody switches it off.
func TestRbacVerbRun_ReadsAVerbListOutOfASentence(t *testing.T) {
	mustFind := map[string]string{
		"— get, list, watch, create, update, patch, delete. It is not":     "create,delete,get,list,patch,update,watch",
		"**Read every Secret in the namespace** — get and list, with no":   "get,list",
		"`get/list` is not a run, but get, list is":                        "get,list",
		"Also get and list on Nodes if config.nodeAccess is on (default).": "get,list",
		// The verb the guard used to be blind to. A page claiming this on top
		// of the seven the chart grants passed, because the reader stopped at
		// "delete" and matched a real rule. Pinned here so it cannot come back.
		"— get, list, watch, create, update, patch, delete, deletecollection. It is not": "create,delete,deletecollection,get,list,patch,update,watch",
	}
	for sentence, want := range mustFind {
		m := rbacVerbRun.FindString(sentence)
		if m == "" {
			t.Errorf("no verb list found in %q — a stale promise in a sentence like this would go unnoticed", sentence)
			continue
		}
		if got := verbKey(splitVerbRun(m)); got != want {
			t.Errorf("read %q out of %q, want %q", got, sentence, want)
		}
	}

	mustNotFind := []string{
		"Permission to create Secrets generally.",
		"Sharko can read your secrets backend.",
		"It only deletes Secrets that carry its own ownership label.",
		"Write on a short list of Sharko's own named Secrets, and nothing else.",
	}
	for _, sentence := range mustNotFind {
		if m := rbacVerbRun.FindString(sentence); m != "" {
			t.Errorf("the sentence reader found the verb list %q in %q, which is ordinary English about "+
				"one permission — firing here is how a guard gets switched off", m, sentence)
		}
	}
}

// -------------------------------------------------------------------------
// ArgoCD's own permissions — a second, separate promise on the same two pages
// -------------------------------------------------------------------------
//
// The rules above are Kubernetes RBAC. ArgoCD keeps its own permission
// settings in the argocd-rbac-cm ConfigMap, and the chart used to edit them
// during install: a post-install hook Job that ran kubectl as Sharko's
// ServiceAccount and gave Sharko a role granting sync on every Application in
// every project. The product owner's ruling removed it. Neither published
// page had ever mentioned it, while both told an operator that Sharko's
// cluster-wide reach was read-only.
//
// So both pages now carry a second promise — that installing Sharko changes
// nothing in ArgoCD — and it needs pinning the same way the first one is:
// derived from the chart, so the chart getting it wrong fails here, and looked
// for in the page, so deleting the sentence fails here too.

// argocdPolicyMarkers are what a chart template that edits ArgoCD's
// permissions cannot avoid naming.
var argocdPolicyMarkers = []string{"argocd-rbac-cm", "policy.csv", "role:sharko"}

// chartMentionsArgoCDPolicy walks every file of the server chart and reports
// the places, outside comments, that name ArgoCD's permission settings.
//
// It returns the count of files it read as well, because a walk that reads
// nothing finds nothing, and "found nothing" is the answer that makes the
// pages' promise look true.
func chartMentionsArgoCDPolicy(t *testing.T, root string) (hits []string, filesRead int) {
	t.Helper()
	chartDir := filepath.Join(root, "charts", "sharko")
	sawRbacTemplate := false

	err := filepath.Walk(chartDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		filesRead++
		rel, _ := filepath.Rel(root, path)
		if filepath.ToSlash(rel) == rbacTemplatePath {
			sawRbacTemplate = true
		}
		for lineNo, raw := range strings.Split(string(body), "\n") {
			line := strings.TrimSpace(raw)
			if strings.HasPrefix(line, "#") {
				continue
			}
			for _, marker := range argocdPolicyMarkers {
				if strings.Contains(line, marker) {
					hits = append(hits, fmt.Sprintf("%s:%d names %q", filepath.ToSlash(rel), lineNo+1, marker))
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v — this guard cannot check anything", chartDir, err)
	}
	if filesRead == 0 {
		t.Fatalf("no files were read out of %s at all — the guard is looking at nothing, and an empty "+
			"look reads as \"the chart touches nothing\"", chartDir)
	}
	if !sawRbacTemplate {
		t.Fatalf("the walk read %d files under %s but never reached %s, which is definitely there. The walk "+
			"is not reading the chart, so its \"found nothing\" answer means nothing.",
			filesRead, chartDir, rbacTemplatePath)
	}
	return hits, filesRead
}

// TestArgoCDPolicyDocs_BothPagesSayWhatTheChartMakesTrue moves in both
// directions: the chart growing a template that edits ArgoCD's permissions
// fails here, and either page dropping the sentence fails here.
func TestArgoCDPolicyDocs_BothPagesSayWhatTheChartMakesTrue(t *testing.T) {
	root := repoRootForDocsGuard(t)
	hits, filesRead := chartMentionsArgoCDPolicy(t, root)

	preview := normalisedPage(t, root, previewPagePath)
	security := normalisedPage(t, root, rbacSecurityPage)

	if len(hits) > 0 {
		t.Fatalf("the server chart names ArgoCD's permission settings again, in %d place(s):\n  %s\n\n"+
			"%s and %s both tell an operator that installing Sharko changes nothing in ArgoCD. If that "+
			"stopped being true, those sentences are now false and have to be rewritten before this list "+
			"is accepted.", len(hits), strings.Join(hits, "\n  "), previewPagePath, rbacSecurityPage)
	}
	t.Logf("read %d chart files, none of which names ArgoCD's permission settings", filesRead)

	// The promise itself, on both pages, in the same words.
	mustSay(t, previewPagePath, preview, "installing Sharko does not change them.")
	mustSay(t, rbacSecurityPage, security, "installing Sharko does not change them.")

	// And the half of the promise that is easy to lose: that a fleet runs
	// perfectly well without the sync permission. Without this sentence an
	// operator reading either page cannot tell whether declining to grant it
	// breaks their addons.
	mustSay(t, previewPagePath, preview, "does **not** need permission to make ArgoCD sync an application")
	mustSay(t, rbacSecurityPage, security, "does **not** need permission to sync applications for addons to be deployed")
}

// TestArgoCDPolicyDocs_TheOptionalPolicyIsScopedAndSaysSo pins the policy the
// security page hands an operator to paste.
//
// A page that documents an optional permission is only as good as the policy
// printed on it. The one the removed hook granted itself — sync on every
// Application in every project — is exactly what must not appear here as
// advice, and the narrow one is exactly what must.
func TestArgoCDPolicyDocs_TheOptionalPolicyIsScopedAndSaysSo(t *testing.T) {
	root := repoRootForDocsGuard(t)
	security := normalisedPage(t, root, rbacSecurityPage)

	// The scoped policy, and the project name it is scoped to, which is a real
	// value in the engine chart rather than a name invented for the page.
	mustSay(t, rbacSecurityPage, security, "p, role:sharko-sync, applications, sync, sharko-addons/*, allow")
	mustSay(t, rbacSecurityPage, security, "**Do not write `applications, sync, */*`.**")

	engineValues, err := os.ReadFile(filepath.Join(root, "charts", "sharko-engine", "values.yaml"))
	if err != nil {
		t.Fatalf("reading the engine chart values: %v — the page's project name cannot be checked", err)
	}
	if !strings.Contains(string(engineValues), "name: sharko-addons") {
		t.Errorf("%s tells operators to scope the sync permission to the AppProject \"sharko-addons\", and "+
			"charts/sharko-engine/values.yaml no longer creates a project by that name. A policy scoped to "+
			"a project that does not exist grants nothing, and the operator would have no way to tell.",
			rbacSecurityPage)
	}

	// Both directions on the wildcard: the page must not be the thing that
	// recommends it. The warning above is the only place it may appear, so
	// any OTHER occurrence is the page quietly handing it out.
	const wildcard = "applications, sync, */*"
	if occurrences := strings.Count(security, wildcard); occurrences != 1 {
		t.Errorf("%s mentions %q %d times. Exactly one is expected — the sentence telling operators not to "+
			"write it. Any other occurrence is the page recommending the permission the install hook used "+
			"to grant itself.", rbacSecurityPage, wildcard, occurrences)
	}
}
