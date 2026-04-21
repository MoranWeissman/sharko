package catalog

import (
	"strings"
	"testing"
)

// TestLoad_Embedded parses the real embedded catalog and sanity-checks its
// shape. This is the smoke test that fails if anyone ships a bad entry.
func TestLoad_Embedded(t *testing.T) {
	cat, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cat.Len() == 0 {
		t.Fatalf("expected non-empty catalog")
	}
	if cat.Len() < 20 {
		t.Errorf("curated catalog unexpectedly small: %d entries", cat.Len())
	}
	// Spot-check a marquee entry present in the initial draft.
	if _, ok := cat.Get("cert-manager"); !ok {
		t.Errorf("expected cert-manager in catalog")
	}
	// V123-1.4: every loaded entry must carry Source="embedded". The `yaml:"-"`
	// tag on Source blocks YAML-level forgery; this asserts the loader itself
	// sets the sentinel on every entry. Skipping this check would let a future
	// refactor silently drop the attribution.
	for _, e := range cat.Entries() {
		if e.Source != SourceEmbedded {
			t.Errorf("entry %q: Source = %q, want %q", e.Name, e.Source, SourceEmbedded)
		}
	}
}

func TestLoadBytes_HappyPath(t *testing.T) {
	y := `
addons:
  - name: cert-manager
    description: TLS lifecycle.
    chart: cert-manager
    repo: https://charts.jetstack.io
    default_namespace: cert-manager
    maintainers: [jetstack]
    license: Apache-2.0
    category: security
    curated_by: [cncf-graduated]
  - name: grafana
    description: Visualisation.
    chart: grafana
    repo: https://grafana.github.io/helm-charts
    default_namespace: monitoring
    maintainers: [grafana]
    license: AGPL-3.0
    category: observability
    curated_by: [cncf-incubating, artifacthub-verified]
    security_score: 7.5
    security_score_updated: "2026-04-15"
    min_kubernetes_version: "1.24"
`
	cat, err := LoadBytes([]byte(y))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if cat.Len() != 2 {
		t.Fatalf("expected 2 entries, got %d", cat.Len())
	}
	// Sorted by name: cert-manager, grafana
	entries := cat.Entries()
	if entries[0].Name != "cert-manager" || entries[1].Name != "grafana" {
		t.Errorf("expected sorted [cert-manager, grafana], got [%s, %s]", entries[0].Name, entries[1].Name)
	}
	g, ok := cat.Get("grafana")
	if !ok {
		t.Fatalf("expected grafana lookup to succeed")
	}
	if !g.SecurityScore.Known || g.SecurityScore.Value != 7.5 {
		t.Errorf("grafana score: got %+v, want 7.5", g.SecurityScore)
	}
	if g.SecurityTier != "Moderate" {
		t.Errorf("grafana tier: got %q, want Moderate", g.SecurityTier)
	}
}

func TestLoadBytes_ErrorCases(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "missing name",
			yaml: `
addons:
  - description: x
    chart: x
    repo: https://x
    default_namespace: x
    maintainers: [m]
    license: Apache-2.0
    category: security
    curated_by: [cncf-graduated]
`,
			want: "missing required field: name",
		},
		{
			name: "duplicate name",
			yaml: `
addons:
  - name: foo
    description: x
    chart: x
    repo: https://x
    default_namespace: x
    maintainers: [m]
    license: Apache-2.0
    category: security
    curated_by: [cncf-graduated]
  - name: foo
    description: y
    chart: y
    repo: https://y
    default_namespace: y
    maintainers: [m]
    license: Apache-2.0
    category: security
    curated_by: [cncf-graduated]
`,
			want: "duplicate entry name",
		},
		{
			name: "unknown category",
			yaml: `
addons:
  - name: foo
    description: x
    chart: x
    repo: https://x
    default_namespace: x
    maintainers: [m]
    license: Apache-2.0
    category: nonsense
    curated_by: [cncf-graduated]
`,
			want: "category \"nonsense\" is not in the allowed set",
		},
		{
			name: "unknown curated_by tag",
			yaml: `
addons:
  - name: foo
    description: x
    chart: x
    repo: https://x
    default_namespace: x
    maintainers: [m]
    license: Apache-2.0
    category: security
    curated_by: [made-up-badge]
`,
			want: "curated_by tag \"made-up-badge\" is not in the allowed set",
		},
		{
			name: "bad repo scheme",
			yaml: `
addons:
  - name: foo
    description: x
    chart: x
    repo: ftp://x
    default_namespace: x
    maintainers: [m]
    license: Apache-2.0
    category: security
    curated_by: [cncf-graduated]
`,
			want: "repo must be http(s) or oci URL",
		},
		{
			name: "security_score out of range",
			yaml: `
addons:
  - name: foo
    description: x
    chart: x
    repo: https://x
    default_namespace: x
    maintainers: [m]
    license: Apache-2.0
    category: security
    curated_by: [cncf-graduated]
    security_score: 42
`,
			want: "security_score must be in [0,10]",
		},
		{
			name: "empty payload",
			yaml: ``,
			want: "catalog:",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadBytes([]byte(tc.yaml))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}

// TestLoadBytes_SourceAlwaysEmbedded_IgnoresYAMLForgery guards the critical
// security invariant from V123-1.4: the Source field has `yaml:"-"`, so a
// hostile third-party YAML cannot forge `source: embedded` to masquerade
// as curated. Even when the YAML payload tries to set Source to anything,
// the loader overwrites it with the embedded sentinel.
func TestLoadBytes_SourceAlwaysEmbedded_IgnoresYAMLForgery(t *testing.T) {
	y := `
addons:
  - name: forged
    description: x
    chart: x
    repo: https://x
    default_namespace: x
    maintainers: [m]
    license: Apache-2.0
    category: security
    curated_by: [cncf-graduated]
    source: "https://attacker.example.com/evil.yaml"
`
	cat, err := LoadBytes([]byte(y))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	e, ok := cat.Get("forged")
	if !ok {
		t.Fatalf("entry missing")
	}
	if e.Source != SourceEmbedded {
		t.Errorf("Source = %q, want %q — YAML-level forgery must be ignored", e.Source, SourceEmbedded)
	}
}

// Unknown YAML fields must NOT break parsing — design §4.2 requires forward
// compatibility so older Sharko binaries can parse newer catalog files.
func TestLoadBytes_TolerateUnknownFields(t *testing.T) {
	y := `
addons:
  - name: foo
    description: x
    chart: x
    repo: https://x
    default_namespace: x
    maintainers: [m]
    license: Apache-2.0
    category: security
    curated_by: [cncf-graduated]
    future_field_added_later: some-value
    another_nested:
      nested_key: 1
`
	cat, err := LoadBytes([]byte(y))
	if err != nil {
		t.Fatalf("expected tolerate-unknown to parse, got: %v", err)
	}
	if cat.Len() != 1 {
		t.Fatalf("expected 1 entry, got %d", cat.Len())
	}
}

func TestScoreValue_UnknownPermitted(t *testing.T) {
	y := `
addons:
  - name: foo
    description: x
    chart: x
    repo: https://x
    default_namespace: x
    maintainers: [m]
    license: Apache-2.0
    category: security
    curated_by: [cncf-graduated]
    security_score: unknown
`
	cat, err := LoadBytes([]byte(y))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	e, _ := cat.Get("foo")
	if e.SecurityScore.Known {
		t.Errorf("expected score to be unknown, got %+v", e.SecurityScore)
	}
	if e.SecurityTier != "" {
		t.Errorf("expected empty tier for unknown score, got %q", e.SecurityTier)
	}
}

func TestUpdateScore(t *testing.T) {
	y := `
addons:
  - name: foo
    description: x
    chart: x
    repo: https://x
    default_namespace: x
    maintainers: [m]
    license: Apache-2.0
    category: security
    curated_by: [cncf-graduated]
`
	cat, err := LoadBytes([]byte(y))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if ok := cat.UpdateScore("foo", 8.2, "2026-04-17"); !ok {
		t.Fatalf("UpdateScore for known name returned false")
	}
	e, _ := cat.Get("foo")
	if !e.SecurityScore.Known || e.SecurityScore.Value != 8.2 {
		t.Errorf("score after update: %+v", e.SecurityScore)
	}
	if e.SecurityTier != "Strong" {
		t.Errorf("tier after score 8.2: got %q, want Strong", e.SecurityTier)
	}
	if ok := cat.UpdateScore("does-not-exist", 1.0, "2026-04-17"); ok {
		t.Errorf("UpdateScore for unknown name should return false")
	}
}
