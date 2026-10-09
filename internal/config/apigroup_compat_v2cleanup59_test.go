package config

// V2-cleanup-59: the envelope API group moved from sharko.io (never owned)
// to the maintainer-owned sharko.dev. READ-BOTH / EMIT-NEW. These tests pin
// the catalog + cluster parser acceptance of the OLD group and the writer's
// emission of ONLY the new group.

import (
	"strings"
	"testing"
)

const oldGroupCatalogYAML = `apiVersion: sharko.io/v1
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

func TestParseAddonsCatalog_OldGroupParsesAndValidatesClean(t *testing.T) {
	t.Parallel()
	entries, err := NewParser().ParseAddonsCatalog([]byte(oldGroupCatalogYAML))
	if err != nil {
		t.Fatalf("old-group (sharko.io/v1) catalog must keep parsing + validating clean: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "cert-manager" {
		t.Fatalf("old-group catalog parse produced wrong entries: %+v", entries)
	}
}

const oldGroupClustersYAML = `apiVersion: sharko.io/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: prod-eu
      labels:
        addon-datadog: enabled
`

func TestParseClusterAddons_OldGroupParsesAndValidatesClean(t *testing.T) {
	t.Parallel()
	clusters, err := NewParser().ParseClusterAddons([]byte(oldGroupClustersYAML))
	if err != nil {
		t.Fatalf("old-group (sharko.io/v1) managed-clusters must keep parsing + validating clean: %v", err)
	}
	if len(clusters) != 1 || clusters[0].Name != "prod-eu" {
		t.Fatalf("old-group clusters parse produced wrong result: %+v", clusters)
	}
}

func TestMarshalAddonCatalog_EmitsOnlyNewGroup(t *testing.T) {
	t.Parallel()
	// Round-trip an old-group catalog through the writer: the output must
	// carry ONLY the new group (EMIT-NEW), regardless of input group.
	entries, err := NewParser().ParseAddonsCatalog([]byte(oldGroupCatalogYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	body, err := MarshalAddonCatalog("addon-catalog", entries)
	if err != nil {
		t.Fatalf("MarshalAddonCatalog: %v", err)
	}
	out := string(body)
	if !strings.Contains(out, "apiVersion: sharko.dev/v1") {
		t.Errorf("writer must emit the new group; got:\n%s", out)
	}
	if strings.Contains(out, "sharko.io/v1") {
		t.Errorf("writer must NEVER emit the legacy group; got:\n%s", out)
	}
}
