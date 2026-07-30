package clusterreconciler

// v4_migration_labels_test.go — the v4 Wave 2 Story 5.2 addon-set
// invariant, checked end to end against BOTH real derivations.
//
// The migration's own test suite (internal/orchestrator/migration_v3v4_test.go)
// asserts the same property, but it has to REPLICATE this package's label
// derivation to do so — clusterreconciler is a separate package and the
// production dependency arrow does not point this way. This test closes
// that gap from the other side: it runs the REAL migration
// (orchestrator.PreviewMigration) and feeds its output into the REAL
// reconciler derivation (readV4AddonLabels), so the two can never drift
// apart without something going red.
//
// The orchestrator import is TEST-ONLY. Go excludes _test.go files from the
// production import graph, so the "clusterreconciler must not import
// orchestrator" boundary (see v4_assignments.go's v4ClustersDir comment) is
// untouched — and there is no cycle either way: orchestrator does not
// import this package.

import (
	"context"
	"reflect"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// migrationV3Registry is a v3 cluster registry covering every case the
// label derivation has to get right: an addon that is on, one that is off,
// a per-cluster version override (which must NOT become an addon), and a
// plain non-addon label (which must not either).
const migrationV3Registry = `clusters:
  - name: prod-eu
    secretPath: k8s-prod-eu
    region: eu-central-1
    labels:
      cert-manager: enabled
      cert-manager-version: 1.12.0
      metrics-server: enabled
      inhouse-api: disabled
      env: prod
  - name: staging-us
    secretPath: k8s-staging-us
    region: us-east-1
    labels:
      metrics-server: enabled
`

const migrationV3Catalog = `applicationsets:
  - name: cert-manager
    repoURL: https://charts.jetstack.io
    chart: cert-manager
    version: 1.14.5
  - name: metrics-server
    repoURL: https://kubernetes-sigs.github.io/metrics-server
    chart: metrics-server
    version: 3.12.1
  - name: inhouse-api
    repoURL: oci://registry.example.com/charts
    chart: inhouse-api
    version: 2.1.0
`

// TestMigration_AddonLabelsIdenticalEitherSide is the acceptance criterion,
// stated as the fleet experiences it: the addon-enablement labels the
// reconciler puts on each cluster's ArgoCD Secret must name exactly the
// same addons after the migration as the v3 labels named before it.
//
// The KEYS deliberately differ — v3 uses a bare `cert-manager: enabled`,
// v4 uses `addons.sharko.dev/cert-manager: enabled` (models.V4AddonLabelPrefix)
// so a repo mid-migration can never have one format's labels mistaken for
// the other's. What must be identical is the addon SET those maps describe.
func TestMigration_AddonLabelsIdenticalEitherSide(t *testing.T) {
	ctx := context.Background()

	v3Repo := &fakeGit{files: map[string][]byte{
		// The two markers that make this a recognisable v3 repo.
		"bootstrap/Chart.yaml":                []byte("name: bootstrap\n"),
		"configuration/managed-clusters.yaml": []byte(migrationV3Registry),
		"configuration/addons-catalog.yaml":   []byte(migrationV3Catalog),
	}}

	orch := orchestrator.New(nil, nil, nil, v3Repo, orchestrator.GitOpsConfig{
		BranchPrefix: "sharko/",
		CommitPrefix: "sharko:",
		BaseBranch:   "main",
		RepoURL:      "https://example.com/org/addons.git",
	}, orchestrator.RepoPathsConfig{
		ClusterValues:   "configuration/addons-clusters-values",
		GlobalValues:    "configuration/addons-global-values",
		Catalog:         "configuration/addons-catalog.yaml",
		ManagedClusters: "configuration/managed-clusters.yaml",
		HostClusterName: "management",
	}, nil)

	plan, err := orch.PreviewMigration(ctx)
	if err != nil {
		t.Fatalf("PreviewMigration: %v", err)
	}

	// Load the migrated repo into a fake and let the REAL reconciler
	// derivation read it, exactly as it would on a real tick.
	migrated := &fakeGit{files: map[string][]byte{}}
	for _, change := range append(append([]orchestrator.MigrationFileChange{}, plan.Add...), plan.Convert...) {
		migrated.files[change.Path] = []byte(change.Content)
	}
	after := readV4AddonLabels(ctx, migrated, "main")

	// What v3 deployed: the ArgoCD ApplicationSet selector matched a bare
	// `<addon>: enabled` label, and one ApplicationSet existed per CATALOG
	// entry — so a label naming an addon the catalog never had deployed
	// nothing and must not count.
	before := map[string]map[string]string{
		"prod-eu": {
			models.V4AddonLabelKey("cert-manager"):   models.LabelEnabled,
			models.V4AddonLabelKey("metrics-server"): models.LabelEnabled,
			// inhouse-api was `disabled`; env is not an addon;
			// cert-manager-version is a version override, not an addon.
		},
		"staging-us": {
			models.V4AddonLabelKey("metrics-server"): models.LabelEnabled,
		},
	}

	if !reflect.DeepEqual(after, before) {
		t.Errorf("the fleet runs a different set of addons after the migration:\n after:  %v\n before: %v", after, before)
	}
}

// TestMigration_VersionOverrideBecomesAPinNotAnAddon guards the one label
// that looks like an addon and is not. `cert-manager-version: 1.12.0` was
// v3's per-cluster version override; reading it as an addon would deploy a
// chart called "cert-manager-version", and dropping it would silently move
// the cluster onto a different cert-manager.
func TestMigration_VersionOverrideBecomesAPinNotAnAddon(t *testing.T) {
	ctx := context.Background()

	v3Repo := &fakeGit{files: map[string][]byte{
		"bootstrap/Chart.yaml":                []byte("name: bootstrap\n"),
		"configuration/managed-clusters.yaml": []byte(migrationV3Registry),
		"configuration/addons-catalog.yaml":   []byte(migrationV3Catalog),
	}}
	orch := orchestrator.New(nil, nil, nil, v3Repo, orchestrator.GitOpsConfig{
		BaseBranch: "main", RepoURL: "https://example.com/org/addons.git",
	}, orchestrator.RepoPathsConfig{
		Catalog:         "configuration/addons-catalog.yaml",
		ManagedClusters: "configuration/managed-clusters.yaml",
	}, nil)

	plan, err := orch.PreviewMigration(ctx)
	if err != nil {
		t.Fatalf("PreviewMigration: %v", err)
	}

	var body []byte
	for _, change := range append(append([]orchestrator.MigrationFileChange{}, plan.Add...), plan.Convert...) {
		if change.Path == "clusters/prod-eu.yaml" {
			body = []byte(change.Content)
		}
	}
	if body == nil {
		t.Fatal("the plan has no clusters/prod-eu.yaml")
	}

	spec, err := models.LoadClusterAddons(body)
	if err != nil {
		t.Fatalf("the migrated assignment file does not read back: %v", err)
	}
	if _, phantom := spec.Addons["cert-manager-version"]; phantom {
		t.Error("the version-override label was read as an addon of its own")
	}
	if got := spec.Addons["cert-manager"].Version; got != "1.12.0" {
		t.Errorf("cert-manager version pin = %q, want 1.12.0 — the cluster would move versions", got)
	}
}
