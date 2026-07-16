package models

import (
	"testing"
)

func TestApplyConnectivityCheckLabel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		input        map[string]string
		featureOn    bool
		wantLabel    bool // true = canonical label present with value LabelEnabled
		wantLegacy   bool // W4b: true = legacy label ALSO present (V3 RW1.8)
	}{
		{
			name:       "feature off, empty labels",
			input:      map[string]string{},
			featureOn:  false,
			wantLabel:  false,
			wantLegacy: false,
		},
		{
			name:       "feature on, empty labels (zero addons)",
			input:      map[string]string{},
			featureOn:  true,
			wantLabel:  true,
			wantLegacy: true, // W4b: both keys stamped transitionally
		},
		{
			name:       "feature on, one addon enabled",
			input:      map[string]string{"velero": LabelEnabled},
			featureOn:  true,
			wantLabel:  false,
			wantLegacy: false, // both keys removed when addons present
		},
		{
			name:       "feature on, one addon disabled",
			input:      map[string]string{"velero": LabelDisabled},
			featureOn:  true,
			wantLabel:  true, // disabled ≠ enabled → count stays 0
			wantLegacy: true,
		},
		{
			name:       "feature on, two addons — one enabled one disabled",
			input:      map[string]string{"velero": LabelEnabled, "datadog": LabelDisabled},
			featureOn:  true,
			wantLabel:  false, // at least one enabled → no check label
			wantLegacy: false,
		},
		{
			name:       "feature on, non-addon label (region) present",
			input:      map[string]string{"region": "us-east-1"},
			featureOn:  true,
			wantLabel:  true, // "us-east-1" != "enabled" → count stays 0
			wantLegacy: true,
		},
		{
			name:       "feature on, label already set, stays set when zero addons",
			input:      map[string]string{LabelConnectivityCheck: LabelEnabled},
			featureOn:  true,
			wantLabel:  true, // self-referential key excluded from count
			wantLegacy: true,
		},
		{
			name:       "feature on, label set but now has an enabled addon — should remove",
			input:      map[string]string{LabelConnectivityCheck: LabelEnabled, "cert-manager": LabelEnabled},
			featureOn:  true,
			wantLabel:  false,
			wantLegacy: false,
		},
		{
			name:       "feature off, label previously set — should be removed",
			input:      map[string]string{LabelConnectivityCheck: LabelEnabled},
			featureOn:  false,
			wantLabel:  false,
			wantLegacy: false,
		},
		{
			name:       "nil map, feature on — no panic",
			input:      nil,
			featureOn:  true,
			wantLabel:  false, // nil-safe: no-op
			wantLegacy: false,
		},
		{
			name:       "nil map, feature off — no panic",
			input:      nil,
			featureOn:  false,
			wantLabel:  false,
			wantLegacy: false,
		},
		{
			name: "multiple addons all disabled",
			input: map[string]string{
				"velero":       LabelDisabled,
				"cert-manager": LabelDisabled,
				"datadog":      LabelDisabled,
			},
			featureOn:  true,
			wantLabel:  true,
			wantLegacy: true,
		},
		{
			name: "multiple addons all enabled",
			input: map[string]string{
				"velero":       LabelEnabled,
				"cert-manager": LabelEnabled,
			},
			featureOn:  true,
			wantLabel:  false,
			wantLegacy: false,
		},
		{
			name:       "W4b: legacy key already present, gets preserved alongside canonical",
			input:      map[string]string{LabelConnectivityCheckLegacy: LabelEnabled},
			featureOn:  true,
			wantLabel:  true,
			wantLegacy: true,
		},
		{
			name:       "W4b: both keys present, both stay when zero addons",
			input:      map[string]string{LabelConnectivityCheck: LabelEnabled, LabelConnectivityCheckLegacy: LabelEnabled},
			featureOn:  true,
			wantLabel:  true,
			wantLegacy: true,
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

			// Check canonical key.
			got, present := "", false
			if labels != nil {
				got, present = labels[LabelConnectivityCheck]
			}

			if tc.wantLabel {
				if !present || got != LabelEnabled {
					t.Errorf("expected canonical label %s=%s to be set, got present=%v value=%q",
						LabelConnectivityCheck, LabelEnabled, present, got)
				}
			} else {
				if present {
					t.Errorf("expected canonical label %s to be absent, but found value=%q",
						LabelConnectivityCheck, got)
				}
			}

			// W4b (V3 RW1.8): Check legacy key.
			gotLegacy, presentLegacy := "", false
			if labels != nil {
				gotLegacy, presentLegacy = labels[LabelConnectivityCheckLegacy]
			}

			if tc.wantLegacy {
				if !presentLegacy || gotLegacy != LabelEnabled {
					t.Errorf("expected legacy label %s=%s to be set (W4b transitional stamp), got present=%v value=%q",
						LabelConnectivityCheckLegacy, LabelEnabled, presentLegacy, gotLegacy)
				}
			} else {
				if presentLegacy {
					t.Errorf("expected legacy label %s to be absent, but found value=%q",
						LabelConnectivityCheckLegacy, gotLegacy)
				}
			}
		})
	}
}
