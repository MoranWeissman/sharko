package api

import (
	"testing"
)

func TestParseSemver(t *testing.T) {
	tests := []struct {
		input     string
		wantMajor int
		wantMinor int
		wantPatch int
		wantPre   string
		wantErr   bool
	}{
		{"1.2.3", 1, 2, 3, "", false},
		{"v1.2.3", 1, 2, 3, "", false},
		{"1.14.0", 1, 14, 0, "", false},
		{"2.0.0-alpha.1", 2, 0, 0, "alpha.1", false},
		{"1.2", 1, 2, 0, "", false},
		{"1", 1, 0, 0, "", false},
		{"v0.6.0", 0, 6, 0, "", false},
		{"invalid", 0, 0, 0, "", true},
		{"1.x.0", 0, 0, 0, "", true},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got, err := parseSemver(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Errorf("parseSemver(%q): expected error, got nil", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSemver(%q): unexpected error: %v", tc.input, err)
			}
			if got.major != tc.wantMajor || got.minor != tc.wantMinor || got.patch != tc.wantPatch {
				t.Errorf("parseSemver(%q): got {%d,%d,%d}, want {%d,%d,%d}",
					tc.input, got.major, got.minor, got.patch, tc.wantMajor, tc.wantMinor, tc.wantPatch)
			}
			if got.pre != tc.wantPre {
				t.Errorf("parseSemver(%q): pre=%q, want %q", tc.input, got.pre, tc.wantPre)
			}
		})
	}
}

func TestCompareSemver(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.14.0", "1.15.0", -1},
		{"1.15.0", "1.14.0", 1},
		{"1.14.0", "1.14.0", 0},
		{"2.0.0", "1.9.9", 1},
		{"1.0.0-alpha", "1.0.0", -1},
		{"1.0.0", "1.0.0-beta", 1},
		{"1.0.0-alpha", "1.0.0-beta", -1},
		{"v1.2.3", "1.2.3", 0},
	}

	for _, tc := range tests {
		t.Run(tc.a+"_vs_"+tc.b, func(t *testing.T) {
			got, err := compareSemver(tc.a, tc.b)
			if err != nil {
				t.Fatalf("compareSemver(%q, %q): unexpected error: %v", tc.a, tc.b, err)
			}
			if got != tc.want {
				t.Errorf("compareSemver(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestCompareSemver_InvalidInput(t *testing.T) {
	_, err := compareSemver("not-a-version", "1.2.3")
	if err == nil {
		t.Error("expected error for invalid semver input")
	}
}
