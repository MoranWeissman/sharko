package catalog

import (
	"testing"
)

func loadFixture(t *testing.T) *Catalog {
	t.Helper()
	y := `
addons:
  - name: cert-manager
    description: Automated TLS.
    chart: cert-manager
    repo: https://charts.jetstack.io
    default_namespace: cert-manager
    maintainers: [jetstack]
    license: Apache-2.0
    category: security
    curated_by: [cncf-graduated, aws-eks-blueprints]
    security_score: 8.3
    min_kubernetes_version: "1.23"
  - name: kube-prometheus-stack
    description: Prometheus + Alertmanager + Grafana.
    chart: kube-prometheus-stack
    repo: https://prometheus-community.github.io/helm-charts
    default_namespace: monitoring
    maintainers: [prometheus-community]
    license: Apache-2.0
    category: observability
    curated_by: [cncf-graduated, aws-eks-blueprints, artifacthub-verified]
    security_score: 7.0
    min_kubernetes_version: "1.24"
  - name: vault
    description: HashiCorp secret manager.
    chart: vault
    repo: https://helm.releases.hashicorp.com
    default_namespace: vault
    maintainers: [hashicorp]
    license: BUSL-1.1
    category: security
    curated_by: [artifacthub-verified]
    security_score: unknown
    min_kubernetes_version: "1.23"
  - name: deprecated-thing
    description: Old.
    chart: deprecated-thing
    repo: https://x
    default_namespace: x
    maintainers: [m]
    license: Apache-2.0
    category: security
    curated_by: [cncf-sandbox]
    deprecated: true
`
	cat, err := LoadBytes([]byte(y))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	return cat
}

func TestList_NoFilters_HidesDeprecated(t *testing.T) {
	cat := loadFixture(t)
	got := cat.List(Query{})
	if len(got) != 3 {
		t.Fatalf("expected 3 non-deprecated entries, got %d", len(got))
	}
	for _, e := range got {
		if e.Name == "deprecated-thing" {
			t.Errorf("deprecated-thing should be hidden when IncludeDeprecated=false")
		}
	}
}

func TestList_IncludeDeprecated(t *testing.T) {
	cat := loadFixture(t)
	got := cat.List(Query{IncludeDeprecated: true})
	if len(got) != 4 {
		t.Fatalf("expected 4 entries with IncludeDeprecated=true, got %d", len(got))
	}
}

func TestList_Category(t *testing.T) {
	cat := loadFixture(t)
	got := cat.List(Query{Category: "security"})
	names := make([]string, 0, len(got))
	for _, e := range got {
		names = append(names, e.Name)
	}
	if len(got) != 2 { // cert-manager, vault; deprecated-thing excluded by default
		t.Fatalf("security category should yield 2 entries (got %d: %v)", len(got), names)
	}
}

func TestList_CuratedBy_All(t *testing.T) {
	cat := loadFixture(t)
	got := cat.List(Query{CuratedBy: []string{"cncf-graduated", "aws-eks-blueprints"}})
	if len(got) != 2 {
		t.Fatalf("expected 2 entries matching BOTH tags, got %d", len(got))
	}
	got = cat.List(Query{CuratedBy: []string{"cncf-graduated", "artifacthub-verified"}})
	if len(got) != 1 || got[0].Name != "kube-prometheus-stack" {
		t.Fatalf("expected only kube-prometheus-stack to carry both tags, got %+v", got)
	}
}

func TestList_License(t *testing.T) {
	cat := loadFixture(t)
	got := cat.List(Query{License: "apache-2.0"})
	if len(got) != 2 {
		t.Fatalf("expected 2 Apache-2.0 entries, got %d", len(got))
	}
}

func TestList_MinScore(t *testing.T) {
	cat := loadFixture(t)
	// MinScore=0 keeps unknown-score entries.
	got := cat.List(Query{MinScore: 0})
	if len(got) != 3 {
		t.Fatalf("MinScore=0 should include all non-deprecated (got %d)", len(got))
	}
	// MinScore=8 excludes unknown + below-threshold.
	got = cat.List(Query{MinScore: 8.0})
	if len(got) != 1 || got[0].Name != "cert-manager" {
		t.Fatalf("MinScore=8 should yield cert-manager only, got %+v", got)
	}
	// MinScore=5 includes both known scorers but NOT vault (unknown).
	got = cat.List(Query{MinScore: 5.0})
	if len(got) != 2 {
		t.Fatalf("MinScore=5 should yield 2 entries, got %d", len(got))
	}
	for _, e := range got {
		if e.Name == "vault" {
			t.Errorf("vault (unknown score) should be excluded when MinScore > 0")
		}
	}
}

func TestList_MinK8sVersion(t *testing.T) {
	cat := loadFixture(t)
	// 1.25 cluster: all 1.23 + 1.24 entries are compatible.
	got := cat.List(Query{MinK8sVersion: "1.25"})
	if len(got) != 3 {
		t.Fatalf("MinK8sVersion=1.25 should allow all; got %d", len(got))
	}
	// 1.23 cluster: entries requiring 1.24 are excluded.
	got = cat.List(Query{MinK8sVersion: "1.23"})
	names := map[string]bool{}
	for _, e := range got {
		names[e.Name] = true
	}
	if names["kube-prometheus-stack"] {
		t.Errorf("1.24-min entry must be excluded when cluster is 1.23")
	}
}

func TestList_TextSearch(t *testing.T) {
	cat := loadFixture(t)
	got := cat.List(Query{Q: "cert"})
	if len(got) != 1 || got[0].Name != "cert-manager" {
		t.Fatalf("text search 'cert' should match cert-manager; got %+v", got)
	}
	got = cat.List(Query{Q: "prometheus"})
	if len(got) != 1 || got[0].Name != "kube-prometheus-stack" {
		t.Fatalf("text search 'prometheus' should match kube-prometheus-stack; got %+v", got)
	}
	got = cat.List(Query{Q: "HASHICORP"}) // case-insensitive, matches maintainer
	if len(got) != 1 || got[0].Name != "vault" {
		t.Fatalf("text search should match case-insensitive maintainer; got %+v", got)
	}
}

func TestList_CombinedFilters(t *testing.T) {
	cat := loadFixture(t)
	got := cat.List(Query{
		Q:        "cert",
		Category: "security",
		MinScore: 7.0,
	})
	if len(got) != 1 || got[0].Name != "cert-manager" {
		t.Fatalf("combined filters: got %+v", got)
	}
}

// TestListFrom_PureFilter covers the V123-1.4 extraction: ListFrom must
// apply the same predicates as Catalog.List when given a synthetic slice.
// We exercise a mix of filters against a hand-built []CatalogEntry that
// doesn't live inside a *Catalog — this mirrors how the api layer uses
// ListFrom on the merged embedded+third-party view.
func TestListFrom_PureFilter(t *testing.T) {
	entries := []CatalogEntry{
		{
			Name:             "cert-manager",
			Description:      "TLS.",
			Chart:            "cert-manager",
			Repo:             "https://charts.jetstack.io",
			DefaultNamespace: "cert-manager",
			Maintainers:      []string{"jetstack"},
			License:          "Apache-2.0",
			Category:         "security",
			CuratedBy:        []string{"cncf-graduated"},
			SecurityScore:    ScoreValue{Known: true, Value: 8.3},
			Source:           SourceEmbedded,
		},
		{
			Name:             "internal-foo",
			Description:      "Proprietary internal addon.",
			Chart:            "foo",
			Repo:             "https://internal.example.com/charts",
			DefaultNamespace: "foo",
			Maintainers:      []string{"platform"},
			License:          "Apache-2.0",
			Category:         "networking",
			CuratedBy:        []string{"artifacthub-verified"},
			Source:           "https://internal.example.com/catalog.yaml",
		},
		{
			Name:             "deprecated-thing",
			Description:      "old.",
			Chart:            "x",
			Repo:             "https://x",
			DefaultNamespace: "x",
			Maintainers:      []string{"m"},
			License:          "Apache-2.0",
			Category:         "security",
			CuratedBy:        []string{"cncf-sandbox"},
			Deprecated:       true,
			Source:           SourceEmbedded,
		},
	}

	// No filters → 2 entries (deprecated hidden), sorted by Name.
	got := ListFrom(entries, Query{})
	if len(got) != 2 {
		t.Fatalf("expected 2 non-deprecated entries, got %d", len(got))
	}
	if got[0].Name != "cert-manager" || got[1].Name != "internal-foo" {
		t.Errorf("expected sorted [cert-manager, internal-foo], got [%s, %s]", got[0].Name, got[1].Name)
	}
	// Source must round-trip through the filter — ListFrom mustn't mutate.
	if got[0].Source != SourceEmbedded {
		t.Errorf("embedded entry lost Source: %q", got[0].Source)
	}
	if got[1].Source != "https://internal.example.com/catalog.yaml" {
		t.Errorf("third-party entry lost Source: %q", got[1].Source)
	}

	// Category filter narrows to cert-manager only.
	got = ListFrom(entries, Query{Category: "security"})
	if len(got) != 1 || got[0].Name != "cert-manager" {
		t.Fatalf("category filter: got %+v", got)
	}

	// Text search is case-insensitive on description.
	got = ListFrom(entries, Query{Q: "INTERNAL"})
	if len(got) != 1 || got[0].Name != "internal-foo" {
		t.Fatalf("text search: got %+v", got)
	}

	// Nil input returns nil.
	if got := ListFrom(nil, Query{}); got != nil {
		t.Errorf("ListFrom(nil) = %+v, want nil", got)
	}

	// IncludeDeprecated=true brings deprecated back in.
	got = ListFrom(entries, Query{IncludeDeprecated: true})
	if len(got) != 3 {
		t.Fatalf("IncludeDeprecated=true: got %d, want 3", len(got))
	}
}

func TestCompareK8sVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.23", "1.24", -1},
		{"1.24", "1.23", 1},
		{"1.23", "1.23", 0},
		{"1.23.4", "1.23", 1},
		{"1.23", "1.23.0", 0},
		{"v1.25", "1.25", 0}, // leading v tolerated
	}
	for _, tc := range cases {
		if got := compareK8sVersion(tc.a, tc.b); got != tc.want {
			t.Errorf("compareK8sVersion(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
