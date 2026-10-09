// Package orchestrator — tests for Story FS-1: the v4 cluster values
// stub pre-fills the ONE known fact (the cluster name) into any leaf
// whose final dotted-path segment is a cluster-name field, instead of
// the generic "<set per cluster>" placeholder. Every other
// cluster-specific leaf keeps rendering the placeholder unchanged.

package orchestrator

import (
	"strings"
	"testing"
)

// TestSeedV4ClusterValuesStubFromGlobal_FlatClusterNameFact covers the
// acceptance criterion: a flat `clusterName` leaf in the global template
// block becomes `# clusterName: prod-eu` in the seeded stub for cluster
// "prod-eu" — the real value, not the placeholder.
func TestSeedV4ClusterValuesStubFromGlobal_FlatClusterNameFact(t *testing.T) {
	upstream := []byte("clusterName: unset\nreplicaCount: 1\n")
	global := GenerateGlobalValuesFileV4("cert-manager", "cert-manager", "1.14.5", "https://charts.jetstack.io", upstream, false, false)

	got := string(seedV4ClusterValuesStubFromGlobal("cert-manager", "prod-eu", global))

	if !strings.Contains(got, "# clusterName: prod-eu\n") {
		t.Errorf("expected the known-fact hint line for the flat clusterName leaf, got:\n%s", got)
	}
	// replicaCount is not a known fact — stays the generic placeholder.
	if !strings.Contains(got, "# replicaCount: <set per cluster>\n") {
		t.Errorf("expected replicaCount to keep the generic placeholder, got:\n%s", got)
	}
}

// TestSeedV4ClusterValuesStubFromGlobal_NestedClusterNameFact covers the
// nested-leaf acceptance criterion: `config.clusterName` gets a
// quoted-key hint carrying the real value.
func TestSeedV4ClusterValuesStubFromGlobal_NestedClusterNameFact(t *testing.T) {
	upstream := []byte("config:\n  clusterName: unset\n  otherField: x\n")
	global := GenerateGlobalValuesFileV4("velero", "velero", "5.2.0", "https://vmware-tanzu.github.io/helm-charts", upstream, false, false)

	got := string(seedV4ClusterValuesStubFromGlobal("velero", "staging-us", global))

	if !strings.Contains(got, `# "config.clusterName": staging-us`+"\n") {
		t.Errorf("expected the quoted-key known-fact hint for config.clusterName, got:\n%s", got)
	}
}

// TestSeedV4ClusterValuesStubFromGlobal_CaseVariantPreservesCasing covers
// the case-variant acceptance criterion: a leaf spelled `ClusterName`
// (capital C) still gets recognized as the known fact, and the KEY keeps
// its original casing while the VALUE is the real cluster name.
func TestSeedV4ClusterValuesStubFromGlobal_CaseVariantPreservesCasing(t *testing.T) {
	upstream := []byte("ClusterName: unset\n")
	global := GenerateGlobalValuesFileV4("example-org-addon", "example-org-addon", "1.0.0", "https://example-org.test/charts", upstream, false, false)

	got := string(seedV4ClusterValuesStubFromGlobal("example-org-addon", "prod-eu", global))

	if !strings.Contains(got, "# ClusterName: prod-eu\n") {
		t.Errorf("expected the original ClusterName casing preserved with the real value, got:\n%s", got)
	}
	if strings.Contains(got, "clusterName: prod-eu") {
		t.Errorf("must not lowercase the leaf's original key casing, got:\n%s", got)
	}
}

// TestSeedV4ClusterValuesStubFromGlobal_NonFactLeavesByteIdentical proves
// the change is scoped to cluster-name leaves only: ingress.host,
// resources.requests.cpu, and a top-level region leaf all keep rendering
// the exact "<set per cluster>" placeholder they rendered before Story
// FS-1, byte for byte.
func TestSeedV4ClusterValuesStubFromGlobal_NonFactLeavesByteIdentical(t *testing.T) {
	upstream := []byte(`ingress:
  host: example.com
resources:
  requests:
    cpu: 100m
region: us-east-1
`)
	global := GenerateGlobalValuesFileV4("cert-manager", "cert-manager", "1.14.5", "https://charts.jetstack.io", upstream, false, false)

	got := string(seedV4ClusterValuesStubFromGlobal("cert-manager", "prod-eu", global))

	for _, want := range []string{
		`# "ingress.host": <set per cluster>` + "\n",
		`# "resources.requests.cpu": <set per cluster>` + "\n",
		"# region: <set per cluster>\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected unchanged placeholder line %q, got:\n%s", want, got)
		}
	}
	// The cluster name legitimately appears once, in the stub header
	// ("... on prod-eu only ..."). It must not also show up as a
	// substituted VALUE on any hint line — every hint line here is a
	// non-fact leaf, so every one of them must still read
	// "<set per cluster>", never "prod-eu".
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "# ") && strings.Contains(line, ": ") && strings.Contains(line, "prod-eu") {
			t.Errorf("non-fact hint line must not carry the real cluster name as a value, got line:\n%s", line)
		}
	}
}

// TestSeedV4ClusterValuesStubFromGlobal_NoGlobalContentPlainStub: no
// global file content at all still falls back to the plain W1 stub,
// unchanged by Story FS-1.
func TestSeedV4ClusterValuesStubFromGlobal_NoGlobalContentPlainStub(t *testing.T) {
	got := string(seedV4ClusterValuesStubFromGlobal("cert-manager", "prod-eu", nil))
	want := string(clusterValuesStub("cert-manager", "prod-eu"))
	if got != want {
		t.Errorf("expected byte-identical plain stub with no global content, got:\n%s\nwant:\n%s", got, want)
	}
}

// TestSeedV4ClusterValuesStubFromGlobal_NoTemplateBlockPlainStub: global
// content that exists but carries no per-cluster overrides template
// marker (e.g. a legacy file) also falls back to the plain stub.
func TestSeedV4ClusterValuesStubFromGlobal_NoTemplateBlockPlainStub(t *testing.T) {
	legacy := []byte("cert-manager:\n  enabled: true\n")
	got := string(seedV4ClusterValuesStubFromGlobal("cert-manager", "prod-eu", legacy))
	want := string(clusterValuesStub("cert-manager", "prod-eu"))
	if got != want {
		t.Errorf("expected byte-identical plain stub with no template block, got:\n%s\nwant:\n%s", got, want)
	}
}

// TestRenderV4TemplateLine_QuotesAmbiguousClusterName: a cluster literally
// named all-digits (a valid name under models.ResourceNamePattern) must be
// quoted in the substituted value so the hint stays a string once
// uncommented, instead of YAML parsing it as a number.
func TestRenderV4TemplateLine_QuotesAmbiguousClusterName(t *testing.T) {
	got := renderV4TemplateLine("clusterName", "12345")
	want := `clusterName: "12345"`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestRenderV4TemplateLine_OrdinaryNameUnquoted: an ordinary cluster name
// is not quoted — matches the plain style renderTemplateLine already
// uses for the placeholder value.
func TestRenderV4TemplateLine_OrdinaryNameUnquoted(t *testing.T) {
	got := renderV4TemplateLine("clusterName", "prod-eu")
	want := "clusterName: prod-eu"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
