package serverrender

// webhook_closed_by_default_test.go — a default `helm install` must not be
// able to open the Git webhook by accident.
//
// The endpoint is the one route past the session/token check, and the only
// thing in front of it is a signature computed with SHARKO_WEBHOOK_SECRET.
// The handler refuses everything while that value is unset, so what this file
// checks is the other half: that the shipped defaults really do leave it
// unset, and that a value an operator types really does arrive.
//
// This is checked against the RENDER, not against values.yaml, because a
// template can invent a value the values file never mentions — a default
// stuck in a `default` pipeline, a `randAlphaNum`, a copy of another key.
// Reading the rendered objects is the only way to see what an install
// actually gets.

import (
	"strings"
	"testing"
)

const webhookSecretSetting = "SHARKO_WEBHOOK_SECRET"

// TestDefaultInstallLeavesTheWebhookClosed renders the chart with no values
// at all — a plain `helm install` — and fails if anything in the render hands
// the container a webhook secret.
func TestDefaultInstallLeavesTheWebhookClosed(t *testing.T) {
	objects := renderServerChart(t)
	if len(objects) == 0 {
		t.Fatal("the default render produced no objects at all — the scan is broken, not the chart")
	}

	var sawASecretObject bool
	for _, obj := range objects {
		if obj.Kind == "Secret" {
			sawASecretObject = true
		}
		for _, where := range []map[string]string{obj.Data, obj.StringData} {
			for key, value := range where {
				if key != webhookSecretSetting {
					continue
				}
				t.Errorf("a default install sets %s on the %s %q (value length %d). "+
					"An install nobody configured must leave the Git webhook closed: the handler "+
					"refuses every call while this is unset, and a value appearing here by itself "+
					"is exactly how the endpoint used to end up answering strangers.",
					webhookSecretSetting, obj.Kind, obj.Metadata.Name, len(value))
			}
		}
	}

	// Refuse to pass vacuously. The chart always renders a Secret; if this
	// render has none, the parse lost it and the loop above checked nothing.
	if !sawASecretObject {
		t.Fatal("the default render contained no Secret object. The chart always makes one, so " +
			"the render or the parse is broken — and a broken scan reports ok forever.")
	}
}

// TestAnOperatorSuppliedWebhookSecretActuallyArrives is the other direction.
//
// A guard that only checks "the name is absent" gets happier the more broken
// the chart is — it would pass on a chart that dropped the setting entirely
// and left an operator unable to switch the webhook on at all.
func TestAnOperatorSuppliedWebhookSecretActuallyArrives(t *testing.T) {
	const supplied = "an-operator-typed-this"
	objects := renderServerChart(t, "--set", "secrets.webhookSecret="+supplied)
	if len(objects) == 0 {
		t.Fatal("the render produced no objects at all — the scan is broken, not the chart")
	}

	var found []string
	for _, obj := range objects {
		for _, where := range []map[string]string{obj.Data, obj.StringData} {
			if value, ok := where[webhookSecretSetting]; ok {
				found = append(found, obj.Kind+"/"+obj.Metadata.Name)
				if !strings.Contains(value, supplied) {
					t.Errorf("%s on %s/%s does not carry what the operator set",
						webhookSecretSetting, obj.Kind, obj.Metadata.Name)
				}
			}
		}
	}

	if len(found) != 1 {
		t.Fatalf("%s reached the container from %d place(s) (%v); it must come from exactly one, "+
			"or an operator has no single place to set and rotate it",
			webhookSecretSetting, len(found), found)
	}
}
