package gitops

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// cluster-addons.yaml fixtures
// ---------------------------------------------------------------------------

const clusterAddonsYAML = `# Cluster addons configuration
clusters:
  - name: feedlot-dev
    labels:
      datadog: enabled
      datadog-version: "3.70.7"
      keda: disabled
  - name: ark-dev-eks
    labels:
      datadog: enabled
      # keda not yet rolled out
  - name: staging-01
    labels:
      datadog: enabled
      keda: enabled
`

// ---------------------------------------------------------------------------
// EnableAddonLabel tests
// ---------------------------------------------------------------------------

func TestEnableAddonLabel_ExistingDisabled(t *testing.T) {
	out, err := EnableAddonLabel([]byte(clusterAddonsYAML), "feedlot-dev", "keda")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := string(out)
	// keda should now be enabled for feedlot-dev
	if !strings.Contains(s, "      keda: enabled") {
		t.Errorf("expected keda: enabled for feedlot-dev, got:\n%s", s)
	}
	// other clusters untouched — staging-01 still has keda: enabled (was already)
	if !strings.Contains(s, "  - name: staging-01") {
		t.Errorf("staging-01 cluster missing")
	}
	// datadog line for feedlot-dev still present
	if !strings.Contains(s, "      datadog: enabled") {
		t.Errorf("datadog label missing")
	}
}

func TestEnableAddonLabel_AddNewLabel(t *testing.T) {
	out, err := EnableAddonLabel([]byte(clusterAddonsYAML), "ark-dev-eks", "keda")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := string(out)
	// The new label should appear inside ark-dev-eks's labels block
	lines := strings.Split(s, "\n")
	inArk := false
	inLabels := false
	found := false
	for _, line := range lines {
		if strings.Contains(line, "- name: ark-dev-eks") {
			inArk = true
		} else if inArk && strings.Contains(line, "- name:") {
			break // next cluster
		}
		if inArk && strings.TrimSpace(line) == "labels:" {
			inLabels = true
		}
		if inArk && inLabels && strings.TrimSpace(line) == "keda: enabled" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected keda: enabled added to ark-dev-eks labels:\n%s", s)
	}
	// comment preserved
	if !strings.Contains(s, "# keda not yet rolled out") {
		t.Errorf("comment was lost")
	}
}

func TestEnableAddonLabel_AlreadyEnabled(t *testing.T) {
	out, err := EnableAddonLabel([]byte(clusterAddonsYAML), "feedlot-dev", "datadog")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// should be idempotent
	if string(out) != clusterAddonsYAML {
		t.Errorf("expected no change when already enabled")
	}
}

func TestDisableAddonLabel_ExistingEnabled(t *testing.T) {
	out, err := DisableAddonLabel([]byte(clusterAddonsYAML), "feedlot-dev", "datadog")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := string(out)
	// Must change only feedlot-dev's datadog, not ark-dev-eks's
	lines := strings.Split(s, "\n")
	inFeedlot := false
	for _, line := range lines {
		if strings.Contains(line, "- name: feedlot-dev") {
			inFeedlot = true
		} else if strings.Contains(line, "- name: ark-dev-eks") {
			inFeedlot = false
		}
		if inFeedlot && strings.TrimSpace(line) == "datadog: disabled" {
			// good
		}
	}
	// feedlot-dev datadog disabled
	if !containsInCluster(s, "feedlot-dev", "datadog: disabled") {
		t.Errorf("expected datadog: disabled in feedlot-dev:\n%s", s)
	}
	// ark-dev-eks datadog still enabled
	if !containsInCluster(s, "ark-dev-eks", "datadog: enabled") {
		t.Errorf("expected datadog: enabled in ark-dev-eks (untouched):\n%s", s)
	}
}

func TestEnableAddonLabel_ClusterNotFound(t *testing.T) {
	_, err := EnableAddonLabel([]byte(clusterAddonsYAML), "nonexistent", "keda")
	if err == nil {
		t.Fatal("expected error for nonexistent cluster")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error should mention cluster name: %v", err)
	}
}

func TestDisableAddonLabel_ClusterNotFound(t *testing.T) {
	_, err := DisableAddonLabel([]byte(clusterAddonsYAML), "nonexistent", "keda")
	if err == nil {
		t.Fatal("expected error for nonexistent cluster")
	}
}

func TestPreserveComments(t *testing.T) {
	out, err := DisableAddonLabel([]byte(clusterAddonsYAML), "feedlot-dev", "keda")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "# Cluster addons configuration") {
		t.Errorf("top-level comment lost")
	}
	if !strings.Contains(s, "# keda not yet rolled out") {
		t.Errorf("inline comment lost")
	}
}

func TestPreserveOtherClusters(t *testing.T) {
	out, err := EnableAddonLabel([]byte(clusterAddonsYAML), "feedlot-dev", "keda")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := string(out)
	// ark-dev-eks should remain identical
	if !strings.Contains(s, "  - name: ark-dev-eks\n    labels:\n      datadog: enabled\n      # keda not yet rolled out") {
		t.Errorf("ark-dev-eks cluster was modified:\n%s", s)
	}
}

// ---------------------------------------------------------------------------
// addons-catalog.yaml fixtures
// ---------------------------------------------------------------------------

const addonsCatalogYAML = `# Addons catalog
applicationsets:
  - appName: datadog
    repoURL: https://helm.datadoghq.com
    chart: datadog
    version: 3.160.1
  - appName: keda
    repoURL: https://kedacore.github.io/charts
    chart: keda
    version: 2.14.2
`

func TestUpdateCatalogVersion_Existing(t *testing.T) {
	out, err := UpdateCatalogVersion([]byte(addonsCatalogYAML), "datadog", "3.170.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "    version: 3.170.0") {
		t.Errorf("expected version 3.170.0 for datadog:\n%s", s)
	}
	// keda version untouched
	if !strings.Contains(s, "    version: 2.14.2") {
		t.Errorf("keda version was modified:\n%s", s)
	}
	// comment preserved
	if !strings.Contains(s, "# Addons catalog") {
		t.Errorf("comment lost")
	}
}

func TestUpdateCatalogVersion_NotFound(t *testing.T) {
	_, err := UpdateCatalogVersion([]byte(addonsCatalogYAML), "nonexistent", "1.0.0")
	if err == nil {
		t.Fatal("expected error for nonexistent addon")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error should mention addon name: %v", err)
	}
}

func TestUpdateCatalogVersion_PreservesComments(t *testing.T) {
	out, err := UpdateCatalogVersion([]byte(addonsCatalogYAML), "keda", "2.15.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "# Addons catalog") {
		t.Errorf("comment lost")
	}
	// datadog untouched
	if !strings.Contains(s, "    version: 3.160.1") {
		t.Errorf("datadog version was modified")
	}
}

// ---------------------------------------------------------------------------
// labels: [] (empty array) tests
// ---------------------------------------------------------------------------

func TestEnableAddonLabel_EmptyArray(t *testing.T) {
	input := `clusters:
  - name: test-cluster
    labels: []
`
	result, err := EnableAddonLabel([]byte(input), "test-cluster", "keda")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := string(result)
	// Should produce labels block with keda: enabled
	if !strings.Contains(s, "    labels:\n      keda: enabled") {
		t.Errorf("expected labels block with keda: enabled, got:\n%s", s)
	}
	// Should NOT contain []
	if strings.Contains(s, "[]") {
		t.Errorf("expected [] to be removed, got:\n%s", s)
	}
}

func TestDisableAddonLabel_EmptyArray(t *testing.T) {
	input := `clusters:
  - name: test-cluster
    labels: []
`
	result, err := DisableAddonLabel([]byte(input), "test-cluster", "keda")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := string(result)
	if !strings.Contains(s, "    labels:\n      keda: disabled") {
		t.Errorf("expected labels block with keda: disabled, got:\n%s", s)
	}
}

// ---------------------------------------------------------------------------
// commented-out label tests
// ---------------------------------------------------------------------------

func TestEnableAddonLabel_CommentedOut(t *testing.T) {
	input := `clusters:
  - name: test-cluster
    labels:
      datadog: enabled
      # keda: disabled
`
	result, err := EnableAddonLabel([]byte(input), "test-cluster", "keda")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := string(result)
	// Should uncomment and set to enabled
	if !containsInCluster(s, "test-cluster", "keda: enabled") {
		t.Errorf("expected keda: enabled (uncommented), got:\n%s", s)
	}
	// Should NOT contain the commented version
	if strings.Contains(s, "# keda") {
		t.Errorf("expected comment to be removed, got:\n%s", s)
	}
	// datadog still there
	if !containsInCluster(s, "test-cluster", "datadog: enabled") {
		t.Errorf("expected datadog: enabled to be preserved, got:\n%s", s)
	}
}

func TestDisableAddonLabel_CommentedOutEnabled(t *testing.T) {
	input := `clusters:
  - name: test-cluster
    labels:
      datadog: enabled
      # keda: enabled
`
	result, err := DisableAddonLabel([]byte(input), "test-cluster", "keda")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := string(result)
	if !containsInCluster(s, "test-cluster", "keda: disabled") {
		t.Errorf("expected keda: disabled (uncommented), got:\n%s", s)
	}
	if strings.Contains(s, "# keda") {
		t.Errorf("expected comment to be removed, got:\n%s", s)
	}
}

// ---------------------------------------------------------------------------
// helper
// ---------------------------------------------------------------------------

// containsInCluster checks that a given line (trimmed) appears between
// "- name: <cluster>" and the next "- name:" block.
func containsInCluster(yaml, clusterName, needle string) bool {
	lines := strings.Split(yaml, "\n")
	inCluster := false
	for _, line := range lines {
		if strings.Contains(line, "- name: "+clusterName) {
			inCluster = true
			continue
		}
		if inCluster && strings.Contains(line, "- name:") {
			return false
		}
		if inCluster && strings.Contains(strings.TrimSpace(line), needle) {
			return true
		}
	}
	return false
}
