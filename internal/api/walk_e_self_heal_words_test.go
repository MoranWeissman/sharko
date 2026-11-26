package api

import (
	"os"
	"strings"
	"testing"
)

// Walk 2, finding 3: the setting and the docs said "off: Sharko only warns",
// but on a repo in the v4 layout Sharko put a hand-changed add-on label back
// within seconds, and the add-on came back. The code means: add-on labels on a
// v4 repo ALWAYS converge, because Git is the record and the label is what
// installs the add-on. The setting only decides whether anything else is put
// back on an older layout. The words now say that.
func TestSelfHealSetting_WordsSayWhatSharkoReallyDoes(t *testing.T) {
	raw, err := os.ReadFile("../../docs/swagger/swagger.json")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)

	mustHave := []string{
		"On a repo in the v4 layout this setting does not matter for add-on labels: Sharko always puts them back on its own, because Git is the record of what runs where.",
		"Taking an add-on label off by hand uninstalls the add-on until Sharko puts the label back.",
		"It never turns off the always-on put-back of add-on labels on a repo in the v4 layout",
	}
	for _, want := range mustHave {
		if !strings.Contains(doc, want) {
			t.Errorf("the generated API description is missing:\n%s", want)
		}
	}
	mustNot := []string{
		"Returns whether Sharko re-applies the labels Git declares to drifted Sharko-managed clusters (V3 G3, default false — drift is recorded only)",
		"Sets whether Sharko re-applies the labels Git declares to drifted Sharko-managed clusters (V3 G3). Admin only.",
	}
	for _, old := range mustNot {
		if strings.Contains(doc, old) {
			t.Errorf("the old description is back:\n%s", old)
		}
	}
}
