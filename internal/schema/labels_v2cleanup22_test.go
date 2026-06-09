package schema

import "testing"

// V2-cleanup-22, Part 3 — the managed-clusters `labels` field is now an
// object with string values (map[string]string at the Go level → schema
// `{type: object, additionalProperties: {type: string}}`). Previously the
// field was `interface{}` → schema `true` (accept anything), so a scalar
// `labels: "oops"` passed validation and silently yielded zero addons.

const labelsObjectBody = `apiVersion: sharko.io/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: prod-eu
      labels:
        cert-manager: enabled
`

const labelsScalarBody = `apiVersion: sharko.io/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: prod-eu
      labels: oops
`

const labelsArrayBody = `apiVersion: sharko.io/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: prod-eu
      labels: []
`

// TestLabelsSchema_ObjectPasses: the legit object-with-string-values shape
// every Sharko producer emits must continue to validate.
func TestLabelsSchema_ObjectPasses(t *testing.T) {
	t.Parallel()
	v, err := DefaultValidator()
	if err != nil {
		t.Fatalf("DefaultValidator: %v", err)
	}
	if err := v.Validate(KindManagedClusters, []byte(labelsObjectBody)); err != nil {
		t.Fatalf("object-shaped labels should validate, got: %v", err)
	}
}

// TestLabelsSchema_ScalarRejected: the footgun is now closed — a scalar
// labels value fails schema validation.
func TestLabelsSchema_ScalarRejected(t *testing.T) {
	t.Parallel()
	v, err := DefaultValidator()
	if err != nil {
		t.Fatalf("DefaultValidator: %v", err)
	}
	if err := v.Validate(KindManagedClusters, []byte(labelsScalarBody)); err == nil {
		t.Fatal("scalar labels value should now FAIL validation, but it passed")
	}
}

// TestLabelsSchema_ArrayRejected: the legacy empty-array sentinel is no longer
// valid on the ENVELOPED path (no producer ever emitted it there; legacy bare
// YAML files that still carry `labels: []` skip schema validation by design).
func TestLabelsSchema_ArrayRejected(t *testing.T) {
	t.Parallel()
	v, err := DefaultValidator()
	if err != nil {
		t.Fatalf("DefaultValidator: %v", err)
	}
	if err := v.Validate(KindManagedClusters, []byte(labelsArrayBody)); err == nil {
		t.Fatal("array labels value should FAIL the object-typed schema, but it passed")
	}
}
