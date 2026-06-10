package models

import (
	"testing"
)

func TestApplyConnectivityCheckLabel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		input      map[string]string
		featureOn  bool
		wantLabel  bool // true = label present with value LabelEnabled
	}{
		{
			name:      "feature off, empty labels",
			input:     map[string]string{},
			featureOn: false,
			wantLabel: false,
		},
		{
			name:      "feature on, empty labels (zero addons)",
			input:     map[string]string{},
			featureOn: true,
			wantLabel: true,
		},
		{
			name:      "feature on, one addon enabled",
			input:     map[string]string{"velero": LabelEnabled},
			featureOn: true,
			wantLabel: false,
		},
		{
			name:      "feature on, one addon disabled",
			input:     map[string]string{"velero": LabelDisabled},
			featureOn: true,
			wantLabel: true, // disabled ≠ enabled → count stays 0
		},
		{
			name:      "feature on, two addons — one enabled one disabled",
			input:     map[string]string{"velero": LabelEnabled, "datadog": LabelDisabled},
			featureOn: true,
			wantLabel: false, // at least one enabled → no check label
		},
		{
			name:      "feature on, non-addon label (region) present",
			input:     map[string]string{"region": "us-east-1"},
			featureOn: true,
			wantLabel: true, // "us-east-1" != "enabled" → count stays 0
		},
		{
			name:      "feature on, label already set, stays set when zero addons",
			input:     map[string]string{LabelConnectivityCheck: LabelEnabled},
			featureOn: true,
			wantLabel: true, // self-referential key excluded from count
		},
		{
			name:      "feature on, label set but now has an enabled addon — should remove",
			input:     map[string]string{LabelConnectivityCheck: LabelEnabled, "cert-manager": LabelEnabled},
			featureOn: true,
			wantLabel: false,
		},
		{
			name:      "feature off, label previously set — should be removed",
			input:     map[string]string{LabelConnectivityCheck: LabelEnabled},
			featureOn: false,
			wantLabel: false,
		},
		{
			name:      "nil map, feature on — no panic",
			input:     nil,
			featureOn: true,
			wantLabel: false, // nil-safe: no-op
		},
		{
			name:      "nil map, feature off — no panic",
			input:     nil,
			featureOn: false,
			wantLabel: false,
		},
		{
			name: "multiple addons all disabled",
			input: map[string]string{
				"velero":       LabelDisabled,
				"cert-manager": LabelDisabled,
				"datadog":      LabelDisabled,
			},
			featureOn: true,
			wantLabel: true,
		},
		{
			name: "multiple addons all enabled",
			input: map[string]string{
				"velero":       LabelEnabled,
				"cert-manager": LabelEnabled,
			},
			featureOn: true,
			wantLabel: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Deep-copy the input so table entries don't bleed into each other.
			var labels map[string]string
			if tc.input != nil {
				labels = make(map[string]string, len(tc.input))
				for k, v := range tc.input {
					labels[k] = v
				}
			}

			ApplyConnectivityCheckLabel(labels, tc.featureOn)

			got, present := "", false
			if labels != nil {
				got, present = labels[LabelConnectivityCheck]
			}

			if tc.wantLabel {
				if !present || got != LabelEnabled {
					t.Errorf("expected label %s=%s to be set, got present=%v value=%q",
						LabelConnectivityCheck, LabelEnabled, present, got)
				}
			} else {
				if present {
					t.Errorf("expected label %s to be absent, but found value=%q",
						LabelConnectivityCheck, got)
				}
			}
		})
	}
}
