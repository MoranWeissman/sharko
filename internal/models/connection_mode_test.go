package models

import (
	"strings"
	"testing"
)

// V2-cleanup-57.2 — connectionManagedBy field semantics.

func TestIsUserManagedConnection(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"", false},        // absent == sharko-managed default
		{"sharko", false},  // explicit default
		{"user", true},     // self-managed
		{"User", true},     // case-insensitive read tolerance (legacy bare path)
		{"owner", false},   // unknown → fail-safe default
		{"userland", false},
	}
	for _, c := range cases {
		if got := IsUserManagedConnection(c.value); got != c.want {
			t.Errorf("IsUserManagedConnection(%q) = %v, want %v", c.value, got, c.want)
		}
	}
}

func TestManagedClusters_RoundTrip_ConnectionManagedBy(t *testing.T) {
	spec := ManagedClustersSpec{Clusters: []ManagedClusterEntry{
		{Name: "byo", ConnectionManagedBy: ConnectionManagedByUser, Labels: ClusterLabels{"monitoring": "enabled"}},
		{Name: "default-mode"},
	}}
	body, err := SaveManagedClusters(spec)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	// The self-managed entry carries the field; the default entry omits it
	// (absent == sharko; pre-57.2 files stay byte-identical on re-emit).
	s := string(body)
	if !strings.Contains(s, "connectionManagedBy: user") {
		t.Fatalf("emitted YAML missing connectionManagedBy: user:\n%s", s)
	}
	if strings.Count(s, "connectionManagedBy") != 1 {
		t.Fatalf("default-mode entry must omit connectionManagedBy:\n%s", s)
	}

	loaded, err := LoadManagedClusters(body)
	if err != nil {
		t.Fatalf("load (schema validation on the enveloped path): %v", err)
	}
	if got := loaded.Clusters[0].ConnectionManagedBy; got != ConnectionManagedByUser {
		t.Fatalf("round-trip lost the mode: %q", got)
	}
	if !loaded.Clusters[0].UserManagedConnection() {
		t.Fatal("UserManagedConnection() must be true after round-trip")
	}
	if loaded.Clusters[1].UserManagedConnection() {
		t.Fatal("entry without the field must default to sharko-managed")
	}
}

func TestManagedClusters_SchemaRejectsUnknownMode(t *testing.T) {
	// The generated JSON Schema pins the enum: a typo'd mode on an
	// ENVELOPED file must fail loudly at read time instead of silently
	// defaulting to Sharko-managed ownership of the user's connection.
	body := []byte(`apiVersion: sharko.io/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: typo-cluster
      connectionManagedBy: owner
`)
	if _, err := LoadManagedClusters(body); err == nil {
		t.Fatal("enveloped file with connectionManagedBy: owner must fail schema validation")
	}
}

func TestConnectionManagedByFor(t *testing.T) {
	clusters := []Cluster{
		{Name: "a", ConnectionManagedBy: ConnectionManagedByUser},
		{Name: "b"},
	}
	if got := ConnectionManagedByFor(clusters, "a"); got != ConnectionManagedByUser {
		t.Fatalf("want user, got %q", got)
	}
	if got := ConnectionManagedByFor(clusters, "b"); got != "" {
		t.Fatalf("want empty (sharko default), got %q", got)
	}
	if got := ConnectionManagedByFor(clusters, "missing"); got != "" {
		t.Fatalf("missing cluster must default to empty, got %q", got)
	}
}
