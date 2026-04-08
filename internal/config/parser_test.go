package config

import (
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
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
  - name: datadog
    repoURL: https://helm.datadoghq.com
    chart: datadog
    version: 3.160.1
  - name: keda
    repoURL: https://kedacore.github.io/charts
    chart: keda
    version: 2.14.2
  - name: istio-base
    repoURL: https://istio-release.storage.googleapis.com/charts
    chart: base
    version: 1.28.0
    namespace: istio-system
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
		if a.Name != tt.appName {
			t.Errorf("[%d] expected appName=%s, got %s", tt.idx, tt.appName, a.Name)
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
		{Name: "datadog", RepoURL: "https://helm.datadoghq.com", Chart: "datadog", Version: "3.160.1"},
		{Name: "keda", RepoURL: "https://kedacore.github.io/charts", Chart: "keda", Version: "2.14.2"},
		{Name: "external-secrets", RepoURL: "https://charts.external-secrets.io", Chart: "external-secrets", Version: "0.19.2"},
		{Name: "istio-base", RepoURL: "https://istio-release.storage.googleapis.com/charts", Chart: "base", Version: "1.28.0"},
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
  - name: datadog
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

// ---------------------------------------------------------------------------
// ParseAddonsCatalog — secrets field
// ---------------------------------------------------------------------------

func TestParseAddonsCatalog_WithSecrets(t *testing.T) {
	input := []byte(`
applicationsets:
  - name: datadog
    repoURL: https://helm.datadoghq.com
    chart: datadog
    version: 3.160.1
    secrets:
      - secretName: datadog-api-key
        namespace: datadog
        keys:
          api-key: secrets/datadog/api-key
          app-key: secrets/datadog/app-key
`)

	parser := NewParser()
	addons, err := parser.ParseAddonsCatalog(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(addons) != 1 {
		t.Fatalf("expected 1 addon, got %d", len(addons))
	}

	dd := addons[0]
	if len(dd.Secrets) != 1 {
		t.Fatalf("expected 1 secret ref, got %d", len(dd.Secrets))
	}

	s := dd.Secrets[0]
	if s.SecretName != "datadog-api-key" {
		t.Errorf("expected secretName=datadog-api-key, got %q", s.SecretName)
	}
	if s.Namespace != "datadog" {
		t.Errorf("expected namespace=datadog, got %q", s.Namespace)
	}
	if s.Keys["api-key"] != "secrets/datadog/api-key" {
		t.Errorf("expected api-key path, got %q", s.Keys["api-key"])
	}
	if s.Keys["app-key"] != "secrets/datadog/app-key" {
		t.Errorf("expected app-key path, got %q", s.Keys["app-key"])
	}
}

func TestParseAddonsCatalog_MultipleSecrets(t *testing.T) {
	input := []byte(`
applicationsets:
  - name: external-secrets
    repoURL: https://charts.external-secrets.io
    chart: external-secrets
    version: 0.9.0
    secrets:
      - secretName: es-api-key
        namespace: external-secrets
        keys:
          token: secrets/es/token
      - secretName: es-tls-certs
        namespace: external-secrets
        keys:
          tls.crt: secrets/es/tls-crt
          tls.key: secrets/es/tls-key
`)

	parser := NewParser()
	addons, err := parser.ParseAddonsCatalog(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(addons[0].Secrets) != 2 {
		t.Fatalf("expected 2 secret refs, got %d", len(addons[0].Secrets))
	}
}

func TestParseAddonsCatalog_NoSecrets(t *testing.T) {
	input := []byte(`
applicationsets:
  - name: keda
    repoURL: https://kedacore.github.io/charts
    chart: keda
    version: 2.14.2
`)

	parser := NewParser()
	addons, err := parser.ParseAddonsCatalog(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(addons[0].Secrets) != 0 {
		t.Errorf("expected 0 secrets, got %d", len(addons[0].Secrets))
	}
}

// ---------------------------------------------------------------------------
// ParseClusterAddons — secretPath field
// ---------------------------------------------------------------------------

func TestParseClusterAddons_WithSecretPath(t *testing.T) {
	input := []byte(`
clusters:
  - name: cluster-prod
    secretPath: secrets/clusters/prod
    labels:
      datadog: enabled
  - name: cluster-dev
    labels:
      datadog: enabled
`)

	parser := NewParser()
	clusters, err := parser.ParseClusterAddons(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(clusters) != 2 {
		t.Fatalf("expected 2 clusters, got %d", len(clusters))
	}

	// cluster-prod should have secretPath set.
	prod := clusters[0]
	if prod.Name != "cluster-prod" {
		t.Fatalf("expected cluster-prod, got %q", prod.Name)
	}
	if prod.SecretPath != "secrets/clusters/prod" {
		t.Errorf("expected secretPath=secrets/clusters/prod, got %q", prod.SecretPath)
	}

	// cluster-dev should have empty secretPath.
	dev := clusters[1]
	if dev.SecretPath != "" {
		t.Errorf("expected empty secretPath for cluster-dev, got %q", dev.SecretPath)
	}
}

func TestParseClusterAddons_SecretPathPreservedInLabels(t *testing.T) {
	input := []byte(`
clusters:
  - name: cluster-staging
    secretPath: aws-sm/clusters/staging
    labels:
      nginx: enabled
      nginx-version: "1.9.0"
`)

	parser := NewParser()
	clusters, err := parser.ParseClusterAddons(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	c := clusters[0]
	// secretPath should not bleed into labels.
	if _, ok := c.Labels["secretPath"]; ok {
		t.Error("secretPath should not appear in labels map")
	}
	if c.SecretPath != "aws-sm/clusters/staging" {
		t.Errorf("expected secretPath=aws-sm/clusters/staging, got %q", c.SecretPath)
	}
	if c.Labels["nginx"] != "enabled" {
		t.Errorf("expected nginx=enabled in labels, got %q", c.Labels["nginx"])
	}
}
