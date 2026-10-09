// Tests for the legacy `<addon>:` wrap detector + unwrapper used by the
// v1.21 Bundle 5 migration endpoint.

package orchestrator

import (
	"strings"
	"testing"
)

func TestUnwrapGlobalValuesFile_WrappedAddonNameRoot(t *testing.T) {
	in := []byte(`# Helm values for cert-manager
cert-manager:
  installCRDs: true
  replicaCount: 2
  ingress:
    enabled: false
`)
	out, wrapped, err := UnwrapGlobalValuesFile(in, "cert-manager", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !wrapped {
		t.Fatal("expected wrapped=true for legacy file")
	}
	body := string(out)
	for _, line := range strings.Split(body, "\n") {
		if line == "cert-manager:" {
			t.Fatalf("root key not removed:\n%s", body)
		}
	}
	for _, want := range []string{"installCRDs: true", "replicaCount: 2", "ingress:", "  enabled: false"} {
		if !strings.Contains(body, want) {
			t.Errorf("expected %q in unwrapped body, got:\n%s", want, body)
		}
	}
	// File header comment is preserved.
	if !strings.Contains(body, "# Helm values for cert-manager") {
		t.Errorf("expected file header comment preserved, got:\n%s", body)
	}
}

func TestUnwrapGlobalValuesFile_WrappedChartName(t *testing.T) {
	// The user's addon may be named `velero-prod` while the chart is `velero`.
	in := []byte(`velero:
  configuration:
    backupStorageLocation: []
`)
	out, wrapped, err := UnwrapGlobalValuesFile(in, "velero-prod", "velero")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !wrapped {
		t.Fatal("expected wrapped=true when chart name matches root")
	}
	if !strings.HasPrefix(string(out), "configuration:") {
		t.Errorf("expected first line `configuration:` after unwrap, got:\n%s", string(out))
	}
}

func TestUnwrapGlobalValuesFile_AlreadyUnwrapped(t *testing.T) {
	in := []byte(`# Helm values for cert-manager — applied to all clusters
installCRDs: true
replicaCount: 2
`)
	out, wrapped, err := UnwrapGlobalValuesFile(in, "cert-manager", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wrapped {
		t.Errorf("expected wrapped=false for already-unwrapped file")
	}
	if string(out) != string(in) {
		t.Errorf("unwrapped no-op should return input verbatim, diff:\n%s\nvs\n%s", string(out), string(in))
	}
}

func TestUnwrapGlobalValuesFile_MultiKeyRoot(t *testing.T) {
	// Two top-level keys → not a wrap pattern, must be a no-op.
	in := []byte(`installCRDs: true
ingress:
  enabled: true
`)
	out, wrapped, err := UnwrapGlobalValuesFile(in, "cert-manager", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wrapped {
		t.Errorf("multi-key root must NOT be detected as wrapped, got wrapped=true")
	}
	if string(out) != string(in) {
		t.Errorf("multi-key file must be returned verbatim")
	}
}

func TestUnwrapGlobalValuesFile_WrongRootKey(t *testing.T) {
	// Single root key but it does NOT match the addon or chart name.
	in := []byte(`global:
  foo: bar
`)
	out, wrapped, err := UnwrapGlobalValuesFile(in, "cert-manager", "cert-manager")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wrapped {
		t.Errorf("unrelated single root must NOT be flagged as wrap")
	}
	if string(out) != string(in) {
		t.Errorf("unrelated file must be returned verbatim")
	}
}

func TestUnwrapGlobalValuesFile_CommentOnly(t *testing.T) {
	in := []byte(`# Helm values for cert-manager
# (no values configured yet)
`)
	out, wrapped, err := UnwrapGlobalValuesFile(in, "cert-manager", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wrapped {
		t.Errorf("comment-only file must NOT be flagged")
	}
	if string(out) != string(in) {
		t.Errorf("comment-only file must be returned verbatim")
	}
}

func TestUnwrapGlobalValuesFile_EmptyInput(t *testing.T) {
	out, wrapped, err := UnwrapGlobalValuesFile([]byte{}, "cert-manager", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wrapped {
		t.Errorf("empty input must NOT be flagged")
	}
	if len(out) != 0 {
		t.Errorf("empty input must return empty output")
	}
}

func TestUnwrapGlobalValuesFile_PreservesInlineComments(t *testing.T) {
	in := []byte(`cert-manager:
  installCRDs: true  # required for the chart
  # replicaCount controls horizontal scaling
  replicaCount: 2
`)
	out, wrapped, err := UnwrapGlobalValuesFile(in, "cert-manager", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !wrapped {
		t.Fatal("expected wrap detection")
	}
	body := string(out)
	if !strings.Contains(body, "installCRDs: true  # required for the chart") {
		t.Errorf("expected inline comment preserved, got:\n%s", body)
	}
	if !strings.Contains(body, "# replicaCount controls horizontal scaling") {
		t.Errorf("expected indented comment preserved, got:\n%s", body)
	}
}

func TestUnwrapGlobalValuesFile_RequiresAddonName(t *testing.T) {
	_, _, err := UnwrapGlobalValuesFile([]byte(`foo: bar`), "", "")
	if err == nil {
		t.Error("expected error when expectedAddonName is empty")
	}
}

func TestDetectLegacyWrap(t *testing.T) {
	wrapped := []byte(`cert-manager:
  installCRDs: true
`)
	unwrapped := []byte(`installCRDs: true
replicaCount: 2
`)
	if !DetectLegacyWrap(wrapped, "cert-manager", "") {
		t.Error("expected DetectLegacyWrap=true on wrapped file")
	}
	if DetectLegacyWrap(unwrapped, "cert-manager", "") {
		t.Error("expected DetectLegacyWrap=false on unwrapped file")
	}
}
