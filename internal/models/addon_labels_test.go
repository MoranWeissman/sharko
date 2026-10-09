package models

import "testing"

func TestAddonLabelValue(t *testing.T) {
	if got := AddonLabelValue(true); got != LabelEnabled {
		t.Errorf("AddonLabelValue(true) = %q, want %q", got, LabelEnabled)
	}
	if got := AddonLabelValue(false); got != LabelDisabled {
		t.Errorf("AddonLabelValue(false) = %q, want %q", got, LabelDisabled)
	}
}

func TestAddonLabelEnabled(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"enabled", true},
		{"Enabled", true},
		{"ENABLED", true},
		{"disabled", false},
		{"true", false}, // legacy boolean is NOT on (must be normalized first)
		{"false", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := AddonLabelEnabled(tc.in); got != tc.want {
			t.Errorf("AddonLabelEnabled(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeAddonLabelValue(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"true", LabelEnabled, true},
		{"TRUE", LabelEnabled, true},
		{" true ", LabelEnabled, true},
		{"false", LabelDisabled, true},
		{"enabled", "enabled", false},   // already canonical, unchanged
		{"disabled", "disabled", false}, // already canonical, unchanged
		{"something-else", "something-else", false},
		{"", "", false},
	}
	for _, tc := range cases {
		got, ok := NormalizeAddonLabelValue(tc.in)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("NormalizeAddonLabelValue(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.wantOK)
		}
	}
}
