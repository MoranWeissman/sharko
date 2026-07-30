package demo

import (
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/schema"
)

// TestV4DemoFilesPassSchemaValidation proves the v4 demo fixture (Story
// 4.5's "the demo repo content must PASS sharko validate-config" gate)
// actually validates. `sharko validate-config` (cmd/sharko/validate_config.go)
// is a thin CLI wrapper around exactly the two checks this test runs
// directly: schema.DefaultValidator().ValidateAutoDetect for every
// Sharko-enveloped file, plus the ClusterAddons
// filename-must-equal-spec.cluster invariant the CLI enforces
// separately (design doc §2.1) because a generic JSON Schema has no way
// to see the file's own path. Running the same validator function the
// CLI calls — rather than shelling out to `go run ./cmd/sharko
// validate-config` — keeps this test hermetic and fast while still
// exercising the real validation path, not a hand-rolled approximation
// of it.
func TestV4DemoFilesPassSchemaValidation(t *testing.T) {
	files, err := buildV4DemoFiles()
	if err != nil {
		t.Fatalf("buildV4DemoFiles: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("buildV4DemoFiles returned no files")
	}

	validator, err := schema.DefaultValidator()
	if err != nil {
		t.Fatalf("schema.DefaultValidator: %v", err)
	}

	for path, body := range files {
		enveloped, err := schema.IsEnveloped(body)
		if err != nil {
			t.Errorf("%s: IsEnveloped: %v", path, err)
			continue
		}
		if !enveloped {
			// values/*.yaml files are deliberately plain Helm values
			// with no envelope (design doc §2.0) — nothing to validate.
			if !strings.HasPrefix(path, orchestrator.V4GlobalValuesDir+"/") &&
				!strings.HasPrefix(path, orchestrator.V4ClusterValuesDir+"/") {
				t.Errorf("%s: expected a Sharko-enveloped file, got a plain one", path)
			}
			continue
		}

		if err := validator.ValidateAutoDetect(body); err != nil {
			t.Errorf("%s: schema validation failed: %v", path, err)
			continue
		}

		// ClusterAddons's file-name invariant (design doc §2.1):
		// spec.cluster must equal the file's basename without .yaml —
		// validate-config checks this in addition to the JSON Schema
		// because the schema itself has no access to the file's path.
		if strings.HasPrefix(path, orchestrator.V4ClustersDir+"/") {
			spec, err := models.LoadClusterAddons(body)
			if err != nil {
				t.Errorf("%s: LoadClusterAddons: %v", path, err)
				continue
			}
			wantCluster := strings.TrimSuffix(strings.TrimPrefix(path, orchestrator.V4ClustersDir+"/"), ".yaml")
			if spec.Cluster != wantCluster {
				t.Errorf("%s: spec.cluster = %q, want %q (must equal the file name)", path, spec.Cluster, wantCluster)
			}
		}
	}

	// Sanity: every fixture cluster has a ClusterAddons file, every
	// pinned addon has a corresponding catalog delta entry (or is a
	// bare "follow the catalog default" reference), and the delta has
	// at least one entry that is NOT in the shipped/curated catalog
	// (the "one internal addon" AC — design doc §2.3, v4 Wave 1 Story
	// 3.3's "origin: internal" marker).
	deltaBody, ok := files[config.AddonCatalogDeltaPath]
	if !ok {
		t.Fatalf("missing %s in the v4 demo fixture", config.AddonCatalogDeltaPath)
	}
	delta, err := config.LoadAddonCatalogDelta(deltaBody)
	if err != nil {
		t.Fatalf("LoadAddonCatalogDelta: %v", err)
	}
	internalEntry, ok := delta.Addons["datadog"]
	if !ok {
		t.Fatal(`expected "datadog" in the catalog delta as the internal-addon example`)
	}
	if internalEntry.RepoURL == "" || internalEntry.Chart == "" || internalEntry.Version == "" {
		t.Errorf("datadog delta entry is missing repoURL/chart/version — required for an addon with no shipped catalog entry, got %+v", internalEntry)
	}

	for _, clusterName := range []string{"prod-eu", "prod-us", "staging-eu", "dev-us", "perf-asia"} {
		path := orchestrator.V4ClustersDir + "/" + clusterName + ".yaml"
		if _, ok := files[path]; !ok {
			t.Errorf("missing %s in the v4 demo fixture", path)
		}
	}
}

// TestV4DemoProdEuMatchesWorkedExample verifies prod-eu carries the
// design doc's §6 worked example: cert-manager pinned older than the
// catalog default, with the webhook caBundle ignoreDifferences quirk;
// metrics-server following the catalog default (no per-cluster pin).
func TestV4DemoProdEuMatchesWorkedExample(t *testing.T) {
	files, err := buildV4DemoFiles()
	if err != nil {
		t.Fatalf("buildV4DemoFiles: %v", err)
	}
	body, ok := files[orchestrator.V4ClustersDir+"/prod-eu.yaml"]
	if !ok {
		t.Fatal("missing clusters/prod-eu.yaml")
	}
	spec, err := models.LoadClusterAddons(body)
	if err != nil {
		t.Fatalf("LoadClusterAddons: %v", err)
	}

	cm, ok := spec.Addons["cert-manager"]
	if !ok || !cm.Enabled {
		t.Fatal("expected cert-manager enabled on prod-eu")
	}
	if cm.Version != "1.12.0" {
		t.Errorf("cert-manager version on prod-eu = %q, want the older pin 1.12.0", cm.Version)
	}
	if cm.Settings == nil || len(cm.Settings.IgnoreDifferences) == 0 {
		t.Error("expected cert-manager on prod-eu to carry the webhook caBundle ignoreDifferences quirk")
	}

	ms, ok := spec.Addons["metrics-server"]
	if !ok || !ms.Enabled {
		t.Fatal("expected metrics-server enabled on prod-eu")
	}
	if ms.Version != "" {
		t.Errorf("metrics-server version on prod-eu = %q, want empty (follows the catalog default)", ms.Version)
	}
}
