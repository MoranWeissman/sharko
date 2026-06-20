package orchestrator

import "testing"

// TestIsSharkoSystemApp verifies that IsSharkoSystemApp correctly identifies
// the bootstrap root Application and all per-cluster connectivity-check probes
// as Sharko system apps, while leaving real catalog addon names unclassified
// (V2-cleanup-52).
func TestIsSharkoSystemApp(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{name: "cluster-addons-bootstrap", want: true},
		{name: "connectivity-check-test-1", want: true},
		{name: "connectivity-check-foo", want: true},
		{name: "keda-prod", want: false},
		// "connectivity" without the trailing dash-segment is NOT the prefix.
		{name: "connectivity", want: false},
		{name: "", want: false},
	}

	for _, tc := range tests {
		got := IsSharkoSystemApp(tc.name)
		if got != tc.want {
			t.Errorf("IsSharkoSystemApp(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}
