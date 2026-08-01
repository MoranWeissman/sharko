// Package enginerender holds a render-level regression test for the Sharko
// v4 "engine" Helm chart (charts/sharko-engine), which replaces
// templates/bootstrap/ as the thing that turns a user's GitOps repo into
// ArgoCD ApplicationSets — see docs/design/2026-07-30-v4-data-file-format.md
// for the render mechanics and
// .bmad/output/architecture/2026-07-31-catalog-approved-model.md for the
// "catalog = the approved list" model this file's fixtures follow.
//
// The fixture at testdata/ is the design doc's own worked example (section
// 6), updated for the approved-list model: two clusters, prod-eu and
// staging-us; cert-manager pinned older on prod-eu with a webhook ignore-diff
// quirk; metrics-server everywhere on the catalog default.
// testdata/engine-values.yaml plus testdata/catalog.yaml are exactly the two
// Helm values sources the real engine pin (sharko-engine.yaml) would pass at render
// time (design doc sections 2.5 / decision D6): repo/host/project plumbing,
// then the org's own catalog.yaml — now the ONLY source of addon
// definitions, since decision 6 removed the chart's baked curated block
// entirely.
//
// What this test proves, and what it does not (design intentionally, per
// the story brief — there is no live ArgoCD available here):
//
//   - PROVEN by `helm template`: the chart renders one AppProject and one
//     ApplicationSet per catalog.yaml addon, with the version pin, values
//     layering paths, settings pass-through, and preserveResourcesOnDeletion
//     default all wired correctly — including the exact `dig`/`hasKey`
//     Go-template calls (with the correct addon name and the correct
//     Helm-baked fleet-wide default) that the ArgoCD ApplicationSet
//     controller evaluates per cluster at round two.
//   - NOT proven here: that a live ArgoCD controller actually resolves
//     those round-two `dig` calls to 1.12.0 on prod-eu and 1.14.5 on
//     staging-us. That requires a running ApplicationSet controller
//     (Sprig included) reading testdata/cluster-addons/*.yaml over git — out of
//     reach for a Go unit test, and explicitly deferred to the live
//     playground per the story brief. testdata/cluster-addons/*.yaml is
//     asserted directly instead (TestEngineChartFixtureClusterAddons)
//     to prove the DATA side matches the worked example exactly, so the
//     only unverified step is ArgoCD's own (already-documented) Sprig
//     evaluation.
package enginerender

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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

// renderEngineChart runs `helm template` against charts/sharko-engine with
// the fixture engine config and the fixture org catalog — the same two
// values sources the real engine pin passes. Skips (not fails) if helm is
// not installed, matching tests/bootstraprender's convention: the
// helm-validate CI job is the hard guard when helm is unavailable locally.
func renderEngineChart(t *testing.T) string {
	t.Helper()
	helmBin, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm not installed; skipping engine render test (CI helm-validate job is the hard guard)")
	}

	root := repoRoot(t)
	chartDir := filepath.Join(root, "charts", "sharko-engine")
	dataDir := filepath.Join(root, "tests", "enginerender", "testdata")

	cmd := exec.Command(helmBin, "template", "testengine", chartDir,
		"--values", filepath.Join(dataDir, "engine-values.yaml"),
		"--values", filepath.Join(dataDir, "catalog.yaml"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helm template failed: %v\n%s", err, out)
	}
	return string(out)
}

// renderEngineChartWithExtra layers one additional --values file (highest
// precedence — Helm's own last-wins ordering) on top of renderEngineChart's
// usual two sources. Used by tests that need one addon's fleet-wide
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

	extraFile := filepath.Join(t.TempDir(), "extra-values.yaml")
	if err := os.WriteFile(extraFile, []byte(extraValuesYAML), 0o644); err != nil {
		t.Fatalf("writing temp extra values file: %v", err)
	}

	cmd := exec.Command(helmBin, "template", "testengine", chartDir,
		"--values", filepath.Join(dataDir, "engine-values.yaml"),
		"--values", filepath.Join(dataDir, "catalog.yaml"),
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
// per addon) plus one ApplicationSet per addon in catalog.yaml, named
// "sharko-<addon>".
func TestEngineChartRendersProjectAndApplicationSets(t *testing.T) {
	rendered := renderEngineChart(t)

	if got := strings.Count(rendered, "kind: AppProject"); got != 1 {
		t.Errorf("expected exactly 1 AppProject (design decision D17: one shared project, not one per addon), found %d.\n--- rendered ---\n%s", got, rendered)
	}
	if !regexp.MustCompile(`(?m)^\s+name: sharko-addons$`).MatchString(rendered) {
		t.Errorf("missing the shared AppProject named sharko-addons.\n--- rendered ---\n%s", rendered)
	}

	// 3 ApplicationSets: one per catalog addon (cert-manager, metrics-server)
	// plus the connectivity-check ApplicationSet, which renders on every
	// default render regardless of catalog content (chart 0.3.0 — see
	// TestEngineChartConnectivityCheckAppSetRendersByDefault below).
	if got := strings.Count(rendered, "kind: ApplicationSet"); got != 3 {
		t.Errorf("expected exactly 3 ApplicationSets (cert-manager, metrics-server, connectivity-check), found %d.\n--- rendered ---\n%s", got, rendered)
	}
	for _, name := range []string{"sharko-cert-manager", "sharko-metrics-server", "sharko-connectivity-check"} {
		needle := "name: '" + name + "'"
		if !strings.Contains(rendered, needle) && !strings.Contains(rendered, "name: "+name) {
			t.Errorf("missing ApplicationSet %q.\n--- rendered ---\n%s", needle, rendered)
		}
	}
}

// TestEngineChartVersionPinBakedDefaults asserts the version-pin dig call
// baked into targetRevision names the right addon and carries the right
// fleet-wide default — the default Helm bakes in from catalog.yaml (design
// doc section 4.4), which the ApplicationSet controller falls back to when a
// cluster's assignment file has no per-cluster override.
func TestEngineChartVersionPinBakedDefaults(t *testing.T) {
	rendered := renderEngineChart(t)

	cases := map[string]string{
		"cert-manager":   "1.14.5", // catalog.yaml
		"metrics-server": "3.12.1", // catalog.yaml
	}
	for addon, version := range cases {
		want := `targetRevision: '{{ dig "addons" "` + addon + `" "version" "` + version + `" (. | default dict) }}'`
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
		settingsDig := `dig "addons" "` + addon + `" "settings" dict (. | default dict) | default dict`
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

// TestEngineChartFixtureClusterAddons asserts the fixture repo's
// cluster-addons/*.yaml files match the design doc's worked example (section 6)
// exactly, in the flat shape (decision 9 — no `metadata:`/`spec:` wrapper):
// prod-eu pins cert-manager older with the webhook ignore-diff quirk,
// staging-us has no override, both enable metrics-server on the catalog
// default. This is the DATA half of the version-pin proof — the engine
// template half is TestEngineChartVersionPinBakedDefaults and
// TestEngineChartTemplatePatchSettingsPassthrough above.
func TestEngineChartFixtureClusterAddons(t *testing.T) {
	root := repoRoot(t)
	dataDir := filepath.Join(root, "tests", "enginerender", "testdata")

	type addonSpec struct {
		Enabled  bool                   `yaml:"enabled"`
		Version  string                 `yaml:"version"`
		Settings map[string]interface{} `yaml:"settings"`
	}
	type clusterAddons struct {
		Cluster string               `yaml:"cluster"`
		Addons  map[string]addonSpec `yaml:"addons"`
	}

	load := func(name string) clusterAddons {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dataDir, "cluster-addons", name+".yaml"))
		if err != nil {
			t.Fatalf("failed to read cluster-addons/%s.yaml: %v", name, err)
		}
		var ca clusterAddons
		if err := yaml.Unmarshal(data, &ca); err != nil {
			t.Fatalf("cluster-addons/%s.yaml is not valid YAML: %v", name, err)
		}
		if ca.Cluster != name {
			t.Errorf("cluster-addons/%s.yaml: cluster = %q, want %q (design doc section 2.1 — file name must equal the in-file cluster field)", name, ca.Cluster, name)
		}
		return ca
	}

	prodEU := load("prod-eu")
	cm, ok := prodEU.Addons["cert-manager"]
	if !ok || !cm.Enabled {
		t.Fatalf("cluster-addons/prod-eu.yaml: cert-manager must be enabled")
	}
	if cm.Version != "1.12.0" {
		t.Errorf("cluster-addons/prod-eu.yaml: cert-manager version = %q, want %q (the worked example's per-cluster pin)", cm.Version, "1.12.0")
	}
	if cm.Settings == nil || cm.Settings["ignoreDifferences"] == nil {
		t.Errorf("cluster-addons/prod-eu.yaml: cert-manager is missing the webhook ignoreDifferences quirk from the worked example")
	}
	ms, ok := prodEU.Addons["metrics-server"]
	if !ok || !ms.Enabled {
		t.Fatalf("cluster-addons/prod-eu.yaml: metrics-server must be enabled")
	}
	if ms.Version != "" {
		t.Errorf("cluster-addons/prod-eu.yaml: metrics-server must have no version override (follows the catalog default), got %q", ms.Version)
	}

	stagingUS := load("staging-us")
	cm2, ok := stagingUS.Addons["cert-manager"]
	if !ok || !cm2.Enabled {
		t.Fatalf("cluster-addons/staging-us.yaml: cert-manager must be enabled")
	}
	if cm2.Version != "" {
		t.Errorf("cluster-addons/staging-us.yaml: cert-manager must have no version override (follows the catalog default 1.14.5), got %q", cm2.Version)
	}
	if cm2.Settings != nil && cm2.Settings["ignoreDifferences"] != nil {
		t.Errorf("cluster-addons/staging-us.yaml: cert-manager must NOT carry the prod-eu-only webhook quirk")
	}
	ms2, ok := stagingUS.Addons["metrics-server"]
	if !ok || !ms2.Enabled {
		t.Fatalf("cluster-addons/staging-us.yaml: metrics-server must be enabled")
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

// TestEngineChartRendersZeroApplicationSetsWithEmptyCatalog asserts a
// totally fresh install — chart defaults only, no catalog.yaml passed at
// all — renders ZERO ADDON ApplicationSets, plus the shared AppProject.
// This is the "catalog = the approved list" model's day-zero promise
// (.bmad/output/architecture/2026-07-31-catalog-approved-model.md decisions
// 3 and 6): nothing runs in a fresh org's fleet until an addon is approved
// into catalog.yaml. The engine chart carries no baked curated defaults any
// more — values.yaml's own default is addons: {}, so chart-defaults-only is
// exactly the empty-catalog case.
//
// The invariant is scoped to ADDON ApplicationSets specifically (not "zero
// ApplicationSets, period") because chart 0.3.0 added a connectivity-check
// ApplicationSet (templates/connectivity-check.yaml) that renders on every
// default render regardless of catalog content — that one is the generator
// for zero-addon clusters' connectivity check, so it is SUPPOSED to be
// there even with an empty catalog; see
// TestEngineChartConnectivityCheckAppSetRendersByDefault.
func TestEngineChartRendersZeroApplicationSetsWithEmptyCatalog(t *testing.T) {
	helmBin, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm not installed; skipping engine render test (CI helm-validate job is the hard guard)")
	}

	root := repoRoot(t)
	chartDir := filepath.Join(root, "charts", "sharko-engine")

	// Deliberately no --values at all: chart defaults are repo/hostCluster/
	// project placeholders plus addons: {} (values.yaml) — the same shape an
	// org's real, empty catalog.yaml would pass.
	cmd := exec.Command(helmBin, "template", "testengine", chartDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helm template failed with chart defaults only: %v\n%s", err, out)
	}
	rendered := string(out)

	if got := strings.Count(rendered, "kind: ApplicationSet"); got != 1 {
		t.Errorf("expected exactly 1 ApplicationSet with an empty catalog (only the connectivity-check generator, zero addon ones), got %d.\n--- rendered ---\n%s", got, rendered)
	}
	if !strings.Contains(rendered, "name: sharko-connectivity-check") {
		t.Errorf("expected the connectivity-check ApplicationSet to still render with an empty catalog (it does not depend on the catalog at all).\n--- rendered ---\n%s", rendered)
	}
	for _, addonAppSet := range []string{"name: 'sharko-cert-manager'", "name: 'sharko-metrics-server'"} {
		if strings.Contains(rendered, addonAppSet) {
			t.Errorf("found addon ApplicationSet %q with an empty catalog — day-zero promise violated.\n--- rendered ---\n%s", addonAppSet, rendered)
		}
	}
	if !strings.Contains(rendered, "kind: AppProject") {
		t.Errorf("expected the shared AppProject to still render with an empty catalog.\n--- rendered ---\n%s", rendered)
	}
}

// TestEngineChartConnectivityCheckAppSetRendersByDefault asserts (chart
// 0.3.0, W1 walk-fix item 1) that a normal render — the fixture catalog,
// same as every other test in this file — always emits the
// connectivity-check ApplicationSet alongside the addon ApplicationSets, so
// a zero-addon cluster (the case this chart cannot see from a rendered
// fixture, since the fixture's clusters both enable addons) always has a
// generator watching for it. Also pins the exact self-referential source
// shape: repoURL from .Values.engineChart.repoURL, chart from .Chart.Name,
// targetRevision from .Chart.Version, and the connectivityCheckOnly=true
// Helm parameter that switches the generated Application into check-only
// mode.
func TestEngineChartConnectivityCheckAppSetRendersByDefault(t *testing.T) {
	rendered := renderEngineChart(t)

	if !strings.Contains(rendered, "kind: ApplicationSet") || !strings.Contains(rendered, "name: sharko-connectivity-check") {
		t.Fatalf("missing the connectivity-check ApplicationSet in a default render.\n--- rendered ---\n%s", rendered)
	}
	for _, needle := range []string{
		"sharko.dev/connectivity-check: enabled",
		"repoURL: ghcr.io/example-org/sharko-engine", // chart default (values.yaml) — testdata/engine-values.yaml does not override engineChart.repoURL
		"chart: sharko-engine",
		"targetRevision: 0.4.0",
		"name: connectivityCheckOnly",
		`value: "true"`,
		"namespace: sharko-connectivity",
	} {
		if !strings.Contains(rendered, needle) {
			t.Errorf("connectivity-check ApplicationSet is missing expected fragment %q.\n--- rendered ---\n%s", needle, rendered)
		}
	}
	// No separate AppProject for the check — it reuses the shared one.
	if got := strings.Count(rendered, "kind: AppProject"); got != 1 {
		t.Errorf("expected exactly 1 AppProject even with the connectivity-check ApplicationSet present (it reuses the shared project, no project of its own), found %d.\n--- rendered ---\n%s", got, rendered)
	}
}

// TestEngineChartConnectivityCheckOnlyRendersOnlyConfigMap asserts (chart
// 0.3.0, W1 walk-fix item 1) that setting connectivityCheckOnly=true — the
// value the generated per-cluster Application's own source carries —
// renders EXACTLY ONE resource, the harmless ConfigMap, and nothing else:
// no AppProject, no addon ApplicationSets, no connectivity-check
// ApplicationSet itself (the generator has no business re-running inside
// the single-cluster check render it produced).
func TestEngineChartConnectivityCheckOnlyRendersOnlyConfigMap(t *testing.T) {
	helmBin, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm not installed; skipping engine render test (CI helm-validate job is the hard guard)")
	}

	root := repoRoot(t)
	chartDir := filepath.Join(root, "charts", "sharko-engine")
	dataDir := filepath.Join(root, "tests", "enginerender", "testdata")

	// Same fixture values as every other render test PLUS the toggle — the
	// real generated Application carries the fleet's usual engine values
	// (it is the same chart/version) on top of the one flipped parameter.
	cmd := exec.Command(helmBin, "template", "testengine", chartDir,
		"--values", filepath.Join(dataDir, "engine-values.yaml"),
		"--values", filepath.Join(dataDir, "catalog.yaml"),
		"--set", "connectivityCheckOnly=true",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helm template failed: %v\n%s", err, out)
	}
	rendered := string(out)

	// The only three kinds this chart ever emits are AppProject,
	// ApplicationSet and ConfigMap (the check). Asserting exactly one
	// ConfigMap and zero of the other two is a complete proof that
	// connectivityCheckOnly=true renders "ONLY the ConfigMap" — a plain
	// document count is not used here because `helm template`'s combined
	// output can carry unrelated stderr warnings (e.g. a locally
	// group/world-readable kubeconfig) ahead of the first `---` separator.
	if got := strings.Count(rendered, "kind: ConfigMap"); got != 1 {
		t.Errorf("expected exactly 1 ConfigMap with connectivityCheckOnly=true, got %d.\n--- rendered ---\n%s", got, rendered)
	}
	if strings.Contains(rendered, "kind: AppProject") {
		t.Errorf("connectivityCheckOnly=true must not render the AppProject.\n--- rendered ---\n%s", rendered)
	}
	if strings.Contains(rendered, "kind: ApplicationSet") {
		t.Errorf("connectivityCheckOnly=true must not render any ApplicationSet.\n--- rendered ---\n%s", rendered)
	}
	if !strings.Contains(rendered, "namespace: sharko-connectivity") {
		t.Errorf("expected the ConfigMap in the sharko-connectivity namespace.\n--- rendered ---\n%s", rendered)
	}
}

// commentOnlyGlobalValuesStub and commentOnlyClusterValuesStub mirror
// (byte-for-byte, in spirit — kept in sync by hand since this test package
// cannot import internal/orchestrator's unexported generators) the scaffold
// content internal/orchestrator/v4paths.go's globalValuesStub /
// clusterValuesStub write for values/global/<addon>.yaml and
// values/clusters/<cluster>/<addon>.yaml (v4-walkfix W1 items 5 and 6):
// comment-only YAML, no keys at all.
const (
	commentOnlyGlobalValuesStub = "# Helm values for cert-manager, applied to every cluster that enables it.\n" +
		"# Cluster-specific overrides live in values/clusters/<cluster>/cert-manager.yaml.\n" +
		"# An empty file means the chart's own defaults.\n"
	commentOnlyClusterValuesStub = "# Helm values for cert-manager on prod-eu only. These override values/global/cert-manager.yaml.\n" +
		"# An empty file means no cluster-specific overrides.\n"
)

// TestEngineChartTreatsCommentOnlyValuesStubAsEmpty is the render-side half
// of v4-walkfix W1 items 5 and 6: AddToCatalog and EnableAddonV4 now
// scaffold values/global/<addon>.yaml and
// values/clusters/<cluster>/<addon>.yaml with a comment-only stub instead
// of skipping the file. The engine chart itself never reads those files'
// CONTENT — appset.yaml only ever emits the literal STRING path
// ($values/values/global/<addon>.yaml) for ArgoCD's own Helm invocation of
// the addon chart to read later — so what this test actually proves is the
// mechanical half that matters: `helm template` (the same binary, the same
// --values file-loading code path ArgoCD's Helm invocation shares) accepts
// a comment-only YAML document as an empty values layer without error, and
// merging it changes nothing about the render. That is the exact property
// the stubs rely on — an empty file means "no overrides", never a render
// failure.
func TestEngineChartTreatsCommentOnlyValuesStubAsEmpty(t *testing.T) {
	baseline := renderEngineChart(t)

	withStubsLayered := renderEngineChartWithExtra(t, commentOnlyGlobalValuesStub+"\n"+commentOnlyClusterValuesStub)

	if baseline != withStubsLayered {
		t.Errorf("layering a comment-only values stub on top of the fixture values changed the render — an empty/comment-only file must be a no-op.\n--- baseline ---\n%s\n--- with stub layered ---\n%s",
			truncate(baseline, 4000), truncate(withStubsLayered, 4000))
	}
}

// truncate keeps failure output readable when a full render would otherwise
// dump thousands of lines into a test failure message.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n... (truncated)"
}
