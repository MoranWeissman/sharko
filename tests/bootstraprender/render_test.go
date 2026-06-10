// Package bootstraprender holds a render-level regression test for the Sharko
// bootstrap Helm chart (templates/bootstrap).
//
// Why this exists (V2-cleanup-17):
//
// Sharko writes its addon catalog under the sharko.io/v1 envelope, so the addon
// list lives at spec.applicationsets. The bootstrap root Application loads that
// catalog as a Helm values file, so the list ends up at .Values.spec.applicationsets.
// The chart templates originally iterated .Values.applicationsets (top level),
// which is undefined under the envelope — so the chart rendered ZERO AppProjects
// and ZERO ApplicationSets, and NO addon ever deployed to any cluster.
//
// CI only ever rendered charts/sharko/, never templates/bootstrap/, so nothing
// caught it. This test renders templates/bootstrap against a realistic enveloped
// catalog (one addon: velero) plus the bootstrap-config and managed-clusters
// value files, and asserts the output contains a velero AppProject AND a velero
// ApplicationSet.
//
// Proof it guards the bug: against the OLD (unfixed) template this test FAILS
// (zero ApplicationSets rendered); against the fixed template it PASSES.
package bootstraprender

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

// repoRoot resolves the repository root from this test file's location so the
// test works regardless of the working directory `go test` is invoked from.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// thisFile = <root>/tests/bootstraprender/render_test.go
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
}

func TestBootstrapChartRendersAddonFromEnvelopedCatalog(t *testing.T) {
	helmBin, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm not installed; skipping bootstrap render test (CI helm-validate job is the hard guard)")
	}

	root := repoRoot(t)
	chartDir := filepath.Join(root, "templates", "bootstrap")
	dataDir := filepath.Join(root, "tests", "bootstraprender", "testdata")

	cmd := exec.Command(helmBin, "template", "testbootstrap", chartDir,
		"--values", filepath.Join(dataDir, "bootstrap-config.yaml"),
		"--values", filepath.Join(dataDir, "addons-catalog.yaml"),
		"--values", filepath.Join(dataDir, "managed-clusters.yaml"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helm template failed: %v\n%s", err, out)
	}
	rendered := string(out)

	// The enveloped fixture contains exactly one addon: velero. The chart must
	// emit a matching AppProject and ApplicationSet, both named "velero". A
	// substring check on "kind: ApplicationSet" alone is enough to catch the
	// wrong-path bug (it rendered zero), but we also assert the addon name to
	// guard against a future "renders an empty/unnamed set" regression.
	assertContains := func(needle string) {
		t.Helper()
		if !strings.Contains(rendered, needle) {
			t.Errorf("rendered bootstrap chart is missing %q.\n"+
				"This means the chart did not generate the addon resources — the V2-cleanup-17 bug "+
				"(template reading .Values.applicationsets instead of .Values.spec.applicationsets) "+
				"has regressed.\n--- rendered output ---\n%s", needle, rendered)
		}
	}

	assertContains("kind: AppProject")
	assertContains("kind: ApplicationSet")

	// Assert a velero AppProject and a velero ApplicationSet specifically.
	// metadata.name is indented under metadata: — match on "name: velero".
	veleroNames := regexp.MustCompile(`(?m)^\s+name: velero$`).FindAllString(rendered, -1)
	if len(veleroNames) < 2 {
		t.Errorf("expected at least 2 resources named 'velero' (AppProject + ApplicationSet), found %d.\n"+
			"--- rendered output ---\n%s", len(veleroNames), rendered)
	}
}

// TestBootstrapConnectivityCheckAlwaysRendered asserts that the bootstrap chart
// always emits the connectivity-check AppProject and ApplicationSet — even when
// the addon catalog is EMPTY (zero applicationsets). This is the never-empty-
// bootstrap guard: with an empty catalog addons-appset.yaml renders nothing, and
// ArgoCD's "auto-sync will wipe out all resources" guard fires. The check appset
// having no {{ if }} gate means the bootstrap Application always has at least one
// resource and the wipe-out guard never fires.
func TestBootstrapConnectivityCheckAlwaysRendered(t *testing.T) {
	helmBin, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm not installed; skipping bootstrap render test (CI helm-validate job is the hard guard)")
	}

	root := repoRoot(t)
	chartDir := filepath.Join(root, "templates", "bootstrap")
	dataDir := filepath.Join(root, "tests", "bootstraprender", "testdata")

	// Render with only bootstrap-config (no catalog → empty applicationsets list).
	cmd := exec.Command(helmBin, "template", "testbootstrap", chartDir,
		"--values", filepath.Join(dataDir, "bootstrap-config.yaml"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helm template failed with empty catalog: %v\n%s", err, out)
	}
	rendered := string(out)

	// The connectivity-check AppProject and ApplicationSet must always be present.
	if !strings.Contains(rendered, "name: connectivity-check") {
		t.Errorf("bootstrap chart with empty catalog is missing connectivity-check resources.\n"+
			"The connectivity-check AppProject/ApplicationSet must be unconditionally rendered "+
			"so the bootstrap Application never renders zero resources.\n"+
			"--- rendered output ---\n%s", rendered)
	}

	// The ApplicationSet must be present (not just the AppProject).
	if !strings.Contains(rendered, "kind: ApplicationSet") {
		t.Errorf("bootstrap chart with empty catalog is missing kind: ApplicationSet.\n"+
			"--- rendered output ---\n%s", rendered)
	}
}

// TestBootstrapConnectivityCheckSelector asserts the ApplicationSet generator
// carries exactly the two required matchLabels so only cluster Secrets that
// Sharko has explicitly marked for connectivity checking are selected.
func TestBootstrapConnectivityCheckSelector(t *testing.T) {
	helmBin, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm not installed; skipping bootstrap render test (CI helm-validate job is the hard guard)")
	}

	root := repoRoot(t)
	chartDir := filepath.Join(root, "templates", "bootstrap")
	dataDir := filepath.Join(root, "tests", "bootstraprender", "testdata")

	cmd := exec.Command(helmBin, "template", "testbootstrap", chartDir,
		"--values", filepath.Join(dataDir, "bootstrap-config.yaml"),
		"--values", filepath.Join(dataDir, "addons-catalog.yaml"),
		"--values", filepath.Join(dataDir, "managed-clusters.yaml"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helm template failed: %v\n%s", err, out)
	}
	rendered := string(out)

	// Both matchLabels must be present in the connectivity-check ApplicationSet.
	if !regexp.MustCompile(`(?m)^\s+argocd\.argoproj\.io/secret-type:\s+cluster\s*$`).MatchString(rendered) {
		t.Errorf("connectivity-check ApplicationSet is missing required matchLabel argocd.argoproj.io/secret-type: cluster\n"+
			"--- rendered output ---\n%s", rendered)
	}
	if !regexp.MustCompile(`(?m)^\s+sharko\.io/connectivity-check:\s+enabled\s*$`).MatchString(rendered) {
		t.Errorf("connectivity-check ApplicationSet is missing required matchLabel sharko.io/connectivity-check: enabled\n"+
			"--- rendered output ---\n%s", rendered)
	}
}

// TestBootstrapConnectivityCheckSourcePath asserts the ApplicationSet template's
// source path is exactly "configuration/connectivity-check" — the directory where
// Sharko's init seeds the check ConfigMap manifest.
func TestBootstrapConnectivityCheckSourcePath(t *testing.T) {
	helmBin, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm not installed; skipping bootstrap render test (CI helm-validate job is the hard guard)")
	}

	root := repoRoot(t)
	chartDir := filepath.Join(root, "templates", "bootstrap")
	dataDir := filepath.Join(root, "tests", "bootstraprender", "testdata")

	cmd := exec.Command(helmBin, "template", "testbootstrap", chartDir,
		"--values", filepath.Join(dataDir, "bootstrap-config.yaml"),
		"--values", filepath.Join(dataDir, "addons-catalog.yaml"),
		"--values", filepath.Join(dataDir, "managed-clusters.yaml"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helm template failed: %v\n%s", err, out)
	}
	rendered := string(out)

	if !strings.Contains(rendered, "path: configuration/connectivity-check") {
		t.Errorf("connectivity-check ApplicationSet source path is not 'configuration/connectivity-check'.\n"+
			"The ApplicationSet template must point to exactly this directory so Sharko's "+
			"init-seeded ConfigMap manifest is deployed.\n"+
			"--- rendered output ---\n%s", rendered)
	}
}

// TestBootstrapConnectivityCheckConfigMapValid asserts that the ConfigMap manifest
// seeded under configuration/connectivity-check/configmap.yaml is valid YAML and
// contains the expected resource fields.
func TestBootstrapConnectivityCheckConfigMapValid(t *testing.T) {
	root := repoRoot(t)
	cmPath := filepath.Join(root, "templates", "bootstrap", "configuration", "connectivity-check", "configmap.yaml")

	data, err := os.ReadFile(cmPath)
	if err != nil {
		t.Fatalf("failed to read configmap.yaml: %v", err)
	}

	// Must be parseable YAML.
	var doc map[string]interface{}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("configmap.yaml is not valid YAML: %v", err)
	}

	// Must declare the expected kind and name.
	if doc["kind"] != "ConfigMap" {
		t.Errorf("expected kind: ConfigMap, got %v", doc["kind"])
	}
	metadata, _ := doc["metadata"].(map[string]interface{})
	if metadata == nil {
		t.Fatal("configmap.yaml is missing metadata block")
	}
	if metadata["name"] != "sharko-connectivity-check" {
		t.Errorf("expected name: sharko-connectivity-check, got %v", metadata["name"])
	}
	// Must carry the managed-by label.
	labels, _ := metadata["labels"].(map[string]interface{})
	if labels == nil {
		t.Error("configmap.yaml metadata is missing labels block")
	} else if labels["app.kubernetes.io/managed-by"] != "sharko" {
		t.Errorf("expected app.kubernetes.io/managed-by: sharko, got %v", labels["app.kubernetes.io/managed-by"])
	}
}

// TestBootstrapAppSetSelectorRequiresEnabledLabel pins the deploy-correctness
// contract for V2-cleanup-20: the ApplicationSet cluster selector matches ONLY
// the canonical "<addon>: enabled" label. This is the downstream half of the
// fix — register/enable now write "enabled" (not the legacy "true") precisely
// because this selector reads only "enabled". If a future template change ever
// loosened or mistyped the selector value, the register-side fix would silently
// stop driving deployment; this test fails first.
func TestBootstrapAppSetSelectorRequiresEnabledLabel(t *testing.T) {
	helmBin, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm not installed; skipping bootstrap render test (CI helm-validate job is the hard guard)")
	}

	root := repoRoot(t)
	chartDir := filepath.Join(root, "templates", "bootstrap")
	dataDir := filepath.Join(root, "tests", "bootstraprender", "testdata")

	cmd := exec.Command(helmBin, "template", "testbootstrap", chartDir,
		"--values", filepath.Join(dataDir, "bootstrap-config.yaml"),
		"--values", filepath.Join(dataDir, "addons-catalog.yaml"),
		"--values", filepath.Join(dataDir, "managed-clusters.yaml"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helm template failed: %v\n%s", err, out)
	}
	rendered := string(out)

	// The velero ApplicationSet's cluster generator must select on
	// "velero: enabled" — the exact value the canonical AddonLabelValue(true)
	// emits and the reconciler propagates to the ArgoCD cluster Secret. A
	// cluster registered with velero on (label "velero: enabled") therefore
	// matches and the velero Application is generated for it.
	selectorRe := regexp.MustCompile(`(?m)^\s+velero:\s+enabled\s*$`)
	if !selectorRe.MatchString(rendered) {
		t.Errorf("ApplicationSet selector does not require \"velero: enabled\" — the canonical "+
			"label register/enable now write would not match, so velero would never deploy.\n"+
			"--- rendered output ---\n%s", rendered)
	}

	// Defensive: the legacy boolean vocabulary must NOT appear as a selector
	// value (it would silently re-introduce the V2-cleanup-20 mismatch).
	if regexp.MustCompile(`(?m)^\s+velero:\s+"?true"?\s*$`).MatchString(rendered) {
		t.Errorf("ApplicationSet selector uses legacy \"velero: true\" — the deploy-correctness "+
			"contract requires \"enabled\".\n--- rendered output ---\n%s", rendered)
	}
}
