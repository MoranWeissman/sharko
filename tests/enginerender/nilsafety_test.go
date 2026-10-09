// Round-two nil-safety simulation for the FIX-3 review fix (v4 Wave 1 R2),
// updated for the flat cluster-addons/<name>.yaml shape (decision 9 of
// .bmad/output/architecture/2026-07-31-catalog-approved-model.md — no more
// `spec:` wrapper).
//
// charts/sharko-engine/templates/appset.yaml's targetRevision, namespace,
// and templatePatch fields all `dig` into `.` (the round-two data itself —
// the assignment file the git-files matrix arm reads from
// cluster-addons/<cluster>.yaml, merged with the clusters arm's own params) to
// find a per-cluster override. `helm template` never reads cluster-addons/*.yaml
// (see the package doc comment at the top of render_test.go and
// testdata/cluster-addons/prod-eu.yaml): that file is a round-two input the real
// ArgoCD ApplicationSet controller reads, so there is no way to exercise
// the nil-safety fix by rendering the chart alone.
//
// This file re-executes the EXACT round-two Go-template fragments the
// chart emits, using Go's stdlib text/template plus a hand-rolled funcmap
// for the three Sprig functions in play (dig, default, hasKey). The
// funcmap is deliberately kept panic-prone in the same place Sprig's own
// `dig` is (a naked `.(map[string]interface{})` type assertion) — a "safe"
// reimplementation that never panics would prove nothing about whether the
// `| default dict` guards in appset.yaml actually matter.
package enginerender

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"text/template"

	"gopkg.in/yaml.v3"
)

// simDig mirrors Masterminds/sprig's dig(keys..., default, dict) — same
// signature, same behavior, including the naked type assertion on the
// final (dict) argument that panics when that argument is not a
// map[string]interface{} (e.g. a nil interface — the pre-flatten era's
// missing/null `.spec` was the example; post-flatten, a present-but-null
// `settings:` key is the reachable equivalent, see
// TestEngineChartTemplatePatchNilSafetyRegressionGuard). The panic is
// recovered here and turned into a normal error so callers can assert on it
// without crashing the test binary — ArgoCD's own controller would
// similarly need to recover from (or error out of) this panic in a live
// cluster.
func simDig(ps ...interface{}) (result interface{}, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("dig panicked: %v", r)
		}
	}()
	if len(ps) < 3 {
		return nil, fmt.Errorf("dig requires at least three arguments")
	}
	dict := ps[len(ps)-1].(map[string]interface{}) // nolint:forcetypeassert // deliberately faithful to Sprig's own panic-prone assertion
	def := ps[len(ps)-2]
	ks := make([]string, len(ps)-2)
	for i := 0; i < len(ps)-2; i++ {
		ks[i] = ps[i].(string)
	}
	step := dict
	for i := 0; i < len(ks)-1; i++ {
		v, ok := step[ks[i]]
		if !ok {
			return def, nil
		}
		vv, ok := v.(map[string]interface{})
		if !ok {
			return def, nil
		}
		step = vv
	}
	v, ok := step[ks[len(ks)-1]]
	if !ok {
		return def, nil
	}
	return v, nil
}

// simEmpty mirrors Sprig's reflect-based empty() helper closely enough for
// this test's purposes: nil, zero, false, and empty string/slice/map all
// count as empty.
func simEmpty(v interface{}) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Array, reflect.Slice, reflect.Map, reflect.String:
		return rv.Len() == 0
	case reflect.Bool:
		return !rv.Bool()
	case reflect.Ptr, reflect.Interface:
		return rv.IsNil()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int() == 0
	}
	return false
}

// simDefault mirrors Sprig's default(d, given...) — called as `X | default
// d` in template source, which Go's template pipe syntax invokes as
// default(d, X).
func simDefault(d interface{}, given ...interface{}) interface{} {
	if len(given) == 0 || simEmpty(given[0]) {
		return d
	}
	return given[0]
}

// simHasKey mirrors Sprig's hasKey(dict, key).
func simHasKey(d map[string]interface{}, key string) (result bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("hasKey panicked: %v", r)
		}
	}()
	_, ok := d[key]
	return ok, nil
}

func simDictFn() map[string]interface{} { return map[string]interface{}{} }

// simWithout mirrors Sprig's without(list, omit...) — returns list with
// every element deep-equal to one of omit removed, preserving order.
// Needed to execute templatePatch's full "both createNamespace and
// syncOptions overridden" branch (Wave 2 ride-along w2-q6 item 5c), which
// nilsafety_test.go's narrower fragments never reached.
func simWithout(list interface{}, omit ...interface{}) []interface{} {
	items, _ := list.([]interface{})
	out := make([]interface{}, 0, len(items))
	for _, item := range items {
		skip := false
		for _, o := range omit {
			if reflect.DeepEqual(item, o) {
				skip = true
				break
			}
		}
		if !skip {
			out = append(out, item)
		}
	}
	return out
}

// simToYaml mirrors Sprig's toYaml(v) — marshals v to a YAML string with
// the trailing newline trimmed (toYaml's own documented behavior, since
// callers pipe it into nindent which adds its own leading newline).
func simToYaml(v interface{}) string {
	b, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Sprintf("error marshaling to YAML: %v", err)
	}
	return strings.TrimSuffix(string(b), "\n")
}

// simNindent mirrors Sprig's nindent(spaces, s) — indents every line of s
// by spaces and prefixes the whole result with a newline, matching how
// `{{ toYaml $x | nindent 8 }}` is meant to be dropped into an
// already-indented YAML context.
func simNindent(spaces int, s string) string {
	pad := strings.Repeat(" ", spaces)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = pad + l
	}
	return "\n" + strings.Join(lines, "\n")
}

var simFuncMap = template.FuncMap{
	"dig":     simDig,
	"default": simDefault,
	"hasKey":  simHasKey,
	"dict":    simDictFn,
	"without": simWithout,
	"toYaml":  simToYaml,
	"nindent": simNindent,
}

// execRoundTwo parses and executes a round-two Go-template fragment with
// `missingkey=zero` set — the same goTemplateOptions this chart's
// appset.yaml declares on every generated ApplicationSet — against data,
// returning the rendered text or the resulting error.
func execRoundTwo(t *testing.T, tmplSrc string, data map[string]interface{}) (string, error) {
	t.Helper()
	tmpl, err := template.New("round-two").Option("missingkey=zero").Funcs(simFuncMap).Parse(tmplSrc)
	if err != nil {
		t.Fatalf("template failed to parse: %v\nsource: %s", err, tmplSrc)
	}
	var sb strings.Builder
	err = tmpl.Execute(&sb, data)
	return sb.String(), err
}

// loadNilSafetyFixture reads and YAML-decodes a fixture file from
// testdata/nilsafety/ and returns its top-level keys — exactly what the
// git-files matrix arm contributes at round two (design doc section 4.1:
// the git generator's parameters ARE the parsed file's own fields). Returns
// nil for a fixture that parses to nothing (e.g. empty.yaml, a genuinely
// empty file) — the flat-format equivalent of the old "no spec: key"
// scenario.
func loadNilSafetyFixture(t *testing.T, name string) map[string]interface{} {
	t.Helper()
	root := repoRoot(t)
	path := filepath.Join(root, "tests", "enginerender", "testdata", "nilsafety", name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read fixture %s: %v", path, err)
	}
	var doc map[string]interface{}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("fixture %s is not valid YAML: %v", path, err)
	}
	return doc
}

// TestEngineChartTemplatePatchNilSafety proves the FIX-3 review fix, updated
// for the flat cluster-addons/<name>.yaml shape (decision 9): a per-cluster
// cluster-addons/<cluster>.yaml file with an addon's `settings:` present but
// null, or an entirely empty file (no `addons:` key at all — the flat
// model's equivalent of the old "no spec: key" scenario), must not crash
// the round-two dig chains the real ArgoCD ApplicationSet controller
// evaluates against `.` (targetRevision, namespace, and templatePatch's
// `$s` lookup — charts/sharko-engine/templates/appset.yaml).
func TestEngineChartTemplatePatchNilSafety(t *testing.T) {
	rendered := renderEngineChart(t)

	// Pin the guard text itself in the rendered chart output so a future
	// edit to appset.yaml can't silently drop it.
	for _, want := range []string{
		`targetRevision: '{{ dig "addons" "cert-manager" "version" "1.14.5" (. | default dict) }}'`,
		`namespace: '{{ dig "addons" "cert-manager" "settings" "namespace" "cert-manager" (. | default dict) }}'`,
		`$s := dig "addons" "cert-manager" "settings" dict (. | default dict) | default dict`,
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("missing nil-safety guard in rendered output.\nwant substring: %q\n--- rendered ---\n%s", want, truncate(rendered, 4000))
		}
	}

	const targetRevisionTmpl = `{{ dig "addons" "cert-manager" "version" "1.14.5" (. | default dict) }}`
	const namespaceTmpl = `{{ dig "addons" "cert-manager" "settings" "namespace" "cert-manager" (. | default dict) }}`
	const templatePatchTmpl = `{{- $s := dig "addons" "cert-manager" "settings" dict (. | default dict) | default dict -}}
prune={{ dig "prune" true $s }} selfHeal={{ dig "selfHeal" true $s }} hasSyncOptions={{ hasKey $s "syncOptions" }}`

	for _, fixture := range []string{"settings-null.yaml", "empty.yaml"} {
		t.Run(fixture, func(t *testing.T) {
			// The flat model's round-two `.` is the merge of both matrix
			// arms' own top-level fields (design doc section 4.1) — build
			// data the same way: the fixture's own top-level keys
			// (nil/empty for empty.yaml) plus the clusters arm's name/
			// server, which the clusters generator always contributes.
			doc := loadNilSafetyFixture(t, fixture)
			data := map[string]interface{}{}
			for k, v := range doc {
				data[k] = v
			}
			data["name"] = "sim-cluster"
			data["server"] = "https://example.invalid"

			if _, err := execRoundTwo(t, targetRevisionTmpl, data); err != nil {
				t.Errorf("targetRevision dig crashed on %s: %v", fixture, err)
			}
			if _, err := execRoundTwo(t, namespaceTmpl, data); err != nil {
				t.Errorf("namespace dig crashed on %s: %v", fixture, err)
			}
			out, err := execRoundTwo(t, templatePatchTmpl, data)
			if err != nil {
				t.Fatalf("templatePatch $s lookup crashed on %s: %v", fixture, err)
			}
			if !strings.Contains(out, "prune=true") || !strings.Contains(out, "selfHeal=true") || !strings.Contains(out, "hasSyncOptions=false") {
				t.Errorf("templatePatch did not fall back to fleet-wide defaults for %s, got: %q", fixture, out)
			}
		})
	}
}

// TestEngineChartTemplatePatchNilSafetyRegressionGuard proves the
// simulation harness above is actually meaningful for the reachable crash
// this file exists to prevent — i.e. that it WOULD catch a regression to
// the pre-fix unguarded template text, not just that the guarded text
// happens to pass. Runs the exact pre-fix (unguarded) fragment against the
// null-settings fixture and asserts it fails.
//
// Scoped to settings-null.yaml only (unlike the pre-flatten version of this
// test, which also exercised a "no spec: key" case): in the flat model,
// round-two `.` is always a concrete, non-nil map — Execute's root context
// is whatever this test harness passes it, never the untyped-nil interface
// value a MISSING map key under `missingkey=zero` used to produce for
// `.spec`. dig's own missing-intermediate-key handling (addons/cert-manager
// both absent from an empty.yaml-derived `.`) already degrades gracefully
// with no guard at all — see TestEngineChartTemplatePatchNilSafety's
// empty.yaml case above, which passes whether or not `(. | default dict)`
// is present. `(. | default dict)` is kept as defensive belt-and-braces
// (matching appset.yaml's own comment) for a genuinely-nil round-two
// context this harness cannot construct and real ArgoCD never produces
// either, since the clusters matrix arm always contributes at least
// name/server. The one REAL reachable crash in the flat model is the
// trailing `| default dict` after the full dig call, guarding an addon's
// `settings:` key being present but explicitly null — that is what this
// regression guard proves.
func TestEngineChartTemplatePatchNilSafetyRegressionGuard(t *testing.T) {
	// Mirrors the real pre-fix templatePatch shape: `dig "prune" true $s`
	// is what actually crashes for the settings-null.yaml fixture — $s
	// itself is a valid (non-nil) map lookup target for the outer dig, so
	// even `hasKey $s ...` below doesn't crash (reading a nil Go map is
	// safe; it is dig's OWN naked map type assertion on $s as its LAST
	// argument that panics).
	const unguardedTemplatePatchTmpl = `{{- $s := dig "addons" "cert-manager" "settings" dict (. | default dict) -}}
prune={{ dig "prune" true $s }} hasSyncOptions={{ hasKey $s "syncOptions" }}`

	doc := loadNilSafetyFixture(t, "settings-null.yaml")
	data := map[string]interface{}{}
	for k, v := range doc {
		data[k] = v
	}
	data["name"] = "sim-cluster"
	data["server"] = "https://example.invalid"

	if _, err := execRoundTwo(t, unguardedTemplatePatchTmpl, data); err == nil {
		t.Errorf("expected the pre-fix unguarded templatePatch template (missing the trailing `| default dict`) to fail on settings-null.yaml, but it succeeded — the simulation harness would not have caught this regression")
	}
}
