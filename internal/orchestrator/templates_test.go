package orchestrator

import (
	"bytes"
	"context"
	"strings"
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

// TestCollectBootstrapFiles_RootAppPath_MatchesConstant is the V124-20 /
// BUG-045 drift guard. It runs CollectBootstrapFiles against the real
// embedded templates.TemplateFS and asserts that the returned commit-path
// map contains exactly one entry at orchestrator.BootstrapRootAppPath whose
// content is the ArgoCD root Application (kind: Application + the canonical
// BootstrapRootAppName).
//
// The historic bug: pollPRMerge and the V124-15 already-init check both
// hardcoded "bootstrap/root-app.yaml" while CollectBootstrapFiles strips the
// "bootstrap/" prefix from repo-root files and commits root-app.yaml at the
// repo root. The github provider only logs on 200, so 404s were silent — the
// wizard hung forever on "Waiting for PR merge" while the file sat at the
// correct path the whole time.
//
// This test is the path-side complement of TestBootstrapRootAppName_MatchesTemplate
// (which guards the *name* the API polls in ArgoCD). Together they pin both
// values so future drift in either layer fails CI instead of silently
// breaking first-run init.
//
// Why a real-templateFS test instead of a unit-test on the strip-prefix
// helper: the bug surfaces at the layer boundary between the orchestrator's
// commit-path computation and the API's poll path. Asserting on the actual
// returned map proves the boundary is consistent, regardless of how the
// orchestrator chooses to derive the path internally.
func TestCollectBootstrapFiles_RootAppPath_MatchesConstant(t *testing.T) {
	// Minimal-but-valid orchestrator: only templateFS + GitOpsConfig.RepoURL
	// are required by CollectBootstrapFiles. Git/argocd/creds are nil — the
	// helper does not touch them.
	orch := New(
		nil,                                                    // gitMu — CollectBootstrapFiles does not lock
		nil,                                                    // credProvider — unused
		nil,                                                    // argocd — unused
		nil,                                                    // git — unused
		GitOpsConfig{BaseBranch: "main", RepoURL: "https://github.com/example/addons"},
		RepoPathsConfig{Bootstrap: "bootstrap"},
		templates.TemplateFS,
	)

	files, err := orch.CollectBootstrapFiles(context.Background())
	if err != nil {
		t.Fatalf("CollectBootstrapFiles: %v", err)
	}

	content, ok := files[BootstrapRootAppPath]
	if !ok {
		paths := make([]string, 0, len(files))
		for p := range files {
			paths = append(paths, p)
		}
		t.Fatalf(
			"CollectBootstrapFiles emitted no file at BootstrapRootAppPath = %q.\n"+
				"This is the V124-20 / BUG-045 drift guard. The orchestrator's strip-prefix\n"+
				"logic in CollectBootstrapFiles must produce a file at this exact path so the\n"+
				"API layer's pollPRMerge and already-init checks (internal/api/init.go) can\n"+
				"find it on the base branch. Either:\n"+
				"  (a) the strip-prefix logic in init.go's CollectBootstrapFiles changed —\n"+
				"      restore the bootstrap/ prefix-strip behavior or update the constant, OR\n"+
				"  (b) the template was renamed/moved — restore templates/bootstrap/root-app.yaml.\n"+
				"Got commit paths: %v",
			BootstrapRootAppPath, paths,
		)
	}

	// Content sanity: catch a regression that emits the file at the right
	// path but with corrupted/wrong content (e.g. a refactor that swaps the
	// root-app YAML for a different template).
	contentStr := string(content)
	if !strings.Contains(contentStr, "kind: Application") {
		t.Errorf(
			"file at BootstrapRootAppPath = %q does not contain `kind: Application`.\n"+
				"Content head: %.200s",
			BootstrapRootAppPath, contentStr,
		)
	}
	if !strings.Contains(contentStr, "name: "+BootstrapRootAppName) {
		t.Errorf(
			"file at BootstrapRootAppPath = %q does not contain `name: %s`.\n"+
				"Content head: %.200s",
			BootstrapRootAppPath, BootstrapRootAppName, contentStr,
		)
	}
}
