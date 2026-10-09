package serverrender

// bf11_address_coverage_test.go — nobody has to remember to add the next
// address to the rule.
//
// The first version of the BF11 address guard carried a hand-written list of
// three fields. That list was wrong on the day it was written: ai.ollama.url
// is a fourth address the chart writes into the Pod as a plain value, and
// setting it to https://svc:<password>@ollama.internal:11434 put a password
// into the Pod specification exactly the way bootstrapAdmin.password used to.
// A hand-written list of the things a rule covers is a list that goes stale,
// and the way it goes stale is silently.
//
// So the coverage question is asked of the CHART instead. This file walks the
// chart templates, finds every environment entry whose value comes from
// something an operator sets, and requires each one to be classified: either
// it is an address the rule checks, or it is written down here as not an
// address. A fifth address added later is in neither list and fails here.
//
// # What BF12-4 changed here, and why it had to
//
// The walk used to resolve only the FIRST setting mentioned in an expression.
// It called FindStringSubmatch, which returns one match, so an expression
// naming two settings was classified by one of them and the other was invisible
// — not to the unclassified check, not to the stale check, not to the list of
// expressions the walk cannot trace.
//
// That is not a theoretical shape. The ordinary Helm way to add a second
// address is a fallback:
//
//	value: {{ default .Values.ai.baseURL .Values.aiFallbackURL | quote }}
//
// A reviewer made exactly that change, set the fallback to an address with a
// credential in the userinfo, and the credential rendered into the Pod in the
// clear with this whole package still green. The promise this file makes —
// that a fifth address cannot be added without a test failing — held only for
// expressions that mention one setting.
//
// Every operand is resolved now: every `.Values.x` in the expression, every
// `$var.field` whose variable came from a values path, and every relative
// `.field` under an open `with`. An expression that mentions three settings
// produces three paths and every one of them has to be classified.
//
// # What the walk can and cannot see
//
// It reads the template text, not a render, because a render only shows the
// branches one set of values reaches and the whole point is to see every
// branch. What it cannot follow is an expression that reaches a value through
// a named template — `{{ include "sharko.argocdNamespace" . }}` reads
// .Values.rbac.argocdNamespace and the walk sees only the include. Those are
// listed separately below and there are four of them, so the same
// fails-on-growth rule applies; but if somebody routes a NEW address through
// a helper, this guard sees a changed include list rather than a new address,
// and a person has to look. That is a real limit and it is stated rather than
// papered over.
//
// An expression is listed as untraceable when ANY of its operands is
// untraceable, even when another operand did resolve. `default .Values.x
// $computed` is both a classified path and an untraceable expression, and it
// has to appear in both lists.
//
// The other thing the walk does not read is a whole block of environment
// entries splatted in with `toYaml` — `.Values.extraEnv` is one, and it is an
// operator-supplied list of arbitrary name/value pairs, so no per-setting
// classification is possible for it at all. Those are found and pinned by
// TestEveryEnvironmentBlockSplattedIntoThePodIsAccountedFor below, which is a
// separate guard because it is a different and weaker promise.

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// --------------------------------------------------------------------------
// what the chart writes, classified
// --------------------------------------------------------------------------

// validatedAddresses are the values paths sharko.validateAddresses checks.
// This list is not trusted: TestTheRuleChecksExactlyTheAddressesClaimedHere
// reads the actual includes out of _helpers.tpl and compares.
var validatedAddresses = []string{
	"ai.baseURL",
	"ai.ollama.url",
	"connection.argocd.serverURL",
	"connection.git.repoURL",
}

// notAddresses are the operator-set values the chart writes into the Pod that
// are not addresses: names, regions, namespaces, model names, booleans,
// durations, prefixes and identifiers. None of them is a place a credential
// travels the way a URL is.
//
// Two entries are here deliberately rather than by oversight:
//
//	connection.gitops.hostClusterName — a cluster NAME, not an address
//	e2e.gitHostsAllowlist             — host names for the test harness, no
//	                                    scheme and no userinfo section
var notAddresses = []string{
	"ai.authHeader",
	"ai.cloudModel",
	"ai.maxIterations",
	"ai.ollama.agentModel",
	"ai.ollama.model",
	"ai.provider",
	"bootstrapAdmin.writeInitialSecret",
	"catalog.freshness.interval",
	"clusterRegSource.argocdNamespace",
	"clusterRegSource.type",
	"config.connectionSecretName",
	"config.environments",
	"connection.addonSecretProvider.namespace",
	"connection.addonSecretProvider.prefix",
	"connection.addonSecretProvider.region",
	"connection.addonSecretProvider.roleArn",
	"connection.addonSecretProvider.type",
	"connection.argocd.insecure",
	"connection.argocd.namespace",
	"connection.git.organization",
	"connection.git.owner",
	"connection.git.project",
	"connection.git.provider",
	"connection.git.repo",
	"connection.git.repository",
	"connection.gitops.baseBranch",
	"connection.gitops.branchPrefix",
	"connection.gitops.commitPrefix",
	"connection.gitops.defaultAddons",
	"connection.gitops.hostClusterName",
	"connection.gitops.prAutoMerge",
	"connection.provider.namespace",
	"connection.provider.prefix",
	"connection.provider.region",
	"connection.provider.roleArn",
	"connection.provider.type",
	"e2e.gitHostsAllowlist",
	"settings.probeMode",
}

// unresolvedValueExpressions are the environment values the walk cannot trace
// back to a values path: a named template, or a local variable computed from
// one. Written out in full so a change to any of them fails here and somebody
// has to decide whether the new expression can carry an address.
var unresolvedValueExpressions = []string{
	`"http://{{ include "sharko.fullname" . }}-ollama:11434"`,
	`{{ $allowInlineValue | quote }}`,
	`{{ include "sharko.argocdNamespace" . | quote }}`,
	`{{ include "sharko.fullname" . | quote }}`,
}

// splattedEnvironmentBlocks are the whole blocks of environment entries the
// chart pours into a container with `toYaml`, by the values path they come
// from.
//
// These are NOT per-setting classifiable: the operator supplies a list of
// arbitrary name/value pairs, so there is no field name for the address rule
// to check. extraEnv is a deliberate escape hatch and the chart does not
// promise anything about what goes in it. What this file DOES promise is that
// a second such block cannot appear without somebody being made to look at it.
var splattedEnvironmentBlocks = []string{
	"extraEnv",
}

// --------------------------------------------------------------------------
// the walk
// --------------------------------------------------------------------------

var (
	templateComment = regexp.MustCompile(`(?s)\{\{-?\s*/\*.*?\*/\s*-?\}\}`)
	blockKeyword    = regexp.MustCompile(`\{\{-?\s*(if|with|range|else if|else|end)\b([^}]*)\}\}`)
	variableAssign  = regexp.MustCompile(`\{\{-?\s*(\$[A-Za-z0-9_]+)\s*:?=\s*([^}]*)\}\}`)
	valueLine       = regexp.MustCompile(`^\s*value:\s*(.*)$`)
	absoluteRef     = regexp.MustCompile(`\.Values\.([A-Za-z0-9_.]+)`)
	relativeRef     = regexp.MustCompile(`(?:^|[\s(|])\.([A-Za-z][A-Za-z0-9_.]*)`)
	variableRef     = regexp.MustCompile(`(\$[A-Za-z0-9_]+)\.([A-Za-z][A-Za-z0-9_.]*)`)
	bareVariableRef = regexp.MustCompile(`\$[A-Za-z0-9_]+`)
	includeCall     = regexp.MustCompile(`\binclude\s+"`)
	bareYAMLKeyLine = regexp.MustCompile(`^(\s*)([A-Za-z][A-Za-z0-9_.-]*):\s*$`)
	toYamlSplat     = regexp.MustCompile(`\{\{-?\s*toYaml\s+(\S+)\s*\|`)
	whitespaceRun   = regexp.MustCompile(`\s+`)
)

// envValueSource is one rendered `value:` in one template.
type envValueSource struct {
	file string
	line int
	// paths is EVERY values path the expression reads, not just the first.
	// Empty when the walk can trace none of them.
	paths []string
	// untraceable is true when at least one operand of the expression could
	// not be traced to a values path — an include, or a computed local. It
	// can be true while paths is non-empty.
	untraceable bool
	// expr is the raw expression, kept so an untraceable one can be named.
	expr string
}

// envSplat is one whole block of environment entries poured in with toYaml.
type envSplat struct {
	file string
	line int
	path string
	expr string
}

// chartTemplateFiles returns every file helm reads out of the chart's
// templates directory, by path relative to that directory.
//
// # Why this is a walk over the whole tree and not a list of extensions
//
// It used to call filepath.Glob with "*.yaml". helm does not work that way. It
// reads EVERY file under the templates directory, whatever the name: this was
// measured with a throwaway chart holding one file per shape, and helm turned
// "other.txt", a file with no extension at all, "plain.yaml.snap" and a loose
// ".tpl" into manifests exactly as it did the ".yaml" ones, and it descended
// into a subdirectory to do the same there. That is not hypothetical either —
// a stray "deployment.yaml.snap" left in this chart's templates directory was
// rendered as a live template during an earlier round of this work.
//
// So "*.yaml" meant _helpers.tpl — which is where sharko.classifyAddress and
// the environment helpers live — was never read by this walk at all, and a
// template added tomorrow as .tpl, .txt or with no extension would have been
// invisible the same way.
//
// Extending the glob to a longer list of extensions would be the same defect
// with more entries in it, so there is no extension test here at all. The two
// things helm treats specially are about what a file EMITS, not about whether
// its content is read: a file whose name starts with "_" is a partial and
// produces no manifest of its own, and NOTES.txt becomes the install notes.
// Both are still parsed, both can still define an environment value, so both
// are read here.
func chartTemplateFiles(t *testing.T) (dir string, rel []string) {
	t.Helper()
	dir = filepath.Join(repoRoot(t), "charts", "sharko", "templates")
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		r, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		rel = append(rel, r)
		return nil
	})
	if err != nil {
		t.Fatalf("walking the chart templates under %s: %v", dir, err)
	}
	sort.Strings(rel)
	if len(rel) == 0 {
		t.Fatalf("no template file found under %s, so this walk read nothing", dir)
	}
	return dir, rel
}

// walkTemplatesForEnvValues reads every chart template and returns every
// `value:` it writes, plus every environment block splatted in with toYaml. It
// never takes a list of files: the directory is walked, so a template added
// later is read without anybody remembering this exists.
func walkTemplatesForEnvValues(t *testing.T) ([]envValueSource, []envSplat) {
	t.Helper()
	dir, rel := chartTemplateFiles(t)

	var found []envValueSource
	var splats []envSplat
	for _, name := range rel {
		body, readErr := os.ReadFile(filepath.Join(dir, name))
		if readErr != nil {
			t.Fatalf("reading %s: %v", name, readErr)
		}
		v, s := walkOneTemplate(filepath.ToSlash(name), string(body))
		found = append(found, v...)
		splats = append(splats, s...)
	}
	if len(found) == 0 {
		t.Fatal("the walk found no environment values in any chart template. Either the templates " +
			"stopped writing any, or the reader is broken — either way nothing below is checking anything.")
	}
	return found, splats
}

// TestTheTemplateWalkReadsEveryFileHelmRenders is the guard on the walk's
// reach, and it is the one that fails when somebody puts an extension test
// back.
//
// helm reads every file under the templates directory. So this compares the
// set of files the walk read against the set of files that are THERE, by
// walking the directory a second time with no filter of any kind. The two have
// to be the same set — not the same count, the same names — so a file the walk
// skipped is named in the failure and a file that has gone is named too.
func TestTheTemplateWalkReadsEveryFileHelmRenders(t *testing.T) {
	dir, rel := chartTemplateFiles(t)

	var onDisk []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		r, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		onDisk = append(onDisk, filepath.ToSlash(r))
		return nil
	})
	if err != nil {
		t.Fatalf("listing %s a second time: %v", dir, err)
	}
	sort.Strings(onDisk)
	if len(onDisk) == 0 {
		t.Fatalf("there is no file at all under %s, so this comparison is between two empty sets", dir)
	}

	var read []string
	for _, r := range rel {
		read = append(read, filepath.ToSlash(r))
	}
	if strings.Join(read, "\n") != strings.Join(onDisk, "\n") {
		t.Errorf("the walk does not read every file helm renders.\nhelm reads:\n  %s\nthe walk read:\n  %s\n"+
			"helm renders every file under this directory whatever it is called, so the walk must not "+
			"test the name.", strings.Join(onDisk, "\n  "), strings.Join(read, "\n  "))
	}

	// And the file that was invisible before: the partial holding the address
	// rule itself. Named here so that dropping it out of the walk again is a
	// failure that says what was lost, not a silently shorter list.
	sawThePartial := false
	for _, r := range read {
		if r == "_helpers.tpl" {
			sawThePartial = true
		}
	}
	if !sawThePartial {
		t.Error("the walk did not read _helpers.tpl, which is where the address rule and the " +
			"environment helpers live. That is the file the old \"*.yaml\" filter hid.")
	}
}

// walkOneTemplate is the reader, split out so a positive control can hand it
// text of its own.
func walkOneTemplate(file, body string) ([]envValueSource, []envSplat) {
	// Comments are removed, but each one is replaced by the newlines it
	// spanned. Collapsing a multi-line comment to nothing would shift every
	// line number after it, and a guard that names the wrong line is a guard
	// somebody stops trusting.
	body = templateComment.ReplaceAllStringFunc(body, func(m string) string {
		return strings.Repeat("\n", strings.Count(m, "\n"))
	})

	// Local variables assigned straight from a values path, so
	// `{{ $bootstrap.writeInitialSecret }}` can be traced.
	vars := map[string]string{}
	for _, m := range variableAssign.FindAllStringSubmatch(body, -1) {
		if ref := absoluteRef.FindStringSubmatch(m[2]); ref != nil {
			vars[m[1]] = ref[1]
		}
	}

	var out []envValueSource
	var splats []envSplat
	var stack []string // one entry per open block; the `with` prefix or ""
	// openKeys tracks the YAML keys still open by indentation, so a splat
	// can name the block it is being poured into.
	type openKey struct {
		indent int
		name   string
	}
	var openKeys []openKey

	for i, line := range strings.Split(body, "\n") {
		if m := bareYAMLKeyLine.FindStringSubmatch(line); m != nil {
			indent := len(m[1])
			for len(openKeys) > 0 && openKeys[len(openKeys)-1].indent >= indent {
				openKeys = openKeys[:len(openKeys)-1]
			}
			openKeys = append(openKeys, openKey{indent: indent, name: m[2]})
		}

		for _, m := range blockKeyword.FindAllStringSubmatch(line, -1) {
			switch m[1] {
			case "if", "range":
				stack = append(stack, "")
			case "with":
				stack = append(stack, withPrefix(m[2], vars))
			case "end":
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
			}
		}

		if m := toYamlSplat.FindStringSubmatch(line); m != nil {
			indent := len(line) - len(strings.TrimLeft(line, " "))
			enclosing := ""
			for k := len(openKeys) - 1; k >= 0; k-- {
				if openKeys[k].indent < indent {
					enclosing = openKeys[k].name
					break
				}
			}
			if enclosing == "env" {
				path := resolveSplatSubject(m[1], stack, vars)
				splats = append(splats, envSplat{
					file: file, line: i + 1, path: path,
					expr: whitespaceRun.ReplaceAllString(strings.TrimSpace(line), " "),
				})
			}
		}

		m := valueLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		expr := strings.TrimSpace(m[1])
		if !strings.Contains(expr, "{{") {
			continue // a constant the chart chose; no operator value in it
		}
		paths, untraceable := resolvePaths(expr, stack, vars)
		out = append(out, envValueSource{
			file: file, line: i + 1,
			paths:       paths,
			untraceable: untraceable,
			expr:        whitespaceRun.ReplaceAllString(expr, " "),
		})
	}
	return out, splats
}

// resolveSplatSubject names the values path a `toYaml` splat pours in. A bare
// "." means whatever `with` scope is open around it.
func resolveSplatSubject(subject string, stack []string, vars map[string]string) string {
	if strings.TrimSpace(subject) == "." {
		return currentPrefix(stack)
	}
	paths, _ := resolvePaths(subject, stack, vars)
	if len(paths) == 0 {
		return ""
	}
	return paths[0]
}

// withPrefix turns the subject of a `with` into a values path prefix.
func withPrefix(subject string, vars map[string]string) string {
	if m := absoluteRef.FindStringSubmatch(subject); m != nil {
		return m[1]
	}
	if m := variableRef.FindStringSubmatch(subject); m != nil {
		if base, ok := vars[m[1]]; ok {
			return base + "." + m[2]
		}
		return ""
	}
	if m := regexp.MustCompile(`^\s*\.([A-Za-z][A-Za-z0-9_.]*)`).FindStringSubmatch(subject); m != nil {
		return "\x00" + m[1] // relative: joined onto whatever is already open
	}
	return ""
}

// resolvePaths works out EVERY values path an expression reads, and says
// whether any part of it could not be traced.
//
// The old version of this returned the first match and stopped, which is what
// let a second address hide behind a `default` fallback. Every operand is
// read now, and an operand the walk cannot follow makes the whole expression
// untraceable even when a sibling operand resolved cleanly.
func resolvePaths(expr string, stack []string, vars map[string]string) (paths []string, untraceable bool) {
	prefix := currentPrefix(stack)

	seen := map[string]bool{}
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		paths = append(paths, p)
	}

	// Every ".Values.x" in the expression, not just the first.
	for _, m := range absoluteRef.FindAllStringSubmatch(expr, -1) {
		add(m[1])
	}

	// Every "$var.field". A variable the walk never saw assigned from a
	// values path is a computed local and cannot be resolved.
	resolvedVars := map[string]bool{}
	for _, m := range variableRef.FindAllStringSubmatch(expr, -1) {
		if base, ok := vars[m[1]]; ok {
			add(base + "." + m[2])
			resolvedVars[m[1]] = true
			continue
		}
		untraceable = true
	}

	// Any remaining bare "$var" is a computed local too.
	for _, m := range bareVariableRef.FindAllString(expr, -1) {
		if !resolvedVars[m] {
			untraceable = true
		}
	}

	// Every relative ".field" under an open `with`. ".Values.x" is matched by
	// this pattern as well, and is skipped here because it is already handled
	// above as an absolute reference.
	for _, m := range relativeRef.FindAllStringSubmatch(expr, -1) {
		if strings.HasPrefix(m[1], "Values.") || m[1] == "Values" {
			continue
		}
		if prefix == "" {
			add(m[1])
			continue
		}
		add(prefix + "." + m[1])
	}

	// A named template hides whatever it reads.
	if includeCall.MatchString(expr) {
		untraceable = true
	}

	if len(paths) == 0 {
		untraceable = true
	}
	return paths, untraceable
}

// currentPrefix joins the open `with` scopes into one values path.
func currentPrefix(stack []string) string {
	var parts []string
	for _, s := range stack {
		switch {
		case s == "":
			continue
		case strings.HasPrefix(s, "\x00"):
			parts = append(parts, strings.TrimPrefix(s, "\x00"))
		default:
			parts = []string{s}
		}
	}
	return strings.Join(parts, ".")
}

// --------------------------------------------------------------------------
// positive control on the walk
// --------------------------------------------------------------------------

// TestTheTemplateWalkFindsAPlantedEnvironmentValue proves the reader can see
// each shape the real templates use before it is trusted to report on them.
//
// The FALLBACK case is the one BF12-4 exists for: an expression naming two
// settings has to produce two paths. Before the fix it produced one, and an
// address hidden in the second operand rendered into the Pod with this whole
// package green.
func TestTheTemplateWalkFindsAPlantedEnvironmentValue(t *testing.T) {
	planted := strings.Join([]string{
		`{{- $boot := .Values.bootstrapAdmin | default dict }}`,
		`          env:`,
		`            - name: PLAIN`,
		`              value: "a constant"`,
		`            - name: ABSOLUTE`,
		`              value: {{ .Values.ai.ollama.url | quote }}`,
		`            {{- with .Values.connection }}`,
		`            {{- with .git }}`,
		`            {{- if .repoURL }}`,
		`            - name: NESTED`,
		`              value: {{ .repoURL | quote }}`,
		`            {{- end }}`,
		`            {{- end }}`,
		`            {{- end }}`,
		`            - name: AFTER_THE_SCOPES_CLOSED`,
		`              value: {{ .Values.config.environments | quote }}`,
		`            - name: FROM_A_VARIABLE`,
		`              value: {{ $boot.writeInitialSecret | quote }}`,
		`            {{- /* value: {{ .Values.ignored.because.this.is.a.comment }} */}}`,
		`            - name: FROM_A_HELPER`,
		`              value: {{ include "sharko.fullname" . | quote }}`,
		`            - name: A_FALLBACK`,
		`              value: {{ default .Values.ai.baseURL .Values.aiFallbackURL | quote }}`,
		`            - name: A_FALLBACK_ONTO_A_COMPUTED_LOCAL`,
		`              value: {{ coalesce .Values.connection.git.repoURL $computed | quote }}`,
		`            {{- with .Values.extraEnv }}`,
		`            {{- toYaml . | nindent 12 }}`,
		`            {{- end }}`,
	}, "\n")

	got, splats := walkOneTemplate("planted.yaml", planted)
	var lines []string
	for _, v := range got {
		mark := ""
		if v.untraceable {
			mark = " [untraceable]"
		}
		lines = append(lines, strings.Join(v.paths, "+")+" <- "+v.expr+mark)
	}
	want := []string{
		`ai.ollama.url <- {{ .Values.ai.ollama.url | quote }}`,
		`connection.git.repoURL <- {{ .repoURL | quote }}`,
		`config.environments <- {{ .Values.config.environments | quote }}`,
		`bootstrapAdmin.writeInitialSecret <- {{ $boot.writeInitialSecret | quote }}`,
		` <- {{ include "sharko.fullname" . | quote }} [untraceable]`,
		`ai.baseURL+aiFallbackURL <- {{ default .Values.ai.baseURL .Values.aiFallbackURL | quote }}`,
		`connection.git.repoURL <- {{ coalesce .Values.connection.git.repoURL $computed | quote }} [untraceable]`,
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("the template walk does not read the shapes the chart is written in.\nwanted:\n  %s\ngot:\n  %s",
			strings.Join(want, "\n  "), strings.Join(lines, "\n  "))
	}

	// And the splat reader, on the same planted text.
	var splatLines []string
	for _, s := range splats {
		splatLines = append(splatLines, s.path+" <- "+s.expr)
	}
	wantSplats := []string{`extraEnv <- {{- toYaml . | nindent 12 }}`}
	if strings.Join(splatLines, "\n") != strings.Join(wantSplats, "\n") {
		t.Fatalf("the walk does not see a whole environment block poured in with toYaml.\nwanted:\n  %s\ngot:\n  %s",
			strings.Join(wantSplats, "\n  "), strings.Join(splatLines, "\n  "))
	}
}

// TestTheWalkResolvesEveryOperandNotJustTheFirst is the guard on the guard.
//
// It is separate from the control above because it is the precise defect
// BF12-4 was opened for, and it must fail on its own if the walk ever goes
// back to reading one operand.
func TestTheWalkResolvesEveryOperandNotJustTheFirst(t *testing.T) {
	cases := []struct {
		expr string
		want []string
	}{
		{`{{ default .Values.ai.baseURL .Values.aiFallbackURL | quote }}`,
			[]string{"ai.baseURL", "aiFallbackURL"}},
		{`{{ coalesce .Values.a .Values.b .Values.c | quote }}`,
			[]string{"a", "b", "c"}},
		{`{{ if .Values.x }}{{ .Values.y }}{{ else }}{{ .Values.z }}{{ end }}`,
			[]string{"x", "y", "z"}},
		{`{{ printf "%s/%s" .Values.connection.git.repoURL .Values.ai.baseURL | quote }}`,
			[]string{"connection.git.repoURL", "ai.baseURL"}},
	}
	for _, c := range cases {
		got, _ := resolvePaths(c.expr, nil, map[string]string{})
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s\n  resolved to %v, wanted %v — an operand that is not resolved is an address "+
				"nothing in this file can see", c.expr, got, c.want)
		}
	}
	if len(cases) == 0 {
		t.Fatal("no expression was resolved, so this guard read nothing")
	}
}

// --------------------------------------------------------------------------
// the coverage rule
// --------------------------------------------------------------------------

// TestEveryOperatorSetEnvironmentValueIsClassified is the guard that makes the
// address list impossible to leave stale.
func TestEveryOperatorSetEnvironmentValueIsClassified(t *testing.T) {
	found, _ := walkTemplatesForEnvValues(t)

	seenPaths := map[string]bool{}
	seenExprs := map[string]bool{}
	where := map[string]string{}
	for _, v := range found {
		at := v.file + ":" + itoa(v.line)
		if v.untraceable {
			seenExprs[v.expr] = true
			where[v.expr] = at
		}
		for _, p := range v.paths {
			seenPaths[p] = true
			where[p] = at
		}
	}
	if len(seenPaths) == 0 {
		t.Fatal("the walk traced no values path at all, so the classification below is over nothing")
	}

	classified := map[string]bool{}
	for _, p := range append(append([]string{}, validatedAddresses...), notAddresses...) {
		classified[p] = true
	}

	// Fails on growth: a values path the chart writes that nobody has said is
	// or is not an address.
	var unclassified []string
	for p := range seenPaths {
		if !classified[p] {
			unclassified = append(unclassified, p+" at "+where[p])
		}
	}
	if len(unclassified) != 0 {
		sort.Strings(unclassified)
		t.Errorf("the chart writes these operator-set values into the Pod and nothing says whether they "+
			"are addresses:\n  %s\nAdd each one to validatedAddresses (and to sharko.validateAddresses in "+
			"_helpers.tpl) or to notAddresses.", strings.Join(unclassified, "\n  "))
	}

	// Fails on a stale entry: something classified here that the chart no
	// longer writes. A list nobody prunes is a list nobody trusts.
	var stale []string
	for p := range classified {
		if !seenPaths[p] {
			stale = append(stale, p)
		}
	}
	if len(stale) != 0 {
		sort.Strings(stale)
		t.Errorf("these values are classified here but the chart no longer writes them into the Pod:\n  %s",
			strings.Join(stale, "\n  "))
	}

	// The same rule for the expressions the walk cannot trace.
	var gotExprs []string
	for e := range seenExprs {
		gotExprs = append(gotExprs, e)
	}
	sort.Strings(gotExprs)
	wantExprs := append([]string{}, unresolvedValueExpressions...)
	sort.Strings(wantExprs)
	if strings.Join(gotExprs, "\n") != strings.Join(wantExprs, "\n") {
		t.Errorf("the set of environment values the walk cannot trace to a setting has changed.\n"+
			"written down here:\n  %s\nfound in the chart:\n  %s\n"+
			"Each of these reaches a value through a named template or a computed variable, so the walk "+
			"cannot tell whether it is an address. Look at the new one and decide.",
			strings.Join(wantExprs, "\n  "), strings.Join(gotExprs, "\n  "))
	}
}

// TestEveryEnvironmentBlockSplattedIntoThePodIsAccountedFor covers the shape
// the value-by-value walk cannot read at all.
//
// `{{- toYaml .Values.extraEnv | nindent 12 }}` pours a whole list of
// name/value pairs into the container's environment. There is no `value:` line
// for the walk to find and no field name for the address rule to check, so the
// headline "every operator-set environment value is classified" was not
// literally true — extraEnv was in neither list and nothing said so.
//
// This does not close the hole: extraEnv is a deliberate escape hatch and an
// operator who puts a credential in it gets a credential in the Pod. What it
// does is make the hole exactly one entry wide and impossible to widen in
// silence.
func TestEveryEnvironmentBlockSplattedIntoThePodIsAccountedFor(t *testing.T) {
	_, splats := walkTemplatesForEnvValues(t)

	var got []string
	where := map[string]string{}
	for _, s := range splats {
		if s.path == "" {
			t.Errorf("%s:%d pours a block into a container's environment from an expression the walk "+
				"cannot trace to a setting: %s", s.file, s.line, s.expr)
			continue
		}
		got = append(got, s.path)
		where[s.path] = s.file + ":" + itoa(s.line)
	}
	sort.Strings(got)
	want := append([]string{}, splattedEnvironmentBlocks...)
	sort.Strings(want)

	if len(got) == 0 {
		t.Fatal("the walk found no environment block splatted into any container, but the chart has " +
			"one. The reader is not seeing what it thinks it is, so this guard proves nothing.")
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("the set of whole environment blocks poured into a container has changed.\n"+
			"written down here: %v\nfound in the chart: %v\n"+
			"A block like this carries operator-supplied name/value pairs that no per-setting rule can "+
			"check. Decide whether the new one is acceptable, then add it here.", want, got)
	}
	for _, p := range want {
		if where[p] == "" {
			continue
		}
		t.Logf("%s is splatted at %s", p, where[p])
	}
}

// TestTheRuleChecksExactlyTheAddressesClaimedHere reads the includes out of
// _helpers.tpl, so validatedAddresses cannot drift away from what the chart
// actually checks.
func TestTheRuleChecksExactlyTheAddressesClaimedHere(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "charts", "sharko", "templates", "_helpers.tpl"))
	if err != nil {
		t.Fatalf("reading _helpers.tpl: %v", err)
	}
	calls := regexp.MustCompile(`sharko\.requireCredentialFreeAddress"\s*\(list\s*"([^"]+)"`).
		FindAllStringSubmatch(string(body), -1)
	if len(calls) == 0 {
		t.Fatal("sharko.validateAddresses checks nothing at all")
	}
	var checked []string
	for _, m := range calls {
		checked = append(checked, m[1])
	}
	sort.Strings(checked)
	want := append([]string{}, validatedAddresses...)
	sort.Strings(want)
	if strings.Join(checked, ",") != strings.Join(want, ",") {
		t.Errorf("the chart checks %v; this file says it checks %v", checked, want)
	}
}

// TestTheAddressRuleIsActuallyInvoked is the half the check above cannot do.
//
// Reading the definition of sharko.validateAddresses says which addresses it
// WOULD check. It says nothing about whether anything calls it. Taking the one
// `{{- include "sharko.validateAddresses" . }}` line out of the Deployment
// leaves every list in this file agreeing with every other list, helm renders
// happily, and not one address is checked. That was measured, and the only
// thing that noticed was the behavioural half in bf11_addresses_test.go, one
// refused address at a time.
//
// So the invocation is pinned structurally too. The templates are WALKED, not
// listed, so the call can move to another template without this going stale —
// what it may not do is disappear.
func TestTheAddressRuleIsActuallyInvoked(t *testing.T) {
	dir, rel := chartTemplateFiles(t)
	call := regexp.MustCompile(`\binclude\s+"sharko\.validateAddresses"`)

	var callers []string
	read := 0
	for _, name := range rel {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("reading the chart template %s: %v", name, err)
		}
		read++
		// The partial that DEFINES the rule is not a caller of it.
		if strings.HasPrefix(filepath.Base(name), "_") {
			continue
		}
		if call.MatchString(string(body)) {
			callers = append(callers, filepath.ToSlash(name))
		}
	}
	if read == 0 {
		t.Fatal("no chart template was read, so this guard looked for the call in nothing")
	}
	if len(callers) == 0 {
		t.Fatal("no template that renders an object calls sharko.validateAddresses. The rule is " +
			"written down and nothing runs it, so every address an operator sets goes into the Pod " +
			"unchecked while every list in this file still agrees with every other one.")
	}
	sort.Strings(callers)
	t.Logf("sharko.validateAddresses is called from %s", strings.Join(callers, ", "))
}
