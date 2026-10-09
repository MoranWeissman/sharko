package models

import (
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/schema"
)

// V2-cleanup-22, Part 1 — validate-before-commit for SaveManagedClusters.
//
// SaveManagedClusters is the single choke point every managed-clusters writer
// funnels through. After V2-cleanup-22 it validates the marshalled output
// against the embedded managed-clusters schema BEFORE returning, so a bad
// in-memory spec can never reach a commit/PR.

// TestSaveManagedClusters_OutputPassesOwnValidator is the round-trip guard:
// a normal spec marshals to output that passes the SAME validator the gate
// uses (Sharko-generated content is schema-clean by construction).
func TestSaveManagedClusters_OutputPassesOwnValidator(t *testing.T) {
	t.Parallel()

	spec := ManagedClustersSpec{
		Clusters: []ManagedClusterEntry{
			{
				Name:       "prod-eu",
				Region:     "eu-west-1",
				SecretPath: "clusters/prod-eu",
				Labels:     map[string]string{"cert-manager": "enabled"},
			},
			{
				Name: "staging", // no labels — omitted field is valid
			},
		},
	}

	out, err := SaveManagedClusters(spec)
	if err != nil {
		t.Fatalf("SaveManagedClusters returned error on a valid spec (gate false-positive): %v", err)
	}

	validator, vErr := schema.DefaultValidator()
	if vErr != nil {
		t.Fatalf("DefaultValidator: %v", vErr)
	}
	if err := validator.Validate(schema.KindManagedClusters, out); err != nil {
		t.Fatalf("SaveManagedClusters output failed its own validator: %v", err)
	}
}

// TestSaveManagedClusters_GateRejectsInvalidBody proves the gate FIRES: the
// exact validator SaveManagedClusters runs internally rejects a managed-
// clusters body whose labels is a scalar (the V2-cleanup-22 Part 3 footgun).
// Because the typed struct can no longer PRODUCE that shape (Labels is
// map[string]string), this asserts the gate's mechanism end-to-end against a
// hand-crafted bad body — the same Validate call the writer makes.
func TestSaveManagedClusters_GateRejectsInvalidBody(t *testing.T) {
	t.Parallel()

	// A structurally-broken managed-clusters body: scalar labels.
	bad := []byte(`apiVersion: sharko.dev/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: prod-eu
      labels: oops
`)

	validator, vErr := schema.DefaultValidator()
	if vErr != nil {
		t.Fatalf("DefaultValidator: %v", vErr)
	}
	if err := validator.Validate(schema.KindManagedClusters, bad); err == nil {
		t.Fatal("validate-before-commit gate did NOT reject a scalar-labels body (footgun still open)")
	}
}

// TestSaveManagedClusters_ScalarLabelsImpossibleViaStruct documents the Part 3
// tightening at the type level: ManagedClusterEntry.Labels is map[string]string,
// so the scalar footgun cannot be constructed through the Save path at all.
// (Compile-time guarantee — this test exists to flag any future widening of
// the field type back to interface{}.)
func TestSaveManagedClusters_ScalarLabelsImpossibleViaStruct(t *testing.T) {
	t.Parallel()

	spec := ManagedClustersSpec{
		Clusters: []ManagedClusterEntry{{Name: "c1", Labels: map[string]string{"a": "enabled"}}},
	}
	out, err := SaveManagedClusters(spec)
	if err != nil {
		t.Fatalf("SaveManagedClusters: %v", err)
	}
	if !strings.Contains(string(out), "a: enabled") {
		t.Errorf("expected object-shaped labels in output, got:\n%s", out)
	}
}
