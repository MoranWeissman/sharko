package notifications

import "testing"

func TestSemverGreater(t *testing.T) {
	tests := []struct {
		a, b     string
		expected bool
	}{
		{"2.0.0", "1.9.9", true},
		{"1.10.0", "1.9.0", true},
		{"1.2.3", "1.2.4", false},
		{"1.2.3", "1.2.3", false},
		{"v2.0.0", "v1.9.9", true},
		{"1.0.0-beta.1", "0.9.9", true},
		{"3.0.0", "2.0.0", true},
	}

	for _, tt := range tests {
		got := semverGreater(tt.a, tt.b)
		if got != tt.expected {
			t.Errorf("semverGreater(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.expected)
		}
	}
}
