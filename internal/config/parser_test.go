package config

import (
	"testing"

	"github.com/moran/argocd-addons-platform/internal/models"
)

func TestParseClusterAddons(t *testing.T) {
	input := []byte(`
clusters:
  - name: cluster-dev
    labels:
      datadog: enabled
      datadog-version: "3.70.7"
      keda: disabled
  - name: cluster-staging
    labels: []
  - name: cluster-prod
    labels:
      external-secrets: enabled
`)

	parser := NewParser()
	clusters, err := parser.ParseClusterAddons(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(clusters) != 3 {
		t.Fatalf("expected 3 clusters, got %d", len(clusters))
	}

	if clusters[0].Name != "cluster-dev" {
		t.Errorf("expected cluster-dev, got %s", clusters[0].Name)
	}
	if clusters[0].Labels["datadog"] != "enabled" {
		t.Errorf("expected datadog=enabled, got %s", clusters[0].Labels["datadog"])
	}
	if clusters[0].Labels["datadog-version"] != "3.70.7" {
		t.Errorf("expected datadog-version=3.70.7, got %s", clusters[0].Labels["datadog-version"])
	}

	if len(clusters[1].Labels) != 0 {
		t.Errorf("expected 0 labels for staging, got %d", len(clusters[1].Labels))
	}

	if clusters[2].Labels["external-secrets"] != "enabled" {
		t.Errorf("expected external-secrets=enabled, got %s", clusters[2].Labels["external-secrets"])
	}
}

func TestParseAddonsCatalog(t *testing.T) {
	input := []byte(`
applicationsets:
  - appName: datadog
    repoURL: https://helm.datadoghq.com
    chart: datadog
    version: 3.160.1
    inMigration: true
  - appName: keda
    repoURL: https://kedacore.github.io/charts
    chart: keda
    version: 2.14.2
    inMigration: true
  - appName: istio-base
    repoURL: https://istio-release.storage.googleapis.com/charts
    chart: base
    version: 1.28.0
    namespace: istio-system
    inMigration: true
`)

	parser := NewParser()
	addons, err := parser.ParseAddonsCatalog(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(addons) != 3 {
		t.Fatalf("expected 3 addons, got %d", len(addons))
	}

	tests := []struct {
		idx       int
		appName   string
		chart     string
		version   string
		namespace string
	}{
		{0, "datadog", "datadog", "3.160.1", ""},
		{1, "keda", "keda", "2.14.2", ""},
		{2, "istio-base", "base", "1.28.0", "istio-system"},
	}

	for _, tt := range tests {
		a := addons[tt.idx]
		if a.AppName != tt.appName {
			t.Errorf("[%d] expected appName=%s, got %s", tt.idx, tt.appName, a.AppName)
		}
		if a.Chart != tt.chart {
			t.Errorf("[%d] expected chart=%s, got %s", tt.idx, tt.chart, a.Chart)
		}
		if a.Version != tt.version {
			t.Errorf("[%d] expected version=%s, got %s", tt.idx, tt.version, a.Version)
		}
		if a.Namespace != tt.namespace {
			t.Errorf("[%d] expected namespace=%s, got %s", tt.idx, tt.namespace, a.Namespace)
		}
	}
}

func TestGetEnabledAddons(t *testing.T) {
	parser := NewParser()

	cluster := models.Cluster{
		Name: "my-cluster",
		Labels: map[string]string{
			"datadog":          "enabled",
			"datadog-version":  "3.70.7",
			"keda":             "disabled",
			"external-secrets": "enabled",
		},
	}

	catalog := []models.AddonCatalogEntry{
		{AppName: "datadog", RepoURL: "https://helm.datadoghq.com", Chart: "datadog", Version: "3.160.1"},
		{AppName: "keda", RepoURL: "https://kedacore.github.io/charts", Chart: "keda", Version: "2.14.2"},
		{AppName: "external-secrets", RepoURL: "https://charts.external-secrets.io", Chart: "external-secrets", Version: "0.19.2"},
		{AppName: "istio-base", RepoURL: "https://istio-release.storage.googleapis.com/charts", Chart: "base", Version: "1.28.0"},
	}

	addons := parser.GetEnabledAddons(cluster, catalog)

	// Only enabled addons are returned (keda=disabled and istio-base=no label are excluded)
	if len(addons) != 2 {
		t.Fatalf("expected 2 enabled addons (datadog + external-secrets), got %d", len(addons))
	}

	// Datadog: enabled with version override
	dd := findAddon(addons, "datadog")
	if dd == nil {
		t.Fatal("datadog not found")
	}
	if !dd.Enabled {
		t.Error("datadog should be enabled")
	}
	if dd.CurrentVersion != "3.70.7" {
		t.Errorf("expected override version 3.70.7, got %s", dd.CurrentVersion)
	}
	if !dd.HasVersionOverride {
		t.Error("datadog should have version override")
	}
	if dd.EnvironmentVersion != "3.160.1" {
		t.Errorf("expected catalog version 3.160.1, got %s", dd.EnvironmentVersion)
	}

	// Keda: disabled — should NOT be in the results
	keda := findAddon(addons, "keda")
	if keda != nil {
		t.Error("keda should not be in results (disabled)")
	}

	// External-secrets: enabled, no override
	es := findAddon(addons, "external-secrets")
	if es == nil {
		t.Fatal("external-secrets not found")
	}
	if !es.Enabled {
		t.Error("external-secrets should be enabled")
	}
	if es.HasVersionOverride {
		t.Error("external-secrets should not have version override")
	}
	if es.CurrentVersion != "0.19.2" {
		t.Errorf("expected version 0.19.2, got %s", es.CurrentVersion)
	}
}

func TestParseClusterValues(t *testing.T) {
	input := []byte(`
clusterGlobalValues:
  env: dev
  clusterName: my-cluster
  region: eu-west-1
`)

	parser := NewParser()
	values, err := parser.ParseClusterValues(input)
	if err != nil {
		t.Fatal(err)
	}

	if values["env"] != "dev" {
		t.Errorf("expected env=dev, got %v", values["env"])
	}
	if values["clusterName"] != "my-cluster" {
		t.Errorf("expected clusterName=my-cluster, got %v", values["clusterName"])
	}
}

func TestParseAll(t *testing.T) {
	clusterData := []byte(`clusters:
  - name: dev
    labels:
      datadog: enabled
`)
	catalogData := []byte(`applicationsets:
  - appName: datadog
    repoURL: https://helm.datadoghq.com
    chart: datadog
    version: 3.160.1
`)

	parser := NewParser()
	cfg, err := parser.ParseAll(clusterData, catalogData)
	if err != nil {
		t.Fatal(err)
	}

	if len(cfg.Clusters) != 1 {
		t.Errorf("expected 1 cluster, got %d", len(cfg.Clusters))
	}
	if len(cfg.Addons) != 1 {
		t.Errorf("expected 1 addon, got %d", len(cfg.Addons))
	}
}

func findAddon(addons []models.ClusterAddonInfo, name string) *models.ClusterAddonInfo {
	for i := range addons {
		if addons[i].AddonName == name {
			return &addons[i]
		}
	}
	return nil
}
