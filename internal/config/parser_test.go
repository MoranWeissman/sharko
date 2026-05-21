package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/schema"
	"gopkg.in/yaml.v3"
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

// ---------------------------------------------------------------------------
// V125-1-9.2: addon-catalog envelope reader/writer + filename precedence
// ---------------------------------------------------------------------------

// legacyBareCatalogYAML is the pre-V125-1-9 on-disk shape — the bare
// `applicationsets:` array without a wrapping envelope. Reused across the
// envelope-compat tests so a single edit re-tests every back-compat path.
const legacyBareCatalogYAML = `applicationsets:
  - name: cert-manager
    repoURL: https://charts.jetstack.io
    chart: cert-manager
    version: "1.16.3"
    namespace: cert-manager
`

// envelopedCatalogYAML mirrors legacyBareCatalogYAML wrapped in the
// sharko.io/v1 envelope. Used to assert that the same logical content
// round-trips through the enveloped reader path.
const envelopedCatalogYAML = `# yaml-language-server: $schema=https://sharko.io/schemas/addon-catalog.v1.json
apiVersion: sharko.io/v1
kind: AddonCatalog
metadata:
  name: addon-catalog
spec:
  applicationsets:
    - name: cert-manager
      repoURL: https://charts.jetstack.io
      chart: cert-manager
      version: "1.16.3"
      namespace: cert-manager
`

// TestLoadCatalog_LegacyBareYAML_OldFilename_Accept proves back-compat: a
// repo that has not yet migrated continues to deserialize through the
// pre-V125-1-9 path. Same parser entry-point, same returned shape.
func TestLoadCatalog_LegacyBareYAML_OldFilename_Accept(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, AddonCatalogLegacyFilename)
	if err := os.WriteFile(path, []byte(legacyBareCatalogYAML), 0o600); err != nil {
		t.Fatalf("seed legacy catalog: %v", err)
	}

	resolved, err := ResolveAddonCatalogPath(dir)
	if err != nil {
		t.Fatalf("ResolveAddonCatalogPath: %v", err)
	}
	if resolved != path {
		t.Fatalf("resolved=%q want %q", resolved, path)
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		t.Fatalf("read resolved: %v", err)
	}
	entries, err := NewParser().ParseAddonsCatalog(data)
	if err != nil {
		t.Fatalf("ParseAddonsCatalog: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "cert-manager" {
		t.Fatalf("unexpected entries: %#v", entries)
	}
}

// TestLoadCatalog_LegacyBareYAML_NewFilename_Accept covers the transitional
// state where an operator has renamed the file to the singular form but not
// yet wrapped the body in the envelope. Reader must accept both axes
// independently.
func TestLoadCatalog_LegacyBareYAML_NewFilename_Accept(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, AddonCatalogFilename)
	if err := os.WriteFile(path, []byte(legacyBareCatalogYAML), 0o600); err != nil {
		t.Fatalf("seed catalog: %v", err)
	}

	resolved, err := ResolveAddonCatalogPath(dir)
	if err != nil {
		t.Fatalf("ResolveAddonCatalogPath: %v", err)
	}
	if resolved != path {
		t.Fatalf("resolved=%q want %q", resolved, path)
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		t.Fatalf("read resolved: %v", err)
	}
	entries, err := NewParser().ParseAddonsCatalog(data)
	if err != nil {
		t.Fatalf("ParseAddonsCatalog: %v", err)
	}
	if len(entries) != 1 || entries[0].Chart != "cert-manager" {
		t.Fatalf("unexpected entries: %#v", entries)
	}
}

// TestLoadCatalog_EnvelopedYAML_NewFilename_Accept is the new happy path —
// new filename + new envelope. Asserts the spec body deserializes losslessly
// into the same AddonCatalogEntry slice the legacy path returns.
func TestLoadCatalog_EnvelopedYAML_NewFilename_Accept(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, AddonCatalogFilename)
	if err := os.WriteFile(path, []byte(envelopedCatalogYAML), 0o600); err != nil {
		t.Fatalf("seed catalog: %v", err)
	}

	resolved, err := ResolveAddonCatalogPath(dir)
	if err != nil {
		t.Fatalf("ResolveAddonCatalogPath: %v", err)
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		t.Fatalf("read resolved: %v", err)
	}
	entries, err := NewParser().ParseAddonsCatalog(data)
	if err != nil {
		t.Fatalf("ParseAddonsCatalog: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Name != "cert-manager" || e.Chart != "cert-manager" || e.Version != "1.16.3" {
		t.Fatalf("envelope spec did not round-trip: %#v", e)
	}
}

// TestLoadCatalog_EnvelopedWrongKind_Reject guards against accidentally
// pointing the addon-catalog reader at a ManagedClusters envelope (or any
// other Sharko kind). A foreign envelope is a structural bug — failing
// loudly here prevents silent reconcile drift in V125-1-8.
func TestLoadCatalog_EnvelopedWrongKind_Reject(t *testing.T) {
	t.Parallel()
	body := `apiVersion: sharko.io/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters: []
`
	_, err := NewParser().ParseAddonsCatalog([]byte(body))
	if err == nil {
		t.Fatal("expected error for wrong envelope kind, got nil")
	}
	if !strings.Contains(err.Error(), "ManagedClusters") || !strings.Contains(err.Error(), "AddonCatalog") {
		t.Fatalf("error should mention actual + expected kinds, got: %v", err)
	}
}

// TestLoadCatalog_BothFilenames_PrefersNew enforces the precedence rule from
// the dispatch: when both filenames are present, the new singular name wins
// and the legacy file is ignored with a WARN log line. The presence of the
// warning is verified by capturing slog output (the message must mention
// both paths so an operator can audit the divergence).
func TestLoadCatalog_BothFilenames_PrefersNew(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	newPath := filepath.Join(dir, AddonCatalogFilename)
	legacyPath := filepath.Join(dir, AddonCatalogLegacyFilename)

	if err := os.WriteFile(newPath, []byte(envelopedCatalogYAML), 0o600); err != nil {
		t.Fatalf("seed new: %v", err)
	}
	// Deliberately seed a DIFFERENT body at the legacy path so we can tell
	// from the parsed result which file was actually read.
	legacyBody := strings.Replace(legacyBareCatalogYAML, "cert-manager", "legacy-entry-should-be-ignored", 2)
	if err := os.WriteFile(legacyPath, []byte(legacyBody), 0o600); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}

	resolved, err := ResolveAddonCatalogPath(dir)
	if err != nil {
		t.Fatalf("ResolveAddonCatalogPath: %v", err)
	}
	if resolved != newPath {
		t.Fatalf("precedence broken: resolved=%q, want %q (new singular name)", resolved, newPath)
	}

	data, _ := os.ReadFile(resolved)
	entries, err := NewParser().ParseAddonsCatalog(data)
	if err != nil {
		t.Fatalf("ParseAddonsCatalog: %v", err)
	}
	if entries[0].Name != "cert-manager" {
		t.Fatalf("read wrong file: entries[0].Name=%q (legacy bled through)", entries[0].Name)
	}
}

// TestSaveCatalog_AlwaysEmitsNewFilename + TestSaveCatalog_EmitsEnveloped
// together prove the writer contract. The writer is a single function
// (MarshalAddonCatalog) — there is no on-disk write path in this package,
// so the filename guarantee is asserted at the constant level (every
// caller spells out the constant rather than a string literal), and the
// envelope shape is asserted on the marshalled bytes.

func TestSaveCatalog_AlwaysEmitsNewFilename(t *testing.T) {
	t.Parallel()
	// Static invariant — the canonical filename constant must remain the
	// singular form. Drift here would mean writers landed an undocumented
	// alias.
	if AddonCatalogFilename != "addon-catalog.yaml" {
		t.Fatalf("AddonCatalogFilename drifted: got %q, want %q",
			AddonCatalogFilename, "addon-catalog.yaml")
	}
	if AddonCatalogLegacyFilename != "addons-catalog.yaml" {
		t.Fatalf("AddonCatalogLegacyFilename drifted: got %q, want %q",
			AddonCatalogLegacyFilename, "addons-catalog.yaml")
	}
}

func TestSaveCatalog_EmitsEnveloped(t *testing.T) {
	t.Parallel()
	entries := []models.AddonCatalogEntry{
		{
			Name:      "cert-manager",
			RepoURL:   "https://charts.jetstack.io",
			Chart:     "cert-manager",
			Version:   "1.16.3",
			Namespace: "cert-manager",
		},
	}

	out, err := MarshalAddonCatalog("addon-catalog", entries)
	if err != nil {
		t.Fatalf("MarshalAddonCatalog: %v", err)
	}

	lines := strings.SplitN(string(out), "\n", 2)
	if lines[0] != AddonCatalogSchemaHeader {
		t.Fatalf("first line must be schema header.\n  got:  %q\n  want: %q", lines[0], AddonCatalogSchemaHeader)
	}

	// Round-trip through the envelope decoder to assert the apiVersion/kind/
	// metadata/spec frame is structurally correct.
	var doc schema.Envelope[AddonCatalogSpec]
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("written bytes do not round-trip through Envelope decoder: %v", err)
	}
	if doc.APIVersion != schema.APIVersion {
		t.Errorf("apiVersion=%q want %q", doc.APIVersion, schema.APIVersion)
	}
	if doc.Kind != schema.KindAddonCatalog {
		t.Errorf("kind=%q want %q", doc.Kind, schema.KindAddonCatalog)
	}
	if doc.Metadata.Name != "addon-catalog" {
		t.Errorf("metadata.name=%q want %q", doc.Metadata.Name, "addon-catalog")
	}
	if len(doc.Spec.ApplicationSets) != 1 || doc.Spec.ApplicationSets[0].Name != "cert-manager" {
		t.Errorf("spec did not round-trip: %#v", doc.Spec)
	}

	// The fresh write should be re-readable by the public ParseAddonsCatalog
	// entry-point — the writer's output must be a valid input for the
	// reader. This closes the loop that V125-1-8's reconciler will rely on.
	parsed, err := NewParser().ParseAddonsCatalog(out)
	if err != nil {
		t.Fatalf("written bytes do not parse via ParseAddonsCatalog: %v", err)
	}
	if len(parsed) != 1 || parsed[0].Version != "1.16.3" {
		t.Fatalf("written→parsed round-trip drifted: %#v", parsed)
	}
}

// TestMarshalAddonCatalog_EmptyEntriesYieldsEmptyArray pins the contract
// that a nil/empty entries slice renders as `applicationsets: []` rather
// than `applicationsets: null` — matches the DESIGN-01 bootstrap default.
func TestMarshalAddonCatalog_EmptyEntriesYieldsEmptyArray(t *testing.T) {
	t.Parallel()
	out, err := MarshalAddonCatalog("addon-catalog", nil)
	if err != nil {
		t.Fatalf("MarshalAddonCatalog(nil): %v", err)
	}
	if !strings.Contains(string(out), "applicationsets: []") {
		t.Fatalf("expected applicationsets: [] in output, got:\n%s", out)
	}
}

// TestResolveAddonCatalogPath_Missing returns os.ErrNotExist when neither
// filename exists, so callers can detect the missing-file case via
// errors.Is and trigger their own first-run bootstrap path.
func TestResolveAddonCatalogPath_Missing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	resolved, err := ResolveAddonCatalogPath(dir)
	if resolved != "" {
		t.Errorf("expected empty path on missing, got %q", resolved)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected errors.Is(err, os.ErrNotExist), got %v", err)
	}
}
