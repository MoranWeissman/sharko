package serverrender

// argocd_permissions_untouched_test.go — installing Sharko must not change
// ArgoCD's permission settings, and must not be able to claim it did.
//
// # What used to be here
//
// The chart shipped a post-install/post-upgrade hook Job that ran kubectl as
// Sharko's own ServiceAccount and rewrote ArgoCD's argocd-rbac-cm ConfigMap to
// give Sharko a role of its own. It was rendered for every install, because
// the value that switched it on is the one that creates Sharko's ordinary
// RBAC and is true by default. Three separate things were wrong with it and
// the product owner's ruling removed all three at once by removing the Job:
// one product's installer must not narrow another product's shared policy,
// nothing about it was written on any published page, and the script it ran
// could report success whether or not anything had happened.
//
// # What this file pins
//
// Two directions, asked two different ways, because either one alone has a
// blind spot:
//
//   - The RENDER guard asks helm what an operator's cluster would actually
//     receive, with default values and with the optional parts switched on.
//     It walks every string in every rendered object, so a mutation hidden in
//     a container argument or a ConfigMap payload is still found. Its blind
//     spot is values: a template that only renders under some combination
//     nobody thought to try is invisible to it.
//   - The SOURCE guard reads every file in every shipped chart, whatever
//     values would switch it on. Its blind spot is meaning: it is text
//     matching, and it cannot tell a real mutation from a mention.
//
// Neither is a count and neither has a floor. A chart root that contributes
// no files, a walk that reads nothing, and a render that produces nothing are
// each fatal, because all three would otherwise pass by finding nothing to
// object to.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// argocdPermissionMarkers are the things a chart that changes ArgoCD's
// permissions cannot avoid naming. argocd-rbac-cm is the ConfigMap itself,
// policy.csv is the key inside it that holds the rules, and role:sharko is the
// role the removed Job invented for Sharko.
var argocdPermissionMarkers = []string{
	"argocd-rbac-cm",
	"policy.csv",
	"role:sharko",
}

// installClaimMarkers are sentences a chart could use to tell an operator that
// it changed ArgoCD's permissions. The removed script ended with the first of
// them and printed it whether or not the patch had been accepted, so `helm
// install` reported a success that had not happened.
var installClaimMarkers = []string{
	"ArgoCD RBAC patched",
	"RBAC patched successfully",
	"Patching ArgoCD RBAC",
}

// -----------------------------------------------------------------------
// The render guard
// -----------------------------------------------------------------------

// renderServerChartRaw runs `helm template` over charts/sharko and returns
// every rendered document decoded as plain nested data, so every string in
// the render can be walked — a container argument and a ConfigMap value are
// as visible as a metadata name.
func renderServerChartRaw(t *testing.T, label string, extraArgs ...string) []interface{} {
	t.Helper()
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Fatalf("helm is not on PATH, so the shipped chart cannot be rendered: %v", err)
	}
	root := repoRoot(t)
	args := append([]string{"template", "sharko", filepath.Join(root, "charts", "sharko")}, extraArgs...)
	cmd := exec.Command(helm, args...)
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("helm template (%s) failed: %v\n%s", label, err, stderr.String())
	}

	var docs []interface{}
	decoder := yaml.NewDecoder(bytes.NewReader(out))
	for {
		var doc interface{}
		if decodeErr := decoder.Decode(&doc); decodeErr != nil {
			break
		}
		if doc == nil {
			continue
		}
		docs = append(docs, doc)
	}
	if len(docs) == 0 {
		t.Fatalf("charts/sharko rendered nothing at all for %s, so this guard would pass by "+
			"having no objects to object to:\n%s", label, out)
	}
	return docs
}

// walkStrings calls visit for every string anywhere in a decoded document,
// keys included: a ConfigMap whose KEY is policy.csv is exactly the shape
// being guarded against.
func walkStrings(node interface{}, visit func(string)) {
	switch v := node.(type) {
	case string:
		visit(v)
	case []interface{}:
		for _, item := range v {
			walkStrings(item, visit)
		}
	case map[string]interface{}:
		for key, item := range v {
			visit(key)
			walkStrings(item, visit)
		}
	case map[interface{}]interface{}:
		for key, item := range v {
			if ks, ok := key.(string); ok {
				visit(ks)
			}
			walkStrings(item, visit)
		}
	}
}

// docKind reads a document's kind, or "" when it has none.
func docKind(doc interface{}) string {
	m, ok := doc.(map[string]interface{})
	if !ok {
		return ""
	}
	kind, _ := m["kind"].(string)
	return kind
}

// docName reads a document's metadata.name, for error messages.
func docName(doc interface{}) string {
	m, ok := doc.(map[string]interface{})
	if !ok {
		return "<unnamed>"
	}
	meta, ok := m["metadata"].(map[string]interface{})
	if !ok {
		return "<unnamed>"
	}
	name, _ := meta["name"].(string)
	if name == "" {
		return "<unnamed>"
	}
	return name
}

// docAnnotations reads a document's metadata.annotations.
func docAnnotations(doc interface{}) map[string]interface{} {
	m, ok := doc.(map[string]interface{})
	if !ok {
		return nil
	}
	meta, ok := m["metadata"].(map[string]interface{})
	if !ok {
		return nil
	}
	ann, _ := meta["annotations"].(map[string]interface{})
	return ann
}

// renderCases are the value combinations an operator can reach. Defaults are
// what almost everybody installs; the AI case switches on the optional parts,
// which is where the second helper container lives.
var renderCases = []struct {
	label string
	args  []string
}{
	{label: "default values", args: nil},
	{label: "rbac.create=true stated explicitly", args: []string{"--set", "rbac.create=true"}},
	{label: "AI with the bundled Ollama pod on", args: []string{
		"--set", "ai.enabled=true", "--set", "ai.provider=ollama", "--set", "ai.ollama.deploy=true",
	}},
}

// TestServerChartRender_ChangesNoArgoCDPermissions is the direction that
// catches the Job coming back, under any name.
func TestServerChartRender_ChangesNoArgoCDPermissions(t *testing.T) {
	for _, tc := range renderCases {
		docs := renderServerChartRaw(t, tc.label, tc.args...)

		// Vacuity: a render that somehow contained no Deployment would mean
		// the guard is reading something other than this chart.
		sawDeployment := false
		for _, doc := range docs {
			if docKind(doc) == "Deployment" {
				sawDeployment = true
			}
		}
		if !sawDeployment {
			t.Fatalf("the %s render has no Deployment in it, so it is not the Sharko chart and every "+
				"check below would pass on the wrong objects", tc.label)
		}

		for _, doc := range docs {
			kind, name := docKind(doc), docName(doc)

			// A Job is how a chart runs something. The chart has no other
			// reason to have one, so its return is worth failing on by
			// itself rather than waiting for the marker check below.
			if kind == "Job" {
				t.Errorf("the %s render contains a Job (%q). Installing Sharko runs no jobs of its own: "+
					"the one that used to be here rewrote ArgoCD's permission settings. If a Job is "+
					"genuinely needed now, say on docs/site/technical-preview.md and "+
					"docs/site/operator/security.md what it does before adding it back.", tc.label, name)
			}
			for annotation := range docAnnotations(doc) {
				if strings.HasPrefix(annotation, "helm.sh/hook") {
					t.Errorf("%s %q in the %s render carries the Helm hook annotation %q. A hook runs "+
						"during install with the operator watching helm rather than the object, which is "+
						"exactly how the removed ArgoCD RBAC patch went unnoticed.", kind, name, tc.label, annotation)
				}
			}

			walkStrings(doc, func(s string) {
				for _, marker := range argocdPermissionMarkers {
					if strings.Contains(s, marker) {
						t.Errorf("%s %q in the %s render mentions %q. Installing Sharko must not touch "+
							"ArgoCD's permission settings — an ArgoCD administrator grants Sharko what "+
							"they choose to grant it, by hand, and both published pages say so.",
							kind, name, tc.label, marker)
					}
				}
				for _, claim := range installClaimMarkers {
					if strings.Contains(s, claim) {
						t.Errorf("%s %q in the %s render can print %q. Nothing in an install may claim "+
							"ArgoCD's permissions were changed, least of all something that would print "+
							"it whether or not anything happened.", kind, name, tc.label, claim)
					}
				}
			})
		}
	}
}

// -----------------------------------------------------------------------
// The source guard
// -----------------------------------------------------------------------

// chartRoots is derived by listing charts/, not written out by hand, so a new
// chart is covered the day it lands.
func chartRoots(t *testing.T) []string {
	t.Helper()
	root := repoRoot(t)
	entries, err := os.ReadDir(filepath.Join(root, "charts"))
	if err != nil {
		t.Fatalf("reading charts/: %v — this guard cannot check anything", err)
	}
	var roots []string
	for _, e := range entries {
		if e.IsDir() {
			roots = append(roots, filepath.Join(root, "charts", e.Name()))
		}
	}
	if len(roots) == 0 {
		t.Fatal("charts/ holds no charts at all — this guard would read nothing and pass")
	}
	return roots
}

// TestServerChartSources_ChangeNoArgoCDPermissions reads every file in every
// shipped chart, whatever values would render it.
func TestServerChartSources_ChangeNoArgoCDPermissions(t *testing.T) {
	roots := chartRoots(t)

	total := 0
	sawClusterRole := false
	for _, chartRoot := range roots {
		perRoot := 0
		err := filepath.Walk(chartRoot, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if info.IsDir() {
				return nil
			}
			body, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			perRoot++
			rel, _ := filepath.Rel(repoRoot(t), path)

			for lineNo, raw := range strings.Split(string(body), "\n") {
				line := strings.TrimSpace(raw)
				// A comment explaining what an operator may do by hand is
				// not the chart doing it. Only what the chart itself would
				// emit is in question here.
				if strings.HasPrefix(line, "#") {
					continue
				}
				if strings.Contains(line, "kind: ClusterRole") {
					sawClusterRole = true
				}
				for _, marker := range append(append([]string{}, argocdPermissionMarkers...), installClaimMarkers...) {
					if strings.Contains(line, marker) {
						t.Errorf("%s:%d mentions %q outside a comment.\nInstalling Sharko must not change "+
							"ArgoCD's permission settings and must not say it did. If this is genuinely "+
							"something else, rename it — this guard is the only thing standing between the "+
							"removed patch Job and a quiet return.\nline: %s", rel, lineNo+1, marker, line)
					}
				}
				if strings.Contains(line, "kind: Job") {
					t.Errorf("%s:%d declares a Job. The chart installs a server, not a task runner; the "+
						"one Job it used to have rewrote another product's permissions.", rel, lineNo+1)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", chartRoot, err)
		}
		if perRoot == 0 {
			t.Fatalf("%s contributed no files to this guard — a chart root that reads as empty makes "+
				"every check in it pass", chartRoot)
		}
		total += perRoot
	}

	if total == 0 {
		t.Fatal("no chart files were read at all — this guard checked nothing")
	}
	// The proof that the walk really sees the chart's RBAC, not just its
	// README: charts/sharko/templates/rbac.yaml declares a ClusterRole, and a
	// walk that misses it is reading the wrong tree.
	if !sawClusterRole {
		t.Fatal("the walk read chart files but never saw a ClusterRole declaration. The chart does declare " +
			"one, so the walk is not reading what it thinks it is, and every check above passed on nothing.")
	}
}

// -----------------------------------------------------------------------
// Image pinning
// -----------------------------------------------------------------------

// imageRef is one container image the repository would pull.
type imageRef struct {
	where string // repo-relative file
	ref   string // the reference as written, e.g. "alpine:3.21"
}

var (
	imageLine   = regexp.MustCompile(`^\s*-?\s*image:\s*(.+?)\s*$`)
	fromLine    = regexp.MustCompile(`^\s*FROM\s+(?:--platform=\S+\s+)?(\S+)`)
	quotedInner = regexp.MustCompile(`"([^"\s]+:[^"\s{}]+)"`)
	templateGo  = regexp.MustCompile(`\{\{.*?\}\}`)
)

// imageRefsInLine pulls every image reference out of one line.
//
// A chart line is often a Go template. Two shapes matter: a reference written
// out in full, and a reference that is a template with a literal fallback
// inside it — `{{ .Values.x | default "ollama/ollama:0.6.8" }}`. The fallback
// is what an operator who sets nothing gets, so it counts as a reference. A
// line that is nothing but a template with no literal in it resolves through
// the chart's own values, and is recorded as "{{templated}}" so it still
// appears in the pinned list below.
func imageRefsInLine(line string) []string {
	var raw string
	if m := imageLine.FindStringSubmatch(line); m != nil {
		raw = m[1]
	} else if m := fromLine.FindStringSubmatch(line); m != nil {
		raw = m[1]
	} else {
		return nil
	}
	// Drop a trailing comment, but only outside a quoted value.
	if !strings.HasPrefix(raw, `"`) {
		if idx := strings.Index(raw, " #"); idx >= 0 {
			raw = strings.TrimSpace(raw[:idx])
		}
	} else if end := strings.LastIndex(raw, `"`); end > 0 {
		raw = raw[:end+1]
	}
	if raw == "" {
		return nil // `image:` opening a mapping, not naming an image
	}

	var out []string
	for _, m := range quotedInner.FindAllStringSubmatch(raw, -1) {
		out = append(out, m[1])
	}
	if len(out) > 0 {
		return out
	}
	unquoted := strings.Trim(raw, `"'`)
	if templateGo.MatchString(unquoted) {
		return []string{"{{templated}}"}
	}
	if unquoted == "" {
		return nil
	}
	return []string{unquoted}
}

// collectImageRefs walks the shipped charts and every Dockerfile in the
// repository and returns every image reference, sorted.
func collectImageRefs(t *testing.T) []imageRef {
	t.Helper()
	root := repoRoot(t)

	var found []imageRef
	scan := func(path string) error {
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		for _, raw := range strings.Split(string(body), "\n") {
			if strings.HasPrefix(strings.TrimSpace(raw), "#") {
				continue
			}
			for _, ref := range imageRefsInLine(raw) {
				found = append(found, imageRef{where: filepath.ToSlash(rel), ref: ref})
			}
		}
		return nil
	}

	chartFiles, dockerFiles := 0, 0
	for _, chartRoot := range chartRoots(t) {
		before := len(found)
		err := filepath.Walk(chartRoot, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil || info.IsDir() {
				return walkErr
			}
			chartFiles++
			return scan(path)
		})
		if err != nil {
			t.Fatalf("walking %s: %v", chartRoot, err)
		}
		_ = before
	}

	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return nil // an unreadable corner of the tree is not this guard's business
		}
		if info.IsDir() {
			name := info.Name()
			if name == ".git" || name == "node_modules" || name == "_dist" || name == "site" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasPrefix(info.Name(), "Dockerfile") {
			return nil
		}
		dockerFiles++
		return scan(path)
	})
	if err != nil {
		t.Fatalf("walking the repository for Dockerfiles: %v", err)
	}

	if chartFiles == 0 {
		t.Fatal("no chart files were read — this guard would find no images and pass")
	}
	if dockerFiles == 0 {
		t.Fatal("no Dockerfile was found anywhere in the repository. There are some, so the walk is broken, " +
			"and a broken walk here reads as \"every image is pinned\".")
	}

	sort.Slice(found, func(i, j int) bool {
		if found[i].where != found[j].where {
			return found[i].where < found[j].where
		}
		return found[i].ref < found[j].ref
	})
	return found
}

// imagesAsShipped is the exact list of image references the charts and the
// Dockerfiles carry today, one line each, compared with != and nothing else.
//
// It is a list rather than a rule for the same reason the RBAC pin next door
// is: a rule only catches the mistake somebody thought of. A new helper image
// appearing anywhere fails here and has to be looked at, whatever its tag.
var imagesAsShipped = []string{
	`Dockerfile -> alpine:3.21`,
	`Dockerfile -> golang:1.26.6-alpine`,
	`Dockerfile -> node:22-alpine`,
	`charts/sharko/templates/deployment.yaml -> {{templated}}`,
	`charts/sharko/templates/ollama.yaml -> ollama/ollama:0.6.8`,
	`charts/sharko/templates/ollama.yaml -> ollama/ollama:0.6.8`,
	`charts/sharko/values.yaml -> ollama/ollama:0.6.8`,
	`tests/e2e/harness/gitfake/Dockerfile -> gcr.io/distroless/static-debian12:nonroot`,
	`tests/e2e/harness/gitfake/Dockerfile -> golang:1.26.6-alpine`,
}

// TestImages_ArePinnedAndAreExactlyTheOnesWrittenDown fails on a floating tag
// and on a new image nobody wrote down.
func TestImages_ArePinnedAndAreExactlyTheOnesWrittenDown(t *testing.T) {
	found := collectImageRefs(t)

	var got []string
	for _, ref := range found {
		got = append(got, ref.where+" -> "+ref.ref)
	}
	if strings.Join(got, "\n") != strings.Join(imagesAsShipped, "\n") {
		t.Errorf("the images this repository pulls are no longer the ones written down here.\n\nnow:\n  %s\n\n"+
			"written down here:\n  %s\n\nEvery entry has to be an immutable reference. Add the new one to "+
			"imagesAsShipped once you have checked its tag.",
			strings.Join(got, "\n  "), strings.Join(imagesAsShipped, "\n  "))
	}

	// And independently of the list: no floating tag, ever. This is the check
	// that keeps meaning something if somebody updates the list without
	// reading it.
	for _, ref := range found {
		if ref.ref == "{{templated}}" {
			continue // resolved through chart values, which are in the list above
		}
		tag := ""
		if idx := strings.LastIndex(ref.ref, ":"); idx >= 0 && !strings.Contains(ref.ref[idx:], "/") {
			tag = ref.ref[idx+1:]
		}
		switch tag {
		case "":
			t.Errorf("%s pulls %q with no tag, which means :latest. Two installs a month apart would run "+
				"different software with nothing recording the change.", ref.where, ref.ref)
		case "latest":
			t.Errorf("%s pulls %q. A floating tag means an install is not reproducible and nobody can say "+
				"afterwards what ran.", ref.where, ref.ref)
		}
	}
}
