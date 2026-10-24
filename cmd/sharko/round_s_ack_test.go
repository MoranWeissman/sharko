package main

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

// EP3 dry run round S, S25: drop-legacy-labels takes "a,b" in
// --acknowledge, the same as take over.
func TestDropLegacyLabels_AcknowledgeTakesCommaSeparatedIDs(t *testing.T) {
	var sent []string
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Acks []string `json:"acknowledged_findings"`
		}
		_ = json.Unmarshal(raw, &body)
		sent = body.Acks
		jsonReply(w, 200, map[string]interface{}{"cluster": "prod-eu", "status": "success"})
	})
	resetFlags(dropLegacyLabelsCmd)
	setFlags(t, dropLegacyLabelsCmd, map[string]string{"yes": "true"})
	setRepeatedFlag(t, dropLegacyLabelsCmd, "acknowledge", "label-a,label-b", "label-a")
	if _, err := captureStdoutT(t, func() error { return dropLegacyLabelsCmd.RunE(dropLegacyLabelsCmd, []string{"prod-eu"}) }); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 2 || sent[0] != "label-a" || sent[1] != "label-b" {
		t.Fatalf("acknowledged_findings sent = %v, want [label-a label-b]", sent)
	}
}
