package schema

// V2-cleanup-59: the envelope API group moved from sharko.io (never owned)
// to the maintainer-owned sharko.dev. READ-BOTH / EMIT-NEW. These tests pin
// the validator-level contract:
//
//   - old-group enveloped file → validates clean (schema enum accepts both)
//   - new-group enveloped file → validates clean
//   - unknown group            → rejected (not silently treated as valid)

import (
	"strings"
	"testing"
)

const compatManagedClustersBody = `kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: prod-eu
      labels:
        addon-datadog: enabled
`

func TestValidate_OldGroupEnvelopeValidatesClean(t *testing.T) {
	t.Parallel()
	v, err := NewValidator()
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	body := "apiVersion: " + APIVersionLegacy + "\n" + compatManagedClustersBody
	if err := v.Validate(KindManagedClusters, []byte(body)); err != nil {
		t.Fatalf("old-group (sharko.io/v1) envelope must validate clean: %v", err)
	}
}

func TestValidate_NewGroupEnvelopeValidatesClean(t *testing.T) {
	t.Parallel()
	v, err := NewValidator()
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	body := "apiVersion: " + APIVersion + "\n" + compatManagedClustersBody
	if err := v.Validate(KindManagedClusters, []byte(body)); err != nil {
		t.Fatalf("new-group (sharko.dev/v1) envelope must validate clean: %v", err)
	}
}

func TestValidate_UnknownGroupRejected(t *testing.T) {
	t.Parallel()
	v, err := NewValidator()
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	// A group Sharko has never used — the schema's apiVersion enum must
	// reject it rather than quietly passing.
	body := "apiVersion: example.com/v1\n" + compatManagedClustersBody
	if err := v.Validate(KindManagedClusters, []byte(body)); err == nil {
		t.Fatal("unknown apiVersion group must fail schema validation, got nil")
	}
}

func TestValidateAutoDetect_OldGroupAccepted_UnknownRejected(t *testing.T) {
	t.Parallel()
	v, err := NewValidator()
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}

	old := "apiVersion: " + APIVersionLegacy + "\n" + compatManagedClustersBody
	if err := v.ValidateAutoDetect([]byte(old)); err != nil {
		t.Fatalf("auto-detect must accept the legacy group: %v", err)
	}

	unknown := "apiVersion: example.com/v1\n" + compatManagedClustersBody
	err = v.ValidateAutoDetect([]byte(unknown))
	if err == nil {
		t.Fatal("auto-detect must reject an unknown group, got nil")
	}
	if !strings.Contains(err.Error(), "not an enveloped Sharko document") {
		t.Errorf("unexpected rejection message: %v", err)
	}
}
