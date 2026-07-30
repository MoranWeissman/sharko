// Package enginerender holds a render-level regression test for the Sharko
// v4 "engine" Helm chart (charts/sharko-engine), which replaces
// templates/bootstrap/ as the thing that turns a user's GitOps repo into
// ArgoCD ApplicationSets — see docs/design/2026-07-30-v4-data-file-format.md.
//
// The fixture at testdata/ is the design doc's own worked example (section
// 6): two clusters, prod-eu and staging-us; cert-manager pinned older on
// prod-eu with a webhook ignore-diff quirk; metrics-server everywhere on
// the catalog default. testdata/engine-values.yaml plus
// testdata/catalog/addons.yaml are exactly the two Helm values sources the
// real engine pin (engine/application.yaml) would pass at render time
// (design doc sections 2.5 / 4.7): repo/host/project plumbing and shipped
// catalog defaults, then the user's own catalog/addons.yaml delta.
//
// What this test proves, and what it does not (design intentionally, per
// the story brief — there is no live ArgoCD available here):
//
//   - PROVEN by `helm template`: the chart renders one AppProject and one
//     ApplicationSet per enabled addon, with the version pin, values
//     layering paths, settings pass-through, and preserveResourcesOnDeletion
//     default all wired correctly — including the exact `dig`/`hasKey`
//     Go-template calls (with the correct addon name and the correct
//     Helm-baked fleet-wide default) that the ArgoCD ApplicationSet
//     controller evaluates per cluster at round two.
//   - NOT proven here: that a live ArgoCD controller actually resolves
//     those round-two `dig` calls to 1.12.0 on prod-eu and 1.14.5 on
//     staging-us. That requires a running ApplicationSet controller
//     (Sprig included) reading testdata/clusters/*.yaml over git — out of
//     reach for a Go unit test, and explicitly deferred to the live
//     playground per the story brief. testdata/clusters/*.yaml is
//     asserted directly instead (TestEngineChartFixtureClusterAssignments)
//     to prove the DATA side matches the worked example exactly, so the
//     only unverified step is ArgoCD's own (already-documented) Sprig
//     evaluation.
package enginerender

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/MoranWeissman/sharko/internal/catalog"
)

// repoRoot resolves the repository root from this test file's location so
// the test works regardless of the working directory `go test` is invoked
// from — mirrors tests/bootstraprender/render_test.go.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// thisFile = <root>/tests/enginerender/render_test.go
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
}

// fixtureAddons is the small, controlled addon set every fixture-based test
// in this file (renderEngineChart) expects to see — exactly the two addons
// testdata/engine-values.yaml and testdata/catalog/addons.yaml describe, and
// the design doc's own worked example (section 6).
var fixtureAddons = map[string]bool{
	"cert-manager":   true,
	"metrics-server": true,
}

// nullOutNonFixtureCuratedAddons writes a temp Helm values file that sets
// `curated.addons.<name>: null` for every REAL catalog entry (from
// catalog.Load(), i.e. catalog/addons.yaml as regenerated into
// charts/sharko-engine/values.yaml by cmd/gen-engine-curated) that is not in
// fixtureAddons.
//
// Why this exists: before cmd/gen-engine-curated existed, the chart's own
// default curated.addons was `{}`, so passing --values engine-values.yaml
// (2 addons) gave a clean 2-addon world for free. Now the chart ships all
// real curated addons by default, and `helm template`/`helm install` deep-
// merge nested maps across --values sources — a later file that only sets
// 2 of the 45 curated.addons keys does NOT replace the other 43, it adds to
// them. Verified empirically: a null literal at a specific nested map key
// (via a values FILE, not just --set) DOES delete that one key from the
// merged result without touching its siblings — that is the documented Helm
// "null removes a value" mechanism, and it is the only reliable way to get
// back to an isolated small-world fixture for these tests.
//
// This keeps the design's real, intentional behavior (every install ships
// the full curated catalog — TestEngineChartRendersOneApplicationSetPerShippedAddon
// covers that) while letting the ORIGINAL fixture-based tests keep testing
// exactly the 2-addon scenario they were written for.
func nullOutNonFixtureCuratedAddons(t *testing.T) string {
	t.Helper()
	cat, err := catalog.Load()
	if err != nil {
		t.Fatalf("loading catalog/addons.yaml: %v", err)
	}

	var b strings.Builder
	b.WriteString("curated:\n  addons:\n")
	for _, e := range cat.Entries() {
		if fixtureAddons[e.Name] {
			continue
		}
		fmt.Fprintf(&b, "    %s: null\n", e.Name)
	}

	path := filepath.Join(t.TempDir(), "null-non-fixture-curated.yaml")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("writing temp null-out values file: %v", err)
	}
	return path
}

// renderEngineChart runs `helm template` against charts/sharko-engine with
// the fixture engine config and the fixture catalog delta — the same two
// values sources the real engine pin passes — after nulling out every
// shipped curated addon outside the small fixture set (see
// nullOutNonFixtureCuratedAddons). Skips (not fails) if helm is not
// installed, matching tests/bootstraprender's convention: the helm-validate
// CI job is the hard guard when helm is unavailable locally.
func renderEngineChart(t *testing.T) string {
	t.Helper()
	helmBin, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm not installed; skipping engine render test (CI helm-validate job is the hard guard)")
	}

	root := repoRoot(t)
	chartDir := filepath.Join(root, "charts", "sharko-engine")
	dataDir := filepath.Join(root, "tests", "enginerender", "testdata")
	nullFile := nullOutNonFixtureCuratedAddons(t)

	cmd := exec.Command(helmBin, "template", "testengine", chartDir,
		"--values", nullFile,
		"--values", filepath.Join(dataDir, "engine-values.yaml"),
		"--values", filepath.Join(dataDir, "catalog", "addons.yaml"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helm template failed: %v\n%s", err, out)
	}
	return string(out)
}

// renderEngineChartWithExtra layers one additional --values file (highest
// precedence — Helm's own last-wins ordering) on top of renderEngineChart's
// usual three sources. Used by tests that need one addon's fleet-wide
// settings to carry a specific quirk (e.g. a syncOptions list that already
// contains CreateNamespace=true) without perturbing every other render
// test's shared fixture.
func renderEngineChartWithExtra(t *testing.T, extraValuesYAML string) string {
	t.Helper()
	helmBin, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm not installed; skipping engine render test (CI helm-validate job is the hard guard)")
	}

	root := repoRoot(t)
	chartDir := filepath.Join(root, "charts", "sharko-engine")
	dataDir := filepath.Join(root, "tests", "enginerender", "testdata")
	nullFile := nullOutNonFixtureCuratedAddons(t)

	extraFile := filepath.Join(t.TempDir(), "extra-values.yaml")
	if err := os.WriteFile(extraFile, []byte(extraValuesYAML), 0o644); err != nil {
		t.Fatalf("writing temp extra values file: %v", err)
	}

	cmd := exec.Command(helmBin, "template", "testengine", chartDir,
		"--values", nullFile,
		"--values", filepath.Join(dataDir, "engine-values.yaml"),
		"--values", filepath.Join(dataDir, "catalog", "addons.yaml"),
		"--values", extraFile,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helm template failed: %v\n%s", err, out)
	}
	return string(out)
}

// extractApplicationSetDoc returns the single rendered YAML document (one
// `helm template` document between `---` separators) for the
// ApplicationSet named "sharko-<addon>", so assertions can be scoped to
// one addon's templatePatch instead of matching across every addon in the
// render.
func extractApplicationSetDoc(t *testing.T, rendered, name string) string {
	t.Helper()
	for _, doc := range strings.Split(rendered, "\n---\n") {
		if strings.Contains(doc, "kind: ApplicationSet") && strings.Contains(doc, "name: '"+name+"'") {
			return doc
		}
	}
	t.Fatalf("could not find ApplicationSet %q in rendered output", name)
	return ""
}

// extractBetween returns the substring of s strictly between the first
// occurrence of start and the following occurrence of end (both markers
// excluded). Used to scope assertions to one templatePatch branch.
func extractBetween(t *testing.T, s, start, end string) string {
	t.Helper()
	i := strings.Index(s, start)
	if i < 0 {
		t.Fatalf("marker %q not found in:\n%s", start, s)
	}
	rest := s[i+len(start):]
	j := strings.Index(rest, end)
	if j < 0 {
		t.Fatalf("marker %q not found after start marker in:\n%s", end, s)
	}
	return rest[:j]
}

// TestEngineChartRendersProjectAndApplicationSets asserts the chart emits
// exactly the shared AppProject (design decision D17 — one project, not one
// per addon) plus one ApplicationSet per enabled addon, named
// "sharko-<addon>".
func TestEngineChartRendersProjectAndApplicationSets(t *testing.T) {
	rendered := renderEngineChart(t)

	if got := strings.Count(rendered, "kind: AppProject"); got != 1 {
		t.Errorf("expected exactly 1 AppProject (design decision D17: one shared project, not one per addon), found %d.\n--- rendered ---\n%s", got, rendered)
	}
	if !regexp.MustCompile(`(?m)^\s+name: sharko-addons$`).MatchString(rendered) {
		t.Errorf("missing the shared AppProject named sharko-addons.\n--- rendered ---\n%s", rendered)
	}

	if got := strings.Count(rendered, "kind: ApplicationSet"); got != 2 {
		t.Errorf("expected exactly 2 ApplicationSets (cert-manager, metrics-server), found %d.\n--- rendered ---\n%s", got, rendered)
	}
	for _, name := range []string{"sharko-cert-manager", "sharko-metrics-server"} {
		needle := "name: '" + name + "'"
		if !strings.Contains(rendered, needle) {
			t.Errorf("missing ApplicationSet %q.\n--- rendered ---\n%s", needle, rendered)
		}
	}
}

// TestEngineChartVersionPinBakedDefaults asserts the version-pin dig call
// baked into targetRevision names the right addon and carries the right
// fleet-wide default — the default Helm bakes in from the merged catalog
// (design doc section 4.4), which the ApplicationSet controller falls back
// to when a cluster's assignment file has no per-cluster override.
func TestEngineChartVersionPinBakedDefaults(t *testing.T) {
	rendered := renderEngineChart(t)

	cases := map[string]string{
		"cert-manager":   "1.14.5", // catalog/addons.yaml fleet-wide delta
		"metrics-server": "3.12.1", // catalog/addons.yaml fleet-wide delta
	}
	for addon, version := range cases {
		want := `targetRevision: '{{ dig "addons" "` + addon + `" "version" "` + version + `" (.spec | default dict) }}'`
		if !strings.Contains(rendered, want) {
			t.Errorf("missing exact version-pin dig call for %s.\nwant substring: %s\n--- rendered ---\n%s", addon, want, rendered)
		}
	}
}

// TestEngineChartValuesLayeringPaths asserts the values layering paths
// match design doc section 4.3 exactly: chart defaults (implicit, Helm's
// own baseline), then the global values file, then the per-cluster file —
// via Helm's own valueFiles ordering, no merge code of Sharko's own.
func TestEngineChartValuesLayeringPaths(t *testing.T) {
	rendered := renderEngineChart(t)

	for _, addon := range []string{"cert-manager", "metrics-server"} {
		global := "- $values/values/global/" + addon + ".yaml"
		cluster := "- $values/values/clusters/{{ .name }}/" + addon + ".yaml"
		if !strings.Contains(rendered, global) {
			t.Errorf("missing global values file entry for %s: %q\n--- rendered ---\n%s", addon, global, rendered)
		}
		if !strings.Contains(rendered, cluster) {
			t.Errorf("missing per-cluster values file entry for %s: %q\n--- rendered ---\n%s", addon, cluster, rendered)
		}
		if !strings.Contains(rendered, "ignoreMissingValueFiles: true") {
			t.Errorf("missing ignoreMissingValueFiles: true — required so absent values files are normal, not errors (design doc section 4.3)")
		}
	}
}

// TestEngineChartPreserveResourcesOnDeletionDefaultTrue asserts every
// ApplicationSet carries syncPolicy.preserveResourcesOnDeletion: true by
// default (design doc section 3.2/2.5 — the deletion-safe default the PRD
// requires; removing an ApplicationSet must never cascade into deleted
// workloads).
func TestEngineChartPreserveResourcesOnDeletionDefaultTrue(t *testing.T) {
	rendered := renderEngineChart(t)

	got := strings.Count(rendered, "preserveResourcesOnDeletion: true")
	if got != 2 {
		t.Errorf("expected preserveResourcesOnDeletion: true exactly twice (once per ApplicationSet), found %d.\n--- rendered ---\n%s", got, rendered)
	}
}

// TestEngineChartClusterIdentityOnlyNameAndServer pins the hard rule from
// design doc section 4.4 (decision D8): inside a generated ApplicationSet,
// the only cluster-identity fields the engine may reference are `.name` and
// `.server` — never `.metadata`. Round-two `metadata` is a merge artifact of
// both matrix arms (the clusters arm wins any key the two share, and the
// matrix generator deep-merges nested maps), so it renders as a hybrid of
// the assignment file's envelope name and the real cluster secret's
// labels/annotations — never a clean handoff to either side, and never
// something the engine chart source should read for anything. The guard
// below matches ANY `.metadata` read (not just `.metadata.labels`, the
// v3-era access pattern) so a future template can't reintroduce the same
// class of mistake through a different subfield.
func TestEngineChartClusterIdentityOnlyNameAndServer(t *testing.T) {
	root := repoRoot(t)
	templatesDir := filepath.Join(root, "charts", "sharko-engine", "templates")
	entries, err := os.ReadDir(templatesDir)
	if err != nil {
		t.Fatalf("failed to read templates dir: %v", err)
	}

	forbidden := regexp.MustCompile(`\.metadata\b`)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(templatesDir, e.Name()))
		if err != nil {
			t.Fatalf("failed to read %s: %v", e.Name(), err)
		}
		if forbidden.Match(data) {
			t.Errorf("%s references .metadata for cluster identity — forbidden by design doc section 4.4 (decision D8). "+
				"Only .name and .server are safe cluster-identity fields inside a generated ApplicationSet.", e.Name())
		}
	}
}

// TestEngineChartZeroPerAddonConditionals asserts the chart's TEMPLATE
// SOURCE (not the rendered output — rendered output legitimately contains
// addon names, that is the whole point of a config-driven engine) contains
// no addon-name-specific text anywhere. Swap any addon name for any other
// and the chart's shape must be identical (design doc section 4.6 / 3.1 —
// "the engine has no `if addon == "cert-manager"` anywhere in it").
func TestEngineChartZeroPerAddonConditionals(t *testing.T) {
	root := repoRoot(t)
	templatesDir := filepath.Join(root, "charts", "sharko-engine", "templates")
	entries, err := os.ReadDir(templatesDir)
	if err != nil {
		t.Fatalf("failed to read templates dir: %v", err)
	}

	addonNames := []string{"cert-manager", "metrics-server"}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(templatesDir, e.Name()))
		if err != nil {
			t.Fatalf("failed to read %s: %v", e.Name(), err)
		}
		for _, addon := range addonNames {
			if strings.Contains(string(data), addon) {
				t.Errorf("%s hardcodes addon name %q — the engine chart source must have zero per-addon conditionals (design doc section 4.6)", e.Name(), addon)
			}
		}
	}
}

// TestEngineChartTemplatePatchSettingsPassthrough asserts the round-two
// templatePatch text carries the per-cluster override machinery for every
// v1 setting that cannot be a typed template string field (design doc
// section 4.5 / decision D14): prune, selfHeal, syncOptions,
// createNamespace (folded into syncOptions, since ArgoCD has no field of
// its own for it), and ignoreDifferences — for both addons, each with the
// correct addon name baked into its own `dig "addons" "<name>" ...` calls.
func TestEngineChartTemplatePatchSettingsPassthrough(t *testing.T) {
	rendered := renderEngineChart(t)

	for _, addon := range []string{"cert-manager", "metrics-server"} {
		settingsDig := `dig "addons" "` + addon + `" "settings" dict (.spec | default dict) | default dict`
		if !strings.Contains(rendered, settingsDig) {
			t.Errorf("missing per-cluster settings lookup for %s: %q\n--- rendered ---\n%s", addon, settingsDig, rendered)
		}
	}

	for _, needle := range []string{
		`dig "prune" true $s`,
		`dig "selfHeal" true $s`,
		`if hasKey $s "syncOptions"`,
		`else if hasKey $s "createNamespace"`,
		`if $s.createNamespace`,
		`if hasKey $s "ignoreDifferences"`,
		`toYaml $s.ignoreDifferences | nindent 8`,
	} {
		if !strings.Contains(rendered, needle) {
			t.Errorf("templatePatch is missing settings pass-through fragment: %q\n--- rendered ---\n%s", needle, rendered)
		}
	}
}

// TestEngineChartFixtureClusterAssignments asserts the fixture repo's
// clusters/*.yaml files match the design doc's worked example (section 6)
// exactly: prod-eu pins cert-manager older with the webhook ignore-diff
// quirk, staging-us has no override, both enable metrics-server on the
// catalog default. This is the DATA half of the version-pin proof — the
// engine template half is TestEngineChartVersionPinBakedDefaults and
// TestEngineChartTemplatePatchSettingsPassthrough above.
func TestEngineChartFixtureClusterAssignments(t *testing.T) {
	root := repoRoot(t)
	dataDir := filepath.Join(root, "tests", "enginerender", "testdata")

	type addonSpec struct {
		Enabled  bool                   `yaml:"enabled"`
		Version  string                 `yaml:"version"`
		Settings map[string]interface{} `yaml:"settings"`
	}
	type clusterAssignment struct {
		Spec struct {
			Cluster string               `yaml:"cluster"`
			Addons  map[string]addonSpec `yaml:"addons"`
		} `yaml:"spec"`
	}

	load := func(name string) clusterAssignment {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dataDir, "clusters", name+".yaml"))
		if err != nil {
			t.Fatalf("failed to read clusters/%s.yaml: %v", name, err)
		}
		var ca clusterAssignment
		if err := yaml.Unmarshal(data, &ca); err != nil {
			t.Fatalf("clusters/%s.yaml is not valid YAML: %v", name, err)
		}
		if ca.Spec.Cluster != name {
			t.Errorf("clusters/%s.yaml: spec.cluster = %q, want %q (design doc section 2.1 — file name must equal spec.cluster)", name, ca.Spec.Cluster, name)
		}
		return ca
	}

	prodEU := load("prod-eu")
	cm, ok := prodEU.Spec.Addons["cert-manager"]
	if !ok || !cm.Enabled {
		t.Fatalf("clusters/prod-eu.yaml: cert-manager must be enabled")
	}
	if cm.Version != "1.12.0" {
		t.Errorf("clusters/prod-eu.yaml: cert-manager version = %q, want %q (the worked example's per-cluster pin)", cm.Version, "1.12.0")
	}
	if cm.Settings == nil || cm.Settings["ignoreDifferences"] == nil {
		t.Errorf("clusters/prod-eu.yaml: cert-manager is missing the webhook ignoreDifferences quirk from the worked example")
	}
	ms, ok := prodEU.Spec.Addons["metrics-server"]
	if !ok || !ms.Enabled {
		t.Fatalf("clusters/prod-eu.yaml: metrics-server must be enabled")
	}
	if ms.Version != "" {
		t.Errorf("clusters/prod-eu.yaml: metrics-server must have no version override (follows the catalog default), got %q", ms.Version)
	}

	stagingUS := load("staging-us")
	cm2, ok := stagingUS.Spec.Addons["cert-manager"]
	if !ok || !cm2.Enabled {
		t.Fatalf("clusters/staging-us.yaml: cert-manager must be enabled")
	}
	if cm2.Version != "" {
		t.Errorf("clusters/staging-us.yaml: cert-manager must have no version override (follows the catalog default 1.14.5), got %q", cm2.Version)
	}
	if cm2.Settings != nil && cm2.Settings["ignoreDifferences"] != nil {
		t.Errorf("clusters/staging-us.yaml: cert-manager must NOT carry the prod-eu-only webhook quirk")
	}
	ms2, ok := stagingUS.Spec.Addons["metrics-server"]
	if !ok || !ms2.Enabled {
		t.Fatalf("clusters/staging-us.yaml: metrics-server must be enabled")
	}
}

// TestEngineChartFixtureValuesLayeringFiles asserts the fixture's
// values/global and values/clusters files exist and match the worked
// example (section 6) — proving the repo shape the engine's valueFiles
// list (design doc section 4.3) is written to consume.
func TestEngineChartFixtureValuesLayeringFiles(t *testing.T) {
	root := repoRoot(t)
	dataDir := filepath.Join(root, "tests", "enginerender", "testdata")

	global := filepath.Join(dataDir, "values", "global", "cert-manager.yaml")
	data, err := os.ReadFile(global)
	if err != nil {
		t.Fatalf("failed to read %s: %v", global, err)
	}
	var globalVals map[string]interface{}
	if err := yaml.Unmarshal(data, &globalVals); err != nil {
		t.Fatalf("%s is not valid YAML: %v", global, err)
	}
	if globalVals["installCRDs"] != true {
		t.Errorf("values/global/cert-manager.yaml: installCRDs = %v, want true", globalVals["installCRDs"])
	}

	perCluster := filepath.Join(dataDir, "values", "clusters", "prod-eu", "cert-manager.yaml")
	data, err = os.ReadFile(perCluster)
	if err != nil {
		t.Fatalf("failed to read %s: %v", perCluster, err)
	}
	var clusterVals map[string]interface{}
	if err := yaml.Unmarshal(data, &clusterVals); err != nil {
		t.Fatalf("%s is not valid YAML: %v", perCluster, err)
	}
	if replicaCount, ok := clusterVals["replicaCount"].(int); !ok || replicaCount != 3 {
		t.Errorf("values/clusters/prod-eu/cert-manager.yaml: replicaCount = %v, want 3", clusterVals["replicaCount"])
	}
}

// TestEngineChartRendersOneApplicationSetPerShippedAddon asserts a totally
// fresh install — chart defaults only, no user delta — renders one
// ApplicationSet per SHIPPED (curated) addon, plus the shared AppProject.
//
// This used to assert zero ApplicationSets, back when
// charts/sharko-engine/values.yaml's curated.addons block was still the
// placeholder `{}` (the flagged gap this test file's own generator,
// cmd/gen-engine-curated, now closes — see design doc section 4.7). Design
// doc section 4.7's merge formula is a UNION of curated.addons and
// spec.addons keys, not delta-only:
//
//	merged = mergeOverwrite(deepCopy(curated.addons), spec.addons)
//
// so every shipped addon gets an ApplicationSet even with an empty user
// delta. That ApplicationSet is inert at round two — its matrix generator's
// clusters arm selects zero clusters until one carries the matching
// addons.sharko.dev/<name>: enabled label — so this is the locked design's
// intentional behavior, not a bug. The expected count is read from
// catalog.Load() rather than hardcoded, so this test tracks the shipped
// catalog's real size instead of silently rotting as it grows.
//
// Design doc section 2.5's kill-Sharko promise ("Sharko itself, not this
// chart, is the thing a user deletes") still holds regardless of this
// count: every one of these ApplicationSets does nothing to a cluster
// unless that cluster's own assignment file switches the addon on.
func TestEngineChartRendersOneApplicationSetPerShippedAddon(t *testing.T) {
	helmBin, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm not installed; skipping engine render test (CI helm-validate job is the hard guard)")
	}

	cat, err := catalog.Load()
	if err != nil {
		t.Fatalf("loading catalog/addons.yaml: %v", err)
	}
	want := cat.Len()
	if want == 0 {
		t.Fatal("catalog/addons.yaml loaded zero entries — cannot assert a meaningful ApplicationSet count")
	}

	root := repoRoot(t)
	chartDir := filepath.Join(root, "charts", "sharko-engine")

	// Deliberately no --values at all: chart defaults now ship the
	// generated curated.addons block (cmd/gen-engine-curated) and an empty
	// spec.addons (values.yaml), so the merged catalog equals curated.addons.
	cmd := exec.Command(helmBin, "template", "testengine", chartDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helm template failed with chart defaults only: %v\n%s", err, out)
	}
	rendered := string(out)

	if got := strings.Count(rendered, "kind: ApplicationSet"); got != want {
		t.Errorf("expected %d ApplicationSets (one per shipped curated addon, catalog/addons.yaml has %d entries), got %d.\n--- rendered head ---\n%s", want, want, got, truncate(rendered, 2000))
	}
	if !strings.Contains(rendered, "kind: AppProject") {
		t.Errorf("expected the shared AppProject to still render with an empty user delta.\n--- rendered ---\n%s", rendered)
	}
}

// TestEngineChartCuratedNamespaceQuirkFlowsThroughWithoutOverride proves the
// flagged gap this story closes: charts/sharko-engine/values.yaml's
// curated.addons block (cmd/gen-engine-curated, generated from
// catalog/addons.yaml) actually reaches the rendered Application when the
// user's own delta doesn't repeat the field.
//
// sealed-secrets is a real "known quirk" pick, not a fabricated one:
// catalog/addons.yaml pins its default_namespace to kube-system, not
// "sealed-secrets" (the addon's own name) — the chart's namespace fallback
// is `settings.namespace | default (addon.namespace | default name)`, so
// without the curated default this would render "sealed-secrets", the
// wrong namespace. A user who writes only
// `sealed-secrets: {version: "..."}` in their own catalog/addons.yaml (no
// namespace override, matching design doc section 2.3 — "for an addon
// Sharko already ships, every field is optional") must still get
// kube-system.
func TestEngineChartCuratedNamespaceQuirkFlowsThroughWithoutOverride(t *testing.T) {
	helmBin, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm not installed; skipping engine render test (CI helm-validate job is the hard guard)")
	}

	root := repoRoot(t)
	chartDir := filepath.Join(root, "charts", "sharko-engine")

	// Only --set the user's delta (mimicking spec.addons from a real
	// catalog/addons.yaml). repo/hostCluster/project and curated.addons all
	// come from the chart's own real (generated) values.yaml — unlike
	// renderEngineChart's fixture helper, which overrides curated.addons
	// entirely via testdata/engine-values.yaml. sealed-secrets carries no
	// namespace override here, so the rendered destination must come from
	// the curated default alone.
	cmd := exec.Command(helmBin, "template", "testengine", chartDir,
		"--set", "spec.addons.sealed-secrets.version=2.17.4",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helm template failed: %v\n%s", err, out)
	}
	rendered := string(out)

	if !strings.Contains(rendered, "name: 'sharko-sealed-secrets'") {
		t.Fatalf("expected an ApplicationSet for sealed-secrets.\n--- rendered ---\n%s", truncate(rendered, 4000))
	}
	want := `namespace: '{{ dig "addons" "sealed-secrets" "settings" "namespace" "kube-system" (.spec | default dict) }}'`
	if !strings.Contains(rendered, want) {
		t.Errorf("sealed-secrets curated namespace default (kube-system, from catalog/addons.yaml's default_namespace) did not flow through the merge.\nwant substring: %s\n--- rendered ---\n%s", want, truncate(rendered, 4000))
	}
}

// truncate keeps failure output readable when the full 45-addon render
// would otherwise dump thousands of lines into a test failure message.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n... (truncated)"
}
