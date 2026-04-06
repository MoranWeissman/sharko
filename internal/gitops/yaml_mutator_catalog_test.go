package gitops

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

const catalogBase = `applicationsets:
  - name: datadog
    repoURL: https://helm.datadoghq.com
    chart: datadog
    version: 3.160.1

  - name: keda
    repoURL: https://kedacore.github.io/charts
    chart: keda
    version: 2.14.2
`

// ---------------------------------------------------------------------------
// AddCatalogEntry
// ---------------------------------------------------------------------------

func TestAddCatalogEntry_Basic(t *testing.T) {
	entry := CatalogEntryInput{
		Name:    "prometheus",
		RepoURL: "https://prometheus-community.github.io/helm-charts",
		Chart:   "prometheus",
		Version: "25.0.0",
	}
	out, err := AddCatalogEntry([]byte(catalogBase), entry)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "  - name: prometheus") {
		t.Errorf("new entry not found:\n%s", s)
	}
	if !strings.Contains(s, "    repoURL: https://prometheus-community.github.io/helm-charts") {
		t.Errorf("repoURL not found:\n%s", s)
	}
	if !strings.Contains(s, "    chart: prometheus") {
		t.Errorf("chart not found:\n%s", s)
	}
	if !strings.Contains(s, "    version: 25.0.0") {
		t.Errorf("version not found:\n%s", s)
	}
	// Namespace must NOT appear.
	if strings.Contains(s, "namespace:") {
		t.Errorf("unexpected namespace field:\n%s", s)
	}
	// Original entries preserved.
	if !strings.Contains(s, "  - name: datadog") {
		t.Errorf("datadog entry lost:\n%s", s)
	}
	if !strings.Contains(s, "  - name: keda") {
		t.Errorf("keda entry lost:\n%s", s)
	}
}

func TestAddCatalogEntry_WithNamespace(t *testing.T) {
	entry := CatalogEntryInput{
		Name:      "metrics-server",
		RepoURL:   "https://kubernetes-sigs.github.io/metrics-server",
		Chart:     "metrics-server",
		Version:   "3.12.0",
		Namespace: "kube-system",
	}
	out, err := AddCatalogEntry([]byte(catalogBase), entry)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "    namespace: kube-system") {
		t.Errorf("namespace field missing:\n%s", s)
	}
}

func TestAddCatalogEntry_WithSyncWave(t *testing.T) {
	entry := CatalogEntryInput{
		Name:     "cert-manager",
		RepoURL:  "https://charts.jetstack.io",
		Chart:    "cert-manager",
		Version:  "1.14.0",
		SyncWave: -5,
	}
	out, err := AddCatalogEntry([]byte(catalogBase), entry)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "    syncWave: -5") {
		t.Errorf("syncWave field missing:\n%s", s)
	}
}

func TestAddCatalogEntry_DuplicateError(t *testing.T) {
	entry := CatalogEntryInput{
		Name:    "datadog",
		RepoURL: "https://helm.datadoghq.com",
		Chart:   "datadog",
		Version: "3.200.0",
	}
	_, err := AddCatalogEntry([]byte(catalogBase), entry)
	if err == nil {
		t.Fatal("expected error for duplicate name")
	}
	if !strings.Contains(err.Error(), "datadog") {
		t.Errorf("error should mention addon name: %v", err)
	}
}

func TestAddCatalogEntry_EmptyApplicationsets(t *testing.T) {
	input := `applicationsets:
`
	entry := CatalogEntryInput{
		Name:    "keda",
		RepoURL: "https://kedacore.github.io/charts",
		Chart:   "keda",
		Version: "2.14.2",
	}
	out, err := AddCatalogEntry([]byte(input), entry)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "  - name: keda") {
		t.Errorf("entry not added to empty applicationsets:\n%s", s)
	}
}

func TestAddCatalogEntry_MissingApplicationsetsKey(t *testing.T) {
	input := `clusters:
  - name: test
`
	entry := CatalogEntryInput{Name: "keda", RepoURL: "x", Chart: "keda", Version: "1.0.0"}
	_, err := AddCatalogEntry([]byte(input), entry)
	if err == nil {
		t.Fatal("expected error when applicationsets: key is absent")
	}
}

func TestAddCatalogEntry_WithPath(t *testing.T) {
	entry := CatalogEntryInput{
		Name:    "hello-world",
		RepoURL: "https://github.com/example/repo",
		Chart:   "",
		Version: "1.0.0",
		Path:    "charts/hello-world",
	}
	out, err := AddCatalogEntry([]byte(catalogBase), entry)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "    path: charts/hello-world") {
		t.Errorf("path field missing:\n%s", s)
	}
}

// ---------------------------------------------------------------------------
// RemoveCatalogEntry
// ---------------------------------------------------------------------------

func TestRemoveCatalogEntry_First(t *testing.T) {
	out, err := RemoveCatalogEntry([]byte(catalogBase), "datadog")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := string(out)
	if strings.Contains(s, "name: datadog") {
		t.Errorf("datadog entry still present:\n%s", s)
	}
	if !strings.Contains(s, "name: keda") {
		t.Errorf("keda entry was removed unexpectedly:\n%s", s)
	}
}

func TestRemoveCatalogEntry_Last(t *testing.T) {
	out, err := RemoveCatalogEntry([]byte(catalogBase), "keda")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := string(out)
	if strings.Contains(s, "name: keda") {
		t.Errorf("keda entry still present:\n%s", s)
	}
	if !strings.Contains(s, "name: datadog") {
		t.Errorf("datadog entry was removed unexpectedly:\n%s", s)
	}
}

func TestRemoveCatalogEntry_Middle(t *testing.T) {
	// Build a three-entry catalog.
	threeEntry := `applicationsets:
  - name: datadog
    repoURL: https://helm.datadoghq.com
    chart: datadog
    version: 3.160.1

  - name: keda
    repoURL: https://kedacore.github.io/charts
    chart: keda
    version: 2.14.2

  - name: prometheus
    repoURL: https://prometheus-community.github.io/helm-charts
    chart: prometheus
    version: 25.0.0
`
	out, err := RemoveCatalogEntry([]byte(threeEntry), "keda")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := string(out)
	if strings.Contains(s, "name: keda") {
		t.Errorf("keda entry still present:\n%s", s)
	}
	if !strings.Contains(s, "name: datadog") {
		t.Errorf("datadog entry missing:\n%s", s)
	}
	if !strings.Contains(s, "name: prometheus") {
		t.Errorf("prometheus entry missing:\n%s", s)
	}
}

func TestRemoveCatalogEntry_WithComment(t *testing.T) {
	input := `applicationsets:
  - name: datadog
    repoURL: https://helm.datadoghq.com
    chart: datadog
    version: 3.160.1

  # keda addon — installed in kube-system
  - name: keda
    repoURL: https://kedacore.github.io/charts
    chart: keda
    version: 2.14.2
`
	out, err := RemoveCatalogEntry([]byte(input), "keda")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := string(out)
	if strings.Contains(s, "keda") {
		t.Errorf("keda (including comment) still present:\n%s", s)
	}
	if !strings.Contains(s, "name: datadog") {
		t.Errorf("datadog entry missing:\n%s", s)
	}
}

func TestRemoveCatalogEntry_NotFound(t *testing.T) {
	_, err := RemoveCatalogEntry([]byte(catalogBase), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent addon")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error should mention addon name: %v", err)
	}
}

// ---------------------------------------------------------------------------
// UpdateCatalogEntry
// ---------------------------------------------------------------------------

func TestUpdateCatalogEntry_Version(t *testing.T) {
	out, err := UpdateCatalogEntry([]byte(catalogBase), "datadog", map[string]string{"version": "3.200.0"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "    version: 3.200.0") {
		t.Errorf("version not updated:\n%s", s)
	}
	// keda version untouched.
	if !strings.Contains(s, "    version: 2.14.2") {
		t.Errorf("keda version was modified:\n%s", s)
	}
}

func TestUpdateCatalogEntry_MultipleFields(t *testing.T) {
	out, err := UpdateCatalogEntry([]byte(catalogBase), "keda", map[string]string{
		"version": "2.15.0",
		"chart":   "keda-patched",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "    version: 2.15.0") {
		t.Errorf("keda version not updated:\n%s", s)
	}
	if !strings.Contains(s, "    chart: keda-patched") {
		t.Errorf("keda chart not updated:\n%s", s)
	}
	// datadog untouched.
	if !strings.Contains(s, "    version: 3.160.1") {
		t.Errorf("datadog version was modified:\n%s", s)
	}
}

func TestUpdateCatalogEntry_AddNewField(t *testing.T) {
	out, err := UpdateCatalogEntry([]byte(catalogBase), "datadog", map[string]string{"namespace": "monitoring"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "    namespace: monitoring") {
		t.Errorf("namespace field not appended:\n%s", s)
	}
	// Entry structure intact — name still at the right position.
	if !strings.Contains(s, "  - name: datadog") {
		t.Errorf("name line missing:\n%s", s)
	}
}

func TestUpdateCatalogEntry_NameRejected(t *testing.T) {
	_, err := UpdateCatalogEntry([]byte(catalogBase), "datadog", map[string]string{"name": "renamed"})
	if err == nil {
		t.Fatal("expected error when attempting to update name")
	}
}

func TestUpdateCatalogEntry_NotFound(t *testing.T) {
	_, err := UpdateCatalogEntry([]byte(catalogBase), "nonexistent", map[string]string{"version": "1.0.0"})
	if err == nil {
		t.Fatal("expected error for nonexistent addon")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error should mention addon name: %v", err)
	}
}
