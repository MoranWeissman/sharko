//go:build e2e

package lifecycle

import "testing"

// TestBuildTLSClientConfig_InsecureAndCAData is a pure, in-process unit
// test for buildTLSClientConfig (cluster_helpers.go) — no kind cluster or
// Docker needed, so it also runs under `make test-e2e-fast`.
//
// It is the regression guard for task #49 round 2: registerClusterInArgoCDDirect
// used to send "insecure": true AND a non-empty "caData" in the same
// tlsClientConfig block, contradicting the mutual-exclusion rule Sharko's
// own production RegisterCluster (internal/argocd/client_write.go) always
// follows — caData present pairs only with insecure:false.
func TestBuildTLSClientConfig_InsecureAndCAData(t *testing.T) {
	tests := []struct {
		name      string
		insecure  bool
		caDataB64 string
	}{
		{name: "insecure_true_drops_caData_even_when_supplied", insecure: true, caDataB64: "c29tZS1jYS1kYXRh"},
		{name: "insecure_true_no_caData_supplied", insecure: true, caDataB64: ""},
		{name: "secure_keeps_caData", insecure: false, caDataB64: "c29tZS1jYS1kYXRh"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildTLSClientConfig(tt.insecure, tt.caDataB64)

			gotInsecure, _ := got["insecure"].(bool)
			if gotInsecure != tt.insecure {
				t.Errorf("insecure = %v, want %v", gotInsecure, tt.insecure)
			}

			_, hasCAData := got["caData"]
			if tt.insecure {
				if hasCAData {
					t.Errorf("insecure:true payload must not carry caData, got %v", got)
				}
				return
			}
			if !hasCAData {
				t.Fatalf("insecure:false payload must carry caData, got %v", got)
			}
			if got["caData"] != tt.caDataB64 {
				t.Errorf("caData = %v, want %v", got["caData"], tt.caDataB64)
			}
		})
	}
}

// TestBuildTLSClientConfig_NeverBothFields is a direct assertion of the
// contract's name: for every combination this helper can produce, the
// result never has both "insecure": true and a non-empty "caData" key set
// at once.
func TestBuildTLSClientConfig_NeverBothFields(t *testing.T) {
	for _, insecure := range []bool{true, false} {
		got := buildTLSClientConfig(insecure, "c29tZS1jYS1kYXRh")
		gotInsecure, _ := got["insecure"].(bool)
		_, hasCAData := got["caData"]
		if gotInsecure && hasCAData {
			t.Fatalf("buildTLSClientConfig(%v, ...) = %v — insecure:true and caData must never both be present", insecure, got)
		}
	}
}
