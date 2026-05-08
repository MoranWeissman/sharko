package orchestrator

import (
	"bytes"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/MoranWeissman/sharko/templates"
)

// TestBootstrapRootAppName_MatchesTemplate is the V124-14 / BUG-031 drift
// guard. It reads the *embedded* templates/bootstrap/root-app.yaml from the
// real templates package, walks the multi-doc YAML, and asserts that the
// `kind: Application` document's metadata.name equals BootstrapRootAppName.
//
// The historic bug: init.go hardcoded "addons-bootstrap" while the template
// declared "cluster-addons-bootstrap". Sharko polled a non-existent app for
// 2 minutes, timed out, and (BUG-032) reported success anyway. This test
// catches future drift in either direction — change the constant or change
// the template, and one will diverge from the other and fail this test.
func TestBootstrapRootAppName_MatchesTemplate(t *testing.T) {
	raw, err := templates.TemplateFS.ReadFile("bootstrap/root-app.yaml")
	if err != nil {
		t.Fatalf("reading bootstrap/root-app.yaml from embedded FS: %v", err)
	}

	// root-app.yaml is multi-document YAML (AppProject + Application).
	// Split on the document separator the same way bootstrapArgoCD does
	// at runtime. Note: split uses "\n---" so it tolerates a leading
	// "---\n" at the top of the file.
	docs := bytes.Split(raw, []byte("\n---"))
	if len(docs) < 2 {
		t.Fatalf("expected at least 2 YAML documents in root-app.yaml, got %d", len(docs))
	}

	type appMetaDoc struct {
		Kind     string `yaml:"kind"`
		Metadata struct {
			Name string `yaml:"name"`
		} `yaml:"metadata"`
	}

	var found bool
	for i, doc := range docs {
		doc = bytes.TrimSpace(doc)
		if len(doc) == 0 {
			continue
		}
		var parsed appMetaDoc
		if err := yaml.Unmarshal(doc, &parsed); err != nil {
			t.Fatalf("parsing YAML doc %d: %v", i, err)
		}
		if parsed.Kind != "Application" {
			continue
		}
		found = true
		if parsed.Metadata.Name != BootstrapRootAppName {
			t.Errorf(
				"templates/bootstrap/root-app.yaml metadata.name = %q, want %q (BootstrapRootAppName).\n"+
					"This is the V124-14 / BUG-031 drift guard. Either:\n"+
					"  (a) the template was renamed — update orchestrator.BootstrapRootAppName, OR\n"+
					"  (b) the constant was changed — revert or update the template's metadata.name.\n"+
					"Both must agree, or first-run init will silently fail step 4.",
				parsed.Metadata.Name, BootstrapRootAppName,
			)
		}
	}
	if !found {
		t.Fatalf("no `kind: Application` document found in templates/bootstrap/root-app.yaml")
	}
}
